// 本文件是工作台状态两表（workbench_bases / workbench_singletons）的持久化实现。
//
// 职责：
//   - 基准目录行的写（PutWorkbenchBase，含 50 行上限的就地淘汰）、列、删
//   - 单例（当前选中目录、悬浮窗现场）的写、列、删
//
// 边界：
//   - **不解释 payload / value**：它们是前端序列化好的 JSON 字符串，本层原样搬运。
//     这条分界是有意的（spec §3）——布局形状将来加字段时后端一行都不用改
//   - 不做长度校验：payload 上限属于接口层的参数校验，在 agentd 侧做
//   - 不产生时间：now 由调用方传入，测试才能钉住淘汰顺序
//   - 与 store.go 一致的叶子层纪律：方法错误 return 前不打日志，由调用方带上下文记录
package store

import (
	"context"
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

// WorkbenchBaseLimit 是 workbench_bases 的行数上限。
//
// 为什么是 50：每个 worktree 都会留一行，跑久了会攒到几百行。50 个目录远超
// 任何人同时在手的工作面，而每行 payload 只有 1–2 KiB，总量可以忽略。
// 不做「路径还在不在」的 GC——那要遍历文件系统、还要跨机器，成本远高于一行 JSON。
const WorkbenchBaseLimit = 50

// 单例键名。只有这两个，agentd 侧的接口层据此白名单校验。
const (
	WorkbenchKeySelected = "selected"
	WorkbenchKeyDock     = "dock"
)

// ListWorkbench 一次读出全部基准行与全部单例。
//
// 返回：
//   - bases: 按 updated_at 倒序（最近动过的在前）。恢复时顺序不重要，
//     但倒序让「淘汰谁」在读取结果里一眼可见，调试时省一次排序
//   - singles: 键为 WorkbenchKeySelected / WorkbenchKeyDock；不存在的键**不出现**
//   - 错误：查询失败
//
// 注意：两张表分两次查询，不在事务里。工作台状态没有跨表不变式——
// selected 指向一个已被淘汰的 base 是完全合法的（前端会退回未选中态）。
func (s *Store) ListWorkbench() ([]proto.WorkbenchBase, map[string]string, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT base_key, payload, updated_at FROM workbench_bases ORDER BY updated_at DESC`)
	if err != nil {
		return nil, nil, fmt.Errorf("查询工作台基准行: %w", err)
	}
	defer rows.Close()
	bases := []proto.WorkbenchBase{}
	for rows.Next() {
		var b proto.WorkbenchBase
		if err := rows.Scan(&b.BaseKey, &b.Payload, &b.UpdatedAt); err != nil {
			return nil, nil, fmt.Errorf("扫描工作台基准行: %w", err)
		}
		bases = append(bases, b)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历工作台基准行: %w", err)
	}

	srows, err := s.db.QueryContext(context.Background(),
		`SELECT key, value FROM workbench_singletons`)
	if err != nil {
		return nil, nil, fmt.Errorf("查询工作台单例: %w", err)
	}
	defer srows.Close()
	singles := map[string]string{}
	for srows.Next() {
		var k, v string
		if err := srows.Scan(&k, &v); err != nil {
			return nil, nil, fmt.Errorf("扫描工作台单例: %w", err)
		}
		singles[k] = v
	}
	if err := srows.Err(); err != nil {
		return nil, nil, fmt.Errorf("遍历工作台单例: %w", err)
	}
	return bases, singles, nil
}

// PutWorkbenchBase 写入或覆盖一行基准状态，并就地把总行数裁到 WorkbenchBaseLimit。
//
// 参数：
//   - key: 基准目录的身份（工作树是 path 或 path@machine，见前端 workspaceBase）
//   - payload: 前端序列化好的 JSON 字符串，本层不解析
//   - now: 毫秒时间戳，由调用方给定
//
// 返回：错误为写库故障。
//
// 注意：淘汰做在写入路径而不是后台定时任务——省一个 goroutine，而且「刚写完
// 立刻裁」的时机最准（此刻的 updated_at 排序就是最终排序）。
func (s *Store) PutWorkbenchBase(key, payload string, now int64) error {
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workbench_bases (base_key, payload, updated_at) VALUES (?, ?, ?)
     ON CONFLICT(base_key) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		key, payload, now); err != nil {
		return fmt.Errorf("写工作台基准行 %s: %w", key, err)
	}
	// 裁到上限：保留 updated_at 最大的 N 行，其余删。
	// 用子查询挑「要留下的」而不是「要删的」，因为 SQLite 的 DELETE 不支持
	// ORDER BY + LIMIT（需要编译期开关），这个写法在任何构建里都成立。
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_bases WHERE base_key NOT IN (
       SELECT base_key FROM workbench_bases ORDER BY updated_at DESC LIMIT ?)`,
		WorkbenchBaseLimit); err != nil {
		return fmt.Errorf("裁剪工作台基准行至 %d: %w", WorkbenchBaseLimit, err)
	}
	return nil
}

// DeleteWorkbenchBase 删除一行基准状态。行不存在时是空操作，不报错。
//
// 幂等的理由：前端在「一个目录的 tab 全关光了」时发删除，而它可能因为去抖
// 合并发两次。让第二次报错只会在控制台留下一条无意义的红。
func (s *Store) DeleteWorkbenchBase(key string) error {
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_bases WHERE base_key = ?`, key); err != nil {
		return fmt.Errorf("删除工作台基准行 %s: %w", key, err)
	}
	return nil
}

// PutWorkbenchSingleton 写入或覆盖一个单例。
//
// 参数：key 必须是 WorkbenchKeySelected / WorkbenchKeyDock 之一（白名单在接口层校验）；
// value 是字符串，语义由前端定义；now 是毫秒时间戳。
//
// 返回：错误为写库故障。
func (s *Store) PutWorkbenchSingleton(key, value string, now int64) error {
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workbench_singletons (key, value, updated_at) VALUES (?, ?, ?)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now); err != nil {
		return fmt.Errorf("写工作台单例 %s: %w", key, err)
	}
	return nil
}

// DeleteWorkbenchSingleton 删除一个单例。不存在时是空操作，不报错（同 DeleteWorkbenchBase）。
func (s *Store) DeleteWorkbenchSingleton(key string) error {
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM workbench_singletons WHERE key = ?`, key); err != nil {
		return fmt.Errorf("删除工作台单例 %s: %w", key, err)
	}
	return nil
}
