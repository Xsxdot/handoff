// 本文件实现工作区（git 工作树）的**现场探测**：给一个 location 的路径，
// 吐出它下面的全部工作树。
//
// 职责：
//   - 调一次 git worktree list --porcelain，解析成 []proto.Workspace
//   - 判定每条是不是主工作树、是不是 agentd 自建的任务工作树
//   - 探测失败时降级为「空列表 + 人话说明」，不向上抛错
//
// 边界：
//   - 不落库：worktree 会在 agentd 背后被 git worktree add/remove 改动，
//     落表必然产生说谎的行；本机文件系统调用是毫秒级的，缓存只会引入失真窗口
//   - 不判断工作树上挂着哪个任务（那是 join 的事，见 projectjoin.go）
//   - 不做鉴权、不碰 HTTP
package agentd

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// worktreeProbeTimeout 是单次探测的上限。
//
// 为什么要有：目录落在挂掉的网络盘上时 git 会卡住，而项目树是 UI 的常规请求，
// 一个卡住的 location 不能拖垮整棵树。
const worktreeProbeTimeout = 5 * time.Second

// headShortLen 是短 sha 的长度，与 git 默认的 7 位一致。
const headShortLen = 7

// probeWorkspaces 现场探测 dir 下的全部工作树。
//
// 参数：
//   - ctx: 上下文；内部再叠加 worktreeProbeTimeout 作为兜底上限
//   - dir: location 的路径（B62 保证它是主工作树根）
//   - managedRoot: agentd 自建 worktree 的根目录（<DataDir>/worktrees）；
//     空串表示「无法判定 managed」，此时全部按 false
//
// 返回：
//   - 工作树列表（**永不为 nil**，失败时是空切片）
//   - 探测失败的人话说明，空串=正常
//
// 注意：
//   - 失败不返回 error 而返回说明字符串，是刻意的：调用方要把它放进
//     ProjectLocationNode.ProbeError 展示，而不是让整棵树 500
func probeWorkspaces(ctx context.Context, dir, managedRoot string) ([]proto.Workspace, string) {
	ctx, cancel := context.WithTimeout(ctx, worktreeProbeTimeout)
	defer cancel()

	log().Debug("工作区探测开始", "dir", dir)
	out, stderr, err := gitRun(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		// 目录被删、不是 git 仓库、git 超时都走这里：这是**可展示的状态**，
		// 不是服务端故障，所以只 Warn 不 Error
		log().Warn("工作区探测失败，降级为空列表", "dir", dir, "cause", msg)
		return []proto.Workspace{}, msg
	}
	ws := parseWorktreePorcelain(out, managedRoot)
	log().Debug("工作区探测完成", "dir", dir, "worktrees", len(ws))
	return ws, ""
}

// parseWorktreePorcelain 解析 git worktree list --porcelain 的输出。
//
// 输出形态（每块之间空行分隔，第一块恒为主工作树）：
//
//	worktree /path
//	HEAD <40 位 sha>
//	branch refs/heads/main      ← detached 时这一行换成 detached
//
// 返回的切片永不为 nil。
func parseWorktreePorcelain(out, managedRoot string) []proto.Workspace {
	list := []proto.Workspace{}
	var cur *proto.Workspace
	flush := func() {
		if cur != nil {
			cur.IsMain = len(list) == 0 // 第一块即主工作树，git 的输出顺序保证
			cur.Managed = underRoot(cur.Path, managedRoot)
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &proto.Workspace{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// 块外的行（不该出现）直接忽略，别 panic
		case strings.HasPrefix(line, "HEAD "):
			sha := strings.TrimPrefix(line, "HEAD ")
			if len(sha) > headShortLen {
				sha = sha[:headShortLen]
			}
			cur.Head = sha
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			// detached 时 branch 留空串——UI 靠 head 显示，不能编一个假分支名
			cur.Branch = ""
		}
	}
	flush()
	return list
}

// underRoot 判断 path 是否落在 root 目录下（含 root 自身的子目录）。
//
// 为什么不用 strings.HasPrefix 裸比：/a/worktrees-old 会被 /a/worktrees 误判为
// 子目录。走 filepath.Rel 再看有没有 ".." 才是准的。
//
// 为什么先解析符号链接：git 的 porcelain 输出会解析路径里的符号链接
// （macOS 上 /var → /private/var 是实景），而 managedRoot 是配置里的原样路径，
// 两者不归一，真实机器上的 managed 判定会恒为 false。EvalSymlinks 对不存在的
// 路径失败（porcelain 测试里的虚构路径），此时退回词汇级比较。
func underRoot(path, root string) bool {
	if root == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if rp, err := filepath.EvalSymlinks(cleanPath); err == nil {
		cleanPath = rp
	}
	if rr, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = rr
	}
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
