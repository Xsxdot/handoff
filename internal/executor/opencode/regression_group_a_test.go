// regression_group_a_test.go —— 第二轮审查 A 组（adapter 重写）缺陷的回归测试。
//
// 职责：为审查报告第四节 A-1..A-12 各留一条会因缺陷复现而变红的用例。
//
// 边界：只覆盖 adapter/api 层的映射与生命周期语义，不触真实 opencode 与 tmux。
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
)

// bareLimit 是本文件里构造超长文本的长度（远超权限描述 200 字上限）。
const bareLimit = 300

// permissionAskedRawEvent 构造一条只有 id 的 permission.asked——真实探针里
// 三种形态都会让描述拼成空串（缺 permission、缺 metadata.command、缺 patterns）。
func permissionAskedRawEvent(id string) string {
	return sseLine(map[string]any{
		"type":      "permission.asked",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"id": id, "sessionID": "sess-1",
		},
	})
}

// TestPermissionTextNeverBlank 验证 A-2 的下限：权限描述拼不出内容时，
// 交给审核者的文本仍能标识这是哪一次审批，而不是一个空白行。
func TestPermissionTextNeverBlank(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-perm-blank", t.TempDir(), t.TempDir())

	fs.push(permissionAskedRawEvent("per_blank"))

	ev := waitEventType(t, ch, "permission")
	if strings.TrimSpace(ev.Text) == "" {
		t.Fatal("权限描述为空：审核者被要求批准一个空白行")
	}
	if !strings.Contains(ev.Text, "per_blank") {
		t.Errorf("无描述时应至少给出权限 id 供定位, got %q", ev.Text)
	}
}

// TestPermissionTextMarksTruncation 验证 A-2 的上限：超长命令被截断时必须
// 带可见标记——否则审核者会以为自己看到的就是完整命令。
func TestPermissionTextMarksTruncation(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-perm-long", t.TempDir(), t.TempDir())
	long := strings.Repeat("x", bareLimit)

	fs.push(permissionAskedEvent("per_long", "bash", long))

	ev := waitEventType(t, ch, "permission")
	if !strings.Contains(ev.Text, "已截断") {
		t.Errorf("超长权限描述被静默截断，审核者无从得知: %q", ev.Text)
	}
}

