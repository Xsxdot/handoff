package agentd

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

func TestPreviewRawWSRequiresAuth(t *testing.T) {
	env := newTestAgentdEnv(t)
	resp, err := http.Get(env.ts.URL + "/ws/preview-raw")
	if err != nil {
		t.Fatalf("unauth get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("unauthenticated preview-raw must not upgrade")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", resp.StatusCode)
	}
}

func TestPreviewRawWSDialsLoopback(t *testing.T) {
	env := newTestAgentdEnv(t)
	content, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("content listen: %v", err)
	}
	defer content.Close()
	go func() {
		conn, err := content.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		_, _ = conn.Write(buf)
	}()

	cl := client.New(env.ts.URL, env.token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := cl.DialPreviewRaw(ctx, "tcp", "localhost:"+portOf(t, content.Addr().String()))
	if err != nil {
		t.Fatalf("DialPreviewRaw: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("probe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(echo) != "probe" {
		t.Fatalf("echo=%q", echo)
	}
}

// SOCKS handle dials with a timeout context and cancel()s it the moment Dial
// returns (net.Dial contract: ctx only covers establishment). If NetConn is
// bound to that ctx, the pipe dies before the HTTP GET and the browser sees
// an empty reply — the B301 live U5 miss.
func TestPreviewRawWSSurvivesDialContextCancel(t *testing.T) {
	env := newTestAgentdEnv(t)
	content, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("content listen: %v", err)
	}
	defer content.Close()
	go func() {
		conn, err := content.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		_, _ = conn.Write(buf)
	}()

	cl := client.New(env.ts.URL, env.token)
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := cl.DialPreviewRaw(dialCtx, "tcp", "127.0.0.1:"+portOf(t, content.Addr().String()))
	cancel()
	if err != nil {
		t.Fatalf("DialPreviewRaw: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("probe")); err != nil {
		t.Fatalf("write after dial cancel: %v", err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read after dial cancel: %v", err)
	}
	if string(echo) != "probe" {
		t.Fatalf("echo=%q", echo)
	}
}

func TestPreviewRawWSViaPool(t *testing.T) {
	env := newTestAgentdEnv(t)
	content, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("content listen: %v", err)
	}
	defer content.Close()
	go func() {
		conn, err := content.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()
	host := strings.TrimPrefix(env.ts.URL, "http://")
	p := targetclient.NewPool(func() *config.Config {
		return &config.Config{Targets: map[string]config.Target{
			"dev": {Addr: host, Token: env.token},
		}}
	}, env.srv.log)
	defer p.Close()
	conn, err := p.DialContext(context.Background(), "dev", "tcp", "localhost:"+portOf(t, content.Addr().String()))
	if err != nil {
		t.Fatalf("pool DialContext: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "abc"); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "abc" {
		t.Fatalf("echo=%q", buf)
	}
}

func portOf(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	return port
}
