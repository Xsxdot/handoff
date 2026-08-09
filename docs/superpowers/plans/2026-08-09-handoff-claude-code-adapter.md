# Claude Code adapter 实现计划（B2）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff dispatch --executor claude` 与 opencode 完全对等——五动作全链路可用、分级审批链原样生效、`handoff attach` 看到同形态的终端实况。

**Architecture:** `claude -p --input-format stream-json --output-format stream-json` 长驻在 tmux 会话 `handoff-<id8>` 内，指令经命名管道 `in.fifo` 投递、事件从 `out.jsonl` 按 offset 续读；权限门经 `--permission-prompt-tool` 挂到 handoff 二进制内置的 stdio MCP server，由它经 unix socket 桥到 adapter，再进 manager 现有的分级审批链。

**Tech Stack:** Go 1.26、标准库（`net`（unix socket）/ `encoding/json` / `os/exec`）、tmux、claude CLI ≥ 2.1.220。无新增第三方依赖。

**Spec:** [2026-08-08-handoff-claude-code-adapter-design.md](../specs/2026-08-08-handoff-claude-code-adapter-design.md)

## Global Constraints

- **前置依赖**：`internal/executor/turn` 共享包由 B3 会话前置抽取并已合入 main。本计划**不包含**抽取任务，从该 commit 起步直接 import。若开工时该包尚未落 main，**停下来等**，不要自己抽（会与 B3 会话产生大面积冲突）。
- **依赖 B19 的 `StartReq.Env`**（[env 注入计划](2026-08-09-handoff-agent-env-injection.md) Task 4）：`Start` 必须把 `req.Env` 透传进 `StartProcReq.Env`，由 `writeRunScript` 生成 `export K='V'` 行，**排在脚本其余内容之前**，值必须单引号包裹（Go 侧已展开过一次，不加引号会被 shell 二次展开）。与 opencode 的 `writeServeScript`、grok 的 `WriteServeScript` 三处同构。
  - **为什么这条必须写进铁律**：`Env` 是加在 `StartReq` 上的契约字段，不读它照样编译通过——用户在 `~/.handoff/env/claude.env` 里写的 `HTTPS_PROXY` 会**静默不生效、无任何报错**。这类缺口最难发现。
  - **若开工时 B19 Task 4 尚未落 main**：`StartProcReq.Env` 字段照加，`Start` 里先传 `nil` 并留 `// TODO(B19): 改为 req.Env` 单行标记，等 B19 合入后替换。**不要把字段删掉**——删了就会忘。
  - claude 侧**没有** `protectedEnvKeys`（不像 grok 的 `GROK_HOME`/`GROK_AGENT_SECRET`、opencode 的 `OPENCODE_*`）：本 adapter 的策略与凭据全部走 `--settings` / `--mcp-config` **命令行参数**，不经环境变量，因此 env 文件覆盖不到。若将来改用环境变量传任何策略，必须同步补一张保留表。
- **日志**：一律 `log/slog`（`*slog.Logger` 注入或 `slog.Default()`，与 opencode adapter 同款）。**禁止 `fmt.Printf` / `println` 作为日志机制**。
- **注释**：每个新文件顶部写「职责 + 边界」；每个导出函数写参数/返回/注意；非显然分支写「为什么」。
- **adapter 不得写 store、不得做审批判断**（`internal/executor/executor.go` 包级边界）。
- **`PermissionID` 必须是 claude 的 `tool_use_id` 原值**，不得自造、不得加前缀（manager 侧会自行命名空间化为 `taskID:permID`）。
- **tmux 会话名固定 `handoff-<id8>`**（`id8` = 任务 uuid 前 8 字符），与 `cmd/attach.go` 及 opencode `proc.go` 三处耦合，不得改。
- **任务目录权限**：含密钥/策略的文件 0600，日志类 0644（与 opencode 一致）。
- **每个任务结束必须 `gofmt`、`go vet ./...`、`go test ./...` 全绿再 commit。**
- 提交信息用中文，与仓库现有风格一致。

---

## 文件结构

| 文件 | 职责 |
|------|------|
| `internal/executor/claudecode/taskenv.go` | 任务级物料生成：`settings.json`（权限策略）、`mcp.json`（裁决工具挂载）、首回合 prompt |
| `internal/executor/claudecode/perm.go` | `perm.sock` 服务端：受理裁决登记、回发裁决、重连幂等 |
| `internal/executor/claudecode/proc.go` | 进程生命周期：`run_claude.sh` 生成、`in.fifo`、tmux 启停、`claude.json` 凭据、存活判据 |
| `internal/executor/claudecode/stream.go` | `out.jsonl` 增量解析：offset 续读、行级宽容解码、`handoff_exit` 哨兵识别 |
| `internal/executor/claudecode/adapter.go` | 五动作编排 + stream 事件 → `executor.AdapterEvent` 映射 + 看门狗 |
| `cmd/permission_mcp.go` | 隐藏子命令 `handoff permission-mcp`：stdio JSON-RPC MCP server |
| `cmd/agentd.go:79-82` | adapter 注册表加 `"claude"` |
| `README.md` | 执行者一节补 claude；已知限制补 Task 1 的探针结论 |

**为什么这样切**：`proc`（进程）/ `stream`（解析）/ `perm`（权限）/ `taskenv`（物料）四件事各自可独立测试，`adapter` 只做编排。opencode 侧同样的四件事散在 `adapter.go`（1432 行）里，是它最难改的地方——新包不重复这个错误。

---

## Task 1: 权限策略优先级探针

**Files:**
- Create: `internal/executor/claudecode/probe_live_test.go`
- Modify: `docs/superpowers/specs/2026-08-08-handoff-claude-code-adapter-design.md`（§5.4 记录结论）

**Interfaces:**
- Consumes: 无
- Produces: 结论一条——`settings.json` 的形态是「`allow` 兜底 + `ask` 收窄」还是「不写 allow，全 ask」；以及 `--setting-sources` 取 `user,project` 还是 `project`。后续 Task 2 按此结论生成 `settings.json`。

**为什么这是第一个任务**：spec §5.4 的两条优先级决定 `settings.json` 长什么样。猜错的代价是「安全门看着在、实际被个人 allowlist 静默绕过」——这类缺陷不会有任何报错，只会在某天真的删了东西时才发现。

- [ ] **Step 1: 写 live 探针测试（默认 skip）**

创建 `internal/executor/claudecode/probe_live_test.go`：

```go
// probe_live_test.go —— 权限策略优先级 live 探针（默认跳过，需真实 claude + 网络）。
//
// 职责：
//   - 验证任务级 settings 的 ask 是否压过同文件内的 allow
//   - 验证任务级 settings 的 ask 是否压过「用户级」settings 的 allow
//
// 边界：
//   - 只做探针不做断言性能/成本；结论人工抄进 spec §5.4 与 README
//   - 默认 t.Skip：CI 与常规 go test ./... 不得因为缺 claude/网络而红
package claudecode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProbePermissionPrecedence 探针：ask 能否压过 allow（同文件内 / 跨来源）。
//
// 运行方式：HANDOFF_LIVE_CLAUDE=1 go test ./internal/executor/claudecode/ -run Probe -v
//
// 注意：
//   - 会真实调用 claude（haiku）产生费用，故默认跳过
//   - 用临时 HOME 构造可控的「用户级 settings」，不依赖执行者本机的个人配置
func TestProbePermissionPrecedence(t *testing.T) {
	if os.Getenv("HANDOFF_LIVE_CLAUDE") != "1" {
		t.Skip("live 探针：设 HANDOFF_LIVE_CLAUDE=1 手动运行")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude 未安装")
	}

	cases := []struct {
		name         string
		userAllow    []string // 写进临时 HOME 的用户级 settings.allow
		taskAllow    []string // 写进任务级 settings.allow
		taskAsk      []string // 写进任务级 settings.ask
		settingSrc   string
		wantAskFired bool // 期望：裁决工具被调用（= ask 生效）
	}{
		{
			name:         "同文件内 ask 压过 allow",
			taskAllow:    []string{"Bash"},
			taskAsk:      []string{"Bash(rm:*)"},
			settingSrc:   "",
			wantAskFired: true,
		},
		{
			name:         "任务级 ask 压过用户级 allow",
			userAllow:    []string{"Bash"},
			taskAsk:      []string{"Bash(rm:*)"},
			settingSrc:   "user",
			wantAskFired: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeSettings(t, filepath.Join(home, ".claude", "settings.json"), c.userAllow, nil)
			taskSettings := filepath.Join(dir, "settings.json")
			writeSettings(t, taskSettings, c.taskAllow, c.taskAsk)

			markerLog := filepath.Join(dir, "asked.log")
			mcpPath := writeProbeMCP(t, dir, markerLog)

			args := []string{
				"-p", "--input-format", "stream-json", "--output-format", "stream-json",
				"--verbose", "--model", "haiku",
				"--setting-sources", c.settingSrc,
				"--settings", taskSettings,
				"--mcp-config", mcpPath, "--strict-mcp-config",
				"--permission-prompt-tool", "mcp__probe__ask",
			}
			cmd := exec.Command("claude", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = strings.NewReader(
				`{"type":"user","message":{"role":"user","content":` +
					`"Use the Bash tool to run exactly: rm -rf /tmp/handoff-probe-victim"}}` + "\n")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("claude 运行失败: %v", err)
			}

			_, statErr := os.Stat(markerLog)
			askFired := statErr == nil
			t.Logf("askFired=%v（期望 %v）；claude 输出 %d 字节", askFired, c.wantAskFired, len(out))
			if askFired != c.wantAskFired {
				t.Errorf("优先级与预期不符：askFired=%v want=%v —— 按 spec §5.4 的处置分支改写 settings 形态",
					askFired, c.wantAskFired)
			}
		})
	}
}

// writeSettings 写一个只含 permissions 段的 settings.json（allow/ask 可空）。
func writeSettings(t *testing.T, path string, allow, ask []string) {
	t.Helper()
	type perms struct {
		Allow []string `json:"allow,omitempty"`
		Ask   []string `json:"ask,omitempty"`
	}
	b, err := json.Marshal(struct {
		Permissions perms `json:"permissions"`
	}{perms{Allow: allow, Ask: ask}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeProbeMCP 生成探针用的极简 stdio MCP server（命中即 touch markerLog 并放行）。
func writeProbeMCP(t *testing.T, dir, markerLog string) string {
	t.Helper()
	script := `#!/usr/bin/env python3
