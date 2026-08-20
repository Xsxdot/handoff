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
	if _, err := s.ledger.CreateCard(newCardForStepTest(project)); err != nil {
		t.Fatal(err)
	}
	if project == "demo" {
		if err := s.st.CreateProjectLocation(&proto.ProjectLocation{
			ProjectID: "demo-project", Name: project, Path: t.TempDir(),
			OriginURL: "git@example.com:demo.git", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func newCardForStepTest(project string) ledger.NewCard {
	return ledger.NewCard{Title: "环节测试卡", Project: project, Workflow: "bug", Actor: "test"}
}

func holdCardStep(t *testing.T, s *Server, cardID string) func() {
	t.Helper()
	if _, err := s.ledger.GetCard(cardID); err != nil {
		if _, createErr := s.ledger.CreateCard(newCardForStepTest("demo")); createErr != nil {
			t.Fatalf("准备占位卡失败: %v", createErr)
		}
	}
	if _, err := s.st.GetProjectLocationByName("demo"); err != nil {
		if createErr := s.st.CreateProjectLocation(&proto.ProjectLocation{
			ProjectID: "demo-project", Name: "demo", Path: t.TempDir(),
			OriginURL: "git@example.com:demo.git", CreatedAt: time.Now().UTC(),
		}); createErr != nil {
			t.Fatalf("准备占位项目失败: %v", createErr)
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
	if err := s.startCardStep("B1", "review", "web:test"); !errors.Is(err, errStepInFlight) {
		t.Fatalf("第二个环节应被拒，实得 %v", err)
	}
}

// TestStartCardStepUnknownProjectRefuses 项目没在本机登记就拒绝，不猜路径。
// 猜错的代价：merge 环节会往错误的仓库 push——外部可见且不易撤回。
func TestStartCardStepUnknownProjectRefuses(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "从未登记的项目")
	err := s.startCardStep("B1", "merge", "web:test")
	if err == nil {
		t.Fatal("未登记项目应被拒")
	}
	if !strings.Contains(err.Error(), "未在本机登记") || !strings.Contains(err.Error(), "从未登记的项目") {
		t.Fatalf("错误要说清是哪个项目、该怎么办：%v", err)
	}
}

// TestStartCardStepBadStepRefuses 只认 review|merge。
func TestStartCardStepBadStepRefuses(t *testing.T) {
	s := newStepTestServer(t)
	seedCardWithProject(t, s, "demo")
	if err := s.startCardStep("B1", "implement", "web:test"); err == nil {
		t.Fatal("implement 不是环节，应被拒")
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
	if err := s.startCardStep("B1", "review", "web:test"); err != nil {
		t.Fatalf("首次应放行: %v", err)
	}
	<-done
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
	if err := s.startCardStep("B1", "review", "web:test"); err != nil {
		t.Fatalf("跑完之后应能再发起: %v", err)
	}
}
