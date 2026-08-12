// 按任务 id 透明转发的测试：镜像路由命中 / 两处皆无 404 / 防环不转发。
package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// taskDetailWire 是对 GET /api/tasks/{id} 响应线格式的独立断言（白盒测试侧）。
type taskDetailWire struct {
	Task proto.Task `json:"task"`
}

// TestTaskRouteForwardsViaMirrorIndex 断言：本机没有、镜像索引指向远端时，
// GET /api/tasks/{id} 的响应来自远端（任务名是远端那条）。
func TestTaskRouteForwardsViaMirrorIndex(t *testing.T) {
	remote := newTestAgentdEnv(t)
	now := time.Now().UTC()
	taskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: taskID, Name: "远端任务",
		State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
		CreatedAt: now, UpdatedAt: now})

	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// 本机没有任务，但有镜像路由记录
	if err := local.st.UpsertMirrorTask("devbox", proto.Task{
		ID: taskID, Name: "远端任务", State: proto.TaskStateRunning,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}

	var wire taskDetailWire
	code := local.getJSON(t, "/api/tasks/"+taskID, &wire)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（响应应来自远端）", code)
	}
	if wire.Task.ID != taskID || wire.Task.Name != "远端任务" {
		t.Errorf("响应不是远端那条任务：%+v", wire.Task)
	}
}

// TestTaskRoute404WhenNowhere 断言：两处都没有 → 404（与今天一致）。
func TestTaskRoute404WhenNowhere(t *testing.T) {
	local := newTestAgentdEnv(t)
	code := local.getJSON(t, "/api/tasks/"+uuid.NewString(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", code)
	}
}

// TestTaskRouteNeverForwardsWhenForwarded 断言：带转发头时不再转发 → 404（防环）。
func TestTaskRouteNeverForwardsWhenForwarded(t *testing.T) {
	remote := newTestAgentdEnv(t)
	now := time.Now().UTC()
	taskID := uuid.NewString()
	mustCreateTask(t, remote.st, &proto.Task{ID: taskID, Name: "远端任务",
		State: proto.TaskStateRunning, RepoPath: "/remote/handoff",
		CreatedAt: now, UpdatedAt: now})
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := local.st.UpsertMirrorTask("devbox", proto.Task{
		ID: taskID, Name: "远端任务", State: proto.TaskStateRunning,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, local.ts.URL+"/api/tasks/"+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(forwardedHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("带转发头必须本机处理（404），实得状态码 %d；体=%s", resp.StatusCode, body)
	}
}
