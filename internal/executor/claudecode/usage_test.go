// claudecode 用量解析测试：输入取自 out.jsonl 的真实 assistant 消息形状
// （探针笔记 §2）。注意 model 与 usage 在 message 对象内部，与 content 同级。
package claudecode_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/claudecode"
)

// TestParseAssistantUsageAddsCache 覆盖 claudecode 的缓存规则：三项相加。
func TestParseAssistantUsageAddsCache(t *testing.T) {
	// mapAssistant 收到的正是 message 对象本身
	msg := []byte(`{"model":"k3-256k","content":[{"type":"text","text":"hi"}],
      "usage":{"input_tokens":121801,"cache_creation_input_tokens":2000,
               "cache_read_input_tokens":5000,"output_tokens":42}}`)

	model, u, ok := claudecode.ParseAssistantUsageForTest(msg)
	if !ok {
		t.Fatalf("应解析成功")
	}
	if model != "k3-256k" {
		t.Fatalf("model = %q，期望 k3-256k", model)
	}
	if u.ContextTokens != 128801 {
		t.Fatalf("应为 121801+5000+2000=128801，得到 %d", u.ContextTokens)
	}
	if u.ContextWindow != nil {
		t.Fatalf("claudecode 不报窗口，ContextWindow 必须是 nil")
	}
}

// TestParseAssistantUsageSkipsZero 覆盖零值：没有有效数字时不产生 Usage，
// 但模型名仍然有效（模型名与用量是两件事）。
func TestParseAssistantUsageSkipsZero(t *testing.T) {
	msg := []byte(`{"model":"k3-256k","usage":{"input_tokens":0,
      "cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)
	model, u, ok := claudecode.ParseAssistantUsageForTest(msg)
	if !ok || model != "k3-256k" {
		t.Fatalf("模型名应仍然有效，得到 ok=%v model=%q", ok, model)
	}
	if u != nil {
		t.Fatalf("零值不该产生 Usage，得到 %+v", u)
	}
}
