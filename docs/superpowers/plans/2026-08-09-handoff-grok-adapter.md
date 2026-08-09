# grok adapter 实现计划（B3）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff dispatch --executor grok` 与 opencode 完全对等——五动作全链路可用、分级审批链原样生效、`handoff attach` 看到同样形态的终端实况。

**Architecture:** `grok agent serve` 在 tmux 里长驻，agentd 用 `coder/websocket` 连它的 ACP（Agent Client Protocol）端点，把 `session/request_permission` / `session/update` / `session/prompt` 响应翻译成 `executor.Adapter` 的五动作与四类事件。进程骨架（tmux + freePort + secret + HTTP 探活 + serve.json + 看门狗 + Resume）与 opencode 同构，回合协议（prompt 纪律模板 + trailer 解析 + git 取证）抽进共享包 `internal/executor/turn` 供两个 adapter 共用。

**Tech Stack:** Go 1.26.1；`github.com/coder/websocket`（项目已有，零新增依赖）；grok 1.0.0（ACP protocolVersion 1）；tmux；测试用假 WebSocket server 与假 grok 脚本，不烧 token。

## Global Constraints

- **设计依据**：[grok adapter 设计（B3）](../specs/2026-08-08-handoff-grok-adapter-design.md)。本计划每个决定都能回指该 spec 的小节。
- **前置排序**：Task 1（抽取 `turn`）必须在 **phase3 B6 落 main 之后**开工——B6 改的正是 `opencode/adapter.go` 的截断代码区且其 plan 带固定行引用。Task 1 完成后**单独合入 main**，Claude Code adapter（B2）会话从该 commit 起步。
- **依赖 B19 的 `StartReq.Env`**（[env 注入计划](2026-08-09-handoff-agent-env-injection.md) Task 4）：Task 3/4 的 `WriteServeScript`/`StartServe` 带 `env []string` 参数，Task 5 的 `Start` 透传 `req.Env`。**若开工时 B19 的 Task 4 尚未落 main**，`executor.StartReq` 还没有 `Env` 字段——此时按本计划签名照写（参数留着），Task 5 的 `Start` 里先传 `nil` 并留 `// TODO(B19): 改为 req.Env` 单行标记，等 B19 合入后一次性替换。**不要反过来把参数删掉**：删了就会忘，最后变成「env 注入对 opencode 生效、对 grok 静默不生效」——这类缺口不报错、极难发现。
- **adapter 边界铁律**（`internal/executor/executor.go` 包级注释）：adapter **不写 store、不做审批判断、不做任务状态机迁移**。任何持久化诉求经事件或返回值交 manager。
- **日志**：一律 `*slog.Logger`（`a.log`），**禁止 `fmt.Printf`**。关键节点（进入/退出、外部调用前后、错误分支、状态变更）必须有日志且带上下文。
- **注释**：新文件必须有文件头注释（职责 + 边界）；导出方法必须有 doc 注释（参数/返回/注意）；非显然分支必须有中文「为什么」注释。
- **秘密脱敏**：`GROK_AGENT_SECRET` 绝不进 argv、绝不进日志、绝不进 `FailReason`。凡输出 `serve.log` 尾部处一律先脱敏。
- **零新增依赖**：不得引入 ACP SDK 或其他第三方包。
- **入站请求必须有出口**（spec §4.2.3 / §5.3）：连接上任何**带 `id`** 的对方消息都必须得到应答——识别的按语义处理，未识别的回 `-32601`。静默丢弃会让 `session/prompt` 永不返回、serve 进程健在、看门狗探活通过，任务表面在跑实则永久静止。这是本 adapter 最坏的一类故障，实测已复现。同理：**分发依据是有没有 `method` 字段，不是 id 数值**——agent 侧请求 id 从 0 自增，与本端 id 空间重叠。
- **测试不烧 token**：所有自动化测试用假 WS server / 假脚本；真机验收单列 Task 9。
- **提交粒度**：每个 Task 末尾一次提交，commit message 用中文，遵循仓库现有风格。

---

### Task 1: 抽取 `internal/executor/turn` 共享包

把两个 adapter 同构的回合协议件从 opencode 搬进新包。**这是 B2/B3 的共同前置，完成即单独合 main。**

**Files:**
- Create: `internal/executor/turn/protocol.go`（prompt 模板 + trailer 解析）
- Create: `internal/executor/turn/text.go`（截断工具）
- Create: `internal/executor/turn/gitprobe.go`（git 回合取证）
- Create: `internal/executor/turn/render.go`（render.log 追加）
- Create: `internal/executor/turn/protocol_test.go`、`text_test.go`、`gitprobe_test.go`、`render_test.go`
- Modify: `internal/executor/opencode/taskenv.go`（删 `promptTemplate`/`promptTmpl`/`promptData`/`Trailer`/`ParseTrailer`，改调 turn）
- Modify: `internal/executor/opencode/adapter.go`（删 `clampQuestion`/`tailRunes`/`gitTurnStatus`/`appendRender`，改调 turn）
- Modify: `internal/executor/opencode/api.go`（删 `truncateMarked`/`truncateRunes`，改调 turn）
- Modify: `internal/executor/opencode/adapter_test.go`（`truncateMarked` 引用改 turn，仅标识符）
- Modify: `internal/executor/opencode/regression_group_a_test.go`（`questionTextLimit`/`tailRunes` 引用改 turn，仅标识符；断言不动）
- Modify: `internal/executor/opencode/taskenv_test.go`（删除 trailer 相关用例，已由 `turn/protocol_test.go` 承接）

**Interfaces:**
- Consumes: `executor.TruncationMarker`（`internal/executor/executor.go:45`）
- Produces（后续 Task 全部依赖这些确切签名）：
  - `turn.RenderPrompt(taskID, planContent string) (string, error)`
  - `turn.ParseTrailer(text string) (kind string, t turn.Trailer)`，`kind` ∈ `"ask"|"finish"|"none"`
  - `type turn.Trailer struct { Question, Branch, Commit, Summary string }`
  - `turn.TruncateMarked(s string, n int) string`
  - `turn.TruncateRunes(s string, n int) string`
  - `turn.TailRunes(s string, n int) string`
  - `turn.ClampQuestion(text string) string`
  - `turn.GitTurnStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error)`
  - `turn.AppendRender(renderLogPath, delta string) error`

- [ ] **Step 1: 建包与文件头注释，先只放类型与签名（不实现）**

`internal/executor/turn/protocol.go`：

```go
// Package turn 提供 executor 无关的「回合协议」：教模型协议的 prompt 模板、
// 解析模型输出的 trailer、回合取证与文本工具。
//
// 职责：
//   - RenderPrompt：把实现计划渲染成带回合纪律（提问/收尾/不切分支）的启动 prompt
//   - ParseTrailer：从回合末文本宽容提取协议 JSON（ask/finish）
//   - GitTurnStatus：trailer 缺失时以「是否有新提交」作事实裁决
//   - 文本截断与 render.log 追加等两 adapter 共用的小工具
//
// 边界：
//   - 不认识任何具体 executor（opencode/grok/claude），不发请求、不起进程
//   - 不做状态机迁移、不写 store：只做纯变换与两个受限 I/O（git 只读、日志追加）
//
// 为什么 prompt 模板与 ParseTrailer 必须同包：教模型协议的 prompt 与解析协议的
// 代码是同一契约的两半，分居两处必然出现「改纪律只改一半」的漂移——两个 executor
// 的审核者会看到不一样的东西。
package turn

// Trailer 是从回合末消息文本提取出的协议数据。
type Trailer struct {
	Question string // ask 类型：需要人决策的问题
	Branch   string // finish 类型：提交所在分支
	Commit   string // finish 类型：提交 hash
	Summary  string // finish 类型：50 字内摘要
}
```

- [ ] **Step 2: 写 protocol 的失败测试**

`internal/executor/turn/protocol_test.go`：

