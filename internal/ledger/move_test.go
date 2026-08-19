package ledger

import (
	"errors"
	"strings"
	"testing"
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

// 认领必须原子：转状态与落驱动同事务。分两步写时并发输家会读到
// 「进行中但驱动为空」的中间态，报不出认领者是谁（判据⑥要会话名），
// 进程在窗口内崩掉还会留下没人驱动的「进行中」卡。
func TestClaimCardIsAtomic(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "认领", Project: "p", Workflow: "bug", Actor: "test"})

	if err := s.ClaimCard(c.ID, StatusDoing, StatusTodo, "sess-A#1"); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != StatusDoing || got.DriverSession != "sess-A#1" {
		t.Fatalf("状态与驱动必须同时落定: status=%q driver=%q", got.Status, got.DriverSession)
	}
	if got.DriverHeartbeatAt.IsZero() {
		t.Fatal("认领未落心跳时间")
	}

	// 第二个会话：前值已不是「待办」，且卡上有活跃驱动——错误必须报出持有者
	err := s.ClaimCard(c.ID, StatusDoing, StatusTodo, "sess-B#2")
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("并发认领应 ErrCASConflict: %v", err)
	}
	if !strings.Contains(err.Error(), "sess-A#1") {
		t.Fatalf("错误必须点名持有者会话（判据⑥）: %v", err)
	}

	// 同一会话重入不算冲突（重试安全）
	if err := s.ClaimCard(c.ID, StatusReview, StatusDoing, "sess-A#1"); err != nil {
		t.Fatalf("同会话推进不应被自己的驱动挡住: %v", err)
	}
}
