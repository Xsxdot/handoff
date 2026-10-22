// b464_coordinator_carrier_test.go —— B464 段 9 I-2：卡协调者链的载体名必须从生产入口一路带到
// coordinatorRunner，才能按席位载体取其所选登录的代理。
//
// 职责：两条生产入口各一支——①清队首轮（drainQueuesOnce → runOnCoordinatorCarrier 的 Launch
// spec）；②冷唤醒（wakeCoordinatorRound，keystone 无进程内缓存 sessions，走 overlayResumeRef）。
// 夹具是「同一 HOME 两条登录、两个载体各选其一、代理不同」：按（CLI, HOME）反查无法判定，
// 只有载体名能定。
// 边界：Coordinator 是记录桩（不起真实 CLI）；登录与代理经账号域/登录行写入，不读真实凭据。
package agentd

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// b464SeedSameHomeCarriers 建出「同 HOME 两登录、两载体各选其一、代理不同」的协调者小队，
// 返回 HOME 与两条代理（载体 A/B 的登录顺序）。小队成员顺序 B、A：LaunchAdmit 取第一个。
func b464SeedSameHomeCarriers(t *testing.T, env *ledgerEnv) (home, proxyA, proxyB string) {
	t.Helper()
	svc := mustScheduling(t, env.srv)
	acc := accounts.New(env.srv.SchedulingRegistry())
	env.srv.SetAccounts(acc)
	svc.SetAccounts(acc)
	acc.SetReferrers(svc.LoginReferrers)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	home = t.TempDir() // 同一 HOME 两条登录：反查无法判定，只有载体名能定
	res, err := acc.Reconcile(accounts.Discovery{Machine: self, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: home,
		Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:333333333333"},
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:444444444444"},
		},
	}}})
	if err != nil || len(res.Logins) != 2 {
		t.Fatalf("同 HOME 应建两条登录：%+v %v", res, err)
	}
	proxyA, proxyB = "http://u:p@127.0.0.1:2081", "http://u:p@127.0.0.1:2082"
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[0].ID, proxyA)
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[1].ID, proxyB)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "coord-a", Machine: self, CLI: "opencode",
		MaxConcurrency: 1, Login: res.Logins[0].ID})
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "coord-b", Machine: self, CLI: "opencode",
		MaxConcurrency: 1, Login: res.Logins[1].ID})
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "coord-b", MaxConcurrency: 1}, {Carrier: "coord-a", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("登记协调小队: %v", err)
	}
	return home, proxyA, proxyB
}

// 清队首轮：launch_queue 出队 → 协调者小队准入 → Launch spec 带席位载体名 → 登录代理。
func TestB464QueuedFirstRoundCarriesSeatCarrierToLoginProxy(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 1)
	_, proxyA, proxyB := b464SeedSameHomeCarriers(t, env)
	rec := &b453RecCoord{}
	env.srv.SetKeystone(keystone.New(coordinatorRunner{coord: rec,
		isPushResidentSession: func(string) (bool, error) { return false, nil },
		accountProxy:          env.srv.coordinatorAccountProxy,
	}, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	card := createCoordCard(t, env)
	if _, err := mustScheduling(t, env.srv).Enqueue(scheduling.IgnitionRequest{
		Card: card, Squad: "coord", Actor: "test", Ready: true,
	}, scheduling.KindLaunchQueue); err != nil {
		t.Fatalf("入队协调者首轮: %v", err)
	}
	processed, err := env.srv.drainQueuesOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("清队 processed=%d err=%v，want 1/nil", processed, err)
	}
	if got, ok := envValue(rec.launch.Env, "HTTPS_PROXY"); !ok || got != proxyB {
		t.Fatalf("首轮应取席位载体 coord-b 那条登录的代理 %q，得 %q（ok=%v）", proxyB, got, ok)
	}
	if got, _ := envValue(rec.launch.Env, "HTTPS_PROXY"); got == proxyA {
		t.Fatalf("首轮取错了另一条登录的代理 %q", proxyA)
	}
}

// 冷唤醒：席位承载 coord-b，本 keystone 无 sessions 缓存 → overlayResumeRef 必须把 spec 的
// 载体名盖进续接 ref → Resume 回合取登录代理。
func TestB464ColdWakeCarriesSeatCarrierToLoginProxy(t *testing.T) {
	env := setupNoPTYSquadEnv(t, 1)
	home, proxyA, proxyB := b464SeedSameHomeCarriers(t, env)
	rec := &b453RecCoord{}
	env.srv.SetKeystone(keystone.New(coordinatorRunner{coord: rec,
		isPushResidentSession: func(string) (bool, error) { return false, nil },
		accountProxy:          env.srv.coordinatorAccountProxy,
	}, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	card := createCoordCard(t, env)
	identity, err := proto.EncodeSeatIdentity("opencode", "ses_b464_cold")
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := env.ledger.BindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush,
		ledger.SeatBearing{Carrier: "coord-b", Machine: "local", HomeDir: home, Workdir: t.TempDir(), Model: "b464-cold"}); err != nil {
		t.Fatalf("坐 push 席位: %v", err)
	}
	if _, err := env.srv.wakeCoordinatorRound(context.Background(), card,
		[]keystone.WakeEvent{{Kind: keystone.WakeMessage, Summary: "继续"}}); err != nil {
		t.Fatalf("冷唤醒: %v", err)
	}
	if got, ok := envValue(rec.resume.Env, "HTTPS_PROXY"); !ok || got != proxyB {
		t.Fatalf("冷唤醒应取席位载体 coord-b 那条登录的代理 %q，得 %q（ok=%v）", proxyB, got, ok)
	}
	if got, _ := envValue(rec.resume.Env, "HTTPS_PROXY"); got == proxyA {
		t.Fatalf("冷唤醒取错了另一条登录的代理 %q", proxyA)
	}
}
