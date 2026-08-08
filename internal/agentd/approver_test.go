// approver 白盒测试：黑名单命中、CLI 裁决、fail-closed 三连与审批链接入 handlePermission。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// newTestApprover 构造带注入 runCmd 的 Approver（裁决输出 out、错误 err）。
func newTestApprover(t *testing.T, out string, err error) *Approver {
	a, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	a.runCmd = func(ctx context.Context, argv []string) (string, error) { return out, err }
	return a
}

func TestApproverNilWhenUnconfigured(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{}, slog.Default())
	if err != nil || a != nil {
		t.Fatalf("未配置应返回 (nil,nil)，得到 %v %v", a, err)
	}
}

func TestBlacklistBuiltinAndCustom(t *testing.T) {
	a, err := NewApprover(config.ApproverConfig{
		Executor: "opencode", Timeout: time.Second,
		Blacklist: []string{`kubectl .*delete`},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"Bash: rm -rf node_modules", "Bash: git push --force origin main",
		"Bash: sudo systemctl restart nginx", "Bash: git reset --hard HEAD~3",
		"Bash: psql -c 'DROP TABLE users'", "Bash: deploy to production",
		"Bash: kubectl pods delete --all",
		// P1-2：长选项与 git -C 绕过——脚本常规写法，黑名单必须拦住
		"Bash: rm --recursive --force /",
		"Bash: git -C /repo push --force origin main",
		"Bash: rm --recursive /tmp/x --force",
	} {
		if hit, _ := a.Blacklisted(s); !hit {
			t.Fatalf("应命中黑名单: %s", s)
		}
	}
	if hit, _ := a.Blacklisted("Bash: go test ./..."); hit {
		t.Fatalf("go test 不应命中黑名单")
	}
}

func TestDecideApprove(t *testing.T) {
	a := newTestApprover(t, "思考过程...\n{\"decision\":\"approve\",\"reason\":\"项目内读写\"}\n", nil)
	d := a.Decide(context.Background(), "Edit: main.go", "修 bug")
	if !d.Approve || d.Err != nil {
		t.Fatalf("应 approve: %+v", d)
	}
}

func TestDecideEscalate(t *testing.T) {
	a := newTestApprover(t, `{"decision":"escalate","reason":"拿不准"}`, nil)
	d := a.Decide(context.Background(), "Bash: curl ...", "")
	if d.Approve || d.Err != nil || d.Reason != "拿不准" {
		t.Fatalf("应干净 escalate: %+v", d)
	}
}

// TestDecideFailClosed 覆盖 fail-closed 三连：命令失败 / 输出无 JSON / decision 取值非法，
// 全部 escalate 且 Err 非 nil（供上层连续失败计数）。
func TestDecideFailClosed(t *testing.T) {
	for name, a := range map[string]*Approver{
		"命令失败":  newTestApprover(t, "", errors.New("exit 1")),
		"无JSON": newTestApprover(t, "我觉得可以批", nil),
		"取值非法":  newTestApprover(t, `{"decision":"deny"}`, nil),
	} {
		if d := a.Decide(context.Background(), "x", ""); d.Approve || d.Err == nil {
			t.Fatalf("%s: 应 fail-closed escalate: %+v", name, d)
		}
	}
}

func TestDecidePromptContainsContext(t *testing.T) {
	var got []string
	a := newTestApprover(t, `{"decision":"approve"}`, nil)
	a.runCmd = func(ctx context.Context, argv []string) (string, error) {
		got = argv
		return `{"decision":"approve"}`, nil
	}
	a.Decide(context.Background(), "PERM-TEXT", "TASK-SUMMARY")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "PERM-TEXT") || !strings.Contains(joined, "TASK-SUMMARY") {
		t.Fatalf("裁决 prompt 应含权限原文与任务摘要: %v", got)
	}
}

// ---- 审批链接入 handlePermission 的集成测试（fake 脚本驱动）----

// approverStep 构造 fake 脚本：一个权限请求后跟一次 OK 的 finish。
func approverStep(perm string) []fake.Step {
	return []fake.Step{{Permission: perm}, {Finish: executor.Result{OK: true, Branch: "handoff/x"}}}
}

