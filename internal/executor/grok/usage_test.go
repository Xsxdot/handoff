// grok 用量解析测试：输入是 08-13 从 grok agent serve 实抓的真实报文
// （探针笔记 §4.1 / §4.2），不是手编的。
package grok_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
)

// TestParseResponseCompletedAddsCache 覆盖 grok 的缓存规则：snake_case 的
// input_tokens **不含**缓存命中，必须相加——与 codex 的规则相反。
func TestParseResponseCompletedAddsCache(t *testing.T) {
	raw := []byte(`{"sessionId":"019ffb4e","update":{
      "sessionUpdate":"response_completed",
      "usage":{"input_tokens":64,"output_tokens":34,
               "cache_read_input_tokens":34688,"cache_creation_input_tokens":0,
               "reasoning_tokens":19}}}`)

	u, ok := grok.ParseResponseCompletedForTest(raw)
	if !ok || u == nil {
		t.Fatalf("应解析成功，得到 ok=%v u=%v", ok, u)
	}
	if u.ContextTokens != 34752 {
		t.Fatalf("应为 64+34688+0=34752（缓存要相加），得到 %d", u.ContextTokens)
	}
	if u.ContextWindow != nil {
		t.Fatalf("这一帧不带分母，ContextWindow 必须是 nil")
	}
}

// TestParseResponseCompletedIgnoresTurnCompleted 是本计划最重要的一条回归：
// turn_completed 是**跨模型调用的累加**，拿它当分子会静默显示 4 倍的错值。
// 实测 modelCalls=4 的回合里它是 138637，而真实占用只有 34752。
func TestParseResponseCompletedIgnoresTurnCompleted(t *testing.T) {
	raw := []byte(`{"sessionId":"019ffb4e","update":{
      "sessionUpdate":"turn_completed","stop_reason":"end_turn",
      "usage":{"inputTokens":138637,"outputTokens":219,"totalTokens":138856,
               "cachedReadTokens":109568,"modelCalls":4}}}`)

	if u, ok := grok.ParseResponseCompletedForTest(raw); ok || u != nil {
		t.Fatalf("turn_completed 绝不能产生 Usage，得到 %+v", u)
	}
}

// TestParseModelsUpdateMatchesCurrentModel 覆盖分母：availableModels 是数组，
// 必须按 currentModelId 匹配，不能取第 0 个。
func TestParseModelsUpdateMatchesCurrentModel(t *testing.T) {
	raw := []byte(`{"currentModelId":"grok-4.6","availableModels":[
      {"modelId":"grok-3","name":"Grok 3","_meta":{"totalContextTokens":128000}},
      {"modelId":"grok-4.6","name":"Grok 4.6","_meta":{"totalContextTokens":500000}}]}`)

	model, window, ok := grok.ParseModelsUpdateForTest(raw)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "grok-4.6" {
		t.Fatalf("model = %q，期望 grok-4.6", model)
	}
	if window != 500000 {
		t.Fatalf("window = %d，期望 500000（不是第 0 个的 128000）", window)
	}
}
