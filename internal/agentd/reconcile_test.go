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
