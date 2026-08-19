package ledger

import (
	"context"
	"testing"
	"time"
)

func TestFollowSQLitePoll(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "被跟的卡")
	start, _ := s.MaxSeq()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan Event, 16)
	done := make(chan error, 1)
	go func() {
		done <- s.Follow(ctx, func() ([]string, error) { return []string{c.ID}, nil },
			start, 100*time.Millisecond, func(e Event) error {
				got <- e
				return nil
			})
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := s.AddComment(c.ID, "新评论", "普通", "test"); err != nil {
		t.Fatalf("comment: %v", err)
	}
	select {
	case e := <-got:
		if e.Type != EvComment {
			t.Fatalf("送达类型: %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("事件未送达")
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Fatalf("follow 退出: %v", err)
	}
}

func TestFollowDynamicMembership(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "根")
	start, _ := s.MaxSeq()
	members := func() ([]string, error) { return s.Subtree(c.ID) }

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan Event, 16)
	go s.Follow(ctx, members, start, 100*time.Millisecond, func(e Event) error {
		got <- e
		return nil
	})
	time.Sleep(300 * time.Millisecond)
	child, err := s.SplitCard(c.ID, "新子卡", "test")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	_, _ = s.AddComment(child.ID, "子卡上的事件", "普通", "test")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-got:
			if e.CardID == child.ID && e.Type == EvComment {
				return
			}
		case <-deadline:
			t.Fatal("新成员事件未进流")
		}
	}
}
