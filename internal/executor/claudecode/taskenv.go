// taskenv.go —— Claude Code 任务环境物料生成。
//
// 职责：
//   - 生成任务级 settings.json（权限静态分级：ask 收窄危险面、allow 兜底放行）
//   - 生成 mcp.json（把 handoff 内置裁决工具挂到本任务的 perm.sock 上）
//   - 渲染首回合 prompt（回合纪律模板复用 turn 共享包，两个 executor 同源）
//
// 边界：
//   - 不启动进程、不建 socket：进程在 proc.go，socket 服务端在 perm.go
//   - 不做权限判断：本文件只生成静态策略，运行期裁决全部经 perm.sock 交 manager
//   - settings.json 只放 permissions、**不含任何凭证**（鉴权走 B19 的 env 注入，
//     2026-08-09 探针实测，见 spec §5.4）——因此它可以放心进日志/工单/diff
package claudecode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/xushixin/handoff/internal/executor/turn"
)

// 任务目录内的物料文件名（目录本身 0700，由 manager 创建）。
const (
	settingsFileName = "settings.json"
	mcpFileName      = "mcp.json"
)

// askRules 是权限静态分级的「危险模式」表（第 0 层审批链），与 opencode 的
// bashPermissionRules ask 项同源。
//
// 匹配语义：claude 的 Bash 规则按命令前缀匹配（"Bash(rm:*)" 匹配以 rm 开头的命令）。
// 命中 ask 的请求会经裁决工具进 handoff 审批链；未命中的落到 allowRules 兜底放行。
//
// 两条实测依据（2026-08-09 探针，spec §5.4）：
//   - 同文件内 ask 压过 allow——「allow 兜底 + ask 收窄」的形状成立
//   - 任务级 ask 压过用户级 allow——个人 allowlist 无法绕过任务级收窄
//
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var askRules = []string{
	"Bash(rm:*)",        // 删除（含 rm -rf）
	"Bash(sudo:*)",      // 提权
	"Bash(git push:*)",  // 外推：收尾纪律要求不 push，出现即异常
	"Bash(git reset:*)", // 丢弃提交
	"Bash(curl:*)",      // 外访直调
	"Bash(wget:*)",      // 外访直调
	"WebFetch",          // 外访
	"WebSearch",         // 外访
}

// allowRules 是兜底放行表：在任务分支上改代码、读文件、跑测试是派发的目的本身，
// diff 审核兜底。不写它会退回「默认全 ask」，造成一期那种连环唤醒审核者的噪音
// （见 opencode/taskenv.go 文件头的 dogfooding 修正记录；ask 压过 allow 的形态
// 已由 2026-08-09 探针证实，spec §5.4）。
var allowRules = []string{"Bash", "Edit", "Write", "Read", "Glob", "Grep"}

// settingsFile 是任务级 settings.json 的结构（只写 permissions 段）。
//
// 用结构体而非裸 map：字段顺序稳定、输出确定，便于测试与人工核对。
type settingsFile struct {
	Permissions permissionsSection `json:"permissions"`
}

// permissionsSection 是 permissions 段。
//
// Deny 恒为空并显式序列化（json 标签不带 omitempty）：留一个可见的空数组，
// 提醒读配置的人「这里是故意不写的」——黑名单命中的语义是升级审核者而非硬拒，
// 写进 deny 会让审核者连看都看不到（spec §5.4）。
type permissionsSection struct {
	Allow []string `json:"allow"`
	Ask   []string `json:"ask"`
	Deny  []string `json:"deny"`
}

// mcpConfig 是 mcp.json 的结构。
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

// mcpServer 是单个 stdio MCP server 的启动定义。
type mcpServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// WriteTaskEnv 在 taskDir 生成 Claude Code 的任务级物料并渲染首回合 prompt。
//
// 参数：
//   - taskDir: 任务工作目录（须已存在，由 manager 创建，0700）
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文，原样嵌入 prompt
//   - sockPath: 本任务的权限裁决 socket 路径（perm.go 监听它）
//   - handoffBin: handoff 二进制绝对路径，作为裁决 MCP server 的启动命令
//
// 返回：
//   - settingsPath / mcpPath: 生成的两个配置文件路径
//   - promptText: 渲染后的首回合 prompt 原文（由 adapter 投递进 fifo）
//   - err: 渲染或写文件失败
//
// 注意：
//   - 重复调用幂等覆盖，Start 失败重试可安全重来
//   - 两个配置文件都是 0600：mcp.json 泄露 socket 路径即泄露裁决入口
func WriteTaskEnv(taskDir, taskID, planContent, sockPath, handoffBin string) (settingsPath, mcpPath, promptText string, err error) {
	log := slog.Default()
	settingsPath = filepath.Join(taskDir, settingsFileName)
	mcpPath = filepath.Join(taskDir, mcpFileName)
	log.Info("claude 生成任务环境", "task", taskID, "task_dir", taskDir,
		"settings", settingsPath, "mcp", mcpPath, "sock", sockPath)

	settings := settingsFile{Permissions: permissionsSection{
		Allow: allowRules,
		Ask:   askRules,
		Deny:  []string{},
	}}
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Error("claude 序列化 settings 失败", "task", taskID, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("序列化 settings: %w", err)
	}
	if err := os.WriteFile(settingsPath, settingsJSON, 0o600); err != nil {
		log.Error("claude 写 settings 失败", "task", taskID, "path", settingsPath, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("写 %s: %w", settingsPath, err)
	}

	mcp := mcpConfig{MCPServers: map[string]mcpServer{
		"handoff": {Command: handoffBin, Args: []string{"permission-mcp", "--sock", sockPath}},
	}}
	mcpJSON, err := json.MarshalIndent(mcp, "", "  ")
	if err != nil {
		log.Error("claude 序列化 mcp 配置失败", "task", taskID, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("序列化 mcp 配置: %w", err)
	}
	if err := os.WriteFile(mcpPath, mcpJSON, 0o600); err != nil {
		log.Error("claude 写 mcp 配置失败", "task", taskID, "path", mcpPath, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("写 %s: %w", mcpPath, err)
	}

	promptText, err = turn.RenderPrompt(taskID, planContent)
	if err != nil {
		log.Error("claude 渲染 prompt 失败", "task", taskID, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("渲染 prompt: %w", err)
	}
	log.Info("claude 任务环境已生成", "task", taskID,
		"ask_rules", len(askRules), "allow_rules", len(allowRules), "prompt_bytes", len(promptText))
	return settingsPath, mcpPath, promptText, nil
}
