// 职责：把 coordinator 的 HTTP/WS 拨号导向 relay 隧道内的 app 流；边界：不改
// client 上层逻辑，只提供 Transport。
package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// Dialer is a coordinator-side relay transport. One lazy physical tunnel is
// shared by all HTTP/WS requests; each DialContext call opens an app-mux stream.
type Dialer struct {
	relayURL   string
	credential string
	node       string
	token      string
	account    string
	log        *slog.Logger

	mu      sync.Mutex
	session *yamux.Session
	raw     net.Conn
	closed  bool
}

// NewDialer constructs a lazy coordinator relay dialer. account may be empty:
// the authenticated CONNECT_OK response supplies it before E2E setup.
func NewDialer(relayURL, credential, node, token, account string, log *slog.Logger) *Dialer {
	if log == nil {
		log = slog.Default()
	}
	return &Dialer{
		relayURL: relayURL, credential: credential, node: node,
		token: token, account: account, log: log,
	}
}

func (d *Dialer) ensureTunnel(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("relay dialer is closed")
	}
	if d.session != nil && !d.session.IsClosed() {
		return nil
	}
	if d.session != nil {
		_ = d.session.Close()
		d.session = nil
		d.raw = nil
	}
	d.log.Debug("relay ws dialing", "url", d.relayURL, "node", d.node)
	ws, _, err := websocket.Dial(ctx, d.relayURL, nil)
	if err != nil {
		d.log.Error("relay ws dial failed", "url", d.relayURL, "node", d.node, "cause", err)
		return fmt.Errorf("dial relay websocket: %w", err)
	}
	d.log.Debug("relay ws dialed", "node", d.node)
	if err := sendControl(ctx, ws, Frame{Type: Connect, Node: d.node, Credential: d.credential}); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "control exchange failed")
		d.log.Error("relay connect send failed", "node", d.node, "cause", err)
		return err
	}
	d.log.Debug("relay connect sent", "node", d.node)
	response, err := recvControl(ctx, ws)
	if err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "connect rejected")
		var ce *ControlError
		if errors.As(err, &ce) {
			d.log.Error("relay connect rejected", "node", d.node, "code", ce.Code)
		}
		return err
	}
	if response.Type != ConnectOK {
		_ = ws.Close(websocket.StatusPolicyViolation, "unexpected control response")
		return fmt.Errorf("relay connect: expected %q, got %q", ConnectOK, response.Type)
	}
	if response.Account == "" && d.account == "" {
		_ = ws.Close(websocket.StatusPolicyViolation, "missing account")
		return errors.New("relay connect_ok missing account")
	}
	if response.Account != "" {
		d.account = response.Account
	}
	d.log.Debug("relay connect_ok received", "node", d.node, "account", d.account)
	raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	// coordinator 的这条 WSS 连接与 executor 的一个 session 流由 relay 1:1 撮合，
	// 因此它本身就是「一个 session」——这里绝不能再套 session-mux yamux：relay 只做
	// 纯字节拷贝、不会解掉这层帧，套了会把 yamux 帧头漏进 executor 的 SecureServer
	// （表现为 salt 长度被读成 0x00010001=65537）。顺序必须与 executor 的
	// listener.serveSession 一致：E2E 先，app-yamux 在 E2E 密文信道内。
	d.log.Debug("e2e handshake begin", "account", d.account, "node", d.node, "role", "initiator")
	secure, err := SecureClient(ctx, raw, d.token, d.account, d.node)
	if err != nil {
		_ = raw.Close()
		d.log.Error("relay e2e handshake failed", "node", d.node, "account", d.account, "cause", err)
		return fmt.Errorf("secure relay session: %w", err)
	}
	d.log.Info("e2e established", "node", d.node, "account", d.account, "role", "initiator")
	session, err := yamux.Client(secure, relayYamuxConfig())
	if err != nil {
		_ = secure.Close()
		_ = raw.Close()
		d.log.Error("relay app yamux setup failed", "node", d.node, "cause", err)
		return fmt.Errorf("create relay app yamux client: %w", err)
	}
	d.raw = raw
	d.session = session
	d.log.Info("relay tunnel established", "node", d.node, "account", d.account)
	return nil
}

// DialContext opens one app-yamux stream inside the E2E-secured tunnel. addr is
// an http.Client placeholder: routing is determined by the authenticated relay
// tunnel, not by addr. E2E and app-yamux are set up once per tunnel in
// ensureTunnel; here we only open a fresh app stream per HTTP/WS connection.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := d.ensureTunnel(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	session := d.session
	d.mu.Unlock()
	conn, err := session.Open()
	if err != nil {
		d.log.Warn("relay app stream open failed", "node", d.node, "cause", err)
		d.mu.Lock()
		if d.session == session {
			_ = session.Close()
			d.session = nil
			d.raw = nil
		}
		d.mu.Unlock()
		return nil, err
	}
	return conn, nil
}

// Ensure 主动建立（或复用）relay 隧道，不发任何业务请求。
//
// 参数：
//   - ctx: 控制本次建隧道的时限；超时/取消原样返回
//
// 返回：
//   - nil 表示隧道已就绪（本次新建或复用现有）；Dialer 已 Close 时恒返回错误
//
// 为什么需要它：协调者侧的预热要把「隧道通没通」与「对端 agentd 活没活」分成
// 两个判据。借一次业务请求（如 GET /api/status）代劳会把两者搅在一起——隧道
// 建好但对端没起时，那次请求失败，预热无从判断该不该重试。
// 不另加日志：内部 ensureTunnel 已有完整的拨号/CONNECT/拒绝日志链。
func (d *Dialer) Ensure(ctx context.Context) error { return d.ensureTunnel(ctx) }

// Transport returns an HTTP transport whose requests all use the relay app
// streams. Proxy is explicitly nil so the relay URL itself is not redirected
// through an unrelated HTTP proxy.
func (d *Dialer) Transport() *http.Transport {
	return &http.Transport{DialContext: d.DialContext, Proxy: nil}
}

// Close closes the current relay tunnel and prevents future lazy reconnects.
func (d *Dialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	var err error
	if d.session != nil {
		err = d.session.Close()
		d.session = nil
	}
	if d.raw != nil {
		if closeErr := d.raw.Close(); err == nil {
			err = closeErr
		}
		d.raw = nil
	}
	return err
}
