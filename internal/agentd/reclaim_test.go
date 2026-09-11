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

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

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
		nil, nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetWorkspace(workspace.NewCapability())
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
	entries, _ := workspace.ListWorktrees(context.Background(), repo)
	if _, ok := workspace.FindWorktree(entries, wt); ok {
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

func TestReclaimRefusesWaitingReview(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-wr", "f-wr")
	id := seedTerminalTask(t, m, repo, wt, "f-wr", proto.TaskStateWaitingReview, true)
	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimNotTerminal) {
		t.Fatalf("waiting_review 应拒绝，实得 %v", err)
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

// 变异 3 补丁（B77 Task 10 变异检验发现）：TestReclaimRefusesWhenRepoUnreachable
// 删的是仓库，命中 ListWorktrees 失败路径，到不了 WorktreeUnknown 分支——那条
// 变异（Unknown 改走 already_absent 静默成功）原来根本没有用例盯着。补这条：
// 把工作树 gitdir 里的 index 弄坏，让 git status 读不出 → 判不出，回收必须拒绝，
// 绝不能把「判不出」当成「无残留」退成功。
func TestReclaimRefusesWhenWorktreeUnreadable(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r9", "f-r9")
	gd := filepath.Join(repo, ".git", "worktrees", filepath.Base(wt))
	if err := os.WriteFile(filepath.Join(gd, "index"), []byte("corrupt"), 0o644); err != nil {
		t.Fatalf("弄坏 index：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r9", proto.TaskStateFailed, true)

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimRepoUnreachable) {
		t.Fatalf("工作树读不出状态应报 repo_unreachable（判不出绝不能静默成功），实得 %v", err)
	}
}
