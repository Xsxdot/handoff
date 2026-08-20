// 本文件验证 targetclient.New 的 relay/直连选路、错误边界与清理契约。
//
// 边界：只断言构造结果，不发起真实 relay 网络请求；隧道行为由 relay 包测试覆盖。
package targetclient

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestNewDirect：直连 target → 直连 client，清理函数是 no-op。
func TestNewDirect(t *testing.T) {
	c, cleanup, err := New("mac-02", config.Target{Addr: "10.0.0.2:7777", Token: "tok"}, slog.Default())
	if err != nil {
		t.Fatalf("直连 target 不该失败: %v", err)
	}
	defer cleanup()
	if got := c.BaseURL(); got != "http://10.0.0.2:7777" {
		t.Fatalf("baseURL = %q，要 http://10.0.0.2:7777", got)
	}
}

// TestNewRelay：relay target → relay-backed client（baseURL 恒为 loopback 占位）。
//
// why 断言 baseURL 而不是断言「能连上」：连得上要真 relay 服务端，而这里要锁的是
// **选路走对了没有**——relay 分支的 baseURL 是 http://localhost（经隧道直达对端
// 的 hostGuard，loopback 名恒在白名单内），直连分支不可能产出这个值。
func TestNewRelay(t *testing.T) {
	tgt := config.Target{
		Relay:      "wss://relay.example.com/relay",
		Credential: "cred",
		Node:       "linux-01",
		Token:      "0123456789abcdef0123456789abcdef",
	}
	c, cleanup, err := New("linux-01", tgt, slog.Default())
	if err != nil {
		t.Fatalf("relay target 不该失败: %v", err)
	}
	defer cleanup()
	if got := c.BaseURL(); got != "http://localhost" {
		t.Fatalf("baseURL = %q，relay 形态要 http://localhost", got)
	}
}

// TestNewNoEndpoint：既无 addr 又无 relay → ErrNoEndpoint，且错误里点名是哪台。
//
// why 要点名：这个错误会原样显示在控制台的机器卡片上，不点名等于让人去猜。
func TestNewNoEndpoint(t *testing.T) {
	_, _, err := New("broken", config.Target{Token: "tok"}, slog.Default())
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("要 ErrNoEndpoint，实得 %v", err)
	}
	if !contains(err.Error(), "broken") {
		t.Fatalf("错误要点名 target，实得 %v", err)
	}
}

// TestNewRelayLowEntropyToken：relay 形态的弱 token 必须被前置拒绝。
//
// why：token 在 relay 形态下额外充当 E2E 的 PSK 源，弱 token 等于隧道没有端到端
// 保护。这道闸在 CLI 侧本来就有，收进工厂后不能丢。
func TestNewRelayLowEntropyToken(t *testing.T) {
	tgt := config.Target{
		Relay: "wss://relay.example.com/relay", Credential: "cred",
		Node: "linux-01", Token: "123",
	}
	if _, _, err := New("linux-01", tgt, slog.Default()); err == nil {
		t.Fatal("弱 token 必须被拒")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
