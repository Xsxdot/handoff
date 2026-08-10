// controlplane ProjectService 测试：项目创建的表单不变量、幂等与部分成功。
//
// 职责：
//   - 覆盖全部表单不变量：无 Location、两个 local、两个 remote、
//     role/machine 不一致、远端路径非绝对、clone 缺 URL、空 clone path 默认值、
//     本机与远端 identity 不一致
//   - 覆盖幂等/部分成功：相同 operation_id 只调用目标一次；部分成功保留成功目录；
//     failed 不创建空 Project
//   - 覆盖 detached 归并：adoption 保留 Workspace ID 与 Task workspace_id
//
// 边界：
//   - 使用内存 fake MachineCommander 与 fake Repository，不依赖真实 git/store
//     （命令/存储语义由 machineauthority/store 专项测试覆盖）
package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

// fakeCommander 是可编程的 MachineCommander。
type fakeCommander struct {
	inspectResults map[string]PathInspection
	inspectErrs    map[string]error
	cloneResults   map[string]PathInspection
	cloneErrs      map[string]error
	inspectCalls   map[string]int
	cloneCalls     map[string]int
}

func (f *fakeCommander) InspectPath(_ context.Context, cmd InspectPathCommand) (PathInspection, error) {
	f.inspectCalls[cmd.TargetID]++
	if err := f.inspectErrs[cmd.TargetID]; err != nil {
		return PathInspection{}, err
	}
	return f.inspectResults[cmd.TargetID], nil
}

func (f *fakeCommander) Clone(_ context.Context, cmd CloneLocationCommand) (PathInspection, error) {
	f.cloneCalls[cmd.TargetID]++
	if err := f.cloneErrs[cmd.TargetID]; err != nil {
		return PathInspection{}, err
	}
	return f.cloneResults[cmd.TargetID], nil
}

// fakeProjectRepo 是 ProjectService 用到的 Repository 子集（内存实现）。
type fakeProjectRepo struct {
	projects     map[string]Project
	locations    map[string]ProjectLocation
	workspaces   map[string]Workspace
	operations   map[string]Operation
	machines     map[string]Machine
	events       []ControlEvent
	lastRevision int64
	createErr    error
}

func newFakeProjectRepo() *fakeProjectRepo {
	return &fakeProjectRepo{
		projects: map[string]Project{}, locations: map[string]ProjectLocation{},
		workspaces: map[string]Workspace{}, operations: map[string]Operation{},
		machines: map[string]Machine{
			"m-local":  {ID: "m-local", Kind: MachineKindLocal},
			"m-remote": {ID: "m-remote", Kind: MachineKindRemote},
		},
	}
}

func (f *fakeProjectRepo) GetMachine(_ context.Context, id string) (Machine, error) {
	m, ok := f.machines[id]
	if !ok {
		return Machine{}, ErrNotFound
	}
	return m, nil
}

func (f *fakeProjectRepo) CreateProject(_ context.Context, p Project, locs []ProjectLocation, ws []Workspace,
	finalOperation *Operation) ([]ControlEvent, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.projects[p.ID] = p
	for _, l := range locs {
		f.locations[l.ID] = l
	}
	for _, w := range ws {
		f.workspaces[w.ID] = w
	}
	values := []struct {
		kind ControlEventKind
		id   string
	}{{ControlEventKindProjectUpsert, p.ID}}
	for _, location := range locs {
		values = append(values, struct {
			kind ControlEventKind
			id   string
		}{ControlEventKindLocationUpsert, location.ID})
	}
	for _, workspace := range ws {
		values = append(values, struct {
			kind ControlEventKind
			id   string
		}{ControlEventKindWorkspaceUpsert, workspace.ID})
	}
	if finalOperation != nil {
		f.operations[finalOperation.OperationID] = *finalOperation
		values = append(values, struct {
			kind ControlEventKind
			id   string
		}{ControlEventKindOperationUpsert, finalOperation.OperationID})
	}
	events := make([]ControlEvent, 0, len(values))
	for _, value := range values {
		f.lastRevision++
		event := ControlEvent{ControlRevision: f.lastRevision, Kind: value.kind, ResourceID: value.id}
		f.events = append(f.events, event)
		events = append(events, event)
	}
	return events, nil
}

func (f *fakeProjectRepo) GetOperation(_ context.Context, id string) (Operation, error) {
	op, ok := f.operations[id]
	if !ok {
		return Operation{}, ErrNotFound
	}
	return op, nil
}

