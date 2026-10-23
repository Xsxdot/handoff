package ledgermirror

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestTaskDiscoveryRebuildsSubscriptionsAndOnlyReadsIncrementalPages(t *testing.T) {
	st := testLedger(t)
	var requests []int64
	m := New(st, newFakeMachines(), Options{Holder: "discovery-self", SelfName: "discovery-self",
		LocalDiscovery: func(_ context.Context, after int64, limit int) (*proto.TaskDiscoveryPage, error) {
			requests = append(requests, after)
			if limit != 100 {
				t.Errorf("limit=%d, want 100", limit)
			}
			switch after {
			case 0:
				return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{
					{Seq: 1, TaskID: "bare-running", State: proto.TaskStateRunning},
					{Seq: 2, TaskID: "terminal-pending", State: proto.TaskStateCompleted, HasPendingTickets: true},
					{Seq: 3, TaskID: "terminal-history", State: proto.TaskStateFailed},
				}, NextSeq: 3, HasMore: false}, nil
			case 3:
				return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{}, NextSeq: 3}, nil
			default:
				return nil, context.Canceled
			}
		},
	})
	m.reconcile(context.Background())
	defer m.Stop()
	if len(m.subs) != 2 {
		t.Fatalf("recovered subscriptions=%v, want nonterminal bare + terminal pending", m.subs)
	}
	if _, ok := m.subs["discovery-self/bare-running"]; !ok {
		t.Fatal("bare nonterminal task without origin or current ticket was omitted")
	}
	if _, ok := m.subs["discovery-self/terminal-pending"]; !ok {
		t.Fatal("terminal task with a pending ticket was omitted")
	}
	entry, err := st.RegistryGet(taskDiscoveryCursorKind, "discovery-self")
	if err != nil {
		t.Fatal(err)
	}
	var saved discoveryCursor
	if err := json.Unmarshal(entry.Body, &saved); err != nil || saved.Seq != 3 {
		t.Fatalf("cursor=%s err=%v", entry.Body, err)
	}
	version := entry.Version
	m.reconcile(context.Background())
	entry, err = st.RegistryGet(taskDiscoveryCursorKind, "discovery-self")
	if err != nil || entry.Version != version {
		t.Fatalf("unchanged source advanced registry version: entry=%+v err=%v", entry, err)
	}
	if len(requests) != 2 || requests[0] != 0 || requests[1] != 3 {
		t.Fatalf("source query cursors=%v", requests)
	}
	if got := len(m.discovered["discovery-self"]); got != 2 {
		t.Fatalf("retained discovery tasks=%d, want only nonterminal and terminal-pending", got)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestWaitingReviewTaskKeepsDiscoveryResponsibilityAndCanEmitNextTicket(t *testing.T) {
	st := testLedger(t)
	var ticketNotices atomic.Int64
	source := func(ctx context.Context, _ client.EventStreamClient, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		for _, event := range []proto.Event{
			{Seq: 1, Type: proto.EventTypeCompleted, Payload: []byte(`{"summary":"turn done"}`)},
			{Seq: 2, Type: proto.EventTypeQuestion, Payload: []byte(`{"ticket_id":"next-question"}`)},
		} {
			if event.Seq <= fromSeq {
				continue
			}
			event.TaskID = taskID
			if err := onEvent(event); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(st, newFakeMachines(), Options{Holder: "review-cache", SelfName: "review-cache", Source: source, LocalSource: source,
		LocalDiscovery: func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error) {
			return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "review-task",
				State: proto.TaskStateWaitingReview, OriginSession: "sid:review"}}, NextSeq: 1}, nil
		},
		TicketNotice: func(_, taskID, _, _ string, event proto.Event) error {
			if taskID == "review-task" && event.Type == proto.EventTypeQuestion {
				ticketNotices.Add(1)
			}
			return nil
		}})
	m.reconcile(context.Background())
	defer m.Stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && ticketNotices.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if ticketNotices.Load() != 1 {
		t.Fatalf("question notices=%d, want 1", ticketNotices.Load())
	}
	if _, ok := m.subs["review-cache/review-task"]; !ok {
		t.Fatal("waiting_review task lost its live source subscription after completed turn")
	}
	if _, ok := m.discovered["review-cache"]["review-task"]; !ok {
		t.Fatal("waiting_review task was evicted from active responsibility cache")
	}
}

func TestSourceDisconnectDoesNotReleaseDiscoveryResponsibility(t *testing.T) {
	st := testLedger(t)
	started := make(chan struct{}, 1)
	source := func(context.Context, client.EventStreamClient, string, int64, func(proto.Event) error) error {
		select {
		case started <- struct{}{}:
		default:
		}
		return errors.New("temporary source disconnect")
	}
	m := New(st, newFakeMachines(), Options{Holder: "disconnect-cache", LocalDiscovery: func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error) {
		return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "live-task", State: proto.TaskStateRunning}}, NextSeq: 1}, nil
	}, LocalSource: source})
	m.reconcile(context.Background())
	defer m.Stop()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source was not attempted")
	}
	m.reconcile(context.Background())
	if _, ok := m.discovered["disconnect-cache"]["live-task"]; !ok {
		t.Fatal("transient source disconnect removed the last-good responsibility")
	}
	if _, ok := m.subs["disconnect-cache/live-task"]; !ok {
		t.Fatal("transient source disconnect removed the retrying subscription")
	}
}

