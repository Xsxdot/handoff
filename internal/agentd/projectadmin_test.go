package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
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

// newPatchTestEnv 搭一个带 manager 的完整 HTTP 环境（PATCH 项目测试专用）。
func newPatchTestEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: testToken}
	env := newTestAgentdEnvWithCfg(t, cfg, logger)
	mgr := NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"fake": fake.New(nil)}, cfg, nil, nil, newTestGate(t), logger)
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env
}

// patchJSON 发起带 token 的 PATCH，断言状态码；out 非 nil 时把响应体解码到 out。
// 风格与 w3a_testhelpers_test.go 的 getJSON 对齐；body 可为 JSON 字符串或可序列化值。
func patchJSON(t *testing.T, env *testAgentdEnv, path string, body any, wantStatus int, out any) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(http.MethodPatch, env.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	defer resp.Body.Close()
	if got := resp.StatusCode; got != wantStatus {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PATCH %s = %d，want %d（body: %s）", path, got, wantStatus, b)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("PATCH %s 解码: %v", path, err)
		}
	}
}

// TestProjectPatchRenames 改名走通，响应里 project_id 不变。
func TestProjectPatchRenames(t *testing.T) {
	env := newPatchTestEnv(t)
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo}); err != nil {
		t.Fatalf("登记: %v", err)
	}
	var loc proto.ProjectLocation
	patchJSON(t, env, "/api/projects/handoff",
		map[string]string{"new_name": "handoff-renamed"}, http.StatusOK, &loc)
	if loc.Name != "handoff-renamed" {
		t.Fatalf("Name = %q, want handoff-renamed", loc.Name)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Fatalf("ProjectID = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
}

// TestProjectPatchChangesPath 改 path 成功：响应里 project_id 不变，Path 指向
// 新目录。repo2 本身是主仓，归并主工作树后就是它自己；断言兼容 EvalSymlinks
// 后的等价性（照 TestRegisterProjectExisting 的风格）。
func TestProjectPatchChangesPath(t *testing.T) {
	env := newPatchTestEnv(t)
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo}); err != nil {
		t.Fatalf("登记: %v", err)
	}
	repo2 := initGitRepoWithOrigin(t, origin)
	var loc proto.ProjectLocation
	patchJSON(t, env, "/api/projects/handoff",
		map[string]string{"path": repo2}, http.StatusOK, &loc)
	if loc.Path == "" {
		t.Fatal("响应 Path 不应为空")
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Fatalf("ProjectID = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	want, _ := filepath.EvalSymlinks(repo2)
	got, _ := filepath.EvalSymlinks(loc.Path)
	if got != want {
		t.Fatalf("改 path 后应指向新目录: got %s, want %s", loc.Path, repo2)
	}
}

// TestProjectPatchRejectsDifferentOrigin 是本 task 的正身：把 path 改到一个 origin
// 不同的仓库 → 400，且报文说明「那是另一个项目」。没有这条校验，「编辑 path」就成
// 了一条不声不响把登记指向另一个仓库的路径：project_id 还是旧的，磁盘上却是别的项目。
func TestProjectPatchRejectsDifferentOrigin(t *testing.T) {
	env := newPatchTestEnv(t)
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo}); err != nil {
		t.Fatalf("登记: %v", err)
	}
	tk := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/tk.git")
	var resp struct {
		Error string `json:"error"`
	}
	patchJSON(t, env, "/api/projects/handoff",
		map[string]string{"path": tk}, http.StatusBadRequest, &resp)
	if !strings.Contains(resp.Error, "另一个项目") && !strings.Contains(resp.Error, "请注销后重新添加") {
		t.Fatalf("报文应说明那是另一个项目，got %q", resp.Error)
	}
}

// TestProjectPatchDuplicateName 撞名 → 409。
func TestProjectPatchDuplicateName(t *testing.T) {
	env := newPatchTestEnv(t)
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo}); err != nil {
		t.Fatalf("登记 handoff: %v", err)
	}
	tk := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/tk.git")
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: "git@github.com:Xsxdot/tk.git", Name: "other", Path: tk}); err != nil {
		t.Fatalf("登记 other: %v", err)
	}
	var resp struct {
		Error string `json:"error"`
	}
	patchJSON(t, env, "/api/projects/handoff",
		map[string]string{"new_name": "other"}, http.StatusConflict, &resp)
}

// TestProjectPatchEmptyBody 两个字段都空 → 400。
func TestProjectPatchEmptyBody(t *testing.T) {
	env := newPatchTestEnv(t)
	const origin = "git@github.com:Xsxdot/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)
	if _, err := env.mgr.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo}); err != nil {
		t.Fatalf("登记: %v", err)
	}
	patchJSON(t, env, "/api/projects/handoff", `{}`, http.StatusBadRequest, nil)
}

