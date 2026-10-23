package orchestration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
)

func newR14LocalApprover(t *testing.T, log *slog.Logger, resolver *envfile.Resolver, executors ...string) *Approver {
	t.Helper()
	a, err := NewApprover(config.ApproverConfig{Executor: config.ExecutorList(executors), Timeout: time.Second}, resolver, log)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func assertR14LogsRedacted(t *testing.T, logs string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("approver logs contain sensitive marker %q:\n%s", secret, logs)
		}
	}
}

func TestB463LocalCandidateInvokeFailureLogsRedactAllPayloads(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	const permission = "PERMISSION_SENTINEL"
	const summary = "TASK_SUMMARY_SENTINEL"
	const cause = "RAW_ERROR_SENTINEL"
	const output = "RAW_OUTPUT_SENTINEL"
	a := newR14LocalApprover(t, log, nil, "codex")
	a.BindOneShotFor("codex", &stubShot{out: output, err: errors.New(cause)})
	decision := a.Decide(context.Background(), permission, summary, ApproverTaskContext{})
	if decision.Err == nil || len(decision.Attempts) != 1 {
		t.Fatalf("expected failed local candidate, got %+v", decision)
	}
	got := buf.String()
	assertR14LogsRedacted(t, got, permission, summary, cause, output)
	for _, marker := range []string{"审批者候选开始", "审批者候选结果", "审批者候选失败，尝试下一个", "审批者全部候选失败",
		"request_id=ap-", "executor=codex", "model=默认", "elapsed_ms=", "error_kind=internal"} {
		if !strings.Contains(got, marker) {
			t.Errorf("default WARN output missing %q:\n%s", marker, got)
		}
	}
}

