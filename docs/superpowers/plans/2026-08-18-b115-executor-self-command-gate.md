# B115 executor 自指令收口 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 executor 在 shell 里调用 handoff 变更类子命令时一律 `Escalate`（升级人工协调者），而不是被廉价模型自动放行。

**Architecture:** 在 `internal/permgate` 新增一份与黑名单并列、但威胁轴不同的「身份越权」判据：白名单形态（只读子命令放行，其余一律拦，未知子命令默认落在拦截侧）。判据前置于 `judgeCommand`，引号与执行包装器的处理与现有黑名单逐条同构，复用现成的 `StripQuoted` / `HasExecWrapper`。未命中时原有行为逐字不变。

**Tech Stack:** Go 1.x，标准库 `strings` / `log/slog`；测试用标准 `testing` 表驱动。项目已有依赖，不引入新的。

**Spec:** `docs/superpowers/specs/2026-08-18-b115-executor-self-command-gate-design.md`

## Global Constraints

- 判据只作用于命令类路由（`judgeCommand`）；`judgeFileWrite` 路由**不改**。
- 不改 `Action` 枚举、不改 `Verdict` 结构、不新增 `Deny` 出口。permgate 的三出口模型保持原样。
- 未命中自指令时，`judgeCommand` 的行为必须与改动前**逐字相同**——现有 `blacklist_test.go` 全部用例不得改动即通过。
- 只读白名单固定 9 项，逐字：`tasks` `show` `diff` `fetch` `status` `frames` `sessions` `footprint` `ls`。
- 变更名单固定 20 项，逐字：`dispatch` `continue` `done` `stop` `reply` `resume` `run` `reclaim` `attach` `pull` `agentd` `init` `service` `skill` `upgrade` `update-check` `project` `machines` `revoke` `console`。
- `Verdict.Rule` 在自指令命中时取常量 `permgate.RuleSelfCommand`，值为 `"self-command"`。
- 日志一律用结构化 logger（permgate 内用 `g.log`，agentd 内用 `m.log`）；**禁止 `fmt.Printf`**。
- 中文注释：新文件写文件头（职责 + 边界），导出符号写 doc 注释，非显然分支写「为什么」。
- 每个 Task 完成即 commit，提交信息按各 Task 的 Commit 步骤原文。

## File Structure

| 文件 | 处置 | 职责 |
|---|---|---|
| `internal/permgate/selfcmd.go` | **新建** | handoff 自指令的纯判定：切段、取候选词元、三级判定。不碰引号剥离与包装器识别 |
| `internal/permgate/selfcmd_test.go` | **新建** | `IsSelfCommand` 的表驱动单测 |
| `internal/permgate/blacklist.go` | 修改 `judgeCommand`（当前 130 行起） | 前置自指令判定，编排引号/包装器三路出口 |
| `internal/permgate/blacklist_test.go` | 追加用例 | 引号降级、包装器硬拦、未命中透传 |
| `internal/agentd/manager.go` | 修改 `judgePermission` 的日志级别分支（当前 1568-1577 行） | 自指令走 Warn，让现场一眼可见 |
| `internal/agentd/manager_test.go` | 追加用例 | `escalateLogLevel` 的三档判定 |
| `internal/agentd/approver.go` | 修改 `approverPromptTemplate`（当前 268 行起） | 补一句自指令语义作兜底 |

为什么自指令判据独立成文件而不塞进 `blacklist.go`：那个文件的职责是**危险性**判据（这条命令会不会破坏东西），本条是**身份越权**判据（这个角色该不该做这件事）。两者威胁轴不同，混在一起下次改会互相牵扯——B115 本身就是「威胁轴只有一条」造成的。

---

### Task 1: 自指令纯判据 `IsSelfCommand`

**Files:**
- Create: `internal/permgate/selfcmd.go`
- Test: `internal/permgate/selfcmd_test.go`

**Interfaces:**
- Consumes: 无（本 task 不依赖其他 task）
- Produces:
  - `const RuleSelfCommand = "self-command"`
  - `func IsSelfCommand(s string) (hit bool, sub string)` — `hit` 为是否判为自指令；`sub` 为命中的子命令名，未命中时为 `""`

