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

# B100 实现计划：把「回合失败」与「执行失败」在事件层面分开

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增事件类型 `turn_failed` 表示「回合失败但任务仍在 `waiting_review`」，

# B97 实现计划：事件与状态失配的对账扫描

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消除「有 `failed` 事件、任务却不在终态」这种破损中间态：一是把制造它的两处改成「先迁状态后追加事件」，二是给看门狗加一条兜底对账扫描。

**Architecture:** 不改状态机。根因修在 `Manager.Stop` 与 `reconcileExecutorGone`；保险丝是 `watchdog.go` 新增的 `scanStateMismatch`，判据三条护栏缺一不可，动作走 `Manager.transit`（不裸调 `UpdateTaskState`）。

**Tech Stack:** Go 1.26。

**设计依据：** `docs/superpowers/specs/2026-08-15-b97-state-event-mismatch-scan-design.md`
（**开工前完整读一遍**，尤其 §1 的前提、§3.2 的三条护栏、§5 的风险）。

## Global Constraints

- **不改状态机、不加新状态、不新增事件类型。**
- **自动迁移必须走 `Manager.transit`**，不许裸调 `st.UpdateTaskState`——终态收口的工单作废与审计留痕（B63）挂在 `transit` 上。
- **三条护栏（最新事件 / 年龄 ≥30s / 本次启动之后）一条都不许省**，每条都要有对应用例。
- 扫描**不清扫进程、不杀 executor**。
- 两边已有的日志调用一条都不能丢；新增分支按 `instrumenting-code` 补日志（扫描每次动手都要有一条 Warn 级记录，含 taskID、原始事件 seq、原状态）。
- 提交前缀 `feat(b97):` / `fix(b97):`；**不合并进长期分支、不 push 到 `w4-delivery`/`main`**；**不动 `~/.handoff`**。
- 本次不做 B101 / B105 / B107 / B108。

---

### Task 1: 根因——两处改成「先迁状态、后追加事件」

**Files:** Modify `internal/agentd/manager.go`（`Stop`）、`internal/agentd/reconcile.go`（`reconcileExecutorGone`）；Test 各自的 `_test.go`

- [ ] **Step 1: 写失败的测试**

两条同构用例，思路是**让 `AppendEvent` 失败，断言状态仍然迁成功**（旧写法此时状态不会迁）。
若 store 不便注入失败，改为断言**调用顺序**：用 store 的事件钩子（`SetEventHook`）在事件落库那一刻回读任务状态，断言此刻**状态已经是目标态**。

```go
// TestStopTransitsBeforeEvent 钉死顺序：事件落库那一刻，状态必须已经就位。
// 反过来（先事件后状态）一旦第二步失败，就留下「事件说终结了、状态还是 running」
// 的破损中间态——协调者对它做任何操作都会被状态机拒，只能干等到 2h stalled。
func TestStopTransitsBeforeEvent(t *testing.T) { /* … */ }

// TestReconcileTransitsBeforeEvent 同上，对 reconcileExecutorGone。
func TestReconcileTransitsBeforeEvent(t *testing.T) { /* … */ }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -count=1 -run 'TransitsBeforeEvent' ./internal/agentd/`
Expected: FAIL。

- [ ] **Step 3: 改实现**

两处都调换顺序，并在原处补注释说明**代价**：反转后失败形态变成「状态对、事件缺」，
这比「状态错」轻——状态错会让协调者对着假的 running 干等且 CLI 全线拒绝操作。
注意 `reconcileExecutorGone` 迁的是 `waiting_review`（`recoverTransit`），不是 `failed`，别改错目标。

- [ ] **Step 4: 跑测试 + 全包回归**

Run: `go test -count=1 ./internal/agentd/`
Expected: 全 PASS。既有用例若因顺序变化变红，先判断它断言的是不是被本次推翻的旧顺序；是则随新契约更新并在 ledger 记一行。

- [ ] **Step 5: Commit**

```bash
git add internal/agentd/
git commit -m "fix(b97): Stop 与对账改成先迁状态后追加事件，不再留下破损中间态"
```

---

### Task 2: `scanStateMismatch` 的判据（纯函数，先不接线）

**Files:** Modify `internal/agentd/watchdog.go`；Test `internal/agentd/watchdog_test.go`

