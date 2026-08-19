# 桌面端与 CLI 版本同步 + 新版通知 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让桌面用户换一份新 `.app` 之后，本机 agentd/CLI 自动同版；有新版安装包时能收到提示；协调者能从桌面端升级执行机。

**Architecture:** 两条独立的路。**同步**用 `.app` 内嵌的那份二进制，不出网，接在 `openConsole` 的已配置分支上、控制台加载之前；**通知**周期性查 GitHub（24h 限流），结果挂到托盘。执行机升级不重建编排，直接 exec `handoff upgrade --now` 并把输出流进一个新的升级面板窗口。

**Tech Stack:** Go 1.26 / Wails v3.0.0-beta.8 / TypeScript + Vite（内嵌前端）/ GitHub Actions

**Spec:** `docs/superpowers/specs/2026-08-19-desktop-cli-sync-and-update-notice-design.md`

## Global Constraints

- **日志**：桌面端用 `desktop/main.go` 里的包级 `logger`（`*slog.Logger`）；`shell` 包用包内 `var logger = slog.Default()` 那个测试缝。**禁止 `fmt.Printf` / `println` 作为日志机制。**
- **注释**：新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc comment（参数、返回、注意事项）；非显然分支必须有中文「为什么」注释。
- **D8（承重）**：同步路径上任何失败**绝不阻断打开控制台**。每个失败点都必须走到「记日志 + 如实告知 + 继续加载控制台」。
- **版本比较只能有一份实现**：全仓唯一入口是 `selfupdate.CompareVersion`。禁止新写第二份，禁止用字典序。
- **同步序列的相对顺序是承重的**：`EnsureRunning` → `client.Status` → `release.Activate` → `skill install` → `RestartAgentd` → 等 agentd 回来。
- **agentd「回来了」的判据是版本号相等**，不是「Status 调得通」。超时 90 秒。
- **任何新建的 Wails 窗口都必须挂 `events.Common.WindowRuntimeReady` 并在导航/发事件前 `shell.AwaitWebviewReady`**。漏挂 = rc7 那个「进程静默消失、无任何输出」的 bug 重演。
- **版本形态**只认 `vX.Y.Z`。解析不出一律走保守分支（不同步、不提示），不猜。

---

## Task 1: 真机验证 P3——Gatekeeper 拦不拦释出的二进制

**这是整个设计的承重底座。它若不成立，Task 5 起的整条同步路都要改成「在 bundle 内运行」。必须最先做，不能留到最后。**

> **✅ 已于 2026-08-19 由审核者在本地完成，P3 成立。** 取证原文见
> `docs/superpowers/plans/2026-08-19-desktop-cli-sync-and-update-notice.notes.md`。
> 本 task 保留在此是为了记录判据与复现方法（其中两条判据当初写错了，已修正）。

**Files:**
- 无代码改动。产出是一份写进 `docs/superpowers/plans/` 同名 `.notes.md` 的取证记录

**Interfaces:**
- Consumes: 无
- Produces: P3 结论（成立 / 不成立）。不成立时本 plan 的 Task 5-8 全部作废，需回 brainstorming

- [ ] **Step 1: 准备一个隔离的 HOME，绝不碰用户真实的 `~/.handoff`**

```bash
export P3HOME=$(mktemp -d /tmp/p3-XXXXXX)
mkdir -p "$P3HOME/.local/bin"
echo "$P3HOME"
```

- [ ] **Step 2: 取一份真实发布的 `.app`（rc10 已验证是签名公证并装订过的）**

```bash
cd "$P3HOME" && gh release download v0.3.0-rc10 --repo Xsxdot/handoff -p '*.dmg'
```

- [ ] **Step 3: 挂载并把 `.app` 拷到隔离 HOME（拷贝保留扩展属性，用 ditto 而非 cp）**

```bash
MNT=$(mktemp -d) && hdiutil attach "$P3HOME"/*.dmg -nobrowse -readonly -mountpoint "$MNT" && ditto "$MNT/handoff-desktop.app" "$P3HOME/handoff-desktop.app" && hdiutil detach "$MNT"
```

- [ ] **Step 3.5: 给 DMG 补上隔离属性（缺了这步整条验证是假绿）**

`gh release download` **不设** `com.apple.quarantine`，只留 `provenance` 和一个 diskimages
校验和。而本条问的恰恰是隔离属性会不会传染。拿 `gh` 下的 DMG 直接测必然"通过"，
且完全测不到真实用户的处境（用户是浏览器下载的）。在 Step 3 挂载**之前**补上：

```bash
xattr -w com.apple.quarantine "0081;$(printf %x $(date +%s));Safari;$(uuidgen)" "$P3HOME"/*.dmg
```

补完再挂载、`ditto` 拷出，`.app` 上应出现 `0281;...` 且 UUID 与 DMG 的一致——传染链成立。

- [ ] **Step 4: 用隔离 HOME **与隔离 PATH** 运行壳内的可执行文件**

隔离 HOME 下 `shell.Resolve` 会判 `StateUnconfigured`，于是 `releaseEmbedded` 被调、把内嵌二进制写到 `$P3HOME/.local/bin/handoff`。窗口会弹出向导——**不要填，直接关窗**，释出发生在向导之前。

**PATH 必须一起隔离。** `ResolveBinPath` 会搜 PATH：只设 `HOME` 时子进程继承操作者的
PATH，会找到**真实的** `~/.local/bin/handoff` 并判成 `use-existing` 直接不释出——
整个验证空转，而日志看着一切正常（`decision=use-existing`）。

```bash
env -i HOME="$P3HOME" PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR=/tmp   "$P3HOME/handoff-desktop.app/Contents/MacOS/handoff-desktop"
```

判据：日志里必须是 `释出决策 decision=install`，不是 `use-existing`。

- [ ] **Step 5: 确认二进制真的被释出了**

```bash
ls -l "$P3HOME/.local/bin/handoff"
```

Expected: 文件存在，权限 `-rwxr-xr-x`。**不存在则本步失败**——先看 `$P3HOME/.handoff/` 下或 stderr 里 `releaseEmbedded` 的日志，可能是 rc10 未带 `-tags embedbin`（那样日志会说「本次构建未内嵌 handoff 二进制」），此时改用带 embedbin 的构建重做。

- [ ] **Step 6: 判 P3 的四项判据**

```bash
echo "--- 1. 隔离属性有没有被传染 ---"; xattr -l "$P3HOME/.local/bin/handoff"
echo "--- 2. 签名 ---"; codesign -dv --verbose=2 "$P3HOME/.local/bin/handoff" 2>&1 | grep -E "Authority|TeamIdentifier|flags"
echo "--- 3. 公证票据 ---"; xcrun stapler validate "$P3HOME/.local/bin/handoff"
echo "--- 4. Gatekeeper 实判 ---"; spctl -a -vvv -t exec "$P3HOME/.local/bin/handoff"
```

**判据**（**已于 2026-08-19 实跑，第 3、4 项被证明用错了对象，此处已修正**）：

- **第 1 项是本条的正题**：`com.apple.quarantine` **不该**出现。Gatekeeper 对可执行文件的
  评估由隔离属性触发，没有它就不评估。实测结果是只有 `com.apple.provenance`——
  隔离属性传染到了 `.app`，但没有继续传染到它写出的文件上
- **第 2 项**：签名必须在，且带 `flags=0x10000(runtime)`
- **第 3 项 `stapler validate` 报「没有票据」是正常的，不是缺陷。** 苹果只支持把票据
  装订到 `.app` / `.dmg` / `.pkg`，**裸 Mach-O 可执行文件无法装订**
- **第 4 项 `spctl` 报 `rejected` 也是正常的——读它给的理由。** 原文是
  `rejected (the code is valid but does not seem to be an app)`，同一行明说了代码有效。
  `spctl -t exec` 对非 bundle 的命令行工具就是这么报

**判据要先确认它适用于被验对象。** 第 3、4 项若照原样当门用，会把一条成立的路径判成
不成立，然后整份设计被推倒重做——代价远大于验错方向。

- [ ] **Step 7: 真的执行它一次——这是唯一无法被前六步替代的判据**

```bash
env -i HOME="$P3HOME" PATH=/usr/bin:/bin:/usr/sbin:/sbin "$P3HOME/.local/bin/handoff" version
```

Expected: 打印版本号并退出 0。

**若被 SIGKILL（退出码 137、无任何输出）或弹出「无法打开」对话框，P3 不成立。** 停下上报，不要继续 Task 2——整条同步路的落点要重新设计。

- [ ] **Step 8: 把结论与四项原文落盘，然后清理**

把 Step 6/7 的**原始输出**（不是你的转述）写进 `docs/superpowers/plans/2026-08-19-desktop-cli-sync-and-update-notice.notes.md` 的「P3」小节，注明日期与用的是哪个 tag。

```bash
rm -rf "$P3HOME"
```

- [ ] **Step 9: Commit**

```bash
git add docs/superpowers/plans/2026-08-19-desktop-cli-sync-and-update-notice.notes.md
git commit -m "docs(plan): P3 真机取证——Gatekeeper 对释出到 ~/.local/bin 的二进制的判定"
```

---

## Task 2: 版本比较函数收敛成一份

spec §6.2。现状两份实现（`selfupdate` 未导出的 `cmpVersion`、`shell` 里另写的 `compareVersion`），本 task 之后只剩一份。

**Files:**
- Modify: `internal/selfupdate/clicheck.go`（`cmpVersion` → 导出为 `CompareVersion`，`parseVersion` 保持未导出）
- Modify: `internal/selfupdate/clicheck_test.go`（跟着改调用名）
- Modify: `desktop/internal/shell/release.go`（删掉本地 `compareVersion` 与 `parseVersion`，改调 `selfupdate.CompareVersion`）
- Modify: `desktop/internal/shell/release_test.go`（若有直接测 `compareVersion` 的用例，改测行为而非私有函数）

**Interfaces:**
- Consumes: 无
- Produces: `selfupdate.CompareVersion(a, b string) (int, bool)` — 全仓唯一的版本比较入口。`a` 比 `b` 旧/同/新时第一个返回值为 `-1/0/1`；任一侧不是 `vX.Y.Z` 形态时第二个返回值为 `false`，此时第一个无意义。Task 3、8 依赖它。

- [ ] **Step 1: 写失败测试——导出入口存在且语义正确**

追加到 `internal/selfupdate/clicheck_test.go`：

```go
// TestCompareVersionIsTheOnlyExportedComparator 钉住导出入口的存在与语义。
//
// 为什么要有这条：本函数历史上被写错过（B59 验收当场抓出反向提示——装了
// v0.1.1 的机器被劝「有新版本 v0.1.0」，根因是没按三段整数比）。它现在有
// 三个消费者（CLI 提示、桌面同步、桌面通知），错一次的代价乘以三。
func TestCompareVersionIsTheOnlyExportedComparator(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.1.0", "v0.1.1", -1, true},
		{"v0.1.1", "v0.1.0", 1, true},
		{"v0.1.0", "v0.1.0", 0, true},
		// 字典序会把 v0.10.0 判成比 v0.9.0 旧——这条是本函数存在的理由
		{"v0.10.0", "v0.9.0", 1, true},
		{"v0.9.0", "v0.10.0", -1, true},
		// 前缀 v 可有可无
		{"0.2.0", "v0.1.0", 1, true},
		// 形态不符一律 ok=false
		{"v0.1", "v0.1.0", 0, false},
		{"", "v0.1.0", 0, false},
		{"v0.1.0", "rc10", 0, false},
		{"v0.1.-1", "v0.1.0", 0, false},
	}
	for _, c := range cases {
		got, ok := selfupdate.CompareVersion(c.a, c.b)
		if ok != c.ok {
			t.Errorf("CompareVersion(%q,%q) ok = %v，想要 %v", c.a, c.b, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("CompareVersion(%q,%q) = %d，想要 %d", c.a, c.b, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/selfupdate/ -run TestCompareVersionIsTheOnly -v`
Expected: 编译失败，`undefined: selfupdate.CompareVersion`

- [ ] **Step 3: 导出它**

`internal/selfupdate/clicheck.go`：把 `func cmpVersion(a, b string) (int, bool)` 改名为 `CompareVersion`，doc comment 顶部改成：

```go
// CompareVersion 比较两个 vX.Y.Z 版本号，是**全仓唯一**的版本比较入口。
//
// 参数：
//   - a, b: 形如 v0.1.2 的标签，前缀 v 可有可无
//
// 返回：
//   - a 小于/等于/大于 b 时分别为 -1/0/1
//   - 任一侧不是 vX.Y.Z 形态时 ok 为 false，此时第一个返回值无意义
//
// 注意：
//   - 三段都按整数比，不能用字典序——字典序会判定 v0.10.0 比 v0.9.0 旧
//   - 只认 vX.Y.Z。release 工作流只产出这个形态，install.sh 的 latest_tag
//     也明确拒绝其余形态；解析不了时由调用方决定怎么办，这里不猜
//   - **不要在别处另写一份。** 消费者已有三个（CLI 提示、桌面同步、桌面
//     通知）；本函数被写错过一次（B59 验收抓出的反向提示），三份实现意味着
//     错一次要修三处、而且一定有一处会被漏掉
func CompareVersion(a, b string) (int, bool) {
```

同文件内 `NotifyLine` 里的 `cmpVersion(` 改为 `CompareVersion(`。

- [ ] **Step 4: 删掉 shell 包里那份**

`desktop/internal/shell/release.go`：删除 `compareVersion` 与 `parseVersion` 两个函数**及其注释**，在 import 块加 `"github.com/Xsxdot/handoff/internal/selfupdate"`，把 `DecideRelease` 里的

```go
	cmp, ok := compareVersion(existVer, embedVer)
```

改成

```go
	cmp, ok := selfupdate.CompareVersion(existVer, embedVer)
```

- [ ] **Step 5: 跑全量测试**

Run: `go test ./... && cd desktop && go test ./...`
Expected: 全绿。`desktop/internal/shell` 里若有直接调 `compareVersion` 的测试会编译失败——改成调 `selfupdate.CompareVersion`，或改成经 `DecideRelease` 测行为。

- [ ] **Step 6: 变异复验——确认新测试真的在测**

把 `CompareVersion` 里的 `for i := range pa` 循环体改成 `return strings.Compare(a, b), true`（字典序），跑 `go test ./internal/selfupdate/ -run TestCompareVersionIsTheOnly`。

Expected: **红**，且失败的是 `v0.10.0 vs v0.9.0` 那两条。改回来。

- [ ] **Step 7: 加注释**

- `internal/selfupdate/clicheck.go` 文件头注释追加一句边界：本文件持有全仓唯一的版本比较入口 `CompareVersion`
- `desktop/internal/shell/release.go` 文件头注释里删掉「另写一份 compareVersion」的那段解释（它描述的代码已经不存在了），改为一句：版本比较统一走 `selfupdate.CompareVersion`

**本 task 无新增日志点**：`CompareVersion` 是纯函数，且挂在每条 CLI 命令的提示路径上，在这里打日志会污染所有命令的输出。判据的日志由调用方打（Task 3、8）。

- [ ] **Step 8: Commit**

```bash
git add internal/selfupdate/ desktop/internal/shell/release.go desktop/internal/shell/release_test.go
git commit -m "refactor(selfupdate): 版本比较收敛成唯一导出入口 CompareVersion

第三个消费者（桌面通知）出现后，release.go 注释里「不值得为此改动
selfupdate 导出面」的判断不再成立。三份实现里一定有一份会被改歪，而这个
函数被写错过一次（B59 验收抓出的反向提示）。"
```

---

## Task 3: `DecideRelease` 改名 + `PlanSync` 四态判据

