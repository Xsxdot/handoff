package agentd

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

// TestTaskTicketNoticeReparsesCurrentResident locks the no-card fallback to
// the registered resident and proves pending notices move when that fact changes.
func TestTaskTicketNoticeReparsesCurrentResident(t *testing.T) {
	env := newLedgerEnv(t)
	room, err := env.ledger.CreateSession("notice origin", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	eventPayload, _ := json.Marshal(map[string]string{"ticket_id": "task-1:p1", "kind": "ask"})
	sourceEvent := proto.Event{Seq: 7, TaskID: "task-1", Type: proto.EventTypeQuestion, Payload: eventPayload}
	if err := env.srv.RecordTaskTicketNotice("source-a", "task-1", "", room.ID, sourceEvent); err != nil {
		t.Fatal(err)
	}
	first, err := env.ledger.GetTaskTicketNotice("source-a", "task-1", "task-1:p1")
	if err != nil || first.Status != "needs_human" || first.Reason != "resident_missing" {
		t.Fatalf("before resident registration: notice=%+v err=%v", first, err)
	}
	for i, identity := range []string{"agent:first", "agent:second"} {
		if i > 0 {
			if err := env.ledger.StandResident(room.ID, "agent:first"); err != nil {
				t.Fatalf("stand previous resident: %v", err)
			}
		}
		if _, err := env.ledger.SitResident(ledger.SessionResident{Session: room.ID, Identity: identity}); err != nil {
			t.Fatalf("register resident %s: %v", identity, err)
		}
		current, err := env.ledger.GetTaskTicketNotice("source-a", "task-1", "task-1:p1")
		if err != nil {
			t.Fatal(err)
		}
		if err := env.srv.RefreshTaskTicketNotice("source-a", "task-1", "", room.ID, current); err != nil {
			t.Fatalf("refresh for resident %s: %v", identity, err)
		}
	}
	final, err := env.ledger.GetTaskTicketNotice("source-a", "task-1", "task-1:p1")
	if err != nil || final.Status != "pending" || final.OwnershipVersion != 3 || final.Target == nil || final.Target.Identity != "agent:second" {
		t.Fatalf("after resident changes: notice=%+v err=%v", final, err)
	}
	events, err := env.ledger.EventsFromAsc(nil, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == ledger.EvTaskTicketNotice {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("address changes should emit two addressed events, got %d", count)
	}
}

func TestTaskTicketNoticeKeepsStandalonePullCard(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	card, err := env.ledger.CreateCard(ledger.NewCard{Title: "standalone", Project: "p", Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	seat := "cli:codex#standalone"
	if err := env.ledger.BindSeatMode(card.ID, seat, proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	if session, err := env.ledger.SessionOfCard(card.ID); err != nil || session != "" {
		t.Fatalf("precondition session=%q err=%v", session, err)
	}
	payload, _ := json.Marshal(map[string]string{"ticket_id": "task:q1", "kind": "ask"})
	if err := env.srv.RecordTaskTicketNotice("source", "task", card.ID, "", proto.Event{Seq: 1, TaskID: "task", Type: proto.EventTypeQuestion, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	got, err := env.ledger.GetTaskTicketNotice("source", "task", "task:q1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Target == nil || got.Target.Kind != proto.DeliverySeat || got.Target.Session != "" || got.Target.Card != card.ID || got.Target.Identity != seat {
		t.Fatalf("standalone pull seat lost: status=%q reason=%q target=%+v", got.Status, got.Reason, got.Target)
	}
}

func TestTaskTicketNoticeSkipsInvalidPullSeatForPushAncestor(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	parent, err := env.ledger.CreateCard(ledger.NewCard{Title: "parent", Project: "p", Workflow: "attachment-gates", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	parentSeat := "cli:opencode#parent"
	if err := env.ledger.BindSeatMode(parent.ID, parentSeat, proto.SeatSourceCoordinate, proto.SeatModePush,
		ledger.SeatBearing{Carrier: "carrier", Machine: "local"}); err != nil {
		t.Fatal(err)
	}
	child, err := env.ledger.CreateCard(ledger.NewCard{Title: "child", Project: "p", Workflow: "attachment-gates", Parent: parent.ID, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", env.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE cards SET driver_mode='pull' WHERE id=?", child.ID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"ticket_id": "task:q1", "kind": "ask"})
	if err := env.srv.RecordTaskTicketNotice("source", "task", child.ID, "", proto.Event{Seq: 1, TaskID: "task", Type: proto.EventTypeQuestion, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	got, err := env.ledger.GetTaskTicketNotice("source", "task", "task:q1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Target == nil || got.Target.Kind != proto.DeliverySeat || got.Target.Card != parent.ID || got.Target.Identity != parentSeat {
		t.Fatalf("invalid pull should ascend to push parent: status=%q reason=%q target=%+v", got.Status, got.Reason, got.Target)
	}
}

func TestRetryTaskTicketNotificationHTTPRoundTrip(t *testing.T) {
	env := newLedgerEnv(t)
	env.srv.Conf()().Name = "source-a"
	room, err := env.ledger.CreateSession("retry origin", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	taskID, ticketID := uuid.NewString(), "ticket:retry"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		State: proto.TaskStateWaitingAnswer, OriginSession: room.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]string{"kind": "ask", "question": "continue?"})
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: ticketID, TaskID: taskID, Kind: "ask", Request: request, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	question, _ := json.Marshal(map[string]string{"ticket_id": ticketID, "kind": "ask"})
	sourceEvent, err := env.st.AppendEvent(taskID, proto.EventTypeQuestion, json.RawMessage(question))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.RecordTaskTicketNotice("source-a", taskID, "", room.ID, sourceEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.SitResident(ledger.SessionResident{Session: room.ID, Identity: "agent:owner"}); err != nil {
		t.Fatal(err)
	}
	cl := client.New(env.ts.URL, env.token)
	if code, _ := ledgerPost(t, env.testAgentdEnv,
		"/api/tasks/"+taskID+"/tickets/"+ticketID+"/notification/retry", `{"target":"caller-controlled"}`); code != 400 {
		t.Fatalf("non-empty retry request must be rejected, got %d", code)
	}
	got, err := cl.RetryTaskTicketNotification(context.Background(), taskID, ticketID)
	if err != nil || got == nil || got.Status != "pending" || got.OwnershipVersion != 2 {
		t.Fatalf("retry result=%+v err=%v", got, err)
	}
	if _, err := cl.RetryTaskTicketNotification(context.Background(), taskID, "unknown-ticket"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("unknown ticket should return HTTP 404, got %v", err)
	}
	if _, err := cl.RetryTaskTicketNotification(context.Background(), "missing-task", ticketID); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing task should return HTTP 404, got %v", err)
	}
	if err := env.st.AnswerTicket(ticketID, "answer"); err != nil {
		t.Fatal(err)
	}
	answeredPayload, _ := json.Marshal(map[string]string{"ticket_id": ticketID, "answer": "answer"})
	if _, err := env.st.AppendEvent(taskID, proto.EventTypeTicketAnswered, json.RawMessage(answeredPayload)); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.RetryTaskTicketNotification(context.Background(), taskID, ticketID); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("answered ticket should return HTTP 409, got %v", err)
	}
	closed, err := env.ledger.GetTaskTicketNotice("source-a", taskID, ticketID)
	if err != nil || closed.Status != "answered" || closed.Reason != "ticket_answered" {
		t.Fatalf("answered source truth did not close notice: %+v err=%v", closed, err)
	}
}

func TestNamelessLocalTaskTicketNoticeProjectionAndRetry(t *testing.T) {
	env := newLedgerEnv(t)
	if env.srv.Conf()().Name != "" {
		t.Fatal("precondition: server must have no configured self name")
	}
	machine := env.srv.localMachineName()
	room, err := env.ledger.CreateSession("nameless retry origin", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	taskID, ticketID := uuid.NewString(), "ticket:nameless"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		State: proto.TaskStateWaitingAnswer, OriginSession: room.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]string{"kind": "ask", "question": "continue?"})
	if _, err := env.st.CreateTicket(&proto.Ticket{ID: ticketID, TaskID: taskID, Kind: "ask", Request: request, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	question, _ := json.Marshal(map[string]string{"ticket_id": ticketID, "kind": "ask"})
	sourceEvent, err := env.st.AppendEvent(taskID, proto.EventTypeQuestion, json.RawMessage(question))
	if err != nil {
		t.Fatal(err)
	}
	if err := env.srv.RecordTaskTicketNotice(machine, taskID, "", room.ID, sourceEvent); err != nil {
		t.Fatal(err)
	}
	stored, err := env.ledger.GetTaskTicketNotice(machine, taskID, ticketID)
	if err != nil || stored.Status != "needs_human" {
		t.Fatalf("precondition notice=%+v err=%v", stored, err)
	}

	raw, code := env.getRaw(t, "/api/tasks/"+taskID)
	if code != http.StatusOK {
		t.Fatalf("detail status=%d want 200", code)
	}
	var detail taskDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	var projected *proto.TicketNotification
	for i := range detail.PendingTickets {
		if detail.PendingTickets[i].ID == ticketID {
			projected = detail.PendingTickets[i].Notification
		}
	}
	cl := client.New(env.ts.URL, env.token)
	retried, err := cl.RetryTaskTicketNotification(context.Background(), taskID, ticketID)
	if projected == nil || projected.Status != "needs_human" {
		t.Errorf("nameless task detail must project source notice: %+v", projected)
	}
	if err != nil || retried == nil || retried.Status != "needs_human" {
		t.Errorf("nameless retry result=%+v err=%v", retried, err)
	}
	sourceTicket, err := env.st.GetTicket(ticketID)
	if err != nil || sourceTicket.Answer != nil {
		t.Fatalf("notification retry must leave original ticket unanswered: ticket=%+v err=%v", sourceTicket, err)
	}
	if _, err := env.ledger.GetTaskTicketNotice(machine, taskID, ticketID); err != nil {
		t.Fatalf("retry lost hostname-keyed notice: %v", err)
	}
}