// TestReasoningTypeSurvivesTurnBoundary 验证 A-4：part 的类型是「这个 part
// 是什么」的事实，不随回合结束失效。第一回合登记的 reasoning part 若在回合
// 边界后被遗忘，它后续的增量会被当模型输出，思维链直接变成面向审核者的提问。
func TestReasoningTypeSurvivesTurnBoundary(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-parttype", t.TempDir(), t.TempDir())

	// 第一回合：登记 reasoning part，另有一个真实文本 part 让回合非空
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedTypedEvent("msg-a1", "prt-reason", "reasoning", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-reason", "推理A"))
	fs.push(partUpdatedTypedEvent("msg-a1", "prt-text", "text", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-text", "回合一文本"))
	fs.push(statusIdleEvent())
	if got := waitEventType(t, ch, "question").Text; got != "回合一文本" {
		t.Fatalf("第一回合文本 = %q, want %q", got, "回合一文本")
	}

	// 第二回合：同一个 reasoning part 继续产出增量
	fs.push(partDeltaEvent("msg-a1", "prt-reason", "推理B-泄漏"))
	fs.push(partUpdatedTypedEvent("msg-a2", "prt-text2", "text", ""))
	fs.push(partDeltaEvent("msg-a2", "prt-text2", "回合二文本"))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ch, "question")
	if strings.Contains(ev.Text, "推理B-泄漏") {
		t.Errorf("回合边界后 reasoning 泄漏进回合文本: %q", ev.Text)
	}
	if ev.Text != "回合二文本" {
		t.Errorf("第二回合文本 = %q, want %q", ev.Text, "回合二文本")
	}
}

// TestServeLogTailRedactsPassword 验证 A-12：serve.log 尾部是 opencode 完全
// 可控的输出，直接进 FailReason 与 agentd.log 前必须抹掉 serve 密码。
func TestServeLogTailRedactsPassword(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, serveLogFileName)
	const pwd = "s3cr3t-serve-password"
	if err := os.WriteFile(logPath, []byte("listening on http://opencode:"+pwd+"@127.0.0.1:4345\n"), 0o644); err != nil {
		t.Fatalf("写 serve.log: %v", err)
	}
	h := procHandle{p: &Proc{Password: pwd, ServeLogPath: logPath}}

	tail := h.LogTail()

	if strings.Contains(tail, pwd) {
		t.Fatalf("serve.log 尾部泄漏 serve 密码: %q", tail)
	}
	if !strings.Contains(tail, "127.0.0.1:4345") {
		t.Errorf("脱敏不应吃掉诊断信息, got %q", tail)
	}
}

// TestDeltaBeforePartTypeKnownIsNotText 验证 A-5：part 类型未知时不得默认按
// 文本累积。「part.updated 总是先于 delta 到达」只是 spike5 的观测属性，
// SSE 跨重连没有顺序保证——赌错方向就是把思维链交给审核者。
func TestDeltaBeforePartTypeKnownIsNotText(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-delta-first", t.TempDir(), t.TempDir())

	// delta 先到（此时该 part 类型未知），随后 part.updated 才揭示它是 reasoning
	fs.push(partDeltaEvent("msg-a1", "prt-x", "秘密推理：我打算直接问用户"))
	fs.push(partUpdatedTypedEvent("msg-a1", "prt-x", "reasoning", ""))
	fs.push(partUpdatedTypedEvent("msg-a1", "prt-t", "text", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-t", "正式回答"))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ch, "question")
	if strings.Contains(ev.Text, "秘密推理") {
		t.Errorf("类型未知的增量被当文本累积，reasoning 泄漏: %q", ev.Text)
	}
}

// TestRevisedSnapshotReplacesInsteadOfDuplicating 验证 A-6：服务端修订同一
// part 的快照时（"Hello world" → "Hi world"），累积结果应是修订后的文本，
// 而不是两段拼接——fallback question 与 render.log 都是给人读的。
func TestRevisedSnapshotReplacesInsteadOfDuplicating(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-revise", t.TempDir(), t.TempDir())

	fs.push(partUpdatedEvent("msg-a1", "prt-1", "Hello world"))
	fs.push(partUpdatedEvent("msg-a1", "prt-1", "Hi world"))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ch, "question")
	if ev.Text != "Hi world" {
		t.Errorf("快照修订后回合文本 = %q, want %q", ev.Text, "Hi world")
	}
}

// TestPermissionWithoutSessionIsNotSilentlyTrusted 验证 A-1 的 fail-open 方向：
// 缺 sessionID 的 permission.asked 不能被当成本任务的审批工单静默放行。
//
// 多任务并发时 /event 是全服务器广播流，一条无归属的审批请求被每个任务都当成
// 自己的，审核者会看到重复且归属错误的审批门。
func TestPermissionWithoutSessionIsNotSilentlyTrusted(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-noses", t.TempDir(), t.TempDir())

	fs.push(sseLine(map[string]any{
		"type":       "permission.asked",
		"properties": map[string]any{"id": "per_noses", "permission": "bash"},
	}))
	fs.push(partUpdatedEvent("msg-a1", "prt-1", "之后的正常文本"))
	fs.push(statusIdleEvent())

	// 用后续的 question 做栅栏：question 到达即说明无会话的权限事件已被处理完
	for {
		ev := nextNonProgress(t, ch)
		if ev.Type == "permission" {
			t.Fatalf("缺 sessionID 的 permission.asked 被当本任务审批放行: %+v", ev)
		}
		if ev.Type == "question" {
			return
		}
	}
}

// TestResumeKeepsReplayedUserMessageFromWipingTurn 验证 A-3：恢复后的运行态
// 会收到服务端重播的同一条 user 消息（spike5 实测每次 session.diff 后重播），
// 若把它当「首见」清空回合，恢复后累积的全部文本会被丢弃、idle 走空回合分支。
func TestResumeKeepsReplayedUserMessageFromWipingTurn(t *testing.T) {
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, "task-resume-user", t.TempDir(), t.TempDir())
	r := ad.lookup("task-resume-user")
	if r == nil {
		t.Fatal("运行态缺失")
	}

	// 模拟恢复语义：运行态是新建的，userMsgs 为空，而服务端正在重播老的 user 消息
	fs.push(partUpdatedEvent("msg-a1", "prt-1", "恢复后累积的文本"))
	fs.push(userMsgEvent("msg-u-old"))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ad.Events("task-resume-user"), "question")
	if ev.Text != "恢复后累积的文本" {
		t.Errorf("重播的 user 消息抹掉了回合文本: got %q", ev.Text)
	}
}

