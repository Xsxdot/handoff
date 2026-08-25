// adapter_sandbox_test.go —— codex 运行态的 git 公共目录测试。
//
// 职责：锁住 newRunState 对 git common directory 的一次性取证与非 git 静默跳过。
// 边界：只验证运行态接缝，不测试 Linux 沙箱本身的 OS 行为。
package codex

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewRunStateCachesGitCommonDirAndSkipsNonGit(t *testing.T) {
	repo := initGitCommonDirRepo(t)
	taskDir := t.TempDir()
	a := New(nil)

	r := a.newRunState("task-git", taskDir, repo)
	want := filepath.Clean(filepath.Join(repo, ".git"))
	if r.gitCommonDir != want {
		t.Fatalf("运行态 gitCommonDir = %q，want %q", r.gitCommonDir, want)
	}

	nonGit := t.TempDir()
	r = a.newRunState("task-non-git", t.TempDir(), nonGit)
	if r.gitCommonDir != "" {
		t.Fatalf("非 git 工作目录不得产生可写根，got %q", r.gitCommonDir)
	}
	if r.gitDir != "" {
		t.Fatalf("非 git 工作目录不得产生私有 git 可写根，got %q", r.gitDir)
	}
}

func initGitCommonDirRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}
