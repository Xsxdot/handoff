# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。

---

# W5b-2：图形化首次引导 + 内嵌二进制释出

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让一台**从没装过 handoff、也从没跑过 `handoff init`** 的机器，双击桌面壳就能走完配置并进入控制台。

**Architecture:** 三段。①把 `cmd` 里那套已与 TUI 解耦、但**未导出**的 init 问答逻辑下沉到 `internal/initflow` 并导出，`cmd/init.go` 退化为薄调用方——这样 CLI 与 GUI 共用同一套问题、同一批默认值。②桌面壳提供一个**事件驱动的 `Prompter` 实现**：Go 侧把「当前该问什么」发给前端，阻塞等前端回答，于是 `AskAll` 的编排逻辑一行不改就能驱动一个图形界面。③薄壳内嵌对应平台的 `handoff` 二进制，按 spec §5.3 的三态规则释出到 `~/.local/bin/handoff`，绝不覆盖用户已有的安装。

**Tech Stack:** Go 1.26 / Wails v3 beta.8（事件机制 `app.Event.On` / `Emit`，与 W5b-1 目录选择器同一套）/ TypeScript + Vite / `go:embed` + build tag。

## Global Constraints

以下为 spec 的项目级约束，**每个 task 的需求都隐含包含本节**：

- 薄壳框架 **Wails v3（beta.8）**；Linux 构建必须显式 `-tags gtk3`（默认后端是 gtk4+webkitgtk-6.0，Ubuntu 22.04 上没有）。
- 薄壳前端构建**必须走 Wails 的 Taskfile**（`wails3 task build`），不得裸调 `npm run build`——v3 的 vite 插件依赖 binding 生成器先产出 bindings。
- **不得往仓库里提交任何构建产物**。`desktop/frontend/dist/` 会被 vite 在每次构建前清空（`emptyOutDir` 默认行为），提交进去的文件会在构建后变成 `D` 状态，而 handoff 自己的 `dispatch` 硬要求工作区干净——W5b-1 已经在这里栽过一次。
- 二进制释出落点 `~/.local/bin/handoff`，与 `install.sh:22` 同一路径。
- 薄壳**绝不把 agentd 内嵌进自己的进程**，**绝不在退出路径上停 agentd**。
- **不要往薄壳里放业务逻辑**（spec §4.7）。`desktop/internal/shell` 与 `desktop/internal/embedbin` 都不得 import Wails——它们要能用普通 `go test` 覆盖。装配与 Wails API 只出现在 `desktop/main.go`。
- 日志用 `log/slog`，**禁止 `fmt.Printf` 当日志**。新文件写文件头注释（职责 + 边界），导出函数写 doc 注释，非显然分支写「为什么」的中文注释。
- Windows 按 spec §4.6 选项 A：**W5b 不出 Windows 资产**。但**代码本身保持跨平台可编译**（`go build` 不许因 GOOS 挂掉），只是不进构建链。
- **不得改动 `handoff` 本体的 `CGO_ENABLED=0`**（`release.yml:167` 的注释解释了为什么它是承重的：开 CGO 会让产物动态链接系统库并被打上构建机的最低系统版本约束，症状只在更老的 macOS 上出现）。薄壳自己必须开 CGO（Wails 绑 WKWebView），这是两条独立的构建线，**不要为了统一而把哪一边改成另一边**。
- 版本字符串的既有契约：release 用 `-ldflags "-X github.com/Xsxdot/handoff/internal/buildinfo.releaseVersion=${TAG}"` 注入（`release.yml:85`、`:233`），`handoff version` 的**第一行**即版本，开发构建下是 `unknown`。**沿用这条契约，不要另造版本来源。**

### 两条待用户确认的假设（照做即可，不要自行改变方向）

| 假设 | 出处 | 若被推翻的影响 |
|---|---|---|
| Task 1 的下沉重构（把 init 纯逻辑移到 `internal/initflow`）是被接受的 | spec §4.4.3 选项 B | 若不接受，退回「薄壳自己重写一套问答」，Task 1 作废、Task 2 改为自带问题集 |
| Windows 薄壳暂不做 | spec §4.6 选项 A | 只影响构建链（W5b-3），不影响本 plan 的任何代码 |

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/initflow/prompter.go`（新，由 `cmd/prompter.go` 移入） | 问答通道的接口与脚本化实现 |
| `internal/initflow/initflow.go`（新，由 `cmd/init.go` 移入） | 问什么、默认值怎么算、角色分支——**CLI 与 GUI 的唯一事实来源** |
| `internal/initflow/boundary_test.go`（新） | 守住依赖边界：不得 import huh / cobra / isatty |
| `cmd/init.go`（改） | 退化为：探测 → 组装 prompter → 调 `initflow.AskAll` → 写盘 → CLI 呈现 |
| `cmd/prompter.go`（删，内容移走） | — |
| `cmd/init_huh.go`（改） | 实现 `initflow.Prompter` 而非包内 `prompter` |
| `desktop/internal/shell/wizard.go`（新） | 事件驱动的 `initflow.Prompter` 实现 + notice writer |
| `desktop/internal/embedbin/{embedbin,embed,stub}.go`（新） | 内嵌二进制的双形态，照搬 `internal/webui` 的先例 |
| `desktop/internal/shell/release.go`（新） | §5.3 三态释出规则 |
| `desktop/internal/shell/binpath.go`（新） | 解析出给 launchd 用的**绝对**二进制路径 |
| `desktop/frontend/index.html` / `src/main.ts` / `src/wizard.ts`（改/新） | 向导界面 |
| `desktop/main.go`（改） | 装配：`StateUnconfigured` 分支接向导 |

---

## Task 1: `internal/initflow` —— 把 init 纯逻辑下沉并导出

**Files:**
- Create: `internal/initflow/prompter.go`（内容来自 `cmd/prompter.go`，整文件移动）
- Create: `internal/initflow/initflow.go`（内容来自 `cmd/init.go` 的若干函数，见下表）
- Create: `internal/initflow/prompter_test.go`、`internal/initflow/initflow_test.go`（由现有测试拆分移入）
- Create: `internal/initflow/boundary_test.go`
- Delete: `cmd/prompter.go`、`cmd/prompter_test.go`
- Modify: `cmd/init.go`、`cmd/init_huh.go`、`cmd/init_test.go`、`cmd/init_role_test.go`

**Interfaces:**
- Consumes: `internal/config`（`Config`）、`internal/toolchain`（`Result`、`FirstReady`）、`internal/service`
- Produces: 下表全部导出名，Task 2 与 Task 7 依赖它们

### 为什么是「移动」而不是「重写」

这是一次**纯机械搬迁**：被搬的代码不依赖 cobra、不依赖终端、不依赖 huh，只依赖 `config` / `toolchain` / `service` / `runtime.GOOS`。

**用编辑器的移动/剪切完成，不要凭记忆重敲。** 上一轮（W5b-1）计划里手写的 Go 代码块有两处 gofmt 不合规并被带进了交付——手抄一遍 220 行只会重演这件事。搬完靠 `go build` + 原有测试证明等价。

### 搬迁清单与重命名表

从 `cmd/prompter.go` 整文件搬走（该文件删除），改包名为 `initflow`：

| 旧名（`package cmd`） | 新名（`package initflow`） |
|---|---|
| `errPromptCanceled` | `ErrCanceled` |
| `promptOption` | `Option` |
| `prompter` | `Prompter` |
| `scriptedPrompter` | `ScriptedPrompter` |
| `newScriptedPrompter` | `NewScriptedPrompter` |

从 `cmd/init.go` 搬走这些函数（原行号为搬迁前的位置，仅供定位）：

| 旧名 @ 行 | 新名 | 说明 |
|---|---|---|
| `askAll` @205 | `AskAll` | 68 行，问答编排 |
| `roleOptions` @287 | `RoleOptions` | |
| `defaultRole` @303 | `DefaultRole` | |
| `executorOptions` @330 | `ExecutorOptions` | |
| `askListen` @345 | `askListen` | 仍未导出，只在包内被 `AskAll` 用 |
| `listenPreset` @375 | `ListenPreset` | 有独立测试，导出 |
| `maybeInstallService` @438 | `MaybeInstallService` | GUI 也要问「是否装 service」 |
| `warnIfNotReady` @471 | `warnIfNotReady` | 仍未导出 |
| `askTargets` @487 | `askTargets` | 仍未导出 |
| 常量 `roleExecutor` / `roleCoordinator` / `roleBoth` @49-51 | `RoleExecutor` / `RoleCoordinator` / `RoleBoth` | |

**留在 `cmd` 不动**：`printDetection`、`coveredBy`、`printPairing`、`initStdinIsTTY`、`newInteractivePrompter`、以及 `cmd/init_huh.go` 整个文件（huh 是 TUI 实现，是 UI 层）。

`cmd/init_huh.go` 只改类型引用：`prompter` → `initflow.Prompter`，`promptOption` → `initflow.Option`，`errPromptCanceled` → `initflow.ErrCanceled`。

`cmd/init.go` 的调用点（原 118 行）变成：

```go
	isExec, role, err := initflow.AskAll(out, pr, cfg, results, cfgExisted)
