package ledger

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestTaskTicketNoticeRoutingIsIdempotentAndAddressed(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:1", Identity: "user:owner"}
	first, changed, err := st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p1", SourceSeq: 41,
		TicketKind: "ask", Target: &target,
	}, "test")
	if err != nil || !changed || first.OwnershipVersion != 1 || first.Status != "pending" {
		t.Fatalf("initial routing=%+v changed=%v err=%v", first, changed, err)
	}
	second, changed, err := st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p1", SourceSeq: 41,
		TicketKind: "ask", Target: &target,
	}, "test")
	if err != nil || changed || second.OwnershipVersion != 1 {
		t.Fatalf("same target replay=%+v changed=%v err=%v", second, changed, err)
	}
	if err := st.SetTaskTicketNoticeStatus("mac-a", "task-a", "task-a:p1", 1, "delivered", "", ""); err != nil {
		t.Fatal(err)
	}
	// PG JSONB canonicalizes whitespace and key order. Route equality is semantic,
	// so a dialect's JSON representation must not create a new ownership version.
	if _, err := st.db.Exec(`UPDATE task_ticket_notices SET target=? WHERE source_machine=? AND task_id=? AND ticket_id=?`,
		`{ "identity": "user:owner", "session": "session:1", "kind": "resident" }`, "mac-a", "task-a", "task-a:p1"); err != nil {
		t.Fatal(err)
	}
	second, changed, err = st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p1", SourceSeq: 41,
		TicketKind: "ask", Target: &target,
	}, "test")
	if err != nil || changed || second.Status != "delivered" {
		t.Fatalf("same route replay must preserve delivered state: %+v changed=%v err=%v", second, changed, err)
	}
	events, err := st.EventsFromAsc(nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != EvTaskTicketNotice {
		t.Fatalf("notice events=%+v", events)
	}
	var wire proto.TaskTicketNotice
	if err := json.Unmarshal(events[0].Payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.TicketID != "task-a:p1" || wire.TicketKind != "ask" || wire.Target != target || wire.OwnershipVersion != 1 {
		t.Fatalf("wire notice=%+v", wire)
	}
	updated := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:2", Identity: "user:owner"}
	third, changed, err := st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p1", SourceSeq: 42,
		TicketKind: "ask", Target: &updated,
	}, "test")
	if err != nil || !changed || third.OwnershipVersion != 2 {
		t.Fatalf("changed routing=%+v changed=%v err=%v", third, changed, err)
	}
	if got, err := st.GetTaskTicketNotice("mac-a", "task-a", "task-a:p1"); err != nil || got.OwnershipVersion != 2 || got.Target == nil || *got.Target != updated {
		t.Fatalf("persisted routing=%+v err=%v", got, err)
	}
	if err := st.SetTaskTicketNoticeStatus("mac-a", "task-a", "task-a:p1", 1, "answered", "", ""); err == nil {
		t.Fatal("stale owner version must not update notice status")
	}
	if err := st.SetTaskTicketNoticeStatus("mac-a", "task-a", "task-a:p1", 2, "answered", "ticket_answered", ""); err != nil {
		t.Fatal(err)
	}
	closed, changed, err := st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p1", SourceSeq: 43,
		TicketKind: "ask", Target: &target,
	}, "test")
	if err != nil || changed || closed.Status != "answered" || closed.OwnershipVersion != 2 {
		t.Fatalf("replay must not revive answered notice: %+v changed=%v err=%v", closed, changed, err)
	}
	unknown, _, err := st.PutTaskTicketNotice(TaskTicketNoticeState{
		SourceMachine: "mac-a", TaskID: "task-a", TicketID: "task-a:p2", SourceSeq: 44, TicketKind: "gate",
		Reason: "origin_unknown",
	}, "test")
	if err != nil || unknown.Status != "needs_human" || unknown.Target != nil {
		t.Fatalf("no-target notice=%+v err=%v", unknown, err)
	}
}

func TestRetryTaskTicketNoticeRequeuesSameOwnerOnlyOnce(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	target := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: "session:retry", Identity: "user:owner"}
	if _, _, err := st.PutTaskTicketNotice(TaskTicketNoticeState{SourceMachine: "m", TaskID: "t", TicketID: "t:q1",
		SourceSeq: 1, TicketKind: "ask", Target: &target}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTaskTicketNoticeStatus("m", "t", "t:q1", 1, "needs_human", "delivery_failed", "offline"); err != nil {
		t.Fatal(err)
	}
	got, requeued, err := st.RetryTaskTicketNotice("m", "t", "t:q1", 1, "user")
	if err != nil || !requeued || got.Status != "pending" || got.OwnershipVersion != 1 {
		t.Fatalf("same-owner retry=%+v requeued=%v err=%v", got, requeued, err)
	}
	_, requeued, err = st.RetryTaskTicketNotice("m", "t", "t:q1", 1, "user")
	if err != nil || requeued {
		t.Fatalf("duplicate retry must not requeue a non-needs_human notice: requeued=%v err=%v", requeued, err)
	}
	events, err := st.EventsFromAsc(nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != EvTaskTicketNotice {
		t.Fatalf("explicit retry should emit exactly one fresh addressed event, got %+v", events)
	}
}

func TestTaskTicketNoticeRejectsUnknownKindAndEmptySeat(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := st.PutTaskTicketNotice(TaskTicketNoticeState{SourceMachine: "m", TaskID: "t", TicketID: "t:q1",
		SourceSeq: 1, TicketKind: "future-kind"}, "test"); err == nil {
		t.Fatal("unknown ticket kind must be rejected by the persistence boundary")
	}
	emptySeat := proto.DeliveryAddress{Kind: proto.DeliverySeat, Card: "card"}
	if _, _, err := st.PutTaskTicketNotice(TaskTicketNoticeState{SourceMachine: "m", TaskID: "t", TicketID: "t:q2",
		SourceSeq: 2, TicketKind: "ask", Target: &emptySeat}, "test"); err == nil {
		t.Fatal("empty target identity must be rejected")
	}
}
