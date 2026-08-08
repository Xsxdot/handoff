// Package localsync 负责把远程执行机上的任务分支同步到审核者本地仓库。
//
// 职责：
//   - 以 git fetch <url> <branch>:<branch> 把远程任务分支拉到本地同名分支
//   - 报告本次同步的增量（新建分支 / 新增提交数），供 CLI 打印给审核者
//
// 边界：
//   - 只 fetch，不 checkout、不 merge、不碰 HEAD——审核者本地可能正在改别的东西，
//     合不合、怎么合是人的决定
//   - 不解析 ssh 配置、不管凭证：RemoteURL 原样交给 git，认证由 ssh 自身完成
//   - 不判断「该不该同步」：触发时机由调用方（wait/pull）决定
package localsync

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FetchTimeout 是一次同步的时长上限：走 ssh 网络，且可能拉较大的提交集。
var FetchTimeout = 2 * time.Minute

// log 返回包日志入口（运行时取 slog.Default()，跟随 CLI 的 logx 配置）。
func log() *slog.Logger { return slog.Default() }

// Opts 描述一次同步。
//
//   - LocalRepo: 本地仓库路径（通常是审核者的 cwd）
//   - RemoteURL: 远程仓库地址，形如 host:/path/to/repo（也接受本地路径，git 同一条路径处理）
//   - Branch:    要同步的任务分支名（取 task.Branch，不是从任务 ID 派生）
type Opts struct {
	LocalRepo string
	RemoteURL string
	Branch    string
}

// Result 是一次同步的结果。
//
//   - Branch:  同步的分支名
//   - Commits: 相对同步前本地分支尖端新增的提交数；Created=true 时为 0（无基准可比）
//   - Created: 本地此前没有该分支，本次新建
type Result struct {
	Branch  string
	Commits int
	Created bool
}

// Fetch 把远程任务分支同步到本地同名分支。
//
// 参数：
//   - ctx: 上层上下文；内部叠加 FetchTimeout
//   - o:   同步参数，见 Opts
//
// 返回：
//   - Result: 同步增量
//   - err: LocalRepo 不是 git 仓库、参数非法、ssh/网络失败、或本地分支与远程分叉
//     （非快进）时返回错误，错误文本含 git stderr 原文
//
// 注意：
//   - 非快进由 git 自身拒绝（fetch <src>:<dst> 的默认语义），这正是要的行为——
//     宁可报错也不能悄悄覆盖审核者的本地提交
func Fetch(ctx context.Context, o Opts) (Result, error) {
	if o.LocalRepo == "" || o.RemoteURL == "" || o.Branch == "" {
		return Result{}, fmt.Errorf("同步参数不完整：local=%q remote=%q branch=%q", o.LocalRepo, o.RemoteURL, o.Branch)
	}
	// 以 "-" 开头的分支名会被 git 当成选项（参数注入面），与 workspace 侧同款拒绝
	if strings.HasPrefix(o.Branch, "-") || strings.HasPrefix(o.RemoteURL, "-") {
		return Result{}, fmt.Errorf("分支名/远程地址不允许以 - 开头：branch=%q remote=%q", o.Branch, o.RemoteURL)
	}
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	if _, _, err := run(ctx, o.LocalRepo, "rev-parse", "--git-dir"); err != nil {
		return Result{}, fmt.Errorf("%s 不是 git 仓库，跳过同步: %w", o.LocalRepo, err)
	}
	before, _, err := run(ctx, o.LocalRepo, "rev-parse", "--verify", "--quiet", "refs/heads/"+o.Branch)
	created := err != nil || strings.TrimSpace(before) == ""

	log().Info("本地同步开始", "local", o.LocalRepo, "remote", o.RemoteURL, "branch", o.Branch, "created", created)
	refspec := o.Branch + ":" + o.Branch
	if _, stderr, ferr := run(ctx, o.LocalRepo, "fetch", o.RemoteURL, refspec); ferr != nil {
		log().Error("本地同步失败", "local", o.LocalRepo, "remote", o.RemoteURL, "branch", o.Branch,
			"stderr", strings.TrimSpace(stderr), "cause", ferr)
		return Result{}, fmt.Errorf("git fetch %s %s: %s: %w", o.RemoteURL, refspec, strings.TrimSpace(stderr), ferr)
	}

	res := Result{Branch: o.Branch, Created: created}
	if !created {
		// 增量 = 同步前尖端..同步后尖端；数不出来不算失败（同步本身已成功）
		countOut, _, cerr := run(ctx, o.LocalRepo, "rev-list", "--count", strings.TrimSpace(before)+".."+o.Branch)
		if cerr == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(countOut)); perr == nil {
				res.Commits = n
			}
		}
	}
	log().Info("本地同步完成", "branch", res.Branch, "commits", res.Commits, "created", res.Created)
	return res, nil
}

// run 在 dir 里执行 git 命令，返回 stdout 与 stderr。
func run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}