// newTestManagerWithApproverOut 构造带审批者（runCmd 注入固定输出/错误）的 manager，
// fake 脚本由调用方提供。
func newTestManagerWithApproverOut(t *testing.T, script []fake.Step, out string, cmdErr error) (*Manager, *store.Store, *fake.Fake) {
	t.Helper()
	ap, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	ap.runCmd = func(ctx context.Context, argv []string) (string, error) { return out, cmdErr }
	fk := fake.New(script)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", ap)
	return m, st, fk
}

// newTestManagerWithApproverFunc 构造带审批者（runCmd 注入自定义函数）的 manager，
// fake 脚本由调用方提供。
func newTestManagerWithApproverFunc(t *testing.T, script []fake.Step, fn func(ctx context.Context, argv []string) (string, error)) (*Manager, *store.Store, *fake.Fake) {
	t.Helper()
	ap, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	ap.runCmd = fn
	fk := fake.New(script)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", ap)
	return m, st, fk
}

// mustApproverDispatch 派发一个任务到 fake（真实工作区准备），返回任务。
func mustApproverDispatch(t *testing.T, m *Manager) *proto.Task {
	t.Helper()
	repo := initTestRepo(t)
	task, err := m.Dispatch(context.Background(), DispatchReq{Repo: repo, Prompt: "跑测试", Executor: "fake"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	return task
}

// waitTaskState 轮询等待任务到达指定状态。
func waitTaskState(t *testing.T, st *store.Store, taskID string, want proto.TaskState) {
	t.Helper()
	eventually(t, 3*time.Second, "任务状态 "+string(want), func() bool {
		cur, err := st.GetTask(taskID)
		return err == nil && cur.State == want
	})
}

// waitCondition 轮询等待条件成立。
func waitCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	eventually(t, 3*time.Second, desc, cond)
}

// mustGetTicket 读取工单；不存在即失败。
func mustGetTicket(t *testing.T, st *store.Store, id string) *proto.Ticket {
	t.Helper()
	tk, err := st.GetTicket(id)
	if err != nil {
		t.Fatalf("GetTicket(%s): %v", id, err)
	}
	return tk
}

// mustEvents 返回任务全部事件（seq 升序）。
func mustEvents(t *testing.T, st *store.Store, taskID string) []proto.Event {
	t.Helper()
	evs, err := st.EventsFrom(taskID, 0, 1000)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	return evs
}

// hasEvent 判断事件列表是否含指定类型。
func hasEvent(evs []proto.Event, typ proto.EventType) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// countEvents 统计事件列表中指定类型的条数。
func countEvents(evs []proto.Event, typ proto.EventType) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// TestApproverApprovesPermissionWithoutWaking 验证 approve 路径：
// 工单自动应答+送达（审计闭环）、只留 approver_decision 审计事件（不产生
// permission_request 唤醒审核者）、executor 真收到 once。
func TestApproverApprovesPermissionWithoutWaking(t *testing.T) {
	m, st, fk := newTestManagerWithApproverOut(t, approverStep("run tests"), `{"decision":"approve","reason":"跑测试"}`, nil)
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview) // 直通完成，未经 waiting_answer 停留

	// 断言 1：工单已建且已答已送达（审计闭环）；answer 为精确 allow（P0-2）
	tk := mustGetTicket(t, st, task.ID+":"+fk.PermID())
	if tk.Answer == nil || *tk.Answer != "allow" || tk.DeliveredAt == nil {
		t.Fatalf("approve 应自动应答精确 allow 并标记送达: %+v", tk)
	}
	// 断言 2：approver_decision 事件在，permission_request 事件不在（不唤醒审核者）
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDecision) || hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("approve 只留审计事件，不发 permission_request: %v", evs)
	}
	// 断言 3：executor 真收到 once
	if fk.LastDecision() != "once" {
		t.Fatalf("executor 应收到 once，得到 %q", fk.LastDecision())
	}
}

// TestApproverEscalateFallsThroughToReviewer 验证 escalate 路径：
// 完整走既有中介流程（waiting_answer + permission_request 唤醒审核者），
// approver_decision 审计事件同时保留。
func TestApproverEscalateFallsThroughToReviewer(t *testing.T) {
	m, st, _ := newTestManagerWithApproverOut(t, approverStep("run tests"), `{"decision":"escalate","reason":"拿不准"}`, nil)
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer)
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDecision) || !hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("escalate 应留审计事件并走既有唤醒流程")
	}
}

