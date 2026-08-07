// 本文件实现 handoff wait 子命令：阻塞等待任务的下一个可动作事件并输出单行 JSON。
//
// 职责：
//   - 调用 client.WaitEvent（progress 不唤醒、断线自动退避重连、cursor 续拉），
//     事件到达时把完整事件 JSON 单行输出到 stdout（供上层脚本解析）
//   - --notify：事件到达时发 macOS 系统通知（spec §7 风险#4 的兜底：审核者会话
//     不在时提醒其重新拉起），失败仅 Warn 不影响主流程
//   - 收到 SIGINT（Ctrl+C）时由进程默认行为终止，WaitEvent 随 ctx 取消退出
//
// 边界：
//   - 不做事件语义判断与审批（审批在审核者脑中），事件原样输出
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/logx"
	"github.com/xushixin/handoff/internal/proto"
)

// notifyFlag 为 true 时事件到达同时发 macOS 系统通知（spec §7 风险#4 的兜底）。
var notifyFlag bool

// waitCmd 阻塞等待指定任务的下一个可动作事件。
//
// 使用方式：handoff wait <task> —— 事件到达打印 {"seq":..,"type":..,"payload":..} 退出 0；
// 加 --notify 时同时发 macOS 系统通知。
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
		if notifyFlag {
			notifyEvent(ev)
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("序列化事件: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return nil
	},
}

// notifyEvent 发 macOS 系统通知提醒审核者事件已到达（--notify 的兜底实现）。
//
// 参数：
//   - ev: WaitEvent 返回的事件（client 返回指针，nil 由调用方保证不会传入）
//
// 注意：
//   - 仅 darwin 生效：其他平台静默跳过（Debug 记录），避免误报「发送失败」
//   - osascript 失败只打 Warn、绝不影响 wait 主流程：通知是锦上添花的兜底，
//     stdout 的 JSON 与 stderr 日志才是事件送达的主通道
func notifyEvent(ev *proto.Event) {
	if runtime.GOOS != "darwin" {
		slog.Debug("非 macOS，--notify 忽略", "task", ev.TaskID, "type", ev.Type)
		return
	}
	msg := "任务 " + id8(ev.TaskID) + ": " + string(ev.Type)
	// strconv.Quote 把文本安全地转成 AppleScript 字符串字面量（转义引号/反斜杠）。
	// 事件类型与任务 id 均为 ASCII，不存在 AppleScript 不认的转义序列
	script := "display notification " + strconv.Quote(msg) + " with title " + strconv.Quote("handoff")
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		slog.Warn("发送系统通知失败", "cause", err, "output", truncateBytes(string(out), 200))
	}
}

// id8 取字符串前 8 个字符（通知文案用，与 tmux 会话名同规则）。
func id8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// truncateBytes 将字符串截断为最多 n 个字节（osascript 输出截断，日志用）。
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func init() {
	waitCmd.Flags().BoolVar(&notifyFlag, "notify", false, "事件到达时发 macOS 系统通知（spec §7 兜底）")
	rootCmd.AddCommand(waitCmd)
}
