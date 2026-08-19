package cmd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

func TestCheckTokenEntropyRejectsWeak(t *testing.T) {
	if err := relay.CheckTokenEntropy("short"); err == nil {
		t.Fatal("weak token must be rejected")
	}
	if err := relay.CheckTokenEntropy(strings.Repeat("a", 32)); err != nil {
		t.Fatalf("128-bit hex should pass: %v", err)
	}
}