```

原 135 行变成：

```go
	initflow.MaybeInstallService(out, pr, isExec, p)
```

- [ ] **Step 1: 先写边界守卫测试（这一步在搬迁之前做，它必须先失败）**

创建 `internal/initflow/boundary_test.go`：

```go
package initflow_test

import (
	"go/build"
	"strings"
	"testing"
)

// initflow 是 CLI 与桌面壳共用的问答逻辑。它一旦沾上 TUI 库或 cobra，
// 桌面壳 import 它就会把整套终端 UI（乃至整个 CLI）链进来——那正是
// spec §4.4.2 否掉「就地导出让薄壳 import cmd」的理由。
// 这道门把该结论钉死在测试里，而不是留在注释里靠人记得。
func TestInitflowHasNoUILayerDeps(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("解析本包依赖失败：%v", err)
	}
	banned := []string{"charmbracelet/huh", "charmbracelet/bubbletea", "spf13/cobra", "mattn/go-isatty"}
	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if strings.Contains(imp, b) {
				t.Errorf("initflow 不得依赖 UI 层：%s", imp)
			}
		}
	}
}
```

- [ ] **Step 2: 跑它，确认此刻是失败的**

Run: `go test ./internal/initflow/...`
Expected: FAIL —— 包还不存在（`no such file or directory` 或 `no Go files`）。这确认测试确实在看新包，而不是空跑。

- [ ] **Step 3: 执行搬迁**

按上面的清单与重命名表移动代码。两个文件各写文件头注释：

`internal/initflow/initflow.go` 的头注释必须包含：

```go
// Package initflow 持有 handoff 首次配置的问答逻辑：问什么、按什么顺序问、
// 默认值怎么算、角色如何分支。
//
// 职责：
//   - 提供 AskAll：按角色分支问完全部问题，就地改写 *config.Config
//   - 提供默认值与选项的纯函数（DefaultRole / ListenPreset / ExecutorOptions / RoleOptions）
//
// 边界：
//   - **不决定 UI 形态**。问答经 Prompter 接口发生：CLI 侧是 huh（cmd/init_huh.go），
//     桌面壳侧是事件驱动实现（desktop/internal/shell/wizard.go）
//   - **不写盘**。AskAll 只改内存里的 cfg，Save 由调用方决定——半截答案不得落盘
//   - **不探测工具链**。探测结果由调用方传入
//   - 不得 import huh / bubbletea / cobra / isatty，见 boundary_test.go
//
// 本包由 cmd 下沉而来（spec §4.4）：那批逻辑本就与 TUI 解耦，但封在 package cmd
// 里且未导出，桌面壳够不着。下沉是为了让 CLI 与 GUI 共用同一份事实来源，
// 避免两套 role 默认值、两套 listen 预设各自漂移。
package initflow
```

- [ ] **Step 4: 迁移测试**

`cmd/init_test.go` 与 `cmd/init_role_test.go` 里**只测被搬走的函数**的用例，随之移到 `internal/initflow/`：

| 用例 | 去向 |
|---|---|
| `cmd/init_test.go:140`（`askAll` + `cancelPrompter`） | `internal/initflow/initflow_test.go` |
| `cmd/init_test.go:670`（`executorOptions`） | 同上 |
| `cmd/init_test.go:702`（`listenPreset` 表驱动） | 同上 |
| `cmd/init_role_test.go` 全部 | 同上 |
| `cmd/prompter_test.go` 全部 | `internal/initflow/prompter_test.go` |

其余 `cmd/init_test.go` 的用例（跑整条 `handoff init` 命令的）**留在 `cmd`**，它们测的是命令装配，不是问答逻辑。

- [ ] **Step 5: 全量验证**

Run:
```bash
gofmt -l ./cmd ./internal/initflow && go vet ./cmd/... ./internal/initflow/... && go test ./cmd/... ./internal/initflow/... ./internal/config/...
```
Expected：`gofmt -l` 无输出；vet 干净；测试全过。

**若测试数量比搬迁前少**，说明有用例在搬迁中丢了——回去找，不要放行。搬迁前先记下基线：`go test ./cmd/... -count=1 -v 2>&1 | grep -c "^=== RUN"`。

- [ ] **Step 6: 用例集合比对，确认搬迁没丢东西**

> **本步骤原为「手工跑一次 `handoff init` 确认行为没变」，2026-08-17 派发中作废并替换。**
> 原因：`cmd/init.go:100` 取 `initStdinIsTTY()`，管道进来的 stdin 不是 TTY，会走 102 行的
> **非交互降级分支**——打一句「未交互配置」、`config.Save` 写盘、return，`askAll` 与
> `maybeInstallService` 根本不被调用。喂进去的换行送给了一段压根不读它们的代码路径，
> 想验的「问答顺序与默认值」一个字都验不到。
>
> **更要紧：不要想办法给它一个真 TTY。** 走到交互分支的尽头是 `cmd/init.go:449`
> `p.Confirm("现在把 agentd 交给本机进程管理器托管", true)`——默认值 **true**，空行取默认，
> 于是 `installService` 会在**执行机上**装一个 launchd 单元指向那份临时配置。
> 而执行机上正跑着派发本任务的那个 agentd。这不是「验证」，这是改承重墙。

搬迁前先在基线上取一份用例清单（已经搬完才想起这步，就在基线 commit 上开个临时 worktree 补取，**不要跳过**）：

```bash
go test ./cmd/... -count=1 -v 2>&1 | grep "^--- PASS\|^--- FAIL" | sort > /tmp/w5b2-before.txt
```

搬迁后跑两处的全部用例：

```bash
go test ./cmd/... ./internal/initflow/... -count=1 -v 2>&1 | grep "^--- PASS\|^--- FAIL" | sort > /tmp/w5b2-after.txt
```

逐条比对用例名集合：

```bash
diff <(sed 's/^--- //' /tmp/w5b2-before.txt | awk '{print $2}' | sort) \
     <(sed 's/^--- //' /tmp/w5b2-after.txt  | awk '{print $2}' | sort)
