package ledger

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSessionRoleActivitySkipsOrdinaryCommentsAndKeepsCurrentWake(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(NewCard{Title: "B456 role activity", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 110; i++ {
		if _, err := s.AddComment(card.ID, "long ordinary spec note", "普通", "user:test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnsureComment(card.ID, "wake_round:start:round-1", `{"phase":"start"}`, "agentd"); err != nil {
		t.Fatal(err)
	}
	events, truncated, err := s.SessionRoleActivityContext(context.Background(), []string{card.ID}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(truncated) != 0 {
		t.Fatalf("普通评论不应挤满 wake 窗口: %v", truncated)
	}
	if len(events) != 1 || events[0].Type != EvComment {
		t.Fatalf("应只读 wake_round 评论: %+v", events)
	}
}

func TestSessionRoleTicketsAreCardScoped(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateCard(NewCard{Title: "B456 first", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateCard(NewCard{Title: "B456 second", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for i, card := range []Card{first, second} {
		if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "worker", Task: "task", SourceSeq: 1, Type: "permission_request", Payload: []byte(`{"ticket_id":"ticket"}`), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	refs, truncated, err := s.SessionRoleTicketsContext(context.Background(), []string{first.ID}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(refs) != 1 || refs[0].CardID != first.ID {
		t.Fatalf("票据必须限制到当前卡: refs=%+v truncated=%t", refs, truncated)
	}
}

func TestSessionRoleActivityBoundsHistoricalTaskPartitions(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(NewCard{Title: "bounded role activity", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{Target: "worker", TaskID: "current-task", Purpose: "implement"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "worker", Task: fmt.Sprintf("historical-%03d", i), SourceSeq: 1,
			Type: "progress", Payload: []byte(`{"task_type":"progress"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "worker", Task: "current-task", SourceSeq: 3,
		Type: "completed", Payload: []byte(`{"task_type":"completed"}`)}); err != nil {
		t.Fatal(err)
	}
	events, truncated, err := s.SessionRoleActivityContext(context.Background(), []string{card.ID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) > 15 {
		t.Fatalf("历史 task 数量不得突破每卡 hard cap（limit+固定必要事实）: rows=%d", len(events))
	}
	if len(truncated) != 1 || truncated[0] != card.ID {
		t.Fatalf("超过窗口必须显式标记截断: rows=%d truncated=%v", len(events), truncated)
	}
	foundCurrent := false
	for _, event := range events {
		if event.Type == "task_mirrored" && event.SourceTask == "current-task" && event.SourceSeq == 3 {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("当前派发 task 的最新事实必须保留: %+v", events)
	}
}
