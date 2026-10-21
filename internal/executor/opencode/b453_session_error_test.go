// b453_session_error_test.go —— B453 Q14：serve 模式下供应商拒绝（如 403 ENTITLEMENT_ERROR）
// 表现为 session.error 事件 + 零文本 idle。今天 FailReason 只说「零文本产出」，撞额度分类
// 看不到任何错误原文；本测锁定会话错误消息进入 FailReason。
//
// 事件形状按上游 Session.Event.Error（properties{sessionID, error{name, data{message,
// statusCode, responseBody}}}）与消息 info.error 推出，非真样本；R6a 到后换回放。
package opencode

import (
	"strings"
	"testing"
)

func sessionErrorEvent(message string, status int) string {
	return sseLine(map[string]any{
		"type":      "session.error",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"error": map[string]any{
				"name": "APIError",
				"data": map[string]any{"message": message, "statusCode": status, "isRetryable": false},
			},
		},
	})
}

func assistantErrorMessageEvent(msgID, message string) string {
	return sseLine(map[string]any{
		"type":      "message.updated",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"info": map[string]any{
				"id": msgID, "role": "assistant", "sessionID": "sess-1",
				"error": map[string]any{"name": "APIError", "data": map[string]any{"message": message}},
			},
		},
	})
}

func TestZeroTextTurnFailReasonCarriesSessionError(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(sessionErrorEvent("Forbidden: ENTITLEMENT_ERROR model not in plan", 403))
	fs.push(statusIdleEvent())
	_, ch := startFakeRun(t, fs, "task-b453-se", t.TempDir(), t.TempDir())
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("零文本回合应失败: %+v", got)
	}
	for _, want := range []string{"零文本", "ENTITLEMENT_ERROR model not in plan", "403"} {
		if !strings.Contains(got.Result.FailReason, want) {
			t.Fatalf("FailReason 缺 %q：%q", want, got.Result.FailReason)
		}
	}
}

// assistant 消息 info.error 与 session.error 同一错误时只出现一次（去重），且消息级错误单独也能带出。
func TestZeroTextTurnFailReasonCarriesAssistantMessageError(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(assistantErrorMessageEvent("msg-a1", "insufficient_quota: You exceeded your current quota"))
	fs.push(assistantErrorMessageEvent("msg-a1", "insufficient_quota: You exceeded your current quota"))
	fs.push(statusIdleEvent())
	_, ch := startFakeRun(t, fs, "task-b453-me", t.TempDir(), t.TempDir())
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("零文本回合应失败: %+v", got)
	}
	if n := strings.Count(got.Result.FailReason, "insufficient_quota"); n != 1 {
		t.Fatalf("消息级错误应恰出现一次，实际 %d：%q", n, got.Result.FailReason)
	}
}

// 下一回合不继承上一回合的会话错误（回合边界清空）。
func TestSessionErrorDoesNotLeakIntoNextTurn(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(sessionErrorEvent("first turn failure", 500))
	fs.push(statusIdleEvent())
	_, ch := startFakeRun(t, fs, "task-b453-leak", t.TempDir(), t.TempDir())
	first := waitEventType(t, ch, "result")
	if !strings.Contains(first.Result.FailReason, "first turn failure") {
		t.Fatalf("首回合应带错误：%q", first.Result.FailReason)
	}
	fs.push(statusBusyEvent())
	fs.push(statusIdleEvent())
	second := waitEventType(t, ch, "result")
	if strings.Contains(second.Result.FailReason, "first turn failure") {
		t.Fatalf("会话错误漏进下一回合：%q", second.Result.FailReason)
	}
}
