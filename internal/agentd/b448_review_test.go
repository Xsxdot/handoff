package agentd

// b448_review_test.go —— B448 review 修复轮补测（I-2 / M-1）：
//   - I-2（F15）：双成员协调小队里受理端选定的冻结载体，执行机只按它准入与拉起，不经 LaunchAdmit 改选；
//     冻结载体离线 / 满员时执行机回准入拒绝，而不是改用另一成员。
//   - M-1（F35）：驻场端点同卡端点——执行机 server 级 WriteTimeout 比回合短时回执仍完整送达。
//
// 边界：沿用 b448_pair_test.go 的双 agentd 共享账本夹具，打受理机 A 的真实入口；只加测试不改生产装配。

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// b448CarrierY 是双成员小队里排在冻结载体 X（b448Carrier）之前的成员：LaunchAdmit 按成员声明顺序先试它，
// 所以执行机若改用 LaunchAdmit 必然改选 Y。HOME / 模型与 X 不同，拉起 spec 可直接分辨用了谁。
const (
	b448CarrierY = "t448-oc-y"
	b448HomeX    = "/home/t448"
	b448HomeY    = "/home/t448-y"
	b448ModelX   = "prov/model-x"
	b448ModelY   = "prov/model-y"
)

// b448TwoMemberSquad 给 X 配模型、登记 Y，并把小队改成 [Y, X] 两个成员（都在执行机 B、上限各 1）。随后占住
// Y 的名额，使受理端 LaunchAdmit 跳过 Y 选中 X 转交；返回 Y 的占位 binding，由调用方在转交途中归还。
func b448TwoMemberSquad(t *testing.T, p *b448Pair) scheduling.Binding {
	t.Helper()
	carriers, err := p.a.sched.CarrierRows()
	if err != nil {
		t.Fatalf("读载体: %v", err)
	}
	xVersion := -1
	for _, r := range carriers {
		if r.Carrier.Name == b448Carrier {
			xVersion = r.Version
		}
	}
	if xVersion < 0 {
		t.Fatalf("找不到载体 %s", b448Carrier)
	}
	if err := p.a.sched.PutCarrier(scheduling.Carrier{
		Name: b448Carrier, Machine: b448MachineB, CLI: "opencode", Model: b448ModelX,
		HomeDir: b448HomeX, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	}, xVersion); err != nil {
		t.Fatalf("给 X 配模型: %v", err)
	}
	putOnlineCarrier(t, p.a.sched, scheduling.Carrier{
		Name: b448CarrierY, Machine: b448MachineB, CLI: "opencode", Model: b448ModelY,
		HomeDir: b448HomeY, MaxConcurrency: 1,
	})
	rows, err := p.a.sched.SquadRows()
	if err != nil {
		t.Fatalf("读小队: %v", err)
	}
	version := -1
	for _, r := range rows {
		if r.Squad.Name == b448Squad {
			version = r.Version
		}
	}
	if version < 0 {
		t.Fatalf("找不到协调小队 %s", b448Squad)
	}
	if err := p.a.sched.PutSquad(scheduling.Squad{
		Name: b448Squad, Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: b448CarrierY, MaxConcurrency: 1}, {Carrier: b448Carrier, MaxConcurrency: 1}},
	}, version); err != nil {
		t.Fatalf("改双成员小队: %v", err)
	}
	if x, err := p.a.sched.Carrier(b448Carrier); err != nil || x.Status != scheduling.StatusOnline || x.Model != b448ModelX {
		t.Fatalf("X 应在线且带模型: %+v err=%v", x, err)
	}
	held, err := p.a.sched.AdmitSeatCarrier(b448Squad, b448CarrierY)
	if err != nil {
		t.Fatalf("预占 Y 名额: %v", err)
	}
	return held
}

func b448CountsY(t *testing.T, p *b448Pair) (squad, carrier int) {
	t.Helper()
	return runningCountIn(t, p.a.srv.autoLedger, "squad/"+b448Squad+"/"+b448CarrierY),
		runningCountIn(t, p.a.srv.autoLedger, "carrier/"+b448CarrierY)
}

