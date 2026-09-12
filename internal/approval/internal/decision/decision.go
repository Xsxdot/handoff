// decision.go —— approval 的裁决映射与文案截断纯逻辑（B233.17 从 approval 公开包迁入嵌套 internal）。
//
// 职责：
//   - 把 ticket answer 映射为放行/拒绝裁决（只有精确 "allow" 放行一次）
//   - 把裁决映射为 executor.ApprovalDecision
//   - 截断权限文案到上限并附截断标记
//
// 边界：
//   - 不 import approval、不碰 Store/Hub/Client；只做纯映射
//   - **无跨域消费者**：只被 approval 公开包的 Client 消费，故可进嵌套
package decision

import (
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
)

// MapGateAnswer 把 ticket answer 映射为 executor.ApprovalDecision。
func MapGateAnswer(ans string) executor.ApprovalDecision {
	d, reason := GateDecision(ans)
	if d == "once" {
		return executor.ApprovalDecision{Status: executor.ApprovalAllow}
	}
	return executor.ApprovalDecision{Status: executor.ApprovalDeny, Reason: reason}
}

// GateDecision preserves the old manager-to-executor translation: only the
// exact answer "allow" grants once; all other answers reject, with a deny
// reason copied only from the explicit deny form.
func GateDecision(answer string) (decision, reason string) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "allow" {
		return "once", ""
	}
	rest := strings.TrimPrefix(trimmed, "deny")
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	if rest == trimmed {
		return "reject", ""
	}
	return "reject", rest
}

// PermEventText 截断权限文案到 200 rune 并附 executor.TruncationMarker。
func PermEventText(s string) string {
	const limit = 200
	if len([]rune(s)) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + executor.TruncationMarker
}
