// B369.2（S2）连接核兑换、切机清罐与离线补配的机内夹具与测试。
//
// 边界：fake agentd 复刻 agentd 的两条路由语义——POST /api/auth/tickets 由
// token 签发一次性 ticket；GET /console?ticket= 原子消费后 Set-Cookie + 302。
// /api/status 在无 handoff_session cookie 时回 401（复刻回环门禁：无 cookie
// 过不了闸）。所有上游经真 relay 线格式（startFakeRelay）到达。
package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// pairToken 是 fake relay 夹具用的合法高熵 token（64 hex，过 CheckTokenEntropy）。
const pairToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// fakeAgentd 是行为等价的对端 agentd（只覆盖本卡所需路由）。
//
// 兑换链形状：POST /api/auth/tickets 由 Authorization 里的 token 签发 ticket，
// ticket 文本编码 device_name（= handoff-mobile-<机器名>），故不同机器换出的
// cookie 值不同——切机清罐断言据此区分机器。GET /console 回 Set-Cookie + 302。
type fakeAgentd struct {
	mu          sync.Mutex
	tickets     int
	afterLogin  int
	consoleAuth []string // /console 请求上的 Authorization（应为空）
	consoleCode int      // 0 表示 302
	omitCookie  bool
	extraCookie string
}

