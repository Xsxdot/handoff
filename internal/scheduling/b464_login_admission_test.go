// b464_login_admission_test.go —— 统一准入的「登录已失效」判据与检测 need_login 的失效证据
// （B464 段 2，plan V11、Q5）。
//
// 职责：五个准入入口（AdmitCarrier、SelectWhere、AdmitFrozen、AdmitSeatCarrier、admitInto 经
// LaunchAdmit/Admit）与 CheckUsable 各锁一条「所选登录已失效 → 跳过并点名『载体 X 的登录已
// 失效：<机器> <HOME>』」与一条「已过期放行」；errors.Is(ErrNoHealthy) 不变。检测得到需要
// 登录（且不是撞额度）→ 给载体所选登录记失效证据（登录读作已过期，不挡派发）。
// 边界：账号与登录经账号域 Reconcile 建（不起发现器）；准入只经 usability（结构断言在
// b453_admission_test.go）。
package scheduling_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// loginGone 让 putAccount 建出的那条登录在下一轮发现里扫不到 → 已失效。
func loginGone(t *testing.T, acc *accounts.Service, name, machine string) {
	t.Helper()
	if _, err := acc.Reconcile(accounts.Discovery{Machine: machine, At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: "~/.handoff/home/" + name, HomeAbs: "/home/" + name}}}); err != nil {
		t.Fatalf("发现标已失效: %v", err)
	}
	if l, err := acc.Login(loginOf(name, machine)); err != nil || l.State != accounts.LoginGone {
		t.Fatalf("夹具：登录应已失效：%+v %v", l, err)
	}
}

// loginExpired 给那条登录记一次失效证据 → 已过期。
func loginExpired(t *testing.T, acc *accounts.Service, name, machine string) {
	t.Helper()
	if _, err := acc.MarkLoginAuthFailed(loginOf(name, machine), time.Now(), "test", "unauthorized"); err != nil {
		t.Fatalf("记失效证据: %v", err)
	}
}

func goneText(carrier, machine, account string) string {
	return "载体 " + carrier + " 的登录已失效：" + machine + " ~/.handoff/home/" + account
}

func assertGone(t *testing.T, label string, err error, want string) {
	t.Helper()
	if !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s 应因登录已失效拒（ErrNoHealthy + 「%s」），得 %v", label, want, err)
	}
}

func TestAdmitSkipsGoneLoginAtEveryEntryAndPassesExpired(t *testing.T) {
	for _, state := range []string{"expired", "gone"} {
		t.Run(state, func(t *testing.T) {
			svc, acc, _ := newAccountFixture(t)
			putAccount(t, acc, "cx", "m1")
			boundCarrier(t, svc, "c1", "m1", "cx")
			if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
				Members: []scheduling.SquadMember{{Carrier: "c1"}}}, 0); err != nil {
				t.Fatal(err)
			}
			if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
				Members: []scheduling.SquadMember{{Carrier: "c1"}}}, 0); err != nil {
				t.Fatal(err)
			}
			if state == "gone" {
				loginGone(t, acc, "cx", "m1")
			} else {
				loginExpired(t, acc, "cx", "m1")
			}
			want := goneText("c1", "m1", "cx")
			frozen := scheduling.Binding{Carrier: "c1", Target: "m1", Executor: "codex", HomeDir: "~/.handoff/home/cx"}
			c, _ := svc.Carrier("c1")
			entries := []struct {
				name string
				run  func() error
			}{
				{"AdmitCarrier", func() error { _, err := svc.AdmitCarrier("c1"); return err }},
				{"SelectWhere", func() error {
					_, err := svc.SelectWhere(scheduling.IgnitionRequest{Squad: "exec", Actor: "test"}, nil)
					return err
				}},
				{"Admit", func() error {
					_, err := svc.Admit(scheduling.IgnitionRequest{Card: "B464", Squad: "exec", Actor: "test"})
					return err
				}},
				{"AdmitFrozen", func() error { _, err := svc.AdmitFrozen(frozen); return err }},
				{"AdmitSeatCarrier", func() error { _, err := svc.AdmitSeatCarrier("coord", "c1"); return err }},
				{"LaunchAdmit", func() error { _, err := svc.LaunchAdmit("coord"); return err }},
				{"CheckUsable", func() error { return svc.CheckUsable(c) }},
			}
			for _, e := range entries {
				err := e.run()
				if state == "expired" {
					if err != nil {
						t.Fatalf("%s：已过期应放行（spec：不暂停派发），得 %v", e.name, err)
					}
					_ = svc.Release("exec", "c1")
					_ = svc.Release("coord", "c1")
					_ = svc.Release("", "c1")
					continue
				}
				assertGone(t, e.name, err, want)
			}
		})
	}
}

func TestDetectNeedLoginMarksLoginEvidence(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	putAccount(t, acc, "cy", "m2") // 另一台机器：同机再跑一轮只含 cy 的发现会把 cx 标已失效
	boundCarrier(t, svc, "c1", "m1", "cx")
	boundCarrier(t, svc, "c2", "m2", "cy")
	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true, NeedLogin: true}, "需要登录"); err != nil {
		t.Fatal(err)
	}
	if l, _ := acc.Login(loginOf("cx", "m1")); l.State != accounts.LoginExpired || l.AuthFailedAt == nil {
		t.Fatalf("检测需要登录应给所选登录记失效证据：%+v", l)
	}
	// 撞额度说明 CLI 带着凭据到了上游：登录类字样只是旁证，不记证据
	if _, err := svc.ApplyDetect("c2", scheduling.DetectEvidence{Reachable: true, NeedLogin: true, Quota: true}, "quota"); err != nil {
		t.Fatal(err)
	}
	if l, _ := acc.Login(loginOf("cy", "m2")); l.State != accounts.LoginOK || l.AuthFailedAt != nil {
		t.Fatalf("撞额度不记登录失效证据：%+v", l)
	}
	// 不可达不是登录证据
	if _, err := svc.ApplyDetect("c2", scheduling.DetectEvidence{Reachable: false, NeedLogin: true}, "down"); err != nil {
		t.Fatal(err)
	}
	if l, _ := acc.Login(loginOf("cy", "m2")); l.AuthFailedAt != nil {
		t.Fatalf("不可达不记登录失效证据：%+v", l)
	}
}
