package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestLastAssistantMessageParsesRealPayload 用真实抓包的报文验解析。
//
// why 用真实夹具而不是手写 JSON：本仓库对「按 schema 名字推断」有过教训
// （B28 的 spike 一次推翻四处推断）。手写的夹具只能证明代码与我的想象一致。
func TestLastAssistantMessageParsesRealPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_messages.json")
	if err != nil {
		t.Fatalf("读夹具失败（先按 Step 1 抓真实报文）: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_test/message" {
			t.Errorf("请求路径不对: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	msg, err := api.LastAssistantMessage(context.Background(), "ses_test")
	if err != nil {
		t.Fatalf("查会话尾部失败: %v", err)
	}
	if msg == nil {
		t.Fatal("夹具里有已完结的 assistant 消息，却返回 nil")
	}
	if msg.Role != "assistant" {
		t.Fatalf("role want assistant, got %q", msg.Role)
	}
	if msg.CompletedMS == 0 {
		t.Fatal("夹具里的消息已完结，CompletedMS 不应为 0")
	}
	if msg.ID == "" {
		t.Fatal("消息 id 不应为空——它是对账水位的载体")
	}
	if msg.Text == "" {
		t.Fatal("夹具里的 assistant 消息有 text part，Text 不应为空")
	}
}

// TestLastAssistantMessageEmptySession 验空会话返回 (nil, nil) 而不是错误。
func TestLastAssistantMessageEmptySession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	msg, err := NewAPI(srv.URL, "pw").LastAssistantMessage(context.Background(), "ses_test")
	if err != nil {
		t.Fatalf("空会话不应报错: %v", err)
	}
	if msg != nil {
		t.Fatalf("空会话应返回 nil，got %+v", msg)
	}
}

// TestLastAssistantMessageHTTPError 验非 2xx 转成带状态码的错误。
func TestLastAssistantMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewAPI(srv.URL, "pw").LastAssistantMessage(context.Background(), "ses_test"); err == nil {
		t.Fatal("500 应转成错误")
	}
}
