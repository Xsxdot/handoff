package claudecode

import (
	"encoding/json"
	"testing"
)

// thinking_delta 必须被识别为思维链而不是正文：这是 claude 侧隔离的根。
func TestSplitDeltaSeparatesThinkingFromText(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"我先想想"}}`)
	text, reasoning := splitDelta(thinking)
	if text != "" {
		t.Errorf("思维链绝不能作为正文返回，实得 %q", text)
	}
	if reasoning != "我先想想" {
		t.Errorf("思维链内容应被取出，实得 %q", reasoning)
	}

	normal := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)
	text, reasoning = splitDelta(normal)
	if text != "你好" {
		t.Errorf("正文应被取出，实得 %q", text)
	}
	if reasoning != "" {
		t.Errorf("正文不该被当成思维链，实得 %q", reasoning)
	}
}

// textDelta 是既有隔离判定的入口，语义必须原封不动：thinking 一律 false。
func TestTextDeltaBehaviourUnchanged(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"我先想想"}}`)
	if got, ok := textDelta(thinking); ok || got != "" {
		t.Fatalf("textDelta 对 thinking_delta 必须返回 (\"\", false)，实得 (%q, %v)", got, ok)
	}
	normal := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)
	if got, ok := textDelta(normal); !ok || got != "你好" {
		t.Fatalf("textDelta 对 text_delta 必须返回 (\"你好\", true)，实得 (%q, %v)", got, ok)
	}
}
