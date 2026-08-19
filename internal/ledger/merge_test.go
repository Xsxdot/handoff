package ledger

import (
	"errors"
	"testing"
)

func TestMergeUnmergeSplit(t *testing.T) {
	s := seedStore(t)
	carrier := mk(t, s, "承载卡")
	m1, m2, m3 := mk(t, s, "m1"), mk(t, s, "m2"), mk(t, s, "m3")
	_ = s.SetAcceptance(m1.ID, "m1 判据", "test")

	if err := s.MergeCards([]string{m1.ID, m2.ID, m3.ID}, carrier.ID, "test"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 跟随派生：Following 指向承载卡
	views, _ := s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	byID := map[string]CardView{}
	for _, view := range views {
		byID[view.ID] = view
	}
	if byID[m1.ID].Following != carrier.ID {
		t.Fatalf("m1 应跟随 %s: %+v", carrier.ID, byID[m1.ID])
	}
	if byID[carrier.ID].MergedCount != 3 {
		t.Fatalf("承载卡应显示 3 个成员: %+v", byID[carrier.ID])
	}
	// 被并卡验收判据无损
	got, _ := s.GetCard(m1.ID)
	if got.AcceptanceCriteria != "m1 判据" {
		t.Fatalf("判据被吞: %q", got.AcceptanceCriteria)
	}
	// 链式拒绝：承载卡再并入别人 / 已并卡再当承载卡
	x := mk(t, s, "x")
	if err := s.MergeCards([]string{carrier.ID}, x.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("承载卡被并应拒: %v", err)
	}
	if err := s.MergeCards([]string{x.ID}, m1.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("被并卡承载应拒: %v", err)
	}
	// 重复并入拒绝
	if err := s.MergeCards([]string{m1.ID}, carrier.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("重复并入应拒: %v", err)
	}
	// 终态卡不许参与合并（成员侧；承载侧同一检查）
	dead := mk(t, s, "dead")
	_ = s.CloseCard(dead.ID, CloseCancelled, "test")
	if err := s.MergeCards([]string{dead.ID}, carrier.ID, "test"); !errors.Is(err, ErrBadMerge) {
		t.Fatalf("终态成员应拒: %v", err)
	}
	// 拆回：恢复自主 + 判据仍在（判据⑫的单测形）
	if err := s.UnmergeCard(m1.ID, "test"); err != nil {
		t.Fatalf("unmerge: %v", err)
	}
	views, _ = s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	for _, view := range views {
		if view.ID == m1.ID && view.Following != "" {
			t.Fatalf("拆回后仍跟随: %+v", view)
		}
	}
	got, _ = s.GetCard(m1.ID)
	if got.AcceptanceCriteria != "m1 判据" {
		t.Fatalf("拆回后判据丢失")
	}
}

func TestMergeCrossBaseRejected(t *testing.T) {
	s := seedStore(t)
	a, _ := s.CreateCard(NewCard{Title: "a", Project: "p", Workflow: "bug",
		BaseBranch: "desktop-shell", Actor: "test"})
	b := mk(t, s, "b") // 基线 = 主线
	err := s.MergeCards([]string{a.ID}, b.ID, "test")
	if !errors.Is(err, ErrBadMerge) {
		t.Fatalf("跨基线应拒: %v", err)
	}
}

func TestSplitCard(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "大卡")
	child, err := s.SplitCard(parent.ID, "拆出的子项", "test")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if child.ParentID != parent.ID || child.ID != parent.ID+".1" {
		t.Fatalf("子卡形态: %+v", child)
	}
	// split_from 边自动挂
	relations, _ := s.RelationsOf(child.ID)
	found := false
	for _, relation := range relations {
		if relation.Type == RelSplitFrom && relation.From == child.ID && relation.To == parent.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 split_from 边: %+v", relations)
	}
}
