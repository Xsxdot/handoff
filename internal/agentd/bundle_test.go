package agentd

import (
	"context"
	"errors"
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

// 有 have 时生成薄包：文件存在、非空。
func TestBundleRangeThin(t *testing.T) {
	repo, base := newBundleRepo(t)
	path, err := BundleRange(context.Background(), repo, base, "feat/x")
	if err != nil {
		t.Fatalf("BundleRange: %v", err)
	}
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("生成的包不存在: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("薄包不该是空文件")
	}
}

// 无 have 时生成全量包：也必须成功，且比薄包大（含 base 提交的对象）。
func TestBundleRangeFull(t *testing.T) {
	repo, base := newBundleRepo(t)
	thin, err := BundleRange(context.Background(), repo, base, "feat/x")
	if err != nil {
		t.Fatalf("薄包: %v", err)
	}
	defer os.Remove(thin)
	full, err := BundleRange(context.Background(), repo, "", "feat/x")
	if err != nil {
		t.Fatalf("全量包: %v", err)
	}
	defer os.Remove(full)
	thinFI, _ := os.Stat(thin)
	fullFI, _ := os.Stat(full)
	if fullFI.Size() <= thinFI.Size() {
		t.Errorf("全量包应大于薄包，实得 full=%d thin=%d", fullFI.Size(), thinFI.Size())
	}
}

// 空区间是**预期形态**，必须是 ErrEmptyRange 而不是 git 的失败原文。
// 这条是承重的：不识别它，第二次 pull 就变成一个 500。
func TestBundleRangeEmpty(t *testing.T) {
	repo, _ := newBundleRepo(t)
	head := headSHAForTest(t, repo, "feat/x")
	_, err := BundleRange(context.Background(), repo, head, "feat/x")
	if !errors.Is(err, ErrEmptyRange) {
		t.Fatalf("空区间应返回 ErrEmptyRange，实得 %v", err)
	}
}

// have 在仓库里不存在时响亮失败，不许悄悄退回全量。
func TestBundleRangeHaveMissing(t *testing.T) {
	repo, _ := newBundleRepo(t)
	absent := "0123456789abcdef0123456789abcdef01234567"
	_, err := BundleRange(context.Background(), repo, absent, "feat/x")
	if !errors.Is(err, ErrHaveMissing) {
		t.Fatalf("have 不存在应返回 ErrHaveMissing，实得 %v", err)
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("报文必须带上那个 sha 才能排障，实得 %q", err.Error())
	}
}

// branch / have 以 - 开头会被 git 当成选项：参数注入面，一律拒绝。
func TestBundleRangeRejectsDashPrefix(t *testing.T) {
	repo, base := newBundleRepo(t)
	if _, err := BundleRange(context.Background(), repo, base, "--upload-pack=x"); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("- 前缀分支名应被拒，实得 %v", err)
	}
	if _, err := BundleRange(context.Background(), repo, "--foo", "feat/x"); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("- 前缀 have 应被拒，实得 %v", err)
	}
	if _, err := BundleRange(context.Background(), repo, base, ""); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("空分支名应被拒，实得 %v", err)
	}
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
