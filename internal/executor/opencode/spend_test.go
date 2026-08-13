// spend_test.go —— opencode 消息级账目解析的测试。
package opencode

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

const msgUpdated = `{"sessionID":"ses_1","info":{
  "id":"msg_1","role":"assistant","cost":0.0001408596,
  "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
            "cache":{"write":0,"read":46464}},
  "modelID":"deepseek-v4-flash"}}`

// TestParseMessageSpendAddsReasoning 验 opencode 的 reasoning 与 output **平行**，要加。
//
// 这与 codex/grok 相反（那两家的 reasoning 是 output 的子集）。
// 实抓等式：total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464。
func TestParseMessageSpendAddsReasoning(t *testing.T) {
	e, ok := parseMessageSpend(json.RawMessage(msgUpdated))
	if !ok {
		t.Fatal("应解析成功")
	}
	if e.Key != "msg_1" {
		t.Fatalf("幂等键应是 info.id，实得 %q", e.Key)
	}
	// opencode 的 input **不含**缓存，直接用
	if e.InputTokens != 131 {
		t.Fatalf("输入应为 131，实得 %d", e.InputTokens)
	}
	if e.CachedTokens != 46464 { // read 46464 + write 0
		t.Fatalf("缓存输入应为 46464，实得 %d", e.CachedTokens)
	}
	if e.OutputTokens != 476 { // 182 + 294，reasoning 是加项
		t.Fatalf("输出应为 output 182 + reasoning 294 = 476，实得 %d", e.OutputTokens)
	}
	if e.CostTicks != 1408596 { // 0.0001408596 × 1e10
		t.Fatalf("花费应为 1408596 ticks，实得 %d", e.CostTicks)
	}
	if e.CostState != proto.CostReported {
		t.Fatalf("opencode 自报花费，应为 reported，实得 %q", e.CostState)
	}
}

// TestParseMessageSpendSkipsUserAndEmpty 验 user 消息与全零新建消息不入账。
func TestParseMessageSpendSkipsUserAndEmpty(t *testing.T) {
	user := `{"info":{"id":"msg_u","role":"user"}}`
	if _, ok := parseMessageSpend(json.RawMessage(user)); ok {
		t.Fatal("user 消息不应产出账目")
	}
	empty := `{"info":{"id":"msg_e","role":"assistant","cost":0,
	  "tokens":{"total":0,"input":0,"output":0,"reasoning":0,
	            "cache":{"write":0,"read":0}}}}`
	if _, ok := parseMessageSpend(json.RawMessage(empty)); ok {
		t.Fatal("新建的全零消息不应产出账目——它随后会被同 id 的真实值覆盖")
	}
}

// TestParseMessageSpendOverwriteShape 验流式增长的两帧同键、后者更大。
//
// 账本按键覆盖，所以这里只需保证两帧的 Key 相同、值取各自帧的当前值。
func TestParseMessageSpendOverwriteShape(t *testing.T) {
	first := `{"info":{"id":"msg_1","role":"assistant","cost":0.00001,
	  "tokens":{"input":10,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}}`
	a, ok1 := parseMessageSpend(json.RawMessage(first))
	b, ok2 := parseMessageSpend(json.RawMessage(msgUpdated))
	if !ok1 || !ok2 {
		t.Fatal("两帧都应解析成功")
	}
	if a.Key != b.Key {
		t.Fatalf("同一条消息的两帧应同键，实得 %q vs %q", a.Key, b.Key)
	}
	if b.OutputTokens <= a.OutputTokens {
		t.Fatalf("后一帧应是增长后的值，实得 %d → %d", a.OutputTokens, b.OutputTokens)
	}
}
