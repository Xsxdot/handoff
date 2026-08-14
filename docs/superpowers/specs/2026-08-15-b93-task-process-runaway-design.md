# B93 任务进程失控：执行者已死，名下进程仍在增长

**日期**：2026-08-15
**分支基线**：`main` @ `fb7f88d2c`
**来源**：08-14 B91 派发（任务 `d912b23a`，mac-02 / opencode）实测事故。

---

## 1. 事故实录

| 时刻 | 观测 |
|---|---|
| seq 698 | `resource_pressure{used: 2409, limit: 2400}` —— 高水位事件发出 |
| seq 700 | `failed{fail_reason: "opencode 事件流意外中断: <nil>"}` —— **executor 已死** |
| 之后 T+0 | `handoff status` 报该任务名下 **2100** 进程 |
| 之后 T+40s | 复查 **2140** —— **执行者死后进程还在涨** |
| 同时 | 整机 uid 进程 2411/2666，agentd 自身被压到不响应：`status` 要跑 >120s，`done` 第一次 `read: operation timed out` |
| `done` 之后 | 整机进程从 2411 **瞬间掉回 290** |

最后那一行是本条最硬的证据：那 2100+ 个进程**全部**属于这一个任务，而且 `done`
一执行就全没了——**回收机制是好的，只是没人在该触发的时候触发它**。

---

## 2. 三条缺口

### 2.1 任务落 `failed` 时不清扫（主缺口）

`SweepTaskProcs`（`internal/agentd/reconcile.go:201`）已经实现得很完整：解析
adapter → 取进程句柄 → `prochost.Sweep` → 按 verdict 分诊。**问题是它的调用方只有三处**：

| 调用点 | 触发条件 |
|---|---|
| `cmd/agentd.go:175` | agentd **重启**时的 `RecoverOnStartup` 判定 executor 已不在 |
| `internal/agentd/reconcile.go:58` | `resume` 路径判定 executor 已不在 |
| `internal/agentd/manager.go:2398` | 恢复应答时发现 executor 已不在 |

三条全是「**事后**发现 executor 不在了」的补救路径。而 `handleResult` 的失败分支
（`manager.go:2530` 附近）——也就是 executor **当场报告自己死了**的那条主路径——
`transitToReview` + `AppendEvent(failed)` + `voidTicketsWithAudit` + `clearApproverState`
四件事都做了，**唯独不清扫进程**。

于是 B93 的形态就是必然的：executor 报告死亡 → 任务进 `waiting_review` → 它 fork
出去的 2100 个后代（`prochost` 的注释明写 opencode 的 Bash 工具会 `setsid`
逃逸出进程组）无人回收，一直挂到审核者手动 `done`。**审核者可能几小时后才醒。**

### 2.2 围栏是 uid 级的救护车道，管不住单个任务

`prochost` 的围栏是给 shim 装 `RLIMIT_NPROC`（`fence_unix.go:46`），值为
`L = 系统上限 − max(200, 0.1×系统上限)`（`fence.go:fenceLimit`）。mac-02 上就是
`2666 − 266 = 2400`——**与事故里 `limit: 2400` 完全对得上**。

`fence.go` 的注释把这个取法的意图写得很清楚：

> 取法是「贴天花板留救护车道」，不是「给 executor 节流」……压得更低不增加安全性，
> 只会让 executor 更早撞墙、让协调者更容易把配额问题误判成代码问题

**这个判断在它自己的范围内是对的，本 spec 不推翻它。** 但它只回答了「怎么保证
agentd/sshd 活得下来」，没有回答「怎么保证**一个**任务不把整台机器的份额吃光」。
事故里围栏确实起了作用（agentd 没被彻底饿死，`done` 最终还是发得出去），但代价是
在 2400 这条线之前，单个任务可以合法地一路涨到 2400。

**RLIMIT_NPROC 在结构上做不了 per-task**：内核对它的判定是「**该 uid 当前进程总数**
是否超过调用者的软限」，不是「该进程树的后代数」。给每个 shim 装 300，效果不是
「每个任务 300」，而是「uid 总数一过 300，所有 shim 一起 fork 失败」——第二个任务
会被第一个任务的用量饿死。**所以每任务预算必须换一种机制，不能靠调小围栏值实现。**

### 2.3 `done` 在 agentd 被压垮时不幂等

事故中 `done` 第一次返回 `read: operation timed out`，**但请求其实已经落库**；
重发得到 409 才发现。审核者的自然反应是「超时 = 没成功 = 重发」，而重发在这里
拿到的是一个看起来像「状态不对」的错误，与「上一次其实成功了」长得完全不一样。

在整机被压垮的场景下，这正是最需要可靠的一条路径——它是收口用的。

