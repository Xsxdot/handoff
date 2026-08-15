# B103 设计：让出生名册接得住「换了进程组的 fire-and-forget 后代」

**日期**：2026-08-15
**基线**：`w4-delivery` @ `d170ab1c3` 之后（已含 B92/B93/B99/B102）
**排查报告**：[notes/2026-08-15-b103-proc-roster-blindspot.md](../notes/2026-08-15-b103-proc-roster-blindspot.md)（结论 (B) 真实缺口）

> **本 spec 由审核者在用户外出期间独立撰写**，没有走 brainstorming 的逐问确认。
> §3.2「不做 pgid 增强」与 §3.1「采样间隔取 1s」是两处**可推翻的判断**，
> 已各自写明理由与代价。用户回来后若不同意，改的是这两处。

---

## 1. 问题：数不到与清不掉是同一个判据的两面

08-15 的真机探针（任务 `35251ad9`，mac-02/grok）跑了：

```bash
for i in $(seq 1 450); do sleep 900 & done
```

同一时刻实测：`ps` 数得到 **450 个 `sleep 900` 全都活着**，它们 `ppid=1`（已被
reparent 给 launchd）、`pgid=76332`；而该任务 shim 的 `pid=pgid=76057`。结果：

| 观测点 | 实测 |
|---|---|
| `handoff status` 该任务那一行 | **「3 进程」** |
| 整机 uid 进程 | 835/2666（450 个确实计进了 uid 总量） |
| 400 告警档 | **一次都没响** |
| `handoff done` 前/后各数一次 | **450 / 450**（终态清扫也够不着） |

**根因链**（报告已逐条给出代码坐标，审核者复核无误）：

1. grok 的 Bash 工具把每条命令包成 `/bin/zsh -c … eval` 并 **setsid**
   （`STAT=Ss`、`pid==pgid`；grok 二进制里搜得到 `process-wrap/src/tokio/process_session.rs`
   ——**不是 handoff 这边设的**）。`sleep &` 继承的是**工具壳的 pgid**，不是 shim 的。
2. `classify` 按 `p.PGID != h.PID { continue }` 判归属（`internal/prochost/footprint.go:115-117`），
   于是这 450 个被跳过。`Footprint` 与 `Sweep` **共用** `classify`——所以「数不到」
   和「清不掉」必然同时发生。
3. 本该兜底的出生名册接不住：`snapshotRoster` 每 **15s** 从 shim 沿 **ppid** 做闭包
   （`shim.go:276-294`、`roster.go:68-96`），并且 `writeRoster` 是**整份覆盖**
   （`roster.go:115-132`）。工具壳只活约 1 秒就退出，子进程随即 reparent，ppid 链
   当场断——此后每一轮 15s 快照都把它们**抹掉**。盘上实证：`roster.json` 终态只有
   2 条（grok serve 76058 + 76075），450 个 sleep 一条都没有。
4. `Manager.Done` **不调用** `SweepTaskProcs`：它只 `stopExecutor` → `Reap` →
   `prochost.Kill` → `killGroup(-shim pid)`，只够着 shim 那一组。`Manager.Stop`
   （`manager.go:1205-1210`）是同样的形态，同样没有清扫。

### 1.1 顺带纠正 B93 的一处叙述

B93 事故那 2100 个进程**当时在 shim 进程组内**——`done` 能把整机 2411 打回 290，
靠的是 `Kill(-91331)`，而它只杀 `pgid == shim pid` 那一组。所以**事故形态不在本盲区里**，
「事故当时 Footprint 是好的」证明不了「B93 接上 Footprint 就挡得住 `sleep &`」。
讽刺的是 B93 ledger 自己写的复验配方 `for i in $(seq 1 600); do sleep 300 & done`
与本次探针同构，**按现实现根本触发不了 400 档**。

这也是本条为什么必须真机验收（§6 第 6 条）：B93 的两档围栏至今没有一次真机证据。

## 2. 目标与非目标

**目标**：让 `Footprint` / `Sweep` / 看门狗点名 / 终态清扫这四件事，都能覆盖
「executor 的 Bash 工具用 `&` 留下的、换了进程组的长命后代」。

**非目标**（明确不做，理由见 §3.2 / §7）：

- 不改 `classify` 的 pgid 判据本身——它对「同组成员」是对的，问题在逃逸那一层。
- **不**用「`ppid==1` 且启动时刻 ≥ shim」去扫。报告明确点名这是 B47 的误杀面
  （曾误杀 114 次），比诚实漏记更糟。
- 不改 grok/opencode 的 Bash 工具行为——那不是 handoff 的代码。

## 3. 方案

