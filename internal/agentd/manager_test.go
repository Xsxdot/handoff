// manager 状态机兜底路径的白盒回归测试。
//
// 背景：handleResult 的 transitToReview 存在一个残留竞态——result 事件在回答待决
// （waiting_answer）期间到达时，首跳必然失败；若应答 goroutine 恰在「首跳失败后、
// 重读前」抢先回迁 running，旧实现会直接报错，把已追加落库的 result 事件连同
// Publish 一起丢弃，任务卡死在 running 直到看门狗。本文件覆盖修复后的两条兜底路径：
//   - TestTransitToReviewResidualRace：确定性复现「重读见 running → 重试补跳」分支
//   - TestTransitToReviewAnswerRaceConverges：并发全流程（result 与应答同时注入），
//     断言结果事件绝不因该竞态被吞（修复后恒绿；旧实现下该用例必现红）
//   - TestTransitToReviewTwoHopFromWaitingAnswer：确定性防御性两跳 + 应答后到不丢
//
// 测试为白盒（package agentd）：直接驱动 manager 内部方法，绕开 fake 的阻塞语义
// （fake 的 Finish 步骤必须等 Send/RespondPermission 后才执行，无法在回答挂起时
// 产出 result，故 reviewer 指出的竞态在集成测试层面不可达）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// chanAdapter 是测试用空操作 adapter：事件通道由测试直接控制（模拟 executor 侧事件流），
// 并记录 RespondPermission/Send 实参供断言（答案侧是否真正回传 executor）。
type chanAdapter struct {
	mu    sync.Mutex
	evCh  chan executor.AdapterEvent
	perms []string
	sends []string
	// respondErr 非 nil 时 RespondPermission/Send 直接返回它（模拟 executor
	// 已退出或调用失败），供恢复操作的失败分支断言
	respondErr error
}

// setRespondErr 设置（或用 nil 清除）RespondPermission/Send 的注入错误。
func (a *chanAdapter) setRespondErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.respondErr = err
}

func (a *chanAdapter) Start(context.Context, executor.StartReq) error { return nil }
func (a *chanAdapter) Events(string) <-chan executor.AdapterEvent     { return a.evCh }

func (a *chanAdapter) Send(_ context.Context, _ string, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.sends = append(a.sends, text)
	return nil
}

func (a *chanAdapter) RespondPermission(_ context.Context, _ string, permID, decision string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.perms = append(a.perms, permID+":"+decision)
	return nil
}

func (a *chanAdapter) Stop(string) error { return nil }

// permsRec 返回已记录的 RespondPermission 实参（副本）。
func (a *chanAdapter) permsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.perms...)
}

// sendsRec 返回已记录的 Send 实参（副本）。
func (a *chanAdapter) sendsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.sends...)
}

// newTestManager 组装 manager 白盒测试环境：真实 store + hub + 可控事件通道 adapter。
func newTestManager(t *testing.T) (*Manager, *store.Store, *Hub, *chanAdapter) {
	t.Helper()
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, hub := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	return m, st, hub, ad
}

// newTestManagerWithAds 组装带 adapter 注册表的 manager 白盒测试环境：
// 真实 store + hub + 给定注册表（defaultName 为缺省执行者名，写进 cfg.Executor.Default）。
func newTestManagerWithAds(t *testing.T, ads map[string]executor.Adapter, defaultName string) (*Manager, *store.Store, *Hub) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: defaultName}}
	return NewManager(st, hub, ads, cfg, logger), st, hub
}

// mustCreateTask 直接落库一个任务（绕过 Dispatch 的工作区准备），供路由类测试造数据。
func mustCreateTask(t *testing.T, st *store.Store, task *proto.Task) {
	t.Helper()
	if err := st.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// TestAdapterForRoutesByTaskExecutor 验证 adapterFor 按 task.Executor 路由：
// 显式 executor 命中注册表对应 adapter；executor 为空回退缺省执行者（老任务兼容）。
func TestAdapterForRoutesByTaskExecutor(t *testing.T) {
	adA, adB := fake.New(nil), fake.New(nil)
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"a": adA, "b": adB}, "a")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "b", State: proto.TaskStateRunning})
	mustCreateTask(t, st, &proto.Task{ID: "t2", RepoPath: "/r", Executor: "", State: proto.TaskStateRunning})
	if got, _ := m.adapterFor("t1"); got != adB {
		t.Fatalf("t1 应路由到 b")
	}
	if got, _ := m.adapterFor("t2"); got != adA {
		t.Fatalf("executor 为空应回退缺省 a")
	}
}

