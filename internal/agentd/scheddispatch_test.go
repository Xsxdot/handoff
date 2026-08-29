package agentd

// B156.3 K2 Task C/D：装配侧 Squad 解析层与排队分支的缝级测试。
//
// 覆盖声明（plan §4.5 缝清单）：
//   - 缝①编制域入站 api 门面：T1/T2/T3 全部经 scheduling.Service 公开方法；
//   - 缝③环节执行体缝：入口 startCardStep → 真实 StepRunner 链；
//   - 反面断言：T4 金样事件序列（Task D 追加）+ ledgerstep 零 import grep。
//
// 夹具说明：载体机器名恒 "ftm" 与假目标机同名——命中载体的 Machine 直接可用
// 作派发 target。假目标机能力位必须为 true：feature-impl 模板点名 implement
// 纪律块，能力位缺席时拒发闸同步拦截（discipline.ResolveDispatch 的保守方向），
// 派发链走不到 wire。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// ---- 夹具：小队工作流卡（形状照 seedImplementCardWithProject，节点带 Squad）----

func seedSquadFlow(t *testing.T, env *ledgerEnv, squad string, count int) []string {
	t.Helper()
	if _, err := env.ledger.PutWorkflow("sqflow", ledger.WorkflowDef{
		Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "implement"},
			{Name: "implement", Next: ledger.StatusReview, Dispatch: true,
				Template: "feature-impl", CarryCardContext: true,
				Override: ledger.NodeOverride{Squad: squad}},
			{Name: ledger.StatusReview, Next: ledger.StatusDone, Dispatch: true,
				Verdict: true, Template: "review-generic", OnFail: "implement"},
			{Name: ledger.StatusDone},
		},
	}); err != nil {
		t.Fatalf("写小队工作流: %v", err)
	}
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		card, err := env.ledger.CreateCard(ledger.NewCard{
			Title: fmt.Sprintf("小队流测试卡%d", i), Project: "handoff",
			Workflow: "sqflow", Actor: "test"})
		if err != nil {
			t.Fatalf("建卡: %v", err)
		}
		ids = append(ids, card.ID)
	}
	return ids
}

// setupSquadEnv 装配真实编制域（SetupAutomation 全真接线）+ 假目标机 +
// 纪律块，返回环境与假机。载体机器名恒 "ftm"（与假目标机同名），使命中载体的
// Machine 直接可用作派发 target。
func setupSquadEnv(t *testing.T, carrierMax int) (*ledgerEnv, *fakeTargetMachine) {
	t.Helper()
	env := newLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger) // 真实 facadeAsRegistry + scheduling.Service
	yes := true
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "ftm", ftm)
	if ver := seedDisciplineOnLedger(t, env, discipline.NameImplement, "# 实现纪律\n完成即 commit\n"); ver < 1 {
		t.Fatalf("纪律块版本异常: %d", ver)
	}
	svc := env.srv.Scheduling()
	if err := svc.PutCarrier(scheduling.Carrier{Name: "c1", Machine: "ftm",
		CLI: "opencode", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: carrierMax}, 0); err != nil {
		t.Fatalf("登记载体: %v", err)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "sq1",
		Role: scheduling.RoleExecutor, Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 8}},
	}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	return env, ftm
}

// runningCountIn 是 runningCount 的 agentd 侧同构（直读门面，缺失=0）。
func runningCountIn(t *testing.T, f *ledgerapi.Facade, key string) int {
	t.Helper()
	e, err := f.Get("sched_running", key)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			return 0
		}
		t.Fatalf("读计数 %s: %v", key, err)
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(e.Body, &body); err != nil {
		t.Fatalf("解码计数 %s: %v", key, err)
	}
	return body.Count
}