import sys, json
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
TOOL = {"name":"ask","description":"probe","inputSchema":{"type":"object","properties":{},"required":[]}}
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    req = json.loads(line)
    m = req.get("method")
    if m == "initialize":
        send({"jsonrpc":"2.0","id":req["id"],"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"probe","version":"0"}}})
    elif m == "tools/list":
        send({"jsonrpc":"2.0","id":req["id"],"result":{"tools":[TOOL]}})
    elif m == "tools/call":
        open(` + "`" + `MARKER` + "`" + `, "a").write("asked\n")
        send({"jsonrpc":"2.0","id":req["id"],"result":{"content":[{"type":"text","text":json.dumps({"behavior":"allow"})}]}})
    elif "id" in req:
        send({"jsonrpc":"2.0","id":req["id"],"result":{}})
`
	script = strings.Replace(script, "`MARKER`", `"`+markerLog+`"`, 1)
	scriptPath := filepath.Join(dir, "probe_mcp.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"probe": map[string]any{"command": "python3", "args": []string{scriptPath}},
	}}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}
```

- [ ] **Step 2: 确认默认跳过**

Run: `go test ./internal/executor/claudecode/ -run Probe -v`
Expected: `--- SKIP: TestProbePermissionPrecedence`（未设环境变量）

- [ ] **Step 3: 真机跑探针**

Run: `HANDOFF_LIVE_CLAUDE=1 go test ./internal/executor/claudecode/ -run Probe -v -timeout 10m`
Expected: 两个子用例都 PASS。任一 FAIL 时**不要改测试让它变绿**——按 spec §5.4 的处置分支决定 `settings.json` 形态，把实际结论记下来。

若临时 HOME 导致鉴权失败（`claude` 报未登录），改用真实 HOME 手动跑第 2 个子用例：把 `Bash` 加进真实 `~/.claude/settings.json` 的 allow 再试，测完删掉。这种情况在测试里 `t.Skip` 并注明原因。

- [ ] **Step 4: 把结论写进 spec §5.4 与 README**

在 spec §5.4 处置分支下追加一行实测结论（形如「2026-08-08 实测：两条均成立，按本节走」）。若结论是降级分支，同步在 `README.md` 的已知限制补一条。

- [ ] **Step 5: Commit**

```bash
git add internal/executor/claudecode/probe_live_test.go docs/superpowers/specs/2026-08-08-handoff-claude-code-adapter-design.md README.md
git commit -m "test: Claude Code 权限策略优先级 live 探针（B2 Task 1）"
```

---

## Task 2: taskenv.go —— 任务级物料生成

**Files:**
- Create: `internal/executor/claudecode/taskenv.go`
- Test: `internal/executor/claudecode/taskenv_test.go`

**Interfaces:**
- Consumes: `turn` 包的 prompt 渲染（B3 前置产出）
- Produces:
  - `func WriteTaskEnv(taskDir, taskID, planContent, sockPath, handoffBin string) (settingsPath, mcpPath, promptText string, err error)`
  - `var askRules []string`（危险模式表，供测试逐条断言）

- [ ] **Step 1: 对齐共享包实际 API**

读 `internal/executor/turn/` 的导出符号，把本任务及后续任务里用到的调用名对齐实际签名：

Run: `go doc ./internal/executor/turn`

本计划按以下预期名书写，**若实际不同以实际为准，语义保持一致**：
- `turn.RenderPrompt(taskID, planContent string) (string, error)` —— 回合纪律 prompt 渲染
- `turn.ParseTrailer(text string) (kind string, t turn.Trailer)`，`turn.Trailer{Question, Branch, Commit, Summary}`
- `turn.AppendRender(renderLogPath, delta string) error`
- `turn.GitStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error)`
- `turn.Truncate(s string, limit int) string` —— 超限追加 `executor.TruncationMarker`

- [ ] **Step 2: 写失败测试**

创建 `internal/executor/claudecode/taskenv_test.go`：

```go
package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTaskEnvGeneratesSettingsAndMCP(t *testing.T) {
	dir := t.TempDir()
	settingsPath, mcpPath, prompt, err := WriteTaskEnv(dir, "T-1", "计划正文", "/tmp/x/perm.sock", "/usr/local/bin/handoff")
	if err != nil {
		t.Fatalf("WriteTaskEnv: %v", err)
	}

	// prompt 必须带上计划正文（回合纪律部分由 turn 包保证，此处只校验透传）
	if !strings.Contains(prompt, "计划正文") {
		t.Errorf("prompt 未包含计划正文: %q", prompt)
	}

	// settings.json：ask 覆盖危险模式，deny 必须为空（why 见 spec §5.4）
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	readJSON(t, settingsPath, &settings)
	if len(settings.Permissions.Deny) != 0 {
		t.Errorf("deny 必须留空（黑名单归 manager 升级审核者），实际 %v", settings.Permissions.Deny)
	}
	for _, want := range []string{"Bash(rm:*)", "Bash(sudo:*)", "Bash(git push:*)", "Bash(curl:*)", "Bash(wget:*)"} {
		if !contains(settings.Permissions.Ask, want) {
			t.Errorf("ask 缺少危险模式 %q（少一条就是静默放行）", want)
		}
	}
	if !contains(settings.Permissions.Allow, "Bash") {
		t.Errorf("allow 应兜底放行 Bash，实际 %v", settings.Permissions.Allow)
	}

	// mcp.json：裁决工具指向 handoff 二进制 + 本任务 socket
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readJSON(t, mcpPath, &mcp)
	srv, ok := mcp.MCPServers["handoff"]
	if !ok {
		t.Fatalf("mcp.json 缺少 handoff server: %+v", mcp)
	}
	if srv.Command != "/usr/local/bin/handoff" {
		t.Errorf("command 应为 handoff 二进制绝对路径，实际 %q", srv.Command)
	}
	if !contains(srv.Args, "/tmp/x/perm.sock") {
		t.Errorf("args 应携带任务 socket 路径，实际 %v", srv.Args)
	}

	// 含策略的文件必须 0600
	for _, p := range []string{settingsPath, mcpPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s 权限应为 0600，实际 %v", filepath.Base(p), fi.Mode().Perm())
		}
	}
}

func TestWriteTaskEnvIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "a", "/s", "/bin/handoff"); err != nil {
		t.Fatal(err)
	}
	// 重复调用覆盖而非报错：Start 失败重试时必须能安全重来
	if _, _, _, err := WriteTaskEnv(dir, "T-1", "b", "/s", "/bin/handoff"); err != nil {
		t.Fatalf("重复调用应幂等: %v", err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run TestWriteTaskEnv -v`
Expected: 编译失败 `undefined: WriteTaskEnv`

- [ ] **Step 4: 实现 taskenv.go**

```go
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
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var askRules = []string{
	"Bash(rm:*)",             // 删除（含 rm -rf）
	"Bash(sudo:*)",           // 提权
	"Bash(git push:*)",       // 外推：收尾纪律要求不 push，出现即异常
	"Bash(git reset:*)",      // 丢弃提交
	"Bash(curl:*)",           // 外访直调
	"Bash(wget:*)",           // 外访直调
	"WebFetch",               // 外访
	"WebSearch",              // 外访
}

// allowRules 是兜底放行表：在任务分支上改代码、读文件、跑测试是派发的目的本身，
// diff 审核兜底。不写它会退回「默认全 ask」，造成一期那种连环唤醒审核者的噪音
// （见 opencode/taskenv.go 文件头的 dogfooding 修正记录）。
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
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run TestWriteTaskEnv -v`
Expected: 两个用例 PASS

- [ ] **Step 6: 自检日志与注释覆盖**

对照 instrumenting-code 清单逐条确认：进入 `WriteTaskEnv` 有 Info（带 task/路径）；三个错误分支各有 Error 带 cause；成功路径有 Info 带规则条数（不是静默成功）；文件头有职责+边界；导出函数有参数/返回/注意；`askRules`/`allowRules`/`Deny` 三处有「为什么」注释。缺项补齐。

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code 任务级物料生成（settings/mcp/prompt，B2 Task 2）"
```

---

## Task 3: perm.go —— 权限裁决 socket 服务端

**Files:**
- Create: `internal/executor/claudecode/perm.go`
- Test: `internal/executor/claudecode/perm_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type permAsk struct { ToolUseID string; ToolName string; Input json.RawMessage }`
  - `type permServer struct{ ... }`
  - `func newPermServer(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error)`
  - `func (s *permServer) Respond(toolUseID, behavior, message string) error`
  - `func (s *permServer) Close() error`
  - 线协议（换行分隔 JSON）：MCP→server `{"type":"ask","tool_use_id":..,"tool_name":..,"input":{..}}`；server→MCP `{"type":"decision","behavior":"allow"|"deny","message":".."}`

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/claudecode/perm_test.go`：

```go
package claudecode

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// dialAsk 模拟 MCP 进程：连 socket、发一条 ask、返回连接供读裁决。
func dialAsk(t *testing.T, sock, toolUseID, toolName, inputJSON string) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("连 socket: %v", err)
	}
	line, _ := json.Marshal(map[string]any{
		"type": "ask", "tool_use_id": toolUseID, "tool_name": toolName,
		"input": json.RawMessage(inputJSON),
	})
	if _, err := c.Write(append(line, '\n')); err != nil {
		t.Fatalf("写 ask: %v", err)
	}
	return c
}

func TestPermServerAskThenRespond(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	asks := make(chan permAsk, 4)
	srv, err := newPermServer(sock, slog.Default(), func(a permAsk) { asks <- a })
	if err != nil {
		t.Fatalf("newPermServer: %v", err)
	}
	defer srv.Close()

	conn := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"rm -rf x"}`)
	defer conn.Close()

	select {
	case a := <-asks:
		if a.ToolUseID != "toolu_1" || a.ToolName != "Bash" {
			t.Fatalf("登记内容不符: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到 ask 回调")
	}

	if err := srv.Respond("toolu_1", "allow", ""); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	var got struct {
		Type     string `json:"type"`
		Behavior string `json:"behavior"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&got); err != nil {
		t.Fatalf("读裁决: %v", err)
	}
	if got.Behavior != "allow" {
		t.Errorf("behavior=%q want allow", got.Behavior)
	}
}

func TestPermServerRespondUnknownID(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	srv, err := newPermServer(sock, slog.Default(), func(permAsk) {})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	// 挂起请求不存在（进程已死 / 请求被重试替换）必须报错，不能静默成功——
	// 静默成功会让 manager 以为裁决已送达，任务永远等不到下一步
	if err := srv.Respond("toolu_missing", "allow", ""); err == nil {
		t.Fatal("对未知 id 应报错")
	}
}

// 断线重连：同一 tool_use_id 用新连接重新登记，裁决必须回到新连接上。
// 这是 agentd 重启后能继续裁决的关键路径。
func TestPermServerReRegisterSameID(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	asks := make(chan permAsk, 4)
	srv, err := newPermServer(sock, slog.Default(), func(a permAsk) { asks <- a })
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	old := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"ls"}`)
	<-asks
	old.Close() // 模拟 agentd 重启导致的连接断开

	fresh := dialAsk(t, sock, "toolu_1", "Bash", `{"command":"ls"}`)
	defer fresh.Close()
	select {
	case a := <-asks:
		if a.ToolUseID != "toolu_1" {
			t.Fatalf("重登记 id 不符: %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("重登记未触发回调（manager 侧 ticket 幂等，这里必须重发）")
	}
	if err := srv.Respond("toolu_1", "deny", "不批"); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	var got struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(bufio.NewReader(fresh)).Decode(&got); err != nil {
		t.Fatalf("新连接读裁决: %v", err)
	}
	if got.Behavior != "deny" || got.Message != "不批" {
		t.Errorf("裁决未回到新连接: %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run TestPermServer -v`
Expected: 编译失败 `undefined: newPermServer`

- [ ] **Step 3: 实现 perm.go**

```go
// perm.go —— 权限裁决 unix socket 服务端。
//
// 职责：
//   - 在任务目录的 perm.sock 上受理裁决请求（由 claude 拉起的 permission-mcp 进程发来）
//   - 把请求以回调交给 adapter（转成 AdapterEvent 进 manager 审批链）
//   - 收到裁决后回发给对应连接，放行或拒绝该次工具调用
//
// 边界：
//   - 不做任何审批判断：批不批由 manager 依审核者应答决定（executor 包级边界）
//   - 不认识 claude 的消息格式：只处理本文件定义的两条线协议
//
// 为什么用 unix socket 而不是 agentd 的 HTTP 口：被监管的 executor 不该拿到
// agentd token；socket 文件落在 0700 的任务目录内，权限即边界，且无需分配端口。
package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
)

// permAsk 是一次待裁决的权限请求（线协议 ask 帧的解码结果）。
type permAsk struct {
	ToolUseID string          `json:"tool_use_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
}

// permDecision 是回发给 MCP 进程的裁决帧。
type permDecision struct {
	Type     string `json:"type"`
	Behavior string `json:"behavior"`          // allow | deny
	Message  string `json:"message,omitempty"` // deny 时的拒绝理由
}

// permServer 在 unix socket 上受理裁决请求并回发裁决。
type permServer struct {
	sockPath string
	ln       net.Listener
	log      *slog.Logger
	onAsk    func(permAsk)

	mu      sync.Mutex
	pending map[string]net.Conn // tool_use_id → 等待裁决的连接
	closed  bool
}

// newPermServer 建立并开始受理裁决请求。
//
// 参数：
//   - sockPath: socket 文件路径（位于 0700 的任务目录内）
//   - log: 日志入口
//   - onAsk: 收到请求时的回调（adapter 在其中 emit permission 事件）；同一
//     tool_use_id 重连重登记时会**再次**回调——manager 侧 ticket 按 id 幂等，
//     重发是 agentd 重启后能继续裁决的必要条件
//
// 返回：
//   - 已在受理的服务端；监听失败时返回错误
//
// 注意：
//   - 复用已存在的 socket 文件前会先删除（agentd 重启后残留），否则 bind 报
//     address already in use
func newPermServer(sockPath string, log *slog.Logger, onAsk func(permAsk)) (*permServer, error) {
	// 残留 socket 文件会让 bind 直接失败，而它恰恰是 agentd 重启后的常态
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		log.Error("清理残留 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("清理残留 socket %s: %w", sockPath, err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Error("监听裁决 socket 失败", "sock", sockPath, "cause", err)
		return nil, fmt.Errorf("监听 %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		log.Warn("设置 socket 权限失败，继续（任务目录本身 0700）", "sock", sockPath, "cause", err)
	}
	s := &permServer{sockPath: sockPath, ln: ln, log: log, onAsk: onAsk,
		pending: map[string]net.Conn{}}
	log.Info("裁决 socket 已就绪", "sock", sockPath)
	go s.acceptLoop()
	return s, nil
}

// acceptLoop 持续受理连接，每条连接一次请求。
func (s *permServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				s.log.Debug("裁决 socket 已关闭，停止受理", "sock", s.sockPath)
				return
			}
			s.log.Error("受理裁决连接失败", "sock", s.sockPath, "cause", err)
			return
		}
		go s.serveConn(conn)
	}
}

// serveConn 读一条 ask 帧、登记挂起并回调；连接在裁决回发后由 Respond 关闭。
func (s *permServer) serveConn(conn net.Conn) {
	var ask permAsk
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&ask); err != nil {
		s.log.Error("解析裁决请求失败，丢弃该连接", "cause", err)
		conn.Close()
		return
	}
	if ask.ToolUseID == "" {
		s.log.Error("裁决请求缺 tool_use_id，丢弃", "tool", ask.ToolName)
		conn.Close()
		return
	}
	s.mu.Lock()
	// 同 id 重连：旧连接多半已死（agentd 重启），换成新连接后仍要回调，
	// 让 manager 重建/复用同一 ticket（id 幂等），否则这次请求永远等不到人
	if old, ok := s.pending[ask.ToolUseID]; ok && old != conn {
		old.Close()
		s.log.Info("裁决请求重连重登记", "tool_use_id", ask.ToolUseID, "tool", ask.ToolName)
	}
	s.pending[ask.ToolUseID] = conn
	s.mu.Unlock()

	s.log.Info("收到权限裁决请求", "tool_use_id", ask.ToolUseID, "tool", ask.ToolName)
	s.onAsk(ask)
}

// Respond 回发裁决并关闭该连接。
//
// 参数：
//   - toolUseID: 目标请求 id（= claude 的 tool_use_id）
//   - behavior: "allow" 或 "deny"
//   - message: deny 时的拒绝理由（allow 时忽略）
//
// 返回：
//   - 找不到挂起请求或写失败时报错。**不得静默成功**：静默成功会让 manager
//     以为裁决已送达，而 claude 侧其实还在等，任务就此卡死
func (s *permServer) Respond(toolUseID, behavior, message string) error {
	s.mu.Lock()
	conn, ok := s.pending[toolUseID]
	if ok {
		delete(s.pending, toolUseID)
	}
	s.mu.Unlock()
	if !ok {
		s.log.Error("裁决目标不存在（请求已失效或进程已退）", "tool_use_id", toolUseID, "behavior", behavior)
		return fmt.Errorf("裁决请求 %s 不存在", toolUseID)
	}
	defer conn.Close()
	b, err := json.Marshal(permDecision{Type: "decision", Behavior: behavior, Message: message})
	if err != nil {
		s.log.Error("序列化裁决失败", "tool_use_id", toolUseID, "cause", err)
		return fmt.Errorf("序列化裁决: %w", err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		s.log.Error("回发裁决失败", "tool_use_id", toolUseID, "behavior", behavior, "cause", err)
		return fmt.Errorf("回发裁决 %s: %w", toolUseID, err)
	}
	s.log.Info("裁决已回发", "tool_use_id", toolUseID, "behavior", behavior)
	return nil
}

// Close 停止受理并关闭全部挂起连接。
//
// 挂起连接被关闭后，MCP 侧会按退避重连重登记——若 agentd 是重启而非退出，
// 重连会落到新的 permServer 上继续裁决。
func (s *permServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]net.Conn, 0, len(s.pending))
	for _, c := range s.pending {
		conns = append(conns, c)
	}
	s.pending = map[string]net.Conn{}
	s.mu.Unlock()

	for _, c := range conns {
		c.Close()
	}
	err := s.ln.Close()
	s.log.Info("裁决 socket 已关闭", "sock", s.sockPath, "closed_pending", len(conns))
	return err
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run TestPermServer -v -race`
Expected: 三个用例 PASS

- [ ] **Step 5: 自检日志与注释覆盖**

确认：`newPermServer` 成功/失败各有日志；每个错误分支带 cause；`Respond` 成功也打 Info（裁决送达是关键状态变更，不能静默）；重连重登记打 Info（这是排查「审核者被唤醒两次」的唯一线索）；文件头有职责+边界与「为什么用 socket」；导出/非导出关键函数均有注释。

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code 权限裁决 socket 服务端（B2 Task 3）"
```

---

## Task 4: cmd/permission_mcp.go —— stdio MCP server 子命令

**Files:**
- Create: `cmd/permission_mcp.go`
- Test: `cmd/permission_mcp_test.go`

**Interfaces:**
- Consumes: Task 3 的线协议（ask / decision 帧）
- Produces: 子命令 `handoff permission-mcp --sock <path>`；导出给测试的纯函数 `func handleRPC(req rpcRequest, sockPath string) (rpcResponse, bool)`

- [ ] **Step 1: 写失败测试**

创建 `cmd/permission_mcp_test.go`：

```go
package cmd

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleRPCInitializeAndList(t *testing.T) {
	resp, ok := handleRPC(rpcRequest{ID: json.RawMessage(`1`), Method: "initialize"}, "/tmp/x.sock")
	if !ok {
		t.Fatal("initialize 必须有响应")
	}
	b, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(b), "protocolVersion") {
		t.Errorf("initialize 结果缺 protocolVersion: %s", b)
	}

	resp, ok = handleRPC(rpcRequest{ID: json.RawMessage(`2`), Method: "tools/list"}, "/tmp/x.sock")
	if !ok {
		t.Fatal("tools/list 必须有响应")
	}
	b, _ = json.Marshal(resp.Result)
	if !strings.Contains(string(b), `"ask"`) {
		t.Errorf("tools/list 未暴露 ask 工具: %s", b)
	}
}

func TestHandleRPCNotificationNoResponse(t *testing.T) {
	// 通知（无 id）不得产生响应，否则 claude 侧会把它当成孤儿响应
	if _, ok := handleRPC(rpcRequest{Method: "notifications/initialized"}, "/tmp/x.sock"); ok {
		t.Error("通知不应产生响应")
	}
}

// tools/call 全链路：起一个假 server 扮演 adapter，校验 ask 帧内容与裁决回传。
func TestHandleRPCToolsCallRoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "perm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var ask struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			ToolName  string          `json:"tool_name"`
			Input     json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(conn).Decode(&ask); err != nil {
			return
		}
		if ask.ToolUseID != "toolu_9" || ask.ToolName != "Bash" {
			t.Errorf("ask 帧内容不符: %+v", ask)
		}
		conn.Write([]byte(`{"type":"decision","behavior":"deny","message":"不批"}` + "\n"))
	}()

	params, _ := json.Marshal(map[string]any{
		"arguments": map[string]any{
			"tool_name":   "Bash",
			"tool_use_id": "toolu_9",
			"input":       map[string]any{"command": "rm -rf x"},
		},
	})
	done := make(chan rpcResponse, 1)
	go func() {
		resp, _ := handleRPC(rpcRequest{ID: json.RawMessage(`3`), Method: "tools/call", Params: params}, sock)
		done <- resp
	}()

	select {
	case resp := <-done:
		b, _ := json.Marshal(resp.Result)
		if !strings.Contains(string(b), `deny`) || !strings.Contains(string(b), "不批") {
			t.Errorf("裁决未透传回 claude: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内未拿到裁决")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestHandleRPC -v`
Expected: 编译失败 `undefined: handleRPC`

- [ ] **Step 3: 实现 cmd/permission_mcp.go**

```go
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
	"description": "Ask the handoff reviewer for permission to run a tool.",
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
	// 后由 manager 侧 ticket 幂等去重，审核者不会被同一请求唤醒两次
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestHandleRPC -v`
Expected: 三个用例 PASS

- [ ] **Step 5: 自检日志与注释覆盖**

本文件的「日志」必须走 stderr 的 `fmt.Fprintf(os.Stderr, ...)`——**这是本仓库唯一允许不用 slog 的地方**，因为 stdout 是 JSON-RPC 通道、而这是个被 claude 拉起的独立短命进程，不接 agentd 的 logger。在文件头注释里写明这条例外的理由。确认：启动打一条、每个错误分支打一条带 cause、裁决到达打一条（成功路径不静默）、重试打一条带次数。

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add cmd/permission_mcp.go cmd/permission_mcp_test.go
git commit -m "feat: handoff permission-mcp 裁决 MCP server 子命令（B2 Task 4）"
```

---

## Task 5: proc.go —— 进程生命周期

**Files:**
- Create: `internal/executor/claudecode/proc.go`
- Test: `internal/executor/claudecode/proc_test.go`

**Interfaces:**
- Consumes: Task 2 的 `settingsPath` / `mcpPath`
- Produces:
  - `type Proc struct { TmuxSession, TaskDir, SessionID string }`
  - `func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error)`
  - `type StartProcReq struct { RepoPath, TaskID, TaskDir, SessionID, Model, SettingsPath, MCPPath string; Env []string }`（`Env` 见下方 B19 耦合说明）
  - `func (p *Proc) WriteInput(text string) error`
  - `func (p *Proc) Kill() error`
  - `func writeRunScript(taskDir string, req StartProcReq) (string, error)`
  - `func procExited(outJSONLPath string) (exited bool, code int)`
  - `func readProcInfo(taskDir string) (*procInfo, error)` / `writeProcInfo`

- [ ] **Step 1: 写失败测试（脚本内容 + fifo + 哨兵，不依赖真 claude）**

创建 `internal/executor/claudecode/proc_test.go`：

```go
package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRunScriptContent(t *testing.T) {
	dir := t.TempDir()
	req := StartProcReq{
		RepoPath: "/repo", TaskID: "abcdef0123", TaskDir: dir,
		SessionID: "sess-1", Model: "opus",
		SettingsPath: filepath.Join(dir, "settings.json"),
		MCPPath:      filepath.Join(dir, "mcp.json"),
	}
	path, err := writeRunScript(dir, req)
	if err != nil {
		t.Fatalf("writeRunScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)

	for _, want := range []string{
		"exec 3<>",                          // fifo 两端永久持有
		"--input-format stream-json",        // 双向流
		"--output-format stream-json",       // 事件流
		"--include-partial-messages",        // 文本增量（实况流式）
		"--permission-prompt-tool mcp__handoff__ask",
		"--setting-sources user,project",
		"--session-id sess-1",
		"--model opus",
		"handoff_exit",                      // 死亡哨兵
		"tee -a",                            // out.jsonl 落盘
	} {
		if !strings.Contains(script, want) {
			t.Errorf("启动脚本缺少 %q:\n%s", want, script)
		}
	}
	// exec claude 会让 sh 被替换掉，哨兵永远写不出来
	if strings.Contains(script, "exec claude") {
		t.Error("claude 一行不得用 exec（否则死亡哨兵写不出来）")
	}
	// stderr 必须单独落盘，混进 stdout 会污染 jsonl 解析
	if !strings.Contains(script, "exec 2>>") {
		t.Error("缺少 stderr 重定向，out.jsonl 会被污染")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("启动脚本权限应 0600，实际 %v", fi.Mode().Perm())
	}
}

func TestWriteRunScriptOmitsEmptyModel(t *testing.T) {
	dir := t.TempDir()
	path, err := writeRunScript(dir, StartProcReq{TaskDir: dir, SessionID: "s", Model: ""})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	// model 为空 = 用 claude 自身默认模型，不能写出裸 --model
	if strings.Contains(string(b), "--model") {
		t.Errorf("model 为空时不应出现 --model:\n%s", b)
	}
}

func TestProcExitedDetectsSentinel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, outFileName)

	if err := os.WriteFile(out, []byte(`{"type":"system","subtype":"init"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exited, _ := procExited(out); exited {
		t.Error("无哨兵时不应判死")
	}

	f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"handoff_exit","code":3}` + "\n")
	f.Close()

	exited, code := procExited(out)
	if !exited || code != 3 {
		t.Errorf("哨兵应判死且带退出码，得到 exited=%v code=%d", exited, code)
	}
}

func TestWriteInputRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &Proc{TaskDir: dir}
	if err := p.ensureFIFO(); err != nil {
		t.Skipf("mkfifo 不可用（平台限制）: %v", err)
	}
	// 读端常开，模拟启动脚本的 exec 3<>
	rd, err := os.OpenFile(filepath.Join(dir, fifoFileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	if err := p.WriteInput("你好"); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	buf := make([]byte, 256)
	n, err := rd.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	line := string(buf[:n])
	if !strings.Contains(line, `"type":"user"`) || !strings.Contains(line, "你好") {
		t.Errorf("fifo 内容不符: %q", line)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'TestWriteRunScript|TestProcExited|TestWriteInput' -v`
Expected: 编译失败 `undefined: writeRunScript`

- [ ] **Step 3: 实现 proc.go**

要点（完整实现按下列骨架补全，注释与日志按 Global Constraints）：

```go
// proc.go —— Claude Code 进程生命周期管理。
//
// 职责：
//   - 在 tmux 会话 handoff-<id8> 内拉起 claude（headless 双向流式），第二窗口 tail render.log
//   - 经命名管道 in.fifo 投递指令；stdout 经 tee 落 out.jsonl，stderr 落 claude.log
//   - 死亡判定（out.jsonl 末尾的 handoff_exit 哨兵）与凭据持久化（claude.json）
//
// 边界：
//   - 不解析事件：out.jsonl 的解析在 stream.go
//   - 不做权限裁决：socket 服务端在 perm.go
//
// 为什么进程放 tmux：agentd 重启或崩溃时子进程树会被一并回收，正在执行的任务
// 会无辜中断；tmux server 独立守护，session 生命周期与 agentd 解耦——这与
// opencode adapter 的取舍完全一致，也让 handoff attach 一套命令覆盖两个 executor。
package claudecode

const (
	runScriptFileName = "run_claude.sh"
	fifoFileName      = "in.fifo"
	outFileName       = "out.jsonl"
	stderrFileName    = "claude.log"
	renderFileName    = "render.log"
	procInfoFileName  = "claude.json"
	sockFileName      = "perm.sock"

	// startReadyTimeout 是等待 system/init 的上限。取 30s 而非 opencode 的 10s：
	// claude 冷启动要加载 settings/plugins/MCP 子进程，10s 会造成假阴性。
	startReadyTimeout = 30 * time.Second
)

// writeRunScript 生成 0600 启动脚本，返回其路径。
//
// why（脚本化而非 tmux 内联）：tmux 客户端进程的 argv 全局可读，参数里带路径
// 与模型名不算秘密，但保持与 opencode 同一形态便于两边一起演进；更实际的原因
// 是 fifo 的 exec 3<> 与末行哨兵都必须在 shell 里表达，内联不了。
//
// why（claude 一行不用 exec）：exec 会让 sh 被 claude 替换掉，末行的 handoff_exit
// 哨兵永远不会执行——而它是本 adapter 唯一可靠的死亡信号（tmux has-session 不可用，
// 因为窗口 1 的 tail -f 会一直撑着会话）。
// why（env 行排在最前、值单引号包裹）：见下方 B19 耦合说明。
func writeRunScript(taskDir string, req StartProcReq) (string, error) {
	// 组装 argv：model 为空则省略 --model（用 claude 自身默认）
	// 用 shellq.Quote 包裹每个路径与模型名
	// req.Env 的每一项（形如 KEY=VALUE，用 strings.Cut 切分，切不开的跳过）
	//   生成一行 `export K=<shellq.Quote(V)>`，整体排在脚本其余内容之前
	// 脚本内容：
	//   #!/bin/sh
	//   exec 2>> <claude.log>
	//   export HTTPS_PROXY='...'        ← req.Env 注入行，排在最前
	//   exec 3<> <in.fifo>
	//   claude -p --input-format stream-json --output-format stream-json --verbose \
	//     --include-partial-messages [--model M] --session-id S \
	//     --setting-sources user,project --settings <settings.json> \
	//     --mcp-config <mcp.json> --permission-prompt-tool mcp__handoff__ask \
	//     <&3 | tee -a <out.jsonl>
	//   printf '{"type":"handoff_exit","code":%d}\n' "$?" >> <out.jsonl>
}

// ensureFIFO 幂等创建 in.fifo（已存在且是管道则复用）。
func (p *Proc) ensureFIFO() error { /* syscall.Mkfifo(path, 0o600)，EEXIST 时校验类型后复用 */ }

// WriteInput 往 in.fifo 投递一条 stream-json user message。
//
// 参数：
//   - text: 指令原文，原样透传不加工（executor 契约要求）
//
// 注意：
//   - 打开 fifo 写端会阻塞直到有读端；启动脚本的 exec 3<> 已永久持有读端，
//     因此这里不会阻塞。若脚本已死，O_NONBLOCK 打开会立刻失败，正是我们要的
//     「进程不在」信号（调用方包装 executor.ErrTaskNotRunning）
func (p *Proc) WriteInput(text string) error { /* ... */ }

// procExited 检查 out.jsonl 是否出现死亡哨兵。
//
// 返回：
//   - exited: 是否已退出；code: 退出码（exited=false 时无意义）
//
// why（从文件读而非记内存）：agentd 重启后内存态全丢，而哨兵落在文件里——
// 重读同样能发现死亡，这是 Resume 判存活的第一条判据。
func procExited(outJSONLPath string) (exited bool, code int) { /* 反向扫末若干行找哨兵 */ }

// StartProc 备物料、起 tmux 会话与渲染窗口，返回进程句柄。
// 就绪判定（等 system/init）由 adapter 在 stream 层完成，本函数只负责把进程拉起来。
func StartProc(ctx context.Context, req StartProcReq, log *slog.Logger) (*Proc, error) { /* ... */ }

// Kill 销毁 tmux 会话（幂等：会话已不存在返回 nil）。
func (p *Proc) Kill() error { /* 与 opencode Proc.Kill 同规则 */ }
```

实现时复用现成件：`internal/shellq.Quote`（引号转义）、opencode `proc.go` 的 `startRenderTailWindow` 手法（先 touch 再开窗口）、`id8` 截断规则。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run 'TestWriteRunScript|TestProcExited|TestWriteInput' -v`
Expected: 四个用例 PASS

- [ ] **Step 5: 加关键节点日志**

- `StartProc` 进入：Info，带 task/session/tmux 会话名/repo
- tmux 启动失败：Error，带 stderr 尾部 + cause
- 渲染窗口启动失败：Warn（不阻断，与 opencode 同）
- `WriteInput`：Debug 带字节数（高频，不用 Info）；失败 Error 带 cause
- `procExited` 发现哨兵：Info，带退出码（这是任务终结的关键状态变更）
- `Kill`：Info 成功 / Error 失败

- [ ] **Step 6: 加意图注释**

文件头职责+边界（含「为什么放 tmux」）；`writeRunScript` 的两条 why（脚本化、不用 exec）；`WriteInput` 的 fifo 阻塞语义 why；`procExited` 的「为什么从文件读」；`startReadyTimeout` 取 30s 的理由。

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code 进程生命周期（tmux/fifo/死亡哨兵，B2 Task 5）"
```

---

## Task 6: stream.go —— out.jsonl 增量解析

**Files:**
- Create: `internal/executor/claudecode/stream.go`
- Test: `internal/executor/claudecode/stream_test.go`
- Test data: `internal/executor/claudecode/testdata/turn_success.jsonl`

**Interfaces:**
- Consumes: Task 5 的 `outFileName`
- Produces:
  - `type streamMsg struct { Type, Subtype, SessionID string; Message json.RawMessage; Event json.RawMessage; Result string; IsError bool; ExitCode int }`
  - `func newTailer(path string, offset int64, log *slog.Logger) *tailer`
  - `func (t *tailer) Run(ctx context.Context, onMsg func(streamMsg)) error`
  - `func (t *tailer) Offset() int64`
  - `func textDelta(ev json.RawMessage) (string, bool)` —— 只认 `text_delta`，`thinking_delta` 返回 false

- [ ] **Step 1: 造 testdata**

创建 `internal/executor/claudecode/testdata/turn_success.jsonl`（形状取自 2026-08-08 真实采样，值已简化）：

```jsonl
{"type":"system","subtype":"init","session_id":"sess-1","tools":["Bash"],"permissionMode":"default"}
{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}
{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"我先看看仓库"}}}
{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"开始执行计划"}}}
{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"text","text":"开始执行计划"}]}}
{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./...","description":"跑测试"}}]}}
{"type":"user","session_id":"sess-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok","is_error":false}]}}
{"type":"rate_limit_event","session_id":"sess-1"}
{"type":"system","subtype":"thinking_tokens","session_id":"sess-1"}
{"type":"stream_event","session_id":"sess-1","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"{\"branch\":\"handoff/ab12\",\"commit\":\"deadbeef\",\"summary\":\"完成\"}"}}}
{"type":"result","subtype":"success","is_error":false,"session_id":"sess-1","result":"{\"branch\":\"handoff/ab12\",\"commit\":\"deadbeef\",\"summary\":\"完成\"}"}
```

- [ ] **Step 2: 写失败测试**

创建 `internal/executor/claudecode/stream_test.go`：

```go
package claudecode

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailerParsesRealSample(t *testing.T) {
	src, err := os.ReadFile("testdata/turn_success.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), outFileName)
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var kinds []string
	var sessionID, resultText string
	tl := newTailer(path, 0, slog.Default())
	go tl.Run(ctx, func(m streamMsg) {
		switch {
		case m.Type == "system" && m.Subtype == "init":
			kinds = append(kinds, "init")
			sessionID = m.SessionID
		case m.Type == "result":
			kinds = append(kinds, "result")
			resultText = m.Result
			cancel()
		}
	})
	<-ctx.Done()

	if sessionID != "sess-1" {
		t.Errorf("session_id=%q want sess-1", sessionID)
	}
	if resultText == "" {
		t.Error("未取到回合文本（trailer 解析的输入）")
	}
	if len(kinds) < 2 {
		t.Errorf("事件序列不完整: %v", kinds)
	}
	if tl.Offset() <= 0 {
		t.Error("offset 应随消费推进")
	}
}

func TestTailerResumeFromOffsetSkipsConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), outFileName)
	first := `{"type":"system","subtype":"init","session_id":"s"}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	// 从已消费的 offset 起读：这一行不得重放，否则 agentd 重启会把旧回合再走一遍
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	seen := 0
	tl := newTailer(path, int64(len(first)), slog.Default())
	go tl.Run(ctx, func(streamMsg) { seen++ })
	<-ctx.Done()
	if seen != 0 {
		t.Errorf("offset 之前的行被重放了 %d 次", seen)
	}
}

func TestTailerToleratesGarbageLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), outFileName)
	content := "这不是 JSON\n" + `{"type":"result","subtype":"success","result":"ok"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := ""
	tl := newTailer(path, 0, slog.Default())
	go tl.Run(ctx, func(m streamMsg) {
		if m.Type == "result" {
			got = m.Result
			cancel()
		}
	})
	<-ctx.Done()
	// 非 JSON 行必须跳过而不是中断解析循环——claude 偶发往 stdout 打非协议内容时
	// 不能让整个任务失联
	if got != "ok" {
		t.Errorf("非 JSON 行后应继续解析，result=%q", got)
	}
}

