package agentd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestTaskTicketNoticeWakeRequiresCurrentLocalAddress exercises the production
// event mapper: only the explicit current local target may enter a wake path.
func TestTaskTicketNoticeWakeRequiresCurrentLocalAddress(t *testing.T) {
	env := newLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	room, err := env.ledger.CreateSession("notice", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.ledger.SitResident(ledger.SessionResident{Session: room.ID, Identity: "agent:desk", DriverMode: proto.ResidentDriverPush, PushSession: "push-session"})
	if err != nil {
		t.Fatal(err)
	}
	currentTarget := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: room.ID, Identity: "agent:desk"}
	if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: "task", TicketID: "task:p1", TicketKind: "ask", SourceSeq: 1, Target: &currentTarget}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		target proto.DeliveryAddress
		want   int
	}{
		{name: "local push resident", target: currentTarget, want: 1},
		{name: "other resident", target: proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: room.ID, Identity: "agent:other"}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(proto.TaskTicketNotice{SourceMachine: "source", TaskID: "task", TicketID: "task:p1", SourceSeq: 1, OwnershipVersion: 1, Target: tc.target, TicketKind: "ask"})
			wakes, err := env.srv.automationWakeEvents(proto.LedgerEvent{Seq: 2, Type: ledger.EvTaskTicketNotice, Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			if len(wakes) != tc.want {
				t.Fatalf("wakes=%#v want %d", wakes, tc.want)
			}
		})
	}
}

func TestDesktopNoticeOpenFailureRemainsHumanVisible(t *testing.T) {
	env := newLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	room, err := env.ledger.CreateSession("desktop notice", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	const identity = "agent:desktop"
	o2SitResident(t, env, room.ID, identity, o2DesktopSession, o2CLISession, "")
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: room.ID, Identity: identity}
	_, _, err = env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: "task-desktop", TicketID: "task-desktop:p1", TicketKind: "ask", SourceSeq: 9, Target: &target}, "test")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var calls int
	env.srv.desktopHome = home
	env.srv.desktopOpen = func(string) error {
		calls++
		if calls == 1 {
			return errors.New("injected opener failure")
		}
		return nil
	}
	events, err := env.ledger.EventsFromAsc(nil, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	wireEvents := make([]proto.LedgerEvent, 0, len(events))
	for _, event := range events {
		wireEvents = append(wireEvents, proto.LedgerEvent{Seq: event.Seq, CardID: event.CardID, Type: event.Type, Actor: event.Actor, Payload: event.Payload, SourceTarget: event.SourceTarget, SourceTask: event.SourceTask, SourceSeq: event.SourceSeq, CreatedAt: event.CreatedAt})
	}
	if err := env.srv.ringResidentDoorbells(0, wireEvents); err != nil {
		t.Fatal(err)
	}
	state, err := env.ledger.GetTaskTicketNotice("source", "task-desktop", "task-desktop:p1")
	if err != nil || state.Status != "needs_human" {
		t.Fatalf("failed ring must remain visible/retryable: state=%+v err=%v", state, err)
	}
	maxSeq, err := env.ledger.MaxSeq()
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.ringResidentDoorbells(maxSeq, nil); err != nil {
		t.Fatal(err)
	}
	state, err = env.ledger.GetTaskTicketNotice("source", "task-desktop", "task-desktop:p1")
	if err != nil || state.Status != "needs_human" || state.LastError != "desktop_open_failed" {
		t.Fatalf("unopened notice must remain visible for a human retry: state=%+v err=%v", state, err)
	}
	if calls != 1 {
		t.Fatalf("opener calls=%d want 1 (do not rewrite an already-dropped letter)", calls)
	}
	entries, err := os.ReadDir(filepath.Join(home, proto.DesktopWakeDirRel))
	if err != nil || len(entries) != 1 {
		t.Fatalf("desktop letters=%d err=%v", len(entries), err)
	}
}

