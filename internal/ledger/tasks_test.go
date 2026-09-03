package ledger

import (
	"errors"
	"testing"
)

func TestLinkTask(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.LinkTask(card.ID, "mac-02", "T1234", "implement", "test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// 同 (target, task) 重复挂账拒绝——一个 task 至多挂一张卡（主键约束转干净错误）
	card2 := mk(t, s, "另一张")
	if err := s.LinkTask(card2.ID, "mac-02", "T1234", "review", "test"); err == nil {
		t.Fatal("重复挂账应拒")
	}
	links, err := s.TasksOf(card.ID)
	if err != nil || len(links) != 1 || links[0].TaskID != "T1234" || links[0].Purpose != "implement" {
		t.Fatalf("TasksOf: %v %+v", err, links)
	}
	allLinks, err := s.AllTaskLinks()
	if err != nil || len(allLinks) != 1 || allLinks[0].CardID != card.ID {
		t.Fatalf("AllTaskLinks: %v %+v", err, allLinks)
	}
	// 反查：task → 卡
	cardID, err := s.CardOfTask("mac-02", "T1234")
	if err != nil || cardID != card.ID {
		t.Fatalf("CardOfTask: %v %q", err, cardID)
	}
	if _, err := s.CardOfTask("mac-02", "无此任务"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("幽灵 task: %v", err)
	}
}

func TestClaimCardIsDisabled(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimCard(card.ID, "session-A"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧认领入口应停用: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil || got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("旧认领不得写席位: %v %+v", err, got)
	}
}

func TestTakeoverCardIsDisabled(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.TakeoverCard(card.ID, "session-new", "cli:test@example"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧接管入口应停用: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("旧接管不得写席位: %+v", got)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == EvDriverTakeover {
			t.Fatalf("旧接管不得落事件: %+v", event)
		}
	}
}
