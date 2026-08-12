// reclaim.go —— 终态任务 managed worktree 的判定与回收。
//
// 职责：
//   - 从 git worktree list --porcelain 拿地面真相，判定工作树四态
//     （净 / 脏 / 元数据残留 / 不在册），仓库不可达时如实报判不出
//   - 对单个终态任务执行回收（git worktree remove，脏树需显式 force）
//
// 边界：
//   - **纯资源动作**：不改任务状态、不追加状态迁移事件、不发唤醒
//   - 不删任务分支（审核者的工作成果），不删任务目录（失败任务的排查素材）
//   - 不读 worktree_managed 判断「现在还在不在」——该字段删成功从不回写，
//     只用于判断「这个任务当初是不是 managed 模式」
//   - 本文件的解析类函数（parseWorktreeList / parsePorcelainStatus / canonPath /
//     findEntry）是纯函数，刻意不打日志；可观测性由调用方（classifyWorktree /
//     Reclaim / ReclaimList）在关键节点承担
package agentd

import (
	"path/filepath"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
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
// 审核者要看的是「动了什么」，拆开反而丢失了「从哪来」这条信息
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

// canonPath 把路径归一到可比较的形态。
//
// 参数：
//   - p: 绝对路径，目录可能已不存在
//
// 返回：
//   - 解析符号链接后的清洁路径
//
// 注意：
//   - 必须穿透符号链接。macOS 上 /tmp 是 /private/tmp 的链接，git 报的是
//     解析后的路径，而任务库里存的可能没解析——不归一就永远匹配不上
//   - 目录已不存在时（prunable 态）EvalSymlinks 会失败，退一步解析父目录
//     再拼回叶子名。不做这层退让，回收入口对 prunable 这一态直接失效
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

// findEntry 在条目表里按归一后的路径查找工作树。
//
// 参数：
//   - entries: parseWorktreeList 的产出（键为 git 原始路径）
//   - workdir: 任务记录里的工作区路径
//
// 返回：
//   - 命中的条目与是否命中
//
// 注意：线性扫描而非直接查表，因为两侧都要经 canonPath 归一才能比较；
// 一个仓库的工作树数量是个位数，这点开销无所谓
func findEntry(entries map[string]worktreeEntry, workdir string) (worktreeEntry, bool) {
	want := canonPath(workdir)
	for p, e := range entries {
		if canonPath(p) == want {
			return e, true
		}
	}
	return worktreeEntry{}, false
}
