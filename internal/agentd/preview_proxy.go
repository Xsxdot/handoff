// 本文件实现 preview 的本机 SOCKS5 投影和 PAC 文本生成。
//
// 职责：
//   - 在 127.0.0.1:0 提供受 session allowlist 保护的 CONNECT 代理
//   - 让 loopback 与 via 中的 IP/CIDR/域名通过同一条 raw dial 路径
//   - 生成不使用 DIRECT 的 PAC，避免 Chromium 绕过 SOCKS 安全检查
//
// 边界：不占 owner port、不提供 HTTP/CONNECT owner 端点、不负责 Chromium/OS 进程。
package agentd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// PreviewRawDial is the target-scoped upstream connection seam.
type PreviewRawDial func(context.Context, string, string) (net.Conn, error)

const previewProxyUsername = "handoff-preview"

// PreviewAllowlist is the normalized session-local network policy.
// loopback is always true; networks and domains contain only explicitly added targets.
type PreviewAllowlist struct {
	loopback bool
	networks []*net.IPNet
	domains  map[string]struct{}
}

// ParsePreviewAllowlist accepts only IP, CIDR, or normalized domain entries.
// Empty input still permits the three loopback spellings and localhost.
func ParsePreviewAllowlist(via []string) (PreviewAllowlist, error) {
	if err := validatePreviewViaSyntax(via); err != nil {
		return PreviewAllowlist{}, err
	}
	allow := PreviewAllowlist{loopback: true, domains: make(map[string]struct{})}
	for _, raw := range via {
		value := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if ip := net.ParseIP(value); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			allow.networks = append(allow.networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if _, network, err := net.ParseCIDR(value); err == nil {
			allow.networks = append(allow.networks, network)
			continue
		}
		allow.domains[value] = struct{}{}
	}
	return allow, nil
}

// Allows reports whether a hostname or IP may be connected by this session.
// Hostnames are matched exactly; no wildcard or implicit subdomain expansion is allowed.
func (a PreviewAllowlist) Allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "localhost" {
		return a.loopback
	}
	if ip := net.ParseIP(host); ip != nil {
		if a.loopback && ip.IsLoopback() {
			return true
		}
		for _, network := range a.networks {
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}
	_, ok := a.domains[host]
	return ok
}

// RenderPreviewPAC renders a PAC that sends every request through the session SOCKS.
// Unauthorized destinations are rejected by the proxy, never converted to DIRECT.
func RenderPreviewPAC(socksURL string, allowlist PreviewAllowlist) ([]byte, error) {
	u, err := url.Parse(socksURL)
	if err != nil || u.Host == "" || (u.Scheme != "socks5" && u.Scheme != "socks5h") {
		return nil, fmt.Errorf("SOCKS URL 非法: %q", socksURL)
	}
	entries := make([]string, 0, len(allowlist.domains)+len(allowlist.networks))
	for domain := range allowlist.domains {
		entries = append(entries, domain)
	}
	// Keep the explicit policy visible in the generated asset for diagnostics; actual
	// authorization remains in SOCKS because PAC JavaScript cannot safely resolve IPs.
	sortStrings(entries)
	proxy := u.Host
	if u.User != nil {
		proxy = u.User.String() + "@" + u.Host
	}
	return []byte(fmt.Sprintf(`function FindProxyForURL(url, host) {
  // preview allowlist: %s
  return "SOCKS5 %s";
}
`, strings.Join(entries, ","), proxy)), nil
}

// PreviewProxy is a lifecycle-bound local SOCKS5 listener.
type PreviewProxy struct {
	sessionID string
	listener  net.Listener
	allowlist PreviewAllowlist
	dial      PreviewRawDial
	nonce     []byte
	log       *slog.Logger
	closeOnce sync.Once
}

// NewPreviewProxy binds a random loopback port and creates a private capability nonce.
// The nonce is used as the SOCKS RFC1929 password and is kept for the launcher boundary;
// it is never logged.
func NewPreviewProxy(ctx context.Context, sessionID string, via []string, dial PreviewRawDial, log *slog.Logger) (*PreviewProxy, error) {
	if sessionID == "" {
		return nil, errors.New("preview session ID 不能为空")
	}
	if dial == nil {
		return nil, errors.New("preview raw dial 未配置")
	}
	allow, err := ParsePreviewAllowlist(via)
	if err != nil {
		return nil, fmt.Errorf("解析 preview allowlist: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("绑定 preview SOCKS 回环端口: %w", err)
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("生成 preview proxy nonce: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	proxy := &PreviewProxy{sessionID: sessionID, listener: listener, allowlist: allow, dial: dial, nonce: nonce, log: log}
	_ = ctx
	proxy.log.Info("preview SOCKS 代理创建成功", "operation", "proxy_create", "session", sessionID, "addr", listener.Addr().String())
	return proxy, nil
}

// Addr returns the loopback listener address.
func (p *PreviewProxy) Addr() net.Addr { return p.listener.Addr() }

// Nonce returns a copy of the launcher capability nonce.
func (p *PreviewProxy) Nonce() []byte { return append([]byte(nil), p.nonce...) }

// Serve accepts SOCKS5 requests until ctx cancellation or Close.
func (p *PreviewProxy) Serve(ctx context.Context) error {
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = p.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("接受 preview SOCKS 连接: %w", err)
		}
		go p.handle(ctx, conn)
	}
}

// Close closes the listener; active connections finish through their request context.
func (p *PreviewProxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		err = p.listener.Close()
		p.log.Info("preview SOCKS 代理已关闭", "operation", "proxy_close", "session", p.sessionID)
	})
	return err
}