// TestProjectPatchNotFound 不存在的名字 → 404。
func TestProjectPatchNotFound(t *testing.T) {
	env := newPatchTestEnv(t)
	var resp struct {
		Error string `json:"error"`
	}
	patchJSON(t, env, "/api/projects/no-such-project",
		map[string]string{"new_name": "x"}, http.StatusNotFound, &resp)
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

// TestRegisterProjectExistingInfersOrigin 验证 path 指向已有仓且请求不带
// origin_url 时，agentd 现读 origin 完成登记（Web「只填 path」主路径）。
func TestRegisterProjectExistingInfersOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{Path: repo})
	if err != nil {
		t.Fatalf("RegisterProject(无 origin): %v", err)
	}
	if loc.OriginURL != origin {
		t.Errorf("OriginURL = %q, want 现读的 %q", loc.OriginURL, origin)
	}
	if loc.ProjectID != projectid.FromOrigin(origin) {
		t.Errorf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(origin))
	}
	if loc.Name != "handoff" {
		t.Errorf("name = %q, want handoff", loc.Name)
	}
}

// TestRegisterProjectRejectsEmptyOriginAndEmptyPath 验证既无 path 也无 origin 时
// 无法确定身份与落点。
func TestRegisterProjectRejectsEmptyOriginAndEmptyPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
}

// TestRegisterProjectClonesToExplicitPath 验证 path 不存在且带 origin 时
// clone 到调用方指定的 path（不是 repo_root/<name>）。
// 造 clone 源的手法与 TestRegisterProjectClonesWhenNoPath 相同：本地目录当源，不依赖网络。
func TestRegisterProjectClonesToExplicitPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	// 父目录 workdir 也不存在，验证实现会 MkdirAll。
	dest := filepath.Join(t.TempDir(), "workdir", "my-handoff")

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src,
		Name:      "my-handoff",
		Path:      dest,
	})
	if err != nil {
		t.Fatalf("RegisterProject(clone-to-path): %v", err)
	}
	want, _ := filepath.Abs(dest)
	if loc.Path != want {
		t.Fatalf("落点 = %q, want %q", loc.Path, want)
	}
	if loc.Name != "my-handoff" {
		t.Errorf("name = %q, want my-handoff", loc.Name)
	}
	if loc.ProjectID != projectid.FromOrigin(src) {
		t.Errorf("project_id = %q, want %q", loc.ProjectID, projectid.FromOrigin(src))
	}
	if _, err := os.Stat(filepath.Join(want, ".git")); err != nil {
		t.Fatalf("落点应是一个克隆好的仓库: %v", err)
	}
}

// TestRegisterProjectMissingPathRequiresOrigin 验证 path 不存在且无 origin → 400。
func TestRegisterProjectMissingPathRequiresOrigin(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	dest := filepath.Join(t.TempDir(), "nope")
	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{Path: dest})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
}

// TestRegisterProjectRejectsRelativePath 验证相对 path 被入口拦下。
//
// 为什么必须拦而不是 filepath.Abs 兜底：Abs 的基准是 agentd 进程的 cwd，
// 调用方（尤其 Web 表单和跨机那一跳）根本不知道那是哪个目录；更要命的是
// gitRun 以 dest 的父目录为 cwd，相对 dest 会被 git 再解析一次，克隆落点
// 与落库路径就此分叉，留下一条指向不存在路径的死记录。
func TestRegisterProjectRejectsRelativePath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "relproj", Path: "workdir/relproj",
	})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
	if !strings.Contains(err.Error(), "绝对路径") {
		t.Errorf("报文 = %q, want 含「绝对路径」（人要看得懂怎么改）", err.Error())
	}
}

// TestRegisterProjectRejectsTildePath 验证 ~ 开头的 path 被拦下。
// Go 不做 ~ 展开——不拦的话 MkdirAll 会造出一个字面量 ~ 目录。
func TestRegisterProjectRejectsTildePath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "tildeproj", Path: "~/code/tildeproj",
	})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
	if _, serr := os.Stat("~"); serr == nil {
		t.Errorf("cwd 下出现了字面量 ~ 目录——说明请求走到了 MkdirAll")
	}
}

