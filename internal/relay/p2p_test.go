package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/pion/transport/v5/vnet"

	"github.com/Xsxdot/handoff/internal/p2p"
	"github.com/Xsxdot/handoff/internal/p2p/p2ptest"
)

// ---- 夹具：支持任意多条 CONNECT、逐条计字节、可记录明文载荷、可整体掐断的假 relay ----

type countingRelay struct {
	url     string
	server  *httptest.Server
	account string
	node    string

	mu       sync.Mutex
	exec     *yamux.Session
	execCond *sync.Cond
	connects []*connectStat
	conns    map[*websocket.Conn]struct{}
	down     bool
}

type connectStat struct {
	bytes  atomic.Int64
	mu     sync.Mutex
	record bytes.Buffer
}

func (c *connectStat) Write(p []byte) (int, error) {
	c.bytes.Add(int64(len(p)))
	c.mu.Lock()
	c.record.Write(p)
	c.mu.Unlock()
	return len(p), nil
}

func (c *connectStat) recorded() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.record.Bytes()...)
}

func newCountingRelay(t *testing.T, account, node string) *countingRelay {
	t.Helper()
	r := &countingRelay{account: account, node: node, conns: map[*websocket.Conn]struct{}{}}
	r.execCond = sync.NewCond(&r.mu)
	r.server = httptest.NewServer(http.HandlerFunc(r.handle))
	r.url = "ws" + strings.TrimPrefix(r.server.URL, "http")
	t.Cleanup(func() { r.kill(); r.server.Close() })
	return r
}

func (r *countingRelay) track(ws *websocket.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down {
		return false
	}
	r.conns[ws] = struct{}{}
	return true
}

func (r *countingRelay) handle(w http.ResponseWriter, req *http.Request) {
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	if !r.track(ws) {
		_ = ws.Close(websocket.StatusGoingAway, "relay down")
		return
	}
	ctx := req.Context()
	frame, err := recvControl(ctx, ws)
	if err != nil {
		return
	}
	raw := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	switch frame.Type {
	case Register:
		if err := sendControl(ctx, ws, Frame{Type: Registered, Account: r.account}); err != nil {
			return
		}
		sess, err := yamux.Client(raw, relayYamuxConfig())
		if err != nil {
			return
		}
		r.mu.Lock()
		r.exec = sess
		r.execCond.Broadcast()
		r.mu.Unlock()
		<-sess.CloseChan()
	case Connect:
		if err := sendControl(ctx, ws, Frame{Type: ConnectOK, Account: r.account}); err != nil {
			return
		}
		r.mu.Lock()
		for (r.exec == nil || r.exec.IsClosed()) && !r.down {
			r.execCond.Wait()
		}
		exec := r.exec
		stat := &connectStat{}
		r.connects = append(r.connects, stat)
		r.mu.Unlock()
		if exec == nil {
			return
		}
		stream, err := exec.Open()
		if err != nil {
			return
		}
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(io.MultiWriter(stream, stat), raw); done <- struct{}{} }()
		go func() { _, _ = io.Copy(io.MultiWriter(raw, stat), stream); done <- struct{}{} }()
		<-done
		_ = stream.Close()
		_ = raw.Close()
		<-done
	}
}

// kill 模拟 relay 整体不可用：断开全部现存连接并拒绝新连接。
func (r *countingRelay) kill() {
	r.mu.Lock()
	r.down = true
	conns := r.conns
	r.conns = map[*websocket.Conn]struct{}{}
	exec := r.exec
	r.execCond.Broadcast()
	r.mu.Unlock()
	for ws := range conns {
		_ = ws.CloseNow()
	}
	if exec != nil {
		_ = exec.Close()
	}
}

func (r *countingRelay) connectCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.connects)
}

// tunnelBytes 返回第一条 CONNECT（Dialer 的 app 隧道）上的字节数。
func (r *countingRelay) tunnelBytes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.connects) == 0 {
		return 0
	}
	return r.connects[0].bytes.Load()
}

func (r *countingRelay) allRecorded() []byte {
	r.mu.Lock()
	stats := append([]*connectStat(nil), r.connects...)
	r.mu.Unlock()
	var all []byte
	for _, s := range stats {
		all = append(all, s.recorded()...)
	}
	return all
}

// ---- 日志捕获 ----

type logRec struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type captureHandler struct {
	mu    *sync.Mutex
	recs  *[]logRec
	attrs []slog.Attr
}

