package agy

import (
	"testing"
)

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name string
		raw  *AgyUsageRaw
		want int // ContextTokens, 0 means nil
	}{
		{"nil", nil, 0},
		{"全零", &AgyUsageRaw{}, 0},
		{"仅 input", &AgyUsageRaw{InputTokens: 100}, 100},
		{"带 cache read", &AgyUsageRaw{InputTokens: 100, CacheReadTokens: 500}, 600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := ParseUsage(c.raw)
			if c.want == 0 {
				if u != nil {
					t.Fatalf("want nil, got %v", u)
				}
			} else {
				if u == nil || u.ContextTokens != c.want {
					t.Fatalf("want %d, got %v", c.want, u)
				}
			}
		})
	}
}
