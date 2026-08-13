package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
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
	const origin = "git@github.com:Xsxdot/handoff.git"
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
	const origin = "git@github.com:Xsxdot/handoff.git"
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
	repo := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/tk.git")

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:Xsxdot/handoff.git", Path: repo})
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
		OriginURL: "git@github.com:Xsxdot/handoff.git", Path: repo})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want errors.Is(..., ErrRepoUnusable)", err)
	}
}

// TestRegisterProjectDuplicateProject 验证同一项目重复登记被拒，且报文指向已有位置。
func TestRegisterProjectDuplicateProject(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:Xsxdot/handoff.git"
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

// TestRegisterProjectIdempotentSamePath 验证同一项目同一路径重复登记是幂等的：
// 第二次调用成功并返回与首次完全一致的行，位置表不新增行。
func TestRegisterProjectIdempotentSamePath(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("首次登记: %v", err)
	}
	second, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("同一项目同一路径重复登记应幂等成功，got %v", err)
	}
	if second.ProjectID != first.ProjectID || second.Name != first.Name || second.Path != first.Path {
		t.Fatalf("幂等返回应与首次登记一致:\n first=%+v\nsecond=%+v", first, second)
	}
	if second.Status != projectStatusOK {
		t.Fatalf("幂等返回的 Status 应为 %q，got %q", projectStatusOK, second.Status)
	}
	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("位置表应有且只有 1 行，got %d", len(locs))
	}
}

// TestRegisterProjectIdempotentLinkedWorktree 验证用 linked worktree 路径重复登记
// 已登记的主仓会幂等成功：归并后路径等于主仓那行，返回主仓位置，表仍只有 1 行。
func TestRegisterProjectIdempotentLinkedWorktree(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:Xsxdot/handoff.git"
	main := initGitRepoWithOrigin(t, origin)
	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: main})
	if err != nil {
		t.Fatalf("登记主仓: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat/x", wt)

	second, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: wt})
	if err != nil {
		t.Fatalf("linked worktree 路径重复登记应幂等成功，got %v", err)
	}
	if second.ProjectID != first.ProjectID || second.Name != first.Name || second.Path != first.Path {
		t.Fatalf("幂等返回应指向主仓登记:\n first=%+v\nsecond=%+v", first, second)
	}
	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("位置表应有且只有 1 行，got %d", len(locs))
	}
}

// TestRegisterProjectIdempotentCloneForm 验证无 path（clone 形态）对已登记项目
// 重复登记会幂等返回已有行，且**根本不触发 clone**：origin 指向一个必然 clone
// 失败的位置（不存在的本地目录），若短路发生在 clone 之前就不会去碰它。
func TestRegisterProjectIdempotentCloneForm(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "/nonexistent/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("登记已有目录: %v", err)
	}
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root

	second, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin})
	if err != nil {
		t.Fatalf("无 path 的重复登记应幂等成功（不应触发 clone），got %v", err)
	}
	if second.ProjectID != first.ProjectID || second.Name != first.Name || second.Path != first.Path {
		t.Fatalf("幂等返回应与首次登记一致:\n first=%+v\nsecond=%+v", first, second)
	}
	dest := filepath.Join(root, "handoff")
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("幂等短路不应创建克隆落点 %s（stat err=%v）", dest, err)
	}
	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("位置表应有且只有 1 行，got %d", len(locs))
	}
}

// TestRegisterProjectNameCollisionFallsBack 验证不同项目撞名字时落到 name-2。
func TestRegisterProjectNameCollisionFallsBack(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	a := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/handoff.git")
	if _, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:Xsxdot/handoff.git", Path: a}); err != nil {
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

// TestRegisterProjectClaimExistingDest 验证克隆落点已存在且就是本项目时**认领**
// 成功：直接登记不 clone。origin 指向一个必然 clone 失败的位置（不存在的本地目录），
// 成功本身即证明认领路径没去 clone（project rm 只删登记不动磁盘，这是「rm 后再派发
// → 自动重登记」成立的机制）。
func TestRegisterProjectClaimExistingDest(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "/nonexistent/handoff.git"
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root
	dest := filepath.Join(root, "handoff")
	repo := initGitRepoIn(t, dest)
	gitAt(t, repo, "remote", "add", "origin", origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin})
	if err != nil {
		t.Fatalf("落点已存在且是本项目时应认领成功，got %v", err)
	}
	want, _ := filepath.EvalSymlinks(dest)
	got, _ := filepath.EvalSymlinks(loc.Path)
	if got != want {
		t.Fatalf("认领后 Path = %q, want %q", loc.Path, dest)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Fatalf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	locs, err := st.ListProjectLocations()
	if err != nil {
		t.Fatalf("ListProjectLocations: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("位置表应有且只有 1 行，got %d", len(locs))
	}
}

// TestRegisterProjectClaimRejectsNonRepoDest 验证落点已存在但不是 git 仓库（普通目录）
// 时认领失败，保持 409，报文带上落点路径。
func TestRegisterProjectClaimRejectsNonRepoDest(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:Xsxdot/handoff.git"
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root
	dest := filepath.Join(root, "handoff")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("建占位目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写占位文件: %v", err)
	}

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectAlreadyExists)", err)
	}
	if !strings.Contains(err.Error(), dest) {
		t.Errorf("报文应含落点路径 %q: %q", dest, err.Error())
	}
}

// TestRegisterProjectClaimRejectsForeignRepoDest 验证落点已存在且是**另一个项目**
// 的仓库时认领失败，保持 409，报文同时给出两边的项目名。
func TestRegisterProjectClaimRejectsForeignRepoDest(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	root := filepath.Join(t.TempDir(), "repos")
	m.cfg.RepoRoot = root
	// 落点是根目录名由请求 origin 派生出的 root/handoff，在那里放一份 origin
	// 指向另一个项目（tk）的仓库。
	dest := filepath.Join(root, "handoff")
	repo := initGitRepoIn(t, dest)
	gitAt(t, repo, "remote", "add", "origin", "git@github.com:Xsxdot/tk.git")

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:Xsxdot/handoff.git"})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want errors.Is(..., ErrProjectAlreadyExists)", err)
	}
	for _, want := range []string{"tk", "handoff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文应同时给出落点项目与请求项目，%q 未含 %q", err.Error(), want)
		}
	}
}

// TestUnregisterProjectRejectsBusy 验证仓库仍被活跃任务占用时拒绝注销。
func TestUnregisterProjectRejectsBusy(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:Xsxdot/handoff.git"
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
