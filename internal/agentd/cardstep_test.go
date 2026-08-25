package agentd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

func newStepTestServer(t *testing.T) *Server {
	t.Helper()
	env := newLedgerEnv(t)
	return env.srv
}

func seedCardWithProject(t *testing.T, s *Server, project string) {
	t.Helper()
	seedAgentdLedger(t, s.ledger, "bug")
	if _, err := s.ledger.CreateCard(newCardForStepTest(project)); err != nil {
		t.Fatal(err)
	}
}

func newCardForStepTest(project string) ledger.NewCard {
	return ledger.NewCard{Title: "环节测试卡", Project: project, Workflow: "bug", Actor: "test"}
}

// seedImplementCardWithProject 建一张钉在「带 implement 节点」的工作流上的卡。
//
// 为什么不能沿用 bug 流的种子卡：受理前会校验节点名确实在卡钉住的工作流里，
// 而 implement 这个名字只存在于 charter 流——charter 不是出厂工作流（出厂只有
// feature/domain/bug/triage），测试环境里没有，所以这里当场写一条最小工作流。
//
// 为什么不能把节点名换成 bug 流里现成的：被测属性正是「守卫不再按节点名拒绝
// implement」，换掉名字测的就变成另一条属性了。
func seedImplementCardWithProject(t *testing.T, s *Server, project string) {
	t.Helper()
	if _, err := s.ledger.PutWorkflow("charter", ledger.WorkflowDef{
		Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "implement"},
			{Name: "implement", Next: "review", Dispatch: true, Verdict: true,
				Template: "review-generic", CarryCardContext: true, MaxRounds: 3},
			{Name: "review", Next: ledger.StatusDone, Dispatch: true, Verdict: true,
				Template: "review-generic", CarryCardContext: true, MaxRounds: 3,
				OnFail: "implement"},
			{Name: ledger.StatusDone},
		},
	}); err != nil {
		t.Fatalf("写入带 implement 节点的测试工作流: %v", err)
	}
	if _, err := s.ledger.CreateCard(ledger.NewCard{
		Title: "charter 环节测试卡", Project: project, Workflow: "charter", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func holdCardStep(t *testing.T, s *Server, cardID string) func() {
	t.Helper()
	if _, err := s.ledger.GetCard(cardID); err != nil {
		seedAgentdLedger(t, s.ledger, "bug")
		if _, createErr := s.ledger.CreateCard(newCardForStepTest("demo")); createErr != nil {
			t.Fatalf("准备占位卡失败: %v", createErr)
		}
	}
	if !s.claimCardStep(cardID) {
		t.Fatalf("占用环节槽位失败: %s", cardID)
	}
	return func() { s.releaseCardStep(cardID) }
}

func cardStepInFlight(s *Server, cardID string) bool { return s.cardStepInFlight(cardID) }

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// TestStartCardStepRejectsSecondInFlight 同一张卡同时只允许一个环节在飞。
// 为什么必须拦：两个 merge 环节并发跑同一个仓路径，会在同一个工作区里
// 互相踩 git 状态——而那一侧的失败信息只会是一句莫名其妙的 git 报错。
func TestStartCardStepRejectsSecondInFlight(t *testing.T) {
	s := newStepTestServer(t)
	release := holdCardStep(t, s, "B1")
	defer release()
	if err := s.startCardStep("B1", proto.CardStepReq{Step: "review", Actor: "web:test"}); !errors.Is(err, errStepInFlight) {
		t.Fatalf("第二个环节应被拒，实得 %v", err)
	}
}

// TestStartCardStepReleasesSlotOnFinish 环节跑完要把位子让出来，
// 否则一张卡审一次之后就再也审不了了——而且这个 bug 要等到第二次点才发现。
func TestStartCardStepReleasesSlotOnFinish(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "demo")
	done := make(chan struct{})
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	if err := s.startCardStep("B1", proto.CardStepReq{Step: ledger.StatusDoing, Actor: "web:test"}); err != nil {
		t.Fatalf("首次应放行: %v", err)
	}
	<-done
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
	if err := s.startCardStep("B1", proto.CardStepReq{Step: "review", Actor: "web:test"}); err != nil {
		t.Fatalf("跑完之后应能再发起: %v", err)
	}
}

// TestStartCardStepAssemblesRunHolder 锁住生产装配不能漏传运行身份。
func TestStartCardStepAssemblesRunHolder(t *testing.T) {
	s := newNoPTYLedgerEnv(t).srv
	seedCardWithProject(t, s, "demo")
	ch := make(chan string, 1)
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		ch <- runner.RunHolder
	}
	if err := s.startCardStep("B1", proto.CardStepReq{Step: "review", Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var holder string
	select {
	case holder = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("编排未被启动")
	}
	if holder == "" || !strings.HasPrefix(holder, "run:") || strings.Count(holder, "#") != 2 {
		t.Fatalf("holder 应为 run:<host>#<pid>#<unixnano> 形态: %q", holder)
	}
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
}

// TestRequiresInlineLocalFile keeps the guard tied to request capabilities rather than node names.
func TestRequiresInlineLocalFile(t *testing.T) {
	for _, req := range []proto.CardStepReq{
		{Step: "implement"},
		{Step: "review", Target: "linux-01", Executor: "codex", Model: "gpt-5", Extra: "x", Actor: "cli:u@h#1"},
		{Step: "review", Target: "", Executor: "", Model: "", Extra: "", Actor: ""},
	} {
		if requiresInlineLocalFile(req) {
			t.Fatalf("requiresInlineLocalFile(%+v) = true, want false", req)
		}
	}
}