**本 task 不加运行时日志**，这是刻意决定不是遗漏：`IsSelfCommand` 是包级纯函数，拿不到 `Gate.log`；在这里塞 logger 参数会污染它的可测性。观测点落在 Task 2 的 `judgeCommand`（`g.log` 在那里可得）与 Task 3 的 `manager.judgePermission`（那里才有 task id 可关联）。

- [ ] **Step 1: 写失败的表驱动测试**

创建 `internal/permgate/selfcmd_test.go`：

```go
package permgate

import "testing"

func TestIsSelfCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		hit  bool
		sub  string
	}{
		// 真调用：三种可执行文件形态
		{"裸调用", "handoff dispatch plan.md", true, "dispatch"},
		{"相对路径", "./handoff run T1 ls", true, "run"},
		{"绝对路径", "/usr/local/bin/handoff done T1", true, "done"},
		{"Windows 后缀", `C:\bin\handoff.exe stop T1`, true, "stop"},

		// flag 插在中间：flag 与它的值都不得干扰候选判定
		{"持久 flag 后接变更命令", "handoff --agentd http://x:1 dispatch plan.md", true, "dispatch"},
		{"持久 flag 后接只读命令", "handoff --agentd http://x:1 tasks", false, ""},

		// 自己批自己的工单
		{"自批工单", "handoff reply T1 --ticket X --approve", true, "reply"},

		// 白名单放行
		{"tasks", "handoff tasks", false, ""},
		{"show", "handoff show T1", false, ""},
		{"diff 带 flag", "handoff diff T1 --base main", false, ""},

		// 切段：管道后的词元不参与本段判定
		{"管道隔段", "handoff tasks | grep done", false, ""},
		{"与号隔段", "cd handoff && make", false, ""},

		// 变更词优先于白名单词
		{"白名单词塞进变更命令参数", "handoff run T1 handoff show", true, "run"},

		// 安全默认：两个名单都不认识的子命令一律拦
		{"未知子命令", "handoff foo", true, "foo"},

		// 候选为空 → 不命中
		{"纯 flag", "handoff --help", false, ""},
		{"裸二进制名", "handoff", false, ""},
		{"cd 到同名目录", "cd ~/handoff", false, ""},
		{"删同名目录", "rm -rf handoff", false, ""},

		// basename 不是 handoff → 不定位
		{"同名前缀路径", "go test ./handoff/...", false, ""},
		{"同名日志文件", "cat handoff.log", false, ""},

		// 前缀不得被当成白名单词
		{"showoff 不是 show", "handoff showoff", true, "showoff"},

		// 已知误伤：echo 之后的词元同样进候选。代价只是一次人工点击，
		// 不值得为它引入命令语义解析。钉住它是为了让这个取舍显式可见
		{"echo 误伤（已知代价）", "echo handoff dispatch", true, "dispatch"},

		// 完全无关
		{"无关命令", "go test ./...", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, sub := IsSelfCommand(c.in)
			if hit != c.hit || sub != c.sub {
				t.Fatalf("IsSelfCommand(%q) = (%v, %q)，期望 (%v, %q)",
					c.in, hit, sub, c.hit, c.sub)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run TestIsSelfCommand -v`
Expected: 编译失败，`undefined: IsSelfCommand`

- [ ] **Step 3: 写最小实现**

创建 `internal/permgate/selfcmd.go`：

