// 运行态对账的白盒测试：executor 已不在这一事实的唯一收尾实现。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
)

// quietLog 返回丢弃所有输出的日志器（对账函数白盒测试用）。
func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestReconcileExecutorGone 表驱动：活跃态收尾并落 waiting_review，终态/待审核态是空操作。
func TestReconcileExecutorGone(t *testing.T) {
	cases := []struct {
		name      string
		from      proto.TaskState
		wantState proto.TaskState
		wantEvent bool
	}{
		{"running 收尾", proto.TaskStateRunning, proto.TaskStateWaitingReview, true},
		{"waiting_answer 两跳收尾", proto.TaskStateWaitingAnswer, proto.TaskStateWaitingReview, true},
		{"waiting_review 空操作", proto.TaskStateWaitingReview, proto.TaskStateWaitingReview, false},
		{"completed 空操作", proto.TaskStateCompleted, proto.TaskStateCompleted, false},
		{"failed 空操作", proto.TaskStateFailed, proto.TaskStateFailed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: c.from})
			got := reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog(), func(string) {})
			if got != c.wantState {
				t.Fatalf("返回状态 = %s，期望 %s", got, c.wantState)
			}
			cur, err := st.GetTask("t1")
			if err != nil {
				t.Fatal(err)
			}
			if cur.State != c.wantState {
				t.Fatalf("落库状态 = %s，期望 %s", cur.State, c.wantState)
			}
			evs, err := st.EventsFromAsc("t1", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			hasFailed := false
			for _, e := range evs {
				if e.Type == proto.EventTypeTurnFailed {
					hasFailed = true
				}
			}
			if hasFailed != c.wantEvent {
				t.Fatalf("turn_failed 事件 = %v，期望 %v", hasFailed, c.wantEvent)
			}
		})
	}
}

// TestReconcileExecutorGoneIdempotent 幂等：三个到达口可能对同一任务先后触发，
// 第二次必须是空操作，不产重复事件。
func TestReconcileExecutorGoneIdempotent(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})
	reconcileExecutorGone(st, NewHub(), "t1", "第一次", quietLog(), func(string) {})
	reconcileExecutorGone(st, NewHub(), "t1", "第二次", quietLog(), func(string) {})
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == proto.EventTypeTurnFailed {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("turn_failed 事件应只有 1 条（幂等），实际 %d", n)
	}
}

// TestReconcileExecutorGoneVoidsPendingTickets 验证挂起工单被作废：
// executor 已不在，attach 继续展示可操作的挂起项就是假象（P1-16 同因）。
func TestReconcileExecutorGoneVoidsPendingTickets(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})
	if _, err := st.CreateTicket(&proto.Ticket{ID: "t1:p1", TaskID: "t1", Kind: "permission", Request: json.RawMessage(`{"permission":"Bash: ls"}`)}); err != nil {
		t.Fatal(err)
	}
	reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog(), func(string) {})
	pend, err := st.PendingTickets("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("挂起工单应被作废，实际剩 %d", len(pend))
	}
}

// TestReconcileTransitsBeforeEvent 同上，对 reconcileExecutorGone：turn_failed
// 事件落库那一刻，状态必须已迁到 waiting_review——reconcileExecutorGone 迁的不是
// failed（任务未终结，executor 死了代码还在，等协调者 diff 完裁决），是 waiting_review。
//
// 断言机制与 TestStopTransitsBeforeEvent 同款：钩子同步触发于 INSERT 之后、AppendEvent
// 返回之前；钩子阻塞住 AppendEvent，主 goroutine 收到通知时（旧实现里 recoverTransit
// 还没跑）读到的状态必然是 running，旧实现断言必失败，新实现必通过。
func TestReconcileTransitsBeforeEvent(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: t.TempDir(), State: proto.TaskStateRunning})

	fired := make(chan proto.Event, 1)
	release := make(chan struct{})
	st.SetEventHook(func(e proto.Event) {
		fired <- e
		<-release
	})

	go func() {
		reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog(), func(string) {})
	}()

	var gotType proto.EventType
	select {
	case e := <-fired:
		gotType = e.Type
	case <-time.After(5 * time.Second):
		t.Fatal("等待对账的 turn_failed 事件落库超时")
	}
	// 放行钩子：GetTask 期间 reconcileExecutorGone 停在 AppendEvent 内，读到的就是
	// 事件落库瞬间的状态
	defer close(release)
	if gotType != proto.EventTypeTurnFailed {
		t.Fatalf("对账首个事件应为 turn_failed，实际 %s", gotType)
	}
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("turn_failed 事件落库瞬间状态应为 %s，实际 %s（先事件后状态 = 破损中间态）",
			proto.TaskStateWaitingReview, cur.State)
	}
}

