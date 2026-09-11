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
//   - 本文件的编排（Reclaim / ReclaimList）在关键节点打日志；解析与巡检的
//     底层 git 助手已迁至 internal/workspace（B233.15）
package agentd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

var (
	// ErrReclaimNotTerminal 表示任务还没到终态，不予回收——删运行中任务的
	// 工作树等于抽它脚下。
	ErrReclaimNotTerminal = errors.New("任务非终态，不回收工作树")
	// ErrReclaimRepoUnreachable 表示仓库不可达或不是 git 仓库，判不出。
	// 单任务回收必须据此拒绝，绝不能降级成「无残留」静默成功。
	ErrReclaimRepoUnreachable = errors.New("仓库不可达，工作树状态判不出")
	// ErrReclaimNotManaged 表示该任务用的是协调者自带的工作树，agentd 无权删。
	ErrReclaimNotManaged = errors.New("工作区不是 agentd 管理的 worktree")
)

// DirtyWorktreeError 表示工作树有未提交改动或未跟踪文件，未带 force 时拒绝回收。
//
// 为什么是带清单的类型而不是裸哨兵：协调者要决定「这些改动能不能丢」，
// 就必须看见改了什么。只给一句「树是脏的」等于把决定权交出去却不给依据。
// 注意名字避开 workspace.go 里的 ErrDirtyWorktree 哨兵——那是 dispatch 拒发的
// 错误，语义是「拒绝派发」，与这里的「回收被拒、带清单」是两回事。
type DirtyWorktreeError struct {
	Files []proto.DirtyFile
}

func (e *DirtyWorktreeError) Error() string {
	return fmt.Sprintf("工作树有 %d 项未提交改动或未跟踪文件", len(e.Files))
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
//   - ErrReclaimNotTerminal / ErrReclaimNotManaged / ErrReclaimRepoUnreachable
//   - *DirtyWorktreeError: 脏树且未带 force
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
		return nil, fmt.Errorf("任务 %s 状态 %s，%w", taskID, cur.State, ErrReclaimNotTerminal)
	}
	if !cur.WorktreeManaged || cur.WorkDir == "" {
		return nil, fmt.Errorf("任务 %s：%w", taskID, ErrReclaimNotManaged)
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
			return nil, fmt.Errorf("任务 %s 状态 %s，%w", taskID, cur.State, ErrReclaimNotTerminal)
		}
		if !cur.WorktreeManaged || cur.WorkDir == "" {
			return nil, fmt.Errorf("任务 %s：%w", taskID, ErrReclaimNotManaged)
		}
		return nil, fmt.Errorf("任务 %s 回收判据为 %s，拒绝回收", taskID, dec)
	}

	entries, lerr := workspace.ListWorktrees(ctx, cur.RepoPath)
	if lerr != nil {
		return nil, fmt.Errorf("任务 %s 的仓库 %s：%v：%w",
			taskID, cur.RepoPath, lerr, ErrReclaimRepoUnreachable)
	}
	state, dirty, note := workspace.ClassifyWorktree(ctx, entries, cur.WorkDir)
	base := &proto.ReclaimResp{WorkDir: cur.WorkDir, Branch: cur.Branch}

	switch state {
	case proto.WorktreeAbsent:
		m.log.Info("reclaim 完成：本就无残留", "task", taskID, "workdir", cur.WorkDir)
		base.Action = proto.ReclaimAlreadyAbsent
		return base, nil
	case proto.WorktreeUnknown:
		return nil, fmt.Errorf("任务 %s 工作树 %s：%s：%w",
			taskID, cur.WorkDir, note, ErrReclaimRepoUnreachable)
	case proto.WorktreeDirty:
		if !force {
			return nil, &DirtyWorktreeError{Files: dirty}
		}
		m.log.Warn("reclaim 强删脏工作树", "task", taskID,
			"workdir", cur.WorkDir, "discard", len(dirty))
	}

	if rerr := workspace.RemoveWorktree(ctx, cur.RepoPath, cur.WorkDir, force); rerr != nil {
		// prunable 兜底：实证 git 2.50.1 上 remove 能直接处理在册但目录已失的
		// 条目，这里只防旧版 git 行为不同。remove 成功是常路，本分支是保险
		if state == proto.WorktreePrunable {
			m.log.Warn("reclaim：prunable 条目 remove 失败，退回 prune",
				"task", taskID, "cause", rerr)
			if perr := workspace.PruneWorktrees(ctx, cur.RepoPath); perr != nil {
				return nil, perr
			}
			after, verr := workspace.ListWorktrees(ctx, cur.RepoPath)
			if verr != nil {
				return nil, fmt.Errorf("prune 后复查工作树册：%w", verr)
			}
			if _, still := workspace.FindWorktree(after, cur.WorkDir); still {
				return nil, fmt.Errorf("prune 后条目 %s 仍在册", cur.WorkDir)
			}
			m.log.Info("reclaim 完成：prune 清掉在册条目", "task", taskID, "workdir", cur.WorkDir)
			base.Removed, base.Action = true, proto.ReclaimPruned
			return base, nil
		}
		return nil, rerr
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
	cache := make(map[string]map[string]workspace.WorktreeEntry)
	failed := make(map[string]string)

	for _, t := range tasks {
		if !t.State.IsTerminal() || !t.WorktreeManaged || t.WorkDir == "" {
			continue
		}
		resp.Scanned++
		entries, cached := cache[t.RepoPath]
		if !cached {
			e, lerr := workspace.ListWorktrees(ctx, t.RepoPath)
			if lerr != nil {
				failed[t.RepoPath] = strings.TrimSpace(truncateRunes(lerr.Error(), 200))
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
		state, dirty, note := workspace.ClassifyWorktree(ctx, entries, t.WorkDir)
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
