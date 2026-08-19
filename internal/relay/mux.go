package relay

import (
	"io"

	"github.com/hashicorp/yamux"
)

// relayYamuxConfig suppresses yamux's default stderr logger. Relay lifecycle
// failures are reported through the package slog logger with node/account
// context; yamux must not emit raw transport details on its own.
func relayYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.Logger = nil
	return cfg
}
