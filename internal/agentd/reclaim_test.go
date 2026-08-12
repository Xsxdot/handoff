// reclaim_test.go —— worktree 回收的判定与动作测试。
//
// 解析类用例用固定文本（不起 git）；判定与回收类用例在 t.TempDir() 里
// git init + git worktree add 造真实工作树，复用 workspace_test.go 的
// initGitRepo / gitAt / writeAndCommit 助手。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
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

// newReclaimManager 造一个带真实 git 仓库的测试 Manager。
// 构造方式照抄 manager_test.go 的 compensateOnlyManager：store.Open 到
// looseTempDir、cfg.DataDir 用 t.TempDir、log 用 slog 写 io.Discard。
func newReclaimManager(t *testing.T) (*Manager, string) {
	t.Helper()
	repo := initGitRepo(t)
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return m, repo
}

// seedTerminalTask 往库里塞一个指定状态的任务，返回任务 ID。
// 用 mustCreateTask 直接落库（不走 Dispatch），状态任意指定（含 running 非终态）。
func seedTerminalTask(t *testing.T, m *Manager, repo, workdir, branch string,
	state proto.TaskState, managed bool) string {
	t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("t-%d", time.Now().UnixNano())
	task := &proto.Task{
		ID: id, RepoPath: repo, WorkDir: workdir, Branch: branch,
		State: state, WorktreeManaged: managed, Executor: "fake",
		CreatedAt: now, UpdatedAt: now,
	}
	mustCreateTask(t, m.st, task)
	return id
}

func TestReclaimRemovesCleanWorktree(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r1", "f-r1")
	id := seedTerminalTask(t, m, repo, wt, "f-r1", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("回收干净树应成功，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved || !resp.Removed {
		t.Fatalf("期望 removed，实得 %+v", resp)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("工作树目录应已删除")
	}
}

func TestReclaimRefusesDirtyWithoutForce(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r2", "f-r2")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r2", proto.TaskStateFailed, true)

	_, err := m.Reclaim(context.Background(), id, false)
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("脏树无 force 应返回 DirtyWorktreeError，实得 %v", err)
	}
	if len(de.Files) != 1 || de.Files[0].Path != "probe.log" {
		t.Fatalf("拒绝时必须带脏清单，实得 %+v", de.Files)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("拒绝后工作树必须原样保留：%v", err)
	}
}

func TestReclaimForceRemovesDirtyAndReportsDiscarded(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r3", "f-r3")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r3", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, true)
	if err != nil {
		t.Fatalf("force 应删成功，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved {
		t.Fatalf("期望 removed，实得 %s", resp.Action)
	}
	// 强删不能悄悄发生：丢了什么必须留痕
	if len(resp.Discarded) != 1 || resp.Discarded[0].Path != "probe.log" {
		t.Fatalf("强删必须报出被丢弃的条目，实得 %+v", resp.Discarded)
	}
}

func TestReclaimHandlesPrunableEntry(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r4", "f-r4")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删目录：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r4", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("prunable 条目应可回收，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved && resp.Action != proto.ReclaimPruned {
		t.Fatalf("期望 removed 或 pruned，实得 %s", resp.Action)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	if _, ok := findEntry(entries, wt); ok {
		t.Fatalf("回收后条目必须从册中消失")
	}
}

// 幂等是「重试入口」的定义：重试第二次会报错的入口，不是重试入口。
func TestReclaimIsIdempotent(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r5", "f-r5")
	id := seedTerminalTask(t, m, repo, wt, "f-r5", proto.TaskStateFailed, true)

	if _, err := m.Reclaim(context.Background(), id, false); err != nil {
		t.Fatalf("首次回收：%v", err)
	}
	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("二次回收必须成功（幂等），实得 %v", err)
	}
	if resp.Action != proto.ReclaimAlreadyAbsent || resp.Removed {
		t.Fatalf("二次回收应报 already_absent 且 removed=false，实得 %+v", resp)
	}
}

func TestReclaimRefusesNonTerminal(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r6", "f-r6")
	id := seedTerminalTask(t, m, repo, wt, "f-r6", proto.TaskStateRunning, true)

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimNotTerminal) {
		t.Fatalf("非终态应拒绝，实得 %v", err)
	}
	if _, serr := os.Stat(wt); serr != nil {
		t.Fatalf("拒绝后工作树必须保留：%v", serr)
	}
}

