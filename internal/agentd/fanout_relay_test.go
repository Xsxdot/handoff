// 本文件锁死项目树与 PTY 两条扇出对 relay 机器的行为：失败要给 relay 的真实
// 原因，不能是 "no Host in request URL"。
package agentd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func relayOnlyServer(t *testing.T) *Server {
	t.Helper()
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
	t.Cleanup(func() { s.CloseTargets() })
	return s
}

// TestProjectTreeFanoutRelayError：项目树扇出对 relay 机器不报 no Host。
func TestProjectTreeFanoutRelayError(t *testing.T) {
	s := relayOnlyServer(t)
	out := s.buildTreeAll(context.Background())
	if len(out.Machines) == 0 {
		t.Fatal("relay 机器要出现在扇出结果里")
	}
	for _, m := range out.Machines {
		if strings.Contains(m.Error, "no Host in request URL") {
			t.Fatalf("不该报 no Host：%s", m.Error)
		}
	}
}

// TestPtyFanoutRelayError：PTY 扇出对 relay 机器不报 no Host。
func TestPtyFanoutRelayError(t *testing.T) {
	s := relayOnlyServer(t)
	// ptySessionsAll 收的是 *http.Request（它要从请求里取本机会话的上下文），
	// 不是裸 ctx；local 传 nil 表示本机没有会话。
	out := s.ptySessionsAll(httptest.NewRequest(http.MethodGet, "/api/pty/sessions", nil), nil)
	if len(out.Machines) == 0 {
		t.Fatal("relay 机器要出现在扇出结果里")
	}
	for _, m := range out.Machines {
		if strings.Contains(m.Error, "no Host in request URL") {
			t.Fatalf("不该报 no Host：%s", m.Error)
		}
	}
}
