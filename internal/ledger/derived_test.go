package ledger

import "testing"

func TestListCardsDerivedBlocked(t *testing.T) {
	s := seedStore(t)
	blocker, blocked := mk(t, s, "blocker"), mk(t, s, "blocked")
	_ = s.AddBlocks(blocker.ID, blocked.ID, "test")

	views, err := s.ListCards(CardFilter{Project: "p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]CardView{}
	for _, view := range views {
		byID[view.ID] = view
	}
	if !byID[blocked.ID].Blocked || len(byID[blocked.ID].BlockedBy) != 1 {
		t.Fatalf("blocked 派生: %+v", byID[blocked.ID])
	}
	// blocker 完成 → 解除
	_ = s.MoveCard(blocker.ID, StatusDoing, "", "test")
	_ = s.MoveCard(blocker.ID, StatusReview, "", "test")
	_ = s.MoveCard(blocker.ID, StatusDone, "", "test")
	views, _ = s.ListCards(CardFilter{Project: "p"})
	for _, view := range views {
		if view.ID == blocked.ID && view.Blocked {
			t.Fatalf("blocker 完成后仍 blocked: %+v", view)
		}
	}
}

func TestBlockerTerminatedNeedsHuman(t *testing.T) {
	s := seedStore(t)
	blocker, blocked := mk(t, s, "blocker"), mk(t, s, "blocked")
	_ = s.AddBlocks(blocker.ID, blocked.ID, "test")
	_ = s.CloseCard(blocker.ID, CloseCancelled, "test")
	// 判据③的单测形：blocker 终止不解锁，下游得等人
	views, _ := s.ListCards(CardFilter{Project: "p"})
	for _, view := range views {
		if view.ID == blocked.ID {
			if !view.Blocked {
				t.Fatalf("blocker 终止不应解锁: %+v", view)
			}
			if view.NeedsReason == "" {
				t.Fatalf("blocker 终止应派生等人: %+v", view)
			}
		}
	}
}

func TestListCardsFilters(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "a")
	b, _ := s.CreateCard(NewCard{Title: "b", Project: "p", Workflow: "bug",
		BaseBranch: "desktop-shell", Actor: "test"})
	_ = s.CloseCard(a.ID, CloseAbandoned, "test")

	// 默认排除终态
	views, _ := s.ListCards(CardFilter{Project: "p"})
	for _, view := range views {
		if view.ID == a.ID {
			t.Fatal("默认应排除终止卡")
		}
	}
	// IncludeTerminal 包含
	views, _ = s.ListCards(CardFilter{Project: "p", IncludeTerminal: true})
	found := false
	for _, view := range views {
		if view.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("IncludeTerminal 应包含终止卡")
	}
	// 基线过滤
	views, _ = s.ListCards(CardFilter{Project: "p", BaseBranch: "desktop-shell"})
	if len(views) != 1 || views[0].ID != b.ID {
		t.Fatalf("基线过滤: %+v", views)
	}
	// Needs 过滤（open 裁决也算——裁决在 Task 10，先用 MarkNeedsHuman）
	_ = s.MarkNeedsHuman(b.ID, "试一下", "test")
	views, _ = s.ListCards(CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || views[0].ID != b.ID {
		t.Fatalf("needs 过滤: %+v", views)
	}
}