// TestSubscribeEventsReportsRealCause 验证 A-7：事件流意外中断时 FailReason
// 必须带真实原因。SubscribeEvents 恒返回 nil 会让失败现场只剩 "<nil>"——
// 看门狗抖动判死而 serve 又活着时，任务被标记 failed 却零信息。
//
// 取消的时点刻意落在两次连接之间的退避等待里（观察到第 2 次连接后再等 50ms，
// 而退避是 500ms），确保被中断的不是在途连接，返回的就是上一次的真实原因。
func TestSubscribeEventsReportsRealCause(t *testing.T) {
	var conns atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	api := NewAPIWithSSEBackoff(ts.URL, "pw", 500*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- api.SubscribeEvents(ctx, func(json.RawMessage) {}, nil) }()

	deadline := time.Now().Add(5 * time.Second)
	for conns.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("超时未等到 2 次连接，当前 %d 次", conns.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // 确保取消落在退避等待中，而非在途连接上
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("最后一次连接是 500，退出时应带上该原因，实际返回 nil")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("失败原因未透出: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后 SubscribeEvents 未在 3s 内返回")
	}
}

// TestWatchdogStaysFastWhileStreaming 验证 A-11：看门狗的活跃判定必须挂在
// 「收到 SSE 事件」上而非「产出 AdapterEvent」上。progress 有 30s 节流，
// 挂在产出上会让正在流式输出的任务绝大部分时间处于慢速探活档。
func TestWatchdogStaysFastWhileStreaming(t *testing.T) {
	fs := newFakeServer(t)
	ad, ch := startFakeRun(t, fs, "task-active", t.TempDir(), t.TempDir())
	r := ad.lookup("task-active")
	if r == nil {
		t.Fatal("运行态缺失")
	}
	// 消费「会话就绪」，再推一段文本把 progress 节流窗口打开——此后 30s 内的
	// 文本增量都不会再产出任何 AdapterEvent，正是流式输出的常态
	waitEventType(t, ch, "progress")
	fs.push(partUpdatedEvent("msg-a1", "prt-1", "第一段"))
	waitEventType(t, ch, "progress")
	before := r.lastEventAt.Load()

	// 只推被节流吃掉的文本增量：不会产出任何 AdapterEvent
	fs.push(partDeltaEvent("msg-a1", "prt-1", "流式输出中"))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.lastEventAt.Load() != before {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("流式输出期间看门狗看不到任何活跃信号，任务会被误判为静默并降频")
}

// TestSSEBackoffGrowsWhenServerAcceptsThenCloses 验证 A-8：退避复位必须挂在
// 「连接活过一段时间」上，而不是「拿到 200 响应头」上。
//
// 半死的 opencode（接受连接、回 200、立刻关流）在错误实现下永不退避——
// 每秒一次重连 + 每次一行 Info 日志，永远升不到 30s 上限。
func TestSSEBackoffGrowsWhenServerAcceptsThenCloses(t *testing.T) {
	var conns atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush() // 200 已发出，随即关流
	}))
	defer ts.Close()
	api := NewAPIWithSSEBackoff(ts.URL, "pw", 50*time.Millisecond, 2*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 1200*time.Millisecond)
	defer cancel()

	_ = api.SubscribeEvents(ctx, func(json.RawMessage) {}, nil)

	// 50ms 起步指数退避：50+100+200+400+800 → 1.2s 内约 5 次连接。
	// 不退避则约 20+ 次
	if n := conns.Load(); n > 8 {
		t.Fatalf("半死 server 下 1.2s 内重连 %d 次：退避未生效（应约 5 次）", n)
	}
}

