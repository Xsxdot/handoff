package agentd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func taskDirOf(m *Manager, taskID string) string {
	return filepath.Join(m.cfg.DataDir, "tasks", taskID)
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
	// 注入 runCmd 返回错误
	app.runCmd = func(ctx context.Context, argv []string) (string, error) {
		return "", errors.New("approver command execution failed")
	}

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
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

// 10. 审批路径不得调用 OpenCode session API：注入 Approver.runCmd，记录 argv；断言 argv 来自 executor.OneShotArgs，不得出现 OpenCode session /session/ 或 PromptAsync
func TestApprovalClientDoesNotInvokeOpenCodeSessionAPI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app, err := NewApprover(config.ApproverConfig{Executor: "opencode", Model: "deepseek-v4-flash", Timeout: time.Second}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	var capturedArgv []string
	app.runCmd = func(ctx context.Context, argv []string) (string, error) {
		capturedArgv = argv
		prompt := argv[len(argv)-1]
		var nonce string
		if idx := strings.Index(prompt, "nonce="); idx != -1 {
			nonce = prompt[idx+len("nonce="):]
			if end := strings.Index(nonce, "，"); end != -1 {
				nonce = strings.TrimSpace(nonce[:end])
			}
		}
		return fmt.Sprintf(`{"decision":"approve","reason":"safe test","nonce":%q}`, nonce), nil
	}

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
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
	if len(capturedArgv) == 0 {
		t.Fatal("Approver.runCmd 必须被调用并记录 argv")
	}
	joinedArgv := strings.Join(capturedArgv, " ")
	if strings.Contains(joinedArgv, "/session/") || strings.Contains(joinedArgv, "PromptAsync") {
		t.Fatalf("argv 不得包含 session 或 PromptAsync: %s", joinedArgv)
	}
}
