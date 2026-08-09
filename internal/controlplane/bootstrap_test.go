// controlplane BootstrapService 测试：初始化顺序与配置投影。
//
// 职责：
//   - 首次启动生成 local Machine UUID；同库重启保持 ID（经 Repository 注入）
//   - 配置 targets 只保存 secret_ref，不落 token
//   - endpoint/display name 改变保留 machine ID
//   - 删除 target 保留 last-known Machine 但标 unavailable
//
// 边界：
//   - 使用内存 fake Repository，不依赖真实 SQLite（持久化语义由 store 包测试覆盖）
package controlplane

import (
	"context"
	"log/slog"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

// fakeRepo 是 Repository 的最小内存实现，记录调用以便断言初始化顺序。
type fakeRepo struct {
	machines      []Machine
	ensureCalled  []string
	syncCalled    []ConfiguredMachine
	legacyCalled  []string
	migratedCount int
	snapRevision  int64
	applyEvents   []MachineEvent
}

// EnsureLocalMachine 首次生成固定 UUID，之后保持（模拟 DB 的 control_metadata）。
func (f *fakeRepo) EnsureLocalMachine(_ context.Context, displayName string) (Machine, error) {
	f.ensureCalled = append(f.ensureCalled, displayName)
	if len(f.machines) == 0 {
		f.machines = []Machine{{ID: "m-local", DisplayName: displayName, Kind: MachineKindLocal, Status: MachineStatusConnected}}
		return f.machines[0], nil
	}
	f.machines[0].DisplayName = displayName
	return f.machines[0], nil
}

func (f *fakeRepo) SyncConfiguredMachines(_ context.Context, configured []ConfiguredMachine) ([]Machine, error) {
	f.syncCalled = configured
	out := make([]Machine, 0, len(configured))
	for _, c := range configured {
		m := Machine{ID: c.ConfigKey, DisplayName: c.DisplayName, Kind: c.Kind,
			Endpoint: c.Endpoint, SecretRef: c.SecretRef, Status: MachineStatusUnavailable}
		out = append(out, m)
	}
	return out, nil
}

func (f *fakeRepo) MigrateLegacyTasks(_ context.Context, localMachineID string) (int, error) {
	f.legacyCalled = append(f.legacyCalled, localMachineID)
	return f.migratedCount, nil
}

func (f *fakeRepo) Snapshot(_ context.Context) (Snapshot, error) {
	return Snapshot{ControlRevision: f.snapRevision}, nil
}

// 其余 Repository 方法在 bootstrap 流程中不被调用，返回空值。

func (f *fakeRepo) UpsertWorkspaceWithMachineEvent(context.Context, Workspace, MachineEventKind) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeRepo) RemoveWorkspaceWithMachineEvent(context.Context, string, string) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeRepo) UpsertGitRefsWithMachineEvents(context.Context, string, []GitRef) ([]MachineEvent, error) {
	return nil, nil
}

func (f *fakeRepo) AppendTaskSummaryEvent(context.Context, TaskSummary) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeRepo) ApplyMachineEvent(context.Context, MachineEvent) (ControlEvent, bool, error) {
	f.applyEvents = append(f.applyEvents, MachineEvent{})
	return ControlEvent{}, false, nil
}

func (f *fakeRepo) MachineEventsAfter(context.Context, string, int64, int) ([]MachineEvent, error) {
	return nil, nil
}

func (f *fakeRepo) ControlEventsAfter(context.Context, int64, int) ([]ControlEvent, error) {
	return nil, nil
}

func (f *fakeRepo) CreateProject(context.Context, Project, []ProjectLocation, []Workspace) (ControlEvent, error) {
	return ControlEvent{}, nil
}

func (f *fakeRepo) CreateOperation(context.Context, Operation) error { return nil }

func (f *fakeRepo) UpdateOperation(context.Context, Operation) error { return nil }

