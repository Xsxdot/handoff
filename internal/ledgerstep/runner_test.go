package ledgerstep

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
	_ "modernc.org/sqlite"
)

func dispatchRunner(t *testing.T, st *ledger.Store, transport func(context.Context, DispatchOpts) (string, string, error)) *StepRunner {
	t.Helper()
	return &StepRunner{
		St: st, Session: "session-runner", Target: "mac-02",
		RunHolder: "run:test-host#4242#1", RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor", Transport: transport},
	}
}

func TestRunnerRejectsUnknownNode(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	runner := &StepRunner{St: st}
	_, err := runner.Run(context.Background(), card, "查无此节点")
	if err == nil {
		t.Fatalf("节点名不在卡钉的工作流里，应报错")
	}
	if !strings.Contains(err.Error(), "查无此节点") {
		t.Fatalf("错误里应带上节点名，实际: %v", err)
	}
}

func TestRunnerFindsNodeInPinnedWorkflowVersion(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("nodeflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: "待办", Next: "进行中"},
		{Name: "进行中"},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "找节点", Project: "p", Workflow: "nodeflow", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	runner := &StepRunner{St: st}
	// 「待办」在工作流里存在但没有 Dispatch 能力：应报「不可执行」而不是「找不到」。
	_, err = runner.Run(context.Background(), card.ID, "待办")
	if err == nil || !strings.Contains(err.Error(), "Dispatch") {
		t.Fatalf("应报缺 Dispatch 能力，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "老定义") || !strings.Contains(err.Error(), "人工列") {
		t.Fatalf("缺少老定义/人工列的原因指引，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "handoff workflow put") || !strings.Contains(err.Error(), "nodeflow") {
		t.Fatalf("缺少 workflow put 修复指引，实际: %v", err)
	}
}

func TestRunnerPassesNodePurposeAndAcceptanceSwitch(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("runnerflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: "待办", Next: "待审阅"},
		{
			Name: "待审阅", Dispatch: true, Template: "feature-impl", CarryCardContext: true,
			OmitAcceptance: true, Override: ledger.NodeOverride{Purpose: ledger.PurposeReview},
		},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "runner 透传", Project: "p", Workflow: "runnerflow", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	const criteria = "go test ./... 全绿且真机跑通"
	if err := st.SetAcceptance(card.ID, criteria, "test"); err != nil {
		t.Fatalf("SetAcceptance: %v", err)
	}
	card, err = st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("GetCard: %v", err)
	}

	var dispatched []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		dispatched = append(dispatched, opts)
		return fmt.Sprintf("T-runner-%d", len(dispatched)), "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("先派实现轮: %v", err)
	}
	runner := &StepRunner{St: st, Dispatcher: d, Target: "mac-02", RunHolder: "run:test-host#4242#2"}
	if _, err := runner.Run(context.Background(), card.ID, "待审阅"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dispatched) != 2 {
		t.Fatalf("应有实现轮和 runner 审阅轮两次派发，实得 %d", len(dispatched))
	}
	if want := "cards/" + card.ID + "-review-1"; dispatched[1].Branch != want {
		t.Fatalf("runner 应传用途并走审阅分支 %q，实得 %q", want, dispatched[1].Branch)
	}
	if strings.Contains(dispatched[1].Prompt, criteria) {
		t.Fatalf("runner 应传递 OmitAcceptance，prompt 不应含判据 %q：\n%s", criteria, dispatched[1].Prompt)
	}
}

// TestRunnerExecutorModelOverridePriorityAndPairRule 验证节点覆盖与 CLI 覆盖的优先级，
// 并确认同层 executor 覆盖会切断更低层 model。
func TestRunnerExecutorModelOverridePriorityAndPairRule(t *testing.T) {
	const target = "mac-02"
	st, card := dispatchTestCard(t)
	setTemplateModel(t, st, target, "template-model")
	var got DispatchOpts
	call := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		call++
		return fmt.Sprintf("T-runner-override-%d", call), "", nil
	}}
	node := ledger.NodeDef{
		Name: "进行中", Dispatch: true, Template: "feature-impl",
		Override: ledger.NodeOverride{Executor: "node-executor", Model: "node-model"},
	}
	runner := &StepRunner{St: st, Dispatcher: d, Target: target}
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("节点覆盖派发: %v", err)
	}
	if got.Executor != "node-executor" || got.Model != "node-model" {
		t.Fatalf("节点覆盖 executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "node-executor", "node-model")
	}

	node.Override.Model = ""
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("节点成对规则派发: %v", err)
	}
	if got.Executor != "node-executor" || got.Model != "" {
		t.Fatalf("节点只覆盖 executor 时 executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "node-executor", "")
	}

	runner.Executor = "cli-executor"
	runner.Model = ""
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("CLI 成对规则派发: %v", err)
	}
	if got.Executor != "cli-executor" || got.Model != "" {
		t.Fatalf("CLI 只覆盖 executor 时 executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "cli-executor", "")
	}

	// 上一段里节点的 model 已经是空的，所以它区分不开「切断下层模型」与「沿用
	// 下层模型」——两条分支的结果都是空。把节点的 model 放回去再验一次，成对
	// 规则才真的被钉住：CLI 换掉执行器且不给模型时，节点层的模型必须被切断，
	// 否则一个模型名会被套到另一个执行器上（跨执行器复用模型名，第一个事件就是 400）。
	node.Override.Model = "node-model"
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("CLI 换执行器且节点有模型时派发: %v", err)
	}
	if got.Executor != "cli-executor" || got.Model != "" {
		t.Fatalf("CLI 换执行器且节点有模型时 executor/model = %q/%q, want %q/%q",
			got.Executor, got.Model, "cli-executor", "")
	}

	runner.Model = "cli-model"
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("CLI 双覆盖派发: %v", err)
	}
	if got.Executor != "cli-executor" || got.Model != "cli-model" {
		t.Fatalf("CLI 双覆盖 executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "cli-executor", "cli-model")
	}
}

// TestRunnerSameExecutorKeepsNodeModel ensures a CLI executor spelling that
// matches the node layer does not discard that layer's model.
func TestRunnerSameExecutorKeepsNodeModel(t *testing.T) {
	const target = "mac-02"
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-runner-same-executor", "", nil
	}}
	node := ledger.NodeDef{
		Name: "进行中", Dispatch: true, Template: "feature-impl",
		Override: ledger.NodeOverride{Executor: "opencode", Model: "node-model"},
	}
	runner := &StepRunner{
		St: st, Dispatcher: d, Target: target,
		Executor: "opencode", Model: "",
	}
	if _, _, err := runner.dispatchNode(nil)(context.Background(), card, node); err != nil {
		t.Fatalf("同 executor 节点覆盖派发: %v", err)
	}
	if got.Executor != "opencode" || got.Model != "node-model" {
		t.Fatalf("same executor executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "opencode", "node-model")
	}
}

func TestRunnerKeepsRunLockWithoutChangingSeat(t *testing.T) {
	st, card := nodeLedger(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		close(started)
		<-finish
		return "T-driver", "", nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
		done <- err
	}()
	<-started
	claimed, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读运行中卡: %v", err)
	}
	if claimed.DriverSession != "" || claimed.DriverSource != "" {
		t.Fatalf("运行中不得写协调者席位: %+v", claimed)
	}
	lockRow, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok || lockRow.Holder != runner.RunHolder {
		t.Fatalf("运行中应有 RunHolder 锁行: ok=%v err=%v row=%+v", ok, err, lockRow)
	}
	if claimed.Status != ledger.StatusTodo {
		t.Fatalf("节点流不应把状态改成进行中，实际 %q", claimed.Status)
	}

	close(finish)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	released, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读收口卡: %v", err)
	}
	if _, ok, _ := st.RunLockOf(card.ID); ok {
		t.Fatal("回合结束运行锁行应消失")
	}
	if released.DriverSession != "" || released.DriverSource != "" {
		t.Fatalf("回合结束不得写协调者席位: %+v", released)
	}
}

func TestRunnerDoesNotOverwriteExistingSeat(t *testing.T) {
	st, card := nodeLedger(t)
	if err := st.BindSeat(card.ID, "cli:codex#holder", proto.SeatSourceBind); err != nil {
		t.Fatalf("预先认领: %v", err)
	}
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "T-existing-seat", "", nil
	})

	if _, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing); err != nil {
		t.Fatalf("已有席位不应阻止运行锁节点: %v", err)
	}
	stillHeld, getErr := st.GetCard(card.ID)
	if getErr != nil {
		t.Fatalf("读冲突卡: %v", getErr)
	}
	if stillHeld.DriverSession != "cli:codex#holder" || stillHeld.DriverSource != string(proto.SeatSourceBind) {
		t.Fatalf("节点执行不应改写既有席位: %+v", stillHeld)
	}
}

func TestRunnerKeepsEmptySeatAfterDispatchFailure(t *testing.T) {
	st, card := nodeLedger(t)
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "", "", fmt.Errorf("目标机不可达")
	})

	if _, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing); err != nil {
		t.Fatalf("派发失败应转等人而不是 Run 错误: %v", err)
	}
	got, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读失败收口卡: %v", err)
	}
	if _, ok, _ := st.RunLockOf(card.ID); ok {
		t.Fatal("失败回合运行锁行也应消失")
	}
	if got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("失败回合不得写协调者席位: %+v", got)
	}
}

func TestRunnerRendersDeclaredOutputPathAndInjectsPrompt(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("output-runner", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "plan"},
		{
			Name: "plan", Dispatch: true, Verdict: true, Template: "feature-impl",
			Next: ledger.StatusReview, OmitAcceptance: true,
			Produces: &ledger.NodeOutput{
				Kind: "doc", Path: "docs/{{DATE}}/{{CARD_LOWER}}-{{NODE}}.md",
			},
		},
		{Name: ledger.StatusReview},
	}}); err != nil {
		t.Fatalf("写 output workflow: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "runner 产物", Project: "p", Workflow: "output-runner", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var opts DispatchOpts
	d := &Dispatcher{
		St: st, Actor: "runner-test",
		Transport: func(ctx context.Context, got DispatchOpts) (string, string, error) {
			opts = got
			return "task-output-runner", "", nil
		},
	}
	runner := &StepRunner{
		St: st, Dispatcher: d, Session: "output-runner-session", Target: "linux-01",
		Now: func() time.Time { return time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC) },
	}
	path := ""
	node := stMustNode(t, st, card.ID, "plan")
	target, taskID, err := runner.dispatchNode(&path)(context.Background(), card, node)
	if err != nil {
		t.Fatalf("dispatchNode: %v", err)
	}
	if target != "linux-01" || taskID != "task-output-runner" {
		t.Fatalf("dispatch 返回: target=%q task=%q", target, taskID)
	}
	wantPath := "docs/2026-08-23/" + strings.ToLower(card.ID) + "-plan.md"
	if path != wantPath || opts.OutputPath != wantPath {
		t.Fatalf("渲染路径: holder=%q opts=%q want=%q", path, opts.OutputPath, wantPath)
	}
	if !strings.Contains(opts.Prompt, "## 本节点产出物") || !strings.Contains(opts.Prompt, wantPath) {
		t.Fatalf("prompt 未注入法定路径:\n%s", opts.Prompt)
	}
}

func assertHaltOnCard(t *testing.T, st *ledger.Store, cardID, wantSub string) {
	t.Helper()
	events, err := st.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	comment, flagged := false, false
	for _, e := range events {
		switch e.Type {
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && strings.Contains(p.Body, wantSub) {
				comment = true
			}
		case ledger.EvNeedsHuman:
			flagged = true
		}
	}
	if !comment || !flagged {
		t.Fatalf("卡上应有含 %q 的评论与 needs_human: comments=%v flagged=%v events=%+v", wantSub, comment, flagged, events)
	}
}

func TestRunnerUnknownNodeHaltsWithCardEvent(t *testing.T) {
	st, card := nodeLedger(t)
	runner := &StepRunner{St: st}
	_, err := runner.Run(context.Background(), card.ID, "查无此节点")
	if err == nil || !strings.Contains(err.Error(), "查无此节点") {
		t.Fatalf("节点解不开应报错并带节点名: %v", err)
	}
	assertHaltOnCard(t, st, card.ID, "查无此节点")
}

func TestRunnerMissingSessionHaltsWithCardEvent(t *testing.T) {
	st, card := nodeLedger(t)
	runner := &StepRunner{St: st, RunHolder: "run:x#1#1", Dispatcher: &Dispatcher{St: st, Actor: ""}}
	_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err == nil {
		t.Fatal("会话未设置应报错")
	}
	assertHaltOnCard(t, st, card.ID, "会话未设置")
}

func TestRunnerRunLockRefusalReportsWhoNodeExpiry(t *testing.T) {
	st, card := nodeLedger(t)
	if _, acq, err := st.AcquireRunLock(card.ID, ledger.StatusDoing, "run:other-host#7#7", ledger.RunLockTTL); err != nil || !acq {
		t.Fatalf("预占运行锁: %v %v", acq, err)
	}
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		t.Fatal("运行锁被拒时不得派发")
		return "", "", nil
	})
	_, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing)
	if err == nil || !strings.Contains(err.Error(), "run:other-host#7#7") || !strings.Contains(err.Error(), ledger.StatusDoing) {
		t.Fatalf("报错应点名持有者与节点: %v", err)
	}
	assertHaltOnCard(t, st, card.ID, "run:other-host#7#7")
	got, _ := st.GetCard(card.ID)
	if got.DriverSession != "" {
		t.Fatalf("运行锁被拒时不得认领归属: %q", got.DriverSession)
	}
}

func TestRunRenewsLockRowOnBeat(t *testing.T) {
	st, card := nodeLedger(t)
	started, release := make(chan struct{}), make(chan struct{})
	beats := make(chan time.Time, 8)
	runner := dispatchRunner(t, st, func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		close(started)
		<-release
		return "T-beat", "", nil
	})
	runner.RenewBeat = beats
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing); done <- err }()
	<-started
	before, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok {
		t.Fatalf("回合中应有锁行: ok=%v err=%v", ok, err)
	}
	beats <- time.Time{}
	deadline := time.Now().Add(time.Second)
	var after ledger.RunLock
	for {
		after, _, _ = st.RunLockOf(card.ID)
		if after.ExpiresAt.After(before.ExpiresAt) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("节拍后库行 expires_at 必须推进: before=%v after=%v", before.ExpiresAt, after.ExpiresAt)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunnerStopsCardWritesAfterLosingWriteGate(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("gateflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "审阅"},
		{Name: "审阅", Dispatch: true, Verdict: true, Template: "feature-impl", Next: ledger.StatusDone, OnFail: ledger.StatusTodo},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "写闸卡", Project: "p", Workflow: "gateflow", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	linksBefore, err := st.TasksOf(card.ID)
	if err != nil {
		t.Fatalf("读失权前挂账: %v", err)
	}
	eventsBefore, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatalf("读失权前事件: %v", err)
	}
	dispatchedBefore := 0
	for _, event := range eventsBefore {
		if event.Type == ledger.EvDispatched {
			dispatchedBefore++
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	runner := &StepRunner{St: st, Session: "session-runner", Target: "mac-02", RunHolder: "run:loser#9#9", RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			close(started)
			<-release
			return "T-gate", "", nil
		}}}
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), card.ID, "审阅"); done <- err }()
	<-started
	lock, ok, err := st.RunLockOf(card.ID)
	if err != nil || !ok {
		t.Fatalf("读锁行: ok=%v err=%v", ok, err)
	}
	if err := st.ReleaseRunLock(card.ID, lock.Holder); err != nil {
		t.Fatal(err)
	}
	close(release)
	runErr := <-done
	if !errors.Is(runErr, ErrWriteGateClosed) {
		t.Fatalf("失去写权后首个卡写应被闸拒绝: %v", runErr)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	explanatory := 0
	for _, e := range events {
		switch e.Type {
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && strings.Contains(p.Body, "本轮运行锁已被接手") {
				explanatory++
			}
		case ledger.EvNeedsHuman, ledger.EvNeedsCleared, ledger.EvReviewVerdict, ledger.EvStatusMoved:
			t.Fatalf("失去写权后不得继续写卡: %+v", e)
		}
	}
	if explanatory != 1 {
		t.Fatalf("说明性 comment 应恰一条，实得 %d 条: %+v", explanatory, events)
	}
	linksAfter, err := st.TasksOf(card.ID)
	if err != nil {
		t.Fatalf("读失权后挂账: %v", err)
	}
	if len(linksAfter) != len(linksBefore) {
		t.Fatalf("失去写权后不得新增 card_tasks 行: before=%d after=%d", len(linksBefore), len(linksAfter))
	}
	dispatchedAfter := 0
	for _, event := range events {
		if event.Type == ledger.EvDispatched {
			dispatchedAfter++
		}
	}
	if dispatchedAfter != dispatchedBefore {
		t.Fatalf("失去写权后不得新增 dispatched 事件: before=%d after=%d", dispatchedBefore, dispatchedAfter)
	}
}

func TestRunnerLossCommentNamesCurrentRunLockHolder(t *testing.T) {
	dbPath := t.TempDir() + "/ledger.db"
	st, err := ledger.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedLedgerStepStore(t, st)
	card, err := st.CreateCard(ledger.NewCard{Title: "失权取证", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	runner := &StepRunner{St: st, Session: "session-runner", Target: "mac-02", RunHolder: "run:runner-a#1#1", RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			close(started)
			<-release
			return "T-holder-a", "", nil
		}}}
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing); done <- err }()
	<-started

	// 让 A 的运行锁过期，再通过真实 AcquireRunLock 让 B 抢占并落接管事件。
	lockDB, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开锁测试连接: %v", err)
	}
	t.Cleanup(func() { _ = lockDB.Close() })
	if _, err := lockDB.Exec("UPDATE card_run_locks SET expires_at = ? WHERE card_id = ?",
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), card.ID); err != nil {
		t.Fatalf("做旧 A 的运行锁: %v", err)
	}
	const holderB = "run:otherbox#999#1"
	if _, acquired, err := st.AcquireRunLock(card.ID, ledger.StatusDoing, holderB, ledger.RunLockTTL); err != nil || !acquired {
		t.Fatalf("B 抢占运行锁: acquired=%v err=%v", acquired, err)
	}

	close(release)
	if err := <-done; !errors.Is(err, ErrWriteGateClosed) {
		t.Fatalf("A 失权后首个卡写应被闸拒绝: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	const holderA = "run:runner-a#1#1"
	for _, event := range events {
		if event.Type != ledger.EvComment {
			continue
		}
		var payload struct {
			Body string `json:"body"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || !strings.Contains(payload.Body, "本轮运行锁已被接手") {
			continue
		}
		if !strings.Contains(payload.Body, holderB) {
			t.Fatalf("失权说明应点名当前 B 持有者 %q: %s", holderB, payload.Body)
		}
		if strings.Contains(payload.Body, holderA) {
			t.Fatalf("失权说明不得把 A 自己写成持有者 %q: %s", holderA, payload.Body)
		}
		return
	}
	t.Fatal("未找到失权说明 comment")
}

func TestRunnerLossCommentReportsRunLockReadError(t *testing.T) {
	dbPath := t.TempDir() + "/ledger.db"
	st, err := ledger.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedLedgerStepStore(t, st)
	card, err := st.CreateCard(ledger.NewCard{Title: "失权读错", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	const holderA = "run:runner-read-error#1#1"
	runner := &StepRunner{St: st, Session: "session-runner", Target: "mac-02", RunHolder: holderA, RenewBeat: make(chan time.Time, 8),
		Dispatcher: &Dispatcher{St: st, Actor: "runner-actor", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			close(started)
			<-release
			return "T-read-error", "", nil
		}}}
	done := make(chan error, 1)
	go func() { _, err := runner.Run(context.Background(), card.ID, ledger.StatusDoing); done <- err }()
	<-started

	lockDB, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode=WAL&_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatalf("打开锁测试连接: %v", err)
	}
	t.Cleanup(func() { _ = lockDB.Close() })
	if _, err := lockDB.Exec("DROP TABLE card_run_locks"); err != nil {
		t.Fatalf("删除运行锁表: %v", err)
	}
	_, _, readErr := st.RunLockOf(card.ID)
	if readErr == nil {
		t.Fatal("删除运行锁表后读取应返回原始错误")
	}

	close(release)
	if err := <-done; !errors.Is(err, ErrWriteGateClosed) {
		t.Fatalf("运行锁读取失败后首个卡写应被闸拒绝: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != ledger.EvComment {
			continue
		}
		var payload struct {
			Body string `json:"body"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || !strings.Contains(payload.Body, "本轮运行锁已被接手") {
			continue
		}
		if !strings.Contains(payload.Body, "现持有者读取失败："+readErr.Error()) {
			t.Fatalf("失权说明应如实包含运行锁读取错误 %q: %s", readErr, payload.Body)
		}
		if strings.Contains(payload.Body, holderA) {
			t.Fatalf("读取失败时不得回落打印本轮 holder %q: %s", holderA, payload.Body)
		}
		return
	}
	t.Fatal("未找到失权读错说明 comment")
}

// TestRunnerLocalClientUsesWaitAndDiffWire 锁住节点生产 Run 路径的本机 client
// 接线：await 仍走 /ws/events + Attach + Done，产出校验仍走 Diff；二者收到的
// target 都是空串，不读本机 ledger 冒充远端协议。
func TestRunnerLocalClientUsesWaitAndDiffWire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const taskID = "task-local-runner"
	const finalText = "```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"
	var wsHits, attachHits, doneHits, diffHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ws/events":
			wsHits.Add(1)
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("Accept WS: %v", err)
				return
			}
			event := proto.Event{
				Seq: 1, TaskID: taskID, Type: proto.EventTypeCompleted,
				Payload:   json.RawMessage(fmt.Sprintf(`{"final_text":%q}`, finalText)),
				CreatedAt: time.Now().UTC(),
			}
			body, _ := json.Marshal(event)
			if err := conn.Write(r.Context(), websocket.MessageText, body); err != nil {
				t.Errorf("写 completed WS: %v", err)
			}
		case r.URL.Path == "/api/tasks/"+taskID && r.Method == http.MethodGet:
			attachHits.Add(1)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"recent_events":[{"type":"completed","payload":{"final_text":%q}}]}`, finalText))
		case r.URL.Path == "/api/tasks/"+taskID+"/done" && r.Method == http.MethodPost:
			doneHits.Add(1)
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.URL.Path == "/api/tasks/"+taskID+"/diff" && r.Method == http.MethodGet:
			diffHits.Add(1)
			_, _ = io.WriteString(w, `{"diff":"diff --git a/docs/out.md b/docs/out.md\n--- a/docs/out.md\n+++ b/docs/out.md\n"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	st, _ := dispatchTestCard(t)
	if _, err := st.PutWorkflow("local-runner", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "审阅"},
		{Name: "审阅", Dispatch: true, Verdict: true, Template: "feature-impl"},
	}}); err != nil {
		t.Fatalf("写本机 runner 工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "本机 runner", Project: "demo", Workflow: "local-runner", Actor: "test"})
	if err != nil {
		t.Fatalf("建本机 runner 卡: %v", err)
	}
	var transportCalls atomic.Int32
	runner := &StepRunner{
		St: st, Session: "runner-local", Target: "", RunHolder: "run:local#1#1",
		Dispatcher: &Dispatcher{St: st, Actor: "runner-local", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			transportCalls.Add(1)
			if opts.Target != "" {
				t.Fatalf("本机 Transport target = %q，期望空串", opts.Target)
			}
			return taskID, "", nil
		}},
		Clients: func(target string) (*client.Client, error) {
			if target != "" {
				return nil, fmt.Errorf("本机 runner 收到非空 target %q", target)
			}
			return client.New(ts.URL, "test-token"), nil
		},
	}
	outcome, err := runner.Run(context.Background(), card.ID, "审阅")
	if err != nil {
		t.Fatalf("本机 runner Run: %v", err)
	}
	if outcome.Action != ActionPass || transportCalls.Load() != 1 {
		t.Fatalf("本机 runner outcome/calls = %+v/%d", outcome, transportCalls.Load())
	}
	paths, err := runner.diffNode()(context.Background(), "", taskID)
	if err != nil {
		t.Fatalf("本机 runner Diff: %v", err)
	}
	if len(paths) != 1 || paths[0] != "docs/out.md" {
		t.Fatalf("本机 runner changed paths = %v，期望 [docs/out.md]", paths)
	}
	if wsHits.Load() != 1 || attachHits.Load() != 1 || doneHits.Load() != 1 || diffHits.Load() != 1 {
		t.Fatalf("本机 runner HTTP hits ws/attach/done/diff = %d/%d/%d/%d，期望均 1",
			wsHits.Load(), attachHits.Load(), doneHits.Load(), diffHits.Load())
	}
}

func stMustNode(t *testing.T, st *ledger.Store, cardID, name string) ledger.NodeDef {
	t.Helper()
	card, err := st.GetCard(cardID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	flow, err := st.GetWorkflow(card.WorkflowName, card.WorkflowVersion)
	if err != nil {
		t.Fatalf("读卡钉工作流: %v", err)
	}
	for _, node := range flow.Def.Nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("找不到节点 %q", name)
	return ledger.NodeDef{}
}
