// 本文件实现 handoff resume 子命令：解开卡死的任务。
//
// 职责：
//   - 调用 client.Resume 让 agentd 重投「已落库但未送达 executor」的应答，
//     并对断连窗口内丢失的回合终态做会话对账（B38）
//   - 原样输出恢复报告 JSON（重投条数 / 对账结果 / executor 是否已不在 /
//     收尾状态 / 结论）
//
// 边界：
//   - 不自己判断任务是否卡死，也不改任何状态：判定与收尾全在 agentd 侧
//     （Manager.RecoverStuck），CLI 只负责发起与呈现
//   - 与 continue/done 的分工：那两条要求任务已在待审核；本条专治两类中间态
//     ——「reply 拿到 502 之后 reply/continue/done 三条路全封死」，以及
//     「agentd 与 executor 断连期间回合已完结、终态事件丢失、任务冻死在 running」
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// resumeForce 对应 --force：对账判不出来时仍强制收口到待审核。
var resumeForce bool

// resumeCmd 恢复卡死的任务。
//
// 使用方式：handoff resume <task> [--force]
//
// 两类卡死都走这条命令，协调者不必自行诊断是哪一类：
//   - 应答已落库但没送到 executor（reply 拿到 502）→ 重投
//   - agentd 与 executor 断连期间回合已完结、终态事件丢失（B38）→ 会话对账补发
//
// --force：对账判不出来时（executor 不支持对账 / 会话确实还在忙 / 查询失败）
// 仍把任务收口到待审核，使 continue/done 可用。**保住 executor 会话**——
// 这是它与 handoff stop 的根本区别：stop 会杀掉会话并把任务落成 failed。
// 收口会留下一条写明「人工强制、未经 executor 确认」的事件。
var resumeCmd = &cobra.Command{
	Use:   "resume <task>",
	Short: "恢复卡死的任务（重投未送达的应答，或对账补回丢失的回合终态）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		report, err := c.Resume(cmd.Context(), taskID, resumeForce)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), report)
		return nil
	},
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeForce, "force", false,
		"对账判不出来时仍强制收口到待审核（保住 executor 会话，不同于 stop）")
	rootCmd.AddCommand(resumeCmd)
}
