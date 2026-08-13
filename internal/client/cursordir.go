// cursordir.go —— 协调者侧游标目录的解析、降级与命名空间折算。
//
// 职责：
//   - 解析游标根：~/.handoff → <cwd>/.handoff 两级确定性降级，都不可写则报错
//   - 把 agentd 地址折算成可作路径段的命名空间名
//
// 边界：
//   - 不读写游标内容（那是 client.go 的 readCursor/writeCursor）
//   - 不做回收（那是 cursorgc.go）
//   - 不认识 --target：命名空间按 agentd 地址而非本机别名，见 cursorNamespace 的 why
package client

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// cursorDirName 是游标在游标根下的子目录名。
//
// 为什么必须有这一层而不是平铺：平铺时游标与 config.yaml、agentd 的 DataDir
// 混在同一层，没有任何一个目录可以被整体处置——既没法「清掉某台机器的全部
// 游标」，也没法把游标单独重定向走。
const cursorDirName = "cursors"

// cursorNamespace 把 agentd 的 baseURL 折算成一个可作路径段的名字。
//
// 参数：baseURL 为 Client 持有的 agentd 地址，可带或不带 scheme
//
// 返回：只含字母数字与 . - 的路径段（如 "100.73.238.21_7777"）；无法解析时返回 "unknown"
//
// 为什么按地址而不是 --target 名字：地址是 agentd 的身份，名字只是本机别名。
// 两个 target 名指向同一台 agentd 时按名字分篓会把同一批任务的游标分裂成两份；
// 改个名字则让已有游标全部失联。这与 resolveProject 里「projectID 是身份、
// 名字只是引用」是同一个判断。
func cursorNamespace(baseURL string) string {
	host := baseURL
	// 不带 scheme 时 url.Parse 会把整串当 Path、Host 为空，此时退回原串按同样
	// 规则折算——两种写法必须折到同一个篓，否则 handoff --agentd 127.0.0.1:7777
	// 与 http://127.0.0.1:7777 会各持一份游标
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// probeCursorDirWritable 通过「真写一次」确认目录可写。
//
// 为什么不查权限位：沙箱（codex 的 seatbelt/landlock）的拒绝不体现在 mode 上，
// 目录 mode 是 0700 而写入照样 EPERM。唯一可靠的判据是真的建一个文件。
func probeCursorDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("试写: %w", err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name) // 探针文件用完即删，失败无所谓：下一次 TTL 清扫会带走
	return nil
}

// cursorRootDir 返回已确认可写的游标根目录（形如 <root>/cursors）。
//
// 返回：
//   - 目录绝对路径；两级候选都不可写时返回错误，错误里点名两个路径与各自原因
//
// 注意：
//   - 全 Client 生命周期只解析一次，结果（含错误）被缓存
//   - 降级发生时打一条 Warn（stderr），只打一次
func (c *Client) cursorRootDir() (string, error) {
	c.cursorRootOnce.Do(func() {
		c.cursorRoot, c.cursorRootErr = c.resolveCursorRoot()
		if c.cursorRootErr == nil {
			// 旧平铺布局的一次性清除挂在这里：它必须只跑一次，而 once 已经
			// 提供了这个保证；单独找一个「启动时」的挂载点反而要在每个命令里
			// 各接一次，漏一个就永远不清
			c.purgeLegacyFlatCursors()
			c.sweepCursors()
		}
	})
	return c.cursorRoot, c.cursorRootErr
}

// resolveCursorRoot 执行两级确定性降级。
//
// 顺序硬约束：先 ~/.handoff（缺省，与历史行为一致），不可写才退 <cwd>/.handoff。
// 为什么降级目标是 cwd 而不是 $TMPDIR：codex 的 workspace-write 可写 cwd、
// $TMPDIR、/tmp 三处，但只有 cwd 是协调者的项目目录、跨 session 稳定；
// $TMPDIR 会被清理，游标续不上等于没修。
func (c *Client) resolveCursorRoot() (string, error) {
	var homeReason string
	home, err := os.UserHomeDir()
	if err != nil {
		homeReason = fmt.Sprintf("读取用户主目录失败: %v", err)
	} else {
		cand := filepath.Join(home, ".handoff", cursorDirName)
		if perr := probeCursorDirWritable(cand); perr == nil {
			c.log().Debug("游标根就位", "dir", cand)
			return cand, nil
		} else {
			homeReason = fmt.Sprintf("%s 不可写: %v", filepath.Dir(cand), perr)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("游标目录不可用：%s；且读取当前目录失败: %w", homeReason, err)
	}
	cand := filepath.Join(cwd, ".handoff", cursorDirName)
	if perr := probeCursorDirWritable(cand); perr != nil {
		return "", fmt.Errorf("游标目录不可用：%s；%s 也不可写: %v",
			homeReason, filepath.Dir(cand), perr)
	}
	// 降级是协调者必须知道的事实（游标换了地方，跨目录 wait 会各持一份），
	// 因此是 Warn 不是 Debug；只打一次由 cursorRootOnce 保证
	c.log().Warn("游标目录不可写，已降级", "原因", homeReason, "改用", cand)
	return cand, nil
}
