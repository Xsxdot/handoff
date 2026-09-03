package ledger

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestMoveCASAndGate(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})

	// gate：无 spec 附件进「已出spec」被拒（判据⑬的单测形）
	err := s.MoveCard(card.ID, "已出spec", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("gate 应拒绝: %v", err)
	}
	_, _ = s.AttachFile(card.ID, "spec", "specs/x.md", "test")
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
	_, _ = s.AttachFile(card.ID, "spec", "s.md", "test")
	_ = s.MoveCard(card.ID, "已出spec", "", "test")
	_ = s.MoveCard(card.ID, StatusDoing, "", "test")
	_ = s.MoveCard(card.ID, StatusReview, "", "test")
	// 判据为空进「待合并」被拒（feature 流第二道门）
	if err := s.MoveCard(card.ID, "待合并", "", "test"); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("缺判据应拒: %v", err)
	}
}

// TestClaimCardIsNoLongerASeatWriter 锁住旧人尺度入口不再碰协调者席位。
func TestClaimCardIsNoLongerASeatWriter(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "归属", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)

	if err := s.ClaimCard(c.ID, "cli:a@h"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧认领入口应停用: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != before.Status || got.DriverSession != before.DriverSession || got.DriverSource != before.DriverSource || !got.DriverHeartbeatAt.Equal(before.DriverHeartbeatAt) {
		t.Fatalf("旧认领不得改卡: before=%+v after=%+v", before, got)
	}
}

// TestReleaseCardDoesNotClearSeat 空座可幂等释放，非空席位交给 rebind。
func TestReleaseCardDoesNotClearSeat(t *testing.T) {
	s := seedStore(t)
	if err := s.ReleaseCard("B99999", "cli:x@h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	c, _ := s.CreateCard(NewCard{Title: "释放", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.ReleaseCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("无主卡释放应幂等成功: %v", err)
	}
	if err := s.BindSeat(c.ID, "cli:codex#session-a", proto.SeatSourceBind); err != nil {
		t.Fatalf("绑定: %v", err)
	}
	if err := s.ReleaseCard(c.ID, "cli:a@h"); !errors.Is(err, ErrBadState) {
		t.Fatalf("非空席位 release 应拒绝: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "cli:codex#session-a" || got.DriverSource != string(proto.SeatSourceBind) {
		t.Fatalf("失败的 release 不得动席位: %+v", got)
	}
}
