package relay_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

func TestE2ERoundTrip(t *testing.T) {
	a, b := net.Pipe()
	ctx := context.Background()
	var sc, ss net.Conn
	var ec, es error
	done := make(chan struct{})
	go func() {
		sc, ec = relay.SecureClient(ctx, a, "tok", "acc1", "devbox")
		close(done)
	}()
	ss, es = relay.SecureServer(ctx, b, "tok", "acc1", "devbox")
	<-done
	if ec != nil || es != nil {
		t.Fatalf("handshake err client=%v server=%v", ec, es)
	}
	defer sc.Close()
	defer ss.Close()
	go sc.Write([]byte("secret-payload"))
	buf := make([]byte, len("secret-payload"))
	if _, err := io.ReadFull(ss, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "secret-payload" {
		t.Fatalf("got %q", buf)
	}
}

func TestE2EWrongTokenFailsHandshake(t *testing.T) {
	a, b := net.Pipe()
	ctx := context.Background()
	go func() { _, _ = relay.SecureClient(ctx, a, "tok-A", "acc1", "devbox") }()
	if _, err := relay.SecureServer(ctx, b, "tok-B", "acc1", "devbox"); err == nil {
		t.Fatal("mismatched PSK must fail the handshake")
	}
}

func TestDerivePSKDomainSeparation(t *testing.T) {
	salt := bytes.Repeat([]byte{1}, 32)
	k1, err := relay.DerivePSKForTest("tok", "acc1", "devbox", salt)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := relay.DerivePSKForTest("tok", "acc1", "other", salt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("different node must yield different PSK")
	}
}
