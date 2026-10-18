// B438.2 竖切（S7）：移动连接核经生产 DialFunc 形状拨 relay 形态机器，relay 隧道
// 先通，后台打洞升级成功后经回环反代的请求不再经 relay；Resume 后新 Dialer 立即
// 重新升级（不等退避）。
//
// 拓扑：p2ptest vnet 世界（A = 手机侧、B = executor 侧，各在端口受限锥 NAT 后）+
// 本文件的 multi-CONNECT splice relay（计字节）+ 真 relay.Listener（打洞应答开）。
package mobilecore

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/p2p/p2ptest"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
)

// directPrefix 是 B 侧看到的 A 的 NAT 外网地址：经直连路径到达的请求 RemoteAddr
// 以它开头；经 relay 的请求 RemoteAddr 是 websocket 适配层的占位地址。
const directPrefix = "27.1.1.1:"

func TestMobileP2POptionsAreProductionDefaults(t *testing.T) {
	o := mobileP2P()
	if !o.Enabled {
		t.Fatal("移动端打洞应恒开")
	}
	want := config.DefaultP2PSTUN()
	if strings.Join(o.STUN, ",") != strings.Join(want, ",") {
		t.Fatalf("STUN 应取配置缺省列表 %v，得到 %v", want, o.STUN)
	}
	if o.Net != nil {
		t.Fatal("生产选项不得注入测试网络")
	}
}

// spliceRelay 是行为等价的最小 relay：executor REGISTER 后，每条 CONNECT 在
// executor 会话上开一条流 1:1 拼接，并按 CONNECT 计字节。
type spliceRelay struct {
	url     string
	account string

	mu       sync.Mutex
	cond     *sync.Cond
	exec     *yamux.Session
	connects []*atomic.Int64
}

func newSpliceRelay(t *testing.T, account string) *spliceRelay {
	t.Helper()
	r := &spliceRelay{account: account}
	r.cond = sync.NewCond(&r.mu)
	srv := httptest.NewServer(http.HandlerFunc(r.handle))
	t.Cleanup(srv.Close)
	r.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return r
}

func (r *spliceRelay) handle(w http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	ctx := req.Context()
	_, data, err := ws.Read(ctx)
	if err != nil {
		return
	}
	f, err := relay.Decode(data)
	if err != nil {
		return
	}
	reply := func(t relay.FrameType) bool {
		b, err := relay.Encode(relay.Frame{Type: t, Account: r.account})
		return err == nil && ws.Write(ctx, websocket.MessageText, b) == nil
	}
	raw := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	switch f.Type {
	case relay.Register:
		if !reply(relay.Registered) {
			return
		}
		sess, err := yamux.Client(raw, yamuxConfig())
		if err != nil {
			return
		}
		r.mu.Lock()
		r.exec = sess
		r.cond.Broadcast()
		r.mu.Unlock()
		<-sess.CloseChan()
	case relay.Connect:
		if !reply(relay.ConnectOK) {
			return
		}
		r.mu.Lock()
		for r.exec == nil || r.exec.IsClosed() {
			r.cond.Wait()
		}
		exec := r.exec
		n := &atomic.Int64{}
		r.connects = append(r.connects, n)
		r.mu.Unlock()
		stream, err := exec.Open()
		if err != nil {
			return
		}
		count := writerFunc(func(p []byte) (int, error) { n.Add(int64(len(p))); return len(p), nil })
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(io.MultiWriter(stream, count), raw); done <- struct{}{} }()
		go func() { _, _ = io.Copy(io.MultiWriter(raw, count), stream); done <- struct{}{} }()
		<-done
		_ = stream.Close()
		_ = raw.Close()
		<-done
	}
}

func (r *spliceRelay) connectCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.connects)
}

// totalBytes 是全部 CONNECT 的字节和（app 隧道 + 信令会话，含每代 Dialer）。
func (r *spliceRelay) totalBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s int64
	for _, n := range r.connects {
		s += n.Load()
	}
	return s
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// remoteRecorder 记录上游看到的最近一次请求 RemoteAddr。
type remoteRecorder struct {
	next http.Handler
	mu   sync.Mutex
	last string
}

func (h *remoteRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.last = r.RemoteAddr
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *remoteRecorder) lastRemote() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

type p2pMobileWorld struct {
	relay    *spliceRelay
	world    *p2ptest.World
	upstream *remoteRecorder
	fa       *fakeAgentd
	core     *Core
	payload  string
	dlog     *logCapture // 手机侧 Dialer 日志
	llog     *logCapture // executor 侧 Listener 日志

	shutdownOnce sync.Once
	shutdownFn   func()
}

// worldOpts：dial 为 nil 时用 vnet 注入的 dialWith；listenerP2P 为 false 时 Listener
// 对信令回 disabled。
type worldOpts struct {
	dial        func(w *p2pMobileWorld) DialFunc
	listenerOff bool
}