func TestTextDeltaIgnoresThinking(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"内心戏"}}`)
	if _, ok := textDelta(thinking); ok {
		t.Error("thinking_delta 不得进 render.log 与回合文本（与 opencode 的 reasoning 隔离一致）")
	}
	text := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"正文"}}`)
	got, ok := textDelta(text)
	if !ok || got != "正文" {
		t.Errorf("text_delta 提取失败: %q ok=%v", got, ok)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'TestTailer|TestTextDelta' -v`
Expected: 编译失败 `undefined: newTailer`

- [ ] **Step 4: 实现 stream.go**

骨架（按注释与日志规范补全）：

```go
// stream.go —— out.jsonl 的增量解析（claude stream-json → 内部消息）。
//
// 职责：
//   - 从指定 offset 起持续读 out.jsonl 的新行，宽容解码为 streamMsg 交回调
//   - 维护已消费 offset，供 claude.json 持久化与 agentd 重启后续读
//   - 从 stream_event 中提取模型正文增量（只认 text_delta）
//
// 边界：
//   - 不映射 AdapterEvent、不碰 render.log：那是 adapter.go 的职责
//   - 不管进程死活：哨兵行原样交出，判定在 proc.go / adapter.go
//
// 为什么轮询文件而不是接管管道：进程活在 tmux 里、stdout 经 tee 落盘，
// agentd 重启后没有任何管道可继承，文件 + offset 是唯一能跨重启接续的形态。
package claudecode

// tailPollInterval 是文件无新内容时的轮询间隔。
// 取 200ms：与 opencode 看门狗活跃档同量级，实况延迟人眼不可察，
// 而每任务每天约 43 万次 read 系统调用的成本远低于 fork tmux 进程。
const tailPollInterval = 200 * time.Millisecond

// streamMsg 是一行 stream-json 的宽容解码结果。
//
// 只声明 adapter 用得到的字段：claude 的消息体字段很多且随版本变化，
// 全量建模会让每次 claude 升级都变成一次编译错误。
type streamMsg struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Message   json.RawMessage `json:"message"`
	Event     json.RawMessage `json:"event"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	ExitCode  int             `json:"code"` // handoff_exit 哨兵携带
}

// newTailer / Run / Offset：
//   - Run 循环：读到 EOF 就 sleep tailPollInterval 再试，直到 ctx 取消
//   - 每成功消费一行推进 offset（按行字节数累加，含换行符）
//   - 解码失败：Warn 跳过并累计连续失败数；连续超 garbageLimit(64) 行判流损坏，
//     返回错误交 adapter 转 failed
//
// textDelta 从 stream_event 里提取正文增量：
//   - event.type == "content_block_delta" 且 delta.type == "text_delta" → 返回 text, true
//   - thinking_delta / signature_delta / 其他 → "", false
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run 'TestTailer|TestTextDelta' -v -race`
Expected: 四个用例 PASS

- [ ] **Step 6: 加关键节点日志与意图注释**

日志：tailer 启动 Info（带 path + 起始 offset）；非 JSON 行 Warn（带行号与前 80 字符）；流损坏 Error（带连续失败数）；ctx 取消退出 Info（带最终 offset）。逐行消费**不打日志**（热循环，会刷爆）。
注释：文件头职责+边界+「为什么轮询文件」；`streamMsg` 为什么只建模部分字段；`tailPollInterval` 取值理由；`textDelta` 为什么排除 thinking。

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code out.jsonl 增量解析（offset 续读/宽容解码，B2 Task 6）"
```

---

## Task 7: adapter.go —— 五动作与事件映射

**Files:**
- Create: `internal/executor/claudecode/adapter.go`
- Test: `internal/executor/claudecode/adapter_test.go`

**Interfaces:**
- Consumes: Task 2–6 的全部产出、`turn` 共享包、`internal/executor` 契约
- Produces:
  - `func New(log *slog.Logger) *Adapter`（实现 `executor.Adapter`）
  - 五方法：`Start` / `Events` / `Send` / `RespondPermission` / `Stop`
  - 包内映射入口（测试直接驱动，不起真进程）：
    - `func (a *Adapter) mapMessage(r *runState, m streamMsg)`
    - `func (a *Adapter) onPermissionAsk(r *runState, ask permAsk)`
    - `type runState struct { ... }`（单任务运行态，字段见 Step 3）

- [ ] **Step 1: 写失败测试（用 testdata 驱动映射，不起真进程）**

创建 `internal/executor/claudecode/adapter_test.go`，覆盖四条映射与两条契约：

```go
package claudecode

import (
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// 事件映射：init → progress 带 SessionID
func TestMapInitEmitsSessionID(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "system", Subtype: "init", SessionID: "sess-9"})
	ev := mustRecv(t, r)
	if ev.Type != "progress" || ev.SessionID != "sess-9" {
		t.Fatalf("init 应产出带 SessionID 的 progress，实际 %+v", ev)
	}
}

