package relay

import (
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
		secure, err := SecureServer(ctx, raw, token, account, node)
		if err != nil {
			return
		}
		session, err := yamux.Server(secure, nil)
		if err != nil {
			return
		}
		for {
			stream, err := session.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = http.Serve(&singleConnListener{conn: stream}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/ping" {
						_, _ = io.WriteString(w, "pong")
						return
					}
					http.NotFound(w, r)
				}))
			}()
		}
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

type singleConnListener struct {
	conn net.Conn
	done bool
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.done {
		return nil, io.EOF
	}
	l.done = true
	return l.conn, nil
}
func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return fakeAddr("relay-stream") }

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }
