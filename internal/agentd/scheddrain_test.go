// scheddrain_test.go —— K5 清队与协调者回合边界的缝级测试。
//
// 职责：通过真实 scheduling registry、keystone service 和 agentd 入口，锁住
// 持久队列重放、Wake-before-dispatch 以及协调者两级名额的回收。
// 边界：不测试 scheduling 的排序/CAS 内部，也不把 keystone 的重建规则复制到本包。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/testhttp"
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
	allowCarrierMachines(t, env.srv, "ftm")
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1,
		Status:         scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	runner := &queueTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	return runner
}

// firstCoordinatorSlotFullRegistry 让第一次协调者准入观察到载体 A 满员，随后
// 还原真实 registry。这样同一轮的 ignition 请求能在真实 wake→dispatch 链上
// 使用载体 B，测试只注入边界读数，不复制 scheduling 的准入规则。
type firstCoordinatorSlotFullRegistry struct {
	inner schedclient.Registry
	full  bool
}

func (r *firstCoordinatorSlotFullRegistry) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	return r.inner.Put(kind, id, expectVersion, body, actor)
}

func (r *firstCoordinatorSlotFullRegistry) Get(kind, id string) (schedclient.Record, error) {
	if r.full && kind == "sched_running" && id == "squad/coord/coord-carrier" {
		r.full = false
		return schedclient.Record{ID: id, Version: 1, Body: []byte(`{"count":1}`)}, nil
	}
	return r.inner.Get(kind, id)
}

func (r *firstCoordinatorSlotFullRegistry) List(kind string) ([]schedclient.Record, error) {
	return r.inner.List(kind)
}

func (r *firstCoordinatorSlotFullRegistry) Delete(kind, id string, expectVersion int, actor string) error {
	return r.inner.Delete(kind, id, expectVersion, actor)
}

// TestDrainQueuesDefersCoordinatorNoSlotAndContinues 锁 P2：launch queue 的协调者
// 请求本次 ErrNoSlot 时只回填当前行，清队继续穿过 ignition queue，并让另一载体
// 的执行者真实进入 Wake-before-dispatch。该 runner 只证明机内清队接缝，不证明
// agentd 重启或 SQLite 多进程恢复。
func TestDrainQueuesDefersCoordinatorNoSlotAndContinues(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 1)
	runner := seedQueueCoordinator(t, env)
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "c2", Machine: "ftm", CLI: "opencode", HomeDir: "/tmp/c2-home",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name: "sq2", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c2", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记小队 sq2: %v", err)
	}
	env.srv.SetScheduling(scheduling.New(&firstCoordinatorSlotFullRegistry{
		inner: facadeAsRegistry{f: env.srv.autoLedger}, full: true,
	}))
	ids := seedSquadFlow(t, env, "sq2", 1)
	coordCard := createCoordCard(t, env)
	if _, err := mustScheduling(t, env.srv).Enqueue(scheduling.IgnitionRequest{
		Card: coordCard, Squad: "coord", Actor: "test", Ready: true,
	}, scheduling.KindLaunchQueue); err != nil {
		t.Fatalf("入队协调者: %v", err)
	}
	if _, err := mustScheduling(t, env.srv).Enqueue(scheduling.IgnitionRequest{
		Card: ids[0], Squad: "sq2", Node: "implement", Actor: "test", Ready: true,
	}, scheduling.KindIgnitionQueue); err != nil {
		t.Fatalf("入队执行者: %v", err)
	}
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {
		runner.markDispatch()
	}

	processed, err := env.srv.drainQueuesOnce(context.Background())
	if err != nil || processed != 2 {
		t.Fatalf("协调者无位后应继续清队，processed=%d err=%v", processed, err)
	}
	waitFor(t, func() bool {
		_, _, trace := runner.snapshot()
		for _, event := range trace {
			if event == "dispatch" && !env.srv.cardStepInFlight(ids[0]) {
				return true
			}
		}
		return false
	})
	rows, err := mustScheduling(t, env.srv).QueueSnapshot()
	if err != nil {
		t.Fatalf("读回填队列: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != scheduling.KindLaunchQueue || rows[0].Req.Card != coordCard {
		t.Fatalf("协调者请求应在队列、执行者应已消费: %+v", rows)
	}
	_, _, trace := runner.snapshot()
	for _, event := range trace {
		if event == "dispatch" {
			return
		}
	}
	t.Fatalf("另一载体执行者未穿过派发入口: %v", trace)
}

