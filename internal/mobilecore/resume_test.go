package mobilecore

// Exercise the existing loopback consumer after its listening socket is lost.
// Recovery must retain the document origin and the authenticated session.
import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestResumeReopensLostListenerAtSameOrigin(t *testing.T) {
	fa := &fakeAgentd{}
	upstream := httptest.NewServer(fa)
	defer upstream.Close()
	var dialError error
	cleanups := 0
	core := New(func(_ context.Context, _ proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
		if dialError != nil {
			return nil, nil, dialError
		}
		return client.New(upstream.URL, m.Token), func() { cleanups++ }, nil
	}, nil)
	defer core.Close()
	payload, err := proto.EncodePairBundle(proto.PairBundle{Version: proto.PairVersion, Machines: []proto.PairMachine{{Name: "phone", Addr: upstream.URL, Token: pairToken}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Pair(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	ck, err := core.Activate(context.Background(), "phone")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := core.Origin("phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := core.machines["phone"].srv.Close(); err != nil {
		t.Fatal(err)
	}
	hc := localClient()
	defer hc.CloseIdleConnections()
	if response, err := hc.Get(origin + "/api/status"); err == nil {
		response.Body.Close()
		t.Fatal("fixture must lose its listener")
	}
	if err := core.Resume(context.Background(), "phone"); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, origin+"/api/status", nil)
	req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	response, err := hc.Do(req)
	if err != nil {
		t.Fatalf("same document cannot reconnect: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	if got, _ := core.Origin("phone"); got != origin {
		t.Fatalf("origin changed: %s -> %s", origin, got)
	}
	if got, err := core.Session(); err != nil || got.Value != ck.Value {
		t.Fatalf("session lost: %v", err)
	}
	fa.mu.Lock()
	tickets := fa.tickets
	fa.mu.Unlock()
	if tickets != 1 {
		t.Fatalf("resume must not exchange authentication again: %d tickets", tickets)
	}
	response, err = hc.Get(origin + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated caller bypassed cookie gate: %d", response.StatusCode)
	}
	// Construction failure must not destroy the working listener.
	dialError = errors.New("transport unavailable")
	if err := core.Resume(context.Background(), "phone"); err == nil {
		t.Fatal("missing dial error")
	}
	response, err = hc.Do(req)
	if err != nil {
		t.Fatalf("dial failure destroyed original listener: %v", err)
	}
	response.Body.Close()
	dialError = nil
	// A temporarily occupied port must reclaim the rejected replacement and
	// allow a later foreground transition to try the same document origin again.
	core.machines["phone"].srv.Close()
	u, _ := url.Parse(origin)
	occupied, err := net.Listen("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	before := cleanups
	err = core.Resume(context.Background(), "phone")
	occupied.Close()
	if err == nil || cleanups != before+2 {
		t.Fatalf("failed bind leaked resources: err=%v cleanups=%d", err, cleanups-before)
	}
	for i := 0; i < 2; i++ {
		if err := core.Resume(context.Background(), "phone"); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
		if i == 0 && cleanups != before+2 {
			t.Fatalf("old cleanup called again after failed bind: %d", cleanups-before)
		}
		response, err = hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("retry status=%d", response.StatusCode)
		}
	}
	if err := core.Resume(context.Background(), "another-machine"); err == nil {
		t.Fatal("unrelated machine accepted")
	}
	core.Close()
	if err := core.Resume(context.Background(), "phone"); !errors.Is(err, errCoreClosed) {
		t.Fatalf("closed core resumed: %v", err)
	}
}
