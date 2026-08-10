package agentd

import (
	"context"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// fakeReconciler 是一个可编程的对账 adapter 桩。
type fakeReconciler struct {
	executor.Adapter
	out    executor.ReconcileOutcome
	err    error
	called int
}

func (f *fakeReconciler) Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error) {
	f.called++
	return f.out, f.err
}

// noReconcileAdapter 是一个只实现 executor.Adapter 五动作、不实现 Reconcile 的空桩。
type noReconcileAdapter struct {
	executor.Adapter
}

// newTestManagerWithReconciler 组装一个把 given 注册为缺省执行者、任务已在
// wantState 的 manager 测试环境，返回 manager 与任务 id。
func newTestManagerWithReconciler(t *testing.T, ad executor.Adapter, wantState proto.TaskState) (*Manager, string) {
	t.Helper()
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	taskID := "task-rec-0001"
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake", State: wantState})
	return m, taskID
}

// containsAll 判 text 是否同时包含全部子串。
func containsAll(text string, subs ...string) bool {
	for _, s := range subs {
		if !strings.Contains(text, s) {
			return false
		}
	}
	return true
}

// TestRecoverStuckFallsThroughToReconcile 验「没有未送达应答」时转入对账，
// 而不是像从前一样直接回「无需恢复」。
func TestRecoverStuckFallsThroughToReconcile(t *testing.T) {
	fr := &fakeReconciler{out: executor.ReconcileOutcome{
		TurnEnded: true, Emitted: 1, Note: "补回了一条断连期间丢失的完成结果"}}
	m, taskID := newTestManagerWithReconciler(t, fr, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if fr.called != 1 {
		t.Fatalf("应调用一次对账，got %d", fr.called)
	}
	if !rep.Reconciled || rep.Emitted != 1 {
		t.Fatalf("报告应体现对账结果，got %+v", rep)
	}
	if rep.Note == "没有卡在半路的应答，无需恢复" {
		t.Fatal("不应再回旧文案——那正是 B38 里审核者撞上的死路")
	}
}

// TestRecoverStuckUnsupportedAdapterIsHonest 验 adapter 未实现对账时如实说明，
// 不伪装成「对账过了」。
func TestRecoverStuckUnsupportedAdapterIsHonest(t *testing.T) {
	m, taskID := newTestManagerWithReconciler(t, &noReconcileAdapter{}, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if rep.Reconciled {
		t.Fatal("adapter 不支持对账时 Reconciled 必须为 false")
	}
	if rep.State != proto.TaskStateRunning {
		t.Fatalf("不支持对账时不应改状态，got %s", rep.State)
	}
}

// TestRecoverStuckForceTransitsToReview 验 --force 在对账判「仍在忙」时
// 仍把任务收口到 waiting_review，并留下人工强制的事件。
func TestRecoverStuckForceTransitsToReview(t *testing.T) {
	fr := &fakeReconciler{out: executor.ReconcileOutcome{
		TurnEnded: false, Note: "executor 的回合仍在进行中，没有丢失的终态"}}
	m, taskID := newTestManagerWithReconciler(t, fr, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, true)
	if err != nil {
		t.Fatalf("强制收口失败: %v", err)
	}
	if !rep.Forced {
		t.Fatal("报告应标明是人工强制收口")
	}
	if rep.State != proto.TaskStateWaitingReview {
		t.Fatalf("强制收口后应落 waiting_review，got %s", rep.State)
	}
	evs, err := m.st.EventsFrom(taskID, 0, 100)
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}
	found := false
	for _, e := range evs {
		if e.Type != proto.EventTypeProgress {
			continue
		}
		if containsAll(string(e.Payload), "人工强制收口", "未经 executor 确认") {
			found = true
		}
	}
	if !found {
		t.Fatal("强制收口必须留下写明「人工强制、未经 executor 确认」的事件")
	}
}
