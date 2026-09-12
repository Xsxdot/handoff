package orchestration

import (
	"path/filepath"
	"testing"
)

// newWorktree 在 repo 下建一个 managed 风格的工作树并返回其路径。
func newWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(repo), name)
	gitAt(t, repo, "worktree", "add", "-q", dir, "-b", branch)
	return dir
}