```go
// selfcmd.go —— handoff 自指令判据（身份越权）。
//
// 职责：
//   - 识别「executor 在 shell 里调用 handoff 自身 CLI 的变更类子命令」
//   - 只读子命令按白名单放行，其余一律判为自指令
//
// 边界：
//   - 不判危险性：rm -rf / sudo 这类由 blacklist.go 管。两者威胁轴不同——
//     那边问「这条命令会不会破坏东西」，这边问「这个角色该不该做这件事」
//   - 不做引号剥离与执行包装器识别：那是 judgeCommand 的编排职责，本文件
//     只提供一次「这段文本里有没有自指令」的纯判定，无 I/O、无状态
package permgate

import "strings"

// RuleSelfCommand 是自指令命中时填进 Verdict.Rule 的固定值。
//
// agentd 侧据它把「升级人工」这条日志的级别提到 Warn（见 manager.go 的
// escalateLogLevel）：自指令在本次改动前会被廉价模型静默放行，属于「本该
// 漏过、现在被拦下」那一类，必须在日志里一眼可见。
const RuleSelfCommand = "self-command"

// selfCmdReadOnly 是允许 executor 调用的只读子命令白名单。
//
// 判据是「只读且无外部副作用」。attach 与 pull **不在**其中：两者都要 ssh 到
// 别的机器、用的是协调者的 ssh 身份，副作用越出本机；attach 还开交互会话，
// 而 executor 无 tty，拦它零损失。
var selfCmdReadOnly = map[string]bool{
	"tasks": true, "show": true, "diff": true, "fetch": true, "status": true,
	"frames": true, "sessions": true, "footprint": true, "ls": true,
}

// selfCmdMutating 是明确的变更类子命令名单。
//
// 它**不是**拦截面的全集——未列入的未知子命令同样会被拦（见 judgeSegment
// 第 3 级）。这份名单只有两个作用：让 Verdict.Reason 能报出具体子命令名；
// 让「变更词优先于白名单词」的顺序可判，堵住 `handoff run T1 handoff show`
// 这种把白名单词塞进变更命令参数里的形态。
var selfCmdMutating = map[string]bool{
	"dispatch": true, "continue": true, "done": true, "stop": true,
	"reply": true, "resume": true, "run": true, "reclaim": true,
	"attach": true, "pull": true, "agentd": true, "init": true,
	"service": true, "skill": true, "upgrade": true, "update-check": true,
	"project": true, "machines": true, "revoke": true, "console": true,
}

// IsSelfCommand 判断命令文本里是否存在 handoff 的变更类自指令调用。
//
// 参数：s 为待判文本（bash 路由传 Command，其余路由传 Text）
//
// 返回：
//   - hit: 是否判为自指令
//   - sub: 命中的子命令名；未知子命令返回该词元原文；未命中返回 ""
//
// 判定分三步（spec §3.3）：
//  1. 按 | ; & 换行切段，逐段独立判定
//  2. 段内找首个 basename 为 handoff/handoff.exe 的词元，其后不以 - 开头的
//     词元即候选
//  3. 三级判定，顺序不可换：含变更词 → 命中；否则含白名单词 → 放行；
//     否则候选非空 → 命中
//
// 注意：本函数不处理引号与执行包装器，调用方（judgeCommand）负责按原文与
// StripQuoted 结果各跑一遍。
func IsSelfCommand(s string) (hit bool, sub string) {
	for _, seg := range splitSegments(s) {
		if h, name := judgeSegment(seg); h {
			return true, name
		}
	}
	return false, ""
}

// splitSegments 按 shell 的命令分隔符把文本切成独立命令段。
//
// 为什么必须先切段：判定问的是「handoff 之后跟了什么」，而 | ; & 之后的词元
// 属于另一条命令。不切的话 `handoff tasks | grep done` 里的 done 会被算成
// handoff 的候选子命令，把一次只读调用误判成变更调用。
func splitSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '\n'
	})
}

// judgeSegment 判定单个命令段，返回值语义同 IsSelfCommand。
func judgeSegment(seg string) (bool, string) {
	fields := strings.Fields(seg)
	idx := -1
	for i, f := range fields {
		if isHandoffBinary(f) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, ""
	}
	// 跳过 flag：`handoff --agentd http://x:1 tasks` 里 --agentd 与它的值都不
	// 该当成子命令。flag 的值跳不掉（它不以 - 开头），但无妨——它落进候选后
	// 两个名单都不认识，会被后面的白名单词或变更词覆盖判定
	var cand []string
	for _, f := range fields[idx+1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		cand = append(cand, f)
	}
	if len(cand) == 0 {
		// `handoff --help`、裸 `handoff`、`cd ~/handoff`、`rm -rf handoff`
		// 都落在这里：没有子命令就没有变更行为
		return false, ""
	}
	// 顺序不可换：变更词优先于白名单词
	for _, c := range cand {
		if selfCmdMutating[c] {
			return true, c
		}
	}
	for _, c := range cand {
		if selfCmdReadOnly[c] {
			return false, ""
		}
	}
	// 安全默认：两个名单都不认识的子命令一律拦。B115 的成因正是黑名单形态下
	// 「新出现的东西默认是敞的」，这条把默认反过来
	return true, cand[0]
}