func countCardEvents(t *testing.T, st *ledger.Store, cardID string) int {
	t.Helper()
	evs, err := st.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

// TestEffectiveCoversParityMatrix 锁装配侧覆盖合并公式（effectiveCovers）。
// 行矩阵与 internal/ledgerstep/runner_test.go 的
// TestRunnerExecutorModelOverridePriorityAndPairRule 逐行对应——同一份语义写了
// 两遍（ledgerstep 一份、装配侧一份），判据按 plan §5 双向点名：这里锁装配份，
// 链路份由既有测试锁，两侧矩阵不一致时人工比对即翻红现场。
//
// 内部锁合法性声明（plan §5）：本函数是纯函数，入口不在任何接缝上；从声明缝
// 构造不出「两份公式逐行相等」的直接断言——缝只能观测其中一份的输出——故以
// 「两侧矩阵逐行对应 + 各自锁定」的形态附加，不顶替缝级断言（T1/T2 仍在）。
func TestEffectiveCoversParityMatrix(t *testing.T) {
	cases := []struct {
		name                string
		reqT, reqE, reqM    string
		nodeT, nodeE, nodeM string
		wantT, wantE, wantM string
	}{
		{"全空走载体", "", "", "", "", "", "", "", "", ""},
		{"节点覆盖保留", "", "", "", "mt", "ne", "nm", "mt", "ne", "nm"},
		{"请求压过节点", "rt", "re", "rm", "mt", "ne", "nm", "rt", "re", "rm"},
		{"换执行器切模型", "", "re", "", "", "ne", "nm", "", "re", ""},
		{"重述同执行器保模型", "", "ne", "rm", "", "ne", "nm", "", "ne", "rm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotT, gotE, gotM := effectiveCovers(
				proto.CardStepReq{Target: tc.reqT, Executor: tc.reqE, Model: tc.reqM},
				ledger.NodeDef{Override: ledger.NodeOverride{
					Target: tc.nodeT, Executor: tc.nodeE, Model: tc.nodeM}})
			if gotT != tc.wantT || gotE != tc.wantE || gotM != tc.wantM {
				t.Fatalf("(t,e,m)=(%q,%q,%q), want (%q,%q,%q)",
					gotT, gotE, gotM, tc.wantT, tc.wantE, tc.wantM)
			}
		})
	}
}

// holdAfterSquadRound 让小队节点在真实派发之后卡住，直到测试结束才返回。
// implement 节点 Dispatch 且无 Verdict：Run 在派发后立刻返回，K5 的
// defer releaseSchedulingBinding 会把计数清零（见 TestCardStepAdmittedRoundReleasesCapacity）。
// 本文件要观察的是「回合还在飞」时的计数与排队，必须把归还推迟到断言之后。
func holdAfterSquadRound(t *testing.T, env *ledgerEnv) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, node string) {
		env.srv.runStep(ctx, runner, cardID, node)
		<-release
	}
}

