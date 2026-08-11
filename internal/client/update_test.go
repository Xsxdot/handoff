package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// TestPushUpdateSurfacesReason 锁住「拒绝原因必须可判别」。
//
// why：busy 与 unmanaged 的处置完全不同（前者能 --force，后者不能），
// 把 409 压成一句人话字符串，CLI 就只能靠 strings.Contains 猜——而猜出来的
// 处置建议会给用户一条注定失败的命令。
func TestPushUpdateSurfacesReason(t *testing.T) {
	for _, tc := range []struct{ reason string }{
		{proto.UpdateReasonBusy}, {proto.UpdateReasonUnmanaged},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(proto.UpdateError{Error: "拒了", Reason: tc.reason})
		}))
		_, err := client.New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("x"), false)
		var rej *client.UpdateRejected
		if !errors.As(err, &rej) {
			t.Fatalf("reason=%s：期望 *client.UpdateRejected，实得 %v", tc.reason, err)
		}
		if rej.Reason != tc.reason {
			t.Fatalf("Reason = %q，期望 %q", rej.Reason, tc.reason)
		}
		srv.Close()
	}
}

// TestPushUpdateOldAgentd：v0.1.0 的 agentd 没有这个端点，404 必须译成
// 一条可判别的哨兵，而不是一句「状态码 404」——巡检要据此说「对端过旧，
// 这一跳得手工做」，那是一条有用的结论，不是一个失败。
func TestPushUpdateOldAgentd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := client.New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("x"), false)
	if !errors.Is(err, client.ErrUpdateUnsupported) {
		t.Fatalf("期望 client.ErrUpdateUnsupported，实得 %v", err)
	}
}

// TestPushUpdateSendsRawBodyAndParams 锁住线格式：body 是 tar.gz 原文，
// tag / sha256 / force 走 query。
func TestPushUpdateSendsRawBodyAndParams(t *testing.T) {
	var gotBody []byte
	var gotQuery url.Values
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(proto.UpdateResp{OK: true, Version: "v1", Prev: "/x.prev", Restarted: true})
	}))
	defer srv.Close()
	resp, err := client.New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("TGZ"), true)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != "TGZ" {
		t.Fatalf("body = %q，期望原样的 tar.gz 字节", gotBody)
	}
	if gotQuery.Get("tag") != "v1" || gotQuery.Get("sha256") == "" || gotQuery.Get("force") == "" {
		t.Fatalf("query 不全: %v", gotQuery)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if resp.Prev != "/x.prev" {
		t.Fatalf("Prev 必须带出来，回滚要用它")
	}
}

// TestRestartAgentdSendsNoBody 锁住 D8 的纯重启模式。
func TestRestartAgentdSendsNoBody(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n = len(b)
		json.NewEncoder(w).Encode(proto.UpdateResp{OK: true, Restarted: true})
	}))
	defer srv.Close()
	if _, err := client.New(srv.URL, "tk").RestartAgentd(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("纯重启必须不带 body，实得 %d 字节", n)
	}
}

// TestWaitVersionTimesOut：新进程没起来时必须超时报错，绝不能悄悄成功。
// 这是「不确认就不报成功」那条纪律在客户端的落点。
func TestWaitVersionTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(proto.StatusResp{Version: proto.BuildInfo{Version: "v0"}})
	}))
	defer srv.Close()
	err := client.New(srv.URL, "tk").WaitVersion(context.Background(), "v1", 150*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("版本一直没变必须报超时")
	}
}

// TestWaitVersionSucceedsAfterRestart：重启期间 status 会连不上（连接被拒），
// 那不是失败，是过程——必须继续等，而不是第一次 dial 失败就放弃。
func TestWaitVersionSucceedsAfterRestart(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(proto.StatusResp{Version: proto.BuildInfo{Version: "v1"}})
	}))
	defer srv.Close()
	if err := client.New(srv.URL, "tk").WaitVersion(context.Background(), "v1", 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatalf("中途的失败是重启过程，不该放弃: %v", err)
	}
}
