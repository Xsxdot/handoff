// 本文件实现 handoff show 子命令：输出任务的完整现场快照。
//
// 职责：
//   - 调用 client.Attach 拉取任务 + 待办工单 + 最近事件，单行输出完整 AttachInfo JSON——
//     pending_tickets 是审核者恢复现场（「我还没答哪些」）的关键数据源
//
// 边界：
//   - 只读快照，不修改任何状态
//   - 二期起快照命令从一期 attach 更名而来：attach 改为终端实况（见 attach.go），
//     本命令是审核者会话恢复的关键数据源，供 wait/tasks/show 之外的脚本解析
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// showCmd 输出指定任务的完整现场快照（任务 + 待办工单 + 最近事件）。
var showCmd = &cobra.Command{
	Use:   "show <task>",
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
	rootCmd.AddCommand(showCmd)
}
