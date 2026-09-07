package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestRunOneShotProcessHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := executor.RunOneShotProcess(ctx, []string{"sleep", "30"}, nil, "", "")
	if err == nil {
		t.Fatal("取消后必须失败")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("进程未被打断，耗时 %s", time.Since(start))
	}
}