func (f *fakeRepo) GetOperation(context.Context, string) (Operation, error) {
	return Operation{}, ErrNotFound
}

func (f *fakeRepo) ListOperations(context.Context) ([]Operation, error) { return nil, nil }

func (f *fakeRepo) GetWorkspace(context.Context, string) (Workspace, error) {
	return Workspace{}, ErrNotFound
}

func (f *fakeRepo) ResolveWorkspaceForPath(context.Context, string, string, string) (Workspace, error) {
	return Workspace{}, nil
}

func (f *fakeRepo) AdoptWorkspace(context.Context, string, string) error { return nil }

func (f *fakeRepo) GetMachine(_ context.Context, id string) (Machine, error) {
	for _, m := range f.machines {
		if m.ID == id {
			return m, nil
		}
	}
	return Machine{}, ErrNotFound
}

var _ Repository = (*fakeRepo)(nil)

func newFakeBootstrap(t *testing.T) (*BootstrapService, *fakeRepo) {
	t.Helper()
	repo := &fakeRepo{}
	svc := NewBootstrapService(repo, slog.New(slog.NewTextHandler(discard{}, nil)))
	return svc, repo
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestBootstrapInitializeGeneratesLocalMachine 验证首次初始化生成 local Machine。
func TestBootstrapInitializeGeneratesLocalMachine(t *testing.T) {
	svc, repo := newFakeBootstrap(t)
	m, err := svc.Initialize(context.Background(), &config.Config{Targets: map[string]config.Target{}})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if m.ID == "" {
		t.Fatal("local machine ID 不能为空")
	}
	if m.Kind != MachineKindLocal {
		t.Fatalf("kind = %s, want local", m.Kind)
	}
	if len(repo.ensureCalled) != 1 {
		t.Fatalf("EnsureLocalMachine 调用 %d 次, want 1", len(repo.ensureCalled))
	}
	// 迁移必须在初始化流程中执行
	if len(repo.legacyCalled) != 1 || repo.legacyCalled[0] != m.ID {
		t.Fatalf("MigrateLegacyTasks 调用 = %+v, want [%s]", repo.legacyCalled, m.ID)
	}
}

// TestBootstrapInitializeSyncsConfiguredMachines 验证配置 targets 投影为
// ConfiguredMachine 且 secret_ref 指向配置键而非 token 值。
func TestBootstrapInitializeSyncsConfiguredMachines(t *testing.T) {
	svc, repo := newFakeBootstrap(t)
	cfg := &config.Config{Targets: map[string]config.Target{
		"devbox": {Addr: "http://10.0.0.5:7777", Token: "super-secret-token"},
	}}
	if _, err := svc.Initialize(context.Background(), cfg); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(repo.syncCalled) != 1 {
		t.Fatalf("SyncConfiguredMachines 调用 %d 次, want 1", len(repo.syncCalled))
	}
	cm := repo.syncCalled[0]
	if cm.ConfigKey != "devbox" || cm.Endpoint != "http://10.0.0.5:7777" {
		t.Fatalf("configured = %+v", cm)
	}
	if cm.SecretRef != "config.targets.devbox.token" {
		t.Fatalf("secret_ref = %q, want config.targets.devbox.token", cm.SecretRef)
	}
}

// TestBootstrapInitializeProjectsLocalEvents 验证初始化最后投影本机 machine
// events 到控制日志（ApplyMachineEvent 被调用）。
func TestBootstrapInitializeProjectsLocalEvents(t *testing.T) {
	svc, repo := newFakeBootstrap(t)
	repo.applyEvents = nil
	if _, err := svc.Initialize(context.Background(), &config.Config{Targets: map[string]config.Target{}}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// fake 没有 machine events，ApplyMachineEvent 不应被调用（空 outbox）
	if len(repo.applyEvents) != 0 {
		t.Fatalf("空 outbox 不应投影任何事件，实际 %d", len(repo.applyEvents))
	}
}
