package targetclient

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/coder/websocket"
)

func TestPoolDialContextUsesRegisteredTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	p := NewPool(confOf(map[string]config.Target{
		"owner": {Addr: listener.Addr().String(), Token: "tok"},
	}), nil)
	defer p.Close()
	conn, err := p.DialContext(context.Background(), "owner", "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	select {
	case serverConn := <-accepted:
		serverConn.Close()
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
	if _, err := io.WriteString(conn, "probe"); err != nil {
		t.Fatalf("write through raw dial: %v", err)
	}
}

func TestPoolDialContextRejectsDirectNonLoopbackDestination(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	p := NewPool(confOf(map[string]config.Target{
		"remote": {Addr: listener.Addr().String(), Token: "tok"},
	}), nil)
	defer p.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	if conn, err := p.DialContext(context.Background(), "remote", "tcp", net.JoinHostPort("0.0.0.0", port)); err == nil {
		conn.Close()
		t.Fatal("direct remote raw dial must fail closed for non-loopback destination")
	}
	select {
	case conn := <-accepted:
		conn.Close()
		t.Fatal("direct remote raw dial must not connect to coordinator-side listener")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPoolDialContextRelayPassesDestinationToRawDial(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	destinations := make(chan string, 1)
	relayURL, closeRelay := startRawDialRelay(t, token, destinations)
	defer closeRelay()

	p := NewPool(confOf(map[string]config.Target{
		"remote": {
			Relay: relayURL, Credential: "cred", Node: "devbox", Token: token,
		},
	}), nil)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := p.DialContext(ctx, "remote", "tcp", "198.51.100.10:8443")
	if err != nil {
		t.Fatalf("relay DialContext: %v", err)
	}
	defer conn.Close()
	select {
	case got := <-destinations:
		if got != "198.51.100.10:8443" {
			t.Fatalf("raw destination=%q, want exact relay destination", got)
		}
	case <-ctx.Done():
		t.Fatal("relay did not receive raw destination")
	}
	if _, err := io.WriteString(conn, "probe"); err != nil {
		t.Fatalf("write raw relay stream: %v", err)
	}
	echo := make([]byte, len("probe"))
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read raw relay stream: %v", err)
	}
	if string(echo) != "probe" {
		t.Fatalf("raw relay echo=%q", echo)
	}
}

func startRawDialRelay(t *testing.T, token string, destinations chan<- string) (string, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_, frameBytes, err := ws.Read(ctx)
		if err != nil {
			return
		}
		frame, err := relay.Decode(frameBytes)
		if err != nil || frame.Type != relay.Connect || frame.Node != "devbox" || frame.Credential != "cred" {
			_ = ws.Close(websocket.StatusPolicyViolation, "bad connect")
			return
		}
		response, err := relay.Encode(relay.Frame{Type: relay.ConnectOK, Account: "acc1"})
		if err != nil {
			return
		}
		if err := ws.Write(ctx, websocket.MessageText, response); err != nil {
			return
		}
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		secure, err := relay.SecureServer(ctx, raw, token, "acc1", "devbox")
		if err != nil {
			return
		}
		reader := bufio.NewReader(secure)
		magic := make([]byte, len("handoff-preview-raw-v1"))
		if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != "handoff-preview-raw-v1" {
			return
		}
		parts := make([]string, 2)
		for i := range parts {
			var size [2]byte
			if _, err := io.ReadFull(reader, size[:]); err != nil {
				return
			}
			part := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(reader, part); err != nil {
				return
			}
			parts[i] = string(part)
		}
		if parts[0] != "tcp" {
			return
		}
		destinations <- parts[1]
		if _, err := secure.Write([]byte{0}); err != nil {
			return
		}
		buf := make([]byte, 1024)
		for {
			n, err := secure.Read(buf)
			if err != nil {
				return
			}
			if _, err := secure.Write(buf[:n]); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

func TestPoolDialContextRejectsUnknownOrClosedTarget(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{}), nil)
	if _, err := p.DialContext(context.Background(), "ghost", "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("unknown target must fail")
	}
	p.Close()
	if _, err := p.DialContext(context.Background(), "ghost", "tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("closed pool must fail")
	}
}