func TestTerminalPendingDiscoveryIsReleasedOnlyAfterSourceTicketCloses(t *testing.T) {
	st := testLedger(t)
	var pending atomic.Int64
	pending.Store(1)
	archived := make(chan struct{}, 1)
	source := func(ctx context.Context, _ client.EventStreamClient, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		if fromSeq == 0 {
			event := proto.Event{Seq: 1, TaskID: taskID, Type: proto.EventTypeArchived, Payload: []byte(`{}`)}
			if err := onEvent(event); err != nil {
				select {
				case archived <- struct{}{}:
				default:
				}
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(st, newFakeMachines(), Options{Holder: "pending-cache", SelfName: "pending-cache", LocalSource: source,
		LocalDiscovery: func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error) {
			return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "terminal-ticket",
				State: proto.TaskStateFailed, OriginSession: "sid:terminal", HasPendingTickets: true}}, NextSeq: 1}, nil
		},
		LocalTaskResponsibility: func(context.Context, string) (TaskResponsibilityStatus, error) {
			return TaskResponsibilityStatus{State: proto.TaskStateFailed, PendingTickets: int(pending.Load())}, nil
		},
		RefreshTicketNotice: func(string, string, string, string, ledger.TaskTicketNoticeState) error { return nil },
	})
	m.reconcile(context.Background())
	defer m.Stop()
	select {
	case <-archived:
	case <-time.After(time.Second):
		t.Fatal("archived event was not consumed")
	}
	if _, ok := m.discovered["pending-cache"]["terminal-ticket"]; !ok {
		t.Fatal("terminal task with source-side pending ticket was prematurely evicted")
	}
	pending.Store(0)
	m.reconcile(context.Background())
	if _, ok := m.discovered["pending-cache"]["terminal-ticket"]; ok {
		t.Fatal("terminal task with no source or mirrored pending tickets remained cached")
	}
	if _, ok := m.subs["pending-cache/terminal-ticket"]; ok {
		t.Fatal("terminal task subscription remained after responsibility cleared")
	}
}

func TestNormalArchiveCloseReleasesTerminalTaskResponsibility(t *testing.T) {
	st := testLedger(t)
	closed := make(chan struct{}, 1)
	source := func(context.Context, client.EventStreamClient, string, int64, func(proto.Event) error) error {
		select {
		case closed <- struct{}{}:
		default:
		}
		return client.ErrStreamNormalClosure
	}

	m := New(st, newFakeMachines(), Options{Holder: "archive-cache", SelfName: "archive-cache", LocalSource: source,
		LocalDiscovery: func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error) {
			return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "archived-task",
				State: proto.TaskStateRunning}}, NextSeq: 1}, nil
		},
		LocalTaskResponsibility: func(context.Context, string) (TaskResponsibilityStatus, error) {
			return TaskResponsibilityStatus{State: proto.TaskStateCompleted}, nil
		},
		RefreshTicketNotice: func(string, string, string, string, ledger.TaskTicketNoticeState) error { return nil },
	})
	m.reconcile(context.Background())
	defer m.Stop()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("source did not report the archive close")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		ended := m.ended["archive-cache/archived-task"]
		m.mu.Unlock()
		if ended {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.reconcile(context.Background())
	if _, ok := m.discovered["archive-cache"]["archived-task"]; ok {
		t.Fatal("archived terminal task with no pending tickets remained cached")
	}
}

func TestNormalWebSocketCloseDoesNotEndNonterminalResponsibility(t *testing.T) {
	st := testLedger(t)
	var calls atomic.Int32
	source := func(context.Context, client.EventStreamClient, string, int64, func(proto.Event) error) error {
		calls.Add(1)
		return client.ErrStreamNormalClosure
	}
	m := New(st, newFakeMachines(), Options{Holder: "normal-close-live", SelfName: "normal-close-live",
		LocalSource: source,
		LocalDiscovery: func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error) {
			return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "still-running",
				State: proto.TaskStateRunning}}, NextSeq: 1}, nil
		},
		LocalTaskResponsibility: func(context.Context, string) (TaskResponsibilityStatus, error) {
			return TaskResponsibilityStatus{State: proto.TaskStateRunning}, nil
		},
		RefreshTicketNotice: func(string, string, string, string, ledger.TaskTicketNoticeState) error { return nil },
	})
	m.reconcile(context.Background())
	defer m.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("normal close should be rechecked and retried; source calls=%d", calls.Load())
	}
	m.mu.Lock()
	ended := m.ended["normal-close-live/still-running"]
	m.mu.Unlock()
	if ended {
		t.Fatal("WebSocket normal close is not authority to end a still-running task")
	}
}

func TestTaskDiscoveryRebuildDoesNotRegressDurableCursor(t *testing.T) {
	st := testLedger(t)
	if _, err := st.RegistryPut(taskDiscoveryCursorKind, "restart", 0, []byte(`{"seq":9}`), "prior"); err != nil {
		t.Fatal(err)
	}
	var requested []int64
	m := New(st, newFakeMachines(), Options{Holder: "restart", LocalDiscovery: func(_ context.Context, after int64, _ int) (*proto.TaskDiscoveryPage, error) {
		requested = append(requested, after)
		if after == 0 {
			return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{{Seq: 1, TaskID: "old", State: proto.TaskStateRunning}}, NextSeq: 1}, nil
		}
		return &proto.TaskDiscoveryPage{Items: []proto.TaskDiscoveryItem{}, NextSeq: after}, nil
	}})
	// Rebuild responsibility from zero, but never regress the durable maximum cursor.
	m.reconcile(context.Background())
	m.Stop()
	if len(requested) != 1 || requested[0] != 0 {
		t.Fatalf("restart bootstrap cursors=%v", requested)
	}
	entry, err := st.RegistryGet(taskDiscoveryCursorKind, "restart")
	if err != nil {
		t.Fatal(err)
	}
	var saved discoveryCursor
	if err := json.Unmarshal(entry.Body, &saved); err != nil || saved.Seq != 9 {
		t.Fatalf("durable cursor regressed: body=%s err=%v", entry.Body, err)
	}
}