---

## 3. 设计

### 3.1 任务落终态即清扫

在 `handleResult` 的失败分支里，**在追加 `failed` 事件之后**调用 `m.SweepTaskProcs(taskID)`。

**为什么放在事件之后**：事件先落库，审核者的 `wait` 才能第一时间醒；清扫是 best-effort
的善后，不该挡在唤醒前面。`SweepTaskProcs` 自身已经是全 best-effort（每个失败分支
只记日志或发 `orphan_risk` 提示，不返回错误），语义上正好适合放在末尾。

**成功分支（`completed`）也要清扫吗？要。** executor 正常收尾同样可能留下 `setsid`
逃逸的后代。`Sweep` 遇到 executor 仍存活会返回 `ErrExecutorAlive` 并**自行放弃**
（`reconcile.go:218` 那个 switch 的第一支），所以「executor 还活着但回合结束了」
这种正常情况不会被误杀——这条保护是既有的，不是本 spec 新加的。

**这不改变「回合结束 ≠ 任务结束」的口径**：任务仍进 `waiting_review` 等审核，
`continue` 仍能续接。清扫的是**这一回合**留下的孤儿后代，不是 executor 本体
（`Sweep` 的 `ErrExecutorAlive` 分支保证了这一点）。

### 3.2 每任务进程预算：watchdog 点名，不是围栏

新增一条 watchdog 扫描 `scanTaskProcs`，与既有的 `scanStalled` / `scanPressure`
并列，每 tick（一分钟）跑一次：

对每个**非终态**任务，用既有的 `prochost.Footprint(h)` 数出它名下的成员数
（`status.go:151` 与 `:261` 已经在用这条路，不是新机制），然后：

| 条件 | 动作 |
|---|---|
| `members > 预算` 且未置位 | 追加并广播 `task_proc_pressure{task, used, budget}` 事件，置位 |
| 仍超预算且已置位 | **不重发**（沿用 `scanPressure` 的边沿触发口径，理由相同：事件风暴会淹掉真正要处置的工单） |
| 回落到预算以下 | 复位 |
| `members > 硬上限` | **清扫并落 failed**，`fail_reason` 写明「任务进程数 N 超过硬上限 M，已强制回收」 |
| `Footprint` 返回非 `VerdictOK` 或出错 | **什么都不做**，只记 Debug 日志 |

**默认值**：预算 `400`，硬上限 `1200`。两个数都进 config，可关（置 0 = 不启用该档）。

- **400 的依据**：事故前 opencode 正常跑 B89/B90 时名下是**个位数到两位数**进程
  （`status` 输出里 `2 进程` / `3 进程`）。400 比正常用量高一个数量级还多，
  不会误伤，同时远低于让整机出问题的量级。
- **1200 的依据**：系统上限 2666 的不到一半。单个任务到这个量级已经确定是失控
  （正常用量的 100 倍），而此时距离 2400 那条围栏线还有足够余量让 agentd 从容
  执行清扫——**不能等贴到围栏才动手，那时 agentd 自己已经 fork 不出 `ps` 了**。
- 两档的关系：400 是「叫醒人」，1200 是「不等人了」。只有一档的话，要么太吵
  （每次都杀），要么太晚（只告警，人没醒机器就没了）。

**为什么用 watchdog 点名而不是内核围栏**：见 §2.2——`RLIMIT_NPROC` 是 uid 级计数，
表达不了「这棵进程树最多 N 个」。macOS 上没有 cgroup 这类每进程树的额度机制。
点名的代价是**有延迟**（最坏一分钟），换来的是**能按任务归因**。事故里从
「executor 死」到「压垮整机」用了远超一分钟，这个延迟够用。

**`task_proc_pressure` 必须 `Publish`，不能只 `AppendEvent`**。B91 刚修过一条
「只落库不广播、审核者永远不知道」的缺陷（`deny_guidance_dropped`），同一个坑
不踩第二次。

### 3.3 `done` 幂等

`handleDone` 在任务已是 `completed` 且归档说明一致时，返回 **200 + 与首次相同的响应体**，
而不是 409。

**判据要严**：只有「当前状态是 `completed`」这一种情形转 200。其余非
`waiting_review` 的状态（`running` / `waiting_answer` / `failed` / `pending`）
仍然 409——那些是真的「状态不对」，把它们也放行等于让 `done` 变成万能收口，
审核者会失去「我操作错了」这个信号。

**为什么不是让客户端重试**：客户端分不清「超时 = 请求没到」和「超时 = 请求到了但
响应没回来」。这是服务端才有的信息，只能在服务端解决。

---

## 4. 明确不做