```go
package turn_test

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
)

func TestRenderPromptEmbedsTaskIDAndPlan(t *testing.T) {
	got, err := turn.RenderPrompt("T1", "第一步：改 foo.go")
	if err != nil {
		t.Fatalf("RenderPrompt 出错: %v", err)
	}
	for _, want := range []string{"T1", "第一步：改 foo.go", `{"ask":`, `{"branch":`} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt 缺少 %q\n实际:\n%s", want, got)
		}
	}
}

func TestParseTrailer(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		wantKind string
		wantVal  string
	}{
		{"ask 单行", `{"ask":"用哪个库？"}`, "ask", "用哪个库？"},
		{"finish 单行", `{"branch":"handoff/T1","commit":"abc123","summary":"done"}`, "finish", "abc123"},
		{"取最后一个 JSON 行", "{\"ask\":\"旧\"}\n说明文字\n{\"ask\":\"新\"}", "ask", "新"},
		{"末行普通文本时回退更早的 JSON 行", "{\"ask\":\"问题\"}\n收尾说明", "ask", "问题"},
		{"损坏 JSON 按 none", `{"ask":`, "none", ""},
		{"无 JSON 行按 none", "普通输出，没有协议行", "none", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, tr := turn.ParseTrailer(c.text)
			if kind != c.wantKind {
				t.Fatalf("kind = %q，期望 %q", kind, c.wantKind)
			}
			switch c.wantKind {
			case "ask":
				if tr.Question != c.wantVal {
					t.Errorf("Question = %q，期望 %q", tr.Question, c.wantVal)
				}
			case "finish":
				if tr.Commit != c.wantVal {
					t.Errorf("Commit = %q，期望 %q", tr.Commit, c.wantVal)
				}
			}
		})
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/executor/turn/ -run 'TestRenderPrompt|TestParseTrailer' -v`
Expected: 编译失败 —— `undefined: turn.RenderPrompt`、`undefined: turn.ParseTrailer`

- [ ] **Step 4: 实现 protocol.go（内容整体搬自 `opencode/taskenv.go:50-65,196-250`，逐字保留纪律文本）**

在 `protocol.go` 追加：

```go
import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// promptTemplate 是任务 prompt 的回合制纪律模板，逐字来自一期 spec §6 的落地。
//
// 注意：模板内嵌的 {"ask":...}/{"branch":...} 是给模型看的协议样例，
// 与 text/template 语法不冲突（不含 {{ ），可直接放在字面文本中。
const promptTemplate = `你是 handoff 任务 {{.TaskID}} 的执行者，按下方实现计划执行。铁律：
1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。审核者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   然后输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。

--- 实现计划 ---
{{.PlanContent}}
`

// promptTmpl 是 promptTemplate 的编译结果。Must 保证拼写错误的模板在包加载时
// 立刻暴露（编程错误），而不是在任务运行时才崩——模板不依赖运行时状态。
var promptTmpl = template.Must(template.New("prompt").Parse(promptTemplate))

type promptData struct {
	TaskID      string
	PlanContent string
}

// RenderPrompt 渲染带回合纪律的启动 prompt。
//
// 参数：
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文（dispatch 侧已把 --prompt 附加指令拼在其后），
//     原样嵌入「实现计划」段，本函数不再二次拼接
//
// 返回：渲染后的 prompt 全文；模板执行失败时返回错误
func RenderPrompt(taskID, planContent string) (string, error) {
	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptData{TaskID: taskID, PlanContent: planContent}); err != nil {
		return "", fmt.Errorf("渲染 prompt 模板: %w", err)
	}
	return buf.String(), nil
}

// ParseTrailer 从回合末消息文本提取协议 JSON（取最后一个以 { 开头的行）。
//
// 返回：
//   - kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
//   - t: 解析出的协议数据；kind 为 "none" 时为零值
//
// 注意：
//   - 宽容语义：末行是普通文本时回退到更早的 { 开头行；找不到或 JSON 损坏时
//     返回 "none"，绝不 panic（模型输出不可信，防御在边界上做）
//   - 纯函数：不打日志，由调用方记录提取结果
func ParseTrailer(text string) (kind string, t Trailer) {
	// 取最后一个以 { 开头的行：模型可能在正文中间输出过协议 JSON 后又追加
	// 说明文字，只有最后一个才有「本回合结论」的语义
	var last string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			last = line
		}
	}
	if last == "" {
		return "none", t
	}

	// 宽容解码：不设 DisallowUnknownFields，模型多带字段时仍能提取已知协议字段
	var payload struct {
		Question string `json:"ask"`
		Branch   string `json:"branch"`
		Commit   string `json:"commit"`
		Summary  string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(last)), &payload); err != nil {
		return "none", t
	}
	t = Trailer{Question: payload.Question, Branch: payload.Branch,
		Commit: payload.Commit, Summary: payload.Summary}

	// ask 与 finish 协议互斥（模型按纪律一次只输出一种），问号优先判定
	switch {
	case t.Question != "":
		return "ask", t
	case t.Branch != "" || t.Commit != "" || t.Summary != "":
		return "finish", t
	default:
		return "none", t
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/executor/turn/ -run 'TestRenderPrompt|TestParseTrailer' -v`
Expected: PASS（8 个子用例全绿）

- [ ] **Step 6: 写 text/gitprobe/render 的失败测试**

`internal/executor/turn/text_test.go`：

```go
package turn_test

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

func TestTruncateMarkedAppendsMarkerOnlyWhenTruncated(t *testing.T) {
	if got := turn.TruncateMarked("短", 10); got != "短" {
		t.Errorf("未超限不应加标记，得到 %q", got)
	}
	got := turn.TruncateMarked(strings.Repeat("字", 20), 5)
	if !strings.HasSuffix(got, executor.TruncationMarker) {
		t.Errorf("超限必须以截断标记收尾，得到 %q", got)
	}
	if r := []rune(strings.TrimSuffix(got, executor.TruncationMarker)); len(r) != 5 {
		t.Errorf("截断后正文应为 5 个 rune，得到 %d", len(r))
	}
}

func TestTailRunesKeepsSuffix(t *testing.T) {
	if got := turn.TailRunes("abcdef", 3); got != "def" {
		t.Errorf("TailRunes = %q，期望 def", got)
	}
	if got := turn.TailRunes("ab", 5); got != "ab" {
		t.Errorf("不足 n 时应原样返回，得到 %q", got)
	}
}

// TestClampQuestionPointsAtRenderLog 钉住 ClampQuestion 与 TruncateMarked 的
// **语义差异**：question 的全文只在 render.log 里，截断后必须指路。
// opencode 的 regression_group_a_test.go 断言同一件事，这里是搬包后的同源钉子。
func TestClampQuestionPointsAtRenderLog(t *testing.T) {
	short := "很短的问题"
	if got := turn.ClampQuestion(short); got != short {
		t.Errorf("未超限不应改写，得到 %q", got)
	}

	long := strings.Repeat("长", turn.QuestionTextLimit+1000)
	got := turn.ClampQuestion(long)
	if n := len([]rune(got)); n > turn.QuestionTextLimit+200 {
		t.Errorf("截断后 %d 字符仍超限（上限 %d）", n, turn.QuestionTextLimit)
	}
	if !strings.Contains(got, "render.log") {
		t.Errorf("截断后必须指明全文去处，尾部为 %q", turn.TailRunes(got, 80))
	}
	// 反向断言：不得退化成 TruncateMarked 的通用尾缀——那会丢掉 render.log 指路，
	// 而 question 的全文不在工单里，审核者将无处可查（见 ClampQuestion 的注释）。
	if strings.HasSuffix(got, executor.TruncationMarker) {
		t.Error("ClampQuestion 不得复用 TruncateMarked 的尾缀")
	}
}
```

> 该文件需 `import ("strings"; "testing"; "github.com/xushixin/handoff/internal/executor"; "github.com/xushixin/handoff/internal/executor/turn")`。

`internal/executor/turn/render_test.go`：

```go
package turn_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
)

func TestAppendRenderCreatesAndAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "render.log")
	if err := turn.AppendRender(p, "第一段"); err != nil {
		t.Fatalf("首次追加出错: %v", err)
	}
	if err := turn.AppendRender(p, "第二段"); err != nil {
		t.Fatalf("二次追加出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if string(b) != "第一段第二段" {
		t.Errorf("内容 = %q，期望 第一段第二段", string(b))
	}
}
```

`internal/executor/turn/gitprobe_test.go`：

```go
package turn_test

import (
	"os/exec"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
)

// initRepo 建一个带首提交的临时仓库，返回路径与首提交 hash。
func initRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse 失败: %v", err)
	}
	return dir, string(out[:len(out)-1])
}

func TestGitTurnStatusDetectsNewCommit(t *testing.T) {
	dir, start := initRepo(t)

	_, commit, hasNew, err := turn.GitTurnStatus(dir, start)
	if err != nil {
		t.Fatalf("无新提交时出错: %v", err)
	}
	if hasNew {
		t.Errorf("尚未提交，hasNew 应为 false")
	}
	if commit != start {
		t.Errorf("commit = %q，期望 %q", commit, start)
	}

	cmd := exec.Command("git", "-c", "user.email=t@e", "-c", "user.name=t",
		"commit", "-q", "--allow-empty", "-m", "second")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("二次提交失败: %v %s", err, out)
	}
	_, commit2, hasNew2, err := turn.GitTurnStatus(dir, start)
	if err != nil {
		t.Fatalf("有新提交时出错: %v", err)
	}
	if !hasNew2 {
		t.Errorf("已有新提交，hasNew 应为 true")
	}
	if commit2 == start {
		t.Errorf("commit 应已推进，仍为 %q", commit2)
	}
}
```

- [ ] **Step 7: 运行确认失败**

Run: `go test ./internal/executor/turn/ -v`
Expected: 编译失败 —— `undefined: turn.TruncateMarked` / `TailRunes` / `AppendRender` / `GitTurnStatus`

- [ ] **Step 8: 实现 text.go / render.go / gitprobe.go（搬自 opencode 对应函数）**

`internal/executor/turn/text.go`：

```go
// text.go —— 回合文本的截断工具。
//
// 职责：按 rune 截断，并在确实发生截断时追加显式标记
// 边界：纯函数，不打日志、不做 I/O；截断标记的语义契约在 executor 包
package turn

import "github.com/xushixin/handoff/internal/executor"

// QuestionTextLimit 是交给审核者的回合文本上限。兜底分类会把整个回合原文当
// question 发出，一个失控的长回合会直接灌进工单行与审核者终端；全文始终在
// 任务目录的 render.log 里，截断不丢证据。
//
// 为什么导出：opencode 的 regression_group_a_test.go 直接断言这个上限，
// 搬包后它得能从 turn 引到同一个值——两处各写一个 8000 就会悄悄漂移。
const QuestionTextLimit = 8000

// TruncateMarked 按 rune 截断到 n，确实截断时追加 executor.TruncationMarker。
//
// 为什么必须带标记：上层据此 fail-closed——权限文本含标记说明裁决者看到的是
// 不完整命令，危险片段可能落在截断之外，黑名单与廉价模型都不可信。
func TruncateMarked(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + executor.TruncationMarker
}

// TruncateRunes 按 rune 截断到 n，不加任何标记。
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// TailRunes 返回末尾 n 个 rune；不足 n 时原样返回。
func TailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// ClampQuestion 把兜底分类产出的整段回合文本收敛到 QuestionTextLimit，
// 超出时追加尾缀指明全文去处。
//
// 为什么**不能**复用 TruncateMarked：两者的「全文在哪」不同，尾缀因此必须不同。
//   - TruncateMarked 用于 permission 文本，全文在工单里（B6 契约：工单存全文、
//     事件截断），审核者 `handoff show` 就能拿到，`…（已截断）` 足够；
//   - 本函数用于 question 文本，全文**不在工单里**，只在任务目录的 render.log。
//     不指路 = 审核者拿到半截文本且不知道去哪找全文，证据链断掉。
//
// 这段尾缀是逐字从 opencode 现有实现搬来的，opencode 的
// regression_group_a_test.go 断言 `strings.Contains(ev.Text, "render.log")`，
// 改字面量即回归。
func ClampQuestion(text string) string {
	if len([]rune(text)) <= QuestionTextLimit {
		return text
	}
	return TruncateRunes(text, QuestionTextLimit) +
		"\n\n…（回合文本过长已截断，完整内容见任务目录 render.log）"
}
```

`internal/executor/turn/render.go`：

```go
// render.go —— 回合文本增量落盘到 render.log。
//
// 职责：把模型文本增量追加到任务目录的 render.log，供 tmux 第二窗口 tail -f 旁观
// 边界：只做追加写；文件不存在时创建；不轮转、不清理（任务归档时随目录一起走）
package turn

import (
	"fmt"
	"os"
)

// AppendRender 把 delta 追加到 renderLogPath（不存在则创建，权限 0644）。
//
// 注意：调用方通常在高频文本增量路径上调用本函数，失败应只 Warn 不中断回合
// ——可见性是增强能力，不值得为它挂掉任务。
func AppendRender(renderLogPath, delta string) error {
	f, err := os.OpenFile(renderLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", renderLogPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(delta); err != nil {
		return fmt.Errorf("写 %s: %w", renderLogPath, err)
	}
	return nil
}
```

`internal/executor/turn/gitprobe.go`：

```go
// gitprobe.go —— 回合的 git 事实取证。
//
// 职责：读当前分支与 HEAD，并与回合起点 commit 比对判断「是否有新提交」
// 边界：只读 git，绝不写；不做任何裁决，裁决由调用方基于事实决定
package turn

import (
	"fmt"
	"os/exec"
	"strings"
)

// GitTurnStatus 返回工作区当前分支、HEAD commit，以及相对 startCommit 是否有新提交。
//
// 参数：
//   - repoPath: 任务工作目录（仓库或 worktree 路径）
//   - startCommit: 回合起点的 HEAD；空串表示起点未知，此时 hasNew 恒为 false
//
// 返回：分支名、HEAD hash、是否有新提交、错误
//
// 为什么需要它：模型可能不守收尾纪律（不输出 trailer）。此时唯一可信的是 git
// 实况——有新提交才可能是「干完了」，没有就该交审核者，绝不替模型宣布完成。
func GitTurnStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error) {
	run := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", repoPath}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if branch, err = run("rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", "", false, err
	}
	if commit, err = run("rev-parse", "HEAD"); err != nil {
		return branch, "", false, err
	}
	return branch, commit, startCommit != "" && commit != startCommit, nil
}
```

- [ ] **Step 9: 运行 turn 包全部测试**

Run: `go test ./internal/executor/turn/ -v`
Expected: PASS（全部用例）

- [ ] **Step 10: opencode 改调 turn 包，删除被搬走的实现**

在 `internal/executor/opencode/` 三个文件里做等价替换（**只改调用，不改语义**）：

| 删除（原位置） | 替换为 |
|---|---|
| `taskenv.go` 的 `promptTemplate`/`promptTmpl`/`promptData` 与 `WriteTaskEnv` 内的模板渲染 | `turn.RenderPrompt(taskID, planContent)` |
| `taskenv.go` 的 `Trailer`/`ParseTrailer` | `turn.Trailer` / `turn.ParseTrailer` |
| `adapter.go` 的 `clampQuestion` | `turn.ClampQuestion` |
| `adapter.go` 的 `tailRunes` | `turn.TailRunes` |
| `adapter.go` 的 `gitTurnStatus`（方法体） | 方法保留为薄封装，内部调 `turn.GitTurnStatus(r.repoPath, r.startCommit)`，保留原有日志 |
| `adapter.go` 的 `appendRender`（方法体） | 方法保留为薄封装，内部调 `turn.AppendRender(filepath.Join(r.taskDir, renderLogFileName), delta)` |
| `api.go` 的 `truncateMarked`/`truncateRunes` | `turn.TruncateMarked` / `turn.TruncateRunes` |
| `adapter.go` 的 `questionTextLimit` 常量 | 删除，引用改 `turn.QuestionTextLimit` |

保留 `gitTurnStatus`/`appendRender` 作方法薄封装的**理由**：它们的调用点带 `r` 的上下文（repoPath/startCommit/taskDir）与既有日志，改成全局函数会让每个调用点重复拼参数，且丢掉日志。

**测试文件同步（本 Task 必须一并处理，否则编译不过）**。这些符号在测试里也有引用，
符号搬家后测试文件**必然要改**——但改的性质分两类，只有第一类被允许：

| 文件 | 引用的符号 | 处置 |
|---|---|---|
| `opencode/adapter_test.go`（`package opencode`） | `truncateMarked` | **只换标识符** → `turn.TruncateMarked`，加 import。断言一字不动 |
| `opencode/regression_group_a_test.go`（`package opencode`） | `questionTextLimit`、`tailRunes` | **只换标识符** → `turn.QuestionTextLimit` / `turn.TailRunes`。`strings.Contains(ev.Text, "render.log")` 这条断言**保持原样**——它正是 `ClampQuestion` 语义的锚点 |
| `opencode/taskenv_test.go`（`package opencode_test`） | `opencode.ParseTrailer`、`opencode.Trailer` | **删除这些用例**：同一批用例 Step 2 已在 `turn/protocol_test.go` 原文重建，留着就是两份同样的测试。删除前逐条比对，确认 turn 侧覆盖了每一条，缺哪条补哪条 |

> 开工前先自己核一遍引用，别只信上表：
> `grep -rn '\bParseTrailer\|\bTrailer\|truncateMarked\|truncateRunes\|tailRunes\|questionTextLimit\|clampQuestion\b' internal/executor/opencode/`
> 本计划撰写时不存在 `export_test.go`，若届时已有，同样按上表分类处置。

- [ ] **Step 11: 跑 opencode 全量回归——这是本 Task 的验收硬指标**

Run: `go test ./internal/executor/... ./internal/agentd/ -count=1`
Expected: **全绿**。任一失败即说明抽取改了语义，回退重做。

**关于「不许改测试」的确切边界**（Step 10 已列出必须改的文件，此处定验收口径）：

- ✅ **允许**：import 行、标识符替换（`truncateMarked` → `turn.TruncateMarked` 这类）、
  以及 Step 10 表里点名的用例删除。这是符号搬家的机械后果，不削弱任何断言。
- ❌ **禁止**：改任何断言、期望值、数字或字符串字面量。测试红了就去改实现，不许改期望。

验收方式（自己先跑一遍再交）：

```bash
git diff -- '*_test.go' | grep '^[+-]' | grep -v '^[+-][+-]' | grep -vE '^\+.*turn\.|^-.*\b(truncateMarked|truncateRunes|tailRunes|questionTextLimit|clampQuestion)\b|^[+-]\s*"github.com/xushixin/handoff/internal/executor/turn"'
```

除 `taskenv_test.go` 的整段删除外，上面这条命令**不应输出任何带字面量的行**。
出现任何数字或字符串字面量的增删，即为不合格。

- [ ] **Step 12: 全仓构建与静态检查**

Run: `go build ./... && go vet ./...`
Expected: 无输出（通过）

- [ ] **Step 13: 提交**

```bash
git add internal/executor/turn internal/executor/opencode
git commit -m "refactor: 抽取 internal/executor/turn 共享包，opencode 改调

回合协议（prompt 纪律模板 + ParseTrailer）、git 取证、render.log 追加、
文本截断从 opencode 搬进 turn 包，供 grok(B3)/claude(B2) 两个 adapter 共用。

why prompt 模板与 ParseTrailer 必须同包：教模型协议的 prompt 与解析协议的
代码是同一契约的两半，分居两处必然出现改一半的漂移。

why ClampQuestion 不复用 TruncateMarked：question 的全文只在 render.log
（permission 的全文在工单里），尾缀必须指路，逐字保留 opencode 原实现。

测试改动仅为符号搬家的机械后果（import + 标识符）；taskenv_test.go 的
trailer 用例整段删除，已由 turn/protocol_test.go 原文承接。无断言变更。

纯重构，opencode 全量回归绿。"
```

---

### Task 2: ACP WebSocket 客户端（`acp.go`）

双向 JSON-RPC：我方请求→响应匹配、对方通知→分发、**对方请求（权限）→ 回调 + 延迟应答**。

**Files:**
- Create: `internal/executor/grok/acp.go`
- Create: `internal/executor/grok/acp_test.go`

**Interfaces:**
- Consumes: `github.com/coder/websocket`
- Produces:
  - `type ACPClient struct{ ... }`
  - `grok.DialACP(ctx context.Context, wsURL string, h ACPHandler, log *slog.Logger) (*ACPClient, error)`
  - `type ACPHandler interface { OnNotify(method string, params json.RawMessage); OnPermission(reqID json.RawMessage, params json.RawMessage); OnAskQuestion(reqID json.RawMessage, params json.RawMessage); OnClosed(err error) }`
  - `(*ACPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error)`（阻塞等响应）
  - `(*ACPClient) CallAsync(method string, params any) (<-chan ACPResult, error)`（用于 `session/prompt`：一整个回合才响应）
  - `(*ACPClient) Reply(reqID json.RawMessage, result any) error`（应答对方请求，用于权限）
  - `(*ACPClient) Notify(method string, params any) error`（用于 `session/cancel`）
  - `(*ACPClient) Close() error`
  - `type ACPResult struct{ Result json.RawMessage; Err error }`

- [ ] **Step 1: 写失败测试——请求/响应匹配与通知分发**

`internal/executor/grok/acp_test.go`：

```go
package grok_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/executor/grok"
)

// fakeHandler 收集回调，供断言。
type fakeHandler struct {
	notifies chan [2]string // method, params
	perms    chan json.RawMessage
	asks     chan json.RawMessage
	closed   chan error
}

func newFakeHandler() *fakeHandler {
	return &fakeHandler{
		notifies: make(chan [2]string, 16),
		perms:    make(chan json.RawMessage, 4),
		asks:     make(chan json.RawMessage, 4),
		closed:   make(chan error, 4),
	}
}

func (f *fakeHandler) OnNotify(method string, params json.RawMessage) {
	f.notifies <- [2]string{method, string(params)}
}
func (f *fakeHandler) OnPermission(reqID, params json.RawMessage)  { f.perms <- params }
func (f *fakeHandler) OnAskQuestion(reqID, params json.RawMessage) { f.asks <- params }
func (f *fakeHandler) OnClosed(err error)                          { f.closed <- err }

// startFakeAgent 起一个假 ACP agent：按脚本回消息。
// script 收到客户端每条消息后返回要发回的若干条消息（原样字符串）。
func startFakeAgent(t *testing.T, script func(in string) []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			for _, out := range script(string(data)) {
				if err := c.Write(ctx, websocket.MessageText, []byte(out)); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(s *httptest.Server) string { return "ws" + s.URL[len("http"):] }

func TestCallMatchesResponseByID(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		if req.Method == "initialize" {
			// 先插一条无关通知，再回响应：验证不会把通知误当响应
			return []string{
				`{"jsonrpc":"2.0","method":"_x.ai/noise","params":{}}`,
				`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`,
			}
		}
		return nil
	})
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cli.Call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	var got struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(res, &got); err != nil || got.ProtocolVersion != 1 {
		t.Fatalf("响应解析异常: %v %s", err, res)
	}
	select {
	case n := <-h.notifies:
		if n[0] != "_x.ai/noise" {
			t.Errorf("通知 method = %q", n[0])
		}
	case <-time.After(2 * time.Second):
		t.Error("未收到通知回调")
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
```

- [ ] **Step 2: 写失败测试——权限请求回调与延迟应答**

在同文件追加：

```go
func TestPermissionRequestCallbackAndDeferredReply(t *testing.T) {
	replies := make(chan string, 4)
	srv := startFakeAgent(t, func(in string) []string {
		var msg struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &msg)
		switch {
		case msg.Method == "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(*msg.ID) + `,"result":{}}`}
		case msg.Method == "":
			// 客户端对我方请求的应答
			replies <- in
			return nil
		}
		return nil
	})
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}

	// agent 侧主动发权限请求（id=0，与我方 id 空间独立）
	// 由假 agent 直接写：借用 script 之外的通道不方便，改为客户端先发一条触发
	// —— 这里通过 Notify 触发 script 返回权限请求
	_ = cli.Notify("trigger/perm", nil)

	select {
	case p := <-h.perms:
		if !json.Valid(p) {
			t.Fatalf("权限参数非法 JSON: %s", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到权限回调")
	}
}
```

> 实现者注意：上面的 script 需要在收到 `trigger/perm` 时返回
> `{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"c1","title":"Execute ` + "`ls`" + `","rawInput":{"command":"ls"}},"options":[{"optionId":"allow-once","kind":"allow_once"},{"optionId":"reject-once","kind":"reject_once"}]}}`。
> 补齐 script 分支后再跑。

- [ ] **Step 2b: 写失败测试——提问请求、未知请求、id 空间重叠**

这三条各自钉住 spec §5.3 的一条实测结论，缺一条就会在真机上复现一次挂死。同文件追加：

```go
// TestAskQuestionRequestIsSurfacedNotDropped 钉住 spec §4.2.3：
// _x.ai/ask_user_question 是带 id 的请求，丢弃会让 session/prompt 永不返回。
func TestAskQuestionRequestIsSurfacedNotDropped(t *testing.T) {
	const askReq = `{"jsonrpc":"2.0","id":0,"method":"_x.ai/ask_user_question","params":` +
		`{"sessionId":"s","toolCallId":"c9","questions":[{"question":"用哪种语言？",` +
		`"options":[{"label":"Go","description":"用 Go"},{"label":"Rust","description":"用 Rust"}],` +
		`"multiSelect":null}],"mode":"default"}}`
	replies := make(chan string, 4)
	srv := startFakeAgentRecording(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{}}`}
		case "trigger/ask":
			return []string{askReq}
		}
		return nil
	}, replies)

	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/ask", map[string]any{})

	select {
	case p := <-h.asks:
		var got struct {
			ToolCallID string `json:"toolCallId"`
			Questions  []struct {
				Question string `json:"question"`
				Options  []struct {
					Label string `json:"label"`
				} `json:"options"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(p, &got); err != nil {
			t.Fatalf("提问参数解析失败: %v", err)
		}
		if got.ToolCallID != "c9" || len(got.Questions) != 1 ||
			got.Questions[0].Question != "用哪种语言？" || len(got.Questions[0].Options) != 2 {
			t.Fatalf("提问参数未原样上抛: %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("提问请求被丢弃 —— 真机上这会让回合永久挂死（spec §5.3(c)）")
	}
}

// TestUnknownAgentRequestGetsMethodNotFound 钉住：未识别的**有 id** 请求必须回错误，
// 不得静默丢弃——丢弃等于制造同款永久挂死。
func TestUnknownAgentRequestGetsMethodNotFound(t *testing.T) {
	replies := make(chan string, 4)
	srv := startFakeAgentRecording(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{}}`}
		case "trigger/unknown":
			return []string{`{"jsonrpc":"2.0","id":7,"method":"_x.ai/brand_new_thing","params":{}}`}
		}
		return nil
	}, replies)

	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/unknown", map[string]any{})

	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-replies:
			var m struct {
				ID    json.RawMessage `json:"id"`
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(raw), &m) == nil && string(m.ID) == "7" {
				if m.Error == nil || m.Error.Code != -32601 {
					t.Fatalf("未知请求应回 -32601，实得: %s", raw)
				}
				return
			}
		case <-deadline:
			t.Fatal("未知请求未收到任何应答 —— 对方会永久等待")
		}
	}
}

