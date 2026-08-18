# B119 清扫的全有全无 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `prochost.Sweep` 在 executor 存活时降级为名册点名而不是整体放弃，把清扫挂到终态迁移的统一收口点，并让 watchdog 硬上限档先停源头再清扫、停不掉就不谎报回收。

**Architecture:** 三层改动自底向上：①`internal/prochost` 把 `Sweep` 的两段拆开决策；②`internal/agentd` 把清扫从 `handleResult` 挪到 `Manager.transit` 的终态分支，并新增 `Manager.ForceReclaim` 作为「停 executor + 落 failed」的共用入口；③`internal/agentd/watchdog.go` 的硬上限档改走 `ForceReclaim`，接线在 `cmd/agentd.go`。

**Tech Stack:** Go 1.26，标准库 `testing`，项目内既有测试桩（`stubAlive` / `stubKillGroup` / `stubKillProc` / `stubEnum` / `writeRoster` / `newTestManager` / `createRunningTask`）。

**Spec:** `docs/superpowers/specs/2026-08-17-b119-sweep-refusal-and-terminal-cleanup-design.md`

## Global Constraints

- 日志一律用所在包的既有入口：`internal/prochost` 用 `log()`，`internal/agentd` 用 `m.log` / 传入的 `log *slog.Logger`。**禁止 `fmt.Printf`**。
- 新增导出函数必须有 doc 注释（参数、返回、注意事项）；非显然分支必须有中文「为什么」注释。
- 不改 `aliveFn` 的存活判据本身；不给 `Sweep` 加绕过存活保护的强杀开关；不改 B72 名册写入侧；不改 `task_budget` / `task_hard_limit` 的默认值（400 / 1200）。
- `RecoverOnStartup` 的 `sweep func(taskID string)` 参数与其调用点**保持不动**——它迁的是 `waiting_review`（非终态）且 executor 确已不在。
- 每个 task 完成即 commit，提交信息用各 Task「Commit」步骤里给定的原文。
- 运行测试统一用 `-count=1`（禁缓存假绿）。

---

### Task 1: `Sweep` 分段决策——活 executor 降级为名册点名

**Files:**
- Modify: `internal/prochost/footprint.go:200-231`（`ErrExecutorAlive` 文案与 doc、`Sweep` 头部分支）
- Test: `internal/prochost/footprint_test.go`（改 1 条既有用例，加 2 条新用例）

**Interfaces:**
- Consumes: 无（本包自足）
- Produces: `func Sweep(h Handle) (killed int, v Verdict, err error)` 签名不变；语义变更——`err == ErrExecutorAlive` 从「什么都没做」改为「段①跳过、段②已执行」，此时 `killed` 为段②实际回收数，**可以非 0**。Task 3 的 `SweepTaskProcs` 分诊依赖这条。

- [ ] **Step 1: 改写既有用例 `TestSweepRefusesWhenExecutorAlive`**

这条用例今天断言「存活 → 一个都不杀」，改后语义是「存活 → 不按组杀，但名册照点」。改名并重写（`internal/prochost/footprint_test.go:185`）：

```go
// TestSweepAliveSkipsGroupPhaseButStillRosterKills 验证 executor 存活时的降级形态：
// 段①（按 pgid 整组杀）必须跳过——它会连 executor 本体一起端掉；段②（按出生名册
// 点名）照常执行——每条成员自带 pid+出生时刻双重凭据，与 executor 的死活无关。
//
// 这条是 B119 的核心：改前两段一起放弃，导致「回合结束收掉 setsid 逃逸后代」这个
// 唯一目的从未达成（生产日志 118 次拒绝，真正跑完的清扫仅 34 次）。
func TestSweepAliveSkipsGroupPhaseButStillRosterKills(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{
		{PID: 501, StartedAt: 5100},
		{PID: 502, StartedAt: 5200},
	}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	killed := stubKillProc(t)
	groupN := stubKillGroup(t, nil)
	stubEnum(t, []procEntry{
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100},
		{PID: 502, PPID: 1, PGID: 502, StartedAt: 5200},
	}, nil)

	n, v, err := Sweep(Handle{PID: 100, StartedAt: t0, RosterPath: roster})
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("执行者存活时应返回 ErrExecutorAlive 表示段①跳过，got %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 2 {
		t.Fatalf("段②应回收 2 个名册成员，实得 %d", n)
	}
	if len(*killed) != 2 {
		t.Fatalf("应逐个发信号回收 2 条，实得 %v", *killed)
	}
	if *groupN != 0 {
		t.Fatalf("执行者存活时绝不能按组杀，实得组信号 %d 次", *groupN)
	}
}
```

- [ ] **Step 2: 加用例——名册混入 executor 本体的 pid 时必须跳过**

追加到 `internal/prochost/footprint_test.go`：

```go
// TestSweepAliveNeverSignalsExecutorItself 是本次唯一新增的误杀面的守门用例：
// 段②降级执行后，若名册里因任何原因含有 executor 本体的 pid，逐个发信号就会
// 杀掉一个活着的 executor——这正是段①被跳过所要避免的事。判据是 h.PID，
// 不依赖名册内容的正确性。
func TestSweepAliveNeverSignalsExecutorItself(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{
		{PID: 100, StartedAt: t0},    // executor 本体
		{PID: 501, StartedAt: 5100},  // 正常逃逸后代
	}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	killed := stubKillProc(t)
	stubKillGroup(t, nil)
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100},
	}, nil)

	n, _, err := Sweep(Handle{PID: 100, StartedAt: t0, RosterPath: roster})
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("应返回 ErrExecutorAlive，got %v", err)
	}
	if n != 1 {
		t.Fatalf("应只回收 501 这一条，实得 %d", n)
	}
	for _, pid := range *killed {
		if pid == 100 {
			t.Fatalf("对 executor 本体 pid=100 发了信号，名单 %v", *killed)
		}
	}
}
```

