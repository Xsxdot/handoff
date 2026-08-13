// 本文件实现 handoff pull 子命令：把远程执行机上的任务分支同步到本地仓库。
//
// 职责：
//   - 查任务拿到 target/仓库路径/分支，换算出 ssh 形式的远程地址并 fetch 到本地同名分支
//
// 边界：
//   - 只 fetch，不 checkout、不合并（合并是协调者的决定）
//   - 本机任务（无 target）无需同步：代码本来就在同一台机器上
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/localsync"
	"github.com/xushixin/handoff/internal/proto"
)

// pullCmd 把指定任务的远程分支同步到本地。
var pullCmd = &cobra.Command{
	Use:   "pull <task>",
	Short: "把远程任务分支同步到本地仓库（只 fetch，不 checkout）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		info, err := client.New(addr, token).Attach(cmd.Context(), args[0])
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
//   - task: 任务快照（需要 Target / RepoPath / Branch 三个字段）
//
// 返回：
//   - 同步结果；任务不是远程任务、缺分支、target 未配置或 fetch 失败时返回错误
//
// 注意：
//   - 远程地址由 sshHostFromTarget(task.Target 的 Target 配置) 与 task.RepoPath
//     拼成 host:/path（attach 与 pull 共用同一个换算点，user 可配置）
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
	return localsync.Fetch(ctx, localsync.Opts{
		LocalRepo: cwd, RemoteURL: sshHostFromTarget(t) + ":" + task.RepoPath, Branch: task.Branch,
	})
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
