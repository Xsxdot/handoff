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
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
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
