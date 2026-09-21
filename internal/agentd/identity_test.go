// 控制台身份解析与读缝测试（B358.9 契约接缝 #1/#4）。
//
// 锁：console_user → user:<name> 的人名收口、缺名 fail-closed（Configured=false，
// 不回落到 web:<host>）、端戳取登录会话设备名、主令牌无端戳不拦门；
// GET /api/identity 三键恒出的 wire 形状。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// identityEnv 构造带指定 console_user 的身份读缝环境。
func identityEnv(t *testing.T, consoleUser string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{Token: hostTestToken, DataDir: t.TempDir(), ConsoleUser: consoleUser}
	srv, ts, _ := newHostTestEnv(t, cfg)
	return srv, ts
}

// getIdentity 请求 /api/identity 并解码。cookie 非空走 cookie 会话路径（不带
// Bearer，否则 Bearer 优先会退化成主令牌身份）；cookie 为空用主令牌。
func getIdentity(t *testing.T, ts *httptest.Server, cookie string) (int, proto.IdentityResp) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	} else {
		req.Header.Set("Authorization", "Bearer "+hostTestToken)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out proto.IdentityResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// TestConsoleIdentityResolvesConfiguredName 锁：配名后人名收口为 user:<name>，
// 端戳取登录会话设备名；主令牌（无会话）身份不返回端戳，但人名照给、Configured=true。
func TestConsoleIdentityResolvesConfiguredName(t *testing.T) {
	srv, ts := identityEnv(t, "sycm")
	cookie := mustSession(t, srv.st, "sess-id", time.Now().Add(time.Hour), false)

	code, got := getIdentity(t, ts, cookie)
	if code != 200 {
		t.Fatalf("GET /api/identity: %d", code)
	}
	if got.Member != "user:sycm" || !got.Configured {
		t.Fatalf("配名后应解析出 user:sycm 且 configured=true: %+v", got)
	}
	if got.Device != "测试设备" {
		t.Fatalf("端戳应为登录会话设备名「测试设备」: %+v", got)
	}

	// 主令牌：无登录会话，端戳空；人名与 configured 不变（端戳缺失不拦门）。
	// 注意 mustSession 的 cookie 走 cookie 分支；这里显式不带 cookie 走 Bearer，
	// 否则 getIdentity 会加 Bearer 头抢在 cookie 前面——两身份路径要分开验。
	code, got = getIdentity(t, ts, "")
	if code != 200 {
		t.Fatalf("主令牌 GET /api/identity: %d", code)
	}
	if got.Member != "user:sycm" || !got.Configured {
		t.Fatalf("主令牌应仍解析人名: %+v", got)
	}
	if got.Device != "" {
		t.Fatalf("主令牌无登录会话，端戳应空: %+v", got)
	}
}

// TestConsoleIdentityFailClosedWithoutName 锁：未配 console_user 时 fail-closed——
// configured=false、member 为空，且绝不回落到旧脸 web:<host>（决定 8）。
func TestConsoleIdentityFailClosedWithoutName(t *testing.T) {
	srv, ts := identityEnv(t, "")
	cookie := mustSession(t, srv.st, "sess-noname", time.Now().Add(time.Hour), false)

	code, got := getIdentity(t, ts, cookie)
	if code != 200 {
		t.Fatalf("GET /api/identity: %d", code)
	}
	if got.Configured || got.Member != "" {
		t.Fatalf("未配名应 fail-closed（configured=false, member 空）: %+v", got)
	}
	if got.Member == "web:127.0.0.1" || got.Member != "" {
		t.Fatalf("不得回落到旧脸 web:<host>: %+v", got)
	}
}

// TestConsoleIdentityRejectsIllegalName 锁：console_user 值非法（含冒号/空白）
// 等同未配——身份解析 fail-closed，不发半截脸。
func TestConsoleIdentityRejectsIllegalName(t *testing.T) {
	_, ts := identityEnv(t, "bad name")
	code, got := getIdentity(t, ts, "")
	if code != 200 {
		t.Fatalf("GET /api/identity: %d", code)
	}
	if got.Configured || got.Member != "" {
		t.Fatalf("非法 console_user 应 fail-closed: %+v", got)
	}
}
