// ptyhost.go —— 隐藏子命令 handoff _ptyhost：单个 PTY 会话的承载进程。
//
// 职责：解析 --spec，把控制权交给 hostproc.Run（阻塞到会话收摊）。
//
// 边界：不做任何业务判断：全部逻辑在 hostproc.Run 里，本文件只是 cobra 包装；它不面向
// 用户，Hidden=true，由 agentd 自己拉起。
package cmd

import (
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/spf13/cobra"
)

var ptyhostSpecPath string

var ptyhostCmd = &cobra.Command{
	Use:    "_ptyhost",
	Short:  "PTY 会话承载进程（内部使用）",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return hostproc.Run(ptyhostSpecPath)
	},
}

func init() {
	ptyhostCmd.Flags().StringVar(&ptyhostSpecPath, "spec", "", "spec.json 路径（必填）")
	_ = ptyhostCmd.MarkFlagRequired("spec")
	rootCmd.AddCommand(ptyhostCmd)
}
