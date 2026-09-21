// B233.19 自 agentd/preview_owner_test.go 迁入：默认工作区解析器读 git 元数据
// （workspace root 归并、origin、分支）。
package workspace

import (
	"context"
	"path/filepath"
	"testing"
)

// initGitRepoWithOrigin 造一个带初始提交且配好 origin 的仓库，返回路径。
// agentd 侧登记测试有同款助手；本包 gittesthelpers_test.go 提供 initGitRepo/gitAt。
func initGitRepoWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	repo := initGitRepo(t)
	gitAt(t, repo, "remote", "add", "origin", origin)
	return repo
}

func TestDefaultPreviewWorkspaceResolverReadsGitMetadata(t *testing.T) {
	repo := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/handoff.git")
	gitAt(t, repo, "checkout", "-q", "-b", "feature/preview")

	root, origin, branch, err := defaultPreviewWorkspaceResolver(context.Background(), func() (string, error) {
		return repo, nil
	})
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("eval repo: %v", err)
	}
	if root != wantRoot || origin != "git@github.com:Xsxdot/handoff.git" || branch != "feature/preview" {
		t.Fatalf("workspace metadata root=%q origin=%q branch=%q want root=%q", root, origin, branch, wantRoot)
	}
}
