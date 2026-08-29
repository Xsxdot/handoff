package agentd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
)

func TestPreviewAllowlistAndPAC(t *testing.T) {
	allow, err := ParsePreviewAllowlist([]string{"10.0.0.0/8", "Example.COM"})
	if err != nil {
		t.Fatalf("parse allowlist: %v", err)
	}
	for _, host := range []string{"127.0.0.1", "::1", "10.2.3.4", "example.com"} {
		if !allow.Allows(host) {
			t.Fatalf("allowlist rejects %q", host)
		}
	}
	for _, host := range []string{"192.0.2.1", "other.example.com"} {
		if allow.Allows(host) {
			t.Fatalf("allowlist accepts %q", host)
		}
	}
	for _, value := range []string{"", "*", "^example$", "example.com:443", "http://example.com", "10.0.0.0/8/1"} {
		if _, err := ParsePreviewAllowlist([]string{value}); err == nil {
			t.Fatalf("allowlist accepts invalid %q", value)
		}
	}
	pac, err := RenderPreviewPAC("socks5://127.0.0.1:4321", allow)
	if err != nil {
		t.Fatalf("render PAC: %v", err)
	}
	pacText := string(pac)
	if !strings.Contains(pacText, "SOCKS5 127.0.0.1:4321") || strings.Contains(pacText, "DIRECT") || !strings.Contains(pacText, "example.com") {
		t.Fatalf("PAC=%s", pacText)
	}
}

func TestPreviewProxySOCKSConnectAppliesAllowlist(t *testing.T) {
	var dialed []string
	proxy, err := NewPreviewProxy(context.Background(), "preview-1", []string{"example.com"}, func(_ context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, network+":"+addr)
		local, remote := net.Pipe()
		go func() {
			_, _ = io.Copy(io.Discard, remote)
			_ = remote.Close()
		}()
		return local, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new proxy: %v", err)
	}
	defer proxy.Close()
	if host, port, err := net.SplitHostPort(proxy.Addr().String()); err != nil || net.ParseIP(host).IsLoopback() == false || port == "0" {
		t.Fatalf("proxy addr=%v host=%q port=%q err=%v", proxy.Addr(), host, port, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = proxy.Serve(ctx) }()

	conn, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("socks greeting: %v", err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(conn, method); err != nil || method[1] != 0 {
		t.Fatalf("socks method=%v err=%v", method, err)
	}
	request := []byte{5, 1, 0, 3, byte(len("example.com"))}
	request = append(request, []byte("example.com")...)
	request = append(request, 0x01, 0xbb)
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("socks connect: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		t.Fatalf("socks success reply=%v err=%v", reply, err)
	}
	if len(dialed) != 1 || dialed[0] != "tcp:example.com:443" {
		t.Fatalf("dialed=%v", dialed)
	}

	denied, err := net.Dial("tcp", proxy.Addr().String())
	if err != nil {
		t.Fatalf("dial denied proxy: %v", err)
	}
	defer denied.Close()
	_, _ = denied.Write([]byte{5, 1, 0})
	_, _ = io.ReadFull(denied, method)
	bad := []byte{5, 1, 0, 3, byte(len("other.example.com"))}
	bad = append(bad, []byte("other.example.com")...)
	bad = append(bad, 0x01, 0xbb)
	_, _ = denied.Write(bad)
	deniedReply := make([]byte, 10)
	if _, err := io.ReadFull(denied, deniedReply); err != nil {
		t.Fatalf("read denied reply: %v", err)
	}
	if deniedReply[1] == 0 || len(dialed) != 1 {
		t.Fatalf("denied reply=%v dialed=%v", deniedReply, dialed)
	}
}
