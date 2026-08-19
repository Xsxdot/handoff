// 本文件覆盖薄壳状态上报器的失败重试与快照刷新行为。
//
// 职责：锁住上报循环不会被一次网络错误打断，且每轮都会读取最新状态。
// 边界：不测试 HTTP 线格式；那由根模块 internal/client 的测试覆盖。
package shell

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func testReporterLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestReporterKeepsBeatingAfterFailure(t *testing.T) {
	oldInterval := reportInterval
	reportInterval = time.Millisecond
	t.Cleanup(func() { reportInterval = oldInterval })

	var calls atomic.Int32
	d := ReportDeps{Put: func(context.Context, proto.DesktopState) error {
		if calls.Add(1) == 1 {
			return errors.New("连接被拒")
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReporter(ctx, testReporterLog(), func() proto.DesktopState { return proto.DesktopState{} }, d)
		close(done)
	}()
	deadline := time.After(time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("失败后没有继续上报，calls=%d", calls.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后上报器未退出")
	}
}

func TestReporterSendsCurrentSnapshot(t *testing.T) {
	oldInterval := reportInterval
	reportInterval = time.Millisecond
	t.Cleanup(func() { reportInterval = oldInterval })

	var version atomic.Int32
	seen := make(chan string, 4)
	d := ReportDeps{Put: func(_ context.Context, st proto.DesktopState) error {
		seen <- st.AppVersion
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunReporter(ctx, testReporterLog(), func() proto.DesktopState {
			return proto.DesktopState{AppVersion: "v" + string(rune('0'+version.Add(1)))}
		}, d)
		close(done)
	}()
	first := <-seen
	second := <-seen
	cancel()
	<-done
	if first == second {
		t.Fatalf("每轮应读取当前 snapshot，连续读到 %q", first)
	}
}