```

Expected：**用例名集合完全一致，且 after 侧无 `--- FAIL`**。少一条就是搬迁中丢了用例，回去找。
两份清单的行数与 diff 的实际输出贴进 ledger。

**为什么这样比手工跑 CLI 更强**：`askAll` 的问答顺序与那四个默认值本来就有单元覆盖
（`cmd/init_test.go:140/670/702`、`cmd/init_role_test.go` 全部），它们走 `prompter` 接口的
脚本化实现、不依赖 TTY——那才是「行为没变」的真判据。

- [ ] **Step 7: Commit**

```bash
git add -A internal/initflow cmd
git commit -m "refactor(initflow): 把 init 问答纯逻辑下沉到 internal/initflow 并导出

spec §4.4.1 核实：那批逻辑与 TUI 已解耦，但七个标识符全未导出，桌面壳够不着；
而就地导出会迫使薄壳 import cmd，把 31 个 cobra 子命令链进桌面壳（§4.4.2）。
故下沉为独立包，CLI 与 GUI 共用同一份问题集与默认值。行为不变，测试随迁。"
```

---

## Task 2: 事件驱动的 `Prompter` —— 让 `AskAll` 驱动图形界面

**Files:**
- Create: `desktop/internal/shell/wizard.go`
- Test: `desktop/internal/shell/wizard_test.go`

**Interfaces:**
- Consumes: `internal/initflow`（`Prompter`、`Option`、`ErrCanceled`）
- Produces: `NewEventPrompter`、`Transport`、`Question`、`NewNoticeWriter`，Task 7 装配时使用

### 设计要点

`initflow.AskAll` 是**同步阻塞**的：它顺序调用 `Select` / `Input` / `Confirm`。GUI 是**异步事件**的。桥接方式是让 `Select` 把问题发出去后阻塞在 channel 上，等前端的答案回来。

**`Transport` 必须是接口，不能直接吃 Wails 的 `app.Event`**——否则本包就 import 了 Wails，违反全局约束，也没法用普通 `go test` 覆盖。

- [ ] **Step 1: 写失败的测试**

创建 `desktop/internal/shell/wizard_test.go`：

```go
package shell_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/initflow"
)

// fakeTransport 记录发出的问题，并按预置答案逐个应答。
type fakeTransport struct {
	asked   []shell.Question
	answers []string
	notices []string
}

