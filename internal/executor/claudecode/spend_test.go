package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestParseResultSpendPerTurn 用三轮实抓值验：token 取本轮、花费取进程内差分。
func TestParseResultSpendPerTurn(t *testing.T) {
	rounds := []struct {
		uuid      string
		in, rd, o int
		totalCost float64
		wantTicks int64
	}{
		{"u1", 2776, 29952, 26, 0.029506, 295060000},
		{"u2", 265, 32512, 19, 0.047562, 180560000},
		{"u3", 54, 32768, 18, 0.064666, 171040000},
	}
	prev := 0.0
	for _, r := range rounds {
		m := streamMsg{UUID: r.uuid, TotalCostUSD: r.totalCost}
		m.Usage = json.RawMessage(`{"input_tokens":` + itoa(r.in) +
			`,"cache_read_input_tokens":` + itoa(r.rd) +
			`,"cache_creation_input_tokens":0,"output_tokens":` + itoa(r.o) + `}`)

		e, next, ok := parseResultSpend(m, prev)
		if !ok {
			t.Fatalf("轮 %s 应解析成功", r.uuid)
		}
		if e.Key != r.uuid {
			t.Fatalf("幂等键应是 result.uuid，实得 %q", e.Key)
		}
		// claudecode 的 input_tokens **不含**缓存，所以输入就是它本身
		if e.InputTokens != r.in {
			t.Fatalf("轮 %s 输入应为 %d，实得 %d", r.uuid, r.in, e.InputTokens)
		}
		if e.CachedTokens != r.rd {
			t.Fatalf("轮 %s 缓存输入应为 %d，实得 %d", r.uuid, r.rd, e.CachedTokens)
		}
		if e.OutputTokens != r.o {
			t.Fatalf("轮 %s 输出应为 %d，实得 %d", r.uuid, r.o, e.OutputTokens)
		}
		if e.CostTicks != r.wantTicks {
			t.Fatalf("轮 %s 花费应为差分 %d ticks，实得 %d", r.uuid, r.wantTicks, e.CostTicks)
		}
		if e.CostState != proto.CostReported {
			t.Fatalf("claudecode 自报花费，应为 reported，实得 %q", e.CostState)
		}
		prev = next
	}
}

// TestParseResultSpendNegativeDelta 验基线陈旧时取当前值，不写负数。
func TestParseResultSpendNegativeDelta(t *testing.T) {
	m := streamMsg{UUID: "u1", TotalCostUSD: 0.01}
	m.Usage = json.RawMessage(`{"input_tokens":1,"output_tokens":1}`)
	e, _, ok := parseResultSpend(m, 0.5) // 上次比这次还大
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.CostTicks != 100000000 {
		t.Fatalf("负差分应退回当前值 100000000 ticks，实得 %d", e.CostTicks)
	}
}

// TestParseResultSpendNoUUID 验没有幂等键就不出账目。
func TestParseResultSpendNoUUID(t *testing.T) {
	m := streamMsg{TotalCostUSD: 0.01}
	m.Usage = json.RawMessage(`{"input_tokens":1}`)
	if _, _, ok := parseResultSpend(m, 0); ok {
		t.Fatal("没有 uuid 时不应产出账目——没有键就没有幂等")
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