// 回合收尾（trailer=finish）→ result，且带 git 取证
func TestMapResultFinishEmitsResult(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "result", Subtype: "success",
		Result: `{"branch":"handoff/ab","commit":"c0ffee","summary":"完成"}`})
	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("finish trailer 应产出成功 result，实际 %+v", ev)
	}
	if ev.Result.Branch != "handoff/ab" || ev.Result.Commit != "c0ffee" {
		t.Errorf("git 字段未透传: %+v", ev.Result)
	}
}

// 回合收尾（trailer=ask）→ question
func TestMapResultAskEmitsQuestion(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "result", Subtype: "success",
		Result: `{"ask":"用 pgx 还是 gorm？"}`})
	ev := mustRecv(t, r)
	if ev.Type != "question" || ev.Text != "用 pgx 还是 gorm？" {
		t.Fatalf("ask trailer 应产出 question，实际 %+v", ev)
	}
}

// 死亡哨兵 → 失败 result（非零退出码）
func TestMapExitSentinelEmitsFailure(t *testing.T) {
	a, r := newTestRun(t)
	a.mapMessage(r, streamMsg{Type: "handoff_exit", ExitCode: 137})
	ev := mustRecv(t, r)
	if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
		t.Fatalf("哨兵应产出失败 result，实际 %+v", ev)
	}
	if ev.Result.FailReason == "" {
		t.Error("失败原因不得为空（审核者要靠它判断怎么处置）")
	}
}

