package claudecode

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
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
	if err := a.RespondPermission(t.Context(), "no-such-task", "p", "once"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("RespondPermission 应包装 ErrTaskNotRunning，实际 %v", err)
	}
	if err := a.Stop("no-such-task"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("Stop 应包装 ErrTaskNotRunning，实际 %v", err)
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
