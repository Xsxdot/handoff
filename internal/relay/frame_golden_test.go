package relay_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/relay"
)

func TestConnectFrameGoldenBytes(t *testing.T) {
	b, err := relay.Encode(relay.Frame{Type: relay.Connect, Node: "devbox", Credential: "c"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"CONNECT","node":"devbox","credential":"c"}`
	if string(b) != want {
		t.Fatalf("CONNECT 帧 = %s, want %s", b, want)
	}
}

func TestConnectOKFrameGoldenBytes(t *testing.T) {
	b, err := relay.Encode(relay.Frame{Type: relay.ConnectOK, Account: "acc1"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"CONNECT_OK","account":"acc1"}`
	if string(b) != want {
		t.Fatalf("CONNECT_OK 帧 = %s, want %s", b, want)
	}
}

func TestRegisterFrameTypeLiteral(t *testing.T) {
	if relay.Register != "REGISTER" || relay.Connect != "CONNECT" ||
		relay.Registered != "REGISTERED" || relay.ConnectOK != "CONNECT_OK" ||
		relay.Error != "ERROR" {
		t.Fatalf("控制帧字面值漂移：REGISTER=%q CONNECT=%q REGISTERED=%q CONNECT_OK=%q ERROR=%q",
			relay.Register, relay.Connect, relay.Registered, relay.ConnectOK, relay.Error)
	}
}
