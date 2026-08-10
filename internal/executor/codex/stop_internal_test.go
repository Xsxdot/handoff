package codex

import (
	"errors"
	"fmt"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
)

// stubStillAlive 把 Kill 换成「已发信号但复核仍存活」的形态。
func stubStillAlive(t *testing.T) {
	t.Helper()
	orig := killProcHost
	killProcHost = func(prochost.Handle) error {
		return fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive)
	}
	t.Cleanup(func() { killProcHost = orig })
}

// TestStopPropagatesStillAlive 验证进程复核失败时，Stop 返回的错误仍能被
// errors.Is 认出 prochost.ErrStillAlive——agentd 侧的人工提示分支全靠它。
// codex 原先在这里就地 emit 一条 progress 然后 return nil，信号到此为止。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := newRunState("t-still-alive", t.TempDir(), t.TempDir())
	r.proc = &Proc{Handle: prochost.Handle{PID: 4242}}
	a.mu.Lock()
	a.runs["t-still-alive"] = r
	a.mu.Unlock()

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
