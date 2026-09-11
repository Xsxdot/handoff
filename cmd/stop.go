// 本文件实现 handoff stop 子命令：主动中止一个还在跑的任务。
//
// 职责：
//   - 调用 agentd 的 stop 路由，停 executor、作废挂起工单、任务落 failed
//   - 依据响应体 worktree_removed 打印与实际行为一致的提示：
//     成功 Stop 不删树；文案不得把 false 说成清理失败
//
// 边界：
//   - 不删任务分支（那是协调者的工作成果，审阅/回滚仍可切回分支）
//   - 不做「停完再重派」：重派是独立决定，由协调者显式 dispatch
package cmd

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

func stopOutcomeLine(taskID string, removed bool) string {
	if removed {
		return fmt.Sprintf("任务 %s 已中止（状态 failed，managed worktree 已删除，分支保留）\n", taskID)
	}
	return fmt.Sprintf("任务 %s 已中止（状态 failed，现场已留存，显式 reclaim/gc 才清，分支保留）\n", taskID)
}

func runStop(cmd *cobra.Command, c client.ExecutionClient, taskID string) error {
	removed, err := c.Stop(cmd.Context(), taskID)
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), stopOutcomeLine(taskID, removed))
	return nil
}

// stopCmd 中止指定任务。
var stopCmd = &cobra.Command{
	Use:   "stop <task>",
	Short: "中止任务（停 executor，任务落 failed）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		return runStop(cmd, c, taskID)
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