// TestRegisterProjectNormalizesDirtyAbsPath 验证绝对路径里的冗余段被 Clean 掉，
// 落库的是归一化后的路径（同一目录不会因为写法不同登记成两条）。
func TestRegisterProjectNormalizesDirtyAbsPath(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	const origin = "git@github.com:xushixin/handoff.git"
	repo := initGitRepoWithOrigin(t, origin)

	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		Path: filepath.Join(repo, "sub", ".."),
	})
	if err != nil {
		t.Fatalf("RegisterProject(含 .. 的绝对路径): %v", err)
	}
	if loc.OriginURL != origin {
		t.Errorf("OriginURL = %q, want %q", loc.OriginURL, origin)
	}
}

// TestRegisterProjectCloneToPathRejectsDifferentLocation 验证同一项目已登记在
// 别处时，clone-to-path 报 409 并指向已有位置——而不是静默返回别处那一行、
// 让调用方以为自己填的落点生效了。
func TestRegisterProjectCloneToPathRejectsDifferentLocation(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)

	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: filepath.Join(t.TempDir(), "first"),
	})
	if err != nil {
		t.Fatalf("首次 clone-to-path: %v", err)
	}

	other := filepath.Join(t.TempDir(), "second")
	_, err = m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: other,
	})
	if !errors.Is(err, ErrProjectAlreadyExists) {
		t.Fatalf("err = %v, want ErrProjectAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), first.Path) {
		t.Errorf("报文 = %q, want 含已有位置 %q", err.Error(), first.Path)
	}
	if _, serr := os.Stat(other); serr == nil {
		t.Errorf("被拒的请求不该在 %s 上留下任何东西", other)
	}
}

// TestRegisterProjectCloneToPathIdempotentSameLocation 验证同项目 + 同落点仍幂等：
// 落点被人手动 rm 掉、位置表还留着那一行时，重复登记不该被 409 打断
// （自动登记链靠这条不断）。
func TestRegisterProjectCloneToPathIdempotentSameLocation(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	src := initGitRepo(t)
	dest := filepath.Join(t.TempDir(), "proj")

	first, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: dest,
	})
	if err != nil {
		t.Fatalf("首次 clone-to-path: %v", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		t.Fatalf("清掉磁盘上的克隆: %v", err)
	}

	again, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: src, Name: "proj-a", Path: dest,
	})
	if err != nil {
		t.Fatalf("同落点重复登记应幂等: %v", err)
	}
	if again.Path != first.Path {
		t.Errorf("Path = %q, want %q", again.Path, first.Path)
	}
}

// TestFirstMissingAncestor 验证助手找的是「MkdirAll 会从哪一层开始造」。
func TestFirstMissingAncestor(t *testing.T) {
	base := t.TempDir()
	if got := firstMissingAncestor(base); got != "" {
		t.Errorf("已存在的目录应返回空串，got %q", got)
	}
	want := filepath.Join(base, "a")
	if got := firstMissingAncestor(filepath.Join(base, "a", "b", "c")); got != want {
		t.Errorf("firstMissingAncestor = %q, want %q", got, want)
	}
}

// TestCloneToPathCleansUpOnFailure 验证 clone 失败时本次新建的目录被回收，
// 而调用方原本就有的目录一根汗毛不动。
func TestCloneToPathCleansUpOnFailure(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, nil, "fake")
	base := t.TempDir()
	// 不是仓库的目录当 origin：git clone 必失败，且不依赖网络。
	bogus := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(bogus, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := m.RegisterProject(context.Background(), RegisterProjectReq{
		OriginURL: bogus, Name: "proj", Path: filepath.Join(base, "a", "b", "proj"),
	})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
	if _, serr := os.Stat(filepath.Join(base, "a")); serr == nil {
		t.Errorf("clone 失败后 %s 不该留下", filepath.Join(base, "a"))
	}
	if _, serr := os.Stat(base); serr != nil {
		t.Errorf("调用方原本就有的 %s 被误删了: %v", base, serr)
	}
}

// registerWorktreeTestProject 在库里登记一个真仓库，返回登记名。
// 建树接口按登记名寻址，所以用例必须有一条真的位置行，不能只造目录。
func registerWorktreeTestProject(t *testing.T, st *store.Store, repo string) string {
	t.Helper()
	loc := &proto.ProjectLocation{
		ProjectID: "p-worktree-test",
		Name:      "demo",
		Path:      repo,
		OriginURL: "git@github.com:Xsxdot/demo.git",
		CreatedAt: time.Now(),
	}
	if err := st.CreateProjectLocation(loc); err != nil {
		t.Fatalf("登记项目: %v", err)
	}
	return loc.Name
}