// TestOverlappingRequestIDsDoNotCollide 钉住 spec §5.3(d)：
// agent 侧请求 id 从 0 自增，与本端 id 空间重叠。此处让 agent 主动发一个
// id=1 的请求，而本端第一个请求的 id 也是 1——两者必须各归各路。
func TestOverlappingRequestIDsDoNotCollide(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		if req.Method != "initialize" {
			return nil
		}
		// 先发一个与本端 id 撞号的 agent 请求，再回真正的响应
		return []string{
			`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"method":"session/request_permission",` +
				`"params":{"sessionId":"s","toolCall":{"toolCallId":"cx","title":"Execute ` + "`ls`" + `",` +
				`"rawInput":{"command":"ls"}},"options":[{"optionId":"allow-once","kind":"allow_once"}]}}`,
			`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`,
		}
	})
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cli.Call(ctx, "initialize", map[string]any{})
	if err != nil {
		t.Fatalf("撞号的 agent 请求污染了响应匹配: %v", err)
	}
	var got struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if json.Unmarshal(res, &got) != nil || got.ProtocolVersion != 1 {
		t.Fatalf("响应内容错误（可能拿到了 agent 请求）: %s", res)
	}
	select {
	case <-h.perms:
	case <-time.After(2 * time.Second):
		t.Fatal("撞号的 agent 请求被当成响应吃掉了")
	}
}
```

> `startFakeAgentRecording` 是 `startFakeAgent` 的变体：额外把**客户端发出的每条消息**
> 原样投递到 `replies` 通道，供断言应答内容。实现时把它和 `startFakeAgent` 收敛成同一个
> 函数（`startFakeAgent` 传 nil 通道即可），不要写两份 WS 样板。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestCallMatches|TestPermissionRequest|TestAskQuestion|TestUnknownAgentRequest|TestOverlappingRequestIDs' -v`
Expected: 编译失败 —— `undefined: grok.DialACP`

- [ ] **Step 4: 实现 acp.go**

```go
// acp.go —— ACP（Agent Client Protocol）的 WebSocket JSON-RPC 双向客户端。
//
// 职责：
//   - 维护一条到 grok agent serve 的 WS 连接，跑 JSON-RPC 2.0 双向消息
//   - 我方请求（initialize/session.*）按 id 匹配响应；session/prompt 用 CallAsync
//     异步等待（它要跑完一整个回合才响应）
//   - 对方通知（session/update 及 _x.ai/* 私有通知）经 OnNotify 分发
//   - 对方请求（session/request_permission）经 OnPermission 上抛，应答可延迟
//     任意久后经 Reply 回发——审核者可能过夜才裁决（spec §5.1 实测 20min 无超时）
//
// 边界：
//   - 不认识 ACP 的业务语义（不知道什么是权限、什么是回合），只做协议管道；
//     语义翻译在 adapter.go
//   - 不重连：重连策略属 adapter 的生命周期决策，本层只在连接死亡时 OnClosed 通知
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/coder/websocket"
)

// ACPResult 是一次异步调用的终局（二选一）。
type ACPResult struct {
	Result json.RawMessage
	Err    error
}

// ACPHandler 是 adapter 侧的回调面。实现方必须假定回调在读循环 goroutine 上
// 触发：**不得在回调里做阻塞操作**，否则会卡住整条连接的消息消费。
type ACPHandler interface {
	// OnNotify 收到对方通知（无 id 的消息）
	OnNotify(method string, params json.RawMessage)
	// OnAskQuestion 收到 _x.ai/ask_user_question（对方请求，**必须应答**）。
	// 不应答会让 session/prompt 永不返回、任务永久静止（spec §4.2.3 / §5.3(c) 实测）。
	OnAskQuestion(reqID json.RawMessage, params json.RawMessage)
	// OnPermission 收到 session/request_permission（对方请求，需应答）。
	// reqID 原样保存，裁决回来后经 Reply 回发。
	OnPermission(reqID json.RawMessage, params json.RawMessage)
	// OnClosed 连接终止（err 为终止原因，正常关闭时为 nil）
	OnClosed(err error)
}

// ACPClient 是一条 ACP 连接。并发安全：nextID/pending 由 mu 保护；
// 写连接由 writeMu 串行化（websocket 不允许并发写）。
type ACPClient struct {
	conn   *websocket.Conn
	log    *slog.Logger
	cancel context.CancelFunc

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan ACPResult
	closed  bool
}

// DialACP 连接 ACP 端点并启动读循环。
//
// 参数：
//   - ctx: 仅控制握手阶段；连接生命周期延续到 Close
//   - wsURL: 形如 ws://127.0.0.1:<port>/ws?server-key=<secret>
//   - h: 回调面（不得为 nil）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 注意：wsURL 含 secret，**日志里绝不能打印它**（本函数只记录 host 与 path）。
func DialACP(ctx context.Context, wsURL string, h ACPHandler, log *slog.Logger) (*ACPClient, error) {
	if log == nil {
		log = slog.Default()
	}
	log.Info("ACP 连接中")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		log.Error("ACP 连接失败", "cause", err)
		return nil, fmt.Errorf("连接 ACP 端点: %w", err)
	}
	// 单条消息上限放宽：initialize 响应含完整模型/命令清单，实测数 KB～数十 KB
	conn.SetReadLimit(8 << 20)

	runCtx, cancel := context.WithCancel(context.Background())
	c := &ACPClient{conn: conn, log: log, cancel: cancel, pending: map[int]chan ACPResult{}}
	go c.readLoop(runCtx, h)
	log.Info("ACP 连接就绪")
	return c, nil
}

// Call 发起请求并阻塞等待响应。
func (c *ACPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch, err := c.CallAsync(method, params)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.Result, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallAsync 发起请求并立即返回结果通道。
//
// 为什么需要它：session/prompt 要跑完一整个回合（可能几十分钟）才响应，
// Start 必须立即返回，不能阻塞在这上面。
func (c *ACPClient) CallAsync(method string, params any) (<-chan ACPResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("ACP 连接已关闭")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan ACPResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.write(msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.log.Error("ACP 请求发送失败", "method", method, "cause", err)
		return nil, err
	}
	c.log.Debug("ACP 请求已发出", "method", method, "id", id)
	return ch, nil
}

// Reply 应答对方请求（用于权限裁决回发）。reqID 必须是 OnPermission 收到的原值。
func (c *ACPClient) Reply(reqID json.RawMessage, result any) error {
	if err := c.write(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(reqID), "result": result,
	}); err != nil {
		c.log.Error("ACP 应答发送失败", "req_id", string(reqID), "cause", err)
		return err
	}
	c.log.Info("ACP 应答已发出", "req_id", string(reqID))
	return nil
}

// Notify 发送通知（无需应答，用于 session/cancel）。
func (c *ACPClient) Notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.write(msg)
}

// Close 关闭连接，所有挂起的请求以错误终结。
func (c *ACPClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.log.Info("ACP 连接关闭")
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *ACPClient) write(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化 ACP 消息: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(context.Background(), websocket.MessageText, b)
}

// readLoop 消费连接上的全部消息直到出错，并在退出时终结所有挂起请求。
func (c *ACPClient) readLoop(ctx context.Context, h ACPHandler) {
	var exitErr error
	defer func() {
		c.mu.Lock()
		c.closed = true
		pend := c.pending
		c.pending = map[int]chan ACPResult{}
		c.mu.Unlock()
		// 挂起请求全部以错误终结，避免调用方永久等待
		for id, ch := range pend {
			c.log.Warn("ACP 连接终止，挂起请求作废", "id", id)
			ch <- ACPResult{Err: fmt.Errorf("ACP 连接终止: %w", exitErr)}
		}
		c.log.Info("ACP 读循环退出", "cause", exitErr)
		h.OnClosed(exitErr)
	}()

	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			exitErr = err
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    any    `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			// 宽容：对端输出不可信，坏消息跳过不中断连接
			c.log.Warn("ACP 消息解析失败，跳过", "cause", err)
			continue
		}

		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			// 对方请求。注意先判 Method 再判 ID：agent 侧请求 id 从 0 自增，与本端
			// 请求 id 空间**重叠**（spec §5.3(d) 实测），只看 id 会把对方的请求
			// 误认成自己请求的响应。
			// 未识别的请求一律回 -32601——静默丢弃有 id 的请求 = 让对方永久等待。
			switch msg.Method {
			case "session/request_permission":
				c.log.Info("ACP 收到权限请求", "req_id", string(msg.ID))
				h.OnPermission(append(json.RawMessage(nil), msg.ID...), msg.Params)
				continue
			case "_x.ai/ask_user_question":
				c.log.Info("ACP 收到提问请求", "req_id", string(msg.ID))
				h.OnAskQuestion(append(json.RawMessage(nil), msg.ID...), msg.Params)
				continue
			}
			c.log.Debug("ACP 未处理的对方请求，回 -32601", "method", msg.Method)
			_ = c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID),
				"error": map[string]any{"code": -32601, "message": "unhandled"}})
		case msg.Method != "":
			h.OnNotify(msg.Method, msg.Params)
		case len(msg.ID) > 0:
			var id int
			if err := json.Unmarshal(msg.ID, &id); err != nil {
				c.log.Warn("ACP 响应 id 非数字，跳过", "id", string(msg.ID))
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if !ok {
				c.log.Warn("ACP 响应无对应请求，丢弃", "id", id)
				continue
			}
			if msg.Error != nil {
				ch <- ACPResult{Err: fmt.Errorf("ACP 错误 %d: %s", msg.Error.Code, msg.Error.Message)}
				continue
			}
			ch <- ACPResult{Result: msg.Result}
		default:
			c.log.Debug("ACP 无法归类的消息，跳过")
		}
	}
}
```

- [ ] **Step 5: 补齐测试 script 的权限分支并运行**

在 `TestPermissionRequestCallbackAndDeferredReply` 的 script 里补 `trigger/perm` 分支（返回 Step 2 注释里给出的权限请求报文）。

Run: `go test ./internal/executor/grok/ -run 'TestCallMatches|TestPermissionRequest|TestAskQuestion|TestUnknownAgentRequest|TestOverlappingRequestIDs' -race -v`
Expected: PASS

- [ ] **Step 6: 加连接终止时挂起请求作废的测试**

```go
func TestPendingCallsFailWhenConnectionDies(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string { return nil }) // 永不回应
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	ch, err := cli.CallAsync("session/prompt", nil)
	if err != nil {
		t.Fatalf("CallAsync 失败: %v", err)
	}
	_ = cli.Close()
	select {
	case r := <-ch:
		if r.Err == nil {
			t.Error("连接终止后挂起请求必须以错误终结，不得永久等待")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("挂起请求未被终结——调用方会永久卡住")
	}
	select {
	case <-h.closed:
	case <-time.After(3 * time.Second):
		t.Error("未触发 OnClosed 回调")
	}
}
```

Run: `go test ./internal/executor/grok/ -race -v`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/executor/grok/acp.go internal/executor/grok/acp_test.go
git commit -m "feat(grok): ACP WebSocket 双向 JSON-RPC 客户端

请求/响应按 id 匹配、通知分发、对方请求（权限）经回调上抛且应答可延迟任意久
（spec §5.1 实测悬挂 20min 无超时）。连接终止时挂起请求全部以错误终结，
不留永久等待的调用方。"
```

---

### Task 3: 任务环境物料（`taskenv.go`）

**Files:**
- Create: `internal/executor/grok/taskenv.go`
- Create: `internal/executor/grok/taskenv_test.go`

**Interfaces:**
- Consumes: 无（纯文件生成）
- Produces:
  - `grok.WriteTaskEnv(taskDir, model string) (homeDir string, err error)`
  - `grok.EnsureAuthLink(homeDir string) error`
  - `grok.WriteServeScript(taskDir, homeDir string, port int, secret string, env []string) (scriptPath string, err error)`
  - `grok.protectedEnvKeys`（非导出）：`GROK_HOME` / `GROK_AGENT_SECRET`

- [ ] **Step 1: 写失败测试**

`internal/executor/grok/taskenv_test.go`：

