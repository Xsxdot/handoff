// preview proxy 测试：owner-loopback 反向代理。
//
// 职责：
//   - 覆盖 HTTP 代理转发 method/path/query/普通 header，并剥离入站 Authorization/Cookie
//   - 覆盖 WebSocket 升级后双向消息透传（HTTP/1.1 hijack 长连接）
//   - 覆盖上游不可用时写回 MACHINE_OFFLINE Problem（503、Retryable）
//
// 边界：
//   - 只用 httptest 起真实 loopback 上游与代理入口，不依赖 SQLite
//   - 日志内容不写入断言；绝不把 nonce 全文放入测试用例
package preview

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// proxyTestSession 构造固定字段、仅端口随上游变化的代理会话。
func proxyTestSession(port int) workspaceapi.PreviewSession {
	return workspaceapi.PreviewSession{
		PreviewSessionID: "proxy-test-session",
		WorkspaceID:      "proxy-test-ws",
		MachineID:        "proxy-test-machine",
		State:            workspaceapi.PreviewStateActive,
		URL:              "http://127.0.0.1:7777/v1/preview-proxy/redacted/",
		Port:             port,
		ExpiresAt:        time.Now().Add(15 * time.Minute),
	}
}

// TestProxyLoopbackProxiesHTTP 验证普通 HTTP 被代理，且入站凭证永不进入上游。
func TestProxyLoopbackProxiesHTTP(t *testing.T) {
	var authSeen, cookieSeen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		cookieSeen = r.Header.Get("Cookie")
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "method="+r.Method+" path="+r.URL.Path+" query="+r.URL.RawQuery+
			" echo="+r.Header.Get("X-Echo")+" body="+string(body))
	}))
	defer upstream.Close()

	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	session := proxyTestSession(port)

	req := httptest.NewRequest(http.MethodGet, "/echo/path?q=1", strings.NewReader("payload"))
	req.URL.Path = "/echo/path"
	req.URL.RawQuery = "q=1"
	req.Header.Set("Authorization", "Bearer sentinel")
	req.Header.Set("Cookie", "session=sentinel")
	req.Header.Set("X-Echo", "echo-value")

	rec := httptest.NewRecorder()
	proxyLoopback(rec, req, session, slog.Default())

	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", res.StatusCode, body)
	}
	for _, want := range []string{"/echo/path", "q=1", "echo-value", "payload"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("upstream echo body %q missing %q", body, want)
		}
	}
	if authSeen != "" {
		t.Errorf("upstream received Authorization %q, want stripped", authSeen)
	}
	if cookieSeen != "" {
		t.Errorf("upstream received Cookie %q, want stripped", cookieSeen)
	}
	if strings.Contains(string(body), "sentinel") {
		t.Errorf("upstream echo body %q leaked sentinel credential", body)
	}
}

// TestProxyLoopbackWebSocket 验证 WebSocket 升级与单条消息回显。
func TestProxyLoopbackWebSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, msg, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, msg)
	}))
	defer upstream.Close()

	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	session := proxyTestSession(port)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyLoopback(w, r, session, slog.Default())
	}))
	defer ts.Close()

	endpoint := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), endpoint, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(context.Background(), websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := conn.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText || string(data) != "ping" {
		t.Errorf("echo = (%v, %q), want (MessageText, \"ping\")", typ, data)
	}
}

// TestProxyLoopbackUpstreamDown 验证上游端口无人监听时写回 503 + MACHINE_OFFLINE。
func TestProxyLoopbackUpstreamDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	session := proxyTestSession(port)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	proxyLoopback(rec, req, session, slog.Default())

	res := rec.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "MACHINE_OFFLINE") {
		t.Errorf("body %q missing MACHINE_OFFLINE", body)
	}
	var p desktopapi.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Code != desktopapi.ProblemMachineOffline {
		t.Errorf("code = %q, want %q", p.Code, desktopapi.ProblemMachineOffline)
	}
	if !p.Retryable {
		t.Error("retryable = false, want true")
	}
	if p.MachineID != session.MachineID || p.WorkspaceID != session.WorkspaceID {
		t.Errorf("problem ids = (%q,%q), want (%q,%q)", p.MachineID, p.WorkspaceID,
			session.MachineID, session.WorkspaceID)
	}
}
