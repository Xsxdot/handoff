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

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
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
	go runWatchdog(ctx, st, hub, time.Nanosecond, 10*time.Millisecond, discardLogger())

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
	go runWatchdog(ctx, st, hub, time.Hour, 10*time.Millisecond, discardLogger())
	time.Sleep(30 * time.Millisecond)
	cancel()

	if got := stalledEvents(t, st, "task-fresh"); len(got) != 0 {
		t.Fatalf("新鲜任务不应触发 stalled，实际 %d 条", len(got))
	}
	if got := stalledEvents(t, st, "task-done"); len(got) != 0 {
		t.Fatalf("终态任务不应触发 stalled，实际 %d 条", len(got))
	}
}

// TestRecoverOnStartup：探活失败 → failed 事件 + waiting_review；
// 探活成功 → 任务保持 running 不动；终态任务不被探测。
func TestRecoverOnStartup(t *testing.T) {
	st := newTestStore(t)
	hub := NewHub()

	// 探活恒 false：running 与 waiting_answer 任务都要转 failed 交审核者裁决
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

	if err := RecoverOnStartup(st, hub, probe, discardLogger()); err != nil {
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

	// failed 事件断言：running 与 waiting_answer 各追加一条，原因固定
	for _, id := range []string{"task-dead-run", "task-dead-wa"} {
		evs, err := st.EventsFrom(id, 0, 100)
		if err != nil {
			t.Fatalf("EventsFrom(%s): %v", id, err)
		}
		var failed []proto.Event
		for _, ev := range evs {
			if ev.Type == proto.EventTypeFailed {
				failed = append(failed, ev)
			}
		}
		if len(failed) != 1 {
			t.Fatalf("任务 %s 期望 1 条 failed 事件，实际 %d 条", id, len(failed))
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
