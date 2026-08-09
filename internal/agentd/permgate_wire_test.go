package agentd

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/permgate"
	"github.com/xushixin/handoff/internal/proto"
)

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