```go
package grok_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestWriteTaskEnvGeneratesPinnedPermissionConfig(t *testing.T) {
	dir := t.TempDir()
	home, err := grok.WriteTaskEnv(dir, "grok-4.5")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("读 config.toml 失败: %v", err)
	}
	cfg := string(b)

	// permission_mode 必须钉死为 default：用户真实配置是 always-approve，
	// 不钉死等于审批门全废（spec §3.3）
	if !strings.Contains(cfg, `permission_mode = "default"`) {
		t.Errorf("config.toml 必须钉死 permission_mode=default，实际:\n%s", cfg)
	}
	if !strings.Contains(cfg, `default = "grok-4.5"`) {
		t.Errorf("config.toml 应写入任务级模型，实际:\n%s", cfg)
	}
	// 危险模式表逐条断言——少一条就是静默放行
	for _, rule := range []string{
		`"Bash(rm *)"`, `"Bash(*sudo*)"`, `"Bash(*git push*)"`,
		`"Bash(*git reset --hard*)"`, `"Bash(*--force*)"`,
		`"Bash(curl *)"`, `"Bash(wget *)"`, `"WebFetch(*)"`,
	} {
		if !strings.Contains(cfg, rule) {
			t.Errorf("ask 规则缺 %s，实际:\n%s", rule, cfg)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("config.toml 权限 = %v，期望 0600", fi.Mode().Perm())
	}
}

func TestWriteTaskEnvOmitsModelSectionWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	home, err := grok.WriteTaskEnv(dir, "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if strings.Contains(string(b), "[models]") {
		t.Errorf("model 为空时不应写 [models] 段（用 grok 自身默认），实际:\n%s", b)
	}
}

func TestEnsureAuthLinkIsIdempotentAndRepairs(t *testing.T) {
	home := t.TempDir()
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("首次建链出错: %v", err)
	}
	link := filepath.Join(home, "auth.json")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("软链未建立: %v", err)
	}
	// 幂等：重复调用不报错
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("重复调用应幂等，出错: %v", err)
	}
	// 修复：软链被删掉后应重建（spec §3.3 实测 token 刷新会干掉它）
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := grok.EnsureAuthLink(home); err != nil {
		t.Fatalf("修复出错: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("软链未被重建: %v", err)
	}
}

func TestWriteServeScriptKeepsSecretOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", nil)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// secret 必须走环境变量，绝不能出现在 grok 的命令行参数里
	// （tmux 客户端 argv 本机全局可读，spec §3.1）
	if !strings.Contains(s, "export GROK_AGENT_SECRET=") {
		t.Errorf("secret 必须经环境变量注入，实际:\n%s", s)
	}
	if strings.Contains(s, "--secret") {
		t.Errorf("secret 绝不能进 argv，实际:\n%s", s)
	}
	if !strings.Contains(s, "export GROK_HOME=") {
		t.Errorf("必须注入任务级 GROK_HOME，实际:\n%s", s)
	}
	if !strings.Contains(s, "--bind 127.0.0.1:24199") {
		t.Errorf("必须绑定回环端口，实际:\n%s", s)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("启动脚本权限 = %v，期望 0600（含 secret）", fi.Mode().Perm())
	}
}

// TestWriteServeScriptInjectsEnvBeforeGrokVars 钉住 B19 的 env 注入契约：
// 注入行必须排在 handoff 自身的 GROK_* 之前，值必须单引号包裹。
// 与 opencode 的 TestServeScriptInjectsEnvBeforeOpencodeVars 同构。
func TestWriteServeScriptInjectsEnvBeforeGrokVars(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HTTPS_PROXY=http://127.0.0.1:7890",
		"LITERAL=$NOT_EXPANDED",
		"WITHSPACE=a b",
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", env)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	proxyIdx := strings.Index(s, "export HTTPS_PROXY='http://127.0.0.1:7890'")
	if proxyIdx < 0 {
		t.Fatalf("脚本缺少注入的 HTTPS_PROXY export 行:\n%s", s)
	}
	// 值必须单引号包裹：Go 侧已展开过一次，不加引号 shell 会再展开一次
	if !strings.Contains(s, "export LITERAL='$NOT_EXPANDED'") {
		t.Errorf("含 $ 的值必须单引号包裹防二次展开:\n%s", s)
	}
	if !strings.Contains(s, "export WITHSPACE='a b'") {
		t.Errorf("含空格的值必须单引号包裹:\n%s", s)
	}
	// 顺序是硬要求：handoff 自身注入的变量必须排在后面才能覆盖 env 文件的同名键
	homeIdx := strings.Index(s, "export GROK_HOME=")
	if homeIdx < 0 || proxyIdx > homeIdx {
		t.Errorf("注入的 env 行必须排在 GROK_HOME 之前（proxy=%d home=%d）:\n%s",
			proxyIdx, homeIdx, s)
	}
}

// TestWriteServeScriptWithoutEnvIsUnchangedInShape 保证 env 为空时脚本形态不变，
// 免得 B19 之前生成的脚本与之后的产生无谓差异。
func TestWriteServeScriptWithoutEnvIsUnchangedInShape(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "grokhome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := grok.WriteServeScript(dir, home, 24199, "s3cr3t", nil)
	if err != nil {
		t.Fatalf("WriteServeScript 出错: %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "\n\nexport GROK_HOME=") {
		t.Errorf("env 为空时不应留下空行:\n%s", b)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestWriteTaskEnv|TestEnsureAuthLink|TestWriteServeScript' -v`
Expected: 编译失败 —— `undefined: grok.WriteTaskEnv` 等

- [ ] **Step 3: 实现 taskenv.go**

```go
// taskenv.go —— grok 任务环境物料生成：任务级 GROK_HOME、权限配置与启动脚本。
//
// 职责：
//   - WriteTaskEnv：建 <taskDir>/grokhome 并写 config.toml（钉死 permission_mode
//     与第 0 层分级规则、注入任务级模型）
//   - EnsureAuthLink：幂等地把 grokhome/auth.json 指向真实 ~/.grok/auth.json
//   - WriteServeScript：生成 tmux 里跑的 serve 启动脚本（secret 走环境变量）
//
// 边界：
//   - 不起进程、不连网络：进程在 proc.go，协议在 acp.go
//   - 不读用户的真实 grok 配置（除 auth.json 软链外一律纯净）
//
// 为什么任务级 GROK_HOME 是必需而非可选：用户真实 ~/.grok/config.toml 常见
// permission_mode = "always-approve"，直接沿用等于所有工具调用自动放行、
// permission 事件永不产生——审批门全废。任务级 home 把它钉死为 "default"。
//
// 为什么权限规则表比 opencode 短：grok 内建按 && / || / ; / 管道分段识别只读
// 命令并自动放行（ls/cat/git status/grep/rg 等），且 `ls && rm -rf /` 会拆开、
// rm 段仍然拦。opencode 那张以 "*": "allow" 收尾的表是手工补的等价物，这里
// 只需补 ask 危险模式与 allow 编辑放行。
//
// 已知泄漏（关不掉）：grok 无视 GROK_HOME，仍从真实 HOME 读 ~/.claude/settings*.json
// 与 ~/.claude/skills。缓解是 grok 的求值为 deny > ask > allow 跨源生效——本文件
// 写的 ask 压得过用户个人 allowlist 的 allow，第 0 层分级仍成立。
package grok

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	homeDirName    = "grokhome"
	configFileName = "config.toml"
	serveScriptName = "run_grok.sh"
	serveLogName    = "serve.log"
	renderLogName   = "render.log"
	serveInfoName   = "serve.json"
)

// askRules 是第 0 层静态分级的危险模式表。
//
// 每次修改本表必须同步 taskenv_test 的逐条断言——少一条就是静默放行。
var askRules = []string{
	"Bash(rm *)",               // 任何直接 rm（误拒成本低、误放成本高）
	"Bash(*sudo*)",             // 提权
	"Bash(*git push*)",         // 外推：收尾纪律要求不 push，出现即异常
	"Bash(*git reset --hard*)", // 丢弃提交
	"Bash(*--force*)",          // 各类强制开关
	"Bash(curl *)",             // 外访直调
	"Bash(wget *)",             // 外访直调
	"WebFetch(*)",              // 外访
}

// allowRules 是放行表：在任务分支上改代码是派发的目的本身，diff 审核兜底。
var allowRules = []string{"Edit", "Write"}

// WriteTaskEnv 建任务级 GROK_HOME 并写入权限配置，返回该 home 目录路径。
//
// 参数：
//   - taskDir: 任务工作目录（须已存在，由调用方保证）
//   - model: 任务级模型；空则不写 [models] 段，用 grok 自身默认
//
// 返回：grokhome 目录路径；建目录或写文件失败时返回错误
//
// 注意：重复调用幂等覆盖，调用方可安全重试
func WriteTaskEnv(taskDir, model string) (homeDir string, err error) {
	log := slog.Default()
	homeDir = filepath.Join(taskDir, homeDirName)
	log.Info("grok 生成任务环境", "task_dir", taskDir, "home", homeDir)
	defer func() {
		if err != nil {
			log.Error("grok 生成任务环境失败", "home", homeDir, "cause", err)
		} else {
			log.Info("grok 任务环境已生成", "home", homeDir, "model", model)
		}
	}()

	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return homeDir, fmt.Errorf("建 grok home %s: %w", homeDir, err)
	}

	var b strings.Builder
	b.WriteString("# 由 handoff agentd 生成的任务级 grok 配置，勿手工编辑。\n\n")
	b.WriteString("[ui]\n")
	b.WriteString("permission_mode = \"default\"\n\n")
	if m := strings.TrimSpace(model); m != "" {
		b.WriteString("[models]\n")
		fmt.Fprintf(&b, "default = %q\n\n", m)
	}
	b.WriteString("[permission]\n")
	b.WriteString("ask = [\n")
	for _, r := range askRules {
		fmt.Fprintf(&b, "  %q,\n", r)
	}
	b.WriteString("]\n")
	b.WriteString("allow = [\n")
	for _, r := range allowRules {
		fmt.Fprintf(&b, "  %q,\n", r)
	}
	b.WriteString("]\n")

	cfgPath := filepath.Join(homeDir, configFileName)
	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return homeDir, fmt.Errorf("写 %s: %w", cfgPath, err)
	}
	return homeDir, nil
}

// EnsureAuthLink 幂等地把 <homeDir>/auth.json 指向真实 ~/.grok/auth.json。
//
// 为什么必须可修复而非一次性建立：spike 实测任务级 home 的软链会在 token 刷新
// 前后消失，随后 session/new 直接返回 Authentication required。Start 与 Resume
// 都调本函数，成本为零。
//
// 为什么用软链而非拷贝：拷贝会让每个任务 home 各自持有凭据并独立刷新，而刷新
// 令牌轮换可能反噬用户本人的登录态——凭据只应有一个权威副本。
func EnsureAuthLink(homeDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("解析用户主目录: %w", err)
	}
	target := filepath.Join(home, ".grok", "auth.json")
	link := filepath.Join(homeDir, "auth.json")

	if cur, err := os.Readlink(link); err == nil && cur == target {
		return nil // 已就位
	}
	// 断链、被替换成普通文件、或根本不存在：一律移除后重建
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理旧 auth 链接 %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("建立 auth 软链 %s -> %s: %w", link, target, err)
	}
	slog.Default().Info("grok auth 软链已就位", "link", link)
	return nil
}

// WriteServeScript 生成 serve 启动脚本，返回脚本路径。
//
// 为什么 secret 走环境变量而非 --secret：tmux 客户端进程的 argv 本机全局可读，
// 这是 opencode 侧 P0-4 划定的安全边界，本 adapter 原样继承。同理不用 tmux -e：
// show-environment 会把它暴露给任何能连上 tmux server 的本机用户。
//
// 为什么这里可以用 exec（与 claude adapter 相反）：grok 有 HTTP 探活面，不需要
// 脚本在进程退出后补写死亡哨兵，因此 sh 可以被替换掉。
//
// why env 行排在 GROK_* 之前且值用单引号（与 opencode 的 writeServeScript 同构，
// B19）：排在前面才能让 handoff 自身注入的变量覆盖 env 文件里的同名键（见
// protectedEnvKeys）；值必须单引号包裹，因为 Go 侧已经展开过一次，不加引号会被
// shell 再展开第二次，含 $ 的值会变成别的东西。
func WriteServeScript(taskDir, homeDir string, port int, secret string, env []string) (string, error) {
	serveLog := filepath.Join(taskDir, serveLogName)
	var envLines strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue // 形如 KEY=VALUE 之外的条目直接跳过，不让它污染脚本语法
		}
		envLines.WriteString("export " + k + "=" + shellQuote(v) + "\n")
	}
	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：grok agent serve 启动脚本（0600，含随机 secret，勿外泄）。
exec 2>> %s
%sexport GROK_HOME=%s
export GROK_AGENT_SECRET=%s
exec grok agent serve --bind 127.0.0.1:%d 2>&1 | tee -a %s
`, shellQuote(serveLog), envLines.String(), shellQuote(homeDir), shellQuote(secret), port, shellQuote(serveLog))

	p := filepath.Join(taskDir, serveScriptName)
	if err := os.WriteFile(p, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("写 serve 启动脚本 %s: %w", p, err)
	}
	slog.Default().Info("grok serve 启动脚本已生成", "path", p, "port", port)
	return p, nil
}
```

> `shellQuote` 在 Task 4 的 `proc.go` 里定义（委托 `internal/shellq`，与 opencode 同源）。本 Task 编译需先补一个最小实现或把 `shellQuote` 一并放进 `proc.go` 后再编译——按 Task 4 Step 3 落地。为让本 Task 独立可测，先在 `taskenv.go` 末尾加：
> ```go
> // shellQuote 把字符串包成单引号 shell 字面量，委托 internal/shellq
> // （与 cmd 包弹终端的 shell 拼接同源，避免复制漂移）。
> func shellQuote(s string) string { return shellq.Quote(s) }
> ```
> 并 import `"github.com/xushixin/handoff/internal/shellq"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/grok/ -run 'TestWriteTaskEnv|TestEnsureAuthLink|TestWriteServeScript' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/executor/grok/taskenv.go internal/executor/grok/taskenv_test.go
git commit -m "feat(grok): 任务级 GROK_HOME 物料与 serve 启动脚本

config.toml 钉死 permission_mode=default（用户真实配置是 always-approve，
不钉死等于审批门全废）并写入第 0 层危险模式表；auth.json 软链幂等可修复
（实测 token 刷新会干掉它）；secret 经 GROK_AGENT_SECRET 注入不进 argv。"
```

---

### Task 4: serve 进程管理（`proc.go`）

**Files:**
- Create: `internal/executor/grok/proc.go`
- Create: `internal/executor/grok/proc_test.go`

**Interfaces:**
- Consumes: `grok.WriteTaskEnv` / `EnsureAuthLink` / `WriteServeScript`（Task 3）
- Produces:
  - `type Proc struct { Session, TaskDir string; Port int; Secret string }`
  - `grok.StartServe(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error)`
  - `(*Proc) Alive() bool`、`(*Proc) Kill() error`、`(*Proc) LogTail() string`、`(*Proc) WSURL() string`
  - `grok.ReadServeInfo(taskDir string) (*Proc, error)`

- [ ] **Step 1: 写失败测试（不依赖真 grok，用假脚本）**

`internal/executor/grok/proc_test.go`：

```go
package grok_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestWSURLCarriesSecretAsServerKey(t *testing.T) {
	p := &grok.Proc{Port: 24199, Secret: "abc"}
	got := p.WSURL()
	want := "ws://127.0.0.1:24199/ws?server-key=abc"
	if got != want {
		t.Errorf("WSURL = %q，期望 %q", got, want)
	}
}

func TestServeInfoRoundTripAndSecretNotInLogTail(t *testing.T) {
	dir := t.TempDir()
	// serve.log 里混入 secret，模拟 grok 启动横幅回显（实测它确实会打印）
	logPath := filepath.Join(dir, "serve.log")
	if err := os.WriteFile(logPath, []byte("Secret:   s3cr3t\npanic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &grok.Proc{Session: "handoff-abcd1234", TaskDir: dir, Port: 24199, Secret: "s3cr3t"}
	if err := grok.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("写 serve.json 失败: %v", err)
	}
	got, err := grok.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("读 serve.json 失败: %v", err)
	}
	if got.Port != 24199 || got.Secret != "s3cr3t" || got.Session != "handoff-abcd1234" {
		t.Errorf("往返不一致: %+v", got)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "serve.json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("serve.json 权限 = %v，期望 0600（含 secret）", fi.Mode().Perm())
	}

	// LogTail 必须脱敏：它会进 FailReason 落事件库，也进 agentd.log
	tail := got.LogTail()
	if strings.Contains(tail, "s3cr3t") {
		t.Errorf("LogTail 必须脱敏 secret，实际: %q", tail)
	}
	if !strings.Contains(tail, "panic: boom") {
		t.Errorf("LogTail 应保留诊断内容，实际: %q", tail)
	}
}
```

新建导出测试缝 `internal/executor/grok/export_test.go`（注意包名是 `grok` 而非 `grok_test`）：

```go
package grok

// WriteServeInfoForTest 暴露 serve.json 写入，供 grok_test 包做往返断言。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestWSURL|TestServeInfo' -v`
Expected: 编译失败 —— `undefined: grok.Proc` 等

- [ ] **Step 3: 实现 proc.go**

