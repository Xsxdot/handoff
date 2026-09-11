// reclaimhelpers.go —— managed worktree 巡检与移除的底层 git 助手（B233.15 从 agentd 迁入）。
//
// 职责：git worktree list --porcelain 的解析、路径归一、四态判定，以及单棵
// worktree 的移除与 prune。回收**编排**（Reclaim/ReclaimList 的状态机与错误映射）
// 仍留在 agentd。
//
// 边界：只读册与删树，不改任务状态、不删分支、不删任务目录。
// 解析类函数是纯函数，刻意不打日志；可观测性由调用方（ClassifyWorktree / agentd 编排）承担。
package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// WorktreeEntry 是 git worktree list --porcelain 里的一条记录。
type WorktreeEntry struct {
	Path        string
	Prunable    bool
	PruneReason string
}

// parseWorktreeList 解析 git worktree list --porcelain 的输出。
//
// 参数：out 为 porcelain 原文。记录之间以空行分隔，每条以 "worktree <路径>" 开头，
// 可选属性行含 HEAD / branch / bare / detached / locked / prunable。
//
// 返回：以 git 报的**原始**路径为键的条目表。路径归一交给 FindWorktree，
// 解析函数保持纯粹以便用固定文本测试。
func parseWorktreeList(out string) map[string]WorktreeEntry {
	entries := make(map[string]WorktreeEntry)
	var cur WorktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries[cur.Path] = cur
		}
		cur = WorktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
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
// 参数：out 为 porcelain 原文，每行形如 "XY 路径"（XY 为两字符状态码）。
// 返回：脏条目清单；输出为空表示工作树干净。
//
// 注意：重命名行形如 "R  old -> new"，这里整段留在 Path 里不再拆。
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

// canonPath 把路径归一到可比较的形态（穿透符号链接；目录已失时解析父目录拼回叶子名）。
func canonPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// FindWorktree 在条目表里按归一后的路径查找工作树。
//
// 参数：entries 为 parseWorktreeList 的产出（键为 git 原始路径）；workdir 任务记录里的路径。
// 返回：命中的条目与是否命中。
func FindWorktree(entries map[string]WorktreeEntry, workdir string) (WorktreeEntry, bool) {
	want := canonPath(workdir)
	for p, e := range entries {
		if canonPath(p) == want {
			return e, true
		}
	}
	return WorktreeEntry{}, false
}

// ListWorktrees 拉取一个仓库当前在册的全部工作树。
//
// 参数：ctx 上层上下文，内部叠加 WorkspaceGitTimeout；repo 仓库路径。
// 返回：条目表；仓库不可达或不是 git 仓库时返回错误（调用方据此报「判不出」）。
//
// 注意：这是**地面真相的唯一来源**——任务库里的 worktree_managed 只说明「当初建过」。
func ListWorktrees(ctx context.Context, repo string) (map[string]WorktreeEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := gitRun(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		log().Warn("拉工作树册失败，该仓库下的任务判不出",
			"repo", repo, "stderr", truncateRunes(stderr, 200), "cause", err)
		return nil, fmt.Errorf("git worktree list %s: %s: %w",
			repo, strings.TrimSpace(truncateRunes(stderr, 200)), err)
	}
	entries := parseWorktreeList(out)
	log().Info("工作树册已拉取", "repo", repo, "entries", len(entries))
	return entries, nil
}

// ClassifyWorktree 判定一个工作区当前所处的态。
//
// 参数：ctx 上层上下文，内部叠加 WorkspaceGitTimeout；entries 为 ListWorktrees 的产出；
// workdir 任务记录里的工作区路径。
// 返回：状态；脏清单（仅 dirty 时非空）；note（unknown / prunable 时的真因）。
//
// 注意：脏的判据含**未跟踪文件**，与 git worktree remove 自身的拒绝条件对齐。
func ClassifyWorktree(ctx context.Context, entries map[string]WorktreeEntry, workdir string) (proto.WorktreeState, []proto.DirtyFile, string) {
	e, ok := FindWorktree(entries, workdir)
	if !ok {
		log().Info("工作树判定：不在册，无残留", "workdir", workdir)
		return proto.WorktreeAbsent, nil, ""
	}
	if e.Prunable {
		log().Info("工作树判定：元数据残留", "workdir", workdir, "reason", e.PruneReason)
		return proto.WorktreePrunable, nil, e.PruneReason
	}
	sctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := gitRun(sctx, workdir, "status", "--porcelain")
	if err != nil {
		note := strings.TrimSpace(truncateRunes(stderr, 200))
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

// RemoveWorktree 删除一棵在册 worktree（git worktree remove [--force]）。
//
// 参数：ctx 上层上下文，内部叠加 WorkspaceGitTimeout；repo 主仓库路径；
// workdir 待删工作树路径；force 为真时对脏树强删（丢弃未提交改动）。
// 返回：git stderr 原文（成功为空串）与错误；调用方需要逐字段还原迁移前的
// 日志/文案时按 CloneRepo 同款约定自取 stderr。
func RemoveWorktree(ctx context.Context, repo, workdir string, force bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	args := []string{"worktree", "remove", workdir}
	if force {
		args = append(args, "--force")
	}
	if _, stderr, err := gitRun(ctx, repo, args...); err != nil {
		return stderr, fmt.Errorf("git worktree remove %s: %s: %w",
			workdir, strings.TrimSpace(truncateRunes(stderr, 200)), err)
	}
	return "", nil
}

// PruneWorktrees 清理丢失目录的 worktree 元数据（git worktree prune）。
//
// 参数：ctx 上层上下文，内部叠加 WorkspaceGitTimeout；repo 主仓库路径。
// 返回：失败时返回包装 stderr 原文的错误，成功 nil。
func PruneWorktrees(ctx context.Context, repo string) error {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	if _, stderr, err := gitRun(ctx, repo, "worktree", "prune"); err != nil {
		return fmt.Errorf("git worktree prune %s: %s: %w",
			repo, strings.TrimSpace(truncateRunes(stderr, 200)), err)
	}
	return nil
}
