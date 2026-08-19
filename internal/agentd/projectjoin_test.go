// 任务归属 join 测试：命中 / 未登记 / 已注销 / 遗留 linked worktree 四态。
package agentd

import (
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

func TestProjectIndexLookup(t *testing.T) {
	idx := newProjectIndex([]proto.ProjectLocation{
		{ProjectID: "aaaa111122223333", Name: "handoff", Path: "/home/dev/handoff"},
		{ProjectID: "bbbb444455556666", Name: "tk", Path: "/home/dev/tk/"},
	})

	cases := []struct {
		name     string
		repoPath string
		want     string
	}{
		{"命中", "/home/dev/handoff", "aaaa111122223333"},
		{"命中（尾斜杠归一）", "/home/dev/tk", "bbbb444455556666"},
		{"命中（非规范路径归一）", "/home/dev/./handoff", "aaaa111122223333"},
		// 已注销 = 表里没这行了；未登记 = 从来没登记过。对 join 是同一件事：
		// 诚实显示未归属，而不是留一列陈旧数据说谎
		{"未登记", "/home/dev/other", ""},
		// B62 之前派发的任务，repo_path 可能指向 linked worktree（当时不归并）。
		// 这类任务 join 不中，显示未归属——这是诚实的降级，不做回填
		{"遗留 linked worktree", "/home/dev/handoff/.worktrees/w1", ""},
		{"空路径", "", ""},
	}
	for _, c := range cases {
		if got := idx.projectIDOf(c.repoPath); got != c.want {
			t.Errorf("%s: projectIDOf(%q) = %q，期望 %q", c.name, c.repoPath, got, c.want)
		}
	}
}

// TestTaskListAnnotatesProjectID 断言 GET /api/tasks 的每条都带上归属注解。
func TestTaskListAnnotatesProjectID(t *testing.T) {
	env := newTestAgentdEnv(t)
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