// 权限请求 → permission 事件，PermissionID 必须是裸 tool_use_id
func TestPermissionEventUsesRawToolUseID(t *testing.T) {
	a, r := newTestRun(t)
	a.onPermissionAsk(r, permAsk{ToolUseID: "toolu_7", ToolName: "Bash",
		Input: []byte(`{"command":"rm -rf x"}`)})
	ev := mustRecv(t, r)
	if ev.Type != "permission" || ev.PermissionID != "toolu_7" {
		t.Fatalf("PermissionID 必须是裸 tool_use_id，实际 %+v", ev)
	}
	if ev.Text == "" {
		t.Error("权限描述不得为空（审核者要靠它决定批不批）")
	}
}

// 契约：任务不在运行中时，Send/RespondPermission/Stop 必须包装哨兵错误
func TestNotRunningWrapsSentinel(t *testing.T) {
	a := New(nil)
	if err := a.Send(t.Context(), "no-such-task", "x"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("Send 应包装 ErrTaskNotRunning，实际 %v", err)
	}
	if err := a.RespondPermission(t.Context(), "no-such-task", "p", "once"); !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Errorf("RespondPermission 应包装 ErrTaskNotRunning，实际 %v", err)
	}
}
```

`newTestRun` / `mustRecv` 两个辅助函数在同文件实现：前者构造 `*Adapter` 与一个带缓冲事件通道的 `*runState`（`taskDir` 用 `t.TempDir()`），后者从通道取一条事件、2 秒超时即 `t.Fatal`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run 'TestMap|TestPermission|TestNotRunning' -v`
Expected: 编译失败 `undefined: New`

