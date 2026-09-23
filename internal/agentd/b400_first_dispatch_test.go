// b400_first_dispatch_test.go —— B400 首派基线护栏的端到端回归。
//
// 职责：锁住「卡首次非审阅派发时，若解析出的基线（默认线）树里没有本卡 spec
// 附件，派发必须被拒、留 needs_human 说明、且绝不触达 Transport」。
// 缝：HTTP `POST /api/cards/{id}/step` → startCardStep → StepRunner.Run →
// ViaTemplate 的派发决议段（spec §6 接缝 1）。断言落在真实生产装配上，不直调
// ViaTemplate，也不替换 runStepFn 的 Runner——只替换 Transport 以观测「护栏是否
// 在派发前拦下」。
// 边界：不复制 workspace 的路径在场判定；不改任何 git ref；不测 task 生命周期。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b400Git 在 dir 执行 git，失败即 Fatal。
func b400Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// b400CloneWithoutSpec 造「裸 origin + 克隆」：默认分支 main 上只有 README.md，
// 没有 docs/superpowers/specs/b400.md。返回克隆路径（= 本机项目仓库）。
//
// 必须用克隆而不是 git init：只有克隆才会自动写 refs/remotes/origin/HEAD，且
// ResolveDispatchBase 对普通分支名会真的 fetch（file:// remote 可在测试内离线完成）。
func b400CloneWithoutSpec(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	b400Git(t, parent, "init", "--bare", "-q", origin)
	seed := filepath.Join(t.TempDir(), "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	b400Git(t, seed, "init", "-q")
	b400Git(t, seed, "checkout", "-q", "-b", "main")
	b400Git(t, seed, "config", "user.email", "t@handoff.dev")
	b400Git(t, seed, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b400Git(t, seed, "add", "README.md")
	b400Git(t, seed, "commit", "-q", "-m", "init")
	b400Git(t, seed, "remote", "add", "origin", origin)
	b400Git(t, seed, "push", "-q", "origin", "main")
	b400Git(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	clone := filepath.Join(t.TempDir(), "clone")
	b400Git(t, parent, "clone", "-q", origin, clone)
	return clone
}

// b400FlowEnv 建一张钉在「首个非审阅派发节点 impl」上的卡，并把项目位置登记到 repo。
func b400FlowEnv(t *testing.T, repo string) (*ledgerEnv, string) {
	t.Helper()
	env := newLedgerEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "本机测试实现纪律")
	if _, err := env.ledger.PutWorkflow("b400-flow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "impl"},
		{Name: "impl", Dispatch: true, Verdict: true, Template: "feature-impl", MaxRounds: 3, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "B400 首派卡", Project: "handoff", Workflow: "b400-flow", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "handoff-b400", Name: "handoff", Path: repo, OriginURL: "", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	return env, card.ID
}

// b400CardState 从卡事件流取 needs_human 的 reason、最后一条 comment 正文与是否已有 dispatched。
func b400CardState(t *testing.T, env *ledgerEnv, cardID string) (reason, comment string, dispatched bool) {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 200)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	for _, e := range events {
		switch e.Type {
		case ledger.EvNeedsHuman:
			var p struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && p.Reason != "" {
				reason = p.Reason
			}
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && p.Body != "" {
				comment = p.Body
			}
		case ledger.EvDispatched:
			dispatched = true
		}
	}
	return reason, comment, dispatched
}

// TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase 锁 spec §6 接缝 1 断言①：
// 卡无快照、无显式基线、spec 附件不在默认线树上 ⇒ 拒发；文案含解析到的分支名与缺失
// 路径；护栏在 Transport 之前（transportCalled=0），且不留 dispatched 快照。
//
// 红（当前 HEAD）：护栏不存在，ViaTemplate 直达被替换的 Transport；卡落的是运输失败
// 说明，transportCalled=1，断言全灭。
// 绿（T2+T3）：护栏拒发，transportCalled=0，reason=派发失败、comment 含 main 与路径。
func TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase(t *testing.T) {
	repo := b400CloneWithoutSpec(t)
	env, cardID := b400FlowEnv(t, repo)
	const specPath = "docs/superpowers/specs/b400.md"
	if _, err := env.ledger.AttachFile(cardID, "spec", specPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}

	var transportCalled int32
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, id, node string) {
		runner.Dispatcher.Transport = func(context.Context, ledgerstep.DispatchOpts) (string, string, error) {
			atomic.StoreInt32(&transportCalled, 1)
			return "", "", errors.New("Transport 不应被触达：基线护栏缺失")
		}
		env.srv.runStep(ctx, runner, id, node)
	}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
		`{"step":"impl","actor":"cli:u@h#1"}`)
	if code != 202 {
		t.Fatalf("首派应 202 受理，实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })

	if got := atomic.LoadInt32(&transportCalled); got != 0 {
		t.Fatalf("护栏未在 Transport 前拦下（transportCalled=%d）", got)
	}
	reason, comment, dispatched := b400CardState(t, env, cardID)
	if dispatched {
		t.Fatalf("被拒的首派不得留 dispatched 快照；comment=%s", comment)
	}
	if reason != "派发失败" {
		t.Fatalf("卡应落 needs_human(派发失败)，实得 reason=%q，comment=%s", reason, comment)
	}
	for _, want := range []string{"main", specPath, "card update", "--base-branch"} {
		if !strings.Contains(comment, want) {
			t.Fatalf("拒发文案缺 %q：\n%s", want, comment)
		}
	}
}

// TestB400ProbeReportsMissingAndPresentPaths 覆盖生产探针正/负两态：默认分支树里有
// README.md、没有 spec 路径。
//
// 内部锁声明：入口 Server.probeBaseAttachments 不在 spec 两条缝的入口符号上；缝级断言
// 由同文件 T1 的端到端用例从 HTTP step 进入给出。本用例是「探针本身可读真仓库」的
// 附加边界锁，不顶替任何缝级断言（§10）。
func TestB400ProbeReportsMissingAndPresentPaths(t *testing.T) {
	repo := b400CloneWithoutSpec(t)
	env, _ := b400FlowEnv(t, repo)
	resolved, missing, err := env.srv.probeBaseAttachments(context.Background(), "handoff", "",
		[]string{"README.md", "docs/superpowers/specs/b400.md"})
	if err != nil {
		t.Fatalf("探针: %v", err)
	}
	if resolved != "main" {
		t.Fatalf("解析默认分支 = %q，want main", resolved)
	}
	if len(missing) != 1 || missing[0] != "docs/superpowers/specs/b400.md" {
		t.Fatalf("缺失路径 = %v，want 只有 spec 路径", missing)
	}
}

// TestB400ProbeSkipsWhenProjectNotRegistered：项目在本机无位置 ⇒ ErrBaseProbeUnavailable。
func TestB400ProbeSkipsWhenProjectNotRegistered(t *testing.T) {
	env := newLedgerEnv(t)
	_, _, err := env.srv.probeBaseAttachments(context.Background(), "nowhere", "", []string{"a"})
	if !errors.Is(err, ledgerstep.ErrBaseProbeUnavailable) {
		t.Fatalf("未登记项目应返回 ErrBaseProbeUnavailable，实得 %v", err)
	}
}
