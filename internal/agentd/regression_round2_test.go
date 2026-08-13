// 本文件是第二轮外部代码审阅（docs/superpowers/reviews/2026-08-08-mvp-code-review-round2.md）
// 确认缺陷的回归测试（agentd 中介层部分）。
//
// 职责：
//   - 锁定 U-1（工单可见前状态必须先落 waiting_answer）、U-3（executor 死亡时
//     挂起工单作废）、N-4（工单存在但通知事件缺失时必须自愈补发）三项修复
//
// 边界：
//   - 白盒测试（package agentd）：直接驱动 manager/server 的内部方法复现时序窗口，
//     不经 HTTP——被测对象是中介顺序本身，不是路由
//   - WS 相关回归（N-1/N-2/N-3）在 server_test.go，不在本文件
package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// replyLikeServer 复刻 Server.handleReply 的回程步骤（落库应答 → 唤醒等待者 →
// 无等待者则自愈中继 → 空闲则回迁 running），供时序窗口的竞态复现使用。
func replyLikeServer(srv *Server, mgr *Manager, taskID, ticketID, answer string) {
	if err := srv.st.AnswerTicket(ticketID, answer); err != nil {
		return
	}
	if !srv.hub.NotifyAnswer(ticketID, answer) {
		_ = mgr.RelayAnswer(taskID, ticketID, answer)
	}
	srv.resumeIfIdle(taskID)
}

// newTestServerWithManager 组装白盒 server+manager（共用一个 store 与 hub）。
func newTestServerWithManager(t *testing.T) (*Server, *Manager, *store.Store) {
	t.Helper()
	mgr, st, hub, _ := newTestManager(t)
	cfg := &config.Config{Token: "test", DataDir: t.TempDir()}
	srv := &Server{cfg: cfg, st: st, hub: hub, log: mgr.log, mgr: mgr}
	return srv, mgr, st
}

// TestPermissionStateVisibleBeforeTicket 验证权限中介的顺序契约（U-1）：
// 工单对协调者可见时，任务状态必须已经是 waiting_answer。
//
// 修复前顺序是 CreateTicket → AppendEvent → transit(waiting_answer)，中间隔着
// SQLite 往返。协调者经 spec §7 的恢复流程（attach 读 pending_tickets → reply）
// 恰好落在这个窗口时：应答被中继、executor 恢复执行、resumeIfIdle 看到 running
// 直接返回，而随后 manager 才把 waiting_answer 盖上去——任务显示「等你回答」
// 却零挂起工单，reply→404 / continue→409 / done→409，无任何恢复路径。
//
// 修复后 transit 先于 CreateTicket：工单不可能早于状态出现，反向窗口不存在。
func TestPermissionStateVisibleBeforeTicket(t *testing.T) {
	srv, mgr, st := newTestServerWithManager(t)
	const rounds = 200
	stuck := 0
	for i := range rounds {
		taskID := fmt.Sprintf("task-%03d", i)
		createRunningTask(t, st, taskID)
		permID := fmt.Sprintf("perm-%03d", i)
		ticketID := taskID + ":" + permID

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			mgr.handlePermission(context.Background(), taskID, executor.AdapterEvent{
				Type: "permission", PermissionID: permID, Text: "bash: go test ./...",
			})
		}()
		go func() {
			defer wg.Done()
			// 模拟协调者「一看到挂起工单就回答」：紧贴工单可见的瞬间进入回程
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if pending, err := st.PendingTickets(taskID); err == nil && len(pending) > 0 {
					replyLikeServer(srv, mgr, taskID, ticketID, "allow")
					return
				}
			}
		}()
		wg.Wait()

		task, err := st.GetTask(taskID)
		if err != nil {
			t.Fatalf("读取任务 %s: %v", taskID, err)
		}
		pending, err := st.PendingTickets(taskID)
		if err != nil {
			t.Fatalf("读取挂起工单 %s: %v", taskID, err)
		}
		if task.State == proto.TaskStateWaitingAnswer && len(pending) == 0 {
			stuck++
		}
	}
	if stuck > 0 {
		t.Errorf("%d/%d 轮任务卡在 waiting_answer 且零挂起工单（reply/continue/done 全不可用的死路）",
			stuck, rounds)
	}
}

