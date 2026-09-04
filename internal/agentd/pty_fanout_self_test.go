package agentd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestPtyFanoutSkipsSelfTarget：指向本机 loopback 的登记名不得出现在 scope=all 的 machines 里。
func TestPtyFanoutSkipsSelfTarget(t *testing.T) {
	cfg := &config.Config{
		Listen: "100.64.0.5:7777",
		Token:  "tok",
		Targets: map[string]config.Target{
			"local":    {Addr: "http://127.0.0.1:7777", Token: "tok"},
			"linux-01": {Addr: "http://10.0.0.9:7777", Token: "tok"},
		},
	}
	s := newPoolWiringServer(t, cfg)
	defer s.CloseTargets()

	out := s.ptySessionsAll(httptest.NewRequest(http.MethodGet, "/api/pty/sessions?scope=all", nil), nil)
	for _, m := range out.Machines {
		if m.Name == "local" {
			t.Fatalf("本机回环 target 不该出现在扇出 machines: %+v", out.Machines)
		}
	}
	foundRemote := false
	for _, m := range out.Machines {
		if m.Name == "linux-01" {
			foundRemote = true
		}
	}
	if !foundRemote {
		t.Fatalf("远端 linux-01 仍应在 machines 里，实得 %+v", out.Machines)
	}
}
