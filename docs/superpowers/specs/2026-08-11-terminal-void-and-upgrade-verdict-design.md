# 终态工单作废 + 升级结论收敛（B63 + B64）设计

## 1. 范围与动机

两条缺陷，共同点是**审核者被一个确定但错误的结论引向错误的动作**——一条把人引去 reply 一个必然 404 的工单 id，一条把人引去给一台注定推不动的机器装 service。

| 条目 | 一句话 | 本 spec 的处置 |
|------|--------|--------------|
| B63 | 任务走到终态时，无人看护的挂起工单不会被作废，`pending_tickets` 长期挂着答不掉的幽灵单 | 做：终态迁移统一作废 + 审计事件 |
| B64 | `upgrade --now` 把「对端没上报」当成「非托管」，处置建议把人引向错误方向 | 做：三态 `Managed` + 两个消费方共用分类函数 |

放在一个 spec 里做，是因为两条是**同一类错误的两个实例**：把「我不知道」当成「我知道，答案是否/假」。B63 是「没有作废动作」被默认成「工单还有效」，B64 是 `Update == nil` 被 bool 零值默认成 `Managed == false`。仓库里已有对这条纪律的成文表述（[status.go](../../../internal/proto/status.go) 里 `Watchers *int` 与 `Update *UpdateStatus` 的注释），本 spec 是把它落到两处漏网点。

两条的代码路径完全不重叠（`internal/agentd` 的工单生命周期 vs `cmd/upgrade.go` 的巡检与处置），可以在一条分支上并行推进而互不干扰。

**不在范围内**：

- 回合结束（`waiting_review`）时的工单作废，见 §3.5
- 幽灵单的主动识别与标注（需要与 executor 侧未决请求对账，四家 adapter 各有形态，成本高一档）
- `handoff upgrade` 的 busy 闸语义与 `--force` 越闸规则，本 spec 只调它的**触发位置**，不改它的判据

## 2. 事实基础：读码复核结论

backlog 上记的两条修法方向，读码后都需要修正。以下是实现前必须以之为准的事实。

### 2.1 B63 漏的只有 `Done` 一条路径

`VoidPendingTickets` 已经挂在四处：`Stop`（[manager.go:1094](../../../internal/agentd/manager.go)）、executor 判死（manager.go:2405）、`reconcileExecutorGone`（[reconcile.go:173](../../../internal/agentd/reconcile.go)）、启动恢复。唯独 `Done` → `completed`（manager.go:1002）没有。

所以这不是「终态迁移全都没作废」，而是**两条终态路径只做对了一条**——`stop` 走 failed 时作废，`done` 走 completed 时不作废。

### 2.2 观察到那张幽灵单时任务并不在终态

B63 的来源观察是：任务走完 `running → waiting_review` 后，那张裸 uuid 单仍挂在 `pending_tickets` 里。`waiting_review` 不是终态（`TerminalStates` 只有 completed / failed，见 [proto.go:31](../../../internal/proto/proto.go)）。

**结论：本 spec 的修法不会让验收现场当时看到的现象立即消失**——那张单要等到 `done` 之后才被清。这是有意接受的，理由见 §3.5。如实记在这里，避免验收时按错误的预期去对照。

### 2.3 B64 不缺分支，缺的是顺序、类型和唯一口径

`process` 里**有**「对端过旧」分支（[upgrade.go:381](../../../cmd/upgrade.go)），文案也对。真问题分三层：

1. **顺序**：闸二（非托管，upgrade.go:352）排在它前面。
2. **类型**：`machineState.Managed` 是 `bool`，`probeMachine` 只在 `st.Update != nil` 时才写它（upgrade.go:248），老 agentd 不上报 `Update` 于是保持零值 `false`——「对端没告诉我」静默变成「它是非托管」，先掉进闸二就返回了，:381 永远够不着。
3. **口径**：`renderCheckRow`（只读巡检）与 `process`（`--now`）各自维护一套 if/switch 给同一台机器下结论，分支集合与优先级并不一致。前两层是这一层的症状。

**同一个病还有另外两个实例**，都不是新需求，是收敛口径后自动消失的：

| 机器状态 | `handoff upgrade`（只读） | `handoff upgrade --now` | 问题 |
|---|---|---|---|
| 远端非托管、但已是最新 | 「已是最新」 | 「跳过 非托管」+ 建议 `service install` | 没事可做却被催着装 service |
| 有活跃任务、但已是最新 | 「已是最新」 | 「跳过 N 个活跃任务」+ 建议 `--force` | 建议的那条命令跑完只会说「已是最新」，白跑一轮 |

三处（含 B64 本身）都是「两套分支表各活各的」的必然产物，靠逐个打补丁按不住。

## 3. 设计 A：终态统一作废（B63）

### 3.1 接缝：`Manager.transit`

作废挂在 `transit`（[manager.go:2471](../../../internal/agentd/manager.go)）里，判 `to.IsTerminal()` 命中即执行。