// TestResolveExecutorRejectsUnknown 验证 dispatch 期未注册执行者被拒，错误列出可用项。
func TestResolveExecutorRejectsUnknown(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"a": fake.New(nil)}, "a")
	if _, _, err := m.resolveExecutor("nope"); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("未注册执行者应报错并列出可用项: %v", err)
	}
}

// createRunningTask 创建任务并迁移到 running（handlePermission 需要 running→waiting_answer 合法）。
func createRunningTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: id, Target: "local", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateRunning); err != nil {
		t.Fatalf("置为 running: %v", err)
	}
}

// waitAnswerRegistered 等待 waitPermission goroutine 完成 WaitAnswer 注册（hub.answers 里有等待者），
// 保证后续 NotifyAnswer 不会因注册未完成而被丢弃。
func waitAnswerRegistered(t *testing.T, hub *Hub, ticketID string) {
	t.Helper()
	eventually(t, 2*time.Second, "waitPermission 已注册到 hub", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.answers[ticketID]) > 0
	})
}

// resultEvent 构造一个 OK 的 result 事件（断言用 branch/commit）。
func resultEvent() executor.AdapterEvent {
	return executor.AdapterEvent{Type: "result", Result: &executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123"}}
}

// eventually 轮询断言：cond 在 timeout 内变为 true 才算通过（与 integration_test 同款）。
func eventually(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// TestExecutorSessionPersisted 断言 session id 最终出现在 task.ExecutorSession
// （store.GetTask 读回）——brief「session id 经 manager 写入 task.ExecutorSession」
// 的闭环验证：
//   - progress「会话就绪」信号：question 收尾主路径的唯一落库通道
//   - result 携带 SessionID：双保险通道（progress 乱序/丢失时兜底）
//   - 空 SessionID 不落库：向后兼容（老 adapter 事件不误写）
func TestExecutorSessionPersisted(t *testing.T) {
	t.Run("progress_signal", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		mgr.handleProgress("t1", executor.AdapterEvent{Type: "progress", SessionID: "sess-1", Text: "会话就绪"})
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "sess-1" {
			t.Fatalf("ExecutorSession=%q，期望 sess-1（会话就绪信号落库）", cur.ExecutorSession)
		}
	})
	t.Run("result_double_insurance", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		ev := resultEvent()
		ev.Result.SessionID = "sess-2"
		mgr.handleResult("t1", ev)
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "sess-2" {
			t.Fatalf("ExecutorSession=%q，期望 sess-2（result 双保险落库）", cur.ExecutorSession)
		}
	})
	t.Run("empty_session_ignored", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		mgr.handleProgress("t1", executor.AdapterEvent{Type: "progress", Text: "心跳"})
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "" {
			t.Fatalf("空 SessionID 不应落库，实际=%q", cur.ExecutorSession)
		}
	})
}

// TestTransitToReviewResidualRace 确定性覆盖修复的核心分支：首跳（waiting_answer→
// waiting_review 非法）失败后、重读前应答 goroutine 已把状态回迁 running——
// 重读兜底必须重试补跳 waiting_review，而不是报错吞掉已追加的 result 事件。
//
// 对照旧实现：旧 transitToReview 在重读见 running（非 waiting_answer）时直接返回
// "任务不在 waiting_answer" 错误，本测试的 `transitToReviewRetry` 调用点即不复存在
// （编译期红）；旧行为的运行期错误路径由 TestTransitToReviewAnswerRaceConverges 实证。
func TestTransitToReviewResidualRace(t *testing.T) {
	mgr, st, _, _ := newTestManager(t)
	createRunningTask(t, st, "t1")
	// 任务挂起在回答待决——handleResult 首跳失败的必经状态
	if err := st.UpdateTaskState("t1", proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("置为 waiting_answer: %v", err)
	}
	// 竞态窗口第一步：与 handleResult 的首次尝试等价——waiting_answer→waiting_review 非法必被拒
	if err := mgr.transit("t1", proto.TaskStateWaitingReview, "test-首跳"); !errors.Is(err, store.ErrBadTransit) {
		t.Fatalf("首跳应被非法迁移拒绝, got %v", err)
	}
	// 竞态窗口第二步：应答 goroutine 在首跳失败后、重读前抢先回迁 running
	if err := mgr.transit("t1", proto.TaskStateRunning, "test-应答回迁"); err != nil {
		t.Fatalf("模拟应答回迁: %v", err)
	}
	// 重读兜底：见 running 必须重试补跳而非报错（旧实现此处报错并吞事件）
	if err := mgr.transitToReviewRetry("t1"); err != nil {
		t.Fatalf("重读见 running 应重试补跳 waiting_review, got %v", err)
	}
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("最终 state=%s, want waiting_review", cur.State)
	}
}

