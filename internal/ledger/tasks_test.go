package ledger

import (
	"errors"
	"testing"
	"time"
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

func TestDriverLease(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// 心跳未过期时他人抢占失败
	if err := s.ClaimDriver(card.ID, "session-B"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("未过期应拒抢: %v", err)
	}
	// 同会话续心跳
	if err := s.HeartbeatDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	// 过期后可抢（把心跳改老模拟过期，driverLeaseTTL 是包常量）
	old := time.Now().Add(-2 * driverLeaseTTL)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), card.ID); err != nil {
		t.Fatalf("做旧: %v", err)
	}
	if err := s.ClaimDriver(card.ID, "session-B"); err != nil {
		t.Fatalf("过期后抢占: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.DriverSession != "session-B" {
		t.Fatalf("驱动会话: %+v", got)
	}
}
