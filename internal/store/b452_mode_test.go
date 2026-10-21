package store_test

// b452_mode_test.go —— B452 tasks.mode 列：落库往返与旧库迁移（缺席即空串 = 分支形态）。
import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func TestB452TaskModeRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	for id, mode := range map[string]string{"ro": proto.TaskModeReadonly, "br": ""} {
		if err := s.CreateTask(&proto.Task{ID: id, RepoPath: "/r", State: proto.TaskStatePending,
			CreatedAt: now, UpdatedAt: now, Mode: mode}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mode != mode {
			t.Fatalf("%s: GetTask Mode=%q want %q", id, got.Mode, mode)
		}
	}
	all, err := s.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, tk := range all {
		seen[tk.ID] = tk.Mode
	}
	if seen["ro"] != proto.TaskModeReadonly || seen["br"] != "" {
		t.Fatalf("ListTasks Mode 往返失败: %v", seen)
	}
}

func TestB452OpenMigratesModeColumnOnLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE tasks (
  id TEXT PRIMARY KEY, target TEXT NOT NULL DEFAULT '', repo_path TEXT NOT NULL,
  branch TEXT NOT NULL DEFAULT '', plan_path TEXT NOT NULL DEFAULT '',
  plan_summary TEXT NOT NULL DEFAULT '', executor_session TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO tasks (id, repo_path, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"legacy", "/r", proto.TaskStatePending, ts, ts); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("打开旧库: %v", err)
	}
	defer s.Close()
	got, err := s.GetTask("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "" {
		t.Fatalf("旧行 Mode 必须空串，实得 %q", got.Mode)
	}
}