- [ ] **Step 3: 加用例——无名册时存活即空跑，不报错**

```go
// TestSweepAliveWithoutRosterIsNoop 覆盖降级路径的下界：没有名册（升级前建的任务、
// 或 shim 还没来得及落第一次名册就死了）时段②无事可做，段①仍被跳过——结论必须是
// 「回收 0 个」而不是 panic 或误判为失败。
func TestSweepAliveWithoutRosterIsNoop(t *testing.T) {
	stubAlive(t, true)
	killed := stubKillProc(t)
	groupN := stubKillGroup(t, nil)
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)

	n, v, err := Sweep(Handle{PID: 100, StartedAt: t0}) // RosterPath 为空
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("应返回 ErrExecutorAlive，got %v", err)
	}
	if v != VerdictOK || n != 0 {
		t.Fatalf("无名册时应回收 0 个且 verdict ok，实得 n=%d v=%s", n, v)
	}
	if len(*killed) != 0 || *groupN != 0 {
		t.Fatalf("无名册时不该发任何信号，实得逐个 %v / 组 %d 次", *killed, *groupN)
	}
}
```

- [ ] **Step 4: 跑测试确认失败**

Run: `go test -run 'TestSweepAlive' ./internal/prochost/ -count=1 -v`
Expected: 三条全 FAIL。前两条失败在 `n != 2` / `n != 1`（改前 `Sweep` 存活即 `return 0`），第三条会因 `errors.Is` 通过但断言 `n==0` 也通过而**意外 PASS**——这是正常的，它守的是回归而非当前缺陷。

- [ ] **Step 5: 改 `Sweep` 的头部分支**

把 `internal/prochost/footprint.go:227-230` 的整体返回改为降级执行：

```go
func Sweep(h Handle) (killed int, v Verdict, err error) {
	if aliveFn(h) {
		// 执行者存活时**只跳过段①**，不整体放弃：段①按 pgid 整组发信号，会连
		// executor 本体一起端掉；段②按出生名册逐个点名，每条成员自带 pid+出生
		// 时刻双重凭据，杀的是 setsid 逃逸出去的后代，与 executor 死活无关。
		//
		// 改前两段一起放弃，使得「回合结束收掉逃逸后代」这个唯一目的从未达成
		// （B119：生产日志 118 次拒绝、真正跑完的清扫仅 34 次）。
		procs, eerr := enumProcsFn()
		if eerr != nil {
			log().Error("降级清扫前枚举进程失败", "pid", h.PID, "cause", eerr)
			return 0, VerdictNoCredential, eerr
		}
		n := rosterKill(h, procs)
		log().Info("执行者存活，跳过组清扫，转入点名回收",
			"pid", h.PID, "lock", h.LockPath, "killed", n)
		return n, VerdictOK, ErrExecutorAlive
	}
	procs, eerr := enumProcsFn()
	// ……以下不变
```

- [ ] **Step 6: 在 `rosterKill` 里跳过 executor 本体**

`internal/prochost/footprint.go` 的 `rosterKill` 循环体开头（`for _, e := range entries {` 之后第一件事）插入：

```go
		if e.PID == h.PID {
			// 段②降级执行时（executor 仍存活）新出现的误杀面：名册理论上只记
			// 后代、不含 executor 本体，但这道判据成本为零，而代价是杀掉一个
			// 活着的 executor——正是跳过段①所要避免的事。判据用 h.PID 而非
			// 名册内容，不依赖名册的正确性。
			log().Warn("名册含执行者本体，跳过", "pid", e.PID)
			continue
		}
```

- [ ] **Step 7: 更新 `ErrExecutorAlive` 的文案与全部相关注释**

`footprint.go:200-203`：

```go
// ErrExecutorAlive 表示执行者仍然活着，Sweep 已降级为只做名册点名（跳过组清扫）。
//
// **不是「什么都没做」**：此时返回的 killed 是段②实际回收的逃逸后代数，可以非 0。
// 调用方靠 errors.Is 判别，禁止按错误文本判——与 ErrLockHeld / ErrStillAlive 同款。
var ErrExecutorAlive = errors.New("执行者仍存活，Sweep 降级为点名回收")
```

同时改 `Sweep` 的 doc（`footprint.go:205-226`）两处：标题行「回收一个**已死**执行者留下的残留后代」改为「回收一个执行者留下的残留后代（执行者仍存活时降级为只做名册点名）」；`返回` 段的 `err` 一条改为「执行者仍存活（ErrExecutorAlive，此时段②已执行、killed 可非 0）、枚举失败、或已发信号但复核仍存活（ErrStillAlive）」；`注意` 段第一条「**前提是执行者已死**……直接拒绝」改为「**存活时降级**：存活锁仍被持有时跳过组清扫（杀活着的执行者是 Kill 的职责），但名册点名照做」。

- [ ] **Step 8: 加日志（关键节点）**

本 task 的日志点已在 Step 5/6 的代码里就位，逐条核对：
- 降级分支入口：Info「执行者存活，跳过组清扫，转入点名回收」带 `pid` / `lock` / `killed`（**成功路径不静默**）
- 降级路径的枚举失败：Error「降级清扫前枚举进程失败」带 `pid` / `cause`
- 跳过 executor 本体：Warn「名册含执行者本体，跳过」带 `pid`（该分支出现即值得追查）

- [ ] **Step 9: 跑测试确认通过**

Run: `go test ./internal/prochost/ -count=1`
Expected: PASS（含既有的 `TestSweepKillsRosterMembers` / `TestSweepSkipsRosterMemberWithMismatchedBirth` / `TestSweepAbortsOnLeaderReuse` 等全部不受影响）

- [ ] **Step 10: Commit**

