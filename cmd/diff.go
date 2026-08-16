// 本文件实现 handoff diff 子命令：取任务分支相对基准分支的审阅素材（git diff + 提交列表）。
//
// 职责：
//   - 调 client.Diff 拉取 diff 文本并原文输出到 stdout（协调者阅读/管道分析用）
//
// 边界：
//   - 不做 diff 语义判断；基准分支可经 --base 指定，缺省由 agentd 按仓库默认分支推导
package cmd

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

var diffBase string

// diffCmd 输出任务的 git diff 与提交列表。
//
// 使用方式：handoff diff <task> [--base <分支>]
var diffCmd = &cobra.Command{
	Use:   "diff <task>",
	Short: "输出任务的 git diff 与提交列表（审阅素材）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		diff, err := client.New(addr, token).Diff(cmd.Context(), args[0], diffBase)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), diff)
		return nil
	},
}

func init() {
	diffCmd.Flags().StringVar(&diffBase, "base", "", "基准分支（默认按仓库默认分支推导）")
	rootCmd.AddCommand(diffCmd)
}
