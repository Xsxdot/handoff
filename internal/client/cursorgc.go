// cursorgc.go —— 协调者侧游标的回收。
//
// 职责：
//   - 任务归档时删掉它的游标（DropCursor）
//   - 按 TTL 清扫超期游标与遗留的写入临时文件（sweepCursors）
//   - 一次性清除旧平铺布局遗留的 cursor-* 文件（purgeLegacyFlatCursors）
//
// 边界：
//   - 不判断任务是否真的终结：那是调用方（观察到 archived 事件 / done 成功）的事
//   - 不解析游标根：复用 cursordir.go 的 cursorRootDir
//   - 全部回收动作都是尽力而为，失败只记 Debug，绝不影响游标读写的成败
package client

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cursorTTL 是游标文件被判定为「无人认领」的年龄阈值。
//
// 为什么需要它而不是只靠 DropCursor：DropCursor 只覆盖「协调者跑完 done」这条
// 主路径。任务被 stop、协调者换了机器、wait 进程被 Ctrl+C——这些情形下没有任何
// 代码会再碰那个文件。实测协调者本机曾堆积 98 个从无回收的游标。
//
// 为什么是 30 天而不是更短：游标的作用是跨 wait 调用记住看到哪儿了，而一个
// 任务从派发到归档可能横跨数日。30 天足够长到不会误删在办任务，也足够短到
// 目录不会无界增长。
const cursorTTL = 30 * 24 * time.Hour

// cursorTempTTL 是 cursor 临时文件被判定为「遗留垃圾」的年龄阈值。
//
// 为什么按年龄而不是一律清空：同一任务可能有并发的 wait 进程正在写各自的
// 临时文件，无差别删除会掐掉别人在途的 Rename。而任何一次正常写入都在毫秒级
// 完成，1 小时的阈值把「在途」与「遗留」分得足够开。
const cursorTempTTL = time.Hour

// DropCursor 删除某任务的游标，幂等。
//
// 参数：taskID 为已终结（归档）的任务 ID
//
// 注意：
//   - 文件不存在不是错误：本函数有两条调用通道（观察到 archived 事件、done 成功
//     返回），两条都可能先到，必须能重复调用
//   - 任何失败只记 Debug：回收是卫生工作，失败不影响任何正确性
func (c *Client) DropCursor(taskID string) {
	p, err := c.cursorPath(taskID)
	if err != nil {
		c.log().Debug("回收游标时路径不可用", "task", taskID, "cause", err)
		return
	}
	if err := os.Remove(p); err != nil {
		if !os.IsNotExist(err) {
			c.log().Debug("回收游标失败", "task", taskID, "path", p, "cause", err)
		}
		return
	}
	c.log().Debug("任务已归档，游标已回收", "task", taskID, "path", p)
}

// sweepCursors 清扫本 agentd 命名空间下超期的游标与遗留临时文件。
//
// 注意：只扫自己这一篓，不碰别的 agentd 的目录——判断别人的文件是否超期需要
// 别人的上下文，本客户端没有。
func (c *Client) sweepCursors() {
	// 不调 c.cursorRootDir()：本函数唯一的挂载点就在 cursorRootDir 的 once 内
	// （见 cursordir.go），再走一次 Do 是对未完成的 once 重入——sync.Once 会
	// 直接死锁。挂载点保证执行到这里时游标根已解析完，直接读缓存即可
	if c.cursorRoot == "" {
		return
	}
	dir := filepath.Join(c.cursorRoot, cursorNamespace(c.baseURL))
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.log().Debug("扫描游标目录失败", "dir", dir, "cause", err)
		return
	}
	var removed int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		ttl := cursorTTL
		if strings.HasSuffix(e.Name(), ".tmp") {
			ttl = cursorTempTTL
		}
		if time.Since(fi.ModTime()) < ttl {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if rerr := os.Remove(p); rerr != nil {
			c.log().Debug("清理超期游标失败", "path", p, "cause", rerr)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.log().Debug("已清理超期游标", "dir", dir, "n", removed)
	}
}

// purgeLegacyFlatCursors 一次性清除旧平铺布局遗留的 cursor-* 文件。
//
// 为什么删而不迁移：旧文件里绝大多数是已归档任务的游标，本来就该删；保住它们
// 唯一的收益是极少数仍在 waiting_review 的老任务下次 wait 少重放一次历史事件，
// 不值得为此写一段只跑一次的迁移代码及其测试。
//
// 只删严格匹配 cursor-* 的文件，config.yaml / agentd.log / skill 等一律不碰。
func (c *Client) purgeLegacyFlatCursors() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	matches, err := filepath.Glob(filepath.Join(home, ".handoff", "cursor-*"))
	if err != nil {
		c.log().Debug("扫描旧平铺游标失败", "cause", err)
		return
	}
	var removed int
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue // 目录不碰：cursors/ 这一层就是目录，绝不能被当成旧文件删掉
		}
		if rerr := os.Remove(m); rerr != nil {
			c.log().Debug("清除旧平铺游标失败", "path", m, "cause", rerr)
			continue
		}
		removed++
	}
	if removed > 0 {
		c.log().Info("已清除旧布局遗留的游标文件", "n", removed)
	}
}

// sweepStaleCursorTemps 清理该任务遗留的 cursor 临时文件。
//
// 为什么需要它：writeCursor 用 CreateTemp + Rename 保证原子写，进程若在两步
// 之间被杀（Ctrl+C、机器重启、oom kill）就会留下一个 .tmp，而此后没有任何
// 代码会再碰它——游标目录里的 .tmp 只增不减。
//
// 清理失败一律只记 Debug：这是顺带的卫生工作，绝不能影响 cursor 写入的成败。
func (c *Client) sweepStaleCursorTemps(dir, taskID string) {
	matches, err := filepath.Glob(filepath.Join(dir, taskID+"-*.tmp"))
	if err != nil {
		c.log().Debug("扫描遗留 cursor 临时文件失败", "task", taskID, "cause", err)
		return
	}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || time.Since(fi.ModTime()) < cursorTempTTL {
			continue // 取不到状态或还在途：交给下一次写入再看
		}
		if rerr := os.Remove(m); rerr != nil {
			c.log().Debug("清理遗留 cursor 临时文件失败", "path", m, "cause", rerr)
			continue
		}
		c.log().Debug("已清理遗留 cursor 临时文件", "task", taskID, "path", m)
	}
}
