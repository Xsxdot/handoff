package codex

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

const tokenUsageFrame = `{"threadId":"t1","turnId":"turn-1","tokenUsage":{
  "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
           "outputTokens":5,"reasoningOutputTokens":0},
  "last":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
          "outputTokens":5,"reasoningOutputTokens":0},
  "modelContextWindow":258400}}`

// TestParseTurnSpendSubtractsCache 验 codex 的输入要**减**缓存（与 claudecode 相反）。
func TestParseTurnSpendSubtractsCache(t *testing.T) {
	e, _, ok := parseTurnSpend(json.RawMessage(tokenUsageFrame), spendBase{Model: "gpt-5.6-sol"})
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "turn-1" {
		t.Fatalf("幂等键应是 turnId，实得 %q", e.Key)
	}
	// 24668 − 9984 = 14684：cachedInputTokens 是 inputTokens 的**子集**
	if e.InputTokens != 14684 {
		t.Fatalf("输入应为 24668−9984=14684，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 9984 {
		t.Fatalf("缓存输入应为 9984，实得 %d", e.CachedTokens)
	}
	// reasoningOutputTokens 是 outputTokens 的子集，不再相加
	if e.OutputTokens != 5 {
		t.Fatalf("输出应为 5（reasoning 已含在内），实得 %d", e.OutputTokens)
	}
	if e.CostState != proto.CostEstimated {
		t.Fatalf("codex 不自报花费，应为 estimated，实得 %q", e.CostState)
	}
}

// TestParseTurnSpendDeltaAcrossTurns 验回合级差分：第二个回合只记增量。
func TestParseTurnSpendDeltaAcrossTurns(t *testing.T) {
	base := spendBase{Model: "gpt-5.6-sol"}
	_, base, _ = parseTurnSpend(json.RawMessage(tokenUsageFrame), base)
	// 回合边界：调用方把 base 推进（模拟 turn/completed）
	base = base.commit()

	second := `{"turnId":"turn-2","tokenUsage":{"total":{"totalTokens":30000,
	  "inputTokens":29000,"cachedInputTokens":12000,"outputTokens":1000,
	  "reasoningOutputTokens":0}}}`
	e, _, ok := parseTurnSpend(json.RawMessage(second), base)
	if !ok {
		t.Fatal("应解析成功")
	}
	// 输入增量 = (29000−12000) − (24668−9984) = 17000 − 14684 = 2316
	if e.InputTokens != 2316 {
		t.Fatalf("第二回合输入增量应为 2316，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 2016 { // 12000 − 9984
		t.Fatalf("第二回合缓存增量应为 2016，实得 %d", e.CachedTokens)
	}
	if e.OutputTokens != 995 { // 1000 − 5
		t.Fatalf("第二回合输出增量应为 995，实得 %d", e.OutputTokens)
	}
}

// TestParseTurnSpendResetGoesPositive 验 total 归零（resume）时不产生负增量。
func TestParseTurnSpendResetGoesPositive(t *testing.T) {
	base := spendBase{Model: "gpt-5.6-sol", Input: 99999, Cached: 99999, Output: 99999}
	e, _, ok := parseTurnSpend(json.RawMessage(tokenUsageFrame), base)
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.InputTokens < 0 || e.CachedTokens < 0 || e.OutputTokens < 0 {
		t.Fatalf("基线大于当前值时不得产生负增量，实得 %+v", e)
	}
	if e.InputTokens != 14684 {
		t.Fatalf("归零后应按当前值全量入账 14684，实得 %d", e.InputTokens)
	}
}

// TestParseTurnSpendNoTurnID 验没有幂等键就不出账目。
func TestParseTurnSpendNoTurnID(t *testing.T) {
	if _, _, ok := parseTurnSpend(json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":5}}}`),
		spendBase{Model: "gpt-5"}); ok {
		t.Fatal("没有 turnId 时不应产出账目")
	}
}
