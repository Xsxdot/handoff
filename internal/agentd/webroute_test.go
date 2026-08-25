// 路由注册测试：控制台 SPA 兜底挂在内层 mux 之后的行为契约。
//
// 背景：B108 复盘发现「把路由注册整行注释掉，internal/agentd 全包测试依然
// 全绿」——所有用例都在直接调 handler 函数，没有一条走完整路由栈。本文件
// 一律经 ts.URL 发请求，覆盖注册这一环。
//
// 边界：白盒测试（package agentd），复用 newHostTestEnv / mustSession /
// getWithCookie 等既有脚手架，不新造鉴权。
package agentd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
)

// consoleTestEnv 建一个带有效会话 cookie 的环境。
//
// 复用本包既有脚手架，不新造鉴权：newHostTestEnv 建 Server + httptest.Server，
// mustSession 直接往 store 里写一条会话并返回对应的 cookie 值（比走
// ticket 兑换流程短，且既有用例都这么做，见 auth_test.go:62）。
func consoleTestEnv(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-console", time.Now().Add(time.Hour), false)
	return ts, cookie
}

// 路由注册本身必须有测试覆盖。
//
// 这道门的由来：B108 复盘时发现「把路由注册整行注释掉，internal/agentd
// 全包测试依然全绿」——所有用例都在直接调 handler 函数，没有一条走完整
// 路由栈。本文件一律经 ts.URL 发请求，覆盖注册这一环。
func TestConsoleRouteRegistered(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / 状态码 = %d，want 200（路由没注册？）", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / 的 Content-Type = %q，want text/html", ct)
	}
}

// 深链接经完整路由栈也要回落，而不是被别的 handler 抢走。
func TestDeepLinkRouteFallsBack(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/tasks/00000000-0000-0000-0000-000000000000", cookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("深链接状态码 = %d，want 200", resp.StatusCode)
	}
}

// 承重：/api 未命中必须是 JSON，不能被 SPA 回落成 HTML。
// 否则前端把 HTML 喂给 JSON.parse，报错与真实原因完全无关。
func TestUnknownAPIPathStaysJSON(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/api/no-such-endpoint", cookie)
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if strings.Contains(strings.ToLower(body), "<!doctype html") ||
		strings.Contains(strings.ToLower(body), "<html") {
		t.Fatalf("/api 未命中被回落成 HTML，body = %q", body)
	}
	if resp.StatusCode == http.StatusOK {
		t.Errorf("/api 未命中状态码 = 200，want 4xx")
	}
}

// /console 仍必须是免鉴权入口，不能被 SPA 抢走。
func TestConsoleTicketRouteNotShadowed(t *testing.T) {
	ts, _ := consoleTestEnv(t)
	// 不带 cookie 直接打 /console（无 ticket）：应由 handleConsole 处理，
	// 表现为 4xx（ticket 缺失），而不是 SPA 的 200 HTML。
	resp, err := noRedirectClient(ts).Get(ts.URL + "/console")
	if err != nil {
		t.Fatalf("请求 /console 失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("/console 无 ticket 却返回 200，疑似被 SPA handler 抢走")
	}
}

// 承重：方法错配在完整路由栈里必须保持 405，不能被 SPA 吞成 200 HTML，
// 也不能被前缀分派压成 404。
//
// 这条与 webhandler_test.go 的 TestSPARejectsNonGet 不是同一件事：那个是
// SPA handler 自己拒非 GET；这条走 ts.URL 全栈，验证「只注册了 POST 的真实
// API 路由」被 GET 打时，ServeMux 的方法裁决一路传到响应。
func TestApiMethodMismatchStays405(t *testing.T) {
	ts, cookie := consoleTestEnv(t)
	resp := getWithCookie(t, ts, "/api/workspaces/reveal", cookie)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 打 POST-only 路由状态码 = %d，want 405", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(strings.ToLower(string(b)), "<html") {
		t.Errorf("405 响应体被回落成 HTML，body = %q", b)
	}
}

// TestCodegraphRoute locks the complete authenticated charter viewer mount.
// It intentionally enters through httptest.Server so mux registration, auth,
// prefix stripping, SPA fallback, and method handling are tested together.
func TestCodegraphRoute(t *testing.T) {
	ts, cookie := consoleTestEnv(t)

	read := func(resp *http.Response) []byte {
		t.Helper()
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("读取 %s: %v", resp.Request.URL.Path, err)
		}
		return body
	}

	rootResp := getWithCookie(t, ts, "/", cookie)
	if rootResp.StatusCode != http.StatusOK {
		t.Fatalf("GET / 状态码 = %d，want 200", rootResp.StatusCode)
	}
	rootBody := read(rootResp)

	charterResp := getWithCookie(t, ts, "/codegraph/app/", cookie)
	if charterResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /codegraph/app/ 状态码 = %d，want 200", charterResp.StatusCode)
	}
	charterBody := read(charterResp)
	if bytes.Equal(charterBody, rootBody) {
		t.Fatal("/codegraph/app/ 返回了 handoff 根 index，而不是 charter index")
	}
	if !bytes.Contains(charterBody, []byte("<html")) {
		t.Fatalf("charter index 不是 HTML，body 前缀 = %q", charterBody[:min(len(charterBody), 120)])
	}

	deepResp := getWithCookie(t, ts, "/codegraph/app/graph/domains/core", cookie)
	if deepResp.StatusCode != http.StatusOK {
		t.Fatalf("GET charter 深路径状态码 = %d，want 200", deepResp.StatusCode)
	}
	if deepBody := read(deepResp); !bytes.Equal(deepBody, charterBody) {
		t.Fatal("charter 深路径没有回落到同一份 charter index")
	}

	assetMatch := regexp.MustCompile(`(?:src|href)="(\./assets/[^"?#]+)"`).FindSubmatch(charterBody)
	if len(assetMatch) != 2 {
		t.Fatalf("charter index 未找到 assets 资源引用，body 前缀 = %q", charterBody[:min(len(charterBody), 240)])
	}
	assetPath := strings.TrimPrefix(string(assetMatch[1]), ".")
	assetResp := getWithCookie(t, ts, "/codegraph/app"+assetPath, cookie)
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("GET charter asset 状态码 = %d，want 200", assetResp.StatusCode)
	}
	if got := assetResp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("charter hashed asset Cache-Control = %q，want immutable", got)
	}
	_ = read(assetResp)

	postReq, err := http.NewRequest(http.MethodPost, ts.URL+"/codegraph/app/", nil)
	if err != nil {
		t.Fatalf("构造 POST: %v", err)
	}
	postReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	postResp, err := ts.Client().Do(postReq)
	if err != nil {
		t.Fatalf("POST /codegraph/app/: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /codegraph/app/ 状态码 = %d，want 405", postResp.StatusCode)
	}

	unauthResp, err := ts.Client().Get(ts.URL + "/codegraph/app/")
	if err != nil {
		t.Fatalf("未登录 GET /codegraph/app/: %v", err)
	}
	defer unauthResp.Body.Close()
	unauthBody, err := io.ReadAll(unauthResp.Body)
	if err != nil {
		t.Fatalf("读取未登录响应: %v", err)
	}
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录 GET /codegraph/app/ 状态码 = %d，want 401", unauthResp.StatusCode)
	}
	if strings.Contains(strings.ToLower(string(unauthBody)), "<html") {
		t.Fatalf("未登录响应被任一 SPA index 吞掉：%q", unauthBody)
	}
}