func newCapture() (*slog.Logger, *captureHandler) {
	h := &captureHandler{mu: &sync.Mutex{}, recs: &[]logRec{}}
	return slog.New(h), h
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }
func (h *captureHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &captureHandler{mu: h.mu, recs: h.recs, attrs: append(append([]slog.Attr(nil), h.attrs...), as...)}
}
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := logRec{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	for _, a := range h.attrs {
		rec.attrs[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool { rec.attrs[a.Key] = a.Value.String(); return true })
	h.mu.Lock()
	*h.recs = append(*h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) all() []logRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]logRec(nil), *h.recs...)
}

func (h *captureHandler) count(msg string) int {
	n := 0
	for _, r := range h.all() {
		if r.msg == msg {
			n++
		}
	}
	return n
}

func (h *captureHandler) find(msg string) (logRec, bool) {
	for _, r := range h.all() {
		if r.msg == msg {
			return r, true
		}
	}
	return logRec{}, false
}

func (h *captureHandler) waitFor(t *testing.T, msg string, n int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if h.count(msg) >= n {
			return time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%v 内未见第 %d 条日志 %q；已有：\n%s", within, n, msg, h.dump())
	return 0
}

func (h *captureHandler) dump() string {
	var b strings.Builder
	for _, r := range h.all() {
		fmt.Fprintf(&b, "%s %s %v\n", r.level, r.msg, r.attrs)
	}
	return b.String()
}

func (h *captureHandler) text() string { return h.dump() }

// ---- 竖切 harness ----

const (
	tToken   = "tok-0123456789abcdef0123456789abcdef"
	tAccount = "acc1"
	tNode    = "devbox"
)

type p2pHarness struct {
	t        *testing.T
	relay    *countingRelay
	world    *p2ptest.World
	listener *Listener
	dialer   *Dialer
	dlog     *captureHandler
	llog     *captureHandler
	hc       *http.Client
	remotes  chan string
	served   atomic.Int64
	stop     context.CancelFunc
	lnDone   chan struct{}
}

type harnessOpts struct {
	natA, natB   *vnet.NATType
	dialerP2P    bool
	listenerP2P  bool
	emptySTUN    bool
	oldListener  bool
	tuneDialer   func(*Dialer)
	tuneListener func(*Listener)
}

func newP2PHarness(t *testing.T, o harnessOpts) *p2pHarness {
	t.Helper()
	h := &p2pHarness{t: t, remotes: make(chan string, 1024), lnDone: make(chan struct{})}
	h.relay = newCountingRelay(t, tAccount, tNode)
	h.world = p2ptest.New(t, o.natA, o.natB)
	stun := []string{h.world.STUNURL()}
	if o.emptySTUN {
		stun = []string{}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.served.Add(1)
		select {
		case h.remotes <- r.RemoteAddr:
		default:
		}
		// 先读完再回写：Go 的 HTTP/1 服务端缺省非全双工，边读边写会截断请求体。
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	})
	var llogger *slog.Logger
	llogger, h.llog = newCapture()
	var lopts []ListenerOption
	if o.listenerP2P {
		lopts = append(lopts, WithListenerP2P(P2POptions{Enabled: true, STUN: stun, Net: h.world.B}))
	}
	h.listener = NewListener(h.relay.url, "cred", tNode, tToken, "", handler, llogger, lopts...)
	if o.tuneListener != nil {
		o.tuneListener(h.listener)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.stop = cancel
	go func() {
		defer close(h.lnDone)
		if o.oldListener {
			runOldListener(ctx, h.listener)
			return
		}
		h.listener.RunWithReconnect(ctx)
	}()
	var dlogger *slog.Logger
	dlogger, h.dlog = newCapture()
	var dopts []DialerOption
	if o.dialerP2P {
		dopts = append(dopts, WithP2P(P2POptions{Enabled: true, STUN: stun, Net: h.world.A}))
	}
	h.dialer = NewDialer(h.relay.url, "cred", tNode, tToken, "", dlogger, dopts...)
	if o.tuneDialer != nil {
		o.tuneDialer(h.dialer)
	}
	h.hc = &http.Client{Transport: h.dialer.Transport(), Timeout: 10 * time.Second}
	t.Cleanup(func() {
		_ = h.dialer.Close()
		cancel()
		select {
		case <-h.lnDone:
		case <-time.After(10 * time.Second):
			buf := make([]byte, 8<<20)
			t.Errorf("listener 10s 内未退出:\n%s", buf[:runtime.Stack(buf, true)])
		}
	})
	return h
}

// runOldListener 复刻 B438 之前的 executor：E2E 后只认 preview-raw 与 app，不认打洞魔数
// （把打洞信令当 app-yamux 喂进去，yamux 读到非 0 版本字节即关会话）。
func runOldListener(ctx context.Context, l *Listener) {
	for ctx.Err() == nil {
		ws, _, err := websocket.Dial(ctx, l.relayURL, nil)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = sendControl(ctx, ws, Frame{Type: Register, Node: l.node, Credential: l.credential})
		resp, err := recvControl(ctx, ws)
		if err != nil {
			continue
		}
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		sess, err := yamux.Server(raw, relayYamuxConfig())
		if err != nil {
			continue
		}
		for {
			stream, err := sess.Accept()
			if err != nil {
				break
			}
			go func() {
				defer stream.Close()
				secure, err := SecureServer(ctx, stream, l.token, resp.Account, l.node)
				if err != nil {
					return
				}
				l.serveApp(ctx, secure, "old")
			}()
		}
	}
}

func (h *p2pHarness) post(size int) (string, error) {
	payload := make([]byte, size)
	_, _ = rand.Read(payload)
	resp, err := h.hc.Post("http://relay-devbox/echo", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, payload) {
		return "", fmt.Errorf("echo 内容不一致 got=%d want=%d status=%d", len(got), len(payload), resp.StatusCode)
	}
	select {
	case remote := <-h.remotes:
		return remote, nil
	case <-time.After(time.Second):
		return "", errors.New("handler 未记录 RemoteAddr")
	}
}

func (h *p2pHarness) mustPost(size int) string {
	h.t.Helper()
	remote, err := h.post(size)
	if err != nil {
		h.t.Fatalf("请求失败: %v\n日志：\n%s", err, h.dlog.dump())
	}
	return remote
}

// 直连请求在 listener 侧看到的对端地址是 A 的 NAT 公网地址（vnet 27.1.1.1）；
// 经 relay 的请求看到的是本机回环。
func isDirect(remote string) bool { return strings.HasPrefix(remote, "27.1.1.1:") }

const (
	msgEstablished = "p2p 直连已建立"
	msgFailed      = "p2p 打洞失败，继续走 relay"
	msgDropped     = "p2p 直连已断开，回落 relay"
	msgUnsupported = "p2p 对端不支持打洞，继续走 relay"
)

// T-S1：relay 先通、后台升级；升级后新请求确实走直连，relay 隧道字节不再随业务增长。
func TestP2PUpgradeMovesTrafficOffRelay(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true})
	start := time.Now()
	if remote := h.mustPost(1024); isDirect(remote) {
		t.Fatalf("首请求不应等打洞，应经 relay，got %s", remote)
	}
	if first := time.Since(start); first > 2*time.Second {
		t.Fatalf("首请求耗时 %v，疑似在等打洞", first)
	}
	took := h.dlog.waitFor(t, msgEstablished, 1, 5*time.Second)
	t.Logf("升级耗时 %v", took)
	rec, _ := h.dlog.find(msgEstablished)
	if rec.attrs["node"] != tNode || rec.attrs["network"] == "" ||
		!strings.Contains("host srflx prflx", rec.attrs["local_type"]) || rec.attrs["local_type"] == "" {
		t.Fatalf("建立日志 attrs 不全: %v", rec.attrs)
	}
	before := h.relay.tunnelBytes()
	for i := 0; i < 20; i++ {
		if remote := h.mustPost(64 * 1024); !isDirect(remote) {
			t.Fatalf("升级后第 %d 个请求未走直连: %s", i, remote)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remote, err := h.post(128 * 1024)
			if err == nil && !isDirect(remote) {
				err = fmt.Errorf("并发请求未走直连: %s", remote)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if grew := h.relay.tunnelBytes() - before; grew > 1024 {
		t.Fatalf("升级后 relay 隧道仍增长 %d 字节（业务量约 1.8 MiB）", grew)
	}
	for _, r := range append(h.dlog.all(), h.llog.all()...) {
		if r.level >= slog.LevelWarn {
			t.Fatalf("成功路径出现 WARN: %s %v", r.msg, r.attrs)
		}
	}
	if h.llog.count(msgEstablished) != 1 {
		t.Fatalf("listener 侧应有一条直连建立日志:\n%s", h.llog.dump())
	}
	// T-S6b：relay 可见字节里没有 ICE 凭据与候选（信令在 E2E 内）。
	seen := h.relay.allRecorded()
	for _, needle := range []string{"ufrag", "typ host", "typ srflx", "192.168.0.1", "10.2.0.1", "27.1.1.1", "28.1.1.1", p2p.SignalMagic} {
		if bytes.Contains(seen, []byte(needle)) {
			t.Fatalf("relay 可见明文里出现 %q", needle)
		}
	}
	// T-S6c：日志不含候选串与凭据。
	all := h.dlog.text() + h.llog.text()
	if strings.Contains(all, " typ ") || strings.Contains(all, "ufrag") {
		t.Fatalf("日志泄漏候选/凭据:\n%s", all)
	}
}

// T-S2：一端对称 NAT——业务始终经 relay 可用，≤15s 放弃并打 INFO，无 WARN，relay 计数增长。
func TestP2PSymmetricNATStaysOnRelay(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.Symmetric,
		dialerP2P: true, listenerP2P: true})
	start := time.Now()
	h.mustPost(1024)
	if first := time.Since(start); first > 2*time.Second {
		t.Fatalf("首请求耗时 %v", first)
	}
	before := h.relay.tunnelBytes()
	took := h.dlog.waitFor(t, msgFailed, 1, 15*time.Second)
	t.Logf("放弃耗时 %v", took)
	rec, _ := h.dlog.find(msgFailed)
	if r := rec.attrs["reason"]; r != reasonTimeout && r != reasonICEFailed {
		t.Fatalf("reason=%q", r)
	}
	if rec.attrs["retry_in"] != "30s" {
		t.Fatalf("首次退避应 30s，got %v", rec.attrs)
	}
	for i := 0; i < 5; i++ {
		if remote := h.mustPost(16 * 1024); isDirect(remote) {
			t.Fatalf("对称 NAT 下不应直连: %s", remote)
		}
	}
	if h.relay.tunnelBytes()-before < 5*16*1024 {
		t.Fatal("relay 计数未随请求增长（计数器失效？）")
	}
	for _, r := range h.dlog.all() {
		if r.level >= slog.LevelWarn {
			t.Fatalf("打洞失败不应 WARN: %s %v", r.msg, r.attrs)
		}
	}
}

