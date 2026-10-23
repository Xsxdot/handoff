// B469 notice persistence is shared across agents. These tests use a fresh PG
// schema and independent stores; SQLite cannot prove cross-connection ordering.
package ledger

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func b469IsolatedPGStores(t *testing.T) (*Store, *Store, string) {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LEDGER_TEST_PG_DSN is unset; B469 real PG evidence not collected")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("invalid PG test connection configuration")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("cannot create PG test connection")
	}
	schema := fmt.Sprintf("b469_notice_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal("cannot create isolated B469 PG schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("cannot remove owned B469 test schema %s", schema)
		}
		_ = admin.Close()
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	isolated := u.String()
	first, err := Open(isolated)
	if err != nil {
		t.Fatal("cannot open first isolated B469 ledger")
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(isolated)
	if err != nil {
		t.Fatal("cannot open second isolated B469 ledger")
	}
	t.Cleanup(func() { _ = second.Close() })
	var currentSchema string
	if err := first.db.QueryRow(`SELECT current_schema()`).Scan(&currentSchema); err != nil || currentSchema != schema {
		t.Fatal("PG ledger did not honor isolated test search_path")
	}
	return first, second, isolated
}

func TestB469TicketNoticePGConcurrentReplayAndOwnership(t *testing.T) {
	first, second, isolated := b469IsolatedPGStores(t)
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:1", Identity: "agent:owner"}
	input := TaskTicketNoticeState{SourceMachine: "source-a", TaskID: "original-task", TicketID: "original-task:p1",
		SourceSeq: 41, TicketKind: "gate", Target: &target}
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		st := first
		if i%2 == 1 {
			st = second
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.PutTaskTicketNotice(input, "b469-test")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent same-ticket replay failed: %v", err)
		}
	}
	events, err := first.EventsFromAsc(nil, 0, 100)
	if err != nil || len(events) != 1 || events[0].Type != EvTaskTicketNotice {
		t.Fatalf("same-ticket replay must emit exactly one notice: count=%d err=%v", len(events), err)
	}
	if err := second.SetTaskTicketNoticeStatus(input.SourceMachine, input.TaskID, input.TicketID, 1, "delivered", "", ""); err != nil {
		t.Fatal(err)
	}
	got, changed, err := first.PutTaskTicketNotice(input, "b469-test")
	if err != nil || changed || got.Status != "delivered" || got.OwnershipVersion != 1 || got.TicketKind != "gate" {
		t.Fatalf("source replay regressed delivered record: %+v changed=%v err=%v", got, changed, err)
	}
	other := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:1", Identity: "agent:new-owner"}
	input.Target = &other
	input.SourceSeq = 99
	got, changed, err = second.PutTaskTicketNotice(input, "b469-test")
	if err != nil || !changed || got.OwnershipVersion != 2 || got.Status != "pending" {
		t.Fatalf("ownership change must produce a new pending version: %+v changed=%v err=%v", got, changed, err)
	}
	if err := first.SetTaskTicketNoticeStatus(input.SourceMachine, input.TaskID, input.TicketID, 1, "delivered", "", ""); err == nil {
		t.Fatal("prior owner must not acknowledge new owner delivery")
	}
	input.SourceSeq = 42
	input.Target = &target
	if _, _, err := first.PutTaskTicketNotice(input, "b469-test"); err == nil {
		t.Fatal("older source replay must be rejected")
	}
	got, err = second.GetTaskTicketNotice(input.SourceMachine, input.TaskID, input.TicketID)
	if err != nil || got.SourceSeq != 99 || got.OwnershipVersion != 2 || got.Target == nil || *got.Target != other {
		t.Fatalf("rejected source replay must not steal current recipient: %+v err=%v", got, err)
	}
	if err := second.SetTaskTicketNoticeStatus(input.SourceMachine, input.TaskID, input.TicketID, 2, "answered", "ticket_answered", ""); err != nil {
		t.Fatal(err)
	}
	input.SourceSeq = 100
	got, changed, err = first.PutTaskTicketNotice(input, "b469-test")
	if err != nil || changed || got.Status != "answered" || got.OwnershipVersion != 2 {
		t.Fatalf("source replay must not revive answered ticket: %+v changed=%v err=%v", got, changed, err)
	}
	reopened, err := Open(isolated)
	if err != nil {
		t.Fatal("cannot reopen isolated B469 ledger")
	}
	defer reopened.Close()
	got, err = reopened.GetTaskTicketNotice(input.SourceMachine, input.TaskID, input.TicketID)
	if err != nil || got.Status != "answered" || got.TicketKind != "gate" || got.OwnershipVersion != 2 {
		t.Fatalf("notice must survive a new ledger connection: %+v err=%v", got, err)
	}
}

func TestB469TicketNoticePGUnknownOriginRemainsVisible(t *testing.T) {
	first, second, _ := b469IsolatedPGStores(t)
	input := TaskTicketNoticeState{SourceMachine: "source-a", TaskID: "unlinked-task", TicketID: "unlinked-task:q1",
		SourceSeq: 1, TicketKind: "ask", Reason: "origin_unknown"}
	got, _, err := first.PutTaskTicketNotice(input, "b469-test")
	if err != nil || got.Status != "needs_human" || got.Target != nil {
		t.Fatalf("unknown origin needs an independent visible record: %+v err=%v", got, err)
	}
	events, err := second.EventsFromAsc(nil, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("no target must not broadcast an event: count=%d err=%v", len(events), err)
	}
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:1", Identity: "agent:owner"}
	input.Target = &target
	got, changed, err := second.PutTaskTicketNotice(input, "b469-test")
	if err != nil || !changed || got.Status != "pending" || got.OwnershipVersion != 2 {
		t.Fatalf("explicit recipient recovery must use same original ticket: %+v changed=%v err=%v", got, changed, err)
	}
	var links int
	if err := first.db.QueryRow(`SELECT count(*) FROM card_tasks`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("unlinked notice must not fabricate card task links: links=%d err=%v", links, err)
	}
	if err := first.SetTaskTicketNoticeStatus(input.SourceMachine, input.TaskID, input.TicketID, 2, "needs_human", "delivery_failed", "synthetic transport failure"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	for _, st := range []*Store{first, second} {
		go func() {
			<-start
			_, retried, err := st.RetryTaskTicketNotice(input.SourceMachine, input.TaskID, input.TicketID, 2, "b469-test")
			results <- retried
			failures <- err
		}()
	}
	close(start)
	retries := 0
	for i := 0; i < 2; i++ {
		if <-results {
			retries++
		}
		if err := <-failures; err != nil {
			t.Fatalf("concurrent retry failed: %v", err)
		}
	}
	if retries != 1 {
		t.Fatalf("concurrent explicit retries must emit once, got %d", retries)
	}
	events, err = first.EventsFromAsc(nil, 0, 100)
	if err != nil || len(events) != 2 {
		t.Fatalf("initial recipient plus explicit retry should be two events: count=%d err=%v", len(events), err)
	}
	if err := second.SetTaskTicketNoticeStatus(input.SourceMachine, input.TaskID, input.TicketID, 2, "answered", "ticket_answered", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.RetryTaskTicketNotice(input.SourceMachine, input.TaskID, input.TicketID, 2, "b469-test"); err == nil {
		t.Fatal("explicit retry must not revive an answered ticket")
	}
}
