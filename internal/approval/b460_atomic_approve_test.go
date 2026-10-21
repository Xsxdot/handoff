package approval_test

// b460_atomic_approve_test.go —— B460 Q3 甲 / V3（SC2 回归锁）：OpenCode 面审批链「批准」
// 分支建的工单从未以 answer IS NULL 的形态存在过（即从未出现在 PendingTickets 里）。
//
// 观测手段：在同一个 SQLite 文件上装一个 AFTER INSERT 触发器，凡插入 answer 为 NULL 的工单
// 行即记入探针表——这等价于「每一次写调用后立刻读 PendingTickets」且不依赖调度时序
// （Hooks.Store 是具体类型，无法包装）。改回「先建未答单、再应答」两步写，本测试必红。
import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"

	_ "modernc.org/sqlite"
)

// b460InstallUnansweredProbe 在 path 的库上装「未答工单插入」探针，返回查询函数。
func b460InstallUnansweredProbe(t *testing.T, path string) func(ticketID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS b460_probe (id TEXT)`,
		`CREATE TRIGGER IF NOT EXISTS b460_unanswered AFTER INSERT ON tickets
		 WHEN NEW.answer IS NULL BEGIN INSERT INTO b460_probe(id) VALUES (NEW.id); END`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("装探针: %v", err)
		}
	}
	return func(ticketID string) int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM b460_probe WHERE id = ?`, ticketID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func TestB460ClientApproveNeverPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	probe := b460InstallUnansweredProbe(t, path)
	const taskID = "b460-approve"
	now := time.Now().UTC()
	wd := t.TempDir()
	if err := st.CreateTask(&proto.Task{ID: taskID, RepoPath: wd, WorkDir: wd,
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c := approval.NewClient(taskID, executor.PolicySnapshot{Version: "v1"}, approval.Hooks{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st,
		JudgePermission: func(string, executor.AdapterEvent) permgate.Verdict {
			return permgate.Verdict{Action: permgate.Consult}
		},
		ShouldConsult: func(string) bool { return true },
		Decide: func(context.Context, string, string) approval.ConsultDecision {
			return approval.ConsultDecision{Approve: true, Reason: "fixture"}
		},
		TransitBestEffort:  func(string, proto.TaskState, string) {},
		NoteDeliveryFailed: func(string, string, error) {},
	})
	res, err := c.Request(context.Background(), executor.ApprovalRequest{NativeID: "n1", Text: "Bash: python3 x.py",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 x.py"}})
	if err != nil || res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("应 allow：%+v %v", res, err)
	}
	if n := probe(res.Ref.ID); n != 0 {
		t.Fatalf("批准工单 %s 曾以未答形态落库 %d 次（会短暂出现在待答列表）", res.Ref.ID, n)
	}
	tk, err := st.GetTicket(res.Ref.ID)
	if err != nil || tk.Answer == nil || *tk.Answer != "allow" {
		t.Fatalf("工单应已答 allow：%+v %v", tk, err)
	}
}