// 退避序列：30s 起翻倍、封顶 10min。
func TestUpgradeBackoffSequence(t *testing.T) {
	var got []string
	cur := time.Duration(0)
	for i := 0; i < 8; i++ {
		cur = nextBackoff(cur, upgradeBackoffBase, upgradeBackoffMax)
		got = append(got, cur.String())
	}
	want := "30s 1m0s 2m0s 4m0s 8m0s 10m0s 10m0s 10m0s"
	if strings.Join(got, " ") != want {
		t.Fatalf("退避序列 %v", got)
	}
}

// T-S3：直连中途断 → ≤30s 回落 relay、新请求成功；恢复后退避期满重新升级。
func TestP2PPathCutFallsBackThenReupgrades(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true,
		tuneDialer: func(d *Dialer) { d.deadRetry = 500 * time.Millisecond }})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgEstablished, 1, 5*time.Second)
	if remote := h.mustPost(1024); !isDirect(remote) {
		t.Fatalf("应已直连: %s", remote)
	}
	h.world.Cut(true)
	took := h.dlog.waitFor(t, msgDropped, 1, 30*time.Second)
	t.Logf("判死回落耗时 %v", took)
	before := h.relay.tunnelBytes()
	if remote := h.mustPost(8 * 1024); isDirect(remote) {
		t.Fatalf("断链后不应走直连: %s", remote)
	}
	if h.relay.tunnelBytes() <= before {
		t.Fatal("回落后 relay 计数未增长")
	}
	h.world.Cut(false)
	time.Sleep(600 * time.Millisecond)
	h.mustPost(1024) // 经 relay，触发重新升级
	h.dlog.waitFor(t, msgEstablished, 2, 10*time.Second)
	if remote := h.mustPost(1024); !isDirect(remote) {
		t.Fatalf("重新升级后应直连: %s", remote)
	}
}

