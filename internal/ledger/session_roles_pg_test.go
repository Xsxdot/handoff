package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// newB456SessionRolesPG creates a disposable schema only inside the explicitly
// named review database. It refuses all other databases and verifies search_path
// before opening the ledger store.
func newB456SessionRolesPG(t *testing.T) *Store {
	st, _, _ := newB456SessionRolesPGScoped(t)
	return st
}

func newB456SessionRolesPGScoped(t *testing.T) (*Store, string, string) {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LEDGER_TEST_PG_DSN is unset")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	if !strings.HasPrefix(cfg.Database, "handoff_b456_review_") {
		t.Fatalf("refusing non-review database (database name must use handoff_b456_review_ prefix)")
	}
	admin := stdlib.OpenDB(*cfg)
	var database string
	if err := admin.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		_ = admin.Close()
		t.Fatalf("read database identity: %v", err)
	}
	if !strings.HasPrefix(database, "handoff_b456_review_") {
		_ = admin.Close()
		t.Fatalf("refusing non-review database (verified database name has wrong prefix)")
	}
	schema := fmt.Sprintf("b456_role_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		_ = admin.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse review DSN URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	st, err := Open(parsed.String())
	if err != nil {
		t.Fatalf("open isolated ledger schema: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var currentSchema string
	if err := st.db.QueryRow(`SELECT current_schema()`).Scan(&currentSchema); err != nil || currentSchema != schema {
		t.Fatalf("search_path verification failed; refusing test writes: schema=%q err=%v", currentSchema, err)
	}
	return st, schema, parsed.String()
}

func TestB456PostgresSchemaVersionTwoInstallsResidentRoundIndex(t *testing.T) {
	st, schema, scopedDSN := newB456SessionRolesPGScoped(t)
	if _, err := st.db.Exec(`DROP INDEX idx_resident_round_session_seq`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE ledger_meta SET value='2' WHERE key=$1`, schemaVersionKey); err != nil {
		t.Fatal(err)
	}
	if got := readSchemaMarker(t, st.db); got != 2 {
		t.Fatalf("precondition schema marker=%d, want 2", got)
	}
	var exists bool
	if err := st.db.QueryRow(`SELECT to_regclass('idx_resident_round_session_seq') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("precondition index must be absent: exists=%t err=%v", exists, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(scopedDSN)
	if err != nil {
		t.Fatalf("formal Open should upgrade version-2 schema: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	var current string
	if err := reopened.db.QueryRow(`SELECT current_schema()`).Scan(&current); err != nil || current != schema {
		t.Fatalf("reopened store left isolated schema: current=%q want=%q err=%v", current, schema, err)
	}
	if got := readSchemaMarker(t, reopened.db); got != ledgerSchemaVersion {
		t.Fatalf("schema marker after upgrade=%d want=%d", got, ledgerSchemaVersion)
	}
	if err := reopened.db.QueryRow(`SELECT to_regclass('idx_resident_round_session_seq') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("formal Open failed to install new resident-round index: exists=%t err=%v", exists, err)
	}
	t.Logf("schema upgrade old_marker=2 new_marker=%d index_installed=%t schema=%s", ledgerSchemaVersion, exists, schema)
}

func TestSessionRoleActivityPostgresTwinAndExplain(t *testing.T) {
	st := newB456SessionRolesPG(t)
	if _, err := st.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	card, err := st.CreateCard(NewCard{Title: "B456 PG role query", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RecordDispatchAndLinkTask(card.ID, DispatchSnapshot{Target: "pg-worker", TaskID: "pg-task", Purpose: "implement"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	previous := 0
	for _, count := range []int{500, 2000} {
		tx, err := st.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := previous + 1; i <= count; i++ {
			taskType := "progress"
			if i == 500 {
				taskType = "completed"
			}
			payload := fmt.Sprintf(`{"node":"implement","attempt":"","task_type":%q,"payload":{"n":%d}}`, taskType, i)
			if _, err := tx.Exec(st.q(`INSERT INTO card_events (card_id,type,actor,payload,source_target,source_task,source_seq,created_at) VALUES (?,?,?,?,?,?,?,?)`),
				card.ID, EvTaskMirrored, "mirror", payload, "pg-worker", "pg-task", i, st.tval(time.Now().UTC())); err != nil {
				_ = tx.Rollback()
				t.Fatalf("seed %d card-scoped mirror facts: %v", count, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		query, args := buildSessionRoleActivityQuery(st.dialect, []string{card.ID}, 100)
		rows, err := st.db.QueryContext(ctx, st.q("EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "+query), args...)
		if err != nil {
			t.Fatalf("EXPLAIN ANALYZE rows=%d: %v", count, err)
		}
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
		t.Logf("EXPLAIN_ANALYZE rows=%d\n%s", count, strings.Join(plan, "\n"))
		started := time.Now()
		events, truncated, err := st.SessionRoleActivityContext(ctx, []string{card.ID}, 100)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatalf("role query rows=%d: %v", count, err)
		}
		if len(events) > 106 || len(truncated) != 1 {
			t.Fatalf("bounded twin rows=%d returned=%d truncated=%v", count, len(events), truncated)
		}
		foundDispatch, foundRaw, foundLifecycle := false, false, false
		for _, event := range events {
			if event.Type == EvDispatched {
				var payload struct {
					TaskID string `json:"task_id"`
					Target string `json:"target"`
				}
				if err := json.Unmarshal(event.Payload, &payload); err == nil && payload.TaskID == "pg-task" && payload.Target == "pg-worker" {
					foundDispatch = true
				}
			}
			if event.Type == EvTaskMirrored && event.SourceTarget == "pg-worker" && event.SourceTask == "pg-task" {
				if event.SourceSeq == int64(count) {
					foundRaw = true
				}
				if count >= 500 && event.SourceSeq == 500 {
					foundLifecycle = true
				}
			}
		}
		if !foundDispatch || !foundRaw || (count >= 500 && !foundLifecycle) {
			t.Fatalf("PG twin lost current facts rows=%d dispatch=%t raw=%t lifecycle=%t", count, foundDispatch, foundRaw, foundLifecycle)
		}
		t.Logf("bounded_query rows=%d returned=%d elapsed=%s", count, len(events), elapsed)
		previous = count
	}
}

func TestSessionRoleTicketsPostgresTwinIsCardScoped(t *testing.T) {
	st := newB456SessionRolesPG(t)
	if _, err := st.PutWorkflow("bug", WorkflowDef{States: []string{StatusTodo, StatusDoing, StatusDone}}); err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateCard(NewCard{Title: "ticket card one", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateCard(NewCard{Title: "ticket card two", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	seeds := []struct {
		card                 Card
		target, task, ticket string
	}{
		{first, "pg-target-a", "pg-task-a", "pg-ticket-a"},
		{second, "pg-target-b", "pg-task-b", "pg-ticket-b"},
	}
	for _, seed := range seeds {
		if _, err := st.AppendMirroredEvent(seed.card.ID, MirroredEvent{Target: seed.target, Task: seed.task, SourceSeq: 1, Type: "permission_request", Payload: []byte(`{"ticket_id":"` + seed.ticket + `"}`), CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	refs, truncated, err := st.SessionRoleTicketsContext(context.Background(), []string{first.ID, second.ID}, 20)
	if err != nil || truncated || len(refs) != 2 {
		t.Fatalf("PG ticket twin both cards: refs=%+v truncated=%t err=%v", refs, truncated, err)
	}
	for _, seed := range seeds {
		one, cut, err := st.SessionRoleTicketsContext(context.Background(), []string{seed.card.ID}, 20)
		if err != nil || cut || len(one) != 1 || one[0].CardID != seed.card.ID || one[0].Target != seed.target || one[0].TaskID != seed.task || one[0].TicketID != seed.ticket {
			t.Fatalf("PG single-card ticket isolation: seed=%+v refs=%+v truncated=%t err=%v", seed, one, cut, err)
		}
	}
}

func TestSessionResidentRoundPostgresTwin(t *testing.T) {
	st := newB456SessionRolesPG(t)
	session, err := st.CreateSession("resident round twin", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"start", "fail"} {
		if _, err := st.RecordSessionResidentRound(session.ID, "agent:coord", "pg-round", phase, "bounded reason"); err != nil {
			t.Fatalf("append %s: %v", phase, err)
		}
	}
	other, err := st.CreateSession("other session", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordSessionResidentRound(other.ID, "agent:coord", "other-round", "start", ""); err != nil {
		t.Fatal(err)
	}
	// Give the planner enough unrelated history to prove the session predicate
	// uses the dedicated partial expression index, not a global event scan.
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		otherSession := fmt.Sprintf("session:unrelated-%d", i)
		if i < 120 {
			otherSession = session.ID
		}
		payload := fmt.Sprintf(`{"session":%q,"identity":"agent:other","round_id":"r-%d","phase":"start"}`, otherSession, i)
		if _, err := tx.Exec(`INSERT INTO card_events (type,actor,payload,created_at) VALUES ('resident_round','agent:other',$1,$2)`, payload, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed unrelated PG resident rounds: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`ANALYZE card_events`); err != nil {
		t.Fatal(err)
	}
	query := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE card_id IS NULL AND type='resident_round' AND payload->>'session'=$1 AND payload->>'identity'=$2 ORDER BY seq DESC LIMIT $3`
	planRows, err := st.db.Query(`EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) `+query, session.ID, "agent:coord", 101)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			_ = planRows.Close()
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := planRows.Err(); err != nil {
		_ = planRows.Close()
		t.Fatal(err)
	}
	_ = planRows.Close()
	joinedPlan := strings.Join(plan, "\n")
	if !strings.Contains(joinedPlan, "idx_resident_round_session_seq") {
		t.Fatalf("PG resident round query missed its session index:\n%s", joinedPlan)
	}
	t.Logf("resident_round_explain\n%s", joinedPlan)
	events, truncated, err := st.SessionResidentRoundEventsContext(context.Background(), session.ID, "agent:coord", 10)
	if err != nil || truncated || len(events) != 2 {
		t.Fatalf("PG resident round JSON session filter: events=%+v truncated=%t err=%v", events, truncated, err)
	}
	var payloads []residentRoundPayload
	for _, event := range events {
		var payload residentRoundPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	if payloads[0].RoundID != "pg-round" || payloads[0].Phase != "start" || payloads[1].RoundID != "pg-round" || payloads[1].Phase != "fail" {
		t.Fatalf("PG resident round ordering/payload mismatch: %+v", payloads)
	}
}

// Compile-time guard for the driver required by this opt-in test.
var _ *sql.DB
