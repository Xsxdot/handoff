package agentd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/store"
)

// initBareOrigin 建一个可 clone 的裸仓库，返回其路径。
func initBareOrigin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

// initWorkRepo 建一个带 origin 且有一个提交的工作仓库，返回其路径。
func initWorkRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-m", "init")
	return dir
}

// TestRepoOriginURL 验证能从仓库读出 origin。
func TestRepoOriginURL(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	got, err := repoOriginURL(context.Background(), repo)
	if err != nil {
		t.Fatalf("repoOriginURL: %v", err)
	}
	if got != origin {
		t.Fatalf("origin = %q, want %q", got, origin)
	}
}

// TestRepoOriginURLNoRemote 验证没有 origin 的仓库报可读错误。
func TestRepoOriginURLNoRemote(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := repoOriginURL(context.Background(), dir); !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
}

// TestRepoNameFromURL 验证登记名的缺省派生。
func TestRepoNameFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:xushixin/handoff.git":     "handoff",
		"https://github.com/xushixin/handoff":     "handoff",
		"https://github.com/xushixin/handoff.git": "handoff",
		"/tmp/whatever/origin.git":                "origin",
	}
	for in, want := range cases {
		if got := repoNameFromURL(in); got != want {
			t.Errorf("repoNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegisterExistingReadsOriginFromDisk 验证形态一：
// origin 由 agentd 在执行机上现读，登记名可省。
func TestRegisterExistingReadsOriginFromDisk(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	got, err := m.RegisterRepo(context.Background(), RegisterRepoReq{Path: repo})
	if err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if got.OriginURL != origin {
		t.Fatalf("OriginURL = %q, want 现读的 %q", got.OriginURL, origin)
	}
	if got.Name != "origin" {
		t.Fatalf("Name = %q, want 由 origin 末段派生的 %q", got.Name, "origin")
	}
}

// TestRegisterExistingRejectsNonRepo 验证非 git 路径拒绝登记，不留空壳。
func TestRegisterExistingRejectsNonRepo(t *testing.T) {
	m := newRepoTestManager(t)
	_, err := m.RegisterRepo(context.Background(), RegisterRepoReq{Name: "x", Path: t.TempDir()})
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want ErrRepoUnusable", err)
	}
	list, _ := m.ListRepos(context.Background())
	if len(list) != 0 {
		t.Fatalf("拒绝后不应留下登记，got %+v", list)
	}
}

// TestCloneAndRegister 验证形态二：clone 成功后才落库。
func TestCloneAndRegister(t *testing.T) {
	origin := initBareOrigin(t)
	work := initWorkRepo(t, origin)
	if out, err := exec.Command("git", "-C", work, "push", "origin", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("push: %v: %s", err, out)
	}
	m := newRepoTestManager(t)
	dest := filepath.Join(t.TempDir(), "landed")
	got, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "landed", Path: dest, URL: origin, Clone: true})
	if err != nil {
		t.Fatalf("RegisterRepo clone: %v", err)
	}
	if got.Path != dest {
		t.Fatalf("Path = %q, want %q", got.Path, dest)
	}
	if err := EnsureRepoUsable(context.Background(), dest); err != nil {
		t.Fatalf("clone 出来的目录不是可用仓库: %v", err)
	}
}

// TestCloneRefusesExistingPath 验证落点已存在时拒绝，绝不覆盖。
func TestCloneRefusesExistingPath(t *testing.T) {
	origin := initBareOrigin(t)
	m := newRepoTestManager(t)
	dest := t.TempDir() // 已存在
	_, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "x", Path: dest, URL: origin, Clone: true})
	if !errors.Is(err, ErrRepoAlreadyExists) {
		t.Fatalf("err = %v, want ErrRepoAlreadyExists", err)
	}
}

// TestCloneFailureLeavesNoRegistration 验证安全边界 3：
// clone 失败时登记表里不得有残留记录。
func TestCloneFailureLeavesNoRegistration(t *testing.T) {
	m := newRepoTestManager(t)
	dest := filepath.Join(t.TempDir(), "nope")
	_, err := m.RegisterRepo(context.Background(), RegisterRepoReq{
		Name: "x", Path: dest, URL: filepath.Join(t.TempDir(), "does-not-exist.git"), Clone: true})
	if err == nil {
		t.Fatal("clone 不存在的 URL 应当失败")
	}
	list, _ := m.ListRepos(context.Background())
	if len(list) != 0 {
		t.Fatalf("clone 失败后不应留下登记，got %+v", list)
	}
}

