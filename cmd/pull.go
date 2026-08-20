// 本文件实现 handoff pull 子命令：把远程执行机上的任务分支同步到本地仓库。
//
// 职责：
//   - 查任务拿到 target/仓库路径/分支，取回任务分支的提交并 fetch 到本地同名分支
//   - 决定走哪条路：优先经 agentd 的 HTTP 面取 git bundle，仅在对端过旧（404）时
//     退回 ssh 老路
//
// 边界：
//   - 只 fetch，不 checkout、不合并（合并是协调者的决定）
//   - 本机任务（无 target）无需同步：代码本来就在同一台机器上
//   - 不做 git bundle 的生成：那在 agentd 侧（internal/agentd/bundle.go）
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/localsync"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// pullCmd 把指定任务的远程分支同步到本地。
var pullCmd = &cobra.Command{
	Use:   "pull <task>",
	Short: "把远程任务分支同步到本地仓库（只 fetch，不 checkout）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		info, err := c.Attach(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		res, err := syncTaskBranch(cmd.Context(), &info.Task.Task)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), syncMessage(res))
		return nil
	},
}

// syncTaskBranch 把任务的远程分支同步到本地 cwd 仓库。
//
// 参数：
//   - task: 任务快照（需要 ID / Target / RepoPath / Branch / BaseCommit 五个字段）
//
// 返回：
//   - 同步结果；任务不是远程任务、缺分支、target 未配置或取回失败时返回错误
//
// 注意：
//   - **降级只对 404 发生**：对端 agentd 过旧（没有 bundle 端点）才退回 ssh；
//     其它错误如实报错。对任何错误都回落会把一次真失败伪装成「老路也能跑」
//   - ssh 老路的远程地址由 sshHostFromTarget(target 配置) 与 task.RepoPath 拼成
//     host:/path。它在 Windows 执行机上不可用（git 的 ssh transport 假定远端登录
//     shell 是 POSIX，cmd.exe 不剥单引号），保留它只为兼容尚未换版的老 agentd
//   - 用 RepoPath 而不是 Workdir()：worktree 是主仓的从属工作树，分支对象在主仓库里
func syncTaskBranch(ctx context.Context, task *proto.Task) (localsync.Result, error) {
	if task.Target == "" {
		return localsync.Result{}, fmt.Errorf("任务 %s 是本机任务，无需同步", task.ID)
	}
	if task.Branch == "" {
		return localsync.Result{}, fmt.Errorf("任务 %s 尚无分支，无可同步", task.ID)
	}
	cfg := loadCLIConfig()
	t, ok := cfg.Targets[task.Target]
	if !ok {
		return localsync.Result{}, fmt.Errorf("target %q 未在配置中定义，无法换算远程地址", task.Target)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return localsync.Result{}, fmt.Errorf("取当前目录: %w", err)
	}
	// have 取任务记录里的基线，但**发出前先在本地核实自己真有**——协调者可能换了
	// 机器接管、或在另一个克隆里执行 pull，不能假设它持有这个提交。核实不过就不带
	// have，服务端给全量包
	have := ""
	if hasLocalCommit(ctx, cwd, task.BaseCommit) {
		have = task.BaseCommit
	}
	if t.IsRelay() {
		// Relay executor may sit behind NAT, where git-over-ssh cannot reach it;
		// Bundle-over-HTTP is the only pull path that stays inside the tunnel.
		c, cleanup, err := newTargetClientNamed(task.Target)
		if err != nil {
			return localsync.Result{}, err
		}
		defer cleanup()
		slog.Default().Info("pull via bundle over relay (ssh skipped)", "target", task.Target, "task", task.ID)
		return syncViaBundleClient(ctx, c, task.ID, have, task.Branch, cwd)
	}
	// 用 task.Target 对应的配置而不是 --target 标志：任务自己知道它在哪台机器上
	res, err := syncViaBundle(ctx, "http://"+t.Addr, t.Token, task.ID, have, task.Branch, cwd)
	if err == nil {
		slog.Default().Info("回程同步走 agentd HTTP bundle",
			"task", task.ID, "target", task.Target, "branch", task.Branch, "have", have)
		return res, nil
	}
	if !errors.Is(err, client.ErrBundleUnsupported) {
		return localsync.Result{}, err
	}
	// 到这里只可能是 404。把「为什么回落」一并写出来——排障时第一个要问的就是
	// 「这次走的哪条路」，只说「用了 ssh」等于没说
	slog.Default().Info("对端 agentd 无 bundle 端点（404），回程同步回落 ssh 老路",
		"task", task.ID, "target", task.Target, "branch", task.Branch)
	return localsync.Fetch(ctx, localsync.Opts{
		LocalRepo: cwd, RemoteURL: sshHostFromTarget(t) + ":" + task.RepoPath, Branch: task.Branch,
	})
}