```bash
git add internal/prochost/footprint.go internal/prochost/footprint_test.go
git commit -m "fix(prochost): Sweep 分段决策——执行者存活时降级为名册点名而非整体放弃（B119 §2.1）"
```

---

### Task 2: 清扫挂到 `Manager.transit` 的终态分支

**Files:**
- Modify: `internal/agentd/manager.go:2644-2646`（`transit` 的 `IsTerminal` 块）
- Modify: `internal/agentd/manager.go:2557-2576`（删掉 `handleResult` 末尾的 `m.sweep` 与其注释）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Sweep` 语义（存活时也会真的回收逃逸后代）
- Produces: 「任何路径迁入终态即清扫」这一不变式。Task 3 的 `ForceReclaim` 依赖它——`ForceReclaim` 自己**不调** `m.sweep`。

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/manager_test.go`：

```go
// TestTransitToTerminalSweeps 钉住 B119 的不变式：清扫挂在终态迁移这一个点上，
// 而不是散落在各条终态路径里。B63 的工单作废已经因为同样的理由挂在这里——
// B93 的清扫是同一个错误的下一次复发（只加在 handleResult，Stop 路径漏了）。
func TestTransitToTerminalSweeps(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	if err := m.transit(taskID, proto.TaskStateFailed, "test"); err != nil {
		t.Fatalf("transit: %v", err)
	}
	if len(swept) != 1 || swept[0] != taskID {
		t.Fatalf("迁入终态应清扫一次该任务，实得 %v", swept)
	}
}

// TestTransitToNonTerminalDoesNotSweep 反向守门：非终态迁移不得触发清扫，
// 否则每次 running→waiting_review 都要枚举一遍进程表。
func TestTransitToNonTerminalDoesNotSweep(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	if err := m.transit(taskID, proto.TaskStateWaitingReview, "test"); err != nil {
		t.Fatalf("transit: %v", err)
	}
	if len(swept) != 0 {
		t.Fatalf("非终态迁移不该清扫，实得 %v", swept)
	}
}

// TestStopSweepsViaTransit 与 TestDoneSweepsViaTransit 是本 task 的**目的本身**：
// 光测 transit 不够——B93 的清扫在单元测试里也是绿的，漏的是「Stop 这条路径根本
// 没接上」。这两条从真实入口进，钉住的是接线而不是函数体。
func TestStopSweepsViaTransit(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	if _, err := m.Stop(context.Background(), taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(swept) != 1 || swept[0] != taskID {
		t.Fatalf("stop 落终态应清扫一次，实得 %v", swept)
	}
}

func TestDoneSweepsViaTransit(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	seedWaitingReviewTask(t, st, "done-sweep")
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	if err := m.Done(context.Background(), "done-sweep", "验收通过"); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if len(swept) != 1 || swept[0] != "done-sweep" {
		t.Fatalf("done 归档应清扫一次，实得 %v", swept)
	}
}

// TestTransitIdempotentDoesNotSweepTwice 幂等分支（当前状态已是目标状态）在
// UpdateTaskState 之前就 return，不得重复清扫——与工单作废同款语义。
func TestTransitIdempotentDoesNotSweepTwice(t *testing.T) {
	m, taskID := newManagerWithRunningTask(t)
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	if err := m.transit(taskID, proto.TaskStateFailed, "test"); err != nil {
		t.Fatalf("首次 transit: %v", err)
	}
	if err := m.transit(taskID, proto.TaskStateFailed, "test again"); err != nil {
		t.Fatalf("重复 transit 应幂等返回 nil: %v", err)
	}
	if len(swept) != 1 {
		t.Fatalf("重复迁入同一终态只该清扫一次，实得 %d 次：%v", len(swept), swept)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -run 'TestTransit' ./internal/agentd/ -count=1 -v`
Expected: `TestTransitToTerminalSweeps` FAIL（`实得 []`）；另两条 PASS（改前也不清扫）。

- [ ] **Step 3: 在 `transit` 的终态块里加清扫**

`internal/agentd/manager.go:2644` 的 `if to.IsTerminal()` 块改为：

```go
	if to.IsTerminal() {
		voidTicketsWithAudit(m.st, taskID, reason, m.log)
		// 终态即清扫（B119）：与上面的工单作废同一个理由——挂在这里才能覆盖
		// **将来新增的**终态路径。B93 把清扫只加在 handleResult 末尾，Stop 这条
		// 终态路径就漏了，标题写的「落终态即清扫」实际只做到了「回合终态」。
		//
		// 排在作废之后：作废是协调者可见的状态语义，清扫是 best-effort 善后
		// （SweepTaskProcs 每个失败分支只记日志或发 orphan_risk，从不返回错误），
		// 不该挡在语义收口前面。
		m.sweep(taskID)
	}
```

- [ ] **Step 4: 删掉 `handleResult` 末尾的清扫**

删除 `internal/agentd/manager.go:2557-2576` 的整段注释与 `m.sweep(taskID)` 调用（从「// 回合结束即清扫这一回合留下的孤儿后代。」到 `m.sweep(taskID)`）。`handleResult` 以 `m.hub.Publish(evt)` 收尾。

**为什么能删**：`handleResult` 通过 `transitToReview` 迁入的是 `waiting_review`（非终态），改前那处清扫本就不是「终态清扫」；真正的终态清扫现在由 `transit` 统一负责。回合结束的逃逸后代由 Task 1 的降级路径在**下一次终态迁移**时收——若要保留「每回合都收」的行为，见下方 Step 5 的说明。

- [ ] **Step 5: 保留「回合结束也清扫」的行为**

删掉的那处清扫有独立价值（回合结束就收掉这一回合的逃逸后代，不必等到任务终结）。在 `transitToReview` 成功之后补回，语义写清楚：

