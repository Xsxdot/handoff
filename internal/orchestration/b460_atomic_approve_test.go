package orchestration

// b460_atomic_approve_test.go —— B460 Q3 甲 / V3（SC2 回归锁）：Manager approvePermission
// （审批链批准与复用两种来源共用）建的工单从未以 answer IS NULL 形态存在过。观测手段同
// approval 包：同库 AFTER INSERT 触发器探针（m.st 是具体类型，无法包装）。
import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

func TestB460ManagerApproveNeverPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
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
	fk := fake.New(nil)
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fk}, cfg, nil, nil, newTestGate(t),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetWorkspace(workspace.NewCapability())
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: "b460-m", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	for _, src := range []string{"approver", "reuse"} {
		ticketID := "b460-m:" + src
		m.approvePermission("b460-m", ticketID, src, "Bash: python3 x.py", "", "ok", src)
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM b460_probe WHERE id = ?`, ticketID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s 来源的批准工单曾以未答形态落库（会短暂出现在待答列表）", src)
		}
		tk, err := st.GetTicket(ticketID)
		if err != nil || tk.Answer == nil || *tk.Answer != "allow" {
			t.Fatalf("%s 工单应已答 allow：%+v %v", src, tk, err)
		}
	}
}