func TestInvalidTicketNoticeWireDoesNotRingOrAcknowledge(t *testing.T) {
	env := newLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	room, err := env.ledger.CreateSession("invalid notice", "user:test", "user:test")
	if err != nil {
		t.Fatal(err)
	}
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: room.ID, Identity: "agent:desktop"}
	if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: "task-invalid", TicketID: "task-invalid:p1", TicketKind: "ask", SourceSeq: 5, Target: &target}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*proto.TaskTicketNotice)
	}{
		{name: "unknown kind", mutate: func(n *proto.TaskTicketNotice) { n.TicketKind = "future-kind" }},
		{name: "zero source seq", mutate: func(n *proto.TaskTicketNotice) { n.SourceSeq = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := proto.TaskTicketNotice{SourceMachine: "source", TaskID: "task-invalid", TicketID: "task-invalid:p1", SourceSeq: 5, OwnershipVersion: 1, Target: target, TicketKind: "ask"}
			tc.mutate(&n)
			payload, _ := json.Marshal(n)
			wake, hit, mapErr := env.srv.taskTicketNoticeWake(proto.LedgerEvent{Seq: 9, Type: ledger.EvTaskTicketNotice, Payload: payload})
			if mapErr != nil || hit {
				t.Fatalf("invalid notice wire must be skipped without blocking cursor: wake=%+v hit=%v err=%v", wake, hit, mapErr)
			}
			if err := env.srv.ringResidentDoorbells(0, []proto.LedgerEvent{{Seq: 9, Type: ledger.EvTaskTicketNotice, Payload: payload}}); err != nil {
				t.Fatalf("invalid notice wire must not block desktop intake cursor: %v", err)
			}
			state, err := env.ledger.GetTaskTicketNotice("source", "task-invalid", "task-invalid:p1")
			if err != nil || state.Status != "pending" {
				t.Fatalf("invalid wire changed notice status: %+v err=%v", state, err)
			}
		})
	}
}

func TestUnknownTicketNoticeDoesNotBlockLaterValidNotice(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)
	first, second := "invalid-task", "valid-task"
	for _, taskID := range []string{first, second} {
		address := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session, Identity: b444Identity}
		if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: taskID,
			TicketID: taskID + ":p1", TicketKind: "ask", SourceSeq: 1, Target: &address}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", env.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE card_events SET payload = json_set(payload, '$.ticket_kind', 'future-kind')
		WHERE seq = (SELECT MIN(seq) FROM card_events WHERE type='task_ticket_notice')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("unknown notice blocked following valid event: %v", err)
	}
	invalid, err := env.ledger.GetTaskTicketNotice("source", first, first+":p1")
	if err != nil || invalid.Status != "pending" {
		t.Fatalf("invalid event must be skipped without acknowledging its source: state=%+v err=%v", invalid, err)
	}
	valid, err := env.ledger.GetTaskTicketNotice("source", second, second+":p1")
	if err != nil || valid.Status != "delivered" {
		t.Fatalf("valid event after unknown kind must still be consumed: state=%+v err=%v", valid, err)
	}
	if len(b444ResidentResumes(runner)) != 1 {
		t.Fatalf("following valid notice should trigger exactly one resident resume, got %d", len(b444ResidentResumes(runner)))
	}
}

func TestResolveWakeCardSkipsInvalidOriginPullButPreservesValidPull(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	parent := createCoordCard(t, env)
	b435BindPush(t, env, parent, "resolver-parent")
	child := splitCoordChild(t, env, parent)
	b435BindPull(t, env, child, "resolver-child")
	if got := env.srv.resolveWakeCard(child); got != "" {
		t.Fatalf("valid origin pull must remain on its own wait path, got %q", got)
	}
	b435ExecSQL(t, env, `UPDATE cards SET driver_session = '', driver_source = '' WHERE id = ?`, child)
	if got := env.srv.resolveWakeCard(child); got != parent {
		t.Fatalf("invalid origin pull should be skipped for push ancestor: got %q want %q", got, parent)
	}
	b435ExecSQL(t, env, `UPDATE cards SET driver_mode = 'push' WHERE id = ?`, child)
	if got := env.srv.resolveWakeCard(child); got != parent {
		t.Fatalf("empty origin push is not a recipient and should be skipped for push ancestor: got %q want %q", got, parent)
	}
}

func TestTaskTicketNoticeCardPushConsumesAndAcknowledges(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	identity := b435BindPush(t, env, card, "notice-card")
	session := b435Session(t, env, card)
	drainAutomation(t, env)
	target := proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: session, Card: card, Identity: identity}
	if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: "card-task", TicketID: "card-task:p1", TicketKind: "gate", SourceSeq: 11, Target: &target}, "test"); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)
	state, err := env.ledger.GetTaskTicketNotice("source", "card-task", "card-task:p1")
	if err != nil || state.Status != "delivered" {
		t.Fatalf("card push notice not acknowledged after short round: state=%+v err=%v", state, err)
	}
	if len(b435Resumes(runner)) != 1 {
		t.Fatalf("card push should receive one automatic wake, got %d", len(b435Resumes(runner)))
	}
	_, prompts, _ := runner.snapshot()
	if len(prompts) != 1 {
		t.Fatalf("card push prompts=%d want 1", len(prompts))
	}
	for _, anchor := range []string{"source_machine=source", "task_id=card-task", "ticket_id=card-task:p1", "ticket_kind=gate", "handoff show card-task --target source"} {
		if !strings.Contains(prompts[0], anchor) {
			t.Fatalf("card push prompt missing %q: %s", anchor, prompts[0])
		}
	}
}

