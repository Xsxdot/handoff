// usage_internal_test.go —— pickModelUsageWindow 的同包纯函数单测。
//
// 为什么用同包测试：pickModelUsageWindow 的参数类型 modelUsage 未导出，
// 外部包无法构造 map 字面量。
package claudecode

import "testing"

// TestPickModelUsageWindowSingleKey 单键：直接取。
func TestPickModelUsageWindowSingleKey(t *testing.T) {
	mu := map[string]modelUsage{
		"k3-256k": {ContextWindow: 262144, CanonicalModel: "k3-256k"},
	}
	w, confident := pickModelUsageWindow(mu, "k3-256k")
	if !confident || w != 262144 {
		t.Fatalf("单键应直接取到 262144 且自信，得到 w=%d confident=%v", w, confident)
	}
}

// TestPickModelUsageWindowMultiKeyPrefersKnownModel 多键：按 runState 已知模型名取。
func TestPickModelUsageWindowMultiKeyPrefersKnownModel(t *testing.T) {
	mu := map[string]modelUsage{
		"k3-256k": {ContextWindow: 262144, CanonicalModel: "k3-256k"},
		"opus-4":  {ContextWindow: 200000, CanonicalModel: "opus-4"},
	}
	w, confident := pickModelUsageWindow(mu, "opus-4")
	if !confident || w != 200000 {
		t.Fatalf("应按已知模型名 opus-4 取 200000，得到 w=%d confident=%v", w, confident)
	}
}

// TestPickModelUsageWindowMultiKeyPrefersCanonical 多键：按条目自身 canonicalModel 匹配。
func TestPickModelUsageWindowMultiKeyPrefersCanonical(t *testing.T) {
	mu := map[string]modelUsage{
		"internal-alias-1": {ContextWindow: 262144, CanonicalModel: "k3-256k"},
		"internal-alias-2": {ContextWindow: 200000, CanonicalModel: "opus-4"},
	}
	w, confident := pickModelUsageWindow(mu, "k3-256k")
	if !confident || w != 262144 {
		t.Fatalf("应按 canonicalModel=k3-256k 取 262144，得到 w=%d confident=%v", w, confident)
	}
}

// TestPickModelUsageWindowMultiKeyNoMatch 多键且都匹配不上：取任意一个且**不自信**
// （调用方必须打 Warn，不得静默挑第一个）。
func TestPickModelUsageWindowMultiKeyNoMatch(t *testing.T) {
	mu := map[string]modelUsage{
		"model-a": {ContextWindow: 100000},
		"model-b": {ContextWindow: 200000},
	}
	w, confident := pickModelUsageWindow(mu, "unknown")
	if confident {
		t.Fatalf("多键匹配不上必须 confident=false，得到 true")
	}
	if w != 100000 && w != 200000 {
		t.Fatalf("应取到其中任意一个窗口，得到 %d", w)
	}
}

// TestPickModelUsageWindowEmptyMap 空 map：0 且自信（正常形态，不是歧义）。
func TestPickModelUsageWindowEmptyMap(t *testing.T) {
	w, confident := pickModelUsageWindow(nil, "")
	if w != 0 || !confident {
		t.Fatalf("空 map 应 0 且自信，得到 w=%d confident=%v", w, confident)
	}
}
