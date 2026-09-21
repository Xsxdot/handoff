// admit_seat_carrier_test.go —— B389 §3.4 冻结载体协调者准入的域内测试。
//
// 职责：经真实账本门面锁住 AdmitSeatCarrier 的两级 CAS 语义——载体由调用方
// 冻结指定、不做候选遍历；成员校验、角色校验与名额计数都真实落盘。
// 边界：不复制 acquire 的 CAS 重试细节，不覆写 coordinator 小队登记策略。
package scheduling_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// newSeatCarrierFixture 是 AdmitSeatCarrier 的协调者小队夹具：与 newFrozenFixture
// 同形（载体 A→B 各一格、独立物理身份），只把小队角色换成 coordinator——后者是
// executor，调 AdmitSeatCarrier 只会命中角色拒绝。T5 的路由矩阵复用它。
func newSeatCarrierFixture(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "seatcarrier.db"))
	if err != nil {
		t.Fatalf("打开冻结席位账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := scheduling.New(facadeRegistry{f: facade})
	for _, c := range []scheduling.Carrier{
		{Name: "A", Machine: "machine-A", CLI: "cli-A", HomeDir: "/home/A", Model: "model-A",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
		{Name: "B", Machine: "machine-B", CLI: "cli-B", HomeDir: "/home/B", Model: "model-B",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
	} {
		putOnlineCarrier(t, svc, c)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "CS", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "A", MaxConcurrency: 1}, {Carrier: "B", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记冻结席位小队: %v", err)
	}
	return svc, facade
}

// TestAdmitSeatCarrierAdmitsFrozenCoordinatorCarrier 锁 §3.4 的成功路径：
// 冻结指定 B 就落 B 的两键计数，不做候选遍历回落到 A。
func TestAdmitSeatCarrierAdmitsFrozenCoordinatorCarrier(t *testing.T) {
	svc, facade := newSeatCarrierFixture(t)
	binding, err := svc.AdmitSeatCarrier("CS", "B")
	if err != nil {
		t.Fatalf("冻结载体协调者准入失败: %v", err)
	}
	if binding.Carrier != "B" {
		t.Fatalf("冻结载体回落到 %q，want B", binding.Carrier)
	}
	if got := runningCount(t, facade, "squad/CS/B"); got != 1 {
		t.Fatalf("成员键 squad/CS/B=%d，want 1", got)
	}
	if got := runningCount(t, facade, "carrier/B"); got != 1 {
		t.Fatalf("载体键 carrier/B=%d，want 1", got)
	}
	if got := runningCount(t, facade, "squad/CS/A"); got != 0 {
		t.Fatalf("不得回落到首个成员，squad/CS/A=%d", got)
	}
}

// TestAdmitSeatCarrierRejectsNonMember 锁 §4-28：载体不在小队成员里即拒绝，
// 且两级计数都不落。
func TestAdmitSeatCarrierRejectsNonMember(t *testing.T) {
	svc, facade := newSeatCarrierFixture(t)
	if _, err := svc.AdmitSeatCarrier("CS", "B"); err != nil {
		t.Fatalf("冻结成员应准入成功: %v", err)
	}
	if _, err := svc.AdmitSeatCarrier("CS", "NOPE"); !errors.Is(err, scheduling.ErrRoleMismatch) {
		t.Fatalf("非成员载体应 ErrRoleMismatch，得 %v", err)
	}
	for _, key := range []string{"squad/CS/NOPE", "carrier/NOPE"} {
		if got := runningCount(t, facade, key); got != 0 {
			t.Fatalf("拒绝后计数 %s=%d，want 0", key, got)
		}
	}
}

// TestAdmitSeatCarrierRejectsExecutorSquad 锁 §3.4.3：执行者小队不能走协调者
// 冻结准入（AdmitFrozen 的角色防线在另一侧）。
func TestAdmitSeatCarrierRejectsExecutorSquad(t *testing.T) {
	svc, _ := newFrozenFixture(t)
	if _, err := svc.AdmitSeatCarrier("S", "A"); !errors.Is(err, scheduling.ErrRoleMismatch) {
		t.Fatalf("执行者小队应 ErrRoleMismatch，得 %v", err)
	}
}