spec §4.1。判据复用，不新造。

**Files:**
- Modify: `desktop/internal/shell/release.go`（`DecisionNotifyOutdated` → `DecisionEmbeddedNewer`）
- Modify: `desktop/main.go:296`（跟着改）
- Create: `desktop/internal/shell/sync.go`（本 task 只放 `PlanSync` 与类型；执行部分是 Task 5）
- Create: `desktop/internal/shell/sync_test.go`

**Interfaces:**
- Consumes: `shell.DecideRelease(existing, existVer, embedVer string) ReleaseDecision`（既有）；`selfupdate.CompareVersion`（Task 2）
- Produces:
  - `type SyncPlan int`，取值 `SyncSkip` / `SyncDo` / `SyncBlocked` / `SyncNoEmbed`
  - `func (p SyncPlan) String() string`
  - `func PlanSync(d ReleaseDecision, busy int, embedAvailable bool) SyncPlan`
  - Task 8 依赖这两个。

- [ ] **Step 1: 写失败测试**

Create `desktop/internal/shell/sync_test.go`：

```go
package shell_test

import (
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// TestPlanSyncExhaustive 穷举四态。
//
// 表里每一行都对应一种真实处境，不是为了凑覆盖率——尤其
// DecisionInstall + 已配置这一格：它意味着「已配置过、但二进制不见了」，
// 此时不该走同步（同步是换版），该让既有的释出路径去处理。
func TestPlanSyncExhaustive(t *testing.T) {
	cases := []struct {
		name  string
		d     shell.ReleaseDecision
		busy  int
		avail bool
		want  shell.SyncPlan
	}{
		{"内嵌更新且空闲 → 换", shell.DecisionEmbeddedNewer, 0, true, shell.SyncDo},
		{"内嵌更新但有任务 → 拦", shell.DecisionEmbeddedNewer, 1, true, shell.SyncBlocked},
		{"内嵌更新、多个任务 → 拦", shell.DecisionEmbeddedNewer, 7, true, shell.SyncBlocked},
		{"内嵌更新但没内嵌 → 开发构建", shell.DecisionEmbeddedNewer, 0, false, shell.SyncNoEmbed},
		{"已有的不旧 → 不动", shell.DecisionUseExisting, 0, true, shell.SyncSkip},
		{"已有的不旧、有任务 → 不动", shell.DecisionUseExisting, 3, true, shell.SyncSkip},
		{"没有既有安装 → 不归同步管", shell.DecisionInstall, 0, true, shell.SyncSkip},
	}
	for _, c := range cases {
		if got := shell.PlanSync(c.d, c.busy, c.avail); got != c.want {
			t.Errorf("%s: PlanSync(%v,%d,%v) = %v，想要 %v", c.name, c.d, c.busy, c.avail, got, c.want)
		}
	}
}

// TestPlanSyncNegativeBusyIsTreatedAsBlocked 钉住「探不出活跃任务数」的保守方向。
//
// busy 为负表示调用方探测失败（见 Task 8 的约定）。此时必须按「有任务」处置：
// 猜错的代价不对称——误判空闲会在用户有活跃任务时重启 agentd，误判繁忙只是
// 这次不升级。
func TestPlanSyncNegativeBusyIsTreatedAsBlocked(t *testing.T) {
	if got := shell.PlanSync(shell.DecisionEmbeddedNewer, -1, true); got != shell.SyncBlocked {
		t.Errorf("busy=-1 时 PlanSync = %v，想要 SyncBlocked", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestPlanSync -v`
Expected: 编译失败，`undefined: shell.PlanSync` / `shell.DecisionEmbeddedNewer`

- [ ] **Step 3: 改名 `DecisionNotifyOutdated`**

`desktop/internal/shell/release.go`：

```go
	// DecisionEmbeddedNewer 表示已有安装比内嵌的旧。
	//
	// **它只陈述事实，不规定处置。** 处置由调用方按当前处境决定：
	// 首次引导时（StateUnconfigured）只提示不换，避免打断；已配置时走同步
	// （见 PlanSync）。原名 DecisionNotifyOutdated 把「提示」这一种处置烧进了
	// 枚举名，而同一个事实现在有两种处置。
	DecisionEmbeddedNewer
```

`String()` 里 `case DecisionNotifyOutdated: return "notify-outdated"` 改为 `case DecisionEmbeddedNewer: return "embedded-newer"`。`DecideRelease` 函数体与 doc comment 里的引用一并改。

`desktop/main.go:296` 的 `case shell.DecisionNotifyOutdated:` 改为 `case shell.DecisionEmbeddedNewer:`。

- [ ] **Step 4: 写 `PlanSync`**

Create `desktop/internal/shell/sync.go`：

```go
// 本文件负责「已配置的机器上要不要把 agentd/CLI 换成内嵌的那份」的判据与执行
// （spec §4.1 / §5）。
//
// 职责：
//   - PlanSync 是纯函数：吃 DecideRelease 的结论 + 活跃任务数 + 内嵌可用性，
//     吐四态之一。不碰文件系统、不发网络请求，因此四态可以穷举测试。
//   - DoSync 才动这台机器：换二进制、同步 skill、触发 agentd 重启。
//
// 边界（承重）：
//   - 本文件**不做版本比较**。判据全部来自 DecideRelease，它背后是全仓唯一的
//     selfupdate.CompareVersion。这里再写一份比较就是第四份。
//   - 本文件**不决定何时被调用**。调用时机与相对顺序是 main.go 的 openConsole
//     的责任（spec §5 的三条承重顺序），放在这里会让顺序无法被单独测试。
//   - 同步失败**绝不阻断打开控制台**（spec D8）：所有函数只返回错误，绝不
//     os.Exit、绝不 panic、绝不阻塞等待用户输入。
package shell

// SyncPlan 是同步决策的四态。
type SyncPlan int

const (
	// SyncSkip 表示不需要同步（已有的不旧，或版本判不出，或压根没有既有安装）。
	SyncSkip SyncPlan = iota
	// SyncDo 表示该换，且此刻换是安全的。
	SyncDo
	// SyncBlocked 表示该换，但有活跃任务，闸一拦下。
	SyncBlocked
	// SyncNoEmbed 表示该换但本次构建没内嵌二进制（开发构建未带 -tags embedbin）。
	SyncNoEmbed
)

// String 返回四态的可读名，供日志用。
func (p SyncPlan) String() string {
	switch p {
	case SyncSkip:
		return "skip"
	case SyncDo:
		return "do"
	case SyncBlocked:
		return "blocked"
	case SyncNoEmbed:
		return "no-embed"
	default:
		return "SyncPlan(" + itoa(int(p)) + ")"
	}
}

// PlanSync 决定要不要把已装的 handoff 换成内嵌的那份，是纯函数。
//
// 参数：
//   - d: DecideRelease 的结论。只有 DecisionEmbeddedNewer 才可能走到换版
//   - busy: 活跃任务数（running/waiting_answer）。**负数表示调用方探测失败**
//   - embedAvailable: 本次构建有没有内嵌二进制（embedbin.Available()）
//
// 返回四态之一，语义见各常量注释。
//
// 注意：
//   - busy 为负一律按 SyncBlocked 处置。猜错的代价不对称：误判空闲会在用户
//     有活跃任务时重启 agentd，误判繁忙只是这次不升级
//   - 本函数不写日志（纯函数约定）。四态决策的日志由调用方拿到返回值后打
func PlanSync(d ReleaseDecision, busy int, embedAvailable bool) SyncPlan {
	if d != DecisionEmbeddedNewer {
		return SyncSkip
	}
	if !embedAvailable {
		return SyncNoEmbed
	}
	// busy != 0 涵盖了负数（探测失败），见 doc comment 的不对称代价说明
	if busy != 0 {
		return SyncBlocked
	}
	return SyncDo
}
```

顶部 import `"strconv"`，并把 `itoa` 换成 `strconv.Itoa`（上面为了让 String 独立可读写了占位，实际用标准库）。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestPlanSync -v`
Expected: 两条都 PASS

- [ ] **Step 6: 变异复验**

把 `if busy != 0` 改成 `if busy > 0`，跑测试。
Expected: **红**，失败的是 `TestPlanSyncNegativeBusyIsTreatedAsBlocked`。改回来。

再把 `if !embedAvailable` 那两行删掉，跑测试。
Expected: **红**，失败的是「内嵌更新但没内嵌」那行。改回来。

- [ ] **Step 7: 加注释**

已在 Step 3/4 的代码里写全（文件头职责+边界、四个常量各自的语义、`PlanSync` 的 doc comment 含不对称代价的「为什么」）。检查一遍：`DecisionEmbeddedNewer` 的改名注释必须说清「为什么原名不对」，否则后人会改回去。

- [ ] **Step 8: 跑全量并 Commit**

Run: `go test ./... && cd desktop && go test ./... && gofmt -l . ../internal ../cmd`
Expected: 全绿，`gofmt -l` 无输出

```bash
git add desktop/internal/shell/ desktop/main.go
git commit -m "feat(desktop): PlanSync 四态判据，DecisionNotifyOutdated 改名为 DecisionEmbeddedNewer

原名把「提示」这一种处置烧进了枚举名，而同一个事实现在有两种处置：
首次引导时提示，已配置时同步。名字该陈述事实。"
```

---

## Task 4: `.app` 的版本号改为随 TAG 注入

spec §6.4。现状 `Info.plist` 里两个版本键都硬编码 `0.1.0`，每一份发出去的 `.app` 都自称 0.1.0。不修的话，Task 1 与后续真机走查里「确认装的是新版」这个判据没有可信读数。

**Files:**
- Modify: `.github/workflows/release.yml`（`build-desktop-darwin` job，在 `wails3 task darwin:build` 之前插一步）
- Modify: `release_workflow_test.go`（加契约测试）

**Interfaces:**
- Consumes: 无
- Produces: 发布出的 `.app` 里 `CFBundleShortVersionString` == TAG 去掉 `v` 前缀，`CFBundleVersion` == 同值。Task 12 的走查清单依赖它。

- [ ] **Step 1: 写失败测试**

追加到 `release_workflow_test.go`：

```go
// TestDarwinAppCarriesRealVersion 钉住 .app 的版本号随 TAG 注入。
//
// 为什么需要这条门：Info.plist 里的版本是**签进 bundle 的静态文件**，不像
// ldflags 那样构建时必然被注入。漏掉这一步不会有任何报错——wails3 照常打包、
// 签名公证照常通过、DMG 照常产出——只有用户在访达「显示简介」里看到一个
// 假版本号。而这正是他排查「我到底装的是哪个版本」时唯一会看的地方。
//
// 断言用精确串计数而不是 Contains：Contains 会被本文件里任何一处提到
// plutil 的注释或 echo 文案蒙混过关（同一个坑本文件里已经吃过两次，见
// TestDarwinDesktopShipsStapledDMG 的注释）。
func TestDarwinAppCarriesRealVersion(t *testing.T) {
	wf := readWorkflow(t)
	plistPath := "desktop/build/darwin/Info.plist"
	want := map[string]int{
		`plutil -replace CFBundleShortVersionString -string "${TAG#v}" ` + plistPath: 1,
		`plutil -replace CFBundleVersion -string "${TAG#v}" ` + plistPath:            1,
	}
	for s, n := range want {
		if got := strings.Count(wf, s); got != n {
			t.Errorf("release.yml 里 %q 出现 %d 次，想要 %d 次", s, got, n)
		}
	}
	// 注入必须排在构建之前——排在之后等于没注入，而且同样不报错
	iInject := strings.Index(wf, `plutil -replace CFBundleShortVersionString`)
	iBuild := strings.Index(wf, `wails3 task darwin:build`)
	if iInject < 0 || iBuild < 0 {
		t.Fatalf("找不到注入步骤或构建步骤：inject=%d build=%d", iInject, iBuild)
	}
	if iInject > iBuild {
		t.Error("版本注入排在了 darwin:build 之后，构建读到的还是旧值")
	}
}
```

若 `readWorkflow` 这个辅助函数在本文件里不叫这个名字，用文件里既有的那个读取方式，不要新造。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test . -run TestDarwinAppCarriesRealVersion -v`
Expected: FAIL，两条计数都是 0

- [ ] **Step 3: 在 release.yml 里插注入步骤**

在 `build-desktop-darwin` job 里、`wails3 task darwin:build`（或 `darwin:package`，取当前实际的构建步骤）**之前**插入：

```yaml
      - name: 把 TAG 写进 Info.plist
        # Info.plist 是签进 bundle 的静态文件，仓库里存的是占位值 0.1.0。
        # 不在这里覆盖，发出去的每一份 .app 都自称 0.1.0——而这是用户排查
        # 「我装的是哪个版本」时唯一会看的地方。漏掉不会有任何报错。
        # ${TAG#v}: CFBundleShortVersionString 按苹果约定不带 v 前缀。
        run: |
          set -euo pipefail
          plutil -replace CFBundleShortVersionString -string "${TAG#v}" desktop/build/darwin/Info.plist
          plutil -replace CFBundleVersion -string "${TAG#v}" desktop/build/darwin/Info.plist
          plutil -p desktop/build/darwin/Info.plist | grep -E "CFBundle(Short)?Version"
```

`TAG` 若在该 job 里不是环境变量而是 `${{ github.ref_name }}` 之类，按该 job 既有的取法写，但**测试里的断言串要与实际写法逐字一致**。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test . -run TestDarwinAppCarriesRealVersion -v`
Expected: PASS

- [ ] **Step 5: 变异复验**

把注入步骤整个移到 `wails3 task darwin:build` **之后**，跑测试。
Expected: **红**，报「版本注入排在了 darwin:build 之后」。移回去。

删掉 `CFBundleVersion` 那一行，跑测试。
Expected: **红**，计数 0≠1。加回去。

- [ ] **Step 6: 加注释**

已在 Step 3 的 YAML 注释里写全（为什么必须覆盖、为什么去 v 前缀、漏掉的症状是静默的）。测试的 doc comment 里必须留下「为什么用计数而不是 Contains」——这是本文件已经吃过两次的坑。

**本 task 无日志点**：改的是 CI 配置，运行期由 `plutil -p` 那行把结果打进 job 日志（Step 3 的最后一行就是为此）。

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/release.yml release_workflow_test.go
git commit -m "fix(release): .app 的版本号改为随 TAG 注入

Info.plist 里两个版本键此前硬编码 0.1.0，每一份发出去的 .app 都自称
0.1.0。漏掉这一步不报错——只有用户在访达里看到假版本号，而那是他排查
「装的是哪个版本」时唯一会看的地方。"
```

---

## Task 5: `DoSync`——换二进制、同步 skill、触发重启

spec §5.1 / §5.3。这是同步的执行部分，**不含**「等 agentd 回来」（Task 6）与「什么时候调用」（Task 7）。

**Files:**
- Modify: `desktop/internal/shell/sync.go`（追加 `SyncDeps` 与 `DoSync`）
- Modify: `desktop/internal/shell/sync_test.go`（追加序列与失败注入测试）

**Interfaces:**
- Consumes: `release.Activate(newPath, target string) (string, error)`（`internal/release/install.go:551`）；`client.New(addr, token).RestartAgentd(ctx, force)`（`internal/client/update.go:64`）；`embedbin.Open() (io.ReadCloser, error)`
- Produces:
  - `type SyncDeps struct { OpenEmbedded func() (io.ReadCloser, error); Activate func(newPath, target string) (string, error); SkillInstall func(ctx context.Context, bin string) ([]byte, error); RestartAgentd func(ctx context.Context, force bool) error }`
  - `func DoSync(ctx context.Context, target string, force bool, d SyncDeps, progress func(stage string)) error`
  - Task 7 依赖这两个。

