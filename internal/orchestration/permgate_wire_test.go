package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// TestSafeCommandPermissionAuditsOnceWithoutTicket drives the real Manager
// permission seam through Store JSON persistence and the fake adapter reply.
func TestSafeCommandPermissionAuditsOnceWithoutTicket(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "safe-task"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	tmpDir := executor.TaskTmpDir(m.cfg.DataDir, taskID)
	command := "go test ./... > " + filepath.Join(tmpDir, "out")
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "safe-1", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "safe-1:once" {
		t.Fatalf("responded permissions = %v, want [safe-1:once]", got)
	}
	events := mustEvents(t, st, taskID)
	var audit proto.Event
	count := 0
	for _, event := range events {
		if event.Type == proto.EventTypePermissionAutoAllow {
			audit = event
			count++
		}
	}
	if count != 1 {
		t.Fatalf("permission_auto_allow count = %d, want 1", count)
	}
	var payload permissionAutoAllowPayload
	if err := json.Unmarshal(audit.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PermissionID != "safe-1" || payload.Tool != executor.PermToolBash ||
		payload.Command != command || payload.Rule != permgate.RuleSafeCommand || payload.Reason == "" {
		t.Fatalf("audit payload = %#v", payload)
	}
	if _, err := st.GetTicket(taskID + ":safe-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ticket lookup error = %v, want store.ErrNotFound", err)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("task state = %s, want running", task.State)
	}

	// Replaying the same permission id must still deliver once: agy may
	// fire a second PreToolUse hook (HOME + workspace) after the first
	// allow already cleared pending. Audit stays unique.
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 2 || got[1] != "safe-1:once" {
		t.Fatalf("replayed responses = %v, want two once deliveries", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 1 {
		t.Fatalf("replayed audit count = %d, want 1", got)
	}
}

// TestRmInScopePermissionAuditsWithRule locks the B383 S2a release's audit
// seam: an rm command whose targets all provably sit in TaskTmpDir gets
// AutoAllow with Rule=rm-in-scope and mints the same structured
// permission_auto_allow event as the static whitelist — it is a release of a
// formerly escalating blacklist hit, so the durable audit must be able to
// answer "who allowed this and under which rule".
func TestRmInScopePermissionAuditsWithRule(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "rm-scope-task"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	command := `cd "$TMPDIR" && rm -rf verify`
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "rm-1", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "rm-1:once" {
		t.Fatalf("responded permissions = %v, want [rm-1:once]", got)
	}
	events := mustEvents(t, st, taskID)
	var audit proto.Event
	count := 0
	for _, event := range events {
		if event.Type == proto.EventTypePermissionAutoAllow {
			audit = event
			count++
		}
	}
	if count != 1 {
		t.Fatalf("permission_auto_allow count = %d, want 1", count)
	}
	var payload permissionAutoAllowPayload
	if err := json.Unmarshal(audit.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PermissionID != "rm-1" || payload.Tool != executor.PermToolBash ||
		payload.Command != command || payload.Rule != permgate.RuleRmInScope || payload.Reason == "" {
		t.Fatalf("audit payload = %#v", payload)
	}
	if _, err := st.GetTicket(taskID + ":rm-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ticket lookup error = %v, want store.ErrNotFound", err)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("task state = %s, want running", task.State)
	}
}

// TestAnsweredTicketReplayStillResponds 锁死 B314：审批链已答之后，
// 同一 PermissionID 的第二次 PreToolUse 仍须按工单回写（agy HOME+workspace 双 hook）。
// 命令故意用白名单拒掉的连接符形态，避免漏回写时误走 AutoAllow 假绿。
func TestAnsweredTicketReplayStillResponds(t *testing.T) {
	cases := []struct {
		name, answer, want string
	}{
		{"allow", "allow", "step_2:once"},
		{"deny", "deny: 太危险", "step_2:reject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, adapter := newTestManager(t)
			taskID := "agy-approve-replay-" + tc.name
			now := time.Now().UTC()
			mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
				State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
			permID := "step_2"
			ticketID := taskID + ":" + permID
			command := "git status && git rev-parse HEAD"
			if _, err := st.CreateTicket(&proto.Ticket{
				ID: ticketID, TaskID: taskID, Kind: "gate",
				Request:   json.RawMessage(`{"kind":"gate","permission":"run_command: ` + command + `"}`),
				CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.AnswerTicket(ticketID, tc.answer); err != nil {
				t.Fatal(err)
			}
			ev := executor.AdapterEvent{
				Type: "permission", PermissionID: permID, Text: "run_command: " + command,
				Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
			}
			m.handlePermission(context.Background(), taskID, ev)
			if got := adapter.recordedPerms(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("answered replay responses = %v, want [%s]", got, tc.want)
			}
			if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 0 {
				t.Fatalf("replay must not mint auto-allow audit, got %d", got)
			}
			task, err := st.GetTask(taskID)
			if err != nil {
				t.Fatal(err)
			}
			if task.State != proto.TaskStateRunning {
				t.Fatalf("task state = %s, want running", task.State)
			}
		})
	}
}

// TestSafeCommandAuditFailureStillResponds verifies Store append failure does
// not leave the executor waiting for its once response.
func TestSafeCommandAuditFailureStillResponds(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "safe-audit-failure"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	m.appendEvent = func(string, proto.EventType, any) (proto.Event, error) {
		return proto.Event{}, errors.New("audit store unavailable")
	}
	command := "go test ./..."
	m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "safe-fail", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	})
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "safe-fail:once" {
		t.Fatalf("responded permissions = %v, want [safe-fail:once]", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 0 {
		t.Fatalf("failed audit count = %d, want 0", got)
	}
}

// TestInScopeWriteDoesNotCreateSafeCommandAudit preserves the distinction
// between ordinary in-scope writes and static command whitelist decisions.
func TestInScopeWriteDoesNotCreateSafeCommandAudit(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "write-no-audit"
	work := t.TempDir()
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	m.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "write-1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{filepath.Join(work, "main.go")}},
	})
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "write-1:once" {
		t.Fatalf("responded permissions = %v, want [write-1:once]", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 0 {
		t.Fatalf("in-scope write audit count = %d, want 0", got)
	}
}

// TestJudgePermissionNilPermEscalates adapter 没给结构 → fail-closed 升级。
func TestJudgePermissionNilPermEscalates(t *testing.T) {
	m := newWireTestManager(t)
	v := m.judgePermission("t1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: /etc/hosts"})
	if v.Action != permgate.Escalate {
		t.Fatalf("Perm 缺失必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionInScopeWriteAutoAllows 工作区内的写自动放行。
func TestJudgePermissionInScopeWriteAutoAllows(t *testing.T) {
	m, taskID, work := newWireTestManagerWithTask(t)
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1",
		Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{filepath.Join(work, "main.go")}},
	})
	if v.Action != permgate.AutoAllow {
		t.Fatalf("工作区内写入应自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionSafeCommandUsesTaskTmpScope verifies Manager wires the
// executor-owned scratch root into the policy gate instead of rebuilding it in
// permgate.
func TestJudgePermissionSafeCommandUsesTaskTmpScope(t *testing.T) {
	m, taskID, _ := newWireTestManagerWithTask(t)
	tmpDir := executor.TaskTmpDir(m.cfg.DataDir, taskID)
	command := "go test ./... > " + filepath.Join(tmpDir, "out")
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p-safe", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	})
	if v.Action != permgate.AutoAllow || v.Rule != permgate.RuleSafeCommand {
		t.Fatalf("task tmp safe command verdict = %#v, want AutoAllow safe-command", v)
	}
}

// TestJudgePermissionOutsideWriteEscalates 越界写升级人工。
func TestJudgePermissionOutsideWriteEscalates(t *testing.T) {
	m, taskID, _ := newWireTestManagerWithTask(t)
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1",
		Text: "Write: /etc/hosts",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{"/etc/hosts"}},
	})
	if v.Action != permgate.Escalate {
		t.Fatalf("越界写必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestAutoAllowWorksWithoutApprover 锁死 spec §5.3：AutoAllow 与审批者
// 启用状态解耦。
//
// 这条必须单独钉：Write/Edit 改成 ask 之后，若 AutoAllow 依赖审批者存在，
// 未配置审批者的部署会被工作区内的每一次写入淹没。
func TestAutoAllowWorksWithoutApprover(t *testing.T) {
	m, taskID, work := newWireTestManagerWithTask(t)
	m.approver = nil // 显式关掉审批链
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{filepath.Join(work, "main.go")}},
	})
	if v.Action != permgate.AutoAllow {
		t.Fatalf("审批者未启用时 AutoAllow 仍须生效，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionUnknownTaskEscalates 读不到任务 → 范围不可知 → 升级。
func TestJudgePermissionUnknownTaskEscalates(t *testing.T) {
	m := newWireTestManager(t)
	v := m.judgePermission("no-such-task", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"main.go"}},
	})
	if v.Action != permgate.Escalate {
		t.Fatalf("任务读不到时范围不可知，必须升级，实得 %s（%s）", v.Action, v.Reason)
	}
}

func newWireTestManager(t *testing.T) *Manager {
	t.Helper()
	g, err := permgate.New(nil, slog.Default())
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	m, _, _, _ := newTestManager(t)
	m.gate = g
	return m
}

func newWireTestManagerWithTask(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, st, _, _ := newTestManager(t)
	g, err := permgate.New(nil, slog.Default())
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	m.gate = g
	work := t.TempDir()
	taskID := "wire-task-1"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	return m, taskID, work
}

// TestJudgePermissionNilGateEscalates 锁死 fail-closed 的最后一环：判据网关
// 未装配时必须升级人工，而不是在权限处理 goroutine 里 panic 掉整个 agentd。
func TestJudgePermissionNilGateEscalates(t *testing.T) {
	m := &Manager{gate: nil, log: slog.Default()}
	v := m.judgePermission("t1", executor.AdapterEvent{PermissionID: "p1"})
	if v.Action != permgate.Escalate {
		t.Fatalf("gate 为 nil 时必须 Escalate，实得 %v", v.Action)
	}
}

// manualChecklistHeredoc 是 B451 spec S1 原形：heredoc 内联 Python 只改工作树
// 内文件（生产 E21 读数里 20 张人工工单的同一形态）。
const manualChecklistHeredoc = "python3 - <<'PY'\nopen('a.txt','w').write('x')\nPY"

// manualChecklistFixture 是人工清单 wire 测试的公共装配：真实 gate + Manager
// 权限接缝 + 记录回传的 chanAdapter + 计数的审批者桩。
type manualChecklistFixture struct {
	m      *Manager
	st     *store.Store
	ad     *chanAdapter
	taskID string
	calls  *atomic.Int32 // 审批者 OneShot 被调用次数
}

// newManualChecklistFixture 装配夹具。decision 为审批者桩的裁决（approve /
// escalate），空串表示不装审批者（链不可用）。桩按 prompt 回显 nonce——不回显
// 会被 Decide 判 Err，走失败分支而不是 approve/escalate，测试会假绿。
func newManualChecklistFixture(t *testing.T, taskID, decision string) *manualChecklistFixture {
	t.Helper()
	calls := &atomic.Int32{}
	var ap *Approver
	if decision != "" {
		var aerr error
		ap, aerr = NewApprover(config.ApproverConfig{Executor: config.ExecutorList{"opencode"}, Timeout: time.Second}, nil, slog.Default())
		if aerr != nil {
			t.Fatal(aerr)
		}
		out := `{"decision":"` + decision + `","reason":"桩裁决"}`
		ap.BindOneShot(&stubShot{fn: func(_ context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			calls.Add(1)
			return executor.OneShotReply{Text: injectNonceForTest(out, extractNonceForTest(req.Prompt)),
				Status: executor.OneShotOK}, nil
		}})
	}
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": ad}, "fake", ap)
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	return &manualChecklistFixture{m: m, st: st, ad: ad, taskID: taskID, calls: calls}
}

// manualChecklistEvent 构造一条 bash 权限事件。
func manualChecklistEvent(permID, command string) executor.AdapterEvent {
	return executor.AdapterEvent{
		Type: "permission", PermissionID: permID, Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
}

// approverDecisions 解出任务的全部 approver_decision 事件载荷（seq 升序）。
func approverDecisions(t *testing.T, st *store.Store, taskID string) []ApproverDecisionPayload {
	t.Helper()
	var out []ApproverDecisionPayload
	for _, e := range mustEvents(t, st, taskID) {
		if e.Type != proto.EventTypeApproverDecision {
			continue
		}
		var p ApproverDecisionPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解析 approver_decision: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// assertNoPendingTicket 断言工单不存在或已答 allow——审批者批准与复用放行都会
// 建一张已答工单留审计，所以「不建待答工单」不能写成「GetTicket 报错」。
func assertNoPendingTicket(t *testing.T, st *store.Store, ticketID string) {
	t.Helper()
	tk, err := st.GetTicket(ticketID)
	if err != nil {
		return
	}
	if tk.Answer == nil || *tk.Answer != "allow" {
		t.Fatalf("工单 %s 存在且未答 allow（answer=%v），不得留待答工单", ticketID, tk.Answer)
	}
}

// TestManualChecklistConsultsApproverAndApproves 锁 B451 S1（Manager 路径）：
// 人工清单形态（heredoc 内联 Python）交审批链，审批者批准即放行——回传 once、
// 落 approver_decision approve、不发 permission_request、任务不进 waiting_answer。
func TestManualChecklistConsultsApproverAndApproves(t *testing.T) {
	f := newManualChecklistFixture(t, "manual-gate-task", "approve")
	f.m.handlePermission(context.Background(), f.taskID, manualChecklistEvent("manual-1", manualChecklistHeredoc))
	waitApproverIdle(t, f.m)

	if f.calls.Load() == 0 {
		t.Fatal("人工清单形态必须经审批链裁决，审批者未被调用")
	}
	if got := f.ad.recordedPerms(); len(got) != 1 || got[0] != "manual-1:once" {
		t.Fatalf("审批者批准后应回传 once，实得 %v", got)
	}
	ds := approverDecisions(t, f.st, f.taskID)
	if len(ds) != 1 || ds[0].Decision != "approve" {
		t.Fatalf("approver_decision = %+v，期望 1 条 approve", ds)
	}
	if got := countEvents(mustEvents(t, f.st, f.taskID), proto.EventTypePermissionRequest); got != 0 {
		t.Fatalf("审批者批准不得发 permission_request，实得 %d", got)
	}
	task, err := f.st.GetTask(f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("task state = %s, want running（不得停在 waiting_answer）", task.State)
	}
	assertNoPendingTicket(t, f.st, f.taskID+":manual-1")
}

// TestManualChecklistApproverEscalateCreatesTicket 锁 B451 S3(a)：审批者判
// escalate 时与普通 Consult 一样建待答工单、发 permission_request、进 waiting_answer。
func TestManualChecklistApproverEscalateCreatesTicket(t *testing.T) {
	f := newManualChecklistFixture(t, "manual-esc-task", "escalate")
	f.m.handlePermission(context.Background(), f.taskID, manualChecklistEvent("manual-1", manualChecklistHeredoc))
	waitApproverIdle(t, f.m)

	ds := approverDecisions(t, f.st, f.taskID)
	if len(ds) != 1 || ds[0].Decision != "escalate" {
		t.Fatalf("approver_decision = %+v，期望 1 条 escalate（error 说明桩没回显 nonce）", ds)
	}
	tk := mustGetTicket(t, f.st, f.taskID+":manual-1")
	if tk.Answer != nil {
		t.Fatalf("审批者升级后工单应待答，实得 answer=%q", *tk.Answer)
	}
	if got := countEvents(mustEvents(t, f.st, f.taskID), proto.EventTypePermissionRequest); got != 1 {
		t.Fatalf("permission_request 事件数 = %d, want 1", got)
	}
	if got := f.ad.recordedPerms(); len(got) != 0 {
		t.Fatalf("审批者升级不得回传 executor，实得 %v", got)
	}
	task, err := f.st.GetTask(f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("task state = %s, want waiting_answer", task.State)
	}
}

// TestManualChecklistApproverUnavailableCreatesTicket 锁 B451 S3(b)：审批链不可用
// （未装配审批者）时建待答工单，不静默放行，也没有 approver_decision。
func TestManualChecklistApproverUnavailableCreatesTicket(t *testing.T) {
	f := newManualChecklistFixture(t, "manual-noap-task", "")
	f.m.handlePermission(context.Background(), f.taskID, manualChecklistEvent("manual-1", manualChecklistHeredoc))

	tk := mustGetTicket(t, f.st, f.taskID+":manual-1")
	if tk.Answer != nil {
		t.Fatalf("链不可用时工单应待答，实得 answer=%q", *tk.Answer)
	}
	if ds := approverDecisions(t, f.st, f.taskID); len(ds) != 0 {
		t.Fatalf("链不可用不得有 approver_decision，实得 %+v", ds)
	}
	if got := countEvents(mustEvents(t, f.st, f.taskID), proto.EventTypePermissionRequest); got != 1 {
		t.Fatalf("permission_request 事件数 = %d, want 1", got)
	}
	if got := f.ad.recordedPerms(); len(got) != 0 {
		t.Fatalf("链不可用不得回传 executor，实得 %v", got)
	}
}

// TestManualChecklistApproverDisabledNamesItInTicket 锁 B451 S3(c)：本任务已停用
// 审批链时不咨询审批者，建工单且请求体理由点名停用。
func TestManualChecklistApproverDisabledNamesItInTicket(t *testing.T) {
	f := newManualChecklistFixture(t, "manual-off-task", "approve")
	f.m.apMu.Lock()
	f.m.apDisabled[f.taskID] = true
	f.m.apMu.Unlock()
	f.m.handlePermission(context.Background(), f.taskID, manualChecklistEvent("manual-1", manualChecklistHeredoc))

	if n := f.calls.Load(); n != 0 {
		t.Fatalf("已停用时不得咨询审批者，实得调用 %d 次", n)
	}
	tk := mustGetTicket(t, f.st, f.taskID+":manual-1")
	var req ticketRequest
	if err := json.Unmarshal(tk.Request, &req); err != nil {
		t.Fatalf("解析工单请求体: %v", err)
	}
	if !strings.Contains(req.Reason, "审批链已停用") {
		t.Fatalf("工单理由必须点名停用，得到 %q", req.Reason)
	}
}

// TestManualChecklistReusesSameTaskHumanAllow 锁 B451 S3 复用腿（plan Q1 甲）：
// Manager 路径先问审批者；审批者 escalate 后在升级出口命中同任务人工 allow
// 复用放行——回传 once，不建第二张待答工单、不发 permission_request。
func TestManualChecklistReusesSameTaskHumanAllow(t *testing.T) {
	f := newManualChecklistFixture(t, "manual-reuse-task", "escalate")
	command := "git checkout -- main.go"
	ev := manualChecklistEvent("manual-2", command)
	now := time.Now().UTC()
	// 同任务先落一张同指纹、已 allow、已送达的人工工单。
	priorID := f.taskID + ":manual-prior"
	if _, err := f.st.CreateTicket(&proto.Ticket{
		ID: priorID, TaskID: f.taskID, Kind: "gate",
		Request:     json.RawMessage(`{"kind":"gate","permission":"Bash: ` + command + `"}`),
		Fingerprint: executor.PermFingerprint(ev), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AnswerTicket(priorID, "allow"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.MarkTicketDelivered(priorID); err != nil {
		t.Fatal(err)
	}
	f.m.handlePermission(context.Background(), f.taskID, ev)
	waitApproverIdle(t, f.m)

	if n := f.calls.Load(); n != 1 {
		t.Fatalf("Manager 路径应先问审批者一次（Q1 甲），实得 %d 次", n)
	}
	if got := f.ad.recordedPerms(); len(got) != 1 || got[0] != "manual-2:once" {
		t.Fatalf("复用腿应回传 once，实得 %v", got)
	}
	evs := mustEvents(t, f.st, f.taskID)
	if got := countEvents(evs, proto.EventTypePermissionReuse); got != 1 {
		t.Fatalf("permission_reuse 事件数 = %d, want 1", got)
	}
	if got := countEvents(evs, proto.EventTypePermissionAutoAllow); got != 0 {
		t.Fatalf("复用腿不得落 auto-allow 审计，实得 %d", got)
	}
	if got := countEvents(evs, proto.EventTypePermissionRequest); got != 0 {
		t.Fatalf("复用腿不得再发人工升级事件，实得 %d", got)
	}
	assertNoPendingTicket(t, f.st, f.taskID+":manual-2")
}

// TestGoModuleCachePathsEscalatesHard 三出口断言之「越界 Paths」腿：以越界
// Paths 上报的 Go 模块缓存读由范围门硬升级，不经 Consult（断言既有语义不回退）。
func TestGoModuleCachePathsEscalatesHard(t *testing.T) {
	ap, aerr := NewApprover(config.ApproverConfig{Executor: config.ExecutorList{"opencode"}, Timeout: time.Second}, nil, slog.Default())
	if aerr != nil {
		t.Fatal(aerr)
	}
	consulted := make(chan struct{}, 1)
	ap.BindOneShot(&stubShot{fn: func(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
		consulted <- struct{}{}
		return executor.OneShotReply{Status: executor.OneShotOK}, nil
	}})
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": ad}, "fake", ap)
	taskID := "gomod-paths-task"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "gomod-1", Text: "Bash: go build ./...",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "go build ./...",
			Paths: []string{"/home/u/go/pkg/mod/cache/download"}},
	}
	verdict := m.judgePermission(taskID, ev)
	if verdict.Action != permgate.Escalate {
		t.Fatalf("越界 Paths 必须硬升级，实得 %s（%s）", verdict.Action, verdict.Reason)
	}
	m.handlePermission(context.Background(), taskID, ev)
	select {
	case <-consulted:
		t.Fatal("越界 Paths 不得经 Consult 审批模型")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := st.GetTicket(taskID + ":gomod-1"); err != nil {
		t.Fatalf("越界 Paths 必须建 gate 工单: %v", err)
	}
}

// TestHeredocInScopePermissionAuditsWithRule 锁死 B383 S2b 的审计缝：heredoc
// 闭集放行与 rm-in-scope 同享结构化 permission_auto_allow 审计，可回答「谁放行的、
// 凭哪条规则」。
func TestHeredocInScopePermissionAuditsWithRule(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "heredoc-scope-task"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	tmpDir := executor.TaskTmpDir(m.cfg.DataDir, taskID)
	command := "cd " + tmpDir + "/cardq && cat > main.go <<'EOF'\npackage main\nEOF"
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "hd-1", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "hd-1:once" {
		t.Fatalf("responded permissions = %v, want [hd-1:once]", got)
	}
	events := mustEvents(t, st, taskID)
	var audit proto.Event
	count := 0
	for _, event := range events {
		if event.Type == proto.EventTypePermissionAutoAllow {
			audit = event
			count++
		}
	}
	if count != 1 {
		t.Fatalf("permission_auto_allow count = %d, want 1", count)
	}
	var payload permissionAutoAllowPayload
	if err := json.Unmarshal(audit.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Rule != permgate.RuleHeredocInScope || payload.PermissionID != "hd-1" || payload.Reason == "" {
		t.Fatalf("audit payload = %#v", payload)
	}
	if _, err := st.GetTicket(taskID + ":hd-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ticket lookup error = %v, want store.ErrNotFound", err)
	}
}