选它的理由：`Done` / `Stop` / 各处 `transitBestEffort` 全部经过它，一处实现覆盖当前全部终态路径，且**将来新增的终态路径自动包含**——B63 本身就是靠「新增一条路径时忘了补」漏出来的。它还现成带一个 `reason` 参数（`"done"` / `"stop"`），可直接用作审计事件的原因，无需另造。

不下沉到 `store.UpdateTaskState`：数据层不该自作主张改写业务语义，且它拿不到 hub、发不了事件。

### 3.2 顺序：CAS 写状态成功之后才作废

必须在 `m.st.UpdateTaskState` 返回 nil 之后。反过来先作废是错的：该迁移可能因并发 CAS 失败（`ErrBadTransit`），任务仍然活着，而它的合法挂起工单已经被砸了。

`transit` 的幂等分支（`cur.State == to` 直接返回 nil）不触发作废——已经在终态说明上一次迁移已经作废过，`VoidPendingTickets` 本就幂等（第二次起返回 0），不产生重复审计事件。

### 3.3 助手：`voidTicketsWithAudit(taskID, reason)`

三件事：

1. 调 `m.st.VoidPendingTickets(taskID)`；出错只记 Error 日志、不中断迁移（状态已经落库，为一次审计写失败回滚终态得不偿失）。
2. `voided > 0` 时打一条 Warn 日志（与现有两处的日志形态一致）。
3. `voided > 0` 时追加 `tickets_voided` 事件，payload 为 `{voided, reason}`。

`voided == 0` 时**什么都不产出**：绝大多数任务终结时没有挂起工单，无条件发事件等于给每个正常任务的事件流添一条噪音。

### 3.4 事件：`EventTypeTicketsVoided`，只入库不 Publish

新增 `EventTypeTicketsVoided EventType = "tickets_voided"`，并加进 [client.go](../../../internal/client/client.go) 的 `isDeliverable` 黑名单，与 `approver_decision` / `approver_disabled` 同待遇。

**为什么必须不可交付**：一次性 `handoff wait` 靠首个可动作事件收手。终态时刻同时产生 `completed` 与 `tickets_voided`，若后者可交付，审核者可能拿到的是一条审计噪音而不是任务真正的结论——这正是 B40 关心的「结论被顶掉」那一类。`all=true` 时全量交付不受影响，排障仍看得见。

### 3.5 边界：不碰回合结束

`result` → `waiting_review`（manager.go:2416）**不**作废挂起工单。

理由是那里的挂起单未必是幽灵：grok 与 opencode 的提问中继就是「回合已结束、人稍后 `reply --answer` 补答」，B3 与 B49 都真机验过 `relayed=true`。在回合结束时统一作废等于砸掉这条既有能力，换来的只是幽灵单早消失几个回合。

代价如实记录：审核者接管一个陌生会话时，若该任务尚未终结，仍可能看到一张答不掉的幽灵单。B58 已经消灭了这类单的常规产生路径（重启重放不再新建单），存量与残留由终态作废兜底。

### 3.6 现有调用点的收敛

- `Stop` 里的显式作废（manager.go:1094）**删除**，交给收口点。不删的话 stop 路径永远 `voided == 0`，拿不到审计事件，两条终态路径的痕迹又不一致——而痕迹不一致正是本条目要修的东西。删除后 stop 的作废时机从「失败事件之前」后移到「状态迁移之后」，窗口是进程内的微秒级，且该窗口内任务已是 failed、`reply` 本就被状态校验挡下。
- `reconcileExecutorGone`（reconcile.go:173）**保留**显式调用，但改为复用同一个助手。它走的是 `recoverTransit` 且目标是 `waiting_review`（非终态，属「executor 已死」的合法回合级作废），不经过 `transit`；复用助手让这条路径也产出审计事件。

## 4. 设计 B：一台机器一套说法（B64）

### 4.1 `Managed` 改三态

`machineState.Managed` 由 `bool` 改 `*bool`，`probeMachine` 只在 `st.Update != nil` 时取地址赋值。nil 的含义是**对端没给这个字段**，与「对端说 false」严格区分。这与同文件同结构里 `ActiveTask.Watchers *int` 的既有纪律一致。

### 4.2 `classify(ms, latest) verdict`：优先级只定义一次

抽一个纯函数，返回单一结论：

| verdict | 判据 | 优先级理由 |
|---|---|---|
| `agentdDown` | 本机 `Err != nil` | 本机 agentd 没跑不是失败，敲命令的人知道自己要不要把它起回来 |
| `unreachable` | 远端 `Err != nil` | 连不上就没有任何后续结论可下 |
| `tooOld` | 远端 `Platform == ""` | 排在托管判定之前：连平台都不上报的对端，它的托管状态同样不可信 |
| `latest` | 版本已对齐（本机需二进制与 agentd 都对齐） | 排在托管判定之前：没事可做时不该催人装 service |
| `unmanaged` | `Managed != nil && !*Managed` | 明确上报的 false 才算非托管 |
| `managedUnknown` | `Managed == nil` 且平台已上报 | 见 §4.4 |
| `needsUpgrade` | 其余 | 兜底 |

本机不判 `tooOld`：本机不需要按平台去取远端资产，`Platform` 对它无意义。

