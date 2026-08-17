// GET /api/tasks/{id}/plan 的测试：有归档、没归档、文件被删、超长截断。
package agentd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

// seedPlanTask 造一条带归档指令文件的任务，返回任务 ID 与文件路径。
func seedPlanTask(t *testing.T, env *testAgentdEnv, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	planPath := filepath.Join(dir, "b119-dispatch.md")
	if err := os.WriteFile(planPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写归档指令: %v", err)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	mustCreateTask(t, env.st, &proto.Task{ID: id, Name: "带 plan 的任务",
		RepoPath: "/home/dev/handoff", PlanPath: planPath,
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	return id, planPath
}

// TestTaskPlanReturnsArchivedInstruction 断言：正常任务返回原文与文件名。
func TestTaskPlanReturnsArchivedInstruction(t *testing.T) {
	env := newTestAgentdEnv(t)
	body := "# 执行纪律\n\n逐 task 实现。\n"
	id, _ := seedPlanTask(t, env, body)

	var got proto.TaskPlan
	code := env.getJSON(t, "/api/tasks/"+id+"/plan", &got)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if got.Content != body {
		t.Fatalf("content = %q，期望原文 %q", got.Content, body)
	}
	if got.Name != "b119-dispatch.md" {
		t.Fatalf("name = %q，期望归档文件名", got.Name)
	}
	if got.Size != int64(len(body)) {
		t.Fatalf("size = %d，期望 %d", got.Size, len(body))
	}
	if got.Truncated {
		t.Fatal("truncated = true，短文件不该被截断")
	}
}

// TestTaskPlanTruncatesHugeFile 断言：超过上限只给开头并置 truncated，size 仍是真实大小。
func TestTaskPlanTruncatesHugeFile(t *testing.T) {
	env := newTestAgentdEnv(t)
	body := strings.Repeat("x", maxPlanBytes+1024)
	id, _ := seedPlanTask(t, env, body)

	var got proto.TaskPlan
	if code := env.getJSON(t, "/api/tasks/"+id+"/plan", &got); code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if len(got.Content) != maxPlanBytes {
		t.Fatalf("content 长度 = %d，期望截到 %d", len(got.Content), maxPlanBytes)
	}
	if !got.Truncated {
		t.Fatal("truncated = false，超长文件必须标出来")
	}
	if got.Size != int64(len(body)) {
		t.Fatalf("size = %d，期望磁盘真实大小 %d", got.Size, len(body))
	}
}

// TestTaskPlanMissingIsNotFound 断言：老任务（PlanPath 为空）与文件已删都是 404，
// 且错误里说得出是哪种情况——两者的处置完全不同。
func TestTaskPlanMissingIsNotFound(t *testing.T) {
	env := newTestAgentdEnv(t)
	now := time.Now().UTC()
	old := uuid.NewString()
	mustCreateTask(t, env.st, &proto.Task{ID: old, Name: "老任务", RepoPath: "/home/dev/handoff",
		State: proto.TaskStateCompleted, CreatedAt: now, UpdatedAt: now})

	var errBody map[string]string
	if code := env.getJSON(t, "/api/tasks/"+old+"/plan", &errBody); code != http.StatusNotFound {
		t.Fatalf("无 plan_path 任务状态码 = %d，期望 404", code)
	}
	if !strings.Contains(errBody["error"], "没有归档派发指令") {
		t.Fatalf("错误原文 = %q，期望说明是老任务", errBody["error"])
	}

	id, planPath := seedPlanTask(t, env, "x")
	if err := os.Remove(planPath); err != nil {
		t.Fatalf("删除归档文件: %v", err)
	}
	errBody = nil
	if code := env.getJSON(t, "/api/tasks/"+id+"/plan", &errBody); code != http.StatusNotFound {
		t.Fatalf("文件已删状态码 = %d，期望 404", code)
	}
	if !strings.Contains(errBody["error"], planPath) {
		t.Fatalf("错误原文 = %q，期望带上缺失的路径", errBody["error"])
	}
}

// TestTaskPlanUnknownTaskIsNotFound 断言：任务不存在返回 404 而不是 500。
func TestTaskPlanUnknownTaskIsNotFound(t *testing.T) {
	env := newTestAgentdEnv(t)
	if code := env.getJSON(t, "/api/tasks/"+uuid.NewString()+"/plan", nil); code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", code)
	}
}
