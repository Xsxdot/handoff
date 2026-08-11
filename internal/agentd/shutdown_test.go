// shutdown 的行为测试。
//
// 信号路径不在单测里触发（给测试进程发 SIGTERM 会连带影响 go test 本身），
// 覆盖的是同一条汇合逻辑的另一个入口：Trigger。两者在 Serve 里汇到同一个
// select，测 Trigger 等于测通了那条路。
package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// quietLogger 返回一个丢弃输出的 logger，避免测试刷屏。
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Trigger 必须幂等：第一次返回 true，之后一律 false。
//
// why 这条重要：B54.3 的更新循环与信号处理可能同时触发关停。若两次都"生效"，
// 就会有两个 goroutine 同时调 srv.Shutdown 并各自跑一遍 cleanup——数据库被
// 关两次、日志出现两套自相矛盾的关停原因。
func TestTriggerIsIdempotent(t *testing.T) {
	sd := NewShutdown(quietLogger())
	if !sd.Trigger("update:v0.2.0") {
		t.Fatal("首次 Trigger 应返回 true")
	}
	if sd.Trigger("signal:SIGTERM") {
		t.Fatal("二次 Trigger 应返回 false")
	}
	if got := sd.Reason(); got != "update:v0.2.0" {
		t.Fatalf("Reason=%q，应保留首次触发的原因", got)
	}
}

// Serve 在被 Trigger 后应优雅返回 nil，并且只跑一次 cleanup。
//
// 返回 nil 是**退出码约定**的实现：cobra 的 RunE 返回 nil → 进程 exit 0 →
// systemd Restart=always / launchd KeepAlive 把新版本拉起来。返回非 nil 就是
// exit 1，那条链会被当成崩溃处理。
func TestServeReturnsNilOnGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())

	var cleanups atomic.Int32
	done := make(chan error, 1)
	go func() { done <- sd.serveWithListener(ln, srv, func() { cleanups.Add(1) }) }()

	// 等服务真的起来再触发，否则测的是"还没开始就停"的空路径
	waitListening(t, ln.Addr().String())
	sd.Trigger("test")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关停应返回 nil，得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 未在 10s 内返回")
	}
	if got := cleanups.Load(); got != 1 {
		t.Fatalf("cleanup 应恰好跑一次，跑了 %d 次", got)
	}
}

// 监听失败必须原样返回错误（→ exit 1），不能被当成优雅关停吞掉。
//
// why：端口被占是最常见的启动失败。若这里返回 nil，systemd 会认为服务
// "正常退出"，配合 Restart=always 变成每 3 秒重启一次的静默死循环。
func TestServeReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:1", Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())
	err := sd.Serve(srv, func() {})
	if err == nil {
		t.Fatal("监听 1 端口应失败并返回错误")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatal("ErrServerClosed 不该外泄，它是优雅关停的正常信号")
	}
}

// waitListening 轮询到端口可连为止，避免用 sleep 猜时间。
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("端口 %s 在 5s 内未就绪", addr)
}

// 确保未使用的导入不报错（context 供 shutdown.go 使用，此处占位断言）
var _ = context.Background
