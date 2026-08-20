# 合并环节的 origin 拓扑与工作区隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让合并环节真的能跑通——把工作分支补齐到 origin、合并在临时 worktree 里做（不碰协调者工作区）、外部推送动作显式落账，并修掉派发时基线分支名不补拉的问题。

**Architecture:** 两个真机执行体（`NewLocalObjective` / `NewLocalMerge`）共用一段「把工作分支补齐到 origin」的脚本片段；合并改为 `git worktree add --detach origin/<基线>` + `git push origin HEAD:<基线>`，全程不 checkout 主工作区；工作分支在本地与 origin 都拿不到时以哨兵错误上抛，由 `MergeNode` 转「等人」并给出可操作的 reason。

**Tech Stack:** Go（`os/exec` 跑 bash 脚本、`errors.Is` 哨兵错误）、React + TypeScript（timeline 事件渲染）。

**上游 spec:** `docs/superpowers/specs/2026-08-20-merge-step-origin-topology-design.md`

## Global Constraints

- **日志用 `slog`，禁止 `fmt.Printf`**；web 侧禁止 `console.log`。
- 新建文件写文件头注释（职责 + 边界）；导出符号写 doc 注释；非显然分支写中文「为什么」注释。
- **gofmt 必须干净**：`gofmt -l . | grep -v '^web/'` 应无输出。
- 每个 task 独立提交，中文提交信息，`type(scope): 说明` 格式。
- **绝不使用 `git push --force`**（spec §3.1）。所有推送用显式 refspec，不依赖当前分支或 upstream 配置。
- 临时目录一律 `mktemp -d`（系统临时目录），**不得建在仓库内**——仓库内的临时目录会破坏 git 相关测试的前提。
- **不改动已落盘的事件 payload 键名**（见 Task 6 的说明），不改 `internal/discipline/`、不改 `internal/permgate/`。
- 本 plan 不做 A 组两条、不做存量切换、不合 main。

### 对 spec §3.6 的一处修正（实现前必读）

spec 说改名的风险点是 `node:` actor 前缀，**这是错的**。实测：

- `actor` 只被渲染、从不被解析（`web/src/app/cards/CardDrawer.tsx:338` 只是显示它），改前缀是纯展示行为。
- 真正被解析的是**事件 payload 的键名**：`CountRounds`（`internal/ledgernode/rounds.go:28`）读 `payload["node"]` 与 `payload["human_reset_node"]`，这两个键已经落在历史事件里。

因此 Task 6 的规则是：**持久化键名一律不动**（继续叫 `node` / `human_reset_node`），只改 Go 标识符、包名、CLI flag 与界面文案。这样根本不需要「同时认两种键」的兼容分支。

---

### Task 1: 工作分支补齐脚本片段与哨兵错误

**Files:**
- Create: `internal/ledgernode/gitscript.go`
- Test: `internal/ledgernode/gitscript_test.go`

**Interfaces:**
- Consumes: 同包既有的 `shellQuote(value string) string`（`wire.go:212`，用单引号包裹并转义内部单引号）
- Produces:
  - `var ErrWorkBranchMissing error` —— 哨兵错误，Task 3 用 `errors.Is` 判它
  - `const workBranchMissingMarker = "HANDOFF_WORK_BRANCH_MISSING"`
  - `func syncWorkBranchScript(branch string) string` —— 返回多行 bash 片段
  - `func classifyScriptError(out []byte, err error, action string) error` —— 把脚本失败翻成 Go 错误，命中 marker 时包装 `ErrWorkBranchMissing`

- [ ] **Step 1: 写失败的测试**

创建 `internal/ledgernode/gitscript_test.go`：

```go
package ledgernode

import (
	"errors"
	"strings"
	"testing"
)

// TestSyncWorkBranchScriptLadder 阶梯三条腿都要在脚本里（spec §3.3）：
// 本地有就推、本地没有就试 fetch、都没有就打 marker 退 3。
func TestSyncWorkBranchScriptLadder(t *testing.T) {
	script := syncWorkBranchScript("feat/x")
	for _, want := range []string{
		"git rev-parse --verify --quiet 'refs/heads/feat/x'",
		"git push origin 'feat/x':'feat/x'",
		"git fetch origin 'feat/x'",
		workBranchMissingMarker,
		"exit 3",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("脚本缺 %q：\n%s", want, script)
		}
	}
	// 绝不强推：这条是安全红线，写成断言防止以后有人「顺手加个 --force 就好了」
	if strings.Contains(script, "--force") || strings.Contains(script, " -f ") {
		t.Fatalf("脚本不得包含强推：\n%s", script)
	}
}

// TestSyncWorkBranchScriptQuotesBranch 分支名带单引号也不能破坏脚本。
func TestSyncWorkBranchScriptQuotesBranch(t *testing.T) {
	script := syncWorkBranchScript("weird'name")
	if !strings.Contains(script, `'weird'"'"'name'`) {
		t.Fatalf("分支名未被正确转义：\n%s", script)
	}
}

// TestClassifyScriptErrorMarker 命中 marker 时必须能被 errors.Is 认出来，
// 否则 MergeNode 会把「工作分支缺失」误报成「合并冲突」，人看到的原因是错的。
func TestClassifyScriptErrorMarker(t *testing.T) {
	out := []byte("something\n" + workBranchMissingMarker + "\n")
	err := classifyScriptError(out, errors.New("exit status 3"), "合并")
	if !errors.Is(err, ErrWorkBranchMissing) {
		t.Fatalf("应能识别为工作分支缺失，实得: %v", err)
	}
}

// TestClassifyScriptErrorPlain 普通失败保留原始输出，不许吞。
func TestClassifyScriptErrorPlain(t *testing.T) {
	out := []byte("CONFLICT (content): foo.go\n")
	err := classifyScriptError(out, errors.New("exit status 1"), "合并")
	if errors.Is(err, ErrWorkBranchMissing) {
		t.Fatalf("普通失败不该被认成工作分支缺失: %v", err)
	}
	if !strings.Contains(err.Error(), "CONFLICT (content): foo.go") {
		t.Fatalf("错误里必须带脚本原始输出: %v", err)
	}
}

// TestClassifyScriptErrorNil err 为 nil 时不造错误。
func TestClassifyScriptErrorNil(t *testing.T) {
	if err := classifyScriptError([]byte("ok"), nil, "合并"); err != nil {
		t.Fatalf("成功时应返回 nil，实得: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledgernode/ -run 'TestSyncWorkBranchScript|TestClassifyScriptError' -count=1`
