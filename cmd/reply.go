// 本文件实现 handoff reply 子命令：回答一个待办工单（权限门/提问）。
//
// 职责：
//   - 把协调者的裁决转成应答原文：--approve → "allow"、--deny [--reason] → "deny[:原因]"、
//     --answer 原样透传，经 client.Reply 交给 agentd
//   - 成功时单行输出 {"ok":true}（供上层脚本解析）
//
// 边界：
//   - 不解释应答语义：answer 原样落库，含义由 manager 侧解释（allow → once / 其余 → reject）
//   - 不校验任务状态（工单是否存在、已回答等由 agentd 判定并返回错误）
package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	replyTicketID string
	replyApprove  bool
	replyDeny     bool
	replyReason   string
	replyAnswer   string
)

// replyCmd 回答一个工单：三种裁决方式互斥，恰好指定一种。
//
// 使用方式：handoff reply <task> --ticket <id> (--approve | --deny [--reason r] | --answer "text")
var replyCmd = &cobra.Command{
	Use:   "reply <task>",
	Short: "回答一个待办工单（权限门/提问）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		answer, err := composeAnswer()
		if err != nil {
			return err
		}
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).Reply(cmd.Context(), taskID, replyTicketID, answer); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

// composeAnswer 把 --approve/--deny/--answer 三种裁决方式翻译成应答原文。
//
// 规则（与 manager 侧语义对齐，见 Task 8 计划）：
//   - --approve  → "allow"（manager 收到 allow 才回 once）
//   - --deny     → "deny"；带 --reason 时拼成 "deny: <原因>"（manager 对 allow 之外的
//     一律回 reject，原因随应答落库供后续追溯）
//   - --answer   → 原样透传（提问的自由文本回答，不加工）
func composeAnswer() (string, error) {
	modes := 0
	if replyApprove {
		modes++
	}
	if replyDeny {
		modes++
	}
	if replyAnswer != "" {
		modes++
	}
	if modes != 1 {
		return "", errors.New("--approve、--deny、--answer 必须且只能指定一种")
	}
	if replyApprove {
		return "allow", nil
	}
	if replyDeny {
		if reason := strings.TrimSpace(replyReason); reason != "" {
			return "deny: " + reason, nil
		}
		return "deny", nil
	}
	return replyAnswer, nil
}

func init() {
	replyCmd.Flags().StringVar(&replyTicketID, "ticket", "", "待回答的工单 ID（必须）")
	replyCmd.Flags().BoolVar(&replyApprove, "approve", false, "批准权限门（对应 allow）")
	replyCmd.Flags().BoolVar(&replyDeny, "deny", false, "拒绝权限门（对应 deny）")
	replyCmd.Flags().StringVar(&replyReason, "reason", "", "拒绝原因（可选，与 --deny 搭配）")
	replyCmd.Flags().StringVar(&replyAnswer, "answer", "", "自由文本回答（提问场景）")
	rootCmd.AddCommand(replyCmd)
}
