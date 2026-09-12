package orchestration

import (
	"context"
	"errors"
	"fmt"
	agentd "github.com/Xsxdot/handoff/internal/agentd"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func taskDirOf(m *Manager, taskID string) string {
	return filepath.Join(m.cfg.DataDir, "tasks", taskID)
}

func TestBindApprovalUsesMovedProductionClient(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	client := m.bindApproval("task-moved", executor.PolicySnapshot{Version: "v1"})
	moved, ok := client.(*approval.Client)
	if !ok || moved == nil {
		t.Fatalf("bindApproval 必须返回 *approval.Client，got %T", client)
	}
	snap, err := client.PolicySnapshot(context.Background())
	if err != nil {
		t.Fatalf("PolicySnapshot: %v", err)
	}
	if snap.TaskID != "task-moved" {
		t.Fatalf("空 snapshot TaskID 必须由 bindApproval 补全，got %q", snap.TaskID)
	}
}

func setupTestTaskClient(t *testing.T, taskID, version string) (*Manager, executor.ApprovalClient, *proto.Task) {
	t.Helper()
	m, st, _, _ := newTestManager(t)
	task := &proto.Task{
		ID: taskID, RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake",
	}
	mustCreateTask(t, st, task)
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, taskID),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, taskID),
	}
	c := m.bindApproval(taskID, executor.PolicySnapshot{Version: version, TaskID: taskID, Scope: scope})
	return m, c, task
}

