// 职责：把 coordinator 的 HTTP/WS 拨号导向 relay 隧道内的 app 流；边界：不改
// client 上层逻辑，只提供 Transport。
package relay

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
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

	mu       sync.Mutex
	session  *yamux.Session
	raw      net.Conn
	rawConns map[net.Conn]struct{}
	closed   bool
}

const (
	previewRawMagic   = "handoff-preview-raw-v1"
	previewRawMaxPart = 4096
	previewRawOK      = 0
	previewRawError   = 1
)

// NewDialer constructs a lazy coordinator relay dialer. account may be empty:
// the authenticated CONNECT_OK response supplies it before E2E setup.
func NewDialer(relayURL, credential, node, token, account string, log *slog.Logger) *Dialer {
	if log == nil {
		log = slog.Default()
	}
	return &Dialer{
		relayURL: relayURL, credential: credential, node: node,
		token: token, account: account, log: log, rawConns: make(map[net.Conn]struct{}),
	}
}

// RawDialContext opens a target-side raw TCP stream for preview SOCKS.
// Unlike DialContext, which returns an HTTP app-yamux stream, this method
// opens a dedicated authenticated relay stream and asks the executor-side
// listener to resolve and dial network/addr there. The caller owns the
// returned connection; Close also reclaims any outstanding raw streams.
func (d *Dialer) RawDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network == "" || addr == "" {
		return nil, errors.New("relay raw dial 缺少 network 或 addr")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("relay dialer is closed")
	}
	account := d.account
	d.mu.Unlock()

	d.log.Info("relay raw stream dialing", "node", d.node, "network", network, "addr", addr)
	ws, _, err := websocket.Dial(ctx, d.relayURL, nil)
	if err != nil {
		d.log.Warn("relay raw websocket dial failed", "node", d.node, "cause", err)
		return nil, fmt.Errorf("dial relay raw websocket: %w", err)
	}
	// Same net.Dial contract as Client.DialPreviewRaw: ctx covers handshake
	// only. SOCKS cancel()s the dial ctx immediately after Dial returns.
	raw := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	closeRaw := func() { _ = raw.Close() }
	if err := sendControl(ctx, ws, Frame{Type: Connect, Node: d.node, Credential: d.credential}); err != nil {
		closeRaw()
		return nil, fmt.Errorf("relay raw connect send: %w", err)
	}
	response, err := recvControl(ctx, ws)
	if err != nil {
		closeRaw()
		return nil, fmt.Errorf("relay raw connect response: %w", err)
	}
	if response.Type != ConnectOK {
		closeRaw()
		return nil, fmt.Errorf("relay raw connect: expected %q, got %q", ConnectOK, response.Type)
	}
	if response.Account != "" {
		account = response.Account
		d.mu.Lock()
		d.account = account
		d.mu.Unlock()
	}
	if account == "" {
		closeRaw()
		return nil, errors.New("relay raw connect_ok missing account")
	}
	secure, err := SecureClient(ctx, raw, d.token, account, d.node)
	if err != nil {
		closeRaw()
		return nil, fmt.Errorf("secure relay raw stream: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = secure.Close()
		}
	}()
	if err := writePreviewRawRequest(secure, network, addr); err != nil {
		return nil, fmt.Errorf("relay raw request: %w", err)
	}
	if err := readPreviewRawResponse(secure); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("relay dialer is closed")
	}
	d.rawConns[secure] = struct{}{}
	d.mu.Unlock()
	keep = true
	return &trackedRawConn{Conn: secure, release: d.releaseRawConn}, nil
}

type trackedRawConn struct {
	net.Conn
	once    sync.Once
	release func(net.Conn)
}

func (c *trackedRawConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.release(c.Conn) })
	return err
}

func (d *Dialer) releaseRawConn(conn net.Conn) {
	d.mu.Lock()
	delete(d.rawConns, conn)
	d.mu.Unlock()
}

// WritePreviewRawRequest frames one owner/coordinator preview-raw request.
func WritePreviewRawRequest(w io.Writer, network, addr string) error {
	return writePreviewRawRequest(w, network, addr)
}

