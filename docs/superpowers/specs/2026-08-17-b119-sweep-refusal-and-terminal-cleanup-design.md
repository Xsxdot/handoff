# B119 清扫的全有全无：活 executor 下三条清扫路径同时空转

**日期**：2026-08-17
**分支基线**：`main` @ `4b239810b`
**来源**：08-17 B93 基准评测的副产品。四份独立实现（参照 + opencode×2 + codex）
按同一份 plan 各自实现了硬上限档，**全部照做，无一上报**——因为缺陷不在实现里，
在 plan 与它所依赖的 `prochost.Sweep` 契约之间。

---

## 1. 一个根因，三个症状

`prochost.Sweep`（`internal/prochost/footprint.go:226`）的第一件事是：

```go
func Sweep(h Handle) (killed int, v Verdict, err error) {
	if aliveFn(h) {
		log().Warn("执行者仍存活，拒绝清扫", "pid", h.PID, "lock", h.LockPath)
		return 0, VerdictOK, ErrExecutorAlive
	}
```

**executor 存活时，整个清扫直接放弃**——不是「跳过危险的那部分」，是一个进程都不动。
仓库里三处清扫调用全都因此空转，而三处的注释都以为自己在工作。

### 1.1 生产实证

mac-02 生产 agentd 日志（`~/.handoff/agentd.log`，采样于 08-17 21:40）：

| 日志行 | 次数 |
|---|---|
| `执行者仍存活，拒绝清扫` | **118** |
| `残留进程已清扫` / `无残留进程`（真正跑完的清扫） | 34 |

最近一次拒绝发生在 08-17 21:38。**这不是推演出来的缺陷，是每天都在发生的空转。**

### 1.2 症状 A：回合结束的清扫（B93 §3.1）空转

**误读的源头是 B93 spec 本身，不是某一份实现。** B93 spec §3.1 原文：

> **成功分支（`completed`）也要清扫吗？要。** executor 正常收尾同样可能留下
> `setsid` 逃逸的后代。`Sweep` 遇到 executor 仍存活会返回 `ErrExecutorAlive` 并
> **自行放弃**……所以「executor 还活着但回合结束了」这种正常情况不会被误杀
>
> 清扫的是**这一回合**留下的孤儿后代，不是 executor 本体（`Sweep` 的
> `ErrExecutorAlive` 分支保证了这一点）。

同一段里两个断言互相抵消：既要求「收掉逃逸后代」，又援引一个「一触发就整体放弃」
的分支作为安全保证。`ErrExecutorAlive` 的真实含义是「整个清扫放弃，逃逸后代也一个
不收」——它保护 executor 的方式就是什么都不做。

`manager.go:2576` 的实现注释逐字继承了这个误读（「executor 本体不会被误杀——Sweep
遇到它仍存活会返回 `ErrExecutorAlive` 并自行放弃」），四份独立实现无一例外。

而 opencode / codex / grok 都是 serve 常驻模型：回合结束进 `waiting_review` 时
executor 进程**必然活着**（`continue` 能续接同一会话正是靠它）。所以这条路径在它
自己写明的主场景里 100% 空转。

### 1.3 症状 B：`Manager.Stop` 根本没有清扫

B93 Task 1 的标题是「回合落终态即清扫」，落地只加在 `handleResult` 末尾
（`manager.go:2576`，全仓唯一的生产 `m.sweep` 调用点）。`Manager.Stop`
（`manager.go:1182`）停完 executor 直接落 failed，**不清扫**。

今天对一个 fork 炸弹任务敲 `handoff stop`：executor 死了，逃逸的后代还在。
「终态」落地成了「回合终态」。

### 1.4 症状 C：硬上限档是空操作，且事件说假话

`watchdog.go:306`：

```go
if hardLimit > 0 && n > hardLimit {
    log.Error("任务进程数超过硬上限，强制回收", ...)
    sweep(t.ID)                                          // ← 恒被拒
    reason := fmt.Sprintf("任务进程数 %d 超过硬上限 %d，已强制回收", n, hardLimit)
    transitFailedWithEvent(st, hub, t.ID, reason, log)   // ← 照样落 failed
```

