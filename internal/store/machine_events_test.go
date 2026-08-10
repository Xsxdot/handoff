// store machine_events 测试：durable outbox 的事务性与单调性。
//
// 职责：
//   - 资源更新与 machine event 同事务（UpsertWorkspaceWithMachineEvent 两写同灭）
//   - machine_seq 每机器单调递增
//   - ApplyMachineEvent 幂等（重复 event_id 忽略）
//
// 边界：
//   - 使用真实 SQLite 文件（t.TempDir）
//   - 不覆盖 peer 同步（由 internal/peer 包测试负责）
package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// marshalWorkspace 把 Workspace 序列化为 payload JSON。
func marshalWorkspace(ws controlplane.Workspace) []byte {
	b, _ := json.Marshal(ws)
	return b
}

// TestUpsertWorkspaceWithMachineEventAtomic 验证 workspace upsert 与 outbox 同事务。
func TestUpsertWorkspaceWithMachineEventAtomic(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	machine, _ := s.EnsureLocalMachine(context.Background(), "本机")
	ws := controlplane.Workspace{
		ID: "ws1", MachineID: machine.ID, Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo",
	}
	ev, err := s.UpsertWorkspaceWithMachineEvent(context.Background(), ws, controlplane.MachineEventWorkspaceUpsert)
	if err != nil {
		t.Fatalf("UpsertWorkspaceWithMachineEvent: %v", err)
	}
	if ev.MachineSeq == 0 {
		t.Fatalf("machine_seq 不应为 0: %+v", ev)
	}
	// 回读：workspace 与 outbox 都在
	got, err := s.GetWorkspace(context.Background(), "ws1")
	if err != nil || got.ID != "ws1" {
		t.Fatalf("GetWorkspace: %+v err=%v", got, err)
	}
	events, err := s.MachineEventsAfter(context.Background(), machine.ID, 0, 10)
	if err != nil {
		t.Fatalf("MachineEventsAfter: %v", err)
	}
	if len(events) != 1 || events[0].Kind != controlplane.MachineEventWorkspaceUpsert {
		t.Fatalf("outbox = %+v, want 1 条 workspace.upsert", events)
	}
}

// TestMachineSeqMonotonicPerMachine 验证 machine_seq 每机器单调、跨机器独立。
func TestMachineSeqMonotonicPerMachine(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	local, _ := s.EnsureLocalMachine(context.Background(), "本机")
	ev1, err := s.UpsertWorkspaceWithMachineEvent(context.Background(),
		controlplane.Workspace{ID: "a", MachineID: local.ID, Kind: controlplane.WorkspaceKindMain,
			Path: "/a", CanonicalPath: "/a"}, controlplane.MachineEventWorkspaceUpsert)
	if err != nil {
		t.Fatalf("ev1: %v", err)
	}
	ev2, err := s.UpsertWorkspaceWithMachineEvent(context.Background(),
		controlplane.Workspace{ID: "b", MachineID: local.ID, Kind: controlplane.WorkspaceKindMain,
			Path: "/b", CanonicalPath: "/b"}, controlplane.MachineEventWorkspaceUpsert)
	if err != nil {
		t.Fatalf("ev2: %v", err)
	}
	if ev2.MachineSeq != ev1.MachineSeq+1 {
		t.Fatalf("machine_seq 应逐格 +1: %d -> %d", ev1.MachineSeq, ev2.MachineSeq)
	}
}

// TestApplyMachineEventIdempotent 验证 ApplyMachineEvent 重复 event_id 幂等忽略。
func TestApplyMachineEventIdempotent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ws := controlplane.Workspace{
		ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo",
	}
	payload := marshalWorkspace(ws)
	ev := controlplane.MachineEvent{
		MachineID: "m1", EventID: "evt-1", Kind: controlplane.MachineEventWorkspaceUpsert,
		ResourceID: "ws1", Payload: payload,
	}
	ce1, applied1, err := s.ApplyMachineEvent(context.Background(), ev)
	if err != nil || !applied1 {
		t.Fatalf("首次 Apply: applied=%v err=%v", applied1, err)
	}
	ce2, applied2, err := s.ApplyMachineEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("二次 Apply: %v", err)
	}
	if applied2 {
		t.Fatal("重复 event_id 应被幂等忽略")
	}
	if ce2.ControlRevision != 0 {
		t.Fatalf("重复事件不应分配新 revision: %d", ce2.ControlRevision)
	}
	if ce1.ControlRevision != 1 {
		t.Fatalf("首次事件 revision = %d, want 1", ce1.ControlRevision)
	}
}

