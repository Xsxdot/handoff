package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

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

	// Replaying the same permission id is idempotent at the Manager seam.
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 1 {
		t.Fatalf("replayed response count = %d, want 1", len(got))
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 1 {
		t.Fatalf("replayed audit count = %d, want 1", got)
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
