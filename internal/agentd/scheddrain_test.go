// scheddrain_test.go —— K5 清队与协调者回合边界的缝级测试。
//
// 职责：通过真实 scheduling registry、keystone service 和 agentd 入口，锁住
// 持久队列重放、Wake-before-dispatch 以及协调者两级名额的回收。
// 边界：不测试 scheduling 的排序/CAS 内部，也不把 keystone 的重建规则复制到本包。
package agentd

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type queueTraceRunner struct {
	mu      sync.Mutex
	launch  int
	resumes []string
	trace   []string
}

func (r *queueTraceRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launch++
	r.trace = append(r.trace, "launch")
	return keysclient.TurnResult{SessionID: "queue-session"}, nil
}

func (r *queueTraceRunner) Resume(_ keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes = append(r.resumes, prompt)
	r.trace = append(r.trace, "resume")
	return keysclient.TurnResult{SessionID: "queue-session"}, nil
}

func (r *queueTraceRunner) snapshot() (int, []string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.launch, append([]string(nil), r.resumes...), append([]string(nil), r.trace...)
}

func (r *queueTraceRunner) markDispatch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trace = append(r.trace, "dispatch")
}

func seedQueueCoordinator(t *testing.T, env *ledgerEnv) *queueTraceRunner {
	t.Helper()
	svc := env.srv.Scheduling()
	if err := svc.PutCarrier(scheduling.Carrier{
		Name: "coord-carrier", Machine: "ftm", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
	}, 0); err != nil {
		t.Fatalf("登记协调者载体: %v", err)
	}
	if err := svc.PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []string{"coord-carrier"}, MaxConcurrency: 1,
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	runner := &queueTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return runner
}

func setupNoPTYSquadEnv(t *testing.T, carrierMax int) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(configPath, env.srv.conf()); err != nil {
		t.Fatalf("准备配置: %v", err)
	}
	env.srv.SetConfigPath(configPath)
	env.srv.SetupAutomation(env.ledger)
	yes := true
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "ftm", ftm)
	if ver := seedDisciplineOnLedger(t, env, discipline.NameImplement, "# 实现纪律\n完成即 commit\n"); ver < 1 {
		t.Fatalf("纪律块版本异常: %d", ver)
	}
	if err := env.srv.Scheduling().PutCarrier(scheduling.Carrier{
		Name: "c1", Machine: "ftm", CLI: "opencode",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: carrierMax,
	}, 0); err != nil {
		t.Fatalf("登记执行者载体: %v", err)
	}
	if err := env.srv.Scheduling().PutSquad(scheduling.Squad{
		Name: "sq1", Role: scheduling.RoleExecutor, Members: []string{"c1"}, MaxConcurrency: 8,
	}, 0); err != nil {
		t.Fatalf("登记执行者小队: %v", err)
	}
	return env
}

func TestAutomationQueueRestartReplay(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 2)
	runner := seedQueueCoordinator(t, env)
	ids := seedSquadFlow(t, env, "sq1", 3)
	for _, req := range []struct {
		req  scheduling.IgnitionRequest
		kind string
	}{
		{req: scheduling.IgnitionRequest{Card: ids[0], Squad: "coord", Actor: "test", Ready: true}, kind: scheduling.KindLaunchQueue},
		{req: scheduling.IgnitionRequest{Card: ids[1], Squad: "sq1", Node: "implement", Executor: "opencode", Actor: "test", Ready: true}, kind: scheduling.KindIgnitionQueue},
		{req: scheduling.IgnitionRequest{Card: ids[2], Squad: "sq1", Node: "implement", Executor: "opencode", Actor: "test", Ready: true}, kind: scheduling.KindIgnitionQueue},
	} {
		if _, err := env.srv.Scheduling().Enqueue(req.req, req.kind); err != nil {
			t.Fatalf("入队 %s: %v", req.kind, err)
		}
	}
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {}

	processed, err := env.srv.drainQueuesOnce(context.Background())
	if err != nil {
		t.Fatalf("清队: %v", err)
	}
	if processed != 3 {
		t.Fatalf("处理行数=%d，want 3", processed)
	}
	waitFor(t, func() bool {
		return !env.srv.cardStepInFlight(ids[1]) && !env.srv.cardStepInFlight(ids[2])
	})
	rows, err := env.srv.Scheduling().QueueSnapshot()
	if err != nil {
		t.Fatalf("读队列快照: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("重放后仍有 %d 行队列: %+v", len(rows), rows)
	}
	launches, resumes, _ := runner.snapshot()
	if launches != 3 || len(resumes) != 0 {
		t.Fatalf("协调者回合数 launch=%d resume=%d，want 3/0", launches, len(resumes))
	}
}

func TestAutomationIgnitionDrainWakesBeforeTrueDispatch(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 2)
	runner := seedQueueCoordinator(t, env)
	ids := seedSquadFlow(t, env, "sq1", 2)
	if _, err := env.srv.keystone.LaunchForCard(context.Background(), ids[1], "test", keysclient.SessionSpec{CLI: "opencode"}); err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	if _, err := env.srv.Scheduling().Enqueue(scheduling.IgnitionRequest{
		Card: ids[1], Squad: "sq1", Node: "implement", Executor: "opencode", Actor: "test", Ready: true,
	}, scheduling.KindIgnitionQueue); err != nil {
		t.Fatalf("入队: %v", err)
	}
	var dispatchSeen bool
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {
		runner.markDispatch()
		dispatchSeen = true
	}

	processed, err := env.srv.drainQueuesOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("清队 processed=%d err=%v，want 1/nil", processed, err)
	}
	waitFor(t, func() bool { return dispatchSeen && !env.srv.cardStepInFlight(ids[1]) })
	_, resumes, trace := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("Resume 次数=%d，want 1", len(resumes))
	}
	if !strings.Contains(resumes[0], "queue_release") {
		t.Fatalf("Wake briefing 缺 queue_release: %q", resumes[0])
	}
	if len(trace) < 2 || trace[len(trace)-2] != "resume" || trace[len(trace)-1] != "dispatch" {
		t.Fatalf("未观察到 Wake-before-dispatch 顺序: %v", trace)
	}
}

func TestAutomationRoundReleasesCoordinatorCounters(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	runner := seedQueueCoordinator(t, env)
	cardID := createCoordCard(t, env)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "launch", call: func() error {
			_, err := env.srv.launchCoordinatorRound(context.Background(), cardID, "test")
			return err
		}},
		{name: "wake", call: func() error {
			_, err := env.srv.wakeCoordinatorRound(context.Background(), cardID, []keystone.WakeEvent{{
				Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal",
			}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("%s 回合: %v", tc.name, err)
			}
			for key := range map[string]struct{}{
				"squad/coord":           {},
				"carrier/coord-carrier": {},
			} {
				if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
					t.Fatalf("%s 后计数 %s=%d，want 0", tc.name, key, got)
				}
			}
		})
	}
	launches, resumes, _ := runner.snapshot()
	if launches != 1 || len(resumes) != 1 {
		t.Fatalf("回合调用 launch=%d resume=%d，want 1/1", launches, len(resumes))
	}
}

func TestAutomationReleaseKicksDrain(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	seedQueueCoordinator(t, env)
	if _, err := env.srv.launchCoordinatorRound(context.Background(), createCoordCard(t, env), "test"); err != nil {
		t.Fatalf("拉起回合: %v", err)
	}
	select {
	case <-env.srv.automationKick:
	default:
		t.Fatal("释放名额后没有收到清队唤醒信号")
	}
}