// T-S3b：直连在役时 relay 整体不可用——新请求仍经直连成功。
func TestP2PDirectSurvivesRelayOutage(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgEstablished, 1, 5*time.Second)
	h.relay.kill()
	time.Sleep(300 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if remote := h.mustPost(4 * 1024); !isDirect(remote) {
			t.Fatalf("relay 掉线时应仍走直连: %s", remote)
		}
	}
	if h.llog.count("p2p 直连会话已关闭") != 0 {
		t.Fatal("relay 掉线不应拖断直连")
	}
}

// T-S5a：新 coordinator ↔ 旧 listener——稳定走 relay，「不支持」INFO 每 Dialer 只一条。
func TestP2POldListenerStaysOnRelay(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, oldListener: true,
		tuneDialer: func(d *Dialer) { d.unsupportedPeriod = 200 * time.Millisecond }})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgUnsupported, 1, 10*time.Second)
	time.Sleep(300 * time.Millisecond)
	h.mustPost(1024) // 退避期满，再试一次
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && h.dlog.count("p2p 对端仍不支持打洞") == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if h.dlog.count("p2p 对端仍不支持打洞") == 0 {
		t.Fatalf("第二次尝试未发生:\n%s", h.dlog.dump())
	}
	if n := h.dlog.count(msgUnsupported); n != 1 {
		t.Fatalf("「不支持」INFO 应只一条，got %d", n)
	}
	for i := 0; i < 3; i++ {
		if remote := h.mustPost(1024); isDirect(remote) {
			t.Fatal("旧 listener 不可能直连")
		}
	}
	for _, r := range h.dlog.all() {
		if r.level >= slog.LevelWarn {
			t.Fatalf("不应 WARN: %s %v", r.msg, r.attrs)
		}
	}
}

