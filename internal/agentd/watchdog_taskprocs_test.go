// watchdog_taskprocs_test.go —— scanTaskProcs（任务级进程点名）的白盒单测。
//
// 职责：验证两档处置语义——超 budget 发一次 task_proc_pressure（边沿触发不重发、
// 回落复位）、超 hardLimit 调用强制回收（理由带真实数字）、两档关闭时
// 完全不产生开销、读数不可信时什么都不做、终态任务不被点名。
//
// 边界：不测 Manager.TaskProcCount 的真实实现——那是 prochost 的活；本文件把
// taskProcCountFn 测试缝直接赋成假实现，聚焦 scanTaskProcs 自己的判定逻辑。
package agentd

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// newWatchdogFixture 造一个带 running 任务的最小看门狗环境。
func newWatchdogFixture(t *testing.T) (*store.Store, *Hub, string) {
	t.Helper()
	st, hub := newTestStoreHub(t)
	task := mustCreateTaskState(t, st, proto.TaskStateRunning)
	return st, hub, task.ID
}

// setTaskProcCount 把进程计数测试缝换成 map 查表，用完还原为 nil 防止用例间串味。
// 表里没有的任务返回 ok=false（等价于「数不出来」）。
func setTaskProcCount(t *testing.T, counts map[string]int) {
	t.Helper()
	taskProcCountFn = func(taskID string) (int, bool) {
		n, ok := counts[taskID]
		return n, ok
	}
	t.Cleanup(func() { taskProcCountFn = nil })
}

// countEventsWithText 数指定类型、且 payload 含指定子串的事件条数。
// 边沿去重的断言要的是「恰好一条」，只判存在性不够。
func countEventsWithText(evs []proto.Event, typ proto.EventType, want string) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ && strings.Contains(string(e.Payload), want) {
			n++
		}
	}
	return n
}

// drain 非阻塞清空通道，直到通道空为止。
func drain(ch <-chan proto.Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// mustUnmarshalPayload 解析事件 payload，失败即 t.Fatal。
func mustUnmarshalPayload(t *testing.T, evt proto.Event, dst any) {
	t.Helper()
	if err := json.Unmarshal(evt.Payload, dst); err != nil {
		t.Fatalf("解析事件 %s payload: %v", evt.Type, err)
	}
}

// mustTransit 把任务迁移到指定状态，失败即 t.Fatal。
func mustTransit(t *testing.T, st *store.Store, taskID string, state proto.TaskState) {
	t.Helper()
	if err := st.UpdateTaskState(taskID, state); err != nil {
		t.Fatalf("迁移任务 %s 到 %s: %v", taskID, state, err)
	}
}

func TestScanTaskProcsWarnsOnceAtBudget(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t) // 造一个 running 任务
	ch, cancel := hub.Subscribe(taskID)
	defer cancel()
	taskProcCountFn = func(string) (int, bool) { return 500, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	fired := map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error {
		t.Fatal("500 未超硬上限 1200，不该清扫")
		return nil
	}, testLogger(t))

	// 必须 Publish，不能只 AppendEvent——B91 刚修过一条「只落库不广播、
	// 审核者永远不知道」的缺陷，同一个坑不踩第二次
	select {
	case evt := <-ch:
		if evt.Type != proto.EventTypeTaskProcPressure {
			t.Fatalf("事件类型 %s", evt.Type)
		}
		var p taskProcPressurePayload
		mustUnmarshalPayload(t, evt, &p)
		if p.Used != 500 || p.Budget != 400 {
			t.Fatalf("payload 应带真实数字，实际 %+v", p)
		}
	default:
		t.Fatal("事件没有被广播出来")
	}

	// 第二轮仍超预算：不重发（沿用 scanPressure 的边沿触发口径，
	// 理由相同——事件风暴会淹掉真正要处置的工单）
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error { return nil }, testLogger(t))
	select {
	case evt := <-ch:
		t.Fatalf("第二轮不该重发，却收到 %s", evt.Type)
	default:
	}
}

func TestScanTaskProcsRearmsAfterFallback(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t)
	ch, cancel := hub.Subscribe(taskID)
	defer cancel()
	fired := map[string]bool{}

	taskProcCountFn = func(string) (int, bool) { return 500, true }
	t.Cleanup(func() { taskProcCountFn = nil })
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error { return nil }, testLogger(t))
	drain(ch)

	// 回落到预算以下 → 复位
	taskProcCountFn = func(string) (int, bool) { return 300, true }
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error { return nil }, testLogger(t))
	if fired[taskID] {
		t.Fatal("回落后应复位")
	}

	// 再次越线 → 重新发一次
	taskProcCountFn = func(string) (int, bool) { return 500, true }
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error { return nil }, testLogger(t))
	select {
	case <-ch:
	default:
		t.Fatal("复位后再越线应重新告警")
	}
}

