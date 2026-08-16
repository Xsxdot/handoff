// 本文件实现 handoff permission-mcp 隐藏子命令：Claude Code 的权限裁决 MCP server。
//
// 职责：
//   - 以 stdio JSON-RPC 提供一个 ask 工具，claude 经 --permission-prompt-tool 调用它
//   - 把每次授权请求经 unix socket 转给 agentd 侧的 adapter，阻塞等待人工/审批者裁决
//   - 把裁决还原成 claude 认识的 {"behavior":"allow"|"deny"} 返回
//
// 边界：
//   - 不读 handoff 配置、不连 agentd HTTP：唯一对外面就是 --sock 指定的路径，
//     被监管的 executor 因此拿不到 agentd token
//   - 不做任何审批判断：连不上就一直重试等待，绝不自作主张放行（fail-closed）
//
// 为什么是隐藏子命令而不是独立二进制：claude 侧只需要一个可执行文件路径，
// 复用 handoff 自身避免了额外分发与版本漂移。
//
// 日志例外（本文件唯一允许不用 slog 的地方）：stdout 是 JSON-RPC 通道，任何
// 非协议内容混入都会让 claude 侧解析失败，且本进程是被 claude 拉起的短命子进程，
// 不接 agentd 的 logger——诊断只能走 stderr 的 fmt.Fprintf。
package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// permSockPath 是 --sock 的绑定变量。
var permSockPath string

// permDialRetryInterval 是连不上 adapter 时的重试间隔。
//
// 为什么无限重试而不是超时放行：agentd 重启期间 socket 会短暂消失，此时放行
// 等于把审批链短路——宁可让 claude 一直等（表现为任务卡住，人能看到），
// 也不能静默放行（表现为一切正常，实际无人把关）。
const permDialRetryInterval = time.Second

// rpcRequest 是收到的 JSON-RPC 请求。ID 为空表示通知（不得回响应）。
type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// rpcResponse 是回给 claude 的 JSON-RPC 响应。
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// askToolDefinition 是暴露给 claude 的裁决工具定义。
//
// 入参三个字段由 claude 的 --permission-prompt-tool 约定填入：tool_name（被
// 授权的工具名）、input（该工具的原始入参）、tool_use_id（本次调用 id，
// handoff 直接拿它当 PermissionID）。
var askToolDefinition = map[string]any{
	"name":        "ask",
	"description": "Ask the handoff coordinator for permission to run a tool.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name":   map[string]any{"type": "string"},
			"input":       map[string]any{"type": "object"},
			"tool_use_id": map[string]any{"type": "string"},
		},
		"required": []string{"tool_name", "input"},
	},
}

// permissionMCPCmd 是 stdio MCP server 子命令（隐藏，不出现在 help 列表）。
var permissionMCPCmd = &cobra.Command{
	Use:    "permission-mcp",
	Short:  "Claude Code 权限裁决 MCP server（内部使用）",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if permSockPath == "" {
			return fmt.Errorf("--sock 不可为空")
		}
		return servePermissionMCP(permSockPath)
	},
}

// servePermissionMCP 读 stdin 的 JSON-RPC 行流并逐条应答，直到 stdin 关闭。
//
// 注意：
//   - 日志一律写 stderr（stdout 是 JSON-RPC 通道，混入任何非协议内容都会让
//     claude 侧解析失败）
func servePermissionMCP(sockPath string) error {
	fmt.Fprintf(os.Stderr, "handoff permission-mcp 启动，sock=%s\n", sockPath)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // 工具入参可能很大（长脚本）
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "解析请求失败，跳过: %v\n", err)
			continue
		}
		resp, ok := handleRPC(req, sockPath)
		if !ok {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "回写响应失败: %v\n", err)
			return err
		}
	}
	fmt.Fprintln(os.Stderr, "handoff permission-mcp 退出（stdin 关闭）")
	return sc.Err()
}

// handleRPC 处理一条 JSON-RPC 请求。
//
// 返回：
//   - resp: 待回写的响应
//   - ok: false 表示这是通知（无 id），不得回响应
//
// 注意：
//   - tools/call 会阻塞直到拿到裁决（fail-closed，见 permDialRetryInterval）
func handleRPC(req rpcRequest, sockPath string) (rpcResponse, bool) {
	if len(req.ID) == 0 {
		return rpcResponse{}, false // 通知：无需响应
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "handoff", "version": "1"},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": []any{askToolDefinition}}
	case "tools/call":
		resp.Result = map[string]any{"content": []any{
			map[string]any{"type": "text", "text": askDecision(req.Params, sockPath)},
		}}
	default:
		resp.Result = map[string]any{}
	}
	return resp, true
}

// askDecision 把一次授权请求转给 adapter 并阻塞等裁决，返回 claude 认识的裁决 JSON 文本。
func askDecision(params json.RawMessage, sockPath string) string {
	var p struct {
		Arguments struct {
			ToolName  string          `json:"tool_name"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		fmt.Fprintf(os.Stderr, "解析 tools/call 入参失败: %v\n", err)
		return `{"behavior":"deny","message":"handoff 无法解析授权请求"}`
	}
	askFrame, err := json.Marshal(map[string]any{
		"type": "ask", "tool_use_id": p.Arguments.ToolUseID,
		"tool_name": p.Arguments.ToolName, "input": p.Arguments.Input,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化 ask 帧失败: %v\n", err)
		return `{"behavior":"deny","message":"handoff 内部错误"}`
	}

	// 无限重试：agentd 重启期间 socket 会短暂消失，同一 tool_use_id 重连重登记
	// 后由 manager 侧 ticket 幂等去重，协调者不会被同一请求唤醒两次
	for attempt := 1; ; attempt++ {
		decision, err := exchange(sockPath, askFrame)
		if err == nil {
			fmt.Fprintf(os.Stderr, "裁决到达 tool_use_id=%s behavior=%s\n",
				p.Arguments.ToolUseID, decision.Behavior)
			b, _ := json.Marshal(map[string]any{
				"behavior": decision.Behavior, "message": decision.Message,
				"updatedInput": p.Arguments.Input,
			})
			return string(b)
		}
		fmt.Fprintf(os.Stderr, "裁决通道不可用（第 %d 次），%v 后重试 tool_use_id=%s: %v\n",
			attempt, permDialRetryInterval, p.Arguments.ToolUseID, err)
		time.Sleep(permDialRetryInterval)
	}
}

// exchange 连一次 socket：发 ask 帧、读一条裁决帧。
func exchange(sockPath string, askFrame []byte) (struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message"`
}, error) {
	var decision struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return decision, fmt.Errorf("连 %s: %w", sockPath, err)
	}
	defer conn.Close()
	if _, err := conn.Write(append(askFrame, '\n')); err != nil {
		return decision, fmt.Errorf("发送授权请求: %w", err)
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&decision); err != nil {
		return decision, fmt.Errorf("读取裁决: %w", err)
	}
	if decision.Behavior != "allow" && decision.Behavior != "deny" {
		return decision, fmt.Errorf("裁决 behavior 非法: %q", decision.Behavior)
	}
	return decision, nil
}

func init() {
	permissionMCPCmd.Flags().StringVar(&permSockPath, "sock", "", "裁决 socket 路径")
	rootCmd.AddCommand(permissionMCPCmd)
}
