// 模板派发共用段的回归测试：CLI 与 agentd 都只需替换 Transport，
// 纪律块角色名、prompt 与快照判据在这里统一守住。
package ledgerstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// seedLedgerStepStore 写入显式测试数据。Store.Open 本身没有生产种子；
// 这些模板和工作流只服务于 ledgerstep 的提示词与快照测试。
func seedLedgerStepStore(t *testing.T, st *ledger.Store) {
	t.Helper()
	for name, def := range map[string]ledger.TemplateDef{
		"feature-impl":   {Executor: "opencode", Purpose: "implement", BranchPrefix: "cards", Discipline: discipline.NameImplement, Prompt: "实现 {{TITLE}}：{{ACCEPT}}"},
		"review-generic": {Executor: "grok", Purpose: "review", BranchPrefix: "cards", Discipline: discipline.NameReview, Prompt: "审阅 {{TITLE}}：{{ACCEPT}}"},
	} {
		if _, err := st.PutTemplate(name, def); err != nil {
			t.Fatalf("写测试模板 %s: %v", name, err)
		}
	}
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: ledger.StatusDoing},
		{Name: ledger.StatusDoing, Next: ledger.StatusReview, Dispatch: true, Template: "feature-impl"},
		{Name: ledger.StatusReview, Next: ledger.StatusDone, Dispatch: true, Verdict: true, Template: "review-generic", OnFail: ledger.StatusDoing},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
}

func dispatchTestCard(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedLedgerStepStore(t, st)
	card, err := st.CreateCard(ledger.NewCard{Title: "要派的卡", Project: "demo", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return st, card
}

// setTemplateModel 为装配层测试给指定目标机注入模板模型。
// 这里复用真实的不可变模板版本写入口，确保断言穿过模板 JSON 存储边界。
func setTemplateModel(t *testing.T, st *ledger.Store, target, model string) {
	t.Helper()
	tpl, err := st.GetTemplate("feature-impl", 0)
	if err != nil {
		t.Fatalf("取 feature-impl 模板: %v", err)
	}
	tpl.Def.ModelByTarget = map[string]string{target: model}
	if _, err := st.PutTemplate("feature-impl", tpl.Def); err != nil {
		t.Fatalf("写带模型模板: %v", err)
	}
}

// TestViaTemplateExecutorModelOverridesAndPairRule 钉住 CLI/节点共用的模板装配语义：
// 同层 executor 覆盖时，model 不能从模板下层漏下来；两者都不覆盖时则保持旧值。
func TestViaTemplateExecutorModelOverridesAndPairRule(t *testing.T) {
	const target = "mac-02"
	cases := []struct {
		name         string
		req          TemplateDispatch
		wantExecutor string
		wantModel    string
	}{
		{name: "无覆盖保留模板", req: TemplateDispatch{Template: "feature-impl", Target: target}, wantExecutor: "opencode", wantModel: "template-model"},
		{name: "只覆盖执行器清空模板模型", req: TemplateDispatch{Template: "feature-impl", Target: target, ExecutorOverride: "grok"}, wantExecutor: "grok", wantModel: ""},
		{name: "只覆盖模型沿用模板执行器", req: TemplateDispatch{Template: "feature-impl", Target: target, ModelOverride: "cli-model"}, wantExecutor: "opencode", wantModel: "cli-model"},
		{name: "同时覆盖执行器模型", req: TemplateDispatch{Template: "feature-impl", Target: target, ExecutorOverride: "grok", ModelOverride: "grok-model"}, wantExecutor: "grok", wantModel: "grok-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, card := dispatchTestCard(t)
			setTemplateModel(t, st, target, "template-model")
			var got DispatchOpts
			d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
				got = opts
				return "T-override", "", nil
			}}
			if _, err := d.ViaTemplate(context.Background(), card, tc.req); err != nil {
				t.Fatalf("ViaTemplate: %v", err)
			}
			if got.Executor != tc.wantExecutor || got.Model != tc.wantModel {
				t.Fatalf("executor/model = %q/%q, want %q/%q", got.Executor, got.Model, tc.wantExecutor, tc.wantModel)
			}
		})
	}
}

// TestViaTemplateSameExecutorKeepsTemplateModel ensures spelling out the template
// executor is a no-op for the paired model override.
func TestViaTemplateSameExecutorKeepsTemplateModel(t *testing.T) {
	const target = "mac-02"
	st, card := dispatchTestCard(t)
	setTemplateModel(t, st, target, "template-model")
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-same-executor", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{
		Template: "feature-impl", Target: target, ExecutorOverride: "opencode",
	}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Executor != "opencode" || got.Model != "template-model" {
		t.Fatalf("same executor executor/model = %q/%q, want %q/%q", got.Executor, got.Model, "opencode", "template-model")
	}
}