- [ ] **Step 3: 实现 adapter.go**

骨架：

```go
// adapter.go —— Claude Code 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 五动作编排：Start（物料 → socket → tmux 进程 → 等 init → 投首回合 prompt）、
//     Send（fifo 续接）、RespondPermission（裁决回发 socket）、Stop（kill + 收摊）、
//     Events（事件通道）
//   - stream 消息 → AdapterEvent 映射：init → progress(SessionID)；text_delta →
//     render.log + 节流 progress；result → trailer 分类（ask→question / finish→result /
//     none→兜底 git 实况裁决）；handoff_exit 哨兵 → 失败 result
//   - 权限：perm.sock 的 ask 回调 → permission 事件（PermissionID = 裸 tool_use_id）
//
// 边界：
//   - 不写 store、不做审批判断（executor 包级边界）：会话 id 经事件交 manager 落库
//   - 不做状态机迁移：6 状态迁移全在 manager
//   - 不重试、不决策：解析宽容（未知消息 Debug 跳过、绝不 panic）
package claudecode

// runState 是单任务运行态：进程句柄、权限服务端、事件通道、回合累积文本、起始 commit。

// Start 步骤（失败时逐级回滚已建资源，避免半启动残留）：
//   1. 生成 session uuid 与 sockPath
//   2. newPermServer(sockPath, log, onAsk)
//   3. WriteTaskEnv(...) → settings/mcp/prompt
//   4. StartProc(...) → tmux 会话
//   5. 起 tailer；等 system/init（startReadyTimeout=30s），超时读 claude.log 尾部报错 + Kill
//   6. WriteInput(promptText) 投首回合
//   7. writeProcInfo(claude.json)；emit progress{SessionID}（会话就绪信号）

// mapMessage 是事件映射唯一入口（表见 spec §4.2）。
// 注意 text_delta 与 assistant text 的去重：delta 已写过 render.log 的部分，
// assistant 整块只用于回合文本累积，不重复追加，否则实况会出现两遍正文。

// onPermissionAsk 把 permAsk 转成 permission 事件：
//   Text = "<ToolName>: <关键入参>"（Bash 取 command，Edit/Write 取 file_path，
//   其余取 input 紧凑 JSON），经 turn.Truncate 截断。

// RespondPermission：decision "once"→allow、"reject"→deny（理由取 manager 传入文本）；
// 其他取值报错——不认识的裁决绝不当成放行。

// Stop：permServer.Close() → proc.Kill() → 关事件通道；幂等。
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -v -race`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志**

