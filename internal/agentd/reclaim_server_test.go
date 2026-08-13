// reclaim_server_test.go —— reclaim 两个 HTTP 端点的路由与状态码测试。
//
// 为什么白盒（package agentd）而不是放进 server_test.go：那个文件是 agentd_test
// 外部包，够不到 initGitRepo / mustCreateTask / newWorktree 等内部助手；而本组
// 用例需要真 git 仓库 + 真 worktree 才能驱动判定逻辑，只能白盒。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// newServerWithDirtyWorktree 造一个 server+manager+真 git 仓库，任务为终态 +
// 脏 managed worktree，返回 server 与任务 ID。
func newServerWithDirtyWorktree(t *testing.T) (*Server, string) {
	t.Helper()
	m, st, hub, _ := newTestManager(t)
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-srv1", "f-srv1")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-srv1", proto.TaskStateFailed, true)
	srv := &Server{cfg: &config.Config{Token: "test"}, st: st, hub: hub, log: m.log, mgr: m}
	return srv, id
}

// newServerWithRunningTask 造一个任务为非终态（running）的 server。
func newServerWithRunningTask(t *testing.T) (*Server, string) {
	t.Helper()
	m, st, hub, _ := newTestManager(t)
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-srv2", "f-srv2")
	id := seedTerminalTask(t, m, repo, wt, "f-srv2", proto.TaskStateRunning, true)
	srv := &Server{cfg: &config.Config{Token: "test"}, st: st, hub: hub, log: m.log, mgr: m}
	return srv, id
}

func TestHandleReclaimDirtyReturns409WithReason(t *testing.T) {
	s, id := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/reclaim",
		strings.NewReader(`{"force":false}`))
	// httptest 默认 Host=example.com，会被 web-console 的 hostGuard 在鉴权前 403。
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer test")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("脏树应返 409，实得 %d：%s", rec.Code, rec.Body.String())
	}
	var body proto.ReclaimError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体应是 ReclaimError：%v：%s", err, rec.Body.String())
	}
	if body.Reason != proto.ReasonDirty {
		t.Fatalf("reason 应为 dirty，实得 %q", body.Reason)
	}
	if len(body.Dirty) == 0 {
		t.Fatalf("dirty 清单不能为空——CLI 要靠它渲染改动列表")
	}
}

func TestHandleReclaimNonTerminalReturns409NotTerminal(t *testing.T) {
	s, id := newServerWithRunningTask(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/reclaim", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer test")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("非终态应返 409，实得 %d", rec.Code)
	}
	var body proto.ReclaimError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != proto.ReasonNotTerminal {
		t.Fatalf("reason 应为 not_terminal，实得 %q", body.Reason)
	}
}

func TestHandleReclaimUnknownTaskReturns404(t *testing.T) {
	s, _ := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/no-such/reclaim", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer test")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应返 404，实得 %d", rec.Code)
	}
}

func TestHandleReclaimListReturnsRows(t *testing.T) {
	s, id := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/reclaim", nil)
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Authorization", "Bearer test")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应返 200，实得 %d", rec.Code)
	}
	var body proto.ReclaimListResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解列表响应：%v", err)
	}
	if len(body.Rows) != 1 || body.Rows[0].TaskID != id {
		t.Fatalf("应含那条脏树任务，实得 %+v", body.Rows)
	}
}
