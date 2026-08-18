package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 200：包体原样交给调用方，empty 为 false，branchHead 从头里取。
func TestBundleOK(t *testing.T) {
	want := []byte("PACK-fake-bundle-bytes")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("have"); got != "abc123" {
			t.Errorf("have 应透传，实得 %q", got)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Handoff-Branch-Head", "deadbeef")
		_, _ = w.Write(want)
	}))
	defer ts.Close()

	rc, empty, branchHead, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "abc123")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if empty {
		t.Fatal("200 时 empty 应为 false")
	}
	if branchHead != "deadbeef" {
		t.Errorf("branchHead 应为 deadbeef，实得 %q", branchHead)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(want) {
		t.Errorf("包体应原样返回，实得 %q", got)
	}
}

// 204：empty 为 true，rc 为 nil，err 为 nil——但 branchHead 要带出来，
// 调用方据此建本地引用。
func TestBundleEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handoff-Branch-Head", "deadbeef")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	rc, empty, branchHead, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
	if err != nil {
		t.Fatalf("204 不该是错误，实得 %v", err)
	}
	if !empty {
		t.Error("204 时 empty 应为 true")
	}
	if rc != nil {
		t.Error("204 时不该返回可读流")
	}
	if branchHead != "deadbeef" {
		t.Errorf("204 的 branchHead 应为 deadbeef，实得 %q", branchHead)
	}
}

// 404：翻成 ErrBundleUnsupported（对端过旧这一**结论**），不是普通错误。
// 承重：cmd 层只对这个哨兵回落 ssh。
func TestBundleUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()

	_, _, _, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
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
		_, _, _, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
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