- [ ] **Step 1: 写失败测试——调用顺序**

追加到 `desktop/internal/shell/sync_test.go`：

```go
// TestDoSyncCallOrderIsLoadBearing 钉住四步的相对顺序。
//
// 顺序是承重的（spec §5）：
//   - Activate 必须在 SkillInstall 之前——skill 要从**新**二进制里装，
//     当前进程内嵌的是旧的（cmd/upgrade.go:591 已记此事）
//   - SkillInstall 必须在 RestartAgentd 之前——重启会让本进程与 agentd
//     的连接断掉，之后再 exec 新二进制装 skill 就成了「重启后才补」，
//     而重启期间协调者拿到的是旧 skill
func TestDoSyncCallOrderIsLoadBearing(t *testing.T) {
	var seq []string
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			seq = append(seq, "open")
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate: func(newPath, target string) (string, error) {
			seq = append(seq, "activate")
			return target + ".prev", nil
		},
		SkillInstall: func(context.Context, string) ([]byte, error) {
			seq = append(seq, "skill")
			return nil, nil
		},
		RestartAgentd: func(context.Context, bool) error {
			seq = append(seq, "restart")
			return nil
		},
	}
	target := filepath.Join(t.TempDir(), "handoff")
	if err := shell.DoSync(context.Background(), target, false, d, func(string) {}); err != nil {
		t.Fatalf("DoSync 返回错误：%v", err)
	}
	want := []string{"open", "activate", "skill", "restart"}
	if !slices.Equal(seq, want) {
		t.Errorf("调用序列 = %v，想要 %v", seq, want)
	}
}

// TestDoSyncSkillInstallFailureIsNotFatal 钉住 skill 同步失败不算同步失败。
//
// 二进制已经换好了——此时报错回去会让调用方以为换版没成功。但也**绝不能
// 静默**：留一份旧 skill 会按已经变了的状态机主动误导协调者（沿用
// cmd/upgrade.go:591 syncSkill 的既有语义）。
func TestDoSyncSkillInstallFailureIsNotFatal(t *testing.T) {
	restarted := false
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate:     func(_, target string) (string, error) { return target + ".prev", nil },
		SkillInstall: func(context.Context, string) ([]byte, error) { return []byte("boom"), errors.New("装不上") },
		RestartAgentd: func(context.Context, bool) error {
			restarted = true
			return nil
		},
	}
	target := filepath.Join(t.TempDir(), "handoff")
	if err := shell.DoSync(context.Background(), target, false, d, func(string) {}); err != nil {
		t.Fatalf("skill 装不上不该让 DoSync 失败，却返回：%v", err)
	}
	if !restarted {
		t.Error("skill 装不上时跳过了重启——二进制已经换了，不重启等于换了个寂寞")
	}
}

// TestDoSyncStopsAtActivateFailure 钉住换版失败时不往下走。
//
// Activate 失败意味着磁盘上还是旧二进制。此时若继续 SkillInstall 会把**新**
// skill 装到**旧**二进制的落点上，造出一个版本不匹配的组合；继续 RestartAgentd
// 更是白重启一次而调用方以为升级成功了。
func TestDoSyncStopsAtActivateFailure(t *testing.T) {
	var seq []string
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate:      func(string, string) (string, error) { return "", errors.New("权限不足") },
		SkillInstall:  func(context.Context, string) ([]byte, error) { seq = append(seq, "skill"); return nil, nil },
		RestartAgentd: func(context.Context, bool) error { seq = append(seq, "restart"); return nil },
	}
	target := filepath.Join(t.TempDir(), "handoff")
	err := shell.DoSync(context.Background(), target, false, d, func(string) {})
	if err == nil {
		t.Fatal("Activate 失败时 DoSync 必须返回错误")
	}
	if len(seq) != 0 {
		t.Errorf("Activate 失败后仍继续执行了 %v", seq)
	}
}

// TestDoSyncLeavesNoTempFileOnFailure 钉住失败时不留半截文件。
//
// 半截的临时文件若以 target 那个名字出现，launchd/schtasks 会把它当可执行
// 拉起来——症状是「装好了但 agentd 起不来」，而根因在一次失败的升级里。
func TestDoSyncLeavesNoTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	d := shell.SyncDeps{
		OpenEmbedded:  func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("BINARY")), nil },
		Activate:      func(string, string) (string, error) { return "", errors.New("权限不足") },
		SkillInstall:  func(context.Context, string) ([]byte, error) { return nil, nil },
		RestartAgentd: func(context.Context, bool) error { return nil },
	}
	_ = shell.DoSync(context.Background(), target, false, d, func(string) {})
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("失败后残留了文件 %s", e.Name())
	}
}
```

import 需要：`context`、`errors`、`io`、`os`、`path/filepath`、`slices`、`strings`、`testing`。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestDoSync -v`
Expected: 编译失败，`undefined: shell.DoSync` / `shell.SyncDeps`

- [ ] **Step 3: 实现 `DoSync`**

追加到 `desktop/internal/shell/sync.go`：

```go
// SyncDeps 是同步动作的外部依赖集合。
//
// 抽成结构体而不是直接调包级函数：这四样全都是「会真的动这台机器」的动作
// （写文件、rename 二进制、exec 子进程、重启服务），测试必须能整体替换掉。
// 漏替一个就会在 CI 上真的把测试二进制 rename 掉——这条纪律抄自
// internal/agentd 的 UpdateDeps，理由完全相同。
type SyncDeps struct {
	// OpenEmbedded 读出内嵌的 handoff 二进制。生产实现是 embedbin.Open
	OpenEmbedded func() (io.ReadCloser, error)
	// Activate 原子换版并返回旧二进制的留存路径。生产实现是 release.Activate
	Activate func(newPath, target string) (string, error)
	// SkillInstall 在指定二进制上跑 skill install，返回其输出。
	// **必须传新二进制的路径**——当前进程内嵌的是旧 skill
	SkillInstall func(ctx context.Context, bin string) ([]byte, error)
	// RestartAgentd 触发 agentd 重启。生产实现是 client.RestartAgentd
	RestartAgentd func(ctx context.Context, force bool) error
}

// DoSync 把已装的 handoff 换成内嵌的那份，并触发 agentd 重启。
//
// 参数：
//   - target: 要被替换的二进制路径（ResolveBinPath 的结果，即 agentd 实际
//     在跑的那一份）
//   - force: 越过闸一（活跃任务）。**不越过闸二**（非托管），那是 agentd 侧
//     的硬拒绝，这里传什么都没用
//   - progress: 阶段回调，供 UI 显示。传 nil 安全
//
// 返回：
//   - 换版失败或重启触发失败时返回错误。**skill 同步失败不算失败**（理由见
//     函数内注释），但会记 Error 日志
//
// 注意：
//   - **本函数不等 agentd 回来**。那是 WaitAgentdBack 的职责，分开是因为
//     两者的失败语义不同：这里失败意味着没换成，那里失败意味着换了但没起来
//   - 四步的相对顺序是承重的，见 TestDoSyncCallOrderIsLoadBearing
func DoSync(ctx context.Context, target string, force bool, d SyncDeps, progress func(stage string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	logger.Info("开始同步 handoff 二进制", "target", target, "force", force)

	progress("正在取出内嵌的 handoff")
	rc, err := d.OpenEmbedded()
	if err != nil {
		logger.Error("打开内嵌二进制失败", "target", target, "cause", err)
		return fmt.Errorf("打开内嵌二进制: %w", err)
	}
	defer rc.Close()

	// 先落到 target 同目录的临时文件：Activate 是 rename，跨设备 rename 会
	// 失败，所以临时文件必须与 target 同一个文件系统。
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".handoff-sync-*")
	if err != nil {
		logger.Error("创建临时文件失败", "dir", dir, "cause", err)
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	// 成功 rename 后置空，防止 defer 把刚落位的文件删掉。失败路径上这个
	// defer 是唯一的清理者——半截文件若以 target 那个名字留下，launchd 会
	// 把它当可执行拉起来，症状是「装好了但 agentd 起不来」。
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		logger.Error("写入内嵌二进制失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("写入内嵌二进制: %w", err)
	}
	if err := tmp.Close(); err != nil {
		logger.Error("关闭临时文件失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	// 在 rename 之前就给执行位：target 一出现就是可执行的，不存在
	// 「已可见但还没权限」的窗口
	if err := os.Chmod(tmpName, 0o755); err != nil {
		logger.Error("设置执行权限失败", "tmp", tmpName, "cause", err)
		return fmt.Errorf("设置执行权限: %w", err)
	}

	progress("正在换版")
	prev, err := d.Activate(tmpName, target)
	if err != nil {
		logger.Error("换版失败，磁盘上仍是旧二进制", "target", target, "cause", err)
		return fmt.Errorf("换版: %w", err)
	}
	tmpName = ""
	logger.Info("换版完成", "target", target, "prev", prev)

	// skill 随二进制分发（B59）。必须 exec **新**二进制来装——当前进程内嵌
	// 的是旧 skill。失败不算同步失败：二进制已经换好了，报错回去会让调用方
	// 以为换版没成功。但绝不能静默：留一份旧 skill 会按已经变了的状态机
	// 主动误导协调者。
	progress("正在同步 skill")
	if out, err := d.SkillInstall(ctx, target); err != nil {
		logger.Error("skill 同步失败，二进制已换但 skill 是旧的",
			"target", target, "cause", err, "output", firstLine(out))
	} else {
		logger.Info("skill 同步完成", "target", target)
	}

	progress("正在重启 agentd")
	if err := d.RestartAgentd(ctx, force); err != nil {
		logger.Error("触发 agentd 重启失败，磁盘已是新版但跑着的仍是旧进程",
			"target", target, "force", force, "cause", err)
		return fmt.Errorf("触发 agentd 重启: %w", err)
	}
	logger.Info("同步完成，已触发 agentd 重启", "target", target, "force", force)
	return nil
}

// firstLine 取输出的第一行，供日志用。多行输出灌进日志会把一条 Error 撑成
// 一屏，反而看不见旁边的行。
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
```

import 补 `"context"`、`"fmt"`、`"io"`、`"os"`、`"path/filepath"`、`"strings"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestDoSync -v`
Expected: 四条全 PASS

- [ ] **Step 5: 变异复验——四条各杀一次**

逐条做，每次改完跑 `cd desktop && go test ./internal/shell/ -run TestDoSync`，确认**红**之后改回来：

1. 把 `d.SkillInstall` 调用整个挪到 `d.RestartAgentd` **之后** → 期望 `TestDoSyncCallOrderIsLoadBearing` 红
2. 把 skill 失败那支改成 `return fmt.Errorf(...)` → 期望 `TestDoSyncSkillInstallFailureIsNotFatal` 红
3. 把 Activate 失败那支的 `return` 删掉（改为只记日志继续） → 期望 `TestDoSyncStopsAtActivateFailure` 红
4. 把 `defer` 里的 `os.Remove(tmpName)` 删掉 → 期望 `TestDoSyncLeavesNoTempFileOnFailure` 红

**四条里有任何一条杀不掉，说明那条测试是假门，必须重写它而不是跳过。**

- [ ] **Step 6: 加日志与注释**

日志点已在 Step 3 的实现里铺齐，对照自查：

- 进入同步（Info，带 target 与 force）✅
- 每一个错误分支都有 Error + 上下文 + cause ✅
- 换版成功（Info，带 prev）✅
- skill 同步成功与失败两支都有日志 ✅（成功路径不静默）
- 退出时的结论（Info「同步完成，已触发 agentd 重启」）✅

注释自查：文件头已在 Task 3 写；`SyncDeps` 与 `DoSync` 都有 doc comment；`tmpName = ""` 的置空、chmod 在 rename 之前、skill 失败不算失败——三处非显然逻辑都有「为什么」。

- [ ] **Step 7: 跑全量并 Commit**

Run: `cd desktop && go test ./... && gofmt -l . && cd .. && go test ./... && gofmt -l internal cmd`
Expected: 全绿，两处 `gofmt -l` 均无输出

```bash
git add desktop/internal/shell/
git commit -m "feat(desktop): DoSync——换二进制、同步 skill、触发 agentd 重启

四步顺序承重：skill 必须从新二进制装（当前进程内嵌的是旧的），且必须在
重启之前装完。skill 装不上不算同步失败但绝不静默——留一份旧 skill 会按
已经变了的状态机主动误导协调者。"
```

---

## Task 6: `WaitAgentdBack`——等 agentd 带着新版本回来

spec §5 承重③ + §5.2。判据是**版本号相等**，不是「Status 调得通」。

**Files:**
- Create: `desktop/internal/shell/waitback.go`
- Create: `desktop/internal/shell/waitback_test.go`

**Interfaces:**
- Consumes: `client.New(addr, token).Status(ctx) (*proto.StatusResp, error)`（`internal/client/client.go:379`）
- Produces: `func WaitAgentdBack(ctx context.Context, wantVer string, d WaitDeps) error` 与 `type WaitDeps struct { Version func(context.Context) (string, error); Nudge func() error; Sleep func(time.Duration) }`。Task 7 依赖。

- [ ] **Step 1: 写失败测试**

Create `desktop/internal/shell/waitback_test.go`：

```go
package shell_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// TestWaitAgentdBackRequiresVersionMatch 是本文件最承重的一条。
//
// 判据必须是「版本号等于期望值」，不能是「Status 调得通」：agentd 优雅
// 关停期间**仍会应答在途请求**，只判连通会立刻通过，然后握手到一个正在
// 退出的进程上。这是「就绪判据取早了」那一类假绿，本仓库已经吃过。
func TestWaitAgentdBackRequiresVersionMatch(t *testing.T) {
	calls := 0
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) {
			calls++
			// 前两次还是旧版本在应答（正在关停），第三次才换上新的
			if calls < 3 {
				return "v0.3.0", nil
			}
			return "v0.4.0", nil
		},
		Nudge: func() error { return nil },
		Sleep: func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatalf("等待失败：%v", err)
	}
	if calls != 3 {
		t.Errorf("查询了 %d 次，想要 3 次——旧版本应答时不能算就绪", calls)
	}
}

// TestWaitAgentdBackNudgesOnceAfterFirstMiss 钉住 Windows 那一下主动催。
//
// Windows 的 KeepAlive 是每分钟一次的模拟（internal/service/windows.go:150
// 的 PT1M），不催就要干等最多 60 秒。催必须在**首次探测失败之后**——
// MultipleInstancesPolicy=IgnoreNew 会把旧进程还没退时的那次催拒掉，而拒绝
// 时写进「上次结果」的正是 0x800710E0，也就是 rc5 那个 bug 的同一个值。
func TestWaitAgentdBackNudgesOnceAfterFirstMiss(t *testing.T) {
	calls, nudges := 0, 0
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("connection refused")
			}
			return "v0.4.0", nil
		},
		Nudge: func() error { nudges++; return nil },
		Sleep: func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatalf("等待失败：%v", err)
	}
	if nudges != 1 {
		t.Errorf("催了 %d 次，想要恰好 1 次", nudges)
	}
}

// TestWaitAgentdBackDoesNotNudgeBeforeFirstProbe 钉住「不在旧进程还活着时催」。
func TestWaitAgentdBackDoesNotNudgeBeforeFirstProbe(t *testing.T) {
	nudgedBeforeProbe := false
	probed := false
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) { probed = true; return "v0.4.0", nil },
		Nudge:   func() error { nudgedBeforeProbe = !probed; return nil },
		Sleep:   func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatal(err)
	}
	if nudgedBeforeProbe {
		t.Error("在第一次探测之前就催了——此时旧进程可能还没退，催会被 IgnoreNew 拒掉")
	}
}

