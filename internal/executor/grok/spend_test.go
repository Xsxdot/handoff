package grok

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

const promptMeta = `{"sessionId":"s1","promptId":"p1","modelId":"grok-4.6",
  "inputTokens":34502,"outputTokens":56,"totalTokens":34558,
  "cachedReadTokens":5888,"reasoningTokens":51,
  "usage":{"inputTokens":34502,"outputTokens":56,"totalTokens":34558,
    "cachedReadTokens":5888,"cacheCreationTokens":0,"reasoningTokens":51,
    "modelCalls":1,"costUsdTicks":605080000,"numTurns":1}}`

// TestParseTurnMetaSpendSubtractsCache 验 grok 的 inputTokens **含缓存**，要减。
func TestParseTurnMetaSpendSubtractsCache(t *testing.T) {
	e, ok := parseTurnMetaSpend(json.RawMessage(promptMeta))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "p1" {
		t.Fatalf("幂等键应是 promptId，实得 %q", e.Key)
	}
	// 34502 − 5888 − 0 = 28614
	if e.InputTokens != 28614 {
		t.Fatalf("输入应为 34502−5888=28614，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 5888 {
		t.Fatalf("缓存输入应为 5888，实得 %d", e.CachedTokens)
	}
	// reasoningTokens 51 是 outputTokens 56 的子集，不相加
	if e.OutputTokens != 56 {
		t.Fatalf("输出应为 56（reasoning 已含在内），实得 %d", e.OutputTokens)
	}
	if e.CostTicks != 605080000 {
		t.Fatalf("花费应直接取 costUsdTicks，实得 %d", e.CostTicks)
	}
	if e.CostState != proto.CostReported {
		t.Fatalf("有 costUsdTicks 时应为 reported，实得 %q", e.CostState)
	}
}

// TestParseTurnMetaSpendNoCostIsUnknown 验花费缺席记 unknown，绝不是 0 元。
//
// grok 只对 API-key 流量打花费戳，pool/OAuth 路径经常整块没有；
// cost_is_partial 为真时它也会主动把所有花费字段一并省略。两种都归 unknown。
func TestParseTurnMetaSpendNoCostIsUnknown(t *testing.T) {
	noCost := `{"promptId":"p2","usage":{"inputTokens":100,"outputTokens":10,
	  "cachedReadTokens":40,"cacheCreationTokens":0}}`
	e, ok := parseTurnMetaSpend(json.RawMessage(noCost))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.CostState != proto.CostUnknown {
		t.Fatalf("没有 costUsdTicks 时应为 unknown，实得 %q", e.CostState)
	}
	if e.CostTicks != 0 {
		t.Fatalf("unknown 时 ticks 必须为 0，实得 %d", e.CostTicks)
	}
	// token 照常入账——不知道花多少钱不代表不知道烧了多少 token
	if e.InputTokens != 60 || e.CachedTokens != 40 || e.OutputTokens != 10 {
		t.Fatalf("token 应照常入账，实得 %+v", e)
	}
}

// TestParseTurnMetaSpendNoPromptID 验没有幂等键就不出账目。
func TestParseTurnMetaSpendNoPromptID(t *testing.T) {
	if _, ok := parseTurnMetaSpend(json.RawMessage(`{"usage":{"inputTokens":5}}`)); ok {
		t.Fatal("没有 promptId 时不应产出账目")
	}
}
