package ledgerstep

import (
	"context"
	"encoding/json"
	"errors"
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
	seedLedgerStepStore(t, s)
	c, _ := s.CreateCard(ledger.NewCard{Title: "被审卡", Project: "p", Workflow: "bug", Actor: "t"})
	return s, c
}

func newReviewReadOnlyStep(t *testing.T, st *ledger.Store, card ledger.Card,
	diff func(context.Context, string, string) ([]string, error)) *NodeStep {
	t.Helper()
	return &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "review-guard", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
			Override: ledger.NodeOverride{Purpose: ledger.PurposeReview},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "target", "task-review-guard", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		Diff: diff,
	}
}

func reviewPassValues(t *testing.T, st *ledger.Store, cardID string) []bool {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatalf("读 review_verdict 事件: %v", err)
	}
	var values []bool
	for _, event := range events {
		if event.Type != ledger.EvReviewVerdict {
			continue
		}
		var payload struct {
			Pass *bool `json:"pass"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("解码 review_verdict: %v", err)
		}
		if payload.Pass == nil {
			t.Fatalf("review_verdict 缺 pass 键: %s", event.Payload)
		}
		values = append(values, *payload.Pass)
	}
	return values
}

func TestNodeStepReviewPurposeAllowsEmptyDiff(t *testing.T) {
	st, card := nodeLedger(t)
	called := false
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		called = true
		return nil, nil
	})
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("空 diff 应 pass: err=%v out=%+v", err, out)
	}
	if !called {
		t.Fatal("purpose=review 且 pass 时必须调用 Diff，即使 Diff 返回空列表")
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || !values[0] {
		t.Fatalf("空 diff 的 review_verdict = %v，want [true]", values)
	}
}

func TestNodeStepReviewPurposeAllowsLedgerPaths(t *testing.T) {
	paths := []string{
		"docs/superpowers/ledgers/foo.md", "docs/ledgers/foo.md",
		"docs/superpowers/ledgers", "docs/ledgers",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			st, card := nodeLedger(t)
			step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
				return []string{path}, nil
			})
			out, err := step.RunOnce(context.Background(), card.ID)
			if err != nil || out.Action != ActionPass {
				t.Fatalf("白名单路径 %q 应 pass: err=%v out=%+v", path, err, out)
			}
			values := reviewPassValues(t, st, card.ID)
			if len(values) != 1 || !values[0] {
				t.Fatalf("白名单路径 %q 的 review_verdict = %v，want [true]", path, values)
			}
		})
	}
}

func TestNodeStepReviewPurposeRejectsOutOfBoundsPaths(t *testing.T) {
	st, card := nodeLedger(t)
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		return []string{"docs/ledgers/allowed.md", "docs/ledgers-extra/bad.md", "internal/old.go", "internal/new.go"}, nil
	})
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionContinue {
		t.Fatalf("越界 diff 应按 on_fail 继续: err=%v out=%+v", err, out)
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读越界 review 卡: %v", err)
	}
	if got.Status != ledger.StatusDoing {
		t.Fatalf("越界 review 应路由到 OnFail 进行中，实际 %q", got.Status)
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || values[0] {
		t.Fatalf("越界 review_verdict 必须只有 pass=false，实际 %v", values)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读越界 review 事件: %v", err)
	}
	commentFound := false
	for _, event := range events {
		if event.Type != ledger.EvComment {
			continue
		}
		var payload struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("解码越界 comment: %v", err)
		}
		if strings.Contains(payload.Body, "docs/ledgers-extra/bad.md") &&
			strings.Contains(payload.Body, "internal/old.go") &&
			strings.Contains(payload.Body, "internal/new.go") {
			commentFound = true
		}
	}
	if !commentFound {
		t.Fatal("越界 review 必须写普通评论并列出每条越界路径")
	}
}

func TestNodeStepReviewPurposeDiffFailureDoesNotRecordVerdict(t *testing.T) {
	cases := []struct {
		name string
		diff func(context.Context, string, string) ([]string, error)
	}{
		{name: "nil diff", diff: nil},
		{name: "diff error", diff: func(context.Context, string, string) ([]string, error) {
			return nil, errors.New("diff backend unavailable")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, card := nodeLedger(t)
			step := newReviewReadOnlyStep(t, st, card, tc.diff)
			beforeEvents, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
			if err != nil {
				t.Fatalf("读 Diff 失败前事件: %v", err)
			}
			beforeRounds := CountRounds(beforeEvents, "review-guard")
			out, err := step.RunOnce(context.Background(), card.ID)
			if err != nil || out.Action != ActionNeedsHuman {
				t.Fatalf("Diff 失败必须 needs_human: err=%v out=%+v", err, out)
			}
			if !strings.Contains(out.Reason, "读取审阅改动失败") {
				t.Fatalf("Reason = %q，缺少审阅 diff 失败语义", out.Reason)
			}
			if values := reviewPassValues(t, st, card.ID); len(values) != 0 {
				t.Fatalf("Diff 失败不得写 review_verdict，实际 %v", values)
			}
			reason, err := st.NeedsOf(card.ID)
			if err != nil {
				t.Fatalf("读 needs_human: %v", err)
			}
			if reason != "读取审阅改动失败" {
				t.Fatalf("needs_human reason = %q", reason)
			}
			got, err := st.GetCard(card.ID)
			if err != nil {
				t.Fatalf("读 Diff 失败卡: %v", err)
			}
			if got.Status != ledger.StatusTodo {
				t.Fatalf("Diff 失败不得路由到 OnFail，status=%q", got.Status)
			}
			afterEvents, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
			if err != nil {
				t.Fatalf("读 Diff 失败后事件: %v", err)
			}
			if afterRounds := CountRounds(afterEvents, "review-guard"); afterRounds != beforeRounds {
				t.Fatalf("Diff 失败不得增加裁决轮次: before=%d after=%d", beforeRounds, afterRounds)
			}
		})
	}
}

func TestNodeStepNameReviewWithoutPurposeKeepsLegacyNoDiff(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St:   st,
		Node: ledger.NodeDef{Name: "review", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "target", "legacy-review-task", nil
		},
		Await: func(context.Context, string, string) (string, error) { return nodePassMessage(), nil },
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("Name=review 且 purpose 空的存量行为必须 pass: err=%v out=%+v", err, out)
	}
}

func TestNodeStepImplementPurposeDoesNotRunReviewGate(t *testing.T) {
	st, card := nodeLedger(t)
	called := false
	step := newReviewReadOnlyStep(t, st, card, func(context.Context, string, string) ([]string, error) {
		called = true
		return []string{"internal/production.go"}, nil
	})
	step.Node.Override.Purpose = ledger.PurposeImplement
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("purpose=implement 不应触发 review 闸: err=%v out=%+v", err, out)
	}
	if called {
		t.Fatal("purpose=implement 不应调用 Diff")
	}
	values := reviewPassValues(t, st, card.ID)
	if len(values) != 1 || !values[0] {
		t.Fatalf("purpose=implement 的裁决应保持 pass=true，实际 %v", values)
	}
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

func TestNodeStepCommentsWhenSalvageDropsNotes(t *testing.T) {
	st, card := nodeLedger(t)
	fence := strings.Repeat(string(rune(96)), 3)
	message := fence + "handoff-verdict\n" +
		"{\"verdict\":\"pass\",\"findings\":[],\"notes\":\"enabled\":true}\n" +
		fence + "\n"
	step := newNodeStep(t, st, ledger.NodeDef{
		Name: "review", Dispatch: true, Verdict: true, Template: "review-generic",
	}, message, nil)
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if out.Action != ActionPass || !out.Verdict.Pass {
		t.Fatalf("outcome = %+v", out)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读取事件: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == ledger.EvComment && strings.Contains(string(event.Payload), "notes") &&
			strings.Contains(string(event.Payload), "抢救") {
			found = true
		}
	}
	if !found {
		t.Fatal("抢救丢弃 notes 没有普通评论留痕")
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

func nodePassMessage() string {
	fence := string([]byte{96, 96, 96})
	return fence + "handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n" + fence
}

// TestNodeStepPublishesWorkBranchBeforeFinishTask 锁 B341：pass 发布 origin
// 必须发生在归档 task 之前，否则 managed worktree 已被 Done 回收。
func TestNodeStepPublishesWorkBranchBeforeFinishTask(t *testing.T) {
	st, card := nodeLedger(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", Target: "mac-02", TaskID: "task-b341",
		Branch: "cards/B341-charter", Purpose: "implement", Actor: "tester",
	}); err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}
	var order []string
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "implement", Dispatch: true, Verdict: true, Template: "feature-impl",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "mac-02", "task-b341", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			order = append(order, "await")
			return nodePassMessage(), nil
		},
		PublishWorkBranch: func(context.Context, string, string, string) error {
			order = append(order, "publish")
			return nil
		},
		FinishTask: func(context.Context, string, string) error {
			order = append(order, "done")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionPass && out.Action != ActionNeedsHuman {
		t.Fatalf("unexpected action %+v", out)
	}
	want := []string{"await", "publish", "done"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("顺序 = %v，want %v（publish 必须在 done 前）", order, want)
	}
}

// TestNodeStepSkipsPublishWhenWorkBranchAlreadyOnOrigin 锁 B346：implement
// 已经 RecordWorkBranchPublished 后，review pass 不得再对已归档 worktree push。
func TestNodeStepSkipsPublishWhenWorkBranchAlreadyOnOrigin(t *testing.T) {
	st, card := nodeLedger(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", Target: "linux-01", TaskID: "task-implement",
		Branch: "cards/B346-charter", Purpose: "implement", Actor: "tester",
	}); err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}
	if err := st.RecordWorkBranchPublished(card.ID, "cards/B346-charter", "linux-01", "task-implement", "node:implement"); err != nil {
		t.Fatalf("RecordWorkBranchPublished: %v", err)
	}
	var order []string
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "review", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "mac-02", "task-review", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			order = append(order, "await")
			return nodePassMessage(), nil
		},
		PublishWorkBranch: func(context.Context, string, string, string) error {
			order = append(order, "publish")
			return errors.New("不该再 push 已归档的 implement worktree")
		},
		FinishTask: func(context.Context, string, string) error {
			order = append(order, "done")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionPass {
		t.Fatalf("Action = %q，want pass（已发布不得 needs_human）", out.Action)
	}
	want := []string{"await", "done"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("顺序 = %v，want %v（已发布必须跳过 publish）", order, want)
	}
}

func TestNodeStepAttachesDeclaredOutputAndRoutes(t *testing.T) {
	st, card := nodeLedger(t)
	attached := 0
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "breakdown", Dispatch: true, Verdict: true, Template: "review-generic",
			Next:     ledger.StatusReview,
			Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b201-breakdown.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-output", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { return "docs/b201-breakdown.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/b201-breakdown.md"}, nil
		},
		Attach: func(cardID, kind, path, actor string) error {
			attached++
			if cardID != card.ID || kind != "doc" || path != "docs/b201-breakdown.md" || actor != "node:breakdown" {
				t.Fatalf("Attach 参数错误: %q %q %q %q", cardID, kind, path, actor)
			}
			_, err := st.AttachFile(cardID, kind, path, actor)
			return err
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("pass 输出: %v %+v", err, out)
	}
	if attached != 1 {
		t.Fatalf("Attach 次数 = %d, want 1", attached)
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.Status != ledger.StatusReview {
		t.Fatalf("挂附件后未路由，status=%q", got.Status)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Kind != "doc" || got.Attachments[0].Path != "docs/b201-breakdown.md" {
		t.Fatalf("附件未落账: %+v", got.Attachments)
	}
}

func TestNodeStepDatePrefixedDeclaredOutputMarksNeedsHuman(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "breakdown", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
			Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b249-breakdown.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-date-prefixed", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { return "docs/b249-breakdown.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/2026-08-25-b249-breakdown.md"}, nil
		},
		Attach: func(string, string, string, string) error {
			t.Fatal("日期前缀产出不应挂附件")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("日期前缀产出执行出错: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
	assertHaltOnCard(t, st, card.ID, "请改名为：docs/b249-breakdown.md")
	assertHaltOnCard(t, st, card.ID, "docs/2026-08-25-b249-breakdown.md")
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.Status == ledger.StatusDoing {
		t.Fatalf("日期前缀错误不得路由到 on_fail: status=%q", got.Status)
	}
}

func TestNodeStepExactDeclaredOutputWinsOverDatePrefixedPath(t *testing.T) {
	st, card := nodeLedger(t)
	attached := false
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "breakdown", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
			Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b249-breakdown.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-exact-date", nil
		},
		Await:      func(context.Context, string, string) (string, error) { return nodePassMessage(), nil },
		OutputPath: func() string { return "docs/b249-breakdown.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/b249-breakdown.md", "docs/2026-08-25-b249-breakdown.md"}, nil
		},
		Attach: func(cardID, kind, path, actor string) error {
			attached = true
			_, err := st.AttachFile(cardID, kind, path, actor)
			return err
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("精确路径同时存在时应通过: err=%v out=%+v", err, out)
	}
	if !attached {
		t.Fatal("精确路径通过时应挂法定附件")
	}
}

func TestNodeStepUnrelatedDatePrefixedOutputRemainsOrdinaryMissing(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "breakdown", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
			Produces: &ledger.NodeOutput{Kind: "doc", Path: "docs/b249-breakdown.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-unrelated-date", nil
		},
		Await:      func(context.Context, string, string) (string, error) { return nodePassMessage(), nil },
		OutputPath: func() string { return "docs/b249-breakdown.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/2026-08-25-other.md"}, nil
		},
		Attach: func(string, string, string, string) error {
			t.Fatal("普通缺失不应挂附件")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("无关日期文件应普通等人: err=%v out=%+v", err, out)
	}
	assertHaltOnCard(t, st, card.ID, "本轮实际改动文件")
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvComment && strings.Contains(string(event.Payload), "请改名为：") {
			t.Fatal("无关日期文件不应出现改名提示")
		}
	}
}

func TestNodeStepMissingDeclaredOutputMarksNeedsHumanWithDiffList(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Next:     ledger.StatusReview,
			Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-missing-output", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { return "docs/b201-plan.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/other.md", "internal/changed.go"}, nil
		},
		Attach: func(string, string, string, string) error {
			t.Fatal("缺产物时不应 Attach")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("缺产物输出: %v %+v", err, out)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == ledger.EvComment &&
			strings.Contains(string(event.Payload), "docs/b201-plan.md") &&
			strings.Contains(string(event.Payload), "docs/other.md") &&
			strings.Contains(string(event.Payload), "internal/changed.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("缺产物 comment 未同时写法定路径与实际改动清单")
	}
}

func TestNodeStepAttachFailureWarnsButStillRoutes(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Next:     ledger.StatusReview,
			Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-attach-error", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { return "docs/b201-plan.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/b201-plan.md"}, nil
		},
		Attach: func(string, string, string, string) error {
			return fmt.Errorf("sqlite 写入失败")
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("挂载失败仍应 pass 并路由: %v %+v", err, out)
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.Status != ledger.StatusReview {
		t.Fatalf("挂载失败不应阻断路由: %q", got.Status)
	}
}

func TestNodeStepWithoutProducesDoesNotInvokeOutputHooks(t *testing.T) {
	st, card := nodeLedger(t)
	called := 0
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview,
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-legacy", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { called++; t.Fatal("legacy 节点不应取输出路径"); return "" },
		Diff: func(context.Context, string, string) ([]string, error) {
			called++
			t.Fatal("legacy 节点不应取 diff")
			return nil, nil
		},
		Attach: func(string, string, string, string) error {
			called++
			t.Fatal("legacy 节点不应挂附件")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil || out.Action != ActionPass || called != 0 {
		t.Fatalf("legacy 节点行为变化: %v %+v hooks=%d", err, out, called)
	}
}

func TestNodeStepRerunSameOutputPathIsIdempotent(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-rerun", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		OutputPath: func() string { return "docs/b201-plan.md" },
		Diff: func(context.Context, string, string) ([]string, error) {
			return []string{"docs/b201-plan.md"}, nil
		},
		Attach: func(cardID, kind, path, actor string) error {
			_, err := st.AttachFile(cardID, kind, path, actor)
			return err
		},
	}
	for i := 0; i < 2; i++ {
		out, err := step.RunOnce(context.Background(), card.ID)
		if err != nil || out.Action != ActionPass {
			t.Fatalf("第 %d 次重跑: %v %+v", i+1, err, out)
		}
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("同 path 重跑应幂等，附件=%+v", got.Attachments)
	}
}

// 裁决未过时产出物校验必须整段跳过：否则失败轮会因为"没有法定产出物"被判成
// 等人，OnFail 的重试回路（3 轮封顶）对所有声明产出的节点就此失效。
func TestNodeStepFailedVerdictSkipsOutputVerification(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Next: ledger.StatusReview, OnFail: "进行中",
			Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-failed-verdict", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return "报告\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```", nil
		},
		OutputPath: func() string {
			t.Fatal("裁决未过时不应渲染产出路径")
			return ""
		},
		Diff: func(context.Context, string, string) ([]string, error) {
			t.Fatal("裁决未过时不应读改动清单")
			return nil, nil
		},
		Attach: func(string, string, string, string) error {
			t.Fatal("裁决未过时不应挂产出物")
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("失败轮执行出错: %v", err)
	}
	if out.Action != ActionContinue {
		t.Fatalf("Action = %q, want %q（失败轮应走 OnFail 而非等人）", out.Action, ActionContinue)
	}
	got, _ := st.GetCard(card.ID)
	if got.Status != "进行中" {
		t.Fatalf("失败轮应退到 OnFail «进行中»，实际 %q", got.Status)
	}
}

