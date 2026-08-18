// deny_test.go —— 拒绝理由正文渲染的行为断言。
//
// 职责：确认理由原文与防重复请求指导同时出现在共享正文中。
// 边界：不测试消息如何传输；socket 同帧传输与 manager 带外传输分别由各自包测试。
package turn

import (
	"strings"
	"testing"
)

// TestDenyGuidanceText 钉住两件事：理由原文必须出现，且必须带上「不要重复发起
// 同一请求」——少了后半句，模型被拒后最常见的下一步就是原地再试一次。
func TestDenyGuidanceText(t *testing.T) {
	got := DenyGuidanceText("改用 go build ./...")
	if !strings.Contains(got, "改用 go build ./...") {
		t.Fatalf("正文 = %q，必须含理由原文", got)
	}
	if !strings.Contains(got, "不要重复发起同一请求") {
		t.Fatalf("正文 = %q，必须含「不要重复发起同一请求」", got)
	}
}
