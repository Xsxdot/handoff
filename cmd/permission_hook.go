// 本文件实现 handoff permission-hook 隐藏子命令：Antigravity CLI (agy) 的权限裁决钩子。
//
// 职责：
//   - 接收 agy 在 PreToolUse 钩子中经 stdin 投递的 JSON 负载
//   - 把每次授权请求经 unix socket 转给 agentd 侧的 agy adapter，阻塞等待人工/审批者裁决
//   - 把裁决还原成 agy 认识的 {"decision":"allow"|"deny", "reason":"..."} 写入 stdout
//
// 边界：
//   - 不读 handoff 配置、不连 agentd HTTP：唯一对外面就是 --sock 指定的路径
//   - 不做任何审批判断：连不上就重试等待，绝不自作主张放行（fail-closed）
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var hookPermSockPath string

type hookPreToolUseInput struct {
	ToolCall struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"toolCall"`
	StepIdx        int    `json:"stepIdx"`
	ConversationID string `json:"conversationId"`
}

type hookDecisionOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

var permissionHookCmd = &cobra.Command{
	Use:    "permission-hook",
	Short:  "Antigravity CLI 权限裁决钩子（内部使用）",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if hookPermSockPath == "" {
			return fmt.Errorf("--sock 不可为空")
		}
		return servePermissionHook(os.Stdin, os.Stdout, hookPermSockPath)
	},
}

func servePermissionHook(r io.Reader, w io.Writer, sockPath string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 stdin 失败: %v\n", err)
		enc := json.NewEncoder(w)
		_ = enc.Encode(hookDecisionOutput{Decision: "deny", Reason: "读取权限请求失败"})
		return nil
	}

	var req hookPreToolUseInput
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintf(os.Stderr, "解析 PreToolUse 入参失败: %v\n", err)
		enc := json.NewEncoder(w)
		_ = enc.Encode(hookDecisionOutput{Decision: "deny", Reason: "解析权限请求失败"})
		return nil
	}

	toolUseID := fmt.Sprintf("step_%d", req.StepIdx)

	askFrame, err := json.Marshal(map[string]any{
		"type":        "ask",
		"tool_use_id": toolUseID,
		"tool_name":   req.ToolCall.Name,
		"input":       req.ToolCall.Args,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化 ask 帧失败: %v\n", err)
		enc := json.NewEncoder(w)
		_ = enc.Encode(hookDecisionOutput{Decision: "deny", Reason: "序列化权限请求失败"})
		return nil
	}

	for attempt := 1; ; attempt++ {
		decision, err := exchange(sockPath, askFrame)
		if err == nil {
			fmt.Fprintf(os.Stderr, "裁决到达 tool_use_id=%s behavior=%s\n", toolUseID, decision.Behavior)
			out := hookDecisionOutput{
				Decision: decision.Behavior,
				Reason:   decision.Message,
			}
			if out.Decision != "allow" && out.Decision != "deny" {
				out.Decision = "deny"
			}
			return json.NewEncoder(w).Encode(out)
		}
		fmt.Fprintf(os.Stderr, "裁决通道不可用（第 %d 次），%v 后重试 tool_use_id=%s: %v\n",
			attempt, permDialRetryInterval, toolUseID, err)
		time.Sleep(permDialRetryInterval)
	}
}

func init() {
	permissionHookCmd.Flags().StringVar(&hookPermSockPath, "sock", "", "裁决 socket 路径")
	rootCmd.AddCommand(permissionHookCmd)
}