// 1. 范围内 edit 立即 allow，零 permission_request
func TestApprovalClientInScopeEditAllowsWithoutPermissionRequest(t *testing.T) {
	m, c, task := setupTestTaskClient(t, "T-edit", "v1")
	path := filepath.Join(task.Workdir(), "a.go")
	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-edit", Text: "edit " + path,
		Perm: &executor.PermRequest{Tool: executor.PermToolEdit, Paths: []string{path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("status=%q want allow", res.Decision.Status)
	}
	evs, err := m.st.EventsFromAsc("T-edit", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionRequest {
			t.Fatal("范围内写不得发布 permission_request")
		}
	}
}

// 2. Text 含 TruncationMarker 不得 allow
func TestApprovalClientTruncatedTextDoesNotAllow(t *testing.T) {
	_, c, task := setupTestTaskClient(t, "T-trunc", "v1")
	path := filepath.Join(task.Workdir(), "a.go")
	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-trunc",
		Text:     "edit " + path + " " + executor.TruncationMarker,
		Perm:     &executor.PermRequest{Tool: executor.PermToolEdit, Paths: []string{path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status == executor.ApprovalAllow {
		t.Fatalf("截断文本不得 allow，got %q", res.Decision.Status)
	}
}

// 3. Request 返回的 Decision.Status 只能是 allow/deny/pending，不得为 consult
func TestApprovalClientStatusNeverConsult(t *testing.T) {
	_, c, _ := setupTestTaskClient(t, "T-status", "v1")
	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-cmd",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status == "consult" {
		t.Fatalf("Decision.Status 不得为 consult")
	}
	if res.Decision.Status != executor.ApprovalAllow &&
		res.Decision.Status != executor.ApprovalDeny &&
		res.Decision.Status != executor.ApprovalPending {
		t.Fatalf("非法 status %q", res.Decision.Status)
	}
}

// 4. 同 Task + 同 Version + 同 PermFingerprint + 已 Acknowledge(delivered) 的 allow -> 第二次 Request 为 allow，且 permission_request 仍为 0，有 permission_reuse 入库
func TestApprovalClientReuseAllow(t *testing.T) {
	m, c, _ := setupTestTaskClient(t, "T-reuse", "v1")
	req := executor.ApprovalRequest{
		NativeID: "perm-1",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res1, err := c.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Decision.Status != executor.ApprovalPending {
		t.Fatalf("首个请求需审批，want pending, got %q", res1.Decision.Status)
	}

	// 人工审批允许
	if err := m.st.AnswerTicket(res1.Ref.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	// Acknowledge delivered
	if err := c.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: res1.Ref, NativeID: req.NativeID, Stage: executor.AckDelivered,
	}); err != nil {
		t.Fatal(err)
	}

	// 第二次请求同一命令（同 task，同 version）
	req2 := executor.ApprovalRequest{
		NativeID: "perm-2",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res2, err := c.Request(context.Background(), req2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("复用请求应为 allow, got %q", res2.Decision.Status)
	}
	if res2.Decision.Rule != "reuse" {
		t.Fatalf("复用 rule 必须为 reuse, got %q", res2.Decision.Rule)
	}

	evs, err := m.st.EventsFromAsc("T-reuse", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	permReqCount := 0
	hasReuse := false
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionRequest {
			permReqCount++
		}
		if e.Type == proto.EventTypePermissionReuse {
			hasReuse = true
		}
	}
	if permReqCount != 1 {
		t.Fatalf("permission_request 期望仅 1 次，实际 %d", permReqCount)
	}
	if !hasReuse {
		t.Fatal("期望记录 permission_reuse 事件")
	}
}

// 5. 换 Version 后同指纹不得复用
func TestApprovalClientDifferentVersionDoesNotReuse(t *testing.T) {
	m, c1, task := setupTestTaskClient(t, "T-ver", "v1")
	req := executor.ApprovalRequest{
		NativeID: "perm-1",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res1, err := c1.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.st.AnswerTicket(res1.Ref.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	if err := c1.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: res1.Ref, NativeID: req.NativeID, Stage: executor.AckDelivered,
	}); err != nil {
		t.Fatal(err)
	}

	// 换 Version: v2
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, "T-ver"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-ver"),
	}
	c2 := m.bindApproval("T-ver", executor.PolicySnapshot{Version: "v2", TaskID: "T-ver", Scope: scope})

	req2 := executor.ApprovalRequest{
		NativeID: "perm-2",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res2, err := c2.Request(context.Background(), req2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Decision.Status == executor.ApprovalAllow {
		t.Fatal("不同 Version 不得复用 allow")
	}
	if res2.Decision.Status != executor.ApprovalPending {
		t.Fatalf("不同 Version 应重新升级 pending，got %q", res2.Decision.Status)
	}
}

// 6. 不同 TaskID 的 client 不得复用对方的 grant
func TestApprovalClientDifferentTaskDoesNotReuse(t *testing.T) {
	m, c1, _ := setupTestTaskClient(t, "T-1", "v1")
	req := executor.ApprovalRequest{
		NativeID: "perm-1",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res1, err := c1.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.st.AnswerTicket(res1.Ref.ID, "allow"); err != nil {
		t.Fatal(err)
	}
	if err := c1.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: res1.Ref, NativeID: req.NativeID, Stage: executor.AckDelivered,
	}); err != nil {
		t.Fatal(err)
	}

	// 任务 T-2
	task2 := &proto.Task{
		ID: "T-2", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake",
	}
	mustCreateTask(t, m.st, task2)
	scope2 := executor.ApprovalScope{
		Workdir:    task2.Workdir(),
		TaskDir:    taskDirOf(m, "T-2"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-2"),
	}
	c2 := m.bindApproval("T-2", executor.PolicySnapshot{Version: "v1", TaskID: "T-2", Scope: scope2})

	res2, err := c2.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Decision.Status == executor.ApprovalAllow {
		t.Fatal("不同 Task 不得复用对方的 grant")
	}
}

// 7. Acknowledge 三次：formed 后 ticket.Answer 非空或有 auto-allow 事件；delivered 后 DeliveredAt 非空；executed 不把 DeliveredAt 清掉、且与 delivered 可分
func TestApprovalClientAcknowledgeStages(t *testing.T) {
	m, c, _ := setupTestTaskClient(t, "T-stages", "v1")
	req := executor.ApprovalRequest{
		NativeID: "perm-1",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	}
	res, err := c.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.st.AnswerTicket(res.Ref.ID, "allow"); err != nil {
		t.Fatal(err)
	}

	ref := res.Ref
	// stage: formed
	if err := c.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: ref, NativeID: req.NativeID, Stage: executor.AckFormed,
	}); err != nil {
		t.Fatalf("AckFormed failed: %v", err)
	}
	tk, err := m.st.GetTicket(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Answer == nil || *tk.Answer == "" {
		t.Fatal("formed 后 ticket.Answer 必须非空")
	}
	if tk.DeliveredAt != nil {
		t.Fatal("formed 阶段 DeliveredAt 仍应为空")
	}

	// stage: delivered
	if err := c.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: ref, NativeID: req.NativeID, Stage: executor.AckDelivered,
	}); err != nil {
		t.Fatalf("AckDelivered failed: %v", err)
	}
	tk, err = m.st.GetTicket(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.DeliveredAt == nil {
		t.Fatal("delivered 后 DeliveredAt 必须非空")
	}
	delivTime := *tk.DeliveredAt

	// stage: executed
	if err := c.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: ref, NativeID: req.NativeID, Stage: executor.AckExecuted,
	}); err != nil {
		t.Fatalf("AckExecuted failed: %v", err)
	}
	tk, err = m.st.GetTicket(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.DeliveredAt == nil || *tk.DeliveredAt != delivTime {
		t.Fatal("executed 阶段不得清掉或修改 DeliveredAt")
	}
}

// 8. client 构造绑定 T1，Request 的 NativeID 只在 T1 命名空间建单
func TestApprovalClientTaskIDNamespacing(t *testing.T) {
	m, c, _ := setupTestTaskClient(t, "T-ns1", "v1")
	req := executor.ApprovalRequest{
		NativeID: "native-123",
		Text:     "bash: custom-cmd",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "custom-cmd"},
	}
	res, err := c.Request(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	expectedTicketID := "T-ns1:native-123"
	if res.Ref.ID != expectedTicketID {
		t.Fatalf("ticket ID = %q, want %q", res.Ref.ID, expectedTicketID)
	}
	tk, err := m.st.GetTicket(expectedTicketID)
	if err != nil || tk == nil {
		t.Fatalf("必须在 T-ns1 命名空间建单: %v", err)
	}
}

// 9. 注入 Judge 失败 / 审批超时：构造 Approver 让 Decide 阻塞超过 Timeout 或返回错误 -> 不得 allow
func TestApprovalClientApproverFailureOrTimeoutDoesNotAllow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 10 * time.Millisecond}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	// 注入 OneShot 返回错误
	app.BindOneShot(&stubShot{err: errors.New("approver command execution failed")})

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := agentd.NewHub()
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, hub, map[string]executor.Adapter{"fake": &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}, cfg, nil, app, newTestGate(t), logger)

	task := &proto.Task{
		ID: "T-app-fail", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake",
	}
	mustCreateTask(t, st, task)
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, "T-app-fail"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-app-fail"),
	}
	c := m.bindApproval("T-app-fail", executor.PolicySnapshot{Version: "v1", TaskID: "T-app-fail", Scope: scope})

	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-fail",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status == executor.ApprovalAllow {
		t.Fatal("Approver 失败时不得 allow，必须降级为人工 pending")
	}
	if res.Decision.Status != executor.ApprovalPending {
		t.Fatalf("status = %q want pending", res.Decision.Status)
	}
}

