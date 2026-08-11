// Host 白名单中间件测试：钉死 spec §12 断言 13/14/15。
//
// 边界：白盒测试（package agentd），因为要伪造 Host 头并直接读 Server 内部构造。
package agentd

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

const hostTestToken = "host-test-token"

// newHostTestEnv 构造一个带真实 store 的 Server 与 httptest 服务。
func newHostTestEnv(t *testing.T, cfg *config.Config) (*Server, *httptest.Server, *strings.Builder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var logs strings.Builder
	srv := NewServer(cfg, st, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, &logs
}

// doWithHost 发一个指定 Host 头的请求。
//
// 注意：必须用 req.Host 而不是 req.Header.Set("Host", ...)——net/http 的客户端
// 只认前者，后者会被静默忽略，测试会假通过。
func doWithHost(t *testing.T, ts *httptest.Server, host, auth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Host = host
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestHostGuardRejectsForeignHostBeforeAuth 钉死断言 13：
// 伪造 Host 得到 403，且**先于**鉴权发生——带一个错误的 token 也仍是 403 而非 401，
// 攻击者从状态码里读不出「凭据对不对」。
func TestHostGuardRejectsForeignHostBeforeAuth(t *testing.T) {
	_, ts, logs := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	resp := doWithHost(t, ts, "evil.com", "Bearer 错的令牌")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", resp.StatusCode)
	}
	// 正确的令牌同样是 403：证明白名单确实在鉴权之前
	resp = doWithHost(t, ts, "evil.com", "Bearer "+hostTestToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("带正确令牌时状态码 = %d，期望仍是 403", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "Host 不在白名单") {
		t.Error("缺少 Host 白名单拒绝的 Warn 日志——这是 rebinding 攻击的唯一信号")
	}
}

// TestHostGuardDNSRebindingRegression 钉死断言 14：
// Host 与 Origin 相等正是 coder/websocket 的 accept.go:239 会直接放过的组合，
// 必须在到达 websocket.Accept 之前就被白名单挡下。
func TestHostGuardDNSRebindingRegression(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws/events?task=任意", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Host = "evil.com"
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding 组合的状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestHostGuardAllowsLoopbackAndConfigured 钉死：回环三件套与配置扩展项放行，
// 端口不参与判定（httptest 的端口是随机的）。
func TestHostGuardAllowsLoopbackAndConfigured(t *testing.T) {
	cfg := &config.Config{
		Token:  hostTestToken,
		Listen: "192.168.1.10:7777",
		Web:    config.WebConfig{AllowedHosts: []string{"handoff.example.com"}},
	}
	_, ts, _ := newHostTestEnv(t, cfg)
	for _, host := range []string{
		"127.0.0.1:7777", "localhost:1234", "[::1]:7777",
		"192.168.1.10:7777", "handoff.example.com", "LOCALHOST:9",
	} {
		resp := doWithHost(t, ts, host, "Bearer "+hostTestToken)
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("Host %q 被 403，应放行", host)
		}
	}
}

// TestHostGuardWildcardListenNotAllowed 钉死：0.0.0.0 不进白名单——
// 它不是一个可用于访问的 Host，放进去没有意义。
func TestHostGuardWildcardListenNotAllowed(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0.0:7777"})
	if resp := doWithHost(t, ts, "0.0.0.0:7777", "Bearer "+hostTestToken); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("0.0.0.0 的状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestNonBrowserBearerClientStillConnects 钉死断言 15：
// 不带 Origin 头的非浏览器客户端（即 CLI）带 Bearer 仍能完成 WS 升级——白名单不得误伤 CLI。
func TestNonBrowserBearerClientStillConnects(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	taskID := mustWSTask(t, srv.st)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(ts)+"/ws/events?task="+taskID, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + hostTestToken}},
	})
	if err != nil {
		t.Fatalf("CLI 形态的 WS 连接被拒: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// mustWSTask 造一个 running 状态的任务，返回它的 id。
//
// handleEvents 会先查任务是否存在、不存在就以 1008 关闭，所以任何要保持连接
// 存活的 WS 测试都必须先有一个真任务。
func mustWSTask(t *testing.T, st *store.Store) string {
	t.Helper()
	const id = "11111111-2222-3333-4444-555555555555"
	now := time.Now()
	mustCreateTask(t, st, &proto.Task{
		ID: id, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	})
	return id
}

// wsURL 把 httptest 的 http:// 前缀换成 ws://。
func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}
