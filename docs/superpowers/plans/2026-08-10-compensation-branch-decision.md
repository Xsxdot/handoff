# 补偿路径分支处置改为显式决策 实现计划（B44）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `deleteCreatedBranch` 里「删不删分支」的判断从 git I/O 中拆成一个纯函数，让原本靠「碰巧写在前面」生效的第一道闸变成一条可被单测逐行钉住的显式规则。

**Architecture:** 三步。第一步纯新增：加 `branchAction` 枚举与纯函数 `decideBranchAction`，配一张五行表驱动单测（此时还没有任何调用方，不改变任何行为）。第二步接线：`branchTip` 的签名从 `string` 改成 `(string, error)`，`deleteCreatedBranch` 改为「取尖端 → 问决策 → 按决定打日志并执行」，记录侧三处调用点收敛到一个 `recordNewBranchTip` 辅助函数；现有 7 条 `TestCompensate*` 一条不改，充当「行为没变」的回归保护。第三步验收：做变异检验证明这次改动确实堵住了「删掉闸1 测试不翻红」的洞，再跑全套闸门。

**Tech Stack:** Go（标准库 + `log/slog`）、`go test`、`go vet`、`gofmt`。无新增依赖。

**Spec:** `docs/superpowers/specs/2026-08-10-compensation-branch-decision-design.md`

## Global Constraints

- 语言：Go。日志一律用已注入的 `m.log`（`manager.go`）/ 包级 `log()`（`workspace.go`），**禁止 `fmt.Printf`**。
- **不改任何对外行为**：四种情形的最终动作（删 / 保留）与改动前逐一相同。本次唯一允许的可观测变化是日志时序（见 Task 2 Step 4）。
- 不引入任何注入 / mock 机制，不新增第三方依赖。
- 不动 `RemoveManagedWorktree` 与归档路径（`Done` / `Stop`）的「只删树不删分支」语义。
- 不修悬空引用本身——那是 git 仓库的病，不是 handoff 的职责。
- 注释一律中文，解释「为什么」而非「做了什么」；导出与非导出的新函数都要有说明参数、返回、注意事项的文档注释。
- 闸门（每个 task 的最后一步之前至少跑前四条，Task 3 跑全套）：
  - `gofmt -l .`（必须无输出）
  - `go build ./...`
  - `go vet ./...`
  - `go test ./... -count=1`
  - `go test -race ./cmd/ ./internal/agentd/ ./internal/store/`
  - `GOOS=windows GOARCH=amd64 go build ./...`

## File Structure

| 文件 | 动作 | 本次承担的职责 |
|---|---|---|
| `internal/agentd/manager.go` | 修改（`deleteCreatedBranch` 及其紧邻处，约 728-748 行） | 新增 `branchAction` 枚举 + `decideBranchAction` 纯决策函数；`deleteCreatedBranch` 退化为「取数据 → 问决策 → 执行 + 打日志」 |
| `internal/agentd/manager_test.go` | 修改（在 `TestCompensateDeletesCreatedBranch` 之前插入） | 新增 `TestDecideBranchAction` 表驱动单测；现有 7 条 `TestCompensate*` 一字不改 |
| `internal/agentd/workspace.go` | 修改（`branchTip` 约 518-531 行；调用点 264 / 280 / 302 行） | `branchTip` 如实返回错误；新增 `recordNewBranchTip` 把「记基线 + 失败告警」收敛到一处 |

决策逻辑放 `manager.go` 而不是 `workspace.go`：它是补偿策略（要不要删），不是 git 能力（尖端是多少）。`workspace.go` 只负责如实报告事实。

---

### Task 1: 纯决策函数与它的表驱动单测

本 task 只做纯新增。做完之后 `decideBranchAction` 还没有任何生产调用方，所有既有行为逐字不变——这是刻意的，好让审阅者能单独判断这张决策表本身对不对。

**Files:**
- Modify: `internal/agentd/manager.go`（在 `deleteCreatedBranch` 的文档注释之前插入新类型与新函数）
- Test: `internal/agentd/manager_test.go`（在 `func TestCompensateDeletesCreatedBranch` 之前插入）

