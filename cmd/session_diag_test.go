// session_diag_test.go 锁 B409.6 (S4-U6) 的 CLI 诊断闭环：session wait 的总启动/
// 积压/命中/超时/信号退出有 operation_id、stdout_bytes、elapsed_ns 与结果分类；
// 账本候选读日志共享同一 operation_id；room read 有总耗时与输出字节。诊断只去
// stderr（slog 默认出口），stdout JSON 行不动。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// cliLogCapture 捕获 slog.Default()（CLI 诊断走 stderr 文本 logger 的同一出口）。
type cliLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *cliLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *cliLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *cliLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *cliLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *cliLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func (c *cliLogCapture) allText() string {
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

func cliRecordAttrs(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

func withCLICapture(t *testing.T) *cliLogCapture {
	t.Helper()
	capture := &cliLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

// TestSessionWaitDiagCompletionBacklogHit 锁积压命中收口：stdout_bytes>0、
// outcome=success_nonempty、op- 关联、elapsed_ns。
func TestSessionWaitDiagCompletionBacklogHit(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 诊断命中", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	capture := withCLICapture(t)

	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0")
	if err != nil {
		t.Fatalf("session wait: %v", err)
	}
	if !strings.Contains(out, "session_wake") && !strings.Contains(out, `"session"`) {
		t.Fatalf("stdout 应是 JSON 行: %q", out)
	}
	rec, ok := capture.find("session wait 完成")
	if !ok {
		t.Fatalf("缺 wait 收口日志：\n%s", capture.allText())
	}
	attrs := cliRecordAttrs(rec)
	if !strings.HasPrefix(attrs["operation_id"], "op-") {
		t.Fatalf("收口缺 op- 关联: %v", attrs)
	}
	if attrs["command"] != "session_wait" {
		t.Fatalf("command=%q want session_wait", attrs["command"])
	}
	if attrs["outcome"] != "success_nonempty" {
		t.Fatalf("outcome=%q want success_nonempty", attrs["outcome"])
	}
	if n := atoiDiag(t, attrs["stdout_bytes"]); n <= 0 || int64(n) != int64(len(out)) {
		t.Fatalf("stdout_bytes=%q want %d", attrs["stdout_bytes"], len(out))
	}
	if _, ok := attrs["elapsed_ns"]; !ok {
		t.Fatalf("收口缺 elapsed_ns: %v", attrs)
	}
}

// TestSessionWaitDiagTimeoutClassifiedCanceled 锁超时退出：一次性空等到点
// exit 124，收口 outcome=canceled + cancel_reason=deadline_exceeded，不谎报
// success_empty。
func TestSessionWaitDiagTimeoutClassifiedCanceled(t *testing.T) {
	dir := t.TempDir()
	_, _, _, _ = mustWaitFixture(t, dir)
	capture := withCLICapture(t)

	sessionWaitPollInterval = 20 * time.Millisecond
	defer func() { sessionWaitPollInterval = 2 * time.Second }()
	out, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0", "--timeout", "100ms")
	if out != "" {
		t.Fatalf("空等不得输出: %q", out)
	}
	var codeErr *exitCodeError
	if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
		t.Fatalf("超时应以 124 退出: %v", err)
	}
	rec, ok := capture.find("session wait 完成")
	if !ok {
		t.Fatalf("缺 wait 收口日志：\n%s", capture.allText())
	}
	attrs := cliRecordAttrs(rec)
	if attrs["outcome"] != "canceled" || attrs["cancel_reason"] != "deadline_exceeded" {
		t.Fatalf("超时收口分类错误: %v", attrs)
	}
	if n := atoiDiag(t, attrs["stdout_bytes"]); n != 0 {
		t.Fatalf("空等 stdout_bytes=%d want 0", n)
	}
}

// TestSessionWaitDiagOperationIDCorrelatesToStore 锁关联贯通：候选读的账本
// 完成日志与 wait 收口共享同一 operation_id（ctx 贯通到 DB 查询）。
func TestSessionWaitDiagOperationIDCorrelatesToStore(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "@user:sy 关联贯通", Mentions: []string{"user:sy"}}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	capture := withCLICapture(t)

	if _, _, err := runLedgerCLI(t, dir, "session", "wait", "user:sy", "--since", "0"); err != nil {
		t.Fatalf("session wait: %v", err)
	}
	completion, ok := capture.find("session wait 完成")
	if !ok {
		t.Fatalf("缺 wait 收口日志：\n%s", capture.allText())
	}
	opID, _ := attrValueCLI(completion, "operation_id")
	if !strings.HasPrefix(opID, "op-") {
		t.Fatalf("收口缺 op- 关联: %q", opID)
	}
	found := false
	for _, r := range capture.records {
		if r.Message != "会话候选读完成" {
			continue
		}
		if v, ok := attrValueCLI(r, "operation_id"); ok && v == opID {
			found = true
		}
	}
	if !found {
		t.Fatalf("候选读日志未携带同一 operation_id=%s：\n%s", opID, capture.allText())
	}
}

// TestRoomReadDiagTotalAndBytes 锁 room read 的总耗时与输出字节收口。
func TestRoomReadDiagTotalAndBytes(t *testing.T) {
	dir := t.TempDir()
	svc, _, _, sessionID := mustWaitFixture(t, dir)
	if _, err := svc.Send(sessionID, proto.RoomMessage{Kind: proto.RoomMsgUser, Body: "room read 金样"}, "user:tester"); err != nil {
		t.Fatal(err)
	}
	capture := withCLICapture(t)

	out, _, err := runLedgerCLI(t, dir, "room", "read", sessionID)
	if err != nil {
		t.Fatalf("room read: %v", err)
	}
	if !strings.Contains(out, "room read 金样") {
		t.Fatalf("stdout 应含正文: %q", out)
	}
	rec, ok := capture.find("CLI room read 完成")
	if !ok {
		t.Fatalf("缺 room read 收口日志：\n%s", capture.allText())
	}
	attrs := cliRecordAttrs(rec)
	if attrs["outcome"] != "success_nonempty" {
		t.Fatalf("outcome=%q want success_nonempty", attrs["outcome"])
	}
	if n := atoiDiag(t, attrs["stdout_bytes"]); n != len(out) {
		t.Fatalf("stdout_bytes=%q want %d", attrs["stdout_bytes"], len(out))
	}
	if _, ok := attrs["elapsed_ns"]; !ok {
		t.Fatalf("收口缺 elapsed_ns: %v", attrs)
	}
}

func attrValueCLI(r slog.Record, key string) (string, bool) {
	var found string
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found, ok = a.Value.String(), true
			return false
		}
		return true
	})
	return found, ok
}

