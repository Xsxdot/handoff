package workspace

// b452_detached_probe_test.go —— B452 段 0 第 2 条承重假设的真 git 探针。
//
// 职责：在不改产品代码的前提下证明 `git worktree add --detach <p> <sha>` 建出的树
// `branch --show-current` 为空串、HEAD 等于基线 sha，且既有 RemoveManagedWorktree 能删掉它、
// 主仓 refs/heads 前后不变。只读形态（S4）的托管树建立在这三条事实之上。
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestB452ProbeDetachedWorktreeLifecycle(t *testing.T) {
	repo := initGitRepo(t)
	sha := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	before := gitT(t, repo, "for-each-ref", "refs/heads")
	dir := filepath.Join(t.TempDir(), "wt")
	gitT(t, repo, "worktree", "add", "--detach", dir, sha)

	if got := strings.TrimSpace(gitT(t, dir, "branch", "--show-current")); got != "" {
		t.Fatalf("detached 树的 branch --show-current 应为空串，得 %q", got)
	}
	if got := strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD")); got != sha {
		t.Fatalf("detached 树 HEAD = %s，want %s", got, sha)
	}
	if after := gitT(t, repo, "for-each-ref", "refs/heads"); after != before {
		t.Fatalf("建 detached 树不得新增分支：before=%q after=%q", before, after)
	}
	if err := RemoveManagedWorktree(context.Background(), repo, dir); err != nil {
		t.Fatalf("RemoveManagedWorktree 删 detached 树失败: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("删除后目录应不存在，stat err=%v", err)
	}
	if after := gitT(t, repo, "for-each-ref", "refs/heads"); after != before {
		t.Fatalf("删 detached 树后分支表应不变：before=%q after=%q", before, after)
	}
}