// TestApproverBlacklistSkipsApprover 验证黑名单命中时直接升级人工审核者，
// 审批者 runCmd 绝不被调用（fake 脚本权限文本命中内置 sudo 规则）。
func TestApproverBlacklistSkipsApprover(t *testing.T) {
	m, st, _ := newTestManagerWithApproverFunc(t, approverStep("Bash: sudo rm -rf /"),
		func(ctx context.Context, argv []string) (string, error) {
			t.Fatal("黑名单命中不应调用审批者")
			return "", nil
		})
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer) // 直接升级审核者
}

// TestApproverFailClosedCountsAndDisables 验证 fail-closed + 连续失败禁用：
// 裁决恒错时每个权限都升级审核者（fail-closed），但审批者只被调 3 次即停用，
// 第 4 个权限直接升级不再调用（防对已损坏审批者命令的重试风暴）。
//
// 为什么直接驱动 handlePermission 而非 fake 脚本：fake 的 Permission 步骤会阻塞
// 等 RespondPermission，而 fail-closed 路径无人应答（升级审核者），脚本跑不完；
// 白盒直驱四个权限事件精确复现「4 请求 / 3 次审批调用」。
func TestApproverFailClosedCountsAndDisables(t *testing.T) {
	callCount := 0
	ap, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	ap.runCmd = func(ctx context.Context, argv []string) (string, error) {
		callCount++
		return "", errors.New("boom")
	}
	fk := fake.New(nil) // 空脚本：不产权限事件，由测试直接驱动 handlePermission
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", ap)
	task := mustApproverDispatch(t, m)

	// 前 3 个权限请求：审批者各裁决一次且全部失败（fail-closed 升级审核者）
	for _, perm := range []string{"p1", "p2", "p3"} {
		m.handlePermission(context.Background(), task.ID,
			executor.AdapterEvent{Type: "permission", PermissionID: perm, Text: "something"})
	}
	waitCondition(t, "审批者被调 3 次", func() bool { return callCount == 3 })
	// 等禁用标记落定（consultApprover goroutine 异步写）
	waitCondition(t, "审批链已停用", func() bool {
		m.apMu.Lock()
		defer m.apMu.Unlock()
		return m.apDisabled[task.ID]
	})

	// 第 4 个权限：审批链已停用，直接升级不调用审批者（callCount 保持 3）
	m.handlePermission(context.Background(), task.ID,
		executor.AdapterEvent{Type: "permission", PermissionID: "p4", Text: "something"})
	waitCondition(t, "第 4 个权限也升级审核者", func() bool {
		return countEvents(mustEvents(t, st, task.ID), proto.EventTypePermissionRequest) == 4
	})
	if callCount != 3 {
		t.Fatalf("停用后审批者不应再被调用，callCount=%d", callCount)
	}

	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypeApproverDisabled) {
		t.Fatalf("连续失败 3 次应记 approver_disabled")
	}
	if countEvents(evs, proto.EventTypePermissionRequest) != 4 {
		t.Fatalf("4 个权限请求都应升级审核者，得到 %d", countEvents(evs, proto.EventTypePermissionRequest))
	}
}

// TestApproverApprovedTicketAnswerIsExactAllow 验证审批者批准写入的工单 answer
// 是精确 "allow"（P0-2）：理由已落在 approver_decision 事件的 Reason 字段，
// 不塞进 answer 串——否则 gate 翻译规则（answer 严格等于 "allow" 才放行）会把
// 审批者的批准在 resume 重投时翻转成 reject。
func TestApproverApprovedTicketAnswerIsExactAllow(t *testing.T) {
	m, st, fk := newTestManagerWithApproverOut(t, approverStep("run tests"), `{"decision":"approve","reason":"跑测试"}`, nil)
	task := mustApproverDispatch(t, m)
	waitCondition(t, "审批者已批准并答题", func() bool {
		tk, err := st.GetTicket(task.ID + ":" + fk.PermID())
		return err == nil && tk.Answer != nil
	})
	tk := mustGetTicket(t, st, task.ID+":"+fk.PermID())
	if tk.Answer == nil || *tk.Answer != "allow" {
		t.Fatalf("审批者批准的 answer 应为精确 allow，得到 %q", *tk.Answer)
	}
}