// T-S5b：listener 未开打洞 → 回 disabled；coordinator 同 T-S5a；listener 未建 ICE 代理。
func TestP2PListenerDisabledRejects(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: false})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgUnsupported, 1, 10*time.Second)
	rec, _ := h.dlog.find(msgUnsupported)
	if !strings.Contains(rec.attrs["cause"], p2p.RejectDisabled) {
		t.Fatalf("应因 disabled 判不支持: %v", rec.attrs)
	}
	if h.llog.count("p2p 候选收集完成") != 0 {
		t.Fatal("关闭打洞的 listener 不应建 ICE 代理")
	}
	if remote := h.mustPost(1024); isDirect(remote) {
		t.Fatal("不应直连")
	}
}

// T-S5c：coordinator 不开打洞——全程只一条 CONNECT、零 STUN。
func TestP2PDialerDisabledNeverSignals(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: false, listenerP2P: true})
	for i := 0; i < 5; i++ {
		h.mustPost(1024)
	}
	time.Sleep(500 * time.Millisecond)
	if n := h.relay.connectCount(); n != 1 {
		t.Fatalf("不开打洞应只有 app 隧道一条 CONNECT，got %d", n)
	}
	if n := h.world.STUNPackets(); n != 0 {
		t.Fatalf("不开打洞却发了 %d 个 STUN 包", n)
	}
	for _, r := range h.dlog.all() {
		if strings.HasPrefix(r.msg, "p2p") {
			t.Fatalf("不开打洞不应有 p2p 日志: %s", r.msg)
		}
	}
}

// S8 空 STUN：只 host 候选、零 STUN 包；跨 NAT 打不通，按普通失败放弃。
func TestP2PEmptySTUNZeroSTUNPackets(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true, emptySTUN: true})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgFailed, 1, 15*time.Second)
	if n := h.world.STUNPackets(); n != 0 {
		t.Fatalf("p2p.stun: [] 仍发出 %d 个 STUN 包", n)
	}
}

// T-S6a（listener 侧）：ICE 连通但直连 E2E token 不符 → 握手失败、handler 零调用、WARN 一条。
func TestP2PDirectE2EMismatchRejected(t *testing.T) {
	world := p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)
	llogger, llog := newCapture()
	var served atomic.Int64
	l := NewListener("ws://unused", "cred", tNode, "listener-token-AAAAAAAAAAAAAAAAAAAAAA", tAccount,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served.Add(1) }), llogger,
		WithListenerP2P(P2POptions{Enabled: true, STUN: []string{world.STUNURL()}, Net: world.B}))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, err := p2p.Gather(ctx, p2p.Config{STUN: []string{world.STUNURL()}, Net: world.A}, p2p.Controlling)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p2p.Gather(ctx, p2p.Config{STUN: []string{world.STUNURL()}, Net: world.B}, p2p.Controlled)
	if err != nil {
		t.Fatal(err)
	}
	if !l.reservePending() {
		t.Fatal("reserve")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.connectAndServe(ctx, ctx, b, a.Local(), tAccount, llogger)
	}()
	conn, err := a.Connect(ctx, b.Local())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if _, err := SecureClient(ctx, conn, "attacker-token-BBBBBBBBBBBBBBBBBBBBBB", tAccount, tNode); err == nil {
		t.Fatal("错 token 的直连 E2E 不应成功")
	}
	<-done
	if served.Load() != 0 {
		t.Fatal("E2E 失败时 handler 不应被调用")
	}
	warns := 0
	for _, r := range llog.all() {
		if r.level == slog.LevelWarn {
			warns++
		}
	}
	if warns != 1 || llog.count("p2p 直连 E2E 握手失败，已关闭直连") != 1 {
		t.Fatalf("应恰一条 WARN:\n%s", llog.dump())
	}
	l.p2pMu.Lock()
	defer l.p2pMu.Unlock()
	if l.p2pPending != 0 || l.p2pEstablished != 0 {
		t.Fatalf("名额未归还: pending=%d established=%d", l.p2pPending, l.p2pEstablished)
	}
}

