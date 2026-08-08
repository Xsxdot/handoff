// 本文件实现 handoff resume 子命令：解开卡死的任务。
//
// 职责：
//   - 调用 client.Resume 让 agentd 重投「已落库但未送达 executor」的应答
//   - 原样输出恢复报告 JSON（重投条数 / executor 是否已不在 / 收尾状态 / 结论）
//
// 边界：
//   - 不自己判断任务是否卡死，也不改任何状态：判定与收尾全在 agentd 侧
//     （Manager.RecoverStuck），CLI 只负责发起与呈现
//   - 与 continue/done 的分工：那两条要求任务已在待审核；本条专治「reply 拿到
//     502 之后 reply/continue/done 三条路全封死」的中间态
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

// resumeCmd 恢复卡死的任务：重投未送达 executor 的应答。
//
// 使用方式：handoff resume <task>
//
// 典型场景：handoff reply 返回 502（应答已落库但没送到 executor）。此时工单
// 已被消耗、attach 看不到挂起项、continue/done 因状态不符被拒——执行本命令
// 让 agentd 把那条裁决重新送达；若 executor 确已不在，任务会被转交审核，
// 之后可正常 continue 重派或 done 归档。
var resumeCmd = &cobra.Command{
	Use:   "resume <task>",
	Short: "恢复卡死的任务（重投未送达 executor 的应答）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		report, err := client.New(addr, token).Resume(cmd.Context(), taskID)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), report)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resumeCmd)
}