// TestRelayAnswerRelaysApproverAllowAsOnce 验证审批者批准的精确 "allow" answer
// 经 RelayAnswer 重投时回传 executor "once" 而非 "reject"（P0-2 的翻转回归）。
func TestRelayAnswerRelaysApproverAllowAsOnce(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: "x"})
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: "t1:perm-1", TaskID: "t1", Kind: "gate", Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AnswerTicket("t1:perm-1", "allow"); err != nil {
		t.Fatal(err)
	}
	if err := m.RelayAnswer("t1", "t1:perm-1", "allow"); err != nil {
		t.Fatalf("RelayAnswer: %v", err)
	}
	if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-1:once" {
		t.Fatalf("executor 应收到 perm-1:once，得到 %v", got)
	}
}

// TestApproverConcurrentTaskEndOnlyAudits 验证审批链异步化的窗口防护（P1-1）：
// 裁决（最长 60s）期间 executor 死亡 → handleResult 已把任务落 waiting_review，
// 随后审批者判 escalate 也不得重建工单/唤醒审核者——只留 approver_decision 审计
// 事件，避免「状态 waiting_review 却带 pending 权限工单」的 U-1/U-3 矛盾形态回归。
func TestApproverConcurrentTaskEndOnlyAudits(t *testing.T) {
	ap, aerr := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	fk := fake.New(nil) // 空脚本：不产权限事件，由测试直接驱动
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", ap)
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateRunning)

	// 裁决窗口内 executor 死亡：runCmd 先触发 handleResult（落 waiting_review），
	// 再返回 escalate——复现「Decide 期间任务已终结、裁决结果后到」的时序
	ap.runCmd = func(ctx context.Context, argv []string) (string, error) {
		m.handleResult(task.ID, resultEvent())
		return `{"decision":"escalate","reason":"窗口内已终结"}`, nil
	}
	m.handlePermission(context.Background(), task.ID,
		executor.AdapterEvent{Type: "permission", PermissionID: "p1", Text: "x"})
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview)

	// 断言：不产生 permission_request、不新建挂起工单、状态保持 waiting_review
	evs := mustEvents(t, st, task.ID)
	if hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("任务已终结时审批者 escalate 不应产生 permission_request: %v", evs)
	}
	if pend, _ := st.PendingTickets(task.ID); len(pend) != 0 {
		t.Fatalf("不应新建挂起工单: %v", pend)
	}
	cur, _ := st.GetTask(task.ID)
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("状态应保持 waiting_review，得到 %s", cur.State)
	}
	if !hasEvent(evs, proto.EventTypeApproverDecision) {
		t.Fatalf("应留 approver_decision 审计事件: %v", evs)
	}
}

// TestApproverTruncatedPermissionEscalates 验证含截断标记的权限请求不交给廉价
// 模型（P1-3）：截断说明「看到的命令不完整」，危险片段可能落在 200 字符之外，
// 黑名单与模型都不可信——fail-closed 的直接延伸，升级人工审核者。
func TestApproverTruncatedPermissionEscalates(t *testing.T) {
	perm := "Bash: go test ./... " + executor.TruncationMarker
	var calls atomic.Int64
	m, st, _ := newTestManagerWithApproverFunc(t, approverStep(perm),
		func(ctx context.Context, argv []string) (string, error) {
			calls.Add(1)
			return `{"decision":"approve"}`, nil
		})
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer)
	if calls.Load() != 0 {
		t.Fatalf("含截断标记的权限不应调用审批者，被调 %d 次", calls.Load())
	}
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("含截断标记的权限应直接升级人工审核者（permission_request）: %v", evs)
	}
}

// TestNilApproverKeepsCurrentBehavior 验证 approver=nil 时现行为回归：
// 权限请求直接产生 permission_request 事件（二期前语义）。
func TestNilApproverKeepsCurrentBehavior(t *testing.T) {
	fk := fake.New(approverStep("run tests"))
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingAnswer)
	evs := mustEvents(t, st, task.ID)
	if !hasEvent(evs, proto.EventTypePermissionRequest) {
		t.Fatalf("approver=nil 时权限应直接走既有升级流程: %v", evs)
	}
}
