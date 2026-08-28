package agy

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestStartOrderingAndTaskEnv(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := t.TempDir()

	oldLook := lookAgyPath
	oldStart := startProcHost
	oldTimeout := fifoReaderTimeout
	defer func() {
		lookAgyPath = oldLook
		startProcHost = oldStart
		fifoReaderTimeout = oldTimeout
	}()

	fifoReaderTimeout = 10 * time.Millisecond
	lookAgyPath = func() (string, error) { return "/mock/agy", nil }

	var capturedSpec prochost.Spec
	startProcHost = func(spec prochost.Spec, selfExe string, extra ...string) (prochost.Handle, error) {
		capturedSpec = spec
		return prochost.Handle{PID: 1234, LockPath: spec.LockPath}, nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ad := New(logger)

	task := proto.Task{
		ID:              "T123",
		WorkDir:         workDir,
		WorktreeManaged: true,
		Model:           "claude-3-5-sonnet",
	}

	// 启动（因为 mock shim 不会真正读 FIFO，最终会就绪超时或取消，我们重点断言被调用时的参数与环境）
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = ad.Start(ctx, executor.StartReq{
		Task:        task,
		TaskDir:     tmpDir,
		PlanContent: "# Plan",
	})

	// 1. 断言 MarkRoot 被正确设置
	expectedMark := prochost.ResolveMarkRoot(workDir, true)
	if capturedSpec.MarkRoot != expectedMark || capturedSpec.MarkRoot == "" {
		t.Fatalf("MarkRoot 未正确设置: got %s, want %s", capturedSpec.MarkRoot, expectedMark)
	}

	// 2. 断言 Env 包含 TMPDIR
	var hasTmp bool
	for _, env := range capturedSpec.Env {
		if strings.HasPrefix(env, "TMPDIR=") {
			hasTmp = true
			break
		}
	}
	if !hasTmp {
		t.Fatalf("Env 未包含隔离 TMPDIR: %v", capturedSpec.Env)
	}

	// 3. 断言 hooks.json 被生成且包含正确的 matcher 与命令
	hooksPath := filepath.Join(workDir, ".agents", "hooks.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("未能生成 .agents/hooks.json: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "permission-hook --sock") {
		t.Fatalf("hooks.json 缺 permission-hook 命令: %s", content)
	}
	if !strings.Contains(content, "run_command|write_to_file") {
		t.Fatalf("hooks.json 缺多工具 matcher: %s", content)
	}
	if !strings.Contains(content, "\"timeout\": 86400") {
		t.Fatalf("hooks.json 缺 24h 超时: %s", content)
	}
}