**Interfaces:**
- Consumes: 无（纯新增，不依赖任何先前 task）
- Produces: 供 Task 2 使用——
  - `type branchAction int`
  - 常量 `branchDelete` / `branchKeepNotOurs` / `branchKeepTipUnknown` / `branchKeepTipMoved`
  - `func decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction`

- [ ] **Step 1: 写下会失败的表驱动单测**

在 `internal/agentd/manager_test.go` 中 `func TestCompensateDeletesCreatedBranch(t *testing.T) {` 这一行**之前**插入：

```go
// actionName 让断言失败时打出可读的枚举名，而不是 0/1/2/3。
// 只服务测试可读性，故不做成生产侧的 String() 方法。
var actionName = map[branchAction]string{
	branchDelete:         "branchDelete",
	branchKeepNotOurs:    "branchKeepNotOurs",
	branchKeepTipUnknown: "branchKeepTipUnknown",
	branchKeepTipMoved:   "branchKeepTipMoved",
}

// TestDecideBranchAction 逐条钉住补偿路径的分支处置规则。
//
// 第二行（recordedTip 空 + tipErr 非 nil）是本用例存在的全部理由：它是
// 「不是本次新建的」这道闸的独占角落——旧结构里 branchTip 失败塌缩成空串，
// 与 recordedTip 的空串撞车，闸2 的 cur != recordedTip 会变成「放行删除」，
// 而该状态在真实仓库里可由悬空 symref 达成（已实测，见 spec §2.1）。
func TestDecideBranchAction(t *testing.T) {
	const (
		shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	errTip := errors.New("rev-parse 失败")

	cases := []struct {
		name        string
		recordedTip string
		currentTip  string
		tipErr      error
		want        branchAction
	}{
		{"用户自带分支：尖端取得到", "", shaA, nil, branchKeepNotOurs},
		{"用户自带分支：尖端取不到（悬空 symref）", "", "", errTip, branchKeepNotOurs},
		{"本次新建且自创建以来零提交", shaA, shaA, nil, branchDelete},
		{"本次新建但尖端已移动", shaA, shaB, nil, branchKeepTipMoved},
		{"本次新建但尖端取不到", shaA, "", errTip, branchKeepTipUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideBranchAction(tc.recordedTip, tc.currentTip, tc.tipErr)
			if got != tc.want {
				t.Fatalf("decideBranchAction(%q, %q, %v) = %s，期望 %s",
					tc.recordedTip, tc.currentTip, tc.tipErr, actionName[got], actionName[tc.want])
			}
		})
	}
}
```

`errors` 已在 `manager_test.go` 的 import 块中，不需要新增 import。

- [ ] **Step 2: 跑测试确认它编译失败**

```bash
go test ./internal/agentd/ -run TestDecideBranchAction -count=1
```

Expected: 编译失败，`undefined: branchAction`、`undefined: decideBranchAction`、`undefined: branchDelete` 等。

- [ ] **Step 3: 写最小实现**

在 `internal/agentd/manager.go` 中 `deleteCreatedBranch` 的文档注释块**之前**插入：

```go
// branchAction 是补偿路径对「本次分支」的处置决定。
// 每个取值对应一条独立规则，便于表驱动测试逐条钉住——这正是把判断从
// deleteCreatedBranch 里拆出来的目的。
type branchAction int

const (
	branchDelete        branchAction = iota // 确认是本次新建且自创建以来零提交，可删
	branchKeepNotOurs                       // 不是本次新建的，是用户自己的分支
	branchKeepTipUnknown                    // 尖端取不到，无从复核
	branchKeepTipMoved                      // 尖端与创建时不符，疑似已有提交
)

// decideBranchAction 判定补偿路径是否可以删除该分支。纯函数：不调 git、不打日志、
// 不碰任何状态，故可以被表驱动测试穷举。
//
// 参数：
//   - recordedTip: PrepareWorkspace 建分支时记下的尖端；空串 = 分支不是本次新建的
//   - currentTip:  当前尖端；tipErr 非 nil 时其值无意义
//   - tipErr:      取当前尖端时的错误
//
// 返回：四种处置之一；只有 branchDelete 允许调用方执行删除。
//
// 注意：
//   - 判定顺序是本函数的全部要点。`recordedTip == ""` 必须排在任何拿 currentTip
//     做的比较之前。旧实现里这条规则靠「碰巧写在前面」生效，一旦有人认为两道闸
//     重复而删掉它，悬空 symref 场景下 branchTip 失败塌缩出的空串会与 recordedTip
//     的空串相等，从而放行删除，毁掉用户自己的分支（该场景已实测可达，见
//     docs/superpowers/specs/2026-08-10-compensation-branch-decision-design.md §2.1）
//   - 取不到尖端一律保留而非删除：删分支不可逆，宁可留残留也不能删错
func decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction {
	if recordedTip == "" {
		return branchKeepNotOurs
	}
	if tipErr != nil {
		return branchKeepTipUnknown
	}
	if currentTip != recordedTip {
		return branchKeepTipMoved
	}
	return branchDelete
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run TestDecideBranchAction -count=1 -v
```