func newP2PMobileWorld(t *testing.T, o ...worldOpts) *p2pMobileWorld {
	t.Helper()
	var opt worldOpts
	if len(o) > 0 {
		opt = o[0]
	}
	const account, node = "acc-p2p", "devbox-p2p"
	w := &p2pMobileWorld{relay: newSpliceRelay(t, account),
		world: p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone),
		dlog:  &logCapture{}, llog: &logCapture{}}
	w.fa = &fakeAgentd{}
	w.upstream = &remoteRecorder{next: w.fa}
	stun := []string{w.world.STUNURL()}
	var lopts []relay.ListenerOption
	if !opt.listenerOff {
		lopts = append(lopts, relay.WithListenerP2P(relay.P2POptions{Enabled: true, STUN: stun, Net: w.world.B}))
	}
	ln := relay.NewListener(w.relay.url, "cred", node, pairToken, "", w.upstream, slog.New(w.llog), lopts...)
	ctx, cancel := context.WithCancel(context.Background())
	lnDone := make(chan struct{})
	go func() { defer close(lnDone); ln.RunWithReconnect(ctx) }()

	dial := opt.dial
	if dial == nil {
		dial = func(w *p2pMobileWorld) DialFunc {
			opts := mobileP2P()
			opts.STUN, opts.Net = stun, w.world.A
			return dialWith(opts, slog.New(w.dlog))
		}
	}
	w.core = New(dial(w), nil)
	w.shutdownFn = func() {
		w.core.Close()
		cancel()
		select {
		case <-lnDone:
		case <-time.After(10 * time.Second):
			t.Error("listener 10s 内未退出")
		}
	}
	t.Cleanup(w.shutdown)
	payload, err := proto.EncodePairBundle(proto.PairBundle{
		Version:   proto.PairVersion,
		Relay:     &proto.PairRelay{URL: w.relay.url, Credential: "pipecred"},
		Machines:  []proto.PairMachine{{Name: "devbox", Token: pairToken, Node: node}},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.payload = payload
	return w
}

func (w *p2pMobileWorld) shutdown() { w.shutdownOnce.Do(w.shutdownFn) }

// logCapture 是记录消息文本的 slog.Handler（并发安全）。
type logCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, r.Message)
	c.mu.Unlock()
	return nil
}
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *logCapture) WithGroup(string) slog.Handler      { return c }