能 fork 到 1200 个进程的任务，其 executor 必然活着——**这一档唯一该管的形态，
恰好是 `Sweep` 恒拒的形态**。实际发生的是：

1. 清扫空转，一个进程没杀
2. 任务照样迁进 `failed` 终态
3. 下一轮 `scanTaskProcs` 走到 `watchdog.go:299` 的 `IsTerminal` 判断，**跳过它**
4. 那批进程继续涨，从此无人跟踪；库里留着一条对审核者说「已强制回收」的假事件

净效果：**把一个仍在被监控的失控任务，变成了一个不再被监控的失控任务**。比 B93
想修的原始形态更难排查——连告警都不会再来了。

`SweepTaskProcs` 收到 `ErrExecutorAlive` 只打一条 Info「交由常规回收路径」
（`reconcile.go:254`），而对这个形态而言，那条常规路径并不存在。

### 1.5 为什么四份实现都没发现

B93 spec §6 把它列为「误杀的三道保护」之②：

> ②`Sweep` 的 `ErrExecutorAlive` 分支会在 executor 仍存活时自行放弃

设计侧确实知道这个分支存在，只是**把它当成了保护，没意识到它同时也是失效**——
在硬上限档这个场景里，「自行放弃」保护的对象恰好就是要收的对象。

plan 把函数体写得极细（含注释原文），实现者照做即及格；要发现这条，必须跨包去读
`prochost.Sweep` 的第一行，那不在任何一个 task 的改动范围内。参照实现的 ledger
还把 `ErrExecutorAlive` 当成利好核对过一次（「T1/T4 两条清扫路径不冲突」），
四份实现没有一份问出「那硬上限档还剩什么」。

**这条对流程的启示**：plan 越详细，实现者越不会去质疑它依赖的外部契约。计划里凡
是援引某个既有分支作为安全保证的地方，都该在 task 里明确要求实现者去读那个分支的
实现并复述其语义——而不是照抄 plan 的转述。

---

## 2. 设计

### 2.1 `Sweep` 改为分段决策，不再全有全无

`Sweep` 的两段风险完全不同：

| 段 | 做什么 | 活 executor 下的风险 |
|---|---|---|
| ① 组清扫 | 按 pgid 回收整组（`killGroupFn(h.PID)`） | **致命**：连 executor 本体一起端掉 |
| ② 名册点名 | 按出生名册逐个回收 setsid 逃逸的后代（`rosterKill`） | **无**：每条成员自带 pid + 出生时刻双重凭据 |

`rosterKill`（`footprint.go:292`）对每条名册成员校验 `live[e.PID] == e.StartedAt`，
pid 易主就拒发信号并留 Warn。它杀的是逃逸后代，与 executor 的死活无关。

**改法**：`aliveFn(h)` 为真时不再整体返回，而是跳过段①、执行段②，并显式跳过
`e.PID == h.PID`（名册理论上不含 executor 本体，但这道门槛成本为零，且是本次唯一
新增的误杀面，必须堵死）。

返回结论要能表达第三态。签名 `(killed int, v Verdict, err error)` 保持不变，
`ErrExecutorAlive` 保留为哨兵值，但**语义改写并须在 doc 注释里写死**：

| | 改前 | 改后 |
|---|---|---|
| `err == ErrExecutorAlive` 的含义 | 什么都没做 | 段①跳过（executor 存活），段②已执行 |
| 此时的 `killed` | 恒 0 | 段②实际回收数，**可以非 0** |

`ErrExecutorAlive` 仍是非 nil error，所以任何 `if err != nil` 的调用方不会误判成
「全成功」；但把它当「什么都没发生」的调用方必须改。全仓 `errors.Is(...,
ErrExecutorAlive)` 只有 `reconcile.go:254` 一处（已核实），今天打的是「交由常规
回收路径」，改后应报告实际回收数——那条路径不存在，日志不能继续这么说。

