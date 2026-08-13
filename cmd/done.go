// 本文件实现 handoff done 子命令：归档任务。
//
// 职责：
//   - 审核通过后调用 client.Done 把任务置为 completed 并回收 executor（任务必须
//     处于 waiting_review）
//   - 成功时单行输出 {"ok":true}（供上层脚本解析）
//   - 携带可选完成说明（--note）并在 stderr 提示保存结果
//
// 边界：
//   - 不做 push 等归档后动作（按任务配置决定是否 push 不在 MVP 范围）
//   - 不做说明内容的校验与加工（只校验长度）
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// doneNote 是本次归档的完成说明（--note）；空串=不留说明。
//
// 为什么是可选而不是必填：现有所有 handoff done <id> 调用（skill 文档、
// e2e-checklist、旧脚本）都不带它，必填会一次性打断全部；而强制也不产生质量，
// 只会退化成敷衍的一个字。取而代之的是缺省时在 stderr 打一行提醒。
var doneNote string

var doneCmd = &cobra.Command{
	Use:   "done <task>",
	Short: "归档任务（要求任务处于待审核）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		// 本地可判的错误就地拒绝，不打一趟网络再被 400 拒回：那会把一次确定的
		// 失败变成一次依赖对端版本的失败。不截断的理由见 proto.MaxDoneNoteBytes
		if len(doneNote) > proto.MaxDoneNoteBytes {
			return fmt.Errorf("--note 超长（%d 字节，上限 %d）；请精简后重试，不会自动截断",
				len(doneNote), proto.MaxDoneNoteBytes)
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cli := client.New(addr, token)
		noteSaved, err := cli.Done(cmd.Context(), taskID, doneNote)
		if err != nil {
			return err
		}
		// 兜底回收：协调者可能从不跑 wait/follow（直接 dispatch → done），
		// 那条通道就观察不到 archived 事件。两条通道幂等，先到者生效
		cli.DropCursor(taskID)
		// stdout 恒为单行 {"ok":true}：上层脚本按此解析，人读的信息一律走 stderr
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		switch {
		case doneNote == "":
			fmt.Fprintln(cmd.ErrOrStderr(),
				`本次归档未留说明（下次可加 --note "一句话说明这次做完了什么"）`)
		case !noteSaved:
			// 归档成功了，丢的只是说明——退出码保持 0，但必须说出来，
			// 否则协调者以为自己留了话（B30 那类哑失败）
			fmt.Fprintln(cmd.ErrOrStderr(),
				"说明未保存：对端 agentd 版本较旧，不支持归档说明。任务已正常归档。")
		}
		return nil
	},
}

func init() {
	doneCmd.Flags().StringVar(&doneNote, "note", "",
		"归档说明：一句话记下这次做完了什么（会写进任务记录与 archived 事件）")
	rootCmd.AddCommand(doneCmd)
}
