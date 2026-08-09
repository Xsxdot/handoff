package turn_test

import (
	"os/exec"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
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
