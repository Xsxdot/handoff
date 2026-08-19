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

func TestEndpointsPreserveRelayTransportForUpgrade(t *testing.T) {
	cfg := writeTestConfig(t, `listen: "127.0.0.1:7777"
token: "local-token"
targets:
  devbox:
    relay: "wss://relay.example/relay"
    credential: "connect-credential"
    node: "devbox"
    token: "0123456789abcdef0123456789abcdef"
`)
	resetFlags(t)
	configPath = cfg
	eps, err := Endpoints("devbox")
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("got %d endpoints, want one", len(eps))
	}
	ep := eps[0]
	if ep.Addr != "http://relay" || ep.RelayURL != "wss://relay.example/relay" ||
		ep.Credential != "connect-credential" || ep.Node != "devbox" {
		t.Fatalf("relay endpoint = %+v", ep)
	}
}
