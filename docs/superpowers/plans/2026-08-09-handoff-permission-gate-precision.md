# 权限门判定精度 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把权限判据从「对一整串展示文本做正则」换成「先结构化解析请求、再对字段判」，一次修掉 B23（叙述性文本误升级）与 B27（写文件类工具不设门）。

**Architecture:** 新增纯函数包 `internal/permgate` 承载全部判据，三个 adapter 共用一份实现。`executor.AdapterEvent` 增加可选结构化字段 `Perm`；manager 在 `handlePermission` 里调 `gate.Judge` 分流到 AutoAllow / Consult / Escalate 三个出口——后两个是今天已有的路径，只是判据变了。`Write`/`Edit` 从各 adapter 的 allow 规则挪进 ask，全部经权限门，由 handoff 判目标路径是否落在任务范围内。

**Tech Stack:** Go 1.x（标准库 `regexp` / `path/filepath` / `log/slog`），既有包 `internal/agentd`、`internal/executor`、`internal/config`。测试用标准 `testing`，表驱动。

## Global Constraints

以下条目来自 spec，**每个 task 的要求都隐含包含本节**：

- **fail-closed 无例外**：新增路径的任何失败（结构缺失、路径归一化失败、读任务失败）一律导向 `Escalate`，**没有任何一条会导向 `AutoAllow`**（spec §7）。
- **`AutoAllow` 只可能出自 `write` / `edit` 路由**；其余工具最宽只到 `Consult`。本次改动不放宽任何现有工具的裁决（spec §3.4）。
- **`AdapterEvent.Text` 一个字节都不改**：工单存全文、事件 payload 截断至 200 字这两条 B6 契约保持原样，不得重验（spec §3.3）。
- **`AutoAllow` 与审批者启用状态解耦**：`m.approver == nil` 或任务审批链已停用时，`AutoAllow` 仍然生效（spec §5.3）。
- **路径归属必须用 `filepath.Rel`，不得用字符串前缀**（`/repo-evil/x` 会被 `/repo` 前缀误判为内部）；必须对已存在的最长前缀求 `filepath.EvalSymlinks`（spec §5.1）。
- **不做审批记忆**、**不覆盖 Bash 重定向越界**（spec §2 非目标）。
- 日志一律用 `slog`，**禁止 `fmt.Printf`**；新文件必须有文件头注释（职责 + 边界），导出函数必须有 doc 注释。
- 每个 task 结束前跑 `gofmt -l .`（须无输出）与 `go vet ./...`。

---

## 文件结构

**新建：**

| 文件 | 职责 |
|---|---|
| `internal/permgate/permgate.go` | 包门面：`Action` / `Request` / `Scope` / `Verdict` / `Gate`、`New`、`Judge` 路由 |
| `internal/permgate/blacklist.go` | 黑名单：`builtinBlacklist`（自 approver.go 迁入）、引号剥离、执行包装器识别、命令类判据 |
| `internal/permgate/path.go` | 路径归一化与范围归属判定 |
| `internal/permgate/blacklist_test.go` | B23 的 9 条实证误判基线 + 真危险样本 + 绕过样本 |
| `internal/permgate/path_test.go` | 路径归属（含前缀陷阱、软链逃逸、相对路径、TaskDir） |
| `internal/permgate/permgate_test.go` | 路由与 §7 fail-closed 表逐行断言 |

**修改：**

| 文件 | 改动 |
|---|---|
| `internal/executor/executor.go` | 新增 `PermRequest` 类型、`PermTool*` 常量、`AdapterEvent.Perm` 字段 |
| `internal/agentd/approver.go` | 删 `builtinBlacklist` / `Blacklisted` / 黑名单编译；`approverPromptTemplate` 增补生产环境语义 |
| `internal/agentd/manager.go` | `Manager.gate` 字段与计数器、`NewManager` 签名、`handlePermission` 分流、`judgePermission`、`autoAllowPermission`、`shouldConsultApprover` 瘦身、`handleResult` 汇总日志 |
| `cmd/agentd.go` | 装配 `permgate.Gate` 并传入 `NewManager` |
| `internal/executor/claudecode/adapter.go` | `permText` 重构为同时产出 `Text` 与 `*PermRequest` |
| `internal/executor/claudecode/taskenv.go` | `Edit`/`Write` 移出 `allowRules`、移入 `askRules` |
| `internal/executor/grok/adapter.go` | `OnPermission` 提取结构 |
| `internal/executor/grok/taskenv.go` | `Edit`/`Write` 移出 `allowRules`、移入 `askRules` |
| `internal/executor/opencode/adapter.go` | `mapPermissionAsked` 提取结构 |
| `internal/executor/opencode/taskenv.go` | `Edit: "allow"` → `"ask"` |
| `docs/superpowers/backlog.md` | 收口 B23 / B27 |

---

## Task 1: 真机载荷探针取样 ✅ 已完成（2026-08-09）

> **本 task 已在 devbox 真机跑完，结论文档：`docs/superpowers/plans/2026-08-09-permission-payload-probe.md`。**
> 九份 testdata 已落盘并提交，Task 7/8/9 的字段名已按结论改写完毕，派发时**跳过本 task**。
> 探针结论推翻了两条计划假设：grok 的 `toolCall.kind` 分不出 Write/Edit（改用 `rawInput.variant`），
> opencode 的 `edit` 不该翻成 `ask`（越界写入已由 `external_directory` 拦住）。

**为什么必须先做**：grok 与 opencode 的文件类权限载荷形态**未知**（opencode 的 `edit` 现为 allow，从未产生过 edit 的 `permission.asked`，连样本都不存在）。本项目已经在 grok 的 `_x.ai/ask_user_question` 应答形态上猜错过两次，两次都靠真机才发现。本 task 只取样，**不改任何产品代码**。

**Files:**
- Create: `internal/executor/claudecode/testdata/perm_write.json`
- Create: `internal/executor/grok/testdata/perm_write.json`
- Create: `internal/executor/opencode/testdata/perm_edit.json`
- Create: `docs/superpowers/plans/2026-08-09-permission-payload-probe.md`（探针结论）

**Interfaces:**
- Consumes: 无
- Produces: 三份真实载荷样本 + 一份结论文档，Task 7/8/9 的字段名全部以它们为准

**远端约定**（与本项目既有做法一致）：devbox `100.73.238.21`，用户 `sycm`，免密 ssh，项目目录 `/Users/sycm/workspace/handoff`；agentd 跑在 tmux 会话 `agentd`，日志 tee 到 `~/.handoff/agentd.log`；本地 CLI 用 `--target devbox`。**homebrew 在 `/Users/sycm/.homebrew`，非交互 ssh 拿不到它**——要用 tmux 请写全路径，或直接 grep `~/.handoff/agentd.log`。

- [x] **Step 1: 在本地临时把三个 adapter 的写文件工具改成 ask（不提交）**

```bash
cd /Users/xushixin/workspace/handoff
# claude：Write 挪进 ask
perl -0pi -e 's/var askRules = \[\]string\{\n\t"Bash\(rm:\*\)"/var askRules = []string{\n\t"Write",\n\t"Edit",\n\t"Bash(rm:*)"/' internal/executor/claudecode/taskenv.go
# grok：Edit/Write 挪进 ask
perl -0pi -e 's/var allowRules = \[\]string\{"Edit", "Write"\}/var allowRules = []string{}/' internal/executor/grok/taskenv.go
perl -0pi -e 's/\t"WebFetch\(\*\)",/\t"WebFetch(*)",\n\t"Edit(*)",\n\t"Write(*)",/' internal/executor/grok/taskenv.go
# opencode：edit 改 ask
perl -0pi -e 's/Edit:              "allow",/Edit:              "ask",/' internal/executor/opencode/taskenv.go
git diff --stat
```

Expected: 三个文件都有改动。**这些改动稍后要还原，不要提交。**

- [x] **Step 2: 构建并部署到 devbox**

```bash
cd /Users/xushixin/workspace/handoff && go build -o /tmp/handoff-probe . && \
  scp /tmp/handoff-probe sycm@100.73.238.21:/Users/sycm/bin/handoff && \
  ssh sycm@100.73.238.21 'pkill -f "handoff agentd" || true'
```

注意：**不要用 `pkill -f "handoff agentd"` 之外的更宽模式**——本项目实证过 `pkill -f` 会匹配到 tmux server 自身保留的 argv，把整个 tmux server 连同所有 executor 会话一起杀掉（backlog B24）。

- [x] **Step 3: 重启 agentd 并确认起来了**

```bash
ssh sycm@100.73.238.21 '/Users/sycm/.homebrew/bin/tmux kill-session -t agentd 2>/dev/null; \
  /Users/sycm/.homebrew/bin/tmux new-session -d -s agentd \
  "handoff agentd 2>&1 | tee -a ~/.handoff/agentd.log"' && sleep 3 && \
  ssh sycm@100.73.238.21 'tail -5 ~/.handoff/agentd.log'
```

Expected: 日志出现 agentd 启动行。

- [x] **Step 4: 对每个 executor 各派发一个「只写一个文件」的任务**

计划正文（三个任务共用，写进本地临时文件后 dispatch）：

```
在仓库根目录用 Write 工具创建文件 probe.md，内容一行：probe
不要用 Bash 完成这件事，必须用 Write 工具。
完成后立即结束本回合，输出 {"summary":"已写 probe.md"}
```

```bash
cd /Users/xushixin/workspace/handoff
printf '在仓库根目录用 Write 工具创建文件 probe.md，内容一行：probe\n不要用 Bash 完成这件事，必须用 Write 工具。\n完成后立即结束本回合，输出 {"summary":"已写 probe.md"}\n' > /tmp/probe-plan.md
for ex in claude grok opencode; do
  handoff dispatch --target devbox --executor "$ex" --plan /tmp/probe-plan.md --new-worktree
done
```

Expected: 三个任务 id，各自随后进入 `waiting_answer`（写文件触发了权限门）。

- [x] **Step 5: 抓三份原始载荷**

各 adapter 记录载荷的位置不同，三条都要看：

```bash
# claude：perm.sock 的 ask 帧 —— agentd 日志有 tool_name，原始 input 需从任务目录的
# out.jsonl 里按 tool_use_id 反查
ssh sycm@100.73.238.21 'grep "收到权限裁决请求" ~/.handoff/agentd.log | tail -3'
# grok：ACP session/request_permission 原文在任务目录的 serve.log
ssh sycm@100.73.238.21 'ls -t ~/.handoff/tasks/*/serve.log | head -1 | xargs grep -o "\"method\":\"session/request_permission\".*" | tail -1'
# opencode：permission.asked 的 SSE 原文 —— agentd 日志只有拼好的描述，
# 需要 curl serve 的 /event 或看 opencode 自身日志
ssh sycm@100.73.238.21 'grep "adapter 产出权限事件" ~/.handoff/agentd.log | tail -3'
```

把三份**原始 JSON 载荷**分别存为：
- `internal/executor/claudecode/testdata/perm_write.json`
- `internal/executor/grok/testdata/perm_write.json`
- `internal/executor/opencode/testdata/perm_edit.json`

- [x] **Step 6: 顺带验证 opencode 的 `external_directory: "ask"` 是否真的拦得住绝对路径写入**

对 opencode 那个任务追加一轮指令（spec §1.3 的待验项）：

```bash
handoff reply <opencode-task-id> --target devbox --ticket <ticket-id> --answer "allow"
# 等它写完 probe.md 后：
handoff continue <opencode-task-id> --target devbox --message '用 Write 工具在 /tmp/probe-outside.md 写一行 outside，然后结束本回合'
```

Expected（二选一，都要如实记录）：任务再次进 `waiting_answer`（说明 `external_directory` 生效），或直接写成功无工单（说明它拦不住绝对路径写入）。

- [x] **Step 7: 归档三个探针任务并还原临时改动**

```bash
for id in <三个任务 id>; do handoff done "$id" --target devbox; done
cd /Users/xushixin/workspace/handoff && git checkout -- \
  internal/executor/claudecode/taskenv.go \
  internal/executor/grok/taskenv.go \
  internal/executor/opencode/taskenv.go
git status --short
```

Expected: 三个 taskenv.go 干净，只剩三份 testdata 是新增。

- [x] **Step 8: 写结论文档**

`docs/superpowers/plans/2026-08-09-permission-payload-probe.md` 必须逐 adapter 回答四个问题：

1. 工具名在载荷的哪个字段、取值原文是什么？
2. 目标路径在哪个字段、是绝对路径还是相对路径？
3. 一次请求是否可能带多个路径？
4. **该 adapter 能否可靠提取路径？** —— 若否，按 spec §6.1 条件性回退：该 adapter 的 `Write`/`Edit` 保持 `allow` 不变，本文档写明原因，Task 10 向 backlog 追加一条该 adapter 的载荷缺口条目。

外加一条：opencode 的 `external_directory: "ask"` 对绝对路径写入是否生效（Step 6 的结论）。

- [x] **Step 9: 提交**

```bash
cd /Users/xushixin/workspace/handoff
git add internal/executor/*/testdata/perm_*.json docs/superpowers/plans/2026-08-09-permission-payload-probe.md
git commit -m "test(perm): 三个 adapter 的写文件权限载荷真机取样

grok/opencode 的文件类权限载荷形态此前无任何样本（opencode 的 edit 是
allow，从未产生过 edit 的 permission.asked）。本项目在 grok 的
ask_user_question 应答形态上猜错过两次，字段名不再靠推断。"
```

---

## Task 2: permgate 骨架与命令类黑名单判据（B23）

