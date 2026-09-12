package agentd

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

func newStepTestServer(t *testing.T) *Server {
	t.Helper()
	env := newLedgerEnv(t)
	return env.srv
}

func seedCardWithProject(t *testing.T, s *Server, project string) {
	t.Helper()
	seedAgentdLedger(t, s.ledger, "bug")
	if _, err := s.ledger.CreateCard(newCardForStepTest(project)); err != nil {
		t.Fatal(err)
	}
}

func newCardForStepTest(project string) ledger.NewCard {
	return ledger.NewCard{Title: "环节测试卡", Project: project, Workflow: "bug", Actor: "test"}
}

// seedImplementCardWithProject 建一张钉在「带 implement 节点」的工作流上的卡。
//
// 为什么不能沿用 bug 流的种子卡：受理前会校验节点名确实在卡钉住的工作流里，
// 而 implement 这个名字只存在于 charter 流——charter 不是出厂工作流（出厂只有
// feature/domain/bug/triage），测试环境里没有，所以这里当场写一条最小工作流。
//
// 为什么不能把节点名换成 bug 流里现成的：被测属性正是「守卫不再按节点名拒绝
// implement」，换掉名字测的就变成另一条属性了。
func seedImplementCardWithProject(t *testing.T, s *Server, project string) {
	t.Helper()
	if _, err := s.ledger.PutWorkflow("charter", ledger.WorkflowDef{
		Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "implement"},
			{Name: "implement", Next: "review", Dispatch: true, Verdict: true,
				Template: "review-generic", CarryCardContext: true, MaxRounds: 3},
			{Name: "review", Next: ledger.StatusDone, Dispatch: true, Verdict: true,
				Template: "review-generic", CarryCardContext: true, MaxRounds: 3,
				OnFail: "implement"},
			{Name: ledger.StatusDone},
		},
	}); err != nil {
		t.Fatalf("写入带 implement 节点的测试工作流: %v", err)
	}
	if _, err := s.ledger.CreateCard(ledger.NewCard{
		Title: "charter 环节测试卡", Project: project, Workflow: "charter", Actor: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func holdCardStep(t *testing.T, s *Server, cardID string) func() {
	t.Helper()
	if _, err := s.ledger.GetCard(cardID); err != nil {
		seedAgentdLedger(t, s.ledger, "bug")
		if _, createErr := s.ledger.CreateCard(newCardForStepTest("demo")); createErr != nil {
			t.Fatalf("准备占位卡失败: %v", createErr)
		}
	}
	if !s.claimCardStep(cardID) {
		t.Fatalf("占用环节槽位失败: %s", cardID)
	}
	return func() { s.releaseCardStep(cardID) }
}

func cardStepInFlight(s *Server, cardID string) bool { return s.cardStepInFlight(cardID) }

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

// TestStartCardStepRejectsSecondInFlight 同一张卡同时只允许一个环节在飞。
// 为什么必须拦：两个 merge 环节并发跑同一个仓路径，会在同一个工作区里
// 互相踩 git 状态——而那一侧的失败信息只会是一句莫名其妙的 git 报错。
// TestDisciplineTargetCapLocalDoesNotNeedPool 本机不是 config.Targets 里的远程
// 机。pool 未装配时若走 pool.For("local") 会炸或拿 nil 能力位，B229 拒发闸会
// 把本机点火误杀。本机能力位与 Status 上报同源，恒 true。
func TestDisciplineTargetCapLocalDoesNotNeedPool(t *testing.T) {
	s := &Server{} // pool 故意不装
	for _, name := range []string{"local", "本机"} {
		cap := s.disciplineTargetCap(name)
		if cap == nil || !*cap {
			t.Fatalf("本机目标 %q 能力位 = %v，want true（不得依赖 target 池）", name, cap)
		}
	}
}

func TestStartCardStepRejectsSecondInFlight(t *testing.T) {
	s := newStepTestServer(t)
	release := holdCardStep(t, s, "B1")
	defer release()
	if err := s.startCardStep("B1", proto.CardStepReq{Step: "review", Actor: "web:test"}); !errors.Is(err, errStepInFlight) {
		t.Fatalf("第二个环节应被拒，实得 %v", err)
	}
}

// TestStartCardStepReleasesSlotOnFinish 环节跑完要把位子让出来，
// 否则一张卡审一次之后就再也审不了了——而且这个 bug 要等到第二次点才发现。
func TestStartCardStepReleasesSlotOnFinish(t *testing.T) {
	env := newLedgerEnv(t)
	s := env.srv
	// 用 handoff 项目让建出的卡确实是 B1（前缀取项目名首字母）：B229 起
	// startCardStep 同步段会解析卡与节点，卡号必须真实存在。
	seedCardWithProject(t, s, "handoff")
	seedDisciplineOnLedger(t, env, discipline.NameReview, "本机测试审阅纪律")
	done := make(chan struct{})
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	if err := s.startCardStep("B1", proto.CardStepReq{Step: ledger.StatusReview, Actor: "web:test"}); err != nil {
		t.Fatalf("首次应放行: %v", err)
	}
	<-done
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
	if err := s.startCardStep("B1", proto.CardStepReq{Step: ledger.StatusReview, Actor: "web:test"}); err != nil {
		t.Fatalf("跑完之后应能再发起: %v", err)
	}
}

// TestStartCardStepAssemblesRunHolder 锁住生产装配不能漏传运行身份。
func TestStartCardStepAssemblesRunHolder(t *testing.T) {
	s := newNoPTYLedgerEnv(t).srv
	// 项目名必须让建出的卡真是 B1：B229 起 startCardStep 同步段会解析卡与节点，
	// 卡号不存在直接拒。handoff 在建表时就钉了前缀 B（internal/ledger/store.go）。
	seedCardWithProject(t, s, "handoff")
	ch := make(chan string, 1)
	s.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		ch <- runner.RunHolder
	}
	if err := s.startCardStep("B1", proto.CardStepReq{Step: ledger.StatusReview, Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var holder string
	select {
	case holder = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("编排未被启动")
	}
	if holder == "" || !strings.HasPrefix(holder, "run:") || strings.Count(holder, "#") != 2 {
		t.Fatalf("holder 应为 run:<host>#<pid>#<unixnano> 形态: %q", holder)
	}
	waitFor(t, func() bool { return !cardStepInFlight(s, "B1") })
}

func TestCardStepAdmittedRoundReleasesCapacity(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 1)
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]
	done := make(chan struct{}, 1)
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {
		done <- struct{}{}
	}
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
		t.Fatalf("准入: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("执行者回合未返回")
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	for _, key := range []string{"squad/sq1/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("起源卡节点回合结束后不应有执行计数 %s=%d，want 0", key, got)
		}
	}
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
		t.Fatalf("释放名额后第二次准入: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("第二次执行者回合未返回")
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
}

type b23310ProfileFakeAdapter struct {
	*fake.Fake
	profile executor.Profile
}

func (a *b23310ProfileFakeAdapter) Profile() executor.Profile { return a.profile }

func setupB23310CardTaskEnv(t *testing.T, script []fake.Step) *ledgerEnv {
	return setupB23310CardTaskEnvWithMachine(t, script, "b23310-loop")
}

func setupB23310CardTaskEnvWithMachine(t *testing.T, script []fake.Step, machine string) *ledgerEnv {
	t.Helper()
	env := setupNoPTYSquadEnv(t, 1)
	if machine != "local" {
		remote := testhttp.NewServer(t, env.srv.Handler())
		if err := env.srv.swapConf(func(c *config.Config) error {
			if c.Targets == nil {
				c.Targets = make(map[string]config.Target)
			}
			c.Targets[machine] = config.Target{
				Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken,
			}
			return nil
		}); err != nil {
			t.Fatalf("登记生命周期测试目标机: %v", err)
		}
	}
	svc := env.srv.Scheduling()
	carrier, err := svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读取 c1: %v", err)
	}
	rec, err := env.srv.autoLedger.Get("carrier", "c1")
	if err != nil {
		t.Fatalf("读取 c1 版本: %v", err)
	}
	carrier.Machine = machine
	carrier.CLI = "fake"
	carrier.HomeDir = filepath.Join(t.TempDir(), "carrier-home")
	if err := svc.PutCarrier(carrier, rec.Version); err != nil {
		t.Fatalf("把 c1 换成本机 fake: %v", err)
	}
	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("确认生命周期测试载体上线: %v", err)
	}

	adapter := &b23310ProfileFakeAdapter{Fake: fake.New(script), profile: &recordingProfile{}}
	mgr := newManagerForServer(t, env.srv, map[string]executor.Adapter{"fake": adapter})
	origin, repo := newOriginAndClone(t)
	if _, err := mgr.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: origin, Name: "handoff", Path: repo,
	}); err != nil {
		t.Fatalf("登记 handoff 项目: %v", err)
	}
	return env
}

