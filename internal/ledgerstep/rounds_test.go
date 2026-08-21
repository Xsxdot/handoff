package ledgerstep

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func ev(typ string, payload map[string]any) ledger.Event {
	raw, _ := json.Marshal(payload)
	return ledger.Event{Type: typ, Payload: raw}
}

func TestCountRounds(t *testing.T) {
	evs := []ledger.Event{
		ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}),
		ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}),
	}
	if n := CountRounds(evs, "review"); n != 2 {
		t.Fatalf("回合数: %d", n)
	}
	evs = append(evs, ev(ledger.EvComment, map[string]any{"kind": "普通", "body": "人工 continue", "human_reset_node": "review"}))
	evs = append(evs, ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}))
	if n := CountRounds(evs, "review"); n != 1 {
		t.Fatalf("重置后回合数: %d", n)
	}
	if n := CountRounds(evs, "merge"); n != 0 {
		t.Fatalf("异节点: %d", n)
	}
}

// TestCountRoundsReadsLegacyPayloadKey 存量事件用的是 payload 键 "node"。
// 改名时若把这个键也改了，存量卡的回合计数会被清零、绕开 3 轮封顶。
// 这条是那道安全阀的回归网。
func TestCountRoundsReadsLegacyPayloadKey(t *testing.T) {
	evs := []ledger.Event{
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review","pass":false}`)},
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review","pass":false}`)},
	}
	if got := CountRounds(evs, "review"); got != 2 {
		t.Fatalf("应数到 2 轮，实得 %d——payload 键被改了？", got)
	}
}

// TestCountRoundsResetKeyUnchanged 清零键同理。
func TestCountRoundsResetKeyUnchanged(t *testing.T) {
	evs := []ledger.Event{
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review"}`)},
		{Type: ledger.EvComment, Payload: []byte(`{"human_reset_node":"review"}`)},
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review"}`)},
	}
	if got := CountRounds(evs, "review"); got != 1 {
		t.Fatalf("重置后应只剩 1 轮，实得 %d", got)
	}
}
