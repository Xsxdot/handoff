package testhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestNewServerSetsLingerOnAcceptAndDefaultClientDial(t *testing.T) {
	var calls atomic.Int32
	old := setLinger
	setLinger = func(*net.TCPConn) error {
		calls.Add(1)
		return nil
	}
	t.Cleanup(func() { setLinger = old })
	ts := NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	resp, err := http.DefaultClient.Get(ts.URL)
	if err != nil {
		t.Fatalf("default client GET: %v", err)
	}
	defer resp.Body.Close()
	if body, err := io.ReadAll(resp.Body); err != nil || string(body) != "ok" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("SetLinger 调用次数=%d，Accept 与 client Dial 两侧都必须调用", got)
	}
}

type addressUnavailable struct{}

func (addressUnavailable) Error() string { return "can't assign requested address" }
func (addressUnavailable) Unwrap() error { return syscall.EADDRNOTAVAIL }

func TestRetryDialContextIsBoundedAndPreservesError(t *testing.T) {
	var calls atomic.Int32
	dial := RetryDialContext(func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, addressUnavailable{}
	})
	_, err := dial(context.Background(), "tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("地址不可用时必须返回最终错误")
	}
	if got := calls.Load(); got != MaxDialAttempts {
		t.Fatalf("dial 次数=%d，期望上限 %d", got, MaxDialAttempts)
	}
	if !errors.Is(err, syscall.EADDRNOTAVAIL) || !strings.Contains(err.Error(), "can't assign requested address") {
		t.Fatalf("最终错误丢失原始形状: %v", err)
	}
}

func TestRetryDialContextDoesNotRetryNonLoopback(t *testing.T) {
	var calls atomic.Int32
	dial := RetryDialContext(func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, addressUnavailable{}
	})
	_, err := dial(context.Background(), "tcp", "192.0.2.1:1")
	if err == nil || calls.Load() != 1 {
		t.Fatalf("非 loopback 必须一次返回原始错误，calls=%d err=%v", calls.Load(), err)
	}
}

func TestRetryDialContextRetriesLocalhostAndIPv6Loopback(t *testing.T) {
	for _, addr := range []string{"localhost:1", "[::1]:1"} {
		t.Run(addr, func(t *testing.T) {
			var calls atomic.Int32
			dial := RetryDialContext(func(context.Context, string, string) (net.Conn, error) {
				calls.Add(1)
				return nil, addressUnavailable{}
			})
			_, err := dial(context.Background(), "tcp", addr)
			if err == nil {
				t.Fatal("loopback address unavailable must return an error")
			}
			if got := calls.Load(); got != MaxDialAttempts {
				t.Fatalf("dial 次数=%d，期望上限 %d", got, MaxDialAttempts)
			}
			if !errors.Is(err, syscall.EADDRNOTAVAIL) || !strings.Contains(err.Error(), "can't assign requested address") {
				t.Fatalf("最终错误丢失原始形状: %v", err)
			}
		})
	}
}