// TestSquadNodeAdmitsAndDispatchesThroughBinding 缝①（编制域入站 api 门面）×
// 缝④（环节执行体缝）：Squad 非空节点受理后，派发真的穿过 Binding 的三元组——
// 目标机/执行者以载体为准，计数两级各 +1，task 挂到卡上。
func TestSquadNodeAdmitsAndDispatchesThroughBinding(t *testing.T) {
	env, ftm := setupSquadEnv(t, 2)
	holdAfterSquadRound(t, env)
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{
		Step: "implement", Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	waitFor(t, func() bool {
		links, e := env.ledger.TasksOf(cardID)
		return e == nil && len(links) == 1
	})
	body := ftm.lastDispatch()
	if body["target"] != "ftm" {
		t.Fatalf("派发目标机=%v，want 载体 machine ftm", body["target"])
	}
	if body["executor"] != "opencode" {
		t.Fatalf("派发执行者=%v，want 载体 cli opencode", body["executor"])
	}
	facade := env.srv.autoLedger
	for key, want := range map[string]int{"squad/sq1/c1": 1, "carrier/c1": 1} {
		if got := runningCountIn(t, facade, key); got != want {
			t.Fatalf("计数 %s=%d，want %d", key, got, want)
		}
	}
}

// TestSquadNodeFullQueuesWithoutTrace 缝①×缝④排队分支：满员第二轮以排队形态
// 结束——受理返回 nil、goroutine 不启动、卡事件流零新增、计数不动、队列行里的
// 覆盖快照与手写裁决（独立 oracle）逐项相等。
func TestSquadNodeFullQueuesWithoutTrace(t *testing.T) {
	env, _ := setupSquadEnv(t, 1) // 载体物理位 1：第一张占满，第二张必排队
	holdAfterSquadRound(t, env)
	ids := seedSquadFlow(t, env, "sq1", 2)
	if err := env.srv.startCardStep(ids[0], proto.CardStepReq{
		Step: "implement", Actor: "web:test", Executor: "grok"}); err != nil {
		t.Fatalf("首卡受理: %v", err)
	}
	waitFor(t, func() bool {
		links, e := env.ledger.TasksOf(ids[0])
		return e == nil && len(links) == 1
	})

	queued := make(chan struct{}, 1)
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {
		queued <- struct{}{} // 排队形态不得走到这里
	}
	before := countCardEvents(t, env.ledger, ids[1])
	if err := env.srv.startCardStep(ids[1], proto.CardStepReq{
		Step: "implement", Actor: "web:test", Executor: "grok"}); err != nil {
		t.Fatalf("满员受理应静默排队（返回 nil），实得 %v", err)
	}
	select {
	case <-queued:
		t.Fatal("排队形态起了 runner——违反「本轮以排队形态结束」")
	default:
	}
	if after := countCardEvents(t, env.ledger, ids[1]); after != before {
		t.Fatalf("排队留痕：卡事件数 %d→%d，want 零新增", before, after)
	}
	facade := env.srv.autoLedger
	if got := runningCountIn(t, facade, "carrier/c1"); got != 1 {
		t.Fatalf("排队不应改计数：carrier/c1=%d，want 1", got)
	}
	// 队列行快照 vs 独立 oracle（手写裁决，不调 effectiveCovers）：
	// IgnitionRequest 的 Target/Executor/Model 在契约里标注为「一次性覆盖」
	// （contract §4.1），载体字段的解析只发生在准入时刻（§4.2 条 8）；出队后
	// 「再次走同一步入口」重新准入（§5），所以队列行必须携带**未解析的原始覆盖**
	// 而不是某个载体的投影——满员时根本没有命中的载体可投影。plan 的 T2 oracle
	// 在此写成 Target="ftm"，与契约冲突，按契约改判：req.Executor="grok" 压过
	// 一切、Target 空（请求与节点都没填）、Model 空串（换执行器切断下层模型）、
	// Priority 取卡默认「中」、Ready 恒 true（plan §D4）。
	e, err := facade.Get(scheduling.KindIgnitionQueue, ids[1]+"|implement")
	if err != nil {
		t.Fatalf("读队列行: %v", err)
	}
	var row struct {
		Req struct {
			Card, Squad, Node       string
			Target, Executor, Model string
			Priority                string
			Ready                   bool
			Actor                   string
		} `json:"req"`
	}
	if err := json.Unmarshal(e.Body, &row); err != nil {
		t.Fatalf("解码队列行: %v", err)
	}
	r := row.Req
	want := map[string]any{"Card": ids[1], "Squad": "sq1", "Node": "implement",
		"Target": "", "Executor": "grok", "Model": "",
		"Priority": "中", "Ready": true, "Actor": "web:test"}
	got := map[string]any{"Card": r.Card, "Squad": r.Squad, "Node": r.Node,
		"Target": r.Target, "Executor": r.Executor, "Model": r.Model,
		"Priority": r.Priority, "Ready": r.Ready, "Actor": r.Actor}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("队列快照 %s=%v，want %v（全量 got=%v）", k, got[k], w, got)
		}
	}
}

