// readstage_test.go 锁 B409.6 (S4-U6) 的账本查询阶段诊断行为：U1–U5 读取查询
// 的完成/失败日志携带 operation_id、池等待与 SQL/行读取分离的阶段字段与安全
// error_class；pool_wait 用 DB.Conn(ctx) 单次测量，不得与 sql_call 揉成一个数。
// PG 场景（池饱和/锁等待）由 LEDGER_TEST_PG_DSN 门控。
package ledger

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/diag"
	"github.com/Xsxdot/handoff/internal/proto"
)

// stageLogCapture 捕获 slog.Default() 的全部记录（collab/ledger 经默认 logger 落日志）。
type stageLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *stageLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *stageLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r.Clone())
	c.mu.Unlock()
	return nil
}
func (c *stageLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *stageLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *stageLogCapture) find(msg string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func (c *stageLogCapture) allText() string {
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

func recordAttrs(r slog.Record) map[string]string {
	out := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

// withCapture 把 slog.Default() 换成捕获 handler，测试结束还原。
func withCapture(t *testing.T) *stageLogCapture {
	t.Helper()
	capture := &stageLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

// seedSessionRoomMessages 建会话房间并落 count 条 user 消息（含对 member 的 @）。
func seedSessionRoomMessages(t *testing.T, s *Store, member string, count int) string {
	t.Helper()
	session, err := s.CreateSession("阶段诊断", member, member)
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	for i := 0; i < count; i++ {
		if _, err := s.RecordRoomMessage("", proto.RoomMessage{
			Kind: proto.RoomMsgUser, Room: session.ID, Body: "正文", Mentions: []string{member},
		}, member); err != nil {
			t.Fatalf("落消息 %d: %v", i, err)
		}
	}
	return session.ID
}

func assertStageFields(t *testing.T, r slog.Record, wantOperationID string) {
	t.Helper()
	attrs := recordAttrs(r)
	if attrs["operation_id"] != wantOperationID {
		t.Fatalf("operation_id=%q want %q（全部日志：%s）", attrs["operation_id"], wantOperationID, "")
	}
	for _, key := range []string{"pool_wait_ns", "sql_call_ns", "row_read_ns", "db_read_ns", "rows_read", "result_bytes", "elapsed_ns"} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("完成日志缺阶段字段 %s: %v", key, attrs)
		}
	}
}

// TestRoomMessageSnapshotsLogsCorrelatedStages 锁 U4 摘要查询的阶段字段与关联 id。
func TestRoomMessageSnapshotsLogsCorrelatedStages(t *testing.T) {
	s := newTestStore(t)
	capture := withCapture(t)
	roomID := seedSessionRoomMessages(t, s, "user:sycm", 2)

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-stage-1"})
	snapshots, err := s.RoomMessageSnapshotsContext(ctx, map[string]int64{roomID: 0})
	if err != nil {
		t.Fatalf("RoomMessageSnapshotsContext: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].MessagesAfter != 2 {
		t.Fatalf("摘要读数不符: %+v", snapshots)
	}
	rec, ok := capture.find("房间消息摘要读取完成")
	if !ok {
		t.Fatalf("缺完成日志：%s", capture.allText())
	}
	assertStageFields(t, rec, "op-stage-1")
}

// TestSessionCandidatesLogsCorrelatedStages 锁 U5 候选读的阶段字段与关联 id。
func TestSessionCandidatesLogsCorrelatedStages(t *testing.T) {
	s := newTestStore(t)
	capture := withCapture(t)
	seedSessionRoomMessages(t, s, "user:sycm", 2)

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-stage-2"})
	out, err := s.SessionMessageCandidatesContext(ctx, "user:sycm", 0, 0, 500)
	if err != nil {
		t.Fatalf("SessionMessageCandidatesContext: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("应命中 2 条候选: %d", len(out))
	}
	rec, ok := capture.find("会话候选读完成")
	if !ok {
		t.Fatalf("缺完成日志：%s", capture.allText())
	}
	assertStageFields(t, rec, "op-stage-2")
}

// TestOpenTicketCountsContextLogsCorrelatedStages 锁 U1 计数查询的 ctx 入口：
// 结果与既有 OpenTicketCounts 一致，且完成日志带阶段字段与关联 id。
func TestOpenTicketCountsContextLogsCorrelatedStages(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "卡")
	if err := s.LinkTask(c.ID, "mac-02", "T1", "implement", "t"); err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	put := func(typ, payload string) {
		seq++
		if _, err := s.AppendMirroredEvent(c.ID, MirroredEvent{Target: "mac-02", Task: "T1",
			SourceSeq: seq, Type: typ, Payload: []byte(payload), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	put(evTicketCreated, `{"ticket_id":"q1"}`)
	put(evTicketCreated, `{"ticket_id":"q2"}`)
	capture := withCapture(t)
	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-stage-3"})
	counts, err := s.OpenTicketCountsContext(ctx)
	if err != nil || counts[c.ID] != 2 {
		t.Fatalf("两单未决: %v %+v", err, counts)
	}
	// Context 变体与既有入口同结果（同一把尺）。
	legacy, err := s.OpenTicketCounts()
	if err != nil || legacy[c.ID] != 2 {
		t.Fatalf("旧入口同读: %v %+v", err, legacy)
	}
	rec, ok := capture.find("聚合未决工单投影完成")
	if !ok {
		t.Fatalf("缺完成日志：%s", capture.allText())
	}
	assertStageFields(t, rec, "op-stage-3")
}

// TestQueryFailureLogsStageAndClass 锁失败分支：SQL 失败日志必须带失败阶段与
// 安全 error_class，且不得出现成功完成日志；取消类错误归类为 canceled。
func TestQueryFailureLogsStageAndClass(t *testing.T) {
	s := newTestStore(t)
	capture := withCapture(t)
	roomID := seedSessionRoomMessages(t, s, "user:sycm", 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := diag.With(context.Background(), diag.Info{OperationID: "op-fail-1"})
	if _, err := s.RoomMessageSnapshotsContext(ctx, map[string]int64{roomID: 0}); err == nil {
		t.Fatal("库已关闭仍读成功")
	}
	rec, ok := capture.find("房间消息摘要读取失败")
	if !ok {
		t.Fatalf("缺读取失败日志：%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["operation_id"] != "op-fail-1" {
		t.Fatalf("失败日志缺关联: %v", attrs)
	}
	if attrs["failed_stage"] != "pool" {
		t.Fatalf("failed_stage=%q want pool（库关闭应停在取连接）", attrs["failed_stage"])
	}
	if class := attrs["error_class"]; class != "pool_error" {
		t.Fatalf("error_class=%q want pool_error", class)
	}
	if _, ok := attrs["elapsed_ns"]; !ok {
		t.Fatalf("失败日志缺已耗时: %v", attrs)
	}
	if _, ok := capture.find("房间消息摘要读取完成"); ok {
		t.Fatal("失败后不得记成功完成日志")
	}
}

// TestQueryCancelClassifiesCanceled 锁取消分支：ctx 取消后日志按 canceled 分类，
// failed_stage 指出取消发生在进入 SQL 之前（预检快速失败）。
func TestQueryCancelClassifiesCanceled(t *testing.T) {
	s := newTestStore(t)
	capture := withCapture(t)
	seedSessionRoomMessages(t, s, "user:sycm", 1)
	ctx, cancel := context.WithCancel(diag.With(context.Background(), diag.Info{OperationID: "op-cancel-1"}))
	cancel()
	if _, err := s.SessionMessageCandidatesContext(ctx, "user:sycm", 0, 0, 500); !errors.Is(err, context.Canceled) {
		t.Fatalf("应向上传播取消: %v", err)
	}
	rec, ok := capture.find("会话候选读已取消")
	if !ok {
		t.Fatalf("缺取消日志：%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["operation_id"] != "op-cancel-1" || attrs["error_class"] != "canceled" {
		t.Fatalf("取消日志分类错误: %v", attrs)
	}
	if attrs["failed_stage"] != "start" {
		t.Fatalf("取消阶段=%q want start（预检取消，不冒充 SQL 已取消）", attrs["failed_stage"])
	}
}

// TestPGPoolWaitSeparateFromSQLLockWait 锁最重的承重断言（PG）：表锁等待期间
// sql_call 反映锁等待而 pool_wait 保持小值，两者不得合并；取消后阶段数据保留。
// 反例变异（把 pool_wait 并进 sql_call 或反之）必须让本测试变红。
func TestPGPoolWaitSeparateFromSQLLockWait(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 阶段分离测试")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("打开隔离 PG: %v", err)
	}
	defer s.Close()
	// 测试守卫：只允许专用可丢弃库。
	var database string
	if err := s.db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if database != "handoff_b409_test" {
		t.Fatalf("拒绝对非隔离库执行锁测试: %q", database)
	}
	roomID := seedSessionRoomMessages(t, s, "user:sycm", 1)

	capture := withCapture(t)
	t.Run("sql_lock_wait_goes_to_sql_call", func(t *testing.T) { lockWaitScenario(t, s, capture, roomID) })
	t.Run("pool_saturation_goes_to_pool_wait", func(t *testing.T) { poolSaturationScenario(t, dsn, capture) })
}

// lockWaitScenario 锁表制造 SQL 层等待：sql_call 反映锁等待，pool_wait 保持小值。
func lockWaitScenario(t *testing.T, s *Store, capture *stageLogCapture, roomID string) {
	// 单独连接持表锁，制造 SQL 层锁等待（池仍有空闲连接，pool_wait 应很小）。
	lockConn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	lockTx, err := lockConn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.ExecContext(context.Background(), `LOCK TABLE card_events IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(diag.With(context.Background(), diag.Info{OperationID: "op-lock-1"}), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.RoomMessagesBeforeContext(ctx, roomID, 0, 10)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("锁等待中不应成功返回")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("锁等待未随 ctx 超时收敛")
	}
	rec, ok := capture.find("房间历史账本 SQL 调用失败")
	if !ok {
		t.Fatalf("缺 SQL 失败（锁等待取消）日志：%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["operation_id"] != "op-lock-1" {
		t.Fatalf("缺关联 id: %v", attrs)
	}
	if attrs["error_class"] != "deadline_exceeded" {
		t.Fatalf("error_class=%q want deadline_exceeded", attrs["error_class"])
	}
	sqlNs := parseNs(t, attrs["sql_call_ns"])
	poolNs := parseNs(t, attrs["pool_wait_ns"])
	if sqlNs < 200*time.Millisecond.Nanoseconds() {
		t.Fatalf("sql_call_ns=%d 未反映锁等待（应 ≥200ms）", sqlNs)
	}
	if poolNs > 100*time.Millisecond.Nanoseconds() {
		t.Fatalf("pool_wait_ns=%d 应保持小值（连接空闲可取）", poolNs)
	}
}

// poolSaturationScenario 锁池制造连接获取等待：pool_wait 反映池等待而
// sql_call 保持 0。两条子场景合起来，把 pool_wait 并进 sql_call（或反过来）
// 的变异必然让其一变红。
func poolSaturationScenario(t *testing.T, dsn string, capture *stageLogCapture) {
	s2, err := Open(dsn)
	if err != nil {
		t.Fatalf("打开隔离 PG（饱和场景）: %v", err)
	}
	defer s2.Close()
	s2.db.SetMaxOpenConns(1)
	roomID := seedSessionRoomMessages(t, s2, "user:sycm", 1)
	// 占住池中唯一连接：读请求的 DB.Conn 只能等待到 ctx 到期。
	busy, err := s2.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	ctx, cancel := context.WithTimeout(diag.With(context.Background(), diag.Info{OperationID: "op-pool-1"}), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := s2.RoomMessagesBeforeContext(ctx, roomID, 0, 10); err == nil {
		t.Fatal("池饱和 + 到期不应成功返回")
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("读请求过早返回（%s），未经历池等待", elapsed)
	}
	rec, ok := capture.find("房间历史账本获取连接失败")
	if !ok {
		t.Fatalf("缺连接获取失败日志：%s", capture.allText())
	}
	attrs := recordAttrs(rec)
	if attrs["operation_id"] != "op-pool-1" || attrs["error_class"] != "deadline_exceeded" ||
		attrs["failed_stage"] != "pool" {
		t.Fatalf("池饱和日志分类错误: %v", attrs)
	}
	poolNs := parseNs(t, attrs["pool_wait_ns"])
	sqlNs := parseNs(t, attrs["sql_call_ns"])
	if poolNs < 200*time.Millisecond.Nanoseconds() {
		t.Fatalf("pool_wait_ns=%d 未反映池等待（应 ≥200ms）", poolNs)
	}
	if sqlNs != 0 {
		t.Fatalf("sql_call_ns=%d 应为 0（未发出 SQL）", sqlNs)
	}
}

func parseNs(t *testing.T, raw string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("解析纳秒值 %q: %v", raw, err)
	}
	return v
}
