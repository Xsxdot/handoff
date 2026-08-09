// machineauthority inventory 测试：真实 Git 仓库的 worktree/ref 发现。
//
// 职责：
//   - `git worktree list --porcelain` 能发现 main 与 worktree
//   - `git for-each-ref refs/heads` 发现本地分支
//   - Git 2.25 基线命令可用
//
// 边界：
//   - 使用真实 git 命令与临时仓库，不用 mock
//   - 不覆盖 watcher（由 git_watch_test.go 负责）
package machineauthority

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
)

// gitInitBare 创建一个临时 git 仓库并返回路径。
func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	// 需要一个提交才能有 HEAD/refs
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// runGit 执行 git 命令并断言成功。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestInventoryDiscoversMainAndWorktree 验证 worktree list --porcelain 发现 main 与 worktree。
func TestInventoryDiscoversMainAndWorktree(t *testing.T) {
	dir := gitInit(t)
	worktreePath := filepath.Join(t.TempDir(), "wt1")
	runGit(t, dir, "worktree", "add", "-q", worktreePath, "-b", "feat/x")

	inv := &Inventory{Root: dir}
	ws, err := inv.DiscoverWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("DiscoverWorkspaces: %v", err)
	}
	// macOS 上 /var 是 /private/var 的 symlink，canonical 会解析；用解析后路径比对
	worktreeCanonical, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(worktreePath): %v", err)
	}
	var main, wt *controlplane.Workspace
	for i := range ws {
		if ws[i].Kind == controlplane.WorkspaceKindMain {
			main = &ws[i]
		}
		if ws[i].Kind == controlplane.WorkspaceKindWorktree && ws[i].CanonicalPath == worktreeCanonical {
			wt = &ws[i]
		}
	}
	if main == nil {
		t.Fatalf("未发现 main workspace: %+v", ws)
	}
	if wt == nil {
		t.Fatalf("未发现 worktree workspace %s: %+v", worktreePath, ws)
	}
	if wt.Branch != "feat/x" {
		t.Fatalf("worktree branch = %q, want feat/x", wt.Branch)
	}
	if main.CanonicalPath == "" || wt.CanonicalPath == "" {
		t.Fatalf("canonical path 不应为空")
	}
}

// TestInventoryDiscoversBranches 验证 for-each-ref 发现本地分支。
func TestInventoryDiscoversBranches(t *testing.T) {
	dir := gitInit(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat/y")

	inv := &Inventory{Root: dir}
	refs, err := inv.DiscoverGitRefs(context.Background(), "loc-main")
	if err != nil {
		t.Fatalf("DiscoverGitRefs: %v", err)
	}
	if len(refs) < 2 { // main + feat/y
		t.Fatalf("refs = %+v, want 至少 main 与 feat/y", refs)
	}
	found := map[string]bool{}
	for _, r := range refs {
		found[r.Name] = true
		if r.LocationID != "loc-main" {
			t.Fatalf("ref location_id = %q, want loc-main", r.LocationID)
		}
		if r.HeadOID == "" {
			t.Fatalf("ref %s head_oid 为空", r.Name)
		}
	}
	if !found["main"] || !found["feat/y"] {
		t.Fatalf("refs 缺分支: %+v", found)
	}
}

// TestInventoryCommonDir 验证 git_common_dir 被填充（worktree 与 main 同 common dir）。
func TestInventoryCommonDir(t *testing.T) {
	dir := gitInit(t)
	worktreePath := filepath.Join(t.TempDir(), "wt1")
	runGit(t, dir, "worktree", "add", "-q", worktreePath, "-b", "feat/x")

	inv := &Inventory{Root: dir}
	ws, err := inv.DiscoverWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("DiscoverWorkspaces: %v", err)
	}
	commonDirs := map[string]bool{}
	for _, w := range ws {
		if w.GitCommonDir == "" {
			t.Fatalf("workspace %s git_common_dir 为空", w.ID)
		}
		commonDirs[w.GitCommonDir] = true
	}
	if len(commonDirs) != 1 {
		t.Fatalf("main 与 worktree 的 common dir 应一致，实际 %d 个: %v", len(commonDirs), commonDirs)
	}
}