**Interfaces:**
- Produces：
  ```go
  // mismatchVerdict 判断一个任务是否处于「有 failed 事件、状态却非终态」的破损中间态。
  //
  // 参数：
  //   - state: 任务当前状态
  //   - latest: 该任务的最新一条事件（nil 表示没有事件）
  //   - now: 当前时刻（测试注入）
  //   - startedAt: 本次 agentd 启动时刻
  //   - minAge: 事件最小年龄（防抢，生产取 30s）
  //
  // 返回：true 表示应当把状态补成 failed
  func mismatchVerdict(state proto.TaskState, latest *proto.Event, now, startedAt time.Time, minAge time.Duration) bool
  ```

- [ ] **Step 1: 写失败的测试（六条，逐条对应 spec §6 第 2 点）**

```go
// 1. 最新事件 failed + running + 事件 60s 前 → true
// 2. 同上但事件只有 10s → false（防抢：Stop/对账正常执行时也会短暂处在中间态）
// 3. 最新事件是 progress（历史上有 failed）→ false
// 4. 最新事件是 turn_failed + waiting_review → false
//    这条是**防误伤的正身**：turn_failed + waiting_review 是健康态，任务正等着
//    协调者裁决，挂三天都正常，扫描一根手指都不许碰
// 5. failed 事件产生于 agentd 启动之前 → false
//    为什么：B100 之前的历史数据里存在**合法的** failed + waiting_review，
//    没这条护栏，升级后会把正等着裁决的存量任务直接判死
// 6. 任务已是终态 → false
```

- [ ] **Step 2: 跑测试确认失败** — `go test -count=1 -run TestMismatchVerdict ./internal/agentd/`

- [ ] **Step 3: 实现纯函数**

注释里必须写明：**本判据依赖 `failed` 只用于任务终结**（B100 及其补漏才使之成立）——
谁再让 `failed` 用于非终态，这个扫描会当场开始误伤。

- [ ] **Step 4: 跑测试确认六条全过**

- [ ] **Step 5: Commit** — `git commit -m "feat(b97): 失配判据纯函数与六条护栏用例"`

---

### Task 3: 接进看门狗并执行迁移

**Files:** Modify `internal/agentd/watchdog.go`、`cmd/agentd.go`（接线）；Test `internal/agentd/watchdog_test.go`

- [ ] **Step 1: 写失败的测试**

```go
// TestScanStateMismatchTransitsAndAudits：失配任务被迁到 failed，
// 且**挂起工单已被作废**（证明走的是 transit 而不是裸 UpdateTaskState），
// 且留下一条 progress 审计，文本含原始事件 seq。
func TestScanStateMismatchTransitsAndAudits(t *testing.T) { /* … */ }

// TestScanStateMismatchLeavesHealthyTaskAlone：turn_failed + waiting_review 不动。
func TestScanStateMismatchLeavesHealthyTaskAlone(t *testing.T) { /* … */ }
```

- [ ] **Step 2: 跑测试确认失败**

- [ ] **Step 3: 实现**

- 扫描签名与既有 `scanTaskProcs` 同风格；`transit` 以回调注入（生产在 `cmd/agentd.go` 传 `mgr.transit` 的包装，与 `sweep` 的接线同款）。
- 每次动手打一条 **Warn**：`taskID`、原始事件 `seq`、原状态、事件年龄。
- 迁移失败只记 Error 不重试（下一轮 tick 会再来）。
- 接进 `RunWatchdog` 的 tick 里，排在 `scanStalled` 之后。

- [ ] **Step 4: 跑测试 + 全包回归**

- [ ] **Step 5: Commit** — `git commit -m "feat(b97): 看门狗接入失配对账扫描，迁移走 transit 并留审计"`

---

### Task 4: ledger 与整分支自检

**Files:** Create `docs/superpowers/notes/2026-08-15-b97-ledger.md`

- [ ] **Step 1: 全量回归** — `go build ./... && go vet ./... && gofmt -l ./internal ./cmd && go test -count=1 ./...`（gofmt 必须无输出）
- [ ] **Step 2: 在 ledger 里逐条列出三条护栏各自对应哪条用例**，缺一即为未完成
- [ ] **Step 3: 如实记下没做的部分**：本扫描兜不住「续接回合正常完成但结果被吞」（库里只有 stalled，扫描无从知道回合其实跑过）——**它是保险丝不是根治**
- [ ] **Step 4: Commit** — `git commit -m "docs(b97): ledger 与自检结论"`