// TestSquadNodeRejectsAreDistinctFromQueueing 缝①错误半边：ErrNoHealthy（空成员
// 小队=配置问题）与角色不符都以受理错误上浮（HTTP default 分支→400），与满员的
// 静默排队形态可区分（T2 已锁另一侧）。条件恒假的回答（判据形状要求4）：
// ErrNoHealthy 恒不触发的机器上，本支退化为只锁角色不符——两条子用例独立成立。
func TestSquadNodeRejectsAreDistinctFromQueueing(t *testing.T) {
	t.Run("空成员小队报ErrNoHealthy", func(t *testing.T) {
		env, _ := setupSquadEnv(t, 2)
		if err := env.srv.Scheduling().PutSquad(scheduling.Squad{Name: "empty",
			Role: scheduling.RoleExecutor, Members: nil}, 0); err != nil {
			t.Fatal(err)
		}
		cardID := seedSquadFlow(t, env, "empty", 1)[0]
		err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "web:test"})
		if err == nil || !errors.Is(err, scheduling.ErrNoHealthy) {
			t.Fatalf("want ErrNoHealthy 上浮，实得 %v", err)
		}
	})
	t.Run("协调者小队报角色不符", func(t *testing.T) {
		env, _ := setupSquadEnv(t, 2)
		if err := env.srv.Scheduling().PutSquad(scheduling.Squad{Name: "coord",
			Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "c1"}}}, 0); err != nil {
			t.Fatal(err)
		}
		cardID := seedSquadFlow(t, env, "coord", 1)[0]
		err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "web:test"})
		if err == nil || !errors.Is(err, scheduling.ErrRoleMismatch) {
			t.Fatalf("want ErrRoleMismatch 上浮，实得 %v", err)
		}
	})
}

// ---- 修复轮（charter-3）：准入重试预算耗尽与满员同处置转排队 + WARN 落痕 ----

// casConflictOnRunning 把 sched_running 的写全部打成 CAS 冲突的替身注册表，
// 其余读写原样透传生产同款适配器。Admit 的计数 CAS 因此连续冲突直至预算耗尽
// ——用替身注入构造 ErrRetryExhausted（协调者裁决：可用替身，不必真打并发）。
type casConflictOnRunning struct{ inner schedclient.Registry }

func (r casConflictOnRunning) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	if kind == "sched_running" {
		return 0, schedclient.ErrCASConflict
	}
	return r.inner.Put(kind, id, expectVersion, body, actor)
}

func (r casConflictOnRunning) Get(kind, id string) (schedclient.Record, error) {
	return r.inner.Get(kind, id)
}

func (r casConflictOnRunning) List(kind string) ([]schedclient.Record, error) {
	return r.inner.List(kind)
}

func (r casConflictOnRunning) Delete(kind, id string, expectVersion int, actor string) error {
	return r.inner.Delete(kind, id, expectVersion, actor)
}

// TestSquadAdmitBudgetExhaustedQueuesWithWarn 锁修复轮裁决：Admit 返回
// ErrRetryExhausted（瞬态争用，不是「没容量」）时与满员同处置——转 Enqueue、
// 本轮以排队形态结束；且必须先落一条 WARN 标记词「准入重试预算耗尽，按满员
// 转排队」，预算耗尽这个真实信号不许被吞。强制正控：去掉该分流分支本测试必翻红。
func TestSquadAdmitBudgetExhaustedQueuesWithWarn(t *testing.T) {
	env, _ := setupSquadEnv(t, 2)
	// 整体替换编制域服务（既有测试缝 SetScheduling）：内层用生产适配器，
	// 只有计数写恒冲突——载体/小队登记已在替换前经真实服务落库。
	env.srv.SetScheduling(scheduling.New(casConflictOnRunning{
		inner: facadeAsRegistry{f: env.srv.autoLedger}}))
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]

	var logs strings.Builder
	prevLog := env.srv.log
	env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { env.srv.log = prevLog })

	queued := make(chan struct{}, 1)
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {
		queued <- struct{}{} // 排队形态不得走到这里
	}
	before := countCardEvents(t, env.ledger, cardID)
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{
		Step: "implement", Actor: "web:test"}); err != nil {
		t.Fatalf("预算耗尽应与满员同处置静默排队（返回 nil），实得 %v", err)
	}
	select {
	case <-queued:
		t.Fatal("预算耗尽的排队形态起了 runner——违反「本轮以排队形态结束」")
	default:
	}
	if after := countCardEvents(t, env.ledger, cardID); after != before {
		t.Fatalf("排队留痕：卡事件数 %d→%d，want 零新增", before, after)
	}
	facade := env.srv.autoLedger
	if got := runningCountIn(t, facade, "carrier/c1"); got != 0 {
		t.Fatalf("计数 CAS 恒冲突下不该有成功准入：carrier/c1=%d，want 0", got)
	}
	if _, err := facade.Get(scheduling.KindIgnitionQueue, cardID+"|implement"); err != nil {
		t.Fatalf("队列行不存在，排队没有真正发生: %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "准入重试预算耗尽，按满员转排队") {
		t.Fatalf("缺 WARN 落痕（级别或标记词缺失），日志原文：\n%s", out)
	}
}