// TestMediateReconcilesOnEventsClosed 到达口②：adapter 关闭事件通道 = executor 终结，
// mediate 退出后必须对账——否则任务停在 running 直到 2h 看门狗（B21 实测静止 1 小时）。
func TestMediateReconcilesOnEventsClosed(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning})
	done := make(chan struct{})
	go func() { m.mediate("t1"); close(done) }()
	close(ad.evCh) // executor 终结
	<-done

	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("事件通道关闭后应对账落 waiting_review，实际 %s", cur.State)
	}
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeTurnFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("应产出 turn_failed 事件说明 executor 已不在（任务收 waiting_review，未终结）")
	}
}

// TestStoppingMarkerSuppressesReconcile 主动停止不该被当成异常终结：
// Manager.Stop 先调 ad.Stop() 再落 failed，中间的窗口里对账会看到 running，
// 补一条噪音 failed 事件并造成 running→waiting_review→failed 的状态抖动。
func TestStoppingMarkerSuppressesReconcile(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning})
	m.noteStopping("t1")
	done := make(chan struct{})
	go func() { m.mediate("t1"); close(done) }()
	close(ad.evCh)
	<-done

	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("主动停止期间不应对账，状态应留在 running，实际 %s", cur.State)
	}
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("主动停止期间不应产出对账事件，实际 %d 条", len(evs))
	}
}

// TestStoppingMarkerIsTakeStyle 取走式：标记的生命周期就是一次主动停止。
// 若标记长期驻留，下一次 executor 猝死会被上一次的主动停止误抑制，就再没人对账了。
func TestStoppingMarkerIsTakeStyle(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	m.noteStopping("t1")
	if !m.takeStopping("t1") {
		t.Fatalf("首次取走应为 true")
	}
	if m.takeStopping("t1") {
		t.Fatalf("标记必须取走即失效，第二次应为 false")
	}
}

// reapAdapter 是实现 reaper 的测试 adapter：Stop 一律返 ErrTaskNotRunning
// （模拟 agentd 重启后内存运行态已丢），Reap 的结果可注入。
type reapAdapter struct {
	chanAdapter
	mu       sync.Mutex
	reapErr  error
	reapHits int
}

func (a *reapAdapter) Stop(taskID string) error {
	return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
}

func (a *reapAdapter) Reap(taskID, taskDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reapHits++
	return a.reapErr
}

// stopErrAdapter 是 Stop 错误可注入的测试 adapter。
// 刻意**不实现** reaper：本组两条用例走的是「Stop 失败且非 ErrTaskNotRunning」
// 这条分支，兜底回收压根不该被触及。
type stopErrAdapter struct {
	chanAdapter
	stopErr error
}

func (a *stopErrAdapter) Stop(string) error { return a.stopErr }

// TestStopExecutorFallsBackToReap Stop 报 ErrTaskNotRunning 时必须走兜底回收。
// B20 现场：不兜底，孤儿执行者进程存活了 11.5 小时。
func TestStopExecutorFallsBackToReap(t *testing.T) {
	ad := &reapAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor("t1", ad)

	ad.mu.Lock()
	hits := ad.reapHits
	ad.mu.Unlock()
	if hits != 1 {
		t.Fatalf("Reap 应被调用 1 次，实际 %d", hits)
	}
}

// TestStopExecutorEmitsEventWhenReapFails 信号对称：回收不掉必须留事件。
// worktree 清理失败会发 progress 提示人工，executor 停不掉却完全静默——
// 协调者根本无从知道有残留（B20 的第二个可改点）。
func TestStopExecutorEmitsEventWhenReapFails(t *testing.T) {
	ad := &reapAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		reapErr: errors.New("进程组回收失败")}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "abcdef12-3456-7890-abcd-ef1234567890",
		RepoPath: "/r", Executor: "fake", State: proto.TaskStateWaitingReview})
	m.stopExecutor("abcdef12-3456-7890-abcd-ef1234567890", ad)

	evs, err := st.EventsFromAsc("abcdef12-3456-7890-abcd-ef1234567890", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "handoff stop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("回收失败应产出指向 handoff stop 的 progress 事件，实际事件: %v", evs)
	}
}