```go
// proc.go —— grok agent serve 的进程生命周期：tmux 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、生成随机 secret、写物料与启动脚本、tmux 起 serve、
//     开 render tail 窗口、HTTP 探活等就绪、落 serve.json
//   - Alive/Kill/LogTail：存活探测、回收、脱敏后的诊断尾部
//   - ReadServeInfo：从 serve.json 重建 Proc，供 agentd 重启后 Resume
//
// 边界：
//   - 不说 ACP、不解析事件：协议在 acp.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么存活判据是 HTTP 端口探活而不是 tmux has-session：会话里第二个窗口的
// tail -f 会一直活着，serve 早死了会话依然存在。grok serve 的根路径返回 404
// ——能收到任何 HTTP 响应就说明进程还在监听。
package grok

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveReadyTimeout  = 15 * time.Second // 大于 opencode 的 10s：grok 冷启动要加载配置与索引
	serveProbeInterval = 200 * time.Millisecond
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500
)

// Proc 是一个 grok serve 实例的句柄与恢复凭据。
//
// 注意：Secret 字段是明文 secret，序列化后的 serve.json 必须 0600，
// 且任何日志/错误文本输出前都要经 LogTail 之类的脱敏路径。
type Proc struct {
	Session string `json:"session"`  // tmux 会话名 handoff-<id8>
	TaskDir string `json:"task_dir"` // 任务目录
	Port    int    `json:"port"`
	Secret  string `json:"secret"`
}

// WSURL 返回 ACP 的 WebSocket 端点。
//
// 注意：返回值含 secret，**绝不可整体写进日志**。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d/ws?server-key=%s", p.Port, p.Secret)
}

// Alive 探测 serve 是否仍在监听（收到任何 HTTP 响应即算活，含 404）。
func (p *Proc) Alive() bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", p.Port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// Kill 杀掉 tmux 会话回收 serve；会话已不存在视为已清理，不报错。
func (p *Proc) Kill() error {
	out, err := exec.Command("tmux", "kill-session", "-t", p.Session).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "can't find session") ||
			strings.Contains(string(out), "no server running") {
			slog.Default().Info("grok tmux 会话已不存在，视为已清理", "session", p.Session)
			return nil
		}
		return fmt.Errorf("kill tmux 会话 %s: %w (%s)", p.Session, err, strings.TrimSpace(string(out)))
	}
	slog.Default().Info("grok tmux 会话已回收", "session", p.Session)
	return nil
}

// LogTail 返回脱敏后的 serve.log 尾部，供启动超时与死亡诊断。
//
// 为什么必须脱敏：这段尾部会进 Result.FailReason（落事件库）与 agentd.log，
// 而它的内容完全由 grok 决定——实测启动横幅就会原样打印 Secret 一行。
func (p *Proc) LogTail() string {
	b, err := os.ReadFile(filepath.Join(p.TaskDir, serveLogName))
	if err != nil {
		return ""
	}
	if len(b) > serveLogTailBytes {
		b = b[len(b)-serveLogTailBytes:]
	}
	tail := turn.TailRunes(string(b), serveLogTailRunes)
	if p.Secret == "" {
		return tail
	}
	return strings.ReplaceAll(tail, p.Secret, "***")
}

// protectedEnvKeys 是 handoff 自身注入、不容 env 文件覆盖的变量（B19）。
//
// 命中时不静默忽略用户写的行——注入顺序保证 handoff 的 export 排在后面因而胜出，
// 同时打 WARN 让用户知道自己那行没生效。
//
// 为什么 GROK_AGENT_SECRET 在列：它被 env 文件覆盖会让 adapter 拿着旧 secret 连
// 不上自己起的 serve；GROK_HOME 被覆盖则整个任务级权限隔离（spec §3.3）失效——
// 那是审批门存在的前提，必须由 handoff 独占。
var protectedEnvKeys = map[string]bool{
	"GROK_HOME":         true,
	"GROK_AGENT_SECRET": true,
}

// StartServe 起一个任务专属的 grok serve 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（tmux 会话的 cwd）
//   - taskID: 任务 ID（取前 8 字符作会话名后缀）
//   - taskDir: 任务物料目录
//   - model: 任务级模型（空=用 grok 默认）
//   - env: 注入到 serve 进程的环境变量（形如 KEY=VALUE，已由 manager 解析展开）；
//     命中 protectedEnvKeys 的条目会被 handoff 自身注入覆盖并打 WARN
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带脱敏后的 serve.log 尾部）
func StartServe(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	log.Info("grok serve 启动中", "task", taskID, "repo", repoPath, "task_dir", taskDir)

	homeDir, err := WriteTaskEnv(taskDir, model)
	if err != nil {
		return nil, err
	}
	if err := EnsureAuthLink(homeDir); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	secret, err := randomSecret()
	if err != nil {
		return nil, err
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）。
	// 与 opencode 的 StartServe 同构。
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			keys = append(keys, k)
			if protectedEnvKeys[k] {
				log.Warn("env 文件定义了 handoff 保留变量，将被 handoff 自身注入覆盖",
					"key", k, "task", taskID)
			}
		}
		log.Info("注入 env 变量到 grok serve 进程", "task", taskID, "keys", keys, "count", len(keys))
	}

	scriptPath, err := WriteServeScript(taskDir, homeDir, port, secret, env)
	if err != nil {
		return nil, err
	}

	p := &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir, Port: port, Secret: secret}
	args := []string{"new-session", "-d", "-s", p.Session, "-c", repoPath,
		"sh " + shellq.Quote(scriptPath)}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		log.Error("grok tmux 启动失败", "task", taskID, "cause", err, "out", strings.TrimSpace(string(out)))
		return nil, fmt.Errorf("tmux 启动 grok serve: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	startRenderTailWindow(p.Session, taskDir, log)

	// 探活等就绪
	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.Alive() {
			if err := writeServeInfo(p); err != nil {
				log.Warn("写 serve.json 失败，Resume 将不可用", "task", taskID, "cause", err)
			}
			log.Info("grok serve 就绪", "task", taskID, "port", port,
				"elapsed_ms", time.Since(start).Milliseconds())
			return p, nil
		}
		select {
		case <-ctx.Done():
			_ = p.Kill()
			return nil, ctx.Err()
		case <-time.After(serveProbeInterval):
		}
	}
	tail := p.LogTail()
	_ = p.Kill() // 清理残留，不留孤儿 serve
	log.Error("grok serve 就绪超时", "task", taskID, "timeout", serveReadyTimeout, "log_tail", tail)
	return nil, fmt.Errorf("grok serve %s 内未就绪: %s", serveReadyTimeout, tail)
}

// startRenderTailWindow 在会话内开第二窗口 tail -f render.log（模型回合文本实况）。
//
// 稳健做法：先 touch render.log 再开窗口——tail -f 对不存在的文件会立即报错退出。
// 窗口启动失败只 Warn 不阻断：这是增强型可见性，不值得为它挂掉任务启动。
func startRenderTailWindow(session, taskDir string, log *slog.Logger) {
	p := filepath.Join(taskDir, renderLogName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("创建 render.log 失败，tmux 第二窗口不可用", "session", session, "cause", err)
		return
	}
	f.Close()
	if err := exec.Command("tmux", "new-window", "-t", session,
		"tail -f "+shellq.Quote(p)).Run(); err != nil {
		log.Warn("tmux 第二窗口启动失败（tail render.log 不可用），不影响主流程",
			"session", session, "cause", err)
	}
}

// writeServeInfo 落恢复凭据（0600：含 secret）。
func writeServeInfo(p *Proc) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 serve.json: %w", err)
	}
	path := filepath.Join(p.TaskDir, serveInfoName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume。
func ReadServeInfo(taskDir string) (*Proc, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, serveInfoName))
	if err != nil {
		return nil, fmt.Errorf("读 serve.json: %w", err)
	}
	var p Proc
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("解析 serve.json: %w", err)
	}
	p.TaskDir = taskDir // 目录可能被整体搬动，以实参为准
	return &p, nil
}

// freePort 让内核分配一个空闲回环端口。
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配空闲端口: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// randomSecret 生成 32 字符十六进制随机 secret。
func randomSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 serve secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// id8 取字符串前 8 字符作 tmux 会话名后缀（与 opencode 同规则，attach 零改动）。
func id8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
```

> 实现时把 `taskenv.go` 里临时定义的 `shellQuote` 删除、改为直接用 `shellq.Quote`（`proc.go` 已 import 它）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/grok/ -run 'TestWSURL|TestServeInfo' -v`
Expected: PASS

- [ ] **Step 5: 全包构建与 vet**

Run: `go build ./... && go vet ./internal/executor/grok/`
Expected: 无输出

- [ ] **Step 6: 提交**

```bash
git add internal/executor/grok/proc.go internal/executor/grok/proc_test.go internal/executor/grok/export_test.go
git commit -m "feat(grok): serve 进程 tmux 托管、HTTP 探活与恢复凭据

存活判据用端口探活而非 tmux has-session（第二窗口的 tail -f 会撑住会话，
serve 死了会话仍在）。LogTail 脱敏 secret——grok 启动横幅实测会原样打印它，
而这段尾部会进 FailReason 落事件库。"
```

---

### Task 5: Adapter 五动作骨架与事件映射（`adapter.go`）

**Files:**
- Create: `internal/executor/grok/adapter.go`
- Create: `internal/executor/grok/adapter_test.go`
- Create: `internal/executor/grok/testdata/updates.jsonl`（spike 采到的真实报文）

**Interfaces:**
- Consumes: `ACPClient`（Task 2）、`Proc`（Task 4）、`turn.*`（Task 1）、`executor.Adapter`（既有契约）
- Produces:
  - `grok.New(log *slog.Logger) *Adapter`
  - `(*Adapter) Start(ctx, executor.StartReq) error`
  - `(*Adapter) Events(taskID string) <-chan executor.AdapterEvent`
  - `(*Adapter) Send(ctx, taskID, text string) error`
  - `(*Adapter) Stop(taskID string) error`

- [ ] **Step 1: 落 testdata（把 spike 真实报文固化为回归基线）**

`internal/executor/grok/testdata/updates.jsonl` —— 每行一条真实 ACP 消息（来自本机 grok 1.0.0 spike）：

```
{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"思考中"}}}}
{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"我要改 foo.go"}}}}
{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"call-1-0","title":"run_terminal_command","rawInput":{"command":"echo hi","description":"say hi"}}}}
{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1-0","status":"completed","title":"Execute `echo hi`"}}}
{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"\n{\"ask\":\"用哪个库？\"}"}}}}
{"jsonrpc":"2.0","method":"_x.ai/queue/changed","params":{"sessionId":"s","entries":[]}}
```

- [ ] **Step 2: 写失败测试——事件映射分流**

`internal/executor/grok/adapter_test.go`：

```go
package grok_test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// TestMapUpdateRoutesByKind 验证四类 session/update 的分流：
// 正文进回合文本、thought 与 tool_call 只进 render.log、私有通知忽略。
func TestMapUpdateRoutesByKind(t *testing.T) {
	f, err := os.Open("testdata/updates.jsonl")
	if err != nil {
		t.Fatalf("读 testdata 失败: %v", err)
	}
	defer f.Close()

	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h.FeedRawForTest([]byte(line))
	}

	// 回合正文只应含 agent_message_chunk 的内容
	body := h.TurnTextForTest()
	if !strings.Contains(body, "我要改 foo.go") {
		t.Errorf("回合正文缺 agent_message_chunk 内容: %q", body)
	}
	if strings.Contains(body, "思考中") {
		t.Errorf("推理流不得进回合正文（会污染 trailer 解析）: %q", body)
	}
	if strings.Contains(body, "run_terminal_command") {
		t.Errorf("工具调用不得进回合正文: %q", body)
	}

	// render.log 应同时含正文、推理与工具动作
	render := h.RenderTextForTest()
	for _, want := range []string{"我要改 foo.go", "思考中", "echo hi"} {
		if !strings.Contains(render, want) {
			t.Errorf("render.log 缺 %q，实际: %q", want, render)
		}
	}
}

// TestTurnTextEndsWithTrailerSoParseWorks 验证累积后的正文能被 turn.ParseTrailer 判为 ask。
func TestTurnTextEndsWithTrailerSoParseWorks(t *testing.T) {
	f, _ := os.Open("testdata/updates.jsonl")
	defer f.Close()
	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.FeedRawForTest([]byte(line))
		}
	}
	kind, tr := h.ClassifyForTest()
	if kind != "ask" {
		t.Fatalf("分类 = %q，期望 ask（正文以 {\"ask\":...} 收尾）", kind)
	}
	if tr.Question != "用哪个库？" {
		t.Errorf("Question = %q", tr.Question)
	}
}
```

在 `export_test.go` 追加测试缝：

```go
// NewTurnAccumulatorForTest 暴露回合累积器，供事件映射的纯逻辑断言
// （不起进程、不连网络）。
func NewTurnAccumulatorForTest() *turnAccumulator { return newTurnAccumulator() }

func (t *turnAccumulator) FeedRawForTest(raw []byte)  { t.feedRaw(raw) }
func (t *turnAccumulator) TurnTextForTest() string    { return t.turnText() }
func (t *turnAccumulator) RenderTextForTest() string  { return t.renderBuf.String() }
func (t *turnAccumulator) ClassifyForTest() (string, turn.Trailer) {
	return turn.ParseTrailer(t.turnText())
}
```

（`export_test.go` 需 import `"github.com/xushixin/handoff/internal/executor/turn"`。）

- [ ] **Step 2b: 写失败测试——提问文本渲染（内部测试）**

`askQuestionText` 是未导出函数，测试放**内部**测试文件
`internal/executor/grok/askquestion_internal_test.go`：

```go
package grok

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAskQuestionTextRendersQuestionsAndOptions(t *testing.T) {
	// 来自本机 grok 1.0.0 实测报文（spec §4.2.3）
	params := json.RawMessage(`{"sessionId":"s","toolCallId":"c9",` +
		`"questions":[{"question":"这个功能用哪种语言实现？","options":[` +
		`{"label":"Go","description":"用 Go 实现该功能"},` +
		`{"label":"Rust","description":"用 Rust 实现该功能"}],"multiSelect":null}],"mode":"default"}`)

	got := askQuestionText(params)
	for _, want := range []string{"这个功能用哪种语言实现？", "1) Go", "2) Rust", "用 Rust 实现该功能"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染文本缺少 %q，实得:\n%s", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("尾部换行未清理: %q", got)
	}
}

func TestAskQuestionTextEmptyOnGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"非 JSON":   `not json`,
		"缺 questions": `{"sessionId":"s"}`,
		"空 questions": `{"questions":[]}`,
	} {
		if got := askQuestionText(json.RawMessage(in)); got != "" {
			t.Errorf("%s: 应返回空串，实得 %q", name, got)
		}
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestMapUpdate|TestTurnText|TestAskQuestionText' -v`
Expected: 编译失败 —— `undefined: newTurnAccumulator`、`undefined: askQuestionText`

- [ ] **Step 4: 实现 adapter.go 的累积器与事件映射**

在 `adapter.go` 里实现（文件头注释见 Step 6）：

```go
// turnAccumulator 是单回合的文本累积器：把 session/update 分流成
// 「回合正文」与「render.log 可见性文本」两股。
//
// 为什么要分两股：ParseTrailer 取最后一个 { 开头的行，推理流里模型复述协议
// 样例会污染判定；但推理与工具动作对旁观者有价值，故只进 render.log。
type turnAccumulator struct {
	bodyBuf   strings.Builder // 回合正文（仅 agent_message_chunk）
	renderBuf strings.Builder // 可见性文本（正文 + 推理 + 工具动作）
}

func newTurnAccumulator() *turnAccumulator { return &turnAccumulator{} }

func (t *turnAccumulator) turnText() string { return t.bodyBuf.String() }

func (t *turnAccumulator) reset() {
	t.bodyBuf.Reset()
	t.renderBuf.Reset()
}

// takeRender 取走并清空待落盘的可见性增量。
func (t *turnAccumulator) takeRender() string {
	s := t.renderBuf.String()
	t.renderBuf.Reset()
	return s
}

// feedRaw 消费一条原始 ACP 消息，按 sessionUpdate 类型分流。
//
// 宽容语义：无法解析或未知类型一律跳过，绝不 panic——executor 侧输出不可信。
func (t *turnAccumulator) feedRaw(raw []byte) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Update struct {
				Kind     string `json:"sessionUpdate"`
				Content  struct {
					Text string `json:"text"`
				} `json:"content"`
				Title    string          `json:"title"`
				Status   string          `json:"status"`
				RawInput json.RawMessage `json:"rawInput"`
			} `json:"update"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if msg.Method != "session/update" {
		return // _x.ai/* 等私有通知一概忽略
	}
	u := msg.Params.Update
	switch u.Kind {
	case "agent_message_chunk":
		t.bodyBuf.WriteString(u.Content.Text)
		t.renderBuf.WriteString(u.Content.Text)
	case "agent_thought_chunk":
		t.renderBuf.WriteString(u.Content.Text)
	case "tool_call":
		t.renderBuf.WriteString("\n▸ " + toolLine(u.Title, u.RawInput) + "\n")
	case "tool_call_update":
		if u.Status != "" {
			t.renderBuf.WriteString("  └ " + u.Status + "\n")
		}
	}
}

