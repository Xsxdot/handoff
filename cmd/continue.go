// 本文件实现 handoff continue 子命令：向任务续发修改指令。
//
// 职责：
//   - 把协调者的修改指令经 client.Continue 原样透传给 executor（同一会话续接，
//     上下文完整保留；任务必须处于 waiting_review）
//   - 成功时单行输出 {"ok":true}（供上层脚本解析）
//
// 边界：
//   - 不解释指令语义，原文透传；任务状态校验由 agentd 判定并返回错误
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// continueCmd 向任务续发修改指令，要求任务处于 waiting_review。
//
// 使用方式：handoff continue <task> "<指令>"（指令含空格时用引号包裹）
var continueCmd = &cobra.Command{
	Use:   "continue <task> <指令>",
	Short: "向任务续发修改指令（要求任务处于待审核）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID, instructions := args[0], args[1]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).Continue(cmd.Context(), taskID, instructions); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(continueCmd)
}