// TestTransitToReviewAnswerRaceConverges 并发全流程回归：任务挂起在 waiting_answer 时
// result 事件与应答同时注入（经真实事件通道与真实 waitPermission goroutine），断言
// 结果事件必然被 Publish（不被竞态吞掉）且只追加一次。
//
// 修复后恒绿：无论应答回迁与 result 中介谁先落地，transitToReviewRetry 都能收敛到
// waiting_review 并 Publish。旧实现下，只要应答 CAS 落入「首跳失败→重读」窗口，
// 事件即被吞、任务卡死 running——本用例以高概率必现（多轮迭代放大命中率）。
func TestTransitToReviewAnswerRaceConverges(t *testing.T) {
	const rounds = 150
	for i := 0; i < rounds; i++ {
		taskID := "t1"
		mgr, st, hub, ad := newTestManager(t)
		createRunningTask(t, st, taskID)
		// 真实权限门路径：ticket + waiting_answer + waitPermission goroutine 挂起等应答
		mgr.handlePermission(context.Background(), taskID, executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"})
		waitAnswerRegistered(t, hub, "t1:perm-1")
		// 订阅 Publish 通道，断言 completed 事件确实被广播（不被竞态吞掉）
		subCh, _ := hub.Subscribe(taskID)

		// result 经真实事件通道进入中介循环，应答随即注入——两者并发竞争同一 CAS
		go mgr.mediate(taskID)
		ad.evCh <- resultEvent()
		// 略延迟应答注入：让 result 处理先行进入「首跳失败→重读」危险窗口，
		// 提高旧实现下竞态命中率；新实现无论先后都收敛，延迟不影响结论
		time.Sleep(100 * time.Microsecond)
		hub.NotifyAnswer("t1:perm-1", "allow")

		// 收敛断言 1：completed 事件必须被 Publish（旧实现竞态命中时此处超时红）
		eventually(t, 2*time.Second, "completed 事件已 Publish", func() bool {
			select {
			case ev := <-subCh:
				return ev.Type == proto.EventTypeCompleted
			default:
				return false
			}
		})
		// 收敛断言 2：答案侧完成（executor 收到 RespondPermission），所有状态迁移落定
		eventually(t, 2*time.Second, "executor 收到 RespondPermission", func() bool {
			return len(ad.permsRec()) == 1
		})
		// 收敛断言 3：恰好一条 completed 事件落库（不重复追加）
		events, err := st.EventsFrom(taskID, 0, 100)
		if err != nil {
			t.Fatalf("EventsFrom: %v", err)
		}
		completed := 0
		for _, e := range events {
			if e.Type == proto.EventTypeCompleted {
				completed++
			}
		}
		if completed != 1 {
			t.Fatalf("第 %d 轮 completed 事件数=%d, want 1", i, completed)
		}
		// 收敛断言 4：终态为 waiting_review（事件已交割给审核者）或 running（应答后到，
		// executor 被重新唤醒续跑，属正常语义而非卡死）；绝不静默停在等待者已应答的空转态
		cur, err := st.GetTask(taskID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.State != proto.TaskStateWaitingReview && cur.State != proto.TaskStateRunning {
			t.Fatalf("第 %d 轮最终 state=%s, want waiting_review 或 running", i, cur.State)
		}
		// 清理：关闭事件通道，中介 goroutine 随 range 退出，不泄漏
		close(ad.evCh)
	}
}

// TestTransitToReviewTwoHopFromWaitingAnswer 确定性覆盖防御性两跳：result 在回答挂起
// 期间到达（应答后到），handleResult 必须经 running 两跳进入 waiting_review 并 Publish；
// 应答随后到达时 waitPermission 合法回迁 running（executor 被唤醒续跑），事件不丢失。
func TestTransitToReviewTwoHopFromWaitingAnswer(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	mgr.handlePermission(context.Background(), "t1", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"})
	waitAnswerRegistered(t, hub, "t1:perm-1")
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("handlePermission 后 state=%s, want waiting_answer", cur.State)
	}
	// result 同步处理（无并发干扰，纯两跳路径）：append → 首跳失败 → 重读见
	// waiting_answer → 经 running 两跳 → waiting_review → Publish
	mgr.handleResult("t1", resultEvent())
	cur, err = st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("两跳后 state=%s, want waiting_review", cur.State)
	}
	events, err := st.EventsFrom("t1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	completed := 0
	for _, e := range events {
		if e.Type == proto.EventTypeCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completed 事件数=%d, want 1", completed)
	}
	// 应答后到：waitPermission 回迁 running（waiting_review→running 合法），
	// executor 收到 RespondPermission——续跑语义成立，事件不丢
	hub.NotifyAnswer("t1:perm-1", "allow")
	eventually(t, 2*time.Second, "executor 收到 RespondPermission 且任务回迁 running", func() bool {
		if len(ad.permsRec()) != 1 {
			return false
		}
		c, err := st.GetTask("t1")
		return err == nil && c.State == proto.TaskStateRunning
	})
}

// TestRelayAnswer 验证 reply 无等待者时的自愈中继（agentd 重启后等待 goroutine 已
// 消亡、/event 不重放历史的场景）：RelayAnswer 直接读工单驱动 adapter，
// gate 按 allow→once/其余→reject 翻译（与 waitPermission 同规则），ask 原文透传。
func TestRelayAnswer(t *testing.T) {
	// createTicket 建一张指定 kind 的工单并返回（request 与 manager 的 ticketRequest 同构）。
	createTicket := func(st *store.Store, id, taskID, kind string) {
		t.Helper()
		req := json.RawMessage(`{"kind":"gate","permission":"Bash: rm -rf node_modules"}`)
		if kind == "ask" {
			req = json.RawMessage(`{"kind":"ask","question":"表结构用单数还是复数?"}`)
		}
		if _, err := st.CreateTicket(&proto.Ticket{
			ID: id, TaskID: taskID, Kind: kind, Request: req, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
	}

	t.Run("gate_allow_once", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t1:perm-1", "t1", "gate")
		if err := mgr.RelayAnswer("t1", "t1:perm-1", "allow"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		// adapter 收到的是裸 permID（剥离 taskID 前缀），而非命名空间化的工单 id
		if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-1:once" {
			t.Fatalf("executor 收到 %v, want [perm-1:once]", got)
		}
	})

	t.Run("gate_deny_reject", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t1:perm-2", "t1", "gate")
		if err := mgr.RelayAnswer("t1", "t1:perm-2", "deny:太危险"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-2:reject" {
			t.Fatalf("executor 收到 %v, want [perm-2:reject]", got)
		}
	})

	t.Run("ask_original_send", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "ask-1", "t1", "ask")
		if err := mgr.RelayAnswer("t1", "ask-1", "复数"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		if got := ad.sendsRec(); len(got) != 1 || got[0] != "复数" {
			t.Fatalf("executor 收到 %v, want [复数]（原文透传）", got)
		}
	})

	t.Run("ticket_not_found", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		if err := mgr.RelayAnswer("t1", "ghost", "x"); err == nil {
			t.Fatal("工单不存在应报错")
		}
	})

	t.Run("ticket_of_other_task", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t2:perm-3", "t2", "gate")
		if err := mgr.RelayAnswer("t1", "t2:perm-3", "allow"); err == nil {
			t.Fatal("跨任务工单应报错")
		}
		if got := ad.permsRec(); len(got) != 0 {
			t.Fatalf("跨任务工单不得触达 executor, got %v", got)
		}
	})
}