func (p *PreviewProxy) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if err := p.socksHandshake(conn); err != nil {
		p.log.Warn("preview SOCKS 握手失败", "operation", "socks_handshake", "session", p.sessionID, "cause", err)
		return
	}
	host, port, err := readSOCKSRequest(conn)
	if err != nil {
		p.log.Warn("preview SOCKS 请求解析失败", "operation", "socks_request", "session", p.sessionID, "cause", err)
		return
	}
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	if !p.allowlist.Allows(host) {
		_ = writeSOCKSReply(conn, 0x02)
		p.log.Warn("preview SOCKS 目标被 allowlist 拒绝", "operation", "socks_connect", "session", p.sessionID, "addr", addr)
		return
	}
	p.log.Info("preview raw upstream 拨号开始", "operation", "socks_connect", "session", p.sessionID, "addr", addr)
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	upstream, err := p.dial(dialCtx, "tcp", addr)
	cancel()
	if err != nil {
		_ = writeSOCKSReply(conn, 0x05)
		p.log.Warn("preview raw upstream 拨号失败", "operation", "socks_connect", "session", p.sessionID, "addr", addr, "cause", err)
		return
	}
	defer upstream.Close()
	if err := writeSOCKSReply(conn, 0x00); err != nil {
		p.log.Warn("preview SOCKS 成功响应失败", "operation", "socks_connect", "session", p.sessionID, "addr", addr, "cause", err)
		return
	}
	p.log.Info("preview raw upstream 拨号成功", "operation", "socks_connect", "session", p.sessionID, "addr", addr)
	pipeSOCKS(conn, upstream)
}

func (p *PreviewProxy) socksHandshake(conn net.Conn) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != 5 {
		return fmt.Errorf("SOCKS version=%d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	for _, method := range methods {
		if method == 2 {
			if _, err := conn.Write([]byte{5, 2}); err != nil {
				return err
			}
			return p.socksUserPass(conn)
		}
	}
	_, err := conn.Write([]byte{5, 0xff})
	if err != nil {
		return err
	}
	return errors.New("不支持 SOCKS 认证方法：preview 必须使用 nonce 认证")
}

func (p *PreviewProxy) socksUserPass(conn net.Conn) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != 1 {
		return fmt.Errorf("SOCKS 用户名密码版本=%d", header[0])
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, username); err != nil {
		return err
	}
	var passwordLength [1]byte
	if _, err := io.ReadFull(conn, passwordLength[:]); err != nil {
		return err
	}
	password := make([]byte, int(passwordLength[0]))
	if _, err := io.ReadFull(conn, password); err != nil {
		return err
	}
	expectedUser := []byte(previewProxyUsername)
	expectedPassword := []byte(hex.EncodeToString(p.nonce))
	valid := len(username) == len(expectedUser) && subtle.ConstantTimeCompare(username, expectedUser) == 1 &&
		len(password) == len(expectedPassword) && subtle.ConstantTimeCompare(password, expectedPassword) == 1
	if !valid {
		_, _ = conn.Write([]byte{1, 1})
		return errors.New("preview SOCKS nonce 认证失败")
	}
	_, err := conn.Write([]byte{1, 0})
	return err
}

func readSOCKSRequest(conn net.Conn) (string, uint16, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return "", 0, err
	}
	if header[0] != 5 || header[1] != 1 {
		return "", 0, fmt.Errorf("SOCKS request version/cmd=%d/%d", header[0], header[1])
	}
	var host string
	switch header[3] {
	case 1:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return "", 0, err
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	case 4:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	default:
		return "", 0, fmt.Errorf("未知 SOCKS address type=%d", header[3])
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(port[:]), nil
}

func writeSOCKSReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func pipeSOCKS(client, upstream net.Conn) {
	result := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); result <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); result <- struct{}{} }()
	<-result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