func (f *fakeProjectRepo) CreateOperation(_ context.Context, op Operation) (ControlEvent, error) {
	f.operations[op.OperationID] = op
	f.lastRevision++
	return ControlEvent{ControlRevision: f.lastRevision, Kind: ControlEventKindOperationUpsert, ResourceID: op.OperationID}, nil
}

func (f *fakeProjectRepo) UpdateOperation(_ context.Context, op Operation) (ControlEvent, error) {
	f.operations[op.OperationID] = op
	f.lastRevision++
	return ControlEvent{ControlRevision: f.lastRevision, Kind: ControlEventKindOperationUpsert, ResourceID: op.OperationID}, nil
}

func (f *fakeProjectRepo) ListOperations(_ context.Context) ([]Operation, error) {
	out := make([]Operation, 0, len(f.operations))
	for _, op := range f.operations {
		out = append(out, op)
	}
	return out, nil
}

func (f *fakeProjectRepo) EnsureLocalMachine(_ context.Context, _ string) (Machine, error) {
	return Machine{ID: "m-local", Kind: MachineKindLocal}, nil
}

func (f *fakeProjectRepo) SyncConfiguredMachines(context.Context, []ConfiguredMachine) ([]Machine, error) {
	return nil, nil
}

func (f *fakeProjectRepo) MigrateLegacyTasks(context.Context, string) (int, error) { return 0, nil }

func (f *fakeProjectRepo) Snapshot(_ context.Context) (Snapshot, error) { return Snapshot{}, nil }

func (f *fakeProjectRepo) UpsertWorkspaceWithMachineEvent(context.Context, Workspace, MachineEventKind) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeProjectRepo) RemoveWorkspaceWithMachineEvent(context.Context, string, string) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeProjectRepo) UpsertGitRefsWithMachineEvents(context.Context, string, []GitRef) ([]MachineEvent, error) {
	return nil, nil
}

func (f *fakeProjectRepo) AppendTaskSummaryEvent(context.Context, TaskSummary) (MachineEvent, error) {
	return MachineEvent{}, nil
}

func (f *fakeProjectRepo) ApplyMachineEvent(context.Context, MachineEvent) (ControlEvent, bool, error) {
	return ControlEvent{}, false, nil
}

func (f *fakeProjectRepo) MachineEventsAfter(context.Context, string, int64, int) ([]MachineEvent, error) {
	return nil, nil
}

func (f *fakeProjectRepo) ControlEventsAfter(context.Context, int64, int) ([]ControlEvent, error) {
	return nil, nil
}

func (f *fakeProjectRepo) GetWorkspace(_ context.Context, id string) (Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	return ws, nil
}

func (f *fakeProjectRepo) GetProject(_ context.Context, id string) (Project, error) {
	project, ok := f.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	return project, nil
}

func (f *fakeProjectRepo) ResolveWorkspaceForPath(_ context.Context, machineID, canonical, displayPath string) (Workspace, error) {
	return Workspace{ID: "ws-resolved", MachineID: machineID, Kind: WorkspaceKindDetached,
		CanonicalPath: canonical, Path: displayPath}, nil
}

func (f *fakeProjectRepo) AdoptWorkspace(_ context.Context, wsID, locationID string) error {
	ws, ok := f.workspaces[wsID]
	if !ok {
		return ErrNotFound
	}
	ws.LocationID = &locationID
	ws.Kind = WorkspaceKindMain
	f.workspaces[wsID] = ws
	return nil
}

func (f *fakeProjectRepo) ListLocationsForMachine(context.Context, string) ([]ProjectLocation, error) {
	return nil, nil
}

var _ = (*fakeProjectRepo)(nil)

// newFakeProjectService 组装 ProjectService 测试环境。
func newFakeProjectService(t *testing.T, cmd *fakeCommander, repo *fakeProjectRepo) *ProjectService {
	t.Helper()
	if cmd == nil {
		cmd = &fakeCommander{inspectResults: map[string]PathInspection{}, inspectCalls: map[string]int{},
			cloneResults: map[string]PathInspection{}, cloneCalls: map[string]int{}}
	}
	if repo == nil {
		repo = newFakeProjectRepo()
	}
	cmd.inspectCalls = map[string]int{}
	cmd.cloneCalls = map[string]int{}
	svc := NewProjectService(repo, cmd, slog.New(slog.NewTextHandler(discard{}, nil)))
	return svc
}

