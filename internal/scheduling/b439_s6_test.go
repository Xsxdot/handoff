// b439_s6_test.go —— B439 S6：载体登记（PutCarrier）在配了自名的机器上拒收
// 本机旧别名（local/本机/hostname），点名改成自名；未配自名保持今天判法（S5）。
package scheduling_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func newS6SchedulingService(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "s6.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	return scheduling.New(facadeRegistry{f: facade}), facade
}

// newS6FrozenAliasFixture 装配 S6 派发准入夹具：先以无自名登记一个 machine 为
// 指定值的在线载体（模拟 B439 之前写下的存量记录），再配自名并注入
// config.IsLegacyLocalAlias，使冻结派发准入的旧别名拒收生效。
func newS6FrozenAliasFixture(t *testing.T, machine string) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	svc, facade := newS6SchedulingService(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "carrier-x", Machine: machine, CLI: "opencode", HomeDir: "/home/x",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
	})
	svc.SetSelfName("linux-01")
	svc.SetLegacyAlias(config.IsLegacyLocalAlias)
	return svc, facade
}

func TestB439S6CarrierRegistrationLegacyAliasGate(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	cases := []struct {
		name    string
		self    string
		machine string
		wantErr bool
	}{
		{"有自名 local 拒", "linux-01", "local", true},
		{"有自名 本机 拒", "linux-01", "本机", true},
		{"有自名 hostname 拒", "linux-01", host, true},
		{"有自名 自名收", "linux-01", "linux-01", false},
		{"无自名 local 收(S5)", "", "local", false},
		{"无自名 本机 收(S5)", "", "本机", false},
		{"无自名 hostname 收(S5)", "", host, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := newS6SchedulingService(t)
			if c.self != "" {
				svc.SetSelfName(c.self)
			}
			err := svc.PutCarrier(scheduling.Carrier{
				Name: "carrier-x", Machine: c.machine, CLI: "opencode",
				Credential: scheduling.CredentialStandalone,
			}, 0)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("machine=%q 不应被拒: %v", c.machine, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("machine=%q 应被拒", c.machine)
			}
			if !errors.Is(err, scheduling.ErrInvalid) {
				t.Fatalf("拒收应包 ErrInvalid（用户输入错=400），实得: %v", err)
			}
			if !strings.Contains(err.Error(), "linux-01") {
				t.Fatalf("拒收文案应点名改成自名 linux-01，实得: %v", err)
			}
		})
	}
}

// TestB439S6AdmitFrozenRejectsLegacyCarrierMachine 锁住 S6 的派发准入半边：有自名
// 的机器遇到**存量** carrier 记录的 machine 是旧别名（local/本机/hostname）时，
// 冻结准入必须在占用写入之前拒发并给可行动错误，不得经 samePhysicalMachine 的
// 别名语义静默当本机放行。无自名的机器保持今天行为（S5，见下一个用例）。
func TestB439S6AdmitFrozenRejectsLegacyCarrierMachine(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	for _, alias := range []string{"local", "本机", host} {
		t.Run(alias, func(t *testing.T) {
			svc, facade := newS6FrozenAliasFixture(t, alias)
			_, err := svc.AdmitFrozen(scheduling.Binding{
				Carrier: "carrier-x", Target: alias, Executor: "opencode", HomeDir: "/home/x",
			})
			if err == nil {
				t.Fatalf("存量 carrier machine=%q 应被拒", alias)
			}
			if !errors.Is(err, scheduling.ErrInvalid) {
				t.Fatalf("拒收应包 ErrInvalid（用户输入错=400），实得: %v", err)
			}
			if !strings.Contains(err.Error(), "载体 carrier-x 的 machine") {
				t.Fatalf("拒收文案应点名 carrier 的 machine，实得: %v", err)
			}
			for _, want := range []string{alias, "linux-01", "改成"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("拒收文案应含 %q，实得: %v", want, err)
				}
			}
			if got := runningCount(t, facade, "carrier/carrier-x"); got != 0 {
				t.Fatalf("拒发必须发生在占用写入之前，carrier/carrier-x=%d，want 0", got)
			}
		})
	}
}

// TestB439S6AdmitFrozenRejectsLegacyFrozenTarget 锁住 B398 冻结派发的另一半：存量
// carrier 的 machine 已是全队名，但冻结快照里的 Binding.Target 本身是旧别名时，
// 同样按 S6 拒发（否则它会与旧别名 machine 一起经别名语义被判等）。
func TestB439S6AdmitFrozenRejectsLegacyFrozenTarget(t *testing.T) {
	svc, _ := newS6FrozenAliasFixture(t, "linux-01")
	_, err := svc.AdmitFrozen(scheduling.Binding{
		Carrier: "carrier-x", Target: "local", Executor: "opencode", HomeDir: "/home/x",
	})
	if err == nil {
		t.Fatal("冻结 Binding.Target=local 应被拒")
	}
	if !errors.Is(err, scheduling.ErrInvalid) {
		t.Fatalf("拒收应包 ErrInvalid，实得: %v", err)
	}
	if !strings.Contains(err.Error(), "冻结绑定的 target") {
		t.Fatalf("拒收文案应点名冻结绑定 target，实得: %v", err)
	}
	if !strings.Contains(err.Error(), "linux-01") {
		t.Fatalf("拒收文案应点名自名 linux-01，实得: %v", err)
	}
}

// TestB439S6AdmitFrozenAllowsTeamName 锁住 S6 不误杀：冻结 Binding.Target 与 carrier
// machine 都是全队名（== 自名）时，准入照常成功（B398 冻结派发不受影响）。
func TestB439S6AdmitFrozenAllowsTeamName(t *testing.T) {
	svc, facade := newS6FrozenAliasFixture(t, "linux-01")
	binding, err := svc.AdmitFrozen(scheduling.Binding{
		Carrier: "carrier-x", Target: "linux-01", Executor: "opencode", HomeDir: "/home/x",
	})
	if err != nil {
		t.Fatalf("全队名冻结派发不应被拒: %v", err)
	}
	if binding.Carrier != "carrier-x" || runningCount(t, facade, "carrier/carrier-x") != 1 {
		t.Fatalf("全队名冻结派发应正常占用: binding=%+v carrier=%d", binding,
			runningCount(t, facade, "carrier/carrier-x"))
	}
}

// TestB439S6AdmitFrozenNoSelfNameKeepsToday 锁住 S5：无自名的机器即使注入了旧别名
// 判据，也不拒旧别名（今天行为逐点不变——本机别名仍当本机）。
func TestB439S6AdmitFrozenNoSelfNameKeepsToday(t *testing.T) {
	svc, _ := newS6SchedulingService(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "carrier-x", Machine: "local", CLI: "opencode", HomeDir: "/home/x",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
	})
	svc.SetLegacyAlias(config.IsLegacyLocalAlias)
	if _, err := svc.AdmitFrozen(scheduling.Binding{
		Carrier: "carrier-x", Target: "local", Executor: "opencode", HomeDir: "/home/x",
	}); err != nil {
		t.Fatalf("无自名机器应保持今天行为（本机别名仍当本机），实得: %v", err)
	}
}
