package targetclient

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/coder/websocket"
)

func TestPoolDialContextDirectNonLoopbackIsSentToOwner(t *testing.T) {
	got := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/preview-raw" {
			http.NotFound(w, r)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		raw := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
		_, addr, err := relay.ReadPreviewRawRequest(raw)
		if err != nil {
			return
		}
		got <- addr
		_ = relay.WritePreviewRawResponse(raw, fmt.Errorf("via dest recorded"))
	}))
	defer server.Close()
	ownerURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse owner url: %v", err)
	}
	p := NewPool(confOf(map[string]config.Target{
		"remote": {Addr: ownerURL.Host, Token: "tok"},
	}), nil)
	defer p.Close()
	_, err = p.DialContext(context.Background(), "remote", "tcp", "8.8.8.8:443")
	if err == nil {
		t.Fatal("expected owner-side error after recording dest")
	}
	select {
	case addr := <-got:
		if addr != "8.8.8.8:443" {
			t.Fatalf("owner dest=%q, want 8.8.8.8:443 (coordinator must not rewrite or locally dial)", addr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner did not receive non-loopback dest")
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

func TestPoolDialContextDirectDialsOwnerLoopback(t *testing.T) {
	content, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("content listen: %v", err)
	}
	defer content.Close()
	contentHits := make(chan struct{}, 1)
	go func() {
		conn, err := content.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		contentHits <- struct{}{}
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		_, _ = conn.Write(buf)
	}()

	ownerHits := make(chan string, 1)
	server := startOwnerOnDistinctLoopback(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/preview-raw" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		network, addr, err := relay.ReadPreviewRawRequest(raw)
		if err != nil {
			return
		}
		ownerHits <- network + " " + addr
		upstream, err := net.Dial("tcp", addr)
		if err != nil {
			_ = relay.WritePreviewRawResponse(raw, err)
			return
		}
		defer upstream.Close()
		if err := relay.WritePreviewRawResponse(raw, nil); err != nil {
			return
		}
		go func() { _, _ = io.Copy(raw, upstream); _ = raw.Close() }()
		_, _ = io.Copy(upstream, raw)
	}))

	ownerURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse owner url: %v", err)
	}
	targetHost, _, err := net.SplitHostPort(ownerURL.Host)
	if err != nil {
		t.Fatalf("split owner host: %v", err)
	}
	if targetHost == "127.0.0.1" {
		t.Fatal("owner host must differ from content 127.0.0.1 so JoinHostPort(targetHost, contentPort) cannot false-green")
	}
	_, contentPort, err := net.SplitHostPort(content.Addr().String())
	if err != nil {
		t.Fatalf("split content: %v", err)
	}

	p := NewPool(confOf(map[string]config.Target{
		"linux-01": {Addr: ownerURL.Host, Token: "tok"},
	}), nil)
	defer p.Close()

	conn, err := p.DialContext(context.Background(), "linux-01", "tcp", net.JoinHostPort("localhost", contentPort))
	select {
	case got := <-ownerHits:
		want := "tcp " + net.JoinHostPort("127.0.0.1", contentPort)
		if got != want {
			t.Fatalf("owner dest=%q, want %q (must not rewrite to target host %q)", got, want, targetHost)
		}
		if strings.Contains(got, targetHost) {
			t.Fatalf("owner dest %q still carries target host %q", got, targetHost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner /ws/preview-raw was not used; directDialer still coordinator-dials content")
	}
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "probe"); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(echo) != "probe" {
		t.Fatalf("echo=%q", string(echo))
	}
	select {
	case <-contentHits:
	case <-time.After(2 * time.Second):
		t.Fatal("content listener was not reached via owner")
	}
}

// startOwnerOnDistinctLoopback binds the owner httptest off 127.0.0.1 so a
// regression that does JoinHostPort(targetHost, contentPort) cannot masquerade
// as the normalized loopback dest. Darwin often cannot bind 127.0.0.2; [::1]
// is the equivalent "target host ≠ content host" split.
func startOwnerOnDistinctLoopback(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for _, addr := range []string{"[::1]:0", "127.0.0.2:0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		host, _, err := net.SplitHostPort(ln.Addr().String())
		if err != nil || host == "127.0.0.1" {
			_ = ln.Close()
			continue
		}
		server := httptest.NewUnstartedServer(h)
		server.Listener = ln
		server.Start()
		t.Cleanup(server.Close)
		return server
	}
	t.Fatal("need owner listener host ≠ 127.0.0.1 ([::1] or 127.0.0.2)")
	return nil
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
