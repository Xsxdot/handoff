// 本文件实现 handoff fetch 子命令：读取任务仓库内文件内容（审核取上下文用）。
//
// 职责：
//   - 调 client.Fetch 拉取仓库内相对路径文件并原文输出到 stdout
//
// 边界：
//   - 不修改文件；路径由审核者指定，逃逸路径由 agentd 拒绝
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// fetchCmd 输出任务仓库内指定文件的内容。
//
// 使用方式：handoff fetch <task> <文件路径（相对仓库根）>
var fetchCmd = &cobra.Command{
	Use:   "fetch <task> <文件路径>",
	Short: "输出任务仓库内文件的内容（审阅上下文）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		content, err := client.New(addr, token).Fetch(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), content)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(fetchCmd)
}