func (f *fakeTransport) Ask(q shell.Question) (string, error) {
	f.asked = append(f.asked, q)
	if len(f.answers) == 0 {
		return "", errors.New("测试用例没有预置足够的答案")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func (f *fakeTransport) Notice(line string) { f.notices = append(f.notices, line) }

func TestEventPrompterSelectReturnsAnswer(t *testing.T) {
	tr := &fakeTransport{answers: []string{"executor"}}
	p := shell.NewEventPrompter(context.Background(), tr)

	opts := []initflow.Option{{Value: "coordinator", Label: "协调者"}, {Value: "executor", Label: "执行机"}}
	got, err := p.Select("这台机器的角色", opts, "coordinator")
	if err != nil {
		t.Fatalf("Select 返回错误：%v", err)
	}
	if got != "executor" {
		t.Errorf("Select=%q, want %q", got, "executor")
	}
	if len(tr.asked) != 1 {
		t.Fatalf("应当只发出 1 个问题，实际 %d", len(tr.asked))
	}
	q := tr.asked[0]
	if q.Kind != "select" || q.Title != "这台机器的角色" || q.Default != "coordinator" {
		t.Errorf("问题描述不对：%+v", q)
	}
	if len(q.Options) != 2 || q.Options[1].Value != "executor" {
		t.Errorf("选项没有原样传给前端：%+v", q.Options)
	}
}

// 空答案必须落回默认值——这是「一路回车保持不变」在 GUI 侧的对应物。
func TestEventPrompterEmptyAnswerFallsBackToDefault(t *testing.T) {
	tr := &fakeTransport{answers: []string{""}}
	p := shell.NewEventPrompter(context.Background(), tr)
	got, err := p.Input("执行者模型", "sonnet")
	if err != nil {
		t.Fatalf("Input 返回错误：%v", err)
	}
	if got != "sonnet" {
		t.Errorf("Input=%q, want 默认值 %q", got, "sonnet")
	}
}

func TestEventPrompterConfirmParsesBool(t *testing.T) {
	tr := &fakeTransport{answers: []string{"false"}}
	p := shell.NewEventPrompter(context.Background(), tr)
	got, err := p.Confirm("自动同步", true)
	if err != nil {
		t.Fatalf("Confirm 返回错误：%v", err)
	}
	if got {
		t.Error("Confirm 应当返回 false")
	}
}

// 用户关掉向导窗口 = 取消。必须映射成 initflow.ErrCanceled，
// 因为 cmd 侧靠它决定「不写盘」——半截答案落盘比取消本身更糟。
func TestEventPrompterCancelMapsToErrCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := shell.NewEventPrompter(ctx, &fakeTransport{answers: []string{"x"}})
	if _, err := p.Input("随便问", "d"); !errors.Is(err, initflow.ErrCanceled) {
		t.Fatalf("取消后应返回 initflow.ErrCanceled，实际 %v", err)
	}
}

// AskAll 写给 io.Writer 的说明文字在 GUI 里不能凭空丢掉：
// warnIfNotReady 之类的警告是用户必须看到的。
func TestNoticeWriterForwardsNonBlankLines(t *testing.T) {
	tr := &fakeTransport{}
	w := shell.NewNoticeWriter(tr)
	if _, err := w.Write([]byte("\n第一行\n\n第二行\n")); err != nil {
		t.Fatalf("Write 返回错误：%v", err)
	}
	if len(tr.notices) != 2 {
		t.Fatalf("应当转发 2 条非空通知，实际 %d：%v", len(tr.notices), tr.notices)
	}
	if !strings.Contains(tr.notices[0], "第一行") {
		t.Errorf("通知内容不对：%v", tr.notices)
	}
}

// 整条链路：真的用 EventPrompter 驱动 initflow.AskAll 跑一遍。
// 这是本 task 存在的理由——不是「实现了一个接口」，而是
// 「AskAll 一行不改就能驱动 GUI」。
func TestAskAllRunsThroughEventPrompter(t *testing.T) {
	tr := &fakeTransport{answers: []string{"coordinator", "true"}}
	p := shell.NewEventPrompter(context.Background(), tr)
	cfg := newTestConfig()

	done := make(chan error, 1)
	go func() {
		_, _, err := initflow.AskAll(shell.NewNoticeWriter(tr), p, cfg, nil, false)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AskAll 经 EventPrompter 失败：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskAll 超时未返回：EventPrompter 可能死锁了")
	}
	if len(tr.asked) == 0 {
		t.Error("一个问题都没发给前端")
	}
}
```

`newTestConfig` 由实现者按 `config.Config` 的实际形态补一个最小可用值（`&config.Config{}` 通常即可；若 `AskAll` 的协调者分支要求某字段非空，按需补）。**协调者分支上的答案数量以实际问题数为准**——`fakeTransport` 答案耗尽时会报「测试用例没有预置足够的答案」，按报错补齐，不要改 `AskAll`。

- [ ] **Step 2: 跑测试，确认失败**

Run: `cd desktop && go test ./internal/shell/ -run 'Wizard|EventPrompter|NoticeWriter|AskAll' -v`
Expected: FAIL —— `undefined: shell.NewEventPrompter`

- [ ] **Step 3: 实现**

创建 `desktop/internal/shell/wizard.go`。文件头注释（职责 + 边界）必须说明：本文件把同步的 `initflow.AskAll` 桥接到异步 GUI；**不 import Wails**，Wails 的事件收发在 `main.go` 里实现 `Transport`。

两个类型的声明**逐字照抄**——Task 6 的前端 TS 接口按这个形状读字段，字段名对不上的症状是「向导页一片空白且无报错」：

```go
// Question 是发给前端的一问。
//
// **刻意不加 json tag**：Go 默认按字段名原样序列化（Kind / Title / Default /
// Options），前端 wizard.ts 的 interface 正是按这些名字读的。加一组小写 tag
// 会让前端拿到一个字段全 undefined 的对象——而且不报错，只是渲染成空白页。
type Question struct {
	// Kind 取 "select" / "input" / "confirm"，决定前端渲染成哪种控件。
	Kind string
	// Title 是问题文本，直接来自 initflow，不在这里改写。
	Title string
	// Default 是预选值；confirm 用 "true" / "false" 表示。
	Default string
	// Options 仅 Kind=="select" 时有意义。
	Options []initflow.Option
}

// Transport 是问答的传输层。**它是接口而不是 Wails 的 app.Event**，
// 因为本包一旦 import Wails 就再也不能用普通 go test 覆盖，
// 而这套桥接逻辑（空答落默认、非法值拒绝、取消映射）恰恰是最需要测的部分。
// Wails 侧的实现在 desktop/main.go。
type Transport interface {
	// Ask 发出一问并阻塞等答案。返回的字符串是原始答案，可能为空。
	Ask(q Question) (string, error)
	// Notice 转发一条面向用户的说明或警告，不需要应答。
	Notice(line string)
}
```

其余要点（实现由实现者定，但这些是硬要求）：

- `NewEventPrompter(ctx context.Context, tr Transport) initflow.Prompter`。
- **每个方法进入时先查 `ctx.Err()`**，非 nil 直接返回 `initflow.ErrCanceled`（包一层带上下文的错误也可，但必须 `errors.Is` 得到）。
- `Select`：答案为空 → 返回 `def`；答案不在 `options` 的 `Value` 集合里 → 返回错误（**不要静默接受**，那会把一个非法值写进 config.yaml）。
- `Input`：答案为空 → 返回 `def`。
- `Confirm`：解析 `strconv.ParseBool`；空 → 返回 `def`；解析失败 → 返回错误。
- `NewNoticeWriter(tr Transport) io.Writer`：按 `\n` 切分，跳过空行与纯空白行，逐行 `tr.Notice`。**必须处理跨 Write 调用的半行**（`fmt.Fprintf` 可能分多次写），用内部缓冲累积到遇见换行为止。

日志：`Ask` 前后各一条 Debug（问题标题、是否取到答案），取消与解析失败各一条 Warn/Error 带上下文。**不要打答案内容**——里面可能有 token 或私有路径。

- [ ] **Step 4: 跑测试，确认通过**

Run: `cd desktop && go test ./internal/shell/ -v`
Expected: PASS，含 W5b-1 已有的全部用例。

- [ ] **Step 5: Commit**

```bash
git add -A desktop/internal/shell
git commit -m "feat(desktop): 事件驱动的 initflow.Prompter，让 AskAll 直接驱动 GUI

Transport 是接口而非 Wails 事件，因此本包仍不 import Wails，可用普通 go test
覆盖整条链路（含一个真的跑 AskAll 的用例）。NoticeWriter 把 AskAll 写给
io.Writer 的警告转成前端通知，避免 GUI 里静默丢掉 warnIfNotReady 的内容。"
```

---

## Task 3: `desktop/internal/embedbin` —— 内嵌二进制的双形态

**Files:**
- Create: `desktop/internal/embedbin/embedbin.go`（共享 API 与文档）
- Create: `desktop/internal/embedbin/embed.go`（`//go:build embedbin`）
- Create: `desktop/internal/embedbin/stub.go`（`//go:build !embedbin`）
- Create: `desktop/internal/embedbin/.gitignore`
- Test: `desktop/internal/embedbin/embed_test.go`（`//go:build embedbin`）、`stub_test.go`（`//go:build !embedbin`）

**Interfaces:**
- Produces: `Available() bool`、`Open() (io.ReadCloser, error)`，Task 4 使用

### 为什么必须是两份实现

与 `internal/webui`（W5a）**完全同一个问题**：`go:embed` 指向不存在的文件是**编译期错误**。开发机上不会有一份编译好的 `handoff` 躺在 `desktop/internal/embedbin/` 里，无条件 embed 会让 `go build ./...` 和 `go test ./...` 在每台没预先构建的机器上整片失败。而把 18MB 二进制提交进仓库既荒唐，又会与「dispatch 要求工作区干净」冲突。

**照搬 `internal/webui` 的形状**（`webui.go` + `embed.go` + `stub.go` + 两侧各一个测试），命名与注释风格保持一致，不要另造一套。

spec §5 没有写这一条——它只裁决了「内嵌」而没处理产物缺席。这里按 §3.1 已确立的先例补齐，**不是新增设计**。

- [ ] **Step 1: 写两侧测试**

`desktop/internal/embedbin/stub_test.go`：

```go
//go:build !embedbin

package embedbin_test

import (
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
)

// 默认构建下必须诚实地报告「没有内嵌二进制」，且 Open 必须返回错误
// 而不是一个空 reader——释出一个 0 字节的 handoff 比不释出坏得多。
func TestStubReportsUnavailable(t *testing.T) {
	if embedbin.Available() {
		t.Fatal("不带 embedbin 标签时 Available() 必须为 false")
	}
	if _, err := embedbin.Open(); err == nil {
		t.Fatal("不带 embedbin 标签时 Open() 必须返回错误")
	}
}
```

`desktop/internal/embedbin/embed_test.go`：

```go
//go:build embedbin

package embedbin_test

import (
	"io"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
)

// 只在 release 构建路径跑。这道门存在的理由与 webui 那道相同：
// go:embed 一个 0 字节占位文件也能编译通过，「编译过了」不代表
// 里面真的是一个可执行的 handoff。
func TestEmbeddedBinaryIsPlausible(t *testing.T) {
	if !embedbin.Available() {
		t.Fatal("带 embedbin 标签时 Available() 必须为 true")
	}
	rc, err := embedbin.Open()
	if err != nil {
		t.Fatalf("Open() 失败：%v", err)
	}
	defer rc.Close()
	n, err := io.Copy(io.Discard, rc)
	if err != nil {
		t.Fatalf("读取内嵌二进制失败：%v", err)
	}
	// handoff 的 release 产物是 18MB 量级；低于 1MB 说明嵌进来的
	// 不是真产物（多半是占位文件或半截拷贝）
	if n < 1<<20 {
		t.Fatalf("内嵌二进制只有 %d 字节，不像真产物", n)
	}
}
```

- [ ] **Step 2: 跑默认侧，确认失败**

Run: `cd desktop && go test ./internal/embedbin/`
Expected: FAIL —— 包不存在。

- [ ] **Step 3: 实现三个文件**

`embedbin.go` 只放 package 文档（照 `internal/webui/webui.go` 的结构），说明：职责（持有内嵌的 handoff 二进制并按标签切换形态）、边界（不负责释出、不判断版本、不产生二进制——产物由构建链拷进来）、以及「为什么要两份实现」。

`embed.go`（`//go:build embedbin`）：`//go:embed handoff` 嵌入同目录下的 `handoff` 文件；`Available()` 返回 `true`；`Open()` 返回该文件的 reader。

`embedbin.go` 里还要有一个版本变量——**内嵌的是一堆字节，没法问它自己是什么版本**：

```go
// Version 是内嵌二进制的版本号，由构建链经 ldflags 注入：
//
//	-X github.com/Xsxdot/handoff/desktop/internal/embedbin.Version=${TAG}
//
// 与 handoff 本体既有的注入路径（internal/buildinfo.releaseVersion，
// 见 release.yml:85）是同一条契约的两端：同一次 release 用同一个 TAG 注入两边。
//
// **默认为空是刻意的**：开发构建下没有注入，此时版本「判不出」，
// 释出决策必须走保守分支（用用户已有的，不覆盖）——见 shell.DecideRelease。
// 注入这一步属 W5b-3，本 plan 只保证空值时行为正确。
var Version string
```

`stub.go`（`//go:build !embedbin`）：`Available()` 返回 `false`；`Open()` 返回一个说明性错误，文案要指出「本次构建未带 `-tags embedbin`」，**不要只说 "not available"**——那种报文出现在用户机器上时没人知道下一步该干什么。

`.gitignore` 内容：

```
handoff
handoff.exe
```

- [ ] **Step 4: 跑测试**

Run: `cd desktop && go test ./internal/embedbin/ -v`
Expected: PASS（默认侧）。

再验带标签一侧会因缺文件而**编译失败**（这是预期行为，不是缺陷）：

Run: `cd desktop && go build -tags embedbin ./internal/embedbin/ 2>&1 | head -3`
Expected: 报 `pattern handoff: no matching files found`。把这条实际输出记进 ledger——它证明了「缺席即编译期失败」这个前提是真的，也就证明了双实现的必要性。

再验放进一份假产物后能构建、且假产物会被 1MB 门槛拦下：

```bash
cd desktop && head -c 1000 /dev/urandom > internal/embedbin/handoff \
  && go test -tags embedbin ./internal/embedbin/ ; rm -f internal/embedbin/handoff
```
Expected: 测试 FAIL 并报「不像真产物」。**确认后必须删掉这个假文件**（上面的命令已带 `rm`），并跑 `git status --porcelain` 确认工作区干净。

- [ ] **Step 5: Commit**

```bash
git add -A desktop/internal/embedbin
git commit -m "feat(desktop): embedbin 双形态承载内嵌 handoff 二进制

与 internal/webui 同一个问题、同一套解法：go:embed 缺席是编译期错误，
而产物既不能提交进仓库（构建后工作区变脏，与 dispatch 的干净要求冲突）
也不能无条件 embed。默认构建走 stub，release 带 -tags embedbin。
带标签一侧的测试卡 1MB 下限，防止占位文件冒充真产物。"
```

---

## Task 4: 释出逻辑 —— spec §5.3 的三态规则

**Files:**
- Create: `desktop/internal/shell/release.go`
- Test: `desktop/internal/shell/release_test.go`

**Interfaces:**
- Consumes: `desktop/internal/embedbin`
- Produces: `ReleaseDecision`、`DecideRelease`、`ReleaseBinary`，Task 7 使用

### 规则（逐字来自 spec §5.3）

| 现状 | 动作 |
|---|---|
| 已有且能跑（`~/.local/bin/handoff` 或 PATH 上） | 直接用，**不释出** |
| 没有 | 释出内嵌的那份，`chmod 0755` |
| 已有但比内嵌的旧 | **提示，不自动换** |

第三条的理由：换版要重启 agentd。

**承重：绝不覆盖用户已有的安装。** 这不是「一般不覆盖」——第三态明确要求即使内嵌的更新也只提示。

- [ ] **Step 1: 写失败的测试**

创建 `desktop/internal/shell/release_test.go`。**决策必须与副作用分离**：`DecideRelease` 是纯函数（吃「现状」出「动作」），`ReleaseBinary` 才碰文件系统。这样三态规则可以穷举测试，不需要造 18MB 的假二进制。

```go
package shell_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestDecideReleaseThreeStates(t *testing.T) {
	cases := []struct {
		name      string
		existing  string // 已有安装的路径，空=没有
		existVer  string // 已有安装的版本，空=判不出
		embedVer  string
		want      shell.ReleaseDecision
	}{
		{"没有安装就释出", "", "", "v1.2.0", shell.DecisionInstall},
		{"已有且更新就直接用", "/home/u/.local/bin/handoff", "v1.3.0", "v1.2.0", shell.DecisionUseExisting},
		{"已有且同版就直接用", "/home/u/.local/bin/handoff", "v1.2.0", "v1.2.0", shell.DecisionUseExisting},
		{"已有但更旧只提示", "/home/u/.local/bin/handoff", "v1.1.0", "v1.2.0", shell.DecisionNotifyOutdated},
		// 判不出已有版本时必须偏保守：用它，不覆盖。
		// 猜错的代价不对称——不覆盖最坏是用户少了个新特性，
		// 覆盖错了是把用户手装的二进制换掉。
		{"已有但版本判不出就直接用", "/home/u/.local/bin/handoff", "", "v1.2.0", shell.DecisionUseExisting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shell.DecideRelease(tc.existing, tc.existVer, tc.embedVer)
			if got != tc.want {
				t.Errorf("DecideRelease(%q,%q,%q)=%v, want %v",
					tc.existing, tc.existVer, tc.embedVer, got, tc.want)
			}
		})
	}
}

// 释出必须落在 0755，否则 launchd 拉不起来，
// 症状是「装好了但 agentd 起不来」，排查成本很高。
func TestReleaseBinaryWritesExecutable(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "bin", "handoff")
	if err := shell.ReleaseBinary(dst, strings.NewReader("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatalf("ReleaseBinary 失败：%v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("释出后目标不存在：%v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("权限 %v，want 0755", fi.Mode().Perm())
	}
}

// 承重：已有文件在任何情况下都不得被 ReleaseBinary 覆盖。
// DecideRelease 说了不释出还是走到这里，属于调用方 bug——
// 此时必须报错，而不是「顺手帮他覆盖了」。
func TestReleaseBinaryRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "handoff")
	if err := os.WriteFile(dst, []byte("用户自己装的"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := shell.ReleaseBinary(dst, strings.NewReader("新的")); err == nil {
		t.Fatal("目标已存在时 ReleaseBinary 必须报错")
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "用户自己装的" {
		t.Fatalf("原文件被改动了：%q", b)
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `cd desktop && go test ./internal/shell/ -run Release -v`
Expected: FAIL —— `undefined: shell.DecideRelease`

- [ ] **Step 3: 实现**

`ReleaseDecision` 是 `int` 枚举，三个值 `DecisionInstall` / `DecisionUseExisting` / `DecisionNotifyOutdated`，带 `String()`。

版本比较：**优先用仓库里已有的比较逻辑**。先搜 `grep -rn "func.*[Cc]ompare.*[Vv]ersion\|semver" --include="*.go" .`（B59 的自更新大概率已有一份）；找到就复用，找不到再写一个最小实现（按 `v?major.minor.patch` 逐段数值比较，解析失败视为「判不出」）。**不要引入新的第三方 semver 依赖。**

`ReleaseBinary(dst string, data []byte) error`：`MkdirAll` 父目录（0755）→ 若 `dst` 已存在则**报错返回** → 写临时文件再 `os.Rename` 落位（避免半截文件被当成可执行）→ `chmod 0755`。

日志：决策结果一条 Info（含三态中的哪一态、已有版本、内嵌版本）；释出成功一条 Info（含落点）；每个错误分支一条 Error 带 cause。

- [ ] **Step 4: 跑测试**

Run: `cd desktop && go test ./internal/shell/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A desktop/internal/shell
git commit -m "feat(desktop): 内嵌二进制的三态释出规则（spec §5.3）

决策与副作用分离：DecideRelease 是纯函数可穷举测试，ReleaseBinary 才碰盘。
承重两条：已存在的目标一律不覆盖（覆盖用户手装的二进制代价远大于少个新特性），
版本判不出时偏保守走「直接用」。释出走临时文件 + rename，避免半截文件被执行。"
```

---

## Task 5: 绝对二进制路径 —— 收口 W5b-1 的承接项

**Files:**
- Create: `desktop/internal/shell/binpath.go`
- Test: `desktop/internal/shell/binpath_test.go`
- Modify: `desktop/main.go`（只改 `specFor`，其余不动）

**Interfaces:**
- Produces: `ResolveBinPath(explicit string) (string, error)`

### 背景

`desktop/main.go:133` 的 `specFor` 现在返回 `service.Spec{BinPath: "handoff"}`，并在注释里写明这是 W5b-2 的承接项。**launchd 解析不了相对路径**：agentd 会被装成一个永远起不来的 service，而症状要等到用户机器上才出现。

- [ ] **Step 1: 写失败的测试**

```go
package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// launchd / systemd 都要求绝对路径。返回相对路径等于装了一个
// 永远起不来的 service，而且失败要到用户机器上才暴露。
func TestResolveBinPathIsAlwaysAbsolute(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "handoff")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := shell.ResolveBinPath(bin)
	if err != nil {
		t.Fatalf("ResolveBinPath 失败：%v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("返回了相对路径 %q", got)
	}
}

func TestResolveBinPathRejectsMissing(t *testing.T) {
	_, err := shell.ResolveBinPath(filepath.Join(t.TempDir(), "不存在"))
	if err == nil {
		t.Fatal("目标不存在时必须报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("报错应当带上被查找的路径，实际：%v", err)
	}
}

// 符号链接必须解开：~/.local/bin/handoff 常是指向别处的软链，
// 把软链写进 plist 后用户一改链接 agentd 就起不来。
func TestResolveBinPathFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-handoff")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "handoff")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本环境不支持符号链接：%v", err)
	}
	got, err := shell.ResolveBinPath(link)
	if err != nil {
		t.Fatalf("ResolveBinPath 失败：%v", err)
	}
	if filepath.Base(got) != "real-handoff" {
		t.Errorf("符号链接没有被解开：%q", got)
	}
}
```

- [ ] **Step 2: 跑测试，确认失败**

Run: `cd desktop && go test ./internal/shell/ -run BinPath -v`
Expected: FAIL —— `undefined: shell.ResolveBinPath`

- [ ] **Step 3: 实现**

`ResolveBinPath(explicit string) (string, error)`：
- `explicit` 非空 → 用它；空 → 依次尝试 `~/.local/bin/handoff`、`exec.LookPath("handoff")`。
- `os.Stat` 确认存在且是常规文件（或指向常规文件的软链）；不存在则返回**带上被查找路径**的错误。
- `filepath.EvalSymlinks` 解链，再 `filepath.Abs`。

改 `desktop/main.go` 的 `specFor`：调 `ResolveBinPath("")`，失败时返回错误（签名改为 `(service.Spec, error)`），调用点把错误经 `showError` 呈现——**不要吞掉它退回相对路径**。同时删掉那段「W5b-2 承接项」的注释，换成说明为什么必须绝对路径。

- [ ] **Step 4: 跑测试 + 构建**

Run: `cd desktop && go test ./... && go build ./...`
Expected: 全过。

- [ ] **Step 5: Commit**

```bash
git add -A desktop
git commit -m "fix(desktop): specFor 给 launchd 绝对路径，收口 W5b-1 承接项

