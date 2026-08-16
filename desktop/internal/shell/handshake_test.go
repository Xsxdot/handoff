package shell

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsoleURLReturnsIssuedURL(t *testing.T) {
	var gotAuth, gotDevice string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/tickets" {
			t.Errorf("请求路径 = %q, want /api/auth/tickets", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			DeviceName string `json:"device_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotDevice = body.DeviceName
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://127.0.0.1:7777/console?ticket=deadbeef",
			"expires_at": time.Now().Add(time.Minute),
		})
	}))
	defer ts.Close()

	ep := Endpoint{Addr: strings.TrimPrefix(ts.URL, "http://"), Token: "tok"}
	got, err := ConsoleURL(context.Background(), ep, "我的 mac")
	if err != nil {
		t.Fatalf("ConsoleURL 报错: %v", err)
	}
	if got != "http://127.0.0.1:7777/console?ticket=deadbeef" {
		t.Fatalf("URL = %q，与 agentd 返回的不一致", got)
	}
	if !strings.Contains(gotAuth, "tok") {
		t.Errorf("Authorization 头没带上主令牌，实际 = %q", gotAuth)
	}
	if gotDevice != "我的 mac" {
		t.Errorf("device_name = %q, want 我的 mac", gotDevice)
	}
}

// agentd 没起来时，错误必须能让人一眼看出是「连不上」，
// 而不是抛一个赤裸的 dial tcp——薄壳的用户没有终端可看。
func TestConsoleURLSaysAgentdUnreachable(t *testing.T) {
	// 关掉的 server：端口上没人监听
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := strings.TrimPrefix(ts.URL, "http://")
	ts.Close()

	_, err := ConsoleURL(context.Background(), Endpoint{Addr: addr, Token: "tok"}, "dev")
	if err == nil {
		t.Fatal("连不上 agentd 却没报错")
	}
	if !strings.Contains(err.Error(), "agentd") {
		t.Errorf("错误信息没提到 agentd，用户无从判断，实际 = %q", err)
	}
}

// 设备名缺省要能推出来，且带得出「这是桌面端」的信息，
// 否则会话列表里全是一样的主机名，吊销时分不清哪个是哪个。
func TestDefaultDeviceNameMentionsDesktop(t *testing.T) {
	got := DefaultDeviceName()
	if !strings.Contains(got, "handoff-desktop") {
		t.Fatalf("缺省设备名 = %q，应含 handoff-desktop", got)
	}
}
