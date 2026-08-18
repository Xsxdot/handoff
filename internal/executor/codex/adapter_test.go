package codex_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
)

func drain(t *testing.T, ch <-chan executor.AdapterEvent, d time.Duration) []executor.AdapterEvent {
	t.Helper()
	var out []executor.AdapterEvent
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}

// finish trailer → result(OK)，且 branch/commit 以 trailer 为先
func TestFinishTrailerEmitsOKResult(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T1")
	// 收尾 trailer 是单行 JSON（turn.ParseTrailer 只认以 { 开头的行），
	// 计划原稿误用了 HANDOFF_STATUS: 纯文本行，那会被 ParseTrailer 判成 none
	body := "干完了。\n" + `{"branch":"handoff/T1","commit":"abc1234","summary":"加了 codex adapter"}`
	codex.FinishTurnForTest(a, r, "completed", "", body)

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) == 0 {
		t.Fatal("没有产出任何事件")
	}
	last := evs[len(evs)-1]
	if last.Type != "result" || last.Result == nil || !last.Result.OK {
		t.Fatalf("应产出成功结果，实得 %+v", last)
	}
	if last.Result.Branch != "handoff/T1" || last.Result.CommitHash != "abc1234" {
		t.Fatalf("trailer 的 branch/commit 应优先，实得 %+v", last.Result)
	}
}

// 被拒清单优先于 trailer：模型被拒后可能悄悄绕路，人必须知情
func TestRejectedListTakesPriority(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T2")
	codex.NoteRejectedOnRunForTest(r, "运行命令：rm -rf /etc")
	codex.FinishTurnForTest(a, r, "completed", "",
		"HANDOFF_STATUS: finish\nHANDOFF_SUMMARY: 完成")

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Type != "question" {
		t.Fatalf("被拒时应只产出一条 question，实得 %+v", evs)
	}
	if !strings.Contains(evs[0].Text, "rm -rf /etc") {
		t.Fatalf("问题正文必须含被拒描述，实得: %s", evs[0].Text)
	}
}

// 回合 failed → 失败结果带上 codex 给的真因（B16：不许扁平化）
func TestTurnFailedCarriesCause(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T3")
	codex.FinishTurnForTest(a, r, "failed", "model stream aborted", "")

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Result == nil || evs[0].Result.OK {
		t.Fatalf("应产出失败结果，实得 %+v", evs)
	}
	if !strings.Contains(evs[0].Result.FailReason, "model stream aborted") {
		t.Fatalf("失败原因必须带 codex 给的真因，实得: %s", evs[0].Result.FailReason)
	}
}

// 主动停止时收到的 interrupted 不是失败
func TestInterruptedWhileStoppingIsNotFailure(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T4")
	codex.MarkStoppingForTest(r)
	codex.FinishTurnForTest(a, r, "interrupted", "", "")

	for _, ev := range drain(t, codex.EventsForTest(r), 300*time.Millisecond) {
		if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
			t.Fatalf("主动停止不得产出失败结果，实得 %+v", ev.Result)
		}
	}
}

// fileChange 权限：索引查不到时 Perm 必须为 nil（manager 据此 fail-closed）
func TestFileChangeApprovalFailsClosedWithoutIndexEntry(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T5")
	h := codex.NewHandlerForTest(a, r)
	ok := h.OnServerRequest(json.RawMessage("9"), "item/fileChange/requestApproval",
		json.RawMessage(`{"itemId":"patch-unknown","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("fileChange 审批必须被本端接管，不能回 -32601")
	}
	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Type != "permission" {
		t.Fatalf("应产出一条 permission 事件，实得 %+v", evs)
	}
	if evs[0].PermissionID != "patch-unknown" {
		t.Fatalf("PermissionID 应为 itemId，实得 %s", evs[0].PermissionID)
	}
	if evs[0].Perm != nil {
		t.Fatalf("索引未命中时 Perm 必须为 nil（fail-closed），实得 %+v", evs[0].Perm)
	}
}

// 索引里有 item 时，权限事件带上结构化路径
func TestFileChangeApprovalUsesIndexedPaths(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T6")
	h := codex.NewHandlerForTest(a, r)
	h.OnNotify("item/started", json.RawMessage(
		`{"item":{"type":"fileChange","id":"patch-1","changes":[{"path":"/w/a.go","kind":{"type":"update"}}]}}`))
	ok := h.OnServerRequest(json.RawMessage("3"), "item/fileChange/requestApproval",
		json.RawMessage(`{"itemId":"patch-1","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("应被接管")
	}
	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	var perm *executor.AdapterEvent
	for i := range evs {
		if evs[i].Type == "permission" {
			perm = &evs[i]
		}
	}
	if perm == nil || perm.Perm == nil {
		t.Fatalf("应产出带 Perm 的 permission 事件，实得 %+v", evs)
	}
	if perm.Perm.Tool != executor.PermToolEdit || len(perm.Perm.Paths) != 1 ||
		perm.Perm.Paths[0] != "/w/a.go" {
		t.Fatalf("Perm = %+v", perm.Perm)
	}
}

// permissions 升级申请一律 fail-closed：不产权限门，只产 progress
func TestPermissionsEscalationIsFailClosedNotAGate(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T7")
	codex.AttachFakeClientForTest(r) // 这条路径会回发应答，需要一条假连接
	h := codex.NewHandlerForTest(a, r)
	ok := h.OnServerRequest(json.RawMessage("4"), "item/permissions/requestApproval",
		json.RawMessage(`{"itemId":"perm-1","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("应被接管（否则 codex 侧挂起）")
	}
	for _, ev := range drain(t, codex.EventsForTest(r), 300*time.Millisecond) {
		if ev.Type == "permission" {
			t.Fatalf("沙箱放宽申请绝不能做成可批准的权限门，实得 %+v", ev)
		}
	}
}

// 401 令牌刷新请求 → 任务失败并给出可操作指引
func TestAuthRefreshFailsTaskWithActionableMessage(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T8")
	codex.AttachFakeClientForTest(r) // 这条路径会回发错误应答，需要一条假连接
	h := codex.NewHandlerForTest(a, r)
	if ok := h.OnServerRequest(json.RawMessage("5"), "account/chatgptAuthTokens/refresh",
		json.RawMessage(`{"reason":"unauthorized"}`)); !ok {
		t.Fatal("应被接管（要回错误，不能静默）")
	}
	var failed *executor.Result
	for _, ev := range drain(t, codex.EventsForTest(r), 500*time.Millisecond) {
		if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
			failed = ev.Result
		}
	}
	if failed == nil {
		t.Fatal("登录态失效必须让任务失败，不能静默继续")
	}
	if !strings.Contains(failed.FailReason, "codex login") {
		t.Fatalf("失败文案必须给出可操作指引，实得: %s", failed.FailReason)
	}
}

// 运行态不存在时三个动作都必须带 ErrTaskNotRunning 哨兵
func TestActionsCarryNotRunningSentinel(t *testing.T) {
	a := codex.New(nil)
	ctx := t.Context()
	if err := a.Send(ctx, "nope", "hi"); !isNotRunning(err) {
		t.Fatalf("Send: %v", err)
	}
	if err := a.RespondPermission(ctx, "nope", "p", "once", ""); !isNotRunning(err) {
		t.Fatalf("RespondPermission: %v", err)
	}
	if err := a.Stop("nope"); !isNotRunning(err) {
		t.Fatalf("Stop: %v", err)
	}
}

func isNotRunning(err error) bool {
	return err != nil && errorsIs(err, executor.ErrTaskNotRunning)
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }
