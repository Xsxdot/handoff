package agentd

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestForceReclaimSuccessTransitsFailed 验证成功路径：executor 停掉之后任务落
// failed，理由原样进事件。清扫由 transit 的终态分支负责（B119 §2.2），本方法
// 不自己调 sweep——重复调用会让一次强制回收枚举两遍进程表。
func TestForceReclaimSuccessTransitsFailed(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "runaway-ok"
	// 显式造一个**带 managed worktree** 的任务：createRunningTask 造出来的
	// WorkDir 为空，拿它断言「worktree 未被删」会恒真，等于没测。
	workDir := t.TempDir()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: taskID, Target: "local", State: proto.TaskStatePending,
		WorkDir: workDir, WorktreeManaged: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil {
		t.Fatalf("置为 running: %v", err)
	}
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	reason := "任务进程数 1300 超过硬上限 1200，已强制回收"
	if err := m.ForceReclaim(taskID, reason); err != nil {
		t.Fatalf("ForceReclaim: %v", err)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateFailed {
		t.Fatalf("停掉之后任务应落 failed，实得 %s", cur.State)
	}
	if len(swept) != 1 {
		t.Fatalf("终态迁移应触发一次清扫，实得 %d 次", len(swept))
	}
	evs := mustEvents(t, st, taskID)
	if !hasEventWithText(evs, proto.EventTypeFailed, reason) {
		t.Fatalf("failed 事件应带真实理由 %q，实得 %v", reason, evs)
	}
	// worktree 必须还在：删 worktree 是不可逆且外部可见的动作，1200 进程这种
	// 现场最需要留证。handoff stop 由人敲、删是人的决定；watchdog 自动触发的
	// 强制回收不继承这个决定（B119 §2.3）。
	if _, serr := os.Stat(workDir); serr != nil {
		t.Fatalf("强制回收不得删除 worktree，但 %s 已不可用：%v", workDir, serr)
	}
}

// TestForceReclaimKeepsTaskActiveWhenStopFails 是 B119 最重的一条：没收掉就不能
// 宣布收掉了。executor 杀不掉时任务必须留在活跃集，下一轮 watchdog 才会继续点名
// 与重试；改前无论成败都落 failed，之后 IsTerminal 直接跳过它，那批进程从此
// 无人跟踪，而库里留着一条说「已强制回收」的假事件。
func TestForceReclaimKeepsTaskActiveWhenStopFails(t *testing.T) {
	ad := &stopErrAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		stopErr:     errors.New("已发 SIGKILL 但复核仍存活"),
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	createRunningTask(t, st, "runaway")

	err := m.ForceReclaim("runaway", "任务进程数 1300 超过硬上限 1200，已强制回收")
	if err == nil {
		t.Fatal("停不掉 executor 时 ForceReclaim 必须返回错误")
	}
	cur, gerr := st.GetTask("runaway")
	if gerr != nil {
		t.Fatalf("GetTask: %v", gerr)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("停不掉时任务必须保持活跃，实得 %s", cur.State)
	}
	evs := mustEvents(t, st, "runaway")
	if hasEvent(evs, proto.EventTypeFailed) {
		t.Fatalf("没收掉就不该落 failed 事件：%v", evs)
	}
}

// hasEventWithText 判断事件列表里是否存在指定类型、且 payload 含指定子串的事件。
// 按 payload 原始 JSON 匹配：failed 事件的理由在 payload.fail_reason 里，
// 逐层解结构体只会让断言跟着 payload 形态漂移。
func hasEventWithText(evs []proto.Event, typ proto.EventType, want string) bool {
	for _, e := range evs {
		if e.Type == typ && strings.Contains(string(e.Payload), want) {
			return true
		}
	}
	return false
}
