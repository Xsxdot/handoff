// reclaim.go —— 终态任务 managed worktree 的判定与回收。
//
// 职责：
//   - 从 git worktree list --porcelain 拿地面真相，判定工作树四态
//     （净 / 脏 / 元数据残留 / 不在册），仓库不可达时如实报判不出
//   - 对单个终态任务执行回收（git worktree remove，脏树需显式 force）
//
// 边界：
//   - **纯资源动作**：不改任务状态、不追加状态迁移事件、不发唤醒
//   - 不删任务分支（协调者的工作成果），不删任务目录（失败任务的排查素材）
//   - 不读 worktree_managed 判断「现在还在不在」——该字段删成功从不回写，
//     只用于判断「这个任务当初是不是 managed 模式」
//   - 本文件的解析类函数（parseWorktreeList / parsePorcelainStatus / CanonPath /
//     findEntry）是纯函数，刻意不打日志；可观测性由调用方（classifyWorktree /
//     Reclaim / ReclaimList）在关键节点承担
package orchestration

import (
	"context"
	"fmt"
	"strings"

	agentd "github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// worktreeEntry 是 git worktree list --porcelain 里的一条记录。
type worktreeEntry struct {
	Path        string
	Prunable    bool
	PruneReason string
}

// parseWorktreeList 解析 git worktree list --porcelain 的输出。
//
// 参数：
//   - out: porcelain 原文。记录之间以空行分隔，每条以 "worktree <路径>" 开头，
//     可选属性行含 HEAD / branch / bare / detached / locked / prunable
//
// 返回：
//   - 以 git 报的**原始**路径为键的条目表。路径归一交给 findEntry，
//     解析函数保持纯粹以便用固定文本测试
func parseWorktreeList(out string) map[string]worktreeEntry {
	entries := make(map[string]worktreeEntry)
	var cur worktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries[cur.Path] = cur
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			// 记录之间正常由空行分隔；这里再 flush 一次是防御畸形输出
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.Prunable = true
			cur.PruneReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	flush()
	return entries
}

// parsePorcelainStatus 解析 git status --porcelain 的输出为脏文件清单。
//
// 参数：
//   - out: porcelain 原文，每行形如 "XY 路径"（XY 为两字符状态码）
//
// 返回：
//   - 脏条目清单；输出为空表示工作树干净
//
// 注意：重命名行形如 "R  old -> new"，这里整段留在 Path 里不再拆——
// 协调者要看的是「动了什么」，拆开反而丢失了「从哪来」这条信息
func parsePorcelainStatus(out string) []proto.DirtyFile {
	var files []proto.DirtyFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		files = append(files, proto.DirtyFile{
			Status: line[:2],
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return files
}

// findEntry 在条目表里按归一后的路径查找工作树。
//
// 参数：
//   - entries: parseWorktreeList 的产出（键为 git 原始路径）
//   - workdir: 任务记录里的工作区路径
//
// 返回：
//   - 命中的条目与是否命中
//
// 注意：线性扫描而非直接查表，因为两侧都要经 agentd.CanonPath 归一才能比较；
// 一个仓库的工作树数量是个位数，这点开销无所谓
func findEntry(entries map[string]worktreeEntry, workdir string) (worktreeEntry, bool) {
	want := agentd.CanonPath(workdir)
	for p, e := range entries {
		if agentd.CanonPath(p) == want {
			return e, true
		}
	}
	return worktreeEntry{}, false
}

// repoWorktrees 拉取一个仓库当前在册的全部工作树。
//
// 参数：
//   - ctx: 上层上下文，内部叠加 agentd.WorkspaceGitTimeout
//   - repo: 仓库路径
//
// 返回：
//   - 条目表；仓库不可达或不是 git 仓库时返回错误（调用方据此报「判不出」）
//
// 注意：这是**地面真相的唯一来源**。任务库里的 worktree_managed 只说明
// 「当初建过」，删成功从不回写，用它判「现在还在不在」必然出假阳性
func repoWorktrees(ctx context.Context, repo string) (map[string]worktreeEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, agentd.WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := agentd.GitRun(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		log().Warn("拉工作树册失败，该仓库下的任务判不出",
			"repo", repo, "stderr", agentd.TruncateRunes(stderr, 200), "cause", err)
		return nil, fmt.Errorf("git worktree list %s: %s: %w",
			repo, strings.TrimSpace(agentd.TruncateRunes(stderr, 200)), err)
	}
	entries := parseWorktreeList(out)
	log().Info("工作树册已拉取", "repo", repo, "entries", len(entries))
	return entries, nil
}

// classifyWorktree 判定一个工作区当前所处的态。
//
// 参数：
//   - ctx: 上层上下文，内部叠加 agentd.WorkspaceGitTimeout
//   - entries: repoWorktrees 的产出
//   - workdir: 任务记录里的工作区路径
//
// 返回：
//   - 状态；脏清单（仅 dirty 时非空）；note（unknown / prunable 时的真因）
//
// 注意：脏的判据含**未跟踪文件**。这不是保守，而是必须与 git worktree remove
// 自身的拒绝条件对齐——实证 git 2.50.1，只有未跟踪文件时 remove 也会失败
func classifyWorktree(ctx context.Context, entries map[string]worktreeEntry, workdir string) (proto.WorktreeState, []proto.DirtyFile, string) {
	e, ok := findEntry(entries, workdir)
	if !ok {
		log().Info("工作树判定：不在册，无残留", "workdir", workdir)
		return proto.WorktreeAbsent, nil, ""
	}
	if e.Prunable {
		log().Info("工作树判定：元数据残留", "workdir", workdir, "reason", e.PruneReason)
		return proto.WorktreePrunable, nil, e.PruneReason
	}
	sctx, cancel := context.WithTimeout(ctx, agentd.WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := agentd.GitRun(sctx, workdir, "status", "--porcelain")
	if err != nil {
		note := strings.TrimSpace(agentd.TruncateRunes(stderr, 200))
		log().Warn("工作树判定：读不到 status，判不出",
			"workdir", workdir, "stderr", note, "cause", err)
		return proto.WorktreeUnknown, nil, note
	}
	files := parsePorcelainStatus(out)
	if len(files) == 0 {
		log().Info("工作树判定：干净，可回收", "workdir", workdir)
		return proto.WorktreeClean, nil, ""
	}
	log().Info("工作树判定：脏，默认拒绝回收", "workdir", workdir, "dirty", len(files))
	return proto.WorktreeDirty, files, ""
}

// Reclaim 回收一个终态任务残留的 managed worktree。
//
// 参数：
//   - ctx: 上层上下文（HTTP 请求）
//   - taskID: 目标任务
//   - force: 为真时对脏工作树也强删（丢弃未提交改动），并在响应里报出丢弃清单
//
// 返回：
//   - 回收结果（removed / pruned / already_absent）
//   - store.ErrNotFound: 任务不存在
//   - agentd.ErrReclaimNotTerminal / agentd.ErrReclaimNotManaged / agentd.ErrReclaimRepoUnreachable
//   - *agentd.DirtyWorktreeError: 脏树且未带 force
//
// 注意：
//   - **纯资源动作**：不改任务状态、不追加事件、不删分支、不删任务目录
//   - 幂等：树已不在则报 already_absent 并成功返回。一条重试第二次会报错的
//     入口不是重试入口
//   - 动手前重读任务快照：failed→running 是合法迁移，列表之后任务可能已被
//     重新派发，终态判定不能停在列表那一刻
func (m *Manager) Reclaim(ctx context.Context, taskID string, force bool) (resp *proto.ReclaimResp, err error) {
	m.log.Info("reclaim 进入", "task", taskID, "force", force)
	defer func() {
		if err != nil {
			m.log.Warn("reclaim 未完成", "task", taskID, "cause", err)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if !cur.State.IsTerminal() {
		return nil, fmt.Errorf("任务 %s 状态 %s，%w", taskID, cur.State, agentd.ErrReclaimNotTerminal)
	}
	if !cur.WorktreeManaged || cur.WorkDir == "" {
		return nil, fmt.Errorf("任务 %s：%w", taskID, agentd.ErrReclaimNotManaged)
	}

	dec := workspace.MayRecycle(workspace.RecycleInput{
		Managed:  cur.WorktreeManaged,
		Manual:   false,
		Terminal: cur.State.IsTerminal(),
		State:    string(cur.State),
		Trigger:  workspace.TriggerExplicit,
	})
	m.log.Info("reclaim 回收判据", "task", taskID, "decision", dec, "state", cur.State)
	if dec != workspace.RetainRecycle {
		m.log.Warn("reclaim 被判据拒绝", "task", taskID, "decision", dec, "state", cur.State)
		if !cur.State.IsTerminal() {
			return nil, fmt.Errorf("任务 %s 状态 %s，%w", taskID, cur.State, agentd.ErrReclaimNotTerminal)
		}
		if !cur.WorktreeManaged || cur.WorkDir == "" {
			return nil, fmt.Errorf("任务 %s：%w", taskID, agentd.ErrReclaimNotManaged)
		}
		return nil, fmt.Errorf("任务 %s 回收判据为 %s，拒绝回收", taskID, dec)
	}

	entries, lerr := repoWorktrees(ctx, cur.RepoPath)
	if lerr != nil {
		return nil, fmt.Errorf("任务 %s 的仓库 %s：%v：%w",
			taskID, cur.RepoPath, lerr, agentd.ErrReclaimRepoUnreachable)
	}
	state, dirty, note := classifyWorktree(ctx, entries, cur.WorkDir)
	base := &proto.ReclaimResp{WorkDir: cur.WorkDir, Branch: cur.Branch}

	switch state {
	case proto.WorktreeAbsent:
		m.log.Info("reclaim 完成：本就无残留", "task", taskID, "workdir", cur.WorkDir)
		base.Action = proto.ReclaimAlreadyAbsent
		return base, nil
	case proto.WorktreeUnknown:
		return nil, fmt.Errorf("任务 %s 工作树 %s：%s：%w",
			taskID, cur.WorkDir, note, agentd.ErrReclaimRepoUnreachable)
	case proto.WorktreeDirty:
		if !force {
			return nil, &agentd.DirtyWorktreeError{Files: dirty}
		}
		m.log.Warn("reclaim 强删脏工作树", "task", taskID,
			"workdir", cur.WorkDir, "discard", len(dirty))
	}

	rctx, cancel := context.WithTimeout(ctx, agentd.WorkspaceGitTimeout)
	defer cancel()
	args := []string{"worktree", "remove", cur.WorkDir}
	if force {
		args = append(args, "--force")
	}
	if _, stderr, rerr := agentd.GitRun(rctx, cur.RepoPath, args...); rerr != nil {
		// prunable 兜底：实证 git 2.50.1 上 remove 能直接处理在册但目录已失的
		// 条目，这里只防旧版 git 行为不同。remove 成功是常路，本分支是保险
		if state == proto.WorktreePrunable {
			m.log.Warn("reclaim：prunable 条目 remove 失败，退回 prune",
				"task", taskID, "stderr", agentd.TruncateRunes(stderr, 200), "cause", rerr)
			if _, pstderr, perr := agentd.GitRun(rctx, cur.RepoPath, "worktree", "prune"); perr != nil {
				return nil, fmt.Errorf("git worktree prune %s: %s: %w",
					cur.RepoPath, strings.TrimSpace(agentd.TruncateRunes(pstderr, 200)), perr)
			}
			after, verr := repoWorktrees(rctx, cur.RepoPath)
			if verr != nil {
				return nil, fmt.Errorf("prune 后复查工作树册：%w", verr)
			}
			if _, still := findEntry(after, cur.WorkDir); still {
				return nil, fmt.Errorf("prune 后条目 %s 仍在册", cur.WorkDir)
			}
			m.log.Info("reclaim 完成：prune 清掉在册条目", "task", taskID, "workdir", cur.WorkDir)
			base.Removed, base.Action = true, proto.ReclaimPruned
			return base, nil
		}
		return nil, fmt.Errorf("git worktree remove %s: %s: %w",
			cur.WorkDir, strings.TrimSpace(agentd.TruncateRunes(stderr, 200)), rerr)
	}

	m.log.Info("reclaim 完成：工作树已删除", "task", taskID,
		"workdir", cur.WorkDir, "branch", cur.Branch, "discarded", len(dirty))
	base.Removed, base.Action, base.Discarded = true, proto.ReclaimRemoved, dirty
	return base, nil
}

// ReclaimList 体检全部终态任务的 managed worktree 残留。
//
// 返回：
//   - 只含「仍有残留或判不出」的行，外加体检总数；查询任务列表失败才返回错误
//
// 注意：
//   - **单个仓库不可达不拖垮整张表**：该行标 unknown 继续走完。列表的核心
//     价值正是在环境已经不健康的时候还能用——这与单任务回收「判不出就拒绝」
//     的处置刻意相反，因为两者的失败代价不同
//   - 按仓库分组只拉一次工作树册：同一仓库下的多个任务共用一次 git 调用
//   - 与 FootprintAll 分工：那个数进程，这个数工作树，互不覆盖
func (m *Manager) ReclaimList() (*proto.ReclaimListResp, error) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("残留体检：查询任务列表失败", "cause", err)
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	m.log.Info("残留体检开始", "tasks", len(tasks))

	ctx := context.Background()
	resp := &proto.ReclaimListResp{Rows: make([]proto.ReclaimRow, 0)}
	// 每个仓库只拉一次册；值为 nil 表示该仓库不可达（判不出）
	cache := make(map[string]map[string]worktreeEntry)
	failed := make(map[string]string)

	for _, t := range tasks {
		if !t.State.IsTerminal() || !t.WorktreeManaged || t.WorkDir == "" {
			continue
		}
		resp.Scanned++
		entries, cached := cache[t.RepoPath]
		if !cached {
			e, lerr := repoWorktrees(ctx, t.RepoPath)
			if lerr != nil {
				failed[t.RepoPath] = strings.TrimSpace(agentd.TruncateRunes(lerr.Error(), 200))
			}
			cache[t.RepoPath], entries = e, e
		}
		row := proto.ReclaimRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State),
			Branch: t.Branch, WorkDir: t.WorkDir,
		}
		if entries == nil {
			row.Worktree, row.Note = proto.WorktreeUnknown, failed[t.RepoPath]
			resp.Rows = append(resp.Rows, row)
			continue
		}
		state, dirty, note := classifyWorktree(ctx, entries, t.WorkDir)
		if state == proto.WorktreeAbsent {
			continue // 干净收场，不入表
		}
		row.Worktree, row.DirtyCount, row.Note = state, len(dirty), note
		resp.Rows = append(resp.Rows, row)
	}
	m.log.Info("残留体检完成", "scanned", resp.Scanned, "rows", len(resp.Rows),
		"bad_repos", len(failed))
	return resp, nil
}

// worktreeCleanupHint 构造「清理失败」提示文案。
//
// 参数：
//   - taskID: 任务 ID（提示里只取前 8 位，与 CLI 的接受形态一致）
//   - cause: 清理失败的真因
//
// 返回：
//   - 带真因与可执行出路的一句话
//
// 注意：刻意不再提「请手动 git worktree remove」。清理失败最常见的原因就是
// 工作树脏而 remove 不带 --force，手工重跑同一条命令撞的是同一堵墙——
// B77 的 2c58bbb7 正是这么无声漏掉的
func worktreeCleanupHint(taskID string, cause error) string {
	return fmt.Sprintf("worktree 清理失败：%v，可重试：handoff reclaim %s",
		cause, shortTaskID(taskID))
}

// shortTaskID 取任务 ID 前 8 位（不足 8 位则原样返回）。
func shortTaskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