哨兵自己的文案也要改：`footprint.go:203` 现为「执行者仍存活，Sweep **不适用**」，
改后 Sweep 是适用的（只是降级为段②），措辞须随语义走，否则下一个人还是照着字面
理解。同理 `footprint.go:200/212` 的 doc 注释与 `footprint.go:228` 的 Warn 文案
（「拒绝清扫」→「跳过组清扫，转入点名回收」）。

**不动的东西**：`aliveFn` 判据本身、段①在 executor 已死时的全部行为、B72 名册的
写入侧。本次只改「活着时做什么」。

### 2.2 清扫挂到 `m.transit` 的终态分支

`manager.go:2624` 的 `transit` 已经有一个终态收口块：

```go
if to.IsTerminal() {
    voidTicketsWithAudit(m.st, taskID, reason, m.log)
}
```

它的注释写着：

> 终态收口（B63）：done / stop / 各处 transitBestEffort 全部经过本函数，作废挂在
> 这里才能覆盖**将来新增的**终态路径——B63 本身就是「新增一条路径时忘了补」漏出来的。

**B93 的清扫是同一个错误的下一次复发**：现成的教训、现成的挂载点，没有复用。

改法：在该块内 `voidTicketsWithAudit` 之后调 `m.sweep(taskID)`，删掉
`manager.go:2576` 的调用。此后 `done` / `stop` / 硬上限 / 将来新增的任何终态路径
自动获得清扫。

**顺序**：作废工单在前、清扫在后。工单作废是状态语义（协调者可见），清扫是
best-effort 善后（`SweepTaskProcs` 每个失败分支只记日志或发 orphan_risk，从不返回
错误），不该挡在语义收口前面。

**对 `Done` 路径的影响**：`Done` 是先 `transit(completed)` 再回收 executor
（`manager.go:1103`），所以清扫会在 executor 仍活着时跑。改造后它执行段②——收掉
这个任务留下的逃逸后代，正是应该做的善后，不再是空转。

**不覆盖的路径**：`watchdog.go:353` 的 `transitFailedWithEvent` 用
`st.UpdateTaskState` 直接迁移，绕开 `m.transit`（它是包级函数，拿不到 Manager），
因此它自己手抄了一份 `voidTicketsWithAudit`。本次把硬上限档改为经 Manager 走
（见 §2.3），该函数在硬上限路径上不再被使用。`RecoverOnStartup` 迁的是
`waiting_review`（非终态）且 executor 确已不在，其显式 sweep 调用**保留不动**。

### 2.3 硬上限档：先停源头，再清扫

想清掉一个正在 fork 的任务，**必须先让 fork 的源头停下来**。杀子进程而留着父进程
是打地鼠。

抽一个 `Manager` 方法供 `Stop` 与硬上限档共用，核心三步：

1. 停 executor（`stopExecutor`，即 `ad.Stop` + reaper 兜底）
2. 判成败
3. 成功才经 `m.transit(taskID, Failed, reason)` 落终态——清扫与工单作废在 transit
   内自动发生（§2.2）

两条路径的差异保留在各自的调用方：

| | `Manager.Stop`（人主动敲） | 硬上限档（watchdog 自动） |
|---|---|---|
| failed 文案 | 「协调者主动中止（handoff stop）」 | 「任务进程数 N 超过硬上限 M，…」 |
| managed worktree | 删（既有行为不变） | **不删** |

**为什么自动档不删 worktree**：删 worktree 是不可逆且外部可见的动作，1200 进程
这种现场恰恰最需要留证。`handoff stop` 是人敲的，删是人的决定；watchdog 自动触发
不该继承这个决定。worktree 留给协调者事后 `handoff reclaim`。

**停不掉时不落 failed**：`ad.Stop` 可能失败（典型 `ErrStillAlive`：已发 SIGKILL
复核仍存活）。没收掉就不能宣布收掉了——

