package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// TestEnsureOnClosedDialerFails：已关闭的 Dialer 必须拒绝建隧道。
//
// why：预热循环会对每台机器反复调 Ensure，池 Close 之后它可能还在跑最后一轮。
// 这一条锁死「关了就是关了」，不会因为预热而复活一条隧道。
func TestEnsureOnClosedDialerFails(t *testing.T) {
	d := NewDialer("wss://example.invalid/relay", "cred", "node", "token", "", slog.Default())
	_ = d.Close()
	if err := d.Ensure(context.Background()); err == nil {
		t.Fatal("已关闭的 Dialer 不该还能建隧道")
	}
}

func TestDialerHTTPRoundTripThroughFakeRelay(t *testing.T) {
	relayURL, cleanup := startFakeRelay(t, "tok", "acc1", "devbox")
	defer cleanup()
	d := NewDialer(relayURL, "cred", "devbox", "tok", "acc1", slog.Default())
	defer d.Close()
	hc := &http.Client{Transport: d.Transport()}
	resp, err := hc.Get("http://relay-devbox/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "pong" {
		t.Fatalf("got %q", body)
	}
}

func startFakeRelay(t *testing.T, token, account, node string) (string, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		f, err := recvControl(ctx, ws)
		if err != nil || f.Type != Connect || f.Node != node || f.Credential != "cred" {
			_ = ws.Close(websocket.StatusPolicyViolation, "bad connect")
			return
		}
		if err := sendControl(ctx, ws, Frame{Type: ConnectOK, Account: account}); err != nil {
			return
		}
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		// 对齐真实结构：coordinator 的这条 WSS 就是「一个 session」，E2E 直接在
		// raw 上，app-yamux 在 E2E 密文信道内。假 relay 在此扮演 relay+executor
		// 合体、终结 E2E（真 relay 不解 E2E，但 dialer 单测只需一个行为等价的对端）。
		secure, err := SecureServer(ctx, raw, token, account, node)
		if err != nil {
			return
		}
		appMux, err := yamux.Server(secure, relayYamuxConfig())
		if err != nil {
			return
		}
		for {
			appStream, err := appMux.Accept()
			if err != nil {
				return
			}
			go func(s net.Conn) {
				_ = http.Serve(newSingleConnListener(s), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/ping" {
						_, _ = io.WriteString(w, "pong")
						return
					}
					http.NotFound(w, r)
				}))
			}(appStream)
		}
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

type singleConnListener struct {
	conn   net.Conn
	done   bool
	closed chan struct{}
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.done {
		<-l.closed
		return nil, net.ErrClosed
	}
	l.done = true
	return l.conn, nil
}
func (l *singleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *singleConnListener) Addr() net.Addr { return fakeAddr("relay-stream") }

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }
