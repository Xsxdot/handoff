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
	"io"
	"net/http"
	"net/http/httptest"
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
