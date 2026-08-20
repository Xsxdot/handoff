package ledgerstep

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

func TestReviewStepPassAndFailLoop(t *testing.T) {
	s, c := nodeLedger(t)
	msgs := []string{
		"第一轮\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"x\"}]}\n```",
		"第二轮\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```",
	}
	i := 0
	runReview := func(ctx context.Context, card ledger.Card) (string, error) {
		message := msgs[i]
		i++
		return message, nil
	}
	n := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return runReview(ctx, c)
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

func TestReviewStepRoundCapAndParseFailure(t *testing.T) {
	s, c := nodeLedger(t)
	failMessage := "```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	n := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return failMessage, nil },
	}
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
	n2 := &NodeStep{
		St:   s2,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return "没有 block 的报文", nil },
	}
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

func TestMergeStepDecision(t *testing.T) {
	s, c := nodeLedger(t)
	m := &MergeStep{St: s,
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
	m2 := &MergeStep{St: s,
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
	m3 := &MergeStep{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error {
			return context.DeadlineExceeded
		},
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error { t.Fatal("红不应合"); return nil },
	}
	if out, err = m3.RunOnce(context.Background(), c3.ID); err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("客观判据红: %v %+v", err, out)
	}
}

// 审阅环节跑不出报文（派发失败、卡在工单上、连接断了）同样要落「需要你」：
// 只把错误抛给调用方等于卡上不留痕——看板会显示一切正常，而实际没人在
// 推它。2026-08-19 真机实测：审阅执行者把裁决塞进提问工单没有完成回合，
// 节点直接报错退出，卡上一片空白。
func TestReviewStepMarksNeedsHumanWhenReviewFails(t *testing.T) {
	s, card := nodeLedger(t)
	node := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return "", fmt.Errorf("事件流中没有 completed/failed 最终报文")
		},
	}
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

