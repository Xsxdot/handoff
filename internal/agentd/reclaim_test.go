// reclaim_test.go —— worktree 回收的判定与动作测试。
//
// 解析类用例用固定文本（不起 git）；判定与回收类用例在 t.TempDir() 里
// git init + git worktree add 造真实工作树，复用 workspace_test.go 的
// initGitRepo / gitAt / writeAndCommit 助手。
package agentd

import (
	"os"
	"path/filepath"
	"testing"
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