- `Start` 的 7 个步骤各一条 Info（带 task/session）；任一失败 Error 带 cause 与已回滚的资源
- 等 init 超时：Error 带 `claude.log` 尾部
- 每个 AdapterEvent 产出：Info（permission/question/result）/ Debug（progress，高频）
- `Send`：Info 带字节数；`RespondPermission`：Info 带 permID + decision
- `Stop`：Info 带 task 与回收结果
- 未知消息类型：Debug（不得 Warn，正常流里大量存在）

- [ ] **Step 6: 加意图注释**

文件头职责+边界+映射依据（注明取自 2026-08-08 真实采样，claude 2.1.220）；`Start` 回滚顺序的 why；delta 与整块去重的 why；`RespondPermission` 拒绝未知裁决的 why；兜底分类（trailer=none 时看有无新提交）的 why。

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code adapter 五动作与事件映射（B2 Task 7）"
```

---

## Task 8: Resume 与看门狗

**Files:**
- Modify: `internal/executor/claudecode/adapter.go`（加 `Resume`）
- Modify: `internal/executor/claudecode/proc.go`（加 `Alive`）
- Test: `internal/executor/claudecode/resume_test.go`

**Interfaces:**
- Consumes: Task 5 的 `procExited` / `readProcInfo`、Task 6 的 tailer
- Produces: `func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (alive bool, err error)`（满足 manager 的 `restorer` 可选接口）

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/claudecode/resume_test.go`，三个用例：