两处改动，**缺一不可**：名册修对了但归档路径不清扫，等于白修；归档路径清扫了但
名册还是空的，等于清了个寂寞。

### 3.1 名册改为「仍存活的旧条目 ∪ 当前 ppid 闭包」，采样间隔压到 1s

现在的 `snapshotRoster` 是「算出闭包 → 整份覆盖」。改成：

1. 读回上一轮名册（`readRoster`）；
2. **保留**其中「pid 仍在进程表里**且** `StartedAt` 一致」的条目；
3. 与本轮 `descendantsOf(shim, procs)` 的结果**取并集**（按 pid 去重）；
4. 落盘。

**删除条件只能是「pid 不在当前进程表」或「`StartedAt` 对不上」**，
**不能**是「本轮从 shim 沿 ppid 走不到」——后者正是现在这个 bug。
`StartedAt` 判据沿用既有的宁漏勿错语义（`roster.go:27-30`，B47 教训）。

**为什么两件事都要做**：

- 只压间隔不够：采到之后，下一轮全量覆盖仍会把它抹掉。
- 只改并集不够：工具壳只活约 1 秒，15s 的 tick 大概率**一次都没打中**它活着的窗口。

**间隔取 1s（可推翻）**。代价是每秒一次全进程表枚举（`enumProcsFn`，darwin 走
`KERN_PROC_ALL` sysctl，不 fork——`procenum.go:9-11` 那条「实现一律不得 fork」的
约束在这里仍然成立）。缓解两条，都要做：

- **内容未变则不落盘**：把本轮序列化结果与上一轮比对，一致就跳过 `writeRoster`。
  否则 2100 条名册在 1s 间隔下就是每秒几十 KB 的原子写 + rename。
- **一条枚举耗时的 Debug 日志**（带条目数与耗时），并在单次快照耗时超过间隔的
  一半时降级打 Warn——这样「名册采样把机器拖慢了」不会变成一个只能靠猜的故障。

**残余盲区（必须在代码注释里写明，不许假装没有）**：活得比采样间隔还短、且在
其存活窗口内没被采到的工具壳，它的后代仍然漏记。1s 把这个窗口从 15s 收到 1s，
不等于消除。

### 3.2 不做「名册顺带记 pgid」这一档增强（可推翻）

报告 §6.3 提了一个可选加强：名册顺带记下见过的后代 pgid，`Footprint`/`Sweep`
把「活着且 pgid 命中、启动时刻 ≥ 该组长」的进程也并进来。

**本 spec 决定不做**，理由是它的**独有覆盖面很窄，而代价是一个新的误杀面**：

- 若我们在工具壳活着时采到过它，那一刻它的子进程**本来就能由 ppid 闭包到**——
  并集规则已经收下了。pgid 增强唯一多覆盖的，是「采样发生在子进程出生**之前**、
  工具壳又在下一次采样**之前**死掉」这个**亚采样间隔窗口**。
- 而代价是：组长已死之后，pgid 会被内核复用。届时「pgid 命中」会把一批毫不相干
  的进程认成本任务成员——`classify` 现有的 `VerdictLeaderReuse` 判据（`footprint.go:75-78`）
  防的就是这件事，pgid 增强等于在它旁边开一个不设防的旁路。

**结论**：用 1s 采样把那个窗口压到最小，把残余漏记如实写进注释，而不是用一个
带误杀风险的判据去填。**如果用户认为漏记比误杀更不可接受，这一节要翻。**

### 3.3 `Done` 与 `Stop` 在停完 executor 之后各补一次清扫

`Manager.Done`（`manager.go:1129-1135`）与 `Manager.Stop`（`manager.go:1205-1210`）
都是 `adapterFor` → `stopExecutor` → 清理 worktree，中间**没有清扫**。两处都补。

**位置**：`stopExecutor` **之后**、worktree 清理**之前**。

- 必须在 `stopExecutor` 之后：`Sweep` 在存活锁仍被持有时直接返回 `ErrExecutorAlive`
  （`footprint.go:203`）——executor 没死就不许 Sweep，这是它与 `Kill` 的分工。
- 必须在 worktree 清理之前：还活着的进程把 cwd 钉在工作树里，会让 `git worktree remove` 失败。

**`ErrExecutorAlive` 竞态必须正面处理，不能装看不见。** `stopExecutor` 返回后
存活锁的释放依赖 shim 进程真正退出，两者之间有窗口。落到这个窗口上 `Sweep` 会被
拒，而 `SweepTaskProcs` 现在把它记成一条 `Info「交由常规回收路径」`——那条常规
路径在 `Done` 里**并不存在**，于是修复静默失效。这正是 B93 犯过的错（宣称
「终态即清扫」，实际每次都被 `ErrExecutorAlive` 拒掉，直到 B103 排查才发现）。

