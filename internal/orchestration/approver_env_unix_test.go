//go:build unix

package orchestration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
)

// newEnvApprover 造一个带 env 文件的审批者；body 为 env 文件内容。
func newEnvApprover(t *testing.T, body string) *Approver {
	t.Helper()
	dir := envfile.Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.env"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res := envfile.NewResolver(dir, envfile.Static(map[string]string{"opencode": "a.env"}),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		res, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	if ap == nil {
		t.Fatal("NewApprover 返回 nil")
	}
	return ap
}

// TestApproverInjectsEnvIntoDecideCommand 断言子进程真的看到了变量——比断言
// 「切片被传下去了」更接近事实。
func TestApproverInjectsEnvIntoDecideCommand(t *testing.T) {
	ap := newEnvApprover(t, "HANDOFF_TEST_VAR=injected\n")
	var capturedEnv []string
	ap.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			capturedEnv = req.Env
			return executor.OneShotReply{Text: `{"decision":"escalate"}`, Status: executor.OneShotOK}, nil
		},
	})
	_ = ap.Decide(context.Background(), "test perm", "test")
	out, err := executor.RunOneShotProcess(context.Background(),
		[]string{"sh", "-c", `printf %s "$HANDOFF_TEST_VAR"`}, capturedEnv, "", "")
	if err != nil {
		t.Fatalf("RunOneShotProcess: %v", err)
	}
	if out != "injected" {
		t.Fatalf("子进程应看到注入的变量，实际输出 %q", out)
	}
}

// TestApproverStillInheritsAgentdEnv 确认注入是「追加」而不是「替换」——
// 替换掉 agentd 环境会让审批者连 PATH 都没有。
func TestApproverStillInheritsAgentdEnv(t *testing.T) {
	t.Setenv("HANDOFF_INHERITED_VAR", "inherited")
	ap := newEnvApprover(t, "HANDOFF_TEST_VAR=injected\n")
	var capturedEnv []string
	ap.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			capturedEnv = req.Env
			return executor.OneShotReply{Text: `{"decision":"escalate"}`, Status: executor.OneShotOK}, nil
		},
	})
	_ = ap.Decide(context.Background(), "test perm", "test")
	out, err := executor.RunOneShotProcess(context.Background(),
		[]string{"sh", "-c", `printf %s "$HANDOFF_INHERITED_VAR"`}, capturedEnv, "", "")
	if err != nil {
		t.Fatalf("RunOneShotProcess: %v", err)
	}
	if out != "inherited" {
		t.Fatalf("应继承 agentd 环境，实际输出 %q", out)
	}
}

// TestApproverEnvFailureDoesNotRunCommand 断言 env 解析失败时不执行裁决命令，
// 直接报错交给 Decide 走 escalate。
func TestApproverEnvFailureDoesNotRunCommand(t *testing.T) {
	dir := envfile.Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	res := envfile.NewResolver(dir, envfile.Static(map[string]string{"opencode": "nope.env"}),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		res, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	var called bool
	ap.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			called = true
			return executor.OneShotReply{Status: executor.OneShotOK}, nil
		},
	})
	d := ap.Decide(context.Background(), "test perm", "test")
	if d.Err == nil {
		t.Fatal("env 解析失败时应报错")
	}
	if !strings.Contains(d.Err.Error(), "nope.env") {
		t.Errorf("错误应带文件名，实际 %q", d.Err.Error())
	}
	if called {
		t.Error("env 解析失败时不应执行裁决命令")
	}
}

// TestApproverWithNilResolverStillRuns 确认 nil resolver（未配置/测试场景）不注入也不报错。
func TestApproverWithNilResolverStillRuns(t *testing.T) {
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	var called bool
	ap.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			called = true
			nonce := extractNonceForTest(req.Prompt)
			return executor.OneShotReply{Text: fmt.Sprintf(`{"decision":"approve","nonce":%q}`, nonce), Status: executor.OneShotOK}, nil
		},
	})
	d := ap.Decide(context.Background(), "test perm", "test")
	if d.Err != nil {
		t.Fatalf("Decide: %v", d.Err)
	}
	if !called {
		t.Fatal("应正常调用 OneShot")
	}
	if !d.Approve {
		t.Fatal("应批准")
	}
}
