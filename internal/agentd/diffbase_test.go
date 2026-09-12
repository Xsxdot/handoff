// diffbase_test.go —— diff 缺省基准的取值规则（B65）：任务有 BaseCommit 就用它，
// 没有才退回按仓库推导。
//
// 边界：本文件不测 Diff() 本身的输出格式（既有行为未变），只测「缺省基准取谁」
// 以及两个端点是否一致地采用了同一个取值。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
	"github.com/Xsxdot/handoff/internal/workspace"
)

type diffRangeCall struct {
	repo string
	base string
	head string
}

type diffHeadSpy struct {
	workspace.Capability
	calls []diffRangeCall
}

func (s *diffHeadSpy) DiffRange(ctx context.Context, repo, base, head string) (string, error) {
	s.calls = append(s.calls, diffRangeCall{repo: repo, base: base, head: head})
	return "diff from result commit", nil
}

// TestTaskDiffUsesResultRefCommit verifies the HTTP → Server → Capability seam:
// a precise assembled commit is the sole diff head, even though ResultRef.Path
// is not exposed and the response remains the historical diff-only shape.
func TestTaskDiffUsesResultRefCommit(t *testing.T) {
	const token = "result-ref-token"
	repo := initTestRepo(t)
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	st, err := store.Open(filepath.Join(t.TempDir(), "result-ref.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: token, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	srv := NewServer(cfg, st, discardLogger())
	m := newManagerForTest(t, ManagerDeps{Store: st, Hub: srv.Hub(), Ads: map[string]executor.Adapter{"fake": fake.New(nil)}, Cfg: cfg, Gate: newTestGate(t), Log: discardLogger(), LiveConfig: srv.Conf()})
	spy := &diffHeadSpy{Capability: workspace.NewCapability()}
	m.SetWorkspace(spy)
	srv.SetManager(m)
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "result-ref-task", RepoPath: repo, WorkDir: repo,
		Branch: "main", Executor: "fake", State: proto.TaskStatePending,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	ts := testhttp.NewServer(t, srv.Handler())
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks/result-ref-task/diff?base=main", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET diff: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status=%d want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("diff response shape=%v want only diff", body)
	}
	if _, ok := body["commit"]; ok {
		t.Fatal("diff response 不得新增 commit 字段")
	}
	if len(spy.calls) != 1 {
		t.Fatalf("DiffRange 调用=%+v want exactly one", spy.calls)
	}
	if got := spy.calls[0]; got.repo != repo || got.base != "main" || got.head != commit {
		t.Fatalf("DiffRange 入参=%+v，want repo=%q base=main head=%q", got, repo, commit)
	}
}

// TestTaskDiffAllowsEmptyResultPathWithCommit verifies the post-recycle shape:
// ResultRef.Path may be empty while the same HTTP diff request still consumes
// the precise commit, and the structured success log records that fact.
func TestTaskDiffAllowsEmptyResultPathWithCommit(t *testing.T) {
	const token = "result-path-token"
	repo := initTestRepo(t)
	commit := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	st, err := store.Open(filepath.Join(t.TempDir(), "result-path.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg := &config.Config{Token: token, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	srv := NewServer(cfg, st, logger)
	m := newManagerForTest(t, ManagerDeps{Store: st, Hub: srv.Hub(), Ads: map[string]executor.Adapter{"fake": fake.New(nil)}, Cfg: cfg, Gate: newTestGate(t), Log: logger, LiveConfig: srv.Conf()})
	spy := &diffHeadSpy{Capability: workspace.NewCapability()}
	m.SetWorkspace(spy)
	srv.SetManager(m)
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "result-path-task", RepoPath: repo,
		WorkDir: filepath.Join(t.TempDir(), "recycled-worktree"), Branch: "main",
		Executor: "fake", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	ts := testhttp.NewServer(t, srv.Handler())
	resp := doAuthorizedDiffRequest(t, ts.URL, token, "result-path-task", "main")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status=%d want %d", resp.StatusCode, http.StatusOK)
	}
	if len(spy.calls) != 1 || spy.calls[0].head != commit {
		t.Fatalf("DiffRange 入参=%+v，want唯一调用 head=%q", spy.calls, commit)
	}
	if !strings.Contains(logs.String(), "result_path_empty=true") {
		t.Fatalf("准确 commit 且空 Path 的成功日志缺 result_path_empty=true: %s", logs.String())
	}
}

// TestTaskDiffEmptyCommitFallsBackThroughHTTP verifies the HTTP seam when git
// cannot produce a commit: the original head is used and the fallback is
// observable in structured logs rather than silently becoming a success.
func TestTaskDiffEmptyCommitFallsBackThroughHTTP(t *testing.T) {
	const token = "empty-commit-token"
	workdir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "empty-commit.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg := &config.Config{Token: token, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	srv := NewServer(cfg, st, logger)
	m := newManagerForTest(t, ManagerDeps{Store: st, Hub: srv.Hub(), Ads: map[string]executor.Adapter{"fake": fake.New(nil)}, Cfg: cfg, Gate: newTestGate(t), Log: logger, LiveConfig: srv.Conf()})
	spy := &diffHeadSpy{Capability: workspace.NewCapability()}
	m.SetWorkspace(spy)
	srv.SetManager(m)
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "empty-commit-task", RepoPath: workdir, WorkDir: workdir,
		Branch: "ignored", Executor: "fake", State: proto.TaskStatePending,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	ts := testhttp.NewServer(t, srv.Handler())
	resp := doAuthorizedDiffRequest(t, ts.URL, token, "empty-commit-task", "main")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status=%d want %d", resp.StatusCode, http.StatusOK)
	}
	if len(spy.calls) != 1 || spy.calls[0].head != "HEAD" {
		t.Fatalf("空 commit 必须沿 HTTP seam fallback 到 HEAD，调用=%+v", spy.calls)
	}
	if !strings.Contains(logs.String(), "commit_fallback=true") || !strings.Contains(logs.String(), "head_rev=HEAD") {
		t.Fatalf("空 commit fallback 日志缺结构化原因/原 head: %s", logs.String())
	}
}

func doAuthorizedDiffRequest(t *testing.T, baseURL, token, taskID, base string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/tasks/"+taskID+"/diff?base="+base, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET diff: %v", err)
	}
	return resp
}

