package agentd

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/prochost"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// 越线时对每个活跃任务发一次，且只发一次——事件风暴会把审核者的
// 会话刷爆，反而淹掉真正要处置的工单。
func TestResourcePressureFiresOnceOnRisingEdge(t *testing.T) {
	st, hub := newTestStoreHub(t) // 复用 watchdog_test.go 既有骨架
	t1 := mustCreateTaskState(t, st, proto.TaskStateRunning)
	t2 := mustCreateTaskState(t, st, proto.TaskStateWaitingAnswer)

	restore := fakeAdmission(prochost.Admission{Used: 2200, Limit: 2400, Known: true})
	defer restore()

	var active bool
	active = scanPressure(st, hub, active, testLogger(t))
	if !active {
		t.Fatalf("越线后应置位")
	}
	assertEventCount(t, st, t1.ID, proto.EventTypeResourcePressure, 1)
	assertEventCount(t, st, t2.ID, proto.EventTypeResourcePressure, 1)

	// 第二轮仍在高水位：不重发
	active = scanPressure(st, hub, active, testLogger(t))
	assertEventCount(t, st, t1.ID, proto.EventTypeResourcePressure, 1)
}

// 回落后复位，再次越线要能再发——否则一次高水位之后这条告警就永久哑了。
func TestResourcePressureRearmsAfterRecovery(t *testing.T) {
	st, hub := newTestStoreHub(t)
	task := mustCreateTaskState(t, st, proto.TaskStateRunning)

	restore := fakeAdmission(prochost.Admission{Used: 2200, Limit: 2400, Known: true})
	active := scanPressure(st, hub, false, testLogger(t))
	restore()

	restore = fakeAdmission(prochost.Admission{Used: 100, Limit: 2400, Known: true})
	active = scanPressure(st, hub, active, testLogger(t))
	if active {
		t.Fatalf("回落后应复位")
	}
	restore()

	restore = fakeAdmission(prochost.Admission{Used: 2300, Limit: 2400, Known: true})
	defer restore()
	scanPressure(st, hub, active, testLogger(t))
	assertEventCount(t, st, task.ID, proto.EventTypeResourcePressure, 2)
}

// 失败事件必须带死亡时刻的占用快照：「死亡时 2390/2400」与「死亡时 300/2400」
// 一眼定性两个完全不同的方向，双向堵误判——既防把配额问题当代码 bug 查，
// 也防把代码 bug 甩锅给配额。
func TestFailedPayloadCarriesUsageSnapshot(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{Used: 2390, Limit: 2400, Known: true})
	defer restore()
	p := newFailedPayload("executor 进程消失", "", "")
	if p.FailReason != "executor 进程消失" {
		t.Fatalf("原因不该被改写，得到 %q", p.FailReason)
	}
	if p.ProcUsage == nil || p.ProcUsage.Used != 2390 || p.ProcUsage.Limit != 2400 {
		t.Fatalf("应附带占用快照，得到 %+v", p.ProcUsage)
	}
}

// 读不出数时快照留空而不是填 0：一个「0/0」的快照会被读成「死亡时机器很空闲」，
// 那是彻头彻尾的谎话，比没有快照更糟。
func TestFailedPayloadOmitsUnknownUsage(t *testing.T) {
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if p := newFailedPayload("x", "", ""); p.ProcUsage != nil {
		t.Fatalf("读数未知时不该附快照，得到 %+v", p.ProcUsage)
	}
}

// 读数未知：什么都不做，也不复位——把「量不出来」当成「回落了」会让
// 下一次真越线时因为状态错乱而漏报。
func TestResourcePressureUnknownIsNoop(t *testing.T) {
	st, hub := newTestStoreHub(t)
	task := mustCreateTaskState(t, st, proto.TaskStateRunning)
	restore := fakeAdmission(prochost.Admission{})
	defer restore()
	if got := scanPressure(st, hub, true, testLogger(t)); !got {
		t.Fatalf("未知读数不应复位置位状态")
	}
	assertEventCount(t, st, task.ID, proto.EventTypeResourcePressure, 0)
}

// 以下四个助手：watchdog_test.go 只有 newTestStore（无 hub）与
// mustCreateTask(t, st, *proto.Task)（签名不同，manager_test.go），没有等价助手，
// 在这里补最小实现。

// newTestStoreHub 打开临时目录下的真实 store（SQLite 落盘）并配一个 hub。
func newTestStoreHub(t *testing.T) (*store.Store, *Hub) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, NewHub()
}

// mustCreateTaskState 直接落库一个处于指定状态的任务，返回它供断言用。
// 名字带 State 后缀：manager_test.go 已有 mustCreateTask(t, st, *proto.Task)，
// Go 不支持重载，同包内不能两个签名同名。
func mustCreateTaskState(t *testing.T, st *store.Store, state proto.TaskState) *proto.Task {
	t.Helper()
	now := time.Now().UTC()
	task := &proto.Task{
		ID:        fmt.Sprintf("%s-%d", state, now.UnixNano()),
		Target:    "local",
		State:     state,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

// assertEventCount 断言任务上指定类型事件的条数。
func assertEventCount(t *testing.T, st *store.Store, taskID string, typ proto.EventType, want int) {
	t.Helper()
	evs, err := st.EventsFrom(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom(%s): %v", taskID, err)
	}
	got := 0
	for _, ev := range evs {
		if ev.Type == typ {
			got++
		}
	}
	if got != want {
		t.Fatalf("任务 %s 期望 %d 条 %s 事件，实际 %d 条", taskID, want, typ, got)
	}
}

// testLogger 返回丢弃输出的 logger（与 watchdog_test.go 的 discardLogger 同款）。
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
