// Host 白名单中间件测试：钉死 spec §12 断言 13/14/15。
//
// 边界：白盒测试（package agentd），因为要伪造 Host 头并直接读 Server 内部构造。
package agentd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
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
	ts := testhttp.NewServer(t, srv.Handler())
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

// stubLocalIPs 把网卡枚举换成固定返回，并在用例结束时还原。
//
// 返回：一个指向调用计数的指针，用于断言「域名不触发重新枚举」
func stubLocalIPs(t *testing.T, ips ...string) *int {
	t.Helper()
	orig := localIPsFn
	calls := 0
	localIPsFn = func() []string { calls++; return ips }
	t.Cleanup(func() { localIPsFn = orig })
	return &calls
}

// TestIsWildcardListenRecognizesShortForm 钉死 `0.0.0` 这种少一段的写法也算通配。
//
// 为什么要专门钉：mac-02 的生产配置里就是 `listen: 0.0.0:7777`，net 库把它当成
// 合法 host 解析。认不出它，B104 的修复对真实现场无效。
func TestIsWildcardListenRecognizesShortForm(t *testing.T) {
	for _, in := range []string{"", ":7777", "0.0.0.0:7777", "0.0.0:7777", "[::]:7777"} {
		if !isWildcardListen(in) {
			t.Fatalf("isWildcardListen(%q) = false，期望 true", in)
		}
	}
	for _, in := range []string{"192.168.1.9:7777", "example.com:7777"} {
		if isWildcardListen(in) {
			t.Fatalf("isWildcardListen(%q) = true，期望 false", in)
		}
	}
}

// TestHostGuardAllowsLocalNICUnderWildcardListen 钉死 B104 的主症状：
// 监听通配地址时，用本机网卡 IP 访问不该再吃 403。
func TestHostGuardAllowsLocalNICUnderWildcardListen(t *testing.T) {
	stubLocalIPs(t, "100.73.238.21")
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0:7777"})
	if resp := doWithHost(t, ts, "100.73.238.21:7777", "Bearer "+hostTestToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("本机网卡 IP 的状态码 = %d，期望 200", resp.StatusCode)
	}
}

// TestHostGuardStillRejectsDomainUnderWildcardListen 钉死「放宽没有放宽错东西」：
// 通配监听下，rebinding 用的域名仍然必须被挡——这正是本防线的对象。
func TestHostGuardStillRejectsDomainUnderWildcardListen(t *testing.T) {
	calls := stubLocalIPs(t, "100.73.238.21")
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0:7777"})
	atStart := *calls
	resp := doWithHost(t, ts, "evil.com", "Bearer "+hostTestToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("域名 Host 的状态码 = %d，期望 403", resp.StatusCode)
	}
	// 域名不该触发网卡重新枚举：否则攻击者用随机域名就能刷出无限次 syscall
	if *calls != atStart {
		t.Fatalf("域名 Host 触发了 %d 次额外的网卡枚举，期望 0", *calls-atStart)
	}
}

// TestHostGuardRescansNICWhenIPHostMisses 钉死运行期自愈：
// agentd 起来之后才拿到的地址（VPN 上线、Tailscale 刚接入）不该需要重启才生效。
func TestHostGuardRescansNICWhenIPHostMisses(t *testing.T) {
	// 间隔必须在**第一次**未命中之前就置零：那一次未命中会把 nextScan 推到
	// 未来，之后再改间隔也追不回来（这正是限流该有的行为）
	origGap := nicRefreshGap
	nicRefreshGap = 0
	t.Cleanup(func() { nicRefreshGap = origGap })

	stubLocalIPs(t) // 构造时机器上还没有这个地址
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0:7777"})
	if resp := doWithHost(t, ts, "10.1.2.3:7777", "Bearer "+hostTestToken); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("地址还不存在时状态码 = %d，期望 403", resp.StatusCode)
	}
	// 地址上线
	stubLocalIPs(t, "10.1.2.3")
	if resp := doWithHost(t, ts, "10.1.2.3:7777", "Bearer "+hostTestToken); resp.StatusCode != http.StatusOK {
		t.Fatalf("地址上线后状态码 = %d，期望 200（应重新枚举网卡）", resp.StatusCode)
	}
}

// TestHostGuardRejectionCarriesActionableHint 钉死 403 文案给的是「下一步做什么」。
//
// 为什么值得一条用例：跨机访问被挡是这条判据最常见的**正当**失败，而从控制台上
// 它只显示成「已断开」——文案是排查者唯一能拿到的线索。
func TestHostGuardRejectionCarriesActionableHint(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "127.0.0.1:7777"})
	resp := doWithHost(t, ts, "evil.com", "Bearer "+hostTestToken)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	if !strings.Contains(string(b), "web.allowed_hosts") {
		t.Fatalf("403 文案未给出可操作提示，body=%s", b)
	}
}

// TestAllowedHostsMergesNICOnlyUnderWildcardListen 钉死**构造时**就并入网卡 IP。
//
// 为什么单独钉这一条：摘掉构造时那段合并，端到端用例仍然全绿——因为未命中时的
// 运行期重扫会把它补回来。也就是说构造时合并对「能不能访问」是冗余的，它买到的
// 是启动日志里那行白名单（排查这类 403 的第一现场）与首个请求不必走重扫。
// 冗余不等于不该有，但**没有用例钉住的冗余会被下一个人当成死代码删掉**。
func TestAllowedHostsMergesNICOnlyUnderWildcardListen(t *testing.T) {
	stubLocalIPs(t, "100.73.238.21")
	srvWild, _, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0:7777"})
	if _, ok := srvWild.allowedHosts()["100.73.238.21"]; !ok {
		t.Fatal("通配监听时构造出的白名单应含本机网卡 IP")
	}
	srvFixed, _, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "127.0.0.1:7777"})
	if _, ok := srvFixed.allowedHosts()["100.73.238.21"]; ok {
		t.Fatal("绑定具体地址时不该并入其它网卡 IP——那超出了 B104 的授权范围")
	}
}

// TestLocalIPsSkipsIPv6LinkLocal 钉死「fe80:: 不进名单」。
//
// 为什么要钉：真机上一台 Mac 有 20 条 fe80 地址，它们作为 Host 永远命中不了
// （没有 zone），却会把启动日志那行白名单撑到读不了——而那行是排查跨机 403 的
// 第一现场。这条用例用真实网卡跑：本机必然有 fe80 地址，没有的话它自己会跳过。
func TestLocalIPsSkipsIPv6LinkLocal(t *testing.T) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("本机枚举网卡失败，跳过: %v", err)
	}
	hasLinkLocalV6 := false
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP != nil && n.IP.To4() == nil && n.IP.IsLinkLocalUnicast() {
			hasLinkLocalV6 = true
			break
		}
	}
	if !hasLinkLocalV6 {
		t.Skip("本机没有 IPv6 链路本地地址，这条用例无从验证")
	}
	for _, ip := range localIPs() {
		p := net.ParseIP(ip)
		if p != nil && p.To4() == nil && p.IsLinkLocalUnicast() {
			t.Fatalf("localIPs 返回了 IPv6 链路本地地址 %s，它作为 Host 永远命中不了", ip)
		}
	}
	// 同时确认没把 IPv4 一起误杀：本机至少该有一个非回环 IPv4
	v4 := 0
	for _, ip := range localIPs() {
		if p := net.ParseIP(ip); p != nil && p.To4() != nil {
			v4++
		}
	}
	if v4 == 0 {
		t.Fatal("localIPs 一个非回环 IPv4 都没返回，过滤过头了")
	}
}
