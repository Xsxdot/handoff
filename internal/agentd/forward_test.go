package agentd

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestForwardProjectAddToNamedMachine 断言：带 ?machine= 的登记请求被原样搬到
// 那台机器，响应状态码与报文原样透传。
func TestForwardProjectAddToNamedMachine(t *testing.T) {
	remote := newTestAgentdEnv(t) // 远程那台：manager 未注入，登记必 503
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox",
		bytes.NewReader([]byte(`{"origin_url":"git@github.com:x/h.git"}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 远端答什么就透什么：状态码与中文报错原文一律不改写
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望原样透传远端的 503；体=%s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte("manager 未就绪")) {
		t.Errorf("远端报错原文必须原样透传，实得 %s", body)
	}
}

// TestForwardPreservesHandoffHeaders 断言：流式端点依赖的 X-Handoff-* 响应头
// 穿过 agentd 反代后仍可见；Content-Length 等本地重编码相关头不在此契约内。
func TestForwardPreservesHandoffHeaders(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Handoff-Frames-Size", "1725")
		w.Header().Set("X-Handoff-Render-Size", "2048")
		_, _ = w.Write([]byte("ok\n"))
	}))
	t.Cleanup(remote.Close)

	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Handoff-Frames-Size"); got != "1725" {
		t.Fatalf("X-Handoff-Frames-Size = %q，期望原样透传 1725", got)
	}
	if got := resp.Header.Get("X-Handoff-Render-Size"); got != "2048" {
		t.Fatalf("X-Handoff-Render-Size = %q，期望原样透传 2048", got)
	}
}

// TestForwardUnknownMachineRejected 断言：机器名不在 targets 里 → 400 且点名它。
func TestForwardUnknownMachineRejected(t *testing.T) {
	local := newTestAgentdEnv(t)
	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=ghost", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("ghost")) {
		t.Errorf("报文必须点名那个机器名，实得 %s", body)
	}
}

// TestForwardedRequestNeverForwardsAgain 是防环的核心断言：带转发头的请求
// 一律本机处理，哪怕它自己也带着 ?machine=。
func TestForwardedRequestNeverForwardsAgain(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req, _ := http.NewRequest(http.MethodPost,
		local.ts.URL+"/api/projects?machine=devbox", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	// devbox 是黑洞地址：真转发了就会是 502/超时；本机处理则是 503（manager 未注入）
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("带转发头的请求必须本机处理，实得状态码 %d", resp.StatusCode)
	}
}
