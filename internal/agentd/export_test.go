package agentd

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// consultApproverForTest 同步调用审批者裁决，用于构造「裁决已经回来但任务已过
// 回合边界」的测试窗口。生产签名保持不变，测试只替换审批者输入。
func (m *Manager) consultApproverForTest(taskID string, approve bool, ticketID, permID string) {
	decision := "escalate"
	if approve {
		decision = "approve"
	}
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, nil, slog.Default())
	if err != nil {
		panic(err)
	}
	ap.runCmd = func(_ context.Context, argv []string) (string, error) {
		out, marshalErr := json.Marshal(map[string]string{
			"decision": decision,
			"reason":   "late decision",
		})
		if marshalErr != nil {
			return "", marshalErr
		}
		return injectNonceForTest(string(out), extractNonceForTest(strings.Join(argv, " "))), nil
	}
	original := m.approver
	m.approver = ap
	defer func() { m.approver = original }()
	m.consultApprover(context.Background(), taskID, executor.AdapterEvent{
		Type:         "permission",
		PermissionID: permID,
		Text:         "late permission",
		Perm:         &executor.PermRequest{Tool: executor.PermToolOther},
	}, ticketID)
}

// seedLateDecisionCase builds a real dispatched task and moves it beyond the
// running/waiting_answer boundary before invoking the fake approver.
func seedLateDecisionCase(t *testing.T, state proto.TaskState) (*Manager, *store.Store, *chanAdapter, string) {
	t.Helper()
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), DispatchReq{ProjectID: pid, Prompt: "late approval", Executor: "fake"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if err := m.transit(task.ID, proto.TaskStateWaitingReview, "late approval test"); err != nil {
		t.Fatalf("迁移到 waiting_review: %v", err)
	}
	if state == proto.TaskStateCompleted {
		if err := m.transit(task.ID, proto.TaskStateCompleted, "late approval test"); err != nil {
			t.Fatalf("迁移到 completed: %v", err)
		}
	}
	return m, st, ad, task.ID
}
