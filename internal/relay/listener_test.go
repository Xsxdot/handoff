package relay

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

func TestListenerServesHandlerThroughFakeRelay(t *testing.T) {
	relayURL, cleanup := startBridgeRelay(t, "tok", "acc1", "devbox")
	defer cleanup()
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "pong")
			return
		}
		_, _ = io.Copy(w, r.Body)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := NewListener(relayURL, "cred", "devbox", "tok", "acc1", echo, slog.Default())
	go l.RunWithReconnect(ctx)
	d := NewDialer(relayURL, "cred", "devbox", "tok", "acc1", slog.Default())
	defer d.Close()
	hc := &http.Client{Transport: d.Transport()}
	requestCtx, requestCancel := context.WithTimeout(ctx, 5*time.Second)
	defer requestCancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://relay-devbox/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
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

func TestListenerRawDialThroughFakeRelay(t *testing.T) {
	relayURL, cleanup := startBridgeRelay(t, "tok", "acc1", "devbox")
	defer cleanup()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, len("ping"))
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverDone <- err
			return
		}
		if string(buf) != "ping" {
			serverDone <- fmt.Errorf("upstream got %q", buf)
			return
		}
		_, err = io.WriteString(conn, "pong")
		serverDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l := NewListener(relayURL, "cred", "devbox", "tok", "acc1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), slog.Default())
	go l.RunWithReconnect(ctx)
	d := NewDialer(relayURL, "cred", "devbox", "tok", "acc1", slog.Default())
	defer d.Close()
	conn, err := d.RawDialContext(ctx, "tcp", upstream.Addr().String())
	if err != nil {
		t.Fatalf("raw dial: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write raw stream: %v", err)
	}
	got := make([]byte, len("pong"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read raw stream: %v", err)
	}
	if string(got) != "pong" {
		t.Fatalf("raw response=%q", got)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("upstream: %v", err)
	}
}

func TestAppListenerAcceptForwardsSession(t *testing.T) {
	left, right := net.Pipe()
	server, err := yamux.Server(left, relayYamuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	client, err := yamux.Client(right, relayYamuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer client.Close()
	listener := &appListener{session: server}
	stream, err := client.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("forwarded")); err != nil {
		t.Fatal(err)
	}
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	buf := make([]byte, len("forwarded"))
	if _, err := io.ReadFull(accepted, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "forwarded" {
		t.Fatalf("got %q", buf)
	}
}

func startBridgeRelay(t *testing.T, token, account, node string) (string, func()) {
	t.Helper()
	// 对齐真 relay：executor 的物理隧道上跑 relay-session-mux（relay 侧 yamux.Client），
	// 每个 coordinator 连接开一条 session 流，再把 coordinator raw 与该 session 流撮合。
	// executor 的 Listener 在物理隧道上跑 yamux.Server 收 session——两侧必须成对，
	// 直接 bridge raw↔raw 会让 executor 的 yamux.Server 读到非帧字节而失败。
	registered := make(chan *yamux.Session, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		frame, err := recvControl(ctx, ws)
		if err != nil {
			return
		}
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		switch frame.Type {
		case Register:
			if frame.Node != node || frame.Credential != "cred" {
				_ = ws.Close(websocket.StatusPolicyViolation, "bad register")
				return
			}
			if err := sendControl(ctx, ws, Frame{Type: Registered, Account: account}); err != nil {
				return
			}
			sess, err := yamux.Client(raw, relayYamuxConfig())
			if err != nil {
				return
			}
			registered <- sess
			<-ctx.Done()
		case Connect:
			if frame.Node != node || frame.Credential != "cred" {
				_ = ws.Close(websocket.StatusPolicyViolation, "bad connect")
				return
			}
			if err := sendControl(ctx, ws, Frame{Type: ConnectOK, Account: account}); err != nil {
				return
			}
			var execSess *yamux.Session
			select {
			case execSess = <-registered:
			case <-ctx.Done():
				return
			}
			stream, err := execSess.Open()
			if err != nil {
				return
			}
			bridgeConns(stream, raw)
		}
	}))
	return "ws" + strings.TrimPrefix(server.URL, "http"), server.Close
}

func bridgeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	done := make(chan struct{}, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	wg.Wait()
}