func (c *logCapture) count(msg string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

// waitLog 等到 msg 至少出现 n 次。
func waitLog(t *testing.T, c *logCapture, msg string, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c.count(msg) >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%v 内日志 %q 只出现 %d 次（要 %d）", within, msg, c.count(msg), n)
}

// waitDirect 经回环反代反复请求，直到上游看到直连地址；返回该次请求的 cookie 客户端。
func (w *p2pMobileWorld) waitDirect(t *testing.T, origin string, ck *http.Cookie, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		w.get(t, origin, ck)
		if strings.HasPrefix(w.upstream.lastRemote(), directPrefix) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%v 内未升级到直连，上游最近 RemoteAddr=%q", within, w.upstream.lastRemote())
}

func (w *p2pMobileWorld) get(t *testing.T, origin string, ck *http.Cookie) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/status", nil)
	req.AddCookie(ck)
	hc := localClient()
	defer hc.CloseIdleConnections()
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("经回环反代请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// TestMobilePairUpgradesToDirect：S7 主路径——先 relay 可用，后台升级后流量离开 relay。
func TestMobilePairUpgradesToDirect(t *testing.T) {
	w := newP2PMobileWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := w.core.Pair(ctx, w.payload)
	if err != nil || len(res.Machines) != 1 || !res.Machines[0].Online {
		t.Fatalf("配对应一台在线: %+v err=%v", res, err)
	}
	// 配对探测经 relay 完成（升级尚在后台）。
	if r := w.upstream.lastRemote(); strings.HasPrefix(r, directPrefix) {
		t.Fatalf("配对探测不应已是直连: %q", r)
	}
	ck, err := w.core.Activate(ctx, "devbox")
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := w.core.Origin("devbox")
	w.waitDirect(t, origin, &http.Cookie{Name: ck.Name, Value: ck.Value}, 10*time.Second)

	before := w.relay.totalBytes()
	for i := 0; i < 20; i++ {
		w.get(t, origin, &http.Cookie{Name: ck.Name, Value: ck.Value})
		if r := w.upstream.lastRemote(); !strings.HasPrefix(r, directPrefix) {
			t.Fatalf("升级后第 %d 个请求未走直连: %q", i, r)
		}
	}
	if after := w.relay.totalBytes(); after != before {
		t.Fatalf("升级后 relay 字节应零增长: %d -> %d", before, after)
	}
	// 用户可见出口日志：手机侧 Dialer 打一次「p2p 直连已建立」（spec 冻结文案）。
	if n := w.dlog.count("p2p 直连已建立"); n != 1 {
		t.Fatalf("手机侧「p2p 直连已建立」应 1 条，得到 %d", n)
	}
}

// TestMobileResumeReupgradesWithoutBackoff：回前台 Resume 换新 Dialer 后立即重新升级。
func TestMobileResumeReupgradesWithoutBackoff(t *testing.T) {
	w := newP2PMobileWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := w.core.Pair(ctx, w.payload); err != nil {
		t.Fatal(err)
	}
	ck, err := w.core.Activate(ctx, "devbox")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: ck.Name, Value: ck.Value}
	origin, _ := w.core.Origin("devbox")
	w.waitDirect(t, origin, cookie, 10*time.Second)

	if n := w.llog.count("p2p 直连会话已关闭"); n != 0 {
		t.Fatalf("Resume 前不应有直连关闭: %d", n)
	}
	start := time.Now()
	if err := w.core.Resume(ctx, "devbox"); err != nil {
		t.Fatal(err)
	}
	// 旧 Dialer 已随 cleanup 关闭：它的直连会话在对端被拆掉。
	waitLog(t, w.llog, "p2p 直连会话已关闭", 1, 5*time.Second)
	// 旧 Dialer 已关：紧随其后的请求经新 Dialer 的 relay 隧道（新直连尚未建立）。
	w.get(t, origin, cookie)
	if r := w.upstream.lastRemote(); strings.HasPrefix(r, directPrefix) {
		t.Fatalf("Resume 后首个请求应经新 relay 隧道: %q", r)
	}
	// 退避基数 30s；新 Dialer 不继承旧状态，应远早于此重新升级。
	w.waitDirect(t, origin, cookie, 10*time.Second)
	if el := time.Since(start); el > 15*time.Second {
		t.Fatalf("Resume 后重新升级耗时 %v，疑似等了退避", el)
	}
	waitLog(t, w.dlog, "p2p 直连已建立", 2, 5*time.Second)
}

// TestDefaultDialStartsUpgrade 锁生产组装点：Core 缺省（DefaultDial）构造的 relay
// Dialer 在隧道建好后会起打洞升级——表现为同一 relay 上出现第二条 CONNECT（信令
// 会话）。关掉打洞的 Dialer 整个生命周期只开一条（F3 / T-S5c 同口径）。
func TestDefaultDialStartsUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name string
		dial func(w *p2pMobileWorld) DialFunc
		want int
	}{
		{"production", func(*p2pMobileWorld) DialFunc { return nil }, 2},
		{"p2p-off-control", func(*p2pMobileWorld) DialFunc { return dialWith(relay.P2POptions{}, nil) }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Listener 关打洞：生产 DefaultDial 用真网络与公网 STUN，这里只看信令是否发起。
			w := newP2PMobileWorld(t, worldOpts{dial: tc.dial, listenerOff: true})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := w.core.Pair(ctx, w.payload); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && w.relay.connectCount() < tc.want {
				time.Sleep(50 * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond) // 给多余的 CONNECT 出现的机会
			if got := w.relay.connectCount(); got != tc.want {
				t.Fatalf("relay CONNECT 数应为 %d，得到 %d", tc.want, got)
			}
		})
	}
}

// TestMobileCloseLeavesNoP2PGoroutines：直连在役时 Core.Close（连同 Listener 停止）
// 后不残留 ICE/sctp goroutine。
func TestMobileCloseLeavesNoP2PGoroutines(t *testing.T) {
	w := newP2PMobileWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := w.core.Pair(ctx, w.payload); err != nil {
		t.Fatal(err)
	}
	ck, err := w.core.Activate(ctx, "devbox")
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := w.core.Origin("devbox")
	w.waitDirect(t, origin, &http.Cookie{Name: ck.Name, Value: ck.Value}, 10*time.Second)
	// 探测器自检：直连在役时必须看得到 pion goroutine，否则下面的「零残留」不证明任何事。
	if pionGoroutines() == "" {
		t.Fatal("直连在役时探测器未看到任何 pion goroutine")
	}
	closed := make(chan struct{})
	go func() { defer close(closed); w.shutdown() }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatalf("Core.Close + Listener 停止 10s 内未返回（直连资源未释放）:\n%s", pionGoroutines())
	}
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = pionGoroutines()
		if last == "" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Close 后残留 pion goroutine:\n%s", last)
}

// pionGoroutines 返回栈里含 pion/ice 或 pion/sctp 的 goroutine（空 = 无）。vnet 世界
// 自身（transport/vnet、turn）随测试 Cleanup 回收，不计入。
func pionGoroutines() string {
	buf := make([]byte, 8<<20)
	all := string(buf[:runtime.Stack(buf, true)])
	var out []string
	for _, g := range strings.Split(all, "\n\n") {
		if strings.Contains(g, "github.com/pion/ice") || strings.Contains(g, "github.com/pion/sctp") {
			out = append(out, g)
		}
	}
	return strings.Join(out, "\n\n")
}