// ---- Task D：反面断言（存量直绑逐字节不变 + 调度侧零痕迹）----

// canonicalPayloadForTest 把事件负载投影成键序稳定的字符串，作为事件序列全等
// 断言的投影函数（Task 0 取证程序里 canonicalPayload 的正式版）。
func canonicalPayloadForTest(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{k: m[k]})
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// legacyGolden 是基线（改动前）非小队节点完整派发链的事件序列投影
// （Seq|Type|Actor|canonical(Payload)），由 Task 0 的 golden_dump 程序在改动前
// 真实捕获后粘贴（连续两次运行逐字节一致）。断言 = 改动后同一夹具重跑产出
// 完全相同的序列（拍板记录⑥：存量直绑逐字节不变的执法形态）+ 调度侧零痕迹
// （即使 SetupAutomation 在场，非小队路径不给 registry 留任何行）。
var legacyGolden = []string{
	`GOLDEN|1|card_created|test|[{"title":"环节测试卡"},{"workflow":"bug"},{"workflow_version":1}]`,
	`GOLDEN|2|comment|web:test|[{"body":"挂账 task T-fake-01@ftm（implement）"},{"kind":"普通"}]`,
	`GOLDEN|3|dispatched|web:test|[{"branch":"cards/B1-implement"},{"discipline_name":"implement"},{"discipline_version":1},{"executor":"opencode"},{"model":""},{"purpose":"implement"},{"target":"ftm"},{"task_id":"T-fake-01"},{"template":"feature-impl"},{"template_version":2}]`,
}

func TestLegacyNodeEventSequenceUnchanged(t *testing.T) {
	env := newLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger) // 刻意在场：证明调度在场也不被触碰
	yes := true                         // 同金样取证程序：能力位必须为真，派发链才走得到 wire
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "ftm", ftm)
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "# 实现纪律\n完成即 commit\n")
	seedCardWithProject(t, env.srv, "handoff")
	if err := env.srv.startCardStep("B1", proto.CardStepReq{
		Step: ledger.StatusDoing, Target: "ftm", Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var evs []ledger.Event
	// 与金样取证同款稳定等待：TasksOf==1 只证明 LinkTask 落库，dispatched 事件
	// 在其后；不等它就读事件流会让断言随调度时序漂移。
	waitFor(t, func() bool {
		rows, e := env.ledger.EventsFromAsc([]string{"B1"}, 0, 10000)
		if e != nil {
			return false
		}
		for _, ev := range rows {
			if ev.Type == ledger.EvDispatched {
				evs = rows
				return true
			}
		}
		return false
	})
	got := make([]string, 0, len(evs))
	for _, ev := range evs {
		got = append(got, fmt.Sprintf("GOLDEN|%d|%s|%s|%s",
			ev.Seq, ev.Type, ev.Actor, canonicalPayloadForTest(t, ev.Payload)))
	}
	if len(got) != len(legacyGolden) {
		t.Fatalf("事件数 %d ≠ 基线 %d\n got=%v\nwant=%v", len(got), len(legacyGolden), got, legacyGolden)
	}
	for i := range got {
		if got[i] != legacyGolden[i] {
			t.Fatalf("第 %d 条漂移：\n got=%s\nwant=%s", i, got[i], legacyGolden[i])
		}
	}
	// 调度侧零痕迹（点数，非抽查）：两类 kind 的 registry 行数都是 0。
	for _, kind := range []string{"sched_running", scheduling.KindIgnitionQueue} {
		rows, err := env.srv.autoLedger.List(kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("非小队路径给 %s 留了 %d 行，want 0", kind, len(rows))
		}
	}
}