- 不迁状态，任务留在活跃集，下一轮 watchdog 继续点名与重试
- 首次失败发一条 `notifyOrphanRisk`（已有通道），用与告警档同款的边沿触发去重，
  避免每分钟刷屏
- 边沿状态与告警档的 `fired` map 分开维护：两者的回落判据不同（告警档看进程数
  回落，本档看停止是否成功）

这同时消掉了那条假事件：failed 事件只在真的停掉之后才落。

### 2.4 注入形态

`runWatchdog` 今天收 `sweep func(string)`。硬上限档改走 Manager 后，该参数替换为
带返回值的强制回收闭包，形如 `func(taskID, reason string) error`，接线在
`cmd/agentd.go`。调用方由此能区分「真回收了」与「没停掉」，这也是 §2.3 边沿告警
的判据来源。

`scanStalled` / `scanPressure` 不受影响。

---

## 3. 不做的事

- **不动 `aliveFn` 的判据**：它是 B20 之后立起来的存活权威，本次只改「判定存活后
  做什么」。
- **不给 `Sweep` 加「强杀」开关**：绕过存活保护的入口一旦存在，迟早会被某条路径
  用错。需要连 executor 一起收的场景，正确姿势是先 `stopExecutor`。
- **不改 B72 名册的写入侧**（`roster.json` 的落盘时机与覆盖语义）。B103 记录的
  「fire-and-forget 后代漏记」是另一条独立缺口，与本次正交。
- **不改硬上限/告警档的默认值**（1200 / 400）。触发条件罕见，本次只让它在触发时
  做对事。
- **不做真机 fork 炸弹复验的自动化**：复验方式沿用 B93 §交接说明的手工构造。

---

## 4. 测试要求

单元测试必须覆盖：

1. **`Sweep` 活 executor 下的部分清扫**：注入存活 + 名册含两条逃逸后代 → 断言段①
   未被调用（`killGroupFn` 零调用）、段②杀掉两条、返回值反映实际数量。
2. **`Sweep` 跳过 `h.PID`**：名册里混入 executor 本体的 pid → 断言它不被发信号。
3. **`transit` 终态分支触发清扫**：分别经 `done` / `stop` / 强制回收三条路径迁入
   终态 → 断言清扫各被调用一次；非终态迁移（如 `running` → `waiting_review`）
   → 断言不调用。
4. **强制回收成功路径**：停 executor 成功 → 任务落 `failed`、事件理由含真实进程数
   与硬上限、worktree **未**被删除。
5. **强制回收失败路径**：`ad.Stop` 返回 `ErrStillAlive` → 任务**仍在活跃态**、
   无 failed 事件、发出一条 orphan_risk；同一任务下一轮再次失败 → **不重复**发。
6. **回归**：`handleResult` 删掉直接调用后，回合结束仍触发清扫（经 transit）。

变异测试（协调者独立复验，每条须 1:1 对应到具体用例）：

- 把 §2.1 的分段决策改回整体 `return` → 用例 1 变红
- 摘掉 `transit` 终态分支里的清扫 → 用例 3 变红
- 让强制回收在 `ad.Stop` 失败时照样落 failed → 用例 5 变红

---

## 5. 真机复验交接

复验机需装本分支构建的 agentd（**先停旧进程再起新的**，不要热替换）。

1. **验症状 A 已修**：派一个普通任务，回合结束后查 `agentd.log`——应出现名册点名
   的结论日志（`点名回收完成`）而不再是 `执行者仍存活，拒绝清扫`。
2. **验硬上限档**：按 B93 §交接说明构造 fork 炸弹任务（`for i in $(seq 1 1300);
   do sleep 300 & done`），确认 executor 被停、进程被收、任务落 `failed` 且理由含
   真实进程数、**worktree 仍在**（`handoff reclaim` 能列出它）。
3. **验失败路径**：不易人工构造，允许记「未验」——不得凭推断写结论。