```go
// 在 handleResult 中 m.hub.Publish(evt) 之后：
	// 回合结束（非终态）也清扫这一回合留下的逃逸后代：不必等到任务终结才收。
	// 与 transit 的终态清扫不重复——那条管终态，这条管回合边界。
	// executor 此时通常仍存活（serve 常驻模型），走 Sweep 的降级路径只做名册
	// 点名（B119 §2.1），executor 本体不会被误杀。
	m.sweep(taskID)
```

放在 `m.hub.Publish(evt)` 之后（事件先落库并广播，审核者的 wait 第一时间醒；清扫是善后，不该挡在唤醒前面）。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test -run 'TestTransit|TestHandleResultSweeps' ./internal/agentd/ -count=1 -v`
Expected: 全 PASS（含既有的 `TestHandleResultSweepsProcsOnFail` / `OnSuccess`——它们断言的是 `handleResult` 后有清扫，Step 5 保留了这条行为）。

- [ ] **Step 7: 加注释（意图）**

本 task 的注释已在 Step 3/5 的代码里就位，核对三处：
- `transit` 终态块：为什么挂在这里（覆盖将来新增路径）、为什么排在作废之后
- `handleResult` 保留的清扫：为什么与终态清扫不重复、为什么 executor 存活也值得调
- 既有 `TestHandleResultSweepsProcsOnFail` 上方注释里那句「Sweep 遇到它仍存活会返回 ErrExecutorAlive 并自行放弃——这条保护是既有的」**必须改**：改后是「降级为名册点名，executor 本体不被误杀但逃逸后代会被收」。旧措辞正是 B119 的误读源头，留着会误导下一个人。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(agentd): 清扫挂到 transit 的终态分支，覆盖将来新增的终态路径（B119 §2.2）"
```

---

### Task 3: `Manager.ForceReclaim`——停 executor 与落 failed 的共用入口

**Files:**
- Modify: `internal/agentd/reconcile.go:72-92`（`stopExecutor` 加 error 返回）
- Modify: `internal/agentd/reconcile.go:252-268`（`SweepTaskProcs` 的 `ErrExecutorAlive` 分诊）
- Modify: `internal/agentd/manager.go:1201-1206`（`Stop` 忽略新返回值）
- Create: `internal/agentd/forcereclaim.go`
- Test: `internal/agentd/forcereclaim_test.go`

**Interfaces:**
- Consumes: Task 2 的不变式——`m.transit(taskID, Failed, reason)` 会自动清扫并作废工单，`ForceReclaim` **不得**自己再调 `m.sweep`
- Produces:
  - `func (m *Manager) stopExecutor(taskID string, ad executor.Adapter) error`（签名变更，原为无返回）
  - `func (m *Manager) ForceReclaim(taskID, reason string) error`——成功返回 nil 且任务已落 `failed`；停不掉时返回非 nil 且**任务状态不变**。Task 4 的 watchdog 按这个契约做边沿告警。

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/forcereclaim_test.go`：

```go
package agentd

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// stopErrAdapter 是一个 Stop 恒定失败的 adapter，用来复现「executor 杀不掉」。
type stopErrAdapter struct {
	executor.Adapter
	err error
}

func (a *stopErrAdapter) Stop(taskID string) error { return a.err }

// TestForceReclaimSuccessTransitsFailed 验证成功路径：executor 停掉之后任务落
// failed，理由原样进事件。清扫由 transit 的终态分支负责（B119 §2.2），本方法
// 不自己调 sweep——重复调用会让一次强制回收枚举两遍进程表。
func TestForceReclaimSuccessTransitsFailed(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "runaway-ok"
	// 显式造一个**带 managed worktree** 的任务：createRunningTask 造出来的
	// WorkDir 为空，拿它断言「worktree 未被删」会恒真，等于没测。
	workDir := t.TempDir()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: taskID, Target: "local", State: proto.TaskStatePending,
		WorkDir: workDir, WorktreeManaged: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil {
		t.Fatalf("置为 running: %v", err)
	}
	var swept []string
	m.sweepProcs = func(id string) { swept = append(swept, id) }

	reason := "任务进程数 1300 超过硬上限 1200，已强制回收"
	if err := m.ForceReclaim(taskID, reason); err != nil {
		t.Fatalf("ForceReclaim: %v", err)
	}
	cur, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateFailed {
		t.Fatalf("停掉之后任务应落 failed，实得 %s", cur.State)
	}
	if len(swept) != 1 {
		t.Fatalf("终态迁移应触发一次清扫，实得 %d 次", len(swept))
	}
	evs := mustEvents(t, st, taskID)
	if !hasEventWithText(evs, proto.EventTypeFailed, reason) {
		t.Fatalf("failed 事件应带真实理由 %q，实得 %v", reason, evs)
	}
	// worktree 必须还在：删 worktree 是不可逆且外部可见的动作，1200 进程这种
	// 现场最需要留证。handoff stop 由人敲、删是人的决定；watchdog 自动触发的
	// 强制回收不继承这个决定（B119 §2.3）。
	if _, serr := os.Stat(workDir); serr != nil {
		t.Fatalf("强制回收不得删除 worktree，但 %s 已不可用：%v", workDir, serr)
	}
}