// TestWaitAgentdBackTimesOut 钉住超时会返回错误而不是永远挂着。
//
// 永远挂着的后果是用户双击了应用、窗口一直不出来，且没有任何解释。
func TestWaitAgentdBackTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) { return "v0.3.0", nil },
		Nudge:   func() error { return nil },
		Sleep:   func(time.Duration) { cancel() },
	}
	err := shell.WaitAgentdBack(ctx, "v0.4.0", d)
	if err == nil {
		t.Fatal("超时必须返回错误")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestWaitAgentdBack -v`
Expected: 编译失败，`undefined: shell.WaitAgentdBack`

- [ ] **Step 3: 实现**

Create `desktop/internal/shell/waitback.go`：

```go
// 本文件负责「触发 agentd 重启之后，等它带着新版本回来」（spec §5 承重③、§5.2）。
//
// 职责：
//   - WaitAgentdBack 轮询 agentd 的版本号，直到它等于期望值或超时
//   - 在首次探测失败后主动催一次进程管理器（Windows 的 60 秒重复触发窗口）
//
// 边界：
//   - 不负责换版，也不负责触发重启——那是 DoSync 的职责。分开是因为两者的
//     失败语义不同：换版失败意味着没换成，这里失败意味着换了但没起来
//   - 不做任何补救动作（不回滚、不重装服务）。判不出来就如实报错，由调用方
//     决定怎么告诉用户
package shell

import (
	"context"
	"fmt"
	"time"
)

// waitBackTimeout 是等 agentd 回来的上限。
//
// 90 秒 = Windows 计划任务 60 秒的重复触发窗口 + 余量。macOS 的 launchd
// KeepAlive 是秒级，用不到这么久；上限按最慢的那个平台取，否则 Windows 上
// 会在管理器还没来得及拉起时就判失败。
const waitBackTimeout = 90 * time.Second

// waitBackInterval 是两次探测之间的间隔。
const waitBackInterval = 500 * time.Millisecond

// WaitDeps 是等待动作的外部依赖集合，抽成结构体只为可测——真实实现会发
// HTTP 请求并调用平台的服务管理器，两者都不能在单元测试里真跑。
type WaitDeps struct {
	// Version 返回当前 agentd 自报的版本号。生产实现是
	// client.New(addr, token).Status(ctx) 取 BuildInfo 的版本字段
	Version func(ctx context.Context) (string, error)
	// Nudge 主动催进程管理器把 agentd 拉起来。生产实现在 Windows 上是
	// schtasks /Run（见 internal/service/windows.go:271），其余平台可为 nil
	Nudge func() error
	// Sleep 是可注入的等待，测试用它避免真睡
	Sleep func(time.Duration)
}

// WaitAgentdBack 等 agentd 重启完成并带着 wantVer 这个版本回来。
//
// 参数：
//   - wantVer: 期望的版本号（即内嵌二进制的版本，embedbin.Version）
//
// 返回：
//   - 超时或 ctx 取消时返回错误；探测本身的错误不终止循环（重启期间连不上
//     是正常的），只在超时后作为最后一次的原因带出去
//
// 注意（承重）：
//   - **判据是版本号相等，不是「调得通」。** agentd 优雅关停期间仍会应答在途
//     请求，只判连通会立刻通过，然后握手到一个正在退出的进程上
//   - **Nudge 只在首次探测失败之后调一次。** 早于此调用会撞上 Windows 的
//     MultipleInstancesPolicy=IgnoreNew——旧进程还没退时催会被拒，而拒绝码
//     0x800710E0 正是 rc5 那个 bug 的同一个值
func WaitAgentdBack(ctx context.Context, wantVer string, d WaitDeps) error {
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	logger.Info("等 agentd 带着新版本回来", "want_version", wantVer, "timeout", waitBackTimeout)

	deadline := time.Now().Add(waitBackTimeout)
	nudged := false
	var lastErr error
	var lastVer string
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			logger.Error("等待被取消", "want_version", wantVer, "attempts", attempt, "cause", err)
			return fmt.Errorf("等 agentd 回来被取消（已试 %d 次，最后看到版本 %q）: %w", attempt, lastVer, err)
		}
		ver, err := d.Version(ctx)
		switch {
		case err != nil:
			lastErr = err
			// 循环内高频日志降级到 Debug，否则 90 秒会刷出近两百行
			logger.Debug("探测 agentd 版本失败，继续等", "attempt", attempt, "cause", err)
		case ver == wantVer:
			logger.Info("agentd 已带新版本回来", "version", ver, "attempts", attempt)
			return nil
		default:
			lastVer = ver
			// 这一支正是本函数存在的理由：连得上、但还是旧进程在应答
			logger.Debug("agentd 应答的仍是旧版本，继续等", "attempt", attempt, "got", ver, "want", wantVer)
		}

		// 首次探测之后才催：早于此旧进程可能还没退，Windows 上会被
		// IgnoreNew 拒掉且把拒绝码写进「上次结果」
		if !nudged && d.Nudge != nil {
			nudged = true
			if nerr := d.Nudge(); nerr != nil {
				logger.Warn("催进程管理器拉起 agentd 失败，改为等它自己拉", "cause", nerr)
			} else {
				logger.Info("已催进程管理器拉起 agentd")
			}
		}

		if time.Now().After(deadline) {
			logger.Error("等 agentd 回来超时", "want_version", wantVer, "last_version", lastVer,
				"attempts", attempt, "cause", lastErr)
			return fmt.Errorf("等 agentd 回到 %s 超时（%s，已试 %d 次，最后看到版本 %q，最后错误 %v）",
				wantVer, waitBackTimeout, attempt, lastVer, lastErr)
		}
		sleep(waitBackInterval)
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestWaitAgentdBack -v`
Expected: 四条全 PASS

- [ ] **Step 5: 变异复验**

1. 把 `case ver == wantVer:` 改成 `case err == nil:`（即只判调得通） → 期望 `TestWaitAgentdBackRequiresVersionMatch` 红
2. 把 `if !nudged && d.Nudge != nil` 那一整块挪到 `ver, err := d.Version(ctx)` **之前** → 期望 `TestWaitAgentdBackDoesNotNudgeBeforeFirstProbe` 红
3. 把 `nudged = true` 删掉 → 期望 `TestWaitAgentdBackNudgesOnceAfterFirstMiss` 红
4. 把超时那段 `return` 改成 `continue` → 期望 `TestWaitAgentdBackTimesOut` 红（会挂住则说明测试写错了，改用 `-timeout 10s` 跑并重写测试）

每条确认红之后改回来。

- [ ] **Step 6: 日志与注释自查**

- 进入等待有 Info（带期望版本与超时）✅
- 循环内的两支都是 Debug（高频降级）✅
- 成功退出有 Info（带版本与试了几次）✅
- 超时与取消两支都是 Error，带 last_version / attempts / cause ✅
- 催成功与失败两支都有日志 ✅
- 文件头有职责与边界；`WaitDeps` 与 `WaitAgentdBack` 有 doc comment；两条承重各有「为什么」✅

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/waitback.go desktop/internal/shell/waitback_test.go
git commit -m "feat(desktop): WaitAgentdBack——判据是版本号相等，不是调得通

agentd 优雅关停期间仍会应答在途请求，只判连通会立刻通过、然后握手到一个
正在退出的进程上。Windows 的 KeepAlive 是每分钟一次的模拟，首次探测失败后
主动催一次 schtasks /Run；催必须在探测之后，早于此会被 IgnoreNew 拒掉。"
```

---

## Task 7: `SyncOnOpen`——把整条承重顺序收进一个可测函数，并接进 `openConsole`

spec §5 的三条承重顺序 + D8。**顺序不能留在 `main.go` 里**：那样它无法被单独测试，而这是本设计最容易被后人改坏的地方。

**Files:**
- Create: `desktop/internal/shell/open_sync.go`
- Create: `desktop/internal/shell/open_sync_test.go`
- Modify: `desktop/main.go`（`openConsole` 的已配置分支）

**Interfaces:**
- Consumes: `EnsureRunning`（既有）、`ResolveBinPath`（既有）、`DecideRelease`（既有）、`PlanSync`（Task 3）、`DoSync`/`SyncDeps`（Task 5）、`WaitAgentdBack`/`WaitDeps`（Task 6）
- Produces:
  - `type OpenSyncDeps struct { EnsureRunning func() error; InstalledPath func() (string, error); InstalledVersion func(path string) string; Busy func(ctx context.Context) (int, error); EmbedVersion string; EmbedAvailable bool; Sync SyncDeps; Wait WaitDeps; Progress func(stage string) }`
  - `type SyncOutcome struct { Plan SyncPlan; Busy int; Err error }`
  - `func SyncOnOpen(ctx context.Context, d OpenSyncDeps) SyncOutcome`
  - Task 9（面板显示进度）与 Task 10（托盘据 `Plan==SyncBlocked` 挂条目）依赖 `SyncOutcome`。

- [ ] **Step 1: 写失败测试——顺序与「绝不阻断」**

Create `desktop/internal/shell/open_sync_test.go`：

```go
package shell_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// okDeps 造一份「该同步且一切顺利」的依赖，并把调用顺序记进 seq。
func okDeps(seq *[]string) shell.OpenSyncDeps {
	return shell.OpenSyncDeps{
		EnsureRunning:    func() error { *seq = append(*seq, "ensure"); return nil },
		InstalledPath:    func() (string, error) { return "/tmp/handoff", nil },
		InstalledVersion: func(string) string { return "v0.3.0" },
		Busy: func(context.Context) (int, error) {
			*seq = append(*seq, "busy")
			return 0, nil
		},
		EmbedVersion:   "v0.4.0",
		EmbedAvailable: true,
		Sync: shell.SyncDeps{
			OpenEmbedded: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("B")), nil },
			Activate: func(_, target string) (string, error) {
				*seq = append(*seq, "activate")
				return target + ".prev", nil
			},
			SkillInstall: func(context.Context, string) ([]byte, error) {
				*seq = append(*seq, "skill")
				return nil, nil
			},
			RestartAgentd: func(context.Context, bool) error {
				*seq = append(*seq, "restart")
				return nil
			},
		},
		Wait: shell.WaitDeps{
			Version: func(context.Context) (string, error) {
				*seq = append(*seq, "waitver")
				return "v0.4.0", nil
			},
			Sleep: func(time.Duration) {},
		},
	}
}

// TestSyncOnOpenOrderIsLoadBearing 钉住 spec §5 的三条承重顺序。
//
// ① EnsureRunning 必须在探 Busy 之前——闸一判据要从 agentd 的 /api/status
//    探，agentd 不在跑就探不出
// ② 探 Busy（闸一）必须在 activate 之前——与 cmd/upgrade.go:500 同序。反过来
//    会留下「磁盘是新的、跑着的是旧的」这种持续不一致
// ③ waitver 必须在 restart 之后——它等的就是重启的结果
func TestSyncOnOpenOrderIsLoadBearing(t *testing.T) {
	var seq []string
	out := shell.SyncOnOpen(context.Background(), okDeps(&seq))
	if out.Err != nil {
		t.Fatalf("一切顺利时不该有错误：%v", out.Err)
	}
	if out.Plan != shell.SyncDo {
		t.Fatalf("Plan = %v，想要 SyncDo", out.Plan)
	}
	want := []string{"ensure", "busy", "activate", "skill", "restart", "waitver"}
	if !slices.Equal(seq, want) {
		t.Errorf("调用序列 = %v，想要 %v", seq, want)
	}
}

// TestSyncOnOpenNeverBlocksOnFailure 是 D8 的唯一守卫。
//
// 同步路径上的任何失败都绝不能阻断打开控制台。本测试对每一个失败点各注入
// 一次，断言 SyncOnOpen 都会返回（而不是 panic、不是挂住），并把错误如实
// 带在 Err 里交给调用方。
//
// 这条测试的价值不在于覆盖率：它是「用户双击应用打不开」这个最严重后果的
// 唯一自动化防线。
func TestSyncOnOpenNeverBlocksOnFailure(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		mutate  func(*shell.OpenSyncDeps)
		wantErr bool
	}{
		{"agentd 起不来", func(d *shell.OpenSyncDeps) {
			d.EnsureRunning = func() error { return boom }
		}, true},
		{"定位不到已装二进制", func(d *shell.OpenSyncDeps) {
			d.InstalledPath = func() (string, error) { return "", boom }
		}, true},
		{"读不出已装版本", func(d *shell.OpenSyncDeps) {
			d.InstalledVersion = func(string) string { return "" }
		}, false}, // 判不出 → SyncSkip，不是错误
		{"探不出活跃任务数", func(d *shell.OpenSyncDeps) {
			d.Busy = func(context.Context) (int, error) { return 0, boom }
		}, false}, // 探不出 → 按繁忙处置 → SyncBlocked，不是错误
		{"换版失败", func(d *shell.OpenSyncDeps) {
			d.Sync.Activate = func(string, string) (string, error) { return "", boom }
		}, true},
		{"触发重启失败", func(d *shell.OpenSyncDeps) {
			d.Sync.RestartAgentd = func(context.Context, bool) error { return boom }
		}, true},
		{"agentd 没回来", func(d *shell.OpenSyncDeps) {
			d.Wait.Version = func(context.Context) (string, error) { return "v0.3.0", nil }
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var seq []string
			d := okDeps(&seq)
			c.mutate(&d)
			done := make(chan shell.SyncOutcome, 1)
			ctx, cancel := context.WithCancel(context.Background())
			// 「agentd 没回来」那条会走满超时循环；用可取消的 Sleep 把它压短，
			// 同时验证取消路径也返回而不是挂住
			d.Wait.Sleep = func(time.Duration) { cancel() }
			go func() { done <- shell.SyncOnOpen(ctx, d) }()
			select {
			case out := <-done:
				if c.wantErr && out.Err == nil {
					t.Errorf("想要一个错误，却拿到 nil（Plan=%v）", out.Plan)
				}
				if !c.wantErr && out.Err != nil {
					t.Errorf("不该有错误，却拿到 %v", out.Err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("SyncOnOpen 挂住了——D8 被破，用户双击应用会打不开")
			}
		})
	}
}

// TestSyncOnOpenBlockedWhenBusy 钉住有活跃任务时不换，且把任务数带出来给 UI。
func TestSyncOnOpenBlockedWhenBusy(t *testing.T) {
	var seq []string
	d := okDeps(&seq)
	d.Busy = func(context.Context) (int, error) { return 3, nil }
	out := shell.SyncOnOpen(context.Background(), d)
	if out.Plan != shell.SyncBlocked {
		t.Errorf("Plan = %v，想要 SyncBlocked", out.Plan)
	}
	if out.Busy != 3 {
		t.Errorf("Busy = %d，想要 3——托盘要用它显示「N 个任务进行中」", out.Busy)
	}
	if slices.Contains(seq, "activate") {
		t.Error("有活跃任务时仍然换了版")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestSyncOnOpen -v`
Expected: 编译失败，`undefined: shell.SyncOnOpen`

- [ ] **Step 3: 实现 `SyncOnOpen`**

Create `desktop/internal/shell/open_sync.go`：

```go
// 本文件把「打开控制台之前的版本对账与同步」整条序列收在一个可测函数里
// （spec §5）。
//
// 职责：
//   - SyncOnOpen 按承重顺序编排：EnsureRunning → 探 Busy → PlanSync → DoSync
//     → WaitAgentdBack，并把结果折成一个 SyncOutcome 交给调用方
//
// 边界（承重）：
//   - **顺序必须留在本文件，不能散回 main.go。** 散回去它就无法被单独测试，
//     而这是整个设计最容易被后人改坏的地方（见 TestSyncOnOpenOrderIsLoadBearing）
//   - **本函数绝不阻断调用方**（spec D8）：任何失败都折进 SyncOutcome.Err
//     返回，绝不 panic、绝不 os.Exit、绝不无限等待。调用方拿到 Err 之后
//     仍然必须继续加载控制台
//   - 不碰 UI。进度只经 Progress 回调往外送，本文件不知道有没有窗口
package shell

import (
	"context"
	"fmt"
)

// OpenSyncDeps 是打开控制台前那段同步序列的全部外部依赖。
type OpenSyncDeps struct {
	// EnsureRunning 确保 agentd 在跑。必须最先调——闸一判据要从它那儿探
	EnsureRunning func() error
	// InstalledPath 返回已装二进制的路径（生产实现是 ResolveBinPath("")）。
	// 注意取的是**实际在用的**那一份，不是约定落点：agentd 正是从它启动的
	InstalledPath func() (string, error)
	// InstalledVersion 从二进制里读版本号，读不出返回空串
	InstalledVersion func(path string) string
	// Busy 返回活跃任务数。生产实现是 client.Status 取 len(Active)
	Busy func(ctx context.Context) (int, error)
	// EmbedVersion 是内嵌二进制的版本（embedbin.Version），开发构建下为空
	EmbedVersion string
	// EmbedAvailable 是本次构建有没有内嵌二进制（embedbin.Available()）
	EmbedAvailable bool
	// Sync 是换版动作的依赖，见 DoSync
	Sync SyncDeps
	// Wait 是等待 agentd 回来的依赖，见 WaitAgentdBack
	Wait WaitDeps
	// Progress 是阶段回调，供 UI 显示。传 nil 安全
	Progress func(stage string)
}

// SyncOutcome 是一次对账的结果。
//
// **Err 非 nil 不代表调用方该停下。** 它只说明「同步这件事没做成」，控制台
// 照样要打开（spec D8）。调用方的正确处置是：把 Err 如实展示出来，然后继续。
type SyncOutcome struct {
	// Plan 是四态决策结果，供 UI 决定显示什么
	Plan SyncPlan
	// Busy 是探到的活跃任务数；探测失败时为 -1
	Busy int
	// Err 是同步过程中的错误，nil 表示没出错（含「本就不需要同步」）
	Err error
}

// SyncOnOpen 在打开控制台之前对账并（必要时）同步 agentd/CLI 到内嵌的版本。
//
// 返回的 SyncOutcome 永远可用，**本函数不会返回错误、不会 panic、不会挂住**。
//
// 注意：调用方拿到结果后必须继续加载控制台，无论 Outcome.Err 是什么。
func SyncOnOpen(ctx context.Context, d OpenSyncDeps) SyncOutcome {
	progress := d.Progress
	if progress == nil {
		progress = func(string) {}
	}

	// ① EnsureRunning 必须最先：闸一判据要从 agentd 的 /api/status 探，
	// 它不在跑就探不出。这里起的是**旧**二进制，无妨——同步紧接着会重启它
	if err := d.EnsureRunning(); err != nil {
		logger.Error("确保 agentd 运行失败，跳过本次对账", "cause", err)
		return SyncOutcome{Plan: SyncSkip, Busy: -1, Err: fmt.Errorf("确保 agentd 运行: %w", err)}
	}

	installed, err := d.InstalledPath()
	if err != nil {
		logger.Error("定位已装 handoff 失败，跳过本次对账", "cause", err)
		return SyncOutcome{Plan: SyncSkip, Busy: -1, Err: fmt.Errorf("定位已装 handoff: %w", err)}
	}
	installedVer := d.InstalledVersion(installed)
	decision := DecideRelease(installed, installedVer, d.EmbedVersion)

	// ② 闸一必须在换文件之前，与 cmd/upgrade.go:500 同序。反过来会留下
	// 「磁盘是新的、跑着的是旧的」这种持续不一致，且用户看不出为什么
	busy, berr := d.Busy(ctx)
	if berr != nil {
		// 探不出就按「有任务」处置：猜错的代价不对称——误判空闲会在用户
		// 有活跃任务时重启 agentd，误判繁忙只是这次不升级
		logger.Warn("探活跃任务数失败，按有任务处置", "cause", berr)
		busy = -1
	}

	plan := PlanSync(decision, busy, d.EmbedAvailable)
	logger.Info("同步对账结果", "plan", plan.String(), "decision", decision.String(),
		"installed", installed, "installed_version", installedVer,
		"embedded_version", d.EmbedVersion, "busy", busy)

	if plan != SyncDo {
		return SyncOutcome{Plan: plan, Busy: busy}
	}

	if err := DoSync(ctx, installed, false, d.Sync, progress); err != nil {
		logger.Error("同步失败，将用现有版本继续打开控制台", "cause", err)
		return SyncOutcome{Plan: plan, Busy: busy, Err: err}
	}

	// ③ 等 agentd 带着新版本回来才算完。不等就握手，会打到一个正在退出的
	// 进程上——报错是 401 或连接被拒，看起来跟「刚升过级」毫无关系
	progress("正在等 agentd 重启完成")
	if err := WaitAgentdBack(ctx, d.EmbedVersion, d.Wait); err != nil {
		logger.Error("agentd 未在预期时间内带新版本回来", "want_version", d.EmbedVersion, "cause", err)
		return SyncOutcome{Plan: plan, Busy: busy, Err: err}
	}
	logger.Info("同步完成", "version", d.EmbedVersion)
	return SyncOutcome{Plan: plan, Busy: busy}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestSyncOnOpen -v`
Expected: 三个测试函数（含子测试）全 PASS

- [ ] **Step 5: 变异复验**

1. 把 `d.Busy(ctx)` 那一段挪到 `DoSync` 调用**之后** → 期望 `TestSyncOnOpenOrderIsLoadBearing` 与 `TestSyncOnOpenBlockedWhenBusy` 红
2. 把 `d.EnsureRunning()` 挪到 `d.Busy(ctx)` 之后 → 期望 `TestSyncOnOpenOrderIsLoadBearing` 红
3. 把 `busy = -1` 改成 `busy = 0`（探不出当空闲）→ 期望 `TestSyncOnOpenNeverBlocksOnFailure` 的「探不出活跃任务数」子测试红（它会变成 SyncDo 并真去换版）
4. 把 `WaitAgentdBack` 的返回值忽略掉 → 期望「agentd 没回来」子测试红

每条确认红之后改回来。

- [ ] **Step 6: 接进 `main.go`**

`desktop/main.go` 的 `openConsole` 里，把原来的

```go
		spec, err := specFor(ep)
		if err != nil { ... }
		if err := shell.EnsureRunning(logger, spec); err != nil { ... }
```

替换为一次 `SyncOnOpen` 调用。**`EnsureRunning` 失败时的既有处置（`showError` + return）必须保留**——那是「agentd 起不来」，与同步失败不是一回事：

```go
		// 对账与同步接在这里，在加载控制台**之前**。此刻窗口还没显示，
		// 重启 agentd 全程不进用户视野——他只会觉得这次打开慢了几秒。
		//
		// 承重：无论 out.Err 是什么，下面加载控制台的代码都必须照常执行
		//（spec D8）。同步失败只是少升一次级，阻断则是「双击打不开应用」。
		out := shell.SyncOnOpen(ctx, openSyncDeps(ep))
		switch {
		case out.Err != nil && out.Plan == shell.SyncSkip:
			// 这一支是 agentd 根本起不来或定位不到二进制——控制台加载不了，
			// 必须停下并告诉用户。与同步失败不是一回事
			logger.Error("agentd 不可用，无法加载控制台", "cause", out.Err)
			showError(app, "无法启动 agentd", out.Err.Error())
			return
		case out.Err != nil:
			// 同步没做成，但 agentd 在跑（只是版本旧）。如实记录，继续
			logger.Warn("同步未完成，将用现有版本继续", "plan", out.Plan.String(), "cause", out.Err)
			noteSyncFailed(out)
		case out.Plan == shell.SyncBlocked:
			logger.Info("有活跃任务，本次不同步", "busy", out.Busy)
			noteSyncBlocked(out)
		}
```

`openSyncDeps(ep)` 是新增的包级辅助函数，把生产实现装配进 `OpenSyncDeps`：

```go
// openSyncDeps 装配 SyncOnOpen 的生产依赖。
//
// 单独抽出来只为让 main.go 里的启动序列保持可读——所有「怎么做」都在
// shell 包里，这里只回答「用哪个实现」。
func openSyncDeps(ep shell.Endpoint) shell.OpenSyncDeps {
	c := client.New(ep.Addr, ep.Token)
	return shell.OpenSyncDeps{
		EnsureRunning: func() error {
			spec, err := specFor(ep)
			if err != nil {
				return err
			}
			return shell.EnsureRunning(logger, spec)
		},
		InstalledPath:    func() (string, error) { return shell.ResolveBinPath("") },
		InstalledVersion: readInstalledVersion,
		Busy: func(ctx context.Context) (int, error) {
			st, err := c.Status(ctx)
			if err != nil {
				return 0, err
			}
			return len(st.Active), nil
		},
		EmbedVersion:   embedbin.Version,
		EmbedAvailable: embedbin.Available(),
		Sync: shell.SyncDeps{
			OpenEmbedded:  embedbin.Open,
			Activate:      release.Activate,
			SkillInstall:  execSkillInstall,
			RestartAgentd: func(ctx context.Context, force bool) error {
				_, err := c.RestartAgentd(ctx, force)
				return err
			},
		},
		Wait: shell.WaitDeps{
			Version: func(ctx context.Context) (string, error) {
				st, err := c.Status(ctx)
				if err != nil {
					return "", err
				}
				return st.Build.Version, nil
			},
			Nudge: nudgeAgentd(ep),
		},
		Progress: emitSyncProgress,
	}
}

// execSkillInstall 在指定二进制上跑 skill install。
//
// 必须 exec **新**二进制：skill 随二进制分发（B59），当前进程内嵌的是旧的。
// hideConsole 是必需的——薄壳是 GUI 进程，不压这一下会在用户屏幕上闪黑窗口。
func execSkillInstall(ctx context.Context, bin string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, "skill", "install")
	hideConsole(c)
	return c.CombinedOutput()
}
```

`st.Build.Version` 的字段名按 `internal/proto/status.go` 里 `StatusResp` 的实际定义写；若版本在 `BuildInfo` 的别的字段上，用那个。**不要猜，先读一遍结构体。**

`nudgeAgentd`、`emitSyncProgress`、`noteSyncFailed`、`noteSyncBlocked` 在本 task 里先写成最小实现（记日志即可），Task 9/10 再接到面板与托盘上：

```go
// nudgeAgentd 催进程管理器把 agentd 拉起来。
//
// 复用 shell.EnsureRunning：它在 agentd 不在跑时会 Install + 拉起，Windows
// 侧正是 schtasks /Run + 500ms 轮询复核（internal/service/windows.go:271），
// 恰是这里要的动作。macOS 的 launchd KeepAlive 秒级自拉，催一次也无害。
func nudgeAgentd(ep shell.Endpoint) func() error {
	return func() error {
		spec, err := specFor(ep)
		if err != nil {
			return err
		}
		return shell.EnsureRunning(logger, spec)
	}
}

// emitSyncProgress 把同步阶段送给 UI。Task 9 之前先只记日志。
func emitSyncProgress(stage string) { logger.Info("同步进度", "stage", stage) }

// noteSyncFailed 记录一次失败的同步，供托盘展示。Task 10 之前先只记日志。
func noteSyncFailed(out shell.SyncOutcome) {
	logger.Warn("同步失败已记录", "plan", out.Plan.String(), "cause", out.Err)
}

// noteSyncBlocked 记录一次被闸一拦下的同步，供托盘展示。Task 10 之前先只记日志。
func noteSyncBlocked(out shell.SyncOutcome) {
	logger.Info("同步被闸一拦下已记录", "busy", out.Busy)
}
```

- [ ] **Step 7: 跑全量并手验一次开发构建下的行为**

Run: `cd desktop && go build ./... && go test ./... && gofmt -l .`
Expected: 全绿

开发构建（未带 `-tags embedbin`）下 `EmbedAvailable` 为 false、`EmbedVersion` 为空，`DecideRelease` 会走 `DecisionUseExisting` → `PlanSync` 返回 `SyncSkip`。跑一次本地壳确认日志里出现 `plan=skip` 且控制台照常打开：

```bash
cd desktop && go run . 2>&1 | grep -E "同步对账结果|加载控制台"
```

Expected: 看到 `plan=skip`，控制台窗口正常出来。

- [ ] **Step 8: 日志与注释自查**

- 三条承重顺序各有编号注释说明「为什么是这个位置」✅
- 对账结果有一条 Info 带全部判据（plan/decision/两个版本/busy）✅ —— 真机走查时这一行是唯一的取证入口
- 每个错误分支有 Error/Warn + cause ✅
- 成功路径有 Info（「同步完成」）✅
- `SyncOutcome.Err` 的 doc comment 必须写明「非 nil 不代表调用方该停下」✅

- [ ] **Step 9: Commit**

```bash
git add desktop/internal/shell/open_sync.go desktop/internal/shell/open_sync_test.go desktop/main.go
git commit -m "feat(desktop): SyncOnOpen——把承重顺序收进可测函数并接进 openConsole

顺序留在 main.go 里就无法单独测试，而这是整个设计最容易被改坏的地方。
三条承重：EnsureRunning 在探 Busy 之前（判据要从 agentd 探）、闸一在换
文件之前（与 cmd/upgrade.go:500 同序）、等 agentd 带新版本回来才握手。

TestSyncOnOpenNeverBlocksOnFailure 是 D8 的唯一守卫：同步路径上任何失败
都不能让用户双击应用打不开。"
```

---

## Task 8: `CheckLatest`——有没有新版安装包

spec §6。与同步完全独立的一条路：要出网、可失败、失败静默。

**Files:**
- Create: `desktop/internal/shell/latest.go`
- Create: `desktop/internal/shell/latest_test.go`

**Interfaces:**
- Consumes: `selfupdate.LoadCLICheck(dataDir) *CLICheck`、`selfupdate.SaveCLICheck(dataDir, *CLICheck) error`、`selfupdate.CLICheckStale(*CLICheck, time.Time) bool`、`selfupdate.CompareVersion`（Task 2）
- Produces: `func CheckLatest(ctx context.Context, dataDir, current string, d LatestDeps) (tag string, newer bool)` 与 `type LatestDeps struct { Fetch func(ctx context.Context) (string, error); Now func() time.Time }`。Task 10 依赖。

- [ ] **Step 1: 写失败测试**

Create `desktop/internal/shell/latest_test.go`：

```go
package shell_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// TestCheckLatestSilentOnUnknownCurrent 钉住 spec §6.3：基准判不出就不提示。
//
// 开发构建下 embedbin.Version 为空。此时既不能说「有新版」（不知道跟谁比），
// 也不能瞎猜——症状（一直提示或永不提示）都不会报错，排查成本极高。
func TestCheckLatestSilentOnUnknownCurrent(t *testing.T) {
	fetched := false
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { fetched = true; return "v9.9.9", nil },
		Now:   time.Now,
	}
	tag, newer := shell.CheckLatest(context.Background(), t.TempDir(), "", d)
	if newer || tag != "" {
		t.Errorf("current 为空时返回了 (%q,%v)，想要 (\"\",false)", tag, newer)
	}
	if fetched {
		t.Error("current 判不出时不该发网络请求——白耗一次 GitHub 匿名限流额度")
	}
}

// TestCheckLatestSilentOnFetchError 钉住任何失败都静默。
//
// 通知路是锦上添花，它自己绝不能成为故障源（沿用 clicheck.go 文件头的既有约定）。
func TestCheckLatestSilentOnFetchError(t *testing.T) {
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { return "", errors.New("网络不通") },
		Now:   time.Now,
	}
	tag, newer := shell.CheckLatest(context.Background(), t.TempDir(), "v0.3.0", d)
	if newer || tag != "" {
		t.Errorf("拉取失败时返回了 (%q,%v)，想要 (\"\",false)", tag, newer)
	}
}

// TestCheckLatestUsesCacheWithin24h 钉住共用 CLI 那份限流缓存。
//
// api.github.com 有 60 次/小时/IP 的匿名限流，而多台执行机很可能共用一个
// 代理出口 IP。限流一旦触发，agentd 的换版也会跟着失败——所以这里省下的
// 不只是自己的一次请求。
func TestCheckLatestUsesCacheWithin24h(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: "v0.4.0"}); err != nil {
		t.Fatal(err)
	}
	fetched := false
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { fetched = true; return "v9.9.9", nil },
		Now:   func() time.Time { return now.Add(time.Hour) },
	}
	tag, newer := shell.CheckLatest(context.Background(), dir, "v0.3.0", d)
	if fetched {
		t.Error("缓存还新鲜却又发了一次请求")
	}
	if !newer || tag != "v0.4.0" {
		t.Errorf("返回 (%q,%v)，想要 (\"v0.4.0\",true)", tag, newer)
	}
}

// TestCheckLatestRefetchesAfter24h 钉住缓存过期会重查并回写。
func TestCheckLatestRefetchesAfter24h(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: "v0.4.0"}); err != nil {
		t.Fatal(err)
	}
	later := now.Add(25 * time.Hour)
	d := shell.LatestDeps{
		Fetch: func(context.Context) (string, error) { return "v0.5.0", nil },
		Now:   func() time.Time { return later },
	}
	tag, newer := shell.CheckLatest(context.Background(), dir, "v0.3.0", d)
	if !newer || tag != "v0.5.0" {
		t.Errorf("返回 (%q,%v)，想要 (\"v0.5.0\",true)", tag, newer)
	}
	// 回写过缓存，下一次才不会又查
	got := selfupdate.LoadCLICheck(dir)
	if got == nil || got.Latest != "v0.5.0" {
		t.Errorf("缓存没被回写：%+v", got)
	}
}

// TestCheckLatestNotNewerWhenSameOrOlder 钉住不会反向提示。
//
// B59 验收当场抓出过反向提示（装了 v0.1.1 的机器被劝「有新版本 v0.1.0」），
// 根因是只判「不相等」而没判方向。
func TestCheckLatestNotNewerWhenSameOrOlder(t *testing.T) {
	for _, latest := range []string{"v0.3.0", "v0.2.9", "v0.10.0"} {
		dir := t.TempDir()
		now := time.Now()
		if err := selfupdate.SaveCLICheck(dir, &selfupdate.CLICheck{CheckedAt: now, Latest: latest}); err != nil {
			t.Fatal(err)
		}
		d := shell.LatestDeps{Now: func() time.Time { return now }}
		tag, newer := shell.CheckLatest(context.Background(), dir, "v0.11.0", d)
		want := latest == "" // 恒 false，这里只为表达「都不该提示」
		if newer != want {
			t.Errorf("current=v0.11.0 latest=%s 时 newer=%v，想要 false（tag=%q）", latest, newer, tag)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test ./internal/shell/ -run TestCheckLatest -v`
Expected: 编译失败，`undefined: shell.CheckLatest`

- [ ] **Step 3: 实现**

Create `desktop/internal/shell/latest.go`：

```go
// 本文件负责「有没有新版安装包可下载」的查询与限流（spec §6）。
//
// 职责：
//   - CheckLatest 查最新 release 并与本机版本比，回答「要不要提示用户去下新版」
//
// 边界（承重）：
//   - **与同步路完全独立。** 同步用内嵌的字节、不出网、必然可完成；本文件
//     要出网、可失败。两者共用同一个进程但不共享任何状态，一条挂了不影响另一条
//   - **任何失败一律静默**，当作「没有新版」。通知是锦上添花，它自己绝不能
//     成为故障源（沿用 internal/selfupdate/clicheck.go 文件头的既有约定）
//   - 不下载、不安装。点击后打开浏览器由 UI 层做，本文件只回答查询
package shell

import (
	"context"
	"time"

	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// LatestDeps 是查询的外部依赖，抽出来只为可测（真实实现要发 HTTP）。
type LatestDeps struct {
	// Fetch 返回最新 release 的 tag。生产实现是 release.NewClient(...).Latest
	Fetch func(ctx context.Context) (string, error)
	// Now 取当前时间，用于判缓存新鲜度
	Now func() time.Time
}

// CheckLatest 查有没有比 current 更新的 release。
//
// 参数：
//   - dataDir: handoff 的数据目录，限流缓存放在它下面
//   - current: 本机版本（即 embedbin.Version）。**为空一律返回不提示**
//
// 返回：
//   - tag: 最新版本号；newer 为 false 时无意义
//   - newer: 是否确实更新。任何失败、任何判不出，一律 false
//
// 注意：
//   - 缓存与 CLI 侧的更新提示**共用同一个文件**（selfupdate.CLICheckPath）。
//     看着像耦合，其实正是要的：api.github.com 有 60 次/小时/IP 的匿名限流，
//     多个消费者各查各的正是触发限流的方式，而限流一旦触发，agentd 的换版
//     也会跟着失败
//   - 方向判断走 selfupdate.CompareVersion，只判「不相等」会造出反向提示
//     （B59 验收当场抓出过：装了 v0.1.1 的机器被劝升到 v0.1.0）
func CheckLatest(ctx context.Context, dataDir, current string, d LatestDeps) (string, bool) {
	if current == "" {
		// 开发构建未注入版本。既不能说有新版（不知道跟谁比），也不能瞎猜——
		// 两种猜错的症状（一直提示 / 永不提示）都不报错，排查成本极高。
		// 提前返回还省掉一次请求：那是宝贵的限流额度
		logger.Debug("本机版本判不出，跳过新版检查")
		return "", false
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}

	c := selfupdate.LoadCLICheck(dataDir)
	if selfupdate.CLICheckStale(c, now()) {
		if d.Fetch == nil {
			logger.Debug("未配置 Fetch，跳过新版检查")
			return "", false
		}
		logger.Info("检查有没有新版安装包", "current", current)
		tag, err := d.Fetch(ctx)
		if err != nil {
			// 静默：见文件头的边界约定
			logger.Debug("查最新 release 失败，按没有新版处理", "cause", err)
			return "", false
		}
		c = &selfupdate.CLICheck{CheckedAt: now(), Latest: tag}
		if err := selfupdate.SaveCLICheck(dataDir, c); err != nil {
			// 写不进缓存不影响本次结论，只是下次会再查一遍
			logger.Debug("回写检查缓存失败，本次结论不受影响", "cause", err)
		}
	}
	if c == nil || c.Latest == "" {
		return "", false
	}
	cmp, ok := selfupdate.CompareVersion(c.Latest, current)
	if !ok || cmp <= 0 {
		logger.Debug("没有更新的版本", "latest", c.Latest, "current", current, "comparable", ok)
		return "", false
	}
	logger.Info("发现新版安装包", "latest", c.Latest, "current", current)
	return c.Latest, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestCheckLatest -v`
Expected: 五条全 PASS

- [ ] **Step 5: 变异复验**

1. 把 `if current == ""` 那块删掉 → 期望 `TestCheckLatestSilentOnUnknownCurrent` 红
2. 把 `!ok || cmp <= 0` 改成 `c.Latest == current` → 期望 `TestCheckLatestNotNewerWhenSameOrOlder` 红（`v0.2.9` 那条会被判成有新版）
3. 把 `selfupdate.CLICheckStale(c, now())` 改成 `true` → 期望 `TestCheckLatestUsesCacheWithin24h` 红
4. 把 `selfupdate.SaveCLICheck` 那行删掉 → 期望 `TestCheckLatestRefetchesAfter24h` 红

每条确认红之后改回来。

- [ ] **Step 6: 日志与注释自查**

- 发起检查有 Info（带 current）✅
- 发现新版有 Info（带 latest 与 current）✅ —— 成功路径不静默
- 所有失败与「没有新版」是 Debug（这条路径每次打开应用都会走，Info 会刷屏）✅
- 文件头写清「与同步路完全独立」「失败一律静默」两条边界 ✅
- 共用缓存的「为什么」写在 doc comment 里 ✅

- [ ] **Step 7: Commit**

```bash
git add desktop/internal/shell/latest.go desktop/internal/shell/latest_test.go
git commit -m "feat(desktop): CheckLatest——有没有新版安装包，24h 限流且失败静默

与 CLI 侧共用同一份限流缓存：api.github.com 有 60 次/小时/IP 的匿名限流，
多个消费者各查各的正是触发限流的方式，而限流一旦触发 agentd 的换版也会
跟着失败。方向判断走 CompareVersion——只判不相等会造出反向提示。"
```

---

## Task 9: 升级面板——第三个 UI 面

spec §7 / §7.1。独立窗口 + 内嵌前端的第二个页面。主窗口此时正显示控制台外链，不能抢。

**Files:**
- Modify: `desktop/frontend/vite.config.ts`（改成多页入口）
- Create: `desktop/frontend/upgrade.html`
- Create: `desktop/frontend/src/upgrade.ts`
- Modify: `desktop/frontend/public/style.css`（追加面板样式）
- Create: `desktop/panel.go`（新窗口的创建与事件推送）

**Interfaces:**
- Consumes: `shell.AwaitWebviewReady(ctx, ready)`（既有，`desktop/internal/shell/ready.go`）
- Produces:
  - `func openUpgradePanel(app *application.App) *upgradePanel`
  - `func (p *upgradePanel) Line(s string)` — 追加一行输出
  - `func (p *upgradePanel) State(state, detail string)` — 置三态之一（`running` / `ok` / `fail`）
  - `func (p *upgradePanel) OnForceRetry(fn func())` — 注册「带 --force 重试」的回调
  - Task 10、11 依赖这四个。

- [ ] **Step 1: 改成多页入口**

`desktop/frontend/vite.config.ts` 的 `defineConfig` 里加：

```ts
  build: {
    rollupOptions: {
      // 多页入口。缺了这段，upgrade.html 不会被打进 dist，而 go:embed
      // all:frontend/dist 照样能编过——症状是窗口打开后 404 一片空白，
      // 且没有任何构建期报错
      input: {
        main: "index.html",
        upgrade: "upgrade.html",
      },
    },
  },
```

- [ ] **Step 2: 写页面骨架**

Create `desktop/frontend/upgrade.html`（照抄 `index.html` 的 head 结构，只换 body 与入口脚本）：

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>handoff 升级</title>
    <link rel="stylesheet" href="/style.css" />
  </head>
  <body>
    <!-- 顶部让出 28px：与 Go 侧 desktopTopInset 必须一致，见 main.go 的注释 -->
    <div class="top-inset"></div>
    <main class="panel">
      <h2 id="panel-title">正在升级</h2>
      <pre id="panel-output" aria-live="polite"></pre>
      <div id="panel-actions" hidden>
        <button id="panel-force" type="button">带 --force 重试</button>
        <p class="panel-hint">
          仅当上面显示有活跃任务时才有用。强制会重启 agentd——执行者进程不受影响，
          中断的是事件推送与在途请求。
        </p>
      </div>
    </main>
    <script type="module" src="/src/upgrade.ts"></script>
  </body>
</html>
```

- [ ] **Step 3: 写页面脚本**

Create `desktop/frontend/src/upgrade.ts`：

```ts
// 升级面板：显示同步进度与 handoff upgrade --now 的流式输出。
//
// 职责：
//   - 监听 upgrade-line：把一行输出追加到输出区并滚到底
//   - 监听 upgrade-state：切换三态标题，失败时亮出「带 --force 重试」
//   - 点「带 --force 重试」时发 upgrade-force-retry 事件回 Go 侧
//
// 边界：
//   - 不解析输出内容。判断「是不是闸一导致的失败」交给用户自己看——输出是
//     给人看的中文表格，解析它是脆的，且失效方式是「按钮再也不出现」，
//     没有任何报错（见 spec §7.2）
//   - 不自己发起任何动作，只显示与回传点击
import { Events } from "@wailsio/runtime";

const title = document.getElementById("panel-title") as HTMLHeadingElement;
const output = document.getElementById("panel-output") as HTMLPreElement;
const actions = document.getElementById("panel-actions") as HTMLDivElement;
const force = document.getElementById("panel-force") as HTMLButtonElement;

Events.On("upgrade-line", (ev: { data: string }) => {
  output.textContent += ev.data + "\n";
  // 滚到底：长输出时用户关心的永远是最后一行
  output.scrollTop = output.scrollHeight;
});

Events.On("upgrade-state", (ev: { data: { state: string; detail: string } }) => {
  const { state, detail } = ev.data;
  switch (state) {
    case "running":
      title.textContent = detail || "正在升级";
      actions.hidden = true;
      break;
    case "ok":
      title.textContent = detail || "升级完成";
      actions.hidden = true;
      break;
    case "fail":
      title.textContent = detail || "升级失败";
      // 只要失败就亮按钮，不去判断失败原因——理由见文件头
      actions.hidden = false;
      force.disabled = false;
      break;
  }
});

force.addEventListener("click", () => {
  // 立刻禁用，避免连点发出两次强制升级
  force.disabled = true;
  Events.Emit("upgrade-force-retry", null);
});
```

- [ ] **Step 4: 追加样式**

`desktop/frontend/public/style.css` 末尾追加：

```css
.panel { padding: 0 16px 16px; }
.panel h2 { font-size: 15px; margin: 8px 0; }
#panel-output {
  height: 320px; overflow: auto; margin: 0;
  padding: 8px; border-radius: 6px;
  background: #1e1e1e; color: #e6e6e6;
  font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  white-space: pre-wrap; word-break: break-all;
}
#panel-actions { margin-top: 12px; }
.panel-hint { font-size: 12px; color: #888; margin: 6px 0 0; }
```

- [ ] **Step 5: 写 Go 侧的窗口**

Create `desktop/panel.go`：

```go
// 本文件负责升级面板窗口——薄壳的第三个 UI 面（spec §7）。
//
// 职责：
//   - 创建并持有一个独立窗口，加载内嵌前端的 /upgrade.html
//   - 把同步进度与 upgrade 命令的输出逐行推给它
//
// 边界（承重）：
//   - **必须为本窗口单独挂 WindowRuntimeReady，并在发任何事件之前等它就绪。**
//     Wails 的 windowsWebviewWindow.setURL 至今没有 nil 守卫（相邻的 execJS 有
//     if w.chromium == nil { return }），往一个还没建好的 chromium 上动作会让
//     进程**直接消失、没有任何输出**。rc7 就是这么来的，漏挂就是第二次
//   - 不抢主窗口：主窗口此刻正显示控制台外链
//   - 不自己决定显示什么内容，只做通道
package main

import (
	"context"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// panelReadyTimeout 是等面板 webview 就绪的上限。
//
// 与控制台那条（30s）取同一量级：超时只意味着这次不显示面板，不影响任何
// 实际动作——升级照跑，输出照进日志。
const panelReadyTimeout = 30 * time.Second

// upgradePanel 是升级面板窗口的句柄。
type upgradePanel struct {
	app   *application.App
	win   *application.WebviewWindow
	ready <-chan struct{}
	// once 保证 ready 只被 close 一次：WindowRuntimeReady 在窗口重新加载时
	// 会再次触发，close 两次会 panic
	once      sync.Once
	forceOnce sync.Once
	// readyOnce/readyOK 把「等就绪」的结果缓存下来，见 await 的注释
	readyOnce sync.Once
	readyOK   bool
}

// openUpgradePanel 创建并显示升级面板窗口。
//
// 返回的句柄总是可用的：即便窗口没能就绪，Line/State 也只是把内容记进日志
// 而不报错——面板是给用户看的，它坏了不该让升级本身失败。
func openUpgradePanel(app *application.App) *upgradePanel {
	readyCh := make(chan struct{})
	p := &upgradePanel{app: app, ready: readyCh}
	p.win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff 升级",
		Width:  680,
		Height: 480,
		URL:    "/upgrade.html",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: desktopTopInset,
		},
	})
	// 承重：见文件头。漏了这一挂，下面 AwaitWebviewReady 会一直等到超时，
	// 面板永远空白——而这不会有任何报错
	p.win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		p.once.Do(func() { close(readyCh) })
	})
	p.win.Show()
	logger.Info("升级面板窗口已创建")
	return p
}

