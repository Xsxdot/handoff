package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// 事件映射：init → progress 带 SessionID
func TestMapInitEmitsSessionID(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "system", Subtype: "init", SessionID: "sess-9"})
	ev := mustRecv(t, r)
	if ev.Type != "progress" || ev.SessionID != "sess-9" {
		t.Fatalf("init 应产出带 SessionID 的 progress，实际 %+v", ev)
	}
}

// 回合收尾（trailer=finish）→ result，且带 git 取证
func TestMapResultFinishEmitsResult(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "result", Subtype: "success",
		Result: `{"branch":"handoff/ab","commit":"c0ffee","summary":"完成"}`})
	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("finish trailer 应产出成功 result，实际 %+v", ev)
	}
	if ev.Result.Branch != "handoff/ab" || ev.Result.CommitHash != "c0ffee" {
		t.Errorf("git 字段未透传: %+v", ev.Result)
	}
}

// 回合收尾（trailer=ask）→ question
func TestMapResultAskEmitsQuestion(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "result", Subtype: "success",
		Result: `{"ask":"用 pgx 还是 gorm？"}`})
	ev := mustRecv(t, r)
	if ev.Type != "question" || ev.Text != "用 pgx 还是 gorm？" {
		t.Fatalf("ask trailer 应产出 question，实际 %+v", ev)
	}
}

// 死亡哨兵 → 失败 result（非零退出码）
func TestMapExitSentinelEmitsFailure(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "handoff_exit", ExitCode: 137})
	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
		t.Fatalf("哨兵应产出失败 result，实际 %+v", ev)
	}
	if ev.Result.FailReason == "" {
		t.Error("失败原因不得为空（协调者要靠它判断怎么处置）")
	}
}

// 权限请求 → permission 事件，PermissionID 必须是裸 tool_use_id
func TestPermissionEventUsesRawToolUseID(t *testing.T) {
	a, r := newTestRun(t)
	a.onPermissionAsk(r, permAsk{ToolUseID: "toolu_7", ToolName: "Bash",
		Input: []byte(`{"command":"rm -rf x"}`)})
	ev := mustRecv(t, r)
	if ev.Type != "permission" || ev.PermissionID != "toolu_7" {
		t.Fatalf("PermissionID 必须是裸 tool_use_id，实际 %+v", ev)
	}
	if ev.Text == "" {
		t.Error("权限描述不得为空（协调者要靠它决定批不批）")
	}
}

// 契约：任务不在运行中时，Send/RespondPermission/Stop 必须包装哨兵错误
func TestNotRunningWrapsSentinel(t *testing.T) {
	a := New(nil)
	if err := a.Send(t.Context(), "no-such-task", "x"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("Send 应包装 ErrTaskNotRunning，实际 %v", err)
	}
	if err := a.RespondPermission(t.Context(), "no-such-task", "p", "once", ""); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("RespondPermission 应包装 ErrTaskNotRunning，实际 %v", err)
	}
	if err := a.Stop("no-such-task"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("Stop 应包装 ErrTaskNotRunning，实际 %v", err)
	}
}

