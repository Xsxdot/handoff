// 本文件实现隐藏子命令 handoff _shim：执行者进程的承载壳。
//
// 职责：
//   - 解析 --spec，把控制权交给 prochost.RunShim（阻塞到执行者退出）
//
// 边界：
//   - 不做任何业务判断：全部逻辑在 prochost.RunShim 里，本文件只是 cobra 包装
//   - 不面向用户：Hidden=true，不出现在 help 里。它由 agentd 自己拉起，
//     人手动跑没有意义（缺 spec.json 就什么都做不了）
package cmd

import (
	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/prochost"
)

// shimSpecPath 是 --spec 的绑定变量。
var shimSpecPath string

// shimCmd 是执行者进程的承载壳，由 agentd 经 prochost.Start 拉起。
var shimCmd = &cobra.Command{
	Use:    "_shim",
	Short:  "执行者进程承载壳（内部使用）",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return prochost.RunShim(shimSpecPath)
	},
}

func init() {
	shimCmd.Flags().StringVar(&shimSpecPath, "spec", "", "spec.json 路径（必填）")
	_ = shimCmd.MarkFlagRequired("spec")
	rootCmd.AddCommand(shimCmd)
}
