// ClassifyListen 的表驱动测试：三档归类与 loopback 变体推导（B85）。
package config

import "testing"

func TestClassifyListen(t *testing.T) {
	cases := []struct {
		name, in string
		cls      ListenClass
		lo       string
	}{
		{"loopback v4", "127.0.0.1:7777", ListenLoopback, "127.0.0.1:7777"},
		{"loopback v4 非 .1", "127.0.0.2:7777", ListenLoopback, "127.0.0.2:7777"},
		{"loopback v6", "[::1]:7777", ListenLoopback, "[::1]:7777"},
		{"localhost", "localhost:7777", ListenLoopback, "localhost:7777"},
		{"通配 v4", "0.0.0.0:7777", ListenWildcard, "127.0.0.1:7777"},
		{"通配 v6", "[::]:7777", ListenWildcard, "127.0.0.1:7777"},
		{"空 host", ":7777", ListenWildcard, "127.0.0.1:7777"},
		{"单网卡 v4", "100.64.0.5:9999", ListenSingle, "127.0.0.1:9999"},
		{"单网卡 v6", "[fd7a:115c::1]:7777", ListenSingle, "127.0.0.1:7777"},
		{"主机名", "myhost.local:7777", ListenSingle, "127.0.0.1:7777"},
		{"缺端口", "127.0.0.1", ListenLoopback, "127.0.0.1"},
		{"乱码", "!!!", ListenLoopback, "!!!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cls, lo := ClassifyListen(c.in)
			if cls != c.cls || lo != c.lo {
				t.Fatalf("ClassifyListen(%q) = (%v, %q), want (%v, %q)",
					c.in, cls, lo, c.cls, c.lo)
			}
		})
	}
}

func TestIsSelfTarget(t *testing.T) {
	cases := []struct {
		name, listen string
		target       Target
		want         bool
	}{
		{"scheme loopback against single listen", "100.64.0.5:7777", Target{Addr: "http://127.0.0.1:7777"}, true},
		{"exact address after scheme removal", "127.0.0.1:7777", Target{Addr: "https://127.0.0.1:7777"}, true},
		{"localhost same port", "127.0.0.1:7777", Target{Addr: "localhost:7777"}, true},
		{"wildcard listen loopback variant", "0.0.0.0:7777", Target{Addr: "http://127.0.0.1:7777"}, true},
		{"ipv6 loopback same port", "[::1]:7777", Target{Addr: "http://[::1]:7777"}, true},
		{"same listen host and port", "myhost.local:7777", Target{Addr: "http://myhost.local:7777"}, true},
		{"other direct host", "127.0.0.1:7777", Target{Addr: "10.0.0.9:7777"}, false},
		{"other port", "127.0.0.1:7777", Target{Addr: "127.0.0.1:8888"}, false},
		{"relay never self", "127.0.0.1:7777", Target{Relay: "wss://relay", Credential: "c", Node: "node", Token: "token"}, false},
		{"malformed direct addr", "127.0.0.1:7777", Target{Addr: "http://"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSelfTarget(tc.listen, tc.target); got != tc.want {
				t.Fatalf("IsSelfTarget(%q, %+v) = %v, want %v", tc.listen, tc.target, got, tc.want)
			}
		})
	}
}

func TestLocalDialAddrReusesListenClassification(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7777":        "http://127.0.0.1:7777",
		"0.0.0.0:7777":          "http://127.0.0.1:7777",
		"100.64.0.5:7777":       "http://127.0.0.1:7777",
		"http://127.0.0.1:7777": "http://127.0.0.1:7777",
	}
	for listen, want := range cases {
		if got := LocalDialAddr(listen); got != want {
			t.Fatalf("LocalDialAddr(%q) = %q, want %q", listen, got, want)
		}
	}
}
