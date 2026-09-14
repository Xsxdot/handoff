// b23319_retained_test.go —— B233.19 迁包后必须留在 gateway 的集成断言。
//
// 职责：锁 /api/tasks 对已登记任务盖 ProjectID 注解的读时 join 行为（实现已迁
// internal/workspace：LoadProjectIndex/ProjectIndex.ProjectIDOf）。
// 边界：断言逐字保留自原 agentd/projectjoin_test.go#TestTaskListAnnotatesProjectID。
package agentd

import (
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

// TestTaskListAnnotatesProjectID 断言 GET /api/tasks 的每条都带上归属注解。
func TestTaskListAnnotatesProjectID(t *testing.T) {
	env := newTestAgentdEnv(t)
	ensureTestManager(t, env)
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "aaaa111122223333", Name: "handoff",
		Path: "/home/dev/handoff", OriginURL: "git@github.com:x/handoff.git",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	now := time.Now().UTC()
	mustCreateTask(t, env.st, &proto.Task{
		ID: uuid.NewString(), RepoPath: "/home/dev/handoff",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	mustCreateTask(t, env.st, &proto.Task{
		ID: uuid.NewString(), RepoPath: "/home/dev/nowhere",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})

	var views []proto.TaskView
	resp := env.getJSON(t, "/api/tasks", &views)
	if resp != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp)
	}
	if len(views) != 2 {
		t.Fatalf("任务数 = %d，期望 2", len(views))
	}
	got := map[string]string{}
	for _, v := range views {
		got[v.RepoPath] = v.ProjectID
	}
	if got["/home/dev/handoff"] != "aaaa111122223333" {
		t.Errorf("已登记任务应带 project_id，实得 %q", got["/home/dev/handoff"])
	}
	if got["/home/dev/nowhere"] != "" {
		t.Errorf("未登记任务应显示未归属（空串），实得 %q", got["/home/dev/nowhere"])
	}
}
