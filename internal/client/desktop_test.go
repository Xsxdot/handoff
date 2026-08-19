// 本文件覆盖 PutDesktopState 的 HTTP 方法、路径、鉴权与 JSON 线格式。
//
// 职责：锁住桌面薄壳到 agentd 的单向上报契约。
// 边界：使用 httptest，不启动真实 agentd，也不测试 reporter 的节奏。
package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestPutDesktopStateSendsMethodPathAndBody(t *testing.T) {
	want := proto.DesktopState{AppVersion: "v0.3.1", SyncPlan: "blocked", SyncBusy: 2}
	var got proto.DesktopState
	var method, path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("请求体不是 JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := client.New(srv.URL, "token").PutDesktopState(context.Background(), want); err != nil {
		t.Fatalf("PutDesktopState: %v", err)
	}
	if method != http.MethodPut || path != "/api/desktop/state" {
		t.Fatalf("请求 = %s %s，想要 PUT /api/desktop/state", method, path)
	}
	if auth != "Bearer token" {
		t.Fatalf("Authorization = %q", auth)
	}
	if got != want {
		t.Fatalf("body = %+v，想要 %+v", got, want)
	}
}
