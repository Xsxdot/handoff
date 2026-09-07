package scheduling_test

// B233.5 T1 接缝测试：载体直派只占物理位，小队准入不覆盖载体身份。

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func newEmptySvc(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "t1.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	return scheduling.New(facadeRegistry{f: facade}), facade
}

func TestPutCarrierRejectsSquadName(t *testing.T) {
	svc, _ := newEmptySvc(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "muse", Machine: "local", CLI: "opencode", Credential: scheduling.CredentialStandalone})
	if err := svc.PutSquad(scheduling.Squad{Name: "rd", Role: scheduling.RoleExecutor}, 0); err != nil {
		t.Fatalf("先登记小队: %v", err)
	}
	err := svc.PutCarrier(scheduling.Carrier{Name: "rd", Machine: "local", CLI: "grok", Credential: scheduling.CredentialStandalone}, 0)
	if !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("同名小队已在，PutCarrier 必须 ErrNameConflict，实得 %v", err)
	}
}

func TestPutSquadRejectsCarrierName(t *testing.T) {
	svc, _ := newEmptySvc(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "muse", Machine: "local", CLI: "opencode", Credential: scheduling.CredentialStandalone})
	err := svc.PutSquad(scheduling.Squad{Name: "muse", Role: scheduling.RoleExecutor}, 0)
	if !errors.Is(err, scheduling.ErrNameConflict) {
		t.Fatalf("同名载体已在，PutSquad 必须 ErrNameConflict，实得 %v", err)
	}
}

func TestAdmitCarrierOccupiesCarrierKeyOnly(t *testing.T) {
	svc, facade := newEmptySvc(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "muse", Machine: "linux-01", CLI: "opencode",
		HomeDir: "~/.handoff/home/muse", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 2, Status: scheduling.StatusOnline,
	})
	b, err := svc.AdmitCarrier("muse")
	if err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	if b.Squad != "" || b.Carrier != "muse" || b.Target != "linux-01" || b.Executor != "opencode" || b.HomeDir != "~/.handoff/home/muse" {
		t.Fatalf("绑定身份不对: %+v", b)
	}
	if got := runningCount(t, facade, "carrier/muse"); got != 1 {
		t.Fatalf("carrier/muse=%d，want 1", got)
	}
	if got := runningCount(t, facade, "squad//muse"); got != 0 {
		t.Fatalf("不得出现 squad//muse，实得 %d", got)
	}
	if err := svc.Release("", "muse"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := runningCount(t, facade, "carrier/muse"); got != 0 {
		t.Fatalf("释放后 carrier/muse=%d，want 0", got)
	}
	if err := svc.Release("", "muse"); err != nil {
		t.Fatalf("重复 Release: %v", err)
	}
	if got := runningCount(t, facade, "carrier/muse"); got < 0 {
		t.Fatalf("重复释放不得产生负数: %d", got)
	}
}

func TestAdmitNoLongerOverridesPhysical(t *testing.T) {
	svc, _ := newCASFixture(t)
	b, err := svc.Admit(scheduling.IgnitionRequest{Card: "B1", Squad: "s1", Target: "other", Executor: "grok", Model: "gpt-y", Actor: "test"})
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if b.Target == "other" || b.Executor == "grok" {
		t.Fatalf("生产 Admit 不得再覆盖物理: %+v", b)
	}
	want := map[string]string{"c1": "m1", "c2": "m2"}
	if target, ok := want[b.Carrier]; !ok || b.Target != target {
		t.Fatalf("Target=%q，want 载体 Machine，binding=%+v", b.Target, b)
	}
}

func TestAdmitCarrierOfflineIsNoHealthy(t *testing.T) {
	svc, facade := newEmptySvc(t)
	if err := svc.PutCarrier(scheduling.Carrier{Name: "muse", Machine: "local", CLI: "opencode", Credential: scheduling.CredentialStandalone}, 0); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AdmitCarrier("muse")
	if !errors.Is(err, scheduling.ErrNoHealthy) {
		t.Fatalf("pending 载体必须 ErrNoHealthy，实得 %v", err)
	}
	if got := runningCount(t, facade, "carrier/muse"); got != 0 {
		t.Fatalf("失败不得占位: %d", got)
	}
}

func TestAdmitCarrierFullIsNoSlot(t *testing.T) {
	svc, facade := newEmptySvc(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "muse", Machine: "local", CLI: "opencode", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1,
	})
	if _, err := svc.AdmitCarrier("muse"); err != nil {
		t.Fatalf("首次 AdmitCarrier: %v", err)
	}
	_, err := svc.AdmitCarrier("muse")
	if !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("满员必须 ErrNoSlot，实得 %v", err)
	}
	if got := runningCount(t, facade, "carrier/muse"); got != 1 {
		t.Fatalf("满员失败不得增加占用: %d", got)
	}
}
