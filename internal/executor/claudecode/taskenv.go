// taskenv.go —— Claude Code 任务环境物料生成。
//
// 职责：
//   - 生成任务级 settings.json（权限静态分级：ask 收窄危险面、allow 逐条白名单）
//   - 生成 mcp.json（把 handoff 内置裁决工具挂到本任务的 perm.sock 上）
//   - 渲染首回合 prompt（回合纪律模板复用 turn 共享包，两个 executor 同源）
//
// 边界：
//   - 不启动进程、不建 socket：进程在 proc.go，socket 服务端在 perm.go
//   - 不做权限判断：本文件只生成静态策略，运行期裁决全部经 perm.sock 交 manager
//   - settings.json 只放 permissions、**不含任何凭证**——凭证由 claude 自己经
//     `--setting-sources user` 从真实 `~/.claude/settings.json` 读取（2026-08-09
//     真机 e2e 实测：不带凭证 env 即跑通，见 spec §5.4）；env 注入（B19）是给
//     代理/自定义 base_url 这类额外环境用的，不是鉴权必要条件。因此 settings.json
//     可以放心进日志/工单/diff
package claudecode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor/turn"
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
// 命中 ask 的请求会经裁决工具进 handoff 审批链；命中 allowRules 的直接执行；
// 两表都不命中的，claude 会照样来问 —— 于是也进审批链（B149 起 allow 不再兜底）。
//
// 两条实测依据（2026-08-09 探针，spec §5.4）：
//   - 同文件内 ask 压过 allow——「allow 放行 + ask 收窄」的形状成立
//   - 任务级 ask 压过用户级 allow——个人 allowlist 无法绕过任务级收窄
//
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var askRules = []string{
	"Write",             // 写文件：路径是否越出任务范围由 handoff 的 permgate 判（B27）
	"Edit",              // 同上
	"Bash(rm:*)",        // 删除（含 rm -rf）
	"Bash(sudo:*)",      // 提权
	"Bash(git push:*)",  // 外推：收尾纪律要求不 push，出现即异常
	"Bash(git reset:*)", // 丢弃提交
	"Bash(curl:*)",      // 外访直调
	"Bash(wget:*)",      // 外访直调
	"WebFetch",          // 外访
	"WebSearch",         // 外访
}

