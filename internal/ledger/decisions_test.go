package ledger

import (
	"errors"
	"testing"
)

func TestDecisionLifecycle(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "有请示的卡")

	decision, err := s.OpenDecision(card.ID, "合并顺序按 done 还是按依赖？", []string{"done 时序", "依赖序"}, "main-session")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if decision.ID == 0 || decision.Status != "open" {
		t.Fatalf("open 返回: %+v", decision)
	}
	// 项目级裁决（card_id 空）
	projectDecision, err := s.OpenDecision("", "推不推汇流线？", nil, "main-session")
	if err != nil {
		t.Fatalf("open project-level: %v", err)
	}

	// open 裁决使卡进「需要你」面（派生联动）
	views, _ := s.ListCards(CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || views[0].ID != card.ID || views[0].OpenDecisions != 1 {
		t.Fatalf("needs 联动: %+v", views)
	}

	// 答复
	if err := s.AnswerDecision(decision.ID, "done 时序", "user"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	// 重复答复拒绝
	if err := s.AnswerDecision(decision.ID, "再答", "user"); !errors.Is(err, ErrBadState) {
		t.Fatalf("重复答复应拒: %v", err)
	}
	// 不存在的裁决
	if err := s.AnswerDecision(99999, "x", "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("幽灵裁决: %v", err)
	}

	// list --open 只剩项目级那条
	open, err := s.ListDecisions(true)
	if err != nil || len(open) != 1 || open[0].ID != projectDecision.ID {
		t.Fatalf("open 列表: %v %+v", err, open)
	}
	// 全量列表两条，且答复字段完整
	all, _ := s.ListDecisions(false)
	if len(all) != 2 {
		t.Fatalf("全量: %+v", all)
	}
	for _, item := range all {
		if item.ID == decision.ID && (item.Answer != "done 时序" || item.AnsweredBy != "user" || item.AnsweredAt.IsZero()) {
			t.Fatalf("答复字段: %+v", item)
		}
	}

	// 事件流：decision_opened ×2 + decision_answered ×1（项目级 card_id 空也在流里）
	events, _ := s.EventsFromAsc(nil, 0, 100)
	opened, answered := 0, 0
	for _, event := range events {
		switch event.Type {
		case EvDecisionOpened:
			opened++
		case EvDecisionAnswered:
			answered++
		}
	}
	if opened != 2 || answered != 1 {
		t.Fatalf("裁决事件: opened=%d answered=%d", opened, answered)
	}
}