// mustDiscard 是测试日志丢弃实现（与 bootstrap_test 一致）。
type mustDiscard struct{}

func (mustDiscard) Write(p []byte) (int, error) { return len(p), nil }

var _ = mustDiscard{}

// TestCreateProjectRejectsNoLocations 覆盖「无 Location 拒绝」。
func TestCreateProjectRejectsNoLocations(t *testing.T) {
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, nil, repo)
	var published []ControlEvent
	svc.SetControlEventPublisher(func(event ControlEvent) { published = append(published, event) })
	op, err := svc.Create(context.Background(), CreateProjectCommand{OperationID: "op1", Name: "p", Locations: nil})
	if err != nil {
		t.Fatalf("业务校验失败应返回可查询的 failed operation: %v", err)
	}
	if op.State != OperationStateFailed || repo.operations["op1"].State != OperationStateFailed {
		t.Fatalf("operation=%+v persisted=%+v, want failed", op, repo.operations["op1"])
	}
	if len(published) != 2 || published[0].Kind != ControlEventKindOperationUpsert ||
		published[1].Kind != ControlEventKindOperationUpsert {
		t.Fatalf("校验失败应发布 pending + failed，events=%+v", published)
	}
}

// TestCreateProjectRejectsTwoLocalOrTwoRemote 覆盖双 local/双 remote 拒绝。
func TestCreateProjectRejectsTwoLocalOrTwoRemote(t *testing.T) {
	svc := newFakeProjectService(t, nil, nil)
	twoLocal := CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/a"},
		{TargetID: "t2", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/b"},
	}}
	if op, err := svc.Create(context.Background(), twoLocal); err != nil || op.State != OperationStateFailed {
		t.Fatalf("两个 local Location 应产生 failed operation，op=%+v err=%v", op, err)
	}
	twoRemote := CreateProjectCommand{OperationID: "op2", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/a"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/b"},
	}}
	if op, err := svc.Create(context.Background(), twoRemote); err != nil || op.State != OperationStateFailed {
		t.Fatalf("两个 remote Location 应产生 failed operation，op=%+v err=%v", op, err)
	}
}

// TestCreateProjectRejectsRoleMachineMismatch 覆盖 role 与 machine kind 不一致拒绝。
func TestCreateProjectRejectsRoleMachineMismatch(t *testing.T) {
	svc := newFakeProjectService(t, nil, nil)
	cmd := CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-remote", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/a"},
	}}
	if op, err := svc.Create(context.Background(), cmd); err != nil || op.State != OperationStateFailed {
		t.Fatalf("local role 配 remote machine 应产生 failed operation，op=%+v err=%v", op, err)
	}
}

// TestCreateProjectRejectsRemoteNonAbsolutePath 覆盖远端已有路径必须绝对。
func TestCreateProjectRejectsRemoteNonAbsolutePath(t *testing.T) {
	svc := newFakeProjectService(t, nil, nil)
	cmd := CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "relative/path"},
	}}
	if op, err := svc.Create(context.Background(), cmd); err != nil || op.State != OperationStateFailed {
		t.Fatalf("远端相对路径应产生 failed operation，op=%+v err=%v", op, err)
	}
}

func TestCreateProjectRejectsBlankNameAndRelativeOwnerPaths(t *testing.T) {
	service := newFakeProjectService(t, nil, nil)
	cases := []CreateProjectCommand{
		{OperationID: "blank-name", Name: "  ", Locations: []CreateLocationCommand{
			{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/repo"},
		}},
		{OperationID: "local-relative", Name: "p", Locations: []CreateLocationCommand{
			{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "relative/repo"},
		}},
		{OperationID: "clone-relative", Name: "p", Locations: []CreateLocationCommand{
			{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceGitClone,
				GitURL: "https://github.com/o/r.git", ClonePath: "relative/repo"},
		}},
	}
	for _, command := range cases {
		if op, err := service.Create(context.Background(), command); err != nil || op.State != OperationStateFailed {
			t.Errorf("command %s 应产生 failed operation，op=%+v err=%v", command.OperationID, op, err)
		}
	}
}

// TestCreateProjectRejectsCloneWithoutURL 覆盖 clone 必须有 Git URL。
func TestCreateProjectRejectsCloneWithoutURL(t *testing.T) {
	svc := newFakeProjectService(t, nil, nil)
	cmd := CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceGitClone, ClonePath: "/tmp/x"},
	}}
	if op, err := svc.Create(context.Background(), cmd); err != nil || op.State != OperationStateFailed {
		t.Fatalf("clone 缺 Git URL 应产生 failed operation，op=%+v err=%v", op, err)
	}
}