func setupNoPTYSquadEnv(t *testing.T, carrierMax int) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(configPath, env.srv.conf()); err != nil {
		t.Fatalf("准备配置: %v", err)
	}
	env.srv.SetConfigPath(configPath)
	SetupAutomationForTest(t, env.srv, env.ledger)
	yes := true
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "ftm", ftm)
	if ver := seedDisciplineOnLedger(t, env, discipline.NameImplement, "# 实现纪律\n完成即 commit\n"); ver < 1 {
		t.Fatalf("纪律块版本异常: %d", ver)
	}
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "c1", Machine: "ftm", CLI: "opencode",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: carrierMax,
		Status: scheduling.StatusOnline,
	})
	if err := mustScheduling(t, env.srv).PutSquad(scheduling.Squad{
		Name: "sq1", Role: scheduling.RoleExecutor, Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 8}},
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
		{req: scheduling.IgnitionRequest{Card: ids[1], Squad: "sq1", Node: "implement", Actor: "test", Ready: true}, kind: scheduling.KindIgnitionQueue},
		{req: scheduling.IgnitionRequest{Card: ids[2], Squad: "sq1", Node: "implement", Actor: "test", Ready: true}, kind: scheduling.KindIgnitionQueue},
	} {
		if _, err := mustScheduling(t, env.srv).Enqueue(req.req, req.kind); err != nil {
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
	rows, err := mustScheduling(t, env.srv).QueueSnapshot()
	if err != nil {
		t.Fatalf("读队列快照: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("重放后仍有 %d 行队列: %+v", len(rows), rows)
	}
	// print 形态：协调者队列回合结束即落席位并归还名额（不再靠 tab 存活占位）。
	coordCard, err := env.ledger.GetCard(ids[0])
	if err != nil {
		t.Fatalf("读回协调者席位: %v", err)
	}
	if coordCard.DriverSession == "" || coordCard.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("协调者队列回合应落下 coordinate 席位，got session=%q source=%q",
			coordCard.DriverSession, coordCard.DriverSource)
	}
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("协调者回合结束应立即释放名额 %s=%d，want 0", key, got)
		}
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("执行者空座不应自动 Resume：resume=%d", len(resumes))
	}
}

func TestAutomationIgnitionDrainWakesBeforeTrueDispatch(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 2)
	runner := seedQueueCoordinator(t, env)
	ids := seedSquadFlow(t, env, "sq1", 2)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), ids[1], "coordinate", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码预绑定协调者席位: %v", err)
	}
	if err := env.ledger.BindSeat(ids[1], identity, proto.SeatSourceCoordinate, ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写预绑定协调者席位: %v", err)
	}
	if _, err := mustScheduling(t, env.srv).Enqueue(scheduling.IgnitionRequest{
		Card: ids[1], Squad: "sq1", Node: "implement", Actor: "test", Ready: true,
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
	SetupAutomationForTest(t, env.srv, env.ledger)
	_ = seedQueueCoordinator(t, env)
	cardID := createCoordCard(t, env)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "launch", call: func() error {
			_, err := env.srv.launchCoordinatorRound(context.Background(), cardID, "coordinate")
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
			// print 形态：launch 与 wake 都在回合返回即归还两级名额，不留窗口占用。
			for key := range map[string]struct{}{
				"squad/coord/coord-carrier": {},
				"carrier/coord-carrier":     {},
			} {
				if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
					t.Fatalf("%s 后计数 %s=%d，want 0", tc.name, key, got)
				}
			}
		})
	}
}

func TestAutomationReleaseKicksDrain(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	cardID := createCoordCard(t, env)
	// print 形态：名额在 launch 回合返回时即由 releaseSchedulingBinding 归还并 kick，
	// 不再等「关 TUI tab」触发。
	if _, err := env.srv.launchCoordinatorRound(context.Background(), cardID, "coordinate"); err != nil {
		t.Fatalf("拉起回合: %v", err)
	}
	select {
	case <-env.srv.automationKick:
	default:
		t.Fatal("回合归还名额后没有收到清队唤醒信号")
	}
}

// bearingTraceRunner 记录 Resume 收到的 SessionRef，用来断言唤醒 spec 来自承载记录
// （既有 queueTraceRunner.Resume 丢弃 ref，锁不住 HomeDir/Model 来源）。
// onResume 可选：在 Resume 进行中（名额尚未归还）采集一次观测，供 §4-29 的
// 「回合进行中占用读数」锁点用——回合结束后的计数对 AdmitSeatCarrier 与
// LaunchAdmit 不可区分（§5.1(b) 实测）。
type bearingTraceRunner struct {
	mu          sync.Mutex
	refs        []keysclient.SessionRef
	onResume    func()
	duringRound map[string]int
}