// TestB23310CardTaskUsesFrozenLocalTarget 穿过真实 startCardStep → stepTransport →
// 本机 HTTP → handleDispatch，锁住本机登记名仍按冻结 Machine 落点，且只建立一次
// 任务占用。
func TestB23310CardTaskUsesFrozenLocalTarget(t *testing.T) {
	env := setupB23310CardTaskEnvWithMachine(t, []fake.Step{{Finish: executor.Result{OK: true}}}, "local")
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]
	runErr := make(chan error, 1)
	b23310RunStep(t, env, runErr)
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
		t.Fatalf("启动本机小队卡节点: %v", err)
	}
	task := b23310TaskForCard(t, env, cardID, runErr)
	if task.Target != "local" {
		t.Fatalf("本机冻结任务 Target = %q，want local", task.Target)
	}
	for _, key := range []string{"squad/sq1/c1", "carrier/c1"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 1 {
			t.Fatalf("本机任务创建后占用 %s=%d，want 1", key, got)
		}
	}
	rr := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if rr.Code != http.StatusOK {
		t.Fatalf("清理本机冻结任务返回 %d: %s", rr.Code, rr.Body.String())
	}
}

func b23310TaskForCard(t *testing.T, env *ledgerEnv, cardID string, runErr <-chan error) *proto.Task {
	t.Helper()
	var task *proto.Task
	var returned bool
	var runnerErr error
	waitFor(t, func() bool {
		links, err := env.ledger.TasksOf(cardID)
		if err == nil && len(links) > 0 {
			var getErr error
			task, getErr = env.st.GetTask(links[0].TaskID)
			return getErr == nil
		}
		select {
		case runnerErr = <-runErr:
			returned = true
			return true
		default:
			return false
		}
	})
	if returned {
		card, cardErr := env.ledger.GetCard(cardID)
		events, eventErr := env.ledger.EventsFromAsc([]string{cardID}, 0, 100)
		payloads := make([]string, 0, len(events))
		for _, event := range events {
			payloads = append(payloads, string(event.Payload))
		}
		t.Fatalf("小队卡节点未形成 task，runner error=%v card=%+v card_error=%v event_payloads=%v event_error=%v",
			runnerErr, card, cardErr, payloads, eventErr)
	}
	return task
}