// ReadPreviewRawRequest parses one preview-raw request after the stream is authenticated.
func ReadPreviewRawRequest(r io.Reader) (network, addr string, err error) {
	return readPreviewRawRequest(r)
}

// WritePreviewRawResponse writes the one-byte status plus optional error text.
func WritePreviewRawResponse(w io.Writer, err error) error {
	return writePreviewRawResponse(w, err)
}

// ReadPreviewRawResponse consumes the preview-raw handshake reply.
func ReadPreviewRawResponse(r io.Reader) error {
	return readPreviewRawResponse(r)
}

func writePreviewRawRequest(w io.Writer, network, addr string) error {
	if len(network) > previewRawMaxPart || len(addr) > previewRawMaxPart {
		return fmt.Errorf("relay raw request 字段过长 network=%d addr=%d", len(network), len(addr))
	}
	if _, err := io.WriteString(w, previewRawMagic); err != nil {
		return err
	}
	for _, part := range []string{network, addr} {
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(part)))
		if _, err := w.Write(size[:]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, part); err != nil {
			return err
		}
	}
	return nil
}

func readPreviewRawRequest(r io.Reader) (network, addr string, err error) {
	magic := make([]byte, len(previewRawMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return "", "", err
	}
	if string(magic) != previewRawMagic {
		return "", "", fmt.Errorf("relay raw magic 非法: %q", magic)
	}
	parts := make([]string, 2)
	for i := range parts {
		var size [2]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return "", "", err
		}
		length := int(binary.BigEndian.Uint16(size[:]))
		if length == 0 || length > previewRawMaxPart {
			return "", "", fmt.Errorf("relay raw request 字段长度非法: %d", length)
		}
		part := make([]byte, length)
		if _, err := io.ReadFull(r, part); err != nil {
			return "", "", err
		}
		parts[i] = string(part)
	}
	return parts[0], parts[1], nil
}

func writePreviewRawResponse(w io.Writer, err error) error {
	if err == nil {
		_, writeErr := w.Write([]byte{previewRawOK})
		return writeErr
	}
	message := err.Error()
	if len(message) > previewRawMaxPart {
		message = message[:previewRawMaxPart]
	}
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(message)))
	if _, writeErr := w.Write([]byte{previewRawError}); writeErr != nil {
		return writeErr
	}
	if _, writeErr := w.Write(size[:]); writeErr != nil {
		return writeErr
	}
	_, writeErr := io.WriteString(w, message)
	return writeErr
}

func readPreviewRawResponse(r io.Reader) error {
	var status [1]byte
	if _, err := io.ReadFull(r, status[:]); err != nil {
		return fmt.Errorf("读取 relay raw 响应: %w", err)
	}
	if status[0] == previewRawOK {
		return nil
	}
	if status[0] != previewRawError {
		return fmt.Errorf("relay raw 响应状态非法: %d", status[0])
	}
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return fmt.Errorf("读取 relay raw 错误长度: %w", err)
	}
	length := int(binary.BigEndian.Uint16(size[:]))
	if length > previewRawMaxPart {
		return fmt.Errorf("relay raw 错误长度非法: %d", length)
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(r, message); err != nil {
		return fmt.Errorf("读取 relay raw 错误: %w", err)
	}
	return fmt.Errorf("owner raw dial 失败: %s", strings.TrimSpace(string(message)))
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
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	session := d.session
	raw := d.raw
	rawConns := make([]net.Conn, 0, len(d.rawConns))
	for conn := range d.rawConns {
		rawConns = append(rawConns, conn)
	}
	d.session = nil
	d.raw = nil
	d.rawConns = make(map[net.Conn]struct{})
	d.mu.Unlock()

	var closeErr error
	if session != nil {
		closeErr = session.Close()
	}
	if raw != nil {
		if err := raw.Close(); closeErr == nil {
			closeErr = err
		}
	}
	for _, conn := range rawConns {
		if err := conn.Close(); closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}
