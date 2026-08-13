// codex 用量解析测试：输入是 08-13 从 codex app-server 实抓的真实报文
// （探针笔记 §1.1），不是手编的。
package codex_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

// TestParseTokenUsageTakesLastAndKeepsCacheAsSubset 覆盖 codex 的两条特殊规则：
// ①取 last 不取 total（total 是整个 thread 的累加，不是当前占用）；
// ②cachedInputTokens 是 inputTokens 的**子集**，绝不能再加一次。
func TestParseTokenUsageTakesLastAndKeepsCacheAsSubset(t *testing.T) {
	raw := []byte(`{"threadId":"019ffb3d","turnId":"019ffb3d","tokenUsage":{
      "total":{"totalTokens":99999,"inputTokens":99000,"cachedInputTokens":50000,
               "outputTokens":999,"reasoningOutputTokens":0},
      "last":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
              "outputTokens":5,"reasoningOutputTokens":0},
      "modelContextWindow":258400}}`)

	u, ok := codex.ParseTokenUsageForTest(raw)
	if !ok || u == nil {
		t.Fatalf("应解析成功，得到 ok=%v u=%v", ok, u)
	}
	if u.ContextTokens != 24668 {
		t.Fatalf("必须取 last.inputTokens 24668（不是 total 的 99000，也不是加了缓存的 34652），得到 %d", u.ContextTokens)
	}
	if u.ContextWindow == nil || *u.ContextWindow != 258400 {
		t.Fatalf("分母应为 258400，得到 %v", u.ContextWindow)
	}
}

// TestParseTokenUsageRejectsEmpty 覆盖宽容解析：坏报文/零值不产生 Usage，
// 绝不用 0 冒充「占用为零」。
func TestParseTokenUsageRejectsEmpty(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`not json`),
		[]byte(`{"tokenUsage":{"last":{"inputTokens":0},"modelContextWindow":258400}}`),
		[]byte(`{}`),
	} {
		if u, ok := codex.ParseTokenUsageForTest(raw); ok || u != nil {
			t.Fatalf("报文 %s 不该产生 Usage，得到 %+v", raw, u)
		}
	}
}
