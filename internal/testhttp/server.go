// server.go —— agentd 测试 HTTP fixture 的 TCP 连接收口与 loopback 拨号重试。
//
// 职责：包装 httptest 的服务端 Accept 与测试 client 的 DialContext，使临时端口
// 在 Darwin 上更快释放，并把极窄的 EADDRNOTAVAIL 重试限制在测试 fixture。
// 边界：本包只供测试导入；不修改生产 client.New、不重试 Client.Do；非 loopback
// 地址与非指定错误都只尝试一次。
package testhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// DialContext is the context-aware dial signature wrapped by the fixture.
type DialContext func(context.Context, string, string) (net.Conn, error)

const (
	// MaxDialAttempts bounds retries for a single loopback address-allocation failure.
	MaxDialAttempts = 4
	dialRetryDelay  = 10 * time.Millisecond
)

var setLinger = func(conn *net.TCPConn) error { return conn.SetLinger(0) }

var configuredTransports = struct {
	sync.Mutex
	set map[*http.Transport]struct{}
}{set: make(map[*http.Transport]struct{})}

// NewServer starts a linger-enabled httptest server, configures the default client and
// registers cleanup that closes the server before tracked idle transports.
func NewServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	oldDefault := http.DefaultClient.Transport
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener = &lingerListener{Listener: ts.Listener}
	ts.Start()
	ConfigureClient(http.DefaultClient)
	ConfigureClient(ts.Client())
	t.Cleanup(func() {
		ts.Close()
		CloseIdleConnections()
		http.DefaultClient.Transport = oldDefault
	})
	return ts
}

// NewUnstartedServer returns a linger-enabled server whose caller may replace Listener or
// modify Config before Start. The caller must call ConfigureClient(ts.Client()) after Start
// because httptest creates its client inside Start.
func NewUnstartedServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	oldDefault := http.DefaultClient.Transport
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener = &lingerListener{Listener: ts.Listener}
	ConfigureClient(http.DefaultClient)
	t.Cleanup(func() {
		ts.Close()
		CloseIdleConnections()
		http.DefaultClient.Transport = oldDefault
	})
	return ts
}

// ConfigureClient replaces only a *http.Transport's dial hooks. A nil Transport is cloned
// from http.DefaultTransport so the process-global default is never mutated; a non-*http.Transport
// is intentionally left unchanged because it has no safe TCP hook.
func ConfigureClient(client *http.Client) {
	if client == nil {
		return
	}
	tr, ok := client.Transport.(*http.Transport)
	if client.Transport == nil {
		defaultTransport, defaultOK := http.DefaultTransport.(*http.Transport)
		if !defaultOK {
			return
		}
		tr = defaultTransport.Clone()
		client.Transport = tr
		ok = true
	}
	if !ok {
		return
	}
	if defaultTransport, defaultOK := http.DefaultTransport.(*http.Transport); defaultOK && tr == defaultTransport {
		tr = defaultTransport.Clone()
		client.Transport = tr
	}
	configuredTransports.Lock()
	if _, exists := configuredTransports.set[tr]; exists {
		configuredTransports.Unlock()
		return
	}
	configuredTransports.set[tr] = struct{}{}
	configuredTransports.Unlock()

	baseContext := tr.DialContext
	baseDial := tr.Dial
	if baseContext == nil {
		if baseDial != nil {
			baseContext = func(_ context.Context, network, addr string) (net.Conn, error) {
				return baseDial(network, addr)
			}
		} else {
			dialer := &net.Dialer{}
			baseContext = dialer.DialContext
		}
	}
	tr.Dial = nil
	tr.DialContext = RetryDialContext(DialContext(baseContext))
}

// RetryDialContext retries only the two Darwin loopback address-allocation errors and
// only up to MaxDialAttempts. A successful loopback connection gets client-side linger.
func RetryDialContext(base DialContext) DialContext {
	if base == nil {
		dialer := &net.Dialer{}
		base = dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var last error
		for attempt := 0; attempt < MaxDialAttempts; attempt++ {
			conn, err := base(ctx, network, addr)
			if err == nil {
				return prepareClientConn(conn, addr)
			}
			last = err
			if !isLoopbackAddr(addr) || !retryableAddressError(err) || attempt+1 == MaxDialAttempts {
				return nil, err
			}
			timer := time.NewTimer(dialRetryDelay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		return nil, last
	}
}

// CloseIdleConnections closes tracked idle transports and the standard default transport.
// It is intentionally separate from request cancellation.
func CloseIdleConnections() {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	configuredTransports.Lock()
	all := make([]*http.Transport, 0, len(configuredTransports.set))
	for tr := range configuredTransports.set {
		all = append(all, tr)
	}
	configuredTransports.Unlock()
	for _, tr := range all {
		tr.CloseIdleConnections()
	}
}

type lingerListener struct{ net.Listener }

func (l *lingerListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("testhttp: httptest listener returned non-TCP connection")
	}
	if err := setLinger(tcp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tcp, nil
}

func prepareClientConn(conn net.Conn, addr string) (net.Conn, error) {
	if !isLoopbackAddr(addr) {
		return conn, nil
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("testhttp: loopback dial returned non-TCP connection")
	}
	if err := setLinger(tcp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tcp, nil
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func retryableAddressError(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || strings.Contains(err.Error(), "can't assign requested address")
}