// TestViaTemplateSnapshotRecordsExecutorModel 穿过真实 dispatched JSON 边界验证执行器和模型快照。
func TestViaTemplateSnapshotRecordsExecutorModel(t *testing.T) {
	st, card := dispatchTestCard(t)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "T-snapshot-executor-model", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{
		Template: "feature-impl", Target: "mac-02", ExecutorOverride: "grok", ModelOverride: "grok-model",
	}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 50)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type != ledger.EvDispatched {
			continue
		}
		var snapshot ledger.DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			t.Fatalf("解 dispatched payload: %v", err)
		}
		if snapshot.Executor != "grok" || snapshot.Model != "grok-model" {
			t.Fatalf("快照 executor/model = %q/%q, want %q/%q", snapshot.Executor, snapshot.Model, "grok", "grok-model")
		}
		return
	}
	t.Fatal("缺 dispatched 事件")
}

// TestViaTemplateCarriesTransportBaseCommitIntoResultAndSnapshot 钉住目标 agentd
// 返回的 Task.BaseCommit 必须原样穿过 Transport、结果和 dispatched 事件；空 base
// 也必须显式落账，不能由协调者本地猜 SHA。
func TestViaTemplateCarriesTransportBaseCommitIntoResultAndSnapshot(t *testing.T) {
	st, card := dispatchTestCard(t)
	wantBaseCommit := strings.Repeat("1", 40)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "task-wire", wantBaseCommit, nil
	}}

	got, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{
		Template: "feature-impl", Target: "mac-02",
	})
	if err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Task != "task-wire" || got.Base != "" || got.BaseCommit != wantBaseCommit {
		t.Fatalf("结果 task/base/base_commit=%q/%q/%q，期望 task-wire/空/%s",
			got.Task, got.Base, got.BaseCommit, wantBaseCommit)
	}

	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type != ledger.EvDispatched {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(event.Payload, &raw); err != nil {
			t.Fatalf("解 dispatched payload: %v", err)
		}
		for _, key := range []string{"base", "base_commit"} {
			if _, ok := raw[key]; !ok {
				t.Fatalf("dispatched payload 缺少 %q: %s", key, event.Payload)
			}
		}
		var snap ledger.DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snap); err != nil {
			t.Fatalf("解 dispatched snapshot: %v", err)
		}
		if snap.Base != "" || snap.BaseCommit != wantBaseCommit {
			t.Fatalf("快照 base/base_commit=%q/%q，期望空/%s", snap.Base, snap.BaseCommit, wantBaseCommit)
		}
		return
	}
	t.Fatal("缺 dispatched 事件")
}

// TestViaTemplateStopsSnapshotAfterWriteGateCloses 覆盖挂账成功后、快照写入前
// 失去运行锁的窗口：已有 card_tasks 行保留，但不得再落 dispatched 事件。
func TestViaTemplateStopsSnapshotAfterWriteGateCloses(t *testing.T) {
	st, card := dispatchTestCard(t)
	const holder = "run:gate-test#1#1"
	if _, acquired, err := st.AcquireRunLock(card.ID, "审阅", holder, ledger.RunLockTTL); err != nil || !acquired {
		t.Fatalf("取得测试运行锁: acquired=%v err=%v", acquired, err)
	}

	gateCalls := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "T-write-gate", "", nil
	}}
	_, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{
		Template: "feature-impl", Target: "mac-02",
		WriteGate: func() bool {
			gateCalls++
			if gateCalls == 2 {
				if err := st.ReleaseRunLock(card.ID, holder); err != nil {
					t.Fatalf("释放测试运行锁: %v", err)
				}
			}
			ok, renewErr := st.RenewRunLock(card.ID, holder, ledger.RunLockTTL)
			if renewErr != nil {
				t.Fatalf("续租测试运行锁: %v", renewErr)
			}
			return ok
		},
	})
	if !errors.Is(err, ErrWriteGateClosed) {
		t.Fatalf("快照写前失权应被拒: %v", err)
	}
	if gateCalls != 2 {
		t.Fatalf("两处账本写入前都应检查写闸，实得 %d 次", gateCalls)
	}
	links, err := st.TasksOf(card.ID)
	if err != nil {
		t.Fatalf("读挂账: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("失权前已完成的挂账应保留，实得 %d 行", len(links))
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	for _, event := range events {
		if event.Type == ledger.EvDispatched {
			t.Fatalf("失权后不得新增 dispatched 事件: %+v", event)
		}
	}
}

// TestViaTemplateSendsDisciplineName 模板的角色名要随派发请求上送，
// 而不是被 CLI 读成正文拼进 prompt。
func TestViaTemplateSendsDisciplineName(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-fake-1", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Discipline != "implement" {
		t.Fatalf("请求里应带角色名 implement，实得 %q", got.Discipline)
	}
}

