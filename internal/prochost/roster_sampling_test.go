// roster_sampling_test.go —— 后代名册采样循环测试。
//
// 职责：验证进程枚举永久不支持时采样只尝试一次，以及瞬时读取失败仍按周期重试。
//
// 边界：只测试采样状态机，不启动真实 shim 或 executor；Windows 运行期仍待真机验证。
package prochost

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

// TestRosterSamplingStopsWhenProcEnumUnsupported 钉住永久不支持时停止采样。
func TestRosterSamplingStopsWhenProcEnumUnsupported(t *testing.T) {
	oldInterval, oldEnum := rosterInterval, enumProcsFn
	t.Cleanup(func() { rosterInterval, enumProcsFn = oldInterval, oldEnum })
	rosterInterval = time.Millisecond

	calls := 0
	enumProcsFn = func() ([]procEntry, error) {
		calls++
		return nil, ErrNotSupported
	}
	s := &rosterSampler{path: filepath.Join(t.TempDir(), "roster.json")}
	runRosterSampling(make(chan struct{}), s, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if calls != 1 {
		t.Fatalf("永久不支持时只应采样一次：calls=%d want=1", calls)
	}
}

// TestRosterSamplingRetriesTransientProcEnumFailure 钉住瞬时失败仍按周期重试。
func TestRosterSamplingRetriesTransientProcEnumFailure(t *testing.T) {
	oldInterval, oldEnum := rosterInterval, enumProcsFn
	t.Cleanup(func() { rosterInterval, enumProcsFn = oldInterval, oldEnum })
	rosterInterval = time.Millisecond

	calls := make(chan struct{}, 4)
	enumProcsFn = func() ([]procEntry, error) {
		calls <- struct{}{}
		return nil, errors.New("本轮读取失败")
	}
	s := &rosterSampler{path: filepath.Join(t.TempDir(), "roster.json")}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runRosterSampling(stop, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-calls:
		case <-time.After(100 * time.Millisecond):
			close(stop)
			<-done
			t.Fatalf("瞬时失败后第 %d 次采样未发生", i+1)
		}
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("停止信号后采样循环未退出")
	}
}