func TestReclaimRefusesNotManaged(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r7", "f-r7")
	id := seedTerminalTask(t, m, repo, wt, "f-r7", proto.TaskStateFailed, false)

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimNotManaged) {
		t.Fatalf("非 managed 应拒绝，实得 %v", err)
	}
	if _, serr := os.Stat(wt); serr != nil {
		t.Fatalf("拒绝后用户自带工作树必须保留：%v", serr)
	}
}

// 仓库不可达时**绝不能**被当成 already_absent 静默退成功——
// 那会让人以为已经清干净了（同 B64 的「把没上报当成没有」缺陷）。
func TestReclaimRefusesWhenRepoUnreachable(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r8", "f-r8")
	id := seedTerminalTask(t, m, repo, wt, "f-r8", proto.TaskStateFailed, true)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("删仓库：%v", err)
	}

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimRepoUnreachable) {
		t.Fatalf("仓库不可达应报 repo_unreachable，实得 %v", err)
	}
}

func TestReclaimNotFound(t *testing.T) {
	m, _ := newReclaimManager(t)
	if _, err := m.Reclaim(context.Background(), "no-such-task", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的任务应返回 ErrNotFound，实得 %v", err)
	}
}

func TestReclaimListShowsResidueOnly(t *testing.T) {
	m, repo := newReclaimManager(t)
	dirtyWT := newWorktree(t, repo, "wt-l1", "f-l1")
	if err := os.WriteFile(filepath.Join(dirtyWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	dirtyID := seedTerminalTask(t, m, repo, dirtyWT, "f-l1", proto.TaskStateFailed, true)
	// 已回收干净的任务：记录还在、worktree_managed 仍是 true，但不该入表
	goneWT := filepath.Join(filepath.Dir(repo), "wt-l2-never")
	seedTerminalTask(t, m, repo, goneWT, "f-l2", proto.TaskStateCompleted, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 2 {
		t.Fatalf("应体检 2 个终态任务，实得 %d", resp.Scanned)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].TaskID != dirtyID {
		t.Fatalf("只有脏树那条该入表，实得 %+v", resp.Rows)
	}
	if resp.Rows[0].Worktree != proto.WorktreeDirty || resp.Rows[0].DirtyCount != 1 {
		t.Fatalf("脏行应带态与条数，实得 %+v", resp.Rows[0])
	}
}

// 非终态任务不入表：它的工作树正被使用，不是残留。
func TestReclaimListSkipsNonTerminal(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-l3", "f-l3")
	seedTerminalTask(t, m, repo, wt, "f-l3", proto.TaskStateRunning, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 0 || len(resp.Rows) != 0 {
		t.Fatalf("非终态不该被体检或入表，实得 %+v", resp)
	}
}

// 一个仓库不可达不能拖垮整张表——列表的核心价值正是在环境已不健康时还能用。
func TestReclaimListDegradesPerRepo(t *testing.T) {
	m, goodRepo := newReclaimManager(t)
	goodWT := newWorktree(t, goodRepo, "wt-l4", "f-l4")
	if err := os.WriteFile(filepath.Join(goodWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	goodID := seedTerminalTask(t, m, goodRepo, goodWT, "f-l4", proto.TaskStateFailed, true)

	deadRepo := t.TempDir() // 不是 git 仓库
	deadID := seedTerminalTask(t, m, deadRepo, filepath.Join(deadRepo, "wt"), "f-l5",
		proto.TaskStateFailed, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("单仓不可达不该让整张表失败，实得 %v", err)
	}
	var sawGood, sawUnknown bool
	for _, r := range resp.Rows {
		if r.TaskID == goodID && r.Worktree == proto.WorktreeDirty {
			sawGood = true
		}
		if r.TaskID == deadID && r.Worktree == proto.WorktreeUnknown {
			sawUnknown = true
		}
	}
	if !sawGood {
		t.Fatalf("健康仓库的行必须照常返回，实得 %+v", resp.Rows)
	}
	if !sawUnknown {
		t.Fatalf("不可达仓库的行必须标 unknown 而不是消失，实得 %+v", resp.Rows)
	}
}

// 非 managed 的任务不入表：用户自带工作树不是 agentd 的残留。
func TestReclaimListSkipsNotManaged(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-l6", "f-l6")
	seedTerminalTask(t, m, repo, wt, "f-l6", proto.TaskStateFailed, false)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 0 || len(resp.Rows) != 0 {
		t.Fatalf("非 managed 不该入表，实得 %+v", resp)
	}
}
