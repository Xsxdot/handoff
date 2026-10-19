package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func loadYAML(t *testing.T, body string) (*config.Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("token: t\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

var wantDefaultSTUN = []string{
	"stun:stun.miwifi.com:3478",
	"stun:stun.cloudflare.com:3478",
	"stun:stun.l.google.com:19302",
}

// C1/C4：省略 p2p 或 stun: null → 开启 + 缺省列表。
func TestP2PDefaults(t *testing.T) {
	for _, body := range []string{"", "p2p:\n  stun: null\n", "p2p: {}\n"} {
		cfg, err := loadYAML(t, body)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !cfg.P2PEnabled() {
			t.Fatalf("%q: 缺省应开启", body)
		}
		if got := cfg.P2PSTUN(); !reflect.DeepEqual(got, wantDefaultSTUN) {
			t.Fatalf("%q: 缺省 STUN = %v", body, got)
		}
	}
	if !reflect.DeepEqual(config.DefaultP2PSTUN(), wantDefaultSTUN) {
		t.Fatalf("DefaultP2PSTUN = %v", config.DefaultP2PSTUN())
	}
	// 返回副本：改它不污染缺省值。
	config.DefaultP2PSTUN()[0] = "x"
	if config.DefaultP2PSTUN()[0] != wantDefaultSTUN[0] {
		t.Fatal("DefaultP2PSTUN 未返回副本")
	}
	var nilCfg *config.Config
	if !nilCfg.P2PEnabled() {
		t.Fatal("nil Config 应按缺省开启")
	}
}

// C2：显式关闭。
func TestP2PDisabled(t *testing.T) {
	cfg, err := loadYAML(t, "p2p:\n  enabled: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.P2PEnabled() {
		t.Fatal("enabled:false 未生效")
	}
}

// C3：显式空列表 = 零 STUN，且 Save→Load 往返不被吞回缺省。
func TestP2PEmptySTUNSurvivesRoundTrip(t *testing.T) {
	cfg, err := loadYAML(t, "p2p:\n  stun: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.P2PSTUN(); got == nil || len(got) != 0 {
		t.Fatalf("stun: [] 应为空非 nil，got %#v", got)
	}
	p := filepath.Join(t.TempDir(), "rt.yaml")
	if err := config.Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.P2PSTUN(); got == nil || len(got) != 0 {
		raw, _ := os.ReadFile(p)
		t.Fatalf("往返后 stun 变成 %#v\n%s", got, raw)
	}
}

// C5：缺省配置 Save 不落 p2p 键（旧版 agentd 严格解析可读）。
func TestP2PDefaultNotPersisted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p) // 首次运行写盘
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "p2p") {
		t.Fatalf("缺省配置落了 p2p 键:\n%s", raw)
	}
}

// C6：stun 项形态校验，错误含下标。
func TestP2PSTUNValidation(t *testing.T) {
	for _, bad := range []string{`""`, `"turn:x:1"`, `"stuns:x:1"`, `"stun:x"`, `"stun:x:port"`, `"x:1"`, `"stun::1"`} {
		_, err := loadYAML(t, "p2p:\n  stun: [\"stun:ok.example:3478\", "+bad+"]\n")
		if err == nil || !strings.Contains(err.Error(), "p2p.stun[1]") {
			t.Fatalf("%s: 应拒且错误含 p2p.stun[1]，got %v", bad, err)
		}
	}
	if _, err := loadYAML(t, "p2p:\n  stun: [\"stun:[2001:db8::1]:3478\"]\n"); err != nil {
		t.Fatalf("IPv6 字面量应接受: %v", err)
	}
}

// C7：代理绕行提示真值表。
func TestP2PProxyNotice(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"", false},
		{"proxy: socks5://127.0.0.1:7897\n", true},
		{"proxy: socks5://127.0.0.1:7897\np2p:\n  enabled: false\n", false},
		{"p2p:\n  enabled: false\n", false},
	}
	for _, c := range cases {
		cfg, err := loadYAML(t, c.body)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.P2PProxyNotice(); got != c.want {
			t.Fatalf("%q: P2PProxyNotice=%v want %v", c.body, got, c.want)
		}
	}
	if !strings.Contains(config.P2PProxyNoticeMessage, "p2p.enabled: false") {
		t.Fatalf("提示文案: %q", config.P2PProxyNoticeMessage)
	}
}

// 严格解析：p2p 内未知键被拒。
func TestP2PUnknownKeyRejected(t *testing.T) {
	if _, err := loadYAML(t, "p2p:\n  turn: x\n"); err == nil {
		t.Fatal("p2p 未知键应被拒")
	}
}