// TestNodeStepLeftoverDecisionHaltsForHuman 终态遗留裁决补解析（契约 §3.7
// 正断言）：卡已到终态但挂着 open 裁决（协调者死亡窗口产物）时，RunOnce 入口
// 必须转等人、reason 以「终态遗留裁决」开头，绝不允许继续派发，绝不伪造
// decision_answered（答案是用户的事实，系统不代答）。负断言（全路径事件流无
// decision_answered）单独成立时是稳定假绿——它不能证明补解析跑过；正断言
// 才是牙，两条一起锁。
func TestNodeStepLeftoverDecisionHaltsForHuman(t *testing.T) {
	s, c := nodeLedger(t)
	cardID := c.ID
	if _, err := s.OpenDecision(cardID, "终态遗留请示", []string{"选项A", "选项B"}, "step:review"); err != nil {
		t.Fatalf("开裁决: %v", err)
	}
	if err := s.MoveCard(cardID, ledger.StatusDone, "", "test"); err != nil {
		t.Fatalf("移到已完成: %v", err)
	}

	dispatched := false
	step := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-1", nil
		},
		Await: func(context.Context, string, string) (string, error) { return "", nil },
	}
	out, err := step.RunOnce(context.Background(), cardID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("正断言：应转等人，实得 %q", out.Action)
	}
	if !strings.HasPrefix(out.Reason, "终态遗留裁决") {
		t.Fatalf("正断言：reason 应以「终态遗留裁决」开头，实得 %q", out.Reason)
	}
	if dispatched {
		t.Fatal("终态遗留裁决必须在派发之前拦下：绝不允许继续派发")
	}

	events, err := s.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	commentCount, answeredCount := 0, 0
	var commentBody string
	for _, ev := range events {
		switch ev.Type {
		case ledger.EvComment:
			commentCount++
			commentBody = string(ev.Payload)
		case ledger.EvDecisionAnswered:
			answeredCount++
		}
	}
	if commentCount != 1 || !strings.Contains(commentBody, "终态遗留裁决") {
		t.Fatalf("应恰一条含「终态遗留裁决」的说明评论，实得 %d 条 body=%q", commentCount, commentBody)
	}
	if answeredCount != 0 {
		t.Fatalf("负断言：全路径事件流不得出现 decision_answered，实得 %d", answeredCount)
	}
}

