package cmd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func TestAgentdRelayTokenGate(t *testing.T) {
	weak := &config.Config{
		Token: "short",
		Relay: &config.RelayConfig{URL: "wss://relay.example", Credential: "cred", Node: "devbox"},
	}
	if err := validateRelayToken(weak); err == nil {
		t.Fatal("relay startup must reject a weak token")
	}

	strong := &config.Config{
		Token: strings.Repeat("a", 32),
		Relay: &config.RelayConfig{URL: "wss://relay.example", Credential: "cred", Node: "devbox"},
	}
	if err := validateRelayToken(strong); err != nil {
		t.Fatalf("relay startup rejected a 128-bit hex token: %v", err)
	}

	if err := validateRelayToken(&config.Config{}); err != nil {
		t.Fatalf("direct-only startup must not require a relay token: %v", err)
	}
}
