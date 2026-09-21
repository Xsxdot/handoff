// b393_timeout_invariant_test.go —— B393 MAJOR-2：回合上界与驱动租约的不变量。
//
// 职责：锁住「协调者无头回合上界必须严格短于驱动租约」——挂死回合必须在租约
// 到期前判失败，否则租约被读成「会话仍活着」，他机接管/看板活性出现双跑假象。
// 边界：只断言常量关系，不驱动真实回合（有界返回的行为回路见 b393_timeout_test.go）。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB393WakeTurnTimeoutWithinLease 锁 MAJOR-2 不变量：coordWakeTurnTimeout 必须
// 严格小于 ledger.DriverLeaseTTL（留出进程组强杀与错误返回的余量）。
//
// 红（当前 HEAD）：coordWakeTurnTimeout=10m > DriverLeaseTTL=5m，注释声称的
// 「须在租约到期前判失败」与取值矛盾。
// 绿：上界由 DriverLeaseTTL 减去安全余量导出，恒 < 租约。
// 变异：把上界改回 10m（或将关系放宽为 <= 且取 5m）→ 复红。
func TestB393WakeTurnTimeoutWithinLease(t *testing.T) {
	if coordWakeTurnTimeout <= 0 {
		t.Fatalf("coordWakeTurnTimeout 必须为正，实得 %v", coordWakeTurnTimeout)
	}
	if coordWakeTurnTimeout >= ledger.DriverLeaseTTL {
		t.Fatalf("回合上界 %v 必须严格短于驱动租约 %v（挂死回合须在租约到期前判失败）",
			coordWakeTurnTimeout, ledger.DriverLeaseTTL)
	}
}