// TestStopExecutorNotifiesOnStillAlive 验证 Stop 报「进程仍存活」时，协调者
// 能在事件流里看到人工提示——B47 的全部意义就在这一条。改动前这里只有一行
// Error 日志进 agentd.log，协调者的终端上什么都不会出现。
func TestStopExecutorNotifiesOnStillAlive(t *testing.T) {
	ad := &stopErrAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		stopErr:     fmt.Errorf("kill codex: %w", prochost.ErrStillAlive),
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	const taskID = "abcdef12-3456-7890-abcd-ef1234567890"
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor(taskID, ad)

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "handoff stop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("进程仍存活应产出指向 handoff stop 的 progress 事件，实际事件: %v", evs)
	}
}

// TestStopExecutorStaysQuietOnOtherErrors 验证其它 Stop 失败**不**发事件：
// 全发等于把协调者淹了，那样这条提示就没人看了。
func TestStopExecutorStaysQuietOnOtherErrors(t *testing.T) {
	ad := &stopErrAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		stopErr:     errors.New("上下文已取消"),
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	const taskID = "abcdef12-3456-7890-abcd-ef1234567891"
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor(taskID, ad)

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress {
			t.Fatalf("非 ErrStillAlive 的失败不应发提示事件，got %s", string(e.Payload))
		}
	}
}

// ladderAdapter 是恢复阶梯的测试 adapter：首次 Send 返 ErrTaskNotRunning，
// Resume 之后的 Send 成功。Resume 的返回值可注入。
type ladderAdapter struct {
	chanAdapter
	mu       sync.Mutex
	resumed  bool
	gotReq   executor.ResumeReq
	outcome  executor.ResumeOutcome
	sendHits int
}

func (a *ladderAdapter) Send(ctx context.Context, taskID, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sendHits++
	if !a.resumed {
		return fmt.Errorf("任务 %s: %w", taskID, executor.ErrTaskNotRunning)
	}
	return nil
}

func (a *ladderAdapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gotReq = req
	if a.outcome.Alive {
		a.resumed = true
	}
	return a.outcome, nil
}

// TestContinueColdResumesAndRetriesSend Send 撞 ErrTaskNotRunning 时走恢复阶梯：
// Cold=true 冷恢复 → 重试 Send 一次 → 任务留在 running。
// 这是 B24「waiting_review 任务成孤儿、只能新开任务」的出口。
func TestContinueColdResumesAndRetriesSend(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold,
			SessionID: "sess-1", Note: "已从磁盘载入原会话"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

	if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
		t.Fatalf("冷恢复成功后 continue 应成功: %v", err)
	}
	ad.mu.Lock()
	req, hits := ad.gotReq, ad.sendHits
	ad.mu.Unlock()
	if !req.Cold {
		t.Fatalf("continue 触发的恢复必须 Cold=true（按需冷恢复，spec §4）")
	}
	if hits != 2 {
		t.Fatalf("Send 应被重试恰好一次（共 2 次），实际 %d", hits)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("续接成功后应留在 running，实际 %s", cur.State)
	}
}

// TestContinueColdResumeUsesPersistedCarrierHome 续接冷恢复必须沿用任务载体 HOME。
func TestContinueColdResumeUsesPersistedCarrierHome(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold, SessionID: "sess-1"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "carrier-continue", RepoPath: "/r", Executor: "fake",
		HomeDir: "/carrier/home", State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

	if err := m.Continue(context.Background(), "carrier-continue", "继续"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	ad.mu.Lock()
	got := ad.gotReq
	ad.mu.Unlock()
	if len(got.Env) != 1 || got.Env[0] != "HOME=/carrier/home" {
		t.Fatalf("续接冷恢复 Env 缺少精确载体 HOME: %v", got.Env)
	}
}

// TestContinueColdResumeEmitsProgressEvent 冷恢复/降级必须产出事件而不只是日志。
// fresh 尤其重要：上下文断了是协调者需要知道的事实——它直接决定下一条指令
// 要不要重述背景。只写日志等于让协调者在不知情的前提下继续对话。
func TestContinueColdResumeEmitsProgressEvent(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeFresh,
			SessionID: "sess-new"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-old"})

	if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetTask("t1")
	if cur.ExecutorSession != "sess-new" {
		t.Fatalf("fresh 的新会话 id 必须落库，实际 %q", cur.ExecutorSession)
	}
	evs, _ := st.EventsFromAsc("t1", 0, 100)
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "sess-new") {
			found = true
		}
	}
	if !found {
		t.Fatalf("降级新会话必须产出带新会话 id 的 progress 事件，实际: %v", evs)
	}
}

