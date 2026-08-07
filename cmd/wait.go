// 本文件实现 handoff wait 子命令：阻塞等待任务的下一个可动作事件并输出单行 JSON。
//
// 职责：
//   - 调用 client.WaitEvent（progress 不唤醒、断线自动退避重连、cursor 续拉），
//     事件到达时把完整事件 JSON 单行输出到 stdout（供上层脚本解析）
//   - 收到 SIGINT（Ctrl+C）时由进程默认行为终止，WaitEvent 随 ctx 取消退出
//
// 边界：
//   - 不做事件语义判断与审批（审批在审核者脑中），事件原样输出
//   - --notify 系统通知在 Task 12 实现（本任务只挂 flag 占位，接受该参数不报错）
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/logx"
)

// notifyFlag 为 true 时事件到达同时发 macOS 系统通知（Task 12 实现，本任务仅占位接受）。
var notifyFlag bool

// waitCmd 阻塞等待指定任务的下一个可动作事件。
//
// 使用方式：handoff wait <task> —— 事件到达打印 {"seq":..,"type":..,"payload":..} 退出 0。
var waitCmd = &cobra.Command{
	Use:   "wait <task>",
	Short: "阻塞等待任务的下一个可动作事件（question/permission_request 等）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		// 统一日志格式：wait 是长驻命令，stderr 日志是「为什么没唤醒」的唯一线索
		slog.SetDefault(logx.Setup("cli", ""))

		ev, err := client.New(addr, token).WaitEvent(cmd.Context(), taskID, false)
		if err != nil {
			return err
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("序列化事件: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

func init() {
	waitCmd.Flags().BoolVar(&notifyFlag, "notify", false, "事件到达时发 macOS 系统通知（Task 12 实现）")
	rootCmd.AddCommand(waitCmd)
}
