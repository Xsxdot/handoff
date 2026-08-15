// agentd 看门狗与启动恢复的白盒测试（package agentd，直接驱动未导出的 runWatchdog）。
//
// 覆盖（brief Task 12 Step 1 三个用例）：
//   - TestWatchdogFiresOnceOnStall：卡住任务触发 stalled，且「只发一次」防事件风暴
//     （tick 两轮只产生一条 stalled，订阅者只收到一次广播）
//   - TestWatchdogIgnoresFreshAndTerminal：新鲜任务（事件未超阈值）与终态任务不触发
//   - TestRecoverOnStartup：探活恒 false → failed 事件 + waiting_review；
//     探活恒 true → 任务保持 running 不动；终态任务不被探测
//
// 为什么用「注入极小 stallTimeout」模拟「last event 3h 前」：AppendEvent 的时间戳
// 由 store 内部取当前时间，测试无法直接回填旧时间（否则要改公开 API 或直改
// SQLite 表）。stallTimeout 本就是可注入参数（与 tick 同款，见 runWatchdog），
// 传入 time.Nanosecond 等价于「最新事件远超阈值」，语义与 3h 前事件完全一致。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// discardLogger 返回丢弃输出的 logger，保证测试输出干净（与 hub_test 的 TestMain 同款）。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestStore 打开临时目录下的真实 store（SQLite 落盘，验证真实持久化行为）。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedRunningTask 创建任务并迁移到 running，随后追加一条事件（看门狗判定的基准）。
func seedRunningTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	createRunningTask(t, st, id)
	if _, err := st.AppendEvent(id, proto.EventTypeProgress, map[string]string{"text": "开始干活"}); err != nil {
		t.Fatalf("AppendEvent(%s): %v", id, err)
	}
}

// seedWaitingAnswerTask 创建任务并迁移到 waiting_answer（running→waiting_answer 合法）。
func seedWaitingAnswerTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	createRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("置为 waiting_answer: %v", err)
	}
	if _, err := st.AppendEvent(id, proto.EventTypePermissionRequest, map[string]string{"text": "等待审批"}); err != nil {
		t.Fatalf("AppendEvent(%s): %v", id, err)
	}
}

// seedCompletedTask 创建任务并迁到终态 completed（seedRunningTask 的事件基准保留）。
func seedCompletedTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	seedRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingReview); err != nil {
		t.Fatalf("置为 waiting_review: %v", id)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateCompleted); err != nil {
		t.Fatalf("置为 completed: %v", id)
	}
}

// seedWaitingReviewTask 创建任务并迁到 waiting_review（协调者审阅中，executor
// 可能还活着等续接指令，也可能已不在）。
func seedWaitingReviewTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	createRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingReview); err != nil {
		t.Fatalf("置为 waiting_review: %v", id)
	}
}

// stalledEvents 返回任务的全部 stalled 事件（断言用）。
func stalledEvents(t *testing.T, st *store.Store, taskID string) []proto.Event {
	t.Helper()
	evs, err := st.EventsFrom(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	var out []proto.Event
	for _, ev := range evs {
		if ev.Type == proto.EventTypeStalled {
			out = append(out, ev)
		}
	}
	return out
}

// assertState 断言任务当前状态。
func assertState(t *testing.T, st *store.Store, taskID string, want proto.TaskState) {
	t.Helper()
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", taskID, err)
	}
	if task.State != want {
		t.Fatalf("任务 %s 状态期望 %s，实际 %s", taskID, want, task.State)
	}
}

