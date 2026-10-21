package workspace

// b452_detached_test.go —— B452 S4 只读形态在工作区一侧的核心方法：detached 托管树。
//
// 锁住：Detached+NewWorktree 以基线 sha 建 detached 树、不建分支、Branch 为空、Managed=true；
// Detached 与任何分支类参数或非新树模式互斥（ErrBadWorkspaceReq，零工作树）。
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestB452PrepareDetachedWorktree(t *testing.T) {
	repo := initGitRepo(t)
	base := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	// 基线之后主仓再前进一步并留脏改动：detached 树必须停在基线，不受两者影响
	writeAndCommitWS(t, repo, "later.txt", "later")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refsBefore := gitT(t, repo, "for-each-ref", "refs/heads")
	wtDir := filepath.Join(t.TempDir(), "worktrees")
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "b452aaaa-0000-0000-0000-000000000000", Base: base,
		NewWorktree: true, Detached: true, WorktreesDir: wtDir,
	})
	if err != nil {
		t.Fatalf("detached 托管树准备失败: %v", err)
	}
	if ws.Branch != "" || !ws.Managed || ws.NewBranchTip != "" {
		t.Fatalf("detached 结果 = %+v，want Branch 空、Managed、无 NewBranchTip", ws)
	}
	if got := strings.TrimSpace(gitT(t, ws.WorkDir, "rev-parse", "HEAD")); got != base {
		t.Fatalf("detached 树 HEAD=%s want 基线 %s", got, base)
	}
	if got := strings.TrimSpace(gitT(t, ws.WorkDir, "branch", "--show-current")); got != "" {
		t.Fatalf("detached 树不应在分支上，得 %q", got)
	}
	porcelain := gitT(t, repo, "worktree", "list", "--porcelain")
	realDir, _ := filepath.EvalSymlinks(ws.WorkDir) // macOS 临时目录 /var → /private/var
	if !strings.Contains(porcelain, "worktree "+realDir) || !strings.Contains(porcelain, "detached") {
		t.Fatalf("worktree list 应含 detached 的 %s：\n%s", ws.WorkDir, porcelain)
	}
	if after := gitT(t, repo, "for-each-ref", "refs/heads"); after != refsBefore {
		t.Fatalf("detached 准备不得新增分支：before=%q after=%q", refsBefore, after)
	}
	if b, _ := os.ReadFile(filepath.Join(ws.WorkDir, "README.md")); string(b) != "# repo\n" {
		t.Fatalf("主仓脏改动不应出现在 detached 树里，得 %q", b)
	}
}

func TestB452PrepareDetachedRejectsBranchParamsAndNonManaged(t *testing.T) {
	repo := initGitRepo(t)
	base := strings.TrimSpace(gitT(t, repo, "rev-parse", "HEAD"))
	wtDir := filepath.Join(t.TempDir(), "worktrees")
	cases := map[string]WorkspaceReq{
		"branch":      {Branch: "main", NewWorktree: true},
		"new-branch":  {NewBranch: "x", NewWorktree: true},
		"in-place":    {},
		"user-tree":   {Worktree: repo},
		"no-base-sha": {NewWorktree: true, Base: ""},
	}
	for name, req := range cases {
		req.Repo, req.TaskID, req.Detached, req.WorktreesDir = repo, "b452bbbb-0000-0000-0000-000000000000", true, wtDir
		if req.Base == "" && name != "no-base-sha" {
			req.Base = base
		}
		_, err := PrepareWorkspace(context.Background(), req)
		if !errors.Is(err, ErrBadWorkspaceReq) {
			t.Fatalf("%s: detached 非法组合应 ErrBadWorkspaceReq，得 %v", name, err)
		}
	}
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(wtDir)
		if len(entries) != 0 {
			t.Fatalf("非法组合不得建任何工作树，得 %v", entries)
		}
	}
}

func writeAndCommitWS(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", name)
	gitT(t, repo, "commit", "-q", "-m", "add "+name)
}