- **改围栏的取值或取法**（`fenceLimit` / `fenceReserveRatio`）。§2.2 论证过它在
  自己的范围内是对的，本 spec 在它之上加一层，不动它。
- **准入闸 `checkProcHeadroom` 的行为**。它自己的注释就说「不承担拦截职责」，
  事故当天两个任务开工时余量都是好的，改它拦不住这类事故。
- **查清 opencode 为什么 fork 出 2100 个进程**。那是 opencode 侧的行为，本 spec
  做的是「无论 executor 怎么失控，handoff 都不被它拖垮」。根因另记。
- **自动 `done` / 自动归档**。§3.2 的硬上限只清扫进程 + 落 `failed`，任务仍留给
  审核者处置。自动归档会把审核者的取证材料（worktree、日志）一起收走。
- **B92 的修复**。B92 的根因**不是**原先假设的「failed 事件落库但状态没迁移」
  ——排查已证伪该假设（`handleResult` 的迁移一直正确）。真因是 grok 在回合
  失败时关闭事件通道，致 `continue` 的续接回合事件被静默丢弃，已单独修复
  并合入。因此 §3.1 挂在 `handleResult` 上的清扫**会**在回合失败时正常触发，
  不存在原先担心的依赖关系。

---

## 5. 验收

1. **终态清扫**：单测——构造一个 `handleResult` 失败分支，断言 `SweepTaskProcs`
   被调用（用测试缝注入一个记录调用的替身），且调用发生在 `AppendEvent(failed)`
   **之后**。成功分支同样断言被调用。
2. **每任务预算，告警档**：单测——注入一个返回 500 成员的 `Footprint` 替身，
   断言活跃任务收到 `task_proc_pressure{used:500, budget:400}` 事件，**且该事件
   被 `Publish`**（用 Hub 的订阅端断言，不能只查库）。第二轮 tick 断言**不重发**。
   回落到 300 后再涨到 500，断言**重新发一次**。
3. **每任务预算，硬上限档**：注入返回 1500 成员的替身，断言清扫被调用且任务落
   `failed`，`fail_reason` 含实际数字。
4. **不启用时零行为**：预算配置为 0 时，断言 `Footprint` 一次都不被调用
   （不是「调用了但不发事件」——不启用就该完全不产生开销）。
5. **`done` 幂等**：对同一个任务连发两次 `done`，第二次返回 200 且响应体与第一次
   相同。对一个 `running` 任务发 `done`，仍然 409。
6. **回归**：`go build ./... && go vet ./... && go test -count=1 ./...` 全过。
7. **变异测试**（沿 B47/B57/B91/B94 先例）：摘掉 §3.1 的 `SweepTaskProcs` 调用，
   第 1 条必须 FAIL；把 §3.2 的 `hub.Publish` 摘掉，第 2 条必须 FAIL；把 §3.3 的
   幂等分支摘掉，第 5 条必须 FAIL。三条要**各自单独**红，不许交叉兜底。
8. **真机复验**（`go test` 之外，必须做）：在 mac-02 上派一个刻意 fork 500+ 进程
   的任务，确认收到 `task_proc_pressure` 事件；再让它超 1200，确认被清扫且落
   `failed`。**这一条不能用单测替代**——B91 就是因为真机复验没做而在验收里留了口子。

---

## 6. 风险

**误杀。** 硬上限档会主动杀进程，这是本仓库第一次让 agentd 在没有人裁决的情况下
杀东西。三道保护：①1200 是正常用量的 100 倍以上；②`Sweep` 的 `ErrExecutorAlive`
分支会在 executor 仍存活时自行放弃；③整档可由 config 关掉。**即便如此，这仍是
一个不可逆动作，事件里必须把 used/budget 两个数字都写上**，让审核者事后能判断
杀得对不对。

**`Footprint` 的开销。** 每 tick 对每个活跃任务枚举一次进程表。活跃任务通常是
个位数，而 `scanStalled` 已经在每 tick 做全表 `ListTasks` + 每任务 `LatestEvent`，
量级相当。真成为问题时把它降频到 5 tick 一次，不必现在优化。

**与 B92 无耦合（原判断已修正）。** 曾担心「B92 未修时 §3.1 的清扫不会执行」，
这基于一个已被证伪的假设。`handleResult` 的状态迁移一直是正确的，§3.1 的清扫
在回合失败时会正常触发。§3.2 的 watchdog 点名仍是有价值的冗余，但它兜的是
另外两条**真实存在**的缺口：`Manager.Stop` 与 `reconcileExecutorGone` 都是
「先追加事件后迁移」，迁移失败会留下「有 failed 事件、状态未终态」的形态。
这两条由 B97 系统性处理。
