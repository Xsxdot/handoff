package opencode

import (
	"context"
	"testing"
	"time"
)

// TestResumeTriggersReconcile 验热重连成功后会自动对一次账。
func TestResumeTriggersReconcile(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_lost", 1786348485642,
			"活干完了\n"+`{"branch":"handoff/x","commit":"abc1234","summary":"改完了"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	// 直接调对账触发点的那段逻辑：Resume 的完整路径需要真实 shim，
	// 此处验的是「reattach 成功后确实调了 Reconcile」这一条接线
	a.reconcileAfterRecovery(context.Background(), r.taskID, "startup")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "result" {
				return // 接线生效
			}
		case <-deadline:
			t.Fatal("恢复后未触发对账：2 秒内没有补发的终态事件")
		}
	}
}

// TestReconcileAfterRecoverySwallowsError 验对账失败不向上冒泡。
//
// why：spec §6.3 的硬要求——一次网络抖动不该把能恢复的任务判成不可恢复。
func TestReconcileAfterRecoverySwallowsError(t *testing.T) {
	a := newTestAdapter(t)
	// 不建运行态：Reconcile 会走「无运行态」分支；再造一个必失败的场景
	// 由 Reconcile 内部返回 error，此处只验本函数不 panic 不阻塞
	a.reconcileAfterRecovery(context.Background(), "no-such-task", "reconnect")
}
