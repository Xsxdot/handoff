// diffbase_test.go —— diff 缺省基准的取值规则（B65）：任务有 BaseCommit 就用它，
// 没有才退回按仓库推导。
//
// 边界：本文件不测 Diff() 本身的输出格式（既有行为未变），只测「缺省基准取谁」
// 以及两个端点是否一致地采用了同一个取值。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// TestDiffBaseForPrefersTaskBaseCommit 钉住优先级：BaseCommit 非空即用它。
func TestDiffBaseForPrefersTaskBaseCommit(t *testing.T) {
	repo := initTestRepo(t)
	task := &proto.Task{ID: "t1", BaseCommit: "0123456789abcdef0123456789abcdef01234567"}
	if got := diffBaseFor(task, repo); got != task.BaseCommit {
		t.Errorf("应优先用任务基线：got=%q want=%q", got, task.BaseCommit)
	}
}

// TestDiffBaseForFallsBackWhenNoBaseCommit 钉住退回：BaseCommit 为空（切已存在
// 分支或老任务）时按仓库推导，退回是正常分支不是兜底。
func TestDiffBaseForFallsBackWhenNoBaseCommit(t *testing.T) {
	repo := initTestRepo(t) // initTestRepo 建的是 main 分支
	task := &proto.Task{ID: "t1"}
	if got := diffBaseFor(task, repo); got != "main" {
		t.Errorf("应退回推导链：got=%q want=%q", got, "main")
	}
}

// TestBranchesEndpointReportsTaskBase 钉住端点一致性：branches 必须把 diff 实际
// 会用的任务基线报出来，否则前端「自动推导（…）」会显示与实际不符的值。
func TestBranchesEndpointReportsTaskBase(t *testing.T) {
	const token = "diffbase-token"
	const sha = "0123456789abcdef0123456789abcdef01234567"
	repo := initTestRepo(t)

	st, err := store.Open(t.TempDir() + "/diffbase.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{
		ID: "t1", Target: "local", State: proto.TaskStatePending,
		WorkDir: repo, BaseCommit: sha, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	srv := NewServer(&config.Config{Token: token, DataDir: t.TempDir()}, st, discardLogger())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks/t1/branches", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 branches: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	var body struct {
		Branches []string `json:"branches"`
		Default  string   `json:"default"`
		TaskBase string   `json:"task_base"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body.TaskBase != sha {
		t.Errorf("task_base 不对：got=%q want=%q", body.TaskBase, sha)
	}
	if !strings.Contains(strings.Join(body.Branches, ","), "main") {
		t.Errorf("分支列表应含 main：%v", body.Branches)
	}
}

// TestTaskDiffTargetFallsBackToRepoWhenWorktreeGone 钉住归档任务的 diff 出路。
//
// 真机实测：任务 done 之后 managed worktree 被回收，而 handleTaskDiff 仍在
// work_dir 里跑 git，目录不存在 → exit status 128 → 500。控制台把这个 500
// 静默吞成空集合，表现为「文件树不再显示新增/修改的颜色」，一点提示都没有。
//
// 分支还在主仓库里，所以回得去：repo_path + 任务分支。
func TestTaskDiffTargetFallsBackToRepoWhenWorktreeGone(t *testing.T) {
	repo := t.TempDir()
	task := &proto.Task{
		RepoPath: repo,
		WorkDir:  filepath.Join(t.TempDir(), "已被回收的-worktree"),
		Branch:   "bench/b93",
	}
	gotRepo, gotHead := taskDiffTarget(task)
	if gotRepo != repo {
		t.Errorf("worktree 没了应回到主仓库，得到 %q 想要 %q", gotRepo, repo)
	}
	// **右端必须是任务分支而不是 HEAD**：主仓库的 HEAD 是主线，拿它当右端
	// 会把主线相对基线的全部历史算成这个任务的改动
	if gotHead != "bench/b93" {
		t.Errorf("回退后右端应是任务分支，得到 %q", gotHead)
	}
}

// TestTaskDiffTargetKeepsWorktreeWhenPresent 反面：worktree 还在就别改行为。
// 跑着的任务要看实时进度，右端保持 HEAD。
func TestTaskDiffTargetKeepsWorktreeWhenPresent(t *testing.T) {
	wt := t.TempDir()
	task := &proto.Task{RepoPath: t.TempDir(), WorkDir: wt, Branch: "bench/b93"}
	gotRepo, gotHead := taskDiffTarget(task)
	if gotRepo != wt || gotHead != "HEAD" {
		t.Errorf("worktree 在时应原样用它 + HEAD，得到 (%q, %q)", gotRepo, gotHead)
	}
}

// TestTaskDiffTargetKeepsWorktreeWhenBranchUnknown 老任务没记分支时不回退：
// 回退了也没有合法的右端，不如让错误原样暴露，别拿主线的 HEAD 冒充。
func TestTaskDiffTargetKeepsWorktreeWhenBranchUnknown(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "没了")
	task := &proto.Task{RepoPath: t.TempDir(), WorkDir: gone, Branch: ""}
	gotRepo, gotHead := taskDiffTarget(task)
	if gotRepo != gone || gotHead != "HEAD" {
		t.Errorf("无分支可用时不该回退，得到 (%q, %q)", gotRepo, gotHead)
	}
}