// toolLine 把工具调用渲染成一行人类可读摘要：优先用 rawInput.command，
// 否则退回 title。
func toolLine(title string, rawInput json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	if len(rawInput) > 0 && json.Unmarshal(rawInput, &in) == nil && in.Command != "" {
		return turn.TruncateMarked(in.Command, 200)
	}
	return title
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/executor/grok/ -run 'TestMapUpdate|TestTurnText|TestAskQuestionText' -v`
Expected: PASS

- [ ] **Step 6: 实现 Adapter 主体（Start/Events/Send/Stop）与文件头注释**

`adapter.go` 顶部：

```go
// adapter.go —— ACP 语义到 executor.Adapter 契约的翻译层。
//
// 职责：
//   - 把 StartServe / DialACP / initialize / session.new / session.prompt 编排成
//     Adapter 的五动作
//   - ACP 消息 → AdapterEvent 映射：session/request_permission → permission 事件；
//     agent_message_chunk 累积成回合正文（thought 与 tool_call 只进 render.log）；
//     session/prompt 的响应（stopReason）作回合边界 → turn.ParseTrailer 分类
//   - 可见性：回合文本增量追加到 <taskDir>/render.log，供 tmux 第二窗口旁观
//
// 边界：
//   - 不写 store、不做审批判断（见 executor.go 包级边界）：会话 id 等持久化诉求
//     经事件（progress「会话就绪」/ Result.SessionID）交 manager 落库
//   - 不做任务状态机迁移：6 状态迁移完全由 manager 负责
//
// 与 opencode adapter 的两处结构性差异：
//   - 回合边界是 session/prompt 的**响应**而非从 idle 事件推断，因此不需要
//     opencode 的 idleGrace 去抖与 scheduleIdle/resolveIdle/cancelPendingIdle 竞态处理
//   - 权限是阻塞式 JSON-RPC 请求，需维护 permID → 请求 id 的挂起表（见 perm.go）
package grok

const (
	progressThrottle = 30 * time.Second // 与 opencode 同值：防高频增量刷爆事件库
	permTextLimit    = 200              // 交给审核者的权限描述上限
)

// Adapter 是 grok 的 executor.Adapter 实现。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态只被该任务自己的回调路径访问。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// New 创建 grok adapter。
//
// 参数：
//   - log: 本模块日志入口（nil 时退回 slog.Default()）
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{log: log, runs: make(map[string]*runState)}
}
```

`runState` 与五动作（关键结构，实现者按此落地）：

```go
// runState 是单任务运行的完整状态。
type runState struct {
	taskID      string
	taskDir     string
	repoPath    string
	sessionID   string
	startCommit string

	proc *Proc
	cli  *ACPClient

	evCh     chan executor.AdapterEvent
	emitMu   sync.Mutex
	evClosed bool

	turnMu       sync.Mutex
	acc          *turnAccumulator
	lastProgress time.Time
	rejected     []string // 本回合被拒的权限描述

	pendMu  sync.Mutex
	pending map[string]json.RawMessage // toolCallId -> ACP 请求 id
}

// Start 异步启动执行并立即返回。
//
// 步骤：物料与 serve（StartServe）→ ACP 连接 → initialize → session/new →
// 不等待地发 session/prompt → emit progress{SessionID}「会话就绪」。
//
// 注意：session/prompt 要跑完一整个回合才响应，因此用 CallAsync 并由独立
// goroutine 等待其终局作回合边界。
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) error

// Events 返回该任务的事件流通道（Start 后可用）。通道关闭表示执行终结。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent

// Send 回答提问 / 回发修改指令，对同一会话续接执行。text 原样透传不加工。
func (a *Adapter) Send(ctx context.Context, taskID, text string) error

// Stop 终止执行并回收资源：session/cancel → 关 ACP → kill tmux → 关事件通道。
func (a *Adapter) Stop(taskID string) error
```

运行态基础设施（**其余各处都在调它们，必须先落地**）：

```go
// lookup 取任务运行态；不存在返回 nil。
func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

// drop 注销任务运行态。
func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

// emit 向事件通道投递一个事件；通道已关闭时静默丢弃并返回 false。
//
// 为什么要 emitMu + evClosed 而不是裸 send：事件可能来自读循环、看门狗、
// 回合终局三个 goroutine，而关闭权只有一处——没有这把锁会 send on closed channel。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		a.log.Debug("事件通道已关闭，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
	select {
	case r.evCh <- ev:
		return true
	default:
		a.log.Warn("事件通道满，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
}

// emitFailed 产出失败终局并关闭事件通道。
//
// 一次性语义：断开处置、看门狗判死、回合异常三条路径都可能同时到达，
// closeEvents 保证只有先到者生效，后到者被丢弃，不会双重终结。
func (a *Adapter) emitFailed(r *runState, reason string) {
	a.log.Error("grok 任务失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
		Result: &executor.Result{OK: false, SessionID: r.sessionID, FailReason: reason}})
	r.closeEvents()
}

// closeEvents 关闭事件通道（幂等）。
func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}

// turnTextAndReset 取走本回合正文并清空累积器，为下一回合做准备。
func (r *runState) turnTextAndReset() string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	s := r.acc.turnText()
	r.acc.reset()
	return s
}

// flushRender 把累积的可见性增量落进 render.log，并按节流产出 progress。
//
// 失败只 Warn 不中断：可见性是增强能力，不值得为它挂掉回合。
func (a *Adapter) flushRender(r *runState) {
	r.turnMu.Lock()
	delta := r.acc.takeRender()
	due := time.Since(r.lastProgress) >= progressThrottle
	if due {
		r.lastProgress = time.Now()
	}
	r.turnMu.Unlock()

	if delta == "" {
		return
	}
	if err := turn.AppendRender(filepath.Join(r.taskDir, renderLogName), delta); err != nil {
		a.log.Warn("追加 render.log 失败，不影响回合", "task", r.taskID, "cause", err)
	}
	if due {
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.sessionID,
			Text: turn.TruncateMarked(strings.TrimSpace(delta), 500)})
	}
}

// firstNonEmpty 返回第一个非空串（trailer 值优先于 git 实测值）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
```

`Start` 的完整实现（顺序即 spec §4.1，每步失败都要清理已建资源，不留孤儿 serve）：

```go
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	taskID := req.Task.ID
	start := time.Now()
	a.log.Info("grok 启动任务", "task", taskID, "repo", req.Task.RepoPath,
		"task_dir", req.TaskDir, "model", req.Task.Model)
	defer func() {
		if err != nil {
			a.log.Error("grok 启动任务失败", "task", taskID, "cause", err)
		}
	}()

	proc, err := StartServe(ctx, req.Task.RepoPath, taskID, req.TaskDir, req.Task.Model, req.Env, a.log)
	if err != nil {
		return err
	}
	// 之后任一步失败都要回收 serve，否则留下一个没人管的 tmux 会话
	defer func() {
		if err != nil {
			_ = proc.Kill()
		}
	}()

	r := &runState{
		taskID: taskID, taskDir: req.TaskDir, repoPath: req.Task.RepoPath,
		proc: proc, evCh: make(chan executor.AdapterEvent, 64),
		acc: newTurnAccumulator(), pending: map[string]json.RawMessage{},
	}
	// 回合起点 commit：兜底分类要靠「是否有新提交」这个事实裁决
	if _, c, _, gerr := turn.GitTurnStatus(req.Task.RepoPath, ""); gerr == nil {
		r.startCommit = c
	} else {
		a.log.Warn("读取回合起点 commit 失败，兜底裁决将退化", "task", taskID, "cause", gerr)
	}

	cli, err := DialACP(ctx, proc.WSURL(), &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		return err
	}
	r.cli = cli
	defer func() {
		if err != nil {
			_ = cli.Close()
		}
	}()

	if _, err = cli.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal": false,
		},
	}); err != nil {
		return fmt.Errorf("ACP initialize: %w", err)
	}

	newRes, err := cli.Call(ctx, "session/new", map[string]any{
		"cwd": req.Task.RepoPath, "mcpServers": []any{},
	})
	if err != nil {
		// 凭据问题重试一万次也不会好，给出可操作的指引（spec §8）
		if strings.Contains(err.Error(), "Authentication required") {
			return fmt.Errorf("grok 未登录或凭据已失效，请在本机执行 `grok login` 后重试: %w", err)
		}
		return fmt.Errorf("ACP session/new: %w", err)
	}
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	if err = json.Unmarshal(newRes, &sess); err != nil || sess.SessionID == "" {
		return fmt.Errorf("ACP session/new 未返回 sessionId: %s", newRes)
	}
	r.sessionID = sess.SessionID

	prompt, err := turn.RenderPrompt(taskID, req.PlanContent)
	if err != nil {
		return err
	}
	// 不等待：session/prompt 要跑完一整个回合才响应，Start 必须立即返回
	resCh, err := cli.CallAsync("session/prompt", map[string]any{
		"sessionId": r.sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": prompt}},
	})
	if err != nil {
		return fmt.Errorf("ACP session/prompt: %w", err)
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()

	go a.awaitTurn(r, resCh)
	go a.watchdog(r)

	// 「会话就绪」信号：审核主路径常以 question 收尾、result 永不出现，
	// progress 是会话 id 到达 manager 的可靠通道
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.sessionID,
		Text: "grok 会话已就绪"})
	a.log.Info("grok 任务已启动", "task", taskID, "session", r.sessionID,
		"port", proc.Port, "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// awaitTurn 等一个回合的终局并交 finishTurn 分类；同时把最后的可见性增量落盘。
func (a *Adapter) awaitTurn(r *runState, ch <-chan ACPResult) {
	res := <-ch
	a.flushRender(r)
	a.finishTurn(r, res)
}

// acpHandler 把 ACP 回调接到 adapter 上。
//
// 纪律：回调运行在 ACP 读循环 goroutine 上，**不得阻塞**——所有耗时动作
// （落盘、git）要么很快，要么另起 goroutine，否则会卡住整条连接的消息消费。
type acpHandler struct {
	a *Adapter
	r *runState
}

func (h *acpHandler) OnNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	h.r.turnMu.Lock()
	h.r.acc.feedRaw(append([]byte(`{"method":"session/update","params":`), append(params, '}')...))
	h.r.turnMu.Unlock()
	h.a.flushRender(h.r)
}

func (h *acpHandler) OnPermission(reqID, params json.RawMessage) {
	var p struct {
		ToolCall struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ToolCall.ToolCallID == "" {
		h.a.log.Error("权限请求解析失败，按拒绝处理（fail-closed）",
			"task", h.r.taskID, "cause", err)
		_ = h.r.cli.Reply(reqID, map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": "reject-once"}})
		return
	}
	h.r.notePending(p.ToolCall.ToolCallID, reqID)
	text := turn.TruncateMarked(p.ToolCall.Title+" | "+toolLine("", p.ToolCall.RawInput), permTextLimit)
	h.a.log.Info("grok 权限门触发", "task", h.r.taskID, "perm", p.ToolCall.ToolCallID)
	h.a.emit(h.r, executor.AdapterEvent{Type: "permission",
		PermissionID: p.ToolCall.ToolCallID, SessionID: h.r.sessionID, Text: text})
}

// OnAskQuestion 处置 grok 的交互提问（spec §4.2.3）。
//
// 纪律：**先解阻塞再做别的**。不应答会让 session/prompt 永不返回、serve 进程健在、
// 看门狗探活通过——任务表面在跑实则永久静止，是最坏的一种故障形态（§5.3(c) 实测）。
// 因此即使参数解析失败也必须回包。
//
// 回 `{}` 而非把审核者答复灌回去：handoff 的提问通道是回合协议的 trailer `{"ask":…}`，
// 只能有一个真相源；grok 收到 `{}` 记为「用户拒答」并带着这个事实继续走到回合结束。
func (h *acpHandler) OnAskQuestion(reqID, params json.RawMessage) {
	// 先解阻塞：任何解析失败都不能挡住这一步
	if err := h.r.cli.Reply(reqID, map[string]any{}); err != nil {
		h.a.log.Error("提问请求应答失败，该回合可能已挂死",
			"task", h.r.taskID, "cause", err)
	}

	text := askQuestionText(params)
	if text == "" {
		h.a.log.Warn("提问请求解析不出内容，已解阻塞但无法上报审核者",
			"task", h.r.taskID)
		return
	}
	h.a.log.Info("grok 走了交互提问工具（绕开回合协议），已转交审核者",
		"task", h.r.taskID)
	if err := turn.AppendRender(filepath.Join(h.r.taskDir, renderLogName),
		"\n【模型提问】"+text+"\n"); err != nil {
		h.a.log.Warn("提问文本写 render.log 失败，不影响上报", "task", h.r.taskID, "cause", err)
	}
	h.a.emit(h.r, executor.AdapterEvent{Type: "question",
		SessionID: h.r.sessionID, Text: turn.ClampQuestion(text)})
}

// askQuestionText 把 _x.ai/ask_user_question 的 params 渲染成人读的一段文本。
// 解析失败返回空串（调用方据此跳过上报，但阻塞已在调用方解除）。
func askQuestionText(params json.RawMessage) string {
	var p struct {
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return ""
	}
	var b strings.Builder
	for _, q := range p.Questions {
		b.WriteString(q.Question)
		b.WriteString("\n")
		for i, o := range q.Options {
			b.WriteString(fmt.Sprintf("  %d) %s", i+1, o.Label))
			if o.Description != "" {
				b.WriteString(" —— " + o.Description)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (h *acpHandler) OnClosed(err error) { h.a.onClosed(h.r, err) }
```

> `onClosed` 与 `watchdog` 分别在 Task 6、Task 7 落地；本 Task 先留可编译的最小实现
> （`onClosed` 直接 `emitFailed`、`watchdog` 空转），Task 6/7 再替换为完整版本。

回合收尾（`session/prompt` 终局到达时）按 spec §4.2.1 落地：

```go
// finishTurn 处理一个回合的终局：按 stopReason 与 trailer 分类产出事件。
//
// 为什么 stopReason != end_turn 一律判失败：那意味着回合没跑完（拒答、达到
// 上限、被取消），此时模型的产出不可信，交审核者比替它猜测安全。
func (a *Adapter) finishTurn(r *runState, res ACPResult) {
	if res.Err != nil {
		a.emitFailed(r, fmt.Sprintf("回合异常终止: %v", res.Err))
		return
	}
	var out struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(res.Result, &out)
	if out.StopReason != "end_turn" {
		a.emitFailed(r, "回合非正常收尾 stopReason="+out.StopReason)
		return
	}

	// 本回合有被拒权限时优先交代：模型被拒后可能悄悄绕路，人不知情
	if rej := r.takeRejected(); len(rej) > 0 {
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion(rejectedTurnQuestion(rej))})
		return
	}

	text := r.turnTextAndReset()
	kind, tr := turn.ParseTrailer(text)
	branch, commit, hasNew, gerr := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if gerr != nil {
		a.log.Warn("git 回合取证失败，降级只用 trailer", "task", r.taskID, "cause", gerr)
	}
	a.log.Info("grok 回合收尾", "task", r.taskID, "kind", kind,
		"has_new_commit", hasNew, "branch", branch)

	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
			Result: &executor.Result{OK: true, Branch: firstNonEmpty(tr.Branch, branch),
				CommitHash: firstNonEmpty(tr.Commit, commit), SessionID: r.sessionID,
				Summary: tr.Summary}})
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——有新提交才可能是
		// 「干完了」，没有就把整段回合文本当提问交审核者，绝不替模型宣布完成。
		if hasNew {
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.sessionID,
				Result: &executor.Result{OK: true, Branch: branch, CommitHash: commit,
					SessionID: r.sessionID, Summary: "（模型未输出收尾协议，按 git 新提交判定完成）"}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
	}
}
```

- [ ] **Step 6b: 加 auth 失败的可操作错误测试（spec §8）**

`adapter_test.go` 追加：

```go
// TestSessionNewAuthErrorGivesActionableMessage 固定 spec §8：凭据问题重试无用，
// 必须给出「跑 grok login」的可操作指引，而不是一个裸的 ACP 错误码。
func TestSessionNewAuthErrorGivesActionableMessage(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`}
		case "session/new":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) +
				`,"error":{"code":-32000,"message":"Authentication required","data":"no auth method id provided"}}`}
		}
		return nil
	})
	err := grok.StartSessionForTest(wsURL(srv), t.TempDir())
	if err == nil {
		t.Fatal("auth 失败必须返回错误")
	}
	if !strings.Contains(err.Error(), "grok login") {
		t.Errorf("错误必须可操作（提示跑 grok login），实际: %v", err)
	}
}
```

`export_test.go` 追加对应测试缝：

```go
// StartSessionForTest 只跑 Start 里「连接 → initialize → session/new」这一段，
// 不起 serve 进程，供 auth 错误路径断言。
func StartSessionForTest(wsURL, repoPath string) error {
	a := New(nil)
	r := &runState{taskID: "t", repoPath: repoPath, pending: map[string]json.RawMessage{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := DialACP(ctx, wsURL, &acpHandler{a: a, r: r}, nil)
	if err != nil {
		return err
	}
	defer cli.Close()
	r.cli = cli
	return a.openSession(ctx, r, repoPath)
}
```

实现时把 `Start` 里 `initialize` + `session/new` 那两段抽成 `openSession(ctx, r, repoPath) error`（含
`Authentication required` → `grok login` 的翻译），`Start` 改调它——这样测试能不起进程地打到该路径。

Run: `go test ./internal/executor/grok/ -run TestSessionNewAuthError -v`
Expected: 先 FAIL（`undefined: StartSessionForTest`），实现后 PASS

- [ ] **Step 7: 加日志覆盖检查（instrumenting-code 清单）**

逐项确认并补齐：
- `Start` 进入（task/repo/taskDir）与退出（成功带 sessionID + 耗时，失败带 cause）
- 外部调用前后：`StartServe`、`DialACP`、`initialize`、`session/new`、`session/prompt` 各一条
- 每个错误分支带上下文与 cause
- 状态变更：回合收尾（kind / hasNew / branch）、会话就绪
- 高频路径（文本增量）用 `Debug` 并受 `progressThrottle` 约束，不刷爆日志

- [ ] **Step 8: 跑全包测试与竞态检查**

Run: `go test ./internal/executor/grok/ -race -count=1 -v`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add internal/executor/grok/adapter.go internal/executor/grok/adapter_test.go internal/executor/grok/testdata internal/executor/grok/export_test.go
git commit -m "feat(grok): Adapter 五动作骨架与 ACP 事件映射

回合边界取 session/prompt 的响应（stopReason）而非从 idle 事件推断，
因此不需要 opencode 那套 idle 去抖与竞态处理。推理流与工具调用只进
render.log 不进回合正文——ParseTrailer 取最后一个 JSON 行，推理里复述
协议样例会污染判定。testdata 用 spike 采到的真实报文作回归基线。"
```

---

### Task 6: 权限门——挂起表、裁决回发与断开处置（`perm.go`）

**Files:**
- Create: `internal/executor/grok/perm.go`
- Create: `internal/executor/grok/perm_test.go`

**Interfaces:**
- Consumes: `ACPClient.Reply`（Task 2）、`runState`（Task 5）
- Produces:
  - `(*Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) error`
  - `(*Adapter) PermissionsVolatile() bool`（恒 true，供 manager 的能力探测）
  - `(*runState) notePending(toolCallID string, reqID json.RawMessage)`
  - `(*runState) takePending(toolCallID string) (json.RawMessage, bool)`
  - `(*runState) voidAllPending() int`

- [ ] **Step 1: 写失败测试**

`internal/executor/grok/perm_test.go`：

```go
package grok_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestRespondPermissionUnknownTaskIsNotRunning(t *testing.T) {
	a := grok.New(nil)
	err := a.RespondPermission(context.Background(), "no-such-task", "call-1", "once")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("未知任务必须包装 ErrTaskNotRunning，得到 %v", err)
	}
}

func TestRespondPermissionUnknownPermIDIsNotRunning(t *testing.T) {
	a, r := grok.NewAdapterWithRunForTest("t1")
	_ = r
	err := a.RespondPermission(context.Background(), "t1", "call-does-not-exist", "once")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("挂起表查不到必须包装 ErrTaskNotRunning（executor 已不在），得到 %v", err)
	}
}