// isHandoffBinary 判断词元是否为 handoff 可执行文件的调用形态。
//
// 取 basename 而非整串比较，是为了覆盖 ./handoff 与 /usr/local/bin/handoff；
// 认 .exe 后缀是为了 Windows 执行机（B37）。反过来，cat handoff.log 的
// basename 是 handoff.log，不命中——同名文件不会被误当成调用。
func isHandoffBinary(tok string) bool {
	base := tok
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	return base == "handoff" || base == "handoff.exe"
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/permgate/ -run TestIsSelfCommand -v`
Expected: PASS，23 个子用例全绿

- [ ] **Step 5: 跑全包回归确认没碰坏既有判据**

Run: `go test ./internal/permgate/`
Expected: ok，`blacklist_test.go` / `path_test.go` / `permgate_test.go` 全部原样通过

- [ ] **Step 6: 格式检查**

Run: `gofmt -l internal/permgate/`
Expected: 无输出

- [ ] **Step 7: Commit**

```bash
git add internal/permgate/selfcmd.go internal/permgate/selfcmd_test.go
git commit -m "feat(permgate): handoff 自指令纯判据——白名单放行只读，未知子命令默认拦"
```

---

### Task 2: 接进 `judgeCommand`，编排引号与包装器

**Files:**
- Modify: `internal/permgate/blacklist.go`（`judgeCommand`，当前 130 行起）
- Test: `internal/permgate/blacklist_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `IsSelfCommand(s string) (bool, string)` 与 `RuleSelfCommand`；本包既有的 `StripQuoted(s string) string`、`HasExecWrapper(s string) bool`、`(g *Gate) match(s string) (bool, string)`
- Produces: `judgeCommand` 在自指令命中时返回 `Verdict{Action: Escalate, Rule: RuleSelfCommand, Reason: "executor 试图调用 handoff 变更命令 <sub>（<why>）"}`

- [ ] **Step 1: 写失败的测试**

在 `internal/permgate/blacklist_test.go` 末尾追加：

```go
// TestJudgeCommandSelfCommand 钉住自指令在 judgeCommand 里的四路出口。
func TestJudgeCommandSelfCommand(t *testing.T) {
	g := newTestGate(t) // blacklist_test.go 里既有的 helper，只带内置黑名单
	cases := []struct {
		name   string
		in     string
		action Action
		rule   string
	}{
		{"真调用硬拦", "handoff dispatch plan.md", Escalate, RuleSelfCommand},
		{"包装器藏引号里硬拦", `sh -c "handoff dispatch plan.md"`, Escalate, RuleSelfCommand},
		{"commit message 降级不硬拦", `git commit -m "修 handoff dispatch 的判据"`, Consult, ""},
		{"只读放行落回原链", "handoff tasks", Consult, ""},
		{"无关命令行为不变", "go test ./...", Consult, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := g.judgeCommand(c.in)
			if v.Action != c.action || v.Rule != c.rule {
				t.Fatalf("judgeCommand(%q) = {%v, rule=%q, reason=%q}，期望 {%v, rule=%q}",
					c.in, v.Action, v.Rule, v.Reason, c.action, c.rule)
			}
		})
	}
}

// TestSelfCommandReasonCarriesSubcommand 钉住 Reason 里带得出子命令名——
// 协调者在工单里要能一眼看懂拦的是什么，不必去翻判据源码。
func TestSelfCommandReasonCarriesSubcommand(t *testing.T) {
	g := newTestGate(t)
	v := g.judgeCommand("handoff reply T1 --ticket X --approve")
	if !strings.Contains(v.Reason, "reply") {
		t.Fatalf("Reason 应含子命令名 reply，实得 %q", v.Reason)
	}
}
```

`blacklist_test.go` 已是 `package permgate`（可直接调用未导出的 `judgeCommand`），且已 import `strings` / `log/slog` / `testing`，无需补 import。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run 'TestJudgeCommandSelfCommand|TestSelfCommandReasonCarriesSubcommand' -v`
Expected: FAIL——`handoff dispatch plan.md` 实得 `Consult`（黑名单未命中），期望 `Escalate`

- [ ] **Step 3: 前置自指令判定**

把 `internal/permgate/blacklist.go` 的 `judgeCommand` 函数体改为（doc 注释在 Step 5 补，先让测试过）：

```go
func (g *Gate) judgeCommand(s string) Verdict {
	// 自指令判据前置于黑名单：这是身份越权，与命令本身危不危险无关。
	// 引号处理与下方黑名单逐条同构——剥完引号还在的是真调用，只在引号内
	// 出现的（commit message）降级落回黑名单链
	if hit, sub := IsSelfCommand(StripQuoted(s)); hit {
		return g.selfCmdVerdict(sub, "剥离引号字面量后仍是自指令调用")
	}
	if hit, sub := IsSelfCommand(s); hit && HasExecWrapper(s) {
		return g.selfCmdVerdict(sub, "自指令藏在执行包装器的引号里，内容将被执行")
	}

	hit, rule := g.match(s)
	if h2, r2 := g.match(StripQuoted(s)); h2 {
		return Verdict{Action: Escalate, Reason: "剥离引号字面量后仍命中黑名单", Rule: r2}
	}
	if HasExecWrapper(s) {
		if hit {
			return Verdict{Action: Escalate,
				Reason: "命中黑名单且含执行包装器，引号内内容将被执行", Rule: rule}
		}
		if evalRx.MatchString(s) {
			return Verdict{Action: Escalate,
				Reason: "命令含 eval 且内容不可见，无法判定是否危险"}
		}
	}
	if hit {
		return Verdict{Action: Consult,
			Reason: "仅引号内字面量命中黑名单，降级交审批者裁决", Rule: rule}
	}
	return Verdict{Action: Consult, Reason: "黑名单未命中"}
}
```

- [ ] **Step 4: 加日志与裁决构造函数**

在 `judgeCommand` 之后追加：

```go
// selfCmdVerdict 构造自指令裁决，并在 permgate 侧留一条 Debug 现场。
//
// 参数：sub 为命中的子命令名，why 为命中路径（真调用 / 藏在包装器里）
//
// 注意：这里只打 Debug，权威的 Warn 一条落在 agentd 的 judgePermission——
// 那里才拿得到 task id 与 permission id，能把这条判定关联到具体任务。
// 两处都打 Warn 会让同一件事在日志里出现两遍。
func (g *Gate) selfCmdVerdict(sub, why string) Verdict {
	g.log.Debug("命中 handoff 自指令判据", "subcommand", sub, "why", why)
	return Verdict{
		Action: Escalate,
		Reason: fmt.Sprintf("executor 试图调用 handoff 变更命令 %s（%s）", sub, why),
		Rule:   RuleSelfCommand,
	}
}
```

若 `blacklist.go` 尚未 import `fmt`，补上。

- [ ] **Step 5: 补 judgeCommand 的 doc 注释**

在 `judgeCommand` 现有 doc 注释的「规则（逐条判定）」列表**最前面**插入两条，并更新返回值说明：

```go
// 规则（逐条判定）：
//   - 剥离引号后仍是 handoff 自指令 → Escalate（身份越权，与危险性无关）
//   - 原文是自指令且含执行包装器   → Escalate（`sh -c "handoff dispatch"`）
//   - 剥离引号字面量后仍命中黑名单 → Escalate（真危险，无论引号与否）
//   ...（以下原有各条不变）
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/permgate/ -v`
Expected: 新增两个测试 PASS，且 `blacklist_test.go` 既有用例**一个都没改**仍全绿

- [ ] **Step 7: 格式检查**

Run: `gofmt -l internal/permgate/`
Expected: 无输出

- [ ] **Step 8: Commit**

```bash
git add internal/permgate/blacklist.go internal/permgate/blacklist_test.go
git commit -m "feat(permgate): 自指令判据接进 judgeCommand，引号与包装器处理与黑名单同构"
```

---

### Task 3: agentd 侧可观测性与审批 prompt 兜底

**Files:**
- Modify: `internal/agentd/manager.go`（`judgePermission` 的日志级别分支，当前 1568-1577 行）
- Modify: `internal/agentd/approver.go`（`approverPromptTemplate`，当前 268 行起）
- Test: `internal/agentd/manager_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `permgate.RuleSelfCommand`
- Produces: `func escalateLogLevel(rule string) slog.Level` — 供 `judgePermission` 决定「升级人工」这条日志的级别

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/manager_test.go` 末尾追加：

```go
// TestEscalateLogLevel 钉住「升级人工」日志的三档级别。
//
// Warn 这一档留给「改动前会被静默放行、现在被拦下」的事件：Rule 为空是
// 越界写与结构缺失（B27），self-command 是自指令（B115）。黑名单命中走
// Info，因为它改动前后都会被拦，不是新增的价值。
func TestEscalateLogLevel(t *testing.T) {
	cases := []struct {
		name string
		rule string
		want slog.Level
	}{
		{"结构缺失或越界写", "", slog.LevelWarn},
		{"自指令", permgate.RuleSelfCommand, slog.LevelWarn},
		{"黑名单命中", `\bsudo\b`, slog.LevelInfo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := escalateLogLevel(c.rule); got != c.want {
				t.Fatalf("escalateLogLevel(%q) = %v，期望 %v", c.rule, got, c.want)
			}
		})
	}
}
```

`manager_test.go` 已 import `log/slog`（第 23 行）与 `github.com/Xsxdot/handoff/internal/permgate`（第 36 行），无需补 import。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestEscalateLogLevel -v`
Expected: 编译失败，`undefined: escalateLogLevel`

