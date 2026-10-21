// ocerror.go —— opencode 错误对象的摘要（B453 Q14）。
//
// 职责：把 opencode 的错误对象摘成一行人话，供回合失败文本使用。三处来源同一形状：
// `run --format json` 的 error 事件行（本包 driveTurn）、serve 模式 SSE 的 session.error
// 事件 properties.error 与消息 info.error（executor/opencode 适配）。
//
// 形状：上游 MessageV2 错误联合 {name, data{message, statusCode, responseBody, …}}；
// R6a 真样本到后核对（待换：真样本入 testdata 后以回放测试锁形状）。
//
// 边界：只做文本摘要，不做分类（分类唯一判据在 quota.ClassifyTurnFailure）。
package hostapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// opencodeErrorBodyLimit 是响应体摘要的字符上限：供应商的额度错误码常只在响应体里
// （如 ENTITLEMENT_ERROR），要带上，但不能让整页 HTML 错误体淹没失败文本。
const opencodeErrorBodyLimit = 400

// opencodeErrorTextLimit 是整条摘要的字符上限。
const opencodeErrorTextLimit = 800

// OpencodeErrorText 摘要 opencode 错误对象：「<name>（HTTP <status>）: <message>；响应体: <body>」，
// 缺的部分省略。字符串形态原样（截断）；空或 null 返回 ""。
func OpencodeErrorText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return truncateRunes(strings.TrimSpace(s), opencodeErrorTextLimit)
	}
	var obj struct {
		Name string `json:"name"`
		Data struct {
			Message      string          `json:"message"`
			StatusCode   int             `json:"statusCode"`
			ResponseBody json.RawMessage `json:"responseBody"`
		} `json:"data"`
		Message string `json:"message"` // 非联合形态的兜底（如 {message: ...}）
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return truncateRunes(string(trimmed), opencodeErrorTextLimit)
	}
	var b strings.Builder
	b.WriteString(obj.Name)
	if obj.Data.StatusCode != 0 {
		fmt.Fprintf(&b, "（HTTP %d）", obj.Data.StatusCode)
	}
	msg := obj.Data.Message
	if msg == "" {
		msg = obj.Message
	}
	if msg != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(msg)
	}
	if body := responseBodyText(obj.Data.ResponseBody); body != "" {
		b.WriteString("；响应体: ")
		b.WriteString(truncateRunes(body, opencodeErrorBodyLimit))
	}
	if b.Len() == 0 {
		return truncateRunes(string(trimmed), opencodeErrorTextLimit)
	}
	return truncateRunes(b.String(), opencodeErrorTextLimit)
}

// responseBodyText 取响应体文本：上游多为字符串（JSON 文本再编码一层），也容忍直接嵌对象。
func responseBodyText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return string(raw)
}

// truncateRunes 按字符截断并标省略。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