func TestB463LocalCandidateEnvAndNonceFailuresDoNotLeak(t *testing.T) {
	t.Run("env resolver", func(t *testing.T) {
		var buf strings.Builder
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		const filename = "RESOLVER_FILENAME_SENTINEL.env"
		resolver := envfile.NewResolver(filepath.Join(t.TempDir(), "env"), envfile.Static(map[string]string{"codex": filename}),
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		a := newR14LocalApprover(t, log, resolver, "codex")
		a.BindOneShotFor("codex", &stubShot{out: `{"decision":"approve"}`})
		decision := a.Decide(context.Background(), "PERMISSION_SENTINEL", "TASK_SUMMARY_SENTINEL", ApproverTaskContext{})
		if decision.Err == nil {
			t.Fatal("missing env file should fail the candidate")
		}
		got := buf.String()
		assertR14LogsRedacted(t, got, filename, "PERMISSION_SENTINEL", "TASK_SUMMARY_SENTINEL")
		for _, marker := range []string{"request_id=ap-", "executor=codex", "model=默认", "elapsed_ms=", "error_kind=internal"} {
			if !strings.Contains(got, marker) {
				t.Errorf("env failure logs missing %q:\n%s", marker, got)
			}
		}
	})

	t.Run("nonce mismatch", func(t *testing.T) {
		var buf strings.Builder
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		const permission = "NONCE_PERMISSION_SENTINEL"
		const summary = "NONCE_SUMMARY_SENTINEL"
		const nonce = "NONCE_VALUE_SENTINEL"
		a := newR14LocalApprover(t, log, nil, "codex")
		a.BindOneShotFor("codex", &stubShot{fn: func(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
			return executor.OneShotReply{Text: `{"decision":"approve","nonce":"` + nonce + `"}`}, nil
		}})
		decision := a.Decide(context.Background(), permission, summary, ApproverTaskContext{})
		if decision.Err == nil {
			t.Fatal("wrong nonce must fail the candidate")
		}
		got := buf.String()
		assertR14LogsRedacted(t, got, permission, summary, nonce)
		if !strings.Contains(got, "error_kind=invalid_output") || !strings.Contains(got, "request_id=ap-") {
			t.Fatalf("nonce failure logs lack safe classification/correlation:\n%s", got)
		}
	})
}

func TestB463LocalCandidateSuccessAndSkippedWarnFields(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newR14LocalApprover(t, log, nil, "codex", "grok")
	a.BindOneShotFor("codex", &stubShot{out: `{"decision":"approve"}`})
	a.BindOneShotFor("grok", &stubShot{out: `{"decision":"approve"}`})
	decision := a.Decide(context.Background(), "Bash: ls", "summary", ApproverTaskContext{})
	if !decision.Approve || decision.Err != nil {
		t.Fatalf("first candidate should approve: %+v", decision)
	}
	got := buf.String()
	for _, group := range []struct {
		name string
		keys []string
	}{
		{name: "start", keys: []string{"审批者候选开始", "request_id=ap-", "executor=codex", "model=默认", "elapsed_ms=", "error_kind=pending"}},
		{name: "result", keys: []string{"审批者候选结果", "request_id=ap-", "executor=codex", "model=默认", "decision=approve", "elapsed_ms=", "error_kind=none"}},
		{name: "skip", keys: []string{"审批者候选跳过", "request_id=ap-", "executor=grok", "model=默认", "reason=前序候选已给出裁决", "elapsed_ms=", "error_kind=skipped"}},
	} {
		for _, key := range group.keys {
			if !strings.Contains(got, key) {
				t.Errorf("%s log missing %q:\n%s", group.name, key, got)
			}
		}
	}
}

func TestB463LocalCandidateTimeoutHasSafeClassification(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newR14LocalApprover(t, log, nil, "codex")
	a.timeout = 5 * time.Millisecond
	a.BindOneShotFor("codex", &stubShot{fn: func(ctx context.Context, _ executor.OneShotReq) (executor.OneShotReply, error) {
		<-ctx.Done()
		return executor.OneShotReply{}, ctx.Err()
	}})
	decision := a.Decide(context.Background(), "Bash: TIMEOUT_PERMISSION_SENTINEL", "TIMEOUT_SUMMARY_SENTINEL", ApproverTaskContext{})
	if decision.Err == nil || len(decision.Attempts) != 1 || decision.Attempts[0].ErrorKind != ApproverFailTimeout {
		t.Fatalf("deadline should be a timeout failure: %+v", decision)
	}
	got := buf.String()
	assertR14LogsRedacted(t, got, "TIMEOUT_PERMISSION_SENTINEL", "TIMEOUT_SUMMARY_SENTINEL", context.DeadlineExceeded.Error())
	for _, field := range []string{"request_id=ap-", "executor=codex", "model=默认", "elapsed_ms=", "error_kind=timeout"} {
		if !strings.Contains(got, field) {
			t.Errorf("timeout log missing %q:\n%s", field, got)
		}
	}
}

func TestB469PermissionLogClassUsesFixedTrustedMapping(t *testing.T) {
	for input, want := range map[string]string{
		"Bash: ls":                     "bash",
		"Write: a.txt":                 "write",
		"Edit: a.txt":                  "edit",
		"WebFetch: url":                "webfetch",
		"TOKEN_SENTINEL: secret value": "unknown",
		"arbitrary-prefix: secret":     "unknown",
	} {
		if got := permissionLogClass(input); got != want {
			t.Errorf("permissionLogClass(%q)=%q want %q", input, got, want)
		}
	}

	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a := newR14LocalApprover(t, log, nil, "codex")
	a.BindOneShotFor("codex", &stubShot{out: `{"decision":"approve"}`})
	_ = a.Decide(context.Background(), "TOKEN_SENTINEL: private permission payload", "summary", ApproverTaskContext{})
	if strings.Contains(strings.ToLower(buf.String()), "token_sentinel") {
		t.Fatalf("permission prefix leaked to WARN logs:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "permission_class=unknown") {
		t.Fatalf("unknown permission class missing safe label:\n%s", buf.String())
	}
}
