// 本文件实现 handoff attach 子命令：输出任务的完整现场快照。
//
// 职责：
//   - 调用 client.Attach 拉取任务 + 待办工单 + 最近事件，单行输出完整 AttachInfo JSON——
//     pending_tickets 是审核者恢复现场（「我还没答哪些」）的关键数据源
//
// 边界：
//   - 只读快照，不修改任何状态
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// attachCmd 输出指定任务的完整现场快照（任务 + 待办工单 + 最近事件）。
var attachCmd = &cobra.Command{
	Use:   "attach <task>",
	Short: "输出任务完整现场快照（含待办工单）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		info, err := client.New(addr, token).Attach(cmd.Context(), taskID)
		if err != nil {
			return err
		}
		b, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("序列化现场快照: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}
