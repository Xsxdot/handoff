// named_test.go —— B233.15 新收具名能力的缝级断言（S4）。
//
// 职责：逐条钉住 DeleteBranch/RestoreWorktree/BranchTip/ResolveBaseBranch/
// OriginURL/TopLevel/HeadBranch/ProbeCommit/CloneRepo/ListWorktrees/RemoveWorktree/
// PruneWorktrees 的可观察行为，使实现绕过 git 直接改状态时必然失败。
// 边界：只测具名能力本身；Capability（S1）与文件条目（S2）由迁入的既有测试承担。
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDeleteBranchRemovesOnlyNamedBranch：删指定分支后该分支 ref 不存在，同仓其他分支仍在。
// 变异：DeleteBranch 只打日志不改 ref 或删错名 → 本用例红。
func TestDeleteBranchRemovesOnlyNamedBranch(t *testing.T) {
	repo := initGitRepo(t)
	gitAt(t, repo, "branch", "keep")
	gitAt(t, repo, "branch", "doomed")

	if err := DeleteBranch(context.Background(), repo, "doomed"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if _, _, err := gitProbe(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/doomed"); err == nil {
		t.Fatalf("doomed 分支应已被删除")
	}
	if _, _, err := gitProbe(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/keep"); err != nil {
		t.Fatalf("keep 分支不应被牵连删除: %v", err)
	}
}

// TestBranchTipReturnsHeadSHA：命中分支返回 40 位 sha；不存在分支返回错误而非空串。
// 变异：失败塌缩成空串 + nil（旧缺陷族）→ 本用例红。
func TestBranchTipReturnsHeadSHA(t *testing.T) {
	repo := initGitRepo(t)
	want := gitOut(t, repo, "rev-parse", "main")
	got, err := BranchTip(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("BranchTip: %v", err)
	}
	if got != want || len(got) != 40 {
		t.Fatalf("BranchTip=%q，期望 %q", got, want)
	}
	if _, err := BranchTip(context.Background(), repo, "no-such-branch"); err == nil {
		t.Fatalf("不存在分支必须返回非 nil error")
	}
}

// TestRestoreWorktreeChecksOutRef：非 managed 补偿路径按 ref 切回。
// 变异：RestoreWorktree 不执行 checkout 只返回 nil → 本用例红。
func TestRestoreWorktreeChecksOutRef(t *testing.T) {
	repo := initGitRepo(t)
	gitAt(t, repo, "checkout", "-q", "-b", "feature")
	if err := RestoreWorktree(context.Background(), repo, "main"); err != nil {
		t.Fatalf("RestoreWorktree: %v", err)
	}
	if got := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("HEAD=%q，期望 main", got)
	}
}

// TestResolveBaseBranchPrefersOriginHead：有 origin/HEAD 时返回它，否则退回 main。
// 变异：把空串当成功继续 → 本用例红。
func TestResolveBaseBranchPrefersOriginHead(t *testing.T) {
	if _, clone := newOriginAndClone(t); ResolveBaseBranch(clone) != "origin/main" {
		t.Fatalf("应优先 origin/HEAD，实得 %q", ResolveBaseBranch(clone))
	}
	plain := initGitRepo(t)
	if got := ResolveBaseBranch(plain); got != "main" {
		t.Fatalf("无 origin 时应退回 main，实得 %q", got)
	}
}

