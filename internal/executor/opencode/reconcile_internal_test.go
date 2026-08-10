package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

// fakeSession 起一个只回 /session/{id}/message 的假 serve，按传入的消息列表回应。
func fakeSession(t *testing.T, msgs []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msgs)
	}))
}

// assistantMsg 造一条 assistant 消息；completed=0 表示回合仍在进行。
func assistantMsg(id string, completed int64, text string) map[string]any {
	return map[string]any{
		"info": map[string]any{
			"id": id, "role": "assistant",
			"time": map[string]any{"completed": completed},
		},
		"parts": []map[string]any{{"type": "text", "text": text}},
	}
}

// newTestAdapter 建一个挂在假 serve 上的空运行集 Adapter（测试助手）。
func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(slog.Default())
}

// testHandle 造一个 LockPath 指向临时目录、PID 非零的 prochost.Handle（测试助手）。
func testHandle(dir string) prochost.Handle {
	return prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "proc.lock")}
}

// newTestRun 建一个挂在假 serve 上的运行态，水位设为 watermark、armed 设为 armed。
func newTestRun(t *testing.T, a *Adapter, srvURL, watermark string, armed bool) *runState {
	t.Helper()
	taskDir := t.TempDir()
	r := a.newRun("task-1", taskDir, taskDir)
	r.session = "ses_test"
	r.api = NewAPI(srvURL, "pw")
	if err := writeProcInfo(taskDir, &procInfo{
		Handle:         testHandle(taskDir),
		Port:           1,
		Password:       "pw",
		LastTurnMsgID:  watermark,
		WatermarkArmed: armed,
	}); err != nil {
		t.Fatalf("写凭据失败: %v", err)
	}
	return r
}

// drainOne 读一条事件；没有事件时返回 ok=false。
func drainOne(r *runState) (executor.AdapterEvent, bool) {
	select {
	case ev := <-r.evCh:
		return ev, true
	default:
		return executor.AdapterEvent{}, false
	}
}

// TestReconcileArmedEmptyWatermarkEmits —— spec §8.1 断言 1，**B38 头号场景**：
// armed（本版本新建的会话）+ 空水位 + 尾部已完结 → 补发 1 条，水位前进。
//
// why 这条是 B38 的核心：任务的第一个回合死在断连窗口里，水位天然为空。armed
// 语义保证「空水位 = 尚无任何回合结束」，故必须补发——这正是旧判定（空水位一律
// 认基线）吞掉的那个现场。
func TestReconcileArmedEmptyWatermarkEmits(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_new", 1786348485642,
			"活干完了\n"+`{"branch":"handoff/x","commit":"abc1234","summary":"改完了"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true) // 空水位 + armed

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if !out.TurnEnded || out.Emitted != 1 {
		t.Fatalf("armed+空水位+已完结 应补发 1 条，got TurnEnded=%v Emitted=%d note=%s",
			out.TurnEnded, out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("事件通道里没有补发的事件")
	}
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("应补发成功结果，got %+v", ev)
	}
	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		t.Fatalf("读凭据失败: %v", err)
	}
	if pi.LastTurnMsgID != "msg_new" {
		t.Fatalf("水位应前进到 msg_new，got %q", pi.LastTurnMsgID)
	}
}

// TestReconcileUnarmedBaselineDoesNotEmit —— spec §8.1 断言 2（升级保护）：
// 未 armed（legacy 会话）+ 空水位 + 尾部已完结 → 补 0 条，只认基线。
//
// why：legacy 任务的空水位分辨不了「从没消费过」和「上一回合终态已正常送达」。
// 若补发会把已审过的回合终态重放一遍（多回合任务的尾部是回合 1 那条 completed）。
func TestReconcileUnarmedBaselineDoesNotEmit(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_legacy", 1786348485642, "活干完了"),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", false) // 空水位 + 未 armed

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("未 armed 认基线，不应补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("未 armed 认基线却补发了事件")
	}
	pi, _ := readProcInfo(r.taskDir)
	if pi.LastTurnMsgID != "msg_legacy" {
		t.Fatalf("认基线应把水位记到尾部 msg_legacy，got %q", pi.LastTurnMsgID)
	}
}

// TestReconcileIsIdempotent —— spec §8.1 断言 3（幂等）：armed + 水位 == 尾部
// msg.ID（终态已送达过）→ 补 0 条。
func TestReconcileIsIdempotent(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_same", 1786348485642, "活干完了"),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_same", true) // 水位 == 尾部消息

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("水位已过，不应补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("水位已过却补发了事件")
	}
}

// TestReconcileSkipsWhenTurnStillRunning —— spec §8.1 断言 4：
// 会话仍在忙 → 补 0 条，不改水位。
func TestReconcileSkipsWhenTurnStillRunning(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_running", 0, "正在干"), // completed=0
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.TurnEnded || out.Emitted != 0 {
		t.Fatalf("回合仍在跑，不应补发，got %+v", out)
	}
	pi, _ := readProcInfo(r.taskDir)
	if pi.LastTurnMsgID != "msg_old" {
		t.Fatalf("回合未完结时水位不应动，got %q", pi.LastTurnMsgID)
	}
}

// TestReconcileQueryFailureDoesNotEmit —— spec §8.1 断言 5：
// 查询失败 → 补 0 条并返回 error（调用方据此只记 WARN，不改状态）。
func TestReconcileQueryFailureDoesNotEmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err == nil {
		t.Fatal("查询失败应返回 error")
	}
	if out.Emitted != 0 {
		t.Fatalf("查询失败不应补发，got Emitted=%d", out.Emitted)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("查询失败却补发了事件")
	}
}

// TestReconcileRestoresQuestionNotResult —— spec §8.1 断言 6，**本设计的核心断言**：
// 以提问收尾的回合必须还原成 question 工单，而不是一条假的「做完了」。
//
// why 这条最重要：若对账一律合成 result，审核者会以为任务完成，实际模型正在等
// 他回答——任务换个姿势继续冻死，而且这次连 stalled 都不会再报（状态已离开 running）。
func TestReconcileRestoresQuestionNotResult(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_ask", 1786348485642,
			"我需要确认一件事\n"+`{"question":"用 A 方案还是 B 方案？"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("应补发 1 条，got %d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "question" {
		t.Fatalf("以提问收尾的回合必须还原成 question，got %q（内容 %q）", ev.Type, ev.Text)
	}
	if ev.Text == "" {
		t.Fatal("提问文本不应为空")
	}
	_ = fmt.Sprint() // 保留 fmt 引用
}
