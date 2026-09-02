package agy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

func TestResumeMissingSession(t *testing.T) {
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := ad.Resume(executor.ResumeReq{TaskID: "T1", TaskDir: t.TempDir()})
	if err == nil {
		t.Fatalf("缺少 SessionID 时应报错")
	}
}

func TestResumeCold(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)

	// 写入 proc.json 记录一个死进程
	lockPath := filepath.Join(tmpDir, lockFileName)
	writeProcInfo(tmpDir, &procInfo{
		Handle:    prochost.Handle{PID: 9999999, LockPath: lockPath},
		SessionID: "sess-cold-1",
	})

	oldStart := startProc
	defer func() { startProc = oldStart }()

	started := false
	startProc = func(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) {
		started = true
		if !req.Resume || req.SessionID != "sess-cold-1" {
			t.Errorf("StartProc 入参不符合预期: %+v", req)
		}
		return &Proc{Handle: prochost.Handle{PID: 2345}, TaskDir: req.TaskDir, SessionID: req.SessionID}, nil
	}

	out, err := ad.Resume(executor.ResumeReq{
		TaskID:    "T1",
		TaskDir:   tmpDir,
		RepoPath:  repoDir,
		SessionID: "sess-cold-1",
		Cold:      true,
	})
	if err != nil {
		t.Fatalf("Resume 失败: %v", err)
	}
	if !out.Alive || out.Mode != executor.ResumeModeCold {
		t.Fatalf("Resume 结果不符合预期: %+v", out)
	}
	if !started {
		t.Fatalf("冷恢复未调用 startProc")
	}
}

func TestResumeColdWriteTaskEnvFailureRestoresHooks(t *testing.T) {
	taskDir := t.TempDir()
	repoDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)
	writeDeadProcInfo(t, taskDir, 9999999, "sess-cold-write-fail")

	oldWrite := writeTaskEnvFn
	oldStart := startProc
	defer func() {
		writeTaskEnvFn = oldWrite
		startProc = oldStart
	}()
	writeTaskEnvFn = func(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (string, string, error) {
		path, prompt, err := WriteTaskEnv(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock)
		if err != nil {
			return path, prompt, err
		}
		return path, prompt, errors.New("cold WriteTaskEnv test failure")
	}
	startProc = func(context.Context, StartProcReq, *slog.Logger) (*Proc, error) {
		return nil, errors.New("startProc should not be called after WriteTaskEnv failure")
	}

	out, err := ad.Resume(executor.ResumeReq{
		TaskID: "T-cold-write-fail", TaskDir: taskDir, RepoPath: repoDir,
		SessionID: "sess-cold-write-fail", Cold: true,
	})
	if err != nil {
		t.Fatalf("Resume 不应抛硬 error，实得 %v", err)
	}
	if out.Alive {
		t.Fatalf("WriteTaskEnv 失败时 Resume 不应报告存活: %+v", out)
	}
	assertHooksAbsent(t, repoDir)
}

func TestResumeColdStartProcFailureRestoresHooks(t *testing.T) {
	taskDir := t.TempDir()
	repoDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)
	writeDeadProcInfo(t, taskDir, 9999999, "sess-cold-start-fail")

	oldWrite := writeTaskEnvFn
	oldStart := startProc
	defer func() {
		writeTaskEnvFn = oldWrite
		startProc = oldStart
	}()
	writeTaskEnvFn = WriteTaskEnv
	startProc = func(context.Context, StartProcReq, *slog.Logger) (*Proc, error) {
		return nil, errors.New("cold startProc test failure")
	}

	out, err := ad.Resume(executor.ResumeReq{
		TaskID: "T-cold-start-fail", TaskDir: taskDir, RepoPath: repoDir,
		SessionID: "sess-cold-start-fail", Cold: true,
	})
	if err != nil {
		t.Fatalf("Resume 不应抛硬 error，实得 %v", err)
	}
	if out.Alive {
		t.Fatalf("startProc 失败时 Resume 不应报告存活: %+v", out)
	}
	assertHooksAbsent(t, repoDir)
}

func writeDeadProcInfo(t *testing.T, taskDir string, pid int, sessionID string) {
	t.Helper()
	if err := writeProcInfo(taskDir, &procInfo{
		Handle:    prochost.Handle{PID: pid, LockPath: filepath.Join(taskDir, lockFileName)},
		SessionID: sessionID,
	}); err != nil {
		t.Fatalf("写 proc.json 失败: %v", err)
	}
}

func assertHooksAbsent(t *testing.T, repoDir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoDir, agentsDirName, hooksFileName)); !os.IsNotExist(err) {
		t.Fatalf("恢复失败后新建 hooks.json 应不存在，实得: %v", err)
	}
}

func TestResumePermServerFailureReturnsNotAlive(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)

	lockPath := filepath.Join(tmpDir, lockFileName)
	writeProcInfo(tmpDir, &procInfo{
		Handle:    prochost.Handle{PID: 9999999, LockPath: lockPath},
		SessionID: "sess-perm-fail",
	})

	oldNewPerm := newPermServerFn
	defer func() { newPermServerFn = oldNewPerm }()

	newPermServerFn = func(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
		return nil, fmt.Errorf("mock permServer bind failed")
	}

	out, err := ad.Resume(executor.ResumeReq{
		TaskID:    "T-PermFail",
		TaskDir:   tmpDir,
		RepoPath:  repoDir,
		SessionID: "sess-perm-fail",
		Cold:      true,
	})
	if err != nil {
		t.Fatalf("Resume 不应抛硬 error，实得 %v", err)
	}
	if out.Alive {
		t.Fatalf("权限服务端启动失败时，Resume 必须返回 Alive: false")
	}
}
