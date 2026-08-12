// 本文件实现 handoff done 子命令：归档任务。
//
// 职责：
//   - 审核通过后调用 client.Done 把任务置为 completed 并回收 executor（任务必须
//     处于 waiting_review）
//   - 成功时单行输出 {"ok":true}（供上层脚本解析）
//
// 边界：
//   - 不做 push 等归档后动作（按任务配置决定是否 push 不在 MVP 范围）
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// doneCmd 归档任务（要求任务处于 waiting_review）。
//
// 使用方式：handoff done <task>
var doneCmd = &cobra.Command{
	Use:   "done <task>",
	Short: "归档任务（要求任务处于待审核）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if _, err := client.New(addr, token).Done(cmd.Context(), taskID, ""); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doneCmd)
}
