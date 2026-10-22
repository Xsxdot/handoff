package store_test

// b453_rerun_test.go —— B453 重跑链链接列（F17）：rerun_of 随建任务落库，rerun_by 由
// SetTaskRerunBy 回写；缺席读回 nil（不编造空链接）。账号快照三列一并往返。
import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func TestB453RerunLinksRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	of := &proto.TaskRef{Task: "t-orig", Machine: "mac-mini"}
	if err := s.CreateTask(&proto.Task{ID: "t-new", RepoPath: "/r", State: proto.TaskStatePending,
		CreatedAt: now, UpdatedAt: now, RerunOf: of,
		Account: "og-mini", AccountKind: "opencode-go", AccountProxy: "socks5://127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTask(&proto.Task{ID: "t-orig", RepoPath: "/r", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("t-new")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.RerunOf, of) || got.RerunBy != nil {
		t.Fatalf("rerun_of 往返失败: of=%+v by=%+v", got.RerunOf, got.RerunBy)
	}
	if got.Account != "og-mini" || got.AccountKind != "opencode-go" || got.AccountProxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("账号快照往返失败: %+v", got)
	}
	orig, err := s.GetTask("t-orig")
	if err != nil {
		t.Fatal(err)
	}
	if orig.RerunOf != nil || orig.RerunBy != nil {
		t.Fatalf("无链接任务读回应为 nil: of=%+v by=%+v", orig.RerunOf, orig.RerunBy)
	}
	by := proto.TaskRef{Task: "t-new", Machine: "linux-01"}
	if err := s.SetTaskRerunBy("t-orig", by); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range all {
		if tk.ID == "t-orig" && (tk.RerunBy == nil || *tk.RerunBy != by) {
			t.Fatalf("rerun_by 回写后 ListTasks 读回 %+v", tk.RerunBy)
		}
	}
	if err := s.SetTaskRerunBy("nope", by); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的任务应 ErrNotFound，实际 %v", err)
	}
}
