// 本文件实现 handoff stop 子命令：主动中止一个还在跑的任务。
//
// 职责：
//   - 调用 agentd 的 stop 路由，停 executor、作废挂起工单、任务落 failed
//   - 依据响应体 worktree_removed 打印与实际行为一致的提示：managed worktree
//     （agentd 建的）已删则如实告知，用户自带 worktree / 原地模式则说明保留
//
// 边界：
//   - 不删任务分支（那是协调者的工作成果，审阅/回滚仍可切回分支）
//   - 不做「停完再重派」：重派是独立决定，由协调者显式 dispatch
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// stopCmd 中止指定任务。
var stopCmd = &cobra.Command{
	Use:   "stop <task>",
	Short: "中止任务（停 executor，任务落 failed）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		removed, err := client.New(addr, token).Stop(cmd.Context(), taskID)
		if err != nil {
			return err
		}
		// worktree_removed 来自 agentd 响应体（不猜）：managed worktree 已删则
		// 明确告知，否则说明保留——两种提示都要说清「分支保留」
		if removed {
			fmt.Fprintf(cmd.OutOrStdout(), "任务 %s 已中止（状态 failed，managed worktree 已删除，分支保留）\n", taskID)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "任务 %s 已中止（状态 failed，分支与 worktree 保留）\n", taskID)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
