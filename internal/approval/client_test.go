// Package approval_test exercises the production approval client through its
// public seam and a real SQLite store, including the durable ticket/event
// projections that make approval decisions recoverable.
package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

type testHub struct {
	mu          sync.Mutex
	events      []proto.Event
	waiters     map[string]chan string
	waiterReady chan string
}

func (h *testHub) Publish(ev proto.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, ev)
}

func (h *testHub) WaitAnswer(ctx context.Context, ticketID string) (string, error) {
	ch := make(chan string, 1)
	h.mu.Lock()
	if h.waiters == nil {
		h.waiters = map[string]chan string{}
	}
	h.waiters[ticketID] = ch
	ready := h.waiterReady
	h.mu.Unlock()
	if ready != nil {
		select {
		case ready <- ticketID:
		default:
		}
	}
	defer func() {
		h.mu.Lock()
		delete(h.waiters, ticketID)
		h.mu.Unlock()
	}()
	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (h *testHub) NotifyAnswer(ticketID, answer string) {
	h.mu.Lock()
	ch := h.waiters[ticketID]
	h.mu.Unlock()
	if ch != nil {
		ch <- answer
	}
}

func (h *testHub) published() []proto.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]proto.Event(nil), h.events...)
}

type hookCalls struct {
	mu          sync.Mutex
	autoAllows  int
	transitions []proto.TaskState
	failures    int
	resets      int
	deliveries  []string
}

