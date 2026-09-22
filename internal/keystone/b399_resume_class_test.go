// b399_resume_class_test.go —— B399 r2：resume 错误分流（超时保留会话，只有
// Session not found 才重建）。
//
// 职责：钉住 Wake 对两类 resume 错误的不同出口——超时/其他不 Launch、会话保留，
// 下一轮仍以同一 session resume；Session not found 才 Launch。
// 缝：keystone.Service.Wake（生产入口）；夹具复用 slice_test.go 的 fakeRunner/
// fakeNarrator/recordingLedger（fakeRunner 的 resumeErr/refs 字段见 T4.1）。
// 边界：不复制 agentd 的回合收口规则；只观察 Launch/Resume 调用与回执。
package keystone_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// resumeClassSeed 建一张有 coordinate 席位的卡 + 真实账本门面，返回 (service, cardID, runner)。
func resumeClassSeed(t *testing.T) (*keystone.Service, string, *fakeRunner) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	if _, err := st.PutWorkflow("rc", ledger.WorkflowDef{States: []string{"进行中", "已完成"}}); err != nil {
		t.Fatalf("建工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "分流卡", Project: "handoff", Workflow: "rc", BaseBranch: "main", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#sess-old", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写席位: %v", err)
	}
	runner := &fakeRunner{}
	return keystone.New(runner, &fakeNarrator{}, &recordingLedger{f: facade}, nil), card.ID, runner
}

// TestB399ResumeTimeoutKeepsSessionNoLaunch 锁 r2 §5①：resume 报「回合超时」时
// 不调用 Launch，回执保留原会话；下一轮仍以同一 session resume。
//
// 红（当前 HEAD）：任何 resume 错误都走 rebuildAfterResumeFailure → Launch，
// launches≠0；新语义应 launches==0。
// 绿（T2.1/T2.2）：超时 → failResumeKeepingSession，launches 恒 0。
// 变异：把 errors.Is(err, keysclient.ErrSessionNotFound) 分支删掉、无条件 rebuild → 复红。
func TestB399ResumeTimeoutKeepsSessionNoLaunch(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)
	runner.failNext = 99
	runner.resumeErr = fmt.Errorf("resume 不可用: %w", keysclient.ErrTurnTimeout)

	spec := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"}
	result, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "x"},
	}, spec)
	if err == nil {
		t.Fatalf("超时 resume 应返回错误")
	}
	if !errors.Is(err, keysclient.ErrTurnTimeout) {
		t.Fatalf("错误必须穿透 keysclient.ErrTurnTimeout: %v", err)
	}
	if len(runner.launches) != 0 {
		t.Fatalf("超时不得重建（Launch 次数=%d）", len(runner.launches))
	}
	if len(runner.refs) != 1 {
		t.Fatalf("应尝试一次 Resume，实得 %d", len(runner.refs))
	}
	if result.SessionID != "sess-old" {
		t.Fatalf("回执应保留原会话 sess-old，实得 %q", result.SessionID)
	}

	// 下一轮仍续接同一 session：failNext 放行一次，断言 ref.SessionID 仍是 sess-old。
	runner.failNext = 0
	if _, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "y"},
	}, spec); err != nil {
		t.Fatalf("下一轮续接应成功: %v", err)
	}
	if len(runner.refs) == 0 || runner.refs[len(runner.refs)-1].SessionID != "sess-old" {
		t.Fatalf("下一轮必须续接同一 session sess-old，实得 %+v", runner.refs)
	}
}

// TestB399ResumeNotFoundTriggersLaunch 锁 r2 §5②：只有 stderr 判据
// keysclient.ErrSessionNotFound 才降级 Launch 重建。
//
// 红（当前 HEAD）：generic 与 not-found 无区分；本测试在旧码上会「恰好也重建」，
// 与 TestB399ResumeTimeoutKeepsSessionNoLaunch 成对才钉住分流（旧码必红于后者）。
// 绿（T2.1）：not-found → rebuildAfterResumeFailure → Launch。
func TestB399ResumeNotFoundTriggersLaunch(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)
	runner.failNext = 99
	runner.resumeErr = fmt.Errorf("resume 不可用: %w", keysclient.ErrSessionNotFound)

	result, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "x"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"})
	if err != nil {
		t.Fatalf("not-found 应降级重建成功: %v", err)
	}
	if len(runner.launches) != 1 {
		t.Fatalf("not-found 必须重建（Launch 次数=%d）", len(runner.launches))
	}
	if !result.Rebuilt || result.SessionID != "sess-new" {
		t.Fatalf("重建回执不完整: %+v", result)
	}
}
