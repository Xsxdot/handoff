package ledgernode

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