func newClientFixture(t *testing.T, verdict permgate.Verdict,
	shouldConsult bool, decide func(context.Context, string, string) approval.ConsultDecision,
) (*store.Store, *testHub, *approval.Client, *hookCalls) {
	t.Helper()
	workdir := t.TempDir()
	taskdir := t.TempDir()
	tmpdir := filepath.Join(taskdir, "tmp")
	if err := os.MkdirAll(tmpdir, 0o700); err != nil {
		t.Fatalf("MkdirAll task tmp: %v", err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const taskID = "task-client-fixture"
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: workdir, WorkDir: workdir,
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	hub := &testHub{}
	calls := &hookCalls{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if decide == nil {
		decide = func(context.Context, string, string) approval.ConsultDecision {
			return approval.ConsultDecision{Reason: "fixture 未提供审批结论"}
		}
	}
	hooks := approval.Hooks{
		Log: logger, Store: st, Hub: hub,
		JudgePermission: func(string, executor.AdapterEvent) permgate.Verdict { return verdict },
		ShouldConsult:   func(string) bool { return shouldConsult },
		Decide:          decide,
		CountConsultFailure: func(string) {
			calls.mu.Lock()
			calls.failures++
			calls.mu.Unlock()
		},
		ResetConsultFailure: func(string) {
			calls.mu.Lock()
			calls.resets++
			calls.mu.Unlock()
		},
		AutoAllow: func(string, executor.AdapterEvent, permgate.Verdict) {
			calls.mu.Lock()
			calls.autoAllows++
			calls.mu.Unlock()
		},
		TransitBestEffort: func(_ string, to proto.TaskState, _ string) {
			calls.mu.Lock()
			calls.transitions = append(calls.transitions, to)
			calls.mu.Unlock()
		},
		NoteDeliveryFailed: func(_, ticketID string, cause error) {
			calls.mu.Lock()
			calls.deliveries = append(calls.deliveries, ticketID+": "+cause.Error())
			calls.mu.Unlock()
		},
	}
	snap := executor.PolicySnapshot{
		Version: "v1",
		Scope:   executor.ApprovalScope{Workdir: workdir, TaskDir: taskdir, TaskTmpDir: tmpdir},
	}
	return st, hub, approval.NewClient(taskID, snap, hooks), calls
}

func permissionRequest(nativeID, text string) executor.ApprovalRequest {
	return executor.ApprovalRequest{
		NativeID: nativeID,
		Text:     text,
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: text},
	}
}

func eventCount(events []proto.Event, typ proto.EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func payloadMap(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("Unmarshal payload %q: %v", payload, err)
	}
	return out
}

func TestNewClientFillsAndPreservesTaskID(t *testing.T) {
	snap := executor.PolicySnapshot{Version: "v1", Scope: executor.ApprovalScope{Workdir: "/work"}}
	client := approval.NewClient("T1", snap, approval.Hooks{})
	got, err := client.PolicySnapshot(context.Background())
	if err != nil {
		t.Fatalf("PolicySnapshot: %v", err)
	}
	if got.TaskID != "T1" || got.Version != "v1" || got.Scope.Workdir != "/work" {
		t.Fatalf("空 TaskID 快照未正确补全: %+v", got)
	}
	preserved := executor.PolicySnapshot{Version: "v2", TaskID: "original", Scope: executor.ApprovalScope{TaskDir: "/task"}}
	got, err = approval.NewClient("T2", preserved, approval.Hooks{}).PolicySnapshot(context.Background())
	if err != nil {
		t.Fatalf("PolicySnapshot preserved: %v", err)
	}
	if got != preserved {
		t.Fatalf("非空 snapshot.TaskID 不应覆盖: got=%+v want=%+v", got, preserved)
	}
}

func TestClientAutoAllowHasNoTicket(t *testing.T) {
	st, hub, client, calls := newClientFixture(t, permgate.Verdict{Action: permgate.AutoAllow, Rule: "safe"}, false, nil)
	res, err := client.Request(context.Background(), permissionRequest("native-auto", "echo safe"))
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if res.Decision.Status != executor.ApprovalAllow || res.Ref.ID != "task-client-fixture:native-auto" {
		t.Fatalf("auto allow result=%+v", res)
	}
	if calls.autoAllows != 1 {
		t.Fatalf("AutoAllow hook calls=%d, want 1", calls.autoAllows)
	}
	if _, err := st.GetTicket(res.Ref.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("auto allow must not create ticket, err=%v", err)
	}
	if got := eventCount(hub.published(), proto.EventTypePermissionRequest); got != 0 {
		t.Fatalf("auto allow published permission_request=%d", got)
	}
}

func TestClientRequestFailClosedAndEscalates(t *testing.T) {
	st, hub, client, _ := newClientFixture(t, permgate.Verdict{Action: permgate.Escalate, Reason: "needs review"}, false, nil)
	res, err := client.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "native-truncated", Text: "bash: long command", Truncated: true,
	})
	if err != nil {
		t.Fatalf("truncated Request: %v", err)
	}
	if res.Decision.Status != executor.ApprovalPending {
		t.Fatalf("truncated status=%q, want pending", res.Decision.Status)
	}
	tk, err := st.GetTicket(res.Ref.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	request := payloadMap(t, tk.Request)
	if request["kind"] != "gate" || request["permission"] != "bash: long command" {
		t.Fatalf("ticket request JSON=%v", request)
	}
	events, err := st.EventsFromAsc("task-client-fixture", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	if eventCount(events, proto.EventTypePermissionRequest) != 1 || eventCount(hub.published(), proto.EventTypePermissionRequest) != 1 {
		t.Fatalf("escalation event store=%d hub=%d", eventCount(events, proto.EventTypePermissionRequest), eventCount(hub.published(), proto.EventTypePermissionRequest))
	}
	if _, err := client.Request(context.Background(), executor.ApprovalRequest{Text: "missing native id"}); err == nil {
		t.Fatal("空 NativeID 必须返回错误")
	}
	if _, err := st.GetTicket("task-client-fixture:"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("空 NativeID 不得建单，err=%v", err)
	}
}

func TestClientAwaitReadsStoreAfterCancel(t *testing.T) {
	st, hub, client, _ := newClientFixture(t, permgate.Verdict{Action: permgate.Escalate}, false, nil)
	res, err := client.Request(context.Background(), permissionRequest("native-await", "echo await"))
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	hub.mu.Lock()
	hub.waiterReady = make(chan string, 1)
	hub.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		d   executor.ApprovalDecision
		err error
	}, 1)
	go func() {
		d, err := client.Await(ctx, res.Ref)
		result <- struct {
			d   executor.ApprovalDecision
			err error
		}{d, err}
	}()
	select {
	case <-hub.waiterReady:
	case <-time.After(time.Second):
		t.Fatal("Await 未进入 Hub 等待")
	}
	if err := st.AnswerTicket(res.Ref.ID, "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	cancel()
	got := <-result
	if got.err != nil || got.d.Status != executor.ApprovalAllow {
		t.Fatalf("Store 已落答案时 Await=%+v", got)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = client.Await(ctx, executor.ApprovalRef{ID: "task-client-fixture:never"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("无答案取消应保留 context.Canceled，err=%v", err)
	}
}

func TestClientAcknowledgeStages(t *testing.T) {
	st, _, client, _ := newClientFixture(t, permgate.Verdict{Action: permgate.Escalate}, false, nil)
	req := permissionRequest("native-ack", "echo ack")
	res, err := client.Request(context.Background(), req)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := st.AnswerTicket(res.Ref.ID, "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	ack := func(stage executor.AckStage) error {
		return client.Acknowledge(context.Background(), executor.ApprovalAck{Ref: res.Ref, NativeID: req.NativeID, Stage: stage})
	}
	if err := ack(executor.AckFormed); err != nil {
		t.Fatalf("AckFormed: %v", err)
	}
	tk, err := st.GetTicket(res.Ref.ID)
	if err != nil || tk.DeliveredAt != nil {
		t.Fatalf("AckFormed ticket=%+v err=%v", tk, err)
	}
	if err := ack(executor.AckDelivered); err != nil {
		t.Fatalf("AckDelivered: %v", err)
	}
	tk, err = st.GetTicket(res.Ref.ID)
	if err != nil || tk.DeliveredAt == nil {
		t.Fatalf("AckDelivered ticket=%+v err=%v", tk, err)
	}
	deliveredAt := *tk.DeliveredAt
	if err := ack(executor.AckExecuted); err != nil {
		t.Fatalf("AckExecuted: %v", err)
	}
	tk, err = st.GetTicket(res.Ref.ID)
	if err != nil || tk.DeliveredAt == nil || !tk.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("AckExecuted changed DeliveredAt: ticket=%+v err=%v", tk, err)
	}
	if err := ack(executor.AckStage("unknown")); err == nil {
		t.Fatal("未知 AckStage 必须返回错误")
	}
}

func TestClientReuseRequiresDeliveredGrant(t *testing.T) {
	st, _, client, _ := newClientFixture(t, permgate.Verdict{Action: permgate.Escalate}, false, nil)
	first, err := client.Request(context.Background(), permissionRequest("native-reuse-1", "python3 script.py"))
	if err != nil {
		t.Fatalf("first Request: %v", err)
	}
	if err := st.AnswerTicket(first.Ref.ID, "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	undelivered, err := client.Request(context.Background(), permissionRequest("native-reuse-2", "python3 script.py"))
	if err != nil {
		t.Fatalf("undelivered Request: %v", err)
	}
	if undelivered.Decision.Status == executor.ApprovalAllow && undelivered.Decision.Rule == "reuse" {
		t.Fatal("未送达 allow 不得成为复用先例")
	}
	if err := client.Acknowledge(context.Background(), executor.ApprovalAck{Ref: first.Ref, Stage: executor.AckDelivered}); err != nil {
		t.Fatalf("AckDelivered: %v", err)
	}
	reused, err := client.Request(context.Background(), permissionRequest("native-reuse-3", "python3 script.py"))
	if err != nil {
		t.Fatalf("reused Request: %v", err)
	}
	if reused.Decision.Status != executor.ApprovalAllow || reused.Decision.Rule != "reuse" {
		t.Fatalf("已送达同指纹应复用: %+v", reused.Decision)
	}
	if reused.Ref.ID != "task-client-fixture:native-reuse-3" {
		t.Fatalf("reuse ref=%q", reused.Ref.ID)
	}
}

func TestClientConsultFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		decide func(context.Context, string, string) approval.ConsultDecision
	}{{
		name: "error",
		decide: func(context.Context, string, string) approval.ConsultDecision {
			return approval.ConsultDecision{Err: errors.New("approver unavailable")}
		},
	}, {
		name: "deny",
		decide: func(context.Context, string, string) approval.ConsultDecision {
			return approval.ConsultDecision{Reason: "not confident"}
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, client, calls := newClientFixture(t, permgate.Verdict{Action: permgate.Consult}, true, tc.decide)
			res, err := client.Request(context.Background(), permissionRequest("native-consult-"+tc.name, "python3 script.py"))
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			if res.Decision.Status == executor.ApprovalAllow || res.Decision.Status != executor.ApprovalPending {
				t.Fatalf("consult %s must fail closed to pending: %+v", tc.name, res.Decision)
			}
			calls.mu.Lock()
			failures, resets := calls.failures, calls.resets
			calls.mu.Unlock()
			if tc.name == "error" && failures != 1 {
				t.Fatalf("CountConsultFailure=%d", failures)
			}
			if tc.name == "deny" && resets != 1 {
				t.Fatalf("ResetConsultFailure=%d", resets)
			}
		})
	}
}

func TestClientConsultAllowPersistsGrant(t *testing.T) {
	st, _, client, _ := newClientFixture(t, permgate.Verdict{Action: permgate.Consult}, true,
		func(context.Context, string, string) approval.ConsultDecision {
			return approval.ConsultDecision{Approve: true, Reason: "approved by fixture", ElapsedMS: 7}
		})
	first, err := client.Request(context.Background(), permissionRequest("native-approver", "python3 script.py"))
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if first.Decision.Status != executor.ApprovalAllow || first.Decision.Rule != "approver" {
		t.Fatalf("approver allow result=%+v", first.Decision)
	}
	tk, err := st.GetTicket(first.Ref.ID)
	if err != nil || tk.Answer == nil || *tk.Answer != "allow" || tk.DeliveredAt == nil {
		t.Fatalf("approver grant=%+v err=%v", tk, err)
	}
	events, err := st.EventsFromAsc("task-client-fixture", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	if eventCount(events, proto.EventTypeApproverDecision) != 1 || eventCount(events, proto.EventTypeTicketAnswered) != 1 || eventCount(events, proto.EventTypePermissionRequest) != 0 {
		t.Fatalf("approver events=%+v", events)
	}
	decision := payloadMap(t, events[0].Payload)
	if decision["decision"] != "approve" || decision["elapsed_ms"] != float64(7) {
		t.Fatalf("approver_decision payload=%v", decision)
	}
	second, err := client.Request(context.Background(), permissionRequest("native-approver-2", "python3 script.py"))
	if err != nil {
		t.Fatalf("reuse Request: %v", err)
	}
	if second.Decision.Status != executor.ApprovalAllow || second.Decision.Rule != "reuse" {
		t.Fatalf("approved grant not reusable: %+v", second.Decision)
	}
}

func TestClientNoteDeliveryFailedTaskIsolation(t *testing.T) {
	_, _, client, calls := newClientFixture(t, permgate.Verdict{Action: permgate.Escalate}, false, nil)
	cause := errors.New("respond failed")
	client.NoteDeliveryFailed("other-task", "other-task:ticket", cause)
	calls.mu.Lock()
	if len(calls.deliveries) != 0 {
		t.Fatalf("cross-task delivery failure must not invoke hook: %v", calls.deliveries)
	}
	calls.mu.Unlock()
	client.NoteDeliveryFailed("task-client-fixture", "task-client-fixture:ticket", cause)
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if len(calls.deliveries) != 1 || calls.deliveries[0] != "task-client-fixture:ticket: respond failed" {
		t.Fatalf("same-task delivery failure=%v", calls.deliveries)
	}
}
