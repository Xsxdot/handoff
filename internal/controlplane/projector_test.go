// controlplane Projector 测试：machine event → 控制面投影与幂等。
//
// 职责：
//   - Apply 在同一事务内：幂等记录 machine event → 更新投影 → 追加 ControlEvent
//     → 更新 last_machine_seq
//   - 重复事件返回 applied=false 且不分配新 revision
//   - 本机与远端共用同一入口
//
// 边界：
//   - 使用内存 fake Repository，不依赖真实 SQLite（事务语义由 store 专项测试覆盖）
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeProjectorRepo 是 Projector 用到的 Repository 子集。
type fakeProjectorRepo struct {
	events       map[string]bool // (machine_id, event_id) 已应用
	applied      []MachineEvent
	controlLog   []ControlEvent
	lastRevision int64
	lastSeq      map[string]int64
}

func newFakeProjectorRepo() *fakeProjectorRepo {
	return &fakeProjectorRepo{
		events: map[string]bool{}, lastSeq: map[string]int64{}, controlLog: []ControlEvent{},
	}
}

func (f *fakeProjectorRepo) ApplyMachineEvent(_ context.Context, ev MachineEvent) (ControlEvent, bool, error) {
	key := ev.MachineID + "/" + ev.EventID
	if f.events[key] {
		return ControlEvent{}, false, nil // 重复幂等
	}
	f.events[key] = true
	f.applied = append(f.applied, ev)
	f.lastRevision++
	ce := ControlEvent{ControlRevision: f.lastRevision, Kind: ControlEventKindWorkspaceUpsert,
		ResourceID: ev.ResourceID, Payload: ev.Payload}
	f.controlLog = append(f.controlLog, ce)
	f.lastSeq[ev.MachineID] = ev.MachineSeq
	return ce, true, nil
}

func (f *fakeProjectorRepo) MachineEventsAfter(context.Context, string, int64, int) ([]MachineEvent, error) {
	return nil, nil
}

var _ = errors.New

// —— 以下为满足 Repository 接口的最小桩（Projector 不调用）——

func (f *fakeProjectorRepo) EnsureLocalMachine(context.Context, string) (Machine, error) {
	return Machine{}, nil
}
func (f *fakeProjectorRepo) SyncConfiguredMachines(context.Context, []ConfiguredMachine) ([]Machine, error) {
	return nil, nil
}
func (f *fakeProjectorRepo) MigrateLegacyTasks(context.Context, string) (int, error) { return 0, nil }
func (f *fakeProjectorRepo) Snapshot(context.Context) (Snapshot, error)              { return Snapshot{}, nil }
func (f *fakeProjectorRepo) UpsertWorkspaceWithMachineEvent(context.Context, Workspace, MachineEventKind) (MachineEvent, error) {
	return MachineEvent{}, nil
}
func (f *fakeProjectorRepo) RemoveWorkspaceWithMachineEvent(context.Context, string, string) (MachineEvent, error) {
	return MachineEvent{}, nil
}
func (f *fakeProjectorRepo) UpsertGitRefsWithMachineEvents(context.Context, string, []GitRef) ([]MachineEvent, error) {
	return nil, nil
}
func (f *fakeProjectorRepo) AppendTaskSummaryEvent(context.Context, TaskSummary) (MachineEvent, error) {
	return MachineEvent{}, nil
}
func (f *fakeProjectorRepo) ControlEventsAfter(context.Context, int64, int) ([]ControlEvent, error) {
	return nil, nil
}
func (f *fakeProjectorRepo) CreateProject(context.Context, Project, []ProjectLocation, []Workspace) (ControlEvent, error) {
	return ControlEvent{}, nil
}
func (f *fakeProjectorRepo) CreateOperation(context.Context, Operation) error { return nil }
func (f *fakeProjectorRepo) UpdateOperation(context.Context, Operation) error { return nil }
func (f *fakeProjectorRepo) GetOperation(context.Context, string) (Operation, error) {
	return Operation{}, ErrNotFound
}
func (f *fakeProjectorRepo) ListOperations(context.Context) ([]Operation, error) { return nil, nil }
func (f *fakeProjectorRepo) GetWorkspace(context.Context, string) (Workspace, error) {
	return Workspace{}, ErrNotFound
}
func (f *fakeProjectorRepo) GetMachine(context.Context, string) (Machine, error) {
	return Machine{}, ErrNotFound
}
func (f *fakeProjectorRepo) ResolveWorkspaceForPath(context.Context, string, string, string) (Workspace, error) {
	return Workspace{}, nil
}
func (f *fakeProjectorRepo) AdoptWorkspace(context.Context, string, string) error { return nil }

// TestProjectorAppliesEvent 验证 Apply 投影事件并分配 revision。
func TestProjectorAppliesEvent(t *testing.T) {
	repo := newFakeProjectorRepo()
	proj := NewProjector(repo)
	ev := MachineEvent{
		MachineID: "m1", MachineSeq: 3, EventID: "evt-1",
		Kind: MachineEventWorkspaceUpsert, ResourceID: "ws1",
		Payload: json.RawMessage(`{"id":"ws1"}`),
	}
	ce, applied, err := proj.Apply(context.Background(), ev)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !applied {
		t.Fatal("首次 Apply 应 applied=true")
	}
	if ce.ControlRevision != 1 {
		t.Fatalf("revision = %d, want 1", ce.ControlRevision)
	}
	if ce.ResourceID != "ws1" {
		t.Fatalf("resource_id = %q", ce.ResourceID)
	}
}

// TestProjectorDuplicateIgnored 验证重复事件幂等忽略且不分配新 revision。
func TestProjectorDuplicateIgnored(t *testing.T) {
	repo := newFakeProjectorRepo()
	proj := NewProjector(repo)
	ev := MachineEvent{
		MachineID: "m1", MachineSeq: 3, EventID: "evt-1",
		Kind: MachineEventWorkspaceUpsert, ResourceID: "ws1",
		Payload: json.RawMessage(`{"id":"ws1"}`),
	}
	if _, applied, err := proj.Apply(context.Background(), ev); err != nil || !applied {
		t.Fatalf("首次 Apply: applied=%v err=%v", applied, err)
	}
	ce2, applied2, err := proj.Apply(context.Background(), ev)
	if err != nil {
		t.Fatalf("二次 Apply: %v", err)
	}
	if applied2 {
		t.Fatal("重复事件应 applied=false")
	}
	if ce2.ControlRevision != 0 {
		t.Fatalf("重复事件不应分配新 revision: %d", ce2.ControlRevision)
	}
}

// TestProjectorGlobalRevisionMonotonic 验证 control_revision 全局单调。
func TestProjectorGlobalRevisionMonotonic(t *testing.T) {
	repo := newFakeProjectorRepo()
	proj := NewProjector(repo)
	var prev int64
	for i := int64(1); i <= 5; i++ {
		ev := MachineEvent{
			MachineID: "m1", EventID: "evt-" + itoa(i),
			Kind: MachineEventWorkspaceUpsert, ResourceID: "ws" + itoa(i),
			Payload: json.RawMessage(`{}`),
		}
		ce, applied, err := proj.Apply(context.Background(), ev)
		if err != nil || !applied {
			t.Fatalf("Apply %d: applied=%v err=%v", i, applied, err)
		}
		if ce.ControlRevision <= prev {
			t.Fatalf("revision 应单调递增: %d -> %d", prev, ce.ControlRevision)
		}
		prev = ce.ControlRevision
	}
}

// itoa 是整数转字符串的测试助手。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
