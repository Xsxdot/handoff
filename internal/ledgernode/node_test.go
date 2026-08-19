package ledgernode

import (
	"context"
	"fmt"
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

// 审阅跑不出报文（派发失败、卡在工单上、连接断了）同样要落「需要你」：
// 只把错误抛给调用方等于卡上不留痕——看板会显示一切正常，而实际没人在
// 推它。2026-08-19 真机实测：审阅执行者把裁决塞进提问工单没有完成回合，
// 节点直接报错退出，卡上一片空白。
func TestReviewNodeMarksNeedsHumanWhenReviewFails(t *testing.T) {
	s, card := nodeLedger(t)
	node := &ReviewNode{St: s, Node: "review",
		RunReview: func(ctx context.Context, c ledger.Card) (string, error) {
			return "", fmt.Errorf("事件流中没有 completed/failed 最终报文")
		}}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("不应把错误直接抛出: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("应转等人: %+v", out)
	}
	view, _ := s.GetCard(card.ID)
	_ = view
	events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
	sawNeeds, sawRaw := false, false
	for _, e := range events {
		if e.Type == ledger.EvNeedsHuman {
			sawNeeds = true
		}
		if e.Type == ledger.EvComment && strings.Contains(string(e.Payload), "没有 completed/failed") {
			sawRaw = true
		}
	}
	if !sawNeeds || !sawRaw {
		t.Fatalf("需要你标记=%v 原文落账=%v（两者都要有）", sawNeeds, sawRaw)
	}
}