Expected: FAIL —— `undefined: syncWorkBranchScript`（以及其余三个符号未定义）

- [ ] **Step 3: 实现**

创建 `internal/ledgernode/gitscript.go`：

```go
// 真机 git 脚本的公共片段与失败归类。
//
// 职责：拼「把工作分支补齐到 origin」这段阶梯脚本，并把脚本的失败翻成
// 调用方能分辨的 Go 错误。
// 边界：只生成脚本文本与翻译错误，不执行任何命令——执行在 wire.go 的两个
// 注入点里，那里才有 repoDir 与 ctx。
package ledgernode

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// ErrWorkBranchMissing 工作分支在协调者本地与 origin 上都不存在。
//
// 为什么要单独一个哨兵：MergeNode 把 DoMerge 的任何错误都记成「合并冲突」，
// 而这一条根本没走到合并——人看到「合并冲突」会去查代码冲突，白费一轮。
var ErrWorkBranchMissing = errors.New("工作分支在本地与 origin 都不存在")

// workBranchMissingMarker 脚本用它向 Go 侧报告「阶梯走到底了」。
// 用带前缀的哨兵串而不是只认退出码：脚本里任何一条 git 命令都可能退 3。
const workBranchMissingMarker = "HANDOFF_WORK_BRANCH_MISSING"

// syncWorkBranchScript 生成「把工作分支补齐到 origin」的 bash 片段（spec §3.3 阶梯）。
//
// 参数：branch 工作分支名（原样传入，内部做 shell 转义）。
// 返回：多行脚本文本，供调用方拼进完整脚本。
//
// 阶梯三条腿，缺一条都会退化成含糊失败：
//  1. 本地有该分支 → 推上 origin（常态：wait 的 sync.auto 已经 fetch 过）
//  2. 本地没有 → 试着从 origin 拉（可能别的协调机已经推过了）
//  3. 都没有 → 打 marker 退 3，由 classifyScriptError 翻成 ErrWorkBranchMissing
//
// 推送用显式 refspec `<分支>:<分支>`，不依赖当前分支或 upstream 配置——
// upstream 名字对不上时裸 push 什么都不做**且不报错**，那是最难查的一类失败。
func syncWorkBranchScript(branch string) string {
	ref := shellQuote("refs/heads/" + branch)
	name := shellQuote(branch)
	return strings.Join([]string{
		"if git rev-parse --verify --quiet " + ref + " >/dev/null; then",
		"  git push origin " + name + ":" + name,
		"elif git fetch origin " + name + "; then",
		"  :",
		"else",
		"  echo " + shellQuote(workBranchMissingMarker),
		"  exit 3",
		"fi",
	}, "\n")
}

// classifyScriptError 把脚本执行结果翻成 Go 错误。
//
// 参数：out 合并后的 stdout+stderr；err exec 的返回；action 用于错误文案的动作名
// （如「合并」「客观判据」）。err 为 nil 时返回 nil。
//
// 注意：命中 marker 时包装 ErrWorkBranchMissing 供 errors.Is 判定，同时**保留
// 脚本原始输出**——取证文化要求错误里始终看得到远端说了什么。
func classifyScriptError(out []byte, err error, action string) error {
	if err == nil {
		return nil
	}
	if bytes.Contains(out, []byte(workBranchMissingMarker)) {
		return fmt.Errorf("%w：\n%s", ErrWorkBranchMissing, out)
	}
	return fmt.Errorf("%s失败:\n%s", action, out)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledgernode/ -run 'TestSyncWorkBranchScript|TestClassifyScriptError' -count=1`
Expected: PASS（五条全过）

- [ ] **Step 5: 加日志与注释（本 task 的门）**

本文件是纯函数（不执行命令、无 I/O、无状态变更），按 `instrumenting-code` 的边界**不需要日志**——日志加在 Task 2 的执行点上。确认注释已具备：文件头职责与边界、`ErrWorkBranchMissing` 的「为什么要单独一个哨兵」、`syncWorkBranchScript` 的阶梯三条腿与「为什么用显式 refspec」、`classifyScriptError` 的「为什么保留原始输出」。

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/ledgernode/gitscript.go internal/ledgernode/gitscript_test.go
git commit -m "feat(ledgerstep): 加工作分支补齐脚本片段与缺失哨兵"
```

---

### Task 2: 两个执行体接入——D1 补推送、D3 临时 worktree、D4 显式推基线

**Files:**
- Modify: `internal/ledgernode/wire.go:154-183`（`NewLocalObjective`）、`internal/ledgernode/wire.go:185-210`（`NewLocalMerge`）
- Test: `internal/ledgernode/wire_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: Task 1 的 `syncWorkBranchScript(branch string) string`、`classifyScriptError(out []byte, err error, action string) error`、`ErrWorkBranchMissing`；同包既有 `taskBranch(st *ledger.Store, card ledger.Card) (string, error)`、`shellQuote`
- Produces: 两个注入点的函数签名**不变**（`func(ctx context.Context, card ledger.Card, base string) error`），Task 3 依赖 `NewLocalMerge` 返回的错误可被 `errors.Is(err, ErrWorkBranchMissing)` 判定

- [ ] **Step 1: 写失败的测试**

在 `internal/ledgernode/wire_test.go` 追加。**注意**：这两个函数返回闭包且真的跑 git，单测只验脚本文本，所以先把脚本拼装抽成可测的纯函数——这一步在 Step 3 一并做：

