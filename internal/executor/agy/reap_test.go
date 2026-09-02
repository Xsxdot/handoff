package agy

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func TestReapAndProcHandle(t *testing.T) {
	tmpDir := t.TempDir()
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	lockPath := filepath.Join(tmpDir, lockFileName)
	writeProcInfo(tmpDir, &procInfo{
		Handle:    prochost.Handle{PID: 12345, LockPath: lockPath},
		SessionID: "s1",
	})

	h, err := ad.ProcHandle("T1", tmpDir)
	if err != nil || h.PID != 12345 {
		t.Fatalf("ProcHandle 失败: %v, pid=%d", err, h.PID)
	}

	if err := ad.Reap("T1", tmpDir); err != nil {
		t.Fatalf("Reap 失败: %v", err)
	}
}

func TestReapRestoresHooks(t *testing.T) {
	taskDir := t.TempDir()
	workDir := t.TempDir()
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := WriteTaskEnv(workDir, taskDir, "T-reap", "# Plan", "/tmp/perm.sock", "/bin/handoff", ""); err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	hooksPath := filepath.Join(workDir, agentsDirName, hooksFileName)
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("WriteTaskEnv 未生成 hooks.json: %v", err)
	}
	if err := writeProcInfo(taskDir, &procInfo{
		Handle:    prochost.Handle{PID: 99999999, LockPath: filepath.Join(taskDir, lockFileName)},
		SessionID: "s-reap",
	}); err != nil {
		t.Fatalf("写 proc.json 失败: %v", err)
	}

	if err := ad.Reap("T-reap", taskDir); err != nil {
		t.Fatalf("Reap 失败: %v", err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Fatalf("Reap 后新建 hooks.json 应被删除，实得: %v", err)
	}
}

func TestReapMissingProcInfoRestoresHooks(t *testing.T) {
	taskDir := t.TempDir()
	workDir := t.TempDir()
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := WriteTaskEnv(workDir, taskDir, "T-reap-no-proc", "# Plan", "/tmp/perm.sock", "/bin/handoff", ""); err != nil {
		t.Fatalf("WriteTaskEnv 失败: %v", err)
	}
	hooksPath := filepath.Join(workDir, agentsDirName, hooksFileName)
	if _, err := os.Stat(hooksPath); err != nil {
		t.Fatalf("WriteTaskEnv 未生成 hooks.json: %v", err)
	}

	if err := ad.Reap("T-reap-no-proc", taskDir); err == nil {
		t.Fatal("proc.json 缺失时 Reap 应报告读取错误")
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Fatalf("proc.json 缺失时 Reap 仍应恢复并删除新建 hooks.json，实得: %v", err)
	}
}