// 基线**显式写成 main** 与留空同义：spec「基线就是 main 时该环节不自动合、
// 直接打『待合并』等人」。只判空串会让 `card add --base-branch main` 悄悄
// 绕开主线的人工门，把改动自动合进 main（2026-08-19 真机验收发现）。
func TestMergeStepTreatsNamedMainAsMainline(t *testing.T) {
	s, _ := nodeLedger(t)
	card, err := s.CreateCard(ledger.NewCard{Title: "顶层热修卡", Project: "p", Workflow: "feature",
		BaseBranch: "main", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	// 铺到「待审阅」：合并环节是从这一状态推「待合并」的
	_ = s.AttachFile(card.ID, "spec", "specs/x.md", "t")
	_ = s.SetAcceptance(card.ID, "测试全绿", "t")
	for _, to := range []string{"已出spec", ledger.StatusDoing, ledger.StatusReview} {
		if err := s.MoveCard(card.ID, to, "", "t"); err != nil {
			t.Fatalf("铺路 %s: %v", to, err)
		}
	}
	m := &MergeStep{St: s,
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

// TestMergeStepDistinguishesMissingWorkBranchAtObjective 工作分支缺失通常先在
// 客观判据阶段暴露，不能被记成「合并判据未过」。真实链路在这里就会停止，DoMerge
// 不应被调用；人需要的是 handoff pull，而不是重新检查测试。
func TestMergeStepDistinguishesMissingWorkBranchAtObjective(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	node := &MergeStep{
		St: st,
		Objective: func(context.Context, ledger.Card, string) error {
			return fmt.Errorf("%w：\n（脚本输出）", ErrWorkBranchMissing)
		},
		DoMerge: func(context.Context, ledger.Card, string) error {
			t.Fatal("客观判据失败后不应调用 DoMerge")
			return nil
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

// TestMergeStepDistinguishesMissingWorkBranch 验证 DoMerge 分支的工作分支缺失归类。
// 这里故意把 Objective 打桩为成功，守的是 DoMerge 侧；真实链路通常先在 Objective
// 失败，上一条用例单独守住那条路径。两条测试各自覆盖一处归类，不能合并。
func TestMergeStepDistinguishesMissingWorkBranch(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	node := &MergeStep{
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

// TestMergeStepConflictStillSaysConflict 普通合并失败仍记「合并冲突」，
// 不能被上一条改动带偏。
func TestMergeStepConflictStillSaysConflict(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	node := &MergeStep{
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

// TestMergeStepSuccessRecordsBranchMerged 合并成功后必须留下外部推送事件，
// 否则 timeline 无法回答这次自动化到底把什么推到了 origin。
func TestMergeStepSuccessRecordsBranchMerged(t *testing.T) {
	st, _ := nodeLedger(t)
	card := seedMergeableCard(t, st)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Branch: "feat/x", Purpose: ledger.PurposeImplement, Actor: "node:dispatch",
	}); err != nil {
		t.Fatalf("记工作分支: %v", err)
	}
	node := &MergeStep{
		St:        st,
		Objective: func(context.Context, ledger.Card, string) error { return nil },
		DoMerge:   func(context.Context, ledger.Card, string) error { return nil },
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionMerged {
		t.Fatalf("合并成功: %v %+v", err, out)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvBranchMerged {
			return
		}
	}
	t.Fatalf("成功路径未落 %s 事件", ledger.EvBranchMerged)
}

// TestReviewStepRetractsOwnStaleNeedsHuman 复现 2026-08-20 真机看到的形态：
// 第一轮审阅没取到报文 → 打「需要你」；第二轮真跑出裁决 → 那面红旗必须落下。
// 不落的话看板会一直把这张卡算进「需要你」，而没有任何一处能撤它。
func TestReviewStepRetractsOwnStaleNeedsHuman(t *testing.T) {
	s, card := nodeLedger(t)
	round := 0
	runReview := func(ctx context.Context, c ledger.Card) (string, error) {
		round++
		if round == 1 {
			return "", fmt.Errorf("派发审阅: 基线提交在任务仓库中不存在")
		}
		return "```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"验收项全部未实现\"}]}\n```", nil
	}
	node := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return runReview(ctx, card)
		},
	}

	first, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("第一轮: %v", err)
	}
	if first.Action != ActionNeedsHuman {
		t.Fatalf("第一轮应转等人: %+v", first)
	}
	if reason, _ := s.NeedsOf(card.ID); reason == "" {
		t.Fatal("第一轮没打上等人标记，后面验不了撤回")
	}

	// 第二轮出裁决（fail 也算跑通——裁决拿到了，只是判不过）。
	second, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("第二轮: %v", err)
	}
	if second.Action != ActionContinue {
		t.Fatalf("第二轮应打回实现: %+v", second)
	}
	if reason, _ := s.NeedsOf(card.ID); reason != "" {
		t.Fatalf("裁决已出，第一轮的等人标记仍挂着: %q", reason)
	}
}

// TestReviewStepKeepsHumanNeedsHuman 人打的「先别动这张卡」不因环节跑成一轮
// 而被抹掉——撤回权只属于打标记的那一方。
func TestReviewStepKeepsHumanNeedsHuman(t *testing.T) {
	s, card := nodeLedger(t)
	if err := s.MarkNeedsHuman(card.ID, "先别动这张卡，等我确认需求", "cli:human"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	node := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(ctx context.Context, c ledger.Card, node ledger.NodeDef) (string, string, error) {
			return "t", "task", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return "```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```", nil
		},
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionPass {
		t.Fatalf("应过: %+v", out)
	}
	if reason, _ := s.NeedsOf(card.ID); reason != "先别动这张卡，等我确认需求" {
		t.Fatalf("人打的等人标记被环节抹掉了: %q", reason)
	}
}

// newNodeStep 组一个注入了假 Dispatch/Await 的 NodeStep。
func newNodeStep(t *testing.T, st *ledger.Store, node ledger.NodeDef, message string, dispatchErr error) *NodeStep {
	t.Helper()
	return &NodeStep{
		St:   st,
		Node: node,
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			if dispatchErr != nil {
				return "", "", dispatchErr
			}
			return "linux-01", "task-1", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			return message, nil
		},
	}
}

func TestNodeStepRejectsManualColumn(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{Name: "待办"}, "", nil)
	if _, err := step.RunOnce(context.Background(), card); err == nil {
		t.Fatalf("纯人工列没有可执行能力，应报错")
	}
}

func TestNodeStepDispatchOnlyReturnsDispatched(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "进行中", Dispatch: true, Template: "feature-impl",
	}, "", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("派发型节点执行失败: %v", err)
	}
	if out.Action != ActionDispatched {
		t.Fatalf("Action = %q, want %q", out.Action, ActionDispatched)
	}
}

func TestNodeStepVerdictRoutesOnPass(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		Next: "已完成", OnFail: "进行中",
	}, "报告\n```handoff-verdict\n{\"verdict\":\"pass\"}\n```", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("裁决节点执行失败: %v", err)
	}
	if out.Action != ActionPass {
		t.Fatalf("Action = %q, want %q", out.Action, ActionPass)
	}
	got, err := st.GetCard(card)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.Status != "已完成" {
		t.Fatalf("通过后应移到 Next «已完成»，实际 %q", got.Status)
	}
}

func TestNodeStepVerdictRoutesOnFail(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		Next: "已完成", OnFail: "进行中",
	}, "报告\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```", nil)
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("裁决节点执行失败: %v", err)
	}
	if out.Action != ActionContinue {
		t.Fatalf("Action = %q, want %q", out.Action, ActionContinue)
	}
	got, _ := st.GetCard(card)
	if got.Status != "进行中" {
		t.Fatalf("未过应退到 OnFail «进行中»，实际 %q", got.Status)
	}
}

func TestNodeStepHumanBaseSkipsDispatch(t *testing.T) {
	st, _ := nodeLedger(t)
	mainCard, err := st.CreateCard(ledger.NewCard{
		Title: "主线卡", Project: "p", Workflow: "bug", BaseBranch: "main", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	card := mainCard.ID
	dispatched := false
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "收尾合并", Dispatch: true, Verdict: true,
			Template: "review-generic", HumanBases: []string{"main"},
		},
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-1", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return "", nil },
	}
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if dispatched {
		t.Fatalf("基线在 HumanBases 里，绝不允许派发")
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
	views, err := st.ListCards(ledger.CardFilter{IncludeTerminal: true})
	if err != nil {
		t.Fatalf("列卡: %v", err)
	}
	marked := false
	for _, view := range views {
		if view.ID == card && view.NeedsReason != "" {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("应打上等人标记")
	}
}

func TestNodeStepMaxRoundsFromNode(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	node := ledger.NodeDef{
		Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
		MaxRounds: 1, Next: "已完成", OnFail: "进行中",
	}
	fail := "报告\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	// 第一轮：正常跑，落一条 review_verdict
	if _, err := newNodeStep(t, st, node, fail, nil).RunOnce(context.Background(), card); err != nil {
		t.Fatalf("第一轮失败: %v", err)
	}
	// 第二轮：MaxRounds=1 已到顶，应直接转等人且不再派发
	dispatched := false
	step := &NodeStep{
		St: st, Node: node,
		Dispatch: func(ctx context.Context, c ledger.Card, n ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-2", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) { return fail, nil },
	}
	out, err := step.RunOnce(context.Background(), card)
	if err != nil {
		t.Fatalf("第二轮失败: %v", err)
	}
	if dispatched {
		t.Fatalf("已到轮次上限，不该再派发")
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
}
