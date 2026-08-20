// closeall.go —— 显式停止路径的批量收口。
//
// 职责：不依赖 agentd，直接扫会话根目录并逐个 kill。
//
// 边界：
//   - **只服务显式停止**（handoff service stop）。信号关停、升级换版、崩溃都
//     不该调它——那几条路必须让会话跨 agentd 生命周期活下来，那正是把 PTY
//     搬出 agentd 进程的全部意义
//   - 不删还活着的会话目录：目录收摊由 ptyhost 进程自己做，这里只发 kill
//   - 不改 agentd 的登记：调它的时候 agentd 可能已经不在了
//
// 为什么走目录扫描而不是让 agentd 代劳：显式停止的语义是「让这台机器上的
// handoff 全停下来」，而它可能在 agentd 已经停掉之后才被执行（先 stop 服务
// 再想起来收口，或者 agentd 本来就崩着）。经 agentd 转一手就会在最需要它的
// 那种情形下失效。
package ptyhost

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

// CloseAll 扫描 root 下全部会话并 kill 掉还活着的那些。
//
// 参数：
//   - root: 会话根目录，通常是 <DataDir>/ptys。**不存在不算错**——这台机器
//     可能从没开过终端
//   - log: 日志入口，不能为 nil
//   - budget: 总时间预算；到点就返回，不阻塞调用方
//
// 返回：成功发出 kill 并等到收摊的会话数，以及扫描失败时的错误。
// 单个会话关不掉只记 Warn 不算整体失败——它可能刚好自己退了。
//
// 注意：dead 与 broken 状态的目录一概不碰。dead 的由 agentd 下次启动时清；
// broken 的意味着「有个进程活着而我们不知道它是什么」，杀它不安全。
func CloseAll(root string, log *slog.Logger, budget time.Duration) (int, error) {
	entries, err := sessdir.Scan(root)
	if err != nil {
		return 0, err
	}
	live := make([]sessdir.Entry, 0, len(entries))
	for _, e := range entries {
		if e.State == sessdir.StateLive {
			live = append(live, e)
		}
	}
	if len(live) == 0 {
		log.Debug("显式停止：没有活着的 PTY 会话", "root", root)
		return 0, nil
	}

	// selfExe 传空串：这条路只 kill，永远不会 Open，拉不起新 ptyhost 也没关系
	h := New(root, "", log)
	h.Adopt(live)

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	var (
		mu     sync.Mutex
		closed int
		wg     sync.WaitGroup
	)
	for _, e := range live {
		id := e.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.Close(id); err != nil {
				log.Warn("显式停止 PTY 会话失败", "session", id, "err", err)
				return
			}
			mu.Lock()
			closed++
			mu.Unlock()
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Info("显式停止：PTY 会话已收口", "closed", closed, "total", len(live))
	case <-ctx.Done():
		// 超时不阻塞停服务：剩下的 ptyhost 各自持有锁，agentd 回来时会重新认领，
		// shell 退出后也有 24 小时 TTL 兜底，不会永久泄漏
		log.Warn("显式停止：PTY 会话收口超时，继续停止服务",
			"closed", closed, "total", len(live), "budget", budget)
	}
	mu.Lock()
	defer mu.Unlock()
	return closed, nil
}