// TestNodeStepLeftoverDecisionHaltIsIdempotent 幂等判据（契约 §4 补解析第 2
// 条）：重复驱动同一张卡不产生第二条说明评论（EnsureComment dedupe_key 生效）。
// 变异靶：实现把 EnsureComment 换回 AddComment → 本测试「恰 1 条评论」翻红。
// dedupeKey 必须是卡级且不带时间戳/节点名，否则同卡重驱动会换键而漏掉第二条。
func TestNodeStepLeftoverDecisionHaltIsIdempotent(t *testing.T) {
	s, c := nodeLedger(t)
	cardID := c.ID
	if _, err := s.OpenDecision(cardID, "终态遗留请示", []string{"选项A"}, "step:review"); err != nil {
		t.Fatalf("开裁决: %v", err)
	}
	if err := s.MoveCard(cardID, ledger.StatusDone, "", "test"); err != nil {
		t.Fatalf("移到已完成: %v", err)
	}
	step := newNodeStep(t, s, ledger.NodeDef{Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic"}, "", nil)
	for i := 0; i < 2; i++ {
		out, err := step.RunOnce(context.Background(), cardID)
		if err != nil || out.Action != ActionNeedsHuman {
			t.Fatalf("第 %d 次驱动: %v %+v", i+1, err, out)
		}
	}
	events, err := s.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	commentCount, answeredCount := 0, 0
	for _, ev := range events {
		switch ev.Type {
		case ledger.EvComment:
			commentCount++
		case ledger.EvDecisionAnswered:
			answeredCount++
		}
	}
	if commentCount != 1 {
		t.Fatalf("重复驱动两次后应仍恰 1 条说明评论（EnsureComment dedupe_key 生效），实得 %d", commentCount)
	}
	if answeredCount != 0 {
		t.Fatalf("负断言：重复驱动路径也不得出现 decision_answered，实得 %d", answeredCount)
	}
}

// TestNodeStepLeftoverDecisionOnTerminatedCard 中间态的另一半：Status=终止
// （CloseCard 进入）同样触发补解析转等人，reason 前缀一致。
func TestNodeStepLeftoverDecisionOnTerminatedCard(t *testing.T) {
	s, c := nodeLedger(t)
	cardID := c.ID
	if _, err := s.OpenDecision(cardID, "终止前遗留", []string{"选项A"}, "step:review"); err != nil {
		t.Fatalf("开裁决: %v", err)
	}
	if err := s.CloseCard(cardID, ledger.CloseAbandoned, "test"); err != nil {
		t.Fatalf("终止: %v", err)
	}
	step := newNodeStep(t, s, ledger.NodeDef{Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic"}, "", nil)
	out, err := step.RunOnce(context.Background(), cardID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("终止卡: %v %+v", err, out)
	}
	if !strings.HasPrefix(out.Reason, "终态遗留裁决") {
		t.Fatalf("reason 前缀: %q", out.Reason)
	}
}

// 声明了产出物却没装配校验钩子时必须显式转等人：静默跳过等于把"节点产出物挂卡"
// 悄悄降级成不校验，而 nil 钩子直接调用会 panic 掉整个环节。
func TestNodeStepProducesWithoutHooksHaltsForHuman(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "plan", Dispatch: true, Verdict: true, Template: "review-generic",
			Next:     ledger.StatusReview,
			Produces: &ledger.NodeOutput{Kind: "plan", Path: "docs/b201-plan.md"},
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "linux-01", "task-unwired", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		// OutputPath / Diff / Attach 三者故意留空
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("未装配钩子时执行出错: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("Action = %q, want %q", out.Action, ActionNeedsHuman)
	}
	if !strings.Contains(out.Reason, "产出物校验未装配") {
		t.Fatalf("Reason = %q，未点明是校验依赖缺失", out.Reason)
	}
	got, _ := st.GetCard(card.ID)
	if got.Status == ledger.StatusReview {
		t.Fatal("未装配校验时不得放行到下一列")
	}
}

// TestNodeStepLeftoverDecisionIgnoresOpenDecisionOnOtherCard 跨卡隔离（契约
// §3.7 中间态判据 + breakdown C3 ④「open 判定复用 ListDecisions(openOnly)
// 按卡过滤」）：open 裁决开在 B 卡上、A 卡一条都没有，驱动 A 的 RunOnce 时
// B 的裁决必须拦不住 A——A 不转等人、不产生「终态遗留裁决」说明评论，正常派发。
// 变异靶：把 node.go 过滤循环里的 `d.CardID == cardID` 整段去掉（任何卡的 open
// 裁决都能拦下本卡）→ 本测试「A 正常派发 / 无 needs_human / 无终态遗留评论」
// 翻红。协调者验收变异实测该过滤从未被测试行使过——本测试就是它的牙。
func TestNodeStepLeftoverDecisionIgnoresOpenDecisionOnOtherCard(t *testing.T) {
	s, cA := nodeLedger(t)
	cardA := cA.ID
	cardB, err := s.CreateCard(ledger.NewCard{Title: "B 卡", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("建 B 卡: %v", err)
	}
	if _, err := s.OpenDecision(cardB.ID, "B 卡的 open 裁决", []string{"选项A"}, "step:review"); err != nil {
		t.Fatalf("在 B 卡上开裁决: %v", err)
	}
	if err := s.MoveCard(cardA, ledger.StatusDone, "", "test"); err != nil {
		t.Fatalf("把 A 移到已完成: %v", err)
	}

	dispatched := false
	step := &NodeStep{
		St:   s,
		Node: ledger.NodeDef{Name: "待审阅", Dispatch: true, Template: "review-generic"},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			dispatched = true
			return "linux-01", "task-1", nil
		},
		Await: func(context.Context, string, string) (string, error) { return "", nil },
	}
	out, err := step.RunOnce(context.Background(), cardA)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionDispatched {
		t.Fatalf("跨卡隔离：A 卡无 open 裁决，B 卡的裁决不得拦下 A——应正常派发，实得 action=%q reason=%q", out.Action, out.Reason)
	}
	if !dispatched {
		t.Fatal("跨卡隔离：B 卡的裁决不得拦下 A 的派发")
	}

	events, err := s.EventsFromAsc([]string{cardA}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	commentCount, needsCount := 0, 0
	var commentBody string
	for _, ev := range events {
		switch ev.Type {
		case ledger.EvComment:
			commentCount++
			commentBody = string(ev.Payload)
		case ledger.EvNeedsHuman:
			needsCount++
		}
	}
	if needsCount != 0 {
		t.Fatalf("跨卡隔离：A 不得因 B 的裁决转等人，实得 %d 条 needs_human", needsCount)
	}
	if commentCount != 0 || strings.Contains(commentBody, "终态遗留裁决") {
		t.Fatalf("跨卡隔离：A 不得产生「终态遗留裁决」评论，实得 %d 条 body=%q", commentCount, commentBody)
	}
}
