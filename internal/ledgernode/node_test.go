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

// seedMergeableCard 建一张基线是集成分支的卡。基线非主线是唯一的关键点：
// 基线为空或为 main 时 isMainline 会直接把卡推去「待合并」等人，根本走不到
// Objective/DoMerge，本 task 要验的分支就摸不到。
func seedMergeableCard(t *testing.T, s *ledger.Store) ledger.Card {
	t.Helper()
	c, err := s.CreateCard(ledger.NewCard{
		Title: "待合并卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration/y", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return c
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

// 基线**显式写成 main** 与留空同义：spec「基线就是 main 时该节点不自动合、
// 直接打『待合并』等人」。只判空串会让 `card add --base-branch main` 悄悄
// 绕开主线的人工门，把改动自动合进 main（2026-08-19 真机验收发现）。
func TestMergeNodeTreatsNamedMainAsMainline(t *testing.T) {
	s, _ := nodeLedger(t)
	card, err := s.CreateCard(ledger.NewCard{Title: "顶层热修卡", Project: "p", Workflow: "feature",
		BaseBranch: "main", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// 铺到「待审阅」：合并节点是从这一状态推「待合并」的
	_ = s.AttachFile(card.ID, "spec", "specs/x.md", "t")
	_ = s.SetAcceptance(card.ID, "测试全绿", "t")
	for _, to := range []string{"已出spec", ledger.StatusDoing, ledger.StatusReview} {
		if err := s.MoveCard(card.ID, to, "", "t"); err != nil {
			t.Fatalf("铺路 %s: %v", to, err)
		}
	}
	m := &MergeNode{St: s,
		Objective: func(ctx context.Context, c ledger.Card, base string) error { return nil },
		DoMerge: func(ctx context.Context, c ledger.Card, base string) error {
			t.Fatal("显式 main 不应自动合")
			return nil
		},
	}
	out, err := m.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("显式 main 应转人工: %v %+v", err, out)
	}
	if got, _ := s.GetCard(card.ID); got.Status != "待合并" {
		t.Fatalf("应已推「待合并」: %s", got.Status)
	}
}

// TestMergeNodeDistinguishesMissingWorkBranch 工作分支缺失不能被记成「合并冲突」。
// 人看到「合并冲突」会去查代码冲突，而真实处置是 handoff pull——原因写错等于
// 把人引到错误的排查路径上。
func TestMergeNodeDistinguishesMissingWorkBranch(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	node := &MergeNode{
		St:        st,
		Objective: func(context.Context, ledger.Card, string) error { return nil },
		DoMerge: func(context.Context, ledger.Card, string) error {
			return fmt.Errorf("%w：\n（脚本输出）", ErrWorkBranchMissing)
		},
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce 不该整体报错: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("应转等人，实得 %q", out.Action)
	}
	if !strings.Contains(out.Reason, "工作分支缺失") {
		t.Fatalf("reason 应指明工作分支缺失，实得 %q", out.Reason)
	}
	if !strings.Contains(out.Reason, "handoff pull") {
		t.Fatalf("reason 必须给出可操作的下一步，实得 %q", out.Reason)
	}
}

// TestMergeNodeConflictStillSaysConflict 普通合并失败仍记「合并冲突」，
// 不能被上一条改动带偏。
func TestMergeNodeConflictStillSaysConflict(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	node := &MergeNode{
		St:        st,
		Objective: func(context.Context, ledger.Card, string) error { return nil },
		DoMerge: func(context.Context, ledger.Card, string) error {
			return fmt.Errorf("合并失败:\nCONFLICT (content): foo.go")
		},
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce 不该整体报错: %v", err)
	}
	if out.Reason != "合并冲突" {
		t.Fatalf("普通失败应记合并冲突，实得 %q", out.Reason)
	}
}