// TestCreateProjectDefaultClonePath 覆盖空 clone path 自动变成 ~/.handoff/<repo-name>。
func TestCreateProjectDefaultClonePath(t *testing.T) {
	cmd := &fakeCommander{
		cloneResults: map[string]PathInspection{
			"t1": {Path: "/home/me/.handoff/r", CanonicalPath: "/home/me/.handoff/r",
				IsRepo: true, RepoIdentity: "github.com/o/r"},
		},
		inspectCalls: map[string]int{}, cloneCalls: map[string]int{},
	}
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, cmd, repo)
	op, err := svc.Create(context.Background(), CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceGitClone,
			GitURL: "https://github.com/o/r.git", ClonePath: ""},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if op.State != OperationStateSucceeded {
		t.Fatalf("operation state = %s, want succeeded", op.State)
	}
	if cmd.cloneCalls["t1"] != 1 {
		t.Fatalf("clone 调用 %d 次, want 1", cmd.cloneCalls["t1"])
	}
	// 已调用 clone：默认 clone path 逻辑需在 Create 内部填充后传给 commander，
	// 这里断言 clone 确实被调用且 operation 成功
	if len(repo.projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(repo.projects))
	}
}

func TestCreateProjectDoesNotPersistGitURLCredentials(t *testing.T) {
	commander := &fakeCommander{cloneResults: map[string]PathInspection{
		"t1": {Path: "/repo", CanonicalPath: "/repo", IsRepo: true, RepoIdentity: "github.com/o/r"},
	}}
	repo := newFakeProjectRepo()
	service := newFakeProjectService(t, commander, repo)
	operation, err := service.Create(context.Background(), CreateProjectCommand{
		OperationID: "op-secret-url", Name: "p", Locations: []CreateLocationCommand{{
			TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal,
			Source: LocationSourceGitClone, GitURL: "https://user:secret@github.com/o/r.git?token=hidden",
			ClonePath: "/repo",
		}},
	})
	if err != nil || operation.State != OperationStateSucceeded {
		t.Fatalf("Create state=%s err=%v", operation.State, err)
	}
	for _, location := range repo.locations {
		if location.GitURL != "https://github.com/o/r.git" {
			t.Fatalf("persisted git_url=%q, want credentials/query removed", location.GitURL)
		}
	}
}

// TestSafeGitURLForStorage 覆盖 URL 与 SCP-like 地址中的 userinfo、query、
// fragment 都不会进入控制面持久化。
func TestSafeGitURLForStorage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "https credentials and query",
			raw:  "https://user:secret@github.com/o/r.git?token=hidden#fragment",
			want: "https://github.com/o/r.git",
		},
		{
			name: "scp userinfo",
			raw:  "deploy-token@github.com:o/r.git",
			want: "github.com:o/r.git",
		},
		{
			name: "local path",
			raw:  "/srv/git/r.git",
			want: "/srv/git/r.git",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeGitURLForStorage(test.raw); got != test.want {
				t.Fatalf("safeGitURLForStorage(%q)=%q, want %q", test.raw, got, test.want)
			}
		})
	}
}

// TestCreateProjectRejectsIdentityMismatch 覆盖本机与远端 identity 不一致拒绝。
func TestCreateProjectRejectsIdentityMismatch(t *testing.T) {
	cmd := &fakeCommander{
		inspectResults: map[string]PathInspection{
			"t1": {Path: "/local/r", CanonicalPath: "/local/r", IsRepo: true, RepoIdentity: "github.com/a/x"},
			"t2": {Path: "/remote/r", CanonicalPath: "/remote/r", IsRepo: true, RepoIdentity: "github.com/b/y"},
		},
		inspectErrs: map[string]error{}, inspectCalls: map[string]int{}, cloneCalls: map[string]int{},
	}
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, cmd, repo)
	op, err := svc.Create(context.Background(), CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/local/r"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/remote/r"},
	}})
	if err != nil {
		t.Fatalf("Create 不应整体失败（应返回 failed operation）: %v", err)
	}
	if op.State != OperationStateFailed {
		t.Fatalf("identity 不一致应产生 failed operation，实际 %s", op.State)
	}
	if len(repo.projects) != 0 {
		t.Fatalf("identity 不一致不应创建空 Project")
	}
}