// await 等面板就绪，**结果只算一次**。
//
// 为什么必须缓存：Line 会被调几十上百次（upgrade 的输出逐行进来）。每次都
// 开一个 30 秒超时，面板万一一直不就绪，整条升级就会被拖成「行数 × 30 秒」
// ——用户看到的是彻底卡死，而根因只是一个没建好的 webview。
func (p *upgradePanel) await() bool {
	p.readyOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), panelReadyTimeout)
		defer cancel()
		if err := shell.AwaitWebviewReady(ctx, p.ready); err != nil {
			logger.Error("升级面板未就绪，此后内容只进日志", "cause", err)
			return
		}
		p.readyOK = true
	})
	return p.readyOK
}

// Line 往面板追加一行输出。
func (p *upgradePanel) Line(s string) {
	logger.Info("升级输出", "line", s)
	if !p.await() {
		return
	}
	p.app.Event.Emit("upgrade-line", s)
}

// State 切换面板的三态。
//
// 参数：
//   - state: running / ok / fail
//   - detail: 显示在标题上的一句话，空串时用默认文案
func (p *upgradePanel) State(state, detail string) {
	logger.Info("升级面板状态", "state", state, "detail", detail)
	if !p.await() {
		return
	}
	p.app.Event.Emit("upgrade-state", map[string]string{"state": state, "detail": detail})
}

