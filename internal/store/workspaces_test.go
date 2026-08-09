// store workspaces.go 测试：canonical path 规范化与 detached Workspace 复用、
// 旧任务迁移的原子性与归并。
//
// 职责：
//   - 旧 Task 按 Task.Workdir() 绑定 local Machine 与稳定 detached Workspace
//   - 两个旧 Task 指向同一 canonical path 时复用一个 Workspace
//   - 迁移失败时不出现「只写了 machine_id 没写 workspace_id」的半迁移状态
//   - TaskState 保持原值，不因迁移制造新生命周期事件
//
// 边界：
//   - 使用真实 SQLite 文件（t.TempDir），不用 mock
//   - 不覆盖 Reconcile 扫描（由 machineauthority 包测试负责）
package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// osMkdirAll 是 os.MkdirAll 的测试别名（保留语义清晰）。
func osMkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// osSymlink 是 os.Symlink 的测试别名。
func osSymlink(oldname, newname string) error { return os.Symlink(oldname, newname) }

// now 返回当前 UTC 时间。
func now() time.Time { return time.Now().UTC() }

// protoTask 构造一个绑定到指定 machine/workspace 的 Task。
func protoTask(id, workspaceID, machineID string) *proto.Task {
	return &proto.Task{ID: id, RepoPath: "/r", State: proto.TaskStatePending,
		MachineID: machineID, WorkspaceID: workspaceID, CreatedAt: now(), UpdatedAt: now()}
}

// TestCanonicalPathNormalization 验证 macOS 路径规范化：
// 绝对路径 clean、symlink 解析为真实路径。
func TestCanonicalPathNormalization(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := osMkdirAll(real); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := osSymlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := store.CanonicalPath(real)
	if err != nil {
		t.Fatalf("CanonicalPath(real): %v", err)
	}
	// macOS 上 /var 是 /private/var 的 symlink，EvalSymlinks 会解析；期望值取解析后的真实路径。
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks(real): %v", err)
	}
	if got != realResolved {
		t.Fatalf("canonical(real) = %q, want %q", got, realResolved)
	}
	gotLink, err := store.CanonicalPath(link)
	if err != nil {
		t.Fatalf("CanonicalPath(link): %v", err)
	}
	if gotLink != realResolved {
		t.Fatalf("canonical(link) = %q, want %q（symlink 应解析到真实路径）", gotLink, realResolved)
	}
	// 带 ./ 与尾部分隔符的绝对路径应 clean 归一
	gotClean, err := store.CanonicalPath(filepath.Join(dir, "real", ".", "..", "real"))
	if err != nil {
		t.Fatalf("CanonicalPath(clean): %v", err)
	}
	if gotClean != realResolved {
		t.Fatalf("canonical(clean) = %q, want %q", gotClean, realResolved)
	}
}

// TestMigrateLegacyTaskBindsLocalMachine 验证旧任务迁移：
// machine_id/workspace_id 为空的任务绑定本机 Machine 与 detached Workspace。
func TestMigrateLegacyTaskBindsLocalMachine(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	local, err := s.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatalf("EnsureLocalMachine: %v", err)
	}
	workdir := t.TempDir()
	task := &proto.Task{ID: "t1", RepoPath: workdir, State: proto.TaskStatePending,
		WorkDir: workdir, CreatedAt: now(), UpdatedAt: now()}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	bound, err := s.MigrateLegacyTasks(context.Background(), local.ID)
	if err != nil {
		t.Fatalf("MigrateLegacyTasks: %v", err)
	}
	if bound != 1 {
		t.Fatalf("迁移任务数 = %d, want 1", bound)
	}
	got, err := s.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.MachineID != local.ID {
		t.Fatalf("machine_id = %q, want %q", got.MachineID, local.ID)
	}
	if got.WorkspaceID == "" {
		t.Fatal("workspace_id 不应为空")
	}
	// 状态保持原值，不制造新生命周期事件
	if got.State != proto.TaskStatePending {
		t.Fatalf("state = %s, want pending（迁移不得改变状态）", got.State)
	}
}

// TestMigrateLegacyTaskReusesDetachedWorkspace 验证两个旧任务指向同一 canonical
// path 时复用一个 detached Workspace。
func TestMigrateLegacyTaskReusesDetachedWorkspace(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	local, _ := s.EnsureLocalMachine(context.Background(), "本机")
	workdir := t.TempDir()
	for _, id := range []string{"t1", "t2"} {
		if err := s.CreateTask(&proto.Task{ID: id, RepoPath: workdir, State: proto.TaskStateRunning,
			WorkDir: workdir, CreatedAt: now(), UpdatedAt: now()}); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}
	if _, err := s.MigrateLegacyTasks(context.Background(), local.ID); err != nil {
		t.Fatalf("MigrateLegacyTasks: %v", err)
	}
	t1, _ := s.GetTask("t1")
	t2, _ := s.GetTask("t2")
	if t1.WorkspaceID != t2.WorkspaceID {
		t.Fatalf("同一 canonical path 应复用同一 Workspace: %q vs %q", t1.WorkspaceID, t2.WorkspaceID)
	}
}

// TestMigrateLegacyTasksIdempotent 验证迁移幂等：重复迁移不再新增 Workspace、
// 任务数不重复绑定。
func TestMigrateLegacyTasksIdempotent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	local, _ := s.EnsureLocalMachine(context.Background(), "本机")
	workdir := t.TempDir()
	if err := s.CreateTask(&proto.Task{ID: "t1", RepoPath: workdir, State: proto.TaskStateRunning,
		WorkDir: workdir, CreatedAt: now(), UpdatedAt: now()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if n, err := s.MigrateLegacyTasks(context.Background(), local.ID); err != nil || n != 1 {
		t.Fatalf("首次迁移: n=%d err=%v", n, err)
	}
	t1, _ := s.GetTask("t1")
	wsID := t1.WorkspaceID
	if n, err := s.MigrateLegacyTasks(context.Background(), local.ID); err != nil || n != 0 {
		t.Fatalf("二次迁移: n=%d err=%v, want 0", n, err)
	}
	t1b, _ := s.GetTask("t1")
	if t1b.WorkspaceID != wsID {
		t.Fatalf("幂等迁移改变 workspace_id: %q -> %q", wsID, t1b.WorkspaceID)
	}
}

// TestMigrateLegacyTaskUpsertsTaskSummary 验证迁移同时 upsert task_summaries，
// 桌面左栏可直接读到旧任务。
func TestMigrateLegacyTaskUpsertsTaskSummary(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	local, _ := s.EnsureLocalMachine(context.Background(), "本机")
	workdir := t.TempDir()
	task := &proto.Task{ID: "t1", RepoPath: workdir, State: proto.TaskStateCompleted,
		Name: "重构", Executor: "opencode", WorkDir: workdir,
		CreatedAt: now(), UpdatedAt: now()}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := s.MigrateLegacyTasks(context.Background(), local.ID); err != nil {
		t.Fatalf("MigrateLegacyTasks: %v", err)
	}
	summaries, err := s.ListTaskSummaries()
	if err != nil {
		t.Fatalf("ListTaskSummaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].TaskID != "t1" {
		t.Fatalf("summaries = %+v, want [t1]", summaries)
	}
	if summaries[0].MachineID != local.ID || summaries[0].WorkspaceID == "" {
		t.Fatalf("summary 归属字段缺失: %+v", summaries[0])
	}
	if summaries[0].State != "completed" {
		t.Fatalf("summary state = %s, want completed", summaries[0].State)
	}
}
