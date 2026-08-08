// 本文件实现 handoff run 子命令：在任务仓库远程执行审阅命令（跑测试/lint）。
//
// 职责：
//   - 把命令原文透传给 agentd 执行（sh -c，10min 超时），合并输出原文打印；
//     非零退出码以错误返回（cobra 打印到 stderr），输出已先行打印
//
// 边界：
//   - 只透传命令，不解释输出语义；命令执行于任务仓库，由 agentd 限时回收
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// runCmd 在任务仓库执行一条审阅命令并输出合并结果。
//
// 使用方式：handoff run <task> <命令...>（如 handoff run T1 go test ./...）
var runCmd = &cobra.Command{
	Use:   "run <task> <命令...>",
	Short: "在任务仓库执行审阅命令（跑测试/lint）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		stdout, code, err := client.New(addr, token).Run(cmd.Context(), args[0], strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), stdout)
		if code != 0 {
			return fmt.Errorf("命令退出码 %d（输出已打印）", code)
		}
		return nil
	},
}

func init() {
	// 关闭 flag 穿插解析（P1-13）：`handoff run T1 go test -v ./...` 中任务名后的
	// -v/-race/-run 是审阅命令自身的参数，必须原样进入 args[1:]；cobra 默认的
	// Interspersed 会把它们当 handoff 的未知 flag 直接报错，审核者最主要的验证
	// 动作不可用。SetInterspersed(false) 让解析在首个位置参数（任务名）处停止，
	// 之后全部按位置参数透传；--agentd/--target 等 handoff 自有 flag 需写在任务名之前
	runCmd.Flags().SetInterspersed(false)
	rootCmd.AddCommand(runCmd)
}
