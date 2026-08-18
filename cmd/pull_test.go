package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newPullRepo 建一个本地仓库（只有 base 提交），返回路径与 base sha。
func newPullRepo(t *testing.T) (repo, baseSHA string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("写 base.txt: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	return repo, run("rev-parse", "HEAD")
}

// newCommit 在 repo 里新增一个提交，返回完整 sha。
func newCommit(t *testing.T, repo, file, content string) string {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s: %v", file, err)
	}
	run("add", ".")
	run("commit", "-m", file)
	return run("rev-parse", "HEAD")
}

// hasLocalCommit：本地有的报 true，本地没有的报 false，非法输入报 false 而不是崩。
func TestHasLocalCommit(t *testing.T) {
	repo, base := newPullRepo(t)
	if !hasLocalCommit(context.Background(), repo, base) {
		t.Error("本地确实有 base，应报 true")
	}
	if hasLocalCommit(context.Background(), repo, "0123456789abcdef0123456789abcdef01234567") {
		t.Error("本地没有的 sha 应报 false")
	}
	if hasLocalCommit(context.Background(), repo, "") {
		t.Error("空 sha 应报 false")
	}
	if hasLocalCommit(context.Background(), repo, "--upload-pack=x") {
		t.Error("- 前缀应报 false，不许当 git 选项送进去")
	}
}

// syncViaBundle 把 HTTP 错误如实往上抛：不自己吞掉，也不自己降级。
//
// 注意：这一层**不守回落纪律**——syncViaBundle 结构上不可能去 ssh，对它断言
// 「没有 ssh 痕迹」是恒真的。回落纪律（仅 404 回落）由
// TestSyncTaskBranchDoesNotFallBackOn500 守在 syncTaskBranch 上。
func TestSyncViaBundleReportsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"git 炸了"}`))
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	_, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if err == nil {
		t.Fatal("500 必须报错")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误应带上 500 的事实，实得 %v", err)
	}
}

// 204：合成「已是最新」的结果，不报错、不 fetch。
func TestSyncViaBundleEmptyRange(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	res, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if err != nil {
		t.Fatalf("204 不该是错误：%v", err)
	}
	if res.Created || res.Commits != 0 {
		t.Errorf("空区间应是「已是最新」，实得 %+v", res)
	}
	if res.Branch != "feat/x" {
		t.Errorf("Branch 应回填为 feat/x，实得 %q", res.Branch)
	}
}

// 404：原样返回 client.ErrBundleUnsupported，由上层决定回落。
// syncViaBundle 自己**不**做回落——职责边界。
func TestSyncViaBundlePropagatesUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	_, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if !errorsIsBundleUnsupported(err) {
		t.Fatalf("404 应原样传出 ErrBundleUnsupported，实得 %v", err)
	}
}

// 承重：204 + 对端给了分支 tip → 本地分支引用真的被建出来，Result.Created 为 true。
func TestSyncViaBundleEmptyRangeCreatesLocalRef(t *testing.T) {
	repo, base := newPullRepo(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Handoff-Branch-Head", base)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	res, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if err != nil {
		t.Fatalf("204 不该是错误：%v", err)
	}
	if !res.Created {
		t.Error("204 建出本地引用时应 Created=true")
	}
	if got := gitRevParse(t, repo, "feat/x"); got != base {
		t.Errorf("feat/x 应指向 %s，实得 %s", base, got)
	}
}

// 兜底：204 + 对端**没**给分支 tip → 不报错、不建 ref，返回 Result{Branch: branch}。
func TestSyncViaBundleEmptyRangeWithoutHead(t *testing.T) {
	repo, _ := newPullRepo(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	res, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", "", "feat/x", repo)
	if err != nil {
		t.Fatalf("无头时不该报错：%v", err)
	}
	if res.Branch != "feat/x" || res.Created {
		t.Errorf("应返回 Result{Branch: feat/x}，实得 %+v", res)
	}
	if got := gitRevParseQuiet(t, repo, "feat/x"); got != "" {
		t.Errorf("无头时不该建 ref，实得 %q", got)
	}
}

func errorsIsBundleUnsupported(err error) bool {
	return errors.Is(err, client.ErrBundleUnsupported)
}

// gitRevParse 取 ref 的 sha，ref 不存在时 t.Fatal。
func gitRevParse(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// gitRevParseQuiet 取 ref 的 sha；ref 不存在时返回空串。
func gitRevParseQuiet(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", ref).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// 承重反面断言：500 时 syncTaskBranch 必须报错，且**不得**回落 ssh。
//
// 为什么必须打在 syncTaskBranch 上而不是 syncViaBundle 上：回落分支根本不在
// syncViaBundle 里——它结构上就不可能去 ssh，对它断言「没有 ssh 痕迹」是恒真的，
// 把「其它错误也回落」写回 syncTaskBranch 照样能过。
//
// 判据不依赖 ssh 的报错文案：真回落了的话返回的错误要么是 localsync.Fetch 的
// fetch 错误、要么是 nil，两种都不含「500」。所以「err 非空且带 500」这一条
// 就足以把回落挡在外面，且不需要真的去拨一个 ssh 主机（那会让用例依赖本机
// 有没有跑 sshd，是不稳定判据）。
func TestSyncTaskBranchDoesNotFallBackOn500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"git 炸了"}`))
	}))
	defer ts.Close()

	repo, base := newPullRepo(t)
	t.Chdir(repo)

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	cfg.Token = "tok"
	cfg.Targets = map[string]config.Target{
		"box": {Addr: strings.TrimPrefix(ts.URL, "http://"), Token: "tok", User: "nobody"},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("准备配置: %v", err)
	}
	old := configPath
	configPath = cfgPath
	t.Cleanup(func() { configPath = old })

	_, err := syncTaskBranch(context.Background(), &proto.Task{
		ID: "task-1", Target: "box", RepoPath: "/remote/repo",
		Branch: "feat/x", BaseCommit: base,
	})
	if err == nil {
		t.Fatal("500 必须报错，不得回落 ssh 之后当成功")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误应是那次 500，实得 %v（不含 500 即说明走了回落）", err)
	}
}

