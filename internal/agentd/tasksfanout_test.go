// 任务列表 ?project= 与 ?scope=all 的测试。
package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// seedTasksEnv 造一台带本地任务、镜像任务与 targets 的本机。
func seedTasksEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff", Path: "/home/dev/handoff",
		OriginURL: "git@github.com:x/handoff.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	now := time.Now().UTC()
	mustCreateTask(t, env.st, &proto.Task{ID: uuid.NewString(), Name: "本机已归属",
		RepoPath: "/home/dev/handoff", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now})
	mustCreateTask(t, env.st, &proto.Task{ID: uuid.NewString(), Name: "本机未归属",
		RepoPath: "/home/dev/nowhere", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now})
	if err := env.st.UpsertMirrorTask("devbox", proto.Task{
		ID: uuid.NewString(), Name: "远端任务", State: proto.TaskStateRunning,
		RepoPath: "/remote/handoff", CreatedAt: now, UpdatedAt: now,
	}, now); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}
	return env
}

// TestTasksProjectFilter 断言：?project=<id> 只返回该项目的任务（裸数组形状不变）。
func TestTasksProjectFilter(t *testing.T) {
	env := seedTasksEnv(t)
	var views []proto.TaskView
	code := env.getJSON(t, "/api/tasks?project=aaaa111122223333", &views)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if len(views) != 1 || views[0].Name != "本机已归属" {
		t.Fatalf("?project 过滤结果 = %+v，期望只剩已归属那一条", views)
	}
}

// TestTasksScopeAllEnvelope 断言：?scope=all 返回信封，本机任务 machine=""、
// 镜像任务带 target 名，machines 里每台都有一行且 fetched_at 非零。
func TestTasksScopeAllEnvelope(t *testing.T) {
	env := seedTasksEnv(t)
	var resp proto.TasksResp
	code := env.getJSON(t, "/api/tasks?scope=all", &resp)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if len(resp.Tasks) != 3 {
		t.Fatalf("任务数 = %d，期望 3（本机 2 + 镜像 1）：%+v", len(resp.Tasks), resp.Tasks)
	}
	byMachine := map[string]int{}
	for _, tv := range resp.Tasks {
		byMachine[tv.Machine]++
	}
	if byMachine[""] != 2 {
		t.Errorf("本机任务（machine=\"\"）应为 2 条，实得 %d", byMachine[""])
	}
	if byMachine["devbox"] != 1 {
		t.Errorf("镜像任务（machine=devbox）应为 1 条，实得 %d", byMachine["devbox"])
	}
	if len(resp.Machines) != 2 {
		t.Fatalf("machines 数 = %d，期望 2（本机 + devbox）：%+v", len(resp.Machines), resp.Machines)
	}
	for _, m := range resp.Machines {
		if m.FetchedAt.IsZero() {
			t.Errorf("每台都要有 fetched_at：%+v", m)
		}
		if !m.Ok {
			t.Errorf("有快照的机器必须 ok=true：%+v", m)
		}
	}
}

// TestTasksBareArrayContract 断言：不带参数时响应仍是裸数组——W2 契约一行不改。
// 响应首字节必须是 '['（裸数组），解到 []proto.TaskView 成功、且不含信封键。
func TestTasksBareArrayContract(t *testing.T) {
	env := seedTasksEnv(t)
	var views []proto.TaskView
	code := env.getJSON(t, "/api/tasks", &views)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if len(views) != 2 {
		t.Fatalf("裸数组应只含本机任务 = 2 条，实得 %d", len(views))
	}
	// 逐字节契约：响应必须是以 '[' 开头的裸数组，绝不能出现信封的 machines/tasks 键
	raw, _ := json.Marshal(views)
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("响应不是裸数组：%s", raw)
	}
	if bytes.Contains(raw, []byte(`"machines"`)) || bytes.Contains(raw, []byte(`"tasks"`)) {
		t.Errorf("裸数组响应不该含信封键：%s", raw)
	}
}