// TestDiffBaseForPrefersTaskBaseCommit 钉住优先级：BaseCommit 非空即用它。
func TestDiffBaseForPrefersTaskBaseCommit(t *testing.T) {
	repo := initTestRepo(t)
	task := &proto.Task{ID: "t1", BaseCommit: "0123456789abcdef0123456789abcdef01234567"}
	if got := workspace.DiffBaseFor(task, repo); got != task.BaseCommit {
		t.Errorf("应优先用任务基线：got=%q want=%q", got, task.BaseCommit)
	}
}

// TestDiffBaseForFallsBackWhenNoBaseCommit 钉住退回：BaseCommit 为空（切已存在
// 分支或老任务）时按仓库推导，退回是正常分支不是兜底。
func TestDiffBaseForFallsBackWhenNoBaseCommit(t *testing.T) {
	repo := initTestRepo(t) // initTestRepo 建的是 main 分支
	task := &proto.Task{ID: "t1"}
	if got := workspace.DiffBaseFor(task, repo); got != "main" {
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
	ts := testhttp.NewServer(t, srv.Handler())

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
	gotRepo, gotHead := workspace.TaskDiffTarget(task)
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
	gotRepo, gotHead := workspace.TaskDiffTarget(task)
	if gotRepo != wt || gotHead != "HEAD" {
		t.Errorf("worktree 在时应原样用它 + HEAD，得到 (%q, %q)", gotRepo, gotHead)
	}
}

// TestTaskDiffTargetKeepsWorktreeWhenBranchUnknown 老任务没记分支时不回退：
// 回退了也没有合法的右端，不如让错误原样暴露，别拿主线的 HEAD 冒充。
func TestTaskDiffTargetKeepsWorktreeWhenBranchUnknown(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "没了")
	task := &proto.Task{RepoPath: t.TempDir(), WorkDir: gone, Branch: ""}
	gotRepo, gotHead := workspace.TaskDiffTarget(task)
	if gotRepo != gone || gotHead != "HEAD" {
		t.Errorf("无分支可用时不该回退，得到 (%q, %q)", gotRepo, gotHead)
	}
}

func TestTaskDiffTargetFallbackHeadRevIsNotHEAD(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	task := &proto.Task{RepoPath: t.TempDir(), WorkDir: gone, Branch: "handoff/deadbeef"}
	_, head := workspace.TaskDiffTarget(task)
	if head == "HEAD" {
		t.Fatal("树已回收时右端不得是主仓 HEAD")
	}
	if head != "handoff/deadbeef" {
		t.Fatalf("右端应是任务分支，实得 %q", head)
	}
}