func TestDecisionMapsToACPOptionIDs(t *testing.T) {
	cases := map[string]string{"once": "allow-once", "reject": "reject-once"}
	for decision, want := range cases {
		if got := grok.OptionIDForTest(decision); got != want {
			t.Errorf("decision %q → %q，期望 %q", decision, got, want)
		}
	}
	// 未知裁决必须 fail-closed 到拒绝，绝不误放行
	if got := grok.OptionIDForTest("garbage"); got != "reject-once" {
		t.Errorf("未知裁决必须 fail-closed 为 reject-once，得到 %q", got)
	}
}

func TestVoidAllPendingCountsAndClears(t *testing.T) {
	_, r := grok.NewAdapterWithRunForTest("t1")
	grok.NotePendingForTest(r, "c1", []byte("1"))
	grok.NotePendingForTest(r, "c2", []byte("2"))
	if n := grok.VoidAllPendingForTest(r); n != 2 {
		t.Errorf("作废数 = %d，期望 2", n)
	}
	if n := grok.VoidAllPendingForTest(r); n != 0 {
		t.Errorf("重复作废应为 0，得到 %d", n)
	}
}

func TestPermissionsVolatileIsTrue(t *testing.T) {
	// 实测：重连 + session/load 后未决权限不会被重发，manager 据此不恢复
	if !grok.New(nil).PermissionsVolatile() {
		t.Error("grok 的权限随连接消亡，必须返回 true")
	}
}
```

在 `export_test.go` 追加：

```go
// NewAdapterWithRunForTest 造一个带空运行态的 adapter，供权限表断言。
func NewAdapterWithRunForTest(taskID string) (*Adapter, *runState) {
	a := New(nil)
	r := &runState{taskID: taskID, pending: map[string]json.RawMessage{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	a.runs[taskID] = r
	return a, r
}

func OptionIDForTest(decision string) string { return optionIDFor(decision) }

func NotePendingForTest(r *runState, id string, reqID []byte) { r.notePending(id, reqID) }
func VoidAllPendingForTest(r *runState) int                   { return r.voidAllPending() }
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestRespondPermission|TestDecisionMaps|TestVoidAll|TestPermissionsVolatile' -v`
Expected: 编译失败 —— `undefined: optionIDFor` 等

- [ ] **Step 3: 实现 perm.go**

```go
// perm.go —— ACP 权限门：挂起表、裁决回发与连接断开时的处置。
//
// 职责：
//   - 暂存 toolCallId → ACP 请求 id，等 manager 的裁决回来后经 Reply 回发
//   - 把 handoff 的 once/reject 翻译为 ACP 的 allow-once/reject-once
//   - 连接断开时作废全部挂起项并告知调用方
//
// 边界：
//   - 不做审批判断：批不批由 manager 依审核者/审批者的应答决定，本层只转发
//   - 不碰 store：工单、黑名单、升级链全在 manager
//
// 为什么需要挂起表（opencode 没有）：ACP 的权限是 agent→client 的**阻塞式
// JSON-RPC 请求**，应答必须带原请求 id 回发；而 opencode 的权限应答是一次
// 独立的 HTTP POST，无需保留连接级状态。
//
// 为什么断开即作废且不再尝试救回：spike 实测 WS 断开后重连 + session/load
// 成功，但未决的权限请求**不会被重发**，grok 侧那次工具调用永久卡在等应答。
// 此时假装恢复成功比直接失败更危险——任务会静止而无人知晓。
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xushixin/handoff/internal/executor"
)

// optionIDFor 把 handoff 的裁决翻译为 ACP 的 optionId。
//
// fail-closed：未知裁决一律按拒绝，绝不误放行——误拒的代价是审核者再来一轮，
// 误放的代价可能是不可逆的破坏性操作。
func optionIDFor(decision string) string {
	if decision == "once" {
		return "allow-once"
	}
	return "reject-once"
}

// notePending 登记一个待裁决的权限请求。
func (r *runState) notePending(toolCallID string, reqID json.RawMessage) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	r.pending[toolCallID] = reqID
}

// takePending 取出并移除挂起项。
func (r *runState) takePending(toolCallID string) (json.RawMessage, bool) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	id, ok := r.pending[toolCallID]
	delete(r.pending, toolCallID)
	return id, ok
}

// voidAllPending 作废全部挂起项，返回作废数量。
func (r *runState) voidAllPending() int {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	n := len(r.pending)
	r.pending = map[string]json.RawMessage{}
	return n
}

// noteRejected 记下本回合被拒的权限描述，回合收尾时一并交代给审核者。
func (r *runState) noteRejected(desc string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.rejected = append(r.rejected, desc)
}

// takeRejected 取走并清空本回合的被拒记录。
func (r *runState) takeRejected() []string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	out := r.rejected
	r.rejected = nil
	return out
}

// rejectedTurnQuestion 把被拒清单拼成交给审核者的问题。
func rejectedTurnQuestion(rejected []string) string {
	var b strings.Builder
	b.WriteString("本回合有权限请求被拒，模型可能改用其它做法或停下。被拒清单：\n")
	for _, d := range rejected {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("请确认下一步该怎么做。")
	return b.String()
}

// PermissionsVolatile 表明本 adapter 的权限请求随连接消亡。
//
// manager 据此在 agentd 重启后拒绝恢复「尚有未决权限工单」的任务——实测
// session/load 只恢复会话历史，不恢复未决授权请求（见 spec §5.2）。
func (a *Adapter) PermissionsVolatile() bool { return true }

// RespondPermission 应答 grok 的权限请求。
//
// 参数：
//   - taskID: 目标任务
//   - permID: 权限请求 id（即 ACP 的 toolCallId，裸值不带命名空间前缀）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//
// 返回：
//   - 任务不在运行中、或挂起表查不到该 permID 时，包装 executor.ErrTaskNotRunning
//     ——两者都意味着「executor 侧那次请求已经不在了」，调用方据此转失败交审核者，
//     而不是当作可重试的瞬时错误
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) (err error) {
	r := a.lookup(taskID)
	if r == nil {
		a.log.Warn("权限应答时任务不在运行中", "task", taskID, "perm", permID)
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	reqID, ok := r.takePending(permID)
	if !ok {
		a.log.Warn("权限应答找不到挂起请求（连接已重建或已作废）",
			"task", taskID, "perm", permID)
		return fmt.Errorf("权限请求 %s 已不在挂起表: %w", permID, executor.ErrTaskNotRunning)
	}

	opt := optionIDFor(decision)
	a.log.Info("回发权限裁决", "task", taskID, "perm", permID, "decision", decision, "option", opt)
	if err := r.cli.Reply(reqID, map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": opt},
	}); err != nil {
		a.log.Error("回发权限裁决失败", "task", taskID, "perm", permID, "cause", err)
		return fmt.Errorf("回发权限裁决: %w", err)
	}
	if opt == "reject-once" {
		r.noteRejected(permID)
	}
	a.log.Info("权限裁决已送达 executor", "task", taskID, "perm", permID)
	return nil
}
```

- [ ] **Step 4: 在 adapter.go 的 ACP 回调里接上权限与断开处置**

`OnPermission` 回调：解析 `toolCall.toolCallId` 与描述文本（`title` + `rawInput.command`，经 `turn.TruncateMarked(…, permTextLimit)`），`notePending` 后 emit permission 事件。

`OnClosed` 回调按 spec §4.2.2 落地：

```go
// onClosed 是 ACP 连接终止的唯一处置入口。
//
// 为什么挂起表非空就直接终结、不再尝试重连：实测重连后 grok 不会重发未决的
// 权限请求，那次工具调用已永久卡死。重连成功反而更危险——adapter 会以为一切
// 正常，而任务再也不会前进。宁可立刻转 failed 让审核者 continue 重开一轮。
func (a *Adapter) onClosed(r *runState, cause error) {
	if n := r.voidAllPending(); n > 0 {
		a.log.Error("ACP 连接断开且有未决权限，任务无法继续",
			"task", r.taskID, "voided", n, "cause", cause)
		a.emitFailed(r, fmt.Sprintf("权限应答通道中断（%d 个未决请求作废），需重新发起一轮", n))
		return
	}
	a.log.Warn("ACP 连接断开，无未决权限", "task", r.taskID, "cause", cause)
	a.emitFailed(r, fmt.Sprintf("ACP 连接断开: %v；serve 日志尾部: %s", cause, r.proc.LogTail()))
}
```

> 本期**不实现退避重连**：spec §4.2.2 的重连分支只在「挂起表为空」时有意义，而实践中断开几乎总伴随未决权限或 serve 死亡。保持单一出口（转 failed 交审核者）更可靠，也少一条难测的路径。若真机验收发现无权限场景下的瞬断频繁，再单独立项加重连——记入 backlog 而非本期实现。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/executor/grok/ -race -count=1 -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/executor/grok/perm.go internal/executor/grok/perm_test.go internal/executor/grok/adapter.go internal/executor/grok/export_test.go
git commit -m "feat(grok): ACP 权限门——挂起表、裁决回发与断开即终结

未知裁决 fail-closed 为 reject-once（误拒代价是再来一轮，误放可能不可逆）。
连接断开且有未决权限时直接转 failed：实测重连后 grok 不会重发未决权限，
假装恢复成功会留下一个静止而无人知晓的任务。"
```

---

### Task 7: Resume、看门狗与 manager 能力接口

**Files:**
- Create: `internal/executor/grok/resume.go`
- Create: `internal/executor/grok/resume_test.go`
- Modify: `internal/agentd/manager.go`（加 `volatilePermitter`，`ResumeTask` 前置判定）
- Modify: `internal/agentd/manager_test.go`（加两条回归）

**Interfaces:**
- Consumes: `ReadServeInfo`（Task 4）、`ACPClient`（Task 2）、`store.PendingTickets`（既有）
- Produces:
  - `(*Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error)`
  - `internal/agentd`：`type volatilePermitter interface{ PermissionsVolatile() bool }`

- [ ] **Step 1: 写 manager 侧失败测试（这是 §5.2 的回归钉子）**

`internal/agentd/manager_test.go` 追加：

```go
// fakeVolatileAdapter 是权限随连接消亡的假 adapter（模拟 grok）。
type fakeVolatileAdapter struct {
	*chanAdapter
	resumeCalled bool
}

func (f *fakeVolatileAdapter) PermissionsVolatile() bool { return true }
func (f *fakeVolatileAdapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error) {
	f.resumeCalled = true
	return true, nil
}

// TestResumeRefusedWhenVolatilePermitterHasPendingTicket 固定 spec §5.2：
// grok 类 adapter 若任务尚有未决权限工单，agentd 重启后不得恢复——实测
// session/load 只恢复会话历史，不恢复未决授权请求，恢复了也永远不会前进。
func TestResumeRefusedWhenVolatilePermitterHasPendingTicket(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "t-volatile"
	// 建任务与一张未应答的权限工单（具体 helper 名以仓库现有测试为准）
	seedTaskWithPendingPermissionTicket(t, st, taskID)

	ad := &fakeVolatileAdapter{chanAdapter: newChanAdapter()}
	m.ads["grok"] = ad

	alive := m.ResumeTask(taskID)
	if alive {
		t.Error("有未决权限工单时必须拒绝恢复")
	}
	if ad.resumeCalled {
		t.Error("必须在调用 adapter.Resume 之前就拒绝，避免建立一条永远不会前进的连接")
	}
}

// TestResumeUnaffectedForNonVolatileAdapter 保证 opencode 不被这条规则波及：
// 它的权限应答是无状态 HTTP，agentd 重启后仍可应答。
func TestResumeUnaffectedForNonVolatileAdapter(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "t-stateless"
	seedTaskWithPendingPermissionTicket(t, st, taskID)

	ad := newResumableChanAdapter(true) // 不实现 PermissionsVolatile
	m.ads["opencode"] = ad

	if alive := m.ResumeTask(taskID); !alive {
		t.Error("无状态权限的 adapter 不应受未决工单影响")
	}
}
```

> 实现者注意：`seedTaskWithPendingPermissionTicket` 与 `newResumableChanAdapter` 若仓库尚无，按现有 `manager_test.go` 的 helper 风格补齐——建任务（`executor` 字段分别为 `grok`/`opencode`、状态 `waiting_review`）、`st.CreateTicket` 一张 `kind=permission` 且未 answer 的工单。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agentd/ -run 'TestResumeRefusedWhenVolatile|TestResumeUnaffected' -v`
Expected: FAIL —— 第一条返回 true（尚无前置判定）

- [ ] **Step 3: 实现 manager 侧能力接口与前置判定**

`internal/agentd/manager.go`，在 `restorer` 附近追加：

```go
// volatilePermitter 表示该 adapter 的权限请求随连接消亡：连接一断，executor 侧
// 那次授权请求就永久卡死，重连也救不回（ACP 类适配器，见 grok spec §5.2）。
//
// 不实现本接口的 adapter（如 opencode——权限应答是无状态 HTTP POST，permID 由
// serve 端持有）行为不变。
type volatilePermitter interface {
	PermissionsVolatile() bool
}
```

在 `ResumeTask` 里、调用 `ad.Resume(...)` **之前**插入：

```go
	// 权限随连接消亡的 adapter：任务若还挂着未决权限工单，恢复了也永远不会前进
	// （executor 侧那次授权请求已随旧连接卡死），直接判不可恢复交审核者裁决。
	if vp, ok := ad.(volatilePermitter); ok && vp.PermissionsVolatile() {
		pending, err := m.st.PendingTickets(taskID)
		if err != nil {
			m.log.Error("读取未决工单失败，保守判定不可恢复", "task", taskID, "cause", err)
			return false
		}
		if len(pending) > 0 {
			m.log.Warn("任务有未决权限工单且执行者权限随连接消亡，不予恢复",
				"task", taskID, "pending", len(pending))
			return false
		}
	}
```

- [ ] **Step 4: 运行 manager 测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestResumeRefusedWhenVolatile|TestResumeUnaffected' -v`
Expected: PASS

- [ ] **Step 5: 实现 grok 侧 Resume 与看门狗（`resume.go`）**

```go
// resume.go —— agentd 重启后的执行恢复与 serve 存活看门狗。
//
// 职责：
//   - Resume：读 serve.json → 端口探活 → 修复 auth 软链 → 重连 ACP →
//     session/load → 重建事件循环
//   - watchdog：周期探活，判死时以脱敏的 serve.log 尾部产出 failed result
//
// 边界：
//   - 不判断「该不该恢复」的业务前提（如是否有未决权限工单）——那需要工单知识，
//     属 manager（见 manager.go 的 volatilePermitter）
//
// 看门狗节奏与 opencode 同规格：活跃期 200ms 高频，连续 fastProbes 次成功且无
// 新事件后降到 2s（任务挂夜里时省下每天数十万次探活），任一失败立即回高频；
// 连续 failThreshold 次失败才判死，吸收抖动不误杀。
package grok

const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3
)

// Resume 尝试恢复一个 agentd 重启前已在执行的任务。
//
// 参数：
//   - taskID/taskDir/repoPath: 任务标识与目录
//   - sessionID: 落库的 executor 会话 id（空则无法 session/load，判不可恢复）
//
// 返回：
//   - true：serve 存活、ACP 已重连、会话已载入、事件流已重建
//   - false：serve 已不在或凭据缺失，调用方据此转 failed 交审核者
func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error)
```

```go
func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error) {
	a.log.Info("grok 尝试恢复任务", "task", taskID, "task_dir", taskDir, "session", sessionID)

	// 没有会话 id 就没法 session/load——恢复的前提不成立，这不是错误
	if sessionID == "" {
		a.log.Info("无 executor 会话 id，判不可恢复", "task", taskID)
		return false, nil
	}
	proc, err := ReadServeInfo(taskDir)
	if err != nil {
		a.log.Info("恢复凭据缺失，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}
	if !proc.Alive() {
		a.log.Info("serve 已不在，判不可恢复", "task", taskID, "port", proc.Port)
		return false, nil
	}
	// token 刷新期间软链可能已被干掉（spec §3.3 实测），重连前先修好
	if err := EnsureAuthLink(filepath.Join(taskDir, homeDirName)); err != nil {
		a.log.Warn("修复 auth 软链失败，仍尝试重连", "task", taskID, "cause", err)
	}

	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath, sessionID: sessionID,
		proc: proc, evCh: make(chan executor.AdapterEvent, 64),
		acc: newTurnAccumulator(), pending: map[string]json.RawMessage{},
	}
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli, err := DialACP(ctx, proc.WSURL(), &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		a.log.Warn("ACP 重连失败，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}
	r.cli = cli
	if _, err := cli.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal": false,
		},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("重连后 initialize 失败，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}
	if _, err := cli.Call(ctx, "session/load", map[string]any{
		"sessionId": sessionID, "cwd": repoPath, "mcpServers": []any{},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("session/load 失败，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	go a.watchdog(r)

	a.log.Info("grok 任务已恢复", "task", taskID, "session", sessionID, "port", proc.Port)
	return true, nil
}
```

**注意本函数不重发 `session/prompt`**：恢复的是「正在跑的回合」的观察通道，不是重开一轮。
若恢复时该回合早已结束（事件已错过），任务会停在 `waiting_review` 由审核者处置——这比
擅自重发一轮安全。

`watchdog` 的实现（周期探活，判死即失败终局）：

```go
// watchdog 周期探活 serve，连续 watchdogFailThreshold 次失败即判死。
//
// 快慢双档：有新事件时 200ms 高频；连续 watchdogFastProbes 次成功且无新事件
// （任务挂夜里等审核）后降到 2s——高频探活每天每任务数十万次 HTTP 请求，
// 降频后一个量级；任一失败立即回高频，保证判死不被降频拖太慢。
func (a *Adapter) watchdog(r *runState) {
	interval, okStreak, failStreak := watchdogFastInterval, 0, 0
	for {
		time.Sleep(interval)
		r.emitMu.Lock()
		closed := r.evClosed
		r.emitMu.Unlock()
		if closed {
			return // 任务已终结，看门狗退场
		}
		if r.proc.Alive() {
			failStreak = 0
			okStreak++
			if okStreak >= watchdogFastProbes {
				interval = watchdogSlowInterval
			}
			continue
		}
		okStreak, interval = 0, watchdogFastInterval
		failStreak++
		if failStreak < watchdogFailThreshold {
			a.log.Warn("grok serve 探活失败", "task", r.taskID, "streak", failStreak)
			continue
		}
		a.log.Error("grok serve 判定死亡", "task", r.taskID, "port", r.proc.Port)
		a.emitFailed(r, "grok serve 进程已死亡；serve 日志尾部: "+r.proc.LogTail())
		return
	}
}
```

- [ ] **Step 6: 写 Resume 的失败判定测试**

`internal/executor/grok/resume_test.go`：

```go
package grok_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	alive, err := a.Resume("t1", t.TempDir(), t.TempDir(), "")
	if alive {
		t.Error("无 sessionID 无法 session/load，必须判不可恢复")
	}
	if err != nil {
		t.Errorf("不可恢复不是错误，应静默返回 false，得到 %v", err)
	}
}

func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := grok.New(nil)
	alive, _ := a.Resume("t1", t.TempDir(), t.TempDir(), "sess-1")
	if alive {
		t.Error("serve.json 缺失时必须判不可恢复")
	}
}
```

- [ ] **Step 7: 跑全量测试**

Run: `go test ./internal/executor/... ./internal/agentd/ -race -count=1`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add internal/executor/grok/resume.go internal/executor/grok/resume_test.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(grok): Resume 与看门狗；manager 加 volatilePermitter 能力接口

未决权限工单的判定放在 manager 而非 adapter——adapter 不碰 store（executor 包级
边界），且这条不能无条件套给 opencode（无状态 HTTP 应答，重启后仍可应答）。
grok 实现该接口返回 true，opencode 不实现，行为不变。"
```

---

### Task 8: 接入面——注册表、one-shot、帮助文本与文档

**Files:**
- Modify: `cmd/agentd.go:77-85`（adapter 注册表与错误文本）
- Modify: `internal/executor/oneshot.go:26-39`（grok 分支）
- Modify: `internal/executor/oneshot_test.go`（grok 用例）
- Modify: `cmd/dispatch.go:100`（`--executor` 帮助文本）
- Modify: `internal/config/config.go:58`（注释里的执行者示例）
- Modify: `README.md`

- [ ] **Step 1: 写 one-shot 的失败测试**

`internal/executor/oneshot_test.go`，把原有 `{"未知执行者", "grok", ...}` 用例改为正例并补两条：

```go
		{"grok 带模型", "grok", "grok-4.5", "p",
			[]string{"grok", "--effort", "low", "-m", "grok-4.5", "-p", "p"}, false},
		{"grok 不带模型", "grok", "", "p",
			[]string{"grok", "--effort", "low", "-p", "p"}, false},
		{"未知执行者", "gemini", "", "p", nil, true},
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/executor/ -run TestOneShotArgs -v`
Expected: FAIL —— grok 仍走 default 分支返回错误

- [ ] **Step 3: 实现 grok 分支**

`internal/executor/oneshot.go` 的 switch 追加：

```go
	case "grok":
		// why --effort low：实测同一条裁决 prompt，默认 high effort 32.4s、
		// low 7.5s，而审批者默认超时 60s——high 档等于把预算烧掉一半以上。
		// 本函数的职责就是「一次性调用形态的唯一登记点」，把「一次性 = 廉价
		// 快速」编码在这里符合定位。
		//
		// why 参数顺序不能动：-p <PROMPT> 是取值参数而非开关，--effort 必须
		// 排在 -p 之前，否则 grok 报 "a value is required for '--single'"。
		// prompt 仍是末位参数，本函数的契约不变。
		if model != "" {
			return []string{"grok", "--effort", "low", "-m", model, "-p", prompt}, nil
		}
		return []string{"grok", "--effort", "low", "-p", prompt}, nil
```

同步 default 分支错误文本：`未知执行者 %q（one-shot 支持 opencode/claude/grok）`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/executor/ -run TestOneShotArgs -v`
Expected: PASS

- [ ] **Step 5: 注册表与帮助文本**

`cmd/agentd.go`：注册表加 `"grok": grok.New(logger)`（import `internal/executor/grok`），并把 `未知 executor %q（支持 opencode/fake）` 改为 `（支持 opencode/grok/fake）`；flag 说明同步。

`cmd/dispatch.go:100`：`--executor` 说明改为 `执行者名（如 opencode/grok/fake；空=agentd 缺省执行者）`。

`internal/config/config.go:58`：注释里的 `（如 opencode/claude）` 改为 `（如 opencode/claude/grok）`。

- [ ] **Step 6: README 补执行者与已知限制**

在执行者一节补 grok（传输形态、模型来源、与 opencode 的差异），并在已知限制补：

```markdown
- **grok 执行者会读到你的 Claude Code 个人配置**：grok 无视 `GROK_HOME`，仍从真实
  HOME 读 `~/.claude/settings.local.json` 的权限规则与 `~/.claude/skills`。handoff
  写入任务级 `ask` 规则可以压过其中的 `allow`（grok 的求值是 `deny` > `ask` > `allow`
  跨源生效），危险模式表仍然有效；但「handoff 没枚举、而你个人 allow 了」的操作
  会被静默放行。agentd 侧的硬黑名单是独立兜底。
- **grok 任务断连即失败**：ACP 的权限是随连接存续的阻塞请求，连接断开后未决的
  授权请求不会被重发。handoff 选择立刻转 failed 交审核者（可 `continue` 重开一轮），
  而不是假装恢复成功留下一个静止的任务。
```

- [ ] **Step 7: 全量构建、vet 与测试**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add cmd internal/executor/oneshot.go internal/executor/oneshot_test.go internal/config/config.go README.md
git commit -m "feat(grok): 接入 adapter 注册表与 one-shot 审批者；文档补已知限制

one-shot 编码 --effort low：实测 high 档 32.4s、low 档 7.5s，而审批者默认
超时 60s。--effort 必须排在 -p 之前（-p 是取值参数不是开关）。"
```

---

### Task 9: 真机验收

**Files:** 无代码改动（发现问题则回到对应 Task 修）

- [ ] **Step 1: 确认前置**

Run: `grok --version && tmux -V && grok inspect | head -5`
Expected: grok 1.0.0、tmux 可用、`Project trusted: yes`。若 grok 未登录，先 `grok login`。

- [ ] **Step 2: 起 agentd 并派发一个真实小任务**

准备一个只需一两步改动的 plan 文件，然后：

```bash
handoff dispatch /tmp/grok-smoke-plan.md --executor grok --new-worktree --name grok-smoke
```

Expected: 派发成功、返回任务 id、终端自动弹出 attach。

- [ ] **Step 3: 确认 attach 两个窗口都活**

Run: `handoff attach <task-id>`
Expected: 窗口 0 是 serve 进程输出，窗口 1 是 `tail -f render.log` 且能看到模型文本、推理与工具动作实时滚动。

- [ ] **Step 4: 走通权限升级 → 审核者批准**

让计划包含一条会命中 `ask` 规则的动作（如 `curl` 或 `rm`）。

Run: `handoff list` 观察工单；`handoff answer <ticket-id> allow`
Expected: 权限事件入库、审核者批准后 executor 继续执行（`render.log` 可见工具真的跑了）。

- [ ] **Step 4b: 验证提问工具不会挂死回合（spec §4.2.3 的真机钉子）**

这条必须真机验——单测里的假 agent 只能证明「回了包」，证明不了「grok 认这个包」。

Run: `handoff continue <task-id> "先别写代码。用 ask_user_question 工具问我：这个功能用 Go 还是 Rust？给两个选项。"`

Expected（三条同时成立才算过）：
1. `handoff show <task-id>` 出现一条 **question** 事件，文本含问题与 `1) Go` / `2) Rust`；
2. 该回合在 **2 分钟内收口**（出现 question 或 result），**不是**永久停在 running
   ——挂死的表现恰恰是进程健在、看门狗探活通过、什么都不发生；
3. `<taskDir>/render.log` 里能看到 `【模型提问】` 段落。

若模型没调该工具（它可能直接用文本提问），换更强的措辞重试一次；两次都没调到就记入
验收备注，不阻塞——但**不得**把这条标记为通过。

- [ ] **Step 5: 走通续接与归档**

Run: `handoff continue <task-id> "再改一处：把注释补全"` 然后 `handoff done <task-id>`
Expected: 同一会话续接（上下文保留）、归档时 managed worktree 被 `git worktree remove` 清理。

- [ ] **Step 6: 验证 secret 未泄漏**

Run: `ps -eo command | grep -c -- "--secret"` 与 `grep -c "$(python3 -c "import json;print(json.load(open('<taskDir>/serve.json'))['secret'])")" <taskDir>/serve.log`
Expected: 第一条为 0（secret 不在任何 argv）；serve.log 里即便有 secret，`handoff` 产生的事件与 agentd.log 中不得出现（抽查 `handoff show <task-id>` 的 failed 事件文本）。

- [ ] **Step 7: 验证 agentd 重启恢复**

在任务处于执行中（无未决权限）时重启 agentd。
Expected: 任务被 `Resume` 接回，事件流继续；若此时**有**未决权限工单，则任务应转 failed 并在事件里说明原因（§5.2 的保守路径）。

- [ ] **Step 8: 记录验收证据并收口 backlog**

把每步的实际输出摘要写进 `docs/superpowers/backlog.md` 的 B3 行，状态转 done（已验），并**更正 B3 原备注**——原记「缺程序化审批挂载点」的前提已被证伪。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs: backlog B3 收口为 done（已验），更正原错误前提

grok 1.0.0 实现完整 ACP，权限门是协议内建的 session/request_permission，
不存在原备注所说的「缺程序化审批挂载点」。附真机验收证据。"
```

---

## 附：与 Claude Code adapter（B2）的协作约定

- Task 1 完成后**立即单独合入 main**，并知会 B2 会话——它的 plan 不含抽取任务，直接 `import "github.com/xushixin/handoff/internal/executor/turn"`。
- Task 2 之后的所有 Task 都应在独立 worktree 里做，避免与 B2 抢主仓（见 `superpowers:using-git-worktrees`）。
- 两个 adapter 最终的冲突面只有三处，合并时逐一手工核对：
  1. `cmd/agentd.go` 的注册表与错误文本（各加一行/各改一处）
  2. `README.md` 的执行者一节与已知限制
  3. `internal/agentd/manager.go`：B3 加 `volatilePermitter`；若 B2 也动 `ResumeTask`，需确认两处判定顺序不互相遮蔽
