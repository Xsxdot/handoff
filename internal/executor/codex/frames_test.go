package codex

import "testing"

// 通知方法名到帧类型的归类：reasoning 的两种 delta 都算思维链，
// agentMessage 的 delta 算正文，命令输出的 delta 不产帧（它属于 tool_result）。
func TestDeltaFrameKind(t *testing.T) {
	cases := map[string]deltaKind{
		"item/agentMessage/delta":           deltaKindText,
		"item/reasoning/textDelta":          deltaKindReasoning,
		"item/reasoning/summaryTextDelta":   deltaKindReasoning,
		"item/commandExecution/outputDelta": deltaKindNone,
		"item/somethingNew/delta":           deltaKindNone,
	}
	for method, want := range cases {
		if got := deltaFrameKind(method); got != want {
			t.Errorf("%s 应归为 %v，实得 %v", method, want, got)
		}
	}
}

// 既有不变式：deltaNotifications 的成员资格不能变——它决定哪些通知
// 只喂 render.log 而不产 handoff 事件，改动会把事件库刷爆。
func TestDeltaNotificationsMembershipUnchanged(t *testing.T) {
	want := []string{
		"item/agentMessage/delta",
		"item/reasoning/textDelta",
		"item/reasoning/summaryTextDelta",
		"item/commandExecution/outputDelta",
	}
	if len(deltaNotifications) != len(want) {
		t.Fatalf("deltaNotifications 数量应为 %d，实得 %d", len(want), len(deltaNotifications))
	}
	for _, m := range want {
		if !deltaNotifications[m] {
			t.Errorf("%s 应在 deltaNotifications 里", m)
		}
	}
}
