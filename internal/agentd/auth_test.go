// 浏览器鉴权测试：cookie 会话放行、失效原因区分、滑动续期节流。
//
// 边界：白盒测试（package agentd），因为要直接造会话行、读 Server 内部常量与字段。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// mustSession 直接在库里造一个会话，返回 cookie 明文。
func mustSession(t *testing.T, st *store.Store, id string, expiresAt time.Time, revoked bool) string {
	t.Helper()
	plain := "cookie-" + id
	now := time.Now()
	sess := &store.Session{
		ID: id, TokenHash: store.HashCredential(plain), DeviceName: "测试设备",
		CreatedAt: now, ExpiresAt: expiresAt, LastSeenAt: now,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if revoked {
		if err := st.RevokeSession(id, now); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
	}
	return plain
}

// getWithCookie 带 cookie 发一个 GET 请求。
func getWithCookie(t *testing.T, ts *httptest.Server, path, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestCookieSessionPassesAPI 钉死断言 8 的 /api 一半：cookie 能通过 API 路由。
func TestCookieSessionPassesAPI(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-ok", time.Now().Add(24*time.Hour), false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
}

// TestSessionFailureReasons 钉死断言 9/11 与 spec §11 的原因区分：
// 吊销、过期、不存在、无凭据必须落成四条不同原因的 Warn。
func TestSessionFailureReasons(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, st *store.Store) string
		reason string
	}{
		{"已吊销", func(t *testing.T, st *store.Store) string {
			return mustSession(t, st, "sess-revoked", time.Now().Add(24*time.Hour), true)
		}, "会话已吊销"},
		{"已过期", func(t *testing.T, st *store.Store) string {
			return mustSession(t, st, "sess-expired", time.Now().Add(-time.Minute), false)
		}, "会话过期"},
		{"不存在", func(t *testing.T, st *store.Store) string { return "不存在的 cookie" }, "会话不存在"},
		{"无凭据", func(t *testing.T, st *store.Store) string { return "" }, "无凭据"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, ts, logs := newHostTestEnv(t, &config.Config{Token: hostTestToken})
			cookie := c.setup(t, srv.st)
			resp := getWithCookie(t, ts, "/api/tasks", cookie)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
			}
			if !strings.Contains(logs.String(), c.reason) {
				t.Errorf("鉴权失败日志缺少原因 %q，实际日志: %s", c.reason, logs.String())
			}
		})
	}
}

// TestSlidingRenewal 钉死断言 12：剩余寿命不足一半时，一次请求把 expires_at 推后。
func TestSlidingRenewal(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	nearExpiry := time.Now().Add(sessionLifetime/2 - time.Hour)
	cookie := mustSession(t, srv.st, "sess-renew", nearExpiry, false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	got, err := srv.st.SessionByID("sess-renew")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !got.ExpiresAt.After(nearExpiry.Add(time.Hour)) {
		t.Fatalf("expires_at = %v，未被推后（原值 %v）", got.ExpiresAt, nearExpiry)
	}
}

// TestNoRenewalWhenFresh 钉死节流规则：寿命充足时一次请求**不写库**。
//
// 为什么要专门测「不写」：文件树、事件流、终端都是高频路由，每请求一次写会把
// SQLite 写成瓶颈——这条断言是那个性能约束的守门人。
func TestNoRenewalWhenFresh(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	fresh := time.Now().Add(sessionLifetime - time.Hour)
	cookie := mustSession(t, srv.st, "sess-fresh", fresh, false)
	before, err := srv.st.SessionByID("sess-fresh")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	after, err := srv.st.SessionByID("sess-fresh")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !after.ExpiresAt.Equal(before.ExpiresAt) || !after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Fatalf("寿命充足时不应写库：before=%+v after=%+v", before, after)
	}
}

// TestBearerStillWorks 钉死断言 1 的核心：主令牌路径不受影响，且身份为 CLI。
func TestBearerStillWorks(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
}

// TestEmptyConfigTokenStillRejectsCookie 钉死断言 2：cfg.Token 为空时
// **连合法 cookie 也拒**——fail-closed 的语义不能被新增的 cookie 路径旁路掉。
func TestEmptyConfigTokenStillRejectsCookie(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: ""})
	cookie := mustSession(t, srv.st, "sess-any", time.Now().Add(24*time.Hour), false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}

// issueTicket 用主令牌换一张 ticket URL。
func issueTicket(t *testing.T, ts *httptest.Server, device string) proto.AuthTicketResp {
	t.Helper()
	return issueTicketRaw(t, ts, `{"device_name":"`+device+`"}`)
}

// issueTicketRaw 同上，但直接给出请求体原文——用于构造含转义序列的设备名。
func issueTicketRaw(t *testing.T, ts *httptest.Server, rawBody string) proto.AuthTicketResp {
	t.Helper()
	body := strings.NewReader(rawBody)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/tickets", body)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("签发 ticket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("签发状态码 = %d，期望 200", resp.StatusCode)
	}
	var out proto.AuthTicketResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析签发响应: %v", err)
	}
	return out
}

