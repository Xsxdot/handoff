// ptyreclaim.go —— agentd 启动时认领既有 PTY 会话，显式退出时收口。
//
// 职责：
//   - reclaimPtySessions：扫描会话目录，活的登记进 Host，死的清掉，坏的留给人
//   - shutdownPtySessions：service stop 时显式向全部会话发送 kill
//
// 边界：
//   - 启动时不连接任何 socket，只扫目录与试锁；PTY 字节由浏览器真正订阅时才转发
//   - 不解释 broken：目录留着、进程不动，只记 Error 让人处理
//   - 不参与 agentd 崩溃或升级重启；没有显式 stop 意图时不会杀会话
//
// 为什么显式 stop 一起停而崩溃/升级不停：用户敲 service stop 的意图是让这台机器上
// 的 handoff 全部停止；升级则明确要求会话跨进程重启保留，两者不能靠猜测合并。
package agentd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

const ptyShutdownWait = 2 * time.Second

// ptyRoot 返回 PTY 会话目录根路径。
//
// 返回：通常是 <DataDir>/ptys；构造时固定，避免配置热更新改变正在托管的会话根。
// 注意：测试与启动器可在构造后替换 ptyRootPath，但生产不会动态迁移会话目录。
func (s *Server) ptyRoot() string { return s.ptyRootPath }

// reclaimPtySessions 扫描并认领 agentd 之前留下的 PTY 会话。
//
// 返回：扫描根目录失败时报错；单个 broken 条目不会让启动失败。
// 注意：StateBroken 既不登记也不删除，活会话只登记静态元数据，不连接 socket。
func (s *Server) reclaimPtySessions() error {
	root := s.ptyRoot()
	entries, err := sessdir.Scan(root)
	if err != nil {
		return fmt.Errorf("扫描 PTY 会话目录 %s: %w", root, err)
	}
	var live, cleaned, broken int
	adopt := make([]sessdir.Entry, 0, len(entries))
	for _, entry := range entries {
		switch entry.State {
		case sessdir.StateLive:
			adopt = append(adopt, entry)
			live++
		case sessdir.StateDead:
			if err := sessdir.Remove(root, entry.ID); err != nil {
				s.log.Warn("清理已死的 PTY 会话目录失败", "session", entry.ID, "err", err)
				continue
			}
			cleaned++
		case sessdir.StateBroken:
			// 有个进程活着而我们不知道它是什么。不删不杀，避免把未知进程当陈旧会话处理。
			s.log.Error("PTY 会话目录异常，已跳过（进程可能仍在运行，需人工处理）",
				"session", entry.ID, "dir", sessdir.Dir(root, entry.ID), "err", entry.Err)
			broken++
		}
	}
	s.pty.Adopt(adopt)
	s.log.Info("PTY 会话认领完成", "live", live, "cleaned", cleaned, "broken", broken)
	return nil
}

// ReclaimPtySessions 在 agentd 启动完成、开始监听前认领既有会话。
//
// 返回：扫描根目录失败时报错；调用方应记录错误并继续启动主服务。
// 注意：该导出薄壳只供 cmd 启动接线，实际三态逻辑在 reclaimPtySessions。
func (s *Server) ReclaimPtySessions() error { return s.reclaimPtySessions() }

// shutdownPtySessions 显式停止全部已登记的 PTY 会话。
//
// 参数：ctx 是 agentd 关停上下文；函数会在最多 ptyShutdownWait 后返回。
// 返回：无；单个会话关闭失败与整体超时都只记日志，不能阻塞 agentd 退出。
// 注意：这是唯一的批量 kill 路径；agentd 崩溃与升级不调用它。
func (s *Server) shutdownPtySessions(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, ptyShutdownWait)
	defer cancel()
	list := s.pty.List()
	if len(list) == 0 {
		s.log.Info("PTY 会话收口完成", "count", 0)
		return
	}

	var wg sync.WaitGroup
	for _, sess := range list {
		id := sess.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.pty.Close(id); err != nil {
				s.log.Warn("停止 PTY 会话失败", "session", id, "err", err)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.log.Info("PTY 会话收口完成", "count", len(list))
	case <-ctx.Done():
		s.log.Warn("PTY 会话收口超时，继续退出", "count", len(list), "wait", ptyShutdownWait)
	}
}

// ShutdownPtySessions 在 agentd 显式关停清理阶段停止全部 PTY 会话。
//
// 参数：ctx 是调用方的关停上下文。
// 返回：无；内部固定使用 2 秒总预算。
// 注意：该导出薄壳只供 cmd 启动接线，崩溃和升级路径不调用它。
func (s *Server) ShutdownPtySessions(ctx context.Context) { s.shutdownPtySessions(ctx) }
