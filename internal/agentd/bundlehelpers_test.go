// bundlehelpers_test.go —— bundle handler 测试共用的 git 夹具（B233.15）。
//
// 职责：建带分支的临时仓库与取分支 sha，供 bundle HTTP handler / e2e 测试使用。
// 边界：与 internal/workspace/bundle_test.go 的同名夹具各自保留一份（测试夹具不跨包）。
package agentd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newBundleRepo 建一个真 git 仓库：base 提交在 main，另有一个 feat/x 分支多一个提交。
// 返回仓库路径与 base 提交的完整 sha。
func newBundleRepo(t *testing.T) (repo, baseSHA string) {
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
	baseSHA = run("rev-parse", "HEAD")
	run("checkout", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("写 work.txt: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "work")
	run("checkout", "main")
	return repo, baseSHA
}

// headSHAForTest 取某分支的完整 sha。
func headSHAForTest(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// bundleHeads 读出 bundle 里携带的 ref → sha 映射。
func bundleHeads(t *testing.T, path string) map[string]string {
	t.Helper()
	out, err := exec.Command("git", "bundle", "list-heads", path).Output()
	if err != nil {
		t.Fatalf("git bundle list-heads: %v", err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			m[f[1]] = f[0]
		}
	}
	return m
}
