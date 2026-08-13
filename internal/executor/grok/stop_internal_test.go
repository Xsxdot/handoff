package grok

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
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

// TestStopPropagatesStillAlive 验证 grok 的 Stop 不再把 Kill 错误丢进 `_`。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := &runState{
		taskID: "t-still-alive",
		evCh:   make(chan executor.AdapterEvent, 4),
		proc:   &Proc{Handle: prochost.Handle{PID: 4242}},
	}
	a.mu.Lock()
	a.runs["t-still-alive"] = r
	a.mu.Unlock()

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