func atoiDiag(t *testing.T, raw string) int {
	t.Helper()
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("解析数值 %q: %v", raw, err)
	}
	return n
}

// TestSessionWaitCancelDuringScanExitSemantics 锁「取消贯通到查询」的退出语义：
// ctx 取消在候选页查询中冒出时，一次性按 124 超时退出、follow 主动退 0——
// 与 select 轮询分支同语义，取消不得被记成读错误。
func TestSessionWaitCancelDuringScanExitSemantics(t *testing.T) {
	// 命中查询路径：钩子在席位版本读之后、候选查询之前取消 ctx，使取消以
	// 查询错误形态返回（select 分支来不及感知）。
	seed := func(t *testing.T) (*collab.Service, *ledger.Store) {
		t.Helper()
		svc, _, st, _ := mustWaitFixture(t, t.TempDir())
		return svc, st
	}
	t.Run("one_shot_exits_124", func(t *testing.T) {
		svc, st := seed(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sessionWaitBeforeQuery = func(from, to int64) { cancel() }
		defer func() { sessionWaitBeforeQuery = nil }()
		var out bytes.Buffer
		err := sessionWaitRun(ctx, svc, st, &out, "user:sy", 0, false, 0, false)
		var codeErr *exitCodeError
		if !errors.As(err, &codeErr) || codeErr.code != ExitTimeout {
			t.Fatalf("一次性取消应按 124 超时退出: %v", err)
		}
	})
	t.Run("follow_exits_0", func(t *testing.T) {
		svc, st := seed(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sessionWaitBeforeQuery = func(from, to int64) { cancel() }
		defer func() { sessionWaitBeforeQuery = nil }()
		var out bytes.Buffer
		if err := sessionWaitRun(ctx, svc, st, &out, "user:sy", 0, false, 0, true); err != nil {
			t.Fatalf("follow 主动取消应退 0: %v", err)
		}
	})
}
