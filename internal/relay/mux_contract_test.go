package relay

import (
	"io"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

func TestRelayYamuxConfigKeepsLibraryKeepalive(t *testing.T) {
	got := relayYamuxConfig()
	def := yamux.DefaultConfig()
	if got.EnableKeepAlive != def.EnableKeepAlive {
		t.Fatalf("EnableKeepAlive = %v, want 库默认 %v", got.EnableKeepAlive, def.EnableKeepAlive)
	}
	if got.KeepAliveInterval != 30*time.Second || got.KeepAliveInterval != def.KeepAliveInterval {
		t.Fatalf("KeepAliveInterval = %s, want 库默认 %s", got.KeepAliveInterval, def.KeepAliveInterval)
	}
	if got.AcceptBacklog != def.AcceptBacklog {
		t.Fatalf("AcceptBacklog = %d, want 库默认 %d", got.AcceptBacklog, def.AcceptBacklog)
	}
	if got.ConnectionWriteTimeout != def.ConnectionWriteTimeout {
		t.Fatalf("ConnectionWriteTimeout = %s, want 库默认 %s", got.ConnectionWriteTimeout, def.ConnectionWriteTimeout)
	}
	if got.LogOutput != io.Discard {
		t.Fatal("本卡只允许改日志输出为 Discard，不得动保活参数")
	}
	if got.Logger != nil {
		t.Fatal("Logger 必须为 nil，与 LogOutput=Discard 互斥约束一致")
	}
}
