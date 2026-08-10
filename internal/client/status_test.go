// client.Status 测试：正常解码、老 agentd 的 404 哨兵、其余错误照常报错。
package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xushixin/handoff/internal/client"
)

// 正常 200：字段要能解出来。
func TestStatusDecodes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Errorf("请求路径=%q, want /api/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/d",
			"version":{"revision":"abc123","go":"go1.26.1"},
			"executors":["claude","opencode"],"default_executor":"opencode",
			"task_counts":{"running":1},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	got, err := client.New(ts.URL, "tok").Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Listen != "0.0.0.0:7777" || got.Version.Revision != "abc123" {
		t.Fatalf("解码结果不对: %+v", got)
	}
	if got.DefaultExecutor != "opencode" {
		t.Fatalf("DefaultExecutor=%q", got.DefaultExecutor)
	}
}

// 老 agentd 不认这个路由 → 必须是可判别的哨兵错误，CLI 据此走降级分支并退 0。
func TestStatusOldAgentdReturnsSentinel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	_, err := client.New(ts.URL, "tok").Status(context.Background())
	if !errors.Is(err, client.ErrStatusUnsupported) {
		t.Fatalf("err=%v，404 必须映射成 ErrStatusUnsupported 哨兵", err)
	}
}

// 401 不是哨兵：token 不对是真失败，CLI 要退 1。
func TestStatusUnauthorizedIsNotSentinel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	_, err := client.New(ts.URL, "tok").Status(context.Background())
	if err == nil {
		t.Fatal("401 必须报错")
	}
	if errors.Is(err, client.ErrStatusUnsupported) {
		t.Fatal("401 不是「版本过旧」，不得映射成哨兵")
	}
}
