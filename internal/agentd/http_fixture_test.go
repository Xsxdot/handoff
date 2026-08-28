// http_fixture_test.go —— B234 三条 agentd 测试拨号路线都受同一上限约束。
// 入口：http.DefaultClient.Do、websocket.Dial、internal/client.Client.HTTPClient().Do。
// 边界：只替换测试对象 transport；不修改 internal/client 的生产默认配置。
package agentd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	handoffclient "github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/testhttp"
	"github.com/coder/websocket"
)

type b234AddressUnavailable struct{}

func (b234AddressUnavailable) Error() string { return "can't assign requested address" }
func (b234AddressUnavailable) Unwrap() error { return syscall.EADDRNOTAVAIL }

func assertB234RetryRoute(t *testing.T, run func(testhttp.DialContext) error) {
	t.Helper()
	calls := 0
	failing := testhttp.DialContext(func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, b234AddressUnavailable{}
	})
	err := run(testhttp.RetryDialContext(failing))
	if err == nil {
		t.Fatal("loopback address unavailable must return an error")
	}
	if calls != testhttp.MaxDialAttempts {
		t.Fatalf("dial calls=%d, want retry upper bound %d", calls, testhttp.MaxDialAttempts)
	}
	if !errors.Is(err, syscall.EADDRNOTAVAIL) || !strings.Contains(err.Error(), "can't assign requested address") {
		t.Fatalf("final error lost address-allocation shape: %v", err)
	}
}

func TestHTTPFixtureDialRoutesReachRetryLimit(t *testing.T) {
	t.Run("http.DefaultClient", func(t *testing.T) {
		assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
			old := http.DefaultClient.Transport
			tr := &http.Transport{DialContext: dial}
			http.DefaultClient.Transport = tr
			t.Cleanup(func() {
				tr.CloseIdleConnections()
				http.DefaultClient.Transport = old
			})
			_, err := http.DefaultClient.Get("http://127.0.0.1:1/")
			return err
		})
	})
	t.Run("websocket.Dial default client", func(t *testing.T) {
		assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
			old := http.DefaultClient.Transport
			tr := &http.Transport{DialContext: dial}
			http.DefaultClient.Transport = tr
			t.Cleanup(func() {
				tr.CloseIdleConnections()
				http.DefaultClient.Transport = old
			})
			_, _, err := websocket.Dial(context.Background(), "ws://127.0.0.1:1/", nil)
			return err
		})
	})
	t.Run("client.New custom transport", func(t *testing.T) {
		assertB234RetryRoute(t, func(dial testhttp.DialContext) error {
			client := handoffclient.New("http://127.0.0.1:1", "")
			tr, ok := client.HTTPClient().Transport.(*http.Transport)
			if !ok {
				t.Fatalf("client.New transport type=%T, want *http.Transport", client.HTTPClient().Transport)
			}
			tr.Dial = nil
			tr.DialContext = dial
			_, err := client.HTTPClient().Get("http://127.0.0.1:1/")
			return err
		})
	})
}
