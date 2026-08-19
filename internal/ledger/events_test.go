package ledger

import (
	"encoding/json"
	"testing"
)

func TestCommentRefsAutoRelate(t *testing.T) {
	s := seedStore(t)
	a, b := mk(t, s, "a"), mk(t, s, "b")
	event, err := s.AddComment(a.ID, "这个问题与 #"+b.ID+" 同源", "普通", "test")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	var payload struct {
		Body string   `json:"body"`
		Refs []string `json:"refs"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	if len(payload.Refs) != 1 || payload.Refs[0] != b.ID {
		t.Fatalf("refs 解析: %+v", payload)
	}
	// 引用自动落 relates 边
	relations, _ := s.RelationsOf(a.ID)
	found := false
	for _, relation := range relations {
		if relation.Type == RelRelates && relation.To == b.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 relates 边: %+v", relations)
	}
	// 引用不存在的卡：评论照发、边不建、不报错（评论是记录不是校验）
	if _, err := s.AddComment(a.ID, "见 #B9999", "普通", "test"); err != nil {
		t.Fatalf("幽灵引用不应报错: %v", err)
	}
	// 更正类评论
	event2, _ := s.AddComment(a.ID, "上一条口径不对", "更正", "test")
	_ = json.Unmarshal(event2.Payload, &payload)
	if payload.Body == "" {
		t.Fatal("更正评论 body 丢失")
	}
	// 重置评论：payload 带 human_reset_node（Plan C 回合计数清零的落账形态）
	event3, err := s.AddCommentReset(a.ID, "人工看过，重新计数", "普通", "test", "review")
	if err != nil {
		t.Fatalf("reset comment: %v", err)
	}
	var resetPayload map[string]any
	_ = json.Unmarshal(event3.Payload, &resetPayload)
	if resetPayload["human_reset_node"] != "review" {
		t.Fatalf("缺 human_reset_node: %v", resetPayload)
	}
	if _, err := s.AddCommentReset(a.ID, "x", "普通", "test", ""); err == nil {
		t.Fatal("空节点名应拒")
	}
}

func TestAcceptanceAndNeeds(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "a")
	if err := s.RecordAcceptance(a.ID, true, "真机跑通 go test", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := s.MarkNeedsHuman(a.ID, "审阅超轮", "test"); err != nil {
		t.Fatalf("needs: %v", err)
	}
	views, _ := s.ListCards(CardFilter{Project: "p"})
	var view CardView
	for _, candidate := range views {
		if candidate.ID == a.ID {
			view = candidate
		}
	}
	if view.NeedsReason != "审阅超轮" {
		t.Fatalf("等人派生: %+v", view)
	}
	if err := s.ClearNeedsHuman(a.ID, "test"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	views, _ = s.ListCards(CardFilter{Project: "p"})
	for _, candidate := range views {
		if candidate.ID == a.ID && candidate.NeedsReason != "" {
			t.Fatalf("等人未清除: %+v", candidate)
		}
	}
}

func TestSubtree(t *testing.T) {
	s := seedStore(t)
	root := mk(t, s, "root")
	child, _ := s.SplitCard(root.ID, "c1", "test")
	_, _ = s.SplitCard(child.ID, "cc", "test")
	member := mk(t, s, "m")
	_ = s.MergeCards([]string{member.ID}, root.ID, "test")
	ids, err := s.Subtree(root.ID)
	if err != nil {
		t.Fatalf("subtree: %v", err)
	}
	// 根 + 两级后代 + 并入成员 = 4
	if len(ids) != 4 {
		t.Fatalf("子树成员 %v", ids)
	}
}
