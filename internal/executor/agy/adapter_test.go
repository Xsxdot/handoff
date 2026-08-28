package agy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestAdapterNotRunning(t *testing.T) {
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Events on non-existent task returns closed channel
	ch := ad.Events("non-existent")
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("未启动任务的 Events 通道应已关闭")
		}
	default:
		t.Fatalf("未启动任务的 Events 通道应能立即读取关闭信号")
	}

	// Send on non-existent task returns ErrTaskNotRunning
	err := ad.Send(context.Background(), "non-existent", "test")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("Send 未运行任务应返回 ErrTaskNotRunning, got: %v", err)
	}

	// Stop on non-existent task returns ErrTaskNotRunning
	err = ad.Stop("non-existent")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("Stop 未运行任务应返回 ErrTaskNotRunning, got: %v", err)
	}
}

func TestAdapterStop(t *testing.T) {
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	tmpDir := t.TempDir()
	r := ad.newRun("T1", tmpDir, tmpDir)

	if err := ad.Stop("T1"); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	select {
	case <-r.stopCh:
	default:
		t.Fatalf("Stop 应该关闭 stopCh")
	}

	// lookup 应该已为 nil
	if ad.lookup("T1") != nil {
		t.Fatalf("Stop 后任务应已注销")
	}
}
