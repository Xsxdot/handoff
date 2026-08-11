package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// openTestStore 开一个临时库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestRepoCRUD 覆盖登记的写入、读取、列出、删除。
func TestRepoCRUD(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	r := &proto.Repo{Name: "handoff", Path: "/root/work/handoff",
		OriginURL: "git@github.com:xushixin/handoff.git", CreatedAt: now}
	if err := st.CreateRepo(r); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	got, err := st.GetRepoByName("handoff")
	if err != nil {
		t.Fatalf("GetRepoByName: %v", err)
	}
	if got.Path != r.Path || got.OriginURL != r.OriginURL {
		t.Fatalf("回读不一致: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 回读为零值")
	}
	list, err := st.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRepos 返回 %d 条，want 1", len(list))
	}
	if err := st.DeleteRepo("handoff"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if _, err := st.GetRepoByName("handoff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后 Get 应为 ErrNotFound，got %v", err)
	}
}

// TestGetRepoByNameNotFound 验证查不到时返回 ErrNotFound 而不是零值。
func TestGetRepoByNameNotFound(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.GetRepoByName("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDeleteRepoNotFound 验证删不存在的登记时报 ErrNotFound（而非静默成功）。
func TestDeleteRepoNotFound(t *testing.T) {
	st := openTestStore(t)
	if err := st.DeleteRepo("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestCreateRepoDuplicateName 验证重名冲突归到 ErrRepoDuplicate。
func TestCreateRepoDuplicateName(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p1", OriginURL: "u1", CreatedAt: now}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p2", OriginURL: "u2", CreatedAt: now})
	if !errors.Is(err, ErrRepoDuplicate) {
		t.Fatalf("err = %v, want ErrRepoDuplicate", err)
	}
}

// TestCreateRepoDuplicatePath 验证同一路径不得被登记两次——两个名字指向同一
// 路径会同时破坏 origin 自动匹配与工作目录占用判定。
func TestCreateRepoDuplicatePath(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	if err := st.CreateRepo(&proto.Repo{Name: "a", Path: "/p", OriginURL: "u1", CreatedAt: now}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	err := st.CreateRepo(&proto.Repo{Name: "b", Path: "/p", OriginURL: "u2", CreatedAt: now})
	if !errors.Is(err, ErrRepoDuplicate) {
		t.Fatalf("err = %v, want ErrRepoDuplicate", err)
	}
}

// TestActiveTasksByRepoPath 验证只返回该仓库下的非终态任务。
func TestActiveTasksByRepoPath(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	mk := func(id string, state proto.TaskState) {
		if err := st.CreateTask(&proto.Task{ID: id, RepoPath: "/root/work/handoff",
			State: state, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("CreateTask %s: %v", id, err)
		}
	}
	mk("t-running", proto.TaskStateRunning)
	mk("t-done", proto.TaskStateCompleted)
	if err := st.CreateTask(&proto.Task{ID: "t-other", RepoPath: "/elsewhere",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask t-other: %v", err)
	}
	tasks, err := st.ActiveTasksByRepoPath("/root/work/handoff")
	if err != nil {
		t.Fatalf("ActiveTasksByRepoPath: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t-running" {
		t.Fatalf("got %+v, want 仅 t-running", tasks)
	}
	if got, _ := st.ActiveTasksByRepoPath(""); len(got) != 0 {
		t.Error("空路径应返回空切片")
	}
}