处置：抽一个不吞错误的内部函数，`Done`/`Stop` 用**有界重试**调用它——
最多 3 次、每次间隔 200ms，仅在 `ErrExecutorAlive` 时重试；用尽仍被拒就打一条
**Warn**（不是 Info），把 taskID 与 shim pid 带上。导出的 `SweepTaskProcs`
保持 best-effort 语义与现有签名不变（它的三个既有调用方不受影响）。

### 3.4 数据格式与兼容

`rosterEntry` 结构**不变**（`{pid, started_at}`）。名册文件格式不变，旧文件可直读，
新旧 agentd 互不干扰。这是有意的：本次不引入任何需要迁移的东西。

## 4. 影响面

| 文件 | 改动 |
|---|---|
| `internal/prochost/shim.go` | `snapshotRoster` 改并集 + 未变则不写 + 耗时日志；`rosterInterval` 15s→1s |
| `internal/prochost/roster.go` | 新增「合并旧名册与本轮闭包」的纯函数（便于单测） |
| `internal/agentd/reconcile.go` | 抽出返回 error 的内部清扫函数，`SweepTaskProcs` 变薄包装 |
| `internal/agentd/manager.go` | `Done` 与 `Stop` 各补一次带有界重试的清扫 |

`internal/prochost/footprint.go` **不改**——`rosterMembers`（`footprint.go:177-198`）
的逻辑本来就是对的，它只是没东西可读。

## 5. 风险

**采样从 15s 提到 1s 是 15 倍的枚举频率。** 这是本次唯一的性能风险，且它落在
「机器已经快 fork 不动」的场景里——那正是这套代码最需要可用的时刻。`enumProcs`
不 fork（sysctl / procfs），所以不会自我加剧，但仍必须实测单次耗时（§6 第 4 条）。

**并集让名册单调增长。** 上界是「当前仍存活的后代数」，因为每轮都剪掉已死的。
2000 进程的任务名册约 60KB，1s 一写——「未变则不写」正是为此。

**`Stop` 路径的行为变化要说清**：`stop` 之后会真的去杀这个任务名下的逃逸后代。
这是期望行为（`stop` 的语义就是「别跑了」），但它比现在杀得多，必须在 backlog
与提交信息里明说，免得日后有人以为是回归。

## 6. 验收

1. **`go build ./... && go vet ./... && go test -count=1 ./...` 全绿、0 FAIL。**
2. **名册并集的四条单测**（用 `enumProcsFn` 与 `rosterInterval` 两个既有测试缝，
   **不真 fork 进程**）：
   - 第一轮工具壳与子进程都能由 ppid 闭包到 → 都进名册；
   - 第二轮工具壳消失、子进程 `ppid` 变 1（走不到了）→ **仍在名册里**（本条是核心，
     它就是 bug 本身）；
   - 第三轮子进程从进程表消失 → 被剪掉；
   - 某 pid 仍在但 `StartedAt` 变了（pid 复用）→ 被剪掉。
3. **「内容未变则不落盘」有单测**：连续两轮同一批后代，第二轮不产生写入。
4. **单次快照耗时有实测数字**落在 ledger 里（不是「应该很快」，要有数）。
5. **`Done` 与 `Stop` 各有一条清扫单测**，形态对齐既有的
   `TestHandleResultSweepsProcsOnFail`（用 `sweepProcs` 测试缝）；
   另有一条断言 `ErrExecutorAlive` 会触发重试而不是静默放过。
6. **真机复验（由审核者做，不由 executor 做）**：在 mac-02 上派一个探针任务跑
   `for i in $(seq 1 450); do sleep 900 & done`，断言三件事：
   - `handoff status` 该任务那一行的进程数 **≥ 450**（现在是 3）；
   - 看门狗打出 **400 档告警事件**（B93 至今没有一次真机证据的那一档）；
   - `handoff done` 之后那 450 个 `sleep` **全部消失**（现在是一个不掉）。

   **为什么这条不派给 executor**：探针要在执行机上 fork 450 个进程，而 mac-02
   正是执行机——让它在自己身上跑这个，等于 B93 事故的重演路径。审核者手工做，
   做完自己收干净。

## 7. 明确不做

- 不改 `classify`、不改 `rosterMembers`、不改 `Kill`。
- 不做 pgid 增强（§3.2）。
- 不给名册加配置项：间隔仍是包内变量（测试缝），不上 `config.yaml`——加一个没人
  会调的旋钮只是增加了一处能配错的地方。
- 不动 B93 的 400/1200 两档阈值本身，本次只让它们**数得准**。
- 不顺手修 B100/B101/B104，发现的新问题记 backlog。
