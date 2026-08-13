package codex

import (
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestEstimateTicksKnownModel 用 gpt-5.6-sol 的牌价验算术。
// 牌价：input $5.00 / cached $0.50 / output $30.00 每百万 token。
// 1M 输入 + 1M 缓存 + 1M 输出 = 5 + 0.5 + 30 = $35.50 = 355000000000 ticks
func TestEstimateTicksKnownModel(t *testing.T) {
	ticks, state := estimateTicks("gpt-5.6-sol", 1_000_000, 1_000_000, 1_000_000)
	if state != proto.CostEstimated {
		t.Fatalf("表里有的模型应为 estimated，实得 %q", state)
	}
	if ticks != 355_000_000_000 {
		t.Fatalf("期望 355000000000 ticks（$35.50），实得 %d", ticks)
	}
}

// TestEstimateTicksUnknownModel 验不在表里的模型是 unknown，不是用默认价猜。
func TestEstimateTicksUnknownModel(t *testing.T) {
	ticks, state := estimateTicks("gpt-5-codex", 1_000_000, 0, 1_000_000)
	if state != proto.CostUnknown {
		t.Fatalf("表里没有的模型应为 unknown，实得 %q", state)
	}
	if ticks != 0 {
		t.Fatalf("unknown 时 ticks 必须为 0（不是猜一个数），实得 %d", ticks)
	}
}

// TestEstimateTicksEmptyModel 验模型名还没拿到时也是 unknown。
func TestEstimateTicksEmptyModel(t *testing.T) {
	if _, state := estimateTicks("", 100, 0, 100); state != proto.CostUnknown {
		t.Fatalf("空模型名应为 unknown，实得 %q", state)
	}
}
