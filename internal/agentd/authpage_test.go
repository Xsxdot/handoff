package agentd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// getNoAuth 不带任何凭据发一个请求，用指定的 Accept。
func getNoAuth(t *testing.T, ts *httptest.Server, path, accept string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := noRedirectClient(ts).Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败：%v", path, err)
	}
	return resp
}

// 浏览器直接打开 agentd 根地址（没有 cookie）时，裸 401 会让人以为服务坏了。
// 给 HTML 请求一个说明页，写清怎么拿入口。
func TestUnauthenticatedHTMLGetsGuidancePage(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	resp := getNoAuth(t, ts, "/", "text/html,application/xhtml+xml")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，want 401（状态码不能因为返回 HTML 就变）", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q，want text/html", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "handoff console") {
		t.Errorf("说明页没写清怎么拿入口，body = %q", b)
	}
}

// 承重：非 HTML 请求（CLI、fetch）必须维持原有 JSON 401，
// 否则所有调用方的错误处理都会因为拿到 HTML 而失效。
func TestUnauthenticatedJSONStaysJSON(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	for _, accept := range []string{"application/json", "*/*", ""} {
		resp := getNoAuth(t, ts, "/api/tasks", accept)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Accept=%q 状态码 = %d，want 401", accept, resp.StatusCode)
		}
		if strings.Contains(strings.ToLower(string(b)), "<html") {
			t.Errorf("Accept=%q 拿到了 HTML，want JSON：%s", accept, b)
		}
	}
}

// token 未配置（fail-closed）分支同样要按 Accept 分流。
//
// 这一条单独立用例的理由：auth 中间件有**两个** 401 出口，上面两个用例都
// 只覆盖 sess == nil 那一个。只改一处的话，「token 未配置」时浏览器仍会
// 拿到裸 JSON——而那恰恰是最需要说明页的场景（运维刚装完还没配 token）。
func TestUnauthenticatedHTMLWhenTokenUnset(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: ""})
	resp := getNoAuth(t, ts, "/", "text/html")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("token 未配置分支的 Content-Type = %q，want text/html（另一个 401 出口漏改了？）", ct)
	}
}
