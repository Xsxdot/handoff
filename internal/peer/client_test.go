// peer client 测试：HTTP 错误映射与 deadline。
//
// 职责：
//   - 401/403 → AUTH_FAILED
//   - 网络错误 → unavailable
//   - Hello/EventsAfter 的请求路径与解码
//
// 边界：
//   - 使用 httptest 模拟远端，不发起真实网络
package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestClientHelloSuccess 验证 Hello 请求成功并解码。
func TestClientHelloSuccess(t *testing.T) {
	var called atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.URL.Path != "/v1/peer/hello" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("缺 Authorization 头")
		}
		json.NewEncoder(w).Encode(Hello{ProtocolVersion: 1, Capabilities: map[string]int{"catalog": 1}})
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "secret-token"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := c.Hello(ctx)
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if h.ProtocolVersion != 1 || h.Capabilities["catalog"] != 1 {
		t.Fatalf("hello = %+v", h)
	}
	if called.Load() != 1 {
		t.Fatalf("请求次数 = %d, want 1", called.Load())
	}
}

// TestClientHelloAuthFailed 验证 401 映射为 ErrAuthFailed。
func TestClientHelloAuthFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "wrong"})
	if _, err := c.Hello(context.Background()); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestClientHelloNetworkError 验证网络错误映射为 ErrUnavailable。
func TestClientHelloNetworkError(t *testing.T) {
	c := NewClient(ClientConfig{Endpoint: "http://127.0.0.1:1", Token: "t"}) // 无监听端口
	if _, err := c.Hello(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// TestClientEventsAfterWire 验证 events-after 的请求与解码。
func TestClientEventsAfterWire(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("machine_id") != "m1" || r.URL.Query().Get("after") != "5" {
			t.Errorf("query = %v", r.URL.Query())
		}
		json.NewEncoder(w).Encode([]MachineEvent{{MachineID: "m1", MachineSeq: 6, EventID: "e6"}})
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "t"})
	evs, err := c.EventsAfter(context.Background(), "m1", 5, 100)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(evs) != 1 || evs[0].MachineSeq != 6 {
		t.Fatalf("events = %+v", evs)
	}
}