// TestForceReclaimKeepsTaskActiveWhenStopFails 是 B119 最重的一条：没收掉就不能
// 宣布收掉了。executor 杀不掉时任务必须留在活跃集，下一轮 watchdog 才会继续点名
// 与重试；改前无论成败都落 failed，之后 IsTerminal 直接跳过它，那批进程从此
// 无人跟踪，而库里留着一条说「已强制回收」的假事件。
func TestForceReclaimKeepsTaskActiveWhenStopFails(t *testing.T) {
	ad := &stopErrAdapter{err: errors.New("已发 SIGKILL 但复核仍存活")}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	createRunningTask(t, st, "runaway")

	err := m.ForceReclaim("runaway", "任务进程数 1300 超过硬上限 1200，已强制回收")
	if err == nil {
		t.Fatal("停不掉 executor 时 ForceReclaim 必须返回错误")
	}
	cur, gerr := st.GetTask("runaway")
	if gerr != nil {
		t.Fatalf("GetTask: %v", gerr)
	}
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("停不掉时任务必须保持活跃，实得 %s", cur.State)
	}
	evs := mustEvents(t, st, "runaway")
	if hasEvent(evs, proto.EventTypeFailed) {
		t.Fatalf("没收掉就不该落 failed 事件：%v", evs)
	}
}
```

`hasEventWithText` 包内不存在，在本文件内实现（与 `approver_test.go:208` 的 `hasEvent` 同款风格，多一层 payload 文本匹配）：

```go
// hasEventWithText 判断事件列表里是否存在指定类型、且 payload 含指定子串的事件。
// 按 payload 原始 JSON 匹配：failed 事件的理由在 payload.fail_reason 里，
// 逐层解结构体只会让断言跟着 payload 形态漂移。
func hasEventWithText(evs []proto.Event, typ proto.EventType, want string) bool {
	for _, e := range evs {
		if e.Type == typ && strings.Contains(string(e.Payload), want) {
			return true
		}
	}
	return false
}
```

import 里需要 `"strings"`。`mustEvents`（`approver_test.go`）/ `newTestManagerWithAds`（`manager_test.go:143`）/ `createRunningTask` / `newManagerWithRunningTask`（`manager_test.go:2052`）是既有夹具，直接用。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -run 'TestForceReclaim' ./internal/agentd/ -count=1 -v`
Expected: 编译失败 `m.ForceReclaim undefined`。

- [ ] **Step 3: 给 `stopExecutor` 加 error 返回**

`internal/agentd/reconcile.go:72`，签名与三条 return 改为：

```go
func (m *Manager) stopExecutor(taskID string, ad executor.Adapter) error {
	m.noteStopping(taskID)
	err := ad.Stop(taskID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：兜底回收对它无意义——
		// 真去 kill 进程反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		// 唯独「已发 SIGKILL 但复核仍存活」要惊动人：这是唯一一种不提示就会
		// 留下长期孤儿的失败（B20 现场存活了 11.5 小时，正是因为完全静默）。
		// 其余 Stop 失败五花八门（ctx 取消、内部状态不一致），全发事件等于
		// 把协调者淹了，那样这条提示就没人看了。
		if errors.Is(err, prochost.ErrStillAlive) {
			m.notifyOrphanRisk(taskID, fmt.Sprintf(
				"executor 进程可能残留（已发 SIGKILL 但复核仍存活），"+
					"请先 handoff status 确认，再 handoff stop %s 回收（原因：%v）", taskID, err))
		}
		return err
	}
	// ……以下 reaper 分支不变，末尾 return nil
```

reaper 分支（`rp, ok := ad.(reaper)` 起）保持原逻辑，函数末尾补 `return nil`。

- [ ] **Step 4: `Stop` 显式忽略新返回值**

`internal/agentd/manager.go:1205` 改为：

```go
		// Stop 是协调者主动敲的：executor 杀不掉也要把任务落 failed 并作废工单
		// ——人已经决定不要这个任务了。与 ForceReclaim 相反（那条是 watchdog
		// 自动触发，没收掉就不能宣布收掉，见 forcereclaim.go）。
		_ = m.stopExecutor(taskID, ad)
```

- [ ] **Step 5: 新建 `internal/agentd/forcereclaim.go`**

```go
// Package agentd 的强制回收入口。
//
// 职责：把「停 executor → 判成败 → 成功才落 failed」这三步收成一个方法，供
// watchdog 的硬上限档调用（B119 §2.3）。
// 边界：不删 worktree（那是 handoff stop 的人工决定，watchdog 自动触发不继承
// 它）；不自己清扫（清扫挂在 transit 的终态分支上，见 manager.go 的 transit）；
// 不做告警去重（边沿状态由调用方 watchdog 持有，见 scanTaskProcs）。
package agentd

import "fmt"

// ForceReclaim 强制回收一个失控任务：先停 executor，停掉了才把任务落 failed。
//
// 参数：
//   - taskID: 目标任务
//   - reason: 落 failed 时写进事件的理由，必须含可判断的真实数字（如实际进程数
//     与硬上限），审核者事后要凭它判断回收得对不对
//
// 返回：
//   - nil: executor 已停、任务已落 failed（清扫与工单作废由 transit 的终态收口
//     自动完成）
//   - 非 nil: 没停掉或状态迁移失败，**任务状态保持不变**——调用方应让它留在活跃
//     集里下一轮继续点名重试
//
// 注意：
//   - **顺序不可换**：想清掉一个正在 fork 的任务，必须先让 fork 的源头停下来；
//     杀子进程而留着父进程是打地鼠。这也是 B119 的根因——改前直接对活着的
//     executor 调 Sweep，段①被跳过后什么都没收，却照样宣布「已强制回收」
//   - 不删 managed worktree：1200 进程这种现场最需要留证，删 worktree 是不可逆
//     且外部可见的动作，留给协调者事后 handoff reclaim
func (m *Manager) ForceReclaim(taskID, reason string) error {
	m.log.Warn("强制回收进入", "task", taskID, "reason", reason)
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("强制回收解析执行者失败", "task", taskID, "cause", err)
		return fmt.Errorf("强制回收解析执行者: %w", err)
	}
	if serr := m.stopExecutor(taskID, ad); serr != nil {
		// 没收掉就不能宣布收掉：保持活跃态，让 watchdog 下一轮继续点名重试。
		// stopExecutor 内部已对 ErrStillAlive 发过 orphan_risk 提示。
		m.log.Error("强制回收失败：executor 未停止，任务保持活跃", "task", taskID, "cause", serr)
		return fmt.Errorf("强制回收停止 executor: %w", serr)
	}
	if terr := m.transit(taskID, proto.TaskStateFailed, reason); terr != nil {
		m.log.Error("强制回收落 failed 失败", "task", taskID, "cause", terr)
		return fmt.Errorf("强制回收落 failed: %w", terr)
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeFailed, newFailedPayload(reason, "", ""))
	if aerr != nil {
		m.log.Error("强制回收追加 failed 事件失败", "task", taskID, "cause", aerr)
		return fmt.Errorf("强制回收追加事件: %w", aerr)
	}
	m.hub.Publish(evt)
	m.log.Warn("强制回收完成", "task", taskID, "reason", reason)
	return nil
}
```