// TestRetainedRunIsRetriedAndBounded 验证 A-10：kill 失败保留的运行态必须
// 真的被重试回收，且重试有限——否则 runs 表只增不减，成为内存与 lookup 阴影。
func TestRetainedRunIsRetriedAndBounded(t *testing.T) {
	fs := newFakeServer(t)
	taskDir := t.TempDir()
	ad, _ := startFakeRun(t, fs, "task-retain", t.TempDir(), taskDir)
	ad.reapInterval = 5 * time.Millisecond
	probe := ad.lookup("task-retain").handle.(*fakeProbe)
	probe.setKillErr(errors.New("kill 失败"))

	if err := ad.Stop("task-retain"); err == nil {
		t.Fatal("kill 失败时 Stop 应返回错误")
	}
	if ad.lookup("task-retain") == nil {
		t.Fatal("kill 失败且 serve 存活时应保留运行态待重试")
	}
	// 外部原因消失后，后台重试应把它收干净
	probe.setKillErr(nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ad.lookup("task-retain") == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("kill 恢复后运行态仍未被回收：保留态永不重试")
}

// TestRetainedRunGivesUpAfterMaxAttempts 验证 A-10 的另一半：kill 始终失败时
// 重试必须有上限并放弃（Error 记录交人工），不能让条目永久驻留 runs 表。
func TestRetainedRunGivesUpAfterMaxAttempts(t *testing.T) {
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, "task-retain-forever", t.TempDir(), t.TempDir())
	ad.reapInterval = 2 * time.Millisecond
	ad.reapMaxAttempts = 3
	probe := ad.lookup("task-retain-forever").handle.(*fakeProbe)
	probe.setKillErr(errors.New("kill 永久失败"))

	_ = ad.Stop("task-retain-forever")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ad.lookup("task-retain-forever") == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("kill 恒失败的运行态永久驻留 runs 表")
}

// TestQuestionTextIsBounded 验证交给审核者的回合文本有上限：兜底分类会把
// 整个回合原文塞进 question，一个 20 万字符的回合会直接灌进工单行与终端。
// 全文始终在任务目录的 render.log 里，截断不丢证据。
func TestQuestionTextIsBounded(t *testing.T) {
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-huge", t.TempDir(), t.TempDir())

	fs.push(partUpdatedEvent("msg-a1", "prt-1", strings.Repeat("长", 200000)))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ch, "question")
	if n := len([]rune(ev.Text)); n > questionTextLimit+200 {
		t.Errorf("question 文本 %d 字符未受限（上限 %d）", n, questionTextLimit)
	}
	if !strings.Contains(ev.Text, "render.log") {
		t.Errorf("截断后应指明全文去处, got 尾部 %q", tailRunes(ev.Text, 80))
	}
}

// nextNonProgress 取下一条非 progress 事件（progress 是噪音）。
// 与 waitAnyClassified 的区别：本函数不跳过 permission，用于断言
// 「某类事件根本不该出现」。
func nextNonProgress(t *testing.T, ch <-chan executor.AdapterEvent) executor.AdapterEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("事件通道已关闭")
			}
			if ev.Type != "progress" {
				return ev
			}
		case <-deadline:
			t.Fatal("等待分类事件超时")
		}
	}
}