// T-S6a（coordinator 侧）：直连 E2E 失败按 WARN 记录并继续 relay。
func TestP2PE2EFailureLogsWarn(t *testing.T) {
	logger, logs := newCapture()
	d := NewDialer("ws://unused", "cred", tNode, tToken, tAccount, logger)
	defer d.Close()
	d.attempting = true
	d.finishAttempt(nil, &upgradeError{reasonE2EFailed, errors.New("x")})
	if logs.count("p2p 直连 E2E 握手失败，继续走 relay") != 1 || logs.count(msgFailed) != 0 {
		t.Fatalf("E2E 失败应 WARN:\n%s", logs.dump())
	}
}

// listener 上限：待连满额回 busy。
func TestP2PListenerBusy(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true,
		tuneListener: func(l *Listener) { l.p2pMaxPending = 0 }})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgFailed, 1, 10*time.Second)
	rec, _ := h.dlog.find(msgFailed)
	if rec.attrs["reason"] != reasonBusy {
		t.Fatalf("应 busy: %v", rec.attrs)
	}
}

// T-leak：直连在役时 Dialer.Close、listener ctx 取消——之后无 pion/Dialer goroutine。
func TestP2PCloseLeavesNoGoroutines(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true})
	h.mustPost(1024)
	h.dlog.waitFor(t, msgEstablished, 1, 5*time.Second)
	_ = h.dialer.Close()
	h.stop()
	select {
	case <-h.lnDone:
	case <-time.After(10 * time.Second):
		t.Fatal("listener 未在 10s 内退出（直连会话未归 Listener 生命周期？）")
	}
	assertNoGoroutines(t, "pion/ice", "pion/sctp", "relay.(*Dialer)", "relay.(*Listener).connectAndServe")
}

// T-leak：升级进行中（收集/等应答阶段）Close——立即中止、不打失败 INFO。
func TestP2PCloseDuringUpgrade(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.Symmetric,
		dialerP2P: true, listenerP2P: true})
	h.mustPost(1024)
	time.Sleep(1500 * time.Millisecond) // 停在 ICE 检查阶段（对称 NAT 要 ~10s 才失败）
	start := time.Now()
	_ = h.dialer.Close()
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("升级中 Close 耗时 %v", took)
	}
	if h.dlog.count(msgFailed) != 0 {
		t.Fatal("Close 取消不应打打洞失败 INFO")
	}
	assertNoGoroutines(t, "relay.(*Dialer)")
}

// CLI 延迟起跳：延迟内关闭 → 零信令、零 STUN。
func TestP2PStartDelayClosesQuietly(t *testing.T) {
	h := newP2PHarness(t, harnessOpts{natA: p2ptest.PortRestrictedCone, natB: p2ptest.PortRestrictedCone,
		dialerP2P: true, listenerP2P: true,
		tuneDialer: func(d *Dialer) { d.p2pStartDelay = 5 * time.Second }})
	h.mustPost(1024)
	_ = h.dialer.Close()
	if n := h.relay.connectCount(); n != 1 {
		t.Fatalf("延迟内退出不应开信令 CONNECT，got %d", n)
	}
	if n := h.world.STUNPackets(); n != 0 {
		t.Fatalf("延迟内退出不应发 STUN，got %d", n)
	}
	assertNoGoroutines(t, "relay.(*Dialer)")
}

func assertNoGoroutines(t *testing.T, needles ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		buf := make([]byte, 4<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		last = ""
		for _, g := range strings.Split(stacks, "\n\n") {
			for _, n := range needles {
				if strings.Contains(g, n) && !strings.Contains(g, "assertNoGoroutines") {
					last = g
				}
			}
		}
		if last == "" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("残留 goroutine:\n%s", last)
}

var _ = net.Pipe