// syncViaBundle 经 agentd 的 HTTP 面取 bundle 并 fetch 进本地仓库。
//
// 参数：
//   - addr, token: agentd 端点与 Bearer token
//   - taskID:      完整任务 UUID
//   - have:        本地已核实存在的基线提交；空串请求全量包
//   - branch:      任务分支名
//   - localRepo:   本地仓库路径（fetch 的落点）
//
// 返回：
//   - 同步结果；空区间时返回 Result{Branch: branch}（即「已是最新」）
//   - err 为 client.ErrBundleUnsupported 时表示对端过旧，**由调用方**决定回落；
//     本函数自己不回落
//
// 注意：
//   - bundle 落 OS 临时目录并 defer os.Remove，**绝不能落在 localRepo 里**——
//     那会弄脏协调者的工作区，而干净工作区是 dispatch 的前置条件
//   - 包拿到了但 fetch 失败时如实报错、不回落：包已到手说明 HTTP 这条路是通的，
//     失败在 git 侧（如缺前置对象），换 ssh 重来只会掩盖它
//   - **没有「空区间」这条分支**：服务端保证包里一定带 ref（放宽区间，spec §5.2），
//     所以「有没有新提交」由 localsync.Fetch 的 Result 如实反映，本地分支引用
//     则总是被 fetch 建出来——与 ssh 老路逐字一致
func syncViaBundle(ctx context.Context, addr, token, taskID, have, branch, localRepo string) (localsync.Result, error) {
	return syncViaBundleClient(ctx, client.New(addr, token), taskID, have, branch, localRepo)
}

func syncViaBundleClient(ctx context.Context, c *client.Client, taskID, have, branch, localRepo string) (localsync.Result, error) {
	rc, err := c.Bundle(ctx, taskID, have)
	if err != nil {
		return localsync.Result{}, err
	}
	defer rc.Close()

	f, err := os.CreateTemp("", "handoff-bundle-*.bundle")
	if err != nil {
		return localsync.Result{}, fmt.Errorf("创建 bundle 临时文件: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	n, copyErr := io.Copy(f, rc)
	closeErr := f.Close()
	if copyErr != nil {
		slog.Default().Error("下载 bundle 失败", "task", taskID, "received", n, "cause", copyErr)
		return localsync.Result{}, fmt.Errorf("下载 bundle（已收 %d 字节）: %w", n, copyErr)
	}
	if closeErr != nil {
		return localsync.Result{}, fmt.Errorf("写入 bundle 临时文件: %w", closeErr)
	}
	slog.Default().Info("bundle 下载完成", "task", taskID, "branch", branch, "bytes", n)

	// git 把 bundle 文件当作一种合法 transport，所以这里把文件路径直接当 RemoteURL
	// 交给现有的 localsync.Fetch——它的文档注释里就写着「也接受本地路径」。
	// 这正是 localsync 一行都不用改的原因
	return localsync.Fetch(ctx, localsync.Opts{LocalRepo: localRepo, RemoteURL: path, Branch: branch})
}

// hasLocalCommit 报告本地仓库里是否已有该提交对象。
//
// 参数：
//   - repo: 本地仓库路径
//   - sha:  完整或缩写的提交号
//
// 返回：true 表示对象存在且是一个 commit；空 sha、以 - 开头的 sha、仓库不可用
// 一律返回 false（不是错误——「没有」是这里的正常答案）。
//
// 为什么拒绝 - 前缀：git 会把它解释为选项，属参数注入面（与 agentd 侧
// ErrBadBaseBranch 同源）。
func hasLocalCommit(ctx context.Context, repo, sha string) bool {
	if repo == "" || sha == "" || strings.HasPrefix(sha, "-") {
		return false
	}
	err := exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-e", sha+"^{commit}").Run()
	if err != nil {
		slog.Default().Debug("本地无该基线提交，将请求全量包", "repo", repo, "sha", sha)
		return false
	}
	return true
}

// syncMessage 把同步结果压成一行给协调者看的中文说明。
func syncMessage(res localsync.Result) string {
	if res.Created {
		return fmt.Sprintf("已同步分支 %s（本地新建）", res.Branch)
	}
	return fmt.Sprintf("已同步分支 %s（新增 %d 个提交）", res.Branch, res.Commits)
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