func (r *bearingTraceRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: "bearing-session"}, nil
}

func (r *bearingTraceRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	r.refs = append(r.refs, ref)
	hook := r.onResume
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

func (r *bearingTraceRunner) snapshot() []keysclient.SessionRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]keysclient.SessionRef(nil), r.refs...)
}

func (r *bearingTraceRunner) duringSnapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.duringRound))
	for k, v := range r.duringRound {
		out[k] = v
	}
	return out
}

// TestB389WakeUsesBearingForSpec 锁 B389 §3.2 第 4 步：本机唤醒的 SessionSpec
// 逐项来自承载记录（HomeDir/Model），CLI 由席位身份解出，不再现挑载体。
func TestB389WakeUsesBearingForSpec(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate",
		keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate, ledger.SeatBearing{
		Carrier: "coord-carrier", Machine: "local", HomeDir: "/tmp/coord-home", Model: "m-bearing",
	}); err != nil {
		t.Fatalf("写承载: %v", err)
	}
	if _, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}}, nil); err != nil {
		t.Fatalf("承载唤醒: %v", err)
	}
	refs := runner.snapshot()
	if len(refs) != 1 {
		t.Fatalf("应恰一次 Resume，实得 %d", len(refs))
	}
	if refs[0].HomeDir != "/tmp/coord-home" || refs[0].Model != "m-bearing" || refs[0].CLI != "opencode" {
		t.Fatalf("唤醒 spec 未来自承载: %+v", refs[0])
	}
}

// TestB389WakeWithoutBearingFailsExplicitly 锁 B389 §2.3：coordinate 席位缺承载
// 时不得静默本机执行——落恰一条 EvSeatBearingMissing、零回合、返回 nil（批次
// 照常收尾，否则该 seq 一直重试）。
func TestB389WakeWithoutBearingFailsExplicitly(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	deleteSeatBearingRow(t, env, cardID)
	if _, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}}, nil); err != nil {
		t.Fatalf("缺承载应落事件后返回 nil（跳过），得 %v", err)
	}
	if refs := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("缺承载不得发起回合: %+v", refs)
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	missing := 0
	for _, ev := range events {
		if ev.Type == ledger.EvSeatBearingMissing {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("应恰落一条 EvSeatBearingMissing，实得 %d", missing)
	}
}

// TestB389TransferPostsOnceWithBatchSeqs 锁 §4-25：承载在远端时，唤醒恰发出一次
// POST /api/cards/{id}/coordinator/wake，body 的 events[].seq 等于本批，且本机零回合。
func TestB389TransferPostsOnceWithBatchSeqs(t *testing.T) {
	var (
		mu       sync.Mutex
		calls    int
		gotPath  string
		gotBody  string
		gotFwdHd string
	)
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		gotPath, gotBody, gotFwdHd = r.URL.Path, string(b), r.Header.Get("X-Handoff-Forwarded")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"woke":true,"session_id":"sess-remote","handled_by":"linux-01"}`))
	}))
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{"linux-01": {Addr: remote.URL, Token: testToken}})
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-old")
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("落远端承载: %v", err)
	}
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "remote", "completed", 1, `{"text":"done"}`)
	raws := []proto.LedgerEvent{{Seq: seq, CardID: cardID, Type: ledger.EvTaskMirrored}}
	result, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}}, raws)
	if err != nil {
		t.Fatalf("转交唤醒: %v", err)
	}
	if !result.Woke || result.SessionID != "sess-remote" {
		t.Fatalf("转交结果未透传: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("应恰发一次 POST，实得 %d", calls)
	}
	if gotPath != "/api/cards/"+cardID+"/coordinator/wake" {
		t.Fatalf("路径=%s", gotPath)
	}
	if gotFwdHd != "1" {
		t.Fatalf("缺防环头 X-Handoff-Forwarded: %q", gotFwdHd)
	}
	var body struct {
		Seat   string `json:"seat"`
		Events []struct {
			Seq int64 `json:"seq"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("body 非 JSON: %s", gotBody)
	}
	if body.Seat != seat || len(body.Events) != 1 || body.Events[0].Seq != seq {
		t.Fatalf("body 缺 seat/seq: %s", gotBody)
	}
	// 本机零回合：runner 未被调用。
	if refs := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("远端承载本机不得跑回合: %+v", refs)
	}
}