// respondAndRead 起裁决 socket、装好 runState、发一条 ask，调 RespondPermission
// 后把回发的裁决读出来。脚手架沿用 perm_test.go 既有的 newPermServer + dialAsk，
// 不另起一套 mock。
func respondAndRead(t *testing.T, decision, reason string) (behavior, message string) {
	return respondAndReadWithLogger(t, decision, reason,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func respondAndReadWithLogger(t *testing.T, decision, reason string, logger *slog.Logger) (behavior, message string) {
	t.Helper()
	// macOS Unix socket 路径上限很短；工作区测试临时目录本身已含长的测试名，
	// 即使缩短文件名也可能在 bind 前失败。短目录放在当前包下并由测试清理。
	sockDir, err := os.MkdirTemp(".", "p")
	if err != nil {
		t.Fatalf("创建短 socket 目录: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "p")
	asked := make(chan permAsk, 1)
	srv, err := newPermServer(sock, slog.Default(), func(ask permAsk) { asked <- ask })
	if err != nil {
		t.Fatalf("newPermServer: %v", err)
	}
	defer srv.Close()

	a := New(logger)
	a.runs["T1"] = &runState{
		taskID: "T1", perm: srv,
		evCh: make(chan executor.AdapterEvent, 4), stopCh: make(chan struct{}),
	}
	conn := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"rm -rf x"}`)
	defer conn.Close()
	select {
	case <-asked:
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到 ask")
	}

	if err := a.RespondPermission(context.Background(), "T1", "toolu_1", decision, reason); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	var got struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&got); err != nil {
		t.Fatalf("读裁决: %v", err)
	}
	return got.Behavior, got.Message
}

// TestRespondPermissionApprovalDoesNotLogDeny 审批通过只应回发 allow，不能污染拒绝日志。
// 这是日志分支回归测试：拒绝日志若仍在 deny 分支外，本用例会在现状下失败。
func TestRespondPermissionApprovalDoesNotLogDeny(t *testing.T) {
	var logs bytes.Buffer
	_, _ = respondAndReadWithLogger(t, "once", "", slog.New(slog.NewTextHandler(&logs, nil)))
	if strings.Contains(logs.String(), "claude 回发拒绝裁决") {
		t.Fatalf("批准不应产生拒绝裁决日志，实际日志：%s", logs.String())
	}
}

// TestRespondPermissionCarriesReason 钉住 B137 主修：协调者的理由必须原样进
// permDecision.Message，而不是被换成一句通用话。通道早就是通的
// （perm.go 的 Message 字段 → cmd/permission_mcp.go 回给模型），
// 此前断在 adapter 把它写死成常量。
func TestRespondPermissionCarriesReason(t *testing.T) {
	const reason = "别删，先 git mv 归档"
	behavior, message := respondAndRead(t, "reject", reason)
	if behavior != "deny" {
		t.Fatalf("behavior = %q，期望 deny", behavior)
	}
	if want := turn.DenyGuidanceText(reason); message != want {
		t.Fatalf("message = %q，期望 %q", message, want)
	}
}

// TestRespondPermissionEmptyReasonFallsBack 协调者没给理由时不能送一句空的：
// 空 message 会让模型以为「理由缺失」本身是异常，通用句才是对的兜底。
func TestRespondPermissionEmptyReasonFallsBack(t *testing.T) {
	behavior, message := respondAndRead(t, "reject", "   ")
	if behavior != "deny" {
		t.Fatalf("behavior = %q，期望 deny", behavior)
	}
	if message != "协调者拒绝了本次操作" {
		t.Fatalf("message = %q，期望回退到通用句", message)
	}
}

// TestDenyReasonInBand claude 必须自报「理由已同帧送达」，否则 manager 会再走
// 一遍带外注入，模型被同一条理由说两遍。
func TestDenyReasonInBand(t *testing.T) {
	if !New(nil).DenyReasonInBand() {
		t.Fatal("claude adapter 必须返回 true")
	}
}

// newTestRun 构造一个带缓冲事件通道的运行态（不起真进程、不建 socket）。
func newTestRun(t *testing.T) (*Adapter, *runState) {
	t.Helper()
	a := New(nil)
	r := &runState{
		taskID:   "T-1",
		taskDir:  t.TempDir(),
		repoPath: "/repo",
		evCh:     make(chan executor.AdapterEvent, 16),
		stopCh:   make(chan struct{}),
		ready:    make(chan struct{}),
	}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	a.mu.Lock()
	a.runs["T-1"] = r
	a.mu.Unlock()
	t.Cleanup(func() {
		a.mu.Lock()
		delete(a.runs, "T-1")
		a.mu.Unlock()
		r.runCancel()
	})
	return a, r
}

// mustRecv 从事件通道取一条事件，2 秒超时即失败。
func mustRecv(t *testing.T, r *runState) executor.AdapterEvent {
	t.Helper()
	select {
	case ev := <-r.evCh:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到事件")
		return executor.AdapterEvent{}
	}
}

// TestFallbackClassifyEmptyTextEmitsFailedResult 兜底分支的空文本守卫。
//
// 旧实现在无新提交时 emit question 携带回合文本，文本为空时产出的是一张**空工单**
// ——协调者收到一个没有内容的问题，除了瞎猜什么也做不了。零文本是故障，按故障报。
func TestFallbackClassifyEmptyTextEmitsFailedResult(t *testing.T) {
	a, r := newTestRun(t)
	r.session = "sess-1"
	// repoPath=/repo 不是 git 仓库，GitTurnStatus 失败 → hasNew=false，走进兜底
	a.fallbackClassify(r, "")

	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
		t.Fatalf("零文本且无新提交应产出失败结果，实际 %s %+v", ev.Type, ev.Result)
	}
	if ev.Result.FailReason == "" {
		t.Fatalf("FailReason 必须写清现场，否则协调者不知道发生了什么")
	}
}

// B80 缺口修复：result 行的 modelUsage 提供窗口。mapResult 必须把窗口存到
// runState 而不是当场 emit——单独发一条 usage 会把 manager 已落库的 model/tokens
// 三元组冲成空。
func TestMapResultStoresCtxWindowWithoutEmitting(t *testing.T) {
	a, r := newTestRun(t)
	r.actualModel = "k3-256k"
	a.mapMessage(r, streamMsg{
		Type: "result", Subtype: "success",
		Result: `{"branch":"handoff/ab","commit":"c0ffee","summary":"完成"}`,
		ModelUsage: map[string]modelUsage{
			"k3-256k": {ContextWindow: 262144, CanonicalModel: "k3-256k"},
		},
	})
	// 唯一应产出的事件是回合收尾的 result，绝不产出 usage
	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("应产出成功 result，实际 %+v", ev)
	}
	if r.ctxWindow != 262144 {
		t.Fatalf("窗口应存到 runState，得到 %d", r.ctxWindow)
	}
	select {
	case extra := <-r.evCh:
		t.Fatalf("result 行不应产出额外事件（尤其不能是 usage），得到 %+v", extra)
	default:
	}
}

// 窗口挂上后，assistant 消息的用量事件必须带 ContextWindow（否则界面永远只显绝对值）。
func TestAssistantUsageCarriesCtxWindow(t *testing.T) {
	a, r := newTestRun(t)
	r.ctxWindow = 262144 // 模拟第一个回合已结束、窗口已暂存
	a.mapMessage(r, streamMsg{
		Type: "assistant",
		Message: []byte(`{"model":"k3-256k","usage":{"input_tokens":100,
      "cache_read_input_tokens":50,"cache_creation_input_tokens":0}}`),
	})
	ev := mustRecv(t, r)
	if ev.Type != "usage" || ev.Usage == nil || ev.Usage.ContextTokens != 150 {
		t.Fatalf("应产出带分子 150 的 usage 事件，实际 %+v", ev)
	}
	if ev.Usage.ContextWindow == nil || *ev.Usage.ContextWindow != 262144 {
		t.Fatalf("assistant 用量事件应带 r.ctxWindow 窗口，实际 %+v", ev.Usage.ContextWindow)
	}
}

// 第一个回合结束前窗口还是 nil：assistant 事件只带分子，ContextWindow 保持 nil。
func TestAssistantUsageNilBeforeFirstResult(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{
		Type:    "assistant",
		Message: []byte(`{"model":"k3-256k","usage":{"input_tokens":100}}`),
	})
	ev := mustRecv(t, r)
	if ev.Usage == nil || ev.Usage.ContextWindow != nil {
		t.Fatalf("首个回合结束前 ContextWindow 必须是 nil，实际 %+v", ev.Usage)
	}
}