- [ ] **Step 3: 抽出日志级别判定并接入**

在 `internal/agentd/manager.go` 的 `judgePermission` 之后追加：

```go
// escalateLogLevel 决定「权限判定：升级人工」这条日志的级别。
//
// 参数：rule 为 Verdict.Rule（黑名单命中时是规则原文，自指令时是
// permgate.RuleSelfCommand，其余情形为空）
//
// 为什么不是一律 Info：Warn 这一档留给「本该被静默通过、现在被拦下」的事件，
// 那是每次收口改动的全部价值所在，必须在日志里一眼可见。今天有两类——
// 越界写与结构缺失（Rule 为空，B27 那一批）、自指令（B115）。黑名单命中
// 走 Info，因为它改动前后都会被拦，不是新增的信号。
func escalateLogLevel(rule string) slog.Level {
	if rule == "" || rule == permgate.RuleSelfCommand {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
```

把 `judgePermission` 里 `default:` 分支的级别判定替换为调用它：

```go
	default:
		lvl := escalateLogLevel(v.Rule)
		m.log.Log(context.Background(), lvl, "权限判定：升级人工",
			"task", taskID, "perm", ev.PermissionID, "tool", ev.Perm.Tool,
			"paths", ev.Perm.Paths, "workdir", scope.Workdir, "task_dir", scope.TaskDir,
			"reason", v.Reason, "rule", v.Rule)
```

