package ledger

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMoveCASAndGate(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})

	// gate：无 spec 附件进「已出spec」被拒（判据⑬的单测形）
	err := s.MoveCard(card.ID, "已出spec", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("gate 应拒绝: %v", err)
	}
	_ = s.AttachFile(card.ID, "spec", "specs/x.md", "test")
	if err := s.MoveCard(card.ID, "已出spec", "", "test"); err != nil {
		t.Fatalf("挂附件后应放行: %v", err)
	}

	// CAS：expect 与实际不符干净失败
	err = s.MoveCard(card.ID, StatusDoing, StatusTodo, "test")
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("CAS 应冲突: %v", err)
	}
	if err := s.MoveCard(card.ID, StatusDoing, "已出spec", "test"); err != nil {
		t.Fatalf("正确前值应成功: %v", err)
	}

	// 未知状态拒绝（必须是 ErrBadState 哨兵——Plan D 的 API 层靠它翻译 409）
	if err := s.MoveCard(card.ID, "不存在的状态", "", "test"); !errors.Is(err, ErrBadState) {
		t.Fatalf("未知状态应拒且 wrap ErrBadState: %v", err)
	}
	// 终态卡不可 move
	_ = s.MoveCard(card.ID, StatusReview, "", "test")
	_ = s.SetAcceptance(card.ID, "判据", "test")
	_ = s.MoveCard(card.ID, "待合并", "", "test")
	_ = s.MoveCard(card.ID, StatusDone, "", "test")
	if err := s.MoveCard(card.ID, StatusTodo, "", "test"); !errors.Is(err, ErrBadState) {
		t.Fatalf("已完成卡 move 应拒: %v", err)
	}

	// 事件审计：status_moved 序列完整
	events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
	moves := 0
	for _, event := range events {
		if event.Type == EvStatusMoved {
			moves++
		}
	}
	if moves != 5 { // 成功的五次：→已出spec →进行中 →待审阅 →待合并 →已完成
		t.Fatalf("status_moved 事件数 %d != 5", moves)
	}
}

func TestMoveGateAcceptance(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})
	_ = s.AttachFile(card.ID, "spec", "s.md", "test")
	_ = s.MoveCard(card.ID, "已出spec", "", "test")
	_ = s.MoveCard(card.ID, StatusDoing, "", "test")
	_ = s.MoveCard(card.ID, StatusReview, "", "test")
	// 判据为空进「待合并」被拒（feature 流第二道门）
	if err := s.MoveCard(card.ID, "待合并", "", "test"); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("缺判据应拒: %v", err)
	}
}

// TestClaimCardOwnershipSemantics 归属锁全集（b239-contract.md §3 断言 1–7）。
// 归属是人尺度：不随时间流逝转移（8-23 decision #1）、不改状态列、幂等重入。
func TestClaimCardOwnershipSemantics(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "归属", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)

	if err := s.ClaimCard("B99999", "cli:a@h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	if err := s.ClaimCard(c.ID, ""); err == nil {
		t.Fatalf("空 owner 应被拒")
	}
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != before.Status {
		t.Fatalf("认领不得改状态列: before=%q after=%q", before.Status, got.Status)
	}
	if got.DriverSession != "cli:a@h" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("认领应写归属与认领时刻: %+v", got)
	}
	events, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	for _, e := range events {
		if e.Type == EvDriverTakeover || e.Type == EvStatusMoved {
			t.Fatalf("认领不得落事件: %+v", e)
		}
	}

	old := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ?`),
		s.tval(old), c.ID); err != nil {
		t.Fatalf("做旧认领时刻: %v", err)
	}
	err := s.ClaimCard(c.ID, "cli:b@h")
	if !errors.Is(err, ErrCASConflict) || !strings.Contains(err.Error(), "cli:a@h") {
		t.Fatalf("他主持有应拒且点名持有者: %v", err)
	}
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("同 owner 重入应幂等: %v", err)
	}
	_ = s.MoveCard(c.ID, StatusDone, "", "t")
	if err := s.ClaimCard(c.ID, "cli:c@h"); !errors.Is(err, ErrBadState) {
		t.Fatalf("终态卡认领应 ErrBadState: %v", err)
	}
}

// TestReleaseCardOwnershipSemantics 归属释放反转（断言 8–11）。
// 今天非持有者释放是静默 no-op + CLI 假成功——这是本卡核心行为反转点。
func TestReleaseCardOwnershipSemantics(t *testing.T) {
	s := seedStore(t)
	if err := s.ReleaseCard("B99999", "cli:x@h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	c, _ := s.CreateCard(NewCard{Title: "释放", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.ReleaseCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("无主卡释放应幂等成功: %v", err)
	}
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("认领: %v", err)
	}
	if err := s.ReleaseCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("本人释放: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "" || !got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("本人释放应清空两个字段: %+v", got)
	}
	_ = s.ClaimCard(c.ID, "cli:a@h")
	err := s.ReleaseCard(c.ID, "cli:b@h")
	if !errors.Is(err, ErrCASConflict) || !strings.Contains(err.Error(), "cli:a@h") {
		t.Fatalf("非持有者释放应可见失败并点名持有者: %v", err)
	}
	after, _ := s.GetCard(c.ID)
	if after.DriverSession != "cli:a@h" {
		t.Fatalf("失败的释放不得动归属: %q", after.DriverSession)
	}
}
