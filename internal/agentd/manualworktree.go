// manualworktree.go —— 手工工作树：不属于任何任务的 git worktree。
//
// 职责：
//   - 校验建树请求（模式/分支名/基线/占用/落点）并给出可判别的拒绝理由
//   - 在 <DataDir>/worktrees/manual/<分支名安全化> 下建树
//   - 回读一次项目树口径的 proto.Workspace 交给调用方
//
// 边界：
//   - **不认识任务**：不落库、不发事件、不参与回收。它建出来的树没有任何自动
//     清理路径（本期不做删除入口，见 spec §8），这是自觉的取舍不是遗漏
//   - 不复用 PrepareWorkspace：那条路径要求 task_id 且按 id8 命名目录，语义是
//     「为一次派发准备工作区」；本文件的语义是「人手开一棵树」，共用只会让两边
//     的参数互相污染。二者共用的只有 gitRun 与参数注入防线
//   - 失败清理只用 os.Remove（只删空目录），**绝不使用递归删除**：落点里一旦有
//     内容，那就是用户的东西，宁可留残骸也不能替他删
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrBadWorktreeReq 标记「请求本身不合法」，HTTP 层据此回 400 而不是 500。
var ErrBadWorktreeReq = errors.New("建树请求不合法")

// manualSubdir 是手工树在 agentd 数据区里的子目录名。
//
// 与任务自建树（worktrees/<id8>）分层而不是混在一起：路径形状本身就能回答
// 「这棵树是谁建的」，不需要再查库。
const manualSubdir = "manual"

// ManualWorktreeRoot 返回手工树的落点根目录。
//
// 参数：worktreesDir 即 <DataDir>/worktrees。
// 返回：<DataDir>/worktrees/manual。界面用它如实回显「会建在哪」。
func ManualWorktreeRoot(worktreesDir string) string {
	return filepath.Join(worktreesDir, manualSubdir)
}

// manualDirName 把分支名转成目录名：'/' 换 '-'，其余原样。
//
// 分支名此前已过 git check-ref-format，不含空格与控制字符，所以这一步只需处理
// 层级分隔符。副作用是 feat/x 与 feat-x 会撞同一个目录名——撞了直接拒（调用方
// 的落点存在性检查），不自动加后缀：自动改名会让人以为建在了他以为的位置。
func manualDirName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// rejectWorktree 包一条拒绝理由，统一挂上 ErrBadWorktreeReq 并打 Warn。
func rejectWorktree(reason string, req proto.CreateWorktreeReq) error {
	log().Warn("建树被拒", "mode", req.Mode, "branch", req.Branch, "base", req.Base, "cause", reason)
	return fmt.Errorf("%w: %s", ErrBadWorktreeReq, reason)
}

// manualBranchExists 判定本地分支是否存在。
//
// 判据是 rev-parse --verify --quiet refs/heads/<名> 有非空输出：--quiet 让
// 「不存在」走退出码而不是 stderr，与 PrepareWorkspace 的判法保持一致。
func manualBranchExists(ctx context.Context, repo, branch string) bool {
	out, _, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil && strings.TrimSpace(out) != ""
}