// 10. 审批路径不得调用 OpenCode session API：注入 Approver.BindOneShot，记录 req；断言 prompt 来自审批模板，不得出现 OpenCode session /session/ 或 PromptAsync
func TestApprovalClientDoesNotInvokeOpenCodeSessionAPI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewApprover(config.ApproverConfig{Executor: "opencode", Model: "deepseek-v4-flash", Timeout: time.Second}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	var capturedReq executor.OneShotReq
	app.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			capturedReq = req
			prompt := req.Prompt
			var nonce string
			if idx := strings.Index(prompt, "nonce="); idx != -1 {
				nonce = prompt[idx+len("nonce="):]
				if end := strings.Index(nonce, "，"); end != -1 {
					nonce = strings.TrimSpace(nonce[:end])
				}
			}
			return executor.OneShotReply{
				Text:   fmt.Sprintf(`{"decision":"approve","reason":"safe test","nonce":%q}`, nonce),
				Status: executor.OneShotOK,
			}, nil
		},
	})

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := agentd.NewHub()
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, hub, map[string]executor.Adapter{"fake": &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}, cfg, nil, app, newTestGate(t), logger)

	task := &proto.Task{
		ID: "T-oneshot", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake",
	}
	mustCreateTask(t, st, task)
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, "T-oneshot"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-oneshot"),
	}
	c := m.bindApproval("T-oneshot", executor.PolicySnapshot{Version: "v1", TaskID: "T-oneshot", Scope: scope})

	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "perm-oneshot",
		Text:     "bash: python3 script.py",
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: "python3 script.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("approver approve 应为 allow, got %q", res.Decision.Status)
	}
	if capturedReq.Prompt == "" {
		t.Fatal("Approver OneShot 必须被调用并记录 req")
	}
	if strings.Contains(capturedReq.Prompt, "/session/") || strings.Contains(capturedReq.Prompt, "PromptAsync") {
		t.Fatalf("prompt 不得包含 session 或 PromptAsync: %s", capturedReq.Prompt)
	}
	if capturedReq.Workdir != "" && strings.Contains(capturedReq.Workdir, "T-oneshot") {
		t.Fatalf("Workdir 不得是被审批任务目录: %s", capturedReq.Workdir)
	}
	if capturedReq.HomeDir != "" && strings.Contains(capturedReq.HomeDir, "T-oneshot") {
		t.Fatalf("HomeDir 不得是被审批任务目录: %s", capturedReq.HomeDir)
	}
}

