// 本文件锁死 relay 形态机器在 GET /api/machines 上的两条：不再因为没有 addr
// 而被当成不可达，且带上可展示的中继身份。
package agentd

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestProbeRelayTargetDoesNotReportNoHost：relay 机器探活失败时，
// 失败原因必须是 relay 拨号的真实原因，不能是 "no Host in request URL"。
//
// why 这条是回归本尊：relay target 没有 addr，旧代码用 client.New("") 造出
// baseURL="http:"，请求 URL 退化成 http:/api/status。界面上显示的「已断开」
// 其实是请求压根没发出去。
func TestProbeRelayTargetDoesNotReportNoHost(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{
			"linux-01": {
				Relay: "wss://127.0.0.1:1/relay", Credential: "cred",
				Node: "linux-01", Token: "0123456789abcdef0123456789abcdef",
			},
		},
	}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()

	m := s.probeRemote(context.Background(), "linux-01")
	if m.Reachable {
		t.Fatal("拨不通的 relay 不该判为可达")
	}
	if strings.Contains(m.Error, "no Host in request URL") {
		t.Fatalf("relay 机器不该再报 no Host：%s", m.Error)
	}
	if m.Relay != "linux-01" {
		t.Fatalf("relay 机器要带节点名，实得 %q", m.Relay)
	}
}
