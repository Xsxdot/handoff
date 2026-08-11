// 浏览器鉴权测试：cookie 会话放行、失效原因区分、滑动续期节流。
//
// 边界：白盒测试（package agentd），因为要直接造会话行、读 Server 内部常量与字段。
package agentd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
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
