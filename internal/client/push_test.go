// push_test.go —— 客户端侧设备登记（缝 S1 经核的第一跳）。
//
// 职责：钉 RegisterPushDevice/DeletePushDevice 的方法、路径、Bearer 与请求体形状。
// 边界：不测 agentd 侧受理（pushapi_test 从 HTTP 进）；不测 relay 传输
// （mobilecore 竖切覆盖）。
package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestRegisterPushDevicePostsJSON 锁 POST /api/push/devices 的请求形状。
func TestRegisterPushDevicePostsJSON(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotAuth, gotBody = r.Method, r.URL.Path,
			r.Header.Get("Authorization"), string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	cl := New(ts.URL, "tk-1")
	err := cl.RegisterPushDevice(context.Background(), proto.PushDeviceRegisterReq{
		DeviceID: "iphone-15", Platform: proto.PushPlatformIOS, APNSToken: "aabbccdd",
	})
	if err != nil {
		t.Fatalf("RegisterPushDevice: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/push/devices" {
		t.Fatalf("请求 = %s %s，期望 POST /api/push/devices", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tk-1" {
		t.Fatalf("Authorization = %q，期望 Bearer tk-1", gotAuth)
	}
	for _, want := range []string{`"device_id":"iphone-15"`, `"platform":"ios"`, `"apns_token":"aabbccdd"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("请求体缺 %s： %s", want, gotBody)
		}
	}
	// member 不得由客户端提供（服务端注入，决策 D3）。
	if strings.Contains(gotBody, `"member"`) {
		t.Errorf("请求体不得携带 member： %s", gotBody)
	}
}

// TestDeletePushDevice 锁 DELETE /api/push/devices 的形状与非 200 上抛。
func TestDeletePushDevice(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(b)
		if strings.Contains(string(b), `"ghost"`) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"设备未登记"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	cl := New(ts.URL, "tk-1")
	if err := cl.DeletePushDevice(context.Background(), "iphone-15"); err != nil {
		t.Fatalf("DeletePushDevice: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/push/devices" {
		t.Fatalf("请求 = %s %s，期望 DELETE /api/push/devices", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"device_id":"iphone-15"`) {
		t.Errorf("请求体缺 device_id： %s", gotBody)
	}

	// 404 必须上抛（调用方据此区分「本来就没登记」）。
	if err := cl.DeletePushDevice(context.Background(), "ghost"); err == nil {
		t.Fatal("404 应上抛错误")
	}
}