原先那段说明「越界写与结构缺失用 Warn 而非 Info」的行内注释已迁进 `escalateLogLevel` 的 doc 注释，从 `default:` 分支里删掉，避免同一段理由存两份。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestEscalateLogLevel -v`
Expected: PASS，3 个子用例全绿

- [ ] **Step 5: 补审批 prompt 的兜底语义**

在 `internal/agentd/approver.go` 的 `approverPromptTemplate` 里，「涉及生产环境、部署动作……」那一行**之后**插入一行：

```
涉及调用 handoff 自身 CLI 变更任务状态的操作（如 dispatch/continue/done/reply/run），一律升级给上级协调者。
```

并在该常量的 doc 注释末尾追加一段说明：

```go
// 2026-08-18 增补自指令一行（B115）：这**不是**主判据——自指令由 permgate
// 的 selfcmd.go 判成 Escalate，根本走不到审批者这里。加它是兜底：万一判据
// 漏了某种形态落到 Consult，模型还有一次机会拦下。
```

- [ ] **Step 6: 跑 agentd 全包回归**

Run: `go test ./internal/agentd/`
Expected: ok。`approver_test.go` 里若有钉住 prompt 全文的用例会因新增一行翻红——那是预期内的，按新文案更新期望值，**不要**改动断言的语义

- [ ] **Step 7: 格式检查**

Run: `gofmt -l internal/agentd/`
Expected: 无输出

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go internal/agentd/approver.go
git commit -m "feat(agentd): 自指令拦截走 Warn 级日志，审批 prompt 补自指令兜底语义"
```

