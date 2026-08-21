package relay_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

func TestFrameRoundTrip(t *testing.T) {
	in := relay.Frame{Type: relay.Connect, Node: "devbox", Credential: "c"}
	b, err := relay.Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := relay.Decode(b)
	if err != nil || out != in {
		t.Fatalf("round trip: %v %+v", err, out)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	if _, err := relay.Decode([]byte(`{"type":"X"}`)); err == nil {
		t.Fatal("want error")
	}
}