// ensureLocalBranch：本地无该分支 → 建出来且 created=true。
func TestEnsureLocalBranchCreates(t *testing.T) {
	repo, base := newPullRepo(t)
	created, err := ensureLocalBranch(context.Background(), repo, "feat/x", base)
	if err != nil {
		t.Fatalf("ensureLocalBranch: %v", err)
	}
	if !created {
		t.Error("本地无分支时应 created=true")
	}
	got := strings.TrimSpace(gitRevParse(t, repo, "feat/x"))
	if got != base {
		t.Errorf("feat/x 应指向 %s，实得 %s", base, got)
	}
}

// ensureLocalBranch：已存在且同 sha → created=false，不报错。
func TestEnsureLocalBranchAlreadyAtHead(t *testing.T) {
	repo, base := newPullRepo(t)
	if _, err := ensureLocalBranch(context.Background(), repo, "feat/x", base); err != nil {
		t.Fatalf("首次创建: %v", err)
	}
	created, err := ensureLocalBranch(context.Background(), repo, "feat/x", base)
	if err != nil {
		t.Fatalf("重复调用不该报错: %v", err)
	}
	if created {
		t.Error("已指向同 sha 时应 created=false")
	}
}

// ensureLocalBranch：已存在但指向别处 → 报错且报文带两个 sha，绝不覆盖。
func TestEnsureLocalBranchDiverged(t *testing.T) {
	repo, base := newPullRepo(t)
	work := newCommit(t, repo, "work.txt", "work\n")
	if _, err := ensureLocalBranch(context.Background(), repo, "feat/x", base); err != nil {
		t.Fatalf("建 base 分支: %v", err)
	}
	_, err := ensureLocalBranch(context.Background(), repo, "feat/x", work)
	if err == nil {
		t.Fatal("分支指向别处时必须报错，不许覆盖")
	}
	if !strings.Contains(err.Error(), base) || !strings.Contains(err.Error(), work) {
		t.Errorf("报文应带两个 sha，实得 %v", err)
	}
}

// ensureLocalBranch：head 对象本地没有 → 报错，且不建悬空引用。
func TestEnsureLocalBranchHeadMissing(t *testing.T) {
	repo, _ := newPullRepo(t)
	absent := "0123456789abcdef0123456789abcdef01234567"
	if _, err := ensureLocalBranch(context.Background(), repo, "feat/x", absent); err == nil {
		t.Fatal("head 不在本地必须报错")
	}
	if got := gitRevParseQuiet(t, repo, "feat/x"); got != "" {
		t.Errorf("不应建出悬空引用 feat/x，实得 %q", got)
	}
}
