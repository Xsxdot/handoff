package store

import (
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestGetMirrorTaskReadsOneCachedSnapshotWithoutFanout(t *testing.T) {
	s := openTestStore(t)
	fetchedAt := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	task := proto.Task{ID: "task-uuid", State: proto.TaskStateWaitingReview, Executor: "opencode"}
	if err := s.UpsertMirrorTask("worker-a", task, fetchedAt); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMirrorTask(task.ID)
	if err != nil || !ok {
		t.Fatalf("GetMirrorTask = (%+v,%t,%v)", got, ok, err)
	}
	if got.Target != "worker-a" || got.Task.State != task.State || !got.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("cached mirror identity/state/fetch time mismatch: %+v", got)
	}
	if _, ok, err := s.GetMirrorTask("missing-task"); err != nil || ok {
		t.Fatalf("missing snapshot = (ok=%t, err=%v), want false,nil", ok, err)
	}
}
