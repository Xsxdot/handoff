// b439_s6_test.go —— B439 S6：载体登记（PutCarrier）在配了自名的机器上拒收
// 本机旧别名（local/本机/hostname），点名改成自名；未配自名保持今天判法（S5）。
package scheduling_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func newS6SchedulingService(t *testing.T) *scheduling.Service {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "s6.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return scheduling.New(facadeRegistry{f: ledgerapi.New(st)})
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
			svc := newS6SchedulingService(t)
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