Expected: PASS，五个子测试全绿。

- [ ] **Step 5: 加日志**

本 task 无需加日志：`decideBranchAction` 是纯函数，spec §5 明确要求它**不打日志**——日志留在调用点（Task 2 Step 3 落实）。此步骤为显式确认，不是跳过。

- [ ] **Step 6: 加注释**

已在 Step 1 / Step 3 的代码中就位，逐项核对：

- `branchAction` 类型注释：说明它是「补偿路径的处置决定」以及为什么要拆出来 ✓
- 四个枚举值各自的行尾注释：说明**这一条规则是什么情形** ✓
- `decideBranchAction` 文档注释：参数、返回、以及「判定顺序是全部要点」这条为什么 ✓
- `TestDecideBranchAction` 的注释：说明第二行用例存在的理由 ✓
- 没有新建文件，故不涉及文件头注释

- [ ] **Step 7: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
```

Expected: `gofmt -l .` 无输出，其余全过。

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(b44): 抽出 decideBranchAction 纯决策函数与表驱动单测

把补偿路径「删不删分支」的判断从 git I/O 里拆出来，四种处置各成一条
显式规则。第一道闸（recordedTip 为空 = 不是本次新建的）从此有了独占的
测试用例，不再依赖第二道闸碰巧覆盖它。

本提交纯新增，尚无生产调用方，行为零变化。"
```

---

### Task 2: 接线——branchTip 如实报错，deleteCreatedBranch 改用决策函数

**Files:**
- Modify: `internal/agentd/workspace.go`（`branchTip` 约 518-531 行；调用点 264 / 280 / 302 行）
- Modify: `internal/agentd/manager.go`（`deleteCreatedBranch`，约 728-748 行）
- Test: `internal/agentd/manager_test.go`（**不改**——现有 7 条 `TestCompensate*` 就是本 task 的回归保护）

**Interfaces:**
- Consumes: Task 1 的 `decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction` 与四个 `branchAction` 常量
- Produces:
  - `func branchTip(ctx context.Context, repo, branch string) (string, error)`（签名变更）
  - `func recordNewBranchTip(ctx context.Context, repo, branch string) string`（新增，`workspace.go` 包内）

- [ ] **Step 1: 先跑一遍现有回归测试，记下绿的基线**

```bash
go test ./internal/agentd/ -run 'TestCompensate' -count=1 -v
```

Expected: 7 条全 PASS。这 7 条在本 task 结束时必须仍然全 PASS 且**一字未改**——它们是「行为没变」的唯一凭据。

- [ ] **Step 2: 改 `branchTip` 签名，让失败如实上报**

`internal/agentd/workspace.go`，把现有的 `branchTip` 整个替换为：

