package ledger

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSessionResidentRoundQueryUsesSessionScopedIndex(t *testing.T) {
	s := newTestStore(t)
	session, err := s.CreateSession("resident query", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSessionResidentRound(session.ID, "agent:coord", "round-1", "start", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSessionResidentRound(session.ID, "agent:coord", "round-1", "end", ""); err != nil {
		t.Fatal(err)
	}
	query := `EXPLAIN QUERY PLAN SELECT seq FROM card_events WHERE card_id IS NULL
		AND type='resident_round' AND json_extract(payload, '$.session')=? AND json_extract(payload, '$.identity')=? ORDER BY seq DESC LIMIT ?`
	rows, err := s.db.Query(query, session.ID, "agent:coord", 101)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_resident_round_session_seq") {
		t.Fatalf("resident session query did not use its partial index:\n%s", joined)
	}
	// Later activity from a different resident must not evict the current
	// resident's lifecycle facts from the bounded result window.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		payload := `{"session":"` + session.ID + `","identity":"agent:other","round_id":"other-` + fmt.Sprint(i) + `","phase":"start"}`
		if _, err := tx.Exec(`INSERT INTO card_events (type,actor,payload,created_at) VALUES ('resident_round','agent:other',?,CURRENT_TIMESTAMP)`, payload); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, pg := range []bool{true, false} {
		ddl := strings.Join(ddlStatements(pg), "\n")
		if !strings.Contains(ddl, "idx_resident_round_session_seq") {
			t.Fatalf("DDL pg=%t missing session-scoped resident-round index", pg)
		}
	}
	events, truncated, err := s.SessionResidentRoundEventsContext(context.Background(), session.ID, "agent:coord", 10)
	if err != nil || truncated || len(events) != 2 {
		t.Fatalf("round event read: events=%+v truncated=%t err=%v", events, truncated, err)
	}
}