BinPath 相对名会让 service 装成一个永远起不来的单元，且症状只在用户机器上出现。
ResolveBinPath 解符号链接后取绝对路径；解析失败向上报错而不是退回相对路径。"
```

---

## Task 6: 向导界面

**Files:**
- Create: `desktop/frontend/src/wizard.ts`
- Modify: `desktop/frontend/index.html`、`desktop/frontend/src/main.ts`

**Interfaces:**
- Consumes: Wails 事件 `wizard-question`（Go→前端，payload 是 Task 2 的 `Question`）、`wizard-notice`（Go→前端，字符串）、`wizard-answer`（前端→Go，字符串）、`wizard-done`（Go→前端）

### 现状与范围

`desktop/frontend/` 目前**整个是 Wails 模板**（Greet 按钮、TypeScript logo、时钟页脚）。窗口以 `URL: "/"` 创建，因此薄壳启动时用户**会先看到这个模板页**再被 `SetURL` 换成控制台。

本 task 把模板页替换为向导页。**范围克制**：一个能问完问题的干净页面即可，不做视觉设计投入——用户在这个页面上平均停留一次，之后永远看到的是 agentd 伺服的控制台。

删除模板遗留：`wails.png`、`typescript.svg`、`bg-desktop.jpg`、`bg-mobile.jpg` 及 `index.html` 里引用它们的 DOM。**`public/` 与 `dist/` 两处都要清**（`dist/` 是构建产物，删 `public/` 后重新构建即可，不要手工删 `dist/` 里的文件然后提交）。

- [ ] **Step 1: 重写 `index.html`**

结构最小化：一个 `<main id="wizard">`，内含标题、问题区 `#question`、通知区 `#notices`、按钮区。保留 `<link rel="stylesheet" href="/style.css">`（`public/style.css` 保留，按需精简）。移除全部模板 DOM 与模板图片引用。`<title>` 改为 `handoff 首次配置`。

