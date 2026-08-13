package claudecode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func stubStillAlive(t *testing.T) {
	t.Helper()
	orig := killProcHost
	killProcHost = func(prochost.Handle) error {
		return fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive)
	}
	t.Cleanup(func() { killProcHost = orig })
}

// TestStopPropagatesStillAlive 是防回归：claudecode 本来就裸抛 kerr，
// 这条钉住它——将来谁想在这里「顺手把错误咽掉」会当场翻红。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := a.newRun("t-still-alive", t.TempDir(), t.TempDir())
	r.proc = &Proc{Handle: prochost.Handle{PID: 4242}}

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
