package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
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

// TestManualChecklistEscalatesToGateTicketWithoutConsult 锁死 B383 S4 的三出口
// 断言之「无复用」腿：人工清单形态（git checkout --）在无可复用同任务 allow 时
// 必须建 gate 工单，不经 Consult 审批模型直批（审批者已启用也不得被咨询）。
func TestManualChecklistEscalatesToGateTicketWithoutConsult(t *testing.T) {
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
	taskID := "manual-gate-task"
	now := time.Now().UTC()
	work := t.TempDir()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	command := "git checkout -- main.go"
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "manual-1", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
	m.handlePermission(context.Background(), taskID, ev)
	// 审批模型不得被咨询：give the (wrong) consult goroutine a chance to fire.
	select {
	case <-consulted:
		t.Fatal("人工清单形态不得经 Consult 审批模型直批")
	case <-time.After(150 * time.Millisecond):
	}
	if got := ad.recordedPerms(); len(got) != 0 {
		t.Fatalf("no-reuse 腿不得自动回传 executor，实得 %v", got)
	}
	if _, err := st.GetTicket(taskID + ":manual-1"); err != nil {
		t.Fatalf("人工清单形态必须建 gate 工单: %v", err)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 0 {
		t.Fatalf("人工门不得落 auto-allow 审计，实得 %d", got)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("task state = %s, want waiting_answer", task.State)
	}
}

// TestManualChecklistReusesSameTaskHumanAllow 三出口断言之「有复用」腿：同任务
// 已有人工 allow 时沿既有 reuseDecision 复用放行——不经 Consult、不建第二张待办。
func TestManualChecklistReusesSameTaskHumanAllow(t *testing.T) {
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
	taskID := "manual-reuse-task"
	now := time.Now().UTC()
	work := t.TempDir()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	command := "git checkout -- main.go"
	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "manual-2", Text: "Bash: " + command,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	}
	// 同任务先落一张同指纹、已 allow、已送达的人工工单。
	priorID := taskID + ":manual-prior"
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: priorID, TaskID: taskID, Kind: "gate",
		Request:     json.RawMessage(`{"kind":"gate","permission":"Bash: ` + command + `"}`),
		Fingerprint: executor.PermFingerprint(ev), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AnswerTicket(priorID, "allow"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTicketDelivered(priorID); err != nil {
		t.Fatal(err)
	}
	m.handlePermission(context.Background(), taskID, ev)
	select {
	case <-consulted:
		t.Fatal("复用放行不得经 Consult 审批模型")
	case <-time.After(150 * time.Millisecond):
	}
	if got := ad.recordedPerms(); len(got) != 1 || got[0] != "manual-2:once" {
		t.Fatalf("复用腿应回传 once，实得 %v", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionReuse); got != 1 {
		t.Fatalf("permission_reuse 事件数 = %d, want 1", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 0 {
		t.Fatalf("复用腿不得落 auto-allow 审计，实得 %d", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionRequest); got != 0 {
		t.Fatalf("复用腿不得再发人工升级事件，实得 %d", got)
	}
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
