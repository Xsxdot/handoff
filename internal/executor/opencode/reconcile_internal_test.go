package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
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
	for {
		select {
		case ev := <-r.evCh:
			if isTimingEvent(ev) {
				continue
			}
			return ev, true
		default:
			return executor.AdapterEvent{}, false
		}
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

// TestReconcileSkipsFrozenToolTail —— B38 Task9：会话尾部冻结在「已完结的纯工具
// 消息」上时（executor 死亡/崩溃/OOM，会话从此不再变化），对账不得判「回合已结束」。
//
// 为什么这条要存在：opencode 一个用户回合会产出多条 assistant 消息，工具调用各自
// 成条、各自带 completed。若只凭「最后一条 assistant 的 CompletedMS != 0」判回合
// 结束，就会把一条纯工具消息误判成回合终态，补出假的 question/result，把一个正常
// 任务主动推进到 waiting_answer——比 B38 原始症状（冻死）更糟。
//
// 夹具：testdata/session_tooltail.json 截取自真机会话（a059b32a）的前 7 条消息，
// 最后一条 assistant 是 finish=tool-calls 的纯工具消息（completed 非零、textlen=0、
// tools=1）——这是「回合仍在进行、executor 却已死」的冻结尾部形态。
//
// 权威信号（真机 SSE 实测）：message.updated 的 info.finish 字段区分「还要继续」
// （"tool-calls"）与「回合到此为止」（"stop"）；session.status idle 是会话级回合
// 结束信号。判据必须用它，不能只看 completed。
func TestReconcileSkipsFrozenToolTail(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_tooltail.json")
	if err != nil {
		t.Fatalf("读夹具失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true) // armed + 空水位：若判「回合已结束」就会补发

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("尾部是纯工具消息（回合未完），不应补发，got Emitted=%d note=%s",
			out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("尾部是纯工具消息（回合未完）却补发了事件")
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
// why 这条最重要：若对账一律合成 result，协调者会以为任务完成，实际模型正在等
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

// assistantToolErrorMsg 造一条 finish=tool-calls、尾部 tool part state.status=error
// 的 assistant 消息（对齐 session_rejectedend.json 的形态，error 文案可指定）。
func assistantToolErrorMsg(id, toolErr string, completed int64) map[string]any {
	return map[string]any{
		"info": map[string]any{
			"id": id, "role": "assistant", "finish": "tool-calls",
			"time": map[string]any{"completed": completed},
		},
		"parts": []map[string]any{
			{"type": "step-start"},
			{"type": "tool", "tool": "bash", "callID": "call_x",
				"state": map[string]any{"status": "error", "error": toolErr}},
			{"type": "step-finish"},
		},
	}
}

// TestReconcileEmitsRejectedToolEnd —— B383 Wave 0 对账哨兵归因：尾部是「finish=
// tool-calls + tool state.status=error」且 error 文案**精确命中**拒绝哨兵 → 补发
// 与实时路径同形的失败 result（VoidReason=权限被拒），而不是 question——旧实现
// 一律发 question，会让断线前的拒绝在对账时多出一张 ask 工单。
//
// 夹具：testdata/session_rejectedend.json 取自真机会话（msg_febd7418…，权限被拒
// 的回合终态），finish=tool-calls、completed 非零、tool part state.status=error。
// 夹具里 tool part 的 error 必须逐字节等于哨兵常量——单点定义的双保险：有人改了
// 常量而没重新抓包核对，本测试立刻翻红。
func TestReconcileEmitsRejectedToolEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_rejectedend.json")
	if err != nil {
		t.Fatalf("读夹具失败: %v", err)
	}
	// 夹具 ↔ 哨兵常量绑定校验（why 见函数头）
	var msgs []struct {
		Parts []struct {
			Type  string `json:"type"`
			State struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"state"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("解析夹具: %v", err)
	}
	fixtureErr := ""
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Type == "tool" && p.State.Status == "error" {
				fixtureErr = p.State.Error
			}
		}
	}
	if fixtureErr != rejectedToolSentinel {
		t.Fatalf("夹具 error=%q 与哨兵常量 %q 不再逐字节一致：改常量前必须重新抓包核对", fixtureErr, rejectedToolSentinel)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true) // armed + 空水位

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("被拒而终的回合必须补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "result" {
		t.Fatalf("哨兵命中应补发失败结果（与实时路径同形），got %q（内容 %q）", ev.Type, ev.Text)
	}
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("应是失败结果: %+v", ev.Result)
	}
	if ev.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Fatalf("VoidReason=%q，期望 %q", ev.Result.VoidReason, executor.VoidReasonPermissionDenied)
	}
	if !strings.Contains(ev.Result.FailReason, "断线期间") {
		t.Fatalf("FailReason 应点明断线期间被拒终结: %q", ev.Result.FailReason)
	}
	if !strings.Contains(out.Note, "权限被拒") {
		t.Fatalf("应命中哨兵归因的专属结论，got note=%q", out.Note)
	}
}

// TestReconcileFreshCaptureSentinelStillAttributed —— B383 Wave 0 新鲜抓包回归：
// opencode 1.18.32（本机 2026-09-28 实测，真实 permission.asked → POST reject →
// 抓 SSE + 会话消息）确认拒绝哨兵文案未漂移，对账归因对该样本同样命中。
// 夹具 session_rejectedend_opencode_1_18_32.json 就是该次抓包的最后一条 assistant
// 消息原文（finish=tool-calls、tool part state.status=error、error=哨兵）。
// 版本演进后再漂移时：本测试翻红提示重新抓包，而不是让归因静默失效。
func TestReconcileFreshCaptureSentinelStillAttributed(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_rejectedend_opencode_1_18_32.json")
	if err != nil {
		t.Fatalf("读新鲜抓包夹具失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("新鲜样本应补发 1 条，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
		t.Fatalf("新鲜样本应归因为被拒失败结果，got %+v", ev)
	}
	if ev.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Fatalf("VoidReason=%q，期望 %q", ev.Result.VoidReason, executor.VoidReasonPermissionDenied)
	}
}

// TestReconcilePlainToolErrorStillAsks —— 普通工具报错（哨兵不命中）必须维持
// question 现状（fail-closed）：row4 的 ToolStatus=error 双义，归因拿不准时宁可
// 回到人工裁决，不能把普通报错误判成权限被拒。
func TestReconcilePlainToolErrorStillAsks(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantToolErrorMsg("msg_toolerr", "connect ECONNREFUSED 127.0.0.1:5432", 1786348485642),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("普通工具报错的回合必须补发，got Emitted=%d", out.Emitted)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "question" {
		t.Fatalf("普通工具报错必须维持 question（fail-closed），got %q", ev.Type)
	}
}

// TestReconcileMutatedSentinelStillAsks —— 哨兵文案漂移（opencode 版本升级改措辞）
// 必须退化到安全侧兜底 ask：精确匹配不命中 ⇒ 维持 question，绝不按被拒归因。
// 漂移的代价是多一次人工裁决，冒认被拒的代价是把普通故障记成人工拒绝——后者不可接受。
func TestReconcileMutatedSentinelStillAsks(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantToolErrorMsg("msg_drift", rejectedToolSentinel+" (denied by policy v2)", 1786348485642),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("漂移形态仍须补发（兜底 ask），got Emitted=%d", out.Emitted)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "question" {
		t.Fatalf("哨兵失配必须退化到 question（安全侧），got %q", ev.Type)
	}
}

// TestReconcileEmitsFinishUnknownTerminal —— B38 Task9 row6（窄兜底）：finish=
// "unknown" 且无 tool part 的真实回合终态 → 必须补发。
//
// 夹具：testdata/session_finishunknown.json 取自真机会话（msg_feb5dcf45…），
// finish=unknown、completed 非零、parts 为 step-start/reasoning/step-finish（无
// tool）。本会话 finish=unknown 共 2 条，均为真实回合终态——row6 判「已结束」正确。
func TestReconcileEmitsFinishUnknownTerminal(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_finishunknown.json")
	if err != nil {
		t.Fatalf("读夹具失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true) // armed + 空水位

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("finish=unknown 的真实终态必须补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); !ok {
		t.Fatal("没有补发事件")
	}
}

// TestReconcileEmitsStopTerminal —— B38 Task9 row2：finish="stop"（自然说完话
// 结束，无 tool part）的真实回合终态 → 必须补发。这是最主流的终态形态。
func TestReconcileEmitsStopTerminal(t *testing.T) {
	srv := fakeSession(t, []map[string]any{{
		"info": map[string]any{
			"id": "msg_stop", "role": "assistant",
			"finish": "stop",
			"time":   map[string]any{"completed": 1786348485642},
		},
		"parts": []map[string]any{
			{"type": "step-start"},
			{"type": "text", "text": "活干完了\n" + `{"branch":"handoff/x","commit":"abc1234","summary":"改完了"}`},
			{"type": "step-finish"},
		},
	}})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("finish=stop 的终态必须补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("finish=stop + finish trailer 应补发成功结果，got %+v", ev)
	}
}

// TestReconcileAbortedTurnEmitsQuestionNotResult —— B38 Task9 row3：会话被人工
// abort（error.name=MessageAbortedError）→ 补发成 **question**（带消息正文），
// 而不是 result{OK:false} 任务失败。
//
// why：abort 在真实使用里几乎总是人工救援动作（解开冻结/卡死会话），释放出来的
// 往往是完整且有价值的内容。判成 failed 会把一次成功的救援翻译成任务失败，且
// FailReason 只带 error 文本、丢掉消息正文——比 B38 原始症状还糟。全库 8236 条
// assistant 消息里 MessageAbortedError 是**唯一**出现过的 error 形态（4 条），
// 即 ErrorText 那个分支实际上只会被 abort 触发；abort 摘出来走 question、保留
// ErrorText 兜未知错误，两条都站得住。
//
// 夹具：testdata/session_aborted.json 取自真机会话（msg_fec00880…，即卡 2h 后
// abort 解开的设计消息），error.name=MessageAbortedError、completed 非零、含完整
// text part（设计正文 1658 字）。此消息 tool part 也是 status=error，但判据 row3
// 先于 row4、classifyReconciled 的 abort 分支先于 tool-error 分支，故走 question。
func TestReconcileAbortedTurnEmitsQuestionNotResult(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_aborted.json")
	if err != nil {
		t.Fatalf("读夹具失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true)

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("abort 而终必须补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "question" {
		t.Fatalf("abort 而终必须补发成 question（救援不是失败），got %q", ev.Type)
	}
	if ev.Text == "" {
		t.Fatal("abort 的提问文本不应为空——它应该带消息正文（那份被救下的内容）")
	}
}

// TestReconcileSkipsUnfinalizedFrozen —— B38 Task9 row1/row5：消息未 finalize
// （time.completed 缺失）→ 必须不补发，且必须由**判据第 1 条**（CompletedMS==0）
// 拦下，而不是靠后续任何一行。
//
// 夹具：testdata/session_unfinalized.json 取自真机会话（msg_fe0f322b…），三查
// 齐备：time.completed 确实缺失（info.time 只有 created）、error 字段为空、
// tool part state.status=running——「在飞、未 finalize」形态。按判据顺序第 1 条
// 就命中，reason=unfinalized。
//
// 自检：临时把 reconcileTurnEnded 的第 1 条注释掉，本测试必须变红——不变红说明
// 夹具没打到 row1/row5（例如误取成 error 非空被 row4 抢先、或 completed 有值被
// 后几行判走）。row3/row4 同理各值得做一遍，动作相反的两行夹具打错立刻暴露。
func TestReconcileSkipsUnfinalizedFrozen(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_unfinalized.json")
	if err != nil {
		t.Fatalf("读夹具失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "", true) // armed + 空水位

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("未 finalize 的消息不应补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("未 finalize 却补发了事件")
	}
}
