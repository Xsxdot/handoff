// decision_test.go —— approval 裁决映射与文案截断的包内白盒测试（内部锁，不顶替缝级断言）。
package decision

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestGateDecisionAllowOnlyExact(t *testing.T) {
	if d, r := GateDecision("allow"); d != "once" || r != "" {
		t.Fatalf("精确 allow 应放行一次，实得 %q %q", d, r)
	}
	if d, _ := GateDecision(" allow "); d != "once" {
		t.Fatalf("首尾空白应被裁剪后放行，实得 %q", d)
	}
	for _, ans := range []string{"", "yes", "Allow", "allowed", "deny"} {
		if d, _ := GateDecision(ans); d != "reject" {
			t.Fatalf("非精确 allow 必须拒绝，%q 实得 %q", ans, d)
		}
	}
}

func TestGateDecisionDenyReasonPassthrough(t *testing.T) {
	if d, r := GateDecision("deny: 危险命令"); d != "reject" || r != "危险命令" {
		t.Fatalf("deny: 原因 应透传原因，实得 %q %q", d, r)
	}
	if d, r := GateDecision("deny:无空格"); d != "reject" || r != "无空格" {
		t.Fatalf("deny: 紧贴原因应透传，实得 %q %q", d, r)
	}
	if d, r := GateDecision("deny"); d != "reject" || r != "" {
		t.Fatalf("裸 deny 拒绝且无原因，实得 %q %q", d, r)
	}
}

func TestMapGateAnswerStatuses(t *testing.T) {
	if got := MapGateAnswer("allow"); got.Status != executor.ApprovalAllow {
		t.Fatalf("allow 应映射为 ApprovalAllow，实得 %v", got.Status)
	}
	got := MapGateAnswer("deny: 原因")
	if got.Status != executor.ApprovalDeny || got.Reason != "原因" {
		t.Fatalf("deny 应映射为 ApprovalDeny 且带原因，实得 %+v", got)
	}
	if got := MapGateAnswer("其它"); got.Status != executor.ApprovalDeny {
		t.Fatalf("其它 answer 应映射为 ApprovalDeny，实得 %v", got.Status)
	}
}

func TestPermEventTextTruncates(t *testing.T) {
	short := "短文案"
	if got := PermEventText(short); got != short {
		t.Fatalf("短文案不应改，实得 %q", got)
	}
	long := strings.Repeat("字", 250)
	got := PermEventText(long)
	if !strings.HasSuffix(got, executor.TruncationMarker) {
		t.Fatalf("超长文案应带截断标记，实得尾部 %q", got[len(got)-10:])
	}
	if r := []rune(got); len(r) != 200+len([]rune(executor.TruncationMarker)) {
		t.Fatalf("截断应保留 200 rune + 标记，实得 %d", len(r))
	}
}
