package agentd

// b460_approver_context_test.go —— B460 S8 V7：任务上下文穿过跨机审批的真实链路
// （发起方 Approver → remoteApproverDecider.DecideRemote → client.ApproverDecide 编码 →
// HTTP → 对端 handleApproverDecide 解码 → RunLocalRing），以及 wire 的缺席/零值区分与
// 老收件端兼容（F7–F9）。
import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/store"
)

// b460Pair 起对端 B（fake codex OneShot）与发起方 A（共享链首环指向 B），返回 A 的
// Approver、B 的 provider 与 B 的请求记录。
func b460Pair(t *testing.T) (*orchestration.Approver, *fakeRingProvider, *recordHandler) {
	t.Helper()
	pB := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","reason":"B 放行","nonce":"NONCE"}`}
	_, _, tsB, recB := newBareServer(t, pB)
	dirA := t.TempDir()
	cfgA := &config.Config{Token: testToken, DataDir: dirA, Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{"linux-01": {Addr: strings.TrimPrefix(tsB.URL, "http://"), Token: testToken}}}
	stA, err := store.Open(filepath.Join(dirA, "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stA.Close() })
	srvA := NewServer(cfgA, stA, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srvA.SetConfigPath(filepath.Join(dirA, "config.yaml"))
	ap, err := orchestration.NewApproverForShared(config.ApproverConfig{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ap.SetSelfMachine(srvA.IsSelfMachine)
	ap.SetRemoteDecider(srvA.ApproverRemoteDecider())
	ap.SetChainReader(&staticChainReader{chain: orchestration.ApproverChain{
		Rings: []orchestration.ApproverRing{{Machine: "linux-01", Executor: "codex"}},
	}})
	return ap, pB, recB
}

func TestB460RemoteDecideCarriesTaskContext(t *testing.T) {
	ap, pB, recB := b460Pair(t)
	tc := orchestration.ApproverTaskContext{Mode: "readonly", Workdir: "/wt/z",
		ArtifactsDir: "/d/tasks/z/artifacts", TaskTmpDir: "/d/tmp/z"}
	if d := ap.Decide(context.Background(), "Bash: ls", "摘要", tc); !d.Approve || d.Err != nil {
		t.Fatalf("应 approve: %+v", d)
	}
	prompt := pB.lastPrompt()
	for _, s := range []string{"/wt/z", "/d/tasks/z/artifacts", "/d/tmp/z", "readonly", "只读任务的裁决政策："} {
		if !strings.Contains(prompt, s) {
			t.Fatalf("对端提示词缺 %q：\n%s", s, prompt)
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(recB.lastBody()), &raw); err != nil {
		t.Fatal(err)
	}
	var sub map[string]string
	if err := json.Unmarshal(raw["task_context"], &sub); err != nil {
		t.Fatalf("请求体应有 task_context 对象：%s（%v）", recB.lastBody(), err)
	}
	want := map[string]string{"mode": "readonly", "workdir": "/wt/z",
		"artifacts_dir": "/d/tasks/z/artifacts", "task_tmp_dir": "/d/tmp/z"}
	for k, v := range want {
		if sub[k] != v {
			t.Fatalf("task_context.%s = %q，期望 %q（%v）", k, sub[k], v, sub)
		}
	}
}

// TestB460RemoteDecideZeroContextAbsentAndBaseline：零值上下文 → 请求体**无** task_context
// 键（区分缺席与零值），对端提示词与基线逐字节一致（同一份编排金样本）。
func TestB460RemoteDecideZeroContextAbsentAndBaseline(t *testing.T) {
	ap, pB, recB := b460Pair(t)
	const summary = "统计行数的调查任务"
	const perm = "Bash: python3 - <<'EOF'\nprint(1)\nEOF"
	if d := ap.Decide(context.Background(), perm, summary, orchestration.ApproverTaskContext{}); !d.Approve {
		t.Fatalf("应 approve: %+v", d)
	}
	if strings.Contains(recB.lastBody(), "task_context") {
		t.Fatalf("零值上下文不得带 task_context 键：%s", recB.lastBody())
	}
	golden, err := os.ReadFile(filepath.Join("..", "orchestration", "testdata", "b460_prompt_zero_context.golden"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := pB.lastPrompt()
	got := strings.ReplaceAll(prompt, extractPromptNonce(prompt), "N0NCE")
	if got != string(golden) {
		t.Fatalf("无上下文时对端提示词必须与基线一致\n--- got ---\n%s\n--- want ---\n%s", got, golden)
	}
}

// baselineApproverDecideReq 是本卡之前 proto.ApproverDecideReq 的结构副本（老收件端）。
type baselineApproverDecideReq struct {
	Permission  string `json:"permission"`
	Executor    string `json:"executor"`
	TaskSummary string `json:"task_summary,omitempty"`
	Model       string `json:"model,omitempty"`
}

// TestB460OldReceiverIgnoresTaskContext：新请求解进老结构成功、字段值不变；收件端对未知键
// 宽松解码（F9：现状 handler 不用 DisallowUnknownFields，带陌生键仍 200）。
func TestB460OldReceiverIgnoresTaskContext(t *testing.T) {
	body := `{"permission":"Bash: ls","executor":"codex","task_summary":"s",` +
		`"task_context":{"mode":"readonly","workdir":"/w"},"future_key":1}`
	var old baselineApproverDecideReq
	if err := json.Unmarshal([]byte(body), &old); err != nil {
		t.Fatalf("老结构解码失败: %v", err)
	}
	if old.Permission != "Bash: ls" || old.Executor != "codex" || old.TaskSummary != "s" {
		t.Fatalf("老结构字段值变了: %+v", old)
	}
	p := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","nonce":"NONCE"}`}
	env := newApproverEndpointEnv(t, p)
	code, out := postApproverDecide(t, env, body, true)
	if code != http.StatusOK || out.Decision != "approve" {
		t.Fatalf("带新键与陌生键的请求应 200 approve: %d %+v", code, out)
	}
	if !strings.Contains(p.lastPrompt(), "/w") {
		t.Fatalf("对端应把 task_context 透传进提示词：\n%s", p.lastPrompt())
	}
}
