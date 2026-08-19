// ws_proxy_test.go —— 钉住「协调者↔agentd 的 WS 拨号永不走代理」（B161 回归）。
//
// 为什么是白盒 + 结构性判据，而不是「设 HTTP_PROXY 看代理有没有被打到」：
// net/http 对 ProxyFromEnvironment 的环境解析有 sync.Once 缓存，t.Setenv 很
// 可能不生效，那种测试会因为「代理压根没被解析」而变绿——为错误的理由通过，
// 比没有测试更糟。这里直接断言「拨号选项带的是本 Client 自己的、Proxy 为空的
// http.Client」，判据与缺陷一一对应且不依赖任何环境状态。
package client

import (
	"net/http"
	"testing"
)

// TestWSDialOptionsUsesOwnNoProxyClient 是 B161 的回归判据。
//
// 缺陷原样：streamOnce 传的是 &websocket.DialOptions{}（不带 HTTPClient），
// coder/websocket 于是退回 http.DefaultClient，其 DefaultTransport 的 Proxy
// 是 ProxyFromEnvironment——WS 拨号被送进 HTTP_PROXY，被代理回 503，长连接
// 永远建不起来**且不报错**（只剩每 6s 一次的 HTTP 对账兜底）。
func TestWSDialOptionsUsesOwnNoProxyClient(t *testing.T) {
	c := New("http://127.0.0.1:7777", "tok")
	opts := c.wsDialOptions()

	if opts.HTTPClient == nil {
		t.Fatal("WS 拨号没有交出 HTTPClient：会退回 http.DefaultClient，从而走 ProxyFromEnvironment")
	}
	if opts.HTTPClient != c.hc {
		t.Fatalf("WS 拨号用的不是本 Client 的 http.Client：HTTP 与 WS 两条路的代理纪律必须同源")
	}
	// coder/websocket 硬要求 HTTPClient.Timeout 为零，否则拨号直接报错
	if opts.HTTPClient.Timeout != 0 {
		t.Fatalf("HTTPClient.Timeout = %v，必须为 0（拨号时限走 context/dialTimeout）", opts.HTTPClient.Timeout)
	}

	tr, ok := opts.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T，想要 *http.Transport", opts.HTTPClient.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("Transport.Proxy 非空：协调者↔agentd 那条链路永不走代理（见 internal/proxycfg 包头）")
	}
}

// TestWSDialOptionsCarriesAuthAndForwardMark 钉住抽函数时不许丢头。
//
// extraHeaders 原先是在 streamOnce 里单独补的一段（streamOnce 不走 do）；
// 抽进 wsDialOptions 时若漏掉，MarkForwarded 的镜像连接从拨号起就没有防环标记，
// 症状是 A→B→A 成环，而单元测试与本机自测都看不出来。
func TestWSDialOptionsCarriesAuthAndForwardMark(t *testing.T) {
	c := New("http://127.0.0.1:7777", "tok").MarkForwarded()
	opts := c.wsDialOptions()

	if got := opts.HTTPHeader.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q，想要 Bearer tok", got)
	}
	if got := opts.HTTPHeader.Get("X-Handoff-Forwarded"); got != "1" {
		t.Fatalf("X-Handoff-Forwarded = %q，想要 1（缺了会让 agentd 之间的转发成环）", got)
	}
}

// TestWSDialOptionsWithoutTokenHasNoAuthHeader 钉住空 token 不发空 Bearer。
func TestWSDialOptionsWithoutTokenHasNoAuthHeader(t *testing.T) {
	opts := New("http://127.0.0.1:7777", "").wsDialOptions()
	if opts.HTTPHeader.Get("Authorization") != "" {
		t.Fatalf("空 token 时不该带 Authorization 头")
	}
}
