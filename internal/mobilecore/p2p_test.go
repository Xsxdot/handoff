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
	"net/http"
	"net/http/httptest"
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
}

func newP2PMobileWorld(t *testing.T) *p2pMobileWorld {
	t.Helper()
	const account, node = "acc-p2p", "devbox-p2p"
	w := &p2pMobileWorld{relay: newSpliceRelay(t, account),
		world: p2ptest.New(t, p2ptest.PortRestrictedCone, p2ptest.PortRestrictedCone)}
	w.fa = &fakeAgentd{}
	w.upstream = &remoteRecorder{next: w.fa}
	stun := []string{w.world.STUNURL()}
	ln := relay.NewListener(w.relay.url, "cred", node, pairToken, "", w.upstream, nil,
		relay.WithListenerP2P(relay.P2POptions{Enabled: true, STUN: stun, Net: w.world.B}))
	ctx, cancel := context.WithCancel(context.Background())
	lnDone := make(chan struct{})
	go func() { defer close(lnDone); ln.RunWithReconnect(ctx) }()

	opts := mobileP2P()
	opts.STUN, opts.Net = stun, w.world.A
	w.core = New(dialWith(opts, nil), nil)
	t.Cleanup(func() {
		w.core.Close()
		cancel()
		select {
		case <-lnDone:
		case <-time.After(10 * time.Second):
			t.Error("listener 10s 内未退出")
		}
	})
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

	start := time.Now()
	if err := w.core.Resume(ctx, "devbox"); err != nil {
		t.Fatal(err)
	}
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
}
