package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 200：包体原样交给调用方，empty 为 false。
func TestBundleOK(t *testing.T) {
	want := []byte("PACK-fake-bundle-bytes")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("have"); got != "abc123" {
			t.Errorf("have 应透传，实得 %q", got)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	}))
	defer ts.Close()

	rc, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "abc123")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(want) {
		t.Errorf("包体应原样返回，实得 %q", got)
	}
}

// 204 是防御分支：本实现的服务端保证区间永不为空，所以收到 204 必须**如实报错**。
//
// 为什么不能当成「已是最新」：那样客户端会在本地根本没有该分支引用的情况下报成功
// ——正是 B143 真机验收抓到的静默倒退。204 只可能来自那个短命的中间版本。
func TestBundleNoContentIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	rc, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
	if err == nil {
		t.Fatal("204 必须报错，不能被当成「已是最新」")
	}
	if errors.Is(err, ErrBundleUnsupported) {
		t.Errorf("204 不是「对端过旧」这一结论，不该翻成 ErrBundleUnsupported：%v", err)
	}
	if rc != nil {
		t.Error("204 时不该返回可读流")
	}
}

// 404：翻成 ErrBundleUnsupported（对端过旧这一**结论**），不是普通错误。
// 承重：cmd 层只对这个哨兵回落 ssh。
func TestBundleUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()

	_, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
	if !errors.Is(err, ErrBundleUnsupported) {
		t.Fatalf("404 应翻成 ErrBundleUnsupported，实得 %v", err)
	}
}

// 400 / 500：普通错误，**绝不能**被误判成 ErrBundleUnsupported——否则
// cmd 层会把一次真失败当成「对端过旧」而回落 ssh，把问题藏起来。
func TestBundleOtherStatusIsNotUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		_, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
		ts.Close()
		if err == nil {
			t.Errorf("状态码 %d 应返回错误", status)
			continue
		}
		if errors.Is(err, ErrBundleUnsupported) {
			t.Errorf("状态码 %d 不该被当成对端过旧", status)
		}
	}
}