func TestScanTaskProcsCallsReclaimAtHardLimit(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t)
	taskProcCountFn = func(string) (int, bool) { return 1500, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	var gotTask, gotReason string
	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, map[string]bool{}, func(id, reason string) error {
		gotTask, gotReason = id, reason
		return nil
	}, testLogger(t))

	if gotTask != taskID {
		t.Fatalf("超硬上限应调用强制回收，实际任务 %q", gotTask)
	}
	if !strings.Contains(gotReason, "1500") || !strings.Contains(gotReason, "1200") {
		t.Fatalf("回收理由应含 used 与 hard limit 两个数字，实际 %q", gotReason)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("状态迁移应由 ForceReclaim 负责，扫描器不应直接改状态，实际 %s", task.State)
	}
}

func TestScanTaskProcsDisabledDoesNotCount(t *testing.T) {
	// why：不启用就该完全不产生开销，而不是「数了但不发事件」。
	// Footprint 每次都要枚举全系统进程表，白数是实打实的浪费
	st, hub, _ := newWatchdogFixture(t)
	called := 0
	taskProcCountFn = func(string) (int, bool) { called++; return 9999, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	scanTaskProcs(st, hub, 0, 0, map[string]bool{}, map[string]bool{}, func(string, string) error {
		t.Fatal("两档都关时不该清扫")
		return nil
	}, testLogger(t))

	if called != 0 {
		t.Fatalf("两档都关时不该数进程，实际数了 %d 次", called)
	}
}

func TestScanTaskProcsUnknownCountDoesNothing(t *testing.T) {
	// why：数不出来就什么都不做。把「量不出来」当成「超了」会误杀，
	// 当成「没超」会让已置位状态错乱——两种都比不动更糟
	st, hub, _ := newWatchdogFixture(t)
	taskProcCountFn = func(string) (int, bool) { return 0, false }
	t.Cleanup(func() { taskProcCountFn = nil })

	fired := map[string]bool{"x": true}
	scanTaskProcs(st, hub, 400, 1200, fired, map[string]bool{}, func(string, string) error {
		t.Fatal("读数不可信时不该清扫")
		return nil
	}, testLogger(t))
	if !fired["x"] {
		t.Fatal("读数不可信时不该改动置位状态")
	}
}

func TestScanTaskProcsSkipsTerminalTasks(t *testing.T) {
	// why：终态任务已经不会再 fork 任何东西。沿用 scanPressure 的 IsTerminal
	// 取反写法，新增状态时自动跟上
	st, hub, taskID := newWatchdogFixture(t)
	mustTransit(t, st, taskID, proto.TaskStateCompleted)
	called := 0
	taskProcCountFn = func(string) (int, bool) { called++; return 9999, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, map[string]bool{}, func(string, string) error { return nil }, testLogger(t))
	if called != 0 {
		t.Fatalf("终态任务不该被点名，实际 %d 次", called)
	}
}

// TestScanTaskProcsHardLimitCallsReclaim 硬上限档必须走强制回收（先停 executor
// 再落 failed），而不是直接对活着的 executor 调 sweep——后者在 B119 之前恒被
// 拒绝，一个进程都不杀却照样宣布「已强制回收」。
func TestScanTaskProcsHardLimitCallsReclaim(t *testing.T) {
	st, hub := newTestStoreHub(t)
	createRunningTask(t, st, "t1")
	setTaskProcCount(t, map[string]int{"t1": 1300})

	var gotTask, gotReason string
	reclaim := func(taskID, reason string) error {
		gotTask, gotReason = taskID, reason
		return nil
	}
	fired, reclaimFailed := map[string]bool{}, map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())

	if gotTask != "t1" {
		t.Fatalf("应对 t1 发起强制回收，实得 %q", gotTask)
	}
	if !strings.Contains(gotReason, "1300") || !strings.Contains(gotReason, "1200") {
		t.Fatalf("理由必须含实际进程数与硬上限两个真实数字，实得 %q", gotReason)
	}
}

// TestScanTaskProcsReclaimFailureWarnsOnce 停不掉时的边沿去重：首轮发一条提示
// 惊动协调者，后续轮次继续重试但不再刷屏。用与告警档分开的 map——两者的回落
// 判据不同（告警档看进程数回落，本档看停止是否成功）。
func TestScanTaskProcsReclaimFailureWarnsOnce(t *testing.T) {
	st, hub := newTestStoreHub(t)
	createRunningTask(t, st, "t1")
	setTaskProcCount(t, map[string]int{"t1": 1300})

	calls := 0
	reclaim := func(taskID, reason string) error {
		calls++
		return errors.New("已发 SIGKILL 但复核仍存活")
	}
	fired, reclaimFailed := map[string]bool{}, map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())

	if calls != 2 {
		t.Fatalf("每轮都应重试强制回收，实得 %d 次", calls)
	}
	evs := mustEvents(t, st, "t1")
	n := countEventsWithText(evs, proto.EventTypeProgress, "强制回收失败")
	if n != 1 {
		t.Fatalf("回收失败提示应只发一条（边沿触发），实得 %d 条", n)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("回收失败时任务必须保持活跃，实得 %s", cur.State)
	}
}