import 里补 `"github.com/Xsxdot/handoff/internal/proto"`。

- [ ] **Step 6: 改 `SweepTaskProcs` 对 `ErrExecutorAlive` 的分诊**

`internal/agentd/reconcile.go:254`，那条 case 改为：

```go
	case errors.Is(err, prochost.ErrExecutorAlive):
		// executor 仍存活 → Sweep 降级为只做名册点名（B119 §2.1）。改前这里打的是
		// 「交由常规回收路径」，而对失控任务而言那条路径并不存在；现在报告实际
		// 回收数，0 与非 0 都是有意义的结论。
		m.log.Info("执行者存活，已降级为点名回收", "task", taskID, "pid", h.PID, "killed", killed)
```

- [ ] **Step 7: 加日志（关键节点）**

核对 `ForceReclaim` 的日志覆盖（Step 5 代码内已就位）：
- 入口 Warn「强制回收进入」带 `task` / `reason`（这是不可逆动作，入口用 Warn 不用 Info）
- 三个错误分支各一条 Error，均带 `task` + `cause`
- 出口 Warn「强制回收完成」带 `task` / `reason`（**成功路径不静默**）

- [ ] **Step 8: 跑测试确认通过**

Run: `go test -run 'TestForceReclaim|TestStop|TestHandleResult' ./internal/agentd/ -count=1 -v`
Expected: 全 PASS。

- [ ] **Step 9: Commit**

```bash
git add internal/agentd/forcereclaim.go internal/agentd/forcereclaim_test.go internal/agentd/reconcile.go internal/agentd/manager.go
git commit -m "feat(agentd): ForceReclaim——先停源头再落 failed，停不掉不谎报回收（B119 §2.3）"
```

---

### Task 4: 硬上限档改走 `ForceReclaim` + 边沿告警 + 接线

**Files:**
- Modify: `internal/agentd/watchdog.go:84,97-98,103,119`（`sweep` 参数换成 `reclaim`）
- Modify: `internal/agentd/watchdog.go:271-341`（`scanTaskProcs` 的硬上限分支与签名）
- Modify: `internal/agentd/watchdog.go:342-367`（删除 `transitFailedWithEvent`）
- Modify: `cmd/agentd.go:186-187`（接线）
- Test: `internal/agentd/watchdog_taskprocs_test.go`、`internal/agentd/watchdog_test.go`（4 处调用适配）

**Interfaces:**
- Consumes: Task 3 的 `func (m *Manager) ForceReclaim(taskID, reason string) error`
- Produces: `func scanTaskProcs(st *store.Store, hub *Hub, budget, hardLimit int, fired, reclaimFailed map[string]bool, reclaim func(taskID, reason string) error, log *slog.Logger)`；`RunWatchdog` / `runWatchdog` 的 `sweep func(string)` 参数替换为 `reclaim func(taskID, reason string) error`（位置不变）

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/watchdog_taskprocs_test.go`：

```go
// TestScanTaskProcsHardLimitCallsReclaim 硬上限档必须走强制回收（先停 executor
// 再落 failed），而不是直接对活着的 executor 调 sweep——后者在 B119 之前恒被
// 拒绝，一个进程都不杀却照样宣布「已强制回收」。
func TestScanTaskProcsHardLimitCallsReclaim(t *testing.T) {
	st, hub := newTestStoreHub(t)
	mustCreateTaskState(t, st, "t1", proto.TaskStateRunning)
	setTaskProcCount(t, map[string]int{"t1": 1300})

	var gotTask, gotReason string
	reclaim := func(taskID, reason string) error {
		gotTask, gotReason = taskID, reason
		return nil
	}
	fired, reclaimFailed := map[string]bool{}, map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())

	if gotTask != "t1" {
		t.Fatalf("应对 t1 发起强制回收，实得 %q", gotTask)
	}
	if !strings.Contains(gotReason, "1300") || !strings.Contains(gotReason, "1200") {
		t.Fatalf("理由必须含实际进程数与硬上限两个真实数字，实得 %q", gotReason)
	}
}

