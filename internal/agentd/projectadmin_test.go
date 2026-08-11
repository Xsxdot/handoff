package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/projectid"
	"github.com/xushixin/handoff/internal/proto"
)

// initGitRepoWithOrigin 造一个带初始提交且配好 origin 的仓库，返回路径。
// 登记层的每条路径都要求非空 origin，所以本文件的仓库都要走这个助手。
func initGitRepoWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	repo := initGitRepo(t)
	gitAt(t, repo, "remote", "add", "origin", origin)
	return repo
}

// TestRegisterProjectExisting 验证登记已有目录：现读 origin、归并主工作树、
// 算出 project_id 落库。
func TestRegisterProjectExisting(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Fatalf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	if loc.Name != "handoff" {
		t.Fatalf("名字应由 origin 末段派生，got %q", loc.Name)
	}
	if !filepath.IsAbs(loc.Path) {
		t.Fatalf("路径必须绝对化，got %q", loc.Path)
	}
}

// TestRegisterProjectMergesWorktree 验证给的是 linked worktree 时登记主仓：
// 位置表以 project_id 为主键，worktree 与主仓 origin 相同，不归并就撞主键。
func TestRegisterProjectMergesWorktree(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	main := initGitRepoWithOrigin(t, origin)
	wt := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat/x", wt)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: wt})
	if err != nil {
		t.Fatalf("RegisterProject(worktree): %v", err)
	}
	wantMain, _ := filepath.EvalSymlinks(main)
	gotReal, _ := filepath.EvalSymlinks(loc.Path)
	if gotReal != wantMain {
		t.Fatalf("worktree 应归并到主仓: got %s, want %s", gotReal, wantMain)
	}
}

// TestRegisterProjectRejectsOriginMismatch 验证「路径敲错但恰好指到另一个真实
// 仓库」被拒——这是自动化最容易造出的脏登记（spec §3.1）。
func TestRegisterProjectRejectsOriginMismatch(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	repo := initGitRepoWithOrigin(t, "git@github.com:xushixin/tk.git")

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: repo})
	if !errors.Is(err, ErrProjectOriginMismatch) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectOriginMismatch)", err)
	}
	for _, want := range []string{"tk", "handoff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文应同时给出两边的 origin，%q 未含 %q", err.Error(), want)
		}
	}
}

// TestRegisterProjectRejectsNoOrigin 验证没有 origin 的仓库拒绝登记：
// 它算不出 project_id，登记进来只会是一条永远引用不到的死记录。
func TestRegisterProjectRejectsNoOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	repo := initGitRepo(t) // 刻意不加 origin
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: repo})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want errors.Is(..., ErrRepoUnusable)", err)
	}
}

// TestRegisterProjectDuplicateProject 验证同一项目重复登记被拒，且报文指向已有位置。
func TestRegisterProjectDuplicateProject(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	first := initGitRepoWithOrigin(t, origin)
	if _, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: first}); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	second := initGitRepoWithOrigin(t, origin)
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: second})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectAlreadyExists)", err)
	}
	if !strings.Contains(err.Error(), first) {
		t.Errorf("报文 %q 应指向已有位置 %s", err.Error(), first)
	}
}

// TestRegisterProjectNameCollisionFallsBack 验证不同项目撞名字时落到 name-2。
func TestRegisterProjectNameCollisionFallsBack(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	a := initGitRepoWithOrigin(t, "git@github.com:xushixin/handoff.git")
	if _, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:xushixin/handoff.git", Path: a}); err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	b := initGitRepoWithOrigin(t, "git@github.com:other/handoff.git")
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:other/handoff.git", Path: b})
	if err != nil {
		t.Fatalf("同名不同项目登记: %v", err)
	}
	if loc.Name != "handoff-2" {
		t.Fatalf("名字应退让为 handoff-2，got %q", loc.Name)
	}
}

// TestRegisterProjectClonesWhenNoPath 验证不给 path 时 clone 到 repo_root/<名字>。
// 用本地目录当 clone 源，不依赖网络。
func TestRegisterProjectClonesWhenNoPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: src, Name: "src"})
	if err != nil {
		t.Fatalf("RegisterProject(clone): %v", err)
	}
	want := filepath.Join(root, "src")
	if loc.Path != want {
		t.Fatalf("落点 = %q, want %q", loc.Path, want)
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("落点应是一个克隆好的仓库: %v", err)
	}
}

// TestUnregisterProjectRejectsBusy 验证仓库仍被活跃任务占用时拒绝注销。
func TestUnregisterProjectRejectsBusy(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("登记: %v", err)
	}
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: loc.Path, State: proto.TaskStateRunning})
	if err := m.UnregisterProject(context.Background(), loc.Name); !errors.Is(err, ErrWorkdirBusy) {
		t.Fatalf("err = %v, want errors.Is(..., ErrWorkdirBusy)", err)
	}
}