**Files:**
- Create: `internal/permgate/permgate.go`
- Create: `internal/permgate/blacklist.go`
- Test: `internal/permgate/blacklist_test.go`

**Interfaces:**
- Consumes: 无（纯新包）
- Produces:
  - `permgate.Action`（`AutoAllow` / `Consult` / `Escalate`）、`Action.String() string`
  - `permgate.Request{Tool, Text, Command string; Paths []string; Truncated bool}`
  - `permgate.Scope{Workdir, TaskDir string}`
  - `permgate.Verdict{Action Action; Reason, Rule string}`
  - `permgate.Gate`、`func New(patterns []string, log *slog.Logger) (*Gate, error)`
  - `func StripQuoted(s string) string`、`func HasExecWrapper(s string) bool`
  - `func (g *Gate) judgeCommand(s string) Verdict`（包内）

- [ ] **Step 1: 写失败测试——B23 的 9 条实证误判基线**

`internal/permgate/blacklist_test.go`：

```go
package permgate

import (
	"log/slog"
	"testing"
)

// newTestGate 造一个只带内置黑名单的 Gate。
func newTestGate(t *testing.T) *Gate {
	t.Helper()
	g, err := New(nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

// TestNarrativeTextNoLongerEscalates 锁死 B23 的验收基线。
//
// 这 9 条是 2026-08-09 用当时的 builtinBlacklist 原文实测出的**全部误命中**
// （spec §1.2），一条都不能少：少一条就是把已证实的误升级悄悄放回去。
func TestNarrativeTextNoLongerEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: git commit -m "fix: 清理逻辑不再误删，去掉 rm -rf 分支"`,
		`Bash: git commit -m "docs: 说明 production 部署流程"`,
		`Bash: go test ./internal/prod/...`,
		`Bash: grep -rn "sudo" internal/`,
		`Bash: cat docs/production-checklist.md`,
		`Bash: npm run build:prod`,
		`Write: /repo/docs/production.md`,
		`Bash: go run ./cmd --note "drop table 是危险操作"`,
		`Bash: echo "见 README：git reset --hard 会丢改动" >> notes.md`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action == Escalate {
			t.Errorf("叙述性文本不应硬升级\n  输入: %s\n  命中规则: %s\n  理由: %s",
				c, v.Rule, v.Reason)
		}
	}
}

// TestRealDangerStillEscalates 反向守卫：修误判不得放松真危险的拦截。
func TestRealDangerStillEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: rm -rf /tmp/x`,
		`Bash: rm --recursive --force /tmp/x`,
		`Bash: sudo systemctl restart nginx`,
		`Bash: git -C /repo push --force origin main`,
		`Bash: git reset --hard HEAD~3`,
		`Bash: psql -c 'drop table users'`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action != Escalate {
			t.Errorf("真危险命令必须硬升级\n  输入: %s\n  实得: %s（%s）", c, v.Action, v.Reason)
		}
	}
}

// TestQuoteBypassStillEscalates 堵引号绕过：剥离后干净但含执行包装器的，
// 不许降级为 Consult（spec §4.1 第二行）。
func TestQuoteBypassStillEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: sh -c "rm -rf /"`,
		`Bash: bash -c 'sudo rm -rf /var'`,
		`Bash: eval "$DANGER"`,
		`Bash: echo x | xargs rm -rf`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action != Escalate {
			t.Errorf("引号绕过形态必须硬升级\n  输入: %s\n  实得: %s（%s）", c, v.Action, v.Reason)
		}
	}
}