```go
// 凭据缺失 → 判死，不报错（manager 按不存活走 failed 恢复路径）
func TestResumeMissingProcInfo(t *testing.T)

// 关键路径：tmux 会话还在（窗口 1 的 tail 撑着）但 claude 已退 → 必须判死。
// 这是本 adapter 最容易误判为存活的场景，opencode 靠 HTTP 探活兜住，我们靠哨兵。
func TestResumeSessionAliveButProcessExited(t *testing.T) {
	// 写 claude.json + out.jsonl（含 handoff_exit 哨兵）
	// 桩掉 tmux 探活让它返回 true
	// 断言 alive == false
}

// 进程存活 → 从 offset 续读，已消费回合不重放
func TestResumeContinuesFromOffset(t *testing.T)
```

tmux 探活需要一个测试缝：在 `proc.go` 里把 `tmux has-session` 包成包级变量 `var tmuxHasSession = func(session string) bool { ... }`，测试中替换（与 `cmd/attach.go` 的 `execveFn` 同手法）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run TestResume -v`
Expected: 编译失败 `undefined: (*Adapter).Resume`

- [ ] **Step 3: 实现 Resume 与看门狗**

```go
// Resume 恢复 agentd 重启前已在执行的任务（manager 的 restorer 可选接口）。
//
// 存活判据（本 adapter 与 opencode 的关键差异）：tmux has-session **不可用**
// ——窗口 1 的 tail -f render.log 会一直活着，claude 早死了会话依然存在。
// 判据是两条，缺一即视为死亡：
//   1. out.jsonl 中不含 handoff_exit 哨兵
//   2. tmux 会话存在
//
// 返回：
//   - alive=false 时 manager 把任务转 failed 交审核者裁决（保守优于静默）
func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error)
```

看门狗：主信号是哨兵（tailer 自然送达，无需轮询）；兜底一路周期 `tmuxHasSession`（间隔 2s，连续 3 次失败判死，与 opencode 的 `watchdogFailThreshold` 同值），发现死亡时以 `claude.log` 尾部作 `FailReason` emit 失败 result。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -v -race`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志与注释**

日志：`Resume` 进入 Info（带 task/session/taskDir）；判死时 Info 带判据（哨兵 or 会话消失）与退出码；判活 Info 带续读 offset；看门狗判死 Error 带 `claude.log` 尾部。
注释：`Resume` 的存活判据 why（整段，这是最容易被后人"优化"掉的地方——必须写清楚为什么不能只看 tmux）。

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/executor/claudecode/
git commit -m "feat: Claude Code adapter 恢复与看门狗（哨兵判死，B2 Task 8）"
```

---

## Task 9: 注册接入、文档与真机验收

**Files:**
- Modify: `cmd/agentd.go:79-82`
- Modify: `README.md`
- Test: `cmd/agentd_test.go`（注册断言与被测代码同包——注册表在 `cmd` 里，放 `internal/agentd` 测不到）

**Interfaces:**
- Consumes: Task 7 的 `claudecode.New`
- Produces: `handoff dispatch --executor claude` 可用

- [ ] **Step 1: 写失败测试**

先把 `cmd/agentd.go:79-82` 的内联字面量抽成同文件的可测构造函数：

```go
// defaultAdapters 返回 agentd 的 executor 注册表（name → Adapter）。
//
// 抽成函数而非内联字面量：注册表是 dispatch --executor 路由的唯一真相，
// 漏注册的症状是「派发时报未注册」而不是编译错误，值得一条断言守着。
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"fake":     fake.New(nil),
	}
}
```

创建 `cmd/agentd_test.go`：

```go
package cmd

import (
	"log/slog"
	"testing"
)

// 注册表必须认识 claude：dispatch --executor claude 的路由前提
func TestAdapterRegistryHasClaude(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	if _, ok := ads["claude"]; !ok {
		names := make([]string, 0, len(ads))
		for n := range ads {
			names = append(names, n)
		}
		t.Fatalf("adapter 注册表缺 claude，实际注册: %v", names)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestAdapterRegistry -v`
Expected: FAIL —— 注册表缺 claude

- [ ] **Step 3: 注册 adapter**

在 `cmd/agentd.go` 的 `defaultAdapters` 里加一行：

```go
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"claude":   claudecode.New(logger),
		"fake":     fake.New(nil),
	}
}
```

并把原 `ads := map[string]executor.Adapter{...}` 调用点改为 `ads := defaultAdapters(logger)`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./... -race`
Expected: 全绿

- [ ] **Step 5: 更新 README**

- 架构图的「executor adapter」一行补 claude
- 快速开始的 `--executor` 说明补 `claude`（并注明需本机装 `claude` 且已登录）
- 已知限制补 Task 1 探针的实际结论（配置继承与权限优先级）
- attach 一节注明：claude 任务的 tmux 两窗口与 opencode 同构

- [ ] **Step 6: 真机 e2e 验收**

在本机跑一次真实派发（**不要在 handoff 仓库自身上跑**，另找一个干净仓库）：

```bash
handoff agentd --executor=claude
handoff dispatch --repo /path/to/clean-repo --prompt "在 README 末尾加一行 'hello from claude'，然后提交"
```

逐项确认并记录：
1. `handoff attach <task>` 进得去，窗口 0 有 stream-json 流、窗口 1 有可读正文实况
2. 触发一次权限升级（prompt 里要求跑 `rm -rf` 之类），审核者侧 `handoff wait` 被唤醒
3. `handoff reply --approve` 后 claude 继续执行
4. `handoff continue <task> "再加一行 world"` 能续接同一会话（检查 `session_id` 未变）
5. `handoff diff` 看到改动，`handoff done` 归档正常
6. 重启 agentd（`kill` 后重跑），`handoff show` 显示任务仍 running 且事件继续

任一项不通过，回到对应 Task 修复，不得跳过。

- [ ] **Step 7: 更新 backlog**

把 `docs/superpowers/backlog.md` 的 B2 状态改为 `✅ done(已验)`，验收栏填实际跑过的命令与结论，变更痕迹栏记 commit 范围。

- [ ] **Step 8: Commit**

```bash
gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/agentd/ ./internal/executor/claudecode/
git add cmd/agentd.go README.md docs/superpowers/backlog.md internal/agentd/manager_test.go
git commit -m "feat: 注册 claude adapter 并补文档（B2 Task 9）"
```

---

## 自检记录

**Spec 覆盖**：§3.1 启动脚本 → Task 5；§3.2 tmux 布局 → Task 5；§3.3 文件契约 → Task 2/5；§3.4 降级路线 → 无需实现（备选记录）；§4.1 Start → Task 7；§4.2 事件映射 → Task 6/7；§4.3 Send → Task 5/7；§4.4 RespondPermission → Task 3/7；§4.5 Stop → Task 7；§4.6 Resume/看门狗 → Task 8；§5.1 权限链路 → Task 3/4/7；§5.2 三条纪律 → Task 3/4；§5.3 权限描述 → Task 7；§5.4 任务级策略 → Task 1/2；§6 共享包 → 前置（B3 会话）；§7 接入面 → Task 9；§8 错误处理 → 分散在各 Task 的错误分支；§9 测试策略 → 各 Task 的测试步骤 + Task 9 真机验收。

**已知取舍**：Task 5/6/7/8 的实现步骤给的是带完整注释与 why 的骨架而非逐行代码——这三个文件与 opencode 对应实现同构，逐行抄写会掩盖「照着 opencode 的形状写」这个真正的指令。测试是完整可运行的，它们定义了行为边界。