// OnForceRetry 注册「带 --force 重试」的回调。只会注册一次。
func (p *upgradePanel) OnForceRetry(fn func()) {
	p.forceOnce.Do(func() {
		p.app.Event.On("upgrade-force-retry", func(*application.CustomEvent) {
			logger.Info("用户点了带 --force 重试")
			go fn()
		})
	})
}
```

`application.WebviewWindowOptions` 的字段名与 `app.Window.NewWithOptions` 的调用形式按 `desktop/main.go` 里既有那次创建窗口的写法对齐——**照抄那处，不要凭 Wails 文档猜**，本仓库钉的是 v3.0.0-beta.8。

- [ ] **Step 6: 构建并手验面板真能出来**

```bash
cd desktop/frontend && npm run build && ls dist/upgrade.html
```

Expected: `dist/upgrade.html` 存在。**不存在说明 Step 1 的多页入口没生效**——此时 Go 侧编译照样能过，症状是窗口一片空白。

在 `main.go` 的 `ApplicationStarted` 回调里临时加一行 `p := openUpgradePanel(app); p.Line("hello"); p.State("fail", "测试")`，跑 `cd desktop && go run .`：

Expected: 弹出一个 680×480 的窗口，显示 `hello`、标题「测试」、下方出现「带 --force 重试」按钮。**验完把这三行删掉。**

- [ ] **Step 7: 日志与注释自查**

- 窗口创建有 Info ✅
- 每一行输出与每一次状态切换都先记日志再推 UI ✅ —— 顺序是刻意的：面板可能没就绪，日志是唯一保底的取证
- 面板未就绪有 Error + cause ✅
- 用户点强制重试有 Info ✅
- 文件头把 rc7 那条承重写清楚 ✅

- [ ] **Step 8: Commit**

```bash
git add desktop/frontend/vite.config.ts desktop/frontend/upgrade.html desktop/frontend/src/upgrade.ts desktop/frontend/public/style.css desktop/panel.go
git commit -m "feat(desktop): 升级面板窗口——薄壳的第三个 UI 面