func TestTaskTicketNoticeSuppressesLaterLegacyMirrorWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	identity := b435BindPush(t, env, card, "notice-dedupe")
	session := b435Session(t, env, card)
	drainAutomation(t, env)
	target := proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: session, Card: card, Identity: identity}
	if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "consumer-target", TaskID: "same-task", TicketID: "same-task:p1", TicketKind: "ask", SourceSeq: 1, Target: &target}, "test"); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)
	if got := len(b435Resumes(runner)); got != 1 {
		t.Fatalf("notice should produce one short wake, got %d", got)
	}
	appendMirroredForConsumer(t, env.ledger, card, "same-task", "question", 1, `{"ticket_id":"same-task:p1"}`)
	drainAutomation(t, env)
	if got := len(b435Resumes(runner)); got != 1 {
		t.Fatalf("legacy mirror for the already-notified ticket duplicated wake, got %d", got)
	}
}

func TestTaskTicketNoticeResidentPushConsumesAndAcknowledges(t *testing.T) {
	env, runner := newB444Env(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	drainAutomation(t, env)
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session, Identity: b444Identity}
	if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: "resident-task", TicketID: "resident-task:p1", TicketKind: "ask", SourceSeq: 12, Target: &target}, "test"); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)
	state, err := env.ledger.GetTaskTicketNotice("source", "resident-task", "resident-task:p1")
	if err != nil || state.Status != "delivered" {
		t.Fatalf("resident push notice not acknowledged after short round: state=%+v err=%v", state, err)
	}
	if len(b444ResidentResumes(runner)) != 1 {
		t.Fatalf("resident push should receive one automatic wake, got %d", len(b444ResidentResumes(runner)))
	}
	_, prompts := runner.snapshot()
	if len(prompts) != 1 {
		t.Fatalf("resident push prompts=%d want 1", len(prompts))
	}
	for _, anchor := range []string{"source_machine=source", "task_id=resident-task", "ticket_id=resident-task:p1", "ticket_kind=ask", "handoff show resident-task --target source"} {
		if !strings.Contains(prompts[0], anchor) {
			t.Fatalf("resident push prompt missing %q: %s", anchor, prompts[0])
		}
	}
}

func TestTaskTicketNoticeDeliveryLeavesSourceGateAndAskUnanswered(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	session := b435Session(t, env)
	b444SitPush(t, env, session, "local")
	for _, kind := range []string{"gate", "ask"} {
		t.Run(kind, func(t *testing.T) {
			taskID, ticketID := "source-"+kind, "source-"+kind+":p1"
			now := time.Now().UTC()
			if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
				State: proto.TaskStateWaitingAnswer, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			request, _ := json.Marshal(map[string]string{"kind": kind, "question": "continue?"})
			if _, err := env.st.CreateTicket(&proto.Ticket{ID: ticketID, TaskID: taskID, Kind: kind, Request: request, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			address := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: session, Identity: b444Identity}
			if _, _, err := env.ledger.PutTaskTicketNotice(ledger.TaskTicketNoticeState{SourceMachine: "source", TaskID: taskID,
				TicketID: ticketID, TicketKind: kind, SourceSeq: 1, Target: &address}, "test"); err != nil {
				t.Fatal(err)
			}
			drainAutomation(t, env)
			notice, err := env.ledger.GetTaskTicketNotice("source", taskID, ticketID)
			if err != nil || notice.Status != "delivered" {
				t.Fatalf("notice should be delivered: notice=%+v err=%v", notice, err)
			}
			pending, err := env.st.PendingTickets(taskID)
			if err != nil || len(pending) != 1 || pending[0].ID != ticketID {
				t.Fatalf("notification delivery must leave original ticket pending: pending=%+v err=%v", pending, err)
			}
			original, err := env.st.GetTicket(ticketID)
			if err != nil || original.Answer != nil {
				t.Fatalf("notice must not answer source ticket: ticket=%+v err=%v", original, err)
			}
			if err := env.st.AnswerTicket(ticketID, "Continue"); err != nil {
				t.Fatal(err)
			}
			answered, err := env.st.GetTicket(ticketID)
			if err != nil || answered.Answer == nil || *answered.Answer != "Continue" {
				t.Fatalf("only source answer CAS should close original ticket: ticket=%+v err=%v", answered, err)
			}
		})
	}
}

func TestLegacyMirrorWithoutNoticeStillWakes(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	card := createCoordCard(t, env)
	b435BindPush(t, env, card, "legacy-mirror")
	b435Session(t, env, card)
	drainAutomation(t, env)
	appendMirroredForConsumer(t, env.ledger, card, "legacy-task", "question", 1, `{"ticket_id":"legacy-task:p1"}`)
	drainAutomation(t, env)
	if got := len(b435Resumes(runner)); got != 1 {
		t.Fatalf("legacy mirror without notice must retain old wake behavior, got %d", got)
	}
}