- [ ] **Step 2: 写 `src/wizard.ts`**

```ts
import {Events} from "@wailsio/runtime";

interface Option { Value: string; Label: string }
interface Question { Kind: "select" | "input" | "confirm"; Title: string; Default: string; Options?: Option[] }

const box = document.getElementById("question")!;
const notices = document.getElementById("notices")!;

// 每一问渲染成一组控件，用户提交后把答案原样发回 Go。
// 空提交发空串——Go 侧 EventPrompter 会落回默认值，
// 与 CLI「一路回车保持不变」的行为一致。
function render(q: Question) {
    box.innerHTML = "";
    const h = document.createElement("h2");
    h.textContent = q.Title;
    box.appendChild(h);

    let read: () => string;

    if (q.Kind === "select") {
        const sel = document.createElement("select");
        for (const o of q.Options ?? []) {
            const opt = document.createElement("option");
            opt.value = o.Value;
            opt.textContent = o.Label;
            if (o.Value === q.Default) opt.selected = true;
            sel.appendChild(opt);
        }
        box.appendChild(sel);
        read = () => sel.value;
    } else if (q.Kind === "confirm") {
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.checked = q.Default === "true";
        box.appendChild(cb);
        read = () => String(cb.checked);
    } else {
        const inp = document.createElement("input");
        inp.type = "text";
        inp.value = q.Default;
        box.appendChild(inp);
        read = () => inp.value;
        inp.addEventListener("keydown", (e) => {
            if ((e as KeyboardEvent).key === "Enter") submit();
        });
    }

    const btn = document.createElement("button");
    btn.textContent = "下一步";
    btn.addEventListener("click", () => submit());
    box.appendChild(btn);

    function submit() {
        btn.disabled = true;          // 防重复提交：一问一答，多发一次会错位
        Events.Emit("wizard-answer", read());
    }
}

Events.On("wizard-question", (ev: {data: Question}) => render(ev.data));

Events.On("wizard-notice", (ev: {data: string}) => {
    const p = document.createElement("p");
    p.textContent = ev.data;
    notices.appendChild(p);
});

Events.On("wizard-done", () => {
    box.innerHTML = "<h2>配置完成，正在启动 agentd…</h2>";
});
```

