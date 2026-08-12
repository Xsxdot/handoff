// shutdown.go —— agentd 的优雅关停协调。
//
// 职责：
//   - 汇合两种停机意图：进程信号（SIGINT/SIGTERM）与进程内触发（Shutdown.Trigger）
//   - 收到意图后停止接受新连接、给在途请求一段收尾时间、跑调用方给的清理闭包
//   - 用返回值表达退出码约定：优雅关停返回 nil（→ exit 0），监听失败原样返回（→ exit 1）
//
// 边界：
//   - 不知道**为什么**要停：信号也好、自更新换版也好，本文件一视同仁
//   - 不关数据库、不释放锁：那些是调用方在 cleanup 闭包里做的事，顺序由调用方定
//   - 不负责重启：把进程拉回来是 systemd / launchd 的职责
//
// 为什么 exit 0 这件事必须写在这里而不是留给调用方：自更新换版的整条链
// （下载 → 替换 → 退出 → 管理器拉起新版）唯一的交接点就是退出码。systemd 的
// Restart=on-failure 在 exit 0 时**不会**重启——那样服务会在换版后无声消失。
// 本期把 deploy 模板改成 Restart=always 正是为此。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ShutdownGrace 是停止接受新连接后，留给在途请求收尾的时间上限。
//
// 15s 的来由：agentd 最长的同步 handler 是 run 路由（RunCmdTimeout=10min），
// 但那类长跑请求本来就会被 http.Server.Shutdown 等到底或随进程退出而断，
// 拿 10min 当 grace 只会让每次重启都卡十分钟。15s 覆盖的是普通 API 调用
// （dispatch/reply/show 都是亚秒级）的收尾，够用且不拖慢换版。
const ShutdownGrace = 15 * time.Second

// Shutdown 协调 agentd 的停机。
//
// 用法：NewShutdown 之后调 Serve，它会一直阻塞到停机或监听失败。
// 进程内的其它组件（B54.3 的更新循环）调 Trigger 来请求停机。
type Shutdown struct {
	log *slog.Logger

	once   sync.Once
	fired  chan struct{}
	mu     sync.Mutex
	reason string
}

// NewShutdown 构造一个停机协调器。
//
// 参数：
//   - log: 日志入口；停机的每个阶段都会在这里留痕
func NewShutdown(log *slog.Logger) *Shutdown {
	return &Shutdown{log: log, fired: make(chan struct{})}
}

// Trigger 请求停机。
//
// 参数：
//   - reason: 停机原因，形如 "signal:terminated" / "update:v0.2.0"。会进日志
//
// 返回：
//   - true 表示本次调用真的触发了停机；false 表示已经在停了，本次被忽略
//
// 注意：
//   - 幂等。多路来源（信号 + 自更新）可能同时触发，只有第一个算数，
//     否则会有两条关停流程并发跑 cleanup
func (s *Shutdown) Trigger(reason string) bool {
	first := false
	s.once.Do(func() {
		first = true
		s.mu.Lock()
		s.reason = reason
		s.mu.Unlock()
		s.log.Info("收到停机请求", "reason", reason)
		close(s.fired)
	})
	if !first {
		s.log.Debug("停机请求被忽略（已在停机中）", "reason", reason, "first_reason", s.Reason())
	}
	return first
}

// Reason 返回首次触发的停机原因；未触发时返回空串。
func (s *Shutdown) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Serve 在 srv.Addr 上监听并阻塞，直到停机或监听失败。
//
// 参数：
//   - srv: 已配置好 Addr 与 Handler 的 HTTP 服务
//   - cleanup: 停机时跑一次的清理闭包（关数据库、释放锁等）。**由调用方决定顺序**
//
// 返回：
//   - nil 表示优雅关停完成（进程应 exit 0，管理器据此重新拉起）
//   - 非 nil 表示监听/启动失败（进程应 exit 1）
func (s *Shutdown) Serve(srv *http.Server, cleanup func()) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		// 端口被占是最常见的启动失败，报文里带上地址，别让用户去日志里找
		return fmt.Errorf("监听 %s: %w", srv.Addr, err)
	}
	return s.serveWithListener(ln, srv, cleanup)
}

// serveWithListener 是 Serve 的可测形态：监听器由调用方给。
//
// 拆出来的理由：单测要在一个随机可用端口上跑（net.Listen ":0"），而 Serve
// 从 srv.Addr 里取地址、拿不到实际分配的端口。测试拿着 listener 才能知道
// 该往哪儿探活。
func (s *Shutdown) serveWithListener(ln net.Listener, srv *http.Server, cleanup func()) error {
	// 信号与进程内触发汇到同一个 Shutdown 上：signal.Notify 收到就转成一次 Trigger
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		s.Trigger("signal:" + sig.String())
	}()

	errCh := make(chan error, 1)
	go func() {
		// Serve 正常收到 Shutdown 时返回 ErrServerClosed，那是预期信号不是错误
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		// 还没触发停机，Serve 就自己返回了——这是真失败
		if err != nil {
			s.log.Error("HTTP 服务异常退出", "cause", err)
			return fmt.Errorf("HTTP 服务: %w", err)
		}
		// 极少见：外部直接关了 srv。当作优雅关停处理，但要留痕
		s.log.Warn("HTTP 服务在未触发停机的情况下正常返回")
		cleanup()
		return nil
	case <-s.fired:
	}

	reason := s.Reason()
	s.log.Info("开始优雅关停", "reason", reason, "grace", ShutdownGrace)
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// 超时不算失败：在途请求没等完也要继续收尾，否则数据库永远关不掉。
		// 但必须留痕——它意味着有请求被硬断了
		s.log.Warn("等待在途请求超时，继续收尾", "cause", err, "grace", ShutdownGrace)
	}
	cleanup()
	s.log.Info("优雅关停完成", "reason", reason)
	return nil
}
