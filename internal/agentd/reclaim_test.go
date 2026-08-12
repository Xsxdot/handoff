// reclaim_test.go —— worktree 回收的判定与动作测试。
//
// 解析类用例用固定文本（不起 git）；判定与回收类用例在 t.TempDir() 里
// git init + git worktree add 造真实工作树，复用 workspace_test.go 的
// initGitRepo / gitAt / writeAndCommit 助手。
package agentd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func TestParseWorktreeListMarksPrunable(t *testing.T) {
	out := "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n\n" +
		"worktree /repo/wt1\nHEAD abc123\nbranch refs/heads/f1\n\n" +
		"worktree /repo/wt2\nHEAD abc123\nbranch refs/heads/f2\n" +
		"prunable gitdir file points to non-existent location\n\n"
	got := parseWorktreeList(out)
	if len(got) != 3 {
		t.Fatalf("期望 3 个条目，实得 %d：%+v", len(got), got)
	}
	if e := got["/repo/wt2"]; !e.Prunable {
		t.Fatalf("wt2 应判为 prunable，实得 %+v", e)
	}
	if e := got["/repo/wt2"]; e.PruneReason == "" {
		t.Fatalf("prunable 必须带原因，实得空")
	}
	if e := got["/repo/wt1"]; e.Prunable {
		t.Fatalf("wt1 不该被判 prunable，实得 %+v", e)
	}
	if e := got["/repo"]; e.Prunable {
		t.Fatalf("主仓不该被判 prunable，实得 %+v", e)
	}
}

func TestParsePorcelainStatusKeepsStatusCode(t *testing.T) {
	out := " M internal/prochost/fence.go\n?? scratch/probe.log\n"
	got := parsePorcelainStatus(out)
	if len(got) != 2 {
		t.Fatalf("期望 2 项，实得 %d：%+v", len(got), got)
	}
	if got[0].Status != " M" || got[0].Path != "internal/prochost/fence.go" {
		t.Fatalf("第 1 项解析错：%+v", got[0])
	}
	if got[1].Status != "??" || got[1].Path != "scratch/probe.log" {
		t.Fatalf("第 2 项解析错：%+v", got[1])
	}
}

func TestParsePorcelainStatusEmptyIsClean(t *testing.T) {
	if got := parsePorcelainStatus("\n"); len(got) != 0 {
		t.Fatalf("空输出应解析为 0 项，实得 %+v", got)
	}
}

// canonPath 必须能穿透符号链接：macOS 上 /tmp 是 /private/tmp 的链接，
// git 报的是解析后的路径，而任务库里存的可能是未解析的——不归一就永远匹配不上。
func TestCanonPathResolvesSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	if canonPath(link) != canonPath(real) {
		t.Fatalf("链接与目标应归一到同一路径：%s vs %s", canonPath(link), canonPath(real))
	}
}

// 目录已不存在（prunable 态）时仍要能归一：退一步解析父目录再拼回叶子名，
// 否则 prunable 条目永远匹配不上，回收入口对这一态直接失效。
func TestCanonPathResolvesMissingLeafViaParent(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	gone := filepath.Join(link, "gone")
	want := filepath.Join(canonPath(real), "gone")
	if got := canonPath(gone); got != want {
		t.Fatalf("缺失叶子应经父目录归一：实得 %s，期望 %s", got, want)
	}
}

func TestFindEntryMatchesAcrossSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	entries := map[string]worktreeEntry{real: {Path: real}}
	if _, ok := findEntry(entries, link); !ok {
		t.Fatalf("经符号链接给的 workdir 应能匹配到条目")
	}
	if _, ok := findEntry(entries, filepath.Join(real, "other")); ok {
		t.Fatalf("不同路径不该匹配")
	}
}

// newWorktree 在 repo 下建一个 managed 风格的工作树并返回其路径。
func newWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(repo), name)
	gitAt(t, repo, "worktree", "add", "-q", dir, "-b", branch)
	return dir
}

func TestClassifyCleanWorktree(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-clean", "f-clean")
	entries, err := repoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("拉工作树册：%v", err)
	}
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeClean {
		t.Fatalf("期望 clean，实得 %s", state)
	}
	if len(dirty) != 0 {
		t.Fatalf("干净树不该有脏清单，实得 %+v", dirty)
	}
}

// 只有未跟踪文件也必须判脏：git worktree remove 正是会因未跟踪文件失败
// （实证 git 2.50.1：fatal: contains modified or untracked files）。
// 判据不与它对齐，就会出现「我说是净的，删的时候被拒了」。
func TestClassifyUntrackedOnlyIsDirty(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-untracked", "f-untracked")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写未跟踪文件：%v", err)
	}
	entries, err := repoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("拉工作树册：%v", err)
	}
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeDirty {
		t.Fatalf("只有未跟踪文件时也应判 dirty，实得 %s", state)
	}
	if len(dirty) != 1 || dirty[0].Path != "probe.log" {
		t.Fatalf("脏清单应含 probe.log，实得 %+v", dirty)
	}
}

func TestClassifyModifiedIsDirty(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-mod", "f-mod")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("改已跟踪文件：%v", err)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeDirty {
		t.Fatalf("期望 dirty，实得 %s", state)
	}
	if len(dirty) != 1 || dirty[0].Path != "README.md" {
		t.Fatalf("脏清单应含 README.md，实得 %+v", dirty)
	}
}

func TestClassifyPrunableWhenDirGone(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-gone", "f-gone")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删工作树目录：%v", err)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	state, _, note := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreePrunable {
		t.Fatalf("目录已失应判 prunable，实得 %s", state)
	}
	if note == "" {
		t.Fatalf("prunable 必须带原因")
	}
}

func TestClassifyAbsentWhenNotRegistered(t *testing.T) {
	repo := initGitRepo(t)
	entries, _ := repoWorktrees(context.Background(), repo)
	state, _, _ := classifyWorktree(context.Background(), entries, filepath.Join(repo, "never-existed"))
	if state != proto.WorktreeAbsent {
		t.Fatalf("未注册路径应判 absent，实得 %s", state)
	}
}

// 仓库不可达必须报 unknown 而不是 absent：把「判不出」渲染成「没有残留」，
// 等于用假结论把该看的东西藏起来（同 B70 的「不猜 0」纪律）。
func TestRepoWorktreesFailsOnNonRepo(t *testing.T) {
	if _, err := repoWorktrees(context.Background(), t.TempDir()); err == nil {
		t.Fatalf("非 git 仓库应返回错误，实得 nil")
	}
}
