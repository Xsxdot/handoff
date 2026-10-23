// b464_resident_carrier_test.go —— B464 最终审查第 2 轮 N1：驻场续轮必须把席位载体名一路带到
// keystone 与出站 Runner，协调者回合才能按载体取其所选登录的代理。
//
// 职责：从生产入口 wakeResidentRound（受理由 agentd 驻场批组装的驻场行参数）进，经真 keystone
// 到 coordinatorRunner → Coordinator；夹具是「同一 HOME 两条登录、两个载体各选其一、代理不同」，
// 断言 Resume 回合拿到的是席位载体那条登录的代理（不是按 (CLI, HOME) 反查——同 HOME 下无从判定）。
// 边界：不起真实 CLI（Coordinator 换成记录桩）；登录代理经账号域登录行写入，不读真实凭据。
package agentd

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestB464ResidentRoundCarriesSeatCarrierToLoginProxy(t *testing.T) {
	env := newSchedEnv(t)
	acc := wireAccounts(t, env)
	self := env.srv.conf().Name
	if self == "" {
		self = "local"
	}
	home := t.TempDir() // 同一 HOME 两条登录：反查无法判定，只有载体名能定
	res, err := acc.Reconcile(accounts.Discovery{Machine: self, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: home, HomeAbs: home,
		Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:111111111111"},
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:222222222222"},
		},
	}}})
	if err != nil || len(res.Logins) != 2 {
		t.Fatalf("同 HOME 应建两条登录：%+v %v", res, err)
	}
	const proxyA, proxyB = "http://u:p@127.0.0.1:1081", "http://u:p@127.0.0.1:1082"
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[0].ID, proxyA)
	setLoginProxy(t, env.srv.SchedulingRegistry(), res.Logins[1].ID, proxyB)
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "coord-a", Machine: self, CLI: "opencode",
		MaxConcurrency: 1, Login: res.Logins[0].ID})
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "coord-b", Machine: self, CLI: "opencode",
		MaxConcurrency: 1, Login: res.Logins[1].ID})
	if err := env.svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "coord-a", MaxConcurrency: 1}, {Carrier: "coord-b", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("登记协调小队: %v", err)
	}
	rec := &b453RecCoord{}
	env.srv.SetKeystone(keystone.New(coordinatorRunner{coord: rec,
		isPushResidentSession: func(string) (bool, error) { return false, nil },
		accountProxy:          env.srv.coordinatorAccountProxy,
	}, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	resident := proto.SessionResident{
		Session: "", Identity: "agent:coord", DriverMode: proto.ResidentDriverPush,
		PushCLI: "opencode", PushSession: "ses_b464_carrier", Carrier: "coord-b", CarrierMachine: self,
		HomeDir: home, Workdir: t.TempDir(), Model: "b464-model",
	}
	session, err := env.srv.rooms.CreateSession("B464 resident round", "agent:coord", "agent:coord")
	if err != nil {
		t.Fatal(err)
	}
	resident.Session = session.ID
	if _, err := env.srv.wakeResidentRound(context.Background(), resident, proto.Session{},
		[]keystone.WakeEvent{{Kind: keystone.WakeMessage, Summary: "继续"}}, "b464-round"); err != nil {
		t.Fatalf("驻场续轮: %v", err)
	}
	if got, ok := envValue(rec.resume.Env, "HTTPS_PROXY"); !ok || got != proxyB {
		t.Fatalf("续轮应取席位载体 coord-b 那条登录的代理 %q，得 %q（ok=%v）", proxyB, got, ok)
	}
	if got, _ := envValue(rec.resume.Env, "HTTPS_PROXY"); got == proxyA {
		t.Fatalf("续轮取错了另一条登录的代理 %q", proxyA)
	}
}