`src/main.ts` 改为只 `import "./wizard";`（删掉模板的 Greet / 时钟 / 版本号逻辑）。

**若 `@wailsio/runtime` 的事件 payload 形状与上面的 `{data: T}` 不符**（v3 beta 版本间有过变化），以运行时实际形状为准调整，并在 ledger 里记下实际形状——不要改 Go 侧去迁就一个猜错的前端类型。

- [ ] **Step 3: 构建验证**

Run: `cd desktop && wails3 task build`
Expected: 构建成功，无 TypeScript 错误。

Run: `git status --porcelain`
Expected: **只有你自己改的源文件**，不得出现 `desktop/frontend/dist/` 下的任何条目（那说明 `.gitignore` 漏了或你提交了产物）。

- [ ] **Step 4: Commit**

```bash
git add -A desktop/frontend
git commit -m "feat(desktop): 首次配置向导界面，替换 Wails 模板页

一问一答式渲染，Go 侧发 wizard-question、前端回 wizard-answer。
空提交发空串，由 EventPrompter 落回默认值，与 CLI「一路回车保持不变」一致。
顺带清掉模板遗留的 Greet/时钟/示意图片——窗口以 / 创建，模板页此前会在
薄壳启动时一闪而过。"
```

---

## Task 7: 装配 —— 把向导接进启动序列

**Files:**
- Modify: `desktop/main.go`

**Interfaces:**
- Consumes: Task 1-6 的全部产出

### 目标序列

`main.go:59` 现在的 `StateUnconfigured` 分支是一个「请去终端跑 handoff init」的错误框。替换为：

```
StateUnconfigured
  → 释出内嵌二进制（DecideRelease → ReleaseBinary，按三态）
  → 窗口显示向导页
  → 起 goroutine 跑 initflow.AskAll（经 EventPrompter）
  → 成功：config.Save → EnsureRunning → ConsoleURL → SetURL
  → 取消（ErrCanceled）：不写盘，窗口留在向导页并说明可以重来
```

**承重：`AskAll` 返回错误时绝不 `config.Save`。** 半截答案落盘会造出一份「配过但配错」的配置，而下次启动时 `Resolve` 会认为这台机器已配置，用户再也回不到向导——这个坑比取消本身糟得多。

### ⚠ 必须接上 `initflow.InstallService`（2026-08-17 派发中补，原 plan 漏了）

Task 1 交付时发现 `MaybeInstallService` 要调的 `installService` 住在 `cmd/service.go`，
直接搬会造成 `initflow → cmd` 循环依赖。交付的解法是留一个函数变量
`var initflow.InstallService func(w io.Writer, cfgPath string) error`，由 `cmd/service.go`
的 `init()` 注入。CLI 侧因此照常工作。

**但薄壳不 import `cmd`**（那正是 §4.4.2 的全部理由），所以在桌面这条路上
`initflow.InstallService` 是 `nil`。交付已做了不 panic 的兜底，会打印
「没有可用的服务安装入口，稍后单独重跑 handoff service install 即可」——
**而这恰好是 W5b-2 的主场景**：一台干净机器，用户装桌面端就是为了不必去敲命令行。
向导问完「要不要托管」再回一句「你自己去命令行装吧」，等于把这一期的价值原地抵消。

所以装配时**必须**注入这个缝，指向薄壳自己的安装入口（复用 W5b-1 的
`desktop/internal/shell/lifecycle.go`，它已持有 `newManager = service.New`）：

- 在 `desktop/internal/shell` 里加一个 `func InstallService(w io.Writer, cfgPath string) error`，
  内部走 `internal/service` 装单元（与 `EnsureRunning` 共用同一条路径，**不要另写一套**）。
- 在 `main.go` 装配处赋值：`initflow.InstallService = shell.InstallService`。
- 加一条测试钉住：**`initflow.InstallService` 在薄壳装配后不得为 nil**。
  这条门的意义是防回归——将来有人重构装配顺序时，缝断掉的症状是「向导走完但 agentd
  没被托管」，而那要等到用户重启机器才暴露。

- [ ] **Step 1: 实现 Wails 侧的 `Transport`**

在 `main.go` 里实现 Task 2 的 `shell.Transport`：
- `Ask(q)`：`app.Event.Emit("wizard-question", q)`，然后阻塞等 `wizard-answer`。用一个带缓冲的 channel 接住；`app.Event.On("wizard-answer", ...)` 在装配时注册**一次**，把值送进 channel。
- `Notice(line)`：`app.Event.Emit("wizard-notice", line)`。

**不要在 `Ask` 里注册事件回调**——每问注册一次会累积 handler，第 N 问会收到 N 份答案。这是本 task 最容易踩的 bug，实现完请自己回头确认注册只发生一次。

- [ ] **Step 2: 改写 `StateUnconfigured` 分支**

释出二进制那一步：`embedbin.Available()` 为 false 时（开发构建）**不是错误**——记一条 Info 说明本次构建未内嵌，继续走向导；用户机器上的 release 构建才带标签。

向导跑在 goroutine 里，`AskAll` 阻塞不能挡住 Wails 主循环。完成后回到主流程（`EnsureRunning` → `ConsoleURL` → `SetURL`）。

- [ ] **Step 3: 更新文件头注释**

`main.go` 的头注释现在写着「不放业务逻辑」——这条**继续成立且必须保持**：本 task 加进来的只有 Wails 事件的收发与装配，向导逻辑在 `initflow`，桥接在 `shell`。在边界一节补一条：首次引导的问题集与默认值属于 `internal/initflow`，**不要在这里加问题**。

- [ ] **Step 4: 编译与既有测试**

Run: `cd desktop && go build ./... && go test ./... && gofmt -l .`
Expected: 构建通过，测试全过，`gofmt -l` 无输出。

- [ ] **Step 5: 真机走查（macOS）**

> ⚠ **如果你是被派发来执行这份计划的执行者：本步骤不在你的范围内，跳过它，在 ledger 里记一行「留审核者」即可。**
>
> 两条理由，都不是形式主义：
> 1. **走查要有人看着一个图形向导并逐题作答**，这件事你做不了。
> 2. **答完向导会走到 `shell.EnsureRunning`，它会在跑着的这台执行机上装 launchd 单元。**
>    而这台机器上正跑着派发本任务的那个 agentd。把它的托管指向一份临时 HOME 里的配置，
>    等于把手伸进承重墙。**不要为了「让这一步变绿」而想办法绕过去。**
>
> 本步骤由审核者在自己的机器上做。

（审核者执行）用**临时 HOME** 模拟一台没配过的机器，**绝不碰 `~/.handoff`**：

```bash
cd desktop && wails3 task build
HOME=$(mktemp -d) ./bin/handoff-desktop
```

确认：①窗口显示向导而不是错误框；②问题能逐个前进；③答完后配置写进了临时 HOME 下的 `.handoff/config.yaml`；④中途关窗后临时 HOME 下**没有** config.yaml 生成。

把实际观察逐条记进 ledger。**这四条里任何一条没做到就不算过**，不要用「代码看起来对」替代观察。

- [ ] **Step 6: Commit**

```bash
git add -A desktop
git commit -m "feat(desktop): 首次引导接进启动序列，干净机器双击即可完成配置

StateUnconfigured 从「请去终端跑 handoff init」换成图形向导：释出内嵌二进制、
经 EventPrompter 驱动 initflow.AskAll、成功才写盘。取消一律不写盘——半截配置会
让下次启动被判为「已配置」，用户再也回不到向导。
wizard-answer 的事件回调只注册一次（每问注册会让第 N 问收到 N 份答案）。"
```

---

## Task 8: 干净检出验收门

**Files:** 无（只验证）

W5a 与 W5b-1 都在这里栽过：构建产物让工作区变脏，而 handoff 自己的 `dispatch` 硬要求工作区干净。**必须在干净检出上验，不能在跑过构建的工作区上验。**

- [ ] **Step 1: 干净检出**

```bash
d=$(mktemp -d) && git clone -q --branch "$(git rev-parse --abbrev-ref HEAD)" . "$d/wc" && cd "$d/wc" && git log --oneline -1
```

- [ ] **Step 2: 默认构建路径**

```bash
go build ./... && go test ./... && (cd desktop && go build ./... && go test ./...)
```
Expected: 全过。**不带任何标签**——这证明没内嵌产物的机器上仓库是健康的。

- [ ] **Step 3: 薄壳构建 + 洁净判据**

```bash
cd desktop && wails3 task build && cd .. && git status --porcelain
```
Expected: `git status --porcelain` **输出为空**。

- [ ] **Step 4: 把实际输出贴进 ledger**

三步的实际输出（不是「通过了」三个字）贴进 ledger 文件。若第 3 步非空，**停下来报告冲突在哪，不要为了让门变绿而放宽判据**。

- [ ] **Step 5: Commit ledger**

```bash
git add docs/ledger-w5b2.md && git commit -m "docs(ledger): W5b-2 干净检出验收门实际输出"
```

---

## 不在本次范围内

- **Windows**：spec §4.6 选项 A。代码保持可编译，但不出资产、不做 Windows 专属分支。
- **构建链把 `handoff` 拷进 `desktop/internal/embedbin/` 并带 `-tags embedbin` 构建**：属 W5b-3（§6.2/§6.3）。本 plan 只保证「有产物时能嵌、没产物时不挂」。
- **macOS 签名顺序**（签 handoff → 嵌 → 签+公证薄壳）：spec §5.4 明确要求真机探针，属 W5b-3。
- **`~/.local/bin` 不在 PATH 上时提示用户**：spec 未裁决。遇到就记账，不要顺手实现。