// TestCloneRepoLocalAndProxyPaths：本地路径两路都成功克隆；本地路不带代理参数，
// 出网路在注入代理后仍成功。代理参数是否前置于子命令由 gitNetArgs 的用例钉住。
// 变异：本地 clone 默认带代理参数 → gitNetArgs 用例红；CloneRepo 忽略 withProxy
// 使两路一致 → 本用例仍绿（已声明局限），但 withProxy 分支覆盖由 gitRunNet 用例补。
func TestCloneRepoLocalAndProxyPaths(t *testing.T) {
	src := initGitRepo(t)
	parent := t.TempDir()

	destLocal := filepath.Join(parent, "local")
	if stderr, err := CloneRepo(context.Background(), parent, src, destLocal, false); err != nil {
		t.Fatalf("CloneRepo(local): %v stderr=%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(destLocal, ".git")); err != nil {
		t.Fatalf("本地克隆落点应是仓库: %v", err)
	}

	ConfigureGitNet(GitNetConfig{Argv: []string{"-c", "http.proxy=socks5://127.0.0.1:1080"}})
	defer ConfigureGitNet(GitNetConfig{})
	destNet := filepath.Join(parent, "net")
	if stderr, err := CloneRepo(context.Background(), parent, src, destNet, true); err != nil {
		t.Fatalf("CloneRepo(withProxy): %v stderr=%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(destNet, ".git")); err != nil {
		t.Fatalf("出网路径克隆落点应是仓库: %v", err)
	}
}

// TestRepoProbes：OriginURL 命中/缺失、TopLevel 子目录归并、HeadBranch detached 空、
// ProbeCommit 命中与未命中。
// 变异：任一探针把失败塌缩成假值 → 对应断言红。
func TestRepoProbes(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	if got, err := OriginURL(context.Background(), clone); err != nil || got != origin {
		t.Fatalf("OriginURL=%q err=%v，期望 %q", got, err, origin)
	}
	plain := initGitRepo(t)
	if _, err := OriginURL(context.Background(), plain); !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("无 origin 应返回 ErrRepoUnusable，实得 %v", err)
	}

	sub := filepath.Join(clone, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("建子目录: %v", err)
	}
	top, ok := TopLevel(context.Background(), sub)
	if !ok || canonPath(top) != canonPath(clone) {
		t.Fatalf("TopLevel(sub)=%q ok=%v，期望 %q", top, ok, clone)
	}

	if got := HeadBranch(context.Background(), clone); got != "main" {
		t.Fatalf("HeadBranch=%q，期望 main", got)
	}
	headSHA := gitOut(t, clone, "rev-parse", "HEAD")
	gitAt(t, clone, "checkout", "-q", "--detach", headSHA)
	if got := HeadBranch(context.Background(), clone); got != "" {
		t.Fatalf("detached 时 HeadBranch 应为空，实得 %q", got)
	}
	gitAt(t, clone, "checkout", "-q", "main")

	if got, err := ProbeCommit(context.Background(), clone, "main"); err != nil || got != headSHA {
		t.Fatalf("ProbeCommit(main)=%q err=%v，期望 %q", got, err, headSHA)
	}
	if _, err := ProbeCommit(context.Background(), clone, "no-such-rev"); err == nil {
		t.Fatalf("不存在的 rev 应返回错误")
	}
}

// TestRemoveWorktreeForceDiscardsDirty：脏树 force=false 拒绝、force=true 删掉且不在册。
// 变异：force 参数被忽略而总是强删 → 本用例红。
func TestRemoveWorktreeForceDiscardsDirty(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-remove", "feat/remove")
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("写脏文件: %v", err)
	}
	if err := RemoveWorktree(context.Background(), repo, wt, false); err == nil {
		t.Fatalf("脏树不带 force 必须拒绝")
	}
	if err := RemoveWorktree(context.Background(), repo, wt, true); err != nil {
		t.Fatalf("force 删除: %v", err)
	}
	entries, err := ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if _, ok := FindWorktree(entries, wt); ok {
		t.Fatalf("force 删除后 %s 不应在册", wt)
	}
}

// TestPruneWorktreesClearsPrunable：目录被人工删除后 prune 清掉在册元数据条目。
// 变异：PruneWorktrees 空操作 → 本用例红。
func TestPruneWorktreesClearsPrunable(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-prune", "feat/prune")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删工作树目录: %v", err)
	}
	if err := PruneWorktrees(context.Background(), repo); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	entries, err := ListWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if _, ok := FindWorktree(entries, wt); ok {
		t.Fatalf("prune 后 %s 不应在册", wt)
	}
}