// 11. 审批模型 Approve=true 时落地已送达 grant，同指纹第二次 Request 自动复用（Major 1）
func TestApprovalClientApproverAllowCreatesReusableGrantAndSecondRequestReuses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: time.Second}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	app.BindOneShot(&stubShot{
		fn: func(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
			prompt := req.Prompt
			var nonce string
			if idx := strings.Index(prompt, "nonce="); idx != -1 {
				nonce = prompt[idx+len("nonce="):]
				if end := strings.Index(nonce, "，"); end != -1 {
					nonce = strings.TrimSpace(nonce[:end])
				}
			}
			return executor.OneShotReply{
				Text:   fmt.Sprintf(`{"decision":"approve","reason":"safe test script","nonce":%q}`, nonce),
				Status: executor.OneShotOK,
			}, nil
		},
	})

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := agentd.NewHub()
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, hub, map[string]executor.Adapter{"fake": &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}, cfg, nil, app, newTestGate(t), logger)

	task := &proto.Task{
		ID: "T-app-reuse", RepoPath: t.TempDir(), WorkDir: t.TempDir(),
		State: proto.TaskStateRunning, Executor: "fake",
	}
	mustCreateTask(t, st, task)
	scope := executor.ApprovalScope{
		Workdir:    task.Workdir(),
		TaskDir:    taskDirOf(m, "T-app-reuse"),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, "T-app-reuse"),
	}
	version := "v1"
	c := m.bindApproval("T-app-reuse", executor.PolicySnapshot{Version: version, TaskID: "T-app-reuse", Scope: scope})

	cmd := "python3 script.py"
	req1 := executor.ApprovalRequest{
		NativeID: "perm-first",
		Text:     "bash: " + cmd,
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}
	res1, err := c.Request(context.Background(), req1)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("第一次模型审批应为 allow, got %q", res1.Decision.Status)
	}
	if res1.Decision.Rule != "approver" {
		t.Fatalf("第一次模型审批 rule 必须为 approver, got %q", res1.Decision.Rule)
	}

	// 断言工单已落库、严格 answer=allow、指纹已写、已送达
	expectedTicketID := "T-app-reuse:perm-first"
	tk, err := st.GetTicket(expectedTicketID)
	if err != nil || tk == nil {
		t.Fatalf("审批模型 allow 必须落 gate 工单: %v", err)
	}
	expectedFP := executor.ReuseFingerprint(version, executor.PermFingerprint(executor.AdapterEvent{
		PermissionID: "perm-first", Text: req1.Text, Perm: req1.Perm,
	}))
	if tk.Fingerprint != expectedFP {
		t.Fatalf("工单 fingerprint=%q, want %q", tk.Fingerprint, expectedFP)
	}
	if tk.Answer == nil || *tk.Answer != "allow" {
		t.Fatalf("工单 answer 必须严格等于 allow, got %v", tk.Answer)
	}
	if tk.DeliveredAt != nil {
		t.Fatalf("Request 返回 allow 时不得已送达（DeliveredAt 必须为空）")
	}
	if prior, err := st.FindReusableGrant("T-app-reuse", expectedFP); err != nil || prior != nil {
		t.Fatalf("未送达 allow 不得被复用: prior=%+v err=%v", prior, err)
	}

	// 断言事件：有 approver_decision 事件，无 permission_request 事件
	evs, err := st.EventsFromAsc("T-app-reuse", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	hasApproverDecision := false
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionRequest {
			t.Fatalf("审批模型放行不得发出 permission_request 事件")
		}
		if e.Type == proto.EventTypeApproverDecision {
			hasApproverDecision = true
		}
	}
	if !hasApproverDecision {
		t.Fatalf("审批模型放行必须记录 approver_decision 审计事件")
	}

	// P4：复用前必须显式送达；Request 本身不写 DeliveredAt。
	if err := c.Acknowledge(context.Background(), executor.ApprovalAck{
		Ref: res1.Ref, NativeID: req1.NativeID, Stage: executor.AckDelivered,
	}); err != nil {
		t.Fatal(err)
	}
	delivered, err := st.GetTicket(expectedTicketID)
	if err != nil || delivered.DeliveredAt == nil {
		t.Fatalf("AckDelivered 之后必须送达: %+v err=%v", delivered, err)
	}

	// 第二次同指纹请求（不同 NativeID）
	req2 := executor.ApprovalRequest{
		NativeID: "perm-second",
		Text:     "bash: " + cmd,
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}
	res2, err := c.Request(context.Background(), req2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("第二次同指纹请求应为 allow, got %q", res2.Decision.Status)
	}
	if res2.Decision.Rule != "reuse" {
		t.Fatalf("第二次同指纹请求 rule 必须为 reuse, got %q", res2.Decision.Rule)
	}

	// 断言第二次请求不发 permission_request，且有 permission_reuse 入库
	evs2, err := st.EventsFromAsc("T-app-reuse", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	hasReuse := false
	for _, e := range evs2 {
		if e.Type == proto.EventTypePermissionRequest {
			t.Fatalf("复用请求不得发出 permission_request 事件")
		}
		if e.Type == proto.EventTypePermissionReuse {
			hasReuse = true
		}
	}
	if !hasReuse {
		t.Fatalf("第二次请求必须记录 permission_reuse 审计事件")
	}
}