// allowRules 是自动放行表。2026-08-18（B149）起**不再有裸 `"Bash"`**，改为逐条
// 前缀白名单。
//
// 为什么必须换掉裸 `"Bash"`：它没有前缀参数，匹配一切 bash 命令，于是
// `echo x > ~/.ssh/authorized_keys` 由 claude 自己放行、handoff 的 permgate
// 连看都看不到（B128 真机验收第 4 条的原始现场：零权限请求、文件直接写成）。
// 连带后果是 permgate 的黑名单、执行包装器识别、B115 的自指令收口在 claude 上
// 全部空转——`handoff dispatch` 不匹配 askRules 那六个前缀，压根到不了 permgate。
//
// 为什么换成前缀白名单就能收住：2026-08-18 真机探针（mac-02 上真跑 claude）实测
// 出一条关键性质——**追加文件重定向会破坏前缀匹配**：
//
//	echo hi-control     → 命中 Bash(echo:*)，直接执行
//	echo hi > out.txt   → **不命中**，落 ask（文件未被创建）
//	echo hi 2>&1        → 命中（fd 复制不算文件写入）
//	echo hi | cat       → 命中（管道不算文件写入）
//
// 也就是说白名单里的命令一旦缀上写文件的重定向，就自动掉出白名单、进 ask →
// permgate → 落点范围判定（B134 的 judgeBash）。白名单在「越界写」这件事上不漏。
//
// 选条目的原则（与 claude 自身内置指导的「绝不要放进 allowlist」清单一致）：
//   - 放：只读检视 + 本项目工具链里边界清楚的子命令
//   - **不放**：shell 与解释器（sh/bash/python/node/ruby）、包运行器（npx/bunx）、
//     网络工具（curl/wget，且它们已在 askRules）、`find`（-exec/-delete 是任意执行）、
//     `sed`（-i 可就地改仓库外文件，不经重定向）、`env`（`env X=1 <任意命令>`）
//   - 用 `go build`/`go test` 这类**二级前缀**而不是 `Bash(go:*)`：后者会把
//     `go run ./x` 与 `go generate` 一并放行，那是任意代码执行
//
// 不在本表的命令不是被拒，是落 ask 交 permgate 判——未知命令天然走判据，
// 这是「安全默认」的形状，与 B115 自指令收口同源。
//
// 每次修改本表必须同步 taskenv_test 的逐条断言——多一条就是一条静默放行的通道。
var allowRules = []string{
	// 非 bash 工具：读文件与检索，本身不写任何东西
	"Read", "Glob", "Grep",

	// 只读检视
	"Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)", "Bash(tail:*)",
	"Bash(wc:*)", "Bash(stat:*)", "Bash(file:*)", "Bash(pwd:*)",
	"Bash(echo:*)", "Bash(which:*)", "Bash(diff:*)",
	"Bash(grep:*)", "Bash(rg:*)", "Bash(sort:*)", "Bash(uniq:*)",

	// git：逐条列出而不是 Bash(git:*)——后者会放行 git config --global
	// （写 ~/.gitconfig，不经重定向即越界）与 git fetch --upload-pack=<cmd>
	// （任意命令执行）。push/reset 另在 askRules 里收窄
	"Bash(git status:*)", "Bash(git log:*)", "Bash(git diff:*)",
	"Bash(git show:*)", "Bash(git branch:*)", "Bash(git add:*)",
	"Bash(git commit:*)", "Bash(git checkout:*)", "Bash(git restore:*)",
	"Bash(git stash:*)", "Bash(git rev-parse:*)", "Bash(git ls-files:*)",

	// Go 工具链：二级前缀，刻意不含 go run / go generate
	"Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)",
	"Bash(go mod:*)", "Bash(go doc:*)", "Bash(go env:*)", "Bash(gofmt:*)",

	// 前端：只放 run/test 两个子命令，不放 npm install/npx（会跑安装脚本，
	// 等于任意代码执行）
	"Bash(npm run:*)", "Bash(npm test:*)",
}

// settingsFile 是任务级 settings.json 的结构（只写 permissions 段）。
//
// 用结构体而非裸 map：字段顺序稳定、输出确定，便于测试与人工核对。
type settingsFile struct {
	Permissions permissionsSection `json:"permissions"`
}

// permissionsSection 是 permissions 段。
//
// Deny 恒为空并显式序列化（json 标签不带 omitempty）：留一个可见的空数组，
// 提醒读配置的人「这里是故意不写的」——黑名单命中的语义是升级协调者而非硬拒，
// 写进 deny 会让协调者连看都看不到（spec §5.4）。
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
//   - disciplineBlock: 唯一来自 StartReq 的纪律块正文；taskenv 不自行解析
//
// 返回：
//   - settingsPath / mcpPath: 生成的两个配置文件路径
//   - promptText: 渲染后的首回合 prompt 原文（由 adapter 投递进 fifo）
//   - err: 渲染或写文件失败
//
// 注意：
//   - 重复调用幂等覆盖，Start 失败重试可安全重来
//   - 两个配置文件都是 0600：mcp.json 泄露 socket 路径即泄露裁决入口
func WriteTaskEnv(taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (settingsPath, mcpPath, promptText string, err error) {
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

	promptText, err = turn.RenderPrompt(taskID, planContent, disciplineBlock)
	if err != nil {
		log.Error("claude 渲染 prompt 失败", "task", taskID, "cause", err)
		return settingsPath, mcpPath, "", fmt.Errorf("渲染 prompt: %w", err)
	}
	log.Info("claude 任务环境已生成", "task", taskID,
		"ask_rules", len(askRules), "allow_rules", len(allowRules), "prompt_bytes", len(promptText))
	return settingsPath, mcpPath, promptText, nil
}