```go
// TestMergeScriptUsesDetachedWorktree D3：合并不得 checkout 主工作区。
// 落点必须是 origin/<基线> 且带 --detach——两条缺一：
//   - 不 detach：主工作区正停在基线分支上时，git 拒绝同一分支被两处 checkout
//   - 用本地基线名：本地那份可能陈旧，合并起点就错了
func TestMergeScriptUsesDetachedWorktree(t *testing.T) {
	script := mergeScript("feat/x", "integration/y")
	if strings.Contains(script, "git checkout") {
		t.Fatalf("合并脚本不得 checkout 主工作区：\n%s", script)
	}
	if !strings.Contains(script, "git worktree add --detach") {
		t.Fatalf("必须用 --detach 建临时 worktree：\n%s", script)
	}
	if !strings.Contains(script, "'origin/integration/y'") {
		t.Fatalf("临时 worktree 落点必须是 origin/<基线>：\n%s", script)
	}
	if !strings.Contains(script, `trap 'git worktree remove --force "$tmp"' EXIT`) {
		t.Fatalf("必须有 trap 清理，否则失败路径会留残骸：\n%s", script)
	}
}

// TestMergeScriptPushesBothRefs D1 + D4：先补工作分支，最后推基线。
func TestMergeScriptPushesBothRefs(t *testing.T) {
	script := mergeScript("feat/x", "integration/y")
	if !strings.Contains(script, "git push origin 'feat/x':'feat/x'") {
		t.Fatalf("缺工作分支补齐（D1）：\n%s", script)
	}
	if !strings.Contains(script, "git push origin HEAD:'integration/y'") {
		t.Fatalf("缺基线推送（D4），且必须用 HEAD: 显式 refspec：\n%s", script)
	}
	if strings.Contains(script, "--force") {
		t.Fatalf("不得强推：\n%s", script)
	}
}

// TestObjectiveScriptSyncsWorkBranch D1 的另一半：客观判据同样从 origin 取
// 工作分支，同样要先补齐，否则它比合并更早撞 couldn't find remote ref。
func TestObjectiveScriptSyncsWorkBranch(t *testing.T) {
	script := objectiveScript("feat/x", "integration/y")
	if !strings.Contains(script, "git push origin 'feat/x':'feat/x'") {
		t.Fatalf("客观判据脚本缺工作分支补齐：\n%s", script)
	}
	if !strings.Contains(script, workBranchMissingMarker) {
		t.Fatalf("客观判据脚本缺缺失阶梯：\n%s", script)
	}
}
```

若 `wire_test.go` 尚未 import `strings`，补上。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledgernode/ -run 'TestMergeScript|TestObjectiveScript' -count=1`
Expected: FAIL —— `undefined: mergeScript` / `undefined: objectiveScript`

- [ ] **Step 3: 抽出脚本拼装并改写两个执行体**

在 `wire.go` 里新增两个纯函数（放在 `NewLocalObjective` 之前）：

```go
// objectiveScript 客观判据脚本：补齐工作分支 → fetch → 在临时 worktree 里
// 跑 gofmt 与测试。临时 worktree 的落点是 origin/<工作分支>，天然脱头。
func objectiveScript(branch, base string) string {
	return strings.Join([]string{
		"set -e",
		syncWorkBranchScript(branch),
		"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
		"tmp=$(mktemp -d)",
		"git worktree add \"$tmp\" " + shellQuote("origin/"+branch),
		`trap 'git worktree remove --force "$tmp"' EXIT`,
		"cd \"$tmp\"",
		"test -z \"$(gofmt -l .)\"",
		"go test ./...",
	}, "\n")
}

// mergeScript 合并脚本：补齐工作分支 → fetch → 在**脱头**的临时 worktree 里
// 合并 → 推基线。
//
// 为什么落点是 origin/<基线> 而不是本地基线分支名（两条原因，缺一都会踩）：
//  1. git 不允许同一分支同时在两个 worktree 里被 checkout——协调者主工作区
//     恰好停在基线分支上时（合并完想看结果，很常见），worktree add 会直接失败
//  2. 用刚 fetch 的 origin/<基线> 作落点，顺带消灭「本地基线陈旧」这个变量
//
// 随之而来的行为：协调者本地的基线分支引用**不再被推进**，新合并提交只落
// origin。这是「origin 为权威」的直接推论，也免去一份会漂移的影子引用。
func mergeScript(branch, base string) string {
	return strings.Join([]string{
		"set -e",
		syncWorkBranchScript(branch),
		"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
		"tmp=$(mktemp -d)",
		"git worktree add --detach \"$tmp\" " + shellQuote("origin/"+base),
		`trap 'git worktree remove --force "$tmp"' EXIT`,
		"cd \"$tmp\"",
		"git merge --no-ff " + shellQuote("origin/"+branch) +
			" || { git diff --name-only --diff-filter=U; git merge --abort; exit 1; }",
		"git push origin HEAD:" + shellQuote(base),
	}, "\n")
}
```

然后把 `NewLocalObjective` 的函数体里那段 `script := strings.Join([...])` 整体换成 `script := objectiveScript(branch, base)`，把结尾的错误处理换成 `classifyScriptError`，并补日志：

```go
		logger := slog.Default().With("step", "objective", "card", card.ID, "branch", branch, "base", base)
		logger.Info("运行客观判据")
		cmd := exec.CommandContext(ctx, "bash", "-c", objectiveScript(branch, base))
		cmd.Dir = repoDir
		out, runErr := cmd.CombinedOutput()
		if cerr := classifyScriptError(out, runErr, "客观判据"); cerr != nil {
			logger.Error("客观判据失败", "err", cerr)
			return cerr
		}
		logger.Info("客观判据通过")
		return nil
```

`NewLocalMerge` 同形状，用 `mergeScript` 与 `"合并"`，日志 `"step", "merge"`，成功日志写 `logger.Info("合并完成并已推 origin", "pushed_base", base)`。

若 `wire.go` 尚未 import `log/slog`，补上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledgernode/ -run 'TestMergeScript|TestObjectiveScript' -count=1`
Expected: PASS（三条全过）

- [ ] **Step 5: 跑本包全量**

Run: `go test ./internal/ledgernode/ -count=1 && go build ./...`
Expected: PASS + 编译通过

- [ ] **Step 6: 加日志与注释（复核）**

确认已具备：两个执行点各有**进入 Info**（带 card/branch/base）、**错误 Error 带 cause**、**成功 Info**（合并那条要带 `pushed_base`，这是 D4 显式化的日志侧）；`mergeScript` 有「为什么 detach + 为什么用 origin/<基线>」的 why 注释与行为变化说明。