```go
// branchTip 取分支当前尖端 sha。
//
// 参数：repo 为主仓库路径，branch 为分支名
//
// 返回：
//   - 成功时返回 40 位 sha 与 nil
//   - 取不到时返回空串与非 nil 错误（分支不存在、悬空引用、git 调用失败）
//
// 注意：失败不再塌缩成空串，也不在此处打日志——两件事都交给调用方。补偿侧的
// 决策必须能区分「尖端取不到」与「分支不是本次新建的」，旧签名让这两件事共用
// 空串，正是那道闸无法被单独测试的根因（见
// docs/superpowers/specs/2026-08-10-compensation-branch-decision-design.md §2）。
func branchTip(ctx context.Context, repo, branch string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("git rev-parse refs/heads/%s: %s: %w", branch, strings.TrimSpace(stderr), err)
	}
	return strings.TrimSpace(out), nil
}

// recordNewBranchTip 记录刚新建分支的尖端，作为补偿路径复核「自创建以来零提交」
// 的基线。PrepareWorkspace 的三条建分支路径（managed worktree / 用户树 / 原地）
// 共用它，避免同一段告警逻辑抄三遍。
//
// 参数：repo 为主仓库路径，branch 为刚新建的分支名
//
// 返回：取到则为 40 位 sha；取不到返回空串。
//
// 注意：取不到时返回空串是刻意的保守选择——没记到基线意味着补偿届时无从复核，
// 空串会让 decideBranchAction 判成 branchKeepNotOurs，即保留该分支不删。代价是
// 补偿时日志说不清「为什么不删」，所以失败必须在**这里**留下 WARN。
func recordNewBranchTip(ctx context.Context, repo, branch string) string {
	tip, err := branchTip(ctx, repo, branch)
	if err != nil {
		log().Warn("新建分支后取尖端失败，补偿将保留该分支", "repo", repo, "branch", branch, "cause", err)
		return ""
	}
	return tip
}
```

`fmt` 与 `strings` 已在 `workspace.go` 的 import 块中，不需要新增 import。

- [ ] **Step 3: 改三处记录侧调用点**

`internal/agentd/workspace.go` 的 264 / 280 / 302 行是三处逐字相同的：

```go
		if !isExisting {
			ws.NewBranchTip = branchTip(ctx, req.Repo, branch)
		}
```

三处都改成：

```go
		if !isExisting {
			ws.NewBranchTip = recordNewBranchTip(ctx, req.Repo, branch)
		}
```

（spec §3.4 给的是内联展开的写法；这里收敛成辅助函数，行为完全一致而不必把同一段告警逻辑抄三遍。）

- [ ] **Step 4: 改写 `deleteCreatedBranch`**

`internal/agentd/manager.go`。先改文档注释的最后一条「注意」——把这一行：

```go
//   - NewBranchTip 为空 = 分支不是本次新建的，是用户的东西，一律不动
```

替换为：

```go
//   - 删不删的规则全部在 decideBranchAction 里（含「NewBranchTip 为空 = 不是
//     本次新建的，一律不动」这一条），本函数只负责取数据、按决定打日志、执行 git
```

其余注意事项与参数说明保持不变。然后把函数体整个替换：

```go
func (m *Manager) deleteCreatedBranch(ctx context.Context, repo string, ws Workspace) {
	cur, tipErr := branchTip(ctx, repo, ws.Branch)
	switch decideBranchAction(ws.NewBranchTip, cur, tipErr) {
	case branchKeepNotOurs:
		// 不是本次新建的，静默保留：每次 --branch <已存在分支> 的派发失败都会
		// 走到这里，是正常出口，打 WARN 只会变成噪音
		return
	case branchKeepTipUnknown:
		m.log.Warn("取分支尖端失败，无从复核，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "cause", tipErr)
		return
	case branchKeepTipMoved:
		m.log.Warn("分支尖端与创建时不符，疑似已有提交，保留待查",
			"repo", repo, "branch", ws.Branch, "expect", ws.NewBranchTip, "actual", cur)
		return
	}
	m.log.Info("补偿删除本次新建的分支", "repo", repo, "branch", ws.Branch, "tip", ws.NewBranchTip)
	// 用 -D 而非 -d：分支起点可能领先仓库当前 HEAD，-d 会因「未合并」误拒；
	// 而「自创建以来零提交」已由上面的尖端复核实证，-D 在这里是确定性而非暴力
	if _, stderr, err := gitRun(ctx, repo, "branch", "-D", ws.Branch); err != nil {
		m.log.Error("补偿删除分支失败", "repo", repo, "branch", ws.Branch,
			"stderr", truncateRunes(stderr, 300), "cause", err)
		return
	}
	m.log.Info("补偿删除分支完成", "repo", repo, "branch", ws.Branch)
}
```

**注意这里有一处刻意的日志时序变化**（spec §5 已记录，是本次唯一的可观测变化）：旧实现先打「补偿删除本次新建的分支」再复核尖端，尖端对不上时会先打一条「要删了」再打一条「保留待查」，读日志的人要看两行才知道结论。新实现把 Info 移到决策之后，日志与实际动作一一对应。返回值与磁盘状态均不变。

