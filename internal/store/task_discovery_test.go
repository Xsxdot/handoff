package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestTaskDiscoveryUsesCreationSequenceAndReportsPendingTickets(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	for _, task := range []proto.Task{
		{ID: "newer-clock", RepoPath: "/repo", State: proto.TaskStateRunning, CreatedAt: now.Add(time.Hour), UpdatedAt: now},
		{ID: "older-clock", RepoPath: "/repo", State: proto.TaskStateRunning, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	} {
		if err := st.CreateTask(&task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateTicket(&proto.Ticket{ID: "older-clock:q1", TaskID: "older-clock", Kind: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first, err := st.DiscoverTasks(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].TaskID != "newer-clock" || first.Items[0].Seq != 1 || !first.HasMore || first.NextSeq != 1 {
		t.Fatalf("first discovery page=%+v; seq must follow creation, not clock time", first)
	}
	second, err := st.DiscoverTasks(first.NextSeq, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].TaskID != "older-clock" || second.Items[0].Seq != 2 || second.HasMore || second.NextSeq != 2 || !second.Items[0].HasPendingTickets {
		t.Fatalf("second discovery page=%+v; pending ticket fact lost", second)
	}
	empty, err := st.DiscoverTasks(second.NextSeq, 10)
	if err != nil || len(empty.Items) != 0 || empty.NextSeq != second.NextSeq || empty.HasMore {
		t.Fatalf("empty discovery page=%+v err=%v", empty, err)
	}
	if _, err := st.DiscoverTasks(-1, 1); err == nil {
		t.Fatal("negative task discovery cursor must fail")
	}
	if _, err := st.DiscoverTasks(0, 101); err == nil {
		t.Fatal("task discovery must cap pages at 100")
	}
	newTask := proto.Task{ID: "later-create", RepoPath: "/repo", State: proto.TaskStateRunning, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	if err := st.CreateTask(&newTask); err != nil {
		t.Fatal(err)
	}
	third, err := st.DiscoverTasks(2, 10)
	if err != nil || len(third.Items) != 1 || third.Items[0].Seq != 3 || third.Items[0].TaskID != newTask.ID {
		t.Fatalf("new task sequence did not continue monotonically: page=%+v err=%v", third, err)
	}
}

func TestTaskDiscoveryBackfillsLegacyTasksOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE tasks (
		id TEXT PRIMARY KEY, target TEXT NOT NULL DEFAULT '', repo_path TEXT NOT NULL,
		state TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (id,repo_path,state,created_at,updated_at) VALUES
		('old-a','/repo','running','2026-10-22T00:00:00Z','2026-10-22T00:00:00Z'),
		('old-b','/repo','running','2026-10-22T00:00:00Z','2026-10-22T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	page, err := st.DiscoverTasks(0, 100)
	if err != nil || len(page.Items) != 2 || page.Items[0].Seq != 1 || page.Items[0].TaskID != "old-a" ||
		page.Items[1].Seq != 2 || page.Items[1].TaskID != "old-b" {
		t.Fatalf("legacy discovery backfill=%+v err=%v", page, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	page, err = st.DiscoverTasks(0, 100)
	if err != nil || len(page.Items) != 2 || page.Items[0].Seq != 1 || page.Items[1].Seq != 2 {
		t.Fatalf("reopening must not renumber backfill: page=%+v err=%v", page, err)
	}
	var backfilled int
	if err := st.db.QueryRow(`SELECT legacy_backfill_done FROM task_creation_seq_meta WHERE singleton=1`).Scan(&backfilled); err != nil || backfilled != 1 {
		t.Fatalf("legacy backfill marker=%d err=%v", backfilled, err)
	}
}