func (f *fakeAgentd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/status":
		if _, err := r.Cookie(sessionCookieName); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "no session")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	case "/api/auth/tickets":
		var body struct {
			DeviceName string `json:"device_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.tickets++
		n := f.tickets
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proto.AuthTicketResp{
			URL:       "http://" + r.Host + "/console?ticket=" + url.QueryEscape(body.DeviceName) + "-" + strconv.Itoa(n),
			ExpiresAt: time.Now().Add(time.Minute),
		})
	case "/console":
		f.mu.Lock()
		f.consoleAuth = append(f.consoleAuth, r.Header.Get("Authorization"))
		code, omit, extra := f.consoleCode, f.omitCookie, f.extraCookie
		f.mu.Unlock()
		if code == 0 {
			code = http.StatusFound
		}
		if extra != "" {
			w.Header().Add("Set-Cookie", extra)
		}
		if !omit {
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    "sess-" + r.URL.Query().Get("ticket"),
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		w.Header().Set("Location", "/after-login")
		w.WriteHeader(code)
	case "/after-login":
		f.mu.Lock()
		f.afterLogin++
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

// relayBundle 造一份单机 relay 形态 bundle（token 走 fake relay 的 E2E）。
func relayBundle(relayURL, name string) proto.PairBundle {
	return proto.PairBundle{
		Version: proto.PairVersion,
		Relay:   &proto.PairRelay{URL: relayURL, Credential: "pipecred"},
		Machines: []proto.PairMachine{
			{Name: name, Token: pairToken, Node: "devbox"},
		},
	}
}

// localClient 是不走任何代理的 HTTP 客户端（回环源与 fake 上游都用它）。
func localClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil}}
}

// TestExchangeTicketVerticalSlice 是最薄路径竖切：一份 bundle 走完编码→配对
// （relay E2E + 探测 + 回环反代）→程序化兑换（领票 + /console Set-Cookie）→
// 拿到会话 cookie；并锁两条承重断言：兑换不跟随 302、兑换请求不带 Authorization、
// 回环门禁（无 cookie 被拒、带 cookie 可达）。
func TestExchangeTicketVerticalSlice(t *testing.T) {
	fa := &fakeAgentd{}
	relayURL, cleanup := startFakeRelay(t, pairToken, "acc1", "devbox", fa)
	defer cleanup()

	payload, err := proto.EncodePairBundle(relayBundle(relayURL, "devbox"))
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := core.Pair(ctx, payload)
	if err != nil {
		t.Fatalf("配对失败: %v", err)
	}
	if len(res.Machines) != 1 || !res.Machines[0].Online || res.Machines[0].Origin == "" {
		t.Fatalf("应一台在线机带 origin: %+v", res.Machines)
	}
	origin := res.Machines[0].Origin

	ck, err := core.Activate(ctx, "devbox")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if ck.Name != sessionCookieName || !strings.Contains(ck.Value, "devbox") {
		t.Fatalf("cookie 形状不对: %+v", ck)
	}
	if ck.Path != "/" || !ck.HttpOnly || ck.Secure {
		t.Fatalf("cookie 属性不对（明文 loopback 必须非 Secure）: %+v", ck)
	}
	if ck.SameSite != "Lax" {
		t.Fatalf("SameSite 应 Lax: %+v", ck)
	}
	if got := core.ActiveMachine(); got != "devbox" {
		t.Fatalf("ActiveMachine=%q，期望 devbox", got)
	}
	if fa.afterLogin != 0 {
		t.Fatalf("兑换不得跟随 302，/after-login 命中 %d 次", fa.afterLogin)
	}
	for _, a := range fa.consoleAuth {
		if a != "" {
			t.Fatalf("兑换请求不得带 Authorization，上游看到 %q", a)
		}
	}

	// 回环门禁：无 cookie 的同机请求被 agentd 拒（401）。
	noCookie, err := localClient().Get(origin + "/api/status")
	if err != nil {
		t.Fatalf("回环请求: %v", err)
	}
	noCookie.Body.Close()
	if noCookie.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 cookie 应被回环门禁拒，得到 %d", noCookie.StatusCode)
	}
	// 带上兑换来的 cookie 即可达（webview 的正常形态）。
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/status", nil)
	req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	withCookie, err := localClient().Do(req)
	if err != nil {
		t.Fatalf("带 cookie 请求: %v", err)
	}
	defer withCookie.Body.Close()
	if withCookie.StatusCode != http.StatusOK {
		t.Fatalf("带会话 cookie 应可达，得到 %d", withCookie.StatusCode)
	}
}

// TestExchangeTicketFailureModes 锁「失败不得静默成功」：非 302、缺 Set-Cookie、
// cookie 空值都必须返回错误，且不得留下活动会话（字段缺失 vs 值为零的区分）。
func TestExchangeTicketFailureModes(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*fakeAgentd)
	}{
		{"200 无 Set-Cookie", func(f *fakeAgentd) { f.consoleCode = http.StatusOK; f.omitCookie = true }},
		{"302 无 Set-Cookie", func(f *fakeAgentd) { f.omitCookie = true }},
		{"200 带 cookie 非 302", func(f *fakeAgentd) { f.consoleCode = http.StatusOK }},
		{"cookie 空值", func(f *fakeAgentd) { f.extraCookie = "handoff_session=; Path=/"; f.omitCookie = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa := &fakeAgentd{}
			tc.mut(fa)
			relayURL, cleanup := startFakeRelay(t, pairToken, "acc1", "devbox", fa)
			defer cleanup()
			payload, err := proto.EncodePairBundle(relayBundle(relayURL, "devbox"))
			if err != nil {
				t.Fatal(err)
			}
			core := New(nil, slog.Default())
			defer core.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := core.Pair(ctx, payload); err != nil {
				t.Fatal(err)
			}
			if _, err := core.Activate(ctx, "devbox"); err == nil {
				t.Fatal("兑换失败必须返回错误，不得静默成功")
			}
			if core.ActiveMachine() != "" {
				t.Fatalf("兑换失败后不得留下活动会话: %q", core.ActiveMachine())
			}
			if _, err := core.Session(); err == nil {
				t.Fatal("无活动会话时 Session 必须报错")
			}
		})
	}
}

// TestExchangeTicketCookieSelection 锁 Set-Cookie 解析的手写投影边界：多个
// Set-Cookie 头、name 大小写（大写是诱饵）、属性逐项（Path/HttpOnly/Secure/SameSite）。
func TestExchangeTicketCookieSelection(t *testing.T) {
	fa := &fakeAgentd{extraCookie: "HANDOFF_SESSION=decoy; Path=/; Secure"}
	relayURL, cleanup := startFakeRelay(t, pairToken, "acc1", "devbox", fa)
	defer cleanup()
	payload, err := proto.EncodePairBundle(relayBundle(relayURL, "devbox"))
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := core.Pair(ctx, payload); err != nil {
		t.Fatal(err)
	}
	ck, err := core.Activate(ctx, "devbox")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !strings.Contains(ck.Value, "devbox") {
		t.Fatalf("必须选中 handoff_session（大写名是诱饵）: %+v", ck)
	}
	if ck.Path != "/" || !ck.HttpOnly || ck.Secure || ck.SameSite != "Lax" {
		t.Fatalf("cookie 属性投影失准: %+v", ck)
	}
	// 缺 name 也不行：只回大写名等价于没有会话。
	fa2 := &fakeAgentd{extraCookie: "HANDOFF_SESSION=decoy; Path=/", omitCookie: true}
	relayURL2, cleanup2 := startFakeRelay(t, pairToken, "acc1", "devbox", fa2)
	defer cleanup2()
	payload2, _ := proto.EncodePairBundle(relayBundle(relayURL2, "devbox"))
	core2 := New(nil, slog.Default())
	defer core2.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if _, err := core2.Pair(ctx2, payload2); err != nil {
		t.Fatal(err)
	}
	if _, err := core2.Activate(ctx2, "devbox"); err == nil {
		t.Fatal("只有大写 cookie 名必须报错（name 精确匹配）")
	}
}

// TestPairDecodeFailureRegistersNothing 契约条 21：解码失败返回错误、不登记机器。
func TestPairDecodeFailureRegistersNothing(t *testing.T) {
	core := New(nil, slog.Default())
	defer core.Close()
	if _, err := core.Pair(context.Background(), "not-json"); err == nil {
		t.Fatal("解码失败必须报错")
	}
	if names := core.MachineNames(); len(names) != 0 {
		t.Fatalf("解码失败不得登记机器: %v", names)
	}
}

// TestSessionJarSwitchesMachine 锁多机会话域（契约条 31）：每机独立回环端口 +
// 单槽会话罐。切机后 cookie 必须是新机的、旧机 cookie 不再可得；切回旧机重兑换。
// 机器名进设备名、进而进 ticket 与 cookie 值，故可区分机器。
func TestSessionJarSwitchesMachine(t *testing.T) {
	fa := &fakeAgentd{}
	relayURL, cleanup := startFakeRelay(t, pairToken, "acc1", "devbox", fa)
	defer cleanup()
	bundle := proto.PairBundle{
		Version: proto.PairVersion,
		Relay:   &proto.PairRelay{URL: relayURL, Credential: "pipecred"},
		Machines: []proto.PairMachine{
			{Name: "alpha", Token: pairToken, Node: "devbox"},
			{Name: "beta", Token: pairToken, Node: "devbox"},
		},
	}
	payload, err := proto.EncodePairBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := core.Pair(ctx, payload)
	if err != nil {
		t.Fatalf("配对: %v", err)
	}
	origins := map[string]string{}
	for _, pm := range res.Machines {
		if !pm.Online {
			t.Fatalf("两机都应在线: %+v", pm)
		}
		origins[pm.Name] = pm.Origin
	}
	if origins["alpha"] == origins["beta"] {
		t.Fatalf("每机必须独立回环端口: %+v", origins)
	}

	ckA, err := core.Activate(ctx, "alpha")
	if err != nil {
		t.Fatalf("Activate alpha: %v", err)
	}
	ckB, err := core.Activate(ctx, "beta")
	if err != nil {
		t.Fatalf("Activate beta: %v", err)
	}
	if ckA.Value == ckB.Value {
		t.Fatalf("切机后必须重兑换，cookie 不得沿用旧机: A=%q B=%q", ckA.Value, ckB.Value)
	}
	if !strings.Contains(ckA.Value, "alpha") || !strings.Contains(ckB.Value, "beta") {
		t.Fatalf("cookie 未绑定机器: A=%q B=%q", ckA.Value, ckB.Value)
	}
	if core.ActiveMachine() != "beta" {
		t.Fatalf("切机后 ActiveMachine=%q，期望 beta", core.ActiveMachine())
	}
	sess, err := core.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sess.Value != ckB.Value {
		t.Fatalf("单槽会话罐必须只装当前机器: got=%q want=%q", sess.Value, ckB.Value)
	}
	// 切回 alpha：必须重新兑换出 alpha 的 cookie。
	ckA2, err := core.Activate(ctx, "alpha")
	if err != nil {
		t.Fatalf("切回 alpha: %v", err)
	}
	if !strings.Contains(ckA2.Value, "alpha") {
		t.Fatalf("切回 alpha 必须重兑换: %+v", ckA2)
	}
	if core.ActiveMachine() != "alpha" {
		t.Fatalf("切回后 ActiveMachine=%q", core.ActiveMachine())
	}
}

// TestActivateSameMachineCached 锁同机重复 Activate 命中缓存（不重复领票）；
// 缓存是切机重兑换语义的对偶：机器没变就不打扰对端。
func TestActivateSameMachineCached(t *testing.T) {
	fa := &fakeAgentd{}
	relayURL, cleanup := startFakeRelay(t, pairToken, "acc1", "devbox", fa)
	defer cleanup()
	payload, err := proto.EncodePairBundle(relayBundle(relayURL, "devbox"))
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := core.Pair(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Activate(ctx, "devbox"); err != nil {
		t.Fatalf("首次 Activate: %v", err)
	}
	fa.mu.Lock()
	afterFirst := fa.tickets
	fa.mu.Unlock()
	if afterFirst != 1 {
		t.Fatalf("首次 Activate 应恰领 1 张 ticket，得到 %d", afterFirst)
	}
	if _, err := core.Activate(ctx, "devbox"); err != nil {
		t.Fatalf("重复 Activate: %v", err)
	}
	fa.mu.Lock()
	afterSecond := fa.tickets
	fa.mu.Unlock()
	if afterSecond != 1 {
		t.Fatalf("同机重复 Activate 必须命中缓存（不重复领票），tickets=%d", afterSecond)
	}
}

// TestRetryOfflineThenOnline 锁离线补配（contract §8.8 / spec「上线后补配」）：
// 首次拨号失败的机器在册标 offline 且 Origin 空、出现在配对清单；重试入口在
// 可达后把机器翻回 online 并给出回环源。用注入 DialFunc 模拟「先不可达、后可达」。
func TestRetryOfflineThenOnline(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	var mu sync.Mutex
	calls := 0
	dial := func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			return nil, nil, errors.New("模拟首次不可达")
		}
		return client.New(ts.URL, m.Token), func() {}, nil
	}
	core := New(dial, slog.Default())
	defer core.Close()
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "late", Token: pairToken, Addr: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := core.Pair(context.Background(), payload)
	if err != nil {
		t.Fatalf("部分 bundle 不应整单失败: %v", err)
	}
	if len(res.Machines) != 1 || res.Machines[0].Online || res.Machines[0].Origin != "" {
		t.Fatalf("首次应离线且 Origin 空: %+v", res.Machines)
	}
	if _, err := core.Origin("late"); err == nil {
		t.Fatal("离线机 Origin 必须报错")
	}
	names := core.MachineNames()
	if len(names) != 1 || names[0] != "late" {
		t.Fatalf("离线机必须在配对清单: %v", names)
	}
	pm, err := core.Retry(context.Background(), "late")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if !pm.Online || pm.Origin == "" {
		t.Fatalf("补配后应在线: %+v", pm)
	}
	if got, err := core.Origin("late"); err != nil || got != pm.Origin {
		t.Fatalf("在线机 Origin 应可查: %q %v", got, err)
	}
	// 未登记机器的补配必须报错（不凭空造机器）。
	if _, err := core.Retry(context.Background(), "ghost"); err == nil {
		t.Fatal("未登记机器 Retry 必须报错")
	}
}

// TestTokenEntropyGateRejectsWeakToken 契约条 11：relay 形态 token 少于 32 hex
// 字符被 entropy 闸拒绝，Core.Pair 据此标离线不整单失败。
func TestTokenEntropyGateRejectsWeakToken(t *testing.T) {
	if _, _, err := DefaultDial(context.Background(),
		proto.PairBundle{Relay: &proto.PairRelay{URL: "wss://relay.invalid"}},
		proto.PairMachine{Name: "m", Token: "abcd", Node: "n"}); err == nil {
		t.Fatal("短 token 必须被熵闸拒绝")
	}
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Relay:   &proto.PairRelay{URL: "wss://relay.invalid", Credential: "c"},
		Machines: []proto.PairMachine{
			{Name: "weak", Token: "abcd", Node: "n"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	res, err := core.Pair(context.Background(), payload)
	if err != nil {
		t.Fatalf("弱 token 不应整单失败: %v", err)
	}
	if len(res.Machines) != 1 || res.Machines[0].Online || res.Machines[0].Origin != "" {
		t.Fatalf("弱 token 机器应离线且 Origin 空: %+v", res.Machines)
	}
}

// TestPairIdempotentNoLeak 契约条 19：同名机器重配覆盖旧客户端与反代，旧资源
// 恰回收一次（不泄漏端口/goroutine）。用注入 DialFunc 的 cleanup 计数作确定性判据。
func TestPairIdempotentNoLeak(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	var mu sync.Mutex
	cleanups := 0
	dial := func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		return client.New(ts.URL, m.Token), func() {
			mu.Lock()
			cleanups++
			mu.Unlock()
		}, nil
	}
	core := New(dial, slog.Default())
	defer core.Close()
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "m", Token: pairToken, Addr: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r1, err := core.Pair(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := core.Pair(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Machines[0].Origin == r2.Machines[0].Origin {
		t.Fatalf("重配应换新回环端口: %q", r1.Machines[0].Origin)
	}
	mu.Lock()
	got := cleanups
	mu.Unlock()
	if got != 1 {
		t.Fatalf("旧客户端 cleanup 应恰回收一次，得到 %d", got)
	}
	if names := core.MachineNames(); len(names) != 1 {
		t.Fatalf("重配不得多出机器: %v", names)
	}
}

// TestCloseIdempotentAndRejectsAfter 契约条 20：Close 幂等；关闭后 Pair/Origin/
// Activate/Retry/Session 一律拒绝且不留会话罐。
func TestCloseIdempotentAndRejectsAfter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	dial := func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		return client.New(ts.URL, m.Token), func() {}, nil
	}
	core := New(dial, slog.Default())
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "m", Token: pairToken, Addr: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := core.Pair(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if core.ActiveMachine() != "" {
		// 未兑换过，罐应为空。
		t.Fatalf("未 Activate 前不应有活动会话: %q", core.ActiveMachine())
	}
	if err := core.Close(); err != nil {
		t.Fatalf("首次 Close: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatalf("Close 必须幂等: %v", err)
	}
	if _, err := core.Pair(ctx, payload); err == nil {
		t.Fatal("Close 后 Pair 必须拒绝")
	}
	if _, err := core.Origin("m"); err == nil {
		t.Fatal("Close 后 Origin 必须拒绝")
	}
	if _, err := core.Activate(ctx, "m"); err == nil {
		t.Fatal("Close 后 Activate 必须拒绝")
	}
	if _, err := core.Retry(ctx, "m"); err == nil {
		t.Fatal("Close 后 Retry 必须拒绝")
	}
	if _, err := core.Session(); err == nil {
		t.Fatal("Close 后 Session 必须拒绝")
	}
}

// TestConcurrentPairActivateClose 并发压测（-race）：Pair/Activate/Close 并发下
// 无数据竞争、无 panic。Activate 未命中机器返回错误属预期。
func TestConcurrentPairActivateClose(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	dial := func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		return client.New(ts.URL, m.Token), func() {}, nil
	}
	core := New(dial, slog.Default())
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version: proto.PairVersion,
		Machines: []proto.PairMachine{
			{Name: "m", Token: pairToken, Addr: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = core.Pair(ctx, payload) }()
	}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = core.Activate(ctx, "m") }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = core.Close() }()
	wg.Wait()
}

// TestSessionFileHasNoRawHTTPClient 锁协调者裁决（2026-09-19）：本卡新增文件
// 不得新增 client.HTTPClient() 取用点（B233.3 冻结条 25 与 B369 §3.3 的冲突
// 已记卡；既有违规只允许留在 core.go / proxy.go）。
func TestSessionFileHasNoRawHTTPClient(t *testing.T) {
	b, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), ".HTTPClient()") {
		t.Fatal("session.go 不得取 client.HTTPClient()（本卡裁决：不新增违规取用点）")
	}
}

// TestSessionCookieNameMatchesAgentdSource 锁 wire 常量对齐：mobilecore 不得
// import agentd，cookie 名是镜像字面值——直接比对 agentd 源，防漂移（序列化边界）。
func TestSessionCookieNameMatchesAgentdSource(t *testing.T) {
	b, err := os.ReadFile("../agentd/auth.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `sessionCookieName = "handoff_session"`) {
		t.Fatal("mobilecore 镜像的 cookie 名与 agentd 源漂移，须同步 sessionCookieName")
	}
}