// TestContinueUnrecoverableFallsBackToReview 阶梯全走完仍不可恢复：
// 回迁 waiting_review（不让任务死在 running），错误里带 Note 说明原因。
func TestContinueUnrecoverableFallsBackToReview(t *testing.T) {
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: false, Note: "会话数据已不在磁盘上"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

	err := m.Continue(context.Background(), "t1", "继续干")
	if err == nil {
		t.Fatalf("不可恢复应返回错误")
	}
	if !strings.Contains(err.Error(), "会话数据已不在磁盘上") {
		t.Fatalf("错误应带 Outcome.Note 让协调者知道为什么: %v", err)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("不可恢复应回迁 waiting_review，实际 %s", cur.State)
	}
}

// TestReconcileExecutorGoneSweepsUnconditionally 验证清扫是无条件后置动作：
// 即使任务状态命中提前返回分支（非 running/waiting_answer），也必须清扫。
//
// 这条守的是事故现场的形态：2026-08-12 两个任务最终都停在 waiting_review，
// 而那正是提前返回会跳过的状态。清扫若跟着提前返回一起被跳过，这个功能
// 在它最该工作的场景里恰好不工作。
func TestReconcileExecutorGoneSweepsUnconditionally(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateWaitingReview})

	swept := 0
	reconcileExecutorGone(st, NewHub(), "t1", "测试", quietLog(), func(string) { swept++ })

	if swept != 1 {
		t.Fatalf("提前返回分支也必须清扫一次，实际 %d 次", swept)
	}
	got, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("读任务失败: %v", err)
	}
	if got.State != proto.TaskStateWaitingReview {
		t.Fatalf("清扫不得改变状态，got %s", got.State)
	}
}

// TestReconcileExecutorGoneSweepsAfterTransit 正常路径：先迁状态、再清扫。
func TestReconcileExecutorGoneSweepsAfterTransit(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})

	var stateAtSweep proto.TaskState
	reconcileExecutorGone(st, NewHub(), "t1", "测试", quietLog(), func(taskID string) {
		cur, _ := st.GetTask(taskID)
		stateAtSweep = cur.State
	})

	if stateAtSweep != proto.TaskStateWaitingReview {
		t.Fatalf("清扫必须发生在状态迁移之后，清扫时状态为 %s", stateAtSweep)
	}
}

// TestReconcileExecutorGoneEmitsTurnFailed 钉死 B100 补漏：对账路径落的必须是
// turn_failed 而不是 failed。
//
// 为什么：本函数迁的是 **waiting_review**（recoverTransit），任务**没有终结**——
// executor 死了但代码还在，值得让协调者 diff 完再决定 continue 还是 done。
// 落 failed 会让 wait --follow 收流、打「任务已终结」并以 0 退出，把一个正等着
// 裁决的任务报成死的。B100 首轮漏了这条：审核者 spec §1.1 的四生产者表把这一行
// 误填成「任务落 failed」，没去看 recoverTransit 的实际迁移目标。
func TestReconcileExecutorGoneEmitsTurnFailed(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})

	got := reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog(), func(string) {})
	if got != proto.TaskStateWaitingReview {
		t.Fatalf("对账应收 waiting_review，实际 %s", got)
	}
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var last proto.Event
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed || e.Type == proto.EventTypeTurnFailed {
			last = e
		}
	}
	if last.Type == proto.EventTypeFailed {
		t.Fatal("对账落了 failed：任务此刻在 waiting_review，没有终结，follow 会据此假报任务已死")
	}
	if last.Type != proto.EventTypeTurnFailed {
		t.Fatalf("对账应落 turn_failed，实际 %q", last.Type)
	}
}