### 4.3 两个消费方各自的职责

- `renderCheckRow`：verdict → 一句结论文案。本机仍分别显示二进制与 agentd 两个版本（该规则来自 B59 spec §4.1，不受本次改动影响）。
- `process`：verdict → 一个动作。busy 闸与 `--force` 留在 `process`，且**只在 `needsUpgrade` 这一格生效**——只有真要换版时，「有活跃任务」才是一个需要越过的障碍。

两套口径从结构上不再可能分叉：分支集合与优先级都只有 `classify` 一个来源。

### 4.4 `managedUnknown` 为什么单列

这一格正是 bool 塌缩掉的那一格。处置是**跳过并如实说明**「对端未上报托管状态，无法确认换版后有没有人把它拉起来」：

- 不猜成托管：猜错就是换完 exit(0) 之后没人拉起，这台机器上就此没有 agentd 在跑，且没有任何信号告诉任何人。
- 不猜成非托管：猜错就是把人引去装一个可能早已装好的 service，即 B64 的原始症状。

**这一格在已发布版本里不可达**：`Platform` 与 `Update` 是同一条演进线，`Update` 在 v0.1.0 就有（`e744692d`），`Platform` 在 v0.1.1 加入（`2bbbddc7`），不存在只报前者的版本。它是防御格，由单测钉住，不为它伪造真机证据（见 §6.3）。

### 4.5 连带自愈

`Platform == ""` 从此到不了 `remoteUpgrade`，那句「对端上报的平台 `""` 格式非法」只在真·格式错时出现，无需单独改动。

## 5. 测试

### 5.1 B63（`internal/agentd`）

| 用例 | 钉住的契约 |
|---|---|
| `transit` → `completed`，任务有挂起工单 | 工单被作废；产出一条 `tickets_voided`，payload 计数与 reason 正确 |
| `transit` → `waiting_review`，任务有挂起工单 | 工单**不**被作废（§3.5 的护栏，防止将来把范围顺手扩到回合结束） |
| `transit` 因非法迁移失败 | 工单不被作废（§3.2 的顺序正确性） |
| 无挂起工单的正常 `done` | 不产出 `tickets_voided`（零噪音） |
| 重复 `done`（幂等分支） | 不产出第二条 `tickets_voided` |

外加 `internal/client` 一条：`isDeliverable(EventTypeTicketsVoided) == false`。

`Stop` 的现有测试跟随调整：显式作废删除后，作废行为不变，且新增一条审计事件。

### 5.2 B64（`cmd`）

- `classify` 表驱动：覆盖 7 个 verdict，并显式覆盖三处优先级——`tooOld` 先于 `unmanaged`、`latest` 先于 `unmanaged`、busy 只在 `needsUpgrade` 之后。
- **两边一致性测试**：同一批 `machineState` 分别喂给 `renderCheckRow` 与 `process`，断言两者结论不互相矛盾。这条是 B64 的核心契约，其余用例是它的分解。busy 那条在 `process` 层用假 peer 覆盖。

## 6. 真机验收

### 6.1 B64：两种「过旧」画像，不能只测一种

| 画像 | 怎么造 | 对端行为 | 今天的表现 | 修完的期望 |
|---|---|---|---|---|
| P1 无 `/api/status` | 从 `70d147d3`（B33 加 status 契约）**之前**的提交现编，如 `c558f240` | `ErrStatusUnsupported`，平台与托管状态都拿不到 | **B64 现场那台就是它**：Managed 塌成 false，报「非托管，先去 service install」❌ | 「对端过旧，需手工升级到 ≥v0.1.1」 |
| P2 v0.1.0 | `git checkout v0.1.0` 现编 | 有 status、有 `Update.Managed`，无 `Platform` | 已经报对（Managed 为真，越过闸二够到 :381）✅ | 结论不变，作为回归护栏 |

两台都要跑 `handoff upgrade`（只读）与 `handoff upgrade --now --target <名>`，核对**两条命令对同一台机器给出的结论一致**——这是本条目的验收要点，单看某一条命令的文案对不对是不够的。

### 6.2 操作安全纪律

- 两台旧实例各用独立端口（7788 / 7789）与独立 DataDir（`~/.handoff-b64-p1` / `~/.handoff-b64-p2`），不碰生产的 7777 与 `~/.handoff`（照 B31 复现时那套隔离做法）。
- `--now` 一律带 `--target` 指名道姓。**绝不裸跑**：裸跑会把本机与生产 devbox 一起换版。

### 6.3 不为不可达的分支伪造证据

`managedUnknown` 在已发布版本里造不出来（§4.4），真机验收不覆盖它，由单测钉住。验收记录里如实写明这一点。

### 6.4 B63

生产实例上派发一个任务 → 让它提一个问题、不作答 → 等它进 `waiting_review` → 直接 `handoff done`，核对三件事：

1. `pending_tickets` 清空；
2. `handoff show` 里有一条 `tickets_voided`，计数与 reason 正确；
3. `handoff wait` 收到的是 `completed`，不是被审计事件抢先（§3.4）。
