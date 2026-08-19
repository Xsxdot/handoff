package ledgernode

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func nodeLedger(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_ = s.EnsureDefaultWorkflows()
	_ = s.EnsureDefaultTemplates()
	c, _ := s.CreateCard(ledger.NewCard{Title: "被审卡", Project: "p", Workflow: "bug", Actor: "t"})
	return s, c
}

func TestReviewNodePassAndFailLoop(t *testing.T) {
	s, c := nodeLedger(t)
	msgs := []string{
		"第一轮\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"x\"}]}\n```",
		"第二轮\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```",
	}
	i := 0
	n := &ReviewNode{St: s, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) {
			message := msgs[i]
			i++
			return message, nil
		},
	}
	out, err := n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionContinue || len(out.Verdict.Findings) != 1 {
		t.Fatalf("round1: %v %+v", err, out)
	}
	out, err = n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("round2: %v %+v", err, out)
	}
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	cnt := 0
	for _, event := range evs {
		if event.Type == ledger.EvReviewVerdict {
			cnt++
		}
	}
	if cnt != 2 {
		t.Fatalf("verdict 事件: %d", cnt)
	}
}

func TestReviewNodeRoundCapAndParseFailure(t *testing.T) {
	s, c := nodeLedger(t)
	failMessage := "```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	n := &ReviewNode{St: s, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) { return failMessage, nil }}
	for i := 0; i < MaxRounds; i++ {
		if out, err := n.RunOnce(context.Background(), c.ID); err != nil || out.Action != ActionContinue {
			t.Fatalf("round%d: %v %+v", i+1, err, out)
		}
	}
	out, err := n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("封顶: %v %+v", err, out)
	}
	views, _ := s.ListCards(ledger.CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || !strings.Contains(views[0].NeedsReason, "超轮") {
		t.Fatalf("等人标记: %+v", views)
	}

	s2, c2 := nodeLedger(t)
	n2 := &ReviewNode{St: s2, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) { return "没有 block 的报文", nil }}
	out, err = n2.RunOnce(context.Background(), c2.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("解析失败处置: %v %+v", err, out)
	}
	evs, _ := s2.EventsFromAsc([]string{c2.ID}, 0, 100)
	rawSaved := false
	for _, event := range evs {
		if event.Type == ledger.EvComment && strings.Contains(string(event.Payload), "没有 block") {
			rawSaved = true
		}
	}
	if !rawSaved {
		t.Fatal("原文未落 timeline")
	}
}

func TestMergeNodeDecision(t *testing.T) {
	s, c := nodeLedger(t)
	m := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error { return nil },
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error {
			t.Fatal("main 不应自动合")
			return nil
		},
	}
	out, err := m.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("main 人工层: %v %+v", err, out)
	}

	cf, _ := s.CreateCard(ledger.NewCard{Title: "热修卡", Project: "p", Workflow: "feature", Actor: "t"})
	_ = s.AttachFile(cf.ID, "spec", "specs/x.md", "t")
	_ = s.SetAcceptance(cf.ID, "测试全绿", "t")
	for _, to := range []string{"已出spec", ledger.StatusDoing, ledger.StatusReview} {
		if err := s.MoveCard(cf.ID, to, "", "t"); err != nil {
			t.Fatalf("铺路 %s: %v", to, err)
		}
	}
	if out, err := m.RunOnce(context.Background(), cf.ID); err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("feature 主线合并: %v %+v", err, out)
	}
	if got, _ := s.GetCard(cf.ID); got.Status != "待合并" {
		t.Fatalf("应已推「待合并」: %s", got.Status)
	}

	c2, _ := s.CreateCard(ledger.NewCard{Title: "集成线卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration", Actor: "t"})
	called := false
	m2 := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error { return nil },
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error {
			called = true
			if base != "integration" {
				t.Fatalf("合并目标: %q", base)
			}
			return nil
		},
	}
	out, err = m2.RunOnce(context.Background(), c2.ID)
	if err != nil || out.Action != ActionMerged || !called {
		t.Fatalf("集成线自动合: %v %+v", err, out)
	}

	c3, _ := s.CreateCard(ledger.NewCard{Title: "红测试卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration", Actor: "t"})
	m3 := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error {
			return context.DeadlineExceeded
		},
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error { t.Fatal("红不应合"); return nil },
	}
	if out, err = m3.RunOnce(context.Background(), c3.ID); err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("客观判据红: %v %+v", err, out)
	}
}
