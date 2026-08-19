package ledger

import (
	"strings"
	"testing"
)

// seedStore 建好默认工作流的测试库——建卡类测试共用。
func seedStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func TestCreateCardAllocatesBNumbers(t *testing.T) {
	s := seedStore(t)
	// 迁移前的新库要先垫号，防与 markdown 总账撞号（spec §2.1 B 号分配）
	if err := s.EnsureMinB(156); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}
	c1, err := s.CreateCard(NewCard{Title: "第一张", Project: "handoff", Workflow: "feature", Actor: "test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c1.ID != "B157" {
		t.Fatalf("垫号后首卡应为 B157，得 %s", c1.ID)
	}
	c2, _ := s.CreateCard(NewCard{Title: "第二张", Project: "handoff", Workflow: "bug", Actor: "test"})
	if c2.ID != "B158" {
		t.Fatalf("连续编号: %s", c2.ID)
	}
	// 子卡走点号位
	ch1, err := s.CreateCard(NewCard{Title: "子一", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if err != nil || ch1.ID != "B157.1" {
		t.Fatalf("子卡: %v %s", err, ch1.ID)
	}
	ch2, _ := s.CreateCard(NewCard{Title: "子二", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if ch2.ID != "B157.2" {
		t.Fatalf("子卡连续: %s", ch2.ID)
	}
	// 初始态 = 工作流首态；钉最新版本
	if c1.Status != StatusTodo || c1.WorkflowVersion != 1 {
		t.Fatalf("初始态/版本: %+v", c1)
	}
	// 出生事件落流
	events, err := s.EventsFromAsc([]string{c1.ID}, 0, 10)
	if err != nil || len(events) == 0 || events[0].Type != EvCardCreated {
		t.Fatalf("出生事件: %v %+v", err, events)
	}
}

func TestCreateCardValidation(t *testing.T) {
	s := seedStore(t)
	if _, err := s.CreateCard(NewCard{Project: "p", Workflow: "feature"}); err == nil {
		t.Fatal("空标题应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "不存在的流"}); err == nil {
		t.Fatal("未知工作流应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Parent: "B999"}); err == nil {
		t.Fatal("父卡不存在应拒绝")
	}
}

func TestUpdateCardAttachAccept(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})
	if err := s.AttachFile(card.ID, "spec", "specs/x.md", "test"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.SetAcceptance(card.ID, "跑通判据一", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "specs/x.md" {
		t.Fatalf("附件: %+v", got.Attachments)
	}
	if got.AcceptanceCriteria != "跑通判据一" {
		t.Fatalf("判据: %q", got.AcceptanceCriteria)
	}
	// 同 path 重复 attach 幂等（不追加重复项）
	_ = s.AttachFile(card.ID, "spec", "specs/x.md", "test")
	got, _ = s.GetCard(card.ID)
	if len(got.Attachments) != 1 {
		t.Fatalf("attach 不幂等: %+v", got.Attachments)
	}
	if err := s.DetachFile(card.ID, "specs/x.md", "test"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ = s.GetCard(card.ID)
	if len(got.Attachments) != 0 {
		t.Fatalf("detach 未生效: %+v", got.Attachments)
	}
}

func TestUpdateCardMeta(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "旧标题")
	if err := s.UpdateCardMeta(c.ID, "新标题", "高", "test"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "高" {
		t.Fatalf("未生效: %+v", got)
	}
	// 只改一项：另一项保持
	_ = s.UpdateCardMeta(c.ID, "", "低", "test")
	got, _ = s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "低" {
		t.Fatalf("空串应保持原值: %+v", got)
	}
}

func TestCloseAndRevive(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "bug", Actor: "test"})
	if err := s.CloseCard(card.ID, "无效理由", "test"); err == nil {
		t.Fatal("非受控 reason 应拒绝")
	}
	if err := s.CloseCard(card.ID, CloseShelved, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.Status != StatusClosed || got.TerminateReason != CloseShelved {
		t.Fatalf("终止态: %+v", got)
	}
	if err := s.ReviveCard(card.ID, "test"); err != nil {
		t.Fatalf("revive: %v", err)
	}
	got, _ = s.GetCard(card.ID)
	if got.Status != StatusTodo || got.TerminateReason != "" {
		t.Fatalf("复活态: %+v", got)
	}
	// 取消/废弃不可复活
	_ = s.CloseCard(card.ID, CloseCancelled, "test")
	if err := s.ReviveCard(card.ID, "test"); err == nil || !strings.Contains(err.Error(), "搁置") {
		t.Fatalf("取消卡不应可复活: %v", err)
	}
}
