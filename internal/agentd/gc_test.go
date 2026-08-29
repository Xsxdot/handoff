// gc_test.go —— B298 agentd gc Ticket 0 接缝测试。
//
// 职责：
//   - 锁定 /api/gc 的空壳在实现前不会误删资源
//   - 验证 Manager.GC 的编排签名已经能被 HTTP handler 调用
//
// 边界：
//   - 不覆盖最终清理语义；实现节点替换空壳后应把测试推进到真实资源断言
package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleGCTicket0 保证 Ticket 0 只暴露接缝，不会在实现前动盘。
func TestHandleGCTicket0(t *testing.T) {
	s := &Server{mgr: &Manager{log: slog.Default()}, log: slog.Default()}
	r := httptest.NewRequest(http.MethodGet, "/api/gc", nil)
	w := httptest.NewRecorder()
	s.handleGC(w, r)

	if got, want := w.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "gc 尚未接线") {
		t.Fatalf("body = %q, want gc unwired marker", body)
	}
}