// TestApplyMachineEventUpdatesCursor 验证 ApplyMachineEvent 更新 last_machine_seq。
func TestApplyMachineEventUpdatesCursor(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ws := controlplane.Workspace{ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo"}
	ev := controlplane.MachineEvent{
		MachineID: "m1", EventID: "evt-1", Kind: controlplane.MachineEventWorkspaceUpsert,
		ResourceID: "ws1", Payload: marshalWorkspace(ws),
	}
	if _, applied, err := s.ApplyMachineEvent(context.Background(), ev); err != nil || !applied {
		t.Fatalf("Apply: applied=%v err=%v", applied, err)
	}
	cursor, err := s.CurrentCursor(context.Background(), "m1")
	if err != nil {
		t.Fatalf("CurrentCursor: %v", err)
	}
	if cursor != 1 {
		t.Fatalf("cursor = %d, want 1", cursor)
	}
}

func TestApplyRemotePtyEventProjectsSummaryWithoutTerminalBytes(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session := workspaceapi.PtySession{TerminalSessionID: "remote-term", Incarnation: "remote-inc",
		WorkspaceID: "ws-remote", State: workspaceapi.PtyStateActive, Shell: "/bin/zsh", ThroughSeq: 4}
	payload, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	controlEvent, applied, err := s.ApplyMachineEvent(context.Background(), controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "remote-pty-upsert", Kind: controlplane.MachineEventPtyUpsert,
		ResourceID: session.TerminalSessionID, Payload: payload,
	})
	if err != nil || !applied || controlEvent.Kind != controlplane.ControlEventKindPtyUpsert {
		t.Fatalf("ApplyMachineEvent = event:%+v applied:%t err:%v", controlEvent, applied, err)
	}
	projected, err := s.GetPtySession(context.Background(), session.TerminalSessionID)
	if err != nil || projected != session {
		t.Fatalf("projected PTY = %+v err=%v", projected, err)
	}
	if bytes.Contains(controlEvent.Payload, []byte("data_base64")) {
		t.Fatalf("terminal bytes 不得进入控制事件: %s", controlEvent.Payload)
	}
}

func TestApplyRemotePtyEventRejectsTerminalBytesBeforePersistence(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	payload := json.RawMessage(`{
  "terminal_session_id":"remote-term",
  "incarnation":"remote-inc",
  "workspace_id":"ws-remote",
  "state":"active",
  "shell":"/bin/zsh",
  "through_seq":4,
  "exit_code":null,
  "data_base64":"c2VjcmV0"
}`)
	_, applied, err := s.ApplyMachineEvent(context.Background(), controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "remote-pty-bytes", Kind: controlplane.MachineEventPtyUpsert,
		ResourceID: "remote-term", Payload: payload,
	})
	if err == nil || applied {
		t.Fatalf("PTY bytes payload must be rejected before persistence: applied=%t err=%v", applied, err)
	}
	events, listErr := s.MachineEventsAfter(context.Background(), "remote-machine", 0, 10)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(events) != 0 {
		t.Fatalf("rejected PTY payload leaked into machine_events: %+v", events)
	}
	controlEvents, listErr := s.ControlEventsAfter(context.Background(), 0, 10)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(controlEvents) != 0 {
		t.Fatalf("rejected PTY payload leaked into control_events: %+v", controlEvents)
	}
}