- [ ] **Step 7: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/ledgernode/wire.go internal/ledgernode/wire_test.go
git commit -m "fix(ledgerstep): 合并改用脱头临时 worktree，两处执行体补齐 origin 工作分支"
```

---

### Task 3: 合并环节区分「工作分支缺失」与「合并冲突」

**Files:**
- Modify: `internal/ledgernode/node.go:169-180`（`MergeNode.RunOnce` 的 `DoMerge` 错误分支）
- Test: `internal/ledgernode/node_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: Task 1 的 `ErrWorkBranchMissing`；既有 `MergeNode` 结构与 `Outcome{Action, Reason}`
- Produces: `DoMerge` 返回的错误命中 `ErrWorkBranchMissing` 时，`Outcome.Reason` 为 `工作分支缺失：先 handoff pull 再重试`（而不是 `合并冲突`）

- [ ] **Step 1: 写失败的测试**

在 `internal/ledgernode/node_test.go` 追加（`newTestStore` 之类的既有辅助按同文件写法复用，不要新造）：

```go
// TestMergeNodeDistinguishesMissingWorkBranch 工作分支缺失不能被记成「合并冲突」。
// 人看到「合并冲突」会去查代码冲突，而真实处置是 handoff pull——原因写错等于
// 把人引到错误的排查路径上。
func TestMergeNodeDistinguishesMissingWorkBranch(t *testing.T) {
	st := newTestStore(t)
	card := seedMergeableCard(t, st)
	node := &MergeNode{
		St:        st,
		Objective: func(context.Context, ledger.Card, string) error { return nil },
		DoMerge: func(context.Context, ledger.Card, string) error {
			return fmt.Errorf("%w：\n（脚本输出）", ErrWorkBranchMissing)
		},
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce 不该整体报错: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("应转等人，实得 %q", out.Action)
	}
	if !strings.Contains(out.Reason, "工作分支缺失") {
		t.Fatalf("reason 应指明工作分支缺失，实得 %q", out.Reason)
	}
	if !strings.Contains(out.Reason, "handoff pull") {
		t.Fatalf("reason 必须给出可操作的下一步，实得 %q", out.Reason)
	}
}

// TestMergeNodeConflictStillSaysConflict 普通合并失败仍记「合并冲突」，
// 不能被上一条改动带偏。
func TestMergeNodeConflictStillSaysConflict(t *testing.T) {
	st := newTestStore(t)
	card := seedMergeableCard(t, st)
	node := &MergeNode{
		St:        st,
		Objective: func(context.Context, ledger.Card, string) error { return nil },
		DoMerge: func(context.Context, ledger.Card, string) error {
			return fmt.Errorf("合并失败:\nCONFLICT (content): foo.go")
		},
	}
	out, err := node.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce 不该整体报错: %v", err)
	}
	if out.Reason != "合并冲突" {
		t.Fatalf("普通失败应记合并冲突，实得 %q", out.Reason)
	}
}
```

**`seedMergeableCard` 是本 task 要写的测试辅助**，放在同一文件里。复用该文件既有的
`nodeLedger(t) (*ledger.Store, ledger.Card)`（`node_test.go:12`，它开临时 SQLite、
seed 默认工作流与模板）拿 store，再建一张**基线非主线**的卡——只有这样
`RunOnce` 才走得过 `isMainline` 那道门：

```go
// seedMergeableCard 建一张基线是集成分支的卡。基线非主线是唯一的关键点：
// 基线为空或为 main 时 isMainline 会直接把卡推去「待合并」等人，根本走不到
// Objective/DoMerge，本 task 要验的分支就摸不到。
func seedMergeableCard(t *testing.T, s *ledger.Store) ledger.Card {
	t.Helper()
	c, err := s.CreateCard(ledger.NewCard{
		Title: "待合并卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration/y", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return c
}
```

