// b439_4_selfname_test.go —— B439.4 真机缺陷：自名未接入 IsSelfTarget 系本机判据。
//
// 职责：有自名的机器上，以 Server.IsSelfTarget（原按本地 targets 地址判）判「本机」的
// 调用点必须认自名——任务镜像（workspace.NewMirror）与预览镜像（NewPreviewMirror）注入
// 的本机判据不再订阅本机自己；scheddrain.go（carrier.Machine、bearing.Machine）、
// desktopwake.go（deliverDesktopLetter、wakeDesktopSeat）、pty_api.go 的本机判断在
// 「值 == 自名」时判本机。无自名机器行为逐点不变（S5）。
// 边界：只测这些调用点的本机判据；镜像/唤醒/扇出的其余语义由既有测试负责，不在本文件
// 重测。不新增生产缝。
package agentd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/workspace"
)

const (
	b439SelfName = "linux-01"
	b439Remote   = "macbook-pro"
	b439Token    = "0123456789abcdef0123456789abcdef"
)

// TestB439_4IsSelfTargetRecognizesSelfName 是核心判据测试：有自名时「值 == 自名」判本机
// （与地址无关），同时保留今天的地址判法；无自名时逐点不变（S5）。
func TestB439_4IsSelfTargetRecognizesSelfName(t *testing.T) {
	env := newTestAgentdEnv(t)
	cfg := env.srv.conf()
	cfg.Listen = "127.0.0.1:7777"
	cfg.Name = b439SelfName
	cfg.Targets = map[string]config.Target{
		// 名字是自名，但地址指向远端：自名判据必须独立于地址成立。
		b439SelfName: {Addr: "10.0.0.9:7777", Token: b439Token},
		// 登记名，地址恰好指向本机 listen：地址判据必须保留。
		"self-by-addr": {Addr: "127.0.0.1:7777", Token: b439Token},
		b439Remote:     {Addr: "10.0.0.8:7777", Token: b439Token},
	}

	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"空串是本机", "", true},
		{"自名是本机（不认地址）", b439SelfName, true},
		{"地址指向本机的登记名仍是本机", "self-by-addr", true},
		{"远端登记名不是本机", b439Remote, false},
		{"未知名不是本机", "nope", false},
	} {
		if got := env.srv.IsSelfTarget(tc.in); got != tc.want {
			t.Errorf("%s: IsSelfTarget(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}

	// 无自名（S5）：回退地址判据，逐点不变。
	cfg.Name = ""
	if !env.srv.IsSelfTarget("") {
		t.Error("无自名：空串仍应判本机")
	}
	if !env.srv.IsSelfTarget("self-by-addr") {
		t.Error("无自名：地址指向本机的登记名仍应判本机")
	}
	if env.srv.IsSelfTarget(b439SelfName) {
		t.Error("无自名：地址指向远端的登记名不得因名字巧合被判本机")
	}
	if env.srv.IsSelfTarget(b439Remote) {
		t.Error("无自名：远端登记名不得判本机")
	}
}

// b439MirrorSource 是 workspace.RemoteTaskSource 的内存替身：记录每台被查询的机器。
type b439MirrorSource struct {
	mu      sync.Mutex
	names   []string
	queried []string
}

func (s *b439MirrorSource) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

func (s *b439MirrorSource) For(name string) (workspace.RemoteTaskClient, error) {
	s.mu.Lock()
	s.queried = append(s.queried, name)
	s.mu.Unlock()
	return b439MirrorClient{}, nil
}

func (s *b439MirrorSource) wasQueried(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, q := range s.queried {
		if q == name {
			return true
		}
	}
	return false
}

type b439MirrorClient struct{}

func (b439MirrorClient) ListTasks(context.Context) ([]proto.TaskView, error) { return nil, nil }

func (b439MirrorClient) StreamEventsOnce(context.Context, string, int64, func(proto.Event) error) error {
	return nil
}

type b439NopPublisher struct{}

func (b439NopPublisher) Publish(proto.Event) {}

// TestB439_4TaskMirrorSkipsSelfName 锁任务镜像注入的本机判据认自名：设备表源（池 Names
// 含自名行）下不再订阅本机自己。从真实注入值 srv.IsSelfTarget 进，观察远端被查询而自名不被查询。
func TestB439_4TaskMirrorSkipsSelfName(t *testing.T) {
	env := newTestAgentdEnv(t)
	env.srv.conf().Name = b439SelfName
	src := &b439MirrorSource{names: []string{b439SelfName, b439Remote}}
	m := workspace.NewMirror(src, env.st, b439NopPublisher{}, env.srv.IsSelfTarget, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for !src.wasQueried(b439Remote) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	m.Stop()

	if !src.wasQueried(b439Remote) {
		t.Fatal("远端机器应被任务镜像查询")
	}
	if src.wasQueried(b439SelfName) {
		t.Fatalf("任务镜像不得订阅本机自名 %q，实得查询序列 %v", b439SelfName, src.queried)
	}
}

// TestB439_4PreviewMirrorSkipsSelfName 锁预览镜像注入的本机判据认自名：池 Names 含自名行
// 时不再把本机自己列进远端投影。
func TestB439_4PreviewMirrorSkipsSelfName(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:7777",
		Name:   b439SelfName,
		Targets: map[string]config.Target{
			b439SelfName: {Addr: "127.0.0.1:1", Token: b439Token},
			b439Remote:   {Addr: "127.0.0.1:1", Token: b439Token},
		},
	}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()
	pm := NewPreviewMirror(s.Pool(), nil, nil, s.IsSelfTarget, testLogger(t))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := pm.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	names := map[string]bool{}
	for _, m := range resp.Machines {
		names[m.Name] = true
	}
	if names[b439SelfName] {
		t.Fatalf("预览镜像不得把本机自名 %q 列进远端投影，实得 %v", b439SelfName, resp.Machines)
	}
	if !names[b439Remote] {
		t.Fatalf("远端机器应出现在预览投影，实得 %v", resp.Machines)
	}
}

// TestB439_4PtyFanoutSkipsSelfName 锁 pty_api.go 的本机判断认自名：scope=all 扇出不再
// 把本机自名当远端回环去连。
func TestB439_4PtyFanoutSkipsSelfName(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:7777",
		Name:   b439SelfName,
		Targets: map[string]config.Target{
			b439SelfName: {Addr: "127.0.0.1:1", Token: b439Token},
			b439Remote:   {Addr: "127.0.0.1:1", Token: b439Token},
		},
	}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()

	out := s.ptySessionsAll(httptest.NewRequest(http.MethodGet, "/api/pty/sessions", nil), nil)
	names := map[string]bool{}
	for _, m := range out.Machines {
		names[m.Name] = true
	}
	if names[b439SelfName] {
		t.Fatalf("PTY 扇出不得把本机自名 %q 当远端回环，实得 %v", b439SelfName, out.Machines)
	}
	if !names[b439Remote] {
		t.Fatalf("远端机器应出现在 PTY 扇出，实得 %v", out.Machines)
	}
}

// TestB439_4CoordinatorLaunchSelfNameCarrierIsLocal 锁 scheddrain.go:284（carrier.Machine）
// 的本机判断认自名：协调者载体登记在自名机器上 → 本机拉起，不再当远端拒绝。
func TestB439_4CoordinatorLaunchSelfNameCarrierIsLocal(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	allowCarrierMachines(t, env.srv, b439SelfName)
	env.srv.conf().Name = b439SelfName
	svc := mustScheduling(t, env.srv)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "coord-carrier", Machine: b439SelfName, CLI: "opencode",
		HomeDir: "/tmp/coord-home", MaxConcurrency: 1,
	})
	if err := svc.PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	runner := &queueTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)

	if _, err := env.srv.launchCoordinatorRound(context.Background(), cardID, "coordinate"); err != nil {
		t.Fatalf("自名载体的协调者回合应判本机、可拉起，实得: %v", err)
	}
	if launches, _, _ := runner.snapshot(); launches != 1 {
		t.Fatalf("自名载体应在本机拉起一次，实得 launch=%d", launches)
	}
}

