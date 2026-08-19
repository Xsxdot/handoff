package config_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func TestTargetIsRelayAndValidate(t *testing.T) {
	rt := config.Target{Relay: "wss://r.example", Credential: "c", Node: "devbox", Token: "tok"}
	if !rt.IsRelay() {
		t.Fatal("should be relay form")
	}
	if err := rt.Validate(); err != nil {
		t.Fatalf("valid relay target rejected: %v", err)
	}
	bad := config.Target{Relay: "wss://r", Node: "devbox", Token: "tok"}
	if err := bad.Validate(); err == nil {
		t.Fatal("relay target without credential must fail")
	}
	mixed := config.Target{Relay: "wss://r", Addr: "1.2.3.4:7777", Credential: "c", Node: "n", Token: "t"}
	if err := mixed.Validate(); err == nil {
		t.Fatal("relay+addr both set must fail")
	}
}

func TestRelayConfigValidate(t *testing.T) {
	if err := (&config.RelayConfig{URL: "wss://r", Credential: "c", Node: "n"}).Validate(); err != nil {
		t.Fatalf("valid relay config rejected: %v", err)
	}
	if err := (&config.RelayConfig{URL: "http://r", Credential: "c", Node: "n"}).Validate(); err == nil {
		t.Fatal("non-ws URL must fail")
	}
}