// TestListReposReportsStatus 验证 ls 现场探测实际状态（漂移可见化）。
func TestListReposReportsStatus(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	if _, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "ok", Path: repo}); err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(list) != 1 || list[0].Status == repoStatusOK {
		t.Fatalf("路径被删后 Status 不应为「有效」，got %+v", list)
	}
}

// TestUnregisterKeepsDiskRepo 验证安全边界 4：只删登记，不动磁盘。
func TestUnregisterKeepsDiskRepo(t *testing.T) {
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	m := newRepoTestManager(t)
	if _, err := m.RegisterRepo(context.Background(),
		RegisterRepoReq{Name: "keep", Path: repo}); err != nil {
		t.Fatalf("RegisterRepo: %v", err)
	}
	if err := m.UnregisterRepo(context.Background(), "keep"); err != nil {
		t.Fatalf("UnregisterRepo: %v", err)
	}
	if err := EnsureRepoUsable(context.Background(), repo); err != nil {
		t.Fatalf("注销登记后磁盘仓库被动过了: %v", err)
	}
}

// newRepoTestManager 造一个只够跑仓库登记操作的 Manager（不含 executor/hub）。
func newRepoTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{st: st, cfg: &config.Config{}, log: slog.Default()}
}

// TestValidateRepoName 覆盖登记名的合法性校验：合法名通过，含路径特征字符 /
// . 或 .. 路径段 / 空名 / 纯空白各自被拒且可被 errors.Is 命中哨兵。
func TestValidateRepoName(t *testing.T) {
	valid := []string{"handoff", "my-repo", "a-b", "x.y", "origin"}
	for _, name := range valid {
		if err := validateRepoName(name); err != nil {
			t.Errorf("validateRepoName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{
		"a/b",            // / 会让它被当成路径
		`C:\x`,           // \ 会让它被当成路径
		"a:b",            // : 会让它被当成路径
		"..",             // 逃出 repo_root
		"../../tmp/evil", // 含 .. 路径段
		"",               // 空名
		"   ",            // 纯空白
	}
	for _, name := range invalid {
		if err := validateRepoName(name); !errors.Is(err, errBadDispatchRequest) {
			t.Errorf("validateRepoName(%q) err = %v, want errors.Is(errBadDispatchRequest)", name, err)
		}
	}
}

// TestRepoNameFromURLAlwaysPassesValidation 验证派生名同样过得了校验——派生名
// 与人工指定名走同一条 validateRepoName，不因来源不同而豁免。
func TestRepoNameFromURLAlwaysPassesValidation(t *testing.T) {
	// 正常 URL 派生出的名字必须能通过校验（否则合法的 --clone 会被误拒）
	for _, url := range []string{
		"git@github.com:xushixin/handoff.git",
		"https://github.com/xushixin/handoff",
		"/tmp/whatever/origin.git",
	} {
		if err := validateRepoName(repoNameFromURL(url)); err != nil {
			t.Errorf("repoNameFromURL(%q)=%q 应通过校验，got %v", url, repoNameFromURL(url), err)
		}
	}
	// 末段为空或诡异的 URL 派生出的名字同样被拒，而不是放行
	for _, url := range []string{
		"git@github.com:",         // 末段为空
		"https://github.com/..",   // 派生为 ..
		"https://github.com/.",    // 派生为 .
		"https://github.com/a\\b", // 派生名含 \（looksLikePath 特征字符）
	} {
		if err := validateRepoName(repoNameFromURL(url)); !errors.Is(err, errBadDispatchRequest) {
			t.Errorf("repoNameFromURL(%q)=%q 应被拒，got %v", url, repoNameFromURL(url), err)
		}
	}
}

// TestCloneAndRegisterRejectsPathTraversalName 验证形态二派生名非法时在 clone
// 之前就被拒，绝不落地任何东西（安全边界 2/3 的组合：不建目录、不落库）。
func TestCloneAndRegisterRejectsPathTraversalName(t *testing.T) {
	origin := initBareOrigin(t)
	m := newRepoTestManager(t)
	// name 由 URL 末段派生；让派生名 = ".."，dest 会算成 repo_root/.. 逃出目录
	url := origin + "/.."
	_, err := m.RegisterRepo(context.Background(), RegisterRepoReq{URL: url, Clone: true})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("err = %v, want errBadDispatchRequest", err)
	}
	list, _ := m.ListRepos(context.Background())
	if len(list) != 0 {
		t.Fatalf("拒绝后不应留下登记，got %+v", list)
	}
}
