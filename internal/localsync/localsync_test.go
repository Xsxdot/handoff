// localsync 测试：在两个本地 git 仓库之间验证「从远程仓库拉任务分支到本地」的
// 全部契约。RemoteURL 用本地路径而不是 host:path——git fetch 对两者走同一条
// 代码路径，用本地路径既真实又不依赖 ssh 环境。
package localsync_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/localsync"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// 身份用 -c 逐次注入，而不是只在 newRepo 里 git config：clone 出来的仓库
	// 不继承源仓库的 user.*，开发机靠全局配置兜住，**干净机器上没有全局配置**，
	// commit 会直接 128 "Author identity unknown"（2026-08-13 CI 实测）
	base := []string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo 造一个带初始提交的仓库。
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// TestFetchBringsTaskBranchLocally 验证任务分支被拉到本地且提交数正确。
func TestFetchBringsTaskBranchLocally(t *testing.T) {
	remote := newRepo(t)
	local := t.TempDir()
	git(t, local, "clone", "-q", remote, ".")

	// 远程上造任务分支与两个提交
	git(t, remote, "checkout", "-q", "-b", "handoff/abc12345")
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(remote, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, remote, "add", n)
		git(t, remote, "commit", "-q", "-m", "add "+n)
	}

	res, err := localsync.Fetch(context.Background(), localsync.Opts{
		LocalRepo: local, RemoteURL: remote, Branch: "handoff/abc12345",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.Created {
		t.Error("本地此前没有该分支，Created 应为 true")
	}
	if got := git(t, local, "rev-parse", "handoff/abc12345"); got == "" {
		t.Error("本地必须出现任务分支")
	}
	// 不得动 HEAD：协调者本地可能正在改别的东西
	if got := git(t, local, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("同步不得切换分支，HEAD = %q", got)
	}
}

// TestFetchReportsNewCommits 验证二次同步只报增量提交数。
func TestFetchReportsNewCommits(t *testing.T) {
	remote := newRepo(t)
	local := t.TempDir()
	git(t, local, "clone", "-q", remote, ".")
	git(t, remote, "checkout", "-q", "-b", "handoff/abc12345")

	opts := localsync.Opts{LocalRepo: local, RemoteURL: remote, Branch: "handoff/abc12345"}
	if _, err := localsync.Fetch(context.Background(), opts); err != nil {
		t.Fatalf("首次 Fetch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(remote, "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, remote, "add", "c.txt")
	git(t, remote, "commit", "-q", "-m", "add c")

	res, err := localsync.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("二次 Fetch: %v", err)
	}
	if res.Created {
		t.Error("分支已存在，Created 应为 false")
	}
	if res.Commits != 1 {
		t.Errorf("增量提交数 = %d，期望 1", res.Commits)
	}
}

// TestFetchRefusesNonFastForward 验证非快进被拒——宁可报错也不能覆盖本地提交。
func TestFetchRefusesNonFastForward(t *testing.T) {
	remote := newRepo(t)
	local := t.TempDir()
	git(t, local, "clone", "-q", remote, ".")
	git(t, remote, "checkout", "-q", "-b", "handoff/abc12345")

	opts := localsync.Opts{LocalRepo: local, RemoteURL: remote, Branch: "handoff/abc12345"}
	if _, err := localsync.Fetch(context.Background(), opts); err != nil {
		t.Fatalf("首次 Fetch: %v", err)
	}
	// 本地在该分支上造一个远程没有的提交，再让远程也走一个不同的提交 → 分叉
	git(t, local, "checkout", "-q", "handoff/abc12345")
	if err := os.WriteFile(filepath.Join(local, "local.txt"), []byte("l"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, local, "add", "local.txt")
	git(t, local, "commit", "-q", "-m", "local only")
	git(t, local, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(remote, "r.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, remote, "add", "r.txt")
	git(t, remote, "commit", "-q", "-m", "remote only")

	if _, err := localsync.Fetch(context.Background(), opts); err == nil {
		t.Fatal("非快进必须报错，绝不能悄悄覆盖本地提交")
	}
}

// TestFetchRejectsNonRepo 验证 LocalRepo 不是 git 仓库时明确报错（供上层降级跳过）。
func TestFetchRejectsNonRepo(t *testing.T) {
	if _, err := localsync.Fetch(context.Background(), localsync.Opts{
		LocalRepo: t.TempDir(), RemoteURL: t.TempDir(), Branch: "handoff/abc12345",
	}); err == nil {
		t.Fatal("非 git 仓库必须报错")
	}
}