// TestScanTaskProcsReclaimFailureWarnsOnce 停不掉时的边沿去重：首轮发一条提示
// 惊动协调者，后续轮次继续重试但不再刷屏。用与告警档分开的 map——两者的回落
// 判据不同（告警档看进程数回落，本档看停止是否成功）。
func TestScanTaskProcsReclaimFailureWarnsOnce(t *testing.T) {
	st, hub := newTestStoreHub(t)
	mustCreateTaskState(t, st, "t1", proto.TaskStateRunning)
	setTaskProcCount(t, map[string]int{"t1": 1300})

	calls := 0
	reclaim := func(taskID, reason string) error {
		calls++
		return errors.New("已发 SIGKILL 但复核仍存活")
	}
	fired, reclaimFailed := map[string]bool{}, map[string]bool{}
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())
	scanTaskProcs(st, hub, 400, 1200, fired, reclaimFailed, reclaim, slog.Default())

	if calls != 2 {
		t.Fatalf("每轮都应重试强制回收，实得 %d 次", calls)
	}
	evs := mustEvents(t, st, "t1")
	n := countEventsWithText(evs, proto.EventTypeProgress, "强制回收失败")
	if n != 1 {
		t.Fatalf("回收失败提示应只发一条（边沿触发），实得 %d 条", n)
	}
	cur, _ := st.GetTask("t1")
	if cur.State != proto.TaskStateRunning {
		t.Fatalf("回收失败时任务必须保持活跃，实得 %s", cur.State)
	}
}
```

两个夹具包内都不存在，在本文件内实现。`setTaskProcCount` 按既有用例的写法（`watchdog_taskprocs_test.go:59-60` 直接赋 `taskProcCountFn` 并 `t.Cleanup` 还原为 nil）封一层，支持按任务查表：

```go
// setTaskProcCount 把进程计数测试缝换成 map 查表，用完还原为 nil 防止用例间串味。
// 表里没有的任务返回 ok=false（等价于「数不出来」）。
func setTaskProcCount(t *testing.T, counts map[string]int) {
	t.Helper()
	taskProcCountFn = func(taskID string) (int, bool) {
		n, ok := counts[taskID]
		return n, ok
	}
	t.Cleanup(func() { taskProcCountFn = nil })
}

