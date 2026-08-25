package turn_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// initRepo 建一个带首提交的临时仓库，返回路径与首提交 hash。
func initRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse 失败: %v", err)
	}
	return dir, string(out[:len(out)-1])
}

func TestGitTurnStatusDetectsNewCommit(t *testing.T) {
	dir, start := initRepo(t)

	_, commit, hasNew, err := turn.GitTurnStatus(dir, start)
	if err != nil {
		t.Fatalf("无新提交时出错: %v", err)
	}
	if hasNew {
		t.Errorf("尚未提交，hasNew 应为 false")
	}
	if commit != start {
		t.Errorf("commit = %q，期望 %q", commit, start)
	}

	cmd := exec.Command("git", "-c", "user.email=t@e", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "second")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("二次提交失败: %v %s", err, out)
	}
	_, commit2, hasNew2, err := turn.GitTurnStatus(dir, start)
	if err != nil {
		t.Fatalf("有新提交时出错: %v", err)
	}
	if !hasNew2 {
		t.Errorf("已有新提交，hasNew 应为 true")
	}
	if commit2 == start {
		t.Errorf("commit 应已推进，仍为 %q", commit2)
	}
}

func TestGitCommonDirNormalizesMainAndLinkedWorktree(t *testing.T) {
	repo, _ := initRepo(t)
	want := filepath.Clean(filepath.Join(repo, ".git"))

	got, err := turn.GitCommonDir(repo)
	if err != nil {
		t.Fatalf("主仓库读取 git-common-dir: %v", err)
	}
	if got != want {
		t.Fatalf("主仓库 common-dir = %q，want %q", got, want)
	}

	linked := filepath.Join(t.TempDir(), "linked")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "probe", linked)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("建立 linked worktree: %v\n%s", err, out)
	}

	got, err = turn.GitCommonDir(linked)
	if err != nil {
		t.Fatalf("linked worktree 读取 git-common-dir: %v", err)
	}
	if got != want {
		t.Fatalf("linked worktree common-dir = %q，want %q", got, want)
	}
}

func TestGitCommonDirRejectsNonGitPath(t *testing.T) {
	if got, err := turn.GitCommonDir(t.TempDir()); err == nil || got != "" {
		t.Fatalf("非 git 目录应返回空路径与错误，got path=%q err=%v", got, err)
	}
}
