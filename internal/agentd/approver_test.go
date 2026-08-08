// approver 白盒测试：黑名单命中、CLI 裁决、fail-closed 三连与 fail-open 拒绝。
package agentd

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
)

// newTestApprover 构造带注入 runCmd 的 Approver（裁决输出 out、错误 err）。
func newTestApprover(t *testing.T, out string, err error) *Approver {
	a, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	a.runCmd = func(ctx context.Context, argv []string) (string, error) { return out, err }
	return a
}

func TestApproverNilWhenUnconfigured(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{}, slog.Default())
	if err != nil || a != nil {
		t.Fatalf("未配置应返回 (nil,nil)，得到 %v %v", a, err)
	}
}

func TestBlacklistBuiltinAndCustom(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: "opencode", Timeout: time.Second,
		Blacklist: []string{`kubectl .*delete`},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"Bash: rm -rf node_modules", "Bash: git push --force origin main",
		"Bash: sudo systemctl restart nginx", "Bash: git reset --hard HEAD~3",
		"Bash: psql -c 'DROP TABLE users'", "Bash: deploy to production",
		"Bash: kubectl pods delete --all",
	} {
		if hit, _ := a.Blacklisted(s); !hit {
			t.Fatalf("应命中黑名单: %s", s)
		}
	}
	if hit, _ := a.Blacklisted("Bash: go test ./..."); hit {
		t.Fatalf("go test 不应命中黑名单")
	}
}

func TestDecideApprove(t *testing.T) {
	a := newTestApprover(t, "思考过程...\n{\"decision\":\"approve\",\"reason\":\"项目内读写\"}\n", nil)
	d := a.Decide(context.Background(), "Edit: main.go", "修 bug")
	if !d.Approve || d.Err != nil {
		t.Fatalf("应 approve: %+v", d)
	}
}

func TestDecideEscalate(t *testing.T) {
	a := newTestApprover(t, `{"decision":"escalate","reason":"拿不准"}`, nil)
	d := a.Decide(context.Background(), "Bash: curl ...", "")
	if d.Approve || d.Err != nil || d.Reason != "拿不准" {
		t.Fatalf("应干净 escalate: %+v", d)
	}
}

// TestDecideFailClosed 覆盖 fail-closed 三连：命令失败 / 输出无 JSON / decision 取值非法，
// 全部 escalate 且 Err 非 nil（供上层连续失败计数）。
func TestDecideFailClosed(t *testing.T) {
	for name, a := range map[string]*Approver{
		"命令失败":  newTestApprover(t, "", errors.New("exit 1")),
		"无JSON":  newTestApprover(t, "我觉得可以批", nil),
		"取值非法": newTestApprover(t, `{"decision":"deny"}`, nil),
	} {
		if d := a.Decide(context.Background(), "x", ""); d.Approve || d.Err == nil {
			t.Fatalf("%s: 应 fail-closed escalate: %+v", name, d)
		}
	}
}

func TestDecidePromptContainsContext(t *testing.T) {
	var got []string
	a := newTestApprover(t, `{"decision":"approve"}`, nil)
	a.runCmd = func(ctx context.Context, argv []string) (string, error) { got = argv; return `{"decision":"approve"}`, nil }
	a.Decide(context.Background(), "PERM-TEXT", "TASK-SUMMARY")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "PERM-TEXT") || !strings.Contains(joined, "TASK-SUMMARY") {
		t.Fatalf("裁决 prompt 应含权限原文与任务摘要: %v", got)
	}
}
