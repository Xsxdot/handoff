// 本文件实现 handoff stop 子命令：主动中止一个还在跑的任务。
//
// 职责：
//   - 调用 agentd 的 stop 路由，停 executor、作废挂起工单、任务落 failed
//
// 边界：
//   - 不删任务分支、不删 worktree（归档清理是 handoff done 的职责）
//   - 不做「停完再重派」：重派是独立决定，由审核者显式 dispatch
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
		if err := client.New(addr, token).Stop(cmd.Context(), taskID); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "任务 %s 已中止（状态 failed，分支与 worktree 保留）\n", taskID)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
