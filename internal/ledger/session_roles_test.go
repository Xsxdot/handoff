package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
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

// The query used to repeat a latest-dispatch lookup for every mirror row. Keep a
// real SQLite growth check so that regression cannot silently return at scale.
func TestSessionRoleActivitySQLiteGrowthAndPlan(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(NewCard{Title: "SQLite activity growth", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{Target: "worker", TaskID: "task", Purpose: "implement"}); err != nil {
		t.Fatal(err)
	}
	previous := 0
	for _, count := range []int{500, 2000, 5000} {
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := previous + 1; i <= count; i++ {
			taskType := "progress"
			if i == 500 {
				taskType = "completed"
			}
			payload := fmt.Sprintf(`{"task_type":%q,"n":%d}`, taskType, i)
			if _, err := tx.Exec(s.q(`INSERT INTO card_events (card_id,type,actor,payload,source_target,source_task,source_seq,created_at) VALUES (?,?,?,?,?,?,?,?)`),
				card.ID, EvTaskMirrored, "mirror", payload, "worker", "task", i, s.tval(time.Now().UTC())); err != nil {
				_ = tx.Rollback()
				t.Fatalf("seed %d SQLite mirror rows: %v", count, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		query, args := buildSessionRoleActivityQuery(s.dialect, []string{card.ID}, 100)
		rows, err := s.db.QueryContext(context.Background(), s.q("EXPLAIN QUERY PLAN "+query), args...)
		if err != nil {
			t.Fatalf("explain rows=%d: %v", count, err)
		}
		var plan []string
		for rows.Next() {
			var detail string
			var id, parent, notUsed int
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
		joinedPlan := strings.Join(plan, "\n")
		if strings.Contains(strings.ToUpper(joinedPlan), "CORRELATED") {
			t.Fatalf("SQLite plan must not contain correlated subqueries at %d rows:\n%s", count, joinedPlan)
		}
		started := time.Now()
		events, truncated, err := s.SessionRoleActivityContext(context.Background(), []string{card.ID}, 100)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatalf("query rows=%d: %v", count, err)
		}
		if len(events) > 106 || len(truncated) != 1 {
			t.Fatalf("bounded result rows=%d returned=%d truncated=%v", count, len(events), truncated)
		}
		foundLatest, foundLifecycle := false, false
		for _, ev := range events {
			if ev.Type == EvTaskMirrored && ev.SourceSeq == int64(count) {
				foundLatest = true
			}
			if ev.Type == EvTaskMirrored && ev.SourceSeq == 500 {
				foundLifecycle = true
			}
		}
		if !foundLatest || !foundLifecycle {
			t.Fatalf("query lost current lifecycle/raw fact rows=%d latest=%t lifecycle=%t", count, foundLatest, foundLifecycle)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("SQLite query regressed to unbounded growth rows=%d elapsed=%s", count, elapsed)
		}
		t.Logf("sqlite_activity rows=%d returned=%d elapsed=%s plan=%q", count, len(events), elapsed, joinedPlan)
		previous = count
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
	for i, seed := range []struct {
		card                 Card
		target, task, ticket string
		seq                  int64
	}{{first, "worker-a", "task-a", "ticket-a", 3}, {second, "worker-b", "task-b", "ticket-b", 9}} {
		if _, err := s.AppendMirroredEvent(seed.card.ID, MirroredEvent{Target: seed.target, Task: seed.task, SourceSeq: seed.seq, Type: "permission_request", Payload: []byte(`{"ticket_id":"` + seed.ticket + `"}`), CreatedAt: time.Now()}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	refs, truncated, err := s.SessionRoleTicketsContext(context.Background(), []string{first.ID, second.ID}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(refs) != 2 {
		t.Fatalf("两卡范围都应返回独立票据: refs=%+v truncated=%t", refs, truncated)
	}
	want := map[string]string{first.ID: "ticket-a", second.ID: "ticket-b"}
	for _, ref := range refs {
		if ref.TicketID != want[ref.CardID] {
			t.Fatalf("target/task/source_seq 必须按真实卡关联: refs=%+v", refs)
		}
	}
	for _, seed := range []struct{ cardID, target, task, ticket string }{
		{first.ID, "worker-a", "task-a", "ticket-a"},
		{second.ID, "worker-b", "task-b", "ticket-b"},
	} {
		oneCard, truncated, err := s.SessionRoleTicketsContext(context.Background(), []string{seed.cardID}, 20)
		if err != nil {
			t.Fatal(err)
		}
		if truncated || len(oneCard) != 1 || oneCard[0].CardID != seed.cardID || oneCard[0].Target != seed.target || oneCard[0].TaskID != seed.task || oneCard[0].TicketID != seed.ticket {
			t.Fatalf("单卡查询必须只返回该卡真实 target/task/ticket ref: card=%s refs=%+v truncated=%t", seed.cardID, oneCard, truncated)
		}
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

func TestSessionRoleActivityRetainsThreeCurrentFactsWithinHardBound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(NewCard{Title: "three current facts", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(card.ID, "cli:agent#current", proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{Target: "old-target", TaskID: "old-task", Purpose: "implement"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{Target: "current-target", TaskID: "current-task", Purpose: "implement"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureComment(card.ID, "wake_round:start:current-round", `{"phase":"start"}`, "agentd"); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Now().UTC().Add(-time.Minute)
	for _, event := range []MirroredEvent{
		{Target: "current-target", Task: "current-task", SourceSeq: 10, Type: "completed", Payload: []byte(`{"summary":"finished"}`), CreatedAt: baseTime},
		{Target: "current-target", Task: "current-task", SourceSeq: 11, Type: "progress", Payload: []byte(`{"summary":"ordinary progress"}`), CreatedAt: baseTime.Add(time.Second)},
	} {
		if _, err := s.AppendMirroredEvent(card.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, err := s.AppendMirroredEvent(card.ID, MirroredEvent{Target: "old-target", Task: "old-task", SourceSeq: int64(i + 1), Type: "progress", Payload: []byte(`{"summary":"historic event"}`), CreatedAt: baseTime.Add(time.Duration(i+2) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	events, truncated, err := s.SessionRoleActivityContext(context.Background(), []string{card.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) > 7 {
		t.Fatalf("每卡最多 limit+6 行: got=%d events=%+v", len(events), events)
	}
	if len(truncated) != 1 || truncated[0] != card.ID {
		t.Fatalf("窗口外事实应显式截断: %v", truncated)
	}
	foundDispatch, foundWake, foundSeat := false, false, false
	foundRaw, foundLifecycle := false, false
	for _, ev := range events {
		switch {
		case ev.Type == EvDispatched:
			var payload struct {
				TaskID string `json:"task_id"`
				Target string `json:"target"`
			}
			if json.Unmarshal(ev.Payload, &payload) == nil && payload.TaskID == "current-task" && payload.Target == "current-target" {
				foundDispatch = true
			}
		case ev.Type == EvComment:
			foundWake = true
		case ev.Type == EvDriverSeatBound:
			foundSeat = true
		case ev.Type == EvTaskMirrored && ev.SourceTask == "current-task" && ev.SourceSeq == 11:
			foundRaw = true
		case ev.Type == EvTaskMirrored && ev.SourceTask == "current-task" && ev.SourceSeq == 10:
			foundLifecycle = true
		}
	}
	if !foundDispatch || !foundWake || !foundSeat || !foundRaw || !foundLifecycle {
		t.Fatalf("三项承重事实须穿有界窗口 dispatch=%t wake=%t seat=%t raw=%t lifecycle=%t events=%+v", foundDispatch, foundWake, foundSeat, foundRaw, foundLifecycle, events)
	}
}