- [ ] **Step 5: 跑回归测试确认行为没变**

```bash
go test ./internal/agentd/ -run 'TestCompensate|TestDecideBranchAction' -count=1 -v
```

Expected: 7 条 `TestCompensate*` + 5 个 `TestDecideBranchAction` 子测试全 PASS。

若 `TestCompensateKeepsBranchWhenTipMoved` 或 `TestCompensateKeepsExistingBranch` 翻红，说明接线接错了——**不要改测试**，回到 Step 4 核对 switch 的分支归属。

- [ ] **Step 6: 确认日志覆盖（instrumenting-code 自检）**

逐项核对，不达标就补：

- 每条错误分支都带上下文与 cause：`recordNewBranchTip` 的 WARN 带 `repo`/`branch`/`cause` ✓；`branchKeepTipUnknown` 的 WARN 带 `expect`/`cause` ✓；`branchKeepTipMoved` 的 WARN 带 `expect`/`actual` ✓；`git branch -D` 失败的 ERROR 带 `stderr`/`cause` ✓
- 外部调用（git）前后有日志：删除动作前 Info、成功后 Info ✓
- 成功路径不静默：「补偿删除分支完成」✓；`branchKeepNotOurs` 是刻意静默的正常出口，理由已写进代码注释 ✓
- 未使用 `fmt.Printf` / `print` 作为日志手段 ✓
- `branchTip` 移除了内部的 WARN：这不是丢日志——四个调用方全部都在自己的错误分支里打了带上下文的日志，且比原先那条更能说清「失败之后会发生什么」

- [ ] **Step 7: 确认注释覆盖**

- `branchTip` 新文档注释说明了新返回约定与「为什么不再塌缩成空串」✓
- `recordNewBranchTip` 新文档注释说明了参数、返回、以及「返回空串是刻意的保守选择」✓
- `deleteCreatedBranch` 文档注释已改为指向 `decideBranchAction` ✓
- `branchKeepNotOurs` 的静默返回有「为什么不打 WARN」的行内注释 ✓
- `-D` 而非 `-d` 的理由注释保留 ✓
- 没有新建文件，故不涉及文件头注释

- [ ] **Step 8: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
```

Expected: `gofmt -l .` 无输出，其余全过。

```bash
git add internal/agentd/manager.go internal/agentd/workspace.go
git commit -m "refactor(b44): branchTip 如实报错，补偿分支处置改走 decideBranchAction

branchTip 从返回空串改为返回 (string, error)，「取不到」不再与「不是本次
新建的」共用空串；deleteCreatedBranch 退化为取数据 + 问决策 + 执行。记录侧
三处调用点收敛到 recordNewBranchTip，失败在那里打 WARN。

行为零变化，由未改动的 7 条 TestCompensate* 回归保护。唯一可观测变化是
「补偿删除本次新建的分支」这条 Info 移到了决策之后，日志与实际动作一一对应。"
```

---

### Task 3: 变异检验与全套闸门

这是 B44 存在的全部理由。B44 本身就是变异检验发现的（删掉闸1 测试全绿），用同一手段验收才自洽——不做这一步，就无法证明这次重构真的解决了问题，只能证明它没把东西改坏。

**Files:**
- 无永久改动。变异检验是临时改一行、跑测试、改回来。

**Interfaces:**
- Consumes: Task 1 的 `decideBranchAction`、Task 2 的全部接线
- Produces: 无代码产出；产出是一份验收证据

- [ ] **Step 1: 确认起点是干净的绿**

```bash
git status --porcelain && go test ./internal/agentd/ -count=1
```

Expected: `git status --porcelain` 无输出（Task 2 已提交），测试 ok。

- [ ] **Step 2: 注入变异——删掉第一道闸**

在 `internal/agentd/manager.go` 的 `decideBranchAction` 中，把开头这三行**临时**注释掉：

```go
	// if recordedTip == "" {
	// 	return branchKeepNotOurs
	// }
