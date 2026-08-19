package client

import (
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

func TestNewRelayUsesPlaceholderAndDialerTransport(t *testing.T) {
	d := relay.NewDialer("ws://relay", "credential", "node", "token", "account", nil)
	c := NewRelay(d, "token")
	if c.baseURL != "http://relay" {
		t.Fatalf("baseURL = %q, want http://relay", c.baseURL)
	}
	if c.token != "token" {
		t.Fatalf("token = %q", c.token)
	}
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok || tr.DialContext == nil {
		t.Fatalf("relay client transport = %T, want configured *http.Transport", c.hc.Transport)
	}
}
