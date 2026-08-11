// watchers_test.go —— watchers 运行态从 hub 上到 API 的白盒测试。
//
// 职责：钉住 /api/tasks 与 /api/tasks/{id} 的 watchers 取自 hub 实时订阅数，
// 以及 Manager.Status / Manager.Done 与订阅表的联动。
//
// 边界：不验 CLI 渲染（那在 cmd/status_test.go），不验线格式兼容（在 internal/proto）。
package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestStatusCarriesStallTimeout 验证 /api/status 把 agentd 自己的 stalltimeout
// 报出来——这是 wait --follow 判断「--timeout 会不会抢在 stalled 前面」的唯一依据。
//
// 刻意设成 90m 而不是默认的 2h：默认值恒等于零值之外的另一个常数，测不出
// 「到底是读了配置还是写死了」。
func TestStatusCarriesStallTimeout(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	m.cfg.StallTimeout = 90 * time.Minute
	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.StallTimeout != "1h30m0s" {
		t.Errorf("StallTimeout = %q, want %q", resp.StallTimeout, "1h30m0s")
	}
}

// TestStatusCarriesWatchers 验证活跃任务带上订阅数，且是指针（老 agentd 缺字段
// 与「确实是 0」必须可区分）。
func TestStatusCarriesWatchers(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	const id = "task-status-watch"
	createRunningTask(t, st, id)

	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.Active) != 1 {
		t.Fatalf("活跃任务数 = %d, want 1", len(resp.Active))
	}
	if resp.Active[0].Watchers == nil || *resp.Active[0].Watchers != 0 {
		t.Fatalf("无人订阅时 Watchers = %v, want 指向 0 的指针", resp.Active[0].Watchers)
	}
	_, cancel := hub.Subscribe(id)
	defer cancel()
	resp, err = m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.Active[0].Watchers == nil || *resp.Active[0].Watchers != 1 {
		t.Fatalf("一个订阅者时 Watchers = %v, want 指向 1 的指针", resp.Active[0].Watchers)
	}
}

// TestDoneClosesEventSubscriptions 验证 done 归档时关闭该任务的全部事件订阅。
//
// 为什么要单独验这条：WS 那侧只证明「订阅一关就正常收尾」，不证明 done 会去关。
// 少了这根接线，归档仍然对跟随端无声。
func TestDoneClosesEventSubscriptions(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	const id = "task-done-close"
	createRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingReview); err != nil {
		t.Fatalf("推到 waiting_review: %v", err)
	}
	ch, cancel := hub.Subscribe(id)
	defer cancel()

	if err := m.Done(context.Background(), id); err != nil {
		t.Fatalf("Done: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("归档后订阅通道仍在投递事件")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("归档未关闭订阅通道：跟随端拿不到「没有下文了」的信号")
	}
	if n := hub.Watchers(id); n != 0 {
		t.Errorf("归档后 Watchers = %d, want 0", n)
	}
}
