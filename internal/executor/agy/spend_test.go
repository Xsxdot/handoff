package agy

import (
	"testing"
)

func TestParseSpend(t *testing.T) {
	cases := []struct {
		name    string
		raw     *AgyUsageRaw
		convID  string
		wantOk  bool
		wantIn  int
		wantOut int
		wantKey string
	}{
		{"nil raw", nil, "c1", false, 0, 0, ""},
		{"empty convID", &AgyUsageRaw{InputTokens: 10}, "", false, 0, 0, ""},
		{"all zero", &AgyUsageRaw{}, "c1", false, 0, 0, ""},
		{"normal", &AgyUsageRaw{InputTokens: 100, OutputTokens: 20, ThinkingTokens: 5, CacheReadTokens: 50}, "c1", true, 100, 25, "c1-spend"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ok := parseSpend(c.raw, c.convID)
			if ok != c.wantOk {
				t.Fatalf("ok=%v, want %v", ok, c.wantOk)
			}
			if ok {
				if s.InputTokens != c.wantIn || s.OutputTokens != c.wantOut || s.Key != c.wantKey {
					t.Fatalf("spend 不符合预期: %+v", s)
				}
			}
		})
	}
}