// doWorktreeReq 发一条已带 Host 与 Bearer 的请求，返回 recorder。
func doWorktreeReq(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

// TestProjectWorktreeCreateRejectsUnknownProject 验证项目不存在时是 404 而不是 500：
// 500 会让界面把「名字打错了」显示成「服务端炸了」。
func TestProjectWorktreeCreateRejectsUnknownProject(t *testing.T) {
	s, _, _ := newTestServerWithManager(t)
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/nope/worktrees",
		`{"mode":"new_branch","branch":"feat/x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestProjectWorktreeCreateRejectsBadBody 验证坏请求体是 400 且报文说清要什么。
func TestProjectWorktreeCreateRejectsBadBody(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees", "not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestProjectWorktreeCreateOK 验证成功路径：返回的是项目树口径的 Workspace，
// 且落点真的在 <DataDir>/worktrees/manual 下。
func TestProjectWorktreeCreateOK(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees",
		`{"mode":"new_branch","branch":"feat/x","base":"main"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ws proto.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if ws.Branch != "feat/x" {
		t.Fatalf("分支 = %q", ws.Branch)
	}
	wantRoot := ManualWorktreeRoot(filepath.Join(s.conf().DataDir, "worktrees"))
	if !strings.HasPrefix(canonPath(ws.Path), canonPath(wantRoot)) {
		t.Fatalf("落点 %q 不在 %q 下", ws.Path, wantRoot)
	}
}

// TestProjectWorktreeCreateRejectsDuplicateBranch 验证请求类拒绝映射成 400 而非 500。
func TestProjectWorktreeCreateRejectsDuplicateBranch(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	repo := initGitRepo(t)
	gitAt(t, repo, "branch", "feat/dup")
	name := registerWorktreeTestProject(t, st, repo)
	rec := doWorktreeReq(t, s, http.MethodPost, "/api/projects/"+name+"/worktrees",
		`{"mode":"new_branch","branch":"feat/dup","base":"main"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "feat/dup") {
		t.Fatalf("报文应含出问题的分支名: %s", rec.Body.String())
	}
}

// TestProjectBranchesMarksOccupied 验证分支列表把「已被工作树检出」如实标出来——
// 弹层据此置灰，标不出来用户就会选一个必然失败的分支。
func TestProjectBranchesMarksOccupied(t *testing.T) {
	s, _, st := newTestServerWithManager(t)
	name := registerWorktreeTestProject(t, st, initGitRepo(t))
	rec := doWorktreeReq(t, s, http.MethodGet, "/api/projects/"+name+"/branches", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp proto.ProjectBranchesResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(resp.Branches) == 0 {
		t.Fatalf("至少应列出主分支")
	}
	// 主仓自己就检出着默认分支，它必须被标为已占用
	var def *proto.ProjectBranch
	for i := range resp.Branches {
		if resp.Branches[i].Name == resp.Default {
			def = &resp.Branches[i]
		}
	}
	if def == nil || def.Worktree == "" {
		t.Fatalf("默认分支应被主工作树占用, got %+v", resp.Branches)
	}
	if resp.WorktreeRoot == "" {
		t.Fatalf("worktree_root 不能为空，界面要靠它回显落点")
	}
}

// TestProjectBranchesUnknownProject 验证项目不存在时 404。
func TestProjectBranchesUnknownProject(t *testing.T) {
	s, _, _ := newTestServerWithManager(t)
	if rec := doWorktreeReq(t, s, http.MethodGet, "/api/projects/nope/branches", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestProjectNameFromURLHandlesWindowsSeparators 覆盖本地路径 origin 的名字派生。
//
// why 这个用例存在：origin 为 Windows 本地路径（`C:\work\x.git`）时，派生名若不切
// 反斜杠就会是 `\work\x`，撞上 validateProjectName 的「含 / \ : 拒收」，
// 表现为自动登记失败、dispatch 400。
func TestProjectNameFromURLHandlesWindowsSeparators(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"windows 本地路径", `C:\work\probe-origin.git`, "probe-origin"},
		{"windows 本地路径带尾部反斜杠", `C:\work\probe-origin\`, "probe-origin"},
		{"ssh scp 简写（回归）", "git@github.com:Xsxdot/handoff.git", "handoff"},
		{"https（回归）", "https://github.com/Xsxdot/handoff", "handoff"},
		{"https 带尾部斜杠（回归）", "https://github.com/Xsxdot/handoff/", "handoff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := projectNameFromURL(c.in)
			if got != c.want {
				t.Fatalf("projectNameFromURL(%q) = %q，期望 %q", c.in, got, c.want)
			}
			// 派生名必须能过校验，否则自动登记依然会失败
			if err := validateProjectName(got); err != nil {
				t.Fatalf("派生名 %q 未通过 validateProjectName: %v", got, err)
			}
		})
	}
}
