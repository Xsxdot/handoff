// 职责：executor 侧从 relay 接受会话并把每条 app 流交给 agentd.Handler（等价本地
// loopback 入站）；边界：不改 agentd 路由，只做传输适配；每会话独立 E2E。
package relay

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/Xsxdot/handoff/internal/p2p"
)

const (
	listenerInitialBackoff = time.Second
	listenerMaxBackoff     = time.Minute
)

// Listener is the executor-side relay listener. It registers once per physical
// tunnel and serves every coordinator app stream with the supplied HTTP handler.
type Listener struct {
	relayURL   string
	credential string
	node       string
	token      string
	account    string
	handler    http.Handler
	log        *slog.Logger

	// 打洞应答（B438，见 listener_p2p.go）。直连会话归 Listener 生命周期
	// （p2pWG + RunWithReconnect 的 ctx），不归单次 Run：relay 断线重连不被在役
	// 直连拖住，也不拖断在役直连。
	p2p            P2POptions
	p2pMu          sync.Mutex
	p2pPending     int
	p2pEstablished int
	p2pWG          sync.WaitGroup
	// 上限与预算：生产取常量，测试可改。
	p2pMaxPending     int
	p2pMaxEstablished int
	p2pRequestTimeout time.Duration
	p2pConnectBudget  time.Duration
}

// ListenerOption 配置 Listener。
type ListenerOption func(*Listener)

// WithListenerP2P 开启打洞应答。不传 = 对打洞信令回 disabled（行为与 B438 之前
// 的 relay 会话服务一致）。
func WithListenerP2P(o P2POptions) ListenerOption { return func(l *Listener) { l.p2p = o } }

// NewListener constructs an executor relay listener. account is optional when
// the relay's REGISTERED response supplies the authenticated account ID.
func NewListener(relayURL, credential, node, token, account string, handler http.Handler, log *slog.Logger, opts ...ListenerOption) *Listener {
	if log == nil {
		log = slog.Default()
	}
	l := &Listener{
		relayURL: relayURL, credential: credential, node: node,
		token: token, account: account, handler: handler, log: log,
		p2pMaxPending: listenerP2PMaxPending, p2pMaxEstablished: listenerP2PMaxEstablished,
		p2pRequestTimeout: listenerP2PRequestTimeout, p2pConnectBudget: upgradeBudget,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Run establishes one registered relay tunnel and serves sessions until it
// closes. A non-nil error means the reconnect wrapper should try again.
func (l *Listener) Run(ctx context.Context) error {
	if l.handler == nil {
		return errors.New("relay listener: nil HTTP handler")
	}
	l.log.Debug("relay ws dialing", "url", l.relayURL, "node", l.node)
	ws, _, err := websocket.Dial(ctx, l.relayURL, nil)
	if err != nil {
		l.log.Error("relay ws dial failed", "url", l.relayURL, "node", l.node, "cause", err)
		return fmt.Errorf("dial relay websocket: %w", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "listener stopping")
	l.log.Debug("relay ws dialed", "node", l.node)
	if err := sendControl(ctx, ws, Frame{Type: Register, Node: l.node, Credential: l.credential}); err != nil {
		l.log.Error("relay register send failed", "node", l.node, "cause", err)
		return err
	}
	l.log.Debug("relay register sent", "node", l.node)
	response, err := recvControl(ctx, ws)
	if err != nil {
		var ce *ControlError
		if errors.As(err, &ce) {
			l.log.Error("relay registration rejected", "node", l.node, "code", ce.Code)
		} else {
			l.log.Error("relay registration failed", "node", l.node, "cause", err)
		}
		return err
	}
	if response.Type != Registered {
		l.log.Error("relay registration returned unexpected frame", "node", l.node, "type", response.Type)
		return fmt.Errorf("relay register: expected %q, got %q", Registered, response.Type)
	}
	if response.Account != "" {
		l.account = response.Account
	}
	if l.account == "" {
		return errors.New("relay registered response missing account")
	}
	l.log.Info("registered to relay", "node", l.node, "account", l.account, "relay_url", l.relayURL)
	raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	session, err := yamux.Server(raw, relayYamuxConfig())
	if err != nil {
		return fmt.Errorf("create relay yamux server: %w", err)
	}
	stop := closeSessionOnContext(ctx, session)
	defer stop()
	var sessions sync.WaitGroup
	for {
		stream, err := session.Accept()
		if err != nil {
			if ctx.Err() != nil {
				sessions.Wait()
				return nil
			}
			sessions.Wait()
			return fmt.Errorf("accept relay session: %w", err)
		}
		l.log.Info("relay session accepted", "node", l.node, "account", l.account)
		sessions.Add(1)
		go func() {
			defer sessions.Done()
			l.serveSession(ctx, stream)
		}()
	}
}

// RunWithReconnect keeps the executor registered through transient relay
// failures. Context cancellation stops both the current tunnel and backoff.
func (l *Listener) RunWithReconnect(ctx context.Context) {
	// 直连会话归 Listener 生命周期：ctx 取消后等它们全部关闭才返回。
	defer l.p2pWG.Wait()
	backoff := listenerInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := l.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		l.log.Warn("relay listener disconnected", "node", l.node, "cause", err)
		l.log.Info("reconnecting to relay", "node", l.node, "backoff", backoff.String())
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if backoff < listenerMaxBackoff/2 {
			backoff *= 2
		} else {
			backoff = listenerMaxBackoff
		}
	}
}

func (l *Listener) serveSession(ctx context.Context, stream net.Conn) {
	defer stream.Close()
	l.log.Debug("e2e handshake begin", "account", l.account, "node", l.node, "role", "responder")
	secure, err := SecureServer(ctx, stream, l.token, l.account, l.node)
	if err != nil {
		l.log.Error("relay session e2e handshake failed", "node", l.node, "account", l.account, "cause", err)
		return
	}
	l.log.Info("e2e established", "node", l.node, "account", l.account, "role", "responder")
	reader := bufio.NewReader(secure)
	// 会话模式三分（两魔数等长，一次 Peek）：打洞信令 / preview raw / app。
	prefix, err := reader.Peek(len(previewRawMagic))
	if err != nil {
		l.log.Warn("relay session mode read failed", "node", l.node, "account", l.account, "cause", err)
		return
	}
	if bytes.Equal(prefix, []byte(p2p.SignalMagic)) {
		l.serveSignalSession(ctx, secure, reader)
		return
	}
	if bytes.Equal(prefix, []byte(previewRawMagic)) {
		l.serveRawSession(ctx, secure, reader)
		return
	}
	l.serveApp(ctx, &bufferedRelayConn{Conn: secure, reader: reader}, "relay")
}

// serveApp 在一条已过 E2E 的会话上跑 app-yamux 并交给同一个 HTTP handler。relay
// 会话与打洞直连会话共用（直连上不再做魔数判别，F1 W13）。
func (l *Listener) serveApp(ctx context.Context, secure net.Conn, via string) {
	appMux, err := yamux.Server(secure, relayYamuxConfig())
	if err != nil {
		l.log.Error("relay app yamux setup failed", "node", l.node, "cause", err)
		return
	}
	app := &appListener{session: appMux}
	serveDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = app.Close()
		case <-serveDone:
		}
	}()
	_ = http.Serve(app, l.handler)
	close(serveDone)
	_ = app.Close()
	l.log.Info("relay session closed", "node", l.node, "account", l.account, "via", via)
}

type bufferedRelayConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedRelayConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (l *Listener) serveRawSession(ctx context.Context, secure net.Conn, reader io.Reader) {
	network, addr, err := readPreviewRawRequest(reader)
	if err != nil {
		l.log.Warn("relay raw request read failed", "node", l.node, "account", l.account, "cause", err)
		_ = writePreviewRawResponse(secure, err)
		return
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		err = fmt.Errorf("不支持 raw network=%q", network)
		_ = writePreviewRawResponse(secure, err)
		l.log.Warn("relay raw network rejected", "node", l.node, "network", network, "addr", addr, "cause", err)
		return
	}
	l.log.Info("relay raw upstream dialing", "node", l.node, "network", network, "addr", addr)
	upstream, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		_ = writePreviewRawResponse(secure, err)
		l.log.Warn("relay raw upstream dial failed", "node", l.node, "network", network, "addr", addr, "cause", err)
		return
	}
	defer upstream.Close()
	if err := writePreviewRawResponse(secure, nil); err != nil {
		l.log.Warn("relay raw response write failed", "node", l.node, "network", network, "addr", addr, "cause", err)
		return
	}
	l.log.Info("relay raw upstream connected", "node", l.node, "network", network, "addr", addr)
	bridgeRelayRaw(secure, upstream)
	l.log.Info("relay raw session closed", "node", l.node, "network", network, "addr", addr)
}

// BridgePreviewRaw copies both directions until either side closes.
func BridgePreviewRaw(a, b net.Conn) {
	bridgeRelayRaw(a, b)
}

func bridgeRelayRaw(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

func closeSessionOnContext(ctx context.Context, session *yamux.Session) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

// appListener adapts yamux Accept into net.Listener so http.Serve can consume
// app streams without any agentd routing changes.
type appListener struct {
	session *yamux.Session
}

// Accept 把会话结束的错误包成非 net.Error：http.Serve 对 Temporary 的 net.Error
// 每秒重试、永不返回。打洞直连死亡时 yamux 的 shutdownErr 是底层 ICE 连接的
// 「i/o timeout」（Temporary=true），不包一层会让 serveApp 卡死、Listener 无法退出。
func (l *appListener) Accept() (net.Conn, error) {
	conn, err := l.session.Accept()
	if err != nil {
		return nil, fmt.Errorf("relay app 会话已结束: %w", err)
	}
	return conn, nil
}
func (l *appListener) Close() error   { return l.session.Close() }
func (l *appListener) Addr() net.Addr { return relayAddr("relay-app") }

type relayAddr string

func (a relayAddr) Network() string { return string(a) }
func (a relayAddr) String() string  { return string(a) }