// TestB439_4CoordinatorWakeBearingSelfNameIsLocal 锁 scheddrain.go:510（bearing.Machine）
// 的本机判断认自名：承载登记在自名机器上 → 本机唤醒，不再转交。
func TestB439_4CoordinatorWakeBearingSelfNameIsLocal(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	env.srv.conf().Name = b439SelfName
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, err := proto.EncodeSeatIdentity("opencode", "sess-self")
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate, ledger.SeatBearing{
		Carrier: "coord-carrier", Machine: b439SelfName, HomeDir: "/tmp/coord-home",
	}); err != nil {
		t.Fatalf("写自名承载: %v", err)
	}
	if _, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}},
		[]proto.LedgerEvent{{Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored}}); err != nil {
		t.Fatalf("自名承载应在本机唤醒，实得错误: %v", err)
	}
	if refs := runner.snapshot(); len(refs) != 1 {
		t.Fatalf("自名承载应恰一次本机 Resume，实得 %d", len(refs))
	}
}

// TestB439_4ResidentDoorbellSelfNameIsLocal 是 B439 两条桌面端「自名判本机」锁（deliverDesktopLetter、
// wakeDesktopSeat，随 B435 X 退役删除）在替代通路上的移植：驻场者登记在自名机器上（machine == 自名、
// 非 hostname）→ 本机写恰一封门铃信 + 一次 continue，不转交、不跳过（residentwake.go 经 IsSelfTarget
// 认自名）。
func TestB439_4ResidentDoorbellSelfNameIsLocal(t *testing.T) {
	env, _, sessionID, _, home, spy := o2ResidentEnv(t)
	if b439SelfName == o2Hostname(t) {
		t.Skip("夹具前提：自名须不同于本机 hostname")
	}
	env.srv.conf().Name = b439SelfName
	b435ExecSQL(t, env, `UPDATE session_residents SET machine = ? WHERE session = ?`, b439SelfName, sessionID)
	seq := sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "人类无 @（自名机器上的驻场者）", nil, 0)
	o2Consume(t, env.srv)
	letters := o2Letters(t, home)
	if len(letters) != 1 {
		t.Fatalf("自名机器上的驻场者应本机写恰一封门铃信，得 %d", len(letters))
	}
	if want := o2WantLetter(sessionID, 1, seq, seq); letters[0].body != want {
		t.Fatalf("信件字节不符: %q", letters[0].body)
	}
	if n := len(spy.calls()); n != 1 {
		t.Fatalf("应恰发一次 continue 深链，得 %d", n)
	}
}