// TestCreateProjectPartialSuccessKeepsSuccessTarget 覆盖部分成功：一个成功、一个
// 失败时 Operation=partial，Project 保存成功 Location。
func TestCreateProjectPartialSuccessKeepsSuccessTarget(t *testing.T) {
	cmd := &fakeCommander{
		inspectResults: map[string]PathInspection{
			"t1": {Path: "/local/r", CanonicalPath: "/local/r", IsRepo: true, RepoIdentity: "github.com/o/r"},
		},
		inspectErrs: map[string]error{
			"t2": errors.New("远端不可达"),
		},
		inspectCalls: map[string]int{}, cloneCalls: map[string]int{},
	}
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, cmd, repo)
	op, err := svc.Create(context.Background(), CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/local/r"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/remote/r"},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if op.State != OperationStatePartial {
		t.Fatalf("部分成功应标记 partial，实际 %s", op.State)
	}
	if len(repo.projects) != 1 {
		t.Fatalf("部分成功应保存成功 Location 的 Project，实际 %d", len(repo.projects))
	}
	// 成功目录（location）保留：project 里有 local location
	if len(repo.locations) != 1 {
		t.Fatalf("成功 Location 应保留，实际 %d", len(repo.locations))
	}
}

// TestCreateProjectPartialRetryOnlyRunsFailedTarget 覆盖 durable Operation 的核心
// 重试语义：partial 不是终点；同 ID 重试保留已成功目标，只补失败目标。
func TestCreateProjectPartialRetryOnlyRunsFailedTarget(t *testing.T) {
	commander := &fakeCommander{
		inspectResults: map[string]PathInspection{
			"t1": {Path: "/local/r", CanonicalPath: "/local/r", IsRepo: true, RepoIdentity: "github.com/o/r"},
			"t2": {Path: "/remote/r", CanonicalPath: "/remote/r", IsRepo: true, RepoIdentity: "github.com/o/r"},
		},
		inspectErrs: map[string]error{"t2": errors.New("远端暂时不可用")},
	}
	repo := newFakeProjectRepo()
	service := newFakeProjectService(t, commander, repo)
	command := CreateProjectCommand{OperationID: "op-partial-retry", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/local/r"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/remote/r"},
	}}

	first, err := service.Create(context.Background(), command)
	if err != nil || first.State != OperationStatePartial {
		t.Fatalf("first state=%s err=%v", first.State, err)
	}
	delete(commander.inspectErrs, "t2")
	second, err := service.Create(context.Background(), command)
	if err != nil || second.State != OperationStateSucceeded {
		t.Fatalf("retry state=%s err=%v", second.State, err)
	}
	if commander.inspectCalls["t1"] != 1 || commander.inspectCalls["t2"] != 2 {
		t.Fatalf("retry calls local=%d remote=%d, want 1/2",
			commander.inspectCalls["t1"], commander.inspectCalls["t2"])
	}
	if len(repo.locations) != 2 {
		t.Fatalf("locations=%d, want 2", len(repo.locations))
	}
}

// TestCreateProjectIdempotentRetry 覆盖同 ID 重试只补失败目标。
func TestCreateProjectIdempotentRetry(t *testing.T) {
	cmd := &fakeCommander{
		inspectResults: map[string]PathInspection{
			"t1": {Path: "/local/r", CanonicalPath: "/local/r", IsRepo: true, RepoIdentity: "github.com/o/r"},
			"t2": {Path: "/remote/r", CanonicalPath: "/remote/r", IsRepo: true, RepoIdentity: "github.com/o/r"},
		},
		inspectErrs: map[string]error{}, inspectCalls: map[string]int{}, cloneCalls: map[string]int{},
	}
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, cmd, repo)
	command := CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/local/r"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/remote/r"},
	}}
	// 首次全成功
	op, err := svc.Create(context.Background(), command)
	if err != nil || op.State != OperationStateSucceeded {
		t.Fatalf("首次 Create: state=%s err=%v", op.State, err)
	}
	// 重试同 ID：不应重复调用目标（幂等）
	if _, err := svc.Create(context.Background(), command); err != nil {
		t.Fatalf("重试 Create: %v", err)
	}
	if cmd.inspectCalls["t1"] != 1 || cmd.inspectCalls["t2"] != 1 {
		t.Fatalf("同 ID 重试应只调用每个目标一次: t1=%d t2=%d", cmd.inspectCalls["t1"], cmd.inspectCalls["t2"])
	}
}