func b23310RunStep(t *testing.T, env *ledgerEnv, runErr chan<- error) {
	t.Helper()
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, id, node string) {
		_, err := runner.Run(ctx, id, node)
		runErr <- err
	}
}

// TestB23310CardTaskOwnsOccupancyUntilTerminal 锁住一次小队卡节点的双重生命期：
// 卡步骤 goroutine 返回只释放 card slot，任务进入终态前仍持有 carrier/member；
// done 才释放两级任务占用，非终态 fake 返回不能提前改计数。
func TestB23310CardTaskOwnsOccupancyUntilTerminal(t *testing.T) {
	t.Run("terminal task releases on done", func(t *testing.T) {
		env := setupB23310CardTaskEnv(t, []fake.Step{{Finish: executor.Result{OK: true}}})
		cardID := seedSquadFlow(t, env, "sq1", 1)[0]
		runErr := make(chan error, 1)
		b23310RunStep(t, env, runErr)
		if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
			t.Fatalf("启动小队卡节点: %v", err)
		}
		task := b23310TaskForCard(t, env, cardID, runErr)
		waitFor(t, func() bool {
			got, err := env.st.GetTask(task.ID)
			return err == nil && got.State == proto.TaskStateWaitingReview && !env.srv.cardStepInFlight(cardID)
		})
		for _, key := range []string{"squad/sq1/c1", "carrier/c1"} {
			if got := runningCountIn(t, env.srv.autoLedger, key); got != 1 {
				t.Fatalf("任务终态前占用 %s=%d，want 1", key, got)
			}
		}
		rr := runAction(env.srv, actionRequest(task.ID, "done", `{}`), env.srv.handleDone)
		if rr.Code != http.StatusOK {
			t.Fatalf("done 返回 %d: %s", rr.Code, rr.Body.String())
		}
		for _, key := range []string{"squad/sq1/c1", "carrier/c1"} {
			if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
				t.Fatalf("done 后占用 %s=%d，want 0", key, got)
			}
		}
	})

	t.Run("nonterminal task keeps occupancy after card goroutine returns", func(t *testing.T) {
		env := setupB23310CardTaskEnv(t, nil)
		cardID := seedSquadFlow(t, env, "sq1", 1)[0]
		runErr := make(chan error, 1)
		b23310RunStep(t, env, runErr)
		if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
			t.Fatalf("启动非终态小队卡节点: %v", err)
		}
		task := b23310TaskForCard(t, env, cardID, runErr)
		waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
		got, err := env.st.GetTask(task.ID)
		if err != nil {
			t.Fatalf("读取非终态任务: %v", err)
		}
		if got.State == proto.TaskStateCompleted || got.State == proto.TaskStateFailed {
			t.Fatalf("fake 非终态任务意外终结: %s", got.State)
		}
		for _, key := range []string{"squad/sq1/c1", "carrier/c1"} {
			if got := runningCountIn(t, env.srv.autoLedger, key); got != 1 {
				t.Fatalf("卡 goroutine 返回后非终态占用 %s=%d，want 1", key, got)
			}
		}
		rr := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
		if rr.Code != http.StatusOK {
			t.Fatalf("清理非终态任务返回 %d: %s", rr.Code, rr.Body.String())
		}
	})
}

// TestRequiresInlineLocalFile keeps the guard tied to request capabilities rather than node names.
func TestRequiresInlineLocalFile(t *testing.T) {
	for _, req := range []proto.CardStepReq{
		{Step: "implement"},
		{Step: "review", Target: "linux-01", Executor: "codex", Model: "gpt-5", Extra: "x", Actor: "cli:u@h#1"},
		{Step: "review", Target: "", Executor: "", Model: "", Extra: "", Actor: ""},
	} {
		if requiresInlineLocalFile(req) {
			t.Fatalf("requiresInlineLocalFile(%+v) = true, want false", req)
		}
	}
}
