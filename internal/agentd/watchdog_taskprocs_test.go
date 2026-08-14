// watchdog_taskprocs_test.go —— scanTaskProcs（任务级进程点名）的白盒单测。
//
// 职责：验证两档处置语义——超 budget 发一次 task_proc_pressure（边沿触发不重发、
// 回落复位）、超 hardLimit 强制清扫并落 failed（理由带真实数字）、两档关闭时
// 完全不产生开销、读数不可信时什么都不做、终态任务不被点名。
//
// 边界：不测 Manager.TaskProcCount 的真实实现——那是 prochost 的活；本文件把
// taskProcCountFn 测试缝直接赋成假实现，聚焦 scanTaskProcs 自己的判定逻辑。
package agentd

import (
	"encoding/json"
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
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {
		t.Fatal("500 未超硬上限 1200，不该清扫")
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
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, testLogger(t))
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
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, testLogger(t))
	drain(ch)

	// 回落到预算以下 → 复位
	taskProcCountFn = func(string) (int, bool) { return 300, true }
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, testLogger(t))
	if fired[taskID] {
		t.Fatal("回落后应复位")
	}

	// 再次越线 → 重新发一次
	taskProcCountFn = func(string) (int, bool) { return 500, true }
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {}, testLogger(t))
	select {
	case <-ch:
	default:
		t.Fatal("复位后再越线应重新告警")
	}
}

func TestScanTaskProcsSweepsAtHardLimit(t *testing.T) {
	st, hub, taskID := newWatchdogFixture(t)
	taskProcCountFn = func(string) (int, bool) { return 1500, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	var swept []string
	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, func(id string) {
		swept = append(swept, id)
	}, testLogger(t))

	if len(swept) != 1 || swept[0] != taskID {
		t.Fatalf("超硬上限应清扫，实际 %v", swept)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateFailed {
		t.Fatalf("超硬上限应落 failed，实际 %s", task.State)
	}
	ev, err := st.LatestEvent(taskID)
	if err != nil {
		t.Fatal(err)
	}
	// 理由里必须带真实数字：这是不可逆动作，审核者事后要能判断杀得对不对。
	// 注意：不能直接 fmt.Sprint(ev) 找 "1500"——Payload 是 json.RawMessage，
	// fmt.Sprint 会打印字节数组而不是 JSON 文本。要解 payload 断言 FailReason。
	var p failedPayload
	mustUnmarshalPayload(t, *ev, &p)
	if !strings.Contains(p.FailReason, "1500") || !strings.Contains(p.FailReason, "1200") {
		t.Fatalf("failed 理由应含 used 与 hard limit 两个数字，实际 %q", p.FailReason)
	}
}

func TestScanTaskProcsDisabledDoesNotCount(t *testing.T) {
	// why：不启用就该完全不产生开销，而不是「数了但不发事件」。
	// Footprint 每次都要枚举全系统进程表，白数是实打实的浪费
	st, hub, _ := newWatchdogFixture(t)
	called := 0
	taskProcCountFn = func(string) (int, bool) { called++; return 9999, true }
	t.Cleanup(func() { taskProcCountFn = nil })

	scanTaskProcs(st, hub, 0, 0, map[string]bool{}, func(string) {
		t.Fatal("两档都关时不该清扫")
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
	scanTaskProcs(st, hub, 400, 1200, fired, func(string) {
		t.Fatal("读数不可信时不该清扫")
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

	scanTaskProcs(st, hub, 400, 1200, map[string]bool{}, func(string) {}, testLogger(t))
	if called != 0 {
		t.Fatalf("终态任务不该被点名，实际 %d 次", called)
	}
}
