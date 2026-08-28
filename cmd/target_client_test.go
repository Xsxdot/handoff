// targetClient 的本机/登记名归一测试：空 target 与指向本机 loopback 的登记名
// 必须共用本机 HTTP 端点；未登记名称仍保留原名错误。
package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// TestTargetClientEmptyAndConfiguredSelf 穿过真实 Status HTTP 请求验证 CLI
// 本机客户端路由，避免只断言 client.New 的内存地址。
func TestTargetClientEmptyAndConfiguredSelf(t *testing.T) {
	var statusHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		statusHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	var remoteStatusHits atomic.Int32
	remoteTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		remoteStatusHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(remoteTS.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	remoteAddr := strings.TrimPrefix(remoteTS.URL, "http://")
	content := fmt.Sprintf("listen: %q\ntoken: %q\ntargets:\n  local:\n    addr: %q\n    token: %q\n  devbox:\n    addr: %q\n    token: %q\n", addr, testToken, "http://"+addr, testToken, remoteAddr, testToken)
	cfgPath := writeTestConfig(t, content)
	resetFlags(t)
	configPath = cfgPath
	targetName = ""
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false

	for _, target := range []string{"", "local", "devbox"} {
		cl, done, err := targetClient(target)
		if err != nil {
			t.Fatalf("targetClient(%q): %v", target, err)
		}
		status, err := cl.Status(t.Context())
		done()
		if err != nil {
			t.Fatalf("targetClient(%q) Status: %v", target, err)
		}
		if status == nil {
			t.Fatalf("targetClient(%q) 返回空 Status", target)
		}
	}
	if got := statusHits.Load(); got != 2 {
		t.Fatalf("本机/本机别名 Status 命中 %d 次，期望 2", got)
	}
	if got := remoteStatusHits.Load(); got != 1 {
		t.Fatalf("远端 devbox Status 命中 %d 次，期望 1", got)
	}
	if _, done, err := targetClient("本机"); err == nil || !strings.Contains(err.Error(), "本机") {
		if done != nil {
			done()
		}
		t.Fatalf("未登记 本机 应保留原名错误，err=%v", err)
	} else if done != nil {
		done()
	}
}