// TestViaTemplateMarksEmptyBaseForTargetResolution 钉住空卡基线的跨机决议边界：
// ledgerstep 不知道目标仓库路径，故只标记「请目标 agentd 解析默认分支」；
// 显式基线仍把原文传下去，不得被这个标记改写。
func TestViaTemplateMarksEmptyBaseForTargetResolution(t *testing.T) {
	t.Run("空基线标记目标侧解析", func(t *testing.T) {
		st, card := dispatchTestCard(t)
		var got DispatchOpts
		d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			got = opts
			return "T-default-base", "", nil
		}}
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("ViaTemplate: %v", err)
		}
		if got.Base != "" {
			t.Fatalf("空卡基线不应在 ledgerstep 猜分支名，实得 %q", got.Base)
		}
		if !got.ResolveDefaultBase {
			t.Fatal("空卡基线必须要求目标 agentd 解析项目默认分支")
		}
	})

	t.Run("显式基线保持原语义", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		seedLedgerStepStore(t, st)
		card, err := st.CreateCard(ledger.NewCard{
			Title: "显式基线卡", Project: "demo", Workflow: "bug", BaseBranch: "release", Actor: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		var got DispatchOpts
		d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			got = opts
			return "T-explicit-base", "", nil
		}}
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("ViaTemplate: %v", err)
		}
		if got.Base != "release" || got.ResolveDefaultBase {
			t.Fatalf("显式基线必须原样传递且不触发默认解析：base=%q resolve=%v", got.Base, got.ResolveDefaultBase)
		}
	})
}

// TestViaTemplateNoDisciplineInPrompt prompt 里不许再出现纪律块正文。
// 这是本次重构的核心判据：两份纪律块同时在场时，审阅那次的「只读，不写」
// 会被实现块的「每个 task 完成即 commit」推翻。
func TestViaTemplateNoDisciplineInPrompt(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-fake-2", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	for _, mark := range []string{"# 审阅纪律", "# 执行纪律", "只读，不写", "每个 task 完成即 commit"} {
		if strings.Contains(got.Prompt, mark) {
			t.Fatalf("prompt 里不该再有纪律块正文，命中 %q：\n%s", mark, got.Prompt)
		}
	}
	if !strings.Contains(got.Prompt, "要派的卡") {
		t.Fatalf("模板正文应还在：\n%s", got.Prompt)
	}
}

// TestViaTemplateOverrideReplacesName --discipline-override 改的是名字，
// 不再是文件路径。
func TestViaTemplateOverrideReplacesName(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-fake-3", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02", DisciplineOverride: "review"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Discipline != "review" {
		t.Fatalf("override 应替换名字，实得 %q", got.Discipline)
	}
}

// TestViaTemplateSnapshotRecordsDisciplineName 派发事件快照要答得出
// 「这次用的哪块纪律」——正文不再经过 CLI，指纹算不出来了，
// 但那个问题本身没消失，答案换成名字。
func TestViaTemplateSnapshotRecordsDisciplineName(t *testing.T) {
	st, card := dispatchTestCard(t)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		return "T-fake-4", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	show := string(raw)
	if !strings.Contains(show, `"discipline_name":"implement"`) {
		t.Fatalf("快照应记下角色名: %q", show)
	}
}

// TestViaTemplateSecondRoundGetsNumberedBranch 非审阅模板重跑时分支按轮次
// 挂号：首轮无后缀（存量 cards/<卡>-implement 命名不变），第二轮 -2。
// 尾部断言穿序列化边界：挂号分支要落进 dispatched 快照并被 WorkBranch 读回，
// 否则终审会审到第一轮的旧分支。
func TestViaTemplateSecondRoundGetsNumberedBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	var branches []string
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		branches = append(branches, opts.Branch)
		return fmt.Sprintf("T-impl-%d", len(branches)), "", nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("第 %d 轮 ViaTemplate: %v", i+1, err)
		}
	}
	want := []string{"cards/" + card.ID + "-implement", "cards/" + card.ID + "-implement-2"}
	for i := range want {
		if branches[i] != want[i] {
			t.Fatalf("第 %d 轮分支应为 %q，实得 %q", i+1, want[i], branches[i])
		}
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if wb.Branch != want[1] || wb.Target != "mac-02" {
		t.Fatalf("WorkBranch 应读回最新挂号分支及目标机 %q，实得 %+v", want[1], wb)
	}
}

