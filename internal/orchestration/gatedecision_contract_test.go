package orchestration

import "testing"

func TestGateDecisionGoldenVectors(t *testing.T) {
	tests := []struct {
		answer   string
		decision string
		reason   string
	}{
		{answer: "allow", decision: "once", reason: ""},
		{answer: "  allow  ", decision: "once", reason: ""},
		{answer: "deny", decision: "reject", reason: ""},
		{answer: "deny: 太危险", decision: "reject", reason: "太危险"},
		{answer: "deny:too-wide", decision: "reject", reason: "too-wide"},
		{answer: "yes", decision: "reject", reason: ""},
		{answer: "", decision: "reject", reason: ""},
	}
	for _, tt := range tests {
		decision, reason := gateDecision(tt.answer)
		if decision != tt.decision || reason != tt.reason {
			t.Fatalf("gateDecision(%q) = (%q, %q), want (%q, %q)",
				tt.answer, decision, reason, tt.decision, tt.reason)
		}
	}
}
