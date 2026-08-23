package ledger

import (
	"encoding/json"
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

func TestDriverClaimDoesNotExpire(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("claim A: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), card.ID); err != nil {
		t.Fatalf("做旧认领时刻: %v", err)
	}
	if err := s.ClaimDriver(card.ID, "session-B"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("旧认领时刻也必须拒绝他会话: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.DriverSession != "session-A" {
		t.Fatalf("冲突认领不得改写驱动: %q", got.DriverSession)
	}
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("同会话重入必须放行: %v", err)
	}
}

func TestReleaseCardOnlyOwnerCanClearAndOtherCanClaim(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-A"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.ReleaseCard(card.ID, "session-B"); err != nil {
		t.Fatalf("非持有者 release 应为无操作: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.DriverSession != "session-A" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("非持有者不得清空驱动或认领时刻: session=%q at=%v", got.DriverSession, got.DriverHeartbeatAt)
	}
	if err := s.ReleaseCard(card.ID, "session-A"); err != nil {
		t.Fatalf("owner release: %v", err)
	}
	if err := s.ClaimDriver(card.ID, "session-B"); err != nil {
		t.Fatalf("release 后应可被新会话认领: %v", err)
	}
}

func TestTakeoverCardWritesDriverAndRoundTripsPayload(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimDriver(card.ID, "session-old"); err != nil {
		t.Fatalf("claim old: %v", err)
	}
	if err := s.TakeoverCard(card.ID, "session-new", "cli:test@example"); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读接管后卡: %v", err)
	}
	if got.DriverSession != "session-new" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("接管必须写新驱动与认领时刻: session=%q at=%v", got.DriverSession, got.DriverHeartbeatAt)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found Event
	for _, event := range events {
		if event.Type == EvDriverTakeover {
			found = event
		}
	}
	if found.Type != EvDriverTakeover || found.Actor != "cli:test@example" {
		t.Fatalf("接管事件类型/actor 错误: %+v", found)
	}
	var payload struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("解码接管 payload: %v", err)
	}
	if payload.From == nil || payload.To == nil || *payload.From != "session-old" || *payload.To != "session-new" {
		t.Fatalf("接管 payload 必须保留 from/to 字段: %+v", payload)
	}
}