// noRedirectClient 返回一个不自动跟随 302 的客户端（要断言 Set-Cookie 与 Location）。
func noRedirectClient(ts *httptest.Server) *http.Client {
	c := *ts.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// TestTicketToCookieHappyPath 钉死断言 3：有效 ticket 换得 cookie 并 302 到 /。
func TestTicketToCookieHappyPath(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "我的-mbp")
	resp, err := noRedirectClient(ts).Get(tk.URL)
	if err != nil {
		t.Fatalf("兑换 ticket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码 = %d，期望 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q，期望 /", loc)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			got = c
		}
	}
	if got == nil {
		t.Fatal("没有下发会话 cookie")
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteStrictMode || got.Path != "/" {
		t.Errorf("cookie 属性不对: %+v", got)
	}
	if got.Secure {
		t.Error("明文 loopback 下不得设置 Secure——会让 cookie 直接失效")
	}
	// 拿到的 cookie 必须真的能用（断言 8）
	if r2 := getWithCookie(t, ts, "/api/tasks", got.Value); r2.StatusCode != http.StatusOK {
		t.Fatalf("用新 cookie 访问 /api/tasks 得到 %d，期望 200", r2.StatusCode)
	}
}

// TestTicketSingleUseOverHTTP 钉死断言 4 的 HTTP 层：同一 URL 第二次访问失败。
func TestTicketSingleUseOverHTTP(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "mbp")
	cl := noRedirectClient(ts)
	if resp, _ := cl.Get(tk.URL); resp.StatusCode != http.StatusFound {
		t.Fatalf("首次兑换状态码 = %d，期望 302", resp.StatusCode)
	}
	resp, err := cl.Get(tk.URL)
	if err != nil {
		t.Fatalf("二次兑换: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("二次兑换状态码 = %d，期望 401", resp.StatusCode)
	}
}

// TestExpiredTicketRejected 钉死断言 6：过期 ticket 兑换失败。
//
// 直接在库里造一张已过期的 ticket，而不是等 60 秒。
func TestExpiredTicketRejected(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	const plain = "过期票"
	past := time.Now().Add(-time.Minute)
	if err := srv.st.CreateAuthTicket(store.HashCredential(plain), "mbp", past.Add(-time.Minute), past); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	resp, err := noRedirectClient(ts).Get(ts.URL + "/console?ticket=" + url.QueryEscape(plain))
	if err != nil {
		t.Fatalf("兑换: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}

// TestSessionRoutesListRevokeLogout 钉死断言 9 与 17 的服务端一半。
func TestSessionRoutesListRevokeLogout(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "手机")
	resp, _ := noRedirectClient(ts).Get(tk.URL)
	resp.Body.Close()
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}

	list := listSessions(t, ts)
	if len(list) != 1 || !strings.Contains(list[0].DeviceName, "手机") {
		t.Fatalf("会话列表不对: %+v", list)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/auth/sessions/"+list[0].ID, nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	dresp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("吊销: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("吊销状态码 = %d，期望 200", dresp.StatusCode)
	}
	// 断言 9：吊销后新请求立即 401
	if r := getWithCookie(t, ts, "/api/tasks", cookie); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销后状态码 = %d，期望 401", r.StatusCode)
	}
	// 列表里仍能看到它，且带 revoked_at
	after := listSessions(t, ts)
	if len(after) != 1 || after[0].RevokedAt == nil {
		t.Fatalf("吊销后的列表不对: %+v", after)
	}
}

// TestSessionCannotIssueTicket 钉死：会话身份不得签发新 ticket。
//
// 为什么：会话代表「一台已授权设备」，让它签发 ticket 等于让一台丢失的手机
// 无限制地再造设备，吊销就失去了意义。
func TestSessionCannotIssueTicket(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-x", time.Now().Add(24*time.Hour), false)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/tickets", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestDeviceNameSanitized 钉死 spec §6：设备名里的控制字符必须在入库前被剥掉，
// 否则一个构造过的 User-Agent 能往终端里注入 ANSI 转义序列。
func TestDeviceNameSanitized(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	// 用 JSON 的 \u001b 转义把 ESC 送进请求体，模拟一个构造过的 --device 或 User-Agent
	tk := issueTicketRaw(t, ts, `{"device_name":"\u001b[31m设备名"}`)
	resp, err := noRedirectClient(ts).Get(tk.URL)
	if err != nil {
		t.Fatalf("兑换: %v", err)
	}
	resp.Body.Close()
	list := listSessions(t, ts)
	if len(list) != 1 {
		t.Fatalf("会话条数 = %d，期望 1", len(list))
	}
	if strings.ContainsRune(list[0].DeviceName, '\x1b') {
		t.Fatalf("设备名残留 ESC 控制字符: %q", list[0].DeviceName)
	}
	if !strings.Contains(list[0].DeviceName, "设备名") {
		t.Errorf("净化把正常字符也吃掉了: %q", list[0].DeviceName)
	}
}

// listSessions 用主令牌列出会话。
func listSessions(t *testing.T, ts *httptest.Server) []proto.SessionInfo {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("列出会话: %v", err)
	}
	defer resp.Body.Close()
	var out []proto.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析会话列表: %v", err)
	}
	return out
}