// 缝 #16（生产组装侧）：经生产 bindApproval 的 AutoAllow 钩子不得回传原生应答。
// 生产钩子是 auditAutoAllowOnly；RespondPermission 只能由 adapter 负责。
// 反向变异：bindApproval 改回 m.autoAllowPermission → recordedPerms 非空，测试红。
func TestApprovalClientAutoAllowProductionHookDoesNotRespond(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "T-auto-prod"
	work := t.TempDir()
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, WorkDir: work, Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	tmp := executor.TaskTmpDir(m.cfg.DataDir, taskID)
	command := "go test ./... > " + filepath.Join(tmp, "out")
	scope := executor.ApprovalScope{
		Workdir:    work,
		TaskDir:    taskDirOf(m, taskID),
		TaskTmpDir: tmp,
	}
	c := m.bindApproval(taskID, executor.PolicySnapshot{Version: "v1", TaskID: taskID, Scope: scope})
	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "native-auto-prod",
		Text:     "Bash: " + command,
		Perm:     &executor.PermRequest{Tool: executor.PermToolBash, Command: command},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("白名单命令应自动放行，实得 %+v", res.Decision)
	}
	if got := adapter.recordedPerms(); len(got) != 0 {
		t.Fatalf("生产 AutoAllow 钩子不得回传原生应答，实得 %v", got)
	}
	if got := countEvents(mustEvents(t, st, taskID), proto.EventTypePermissionAutoAllow); got != 1 {
		t.Fatalf("自动放行审计事件 = %d，want 1", got)
	}
	if _, err := st.GetTicket(taskID + ":native-auto-prod"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("免审不得建工单，err=%v", err)
	}
	if got := m.takeAutoAllowed(taskID); got != 1 {
		t.Fatalf("P3：计数应跟审计走，实得 %d", got)
	}
}