必须为新窗口单独挂 WindowRuntimeReady 并等它就绪：Wails 的 setURL 至今
没有 nil 守卫，往没建好的 chromium 上动作会让进程直接消失且无任何输出，
rc7 就是这么来的。

多页入口漏配的症状同样是静默的：Go 侧照常编过，窗口打开后一片空白。"
```

---

## Task 10: 托盘条目

spec §7.2。三种条目：同步被闸一拦下、有新版安装包、升级执行机。

**Files:**
- Modify: `desktop/main.go`（托盘菜单构建改为可重建；把 Task 7 留的四个占位函数接到真实实现）

**Interfaces:**
- Consumes: `shell.SyncOutcome`（Task 7）、`shell.CheckLatest`（Task 8）、`openUpgradePanel`（Task 9）
- Produces: `func rebuildTray()` — 按当前状态重建托盘菜单。Task 11 依赖它挂「升级执行机」那条。

- [ ] **Step 1: 把托盘状态抽成包级变量并加重建函数**

`desktop/main.go` 里，托盘菜单当前是一次性构建的（`menu.Add("打开控制台")` 与 `menu.Add("退出（agentd 继续运行）")`）。改为：

```go
// trayState 是托盘菜单要展示的动态状态。
//
// 为什么用包级变量 + 重建而不是让菜单项自己刷新：Wails v3 的托盘菜单没有
// 「改一项的文案」这种接口，改动只能整体重建。状态集中在一处，重建函数才
// 能是幂等的。
var (
	trayMu      sync.Mutex
	traySync    shell.SyncOutcome // 最近一次对账的结果
	traySyncErr error             // 最近一次同步失败的原因，nil 表示没失败
	trayLatest  string            // 发现的新版 tag，空串表示没有
	trayApp     *application.App
	trayTray    *application.SystemTray
)

// rebuildTray 按当前 trayState 重建托盘菜单。
//
// 幂等：可以随时调，每次都从零构建整个菜单。加锁是因为它会被三个来源调用
//（启动序列、新版检查的 goroutine、用户点完强制同步之后），而 Wails 的
// 菜单对象不是并发安全的。
func rebuildTray() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayApp == nil || trayTray == nil {
		return
	}
	menu := trayApp.Menu.New()
	menu.Add("打开控制台").OnClick(func(*application.Context) { go openConsoleFn() })

	// 同步被闸一拦下：给强制入口，但藏在面板后面一层（spec D4）
	if traySync.Plan == shell.SyncBlocked {
		label := fmt.Sprintf("有更新待应用（%d 个任务进行中）", traySync.Busy)
		if traySync.Busy < 0 {
			// 探测失败时不谎报数字
			label = "有更新待应用（活跃任务数未知）"
		}
		menu.Add(label).OnClick(func(*application.Context) { go showBlockedPanel() })
	}
	if traySyncErr != nil {
		menu.Add("上次同步失败，查看详情").OnClick(func(*application.Context) { go showSyncFailurePanel() })
	}
	if trayLatest != "" {
		menu.Add(fmt.Sprintf("有新版 %s 可下载", trayLatest)).
			OnClick(func(*application.Context) { go openReleasePage(trayLatest) })
	}
	menu.Add("升级执行机…").OnClick(func(*application.Context) { go runRemoteUpgrade(false) })

	menu.Add("退出（agentd 继续运行）").OnClick(func(*application.Context) {
		logger.Info("用户从托盘退出薄壳；agentd 不受影响")
		trayApp.Quit()
	})
	trayTray.SetMenu(menu)
	logger.Info("托盘菜单已重建", "plan", traySync.Plan.String(),
		"sync_failed", traySyncErr != nil, "latest", trayLatest)
}
```

`app.Menu.New()`、`tray.SetMenu(menu)` 的实际方法名按 `main.go` 里既有的托盘构建代码对齐——**照抄那处**。

`openConsoleFn` 需要一点小心：`openConsole` 是 `main()` 里的**闭包**，捕获了 `app`、`win`、`runtimeReadyCh` 三个局部变量，**不能直接提成普通的包级函数**。正确做法是声明一个包级变量并在 `main()` 里赋值：

```go
// openConsoleFn 指向 main() 里那个 openConsole 闭包。
//
// 为什么要这个间接层：openConsole 捕获了 app/win/runtimeReadyCh 三个 main()
// 的局部变量，提不成包级函数；而 rebuildTray 与托盘回调都在包级作用域里，
// 引用不到闭包。用一个包级变量把它导出到包作用域是最小的接法。
var openConsoleFn func()
```

在 `main()` 里定义完闭包后紧接着 `openConsoleFn = openConsole`，并把托盘与 `ApplicationStarted` 里原来直接调 `openConsole` 的地方保持不变（它们在 `main()` 作用域内，直接调闭包即可）。`trayApp` / `trayTray` 同样在 `main()` 里赋值。

- [ ] **Step 2: 把 Task 7 留的占位函数接到真实实现**

```go
// noteSyncFailed 记录一次失败的同步并刷新托盘。
func noteSyncFailed(out shell.SyncOutcome) {
	trayMu.Lock()
	traySync, traySyncErr = out, out.Err
	trayMu.Unlock()
	rebuildTray()
}

// noteSyncBlocked 记录一次被闸一拦下的同步并刷新托盘。
func noteSyncBlocked(out shell.SyncOutcome) {
	trayMu.Lock()
	traySync, traySyncErr = out, nil
	trayMu.Unlock()
	rebuildTray()
}

// showBlockedPanel 打开面板，说明为什么没同步，并提供强制入口。
//
// 代价必须写准：执行者是 setsid 出去的独立进程，B59 V3 实测跨过 agentd
// 重启存活 16m29s，工单也在库里不丢。重启真正打断的是事件推送与在途请求，
// **不是任务本身**。写成「会中断任务」是吓唬用户，也不诚实。
func showBlockedPanel() {
	p := openUpgradePanel(trayApp)
	trayMu.Lock()
	busy := traySync.Busy
	trayMu.Unlock()
	p.State("fail", "有活跃任务，本次未同步")
	if busy >= 0 {
		p.Line(fmt.Sprintf("当前有 %d 个活跃任务（running / waiting_answer）。", busy))
	} else {
		p.Line("探测活跃任务数失败，按「有任务」保守处置。")
	}
	p.Line("")
	p.Line("强制同步会重启 agentd。实际代价：")
	p.Line("  - 执行者进程不受影响（它们是 setsid 出去的独立进程）")
	p.Line("  - 挂起的工单在库里，不会丢")
	p.Line("  - 中断的是事件推送与在途请求，agentd 起回来后自动恢复")
	p.OnForceRetry(func() { forceSyncNow(p) })
}

// showSyncFailurePanel 打开面板展示上次同步失败的原因。
func showSyncFailurePanel() {
	p := openUpgradePanel(trayApp)
	trayMu.Lock()
	err := traySyncErr
	trayMu.Unlock()
	p.State("fail", "上次同步失败")
	if err != nil {
		p.Line(err.Error())
	}
	p.Line("")
	p.Line("agentd 仍在用旧版本运行，控制台不受影响。")
	p.OnForceRetry(func() { forceSyncNow(p) })
}

// forceSyncNow 越过闸一立即同步。只由用户在面板上点击触发。
func forceSyncNow(p *upgradePanel) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p.State("running", "正在强制同步")
	ep, _, err := shell.Resolve("")
	if err != nil {
		p.State("fail", "读取配置失败")
		p.Line(err.Error())
		return
	}
	d := openSyncDeps(ep)
	target, err := d.InstalledPath()
	if err != nil {
		p.State("fail", "定位 handoff 失败")
		p.Line(err.Error())
		return
	}
	if err := shell.DoSync(ctx, target, true, d.Sync, func(s string) { p.Line(s) }); err != nil {
		p.State("fail", "同步失败")
		p.Line(err.Error())
		return
	}
	p.Line("正在等 agentd 重启完成…")
	if err := shell.WaitAgentdBack(ctx, d.EmbedVersion, d.Wait); err != nil {
		p.State("fail", "agentd 未按时回来")
		p.Line(err.Error())
		return
	}
	p.State("ok", "已同步到 "+d.EmbedVersion)
	trayMu.Lock()
	traySync, traySyncErr = shell.SyncOutcome{Plan: shell.SyncSkip}, nil
	trayMu.Unlock()
	rebuildTray()
}

// openReleasePage 打开 release 页面让用户自己下载。
//
// 不做自动下载替换：那要处理 .app 自我替换、Gatekeeper、DMG 挂载，是另一个
// 量级（spec §2 非目标）。
func openReleasePage(tag string) {
	url := "https://github.com/Xsxdot/handoff/releases/tag/" + tag
	logger.Info("打开 release 页面", "url", url)
	trayApp.Browser.OpenURL(url)
}
```

`trayApp.Browser.OpenURL` 的实际调用形式按 Wails v3.0.0-beta.8 的 API 写；若该版本上是 `application.OpenURL(url)` 或别的形式，用实际存在的那个（先 `grep -rn "OpenURL" desktop/node_modules 2>/dev/null` 或查 vendor 里的 wails 包）。**不要留一个编不过的调用。**

- [ ] **Step 3: 启动时跑一次新版检查**

在 `ApplicationStarted` 回调里，`go openConsole()` 之后追加：

```go
		// 新版检查独立于同步：它要出网、可失败，绝不能挡在打开控制台前面。
		// 单开 goroutine，结果到了再刷托盘
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			dataDir, err := config.DefaultDataDir()
			if err != nil {
				logger.Debug("取不到数据目录，跳过新版检查", "cause", err)
				return
			}
			tag, newer := shell.CheckLatest(ctx, dataDir, embedbin.Version, shell.LatestDeps{
				Fetch: func(ctx context.Context) (string, error) {
					rel, err := release.NewClient(nil).Latest(ctx)
					if err != nil {
						return "", err
					}
					return rel.Tag, nil
				},
			})
			if !newer {
				return
			}
			trayMu.Lock()
			trayLatest = tag
			trayMu.Unlock()
			rebuildTray()
		}()
```

`config.DefaultDataDir()` 与 `release.NewClient(nil)` 的实际签名按各自包里的定义写；`release.NewClient` 若要求 `*http.Transport` 参数，传 `proxyTransport` 的等价物或 `nil`（`cmd/upgrade.go:187` 的注释说明 nil 即标准库默认行为）。

- [ ] **Step 4: 构建并手验托盘**

Run: `cd desktop && go build ./... && go vet ./... && gofmt -l .`
Expected: 全绿

```bash
cd desktop && go run .
```

Expected: 托盘出现，菜单里至少有「打开控制台」「升级执行机…」「退出（agentd 继续运行）」三条。开发构建下不该出现「有更新待应用」（`EmbedAvailable` 为 false → `SyncNoEmbed`）。

- [ ] **Step 5: 日志与注释自查**

- 托盘重建有 Info，带三项状态 ✅
- 打开 release 页面有 Info 带 url ✅
- `forceSyncNow` 的每个失败分支都把原文推给面板**并且**已经由 `DoSync`/`WaitAgentdBack` 记了日志 ✅
- `rebuildTray` 的「为什么用包级变量 + 整体重建」有注释 ✅
- `showBlockedPanel` 里「实际代价」那段必须有注释说明为什么不写「会中断任务」 ✅

- [ ] **Step 6: Commit**

```bash
git add desktop/main.go
git commit -m "feat(desktop): 托盘条目——同步被拦、有新版、升级执行机