func TestCreateProjectConcurrentSameOperationExecutesTargetOnce(t *testing.T) {
	commander := &fakeCommander{inspectResults: map[string]PathInspection{
		"t1": {Path: "/repo", CanonicalPath: "/repo"},
	}}
	service := newFakeProjectService(t, commander, newFakeProjectRepo())
	command := CreateProjectCommand{OperationID: "op-concurrent", Name: "p", Locations: []CreateLocationCommand{{
		TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal,
		Source: LocationSourceExistingPath, Path: "/repo",
	}}}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Create(context.Background(), command)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	if commander.inspectCalls["t1"] != 1 {
		t.Fatalf("concurrent same operation inspect calls=%d, want 1", commander.inspectCalls["t1"])
	}
}

func TestCreateProjectPublishesCommittedControlEvents(t *testing.T) {
	commander := &fakeCommander{inspectResults: map[string]PathInspection{
		"t1": {Path: "/repo", CanonicalPath: "/repo"},
	}}
	repo := newFakeProjectRepo()
	service := newFakeProjectService(t, commander, repo)
	var published []ControlEvent
	service.SetControlEventPublisher(func(event ControlEvent) { published = append(published, event) })

	operation, err := service.Create(context.Background(), CreateProjectCommand{
		OperationID: "op-events", Name: "p", Locations: []CreateLocationCommand{{
			TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal,
			Source: LocationSourceExistingPath, Path: "/repo",
		}},
	})
	if err != nil || operation.State != OperationStateSucceeded {
		t.Fatalf("Create state=%s err=%v", operation.State, err)
	}
	want := []ControlEventKind{
		ControlEventKindOperationUpsert,
		ControlEventKindOperationUpsert,
		ControlEventKindProjectUpsert,
		ControlEventKindLocationUpsert,
		ControlEventKindWorkspaceUpsert,
		ControlEventKindOperationUpsert,
	}
	if len(published) != len(want) {
		t.Fatalf("published=%d, want %d: %+v", len(published), len(want), published)
	}
	for index, kind := range want {
		if published[index].Kind != kind {
			t.Fatalf("published[%d]=%s, want %s", index, published[index].Kind, kind)
		}
	}
}

// TestDefaultClonePath 验证空 clone path 自动变成 ~/.handoff/<repo-name>。
func TestDefaultClonePath(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/o/r.git", "~/.handoff/r"},
		{"git@github.com:o/super-debug.git", "~/.handoff/super-debug"},
		{"https://gitlab.com/group/sub/repo", "~/.handoff/repo"},
		{"https://github.com/only.git", "~/.handoff/only"},
		{"https://user:secret@github.com/o/r.git?token=hidden#fragment", "~/.handoff/r"},
	}
	for _, c := range cases {
		if got := defaultClonePath(c.url); got != c.want {
			t.Errorf("defaultClonePath(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestCreateProjectBothFailNoEmptyProject 覆盖两目标都失败时不创建空 Project。
func TestCreateProjectBothFailNoEmptyProject(t *testing.T) {
	cmd := &fakeCommander{
		inspectErrs: map[string]error{
			"t1": errors.New("本地不可达"), "t2": errors.New("远端不可达"),
		},
		inspectResults: map[string]PathInspection{}, inspectCalls: map[string]int{}, cloneCalls: map[string]int{},
	}
	repo := newFakeProjectRepo()
	svc := newFakeProjectService(t, cmd, repo)
	op, err := svc.Create(context.Background(), CreateProjectCommand{OperationID: "op1", Name: "p", Locations: []CreateLocationCommand{
		{TargetID: "t1", MachineID: "m-local", Role: LocationRoleLocal, Source: LocationSourceExistingPath, Path: "/local/r"},
		{TargetID: "t2", MachineID: "m-remote", Role: LocationRoleRemote, Source: LocationSourceExistingPath, Path: "/remote/r"},
	}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if op.State != OperationStateFailed {
		t.Fatalf("两目标失败应 failed，实际 %s", op.State)
	}
	if len(repo.projects) != 0 {
		t.Fatalf("全失败不应创建空 Project")
	}
}
