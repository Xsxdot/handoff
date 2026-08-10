// store projects.go 测试：Project/Location 持久化与 detached 归并。
//
// 职责：
//   - CreateProject 同事务创建 Project + Location + Workspace 并追加 control event
//   - AdoptWorkspace 保留 Workspace ID 与 Task 引用
//   - location (project_id, role) 唯一索引兜底
//
// 边界：
//   - 使用真实 SQLite 文件（t.TempDir）
//   - 业务校验（identity 匹配）由 ProjectService 测试覆盖
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// TestCreateProjectPersistsLocationsAndWorkspaces 验证同事务创建。
func TestCreateProjectPersistsLocationsAndWorkspaces(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	project := controlplane.Project{ID: "p1", Name: "handoff", CreatedAt: now(), UpdatedAt: now()}
	loc := controlplane.ProjectLocation{
		ID: "loc1", ProjectID: "p1", MachineID: "m-local", MachineKind: controlplane.MachineKindLocal,
		Role: controlplane.LocationRoleLocal, MainWorkspaceID: "ws1",
		Source:    controlplane.LocationSourceExistingPath,
		CreatedAt: now(), UpdatedAt: now(),
	}
	ws := controlplane.Workspace{ID: "ws1", MachineID: "m-local", Kind: controlplane.WorkspaceKindMain,
		Path: "/repo", CanonicalPath: "/repo"}
	operation := controlplane.Operation{OperationID: "op1", Kind: controlplane.OperationKindCreateProject,
		State: controlplane.OperationStateSucceeded, ProjectID: project.ID, CreatedAt: now(), UpdatedAt: now()}
	if _, err := s.CreateOperation(ctx, operation); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}
	events, err := s.CreateProject(ctx, project, []controlplane.ProjectLocation{loc}, []controlplane.Workspace{ws}, &operation)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("control events = %d, want project/location/workspace/operation 四条", len(events))
	}
	wantKinds := []controlplane.ControlEventKind{
		controlplane.ControlEventKindProjectUpsert,
		controlplane.ControlEventKindLocationUpsert,
		controlplane.ControlEventKindWorkspaceUpsert,
		controlplane.ControlEventKindOperationUpsert,
	}
	for index, event := range events {
		if event.ControlRevision != int64(index+2) || event.Kind != wantKinds[index] {
			t.Fatalf("event[%d] = revision %d kind %s, want %d/%s",
				index, event.ControlRevision, event.Kind, index+2, wantKinds[index])
		}
	}

	projects, err := s.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].ID != "p1" {
		t.Fatalf("projects = %+v err=%v", projects, err)
	}
	locs, err := s.ListLocations(ctx)
	if err != nil || len(locs) != 1 || locs[0].MainWorkspaceID != "ws1" {
		t.Fatalf("locations = %+v err=%v", locs, err)
	}
	wsGot, err := s.GetWorkspace(context.Background(), "ws1")
	if err != nil || wsGot.ID != "ws1" {
		t.Fatalf("workspace = %+v err=%v", wsGot, err)
	}
}

// TestCreateProjectRollsBackOnError 验证任一行失败整体回滚（location 唯一冲突）。
func TestCreateProjectRollsBackOnError(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	project := controlplane.Project{ID: "p1", Name: "p", CreatedAt: now(), UpdatedAt: now()}
	loc := controlplane.ProjectLocation{
		ID: "loc1", ProjectID: "p1", MachineID: "m1", MachineKind: controlplane.MachineKindLocal,
		Role: controlplane.LocationRoleLocal, Source: controlplane.LocationSourceExistingPath,
		CreatedAt: now(), UpdatedAt: now(),
	}
	if _, err := s.CreateProject(ctx, project, []controlplane.ProjectLocation{loc}, nil, nil); err != nil {
		t.Fatalf("首次 CreateProject: %v", err)
	}
	// 不同 location ID 争用同 (project_id, role)：唯一索引应让整个事务回滚。
	conflict := loc
	conflict.ID = "loc2"
	if _, err := s.CreateProject(ctx, project, []controlplane.ProjectLocation{conflict}, nil, nil); err == nil {
		t.Fatal("重复 (project_id, role) 应被唯一索引拒绝")
	}
	// 回滚后不应出现重复 project/location
	projects, _ := s.ListProjects(ctx)
	if len(projects) != 1 {
		t.Fatalf("回滚后 projects = %d, want 1", len(projects))
	}
	locs, _ := s.ListLocations(ctx)
	if len(locs) != 1 {
		t.Fatalf("回滚后 locations = %d, want 1", len(locs))
	}
}

func TestCreateProjectRollsBackAggregateWhenFinalOperationMissing(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	project := controlplane.Project{ID: "p-atomic", Name: "p", CreatedAt: now(), UpdatedAt: now()}
	operation := controlplane.Operation{OperationID: "missing-operation", Kind: controlplane.OperationKindCreateProject,
		State: controlplane.OperationStateSucceeded, ProjectID: project.ID, CreatedAt: now(), UpdatedAt: now()}
	if _, err := s.CreateProject(context.Background(), project, nil, nil, &operation); err == nil {
		t.Fatal("final operation 不存在时应回滚整个项目聚合")
	}
	projects, err := s.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("rollback 后 projects=%+v", projects)
	}
}

// TestAdoptWorkspacePreservesIDAndTasks 验证归并保留 Workspace ID 与 Task 引用。
func TestAdoptWorkspacePreservesIDAndTasks(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	canonical := t.TempDir()
	ws, err := s.ResolveWorkspaceForPath(ctx, "m-local", canonical, canonical)
	if err != nil {
		t.Fatalf("ResolveWorkspaceForPath: %v", err)
	}
	if ws.Kind != controlplane.WorkspaceKindDetached {
		t.Fatalf("新解析应为 detached: %+v", ws)
	}
	// 挂一个 Task 引用该 Workspace
	task := protoTask("t1", ws.ID, "m-local")
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := s.AdoptWorkspace(ctx, ws.ID, "loc-main"); err != nil {
		t.Fatalf("AdoptWorkspace: %v", err)
	}
	got, err := s.GetWorkspace(context.Background(), ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.Kind != controlplane.WorkspaceKindMain {
		t.Fatalf("归并后 kind = %s, want main", got.Kind)
	}
	if got.LocationID == nil || *got.LocationID != "loc-main" {
		t.Fatalf("归并后 location_id = %v, want loc-main", got.LocationID)
	}
	if got.ID != ws.ID {
		t.Fatalf("归并不应改变 Workspace ID: %s -> %s", ws.ID, got.ID)
	}
	// Task 的 workspace_id 保持不变（不批量重写）
	gotTask, err := s.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if gotTask.WorkspaceID != ws.ID {
		t.Fatalf("归并不应改 Task workspace_id: %q", gotTask.WorkspaceID)
	}
}

// TestAdoptWorkspaceNotFound 验证归并不存在的 workspace 报 ErrNotFound。
func TestAdoptWorkspaceNotFound(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.AdoptWorkspace(context.Background(), "nope", "loc1"); err != store.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