// TestWatchdogFiresOnceOnStall：卡住任务（事件远超 stallTimeout）触发 stalled，
// 但两轮 tick 只产生一条——「只发一次」防事件风暴。
func TestWatchdogFiresOnceOnStall(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	seedRunningTask(t, st, "task-stalled")

	// 订阅事件流：断言的第二通道（事件既落库也经 hub 广播）
	evCh, unsubscribe := hub.Subscribe("task-stalled")
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// tick 10ms 注入（测试参数），stallTimeout 极小 = 事件必然超阈值
	go runWatchdog(ctx, st, hub, time.Nanosecond, 10*time.Millisecond, 0, 0, func(string) {}, discardLogger())

	// 等第一条 stalled 落库（首轮触发）
	eventually(t, 2*time.Second, "stalled 事件已落库", func() bool {
		return len(stalledEvents(t, st, "task-stalled")) == 1
	})
	// 再等 3 个 tick 窗口：若「只发一次」失效，此间会重复触发
	time.Sleep(30 * time.Millisecond)
	cancel()

	got := stalledEvents(t, st, "task-stalled")
	if len(got) != 1 {
		t.Fatalf("期望恰好 1 条 stalled 事件，实际 %d 条（只发一次失效）", len(got))
	}

	// payload 断言：last_seq 指向触发时刻的最新事件，idle 非空
	var pl stalledPayload
	if err := json.Unmarshal(got[0].Payload, &pl); err != nil {
		t.Fatalf("解析 stalled payload: %v", err)
	}
	if pl.LastSeq == 0 || pl.Idle == "" {
		t.Fatalf("stalled payload 缺字段: %+v", pl)
	}

	// 广播断言：订阅者收到且只收到一条 stalled
	select {
	case ev := <-evCh:
		if ev.Type != proto.EventTypeStalled {
			t.Fatalf("订阅者收到非 stalled 事件: %s", ev.Type)
		}
	default:
		t.Fatal("订阅者未收到 stalled 事件（Publish 未生效）")
	}
	select {
	case ev := <-evCh:
		t.Fatalf("订阅者收到第二条事件（只发一次失效）: %s", ev.Type)
	default:
	}
}

// TestWatchdogIgnoresFreshAndTerminal：新鲜任务与终态任务都不触发 stalled。
func TestWatchdogIgnoresFreshAndTerminal(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	// 新鲜任务：事件刚产生（stallTimeout 1h 远超事件年龄），不判定卡住
	seedRunningTask(t, st, "task-fresh")
	// 终态任务：即使事件陈旧也不在扫描范围（只扫 running/waiting_answer）
	seedCompletedTask(t, st, "task-done")

	ctx, cancel := context.WithCancel(context.Background())
	go runWatchdog(ctx, st, hub, time.Hour, 10*time.Millisecond, 0, 0, func(string) {}, discardLogger())
	time.Sleep(30 * time.Millisecond)
	cancel()

	if got := stalledEvents(t, st, "task-fresh"); len(got) != 0 {
		t.Fatalf("新鲜任务不应触发 stalled，实际 %d 条", len(got))
	}
	if got := stalledEvents(t, st, "task-done"); len(got) != 0 {
		t.Fatalf("终态任务不应触发 stalled，实际 %d 条", len(got))
	}
}