// I-2 正例（F15）：受理机选定 X 转交；途中 Y 空出（执行机若 LaunchAdmit 会改选 Y）。执行机只按冻结的 X 拉起：
// Runner 收到的 HOME / 模型是 X 的，承载记 X，Y 计数全程为 0。
func TestB448FrozenCarrierNotReselectedInTwoMemberSquad(t *testing.T) {
	p := newB448Pair(t, b448PairOpts{})
	held := b448TwoMemberSquad(t, p)
	proxy := newB448Proxy(t, p.b)
	p.a.setTargets(t, map[string]string{b448MachineB: proxy.addr()})
	proxy.before = func(string) {
		if err := p.b.sched.Release(held.Squad, held.Carrier); err != nil {
			t.Errorf("途中归还 Y: %v", err)
		}
	}
	var midYSquad, midYCarrier int
	p.b.runner.onLaunch = func(keysclient.SessionSpec) { midYSquad, midYCarrier = b448CountsY(t, p) }
	card := b448Card(t, p)

	code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
	if code != http.StatusOK {
		t.Fatalf("冻结载体 X 拉起应 200，得 %d %s", code, body)
	}
	proxy.mu.Lock()
	bodies := proxy.bodies
	proxy.mu.Unlock()
	if len(bodies) != 1 || string(bodies[0]["carrier"]) != `"`+b448Carrier+`"` {
		t.Fatalf("受理机应选定 X 转交一次: %v", bodies)
	}
	specs := p.b.runner.launchSpecs()
	if len(specs) != 1 || specs[0].HomeDir != b448HomeX || specs[0].Model != b448ModelX {
		t.Fatalf("执行机应只用冻结载体 X（HOME %s / 模型 %s）拉起，不改选 Y: %+v", b448HomeX, b448ModelX, specs)
	}
	if midYSquad != 0 || midYCarrier != 0 {
		t.Fatalf("回合中 Y 不得被准入，得 squad=%d carrier=%d", midYSquad, midYCarrier)
	}
	bearing, ok, err := p.a.ledger.SeatBearingOf(card)
	if err != nil || !ok || bearing.Carrier != b448Carrier || bearing.Machine != b448MachineB {
		t.Fatalf("承载应记冻结载体 X: %+v ok=%v err=%v", bearing, ok, err)
	}
	if len(p.a.runner.launchSpecs()) != 0 {
		t.Fatal("受理机不得拉起")
	}
	p.assertZeroCounts(t)
}

// I-2 反例（F15/F22/F23）：途中 Y 空出，但冻结载体 X 离线 / 满员 → 执行机回准入拒绝（离线 admission_rejected →
// 入口 400；满员 no_slot → 入口 409「协调者并发已满」），不改用 Y 拉起；零席位、零回合。
func TestB448FrozenCarrierUnavailableRejectsWithoutReselect(t *testing.T) {
	for _, tc := range []struct {
		name     string
		block    func(t *testing.T, p *b448Pair) func()
		wantCode int
		wantText string
	}{
		{"X 离线", func(t *testing.T, p *b448Pair) func() {
			if _, err := p.b.sched.ApplyDetect(b448Carrier, scheduling.DetectEvidence{Reachable: false}, "测试断开"); err != nil {
				t.Errorf("X 置离线: %v", err)
			}
			return func() {}
		}, http.StatusBadRequest, b448Carrier},
		{"X 满员", func(t *testing.T, p *b448Pair) func() {
			b, err := p.b.sched.AdmitSeatCarrier(b448Squad, b448Carrier)
			if err != nil {
				t.Errorf("占满 X: %v", err)
				return func() {}
			}
			return func() { _ = p.b.sched.Release(b.Squad, b.Carrier) }
		}, http.StatusConflict, "协调者并发已满（小队 " + b448Squad + "）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newB448Pair(t, b448PairOpts{})
			held := b448TwoMemberSquad(t, p)
			proxy := newB448Proxy(t, p.b)
			p.a.setTargets(t, map[string]string{b448MachineB: proxy.addr()})
			unblock := func() {}
			proxy.before = func(string) {
				if err := p.b.sched.Release(held.Squad, held.Carrier); err != nil {
					t.Errorf("途中归还 Y: %v", err)
				}
				unblock = tc.block(t, p)
			}
			card := b448Card(t, p)
			code, body := ledgerPost(t, p.a.testAgentdEnv, "/api/cards/"+card+"/coordinator/launch", `{}`)
			unblock()
			if code != tc.wantCode || !strings.Contains(body, tc.wantText) {
				t.Fatalf("冻结载体 X 不可用应 %d 含 %q（不改用 Y），得 %d %s", tc.wantCode, tc.wantText, code, body)
			}
			if n := len(p.b.runner.launchSpecs()) + len(p.a.runner.launchSpecs()); n != 0 {
				t.Fatalf("准入拒绝不得拉起，得 %d 次: %+v", n, p.b.runner.launchSpecs())
			}
			b448AssertEmptySeat(t, p, card)
			if sq, ca := b448CountsY(t, p); sq != 0 || ca != 0 {
				t.Fatalf("Y 不得被准入，得 squad=%d carrier=%d", sq, ca)
			}
			p.assertZeroCounts(t)
		})
	}
}

// M-1（F35，驻场端点）：执行机 server 级 WriteTimeout 比驻场首轮短时，建会话入口的 resident 仍成功
// （收件端放宽了写截止，回执完整送达受理机）。
func TestB448ResidentReceiptOutlivesServerWriteTimeout(t *testing.T) {
	old := coordWakeTurnTimeout
	coordWakeTurnTimeout = 3 * time.Second
	t.Cleanup(func() { coordWakeTurnTimeout = old })
	p := newB448Pair(t, b448PairOpts{bWriteTimeout: 200 * time.Millisecond})
	p.b.runner.delay = 600 * time.Millisecond
	resp := b448CreateSession(t, p, b448Card(t, p))
	if resp.Resident == nil || resp.Resident.Error != "" || resp.Resident.CarrierMachine != b448MachineB {
		t.Fatalf("驻场首轮长于执行机 WriteTimeout 时回执仍应送达，得 %+v", resp.Resident)
	}
	p.assertZeroCounts(t)
}
