// workspace_run_http_test.go —— RunCmd 经 /api/tasks/<id>/run 路由的工作目录判据测试。
//
// 职责：钉死「工作树已被回收」时 HTTP 层返回 400 而不是 500。
// 边界：纯 RunCmd 行为测试在 internal/workspace/workspace_run_test.go。
package agentd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTaskRunMissingWorkdirReturns400 验证工作树已回收时是 400 而不是 500。
//
// why 这条断言重要：500 会让脚本化调用方把「你要的工作树没了」读成「服务端炸了」，
// 两者的处置完全相反——前者该换任务/重新派发，后者该去查 agentd。
func TestTaskRunMissingWorkdirReturns400(t *testing.T) {
	s, taskID := newTestServerWithTask(t) // 该任务的 RepoPath 指向一个不存在的目录

	// why 不复用 doWorktreeReq：它把 Bearer 写死成 "test"，而本助手建的服务器用
	// testToken。改服务器的 token 去迁就助手要写 s.conf().Token = ...，那是在
	// atomic.Pointer 持有的那份 config 上就地改字段，绕过 swapConf 的换配置纪律——
	// 本用例不会 race，但这个形状会被照抄。自己拼请求更干净。
	r := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/run",
		strings.NewReader(`{"cmd":"echo x"}`))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码应为 400，实为 %d，响应体 %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "工作目录不存在") {
		t.Fatalf("响应体应点名真因，实为 %s", rec.Body.String())
	}
}
