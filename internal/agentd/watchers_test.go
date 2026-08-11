// watchers_test.go —— watchers 运行态从 hub 上到 API 的白盒测试。
//
// 职责：钉住 /api/tasks 与 /api/tasks/{id} 的 watchers 取自 hub 实时订阅数，
// 以及 Manager.Status / Manager.Done 与订阅表的联动。
//
// 边界：不验 CLI 渲染（那在 cmd/status_test.go），不验线格式兼容（在 internal/proto）。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestListTasksCarriesWatchers 验证 /api/tasks 的每个任务都带 watchers，
// 且数值来自 hub 的实时订阅数而不是恒 0。
func TestListTasksCarriesWatchers(t *testing.T) {
	srv, _, st := newTestServerWithManager(t)
	const id = "task-watch-list"
	createRunningTask(t, st, id)

	if got := listWatchers(t, srv, id); got != 0 {
		t.Fatalf("无人订阅时 watchers = %d, want 0", got)
	}
	_, cancel := srv.hub.Subscribe(id)
	defer cancel()
	if got := listWatchers(t, srv, id); got != 1 {
		t.Fatalf("一个订阅者时 watchers = %d, want 1", got)
	}
	cancel()
	if got := listWatchers(t, srv, id); got != 0 {
		t.Fatalf("取消订阅后 watchers = %d, want 0", got)
	}
}

// TestGetTaskCarriesWatchers 验证任务详情接口同样带 watchers。
func TestGetTaskCarriesWatchers(t *testing.T) {
	srv, _, st := newTestServerWithManager(t)
	const id = "task-watch-detail"
	createRunningTask(t, st, id)
	_, cancel := srv.hub.Subscribe(id)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	srv.handleGetTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var detail struct {
		Task proto.TaskView `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解码详情: %v", err)
	}
	if detail.Task.Watchers != 1 {
		t.Errorf("详情 watchers = %d, want 1", detail.Task.Watchers)
	}
	if detail.Task.ID != id {
		t.Errorf("详情 task.id = %q, want %q（字段提升没生效？）", detail.Task.ID, id)
	}
}

// listWatchers 调 handleListTasks 并取出目标任务的 watchers。
func listWatchers(t *testing.T, srv *Server, taskID string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleListTasks(rec, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var views []proto.TaskView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("解码任务列表: %v", err)
	}
	for _, v := range views {
		if v.ID == taskID {
			return v.Watchers
		}
	}
	t.Fatalf("任务列表里没有 %s", taskID)
	return 0
}
