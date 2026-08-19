package ledger

import (
	"errors"
	"testing"
)

func mk(t *testing.T, s *Store, title string) Card {
	t.Helper()
	card, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("mk: %v", err)
	}
	return card
}

func TestBlocksCycleDetection(t *testing.T) {
	s := seedStore(t)
	a, b, c := mk(t, s, "a"), mk(t, s, "b"), mk(t, s, "c")
	// a blocks b, b blocks c 合法
	if err := s.AddBlocks(a.ID, b.ID, "test"); err != nil {
		t.Fatalf("ab: %v", err)
	}
	if err := s.AddBlocks(b.ID, c.ID, "test"); err != nil {
		t.Fatalf("bc: %v", err)
	}
	// c blocks a 成环，拒绝
	if err := s.AddBlocks(c.ID, a.ID, "test"); !errors.Is(err, ErrCycle) {
		t.Fatalf("应报环: %v", err)
	}
	// 自阻塞拒绝
	if err := s.AddBlocks(a.ID, a.ID, "test"); err == nil {
		t.Fatal("自阻塞应拒")
	}
	// 阻塞自己的祖先/后代拒绝（parent 树与 blocks 混合成环的具体解释）
	child, _ := s.CreateCard(NewCard{Title: "child", Project: "p", Workflow: "bug", Parent: a.ID, Actor: "test"})
	if err := s.AddBlocks(child.ID, a.ID, "test"); !errors.Is(err, ErrCycle) {
		t.Fatalf("子阻塞父应拒: %v", err)
	}
	// 重复加边幂等报错（主键冲突转干净错误）
	if err := s.AddBlocks(a.ID, b.ID, "test"); err == nil {
		t.Fatal("重复边应报错")
	}
	// 解除
	if err := s.RemoveRelation(a.ID, b.ID, RelBlocks); err != nil {
		t.Fatalf("unlink: %v", err)
	}
}

func TestAddRelationTypes(t *testing.T) {
	s := seedStore(t)
	a, b := mk(t, s, "a"), mk(t, s, "b")
	if err := s.AddRelation(a.ID, b.ID, RelDiscoveredFrom, "test"); err != nil {
		t.Fatalf("discovered_from: %v", err)
	}
	// merged_into 禁止直建（必须走 MergeCards 的校验）
	if err := s.AddRelation(a.ID, b.ID, RelMergedInto, "test"); err == nil {
		t.Fatal("merged_into 直建应拒")
	}
	// 未知类型拒绝
	if err := s.AddRelation(a.ID, b.ID, "xx", "test"); err == nil {
		t.Fatal("未知类型应拒")
	}
	relations, err := s.RelationsOf(a.ID)
	if err != nil || len(relations) != 1 || relations[0].Type != RelDiscoveredFrom {
		t.Fatalf("RelationsOf: %v %+v", err, relations)
	}
}

func TestEffectiveBaseBranch(t *testing.T) {
	s := seedStore(t)
	epic, _ := s.CreateCard(NewCard{Title: "epic", Project: "p", Workflow: "feature",
		BaseBranch: "desktop-shell", Actor: "test"})
	child, _ := s.CreateCard(NewCard{Title: "c", Project: "p", Workflow: "feature",
		Parent: epic.ID, Actor: "test"})
	grandchild, _ := s.CreateCard(NewCard{Title: "g", Project: "p", Workflow: "feature",
		Parent: child.ID, Actor: "test"})
	top, _ := s.CreateCard(NewCard{Title: "hotfix", Project: "p", Workflow: "bug", Actor: "test"})

	for _, testCase := range []struct{ id, want string }{
		{epic.ID, "desktop-shell"},
		{child.ID, "desktop-shell"},      // 一级继承
		{grandchild.ID, "desktop-shell"}, // 跨级继承
		{top.ID, ""},                     // 顶层无设置 = 空（主线）
	} {
		got, err := s.EffectiveBaseBranch(testCase.id)
		if err != nil || got != testCase.want {
			t.Fatalf("%s: got %q want %q err %v", testCase.id, got, testCase.want, err)
		}
	}
}