// TestViaTemplateContinuationUsesLocalWorkBranch 验证第二轮沿同机工作分支接续，
// 且把本地起点标记送入 Transport；首轮仍保留空基线的默认分支语义。
func TestViaTemplateContinuationUsesLocalWorkBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	var dispatched []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		dispatched = append(dispatched, opts)
		return fmt.Sprintf("T-continuation-%d", len(dispatched)), "", nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("第 %d 轮 ViaTemplate: %v", i+1, err)
		}
	}
	if dispatched[0].LocalBaseBranch || !dispatched[0].ResolveDefaultBase || dispatched[0].Base != "" {
		t.Fatalf("首轮应沿空基线默认解析：base=%q local=%v default=%v", dispatched[0].Base,
			dispatched[0].LocalBaseBranch, dispatched[0].ResolveDefaultBase)
	}
	wantBranch := "cards/" + card.ID + "-implement"
	if dispatched[1].Base != wantBranch || !dispatched[1].LocalBaseBranch || dispatched[1].ResolveDefaultBase {
		t.Fatalf("第二轮应从同机工作分支本地解析：base=%q local=%v default=%v", dispatched[1].Base,
			dispatched[1].LocalBaseBranch, dispatched[1].ResolveDefaultBase)
	}
}

// TestViaTemplateRejectsCrossTargetBeforeTransport 验证跨机时不静默掉回卡基线，
// 且拒绝发生在 Transport/LinkTask/RecordDispatch 之前，不留下新的派发副作用。
func TestViaTemplateRejectsCrossTargetBeforeTransport(t *testing.T) {
	st, card := dispatchTestCard(t)
	transportCalls := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		transportCalls++
		return fmt.Sprintf("T-cross-%d", transportCalls), "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("首轮 ViaTemplate: %v", err)
	}
	before, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "linux-01"}); err == nil {
		t.Fatal("跨机接续必须拒绝")
	} else {
		for _, want := range []string{"工作分支只存在于创建它的那台机器", "git push", "--base"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("跨机拒绝应包含 %q，实得 %v", want, err)
			}
		}
	}
	if transportCalls != 1 {
		t.Fatalf("跨机拒绝不得调用 Transport，调用次数=%d", transportCalls)
	}
	after, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("跨机拒绝不得新增账本事件：before=%d after=%d", len(before), len(after))
	}
}

func TestViaTemplateNodePurposeTakesReviewPath(t *testing.T) {
	st, card := dispatchTestCard(t)
	var dispatched []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		dispatched = append(dispatched, opts)
		return fmt.Sprintf("T-purpose-%d", len(dispatched)), "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("实现轮 ViaTemplate: %v", err)
	}
	workBranch := "cards/" + card.ID + "-implement"
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{
			Template: "feature-impl", Target: "mac-02", PurposeOverride: ledger.PurposeReview,
		}); err != nil {
		t.Fatalf("审阅轮 ViaTemplate: %v", err)
	}
	if len(dispatched) != 2 {
		t.Fatalf("应派两轮，实得 %d", len(dispatched))
	}
	if got, want := dispatched[1].Branch, "cards/"+card.ID+"-review-1"; got != want {
		t.Fatalf("审阅分支应为 %q，实得 %q", want, got)
	}
	if dispatched[1].Base != workBranch {
		t.Fatalf("审阅分支基线应为首轮工作分支 %q，实得 %q", workBranch, dispatched[1].Base)
	}
	if dispatched[1].ResolveDefaultBase {
		t.Fatal("审阅轮已有工作分支，不应要求目标侧解析默认基线")
	}
	if !dispatched[1].LocalBaseBranch {
		t.Fatal("审阅轮工作分支必须标记为目标机本地起点")
	}
	reviews, err := st.ReviewRounds(card.ID)
	if err != nil {
		t.Fatalf("ReviewRounds: %v", err)
	}
	if reviews != 1 {
		t.Fatalf("审阅轮数应为 1，实得 %d", reviews)
	}
	gotWorkBranch, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if gotWorkBranch.Branch != workBranch || gotWorkBranch.Target != "mac-02" {
		t.Fatalf("WorkBranch 应跳过审阅轮并保持分支及目标机 %q，实得 %+v", workBranch, gotWorkBranch)
	}
}

func TestViaTemplateWithoutPurposeOverrideKeepsTemplatePurpose(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-template-purpose", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if want := "cards/" + card.ID + "-implement"; got.Branch != want {
		t.Fatalf("无覆盖时应沿用模板用途的分支 %q，实得 %q", want, got.Branch)
	}
	if got.Base != "" {
		t.Fatalf("首轮无卡基线时 Base 应为空，实得 %q", got.Base)
	}
	if !got.ResolveDefaultBase {
		t.Fatal("首轮无卡基线时应要求目标侧解析默认基线")
	}
}