// countEventsWithText 数指定类型、且 payload 含指定子串的事件条数。
// 边沿去重的断言要的是「恰好一条」，只判存在性不够。
func countEventsWithText(evs []proto.Event, typ proto.EventType, want string) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ && strings.Contains(string(e.Payload), want) {
			n++
		}
	}
	return n
}
```

import 里需要 `"strings"` / `"errors"` / `"log/slog"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -run 'TestScanTaskProcsHardLimit|TestScanTaskProcsReclaim' ./internal/agentd/ -count=1 -v`
Expected: 编译失败（`scanTaskProcs` 参数个数不匹配）。

- [ ] **Step 3: 改 `scanTaskProcs` 的签名与硬上限分支**

`internal/agentd/watchdog.go:281` 起：

```go
func scanTaskProcs(st *store.Store, hub *Hub, budget, hardLimit int,
	fired, reclaimFailed map[string]bool,
	reclaim func(taskID, reason string) error, log *slog.Logger) {
```

硬上限分支（原 `watchdog.go:306-313`）改为：

```go
		if hardLimit > 0 && n > hardLimit {
			log.Error("任务进程数超过硬上限，强制回收", "task", t.ID, "used", n, "hard_limit", hardLimit)
			reason := fmt.Sprintf("任务进程数 %d 超过硬上限 %d，已强制回收", n, hardLimit)
			if err := reclaim(t.ID, reason); err != nil {
				// 没收掉就不落终态：任务留在活跃集，下一轮继续点名重试。改前
				// 无论成败都落 failed，之后 IsTerminal 直接跳过它——一个仍被
				// 监控的失控任务变成了不再被监控的失控任务（B119 §1.4）。
				//
				// 边沿触发：每分钟一条会把协调者淹了，而这条提示恰恰是要被看见的。
				if !reclaimFailed[t.ID] {
					reclaimFailed[t.ID] = true
					emitReclaimFailed(st, hub, t.ID, n, hardLimit, err, log)
				}
				continue
			}
			delete(fired, t.ID)
			delete(reclaimFailed, t.ID)
			continue
		}
```

- [ ] **Step 4: 新增 `emitReclaimFailed`**

替换掉原 `transitFailedWithEvent`（`watchdog.go:342-367` 整段删除，它在本次之后没有任何调用方）：

```go
// emitReclaimFailed 在强制回收失败时向协调者发一条可见提示。
//
// 参数：taskID / used / hardLimit / cause 全部进文案——审核者要据此判断该不该
// 人工上机处理；log 为本模块日志入口。
//
// 注意：调用方负责边沿去重（见 scanTaskProcs 的 reclaimFailed）。本函数不改
// 任务状态——回收失败时任务必须留在活跃集里继续被点名。
func emitReclaimFailed(st *store.Store, hub *Hub, taskID string, used, hardLimit int, cause error, log *slog.Logger) {
	text := fmt.Sprintf("强制回收失败：任务进程数 %d 超过硬上限 %d，但 executor 未能停止（原因：%v）。"+
		"任务保持活跃并将每轮重试，请用 handoff status %s 确认后人工处理", used, hardLimit, cause, taskID)
	evt, err := st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if err != nil {
		log.Error("追加强制回收失败提示事件失败", "task", taskID, "cause", err)
		return
	}
	hub.Publish(evt)
	log.Warn("已向协调者发出强制回收失败提示", "task", taskID, "used", used, "hard_limit", hardLimit)
}
```

- [ ] **Step 5: 改 `runWatchdog` / `RunWatchdog` 的参数与 map**

`watchdog.go:97-98`、`:103`、`:119`：

```go
func RunWatchdog(ctx context.Context, st *store.Store, hub *Hub, stallTimeout time.Duration, budget, hardLimit int, reclaim func(taskID, reason string) error, log *slog.Logger) {
	runWatchdog(ctx, st, hub, stallTimeout, watchdogTick, budget, hardLimit, reclaim, log)
}
```

`runWatchdog` 内，在既有 `taskFired` 旁边加一个 map，并改调用：

```go
	taskReclaimFailed := map[string]bool{}
	// ……循环体内：
			scanTaskProcs(st, hub, budget, hardLimit, taskFired, taskReclaimFailed, reclaim, log)
```

同步更新 `RunWatchdog` doc 里 `watchdog.go:84` 的参数说明：`sweep` 一条改为「reclaim: 强制回收入口（接线传 mgr.ForceReclaim）；返回非 nil 表示 executor 没停掉，此时任务不落终态」。

- [ ] **Step 6: 改接线**

`cmd/agentd.go:186-187`：

```go
		go agentd.RunWatchdog(wdCtx, st, srv.Hub(), cfg.StallTimeout,
			cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, mgr.ForceReclaim, logger)
```

`cmd/agentd.go:178` 的 `RecoverOnStartup(..., mgr.SweepTaskProcs, logger)` **保持不动**。

- [ ] **Step 7: 适配既有 watchdog 测试**

`internal/agentd/watchdog_test.go` 的 4 处 `runWatchdog(...)` 调用（约 `:130` / `:182` / `:211` / `:250`）补上新参数：`sweep func(string)` 位置替换为 `func(string, string) error { return nil }`。既有 `scanTaskProcs` 调用点补 `map[string]bool{}` 作为 `reclaimFailed`。

- [ ] **Step 8: 加日志与注释**

核对本 task 的可观测性：
- 硬上限触发：既有 Error「任务进程数超过硬上限，强制回收」带 `used` / `hard_limit`（保留）
- 回收失败：`emitReclaimFailed` 的 Warn 带 `task` / `used` / `hard_limit`；事件文案含 `cause` 原文与下一步动作
- 事件追加失败：Error 带 `task` / `cause`
- 注释三处：硬上限失败分支的「为什么不落终态」、边沿去重的「为什么」、`emitReclaimFailed` 的 doc（含「调用方负责去重」「本函数不改状态」两条边界）

- [ ] **Step 9: 跑测试确认通过**

Run: `go test ./internal/agentd/ ./cmd/... -count=1`
Expected: PASS

- [ ] **Step 10: 确认 `transitFailedWithEvent` 已无残留**

Run: `grep -rn "transitFailedWithEvent" internal/ cmd/`
Expected: 无输出（含测试文件；若测试里还有引用，一并删除或改写为对 `ForceReclaim` 的断言）

- [ ] **Step 11: Commit**

```bash
git add internal/agentd/watchdog.go internal/agentd/watchdog_taskprocs_test.go internal/agentd/watchdog_test.go cmd/agentd.go
git commit -m "fix(agentd): 硬上限档改走 ForceReclaim，回收失败不落终态并边沿告警（B119 §2.3）"
```

---

### Task 5: 总回归、变异测试与 ledger

**Files:**
- Create: `docs/superpowers/notes/2026-08-17-b119-ledger.md`

**Interfaces:**
- Consumes: Task 1-4 的全部交付物
- Produces: 无代码产出；ledger 供协调者复验取证

- [ ] **Step 1: 跑六道门**

逐条执行并**贴原始输出**（跑不动就贴报错原文，不许替它归因）：

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./... -count=1
go test -race ./internal/agentd/ ./internal/prochost/ -count=1
```

`gofmt -l .` 必须是空输出。`go test -race` 的 `TestApproverConcurrentTaskEndOnlyAudits` 是**既有 flaky**（时序敏感，与本分支无关，B93 评测时已确认），若红了单跑三次确认，并在 ledger 里如实记录。

- [ ] **Step 2: 变异测试 1——`Sweep` 改回整体放弃**

把 `internal/prochost/footprint.go` 降级分支的三行（枚举 + `rosterKill` + Info 日志）替换为 `return 0, VerdictOK, ErrExecutorAlive`。

Run: `go test -run 'TestSweepAlive' ./internal/prochost/ -count=1`
Expected: **FAIL**，`TestSweepAliveSkipsGroupPhaseButStillRosterKills`（段②应回收 2 个，实得 0）与 `TestSweepAliveNeverSignalsExecutorItself`（应只回收 501 这一条，实得 0）两条变红。

还原：`git checkout internal/prochost/footprint.go`，并 `git status --short` 确认工作区干净。

- [ ] **Step 3: 变异测试 2——摘掉 `transit` 终态分支的清扫**

把 `internal/agentd/manager.go` 终态块里的 `m.sweep(taskID)` 改为 `_ = taskID`（**不要直接删**——删掉可能造成未使用变量的编译失败，那是编译错误不是变异）。

Run: `go test -run 'TestTransitToTerminalSweeps|TestForceReclaimSuccess' ./internal/agentd/ -count=1`
Expected: **FAIL**，两条都红。

还原并确认工作区干净。

- [ ] **Step 4: 变异测试 3——回收失败照样落 failed**

把 `internal/agentd/forcereclaim.go` 里 `stopExecutor` 失败分支的 `return fmt.Errorf(...)` 改为继续往下走（注释掉那条 return）。

Run: `go test -run 'TestForceReclaimKeepsTaskActive|TestScanTaskProcsReclaimFailure' ./internal/agentd/ -count=1`
Expected: **FAIL**，`TestForceReclaimKeepsTaskActiveWhenStopFails`（停不掉时任务必须保持活跃）变红。

还原并确认工作区干净。

- [ ] **Step 5: 写 ledger**

创建 `docs/superpowers/notes/2026-08-17-b119-ledger.md`，含：分支名与起点 commit；每 task 一行（内容 / 状态 / commit 范围）；每轮修复各一行；六道门的**原始输出**；三条变异测试各自的失败用例名与断言原文；Minor 记账（不进修复回路，留给终审 triage）。

- [ ] **Step 6: 写真机复验交接说明**

在 ledger 末尾补一节，按 spec §5 三条写清楚：验症状 A（普通任务回合结束后 `agentd.log` 应出现「点名回收完成」而非「执行者仍存活，拒绝清扫」）、验硬上限档（构造 fork 炸弹、确认 executor 被停/进程被收/任务落 failed 且理由含真实进程数/**worktree 仍在**）、验失败路径（不易人工构造，允许记「未验」，**不得凭推断写结论**）。

- [ ] **Step 7: Commit**

```bash
git add docs/superpowers/notes/2026-08-17-b119-ledger.md
git commit -m "chore: B119 总回归、变异测试与真机复验交接说明"
```
