package targetclient

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func cfgFrom(t *testing.T, body string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("token: t\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

var relayTarget = config.Target{
	Relay: "wss://relay.example.com/relay", Credential: "cred",
	Node: "linux-01", Token: "0123456789abcdef0123456789abcdef",
}

// T-S4：直连形态 target 即使打洞开着、配了代理，也不构造 relay Dialer、不打任何 p2p 日志。
func TestDirectTargetIgnoresP2P(t *testing.T) {
	cfg := cfgFrom(t, "proxy: socks5://127.0.0.1:7897\n")
	log, buf := bufLogger()
	_, dialer, cleanup, err := newWithDialer("mac-02", config.Target{Addr: "10.0.0.2:7777", Token: "tok"}, CLIP2P(cfg), log)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if dialer != nil {
		t.Fatal("直连形态不应构造 relay Dialer")
	}
	if strings.Contains(buf.String(), "p2p") {
		t.Fatalf("直连形态出现 p2p 日志:\n%s", buf.String())
	}
}

// S8（CLI 路径）：relay 形态 + 代理 + 打洞开 → 构造时一条代理绕行 INFO；关打洞则无。
func TestCLIProxyNoticeOnRelayDialer(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{"proxy: socks5://127.0.0.1:7897\n", 1},
		{"proxy: socks5://127.0.0.1:7897\np2p:\n  enabled: false\n", 0},
		{"", 0},
	}
	for _, c := range cases {
		log, buf := bufLogger()
		_, cleanup, err := New("linux-01", relayTarget, CLIP2P(cfgFrom(t, c.body)), log)
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
		if got := strings.Count(buf.String(), config.P2PProxyNoticeMessage); got != c.want {
			t.Fatalf("%q: 提示 %d 条，want %d\n%s", c.body, got, c.want, buf.String())
		}
	}
}

// CLIP2P / P2PFromConfig：唯一翻译处取 config 的生效值。
func TestP2PFromConfig(t *testing.T) {
	o := P2PFromConfig(cfgFrom(t, "p2p:\n  stun: []\n"))
	if !o.Enabled || o.STUN == nil || len(o.STUN) != 0 {
		t.Fatalf("stun: [] 翻译错: %+v", o)
	}
	if o := P2PFromConfig(cfgFrom(t, "")); len(o.STUN) != len(config.DefaultP2PSTUN()) {
		t.Fatalf("缺省 STUN 未传入: %+v", o)
	}
	if p := CLIP2P(cfgFrom(t, "")); p.StartDelay != cliP2PStartDelay || p.ProxyNotice {
		t.Fatalf("CLIP2P = %+v", p)
	}
}
