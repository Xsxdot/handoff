// 本文件实现 handoff diff 子命令：取任务分支相对基准分支的审阅素材（git diff + 提交列表）。
//
// 职责：
//   - 调 client.Diff 拉取 diff 文本并原文输出到 stdout（协调者阅读/管道分析用）
//
// 边界：
//   - 不做 diff 语义判断；基准可经 --base 指定，缺省优先用任务基线提交，没有才由
//     agentd 按仓库默认分支推导
package cmd

import (
	"fmt"

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
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		diff, err := c.Diff(cmd.Context(), args[0], diffBase)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), diff)
		return nil
	},
}

func init() {
	diffCmd.Flags().StringVar(&diffBase, "base", "", "基准（默认用任务基线提交，没有才按仓库默认分支推导）")
	rootCmd.AddCommand(diffCmd)
}