// TestB389RemoteBearingDoesNotRunLocally 锁 §4-23：承载在远端且无转交目标时本机
// 不跑回合（转交失败路径），零 resume/launch。
func TestB389RemoteBearingDoesNotRunLocally(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-remote")
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("落远端承载: %v", err)
	}
	_, _ = env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}},
		[]proto.LedgerEvent{{Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored}})
	if refs := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("远端承载本机不得跑回合: %+v", refs)
	}
}

// TestB389RemoteBearingKeepsLocalSlotsUnchanged 锁 §4-24：远端归属时本机协调者
// 两键计数不变。
func TestB389RemoteBearingKeepsLocalSlotsUnchanged(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-remote")
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("落远端承载: %v", err)
	}
	_, _ = env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}},
		[]proto.LedgerEvent{{Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored}})
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("远端归属不得占本机名额 %s=%d", key, got)
		}
	}
	// 断言本机零回合：否则「计数为 0」只是 defer 释放后的读数，拦不住误跑。
	if refs := runner.snapshot(); len(refs) != 0 {
		t.Fatalf("远端归属本机不得跑回合: %+v", refs)
	}
}

// TestB389WakeUsesFrozenCarrierNotLaunchAdmit 锁 §4-29：唤醒只打承载记录里的
// 载体，即使小队里另有可用成员也不做候选遍历。锁点必须能区分 AdmitSeatCarrier
// 与 LaunchAdmit——两者在回合结束后的计数都归零（§5.1(b) 实测原件如此，抓不住），
// 故改读「回合进行中」的两级占用：冻结路径下承载载体键为 1、首个成员键为 0；
// 换成 LaunchAdmit 会按小队顺序回落到首个成员，读数恰好相反。
// 本测试自建双成员协调者小队，不复用 seedQueueCoordinator（后者已登记单成员
// coord 小队，再 PutSquad(expect=0) 会 CAS 冲突）。
func TestB389WakeUsesFrozenCarrierNotLaunchAdmit(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier-2", Machine: "local", CLI: "opencode",
		HomeDir: "/tmp/coord-home-2", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{
			{Carrier: "coord-carrier", MaxConcurrency: 1},
			{Carrier: "coord-carrier-2", MaxConcurrency: 1},
		}}, 0); err != nil {
		t.Fatalf("登记双成员协调者小队: %v", err)
	}
	// 回合进行中的占用读数（Resume 内采集）：冻结路径应为
	// carrier/coord-carrier-2=1、carrier/coord-carrier=0；LaunchAdmit 回落到
	// 首个成员后读数恰好相反。
	runner := &bearingTraceRunner{}
	runner.onResume = func() {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		runner.duringRound = map[string]int{
			"carrier/coord-carrier":   runningCountIn(t, env.srv.autoLedger, "carrier/coord-carrier"),
			"carrier/coord-carrier-2": runningCountIn(t, env.srv.autoLedger, "carrier/coord-carrier-2"),
		}
	}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate",
		keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写初始承载: %v", err)
	}
	// 把承载改冻结到第二个成员：唤醒必须打它，而不是按小队顺序回落到第一个。
	if err := env.ledger.SetSeatBearing(cardID, identity, ledger.SeatBearing{
		Carrier: "coord-carrier-2", Machine: "local", HomeDir: "/tmp/coord-home-2",
	}); err != nil {
		t.Fatalf("写冻结承载: %v", err)
	}
	appendMirroredForConsumer(t, env.ledger, cardID, "frozen", "completed", 1, `{"text":"done"}`)
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("消费: %v", err)
	}
	if refs := runner.snapshot(); len(refs) != 1 {
		t.Fatalf("应唤醒一次，实得 %d", len(refs))
	}
	if refs := runner.snapshot(); refs[0].HomeDir != "/tmp/coord-home-2" {
		t.Fatalf("唤醒应使用冻结载体的环境: %+v", refs[0])
	}
	during := runner.duringSnapshot()
	if during["carrier/coord-carrier-2"] != 1 {
		t.Fatalf("回合进行中冻结载体应被占用: %v（AdmitSeatCarrier 未生效或换成了 LaunchAdmit）", during)
	}
	if during["carrier/coord-carrier"] != 0 {
		t.Fatalf("回合进行中不得占用首个成员（LaunchAdmit 候选遍历特征）: %v", during)
	}
	// 回合结束后名额归还（§4-24 同族读数的收尾侧）。
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier",
		"squad/coord/coord-carrier-2", "carrier/coord-carrier-2"} {
		if got := runningCountIn(t, env.srv.autoLedger, key); got != 0 {
			t.Fatalf("回合结束名额应归还/不误占 %s=%d", key, got)
		}
	}
}
