package targetclient

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
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