强制同步藏在面板后面一层（spec D4）：默认不发生，要两次主动点击才走得到。
面板上写的代价是实测过的——执行者是 setsid 出去的独立进程，B59 V3 实测
跨过 agentd 重启存活，中断的是事件推送与在途请求，不是任务本身。"
```

---

## Task 11: 升级执行机——exec 既有 CLI 并把输出流进面板

spec D5。**不重建编排**：七种结论、两道闸、部分失败不中断、逐行报告全在 CLI 里，壳只负责显示。

**Files:**
- Create: `desktop/remote_upgrade.go`
- Create: `desktop/remote_upgrade_test.go`

**Interfaces:**
- Consumes: `upgradePanel`（Task 9）、`shell.ResolveBinPath`（既有）、`hideConsole`（既有，`desktop/noconsole_*.go`）
- Produces: `func runRemoteUpgrade(force bool)` — Task 10 的托盘条目调它

- [ ] **Step 1: 写失败测试——流式与退出码**

Create `desktop/remote_upgrade_test.go`：

```go
package main

import (
	"context"
	"os/exec"
	"slices"
	"testing"
)

// TestStreamCommandDeliversEachLine 钉住输出是**逐行流式**送出的，不是攒到最后。
//
// 为什么承重：handoff upgrade --now 会逐台机器处理，一台可能要几十秒。攒到
// 最后再显示，用户会以为面板卡死了——而这正是他最想看到进度的时刻。
func TestStreamCommandDeliversEachLine(t *testing.T) {
	var got []string
	code, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo a; echo b; echo c"),
		func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("streamCommand 出错：%v", err)
	}
	if code != 0 {
		t.Errorf("退出码 = %d，想要 0", code)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Errorf("收到 %v，想要 %v", got, want)
	}
}

// TestStreamCommandReportsNonZeroExit 钉住非零退出码被如实带出来。
//
// 面板靠它决定亮不亮「带 --force 重试」——判据是退出码，不是解析输出文本
//（spec §7.2：解析中文表格是脆的，失效方式还是静默的）。
func TestStreamCommandReportsNonZeroExit(t *testing.T) {
	var got []string
	code, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo 失败了; exit 3"),
		func(line string) { got = append(got, line) })
	if err != nil {
		t.Fatalf("非零退出不该作为 error 返回（那样调用方分不清「跑不起来」和「跑了但失败」）：%v", err)
	}
	if code != 3 {
		t.Errorf("退出码 = %d，想要 3", code)
	}
	if !slices.Contains(got, "失败了") {
		t.Errorf("非零退出时输出也必须送到：%v", got)
	}
}

// TestStreamCommandMergesStderr 钉住 stderr 也进面板。
//
// handoff 的更新提示、警告都走 stderr（cmd/root.go 的 maybeNotifyUpdate 就是
// 一例）。只收 stdout 会让面板漏掉恰恰最需要看见的那几行。
func TestStreamCommandMergesStderr(t *testing.T) {
	var got []string
	if _, err := streamCommand(context.Background(),
		exec.Command("sh", "-c", "echo out; echo err 1>&2"),
		func(line string) { got = append(got, line) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "err") {
		t.Errorf("stderr 没被收进来：%v", got)
	}
}

// TestStreamCommandFailsWhenBinaryMissing 钉住「跑不起来」返回 error。
func TestStreamCommandFailsWhenBinaryMissing(t *testing.T) {
	_, err := streamCommand(context.Background(),
		exec.Command("这个命令肯定不存在-handoff-test"), func(string) {})
	if err == nil {
		t.Fatal("命令不存在时必须返回 error")
	}
}
```

`sh -c` 在 Windows 上不可用——本测试文件顶部加 `//go:build !windows` 并在注释里说明：验的是 `streamCommand` 的流式与退出码语义，与平台无关，Windows 侧由 Task 12 的真机走查覆盖。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd desktop && go test . -run TestStreamCommand -v`
Expected: 编译失败，`undefined: streamCommand`

- [ ] **Step 3: 实现**

Create `desktop/remote_upgrade.go`：

```go
// 本文件负责「从桌面端升级执行机」（spec D5）。
//
// 职责：
//   - streamCommand 跑一条命令并把 stdout+stderr 逐行回调出去
//   - runRemoteUpgrade 用它跑 handoff upgrade --now 并把输出流进升级面板
//
// 边界（承重）：
//   - **不重建 upgrade 的编排。** 七种结论、两道闸、部分失败不中断、逐行报告
//     全在 CLI 里（cmd/upgrade.go）。这里只负责起进程和显示，多写一行判断
//     逻辑就是在造第二套会与 CLI 分叉的实现
//   - **不解析输出内容。** 是不是闸一导致的失败交给用户自己看——输出是给人看
//     的中文表格，解析它会在格式一改时静默失效（spec §7.2）
//   - 不调起真实终端：GUI 进程的 PATH 与登录 shell 不同（B71 同源教训），
//     Windows 上还要回答「哪个终端」，且失败时用户被丢在 shell 里没有指引
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// remoteUpgradeTimeout 是整条升级的上限。
//
// 10 分钟：upgrade --now 要逐台机器下载并推送资产，机器多、网络慢时确实要
// 这么久。设短了会在最后一台上被砍断，而那台的状态是「推了一半」。
const remoteUpgradeTimeout = 10 * time.Minute

// streamCommand 跑一条命令，把 stdout 与 stderr 合并后逐行送给 onLine。
//
// 返回：
//   - 退出码。**命令跑起来了但退出非零时 error 为 nil** —— 调用方必须能分清
//     「跑不起来」（error 非 nil）和「跑了但失败」（error 为 nil、code 非 0），
//     这两种的处置完全不同
//   - error 只在「起不来」「读不了输出」这类情况下非 nil
//
// 注意：
//   - stderr 必须合并进来。handoff 的警告与更新提示都走 stderr，只收 stdout
//     会漏掉恰恰最该看见的那几行
//   - 逐行送出而不是攒到最后：单台机器可能要几十秒，攒着显示会让用户以为卡死
func streamCommand(ctx context.Context, c *exec.Cmd, onLine func(string)) (int, error) {
	// 薄壳是 GUI 进程：不压这一下，Windows 上每跑一条命令都会闪黑窗口
	hideConsole(c)
	pipe, err := c.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("接管 stdout: %w", err)
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		return -1, fmt.Errorf("启动 %s: %w", c.Path, err)
	}
	sc := bufio.NewScanner(pipe)
	// upgrade 的表格行不长，但错误原文可能很长（含远端 fetch 的 stderr 原样回显）
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	scanErr := sc.Err()
	waitErr := c.Wait()
	if scanErr != nil {
		return -1, fmt.Errorf("读取输出: %w", scanErr)
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		// 跑了但失败：不是 error，退出码才是结论
		return ee.ExitCode(), nil
	}
	if waitErr != nil {
		return -1, fmt.Errorf("等待命令结束: %w", waitErr)
	}
	return 0, nil
}

// runRemoteUpgrade 跑 handoff upgrade --now 并把输出流进升级面板。
//
// 参数：
//   - force: 带上 --force 越过闸一（活跃任务）。**不越过闸二**（非托管），
//     那是 agentd 侧的硬拒绝
//
// 注意：exec 的是 ResolveBinPath 解出来的那份——也就是刚刚被同步过的、
// 版本已知的那一份，不是 PATH 上碰运气找到的。
func runRemoteUpgrade(force bool) {
	p := openUpgradePanel(trayApp)
	p.State("running", "正在升级所有机器")
	p.OnForceRetry(func() { runRemoteUpgrade(true) })

	bin, err := shell.ResolveBinPath("")
	if err != nil {
		logger.Error("定位 handoff 失败，无法升级执行机", "cause", err)
		p.State("fail", "定位 handoff 失败")
		p.Line(err.Error())
		return
	}
	args := []string{"upgrade", "--now"}
	if force {
		args = append(args, "--force")
	}
	logger.Info("开始升级执行机", "bin", bin, "args", args)
	p.Line("$ " + bin + " " + fmt.Sprint(args))

	ctx, cancel := context.WithTimeout(context.Background(), remoteUpgradeTimeout)
	defer cancel()
	code, err := streamCommand(ctx, exec.CommandContext(ctx, bin, args...), p.Line)
	switch {
	case err != nil:
		logger.Error("升级执行机：命令起不来", "bin", bin, "cause", err)
		p.State("fail", "命令无法启动")
		p.Line(err.Error())
	case code != 0:
		logger.Warn("升级执行机：有机器没升成", "exit_code", code)
		p.State("fail", fmt.Sprintf("有机器没升成（退出码 %d）", code))
	default:
		logger.Info("升级执行机完成", "force", force)
		p.State("ok", "所有机器已是最新")
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test . -run TestStreamCommand -v`
Expected: 四条全 PASS

- [ ] **Step 5: 变异复验**

1. 把 `c.Stderr = c.Stdout` 删掉 → 期望 `TestStreamCommandMergesStderr` 红
2. 把 `errors.As(waitErr, &ee)` 那支改成 `return -1, waitErr` → 期望 `TestStreamCommandReportsNonZeroExit` 红
3. 把逐行回调改成先攒进切片、循环结束后一次性回调 → `TestStreamCommandDeliversEachLine` 仍会绿（它只验内容不验时机）。**这说明该测试测不出流式**——补一条：在 `onLine` 里断言「收到第一行时命令还没退出」，用一个 `sh -c 'echo a; sleep 1; echo b'` 并记录首行到达的时刻。改完再验一次变异必须红

- [ ] **Step 6: 手验一次真实调用**

```bash
cd desktop && go run .
```

托盘点「升级执行机…」。Expected: 面板弹出，逐行出现 `handoff upgrade` 的巡检表；本机若已是最新会显示「所有机器已是最新」。

**若本机 agentd 非托管**，CLI 会报「agentd 非托管启动」并退出非零——此时面板应显示失败且亮出「带 --force 重试」。**点它验证一次**：预期仍然失败（`--force` 不越过闸二），且输出里明说了原因。这正是 §7.2 那个已知代价的实例，确认它至少不误导用户。

- [ ] **Step 7: 日志与注释自查**

- 开始升级有 Info（带 bin 与 args）✅
- 三种结局（起不来 / 非零 / 成功）各有日志且级别不同 ✅
- 面板上第一行回显了实际执行的命令 ✅ —— 用户能自己复制去终端重跑，这是最有用的一行
- 文件头三条边界（不重建编排、不解析输出、不调终端）都写了「为什么」✅
- `streamCommand` 的 doc comment 说清了「跑不起来」与「跑了但失败」的区分 ✅

- [ ] **Step 8: Commit**

```bash
git add desktop/remote_upgrade.go desktop/remote_upgrade_test.go
git commit -m "feat(desktop): 升级执行机——exec 既有 CLI 并把输出流进面板

不重建编排：七种结论、两道闸、部分失败不中断全在 cmd/upgrade.go 里。
streamCommand 区分「跑不起来」（error）与「跑了但失败」（退出码），
两者处置完全不同；stderr 必须合并——handoff 的警告都走那儿。"
```

---

## Task 12: 走查清单

spec §10。单测验不了的六项，加上本次改动带来的新失败模式。

**Files:**
- Modify: `docs/windows-desktop-acceptance.md`
- Create: `docs/macos-desktop-acceptance.md`

**Interfaces:**
- Consumes: 前十一个 task 的全部产出
- Produces: 两份可照着做的走查清单

- [ ] **Step 1: 写 macOS 清单**

Create `docs/macos-desktop-acceptance.md`，内容按下面的骨架写实（每一项都要给出**具体命令**与**判否则说明什么**，不要只写「验证 X 正常」）：

```markdown
# macOS 桌面端走查清单

## 走查前必须先确认

- [ ] 装的是 **release 构建**（带 `-tags embedbin`）。开发构建下 `PlanSync`
      恒为 `SyncNoEmbed`，整条同步路都不会跑——照着走查会全部"通过"而什么都没验
      判据：`log show` 或壳的日志里 `同步对账结果` 那行的 `plan` 字段
- [ ] `.app` 的版本号不是 `0.1.0`（Task 4 之后应为真实 tag）
      判据：`mdls -name kMDItemVersion /Applications/handoff-desktop.app`

## P1 换版后 launchd 真拉起新版

...（每项给命令与判据）

## P3 Gatekeeper 不拦释出的二进制

见 Task 1 的九个步骤，此处只记结论与复验方法。

## P4 有活跃任务时不同步

## P6 同步失败时控制台仍能打开

用只读的落点制造一次必然失败：`chmod 555` 掉 `~/.local/bin` 后打开应用。
判否则说明 D8 被破——这是本次改动最严重的回归。
```

- [ ] **Step 2: 补 Windows 清单**

`docs/windows-desktop-acceptance.md` 追加三节：

- **P2 换版后 schtasks 真拉起新版，且主动催缩短了等待**。判据：壳日志里 `已催进程管理器拉起 agentd` 与随后的 `agentd 已带新版本回来`，两条之间的 `attempts` 应远小于 120（120 = 60 秒 ÷ 500ms，即完全靠重复触发的情形）
- **P5 升级面板窗口能出来**。判否则是 rc7 那个 bug 的第二次——症状是进程静默消失，无任何输出
- **强制同步入口**：托盘 →「有更新待应用」→ 面板 →「带 --force 重试」，四步都要能走到

追加到既有的「走查前必须先确认」那节：

- [ ] 中文串不要用 `grep` 匹配 ssh 回来的输出（会被通道改写，永不匹配）；判据用退出码或英文/数字字段

- [ ] **Step 3: 把已知代价写进清单，避免走查者当成 bug 上报**

两份清单都加一节「已知且刻意如此」：

```markdown
## 已知且刻意如此

- 「带 --force 重试」在**非闸一原因**的失败上点了也没用（如网络不通）。
  按钮只按退出码非零就亮，不解析输出——解析中文表格会在格式一改时静默失效
  （spec §7.2）。这不是 bug
- agentd 被锁在你手上这份 `.app` 的版本。要升到更新的版本必须换 `.app`，
  托盘的「有新版可下载」就是提醒你去换（spec §11 风险③）
- 开发构建（未带 `-tags embedbin`）下同步整条路不跑，日志里 `plan=no-embed`
```

- [ ] **Step 4: Commit**

```bash
git add docs/windows-desktop-acceptance.md docs/macos-desktop-acceptance.md
git commit -m "docs: 桌面端同步与升级的两平台走查清单

每项给命令与「判否则说明什么」。单列「已知且刻意如此」一节——强制重试
按钮对非闸一失败无效、agentd 锁在当前 .app 版本，都是设计取舍不是 bug，
不写清楚会被当成缺陷反复上报。"
```

---

## 收尾

全部 task 完成后：

- [ ] 相对分支起点跑一次整分支终审：`git diff <起点>..HEAD`
- [ ] 全量门：`go build ./... && go vet ./... && go test ./... && gofmt -l . internal cmd` 与 `cd desktop && go build ./... && go vet ./... && go test ./... && gofmt -l .`
- [ ] `docs/superpowers/backlog.md` 记一行本条需求并按证据门填「验收」列
- [ ] **P3 若在 Task 1 判否**，整个 plan 作废，回 brainstorming 重新设计落点