// CreateManualWorktree 在 worktreesDir/manual 下开一棵不属于任何任务的工作树。
//
// 参数：
//   - repo: 项目主仓路径（登记表里的 path）
//   - worktreesDir: <DataDir>/worktrees
//   - req: 建树请求，见 proto.CreateWorktreeReq
//
// 返回：
//   - 新工作树的 proto.Workspace，口径与项目树上那一条完全一致（含 head/created_at）
//   - 错误：请求类问题一律包 ErrBadWorktreeReq（调用方回 400）；git 执行失败原样返回
//
// 注意：
//   - 整个过程限时 WorkspaceGitTimeout（2 分钟）——pre-checkout hook 与凭证交互
//     提示都能把 git 挂死，不设上限会拖死一个 HTTP 连接
//   - 成功后会多跑一次 git worktree list 做回读，为的是让返回值与树上同源；
//     回读挑不到时退回手工组装并 Warn，**不因为回读失败就把成功报成失败**
func CreateManualWorktree(ctx context.Context, repo, worktreesDir string, req proto.CreateWorktreeReq) (proto.Workspace, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	start := time.Now()
	log().Info("手工建树进入", "repo", repo, "mode", req.Mode, "branch", req.Branch,
		"base", req.Base, "worktrees_dir", worktreesDir, "timeout", WorkspaceGitTimeout)

	// 第 1 层：纯内存参数校验（模式/空值/注入面）
	if req.Mode != "new_branch" && req.Mode != "existing_branch" {
		return proto.Workspace{}, rejectWorktree("mode 必须是 new_branch 或 existing_branch，收到 "+req.Mode, req)
	}
	if strings.TrimSpace(req.Branch) == "" {
		return proto.Workspace{}, rejectWorktree("branch 必填", req)
	}
	for _, v := range []struct{ what, val string }{{"branch", req.Branch}, {"base", req.Base}} {
		if strings.HasPrefix(v.val, "-") {
			return proto.Workspace{}, rejectWorktree(v.what+" 不允许以 - 开头（git 参数注入面）: "+v.val, req)
		}
	}
	// 分支名合法性交给 git 自己判，不自己写正则：ref 命名规则有十来条，
	// 手写一份必然与 git 的实现分叉
	if _, stderr, err := gitRun(ctx, repo, "check-ref-format", "--branch", req.Branch); err != nil {
		return proto.Workspace{}, rejectWorktree("分支名 "+req.Branch+" 不是合法的 git 分支名: "+strings.TrimSpace(stderr), req)
	}

	// 第 2 层：仓库现状校验
	exists := manualBranchExists(ctx, repo, req.Branch)
	base := req.Base
	switch req.Mode {
	case "new_branch":
		if exists {
			return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 已存在，请换个名字或改用「检出已有分支」", req)
		}
		if base == "" {
			base = resolveBaseBranch(repo)
			log().Info("建树基线由推导得出", "repo", repo, "base", base)
		}
		if base == "" {
			return proto.Workspace{}, rejectWorktree("推导不出基准分支，请显式指定基线", req)
		}
		if _, _, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", base); err != nil {
			return proto.Workspace{}, rejectWorktree("基线 "+base+" 在仓库里不存在", req)
		}
	case "existing_branch":
		if !exists {
			return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 不存在", req)
		}
	}

	managedRoot := worktreesDir
	// 占用检查放在 git 之前只为给人话：git 自己那层拒绝（already checked out）
	// 仍然是最终防线，两处都留着
	if req.Mode == "existing_branch" {
		existing, probeErr := probeWorkspaces(ctx, repo, managedRoot)
		if probeErr != "" {
			log().Warn("建树前探测已有工作树失败，占用检查降级为由 git 兜底", "repo", repo, "cause", probeErr)
		}
		for _, ws := range existing {
			if ws.Branch == req.Branch {
				return proto.Workspace{}, rejectWorktree("分支 "+req.Branch+" 已被工作树 "+ws.Path+" 检出，一个分支只能有一棵树", req)
			}
		}
	}

	// 第 3 层：落点
	root := ManualWorktreeRoot(worktreesDir)
	dir := filepath.Join(root, manualDirName(req.Branch))
	if _, err := os.Stat(dir); err == nil {
		return proto.Workspace{}, rejectWorktree("落点 "+dir+" 已存在，请换个分支名或先清理该目录", req)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		log().Error("建树失败：创建落点根目录", "root", root, "cause", err)
		return proto.Workspace{}, fmt.Errorf("创建目录 %s: %w", root, err)
	}

	args := []string{"worktree", "add", dir, req.Branch}
	if req.Mode == "new_branch" {
		args = []string{"worktree", "add", "-b", req.Branch, dir, base}
	}
	if _, stderr, err := gitRun(ctx, repo, args...); err != nil {
		log().Error("建树失败：git worktree add", "repo", repo, "dir", dir,
			"branch", req.Branch, "stderr", truncateRunes(stderr, 500), "cause", err)
		// best-effort 清空目录：add 失败通常不留目录，留下的也只可能是空壳。
		// 只用 Remove（非空即失败）——落点里若有内容那是用户的东西
		if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
			log().Warn("建树失败后清理落点未完成，保留现场待查", "dir", dir, "cause", rmErr)
		}
		return proto.Workspace{}, fmt.Errorf("git worktree add %s: %s: %w", dir, strings.TrimSpace(stderr), err)
	}

	ws := readBackWorktree(ctx, repo, managedRoot, dir, req.Branch)
	log().Info("手工建树完成", "repo", repo, "dir", ws.Path, "branch", ws.Branch,
		"managed", ws.Managed, "elapsed_ms", time.Since(start).Milliseconds())
	return ws, nil
}

// readBackWorktree 回读刚建出来的树，返回与项目树同口径的 proto.Workspace。
//
// 路径比对走 canonPath：macOS 上 /var → /private/var 是实景，直接字符串比会
// 一个都对不上。挑不到时退回手工组装的最小值并 Warn——树已经建成了，不能因为
// 回读没赶上（并发被删、探测超时）就把一次成功报成失败。
func readBackWorktree(ctx context.Context, repo, managedRoot, dir, branch string) proto.Workspace {
	list, probeErr := probeWorkspaces(ctx, repo, managedRoot)
	if probeErr != "" {
		log().Warn("建树后回读探测失败，返回最小信息", "dir", dir, "cause", probeErr)
	}
	want := canonPath(dir)
	for _, ws := range list {
		if canonPath(ws.Path) == want {
			return ws
		}
	}
	log().Warn("建树后回读挑不到新树，返回最小信息", "dir", dir, "branch", branch)
	return proto.Workspace{Path: dir, Branch: branch, Managed: true}
}
