// inbox_test.go 验证 agentd /api/inbox 的对象信封解码边界。
// 职责：锁住空收件箱的 HTTP 传输契约；边界：不测试服务端聚合或 CLI 格式化。
package client_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
)

// TestInboxDecodesEmptyEnvelope 钉住服务端返回 {"items":[]} 时 client 不报错。
func TestInboxDecodesEmptyEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/inbox" {
			t.Errorf("请求 = %s %s, want GET /api/inbox", r.Method, r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(ts.Close)

	got, err := client.New(ts.URL, "").Inbox(t.Context())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if got == nil {
		t.Fatal("空信封应返回空切片，不应返回 nil")
	}
	if len(got) != 0 {
		t.Fatalf("Inbox 返回 %d 条，want 0", len(got))
	}
}
