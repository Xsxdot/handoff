// 运行态对账的白盒测试：executor 已不在这一事实的唯一收尾实现。
package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
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
			got := reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog())
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
				if e.Type == proto.EventTypeFailed {
					hasFailed = true
				}
			}
			if hasFailed != c.wantEvent {
				t.Fatalf("failed 事件 = %v，期望 %v", hasFailed, c.wantEvent)
			}
		})
	}
}

// TestReconcileExecutorGoneIdempotent 幂等：三个到达口可能对同一任务先后触发，
// 第二次必须是空操作，不产重复事件。
func TestReconcileExecutorGoneIdempotent(t *testing.T) {
	st := newTestStore(t)
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", State: proto.TaskStateRunning})
	reconcileExecutorGone(st, NewHub(), "t1", "第一次", quietLog())
	reconcileExecutorGone(st, NewHub(), "t1", "第二次", quietLog())
	evs, err := st.EventsFromAsc("t1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("failed 事件应只有 1 条（幂等），实际 %d", n)
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
	reconcileExecutorGone(st, NewHub(), "t1", "测试来源", quietLog())
	pend, err := st.PendingTickets("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("挂起工单应被作废，实际剩 %d", len(pend))
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
		if e.Type == proto.EventTypeFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("应产出 failed 事件说明 executor 终结")
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