// TestWaiterCanceledOnTaskEnd 覆盖 P1-2 的 ctx 取消半段：任务执行终结（adapter
// 事件通道关闭）后，挂起的应答等待 goroutine 必须被取消并从 hub 等待表移除——
// 旧实现 waitPermission 用 context.Background() 永久挂死（审核者不再回答即泄漏）。
//
// 为什么事件通道关闭是唯一取消时机：result → waiting_review 后任务仍活，回答
// 晚于 result 到达是合法流程（见 mediate 的 why 注释），只有「执行终结」才
// 取消在等应答的 waiter。
func TestWaiterCanceledOnTaskEnd(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	go mgr.mediate("t1")
	ad.evCh <- executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"}
	waitAnswerRegistered(t, hub, "t1:perm-1")

	// 执行终结：关闭事件通道 → 中介循环退出 → defer cancel → waiter 被取消并移除
	close(ad.evCh)
	eventually(t, 2*time.Second, "waiter 已被取消并从 hub 等待表移除", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.answers["t1:perm-1"]) == 0
	})
}

// TestTicketNamespacePerTask 覆盖 P1-6：ticket id 按任务命名空间隔离
// （taskID:permID）。两个任务收到相同 PermissionID 时，旧实现（裸 permID 作
// ticket id）第二个任务的工单被 INSERT OR IGNORE 静默吞掉——attach 显示 0
// 挂起项且永远无法应答；命名空间化后两个工单都存在且可分别应答。
func TestTicketNamespacePerTask(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	createRunningTask(t, st, "t2")
	mgr.handlePermission(context.Background(), "t1", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "x"})
	mgr.handlePermission(context.Background(), "t2", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "y"})
	waitAnswerRegistered(t, hub, "t1:perm-1")
	waitAnswerRegistered(t, hub, "t2:perm-1")

	// 两个任务的工单都存在，id 均带任务前缀
	p1, err := st.PendingTickets("t1")
	if err != nil {
		t.Fatalf("PendingTickets(t1): %v", err)
	}
	if len(p1) != 1 || p1[0].ID != "t1:perm-1" {
		t.Fatalf("t1 pending=%+v, want [t1:perm-1]", p1)
	}
	p2, err := st.PendingTickets("t2")
	if err != nil {
		t.Fatalf("PendingTickets(t2): %v", err)
	}
	if len(p2) != 1 || p2[0].ID != "t2:perm-1" {
		t.Fatalf("t2 pending=%+v, want [t2:perm-1]（旧实现此处为 0，工单被 t1 吞掉）", p2)
	}

	// 分别可应答（store 层断言：命名空间化后两个工单互不干扰）；
	// 经 hub 唤醒各自 waiter 后，executor 各收到一次裸 permID 的 RespondPermission
	if err := st.AnswerTicket("t1:perm-1", "allow"); err != nil {
		t.Fatalf("AnswerTicket(t1): %v", err)
	}
	if err := st.AnswerTicket("t2:perm-1", "allow"); err != nil {
		t.Fatalf("AnswerTicket(t2): %v", err)
	}
	hub.NotifyAnswer("t1:perm-1", "allow")
	hub.NotifyAnswer("t2:perm-1", "allow")
	eventually(t, 2*time.Second, "两个任务的 executor 各收到一次应答", func() bool {
		return len(ad.permsRec()) == 2
	})
}