// TestWatchdogRefiresStalledAfterReply（P1-15a）：已 stalled 的任务在协调者回答后
// 仍无新事件产出（executor 假死），下一轮 tick 必须二次触发 stalled——这是最需要
// 二次告警的场景（旧实现「只发一次」裁决后永远不再告警）；而无活动时依旧只发一次
// 不刷屏。
func TestWatchdogRefiresStalledAfterReply(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	seedWaitingAnswerTask(t, st, "task-reply")
	// 补一个挂起工单供 reply 回答，走真实 handleReply 的 AnswerTicket 路径
	if _, err := st.CreateTicket(&proto.Ticket{ID: "task-reply:t1", TaskID: "task-reply",
		Kind: "gate", Request: []byte(`{"kind":"gate"}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runWatchdog(ctx, st, hub, time.Nanosecond, 10*time.Millisecond, 0, 0, func(string) {}, discardLogger())

	// 第一轮：stalled 触发一次
	eventually(t, 2*time.Second, "首条 stalled 已落库", func() bool {
		return len(stalledEvents(t, st, "task-reply")) == 1
	})

	// 模拟协调者回答 + 回迁（server.handleReply 的回程：AnswerTicket → resumeIfIdle）
	if err := st.AnswerTicket("task-reply:t1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	if err := st.UpdateTaskState("task-reply", proto.TaskStateRunning); err != nil {
		t.Fatalf("回迁 running: %v", err)
	}

	// 第二轮：executor 仍无事件产出但 updated_at 已前进 → 必须二次 stalled
	eventually(t, 2*time.Second, "二次 stalled 已落库", func() bool {
		return len(stalledEvents(t, st, "task-reply")) == 2
	})

	// 之后无活动不刷屏：再等若干 tick，仍只有 2 条（二次告警是活动驱动的，不是每轮重发）
	time.Sleep(50 * time.Millisecond)
	cancel()
	if got := len(stalledEvents(t, st, "task-reply")); got != 2 {
		t.Fatalf("无活动后 stalled 应保持 2 条，实际 %d（二次告警刷屏）", got)
	}
}

// TestWatchdogCatchesZeroEventTask（P1-15b）：从未产出任何事件的任务（静默挂起）
// 不再被跳过——以 task.UpdatedAt 兜底基线，超时后同样触发 stalled 且只触发一次，
// 事件锚点 LastSeq 记 0（无事件可锚）。
func TestWatchdogCatchesZeroEventTask(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	createRunningTask(t, st, "task-silent") // 无事件：只有任务行

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runWatchdog(ctx, st, hub, time.Nanosecond, 10*time.Millisecond, 0, 0, func(string) {}, discardLogger())

	eventually(t, 2*time.Second, "零事件任务 stalled 已落库", func() bool {
		return len(stalledEvents(t, st, "task-silent")) == 1
	})
	time.Sleep(30 * time.Millisecond)
	cancel()
	if got := len(stalledEvents(t, st, "task-silent")); got != 1 {
		t.Fatalf("零事件任务 stalled 应恰好 1 条，实际 %d（应只发一次）", got)
	}
	var pl stalledPayload
	evs := stalledEvents(t, st, "task-silent")
	if err := json.Unmarshal(evs[0].Payload, &pl); err != nil {
		t.Fatalf("解析 stalled payload: %v", err)
	}
	if pl.LastSeq != 0 {
		t.Fatalf("零事件任务 stalled LastSeq=%d, want 0（无事件可锚）", pl.LastSeq)
	}
	if pl.Idle == "" {
		t.Fatal("零事件任务 stalled 缺 Idle 字段")
	}
}

// TestRecoverOnStartupVoidsPendingTickets（P1-16）：探活失败的 dead 任务，其挂起
// 工单被作废——attach 的 pending_tickets 不再出现无法操作的挂起项（executor 已不
// 在，一操作就撞 P0-5）；answer 置为 VoidAnswer 留审计痕迹，且该任务仍迁移
// waiting_review 交协调者裁决。
func TestRecoverOnStartupVoidsPendingTickets(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	seedWaitingAnswerTask(t, st, "task-dead-tk")
	if _, err := st.CreateTicket(&proto.Ticket{ID: "task-dead-tk:p1", TaskID: "task-dead-tk",
		Kind: "gate", Request: []byte(`{"kind":"gate"}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if err := RecoverOnStartup(st, hub, func(string) bool { return false }, func(string) {}, discardLogger()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	// 任务仍按既有恢复语义落 waiting_review
	assertState(t, st, "task-dead-tk", proto.TaskStateWaitingReview)

	// attach 数据源：pending_tickets 为空（作废后 answer 非空，天然不再返回）
	pending, err := st.PendingTickets("task-dead-tk")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("dead 任务恢复后 pending_tickets=%d, want 0（挂起项必须作废）", len(pending))
	}
	// 审计痕迹：answer 置为 VoidAnswer
	tk, err := st.GetTicket("task-dead-tk:p1")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if tk.Answer == nil || *tk.Answer != store.VoidAnswer {
		t.Fatalf("作废工单 answer=%v, want VoidAnswer(%q)", tk.Answer, store.VoidAnswer)
	}
}

// TestRecoverOnStartup：探活失败 → failed 事件 + waiting_review；
// 探活成功 → 任务保持 running 不动；终态任务不被探测。
func TestRecoverOnStartup(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()

	// 探活恒 false：running 与 waiting_answer 任务都要转 failed 交协调者裁决
	seedRunningTask(t, st, "task-dead-run")
	seedWaitingAnswerTask(t, st, "task-dead-wa")
	// 探活恒 true：任务保持 running 不动
	seedRunningTask(t, st, "task-alive")
	// 终态任务：不在恢复范围，也不该被探测
	seedCompletedTask(t, st, "task-done")

	probed := map[string]int{}
	probe := func(taskID string) bool {
		probed[taskID]++
		return taskID == "task-alive"
	}

	if err := RecoverOnStartup(st, hub, probe, func(string) {}, discardLogger()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	// 失败路径：两个非存活任务都落到 waiting_review
	assertState(t, st, "task-dead-run", proto.TaskStateWaitingReview)
	assertState(t, st, "task-dead-wa", proto.TaskStateWaitingReview)
	// 成功路径：任务不动
	assertState(t, st, "task-alive", proto.TaskStateRunning)
	// 终态：不动、不被探测
	assertState(t, st, "task-done", proto.TaskStateCompleted)
	if _, ok := probed["task-done"]; ok {
		t.Fatal("终态任务不应被探活")
	}

	// turn_failed 事件断言：running 与 waiting_answer 各追加一条，原因固定。
	// 是 turn_failed 不是 failed——两个任务都收在 waiting_review（见上面的 assertState），没有终结（B100 补漏）
	for _, id := range []string{"task-dead-run", "task-dead-wa"} {
		evs, err := st.EventsFrom(id, 0, 100)
		if err != nil {
			t.Fatalf("EventsFrom(%s): %v", id, err)
		}
		var failed []proto.Event
		for _, ev := range evs {
			if ev.Type == proto.EventTypeTurnFailed {
				failed = append(failed, ev)
			}
		}
		if len(failed) != 1 {
			t.Fatalf("任务 %s 期望 1 条 turn_failed 事件，实际 %d 条", id, len(failed))
		}
		var pl failedPayload
		if err := json.Unmarshal(failed[0].Payload, &pl); err != nil {
			t.Fatalf("解析 failed payload: %v", err)
		}
		if pl.FailReason != "agentd 重启后执行器已不在" {
			t.Fatalf("任务 %s failed 原因期望固定文案，实际 %q", id, pl.FailReason)
		}
	}

	// 成功路径：task-alive 不应有新事件
	evs, err := st.EventsFrom("task-alive", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom(task-alive): %v", err)
	}
	for _, ev := range evs {
		if ev.Type == proto.EventTypeFailed {
			t.Fatalf("存活任务不应产生 failed 事件: seq=%d", ev.Seq)
		}
	}
}

// TestRecoverOnStartupRebuildsWaitingReview 覆盖 agentd 重启后 waiting_review 任务
// 的续接恢复：executor 存活（probe=true）时必须重建订阅与中介循环（即被探活），
// 但**不改任务状态**——waiting_review 是协调者裁决的落点，就该留在原地等人；也不得
// 追加任何事件。旧实现显式跳过 waiting_review（「是人的节奏」），续接上下文随
// agentd 进程消亡而丢失，continue 永久失败。
func TestRecoverOnStartupRebuildsWaitingReview(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	seedWaitingReviewTask(t, st, "task-review-alive")

	probed := map[string]int{}
	probe := func(taskID string) bool {
		probed[taskID]++
		return true
	}

	if err := RecoverOnStartup(st, hub, probe, func(string) {}, discardLogger()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}
	if probed["task-review-alive"] != 1 {
		t.Fatalf("waiting_review 存活任务应被探活重建，probed=%d, want 1", probed["task-review-alive"])
	}
	assertState(t, st, "task-review-alive", proto.TaskStateWaitingReview)

	evs, err := st.EventsFrom("task-review-alive", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	for _, ev := range evs {
		if ev.Type == proto.EventTypeFailed {
			t.Fatalf("waiting_review 存活重建不得追加 failed 事件: seq=%d", ev.Seq)
		}
	}
}

// TestRecoverOnStartupKeepsDeadWaitingReview 覆盖 waiting_review 任务 executor 已不在
// 的恢复：保持现状即可——不追加 failed 事件、不迁移状态（它本来就是待审核终态，
// 追加事件只会产生噪音）。与 running/waiting_answer 的「failed 迁移」路径严格区分。
func TestRecoverOnStartupKeepsDeadWaitingReview(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()
	seedWaitingReviewTask(t, st, "task-review-dead")

	probed := map[string]int{}
	probe := func(taskID string) bool {
		probed[taskID]++
		return false
	}
	swept := 0

	if err := RecoverOnStartup(st, hub, probe, func(string) { swept++ }, discardLogger()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}
	if probed["task-review-dead"] != 1 {
		t.Fatalf("waiting_review 任务应被探活判断，probed=%d, want 1", probed["task-review-dead"])
	}
	assertState(t, st, "task-review-dead", proto.TaskStateWaitingReview)
	if swept != 1 {
		t.Fatalf("waiting_review 保持分支也必须清扫一次残留，实际 %d 次", swept)
	}

	evs, err := st.EventsFrom("task-review-dead", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	for _, ev := range evs {
		if ev.Type == proto.EventTypeFailed {
			t.Fatalf("waiting_review 任务 executor 不在也不得追加 failed 事件: seq=%d", ev.Seq)
		}
	}
}