两条用例里的 `st := newTestStore(t)` 相应改成 `st, _ := nodeLedger(t)`
（`nodeLedger` 返回的第二个值是它自建的「被审卡」，本 task 用不上，丢弃）。
`MergeNode.RunOnce` 对卡的**状态**没有门禁，所以不需要 `MoveCard` 铺路。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledgernode/ -run 'TestMergeNodeDistinguishes|TestMergeNodeConflictStill' -count=1`
Expected: FAIL —— 第一条报 `reason 应指明工作分支缺失，实得 "合并冲突"`

- [ ] **Step 3: 实现**

`internal/ledgernode/node.go`，把 `DoMerge` 的错误分支改成：

```go
	if err := m.DoMerge(ctx, card, base); err != nil {
		// 工作分支根本没拿到时，压根没走到合并——记成「合并冲突」会把人
		// 引去查代码冲突，而真实处置是 handoff pull。原因必须可操作。
		reason := "合并冲突"
		if errors.Is(err, ErrWorkBranchMissing) {
			reason = "工作分支缺失：先 handoff pull 再重试"
		}
		logger.Info("合并执行失败转等人", "reason", reason, "err", err)
		if _, commentErr := m.St.AddComment(cardID, reason+"：\n"+err.Error(), "普通", "node:merge"); commentErr != nil {
			return Outcome{}, commentErr
		}
		if err := m.St.MarkNeedsHuman(cardID, reason, "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
```

若 `node.go` 尚未 import `errors`，补上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledgernode/ -count=1`
Expected: PASS（本包全绿）

- [ ] **Step 5: 加日志与注释（复核）**

确认：失败分支的 Info 日志带 `reason` 与 cause；分支上有「为什么要分辨」的 why 注释。

- [ ] **Step 6: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/ledgernode/node.go internal/ledgernode/node_test.go
git commit -m "fix(ledgerstep): 工作分支缺失不再误报为合并冲突"
```

---

### Task 4: 合并成功落 `branch_merged` 事件（D4 显式化）

**Files:**
- Modify: `internal/ledger/types.go:45-59`（事件类型词表加一项）
- Modify: `internal/ledger/events.go`（加 `RecordBranchMerged`）
- Modify: `internal/ledgernode/node.go`（`RunOnce` 成功路径调它）
- Modify: `web/src/app/cards/CardDrawer.tsx`（`eventSummary` 加一条渲染）
- Test: `internal/ledger/events_test.go`、`internal/ledgernode/node_test.go`、`web/src/app/cards/CardDrawer.test.tsx`

**Interfaces:**
- Consumes: 既有 `(*Store).mutate` / `appendEvent` 模式（照 `RecordAcceptance`，`internal/ledger/events.go:214`）
- Produces: `func (s *Store) RecordBranchMerged(cardID, workBranch, base string, pushedWorkBranch bool, actor string) error`，落 `EvBranchMerged` 事件，payload 三键：`pushed_work_branch`（bool）、`merged_into`（string）、`pushed_base`（string）

**注意**：**不要复用既有的 `EvMerged`**——它已经是「卡并入承载卡」的语义（`internal/ledger/merge.go:87`，payload `{members: [...]}`），复用会让两件完全不同的事撞在一个类型上。

- [ ] **Step 1: 写失败的测试**

在 `internal/ledger/events_test.go` 追加：

```go
// TestRecordBranchMerged 合并环节的外部动作必须落账：推了什么、合进哪里。
func TestRecordBranchMerged(t *testing.T) {
	s := newTestStore(t)
	a := seedCard(t, s)
	if err := s.RecordBranchMerged(a.ID, "feat/x", "integration/y", true, "node:merge"); err != nil {
		t.Fatalf("RecordBranchMerged: %v", err)
	}
	events, err := s.EventsFromAsc([]string{a.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.Type != EvBranchMerged {
			continue
		}
		found = true
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解 payload: %v", err)
		}
		if p["pushed_work_branch"] != true {
			t.Fatalf("pushed_work_branch 应为 true: %v", p)
		}
		if p["merged_into"] != "integration/y" || p["pushed_base"] != "integration/y" {
			t.Fatalf("分支字段不对: %v", p)
		}
	}
	if !found {
		t.Fatalf("没落 %s 事件", EvBranchMerged)
	}
}
```

`newTestStore` / `seedCard` 按该文件既有辅助的名字用；若名字不同，改成实际的，**不要新造一套**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/ledger/ -run TestRecordBranchMerged -count=1`
Expected: FAIL —— `undefined: EvBranchMerged` / `RecordBranchMerged`

- [ ] **Step 3: 加事件类型与 store 方法**

`internal/ledger/types.go` 的事件词表里，在 `EvMerged` 之后加一行：

```go
	// EvBranchMerged 合并环节把工作分支合进基线并推 origin。与 EvMerged
	// （卡并入承载卡）是两回事，不可复用。
	EvBranchMerged = "branch_merged"
```

`internal/ledger/events.go` 加方法（照 `RecordAcceptance` 的形状）：

```go
// RecordBranchMerged 落合并环节的外部动作事件。
//
// 参数：workBranch 工作分支名；base 基线分支名；pushedWorkBranch 本次是否
// 真的推了工作分支（本地已有则推，走 fetch 兜底那条腿则为 false）。
//
// 为什么要专门落这条：合并环节会 push 到 origin——外部可见、不易撤回。
// 自动化做的外部动作必须在 timeline 上留痕，否则「这次到底往 origin 推了
// 什么」只能去翻日志。
func (s *Store) RecordBranchMerged(cardID, workBranch, base string, pushedWorkBranch bool, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("合并落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvBranchMerged, actor,
			map[string]any{
				"work_branch":        workBranch,
				"pushed_work_branch": pushedWorkBranch,
				"merged_into":        base,
				"pushed_base":        base,
			})
		return err
	})
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/ledger/ -run TestRecordBranchMerged -count=1`
Expected: PASS

- [ ] **Step 5: 合并环节成功路径调它**

`internal/ledgernode/node.go` 的 `RunOnce` 成功路径（原来只有 `logger.Info("已自动合回基线", ...)` 加 `return`）改为：

```go
	logger.Info("已自动合回基线并推 origin", "base", base)
	branch, branchErr := taskBranch(m.St, card)
	if branchErr != nil {
		// 分支名取不到不推翻已经完成的合并——合并是真的做了，落账缺一条
		// 比谎报失败好。留 Warn 供事后追。
		logger.Warn("合并已完成但取工作分支名失败，事件缺分支字段", "err", branchErr)
		branch = ""
	}
	if err := m.St.RecordBranchMerged(cardID, branch, base, true, "node:merge"); err != nil {
		logger.Warn("合并已完成但落账失败", "err", err)
	}
	return Outcome{Action: ActionMerged}, nil
```

**为什么落账失败只 Warn 不返回错误**：合并已经真的做了、也推上 origin 了，此时返回错误会让上层把一次成功的合并记成失败，比缺一条事件更糟。

在 `internal/ledgernode/node_test.go` 追加一条断言成功路径落了事件的用例，构造方式与 Task 3 的 `seedMergeableCard` 相同，`DoMerge` 返回 nil，断言事件流里出现 `ledger.EvBranchMerged`。

- [ ] **Step 6: web 渲染**

`web/src/app/cards/CardDrawer.tsx` 的 `eventSummary`，在 `status_moved` 那个分支之后加：

```ts
  if (event.type === 'branch_merged') {
    const pushed = payload.pushed_work_branch === true ? '已推工作分支' : '工作分支来自 origin'
    return `合并 ${String(payload.work_branch ?? '')} → ${String(payload.merged_into ?? '')}（${pushed}，已推 ${String(payload.pushed_base ?? '')}）`
  }
```

在 `web/src/app/cards/CardDrawer.test.tsx` 追加一条：给一个 `branch_merged` 事件，断言渲染文本含 `合并` 与基线分支名，且**不含**原始 JSON 花括号（证明走了专门分支而不是兜底的 `JSON.stringify`）。

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/ledger/ ./internal/ledgernode/ -count=1`
Expected: PASS

Run（在 `web/` 下）: `npx vitest run src/app/cards/CardDrawer.test.tsx && npx tsc --noEmit`
Expected: PASS + 0 类型错误

- [ ] **Step 8: 加日志与注释（复核）**

确认：`RecordBranchMerged` 有 doc 注释与「为什么要专门落这条」；成功路径有 Info 日志；两个降级分支（取分支名失败、落账失败）各有 Warn 带 cause 与「为什么不推翻合并」的 why 注释；web 无 `console.log`。

- [ ] **Step 9: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/ledger/types.go internal/ledger/events.go internal/ledger/events_test.go internal/ledgernode/node.go internal/ledgernode/node_test.go web/src/app/cards/CardDrawer.tsx web/src/app/cards/CardDrawer.test.tsx
git commit -m "feat(ledger): 合并环节落 branch_merged 事件，外部推送动作留痕"
```

---

### Task 5: D2 —— 派发时基线分支名无条件补拉

**Files:**
- Modify: `internal/agentd/workspace.go`（在 `ResolveBaseline` 附近新增分支解析函数并接入分支路径）
- Test: `internal/agentd/workspace_test.go`（文件已存在，追加用例）

**Interfaces:**
- Consumes: 同文件既有的 `gitRun` / `gitRunNet` / `FetchTimeout` / `truncateRunes` / `log()`
- Produces: `func ResolveBaseBranch(ctx context.Context, repo, branch string) (string, error)` —— 返回 `origin/<branch>` 解析出的完整 sha；分支在 origin 上不存在时返回带 fetch stderr 原文的错误

**先读懂再动**：打开 `internal/agentd/workspace.go` 的 `ResolveBaseline`（约 925 行）。它处理的是基线**提交**，逻辑是「本地没有才 fetch」，并且那是**刻意的性能设计**（`cat-file` 是纯本地查询，只有真落后才付网络代价）。**本 task 不改它。**

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/workspace_test.go` 追加。测试用真实的本地 git 仓库对（origin 裸仓 + 克隆），照该文件既有用例搭建仓库的方式；若已有搭建辅助就复用：

```go
// TestResolveBaseBranchAlwaysFetches 分支路径必须无条件补拉。
//
// 为什么不能照抄提交路径的「本地没有才拉」：分支名在本地**永远解析得到**
// （那正是陈旧的那一份），拿「解析得到」当「不用拉」的信号，等于让这个 bug
// 永远走不到修复路径。
func TestResolveBaseBranchAlwaysFetches(t *testing.T) {
	origin, clone := newOriginAndClone(t) // 见下方说明
	// 在 origin 上再推一个提交，clone 此时还不知道
	newSha := commitOnOrigin(t, origin, "second.txt", "2")

	got, err := ResolveBaseBranch(context.Background(), clone, "main")
	if err != nil {
		t.Fatalf("ResolveBaseBranch: %v", err)
	}
	if got != newSha {
		t.Fatalf("应解析到 origin 上的最新提交 %s，实得 %s（说明没补拉）", newSha, got)
	}
}

// TestResolveBaseBranchMissingBranch origin 上没有该分支时拒绝，且带原文。
func TestResolveBaseBranchMissingBranch(t *testing.T) {
	_, clone := newOriginAndClone(t)
	_, err := ResolveBaseBranch(context.Background(), clone, "no-such-branch")
	if err == nil {
		t.Fatalf("不存在的分支应报错")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Fatalf("错误里应带分支名: %v", err)
	}
}
```

`newOriginAndClone(t)` 与 `commitOnOrigin(t, ...)` 是本 task 要写的测试辅助（若该文件已有等价物就复用）：前者 `git init --bare` 出 origin、克隆一份并推一个初始提交，返回两个路径；后者在 origin 上直接造一个提交并返回其 sha。**两个目录都用 `t.TempDir()`——绝不建在仓库内**（仓库内的临时 git 目录会破坏 git 相关测试的前提）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestResolveBaseBranch -count=1`
Expected: FAIL —— `undefined: ResolveBaseBranch`

- [ ] **Step 3: 实现**

`internal/agentd/workspace.go`，在 `ResolveBaseline` 之后加：

```go
// ResolveBaseBranch 把基线**分支名**解析成完整 sha，起点取 origin 上的那一份。
//
// 参数：repo 任务仓库路径；branch 基线分支名。
// 返回：origin/<branch> 的完整 sha；分支在 origin 上不存在或 fetch 失败时报错。
//
// 与 ResolveBaseline（提交路径）唯一的、也是必须的不同：**无条件 fetch**。
// 提交路径是「本地没有才拉」，因为 commit sha 在本地要么有要么没有，是可靠
// 信号；而分支名在本地永远解析得到——那正是陈旧的那一份。拿「解析得到」当
// 「不用拉」的信号，会让「执行机镜像陈旧导致起点错」这个 bug 永远不触发
// 修复路径。
func ResolveBaseBranch(ctx context.Context, repo, branch string) (string, error) {
	log().Info("解析基线分支，补拉远端", "repo", repo, "branch", branch, "timeout", FetchTimeout)
	fctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	if _, stderr, err := gitRunNet(fctx, repo, "fetch", "origin", branch); err != nil {
		log().Error("基线分支补拉失败", "repo", repo, "branch", branch,
			"stderr", truncateRunes(stderr, 500), "cause", err)
		return "", fmt.Errorf("%w: 基线分支 %q 补拉失败（fetch 输出：%s）",
			ErrBaseCommitMissing, branch, strings.TrimSpace(truncateRunes(stderr, 300)))
	}
	out, stderr, err := gitRun(ctx, repo, "rev-parse", "FETCH_HEAD")
	if err != nil {
		log().Warn("基线分支解析失败", "repo", repo, "branch", branch,
			"stderr", truncateRunes(stderr, 300))
		return "", fmt.Errorf("%w: 基线分支 %q 在 origin 上不存在", ErrBaseCommitMissing, branch)
	}
	sha := strings.TrimSpace(out)
	log().Info("基线分支解析完成", "repo", repo, "branch", branch, "start", sha)
	return sha, nil
}
```

接入点是 `internal/agentd/manager.go:748-771`。先读懂那里的两个字段——**它们不是一回事**：

- `req.BaseCommit`（40 位 sha）走 `ResolveBaseline`（第 740 行），**本 task 不碰**。
- `req.Base`（新分支起点，`card dispatch` 传进来的就是**分支名**）走第 748 行的
  `start, ahead := req.Base, 0`，最后在第 764 行由 `resolveCommit(ctx, repoPath, start)`
  解析——**纯本地解析，没有补拉，这正是 D2**。

改法：在 `if start != ""` 那个块里，`resolveCommit` **之前**按形态分流：

```go
	if start != "" {
		// D2：start 是分支名时必须先补拉。分支名在本地永远解析得到（那正是
		// 陈旧的那一份），直接 resolveCommit 会拿旧引用当起点。40 位 sha 不需要
		// ——ResolveBaseline 已经保证它在库里了。
		if !baseCommitRe.MatchString(start) {
			fetched, ferr := ResolveBaseBranch(ctx, repoPath, start)
			if ferr != nil {
				return nil, ferr
			}
			m.log.Info("基线分支已补拉并解析", "repo", repoPath, "branch", start, "sha", fetched)
			start = fetched
		}
		resolved, rerr := resolveCommit(ctx, repoPath, start)
		...原有内容不变...
	}
```

**保留第 759-762 行那段 B76 注释不动**（「起点必须以 sha 形态交给 git，给分支名会
触发 DWIM」）——它解释的正是这个块存在的理由，本改动是在它前面加一道补拉，
不是取代它。**`ResolveBaseline` 内部一行都不改。**

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestResolveBaseBranch -count=1`
Expected: PASS（两条）

- [ ] **Step 5: 跑本包全量**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS（该包较慢，允许 ~110s）

- [ ] **Step 6: 加日志与注释（复核）**

确认：进入 Info（带 repo/branch/timeout）、fetch 失败 Error 带 stderr 原文、解析失败 Warn、成功 Info 带解析出的 sha；函数 doc 注释写清「与提交路径唯一且必须的不同」。

- [ ] **Step 7: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "fix(agentd): 基线分支名无条件补拉，不再基于陈旧本地引用起分支"
```

---

### Task 6: 改名 `节点` → `环节`（独立提交，不动持久化键）

**Files:**
- Rename: 目录 `internal/ledgernode/` → `internal/ledgerstep/`（含其中全部 `.go` 与 `_test.go`）
- Modify: 全仓对该包的 import 与符号引用（`cmd/card_node.go`、`cmd/card_dispatch.go` 等）
- Modify: `cmd/card_dispatch.go` 的 `--node` flag → `--step`
- Modify: `web/src/app/cards/CardDrawer.tsx` 的「节点动作」区标题 → 「环节动作」

**Interfaces:**
- Consumes: Task 1-5 的全部产出（本 task 在它们之后做，避免改名噪音淹没行为 diff）
- Produces: 包名 `ledgerstep`；`ReviewStep` / `MergeStep`；`Step` 字段；CLI `--step review|merge`

**铁律：持久化键名一律不动。** 以下三处是已落盘的历史数据，改了会让存量卡的回合计数被清零、从而绕开 3 轮封顶这道安全阀：

| 不许改 | 在哪 |
|---|---|
| 事件 payload 键 `node` | `RecordReviewVerdict` 写入，`CountRounds`（`rounds.go:28`）读取 |
| 事件 payload 键 `human_reset_node` | `AddCommentReset` 写入（`events.go:195`），`CountRounds`（`rounds.go:31`）读取 |
| actor 前缀 `node:` | `node.go` 多处写入 |

actor 前缀虽然无人解析（只在 `CardDrawer.tsx:338` 被渲染），但**一并不动**——改了只会让历史与新事件的 actor 长得不一样，收益为零。

- [ ] **Step 1: 写失败的测试**

在 `internal/ledgernode/rounds_test.go` 追加（改名前先立回归网）：

```go
// TestCountRoundsReadsLegacyPayloadKey 存量事件用的是 payload 键 "node"。
// 改名时若把这个键也改了，存量卡的回合计数会被清零、绕开 3 轮封顶。
// 这条是那道安全阀的回归网。
func TestCountRoundsReadsLegacyPayloadKey(t *testing.T) {
	evs := []ledger.Event{
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review","pass":false}`)},
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review","pass":false}`)},
	}
	if got := CountRounds(evs, "review"); got != 2 {
		t.Fatalf("应数到 2 轮，实得 %d——payload 键被改了？", got)
	}
}