// TestCleanCommandGoesToApprover 未命中黑名单的普通命令走审批者，形状不变。
func TestCleanCommandGoesToApprover(t *testing.T) {
	g := newTestGate(t)
	if v := g.judgeCommand(`Bash: go build ./...`); v.Action != Consult {
		t.Fatalf("干净命令应交审批者，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestStripQuoted 逐条锁死剥离语义。
func TestStripQuoted(t *testing.T) {
	cases := []struct{ in, want string }{
		{`git commit -m "去掉 rm -rf 分支"`, `git commit -m ""`},
		{`echo 'sudo x' > a`, `echo '' > a`},
		{`rm -rf /tmp/x`, `rm -rf /tmp/x`},
		{`echo "a" b 'c'`, `echo "" b ''`},
	}
	for _, c := range cases {
		if got := StripQuoted(c.in); got != c.want {
			t.Errorf("StripQuoted(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestHasExecWrapper 确认包装器识别不会被 push/ssh 这类含 sh 的词误伤。
func TestHasExecWrapper(t *testing.T) {
	yes := []string{`sh -c "x"`, `bash -c 'x'`, `zsh -c x`, `eval x`, `xargs rm`}
	no := []string{`git push origin main`, `ssh host ls`, `echo shell`}
	for _, s := range yes {
		if !HasExecWrapper(s) {
			t.Errorf("应识别为执行包装器: %q", s)
		}
	}
	for _, s := range no {
		if HasExecWrapper(s) {
			t.Errorf("不应识别为执行包装器: %q", s)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/`
Expected: 编译失败，`undefined: New` / `undefined: Gate` 等——包还不存在。

- [ ] **Step 3: 写 permgate.go（类型与 Gate 骨架）**

```go
// Package permgate 提供权限请求的结构化判据。
//
// 职责：
//   - 把一次权限请求判成三个出口之一：AutoAllow（立即放行）、Consult
//     （交廉价模型审批者）、Escalate（直接升级人工审核者）
//   - 承载全部判定规则：黑名单模式匹配、引号剥离、执行包装器识别、
//     写文件目标路径的范围归属
//
// 边界：
//   - 纯计算，无 I/O：不写 store、不碰 adapter、不发网络请求（EvalSymlinks
//     的文件系统只读探测除外，它是路径判定的必需品）
//   - 无 deny 权：出口里没有「拒绝」——拒绝只有人能做，与 approver 同源
//   - 不做状态迁移、不建工单：调用方（manager）据 Verdict 决定后续动作
//
// 为什么判据要独立成包：三个 adapter（claude/grok/opencode）的权限载荷形态
// 完全不同，但判据必须只有一份——判据分散到 adapter 里，就会重演「opencode
// 有 external_directory、claude 和 grok 没有」这种各家一套的漂移。
package permgate

import (
	"fmt"
	"log/slog"
	"regexp"
)

// Action 是一次权限裁决的出口。
type Action int

const (
	// AutoAllow 立即放行：不建工单、不发事件、不唤醒任何人。
	// 只可能出自 write/edit 路由（目标路径全部落在任务范围内）。
	AutoAllow Action = iota
	// Consult 交廉价模型审批者裁决（今天未命中黑名单时的默认路径）。
	Consult
	// Escalate 直接升级人工审核者（今天黑名单命中时的路径）。
	Escalate
)

// String 给出日志可读的短标签。
func (a Action) String() string {
	switch a {
	case AutoAllow:
		return "auto_allow"
	case Consult:
		return "consult"
	default:
		return "escalate"
	}
}

// Request 是结构化后的权限请求。
//
// Text 与 Command 不是重复：Text 是给人看的全文（与工单同源，形如
// "Bash: xxx"），Command 是命令类工具的纯命令串。bash 路由判 Command，
// 其余路由退回判 Text。
type Request struct {
	Tool      string   // 归一化工具名，取 executor.PermTool* 常量
	Text      string   // 权限描述全文（与工单同源）
	Command   string   // Tool=bash 时的完整命令串
	Paths     []string // Tool=write|edit 时的目标路径（可为相对路径）
	Truncated bool     // 描述含 executor.TruncationMarker
}

// Scope 是本任务的合法作用范围。
//
// 两处都是 handoff 分配给该任务的空间：Workdir 是它要改的仓库/worktree，
// TaskDir 是 agentd 给它的 0700 私有目录。写这两处不该叫醒任何人。
type Scope struct {
	Workdir string
	TaskDir string
}

// Verdict 是裁决结果。
//
//   - Reason: 可读理由，进日志与审计
//   - Rule: 因黑名单而 Escalate 时命中的规则原文；其余情形为空
type Verdict struct {
	Action Action
	Reason string
	Rule   string
}

// Gate 持有编译后的黑名单，是判据的唯一入口。
type Gate struct {
	blacklist []*regexp.Regexp
	log       *slog.Logger
}

// New 构造判据网关。
//
// 参数：
//   - patterns: 用户自定义黑名单（config.ApproverConfig.Blacklist）；内置
//     黑名单自动前置，无需调用方传入
//   - log: 包日志入口；nil 时用 slog.Default()
//
// 返回：
//   - 可用的 Gate；任一正则编译失败即返回错误（配置错误应在启动期暴露）
//
// 注意：
//   - **无论审批者是否启用都必须构造**。AutoAllow 是第 0 层静态判据、不是
//     审批者的职权；漏构造会让未配置审批者的部署被工作区内的每次写入淹没
func New(patterns []string, log *slog.Logger) (*Gate, error) {
	if log == nil {
		log = slog.Default()
	}
	all := append([]string(nil), builtinBlacklist...)
	all = append(all, patterns...)
	rx := make([]*regexp.Regexp, 0, len(all))
	for _, p := range all {
		r, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("编译黑名单正则 %q: %w", p, err)
		}
		rx = append(rx, r)
	}
	log.Info("权限判据网关已就绪", "builtin_rules", len(builtinBlacklist),
		"custom_rules", len(patterns))
	return &Gate{blacklist: rx, log: log}, nil
}

// match 返回是否命中黑名单及命中的规则原文。
func (g *Gate) match(s string) (bool, string) {
	for _, r := range g.blacklist {
		if r.MatchString(s) {
			return true, r.String()
		}
	}
	return false, ""
}
```

- [ ] **Step 4: 写 blacklist.go（内置规则 + 剥离 + 包装器 + 命令类判据）**

```go
// blacklist.go —— 黑名单硬规则与命令类判据。
//
// 职责：
//   - 持有内置黑名单模式表（自 internal/agentd/approver.go 迁入）
//   - 引号字面量剥离与执行包装器识别
//   - judgeCommand：命令类请求（bash 及其余工具）的判定
//
// 边界：
//   - 不认识工具名、不碰路径：路由在 permgate.go，路径判定在 path.go
package permgate

import (
	"regexp"
	"strings"
)

// builtinBlacklist 是内置黑名单（(?i) 不区分大小写编译）。
//
// 拦截意图（逐条）：
//   - rm 破坏性删除：短连写（-rf/-fr）与分写（-r ... -f）之外，还要拦长选项
//     （--recursive/--force）与长短混写——`rm --recursive --force /`、`rm -r --force`
//     都是脚本常规写法，只认短连写会被绕过。两段式匹配：rm 后同时含「递归标志」
//     与「强制标志」即命中（两种顺序各一条；RE2 不支持 lookahead）
//   - git push 强推：`git\b.*\bpush\b` 而非 `git\s+push`——`git -C /repo push
//     --force origin main` 的 -C 会插在中间；--force\b 同时覆盖 --force-with-lease
//   - sudo：提权命令影响整个主机，绝不能由廉价模型自动放行
//   - git reset --hard：丢弃未提交改动，不可逆
//   - drop table/database：删库删表不可恢复
//
// 曾经有过第 9 条 `\bproduction\b|\bprod\b`，2026-08-09 删除（spec §4.2）：
// 它想拦「操作生产环境」，实现成的却是「文本里出现 prod 字样」——实测 9 条
// 误命中里有 4 条出自它（`go test ./internal/prod/...`、`npm run build:prod`、
// `Write: /repo/docs/production.md`、`cat docs/production-checklist.md`）。
// 而 agent 跑在 worktree 里，接触生产的途径是 `kubectl -n prod` 这类命令形态，
// 不是关键词。正则分不出这两者，廉价模型分得出——该语义已移入
// approverPromptTemplate。用户仍可经 config 的 approver.blacklist 自定义补回。
var builtinBlacklist = []string{
	`rm\s+-[a-z]*[rf][a-z]*[rf]`,                                  // rm -rf / -fr 常见连写
	`rm\s+-[rf]\b.*\s-[rf]\b`,                                     // rm -r ... -f 分写
	`rm\b[^;&\n]*(--recursive|-r\b|-R\b)[^;&\n]*(--force\b|-f\b)`, // 递归段在前
	`rm\b[^;&\n]*(--force\b|-f\b)[^;&\n]*(--recursive|-r\b|-R\b)`, // 强制段在前
	`git\b.*\bpush\b.*(--force\b|-f\b)`,                           // force push
	`\bsudo\b`,
	`git\s+reset\s+--hard`,
	`drop\s+(table|database)`,
}

// execWrapperRx 识别「把命令藏进字符串再执行」的包装器。
//
// 为什么需要它：黑名单改为对剥离引号后的文本匹配之后，`sh -c "rm -rf /"`
// 剥完就干净了。命中这条即恢复硬拦，不给绕过留口子。
// 用 \b 边界而非 strings.Contains：`git push` 的 "sh"、`ssh host` 的 "sh"
// 前后都是词字符，不会误伤（TestHasExecWrapper 锁死）。
var execWrapperRx = regexp.MustCompile(`(?i)\b(sh|bash|zsh|dash|ksh|env)\b[^|;&]*\s-c\b|\beval\b|\bxargs\b`)

// HasExecWrapper 判断命令是否含执行包装器（sh -c / bash -c / eval / xargs 等）。
func HasExecWrapper(s string) bool { return execWrapperRx.MatchString(s) }

// StripQuoted 把成对引号内的内容清空，保留引号本身。
//
// 参数：s 为原始命令串
// 返回：剥离后的串，如 `git commit -m "去掉 rm -rf 分支"` → `git commit -m ""`
//
// 注意：
//   - 不做完整 shell 解析。反斜杠转义（`"it\"s"`）会让本函数提前认为引号闭合，
//     结果是**剥得更少**——剥得少意味着更可能仍然命中黑名单、更可能 Escalate，
//     方向是安全的，因此接受
//   - 未闭合引号：引号之后的内容全部丢弃
func StripQuoted(s string) string {
	var b strings.Builder
	var quote rune // 0 = 不在引号内
	for _, r := range s {
		switch {
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			b.WriteRune(r)
		case quote != 0 && r == quote:
			quote = 0
			b.WriteRune(r)
		case quote != 0:
			// 引号内字面量：丢弃
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// judgeCommand 判定命令类请求（spec §4.1 的四条规则）。
//
// 参数：s 为待判文本（bash 路由传 Command，其余路由传 Text）
// 返回：Consult 或 Escalate；本函数**永不返回 AutoAllow**
//
// 规则：
//   - 原文不命中               → Consult（与改动前一致）
//   - 剥离引号后仍命中         → Escalate（真危险）
//   - 剥离后不命中但含包装器   → Escalate（不给引号绕过留口子）
//   - 剥离后不命中且无包装器   → Consult（仅引号内字面量命中，降级交模型）
//
// 最后一条是本设计的核心取舍：误判的修法不是「直接放行」，而是「从硬拦
// 降级为让模型看一眼」——未枚举到的绕过形态最坏也只落到廉价模型手上。
func (g *Gate) judgeCommand(s string) Verdict {
	hit, rule := g.match(s)
	if !hit {
		return Verdict{Action: Consult, Reason: "黑名单未命中"}
	}
	if h2, r2 := g.match(StripQuoted(s)); h2 {
		return Verdict{Action: Escalate, Reason: "剥离引号字面量后仍命中黑名单", Rule: r2}
	}
	if HasExecWrapper(s) {
		return Verdict{Action: Escalate,
			Reason: "命中黑名单且含执行包装器，不排除引号内藏命令", Rule: rule}
	}
	return Verdict{Action: Consult,
		Reason: "仅引号内字面量命中黑名单，降级交审批者裁决", Rule: rule}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/permgate/ -v -run 'Narrative|RealDanger|QuoteBypass|CleanCommand|StripQuoted|HasExecWrapper'`
Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/permgate/
git add internal/permgate/
git commit -m "feat(permgate): 命令类黑名单判据改为剥离引号字面量后匹配

B23：叙述性文本（提交信息、注释、grep 关键词）不再触发硬升级。9 条实测
误命中样本进测试作验收基线。误判的修法不是直接放行，而是从硬拦降级为
Consult——未枚举到的引号绕过形态最坏也只落到廉价模型手上；含执行包装器
（sh -c/eval/xargs）时恢复硬拦。

同时删除内置规则 prod|production：9 条误命中里 4 条出自它，该语义移交
审批者 prompt。"
```

---

## Task 3: 路径范围归属判定（B27 判据）

**Files:**
- Create: `internal/permgate/path.go`
- Test: `internal/permgate/path_test.go`

**Interfaces:**
- Consumes: `permgate.Scope`（Task 2 定义）
- Produces: `func InScope(path string, scope Scope) (in bool, base string, err error)`

- [ ] **Step 1: 写失败测试**

`internal/permgate/path_test.go`：

```go
package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInScopeAccepts 范围内的路径都应判为 in。
func TestInScopeAccepts(t *testing.T) {
	work := t.TempDir()
	task := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: task}
	cases := []string{
		filepath.Join(work, "main.go"),
		filepath.Join(work, "internal", "a", "b.go"), // 目录尚不存在
		"main.go",                                    // 相对路径按 Workdir 解析
		"./internal/x.go",
		filepath.Join(task, "notes.md"),
	}
	for _, p := range cases {
		in, base, err := InScope(p, sc)
		if err != nil {
			t.Fatalf("InScope(%q) 报错: %v", p, err)
		}
		if !in {
			t.Errorf("应判为范围内: %q", p)
			continue
		}
		if base == "" {
			t.Errorf("范围内必须回报命中的基准目录: %q", p)
		}
	}
}

// TestInScopeRejectsPrefixTrap 锁死「不得用字符串前缀」这条约束。
//
// /repo-evil 以 /repo 开头，strings.HasPrefix 会把它误判成仓库内部。
func TestInScopeRejectsPrefixTrap(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "repo")
	evil := filepath.Join(root, "repo-evil")
	for _, d := range []string{work, evil} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("建目录 %s: %v", d, err)
		}
	}
	in, _, err := InScope(filepath.Join(evil, "x.go"), Scope{Workdir: work})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("repo-evil 不是 repo 的子目录，必须判为越界（前缀匹配的经典陷阱）")
	}
}

// TestInScopeRejectsSymlinkEscape 锁死软链逃逸。
//
// 仓库里放一个指向仓库外的软链，经它写出去必须判越界，否则
// `ln -s ~ /repo/link` 之后写 /repo/link/.ssh/authorized_keys 就绕过了。
func TestInScopeRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{work, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("建目录 %s: %v", d, err)
		}
	}
	link := filepath.Join(work, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("建软链: %v", err)
	}
	in, _, err := InScope(filepath.Join(link, "pwned"), Scope{Workdir: work})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("经软链写到仓库外必须判越界")
	}
}

// TestInScopeRejectsOutside 常见的宿主机敏感路径必须判越界。
func TestInScopeRejectsOutside(t *testing.T) {
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir()}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("取 home: %v", err)
	}
	cases := []string{
		filepath.Join(home, ".ssh", "authorized_keys"),
		filepath.Join(home, ".zshrc"),
		"/etc/hosts",
		filepath.Join(work, "..", "escape.go"), // 相对回退
	}
	for _, p := range cases {
		in, _, err := InScope(p, sc)
		if err != nil {
			t.Fatalf("InScope(%q) 报错: %v", p, err)
		}
		if in {
			t.Errorf("必须判为越界: %q", p)
		}
	}
}

// TestInScopeEmptyBaseIgnored TaskDir 为空时不参与判定，不得因此把任何
// 路径判成范围内。
func TestInScopeEmptyBaseIgnored(t *testing.T) {
	work := t.TempDir()
	in, _, err := InScope("/etc/hosts", Scope{Workdir: work, TaskDir: ""})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("TaskDir 为空时不得放宽判定")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run InScope`
Expected: 编译失败 `undefined: InScope`。

- [ ] **Step 3: 写 path.go**

```go
// path.go —— 写文件目标路径的范围归属判定。
//
// 职责：
//   - 把可能是相对路径、可能经软链的目标路径归一化为真实绝对路径
//   - 判定它是否落在任务范围（Workdir 或 TaskDir）的子树内
//
// 边界：
//   - 只读文件系统（EvalSymlinks 探测），不创建、不修改任何东西
//   - 不认识工具名、不做黑名单匹配
//
// 已知残余风险（TOCTOU）：判定通过后、executor 实际写入前，软链可能被换掉。
// 闭合它需要在 executor 侧持有文件句柄，而写入动作发生在 agent 进程里，
// 超出 handoff 的可控范围——spec §5.4 明确接受此风险。
package permgate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// InScope 判定目标路径是否落在任务范围内。
//
// 参数：
//   - path: 目标路径，可为相对路径（按 scope.Workdir 解析）
//   - scope: 任务范围；其中为空的基准目录被跳过，不参与判定
//
// 返回：
//   - in: 是否落在范围内
//   - base: in=true 时命中的基准目录（归一化后），供日志说明「凭哪条放行」
//   - err: 路径归一化失败；调用方须按 fail-closed 处理为升级人工
//
// 注意：
//   - 用 filepath.Rel 判归属而非字符串前缀——strings.HasPrefix("/repo-evil/x",
//     "/repo") 为真，前缀匹配会把仓库外的路径判成内部
//   - 对已存在的最长前缀求 EvalSymlinks——目标文件常常尚不存在（Write 新建），
//     不解软链则 `ln -s ~ /repo/link` 之后写 /repo/link/.ssh/authorized_keys
//     直接绕过
func InScope(path string, scope Scope) (in bool, base string, err error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(scope.Workdir, p)
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return false, "", fmt.Errorf("绝对化目标路径 %q: %w", path, err)
	}
	p = resolveExistingPrefix(p)

	for _, b := range []string{scope.Workdir, scope.TaskDir} {
		if b == "" {
			continue
		}
		rb, aerr := filepath.Abs(b)
		if aerr != nil {
			return false, "", fmt.Errorf("绝对化基准目录 %q: %w", b, aerr)
		}
		rb = resolveExistingPrefix(rb)
		rel, rerr := filepath.Rel(rb, p)
		if rerr != nil {
			// 跨卷等无法求相对路径的情形：视作不在该基准内，继续判下一个
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, rb, nil
		}
	}
	return false, "", nil
}

// resolveExistingPrefix 对路径中「已存在的最长前缀」求 EvalSymlinks，
// 再把剩余不存在的部分接回去。
//
// 为什么不能直接 EvalSymlinks(p)：Write 创建新文件时目标路径尚不存在，
// EvalSymlinks 会直接报错，那样每一次新建文件都会走 fail-closed 升级人工。
//
// 到根都解不动时原样返回——此时没有软链可解，原路径即真实路径。
func resolveExistingPrefix(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/permgate/ -v -run InScope`
Expected: 5 个用例全部 PASS。

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/permgate/
git add internal/permgate/path.go internal/permgate/path_test.go
git commit -m "feat(permgate): 写文件目标路径的范围归属判定

用 filepath.Rel 而非字符串前缀（/repo-evil 会被 /repo 前缀误判为内部），
对已存在的最长前缀求 EvalSymlinks（否则 ln -s ~ /repo/link 之后写
/repo/link/.ssh/authorized_keys 直接绕过）。目标文件常常尚不存在，
所以只能解已存在的前缀。TOCTOU 残余风险按 spec §5.4 接受。"
```

---

## Task 4: Judge 路由与 fail-closed 表

**Files:**
- Modify: `internal/permgate/permgate.go`（追加 `Judge` 与 `judgeFileWrite`）
- Test: `internal/permgate/permgate_test.go`

**Interfaces:**
- Consumes: `judgeCommand`（Task 2）、`InScope`（Task 3）
- Produces: `func (g *Gate) Judge(req Request, scope Scope) Verdict`

- [ ] **Step 1: 写失败测试——spec §7 fail-closed 表逐行**

`internal/permgate/permgate_test.go`：

```go
package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJudgeFailClosedTable 把 spec §7 的 fail-closed 表逐行钉死。
//
// 表里没有任何一行导向 AutoAllow——这是整个设计的支点，一旦有人加了新的
// 提前返回并误落到 AutoAllow，本用例必须红。
func TestJudgeFailClosedTable(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir()}
	cases := []struct {
		name string
		req  Request
	}{
		{"描述含截断标记", Request{Tool: "bash", Text: "Bash: x", Command: "x", Truncated: true}},
		{"写文件但无路径", Request{Tool: "write", Text: "Write: ?"}},
		{"路径越界", Request{Tool: "write", Text: "Write: /etc/hosts", Paths: []string{"/etc/hosts"}}},
		{"多路径任一越界", Request{Tool: "edit", Text: "Edit: x",
			Paths: []string{filepath.Join(work, "a.go"), "/etc/hosts"}}},
		{"写文件描述命中黑名单", Request{Tool: "write", Text: "Write: /x/sudoers-sudo",
			Paths: []string{filepath.Join(work, "a.go")}}},
		{"剥离后仍命中", Request{Tool: "bash", Text: "Bash: rm -rf /", Command: "rm -rf /"}},
		{"含执行包装器", Request{Tool: "bash", Text: `Bash: sh -c "rm -rf /"`, Command: `sh -c "rm -rf /"`}},
	}
	for _, c := range cases {
		if v := g.Judge(c.req, sc); v.Action != Escalate {
			t.Errorf("%s：必须 Escalate，实得 %s（%s）", c.name, v.Action, v.Reason)
		}
	}
}

// TestJudgeAutoAllowOnlyForInScopeWrites AutoAllow 的唯一合法来源。
func TestJudgeAutoAllowOnlyForInScopeWrites(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	task := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: task}
	ok := []Request{
		{Tool: "write", Text: "Write: main.go", Paths: []string{"main.go"}},
		{Tool: "edit", Text: "Edit: " + filepath.Join(work, "a.go"),
			Paths: []string{filepath.Join(work, "a.go")}},
		{Tool: "write", Text: "Write: notes.md", Paths: []string{filepath.Join(task, "notes.md")}},
	}
	for _, r := range ok {
		if v := g.Judge(r, sc); v.Action != AutoAllow {
			t.Errorf("范围内写入应自动放行：%v，实得 %s（%s）", r.Paths, v.Action, v.Reason)
		}
	}
}

// TestJudgeNeverAutoAllowsNonFileTools 非写文件工具永远拿不到 AutoAllow。
//
// 本次改动不放宽任何现有工具的裁决——bash 再干净也只到 Consult。
func TestJudgeNeverAutoAllowsNonFileTools(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir()}
	reqs := []Request{
		{Tool: "bash", Text: "Bash: go build ./...", Command: "go build ./..."},
		{Tool: "webfetch", Text: "WebFetch: https://example.com"},
		{Tool: "other", Text: "SomeTool: whatever"},
	}
	for _, r := range reqs {
		if v := g.Judge(r, sc); v.Action == AutoAllow {
			t.Errorf("非写文件工具不得自动放行：%s", r.Text)
		}
	}
}

// TestJudgeNormalizationFailureEscalates 路径归一化失败走 fail-closed。
//
// 用一个 NUL 字节构造必然失败的路径——EvalSymlinks/Abs 都无法处理。
func TestJudgeNormalizationFailureEscalates(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	// 目标路径落在一个「父目录是普通文件」的位置：EvalSymlinks 解不动，
	// resolveExistingPrefix 会退回原路径，最终仍应判越界或归一化失败——
	// 两者都是 Escalate。
	f := filepath.Join(work, "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	v := g.Judge(Request{Tool: "write", Text: "Write: x",
		Paths: []string{filepath.Join(f, "..", "..", "outside.go")}}, Scope{Workdir: work})
	if v.Action == AutoAllow {
		t.Fatalf("越出 Workdir 的路径不得自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/permgate/ -run Judge`
Expected: 编译失败 `g.Judge undefined`。

- [ ] **Step 3: 在 permgate.go 追加 Judge 与 judgeFileWrite**

```go
// Judge 判定一次权限请求（spec §3.4 的路由 + §7 的 fail-closed 表）。
//
// 参数：
//   - req: 结构化后的权限请求
//   - scope: 本任务的合法作用范围
//
// 返回：Verdict，调用方据 Action 决定后续动作
//
// 路由：
//   - write / edit → 路径归属判定，**并且**对 Text 跑一次黑名单（路径本身
//     可能命中，如 Write: /etc/sudoers）；两项是与关系
//   - bash        → 对 Command 做命令类判定
//   - 其余        → 对 Text 做命令类判定
//
// 注意：
//   - Truncated 一律直接 Escalate 且不再往下判：看到的是不完整的描述，
//     危险片段可能落在截断之外，黑名单与模型都不可信
//   - 本方法**永不因失败而返回 AutoAllow**（spec §7）
func (g *Gate) Judge(req Request, scope Scope) Verdict {
	if req.Truncated {
		return Verdict{Action: Escalate,
			Reason: "权限描述含截断标记，危险片段可能落在截断之外"}
	}
	switch req.Tool {
	case executor.PermToolWrite, executor.PermToolEdit:
		return g.judgeFileWrite(req, scope)
	case executor.PermToolBash:
		return g.judgeCommand(req.Command)
	default:
		return g.judgeCommand(req.Text)
	}
}

// judgeFileWrite 判定写文件类请求：黑名单与路径归属都通过才自动放行。
//
// 返回：AutoAllow 或 Escalate；本函数**永不返回 Consult**——写文件的危险
// 判定是确定性的（路径在不在范围内），不需要模型介入。
func (g *Gate) judgeFileWrite(req Request, scope Scope) Verdict {
	if len(req.Paths) == 0 {
		return Verdict{Action: Escalate,
			Reason: "写文件请求未能提取出目标路径，无法判定范围"}
	}
	if hit, rule := g.match(req.Text); hit {
		return Verdict{Action: Escalate,
			Reason: "写文件描述命中黑名单", Rule: rule}
	}
	for _, p := range req.Paths {
		in, base, err := InScope(p, scope)
		if err != nil {
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径归一化失败 %q: %v", p, err)}
		}
		if !in {
			return Verdict{Action: Escalate,
				Reason: fmt.Sprintf("目标路径越出任务范围: %s", p)}
		}
		g.log.Debug("写入路径在任务范围内", "path", p, "base", base)
	}
	return Verdict{Action: AutoAllow, Reason: "全部目标路径落在任务范围内"}
}
```

同时给 `permgate.go` 的 import 块加上 `"github.com/xushixin/handoff/internal/executor"`。
（依赖方向：permgate → executor，executor 不反向依赖 permgate，无环。）

- [ ] **Step 4: 跑全包测试确认通过**

Run: `go test ./internal/permgate/ -v`
Expected: 全部 PASS（Task 2/3/4 的用例都在）。

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/permgate/
git add internal/permgate/
git commit -m "feat(permgate): Judge 路由与 fail-closed 表

write/edit 走路径归属且同时判黑名单（两项与关系），bash 判 Command，
其余判 Text。AutoAllow 只可能出自 write/edit——非写文件工具再干净也只到
Consult，本次改动不放宽任何现有工具的裁决。spec §7 的 fail-closed 表
逐行进测试。"
```

---

## Task 5: executor 侧的结构化契约

**Files:**
- Modify: `internal/executor/executor.go`
- Test: `internal/executor/executor_test.go`（若不存在则新建）

**Interfaces:**
- Consumes: 无
- Produces:
  - `executor.PermRequest{Tool, Command string; Paths []string}`
  - 常量 `PermToolBash = "bash"`、`PermToolWrite = "write"`、`PermToolEdit = "edit"`、`PermToolWebFetch = "webfetch"`、`PermToolOther = "other"`
  - `func NormalizePermTool(raw string) string`
  - `AdapterEvent.Perm *PermRequest` 字段

- [ ] **Step 1: 写失败测试**

在 `internal/executor/executor_test.go`：

```go
package executor

import "testing"

// TestNormalizePermTool 锁死归一化映射。
//
// 只收本项目实际见过的工具名——不猜 grok/opencode 的别名，那两个的真实
// 取值由 Task 1 的真机探针给出，各自在 adapter 侧补进本表。
func TestNormalizePermTool(t *testing.T) {
	cases := map[string]string{
		"Bash":     PermToolBash,
		"bash":     PermToolBash,
		"  Bash  ": PermToolBash,
		"Write":    PermToolWrite,
		"Edit":     PermToolEdit,
		"WebFetch": PermToolWebFetch,
		"Glob":     PermToolOther,
		"":         PermToolOther,
	}
	for raw, want := range cases {
		if got := NormalizePermTool(raw); got != want {
			t.Errorf("NormalizePermTool(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestAdapterEventPermOptional Perm 是可选字段：不填时为 nil，
// manager 据此走 fail-closed 升级。
func TestAdapterEventPermOptional(t *testing.T) {
	if (AdapterEvent{Type: "permission"}).Perm != nil {
		t.Fatal("未填写的 Perm 必须为 nil")
	}
	ev := AdapterEvent{Type: "permission",
		Perm: &PermRequest{Tool: PermToolWrite, Paths: []string{"/x"}}}
	if ev.Perm.Tool != PermToolWrite || len(ev.Perm.Paths) != 1 {
		t.Fatal("Perm 字段未按预期携带结构")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/ -run 'NormalizePermTool|AdapterEventPerm'`
Expected: 编译失败 `undefined: PermToolBash` 等。

- [ ] **Step 3: 在 executor.go 里加类型、常量与字段**

在 `AdapterEvent` 定义之前插入：

```go
// 归一化工具名：各 adapter 的原始工具名折算到这一组常量，permgate 只认它们。
//
// 为什么要归一化：三个 executor 对同一件事的叫法不同（claude 的 "Write"、
// opencode 的 "edit"、grok 的工具名见 Task 1 探针），而判据只有一份。
const (
	PermToolBash     = "bash"
	PermToolWrite    = "write"
	PermToolEdit     = "edit"
	PermToolWebFetch = "webfetch"
	PermToolOther    = "other" // 未识别的工具：判据退回按描述全文处理
)

// PermRequest 是权限请求的结构化形态。
//
// 它与 AdapterEvent.Text 不是重复：Text 是给人看的全文（工单与展示的唯一
// 真相源），PermRequest 是给判据看的字段。拍平成字符串会丢掉工具名与路径，
// 那正是黑名单只能对整串做正则、于是既误判又漏判的根因。
//
// 边界：
//   - adapter 提取不出结构时**不要伪造**：整个 Perm 置 nil，manager 会
//     fail-closed 升级人工。填一个空壳会让判据误以为拿到了结构
type PermRequest struct {
	Tool    string   // 归一化工具名，取上面的 PermTool* 常量
	Command string   // Tool=bash 时的完整命令串（不截断）
	Paths   []string // Tool=write|edit 时的目标路径（可为相对路径）
}

// NormalizePermTool 把 executor 的原始工具名折算为归一化名。
//
// 参数：raw 为 executor 侧的原始工具名，允许带空白、大小写任意
// 返回：PermTool* 之一；未识别时返回 PermToolOther
//
// 注意：本表只收本项目**实测见过**的名字。新增 executor 时先取真实样本再
// 补表，不要按想象加别名——猜错的代价是判据静默走错路由。
func NormalizePermTool(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "bash":
		return PermToolBash
	case "write":
		return PermToolWrite
	case "edit":
		return PermToolEdit
	case "webfetch":
		return PermToolWebFetch
	default:
		return PermToolOther
	}
}
```

给 `AdapterEvent` 加字段（其余字段与注释一字不动）：

```go
type AdapterEvent struct {
	Type         string  // "permission" | "question" | "progress" | "result"
	PermissionID string  // Type=permission 时有效（manager 按其派生 ticket id，天然幂等）
	SessionID    string  // 可选：executor 会话标识，manager 落 task.ExecutorSession；空则忽略
	Text         string  // permission 描述 / question 原文 / progress 文本
	// Perm 是 Type=permission 时的结构化载荷；nil 表示 adapter 提取不出结构，
	// manager 据此 fail-closed 升级人工（看不懂的请求交给人）。
	Perm   *PermRequest
	Result *Result // Type=result 时有效
}
```

确认 `executor.go` 的 import 块含 `"strings"`，缺则补上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/... `
Expected: PASS（既有用例不受影响——`Perm` 是新增可选字段）。

- [ ] **Step 5: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/executor/
git add internal/executor/executor.go internal/executor/executor_test.go
git commit -m "feat(executor): 权限事件增加结构化载荷 PermRequest

Text 一个字节不动（工单存全文、事件截断两条 B6 契约保持原样）。
Perm 为 nil 表示 adapter 提取不出结构，manager 据此 fail-closed 升级。
归一化工具名只收实测见过的名字，不按想象加别名。"
```

---

## Task 6: manager 接入判据网关

**Files:**
- Modify: `internal/agentd/manager.go`
- Modify: `internal/agentd/approver.go`
- Modify: `cmd/agentd.go`
- Test: `internal/agentd/permgate_wire_test.go`（新建）

**Interfaces:**
- Consumes: `permgate.New` / `permgate.Gate.Judge` / `permgate.Verdict`（Task 2–4）、`executor.PermRequest`（Task 5）
- Produces:
  - `NewManager(st, hub, ads, cfg, approver, gate *permgate.Gate, log) *Manager`（签名新增 `gate`）
  - `func (m *Manager) judgePermission(taskID string, ev executor.AdapterEvent) permgate.Verdict`
  - `func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent, v permgate.Verdict)`

- [ ] **Step 1: 写失败测试**

`internal/agentd/permgate_wire_test.go`：

```go
package agentd

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/permgate"
)

// TestJudgePermissionNilPermEscalates adapter 没给结构 → fail-closed 升级。
func TestJudgePermissionNilPermEscalates(t *testing.T) {
	m := newWireTestManager(t)
	v := m.judgePermission("t1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: /etc/hosts"})
	if v.Action != permgate.Escalate {
		t.Fatalf("Perm 缺失必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionInScopeWriteAutoAllows 工作区内的写自动放行。
func TestJudgePermissionInScopeWriteAutoAllows(t *testing.T) {
	m, taskID, work := newWireTestManagerWithTask(t)
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1",
		Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{filepath.Join(work, "main.go")}},
	})
	if v.Action != permgate.AutoAllow {
		t.Fatalf("工作区内写入应自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionOutsideWriteEscalates 越界写升级人工。
func TestJudgePermissionOutsideWriteEscalates(t *testing.T) {
	m, taskID, _ := newWireTestManagerWithTask(t)
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1",
		Text: "Write: /etc/hosts",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{"/etc/hosts"}},
	})
	if v.Action != permgate.Escalate {
		t.Fatalf("越界写必须升级人工，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestAutoAllowWorksWithoutApprover 锁死 spec §5.3：AutoAllow 与审批者
// 启用状态解耦。
//
// 这条必须单独钉：Write/Edit 改成 ask 之后，若 AutoAllow 依赖审批者存在，
// 未配置审批者的部署会被工作区内的每一次写入淹没。
func TestAutoAllowWorksWithoutApprover(t *testing.T) {
	m, taskID, work := newWireTestManagerWithTask(t)
	m.approver = nil // 显式关掉审批链
	v := m.judgePermission(taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite,
			Paths: []string{filepath.Join(work, "main.go")}},
	})
	if v.Action != permgate.AutoAllow {
		t.Fatalf("审批者未启用时 AutoAllow 仍须生效，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgePermissionUnknownTaskEscalates 读不到任务 → 范围不可知 → 升级。
func TestJudgePermissionUnknownTaskEscalates(t *testing.T) {
	m := newWireTestManager(t)
	v := m.judgePermission("no-such-task", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "Write: main.go",
		Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"main.go"}},
	})
	if v.Action != permgate.Escalate {
		t.Fatalf("任务读不到时范围不可知，必须升级，实得 %s（%s）", v.Action, v.Reason)
	}
}

func newWireTestManager(t *testing.T) *Manager {
	t.Helper()
	g, err := permgate.New(nil, slog.Default())
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	m := newTestManager(t) // 既有测试辅助；若签名不同，按其现状调用
	m.gate = g
	return m
}

func newWireTestManagerWithTask(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m := newWireTestManager(t)
	work := t.TempDir()
	taskID := createTestTask(t, m, work) // 既有测试辅助；若不存在，用 m.st.CreateTask 直建
	return m, taskID, work
}
```

> 实现者注意：`newTestManager` / `createTestTask` 这两个辅助按 `internal/agentd/manager_test.go` 里的**现有**同类辅助调用；名字不符就用那里已有的写法建 Manager 与任务，不要新造一套测试脚手架。任务必须落 `RepoPath = work`（原地模式，`Workdir()` 返回 `RepoPath`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'JudgePermission|AutoAllowWorks'`
Expected: 编译失败 `m.gate undefined` / `m.judgePermission undefined`。

- [ ] **Step 3: 给 Manager 加字段与构造参数**

在 `Manager` 结构体的 `approver` 字段之后插入：

```go
	// gate 是权限判据网关（B23/B27）：把权限请求判成 AutoAllow/Consult/Escalate。
	// **与 approver 解耦**——approver 为 nil 时 gate 依然工作，否则未配置审批者的
	// 部署会被工作区内的每一次写入淹没（spec §5.3）。构造后只读。
	gate *permgate.Gate
	// aaMu 保护 aaCount：每任务累计的自动放行次数，回合终结时汇总打一条 Info。
	// 不能完全静默——出问题时要有第一现场。
	aaMu    sync.Mutex
	aaCount map[string]int
```

`NewManager` 签名加 `gate *permgate.Gate`（放在 `approver` 之后、`log` 之前），并在返回的结构体里赋值 `gate: gate, aaCount: map[string]int{}`；doc 注释补一行参数说明：

```go
//   - gate: 权限判据网关；**不得为 nil**，它与 approver 是否启用无关
```

- [ ] **Step 4: 实现 judgePermission / autoAllowPermission / 计数器**

在 `shouldConsultApprover` 之前插入：

```go
// judgePermission 把一次权限事件交给判据网关，返回裁决。
//
// 参数：
//   - taskID: 任务 id（用于取工作区范围）
//   - ev: 权限事件
//
// 返回：permgate.Verdict；一切无法判定的情形都返回 Escalate（fail-closed）
//
// 注意：
//   - ev.Perm 为 nil 表示 adapter 提取不出结构——看不懂的请求交给人，
//     绝不让廉价模型去猜
//   - 读任务失败时工作区范围不可知，同样升级人工：范围未知时判「路径在不在
//     范围内」是没有意义的
func (m *Manager) judgePermission(taskID string, ev executor.AdapterEvent) permgate.Verdict {
	if ev.Perm == nil {
		m.log.Warn("权限事件缺结构化载荷，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID,
			"text", truncateRunes(ev.Text, 120))
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "adapter 未提供结构化权限载荷"}
	}
	task, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Warn("读任务失败，工作区范围不可知，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "读任务失败，工作区范围不可知"}
	}
	scope := permgate.Scope{
		Workdir: task.Workdir(),
		TaskDir: filepath.Join(m.cfg.DataDir, "tasks", taskID),
	}
	v := m.gate.Judge(permgate.Request{
		Tool:      ev.Perm.Tool,
		Text:      ev.Text,
		Command:   ev.Perm.Command,
		Paths:     ev.Perm.Paths,
		Truncated: strings.Contains(ev.Text, executor.TruncationMarker),
	}, scope)
	switch v.Action {
	case permgate.AutoAllow:
		m.log.Debug("权限判定：自动放行", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "paths", ev.Perm.Paths, "reason", v.Reason)
	case permgate.Consult:
		m.log.Info("权限判定：交审批者", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "reason", v.Reason, "rule", v.Rule)
	default:
		// 越界写与结构缺失用 Warn 而非 Info：这两类正是「本该被静默通过、
		// 现在被拦下」的事件，是本次改动的全部价值，必须在日志里一眼可见
		lvl := slog.LevelInfo
		if v.Rule == "" {
			lvl = slog.LevelWarn
		}
		m.log.Log(context.Background(), lvl, "权限判定：升级人工",
			"task", taskID, "perm", ev.PermissionID, "tool", ev.Perm.Tool,
			"paths", ev.Perm.Paths, "workdir", scope.Workdir, "task_dir", scope.TaskDir,
			"reason", v.Reason, "rule", v.Rule)
	}
	return v
}

// autoAllowPermission 自动放行一次权限请求：不建工单、不发事件、不改状态，
// 直接把 once 回传 executor。
//
// 注意：
//   - 没有工单可失败，因此回传失败**不产 delivery_failed 事件**；最常见的
//     失败成因是订阅重放（同一权限请求被再次投递，而 executor 侧那次请求
//     早已应答完毕），按 Warn 记录即可
//   - adapterFor 失败意味着任务的运行态已经没了，executor 侧那次请求将无人
//     应答——这是 Error 级，但同样无工单可失败
func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("自动放行：解析执行者失败，该权限请求将无人应答",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return
	}
	actx, acancel := unaryCtx(context.Background())
	defer acancel()
	if err := ad.RespondPermission(actx, taskID, ev.PermissionID, "once"); err != nil {
		m.log.Warn("自动放行回传 executor 失败（多为订阅重放，请求已失效）",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return
	}
	m.noteAutoAllowed(taskID)
}

// noteAutoAllowed 累计一次自动放行。
func (m *Manager) noteAutoAllowed(taskID string) {
	m.aaMu.Lock()
	defer m.aaMu.Unlock()
	m.aaCount[taskID]++
}

// takeAutoAllowed 取走并清空某任务的自动放行计数。
//
// 取走式而非只读：计数的意义是「这一段执行里静默放行了多少次」，汇总打完
// 就该归零，否则下一段的汇总会把上一段的数字算进去。
func (m *Manager) takeAutoAllowed(taskID string) int {
	m.aaMu.Lock()
	defer m.aaMu.Unlock()
	n := m.aaCount[taskID]
	delete(m.aaCount, taskID)
	return n
}
```

补 import：`"path/filepath"`、`"github.com/xushixin/handoff/internal/permgate"`（`context` / `strings` / `slog` 已在）。

- [ ] **Step 5: 改 handlePermission 分流**

把 `handlePermission` 里从 `// 审批链前置分流` 注释开始到 `m.escalatePermission(...)` 的那一段替换为：

```go
	// 判据前置分流（B23/B27）：结构化判据先判，三个出口对应三条既有路径。
	// AutoAllow 不建工单、不发事件、不改状态——工作区内的写入是派发的目的
	// 本身，为它唤醒任何人都是噪音。
	switch m.judgePermission(taskID, ev).Action {
	case permgate.AutoAllow:
		m.autoAllowPermission(taskID, ev)
		return
	case permgate.Consult:
		// 审批者可用且本任务未停用时才咨询；否则退化为升级人工（原行为）。
		// 已在裁决中的重放（markApproverInflight 返回 false）直接吞掉。
		if m.shouldConsultApprover(taskID) {
			if m.markApproverInflight(ticketID) {
				go m.consultApprover(ctx, taskID, ev, ticketID)
			}
			return
		}
	}
	m.escalatePermission(ctx, taskID, ev, ticketID)
```

- [ ] **Step 6: 瘦身 shouldConsultApprover**

整体替换为（黑名单与截断判定都已迁进 permgate）：

```go
// shouldConsultApprover 判断本任务此刻能否走审批者裁决：审批者已启用
// 且该任务的审批链未被连续失败停用。
//
// 权限内容层面的判定（黑名单、截断标记）已迁入 internal/permgate，
// 本函数只管「审批者这条路通不通」。
func (m *Manager) shouldConsultApprover(taskID string) bool {
	if m.approver == nil {
		return false
	}
	m.apMu.Lock()
	disabled := m.apDisabled[taskID]
	m.apMu.Unlock()
	if disabled {
		m.log.Debug("审批链已停用，直接升级审核者", "task", taskID)
		return false
	}
	return true
}
```

- [ ] **Step 7: handleResult 里打自动放行汇总**

在 `handleResult` 函数体开头插入：

```go
	// 自动放行汇总：AutoAllow 路径逐次只打 Debug，这里给出一条 Info 级总量。
	// 完全静默会让「出问题时没有第一现场」，而逐次 Info 会淹没日志。
	if n := m.takeAutoAllowed(taskID); n > 0 {
		m.log.Info("本段执行自动放行工作区内写入", "task", taskID, "n", n)
	}
```

- [ ] **Step 8: approver.go 瘦身**

- 删除 `builtinBlacklist` 变量（已迁入 `internal/permgate/blacklist.go`）。
- 删除 `Approver.blacklist` 字段、`NewApprover` 里的正则编译循环、`Blacklisted` 方法。
- `NewApprover` 的返回值签名保持 `(*Approver, error)` 不变（`cfg.Executor == ""` 仍返回 `(nil, nil)`）；编译失败这条错误路径随黑名单一起移走，函数体里不再有 `regexp` 相关代码，若 `regexp` 成为未用 import 则删掉。
- `approverPromptTemplate` 增补一行（放在「任何不确定……」那行之后）：

```
涉及生产环境、部署动作、运维目标机（如 kubectl -n prod、ssh 到生产主机、terraform apply）的操作，一律升级给上级审核者。
```

在 `approverPromptTemplate` 的 doc 注释里补一句 why：

```go
// 2026-08-09 增补生产环境一行：内置黑名单原有 `\bproduction\b|\bprod\b` 一条，
// 实测 9 条误命中里 4 条出自它（`go test ./internal/prod/...` 这类），已删除；
// 该语义改由模型承担——正则分不出 `go test ./internal/prod/...` 与
// `kubectl -n prod delete deploy/api`，模型分得出。
```

- [ ] **Step 9: cmd/agentd.go 装配**

在 `agentd.NewApprover(...)` 之后、`agentd.NewManager(...)` 之前插入：

```go
		// 判据网关必须构造，且与审批者是否启用无关：AutoAllow 是第 0 层静态
		// 判据，漏了它，未配置审批者的部署会被工作区内的每次写入淹没
		gate, err := permgate.New(cfg.Approver.Blacklist, logger)
		if err != nil {
			return fmt.Errorf("构造权限判据网关: %w", err)
		}
```

并把 `NewManager` 调用改为 `agentd.NewManager(st, srv.Hub(), ads, cfg, ap, gate, logger)`，
import 补 `"github.com/xushixin/handoff/internal/permgate"`。

> 实现者注意：该处的错误返回写法要跟随同一函数里既有的错误处理形态（若不是 `return err` 而是 `log.Fatal` 之类，照其现状写）。

- [ ] **Step 10: 修既有测试的 NewManager 调用点**

```bash
cd /Users/xushixin/workspace/handoff && grep -rn "NewManager(" --include="*.go" . | grep -v "func NewManager"
```

对每一处补 `gate` 实参。测试里统一用：

```go
gate, err := permgate.New(nil, slog.Default())
if err != nil { t.Fatalf("permgate.New: %v", err) }
```

同样地，`grep -rn "Blacklisted(" --include="*.go" .` 找出对已删除方法的引用（`approver_test.go` 里有），把那些用例迁到 `internal/permgate/blacklist_test.go` 的等价断言上，或直接删除——**不要保留调用不存在方法的死代码**。

- [ ] **Step 11: 全量测试**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./...`
Expected: 全绿，`gofmt -l .` 无输出。

Run: `go test -race ./internal/agentd/ ./internal/permgate/`
Expected: 全绿（`aaCount` 是新增的并发共享状态，必须过 race）。

- [ ] **Step 12: 提交**

```bash
cd /Users/xushixin/workspace/handoff
git add internal/agentd/ cmd/agentd.go
git commit -m "feat(agentd): 权限门接入判据网关，工作区内写入自动放行

handlePermission 前置 permgate.Judge，三个出口对应三条既有路径。
AutoAllow 不建工单、不发事件、不改状态，直接回传 once；逐次 Debug、
回合终结打一条 Info 汇总，不完全静默。

AutoAllow 与 approver 解耦（spec §5.3）：approver 为 nil 时依然生效，
否则 Write/Edit 改 ask 之后未配置审批者的部署会被每次写入淹没。

黑名单与截断判定从 approver 迁往 permgate，Approver 退化为只做
「调模型 + 解析裁决」。approverPromptTemplate 增补生产环境语义，
承接被删除的 prod|production 规则。"
```

---

## Task 7: claude adapter 结构提取与规则表

> **前置**：Task 1 的探针结论文档已确认 claude 可靠提取路径。若结论为否，跳过本 task 的 Step 5–6（规则表改动），只做结构提取，并在 Task 10 记 backlog。

**Files:**
- Modify: `internal/executor/claudecode/adapter.go`（`permText` / `onPermissionAsk`）
- Modify: `internal/executor/claudecode/taskenv.go`（`askRules` / `allowRules`）
- Test: `internal/executor/claudecode/perm_test.go`、`internal/executor/claudecode/taskenv_test.go`

**Interfaces:**
- Consumes: `executor.PermRequest`、`executor.PermTool*`、`executor.NormalizePermTool`（Task 5）
- Produces: `func permTextAndRequest(toolName string, input json.RawMessage) (string, *executor.PermRequest)`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/claudecode/perm_test.go` 追加：

```go
// TestPermTextAndRequest 用 Task 1 真机取样的载荷断言结构提取。
func TestPermTextAndRequest(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		input    string
		wantText string
		wantTool string
		wantCmd  string
		wantPath []string
	}{
		{"Bash", "Bash", `{"command":"go build ./..."}`,
			"Bash: go build ./...", executor.PermToolBash, "go build ./...", nil},
		{"Write", "Write", `{"file_path":"/repo/main.go","content":"x"}`,
			"Write: /repo/main.go", executor.PermToolWrite, "", []string{"/repo/main.go"}},
		{"Edit", "Edit", `{"file_path":"/repo/a.go","old_string":"a","new_string":"b"}`,
			"Edit: /repo/a.go", executor.PermToolEdit, "", []string{"/repo/a.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, req := permTextAndRequest(c.tool, json.RawMessage(c.input))
			if text != c.wantText {
				t.Errorf("text = %q，期望 %q", text, c.wantText)
			}
			if req == nil {
				t.Fatal("必须产出结构化载荷")
			}
			if req.Tool != c.wantTool {
				t.Errorf("tool = %q，期望 %q", req.Tool, c.wantTool)
			}
			if req.Command != c.wantCmd {
				t.Errorf("command = %q，期望 %q", req.Command, c.wantCmd)
			}
			if len(req.Paths) != len(c.wantPath) {
				t.Fatalf("paths = %v，期望 %v", req.Paths, c.wantPath)
			}
			for i := range c.wantPath {
				if req.Paths[i] != c.wantPath[i] {
					t.Errorf("paths[%d] = %q，期望 %q", i, req.Paths[i], c.wantPath[i])
				}
			}
		})
	}
}

// TestPermRequestNilWhenUnparsable 提取不出结构时必须返回 nil，
// 不得伪造空壳——空壳会让判据误以为拿到了结构。
func TestPermRequestNilWhenUnparsable(t *testing.T) {
	if _, req := permTextAndRequest("Bash", json.RawMessage(`{"nope":1}`)); req != nil {
		t.Fatalf("命令缺失时必须返回 nil，实得 %+v", req)
	}
	if _, req := permTextAndRequest("Write", json.RawMessage(`{"nope":1}`)); req != nil {
		t.Fatalf("路径缺失时必须返回 nil，实得 %+v", req)
	}
}

// TestRealProbePayload 用 Task 1 取回的真实载荷跑一遍，防止手写样本与
// 真机形态漂移。
func TestRealProbePayload(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "perm_write.json"))
	if err != nil {
		t.Fatalf("读真机载荷样本: %v", err)
	}
	var ask struct {
		ToolName string          `json:"tool_name"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &ask); err != nil {
		t.Fatalf("解析真机载荷样本: %v", err)
	}
	_, req := permTextAndRequest(ask.ToolName, ask.Input)
	if req == nil || len(req.Paths) == 0 {
		t.Fatalf("真机 Write 载荷必须能提取出路径，实得 %+v", req)
	}
}
```

补 import：`"encoding/json"`、`"os"`、`"path/filepath"`、`"github.com/xushixin/handoff/internal/executor"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run PermText`
Expected: 编译失败 `undefined: permTextAndRequest`。

- [ ] **Step 3: 把 permText 重构为同时产出两者**

替换 `permText`（保留原函数名作薄封装，既有调用点不动）：

```go
// permTextAndRequest 从工具名与入参同时组装展示文本与结构化载荷。
//
// 参数：
//   - toolName: claude 的原始工具名（Bash / Write / Edit / …）
//   - input: 该次工具调用的原始入参 JSON
//
// 返回：
//   - text: 展示文本，形如 "Bash: <命令>" / "Write: <路径>"；提取不出关键
//     入参时退回工具名 + 紧凑 JSON
//   - req: 结构化载荷；**关键字段缺失时返回 nil**——伪造空壳会让判据误以为
//     拿到了结构，从而跳过 fail-closed 升级
//
// 两者共用一次解析：它们必须描述同一件事，分两处解析迟早漂移。
func permTextAndRequest(toolName string, input json.RawMessage) (string, *executor.PermRequest) {
	switch toolName {
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			return "Bash: " + in.Command,
				&executor.PermRequest{Tool: executor.PermToolBash, Command: in.Command}
		}
	case "Edit", "Write":
		var in struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(input, &in) == nil && in.FilePath != "" {
			return toolName + ": " + in.FilePath,
				&executor.PermRequest{Tool: executor.NormalizePermTool(toolName),
					Paths: []string{in.FilePath}}
		}
	}
	// 其余工具与解析失败：给出可读文本，但不伪造结构
	return permFallbackText(toolName, input), nil
}
```

`permFallbackText` 用原 `permText` 里「其余取 input 的紧凑 JSON」那段代码搬过去，函数名与注释按原样保留其语义。原 `permText` 若仍有其它调用点，改为：

```go
// permText 保留原签名，只取展示文本（结构化载荷见 permTextAndRequest）。
func permText(toolName string, input json.RawMessage) string {
	text, _ := permTextAndRequest(toolName, input)
	return text
}
```

- [ ] **Step 4: onPermissionAsk 带上结构**

```go
func (a *Adapter) onPermissionAsk(r *runState, ask permAsk) {
	text, req := permTextAndRequest(ask.ToolName, ask.Input)
	if strings.TrimSpace(text) == "" {
		a.log.Warn("claude 权限请求无可读描述，按未说明权限交审核者",
			"task", r.taskID, "perm", ask.ToolUseID)
		text = "claude 未提供可读描述（tool_use_id " + ask.ToolUseID + "），请 tmux attach 查看现场"
	}
	if req == nil {
		// 提取不出结构 → manager 会 fail-closed 升级人工。记一条，否则
		// 「为什么这个请求没走审批者」在日志里无从查起
		a.log.Warn("claude 权限请求提取不出结构化载荷，将由 manager 升级人工",
			"task", r.taskID, "perm", ask.ToolUseID, "tool", ask.ToolName)
	}
	a.emit(r, executor.AdapterEvent{
		Type: "permission", PermissionID: ask.ToolUseID,
		Text: turn.TruncateMarked(text, permTextHardLimit),
		Perm: req,
	})
}
```

- [ ] **Step 5: 改规则表**

`internal/executor/claudecode/taskenv.go`：`allowRules` 去掉 `Edit`、`Write`；`askRules` 加两条（放在表首，带注释）：

```go
var askRules = []string{
	"Write", // 写文件：路径是否越出任务范围由 handoff 的 permgate 判（B27）
	"Edit",  // 同上
	"Bash(rm:*)",        // 删除（含 rm -rf）
	// …以下原样保留
}

var allowRules = []string{"Bash", "Read", "Glob", "Grep"}
```

并在 `allowRules` 的注释里补一句 why：

```go
// 2026-08-09 起 Edit/Write 移出本表进 askRules：它们经权限门后由 handoff
// 判目标路径是否落在任务范围内，范围内的写入在 manager 侧微秒级自动放行、
// 不建工单不发事件，越界的才升级人工（spec §5.2）。留在 allow 里等于
// 「写 ~/.ssh/authorized_keys 连事件都不留」。
```

- [ ] **Step 6: 同步 taskenv_test 的逐条断言**

`taskenv_test.go` 里断言规则表的用例（文件注释写明「少一条就是静默放行」）必须同步：`askRules` 期望值加 `"Write"`、`"Edit"`，`allowRules` 期望值去掉这两条。另加一条守卫：

```go
// TestWriteEditNotInAllow 锁死 B27：写文件类工具不得回到 allow 表。
func TestWriteEditNotInAllow(t *testing.T) {
	for _, r := range allowRules {
		if r == "Write" || r == "Edit" {
			t.Fatalf("%s 不得出现在 allowRules——那等于写仓库外路径不经任何人（B27）", r)
		}
	}
}
```

- [ ] **Step 7: 跑测试**

Run: `go test ./internal/executor/claudecode/ -v`
Expected: 全部 PASS。

- [ ] **Step 8: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/executor/claudecode/
git add internal/executor/claudecode/
git commit -m "feat(claude): 权限事件带结构化载荷，Write/Edit 移入 ask

permText 重构为 permTextAndRequest，展示文本与结构化载荷共用一次解析。
关键字段缺失时返回 nil 而非空壳——空壳会让判据误以为拿到了结构、跳过
fail-closed 升级。

Write/Edit 移出 allowRules 进 askRules：留在 allow 里等于写
~/.ssh/authorized_keys 连事件都不留（B27）。范围内的写入由 manager
自动放行，不产生工单。"
```

---

## Task 8: grok adapter 结构提取与规则表

> **Task 1 已完成，结论见 `docs/superpowers/plans/2026-08-09-permission-payload-probe.md` §2。字段名以下面正文为准（已按真机样本改写完毕），不必再去猜：**
>
> - 工具名取 `toolCall.rawInput.variant`（`"Write"` / `"SearchReplace"`），缺失时回落 `toolCall._meta["x.ai/tool"].kind`（`"write"` / `"edit"`）。**不要用 `toolCall.kind`**——它对 Write 和 Edit 一律是 `"edit"`，区分不出来。
> - 路径取 `toolCall.rawInput.file_path`。**绝对与相对都真实出现过**（见 `testdata/perm_edit_relative.json`），相对路径的解析在 Task 3 的 `InScope` 里已经实现，本 task 只需把原样的路径串传过去，不要在这里自作主张拼接。
> - Step 4 的规则表改动**保留**：grok 的 `allowRules` 里留着 `Edit`/`Write` 等于这些事件根本不产生。探针期间置空 `allowRules` 实测未出现「默认全 ask」的连环唤醒。

**Files:**
- Modify: `internal/executor/grok/adapter.go`（`OnPermission`、新增提取函数）
- Modify: `internal/executor/grok/taskenv.go`（`askRules` / `allowRules`）
- Test: `internal/executor/grok/perm_test.go`、`internal/executor/grok/taskenv_test.go`

**Interfaces:**
- Consumes: `executor.PermRequest`、`executor.PermTool*`（Task 5）
- Produces: `func permRequestFromToolCall(toolName string, rawInput json.RawMessage) *executor.PermRequest`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/grok/perm_test.go` 追加。注意 testdata 是 `session/request_permission`
的 **params 本体**（顶层就是 `sessionId` / `toolCall` / `options`，没有 `params` 外层）：

```go
// TestPermRequestFromToolCall 用 Task 1 真机取样的三份载荷断言结构提取。
//
// 字段名全部来自真机样本（testdata/perm_*.json）——grok 的载荷形态在本项目
// 已经猜错过两次，不再手写想象中的形状。三份样本各锁一件事：
//   - perm_write.json          Write，绝对路径
//   - perm_edit_absolute.json  Edit，绝对路径
//   - perm_edit_relative.json  Edit，**相对路径**（真机确实会给相对路径）
func TestPermRequestFromToolCall(t *testing.T) {
	cases := []struct {
		file     string
		wantTool executor.PermTool
		wantPath string
	}{
		{"perm_write.json", executor.PermToolWrite, "/Users/sycm/.handoff/worktrees/a2e10493/probe.md"},
		{"perm_edit_absolute.json", executor.PermToolEdit, "/Users/sycm/.handoff/worktrees/a2e10493/probe.md"},
		{"perm_edit_relative.json", executor.PermToolEdit, "probe.md"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatalf("读真机载荷样本: %v", err)
			}
			var p struct {
				ToolCall permToolCall `json:"toolCall"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("解析真机载荷样本: %v", err)
			}
			req := permRequestFromToolCall(p.ToolCall)
			if req == nil {
				t.Fatal("真机写文件载荷必须能提取出结构")
			}
			if req.Tool != c.wantTool {
				t.Fatalf("工具名 = %q，期望 %q", req.Tool, c.wantTool)
			}
			if len(req.Paths) != 1 || req.Paths[0] != c.wantPath {
				t.Fatalf("路径 = %v，期望 [%s]（相对路径必须原样透传，解析交给 permgate）",
					req.Paths, c.wantPath)
			}
		})
	}
}

// TestPermRequestToolCallKindIsUseless 锁死一条真机教训：toolCall.kind 对
// Write 和 Edit 一律是 "edit"，不能拿它当工具名来源。这条用例存在的意义是
// 让任何一个想「简化成读 kind」的后来者立刻红。
func TestPermRequestToolCallKindIsUseless(t *testing.T) {
	for _, f := range []string{"perm_write.json", "perm_edit_absolute.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatalf("读真机载荷样本: %v", err)
		}
		var p struct {
			ToolCall struct {
				Kind string `json:"kind"`
			} `json:"toolCall"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("解析: %v", err)
		}
		if p.ToolCall.Kind != "edit" {
			t.Fatalf("%s 的 toolCall.kind = %q，真机实测两者都是 \"edit\"；"+
				"若 grok 改了形态，本用例与 permRequestFromToolCall 都要复核", f, p.ToolCall.Kind)
		}
	}
}

// TestPermRequestBashKeepsFullCommand 命令必须取完整原文，不能取 title 的
// 摘要——title 的 200 截断会把命令尾部的危险片段切掉。
func TestPermRequestBashKeepsFullCommand(t *testing.T) {
	long := "go test ./... && " + strings.Repeat("x", 300) + " && rm -rf /tmp/x"
	in, err := json.Marshal(map[string]string{"command": long})
	if err != nil {
		t.Fatalf("构造入参: %v", err)
	}
	req := permRequestFromToolCall(permToolCall{Kind: "execute", RawInput: in})
	if req == nil || req.Command != long {
		t.Fatalf("命令必须取完整原文，实得 %+v", req)
	}
}

// TestPermRequestNilWhenNoStructure 提取不出结构时返回 nil，不伪造空壳。
func TestPermRequestNilWhenNoStructure(t *testing.T) {
	if req := permRequestFromToolCall(permToolCall{RawInput: json.RawMessage(`{}`)}); req != nil {
		t.Fatalf("无可用字段时必须返回 nil，实得 %+v", req)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run PermRequest`
Expected: 编译失败 `undefined: permRequestFromToolCall`。

- [ ] **Step 3: 实现提取并接进 OnPermission**

先把 `OnPermission` 里那个匿名的 toolCall 结构体提成具名类型（测试要复用它）：

```go
// permToolCall 是 ACP session/request_permission 里 toolCall 的可用子集。
//
// 字段取舍全部来自 Task 1 的真机取样（testdata/perm_*.json），逐条理由：
//   - Kind 是 toolCall.kind：**不能**用来分辨工具，真机实测 Write 与 Edit
//     都是 "edit"。留着只是为了给命令类（"execute")做兜底归一化。
//   - RawInput.Variant 才是可分辨的工具名（"Write" / "SearchReplace"）。
//   - Meta 是 rawInput 缺 variant 时的回落来源（_meta["x.ai/tool"].kind
//     给 "write" / "edit"）。
type permToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	RawInput   json.RawMessage `json:"rawInput"`
	Meta       struct {
		XAI struct {
			Kind string `json:"kind"`
		} `json:"x.ai/tool"`
	} `json:"_meta"`
}

// permRequestFromToolCall 从 ACP toolCall 提取结构化权限载荷。
//
// 参数：
//   - tc: 一次 session/request_permission 的 toolCall 本体
//
// 返回：结构化载荷；关键字段缺失时返回 nil（不伪造空壳，manager 会
// fail-closed 升级人工）
//
// 注意：
//   - 命令取 rawInput.command 的完整原文，不取 toolCall.title——title 是
//     render.log 的行摘要、带 200 截断，命令尾部可能正藏着危险片段。
//   - 路径**原样透传**，不在这里展开相对路径。真机样本里 Edit 给过相对
//     路径 "probe.md"，展开成绝对路径是 permgate 的 InScope 的职责（它
//     知道任务工作目录，adapter 不知道）。
func permRequestFromToolCall(tc permToolCall) *executor.PermRequest {
	// 命令类：取 command 全文
	if cmd := rawCommand(tc.RawInput); cmd != "" {
		tool := executor.NormalizePermTool(tc.Kind)
		if tool == executor.PermToolOther {
			tool = executor.PermToolBash
		}
		return &executor.PermRequest{Tool: tool, Command: cmd}
	}
	// 文件类：先定工具名，再取路径
	if paths := rawPaths(tc.RawInput); len(paths) > 0 {
		tool := fileToolOf(tc)
		return &executor.PermRequest{Tool: tool, Paths: paths}
	}
	return nil
}

// fileToolOf 判定文件类工具究竟是 write 还是 edit。
//
// 为什么不能读 toolCall.kind：2026-08-09 真机实测，Write 与 Edit 的
// toolCall.kind 都是 "edit"（见 testdata/perm_write.json）。用它做判据会把
// 每一次整文件覆写误报成局部编辑。可分辨的来源只有两处，按可靠性排序：
//  1. rawInput.variant —— "Write" / "SearchReplace"
//  2. _meta["x.ai/tool"].kind —— "write" / "edit"
//
// 两处都缺时保守取 write：write 的破坏面更大，宁可按更严的那个判。
func fileToolOf(tc permToolCall) executor.PermTool {
	var in struct {
		Variant string `json:"variant"`
	}
	if len(tc.RawInput) > 0 && json.Unmarshal(tc.RawInput, &in) == nil {
		switch in.Variant {
		case "Write":
			return executor.PermToolWrite
		case "SearchReplace", "Edit":
			return executor.PermToolEdit
		}
	}
	if t := executor.NormalizePermTool(tc.Meta.XAI.Kind); t != executor.PermToolOther {
		return t
	}
	return executor.PermToolWrite
}

// rawPaths 从 rawInput 提取文件类工具的目标路径。
//
// 字段名 file_path 来自 Task 1 的真机探针（testdata/perm_write.json 与
// perm_edit_*.json 三份样本一致），**不是推断**。真机每次只带一个路径，
// 这里仍返回切片是为了对齐 executor.PermRequest.Paths 的形状。
func rawPaths(rawInput json.RawMessage) []string {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if len(rawInput) == 0 || json.Unmarshal(rawInput, &in) != nil {
		return nil
	}
	if in.FilePath == "" {
		return nil
	}
	return []string{in.FilePath}
}
```

`OnPermission` 里的 emit 改为（`p.ToolCall` 的类型换成上面的 `permToolCall`）：

```go
	req := permRequestFromToolCall(p.ToolCall)
	if req == nil {
		a.log.Warn("grok 权限请求提取不出结构化载荷，将由 manager 升级人工",
			"task", h.r.taskID, "perm", p.ToolCall.ToolCallID, "kind", p.ToolCall.Kind)
	} else {
		a.log.Info("grok 权限请求已结构化", "task", h.r.taskID,
			"perm", p.ToolCall.ToolCallID, "tool", req.Tool, "paths", len(req.Paths))
	}
	h.a.emit(h.r, executor.AdapterEvent{Type: "permission",
		PermissionID: p.ToolCall.ToolCallID, SessionID: h.r.sessionID,
		Text: text, Perm: req})
```

- [ ] **Step 4: 改规则表**

`internal/executor/grok/taskenv.go`：

```go
var askRules = []string{
	"Write(*)", // 写文件：路径是否越出任务范围由 handoff 的 permgate 判（B27）
	"Edit(*)",  // 同上
	"Bash(rm *)",               // 任何直接 rm（误拒成本低、误放成本高）
	// …以下原样保留
}

// allowRules 2026-08-09 起为空：Edit/Write 已移入 askRules，由 handoff 判
// 目标路径是否落在任务范围内，范围内的写入在 manager 侧自动放行、不建工单。
// 留在 allow 里等于「写 ~/.ssh/authorized_keys 连事件都不留」（B27）。
var allowRules = []string{}
```

> 若 grok 的配置在 `allowRules` 为空时会退化成「默认全 ask」（与 opencode 一期同样的连环唤醒风险），改为保留一条对只读工具的显式放行——具体取值以 Task 1 探针跑通的配置为准，并在注释里写明实测依据。

- [ ] **Step 5: 同步 taskenv_test 断言**

同 Task 7 Step 6 的做法：`askRules` 期望值加两条、`allowRules` 期望值清空，另加守卫用例：

```go
// TestWriteEditNotInAllow 锁死 B27：写文件类工具不得回到 allow 表。
func TestWriteEditNotInAllow(t *testing.T) {
	for _, r := range allowRules {
		if strings.HasPrefix(r, "Write") || strings.HasPrefix(r, "Edit") {
			t.Fatalf("%s 不得出现在 allowRules——那等于写仓库外路径不经任何人（B27）", r)
		}
	}
}
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/executor/grok/ -v && go test -race ./internal/executor/grok/`
Expected: 全部 PASS。

- [ ] **Step 7: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/executor/grok/
git add internal/executor/grok/
git commit -m "feat(grok): 权限事件带结构化载荷，Write/Edit 移入 ask

字段名全部取自真机探针样本 testdata/perm_write.json——grok 的载荷形态
在本项目已经猜错过两次（ask_user_question 应答形态），不再手写想象中的
形状。命令取 rawInput 全文而非 title，后者的 200 截断会切掉命令尾部。"
```

---

## Task 9: opencode adapter 结构提取与规则表

> **Task 1 已完成，结论见 `docs/superpowers/plans/2026-08-09-permission-payload-probe.md` §3。两条硬性修正，正文已按它改写：**
>
> - 路径字段是 `metadata.filepath`（**小写 p，不是 `filePath`**），绝对路径。`patterns` 里是相对/通配摘要，**不可**作判据。
> - **原 Step 4 / Step 5（把 `edit` 由 `allow` 翻成 `ask`）已删除。** 真机实测：生产配置下工作树内的 Write 根本不产生权限事件（正是 B27 想要的 AutoAllow），工作树外的绝对路径 Write 触发 `external_directory` 且文件未被创建（正是 B27 想要的升级）。opencode 的越界写入**已经**被 `external_directory: "ask"` 拦住了，翻成 `ask` 只会给每一次范围内的正常编辑加一道判完还是 AutoAllow 的空门——噪音为正、收益为零。

**Files:**
- Modify: `internal/executor/opencode/adapter.go`（`mapPermissionAsked`）
- Test: `internal/executor/opencode/adapter_test.go`

**Interfaces:**
- Consumes: `executor.PermRequest`、`executor.PermTool*`（Task 5）
- Produces: `mapPermissionAsked` 产出的 `AdapterEvent.Perm`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/opencode/adapter_test.go` 追加：

```go
// TestPermissionAskedCarriesStructure 用真机取样的 permission.asked 载荷
// 断言结构提取。
//
// 主用例是 perm_external_directory_file.json 而不是 perm_edit.json：生产配置
// 下 edit 是 allow，工作树内的编辑根本不产生事件，真正会到达 handoff 的文件
// 类事件就是这个 external_directory（Task 1 探针 §3.1 实测）。perm_edit.json
// 作为次要用例保留，防止将来有人把 edit 翻成 ask 时提取路径的代码不在位。
func TestPermissionAskedCarriesStructure(t *testing.T) {
	for _, f := range []string{"perm_external_directory_file.json", "perm_edit.json"} {
		t.Run(f, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", f))
			if err != nil {
				t.Fatalf("读真机载荷样本: %v", err)
			}
			a, r := newAdapterWithRunForTest(t) // 按本包既有测试辅助的写法构造
			a.mapPermissionAsked(r, raw)
			ev := <-r.EventsForTest()
			if ev.Type != "permission" {
				t.Fatalf("应产出 permission 事件，实得 %q", ev.Type)
			}
			if ev.Perm == nil {
				t.Fatal("真机文件类载荷必须能提取出结构")
			}
			if len(ev.Perm.Paths) != 1 || !filepath.IsAbs(ev.Perm.Paths[0]) {
				t.Fatalf("路径 = %v，期望恰好一个绝对路径（取自 metadata.filepath，"+
					"不是 patterns——后者是相对/通配摘要）", ev.Perm.Paths)
			}
		})
	}
}

// TestPermissionAskedExternalDirBash external_directory 的 bash 形态没有
// filepath，路径在 metadata.directories，可能多项。
func TestPermissionAskedExternalDirBash(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "perm_external_directory_bash.json"))
	if err != nil {
		t.Fatalf("读真机载荷样本: %v", err)
	}
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, raw)
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Command == "" {
		t.Fatalf("bash 形态必须带命令原文，实得 %+v", ev.Perm)
	}
	if len(ev.Perm.Paths) == 0 {
		t.Fatal("bash 形态的越界目录必须进 Paths，否则 permgate 判不出越界")
	}
}

// TestPermissionAskedBashCarriesCommand bash 请求带完整命令。
func TestPermissionAskedBashCarriesCommand(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	props := []byte(`{"id":"p1","permission":"bash","metadata":{"command":"go build ./..."}}`)
	a.mapPermissionAsked(r, props)
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Tool != executor.PermToolBash {
		t.Fatalf("bash 请求应归一化为 bash，实得 %+v", ev.Perm)
	}
	if ev.Perm.Command != "go build ./..." {
		t.Fatalf("command = %q，期望完整原文", ev.Perm.Command)
	}
}

// TestPermissionAskedNilPermWhenNoStructure 无可用字段时 Perm 为 nil。
func TestPermissionAskedNilPermWhenNoStructure(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, []byte(`{"id":"p1"}`))
	ev := <-r.EventsForTest()
	if ev.Perm != nil {
		t.Fatalf("无可用字段时必须为 nil，实得 %+v", ev.Perm)
	}
}
```

> 实现者注意：`newAdapterWithRunForTest` 与 `EventsForTest` 按本包**既有**测试辅助的名字与签名调用；本包没有等价辅助时，参照 `internal/executor/grok/export_test.go` 的做法在 `internal/executor/opencode/export_test.go` 里新增一个最小构造（带空 `runState` 与带缓冲的事件通道），不要在测试里直接摸私有字段。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run PermissionAsked`
Expected: FAIL——`ev.Perm` 为 nil（字段还没填）。

- [ ] **Step 3: 在 mapPermissionAsked 里提取结构**

扩展现有的匿名结构体（字段名逐条来自 Task 1 真机样本）：

```go
	var pa struct {
		ID         string   `json:"id"`
		Permission string   `json:"permission"`
		Patterns   []string `json:"patterns"`
		Metadata   struct {
			Command string `json:"command"`
			// filepath 是小写 p——真机样本如此（testdata/perm_edit.json）。
			// 写成 filePath 会静默取到空串，然后整条请求退化成「提取不出
			// 结构」被 fail-closed 升级，表现为每次编辑都唤醒人，很难查。
			FilePath string `json:"filepath"`
			// external_directory 的 bash 形态没有 filepath，越界目录在这里
			ParentDir   string   `json:"parentDir"`
			Directories []string `json:"directories"`
		} `json:"metadata"`
	}
```

emit 之前：

```go
	// 结构化载荷（B23/B27）：permission 字段就是 opencode 的工具类别原文，
	// 真机实测取值有 bash / edit / external_directory，直接作归一化来源。
	//
	// 为什么路径不取 patterns：patterns 里是相对路径与通配摘要（真机样本里
	// edit 的 patterns 是 ["probe.md"]、external_directory 的是 ["/tmp/*"]），
	// 拿它判归属会把通配符当成路径。绝对路径只在 metadata 里。
	var req *executor.PermRequest
	tool := executor.NormalizePermTool(pa.Permission)
	paths := pa.Metadata.Directories
	if pa.Metadata.FilePath != "" {
		paths = append([]string{pa.Metadata.FilePath}, paths...)
	}
	switch {
	case pa.Metadata.Command != "":
		if tool == executor.PermToolOther {
			tool = executor.PermToolBash
		}
		// bash 形态的 external_directory 同时带命令与越界目录，两个都要给
		// permgate——命令走黑名单判据，目录走归属判据
		req = &executor.PermRequest{Tool: tool, Command: pa.Metadata.Command, Paths: paths}
	case len(paths) > 0:
		if tool == executor.PermToolOther {
			tool = executor.PermToolWrite
		}
		req = &executor.PermRequest{Tool: tool, Paths: paths}
	}
	if req == nil {
		// 提取不出结构 → manager 会 fail-closed 升级人工。记一条，否则
		// 「为什么这个请求没走审批者」在日志里无从查起
		a.log.Warn("opencode 权限请求提取不出结构化载荷，将由 manager 升级人工",
			"task", r.taskID, "perm", pa.ID, "permission", pa.Permission)
	}
	a.emit(r, executor.AdapterEvent{
		Type: "permission", PermissionID: pa.ID,
		Text: turn.TruncateMarked(text, permTextHardLimit),
		Perm: req,
	})
```

> 实现者注意：`Metadata` 的键名已按 Task 1 真机样本定死（`filepath` / `parentDir` / `directories`），不要再改成驼峰。四份 opencode testdata 覆盖了全部三种 `permission` 取值，跑测试即可验证。

- [ ] **Step 4: 保持规则表不变（原改动已按探针结论撤销）**

`internal/executor/opencode/taskenv.go` **不改**。原计划要把 `Edit: "allow"` 翻成 `"ask"`，Task 1 探针推翻了这个前提（详见探针结论 §3.1）：

- 工作树内 `Write`：生产配置下**不产生任何权限事件**，文件直接写成——这已经是 B27 要的 AutoAllow。
- 工作树外绝对路径 `Write`：触发 `permission: "external_directory"`，**文件未被创建**，任务停在待答复——这已经是 B27 要的 Escalate。

翻成 `ask` 只会给每一次范围内的正常编辑加一道「permgate 判完还是 AutoAllow」的空门，噪音为正、安全收益为零。

只在 `taskenv.go` 文件头注释里 `edit: allow` 那一段补一句实测依据：

```go
//   - edit 保持 allow（2026-08-09 真机探针复核）：越界写入由
//     external_directory: "ask" 拦截并升级人工，范围内写入本就该直接放行。
//     翻成 ask 等于给每次正常编辑加一道判完还是放行的空门（B27 复核结论，
//     见 docs/superpowers/plans/2026-08-09-permission-payload-probe.md §3.1）。
```

- [ ] **Step 5: 加一条锁死这个结论的守卫用例**

在 `internal/executor/opencode/taskenv_test.go`：

```go
// TestExternalDirectoryIsAsk 锁死 B27 对 opencode 的真实拦截点。
//
// opencode 的越界写入不是靠 edit 的 ask 拦的（edit 是 allow、范围内写入
// 无事件），而是靠 external_directory。这条一旦被改成 allow，写
// ~/.ssh/authorized_keys 就会连事件都不留。
func TestExternalDirectoryIsAsk(t *testing.T) {
	// …按本文件既有做法解析生成的 opencode.json…
	if got := cfg.Permission.ExternalDirectory; got != "ask" {
		t.Fatalf("external_directory 必须是 ask，实得 %q——这是 opencode 侧唯一的越界写入拦截点（B27）", got)
	}
}
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/executor/opencode/ -v && go test -race ./internal/executor/opencode/`
Expected: 全部 PASS。

- [ ] **Step 7: 提交**

```bash
cd /Users/xushixin/workspace/handoff
gofmt -l . && go vet ./internal/executor/opencode/
git add internal/executor/opencode/
git commit -m "feat(opencode): 权限事件带结构化载荷

permission 字段（bash/edit/external_directory）直接作归一化工具名来源，
路径取 metadata.filepath（小写 p，真机样本如此）。edit 保持 allow：探针
实测越界写入由 external_directory 拦截并升级人工，范围内写入本就无事件，
翻成 ask 只会加一道判完还是放行的空门。"
```

---

## Task 10: 真机 e2e 验收与 backlog 收口

**Files:**
- Modify: `docs/superpowers/backlog.md`

**Interfaces:**
- Consumes: Task 1–9 的全部改动
- Produces: backlog 上 B23 / B27 的 `✅ done(已验)` 行 + 可能新增的载荷缺口条目

- [ ] **Step 1: 合并结果上跑全量测试**

```bash
cd /Users/xushixin/workspace/handoff
go build ./... && go vet ./... && gofmt -l . && go test ./... && \
go test -race ./internal/agentd/ ./internal/permgate/ ./internal/executor/claudecode/ \
  ./internal/executor/grok/ ./internal/executor/opencode/ ./internal/localsync/ ./cmd/
```

Expected: 全绿，`gofmt -l .` 无输出。把实际输出（含通过的包数）记下来，Step 6 要写进验收列。

- [ ] **Step 2: 部署到 devbox**

```bash
cd /Users/xushixin/workspace/handoff && go build -o /tmp/handoff-new . && \
  scp /tmp/handoff-new sycm@100.73.238.21:/Users/sycm/bin/handoff && \
  ssh sycm@100.73.238.21 'pkill -f "handoff agentd" || true' && \
  ssh sycm@100.73.238.21 '/Users/sycm/.homebrew/bin/tmux kill-session -t agentd 2>/dev/null; \
  /Users/sycm/.homebrew/bin/tmux new-session -d -s agentd \
  "handoff agentd 2>&1 | tee -a ~/.handoff/agentd.log"' && sleep 3 && \
  ssh sycm@100.73.238.21 'grep "权限判据网关已就绪" ~/.handoff/agentd.log | tail -1'
```

Expected: 日志出现 `权限判据网关已就绪 builtin_rules=8 custom_rules=…`（内置 8 条——原 9 条删掉 `prod|production`）。

- [ ] **Step 3: e2e 第 1 项——工作区内写入不叫人**

对每个规则表已改动的 executor 各跑一次：

```bash
cd /Users/xushixin/workspace/handoff
printf '用 Write 工具在仓库根目录创建 e2e.md，内容一行：ok\n再用 Edit 工具把它改成两行\n完成后输出 {"summary":"已改 e2e.md"}\n' > /tmp/e2e-in.md
handoff dispatch --target devbox --executor <ex> --plan /tmp/e2e-in.md --new-worktree
```

Expected（三条都要满足）：
1. 任务直接跑到 `waiting_review`，**全程没有进过 `waiting_answer`**；
2. `handoff show <id> --target devbox` 里**没有** `permission_request` 事件；
3. agentd 日志有 `本段执行自动放行工作区内写入 task=<id> n=<≥2>`。

```bash
ssh sycm@100.73.238.21 'grep "自动放行工作区内写入" ~/.handoff/agentd.log | tail -3'
```

- [ ] **Step 4: e2e 第 2 项——越界写必须叫人**

```bash
handoff continue <id> --target devbox --message '用 Write 工具在 /tmp/e2e-outside.md 写一行 outside，然后结束本回合'
```

Expected（三条都要满足）：
1. 任务进 `waiting_answer` 且有一张 `gate` 工单，工单里的 permission 文本含 `/tmp/e2e-outside.md`；
2. agentd 日志有 **Warn 级** `权限判定：升级人工 … reason=目标路径越出任务范围: /tmp/e2e-outside.md`；
3. `handoff reply <id> --target devbox --ticket <tid> --answer reject` 后，`/tmp/e2e-outside.md` **不存在**：

```bash
ssh sycm@100.73.238.21 'ls -l /tmp/e2e-outside.md 2>&1'
```

Expected: `No such file or directory`。

- [ ] **Step 5: e2e 第 3 项——B23 的误判确实消失了**

```bash
handoff continue <id> --target devbox --message '执行 git commit --allow-empty -m "chore: 验证 rm -rf 字样不再误触发审批" 然后结束本回合'
```

Expected：该命令**不产生 gate 工单**（它含 `rm -rf` 字样但只在引号内）；agentd 日志里对应一条 `权限判定：交审批者 … reason=仅引号内字面量命中黑名单，降级交审批者裁决`。

```bash
ssh sycm@100.73.238.21 'grep "仅引号内字面量命中黑名单" ~/.handoff/agentd.log | tail -2'
```

- [ ] **Step 6: 归档 e2e 任务并收口 backlog**

```bash
handoff done <每个 e2e 任务 id> --target devbox
```

`docs/superpowers/backlog.md` 上：

- B23 行：状态 `📋 specced` → `✅ done(已验)`，`验收` 列写入 Step 1 的测试命令与结果、Step 5 的真机证据（含日志原文片段）、以及「无原型/流程图，自动免除对照 08-09」。
- B27 行：同样转 `✅ done(已验)`，`验收` 列写入 Step 1 结果、Step 3 与 Step 4 的真机证据（含「越界文件确实没被创建」这一条），以及自动免除对照。
- 载荷缺口条目**不需要新增**：Task 1 探针实测三个 adapter 都能可靠提取路径（结论文档 §4），spec §6.1 的条件性回退未触发。
- 「待验证的空白」小节：spec §1.3 关于 opencode `external_directory: "ask"` 的待验项已由 Task 1 探针证实**生效**（越界写入被拦且文件未创建，结论文档 §3.1），据此改写为已验，并注明因此 opencode 的 `edit` 保持 `allow`、B27 对 opencode 的覆盖由 `external_directory` 承担。

- [ ] **Step 7: 提交**

```bash
cd /Users/xushixin/workspace/handoff
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B23/B27 收口为 done（已验）

权限判据改为结构化后，工作区内写入自动放行不叫人、越界写必须经人且
留 Warn 级事件、引号内的 rm -rf 字样不再硬升级——三条都有真机证据。"
```

---

## Self-Review

**1. Spec coverage**

| spec 章节 | 对应 task |
|---|---|
| §3.1 permgate 契约 | Task 2 Step 3 |
| §3.2 数据流 | Task 6 Step 5 |
| §3.3 AdapterEvent 扩展 | Task 5 |
| §3.4 按工具路由 | Task 4 Step 3 |
| §4.1 Bash 四条规则 | Task 2 Step 4（`judgeCommand`）+ Task 2 Step 1 三组用例 |
| §4.2 删 prod 规则 | Task 2 Step 4（`builtinBlacklist` 注释）+ Task 6 Step 8（prompt 增补） |
| §5.1 路径归一化 | Task 3 Step 3 |
| §5.2 裁决与汇总日志 | Task 6 Step 4、Step 7 |
| §5.3 与审批者解耦 | Task 6 Step 1（`TestAutoAllowWorksWithoutApprover`）+ Step 3 字段注释 |
| §5.4 TOCTOU | Task 3 Step 3 文件头注释 |
| §6.1 前置探针 + 条件性回退 | Task 1（含 Step 8 第 4 问）、Task 7/8/9 的前置说明、Task 10 Step 6 |
| §6.2 三处规则表 | Task 7 Step 5、Task 8 Step 4、Task 9 Step 4 |
| §6.3 结构提取 | Task 7 Step 3、Task 8 Step 3、Task 9 Step 3 |
| §7 fail-closed 表 | Task 4 Step 1（`TestJudgeFailClosedTable` 逐行） |
| §8 可观测性表 | Task 6 Step 4（judgePermission 的三分支日志）、Step 7（汇总）、各 adapter 的提取失败 Warn |
| §9.1–9.3 测试 | Task 2/3/4/5/6 各自的测试步 |
| §9.4 真机 e2e | Task 10 Step 3–5 |
| §11 backlog 影响 | Task 10 Step 6 |

无缺口。

**2. 占位符扫描**

三处「以 Task 1 探针结论为准」的字段名不是占位符而是**有意的依赖声明**：spec §6.1 明确规定字段名必须来自真机取样，本项目已经在 grok 的载荷形态上猜错过两次。每一处都给了可运行的默认实现（多候选字段名依次尝试）与明确的替换指令，并在测试里用真机样本作断言——探针结论一到，测试立刻能判对错。

Task 6 Step 1 与 Task 9 Step 1 里对既有测试辅助（`newTestManager` / `createTestTask` / `newAdapterWithRunForTest`）的引用附了「按本包既有写法调用，名字不符就用那里已有的写法」的实现者注记，不是「自行发挥」。

**3. 类型一致性**

- `permgate.Request` 的字段（`Tool`/`Text`/`Command`/`Paths`/`Truncated`）在 Task 2 定义、Task 4 使用、Task 6 组装——三处一致。
- `executor.PermRequest` 的字段（`Tool`/`Command`/`Paths`）在 Task 5 定义，Task 7/8/9 构造、Task 6 读取——四处一致；**注意它没有 `Text` 字段**（`Text` 在 `AdapterEvent` 上），Task 6 组装 `permgate.Request` 时 `Text` 取自 `ev.Text` 而非 `ev.Perm`。
- `PermTool*` 常量在 Task 5 定义，Task 4（permgate 路由）与 Task 7/8/9 使用——取值 `bash`/`write`/`edit`/`webfetch`/`other` 全程一致。
- `InScope` 三返回值 `(in bool, base string, err error)` 在 Task 3 定义、Task 4 使用——一致。
- `NewManager` 新签名在 Task 6 Step 3 定义、Step 9（cmd）与 Step 10（既有测试）使用——一致。
- `Action.String()` 返回 `auto_allow`/`consult`/`escalate`，测试里只用于 `%s` 打印，无断言依赖具体字符串。
