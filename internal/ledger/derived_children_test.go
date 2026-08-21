// CardView 子卡计数派生：children_total/children_done 查询期现算。
// done 语义 = 已完结（已完成或终止），与聚合闸（move_children_gate_test）
// 保持同一把尺——徽标显示 2/3 而闸放行，是两处语义漂移的经典形态。
package ledger

import "testing"

func TestListCardsDerivesChildrenCounts(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "母卡")
	childA := mustChild(t, s, parent.ID, "子 A")
	childB := mustChild(t, s, parent.ID, "子 B")
	mustChild(t, s, parent.ID, "子 C")
	// 孙卡不计入母卡（只数直接子卡）
	mustChild(t, s, childA.ID, "孙卡")

	if err := s.MoveCard(childA.ID, StatusDone, "", "test"); err != nil {
		t.Fatalf("完结子 A: %v", err)
	}
	if err := s.CloseCard(childB.ID, CloseShelved, "test"); err != nil {
		t.Fatalf("搁置子 B: %v", err)
	}

	views, err := s.ListCards(CardFilter{})
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	var got *CardView
	for i := range views {
		if views[i].ID == parent.ID {
			got = &views[i]
		}
	}
	if got == nil {
		t.Fatalf("列表里找不到母卡 %s", parent.ID)
	}
	if got.ChildrenTotal != 3 || got.ChildrenDone != 2 {
		t.Fatalf("应 total=3 done=2（done 含终止），实得 total=%d done=%d",
			got.ChildrenTotal, got.ChildrenDone)
	}
}