func TestViaTemplateOmitAcceptanceWithholdsCriteria(t *testing.T) {
	const criteria = "go test ./... 全绿且真机跑通"
	for _, tc := range []struct {
		name       string
		omit       bool
		wantCount  int
		wantNotice bool
	}{
		{name: "收起判据", omit: true, wantCount: 0, wantNotice: true},
		{name: "保留判据", omit: false, wantCount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, card := dispatchTestCard(t)
			if err := st.SetAcceptance(card.ID, criteria, "test"); err != nil {
				t.Fatalf("SetAcceptance: %v", err)
			}
			card, err := st.GetCard(card.ID)
			if err != nil {
				t.Fatalf("GetCard: %v", err)
			}
			var got DispatchOpts
			d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
				got = opts
				return "T-acceptance", "", nil
			}}
			if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{
				Template: "feature-impl", Target: "mac-02", CarryCardContext: true,
				OmitAcceptance: tc.omit,
			}); err != nil {
				t.Fatalf("ViaTemplate: %v", err)
			}
			if count := strings.Count(got.Prompt, criteria); count != tc.wantCount {
				t.Fatalf("判据原文应出现 %d 次，实得 %d：\n%s", tc.wantCount, count, got.Prompt)
			}
			if tc.wantNotice && !strings.Contains(got.Prompt, "本节点不注入整卡验收判据") {
				t.Fatalf("收起判据时应保留显式说明：\n%s", got.Prompt)
			}
			if strings.Contains(got.Prompt, "这是整卡的最终验收判据") {
				t.Fatalf("omit 说明已是完整句，prompt 不应再跟「这是整卡的最终验收判据」：\n%s", got.Prompt)
			}
		})
	}
}

func TestBuildPromptThreeSections(t *testing.T) {
	card := ledger.Card{
		ID: "B9.1", Title: "做点什么", AcceptanceCriteria: "测试全绿",
		Attachments: []ledger.Attachment{
			{Kind: "spec", Path: "docs/spec.md"},
			{Kind: "plan", Path: "docs/plan.md"},
		},
	}
	t.Run("全关时只有模板正文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, false, "", "")
		if got != "模板正文" {
			t.Fatalf("不该有多余段落:\n%s", got)
		}
	})
	t.Run("带卡上下文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", true, false, "", "")
		for _, want := range []string{
			"模板正文", "## 本卡上下文", "B9.1", "做点什么",
			"feat/x", "合并目标以此为准", "测试全绿",
			"spec: docs/spec.md", "plan: docs/plan.md",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("缺 %q:\n%s", want, got)
			}
		}
	})
	t.Run("带本次补充", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, false, "这次只看并发安全", "")
		if !strings.Contains(got, "## 本次补充") || !strings.Contains(got, "这次只看并发安全") {
			t.Fatalf("补充段没拼进去:\n%s", got)
		}
	})
	t.Run("空基线不写死 main", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "", true, false, "", "")
		if strings.Contains(got, "有效基线分支：main") {
			t.Fatalf("基线为空时不得替用户猜一个:\n%s", got)
		}
		if !strings.Contains(got, "有效基线分支：（未设置") {
			t.Fatalf("基线为空时应显式说明未设置:\n%s", got)
		}
	})
	t.Run("无附件不留空标题", func(t *testing.T) {
		bare := ledger.Card{ID: "B9.2", Title: "无附件"}
		got := buildPrompt("模板正文", bare, "feat/x", true, false, "", "")
		if strings.Contains(got, "- 附件：") {
			t.Fatalf("没有附件时不该出现附件小节:\n%s", got)
		}
	})
}

func TestBuildPromptIncludesOutputPathWithoutCardContext(t *testing.T) {
	card := ledger.Card{ID: "B201", Title: "产文档"}
	got := buildPrompt(
		"模板正文", card, "", false, true, "",
		"docs/b201-plan.md",
	)
	if strings.Contains(got, "## 本卡上下文") {
		t.Fatalf("carry=false 不应注入卡上下文:\n%s", got)
	}
	for _, want := range []string{
		"## 本节点产出物",
		"docs/b201-plan.md",
		"请把本节点产出物写到该路径，不要另起文件名",
		"不要加日期前缀",
		"带 YYYY-MM-DD- 的是历史文件，不是本节点法定产出",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺产出路径段 %q:\n%s", want, got)
		}
	}
}
