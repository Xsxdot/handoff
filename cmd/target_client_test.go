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

// TestNamedTargetNoEndpointReportsClearly：无端点的 target 报清楚的错，
// 而不是造出一个注定失败的直连 client。
//
// why：这正是 relay 显示问题的镜像面——CLI 侧本来就不会走到这里，但重构后
// 两侧共用一个工厂，这条断言保证共用之后 CLI 的错误语义只会变好不会变差。
func TestNamedTargetNoEndpointReportsClearly(t *testing.T) {
	cfg := writeTestConfig(t, `listen: "127.0.0.1:7777"
token: "local-token"
targets:
  broken:
    token: "some-token"
`)
	resetFlags(t)
	configPath = cfg
	targetName = "broken"

	_, cleanup, err := newTargetClient()
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("无端点的 target 必须报错")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("错误要点名 target，实得 %v", err)
	}
}