// TestPermissionReplaySkipsDuplicates 覆盖 P1-7 的 manager 层：同任务同 permID
// 的事件重放（SSE 断线重连/重启后订阅重建）必须跳过全部中介动作——
// 只有一条 permission_request 事件、一次状态迁移、一个 waiter、一次
// RespondPermission；已答后的重放不注册新 waiter、不重复唤醒。
func TestPermissionReplaySkipsDuplicates(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"}
	mgr.handlePermission(context.Background(), "t1", ev)
	waitAnswerRegistered(t, hub, "t1:perm-1")
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("首次中介后 state=%s, want waiting_answer", cur.State)
	}

	// 重放：created=false → 跳过，不追加事件、不再起 waiter
	mgr.handlePermission(context.Background(), "t1", ev)
	events, err := st.EventsFrom("t1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	permReq := 0
	for _, e := range events {
		if e.Type == proto.EventTypePermissionRequest {
			permReq++
		}
	}
	if permReq != 1 {
		t.Fatalf("permission_request 事件数=%d, want 1（重放不得重复追加）", permReq)
	}
	cur, err = st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("重放后 state=%s, want waiting_answer（不重复迁移）", cur.State)
	}

	// 一次应答 → 恰好一次 RespondPermission（不重复唤醒审核者/executor）
	hub.NotifyAnswer("t1:perm-1", "allow")
	eventually(t, 2*time.Second, "executor 收到恰一次应答", func() bool {
		return len(ad.permsRec()) == 1
	})

	// 已答后重放：不注册新 waiter（NotifyAnswer 应无等待者可投递）、
	// 不出现第二次 RespondPermission
	mgr.handlePermission(context.Background(), "t1", ev)
	if hub.NotifyAnswer("t1:perm-1", "allow") {
		t.Fatal("已答后重放不应注册新 waiter")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(ad.permsRec()) != 1 {
			t.Fatalf("已答后重放不得再次 RespondPermission, got %v", ad.permsRec())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPlanSummaryFromContent（P1-12）：摘要规则——取首个非空行（markdown 计划的
// 标题位），按 planSummaryLimit 截断；内容为空或全空行时返回空串。
func TestPlanSummaryFromContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"标题行", "# 修复登录态丢失\n\n## 背景\n...", "# 修复登录态丢失"},
		{"开头空行跳过后取标题", "\n\n   \n# 建表\nrest", "# 建表"},
		{"超长首行按 rune 截断", strings.Repeat("长", 300), strings.Repeat("长", 200)},
		{"空内容", "", ""},
		{"全空行", "\n  \n\n", ""},
	}
	for _, c := range cases {
		if got := planSummaryFromContent([]byte(c.content)); got != c.want {
			t.Errorf("%s: planSummaryFromContent=%q, want %q", c.name, got, c.want)
		}
	}
}