---

### Task 4: 总回归与变异测试

**Files:**
- 无新增；本 task 只跑验证并落一份证据

**Interfaces:**
- Consumes: Task 1-3 的全部产出
- Produces: 回归证据（贴进 commit message）

- [ ] **Step 1: 主模块全量测试**

Run: `go test ./...`
Expected: 0 FAIL。记下 ok 的包数

- [ ] **Step 2: 竞态检测**

Run: `go test -race ./internal/agentd/ ./internal/permgate/`
Expected: 0 FAIL，无 race 报告

- [ ] **Step 3: 静态检查与格式**

Run: `go vet ./... && gofmt -l .`
Expected: vet 无输出、gofmt 无输出

- [ ] **Step 4: 变异测试——确认判据真的被测试钉住**

手工做三处变异，每处改完跑 `go test ./internal/permgate/`，确认**有测试翻红**，然后改回：

| 变异 | 期望翻红的用例 |
|---|---|
| `judgeSegment` 里把变更词循环与白名单词循环**对调顺序** | `白名单词塞进变更命令参数` |
| `judgeSegment` 最后一行 `return true, cand[0]` 改成 `return false, ""` | `未知子命令` |
| `splitSegments` 的分隔符去掉 `'\|'` | `管道隔段` |

三处都必须有测试翻红。任何一处改了测试仍全绿，说明该分支没有被钉住，**补测试**再继续。

- [ ] **Step 5: 落 ledger**

在 `docs/superpowers/notes/2026-08-18-b115-self-command-gate-ledger.md` 记：每个 task 的 commit 范围、Step 1-3 的真实输出（包数、FAIL 数）、Step 4 三处变异的翻红结果。**没有亲自跑到结果的命令不许写它的结论**；跑了但失败就贴原始报错原文。

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/notes/2026-08-18-b115-self-command-gate-ledger.md
git commit -m "chore: B115 总回归与变异测试记录"
```

---

## 交付后由审核者做（不在本计划范围）

以下需要驱动 agentd / 真实 executor，按 B126 的纪律归审核者，**执行者不要做**：

1. 真机验证：派一个 codex 任务，让它在 shell 里敲 `handoff dispatch`，确认工单被建出来且 `agentd.log` 里有 Warn 级「权限判定：升级人工」带 `rule=self-command`。
2. 反向验证：同一任务里敲 `handoff tasks`，确认**不**产生工单（落回原链，由审批者或黑名单处置）。
3. backlog 回填：B115 转 `✅ done`，挂 spec / plan / ledger 链接与上述真机证据。注意 B115 这行目前只在 `main` 上，前沿线是 `claude/windows-native-executor-distance-50942a`（B126），回填前先确定并账方案。