// TestCountRoundsResetKeyUnchanged 清零键同理。
func TestCountRoundsResetKeyUnchanged(t *testing.T) {
	evs := []ledger.Event{
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review"}`)},
		{Type: ledger.EvComment, Payload: []byte(`{"human_reset_node":"review"}`)},
		{Type: ledger.EvReviewVerdict, Payload: []byte(`{"node":"review"}`)},
	}
	if got := CountRounds(evs, "review"); got != 1 {
		t.Fatalf("重置后应只剩 1 轮，实得 %d", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它通过**

Run: `go test ./internal/ledgernode/ -run TestCountRounds -count=1`
Expected: **PASS** —— 这两条是**改名前就该绿**的回归网（不是 TDD 的红→绿，是「先量出网罩住什么」）。若此时是红的，说明理解有误，停下来查清楚再动。

- [ ] **Step 3: 改包名与目录**

```bash
git mv internal/ledgernode internal/ledgerstep
```

然后把该目录下所有 `.go` 文件的 `package ledgernode` 改为 `package ledgerstep`。

- [ ] **Step 4: 改类型与字段名**

在 `internal/ledgerstep/` 内：`ReviewNode` → `ReviewStep`、`MergeNode` → `MergeStep`、`ReviewStep.Node` 字段 → `ReviewStep.Step`。`CountRounds(evs, node string)` 的**参数名**可改为 `step`，但**函数内读的 payload 键必须仍是 `"node"` 与 `"human_reset_node"`**。

- [ ] **Step 5: 改全仓引用**

```bash
grep -rln "ledgernode" --include="*.go" . | xargs sed -i '' 's/ledgernode/ledgerstep/g'
grep -rn "ReviewNode\|MergeNode" --include="*.go" .
```

第二条的命中逐个改成 `ReviewStep` / `MergeStep`。

- [ ] **Step 6: 改 CLI flag 与界面文案**

`cmd/card_dispatch.go`：`--node` 改为 `--step`，帮助文案 `"节点执行器：review|merge"` 改为 `"自动环节：review|merge"`；变量名 `cardDispatchNode` → `cardDispatchStep`。相关测试里的 `"--node"` 一并改。

`web/src/app/cards/CardDrawer.tsx`：`节点动作` → `环节动作`；同步改 `CardDrawer.test.tsx` 里对该文案的断言（若有）。

- [ ] **Step 7: 跑全量确认改名没破坏行为**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS。**特别确认 `TestCountRoundsReadsLegacyPayloadKey` 与 `TestCountRoundsResetKeyUnchanged` 仍是绿的**——它们红了就说明持久化键被改了，必须改回来。

Run（在 `web/` 下）: `npx vitest run && npx tsc --noEmit`
Expected: PASS + 0 错

- [ ] **Step 8: 确认没有漏网**

```bash
grep -rn "ledgernode\|ReviewNode\|MergeNode" --include="*.go" .
grep -rn '"--node"' --include="*.go" .
```

Expected: 两条都无输出。

- [ ] **Step 9: gofmt + 提交**

```bash
gofmt -l . | grep -v '^web/'
git add -A
git commit -m "refactor: 节点改名为环节，持久化键名保持不变

包 ledgernode→ledgerstep、ReviewNode/MergeNode→ReviewStep/MergeStep、
CLI --node→--step、界面「节点动作」→「环节动作」。

事件 payload 键 node / human_reset_node 与 actor 前缀 node: 一律不动：
CountRounds 按这两个键推导回合数，改了会让存量卡计数清零、绕开 3 轮封顶。"
```

---

### Task 7: 全量门

**Files:** 无新增；只跑验证并修出现的问题

**Interfaces:**
- Consumes: Task 1-6 的全部产出
- Produces: 一份可信的「全绿」结论

- [ ] **Step 1: 后端全量**

```bash
gofmt -l . | grep -v '^web/'
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: gofmt 无输出；build/vet 退 0；test 全绿。

**已知的平台性既有红**（不是本次引入，如实记、不要去修）：`internal/prochost` 的 `TestFenceCannotBeRaisedBack`（root 能抬回 RLIMIT）、`internal/executor/grok` 的 `TestSyncAuthKeepsTaskCopyWhenWriteFails`（root 能写只读文件）、`internal/agentd` 的 `TestServeReturnsListenError`（该机既有超时）。这三条在基线上就红。**其它任何红都必须归因**。

- [ ] **Step 2: 前端全量**

```bash
cd web && npx tsc --noEmit && npx vitest run && npm run build
```

Expected: 全部通过。

- [ ] **Step 3: 红线自检**

```bash
grep -rn 'fmt\.Printf' internal/ cmd/ | grep -v '_test.go'
grep -rn 'console\.log' web/src | grep -v test
grep -rn 'push --force\|push -f\|--force-with-lease' internal/ledgerstep/
```

Expected: 三条都无输出。第三条是本轮的安全红线。

- [ ] **Step 4: 落 ledger**

按纪律块要求把 Step 1-3 的**实际输出**写进 ledger（不是预期）。沙箱受限导致的失败如实标注形状。**没跑到结果的不许写结论。**

**本 task 到此为止。** 真机验收（起隔离 agentd 实例、造真实 git 远端、跑 `handoff card ...`）**不属于执行者范围**——纪律块禁止调用 handoff CLI，那部分见文末清单，由协调者执行。

- [ ] **Step 5: 提交（如有修复）**

```bash
gofmt -l . | grep -v '^web/'
git add -A
git commit -m "fix(ledgerstep): 全量门发现的问题修复"
```

若 Step 1-3 全绿无需修复，本步跳过，不要造空提交。

---

## 附一：审核者本地验收清单（**不派发**，协调者执行）

以下要驱动 handoff 自身或造真实 git 远端，与纪律块的「不要调用 handoff CLI、
不要起任何新的 executor 进程」冲突，**故意留在派发范围之外**。

对应 spec 判据 ①②③⑤⑥⑦：

**A. 造靶子** —— `git init --bare` 一个本地 origin，克隆两份（一份当协调者仓，
一份当执行机仓库），起隔离 agentd 实例（独立 DataDir + 端口、`ledger.enabled: true`、
`executor.default: fake`），**绝不重启 launchd 托管的生产 agentd**。

**B. 判据①（D1 正向）** —— 建卡 → 派发 → 推「待审阅」→ 跑审阅环节 →
`card dispatch <id> --step merge`。合并不再报 `couldn't find remote ref`；
`git ls-remote origin <工作分支>` 查得到。

**C. 判据②③（D3 工作区隔离）** —— 三种情形各跑一次合并：
① 协调者仓停在别的分支；② 协调者仓**留有未提交改动**；③ 协调者仓**正停在基线分支上**
（这条是 `--detach` 要挡的失败，不覆盖等于没验）。每次跑完确认当前分支与
`git status` 不变、`git worktree list` 无残留、`git ls-remote origin <基线>` 已推进。

**D. 判据⑤（缺失阶梯）** —— 用 `--no-sync` 造一张协调者本地无工作分支的卡，
合并环节应走 fetch 那条腿；再把 origin 上的分支也删掉重试，应转「等人」且 reason
含「先 handoff pull」。

**E. 判据⑥（非快进保护）** —— 在 origin 上把同名工作分支改成分叉状态，
合并环节的 push 应以非快进失败并转「等人」，确认全程无 `--force`。

**F. 判据⑦（D2）** —— 把执行机仓库的本地 main 人为 `reset --hard` 到旧提交，
派一张 `--base-branch main` 的卡，确认新分支起点是补拉后的 main。

**G. skill 补一句（判据④的后半）** —— `skills/handoff/SKILL.md` 的「账本模式」
第 4 点补：合并环节会把工作分支与基线**都推上 origin**，且协调者本地的基线引用
不再被推进（想看结果 `git fetch`）。这份 skill 由协调者维护，不在派发范围内。

**H. 清理** —— 删掉临时 origin 与两份克隆、停隔离实例。

## 附二：本 plan 明确不做

- 不动 `internal/discipline/`、`internal/permgate/`、执行者 taskenv。
- 不给自动推基线加开关。
- 不碰 `NewLocalObjective` 里写死的 `gofmt` + `go test ./...`（客观判据只认 Go 项目，另立条目）。
- 不做 A 组两条（按环节派发按钮、子任务树 rollup）。
- 不做存量切换、不合 main、不动 `docs/superpowers/backlog.md`。
- 不改 `skills/handoff/SKILL.md`——skill 的账本模式节由协调者维护。