```

- [ ] **Step 3: 跑测试，必须翻红**

```bash
go test ./internal/agentd/ -count=1
```

Expected: **FAIL**。至少 `TestDecideBranchAction/用户自带分支：尖端取不到（悬空_symref）` 必须失败，报 `= branchKeepTipUnknown，期望 branchKeepNotOurs`。

**这一步如果是绿的，说明这次改动没有达成目的——停下来，不要继续，回到 Task 1 检查表里第二行用例是否真的构造了 `recordedTip == "" && tipErr != nil`。**

把失败输出原样记下来，它是验收证据。

- [ ] **Step 4: 恢复变异，确认转绿**

把 Step 2 注释掉的三行恢复原样，然后：

```bash
git diff --exit-code && go test ./internal/agentd/ -count=1
```

Expected: `git diff --exit-code` 无输出且退出码 0（证明确实恢复到了提交状态，没有残留的临时改动），测试 ok。

- [ ] **Step 5: 跑全套闸门**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
```

```bash
go test -race ./cmd/ ./internal/agentd/ ./internal/store/ -count=1
```

```bash
GOOS=windows GOARCH=amd64 go build ./...
```

Expected: `gofmt -l .` 无输出；其余全部通过。

- [ ] **Step 6: 汇总验收证据**

对照 spec §7 逐条确认，把每条的实际命令与输出写进完工报告：

1. `TestDecideBranchAction` 五行全过
2. 现有 7 条 `TestCompensate*` 未改动（`git diff` 里 `manager_test.go` 只有新增，无删改）且全过
3. 变异检验：删掉 `recordedTip == ""` 分支 → 翻红（附 Step 3 的失败输出）；恢复后转绿
4. 全套闸门绿

第 2 条用这条命令自查：

```bash
git diff main -- internal/agentd/manager_test.go | grep '^-[^-]' || echo "无删改行，只有新增"
```

Expected: 输出「无删改行，只有新增」。

---

## Self-Review

**1. Spec 覆盖检查**

| Spec 章节 | 对应 task |
|---|---|
| §3.1 `branchTip` 如实报告失败 | Task 2 Step 2 |
| §3.2 新增纯决策函数 | Task 1 Step 3 |
| §3.3 `deleteCreatedBranch` 改写 | Task 2 Step 4 |
| §3.4 记录侧三处调用点 | Task 2 Step 2（`recordNewBranchTip`）+ Step 3（三处调用） |
| §4.1 表驱动单测 | Task 1 Step 1 |
| §4.2 回归保护（7 条不改） | Task 2 Step 1、Step 5、Task 3 Step 6 |
| §4.3 变异检验 | Task 3 Steps 2-4 |
| §5 日志与注释要求 | Task 1 Steps 5-6、Task 2 Steps 6-7 |
| §6 边界（明确不做） | Global Constraints |
| §7 验收标准 | Task 3 Steps 5-6 |

无遗漏。

**2. 与 spec 的一处刻意偏离**

§3.4 展示的是把「取尖端 + 失败告警」内联展开到三处调用点。本计划改为抽出 `recordNewBranchTip`，行为完全一致，理由是避免同一段五行逻辑抄三遍。已在 Task 2 Step 3 处注明。

**3. 占位符扫描**

无 TBD / TODO / 「类似 Task N」/「加适当的错误处理」。每个代码步骤都给了可直接粘贴的完整代码；每个测试步骤都给了确切命令与期望输出。

**4. 类型一致性**

- `branchAction` / `branchDelete` / `branchKeepNotOurs` / `branchKeepTipUnknown` / `branchKeepTipMoved`：Task 1 定义，Task 1 测试与 Task 2 switch 中拼写一致 ✓
- `decideBranchAction(recordedTip, currentTip string, tipErr error) branchAction`：Task 1 定义，Task 2 调用处实参顺序为 `(ws.NewBranchTip, cur, tipErr)`，与形参语义一致 ✓
- `branchTip` 新签名 `(string, error)`：Task 2 中全部四个调用点（`recordNewBranchTip` 一处 + `deleteCreatedBranch` 一处，加上三处记录点已改为走 `recordNewBranchTip`）均已适配 ✓
- `recordNewBranchTip(ctx, repo, branch) string`：定义与三处调用签名一致 ✓
- `truncateRunes` / `gitRun` / `log()` / `m.log` 均为既有符号，未改动 ✓
