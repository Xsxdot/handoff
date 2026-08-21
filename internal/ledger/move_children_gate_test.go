// 聚合闸：RequireChildrenDone 的门条件。父卡进目标列前，全部直接子卡
// 必须已完结（已完成或终止）；无子卡时空洞为真（同一工作流复用给不扇出
// 的卡时不该被闸卡住）。
package ledger

import (
	"errors"
	"strings"
	"testing"
)

// fanoutStore 建一条带聚合闸的两列工作流：进行中 →（闸）集成。
func fanoutStore(t *testing.T) *Store {
	t.Helper()
	s := seedStore(t)
	if _, err := s.PutWorkflow("fanout", WorkflowDef{Nodes: []NodeDef{
		{Name: "进行中", Next: "集成"},
		{Name: "集成", Gate: Gate{RequireChildrenDone: true}},
	}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	return s
}

func mkFanout(t *testing.T, s *Store, title string) Card {
	t.Helper()
	card, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "fanout", Actor: "test"})
	if err != nil {
		t.Fatalf("建 fanout 卡: %v", err)
	}
	return card
}

// 有未完结子卡 → 拒，错误里点名 pending 的子卡；子卡全完结（done 或
// closed 混合）→ 放行。
func TestChildrenGateBlocksUntilChildrenSettled(t *testing.T) {
	s := fanoutStore(t)
	parent := mkFanout(t, s, "母卡")
	childA := mustChild(t, s, parent.ID, "子 A")
	childB := mustChild(t, s, parent.ID, "子 B")

	err := s.MoveCard(parent.ID, "集成", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("应被聚合闸拦下，实得: %v", err)
	}
	if !strings.Contains(err.Error(), childA.ID) {
		t.Fatalf("错误应点名未完结子卡 %s，实得: %v", childA.ID, err)
	}

	if err := s.MoveCard(childA.ID, StatusDone, "", "test"); err != nil {
		t.Fatalf("完结子 A: %v", err)
	}
	if err := s.CloseCard(childB.ID, CloseCancelled, "test"); err != nil {
		t.Fatalf("终止子 B: %v", err)
	}
	if err := s.MoveCard(parent.ID, "集成", "", "test"); err != nil {
		t.Fatalf("子卡全完结后应放行，实得: %v", err)
	}
}

// 无子卡 = 空洞为真：同一工作流给不扇出的卡复用时直接过闸。
func TestChildrenGatePassesWithNoChildren(t *testing.T) {
	s := fanoutStore(t)
	solo := mkFanout(t, s, "独卡")
	if err := s.MoveCard(solo.ID, "集成", "", "test"); err != nil {
		t.Fatalf("无子卡应过闸，实得: %v", err)
	}
}