// TestDeadExecutorVoidsPendingTickets 验证 executor 进程内死亡时挂起工单一并作废
// （U-3）。
//
// 修复前只有 agentd 重启路径（RecoverOnStartup）作废工单，进程内死亡
// （subscribeLoop 产出 !OK result → handleResult）不作废：attach 仍向协调者
// 展示可操作的挂起项，而 executor 已经不在——一旦 reply，工单被消耗、中继失败
// 返回 502，任务进入不可恢复状态。
func TestDeadExecutorVoidsPendingTickets(t *testing.T) {
	mgr, st, _, _ := newTestManager(t)
	const taskID = "task-dead"
	createRunningTask(t, st, taskID)

	ticketID := taskID + ":perm-1"
	req, _ := json.Marshal(map[string]string{"kind": "gate", "permission": "bash: rm -rf build"})
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate", Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	mgr.handleResult(taskID, executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "opencode serve 已退出",
	}})

	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateWaitingReview {
		t.Fatalf("失败结果应进 waiting_review，实际 %s", task.State)
	}
	pending, err := st.PendingTickets(taskID)
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("executor 已死，attach 不应再展示可操作挂起工单，实际还有 %d 条", len(pending))
	}
}

// TestPermissionSelfHealsWhenEventMissing 验证「工单已存在但通知事件缺失」时
// 中介必须自愈补发（N-4）。
//
// 场景：agentd 在 CreateTicket 成功、AppendEvent 之前崩溃（或 AppendEvent 失败），
// 留下一个有工单无事件的状态。此后 SSE 重放同一权限时，修复前仅凭 created==false
// 就整体跳过——permission_request 事件永不产生、状态停在 running、无等待者，
// 协调者的 wait 永不触发。修复前的基线版本在这里是能自愈的，这是修复引入的退化。
func TestPermissionSelfHealsWhenEventMissing(t *testing.T) {
	mgr, st, _, _ := newTestManager(t)
	const taskID = "task-orphan-ticket"
	createRunningTask(t, st, taskID)

	permID := "perm-orphan"
	ticketID := taskID + ":" + permID
	req, _ := json.Marshal(map[string]string{"kind": "gate", "permission": "bash: ls"})
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "gate", Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("预置孤儿工单: %v", err)
	}

	mgr.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: permID, Text: "bash: ls",
	})

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	found := false
	for _, ev := range evs {
		if ev.Type == proto.EventTypePermissionRequest {
			found = true
		}
	}
	if !found {
		t.Error("工单存在但通知事件缺失时未自愈补发 permission_request——协调者的 wait 永不触发")
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateWaitingAnswer {
		t.Errorf("自愈后应置 waiting_answer，实际 %s", task.State)
	}
}

// TestPermissionReplayStillSkipped 验证正常重放（工单与事件都在）仍然被跳过，
// 即 N-4 的自愈不得把 P1-7 的幂等改回重复唤醒。
func TestPermissionReplayStillSkipped(t *testing.T) {
	mgr, st, _, _ := newTestManager(t)
	const taskID = "task-replay"
	createRunningTask(t, st, taskID)
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "bash: ls"}

	mgr.handlePermission(context.Background(), taskID, ev)
	mgr.handlePermission(context.Background(), taskID, ev)
	mgr.handlePermission(context.Background(), taskID, ev)

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionRequest {
			n++
		}
	}
	if n != 1 {
		t.Errorf("同一权限重放 3 次应只产出 1 条 permission_request，实际 %d 条", n)
	}
}