func TestApplyRemotePtyEventRejectsInvalidStateAndTerminalRegression(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	invalid := workspaceapi.PtySession{TerminalSessionID: "invalid-term", Incarnation: "invalid-inc",
		WorkspaceID: "ws-remote", State: workspaceapi.PtyState("mystery"), Shell: "/bin/zsh", ThroughSeq: -1}
	invalidPayload, _ := json.Marshal(invalid)
	if _, applied, applyErr := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "invalid-state", Kind: controlplane.MachineEventPtyUpsert,
		ResourceID: invalid.TerminalSessionID, Payload: invalidPayload,
	}); applyErr == nil || applied {
		t.Fatalf("invalid PTY state accepted: applied=%t err=%v", applied, applyErr)
	}

	ended := workspaceapi.PtySession{TerminalSessionID: "ended-term", Incarnation: "ended-inc",
		WorkspaceID: "ws-remote", State: workspaceapi.PtyStateEnded, Shell: "/bin/zsh", ThroughSeq: 5}
	endedPayload, _ := json.Marshal(ended)
	if _, applied, applyErr := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "ended-event", Kind: controlplane.MachineEventPtyExit,
		ResourceID: ended.TerminalSessionID, Payload: endedPayload,
	}); applyErr != nil || !applied {
		t.Fatalf("valid PTY exit rejected: applied=%t err=%v", applied, applyErr)
	}
	regressed := ended
	regressed.State = workspaceapi.PtyStateActive
	regressed.ThroughSeq = 6
	regressedPayload, _ := json.Marshal(regressed)
	if _, applied, applyErr := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "regression-event", MachineSeq: 2,
		Kind: controlplane.MachineEventPtyUpsert, ResourceID: regressed.TerminalSessionID, Payload: regressedPayload,
	}); applyErr == nil || applied {
		t.Fatalf("ended PTY regression accepted: applied=%t err=%v", applied, applyErr)
	}
	projected, getErr := s.GetPtySession(ctx, ended.TerminalSessionID)
	if getErr != nil || projected.State != workspaceapi.PtyStateEnded || projected.ThroughSeq != 5 {
		t.Fatalf("PTY terminal state changed after rejection: %+v err=%v", projected, getErr)
	}
	changedExit := ended
	changedExit.ThroughSeq = 6
	changedExit.Shell = "/bin/bash"
	exitCode := 1
	changedExit.ExitCode = &exitCode
	changedPayload, _ := json.Marshal(changedExit)
	if _, applied, applyErr := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
		MachineID: "remote-machine", EventID: "changed-exit", MachineSeq: 2,
		Kind: controlplane.MachineEventPtyExit, ResourceID: changedExit.TerminalSessionID, Payload: changedPayload,
	}); applyErr == nil || applied {
		t.Fatalf("PTY final state rewrite accepted: applied=%t err=%v", applied, applyErr)
	}
}

func TestApplyLocalPtyOutboxDoesNotRegressOwnerState(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	session := workspaceapi.PtySession{TerminalSessionID: "local-term", Incarnation: "local-inc",
		WorkspaceID: "ws-local", State: workspaceapi.PtyStateStarting, Shell: "/bin/zsh"}
	if _, _, _, err := s.CreatePtySessionWithMachineEvent(ctx, "local-machine", "local-command", session); err != nil {
		t.Fatal(err)
	}
	session.State = workspaceapi.PtyStateActive
	session.ThroughSeq = 3
	if _, err := s.UpdatePtySessionWithMachineEvent(ctx, "local-machine", session, controlplane.MachineEventPtyUpsert); err != nil {
		t.Fatal(err)
	}
	session.State = workspaceapi.PtyStateEnded
	session.ThroughSeq = 5
	if _, err := s.UpdatePtySessionWithMachineEvent(ctx, "local-machine", session, controlplane.MachineEventPtyExit); err != nil {
		t.Fatal(err)
	}
	events, err := s.MachineEventsAfter(ctx, "local-machine", 0, 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("local PTY outbox = %+v err=%v", events, err)
	}
	for _, event := range events {
		if _, applied, applyErr := s.ApplyMachineEvent(ctx, event); applyErr != nil || !applied {
			t.Fatalf("apply local PTY event seq=%d: applied=%t err=%v", event.MachineSeq, applied, applyErr)
		}
	}
	projected, err := s.GetPtySession(ctx, session.TerminalSessionID)
	if err != nil || projected.State != workspaceapi.PtyStateEnded || projected.ThroughSeq != 5 {
		t.Fatalf("local PTY owner state regressed: %+v err=%v", projected, err)
	}
}

