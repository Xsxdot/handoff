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

// 承重反面断言：对端回 500 时必须报错，且**不得**回落 ssh。
//
// 少了这条，把「其它错误也回落」写回去照样能过——那会把一次真失败伪装成
// 「老路也能跑」，正是 spec §6 要守住的东西。
// 判据：ssh 回落必然去拨一个 ssh 主机；测试里那个 target 的 Addr 指向本测试
// 的 httptest 服务器，ssh 过去必然失败且报文里带 ssh 的痕迹。所以断言错误里
// **有 500 的痕迹、没有 ssh 的痕迹**。
func TestSyncViaBundleDoesNotFallBackOn500(t *testing.T) {
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
	if strings.Contains(err.Error(), "ssh") || strings.Contains(err.Error(), "Host key") {
		t.Errorf("500 不该触发 ssh 回落，实得 %v", err)
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

func errorsIsBundleUnsupported(err error) bool {
	return errors.Is(err, client.ErrBundleUnsupported)
}
