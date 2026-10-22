// b453_session_error_test.go —— B453 Q14：serve 模式下供应商拒绝（如 403 ENTITLEMENT_ERROR）
// 表现为 session.error 事件 + 零文本 idle。今天 FailReason 只说「零文本产出」，撞额度分类
// 看不到任何错误原文；本测锁定会话错误消息进入 FailReason。
//
// 事件形状按上游 Session.Event.Error（properties{sessionID, error{name, data{message,
// statusCode, responseBody}}}）与消息 info.error 推出，非真样本；R6a 到后换回放。
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// R6a 真样本：`run --format json` 的 error 行里的错误对象与 serve 模式 session.error 的
// properties.error 是同一个上游对象（MessageV2 错误联合）。把真样本对象装进 session.error 回放，
// 锁定 FailReason 带出真实形状里的消息与状态码。
func TestZeroTextTurnFailReasonCarriesRealErrorObject(t *testing.T) {
	quietLog(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "hostapi", "testdata", "b453", "opencode_key_expired.real.stdout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var line struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &line); err != nil {
		t.Fatal(err)
	}
	fs := newFakeServer(t)
	fs.push(sseLine(map[string]any{
		"type": "session.error", "sessionID": "sess-1",
		"properties": map[string]any{"sessionID": "sess-1", "error": line.Error},
	}))
	fs.push(statusIdleEvent())
	_, ch := startFakeRun(t, fs, "task-b453-real", t.TempDir(), t.TempDir())
	got := waitEventType(t, ch, "result")
	for _, want := range []string{"This API key has expired", "HTTP 401", "api_key_expired"} {
		if !strings.Contains(got.Result.FailReason, want) {
			t.Fatalf("FailReason 缺 %q：%q", want, got.Result.FailReason)
		}
	}
}

// 零文本 + 会话错误：错误原文另走 Result.ErrorDetail（不带「零文本产出 / 可 continue」等通用处置
// 句），编排层判额度耗尽时据此写原因（真机 R6b/R7 瑕疵 1）。无会话错误时 ErrorDetail 为空。
func TestZeroTextTurnErrorDetailIsSessionErrorOnly(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(sessionErrorEvent("Forbidden: ENTITLEMENT_ERROR model not in plan", 403))
	fs.push(statusIdleEvent())
	_, ch := startFakeRun(t, fs, "task-b453-detail", t.TempDir(), t.TempDir())
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("零文本回合应失败: %+v", got)
	}
	d := got.Result.ErrorDetail
	if !strings.Contains(d, "ENTITLEMENT_ERROR model not in plan") || !strings.Contains(d, "403") {
		t.Fatalf("ErrorDetail 应是会话错误原文：%q", d)
	}
	for _, bad := range []string{"零文本", "continue", "供应商流中断"} {
		if strings.Contains(d, bad) {
			t.Fatalf("ErrorDetail 不应含通用处置句 %q：%q", bad, d)
		}
	}

	fs2 := newFakeServer(t)
	fs2.push(statusIdleEvent())
	_, ch2 := startFakeRun(t, fs2, "task-b453-nodetail", t.TempDir(), t.TempDir())
	if got := waitEventType(t, ch2, "result"); got.Result == nil || got.Result.ErrorDetail != "" {
		t.Fatalf("无会话错误时 ErrorDetail 应为空: %+v", got.Result)
	}
}
