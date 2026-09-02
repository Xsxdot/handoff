package agy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
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

func TestManagedTaskTmpEnvHome(t *testing.T) {
	taskDir := t.TempDir()
	_, env := managedTaskTmpEnv(taskDir, "t1")
	wantHome := "HOME=" + filepath.Join(taskDir, agyHomeDirName)
	hasHome := false
	hasTmp := false
	for _, item := range env {
		if item == wantHome {
			hasHome = true
		}
		if strings.HasPrefix(item, "TMPDIR=") {
			hasTmp = true
		}
	}
	if !hasHome {
		t.Fatalf("env 缺任务级 HOME=%q: %v", wantHome, env)
	}
	if !hasTmp {
		t.Fatalf("env 缺 TMPDIR: %v", env)
	}
}

func TestStopRestoresHooks(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()
	if _, _, err := WriteTaskEnv(workDir, taskDir, "T-stop", "# Plan", "/tmp/perm.sock", "/bin/handoff", ""); err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	hooksPath := filepath.Join(workDir, agentsDirName, hooksFileName)
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("WriteTaskEnv 未生成 hooks.json: %v", err)
	}

	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ad.newRun("T-stop", taskDir, workDir)
	if err := ad.Stop("T-stop"); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Fatalf("Stop 后新建 hooks.json 应被删除，实得: %v", err)
	}
}

func TestStartRollbackRestoresHooks(t *testing.T) {
	workDir := t.TempDir()
	taskDir := t.TempDir()
	oldStart := startProc
	defer func() { startProc = oldStart }()
	startProc = func(context.Context, StartProcReq, *slog.Logger) (*Proc, error) {
		return nil, errors.New("start proc test failure")
	}

	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := ad.Start(context.Background(), executor.StartReq{
		Task:    proto.Task{ID: "T-start-rollback", WorkDir: workDir},
		TaskDir: taskDir,
	})
	if err == nil {
		t.Fatal("startProc 失败桩下 Start 应失败")
	}
	hooksPath := filepath.Join(workDir, agentsDirName, hooksFileName)
	if _, statErr := os.Stat(hooksPath); !os.IsNotExist(statErr) {
		t.Fatalf("Start rollback 后新建 hooks.json 应被删除，实得: %v", statErr)
	}
}
