// projection_diag_test.go 锁 B409.6 (S4-U6) 的 collab 投影诊断行为：投影读的
// 完成/失败日志携带 operation_id、累计账本调用耗时（ledger_call_ns）、应用装配
// 耗时（projection_ns）、rows_returned 与结果分类（success_nonempty/success_empty/
// error/canceled）；真实错误不得被记成 success_empty。
package collab

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/diag"
	"github.com/Xsxdot/handoff/internal/proto"
)

// projLogCapture 捕获 collab 包 slog.Default() 日志。
type projLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *projLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *projLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *projLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *projLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *projLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func (c *projLogCapture) allText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, r := range c.records {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

func projAttrs(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

func withProjCapture(t *testing.T) *projLogCapture {
	t.Helper()
	capture := &projLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

func assertProjectionFields(t *testing.T, r slog.Record, opID string, minRows int) {
	t.Helper()
	attrs := projAttrs(r)
	if attrs["operation_id"] != opID {
		t.Fatalf("operation_id=%q want %q", attrs["operation_id"], opID)
	}
	for _, key := range []string{"ledger_call_ns", "projection_ns", "rows_returned", "outcome", "elapsed_ns"} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("投影完成日志缺 %s: %v", key, attrs)
		}
	}
	if attrs["outcome"] != "success_nonempty" {
		t.Fatalf("非空读 outcome=%q want success_nonempty", attrs["outcome"])
	}
}

// TestListRoomsProjectionLogsLedgerAndAssembly 锁房间列表投影的账本/装配分段。
func TestListRoomsProjectionLogsLedgerAndAssembly(t *testing.T) {
	svc, st := newFixture(t)
	mustCard(t, svc, st, "投影卡")
	capture := withProjCapture(t)

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-proj-1"})
	rooms, err := svc.ListRoomsContext(ctx, "handoff")
	if err != nil {
		t.Fatalf("ListRoomsContext: %v", err)
	}
	if len(rooms) == 0 {
		t.Fatal("应有房间")
	}
	rec, ok := capture.find("会话列表已组装")
	if !ok {
		t.Fatalf("缺投影完成日志：%s", capture.allText())
	}
	assertProjectionFields(t, rec, "op-proj-1", len(rooms))
}

// TestListSessionsProjectionLogsLedgerAndAssembly 锁会话列表投影分段。
func TestListSessionsProjectionLogsLedgerAndAssembly(t *testing.T) {
	svc, st := newFixture(t)
	capture := withProjCapture(t)
	if _, err := st.CreateSession("投影会话", "user:a", "user:a"); err != nil {
		t.Fatal(err)
	}

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-proj-2"})
	summaries, err := svc.ListSessionsContext(ctx, "user:a")
	if err != nil {
		t.Fatalf("ListSessionsContext: %v", err)
	}
	if len(summaries) == 0 {
		t.Fatal("应有会话")
	}
	rec, ok := capture.find("会话列表投影完成")
	if !ok {
		t.Fatalf("缺投影完成日志：%s", capture.allText())
	}
	assertProjectionFields(t, rec, "op-proj-2", len(summaries))
}

// TestPendingProjectionLogsLedgerAndAssembly 锁 U5 定向收件箱投影分段。
func TestPendingProjectionLogsLedgerAndAssembly(t *testing.T) {
	svc, st := newFixture(t)
	capture := withProjCapture(t)
	if _, err := st.CreateSession("收件箱会话", "user:a", "user:a"); err != nil {
		t.Fatal(err)
	}
	// 群级 @user:a 消息 → Pending 应命中。
	if _, err := st.RecordRoomMessage("", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Room: "session:1", Body: "hi", Mentions: []string{"user:a"},
	}, "user:b"); err != nil {
		t.Fatal(err)
	}

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-proj-3"})
	pending, err := svc.PendingContext(ctx, "user:a")
	if err != nil {
		t.Fatalf("PendingContext: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("应命中一条待消费")
	}
	rec, ok := capture.find("待消费队列已组装")
	if !ok {
		t.Fatalf("缺投影完成日志：%s", capture.allText())
	}
	assertProjectionFields(t, rec, "op-proj-3", len(pending))
}

// TestProjectionErrorNeverReportedSuccessEmpty 锁红线：真实读错归 error，
// 绝不落 success_empty；取消归 canceled。
func TestProjectionErrorNeverReportedSuccessEmpty(t *testing.T) {
	svc, st := newFixture(t)
	capture := withProjCapture(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-proj-4"})
	if _, err := svc.ListRoomsContext(ctx, "handoff"); err == nil {
		t.Fatal("库关闭应读失败")
	}
	rec, ok := capture.find("会话列表已组装")
	if !ok {
		t.Fatalf("缺失败投影日志：%s", capture.allText())
	}
	attrs := projAttrs(rec)
	if attrs["outcome"] != "error" {
		t.Fatalf("outcome=%q want error（错误不得记成 success_empty）", attrs["outcome"])
	}
	if attrs["error_class"] == "" {
		t.Fatalf("失败投影缺 error_class: %v", attrs)
	}
}

// TestProjectionCancelClassifiedCanceled 锁取消分类。
func TestProjectionCancelClassifiedCanceled(t *testing.T) {
	svc, st := newFixture(t)
	capture := withProjCapture(t)
	mustCard(t, svc, st, "取消卡")
	ctx, cancel := context.WithCancel(diag.With(context.Background(), diag.Info{OperationID: "op-proj-5"}))
	cancel()
	if _, err := svc.ListRoomsContext(ctx, "handoff"); err == nil {
		t.Fatal("取消后应失败")
	}
	rec, ok := capture.find("会话列表已组装")
	if !ok {
		t.Fatalf("缺取消投影日志：%s", capture.allText())
	}
	if got := projAttrs(rec)["outcome"]; got != "canceled" {
		t.Fatalf("outcome=%q want canceled", got)
	}
}