// TestApplyGitRefRemoveDeletesOnlyScopedProjection 验证分支删除事件携带
// location 身份；不同项目/宿主上的同名分支不能被一并删除。
func TestApplyGitRefRemoveDeletesOnlyScopedProjection(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for index, ref := range []controlplane.GitRef{
		{LocationID: "loc-1", Name: "main", HeadOID: "one", CheckedOutWorkspaceIDs: []string{}},
		{LocationID: "loc-2", Name: "main", HeadOID: "two", CheckedOutWorkspaceIDs: []string{}},
	} {
		payload, _ := json.Marshal(ref)
		if _, applied, err := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
			MachineID: "remote", EventID: fmt.Sprintf("upsert-%d", index),
			Kind: controlplane.MachineEventGitRefUpsert, ResourceID: ref.Name, Payload: payload,
		}); err != nil || !applied {
			t.Fatalf("apply upsert %d: applied=%t err=%v", index, applied, err)
		}
	}
	removePayload, _ := json.Marshal(controlplane.GitRef{LocationID: "loc-1", Name: "main"})
	if _, applied, err := s.ApplyMachineEvent(ctx, controlplane.MachineEvent{
		MachineID: "remote", EventID: "remove-1", Kind: controlplane.MachineEventGitRefRemove,
		ResourceID: "main", Payload: removePayload,
	}); err != nil || !applied {
		t.Fatalf("apply remove: applied=%t err=%v", applied, err)
	}
	refs, err := s.ListAllGitRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].LocationID != "loc-2" || refs[0].Name != "main" {
		t.Fatalf("refs = %+v, want loc-2/main", refs)
	}
}

// TestApplyExistingLocalOutboxEventProjectsOnce 验证 owner 与控制面共用同库时，
// 已写入 machine_events 但 cursor 尚未推进的本机 outbox 仍能投影一次。
func TestApplyExistingLocalOutboxEventProjectsOnce(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	machine, err := s.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.UpsertWorkspaceWithMachineEvent(context.Background(), controlplane.Workspace{
		ID: "ws-local", MachineID: machine.ID, Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo",
	}, controlplane.MachineEventWorkspaceUpsert)
	if err != nil {
		t.Fatal(err)
	}
	ce, applied, err := s.ApplyMachineEvent(context.Background(), event)
	if err != nil || !applied || ce.ControlRevision != 1 {
		t.Fatalf("首次投影 existing outbox: applied=%t event=%+v err=%v", applied, ce, err)
	}
	if _, applied, err := s.ApplyMachineEvent(context.Background(), event); err != nil || applied {
		t.Fatalf("重复投影 existing outbox: applied=%t err=%v", applied, err)
	}
}

func TestUpdateTaskStateEventCarriesCompleteSummary(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := protoTask("task-1", "ws-1", "machine-1")
	task.Name = "修复推送"
	task.Executor = "opencode"
	if _, err := s.CreateTaskWithMachineEvent(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	event, err := s.UpdateTaskStateWithEvent(context.Background(), task.ID, proto.TaskStateRunning)
	if err != nil {
		t.Fatal(err)
	}
	var summary controlplane.TaskSummary
	if err := json.Unmarshal(event.Payload, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.TaskID != task.ID || summary.Name != task.Name || summary.Executor != task.Executor ||
		summary.State != controlplane.TaskSummaryStateRunning || summary.UpdatedAt.IsZero() {
		t.Fatalf("task event summary = %+v", summary)
	}
}
