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
// 为什么显式 stop 一起停而崩溃/升级不停：显式 stop 的入口会调用
// ShutdownPtySessions；信号关停与进程内 Trigger（包括升级换版）只取消后台扫描，
// 让 ptyhost 跨 agentd 生命周期继续持有会话。两类意图不能共用一个会杀会话的 cleanup。
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

// GracefulShutdownCleanup 返回 agentd 通用优雅关停使用的 cleanup 闭包。
//
// 参数：wdCancel 是后台看门狗与镜像循环的取消函数。
// 返回：只取消这些 agentd 内部后台循环的 cleanup；不会关闭任何 PTY 会话。
// 注意：信号关停与进程内 Trigger 走同一条 Shutdown 路径，包含升级换版；显式停止
// PTY 必须由独立的显式 stop 入口调用 ShutdownPtySessions，不能挂在这里。
func (s *Server) GracefulShutdownCleanup(wdCancel context.CancelFunc) func() {
	return func() { wdCancel() }
}

// shutdownPtySessions 显式停止全部已登记的 PTY 会话。
//
// 参数：ctx 是 agentd 关停上下文；函数会在最多 ptyShutdownWait 后返回。
// 返回：无；单个会话关闭失败与整体超时都只记日志，不能阻塞 agentd 退出。
// 注意：每个 Host.Close 会等待自身的 ptyhost/PTY 收摊；这里仍保留 2 秒总预算，
// 到点只记录 Warn 并继续退出。这是唯一的批量 kill 路径；agentd 崩溃与升级不调用它。
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
		pid := sess.PID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.pty.Close(id); err != nil {
				s.log.Warn("停止 PTY 会话失败", "session", id, "pid", pid, "cause", err)
				return
			}
			s.log.Info("停止 PTY 会话完成", "session", id, "pid", pid)
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
		s.log.Warn("PTY 会话收口超时，继续退出", "count", len(list), "wait", ptyShutdownWait, "cause", ctx.Err())
	}
}

// ShutdownPtySessions 在 agentd 显式停止路径中停止全部 PTY 会话。
//
// 参数：ctx 是调用方的关停上下文。
// 返回：无；内部固定使用 2 秒总预算。
// 注意：该入口由显式停止路径调用，不挂在信号关停或进程内 Trigger（包括升级换版）上；
// 崩溃、OOM 与升级重启都必须让 ptyhost 留存。
func (s *Server) ShutdownPtySessions(ctx context.Context) { s.shutdownPtySessions(ctx) }
