# B390 debug 计划：唤醒消费轮在持续准入失败后静默停摆——根因、分流与修复任务

> 卡 B390 · 入口节点 charter:debug · spec `docs/superpowers/specs/b390.md`（已批准）
> 基线分支 `cards/B390-charter-2`，HEAD `83a811989e558588e014b3e2d4cd9d6be033fa7a`（动手前核对，行号漂了以符号为准）。
> 台账 `docs/superpowers/ledgers/2026-09-21-b390-plan-ledger.md`（含红色回路实跑原文与只读账本读数）。
> 读者：对 handoff 仓零上下文的执行者。本计划的结论型事实全部来自台账记录的亲跑命令与原始输出；未跑到的标「未验证」。

---

## 0. 待拍板清单（阻塞实施，交协调者裁决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **有效基线是否先并 B389 第 2–7 片 / `130a6634`？** | (甲) 先合 `origin/cards/B389-charter-7`（`6f20dfce`）或 `130a6634` 再实施；(乙) 直接在本分支实施 | 本分支 `internal/agentd/wakeconsumer.go:421-458` 是 B389 之前的旧失败路径（吞事件、推进游标）；`6f20dfce` 已把它改成「认领 + 退避 + 单卡失败 continue + 终局前缀水位」。直接在本分支实施会**重写 B389 已收口的失败路径**（spec §3 明写「不动 B389 已收口的单卡失败路径」）。台账已有证据：本分支缺 `6f20dfce`（`git merge-base --is-ancestor 6f20dfce HEAD` → NO）。 |
| **P2** | **「按节拍重试」的重试载体** | (甲) 本计划 T2 的内存重试表（`automationRetry`，跨重启丢，H4 自愈另立卡）；(乙) 并 B389 后改用「失败不 `completeWakeBatch`、保留认领挡水位 + `shouldSkipByBackoff`」；(丙) 另建 per-card 唤醒重试队列 | 决定 T2 代码块。甲改本分支即可编译；乙更贴契约但依赖 P1 合线；丙是架构级、回 spec。 |
| **P3** | **`--mention @卡号` 前缀不解析，归 B390 还是另立卡？** | (甲) 并入 B390（一行 `strings.TrimPrefix(mention,"@")`）；(乙) 另立卡 | 实测 `@B1` 不唤醒（台账诊断件），CLI 帮助文本写的是「@成员或卡号」；触及契约 §3.8 寻址语义，属跨子系统寻址面，保守应回 spec/另立卡。**本计划按 (乙) 记录为独立发现，不并入 T1–T3**。 |
| **P4** | **重试节拍 / 升级阈值常量** | 建议：重试节拍 = 既有轮询 `automationPollInterval=2s`（即每轮都重试一次，无额外退避）；连续失败 N=3 落一次 needs_human | 写入 T2 常量。**为什么不做秒级退避**：T1 回路后连续 3 轮（背靠背、无真实 2s 间隔）内必须再试，若加 30s 退避则回路永远转不绿；且「按节拍重试」的字面节拍就是 2s 轮询。要退避须同时引入可注入时钟缝（`automationNow`），属架构级，回 spec。 |

---

## 1. 根因（证据驱动）

### 根因 R1：消费轮「准入失败即吞事件、不重试、不落需要人」

**现状读码**（本分支 `internal/agentd/wakeconsumer.go:421-458`）：`wakeCoordinatorRound` 返回错误后，代码只 `s.log.Error("自动化事件批次唤醒失败", ...)`，随后（若 `result.Escalated`）把自生 `needs_human` 标 `seen`，再 `s.advanceAutomationCursor(maxProcessed)`，最后 `return processed, escalated, wakeErr`。即：

1. 失败后**游标照常推进**、事件标 `seen` → 该唤醒永远不会再被读到；
2. 准入满员（可恢复的排队态）与账本故障、承载缺失走**同一条吞事件路径**；
3. 全程不落卡级 `needs_human`（`needs_human` 只在 `result.Escalated` 时由 keystone 内部产生，准入失败不产生）；
4. 每轮无成功心跳之外的可行动信号，2s 节拍下静默。

**红色回路实跑**（台账原文，`alwaysNoSlotScheduling` 恒返回 `scheduling.ErrNoSlot` 驱动 `runAutomationPass` 3 轮）：

```
RED(a) 准入持续失败时未按节拍重试：3 轮只尝试 1 次（want ≥ 3）
RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）
--- FAIL: TestB390RedLoopStalledAdmission (0.23s)
```

诊断件补充（台账原文）：`准入失败: event_seq=5 cursor 4 -> 5 admits=1` / `第二轮: admits=1（同 seq 未重试）`——**失败吞事件**被直接观测到。

### 根因 R2：`--mention @卡号` 字面前缀不解析（真机验收链被堵的直接原因）

**实测**（台账诊断件 `TestB390DiagMentionWithAtPrefix`）：mentions 传 `"@B1"`（与卡上 `seq=16861` 的 `mentions:["@B382"]` 完全同形），消费轮 `processed=0 resumes=0`——不唤醒。原因：`internal/collab/sessions.go` 的 `resolveMessageTargets` 把 mention 原样交给 `s.lc.GetCard(mention)`（`internal/collab/sessions.go:131-139`），`GetCard("@B382")` 在库中不存在（远程账本实测 `cards with id='@B382': 0`、`cards with id='B382': 1`），于是按外部会话身份处理，`room.ResolveDelivery` 落空。而 `cmd/session.go:558` 的 `--mention` 帮助文本写的是「@成员或卡号（可重复；寻址唤醒的来源）」→ **话术与实现不一致**。真机据此写的 `@B382` 因此不命中。

### 根因 R3（H4 判定）：`sched_running` 名额键无租约 TTL，启动对账只读不清，失败残留跨重启不自愈

**共享账本只读读数**（台账，`registry` 表，`postgres://handoff@100.84.251.46:54322/handoff`）：

- `sched_running|squad/leader/cmd` count=**1**，updated_at=**2026-09-19T23:42:32**（正是故障时刻），version=107——此后**再未变化**；
- 同族陈旧残留：`squad/leader/muse`=2（09-05）、`squad/leader/grok`=3（09-07）、`squad/leader/agy`=1（09-05）、`carrier/grok`=4（09-07）等；
- 本机 sqlite 同样有 09-01 起的残留 `squad/coord/opencode`=1、`carrier/opencode`=1（`2026-09-01T22:42:45`，台账）。

**不自愈机制读码**（本节点亲读）：

- `internal/scheduling/scheduling.go` 的 `sched_running` 只有 `acquire`（`968`）`+1`、`Release`（`747`）`-1`、`stepRunning`（`1099`）带符号增量；`readCount`（`1073`）只解码 `{count}`——**无 owner、无 expires/TTL 字段**；
- 启动对账 `orchestration.ReconcileSchedRunning`（`internal/orchestration/watchdog.go:467`）注释明写「对共享 sched_running **只读不写**」「保留无本机 owner 的运行计数（跨机占用不得清零）」（`watchdog.go:507`），且只在 agentd 启动时跑一次。

**结论（H4 成立）**：一次失败（或崩溃/会话残留）留下的计数不因重启、不因时间而消失；`squad/leader/cmd` 的 1 自 09-19T23:42 起持续占用，**跨「换版重启」依然存在**，与现象「换版重启后依旧静默」吻合。

**边界澄清（诚实差异）**：`squad|leader` 的成员政策是 `max_concurrency:3`，`squad/leader/cmd` count=1 **单独不足以**触发 23:42 的「并发已满」；当时应是 `carrier/cmd`（物理位 `max_concurrency:2`）先满使 `acquire` 返回 `errMemberFull`（`scheduling.go:996`）→ `ErrNoSlot`。故 R3 是「**残留不可自愈**」的根因，不是「此刻必然满员」的根因。按卡红线：**不清理这些键**。

### 根因 R4（H5 判定）：现象含「日志缺席」成分——默认日志级别 `warn` 吞掉循环 INFO 心跳

**读码 + 读数**：`internal/logx/logx.go:32` 按 `HANDOFF_LOG_LEVEL` 取级别，缺省回退 `slog.LevelWarn`（`logx.go:55`）；真机 systemd `Environment=` 为空（本节点跑 `systemctl show handoff-agentd -p Environment`），故循环的 INFO（`自动化事件消费轮完成`、`自动化编排已挂载`）**全部不可见**。真机 `agentd.log` 里 `自动化` 行仅 3 条，且全在 `2026-09-16T21:33`（含 `自动化循环停止 cause=context canceled`）；`协调者小队` / `leader 准入失败` / `批次唤醒失败` 计数均为 0。

**H5 成立（部分）**：故障期「23:42 后一行自动化都没有」是**日志级别吞 INFO 心跳**叠加故障期进程反复重启（`agentd.log` 09-19T23:23 有 4 条「打开账本库失败 SASL auth」、23:25 后直接跳到 09-20T00:46，23:42 窗口无行）的产物。**观测性缺陷**：循环「在跑」与「死了」在日志上不可区分。

---

## 2. H1–H5 逐条判定

| 假设 | 判定 | 依据 |
|---|---|---|
| **H1 循环退出了**（Run 返回、goroutine 结束、无人重启） | **不成立（当前实例）** | `automation-cursor.json` 持续变化：16:32:26 `{"seq":17114}` → 16:58:08 `{"seq":17175}`；`seq 16861`（09-21 15:10 那条 `@B382`）已被消费（cursor 17175 > 16861）。循环在跑，非退出。**残余风险**：`automationLoop`（`scheddrain.go:66`）无 `recover()`，单 goroutine panic 不受 systemd `Restart=always` 罩（本机无 panic 证据，标未验证）。 |
| **H2 循环在跑但只在错误路径静默** | **成立（本分支码）**；真机 130a6634 另有 R2 | 红色回路 (a)(b) 双红；本分支失败路径不落 needs_human、无成功/失败节拍可见信号。真机 130a6634 含 `6f20dfce`，其失败路径 `s.log.Error("自动化事件批次唤醒失败")` 应可见，但被 H5 的级别+进程重启叠加掩盖。 |
| **H3 ctx 被取消后无人重启** | **不成立 / 无证据** | ctx 由 `cmd/agentd.go` 随进程生命周期持有，agentd 常驻未退出；循环在跑（H1 反证）。若未来 Run 返回，当前无「重启循环」路径——属 R1 生命周期残余，未验证。 |
| **H4 名额键残留使准入永久失败** | **成立（残留不可自愈的部分）** | 见 R3：`squad/leader/cmd`=1 自 09-19T23:42 未变；无 TTL/owner；启动对账只读不清。**限定**：当前 count=1 < 成员政策 3，单独不满；23:42 的直接满员来自 `carrier/cmd`（物理位 2）。 |
| **H5 现象是日志缺席而非循环死亡** | **成立（部分）** | 见 R4：默认级别 warn 吞循环 INFO 心跳；真机无自动化 ERROR 行是级别+故障期重启叠加。当前实例循环在跑但日志无感。 |

---

## 3. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| **R1 可见性半边**（准入持续失败落 needs_human、轮次节拍可见、不静默） | 一两行小修 + 日志级别 | **本计划 T2** |
| **R1 重试半边**（真「按节拍重试」且不吞事件） | 改游标/认领语义 = 架构级 | **回 spec 重新定级**（或按 P1/P2 并 B389 后收口）；本计划 T2 给最小实现（待 P2 拍板） |
| **R2 `@卡号` 前缀** | 跨子系统寻址语义，触及契约 §3.8 | **待拍板 P3**（倾向另立卡） |
| **R3 名额键 TTL / owner / 自愈** | 改 `sched_running` schema + 对账语义 = 架构级 | **回 spec 重新定级**；本计划 T3 只做**观察面** |
| **R4 日志级别吞心跳** | 观测性小修 | **本计划 T2**（失败/停摆走 Warn/Error + needs_human；不改全局默认级别） |

> spec §2.4：「一两行小修顺手修掉；架构级修复回本 spec 重新定级，不许在排查现场顺手动架构。」本计划严格照此：T2/T3 是可见性与观察面，架构级项（R1 重试语义、R3 自愈）只出结论、不写实现。

---

## 4. 任务 DAG

```
T0（修基线编译破口，前置）
  └→ T1（红色回路转正为回归测试；锁 runAutomationPass 三条断言）
        └→ T2（准入持续失败：按节拍重试 + 落 needs_human + 不静默）
              └→ T3（名额键残留观察面 CLI/HTTP）
（独立）R2 @前缀、R3 TTL/自愈 → 回 spec / 另立卡，不在本 DAG
```

次序承重：T1 的回路断言依赖包可编译（T0）；T2 跑红必须已有 T1 的回路载体；T3 的读面依赖 T2 失败态可复现以造出非零残留。全量测试不属于任何单个 task（implement 三段律）。

---

## 5. 基线事实（所有 task 共享，动手前不必重跑）

**判据基线（本节点亲跑，原始输出见台账）**：

- `go vet ./internal/agentd/`（未改任何文件）→ 编译失败：
  ```
  internal/agentd/scheddrain_test.go:271:78: undefined: ledger
  internal/agentd/wakeconsumer_b358_test.go:204:106: undefined: ledger
  internal/agentd/wakeconsumer_b358_test.go:232:106: undefined: ledger
  internal/agentd/wakeconsumer_b358_test.go:291:98: undefined: ledger
  internal/agentd/wakeconsumer_b358_test.go:294:129: undefined: ledger
  ```
  根因：两文件用 `ledger.SeatBearing`（`internal/ledger/binding.go:149`）未 import `internal/ledger`；由 B389 commit `734caefc` 引入、`6f20dfce` 补齐，本分支只合到 `cc77e0a3`（不含 `6f20dfce`）故落破口。
- **T0 后基线绿**：补齐 import 后 `go test ./internal/agentd/ -run 'TestB3583|TestAutomation|TestDrainQueues|TestCoord' -count=1` → `ok ... 11.303s`；全包 `go test ./internal/agentd/ -count=1` → `ok ... 165.798s`。这是各 task 跑红/跑绿的基线参照：除本计划显式标注「预期红」的新测试外，任何既有测试翻红都先停下查原因，不许顺手改断言。

**服务端事实（判据出处，本节点亲读复核）**：

- 消费轮入口 `func (s *Server) runAutomationPass(ctx context.Context)`（`internal/agentd/scheddrain.go:83`）：每轮 `consumeAutomationEventsOnce` + `drainQueuesOnce`，错误只 `s.log.Error`。
- 消费轮本体 `func (s *Server) consumeAutomationEventsOnce(ctx) (processed int, escalated bool, err error)`（`internal/agentd/wakeconsumer.go:296`）；失败分支 `wakeconsumer.go:421-458`（吞事件、推进游标）。
- 入口 `StartAutomation`（`scheddrain.go:52`）依赖未装配只 `Warn` 即 return；`automationLoop`（`scheddrain.go:66`）无 `recover()`；轮询 `automationPollInterval = 2 * time.Second`（`scheddrain.go:23`）。
- 准入错误形状：`coordinatorAdmissionError{squad, err}`（`scheddrain.go:26-35`），由 `wakeCoordinatorRound` 的 `LaunchAdmit` 失败构造（`scheddrain.go:359-362`）；`LaunchAdmit`（`scheduling.go:712`）经 `admitInto`（`922`）→ `acquire`（`968`）；物理位满 `errMemberFull`（`996`）→ 外露 `ErrNoSlot`（`scheduling.go:149`）。
- 名额键：`OccupancyKeys` / `OccupancyMemberKey` / `OccupancyCarrierKey`（`internal/scheduling/occupancy.go:10/20/28`）；`kindRunning = "sched_running"`（`scheduling.go:30`）。
- 落等人：`Store.MarkNeedsHuman(cardID, reason, actor string) error`（`internal/ledger/events.go:384`）与 `Facade.MarkNeedsHuman`（`internal/ledger/api/keystone.go:17`）；`Server.ledger *ledger.Store`（`server.go:110`）、`Server.autoLedger *ledgerapi.Facade`（`server.go:176`）。
- 测试夹具（照抄，勿新造）：`newNoPTYAutomationEnv`（`wakeconsumer_test.go:28`）、`seedQueueCoordinator`（`scheddrain_test.go:60`）、`createCoordCard`（`coordapi_test.go:146`）、`mustWakeSessionFixture` / `drainAutomation` / `sendSessionMessage`（`wakeconsumer_b358_test.go:26/42/50`）、`mustScheduling`（`b23314_scheduling_testhelpers_test.go:86`）、`runningCountIn`（`scheddispatch_test.go:92`）。
- 观测面入口：`registerSchedulingRoutes`（`internal/agentd/schedapi.go:37`，`GET /api/squads`、`GET /api/queue`）；`handleSquadsGet`（`schedapi.go:63`）；`SquadRows`/`CarrierRows`/`QueueSnapshot`（`internal/scheduling/registry_read.go:44/57/83`）；wire `CarrierView`/`SquadView`/`SquadsResp`（`internal/proto/scheduling.go:18/38/47`，与 `web/src/api/scheduling.ts:6/23/35` 镜像）；client `Squads`（`internal/client/squads.go:34`）；CLI `squadListCmd`（`cmd/squad.go:146`）。

---

## Task 0：修复基线测试编译破口（前置，红→绿）

**为什么单独成 task**：本分支 `internal/agentd` 测试包**当前不编译**，任何回归测试都跑不起来；这是 B390 全部后续步骤的前置。它本身是一处漏 import，不是本卡根因。

**文件集**：`internal/agentd/scheddrain_test.go`、`internal/agentd/wakeconsumer_b358_test.go`。

### Interfaces

Consumes（既有，不改）：`ledger.SeatBearing`（`internal/ledger/binding.go:149`）；`Store.BindSeat/RebindSeat(..., SeatBearing)`。
Produces：无（仅 import 补齐）。

### 步骤

1. **跑红（基线原文已有，复核即可）**：`go test ./internal/agentd/ -run TestAutomationQueueRestartReplay -count=1` → `# github.com/Xsxdot/handoff/internal/agentd [build failed]`，`undefined: ledger`（见 §5）。
2. **最小实现**：在两文件的 import 块各加一行（`scheddrain_test.go` 加在 `internal/ledgerstep` 之前；`wakeconsumer_b358_test.go` 加在 `internal/collab` 之后）：
   ```go
   "github.com/Xsxdot/handoff/internal/ledger"
   ```
   （goimports 会按字母序自动归位；两份文件本来就分别用了 `ledger.SeatBearing`，加 import 后无 unused。）
3. **跑绿**：`go test ./internal/agentd/ -run 'TestB3583|TestAutomation|TestDrainQueues|TestCoord' -count=1` → `ok`；`go test ./internal/agentd/ -count=1` → `ok`。
4. **日志/注释步骤**：不新增日志（纯 import 修复，无成功/失败分支）；不新增注释（改动不言自明）。
5. **提交**：`git add internal/agentd/scheddrain_test.go internal/agentd/wakeconsumer_b358_test.go && git commit -m "fix(B390): 补 internal/agentd 测试缺失的 internal/ledger import——基线测试包恢复可编译"`。

**判据**（钉行为不钉计数）：改前 `undefined: ledger` 编译失败；改后同命令退出 0 且既有测试全绿。

### 接缝覆盖

本 task 的入口符号是 `go test` 编译面，不在 B390 缝清单上；**属内部锁（编译前置）**，声明理由唯一形状：**从声明缝构造不出「测试包可编译」这条断言——编译失败时任何缝级测试都无法被构造**。此声明为占位符扫描节所要求。

---

## Task 1：红色回路转正为回归测试（锁 `runAutomationPass` 三条断言）

**为什么**：spec §2.2 红线——修之前必须先有能复现该 bug 的失败测试。本节点已用临时件跑出红（台账），本 task 把它落成仓内回归测试（转正），锁住「准入持续失败不许静默」。

**文件集**：`internal/agentd/b390_wake_stall_test.go`（新建）。

### Interfaces

Consumes（既有，不改）：

- `func (s *Server) runAutomationPass(ctx context.Context)`（`scheddrain.go:83`）——本 task 的**入口缝**。
- `Server.SetScheduling(svc SchedulingClient)`（`server.go:1022`）；`SchedulingClient`（`internal/agentd/scheduling_client.go:24`）。
- 夹具：`newNoPTYAutomationEnv`、`createCoordCard`、`mustWakeSessionFixture`、`drainAutomation`、`sendSessionMessage`、`mustScheduling`。
- `ledger.EvNeedsHuman`；`scheduling.ErrNoSlot`、`scheduling.Binding`。

Produces（测试内类型，同文件）：

- `type alwaysNoSlotScheduling struct { *scheduling.Service; mu sync.Mutex; admits int }`，覆写 `LaunchAdmit(string) (scheduling.Binding, error)` 恒返回 `fmt.Errorf("stub 准入满员: %w", scheduling.ErrNoSlot)`；`func (s *alwaysNoSlotScheduling) admitCount() int`。
- `func b390HasNeedsHuman(t *testing.T, env *ledgerEnv, cardID string) bool`。

### 步骤（红绿）

1. **判据先在基线跑**：`go test ./internal/agentd/ -run TestB390WakeStallRedLoop -count=1` 在只有 T0 的基线上必然 `FAIL`（测试尚不存在是编译错；先写下测试，再跑出**断言红**）。预期红原文（本节点临时件实测）：
   ```
   RED(a) 准入持续失败时未按节拍重试：3 轮只尝试 1 次（want ≥ 3）
   RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）
   ```
2. **写测试（完整代码，照抄）**：

   ```go
   // b390_wake_stall_test.go —— B390 回归回路：唤醒消费轮在持续准入失败后不得静默。
   //
   // 职责：用「准入恒返回并发已满」的调度桩驱动 runAutomationPass，锁住三条同时
   // 成立——(a) 按节拍重试而非一次就停；(b) 停摆落成账本可见 needs_human；
   // (c) 循环不静默退出。入口缝是 Server.runAutomationPass。
   // 边界：只覆写 LaunchAdmit 这一级准入面，其余走真实 ledger/keystone/rooms 接线；
   // 不复制 scheduling 的准入或 keystone 的重建规则。
   package agentd

   import (
       "context"
       "fmt"
       "sync"
       "testing"

       "github.com/Xsxdot/handoff/internal/ledger"
       "github.com/Xsxdot/handoff/internal/scheduling"
   )

   // alwaysNoSlotScheduling 把协调者准入恒判为满员，并记录尝试次数。
   type alwaysNoSlotScheduling struct {
       *scheduling.Service
       mu     sync.Mutex
       admits int
   }

   func (s *alwaysNoSlotScheduling) LaunchAdmit(string) (scheduling.Binding, error) {
       s.mu.Lock()
       s.admits++
       s.mu.Unlock()
       return scheduling.Binding{}, fmt.Errorf("stub 准入满员: %w", scheduling.ErrNoSlot)
   }

   func (s *alwaysNoSlotScheduling) admitCount() int {
       s.mu.Lock()
       defer s.mu.Unlock()
       return s.admits
   }

   // b390HasNeedsHuman 扫全流看该卡是否落了 needs_human。
   func b390HasNeedsHuman(t *testing.T, env *ledgerEnv, cardID string) bool {
       t.Helper()
       events, err := env.ledger.EventsFromAsc(nil, 0, 100000)
       if err != nil {
           t.Fatalf("读账本事件: %v", err)
       }
       for _, ev := range events {
           if ev.Type == ledger.EvNeedsHuman && ev.CardID == cardID {
               return true
           }
       }
       return false
   }

   func TestB390WakeStallRedLoop(t *testing.T) {
       env, _ := newNoPTYAutomationEnv(t)
       cardID := createCoordCard(t, env)
       sessionID, svc := mustWakeSessionFixture(t, env, cardID)
       drainAutomation(t, env)

       stub := &alwaysNoSlotScheduling{Service: mustScheduling(t, env.srv)}
       env.srv.SetScheduling(stub)

       sendSessionMessage(t, svc, sessionID, "user:tester", "请看这张卡", []string{cardID}, 0)

       const passes = 3
       for i := 0; i < passes; i++ {
           env.srv.runAutomationPass(context.Background())
       }

       // (a) 按节拍重试：N 轮后准入至少被尝试 N 次。
       if got := stub.admitCount(); got < passes {
           t.Errorf("RED(a) 准入持续失败时未按节拍重试：%d 轮只尝试 %d 次（want ≥ %d）", passes, got, passes)
       }
       // (b) 停摆必须落成账本可见 needs_human，不许只写日志。
       if !b390HasNeedsHuman(t, env, cardID) {
           t.Errorf("RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）")
       }
       // (c) 循环不静默退出：仍应有准入尝试。
       if got := stub.admitCount(); got == 0 {
           t.Errorf("RED(c) 消费循环静默退出：无任何准入尝试")
       }
   }
   ```

3. **跑红**：`go test ./internal/agentd/ -run TestB390WakeStallRedLoop -count=1 -v` → `FAIL`，`RED(a)`/`RED(b)` 双红（与台账一致）。
4. **本 task 不转绿**：回路依赖 T2 的修复。T1 收尾时该测试保持红是**预期**，T1 的提交信息与台账显式声明「回路红，待 T2 转绿」；全包测试在 T1 后仍红一条（implement 纪律允许：本 task 声明的范围只有本测试）。
5. **日志/注释步骤**：测试文件头职责/边界注释已含在代码块（照抄）；无生产代码改动，不加生产日志。
6. **提交**：`git add internal/agentd/b390_wake_stall_test.go && git commit -m "test(B390): 唤醒停摆红色回路转正——runAutomationPass 三条断言（现状红）"`。

**判据**：回路在本分支基线上以 `RED(a)+(b)` 失败（钉行为：重试次数与账本 needs_human 事件，而非计数型代理）。

### 接缝覆盖

- **测试 → 缝**：入口调用符号 `env.srv.runAutomationPass`，正是缝 1（`scheddrain.go:83`）。✓
- **缝 → 测试**：缝 1 由本测试三条断言锁住。✓

---

## Task 2：准入持续失败——按节拍重试 + 落 needs_human + 不静默（红→绿）

**为什么**：R1/R4。spec §4.2「准入持续失败时：按节拍重试 + 账本可见留痕（需要人），不允许静默」；spec §2.4 允许一两行小修顺手修掉。**注意 P1/P2 未拍板前，本 task 代码按「本分支独立可编译」的甲案撰写；若协调者选乙/丙，只替换本 task 第 2 步的失败分支实现，断言与测试不变。**

**文件集**：`internal/agentd/wakeconsumer.go`、`internal/agentd/scheddrain.go`、`internal/agentd/server.go`（新增字段与初始化）、`internal/agentd/b390_wake_stall_test.go`（T1 的测试在此转绿）。

### Interfaces

Consumes（既有，不改）：

- `coordinatorAdmissionError`（`scheddrain.go:26`）——准入失败的错误形状；`errors.As(err, *coordinatorAdmissionError)`。
- `scheduling.ErrNoSlot`（`scheduling.go:149`）。
- `Store.MarkNeedsHuman(cardID, reason, actor string) error`（`internal/ledger/events.go:384`）。
- `Server.automationMu sync.Mutex`、`Server.log *slog.Logger`、`Server.ledger *ledger.Store`。

Produces（新）：

- `type wakeRetryState struct { evs []keystone.WakeEvent; attempts int }`（`wakeconsumer.go` 文件内）。
- `func admissionStalled(err error) bool`。
- `func (s *Server) scheduleWakeRetry(card string, evs []keystone.WakeEvent)`。
- `func (s *Server) retryPendingWakes(ctx context.Context)`。
- `const automationStallEscalateAfter = 3`。
- `Server.automationRetry map[string]wakeRetryState`（新字段）。

### 步骤（红绿）

1. **判据先在基线跑**：T1 已给出基线红（`RED(a)`/`RED(b)`）。本 task 动手前复核 `go test ./internal/agentd/ -run TestB390WakeStallRedLoop -count=1` 仍红。
2. **最小实现**：

   a) `internal/agentd/server.go`——在 automation 字段组（`server.go:188-195`）加：

   ```go
   // automationRetry 是按卡保留的待重试唤醒：准入失败（可恢复排队态）时进入此表，
   // 由 retryPendingWakes 按节拍重试；成功即删。内存态：进程重启即清（跨重启自愈
   // 不在本卡，见 R3）。受 automationMu 保护。
   automationRetry map[string]wakeRetryState
   ```

   并在 `NewServer` 初始化 automationKick 处（`server.go:284`）加一行：

   ```go
   automationRetry:        make(map[string]wakeRetryState),
   ```

   b) `internal/agentd/wakeconsumer.go`——**先补一个 import**（现有 import 块 `wakeconsumer.go:8-22` 没有它；不加则 `scheduling.ErrNoSlot` 编不过）：

   ```go
   import (
       // ... 既有 ...
       "github.com/Xsxdot/handoff/internal/scheduling"
   )
   ```

   然后在文件内加常量与类型：

   ```go
   // automationStallEscalateAfter 是同卡连续准入失败的升级阈值：达到即落一次
   // needs_human，让人看见唤醒链停摆（spec §4.2「不允许静默」）。
   const automationStallEscalateAfter = 3

   // wakeRetryState 是一张卡的待重试唤醒：evs 是同批唤醒事件，attempts 是连续
   // 失败次数（用于升级阈值判定，也是日志节拍的读数）。
   type wakeRetryState struct {
       evs      []keystone.WakeEvent
       attempts int
   }

   // admissionStalled 只认「协调者准入满员」这一可恢复排队态；账本故障、承载缺失、
   // 席位非法等非准入错误不进入重试与升级（避免把真实故障说成满员）。
   func admissionStalled(err error) bool {
       var adm *coordinatorAdmissionError
       return errors.As(err, &adm) && errors.Is(err, scheduling.ErrNoSlot)
   }

   // scheduleWakeRetry 记录一次准入失败并留可见痕：每条失败都 Warn（节拍可见），
   // 连续失败达阈值时落一次 needs_human。needs_human 按「次数==阈值」恰一次落，
   // 之后重试不再重复落（避免每次重试刷屏）。
   func (s *Server) scheduleWakeRetry(card string, evs []keystone.WakeEvent) {
       s.automationMu.Lock()
       if s.automationRetry == nil {
           s.automationRetry = make(map[string]wakeRetryState)
       }
       st := s.automationRetry[card]
       st.evs = evs
       st.attempts++
       s.automationRetry[card] = st
       attempts := st.attempts
       s.automationMu.Unlock()

       s.log.Warn("自动化唤醒准入失败，进入重试节拍", "card", card, "attempts", attempts)
       if attempts != automationStallEscalateAfter {
           return
       }
       reason := fmt.Sprintf("自动化唤醒持续准入失败 %d 次：协调者小队名额被占满，唤醒链停摆，请人工处置（可检查名额键残留或等待释放）", attempts)
       if err := s.ledger.MarkNeedsHuman(card, reason, "agentd"); err != nil {
           s.log.Error("准入停摆落等人失败", "card", card, "cause", err)
           return
       }
       s.log.Error("准入停摆已落 needs_human", "card", card, "attempts", attempts)
   }

   // retryPendingWakes 在每个消费轮开始前重试待重试唤醒；成功或遇到非准入
   // 错误即移出重试表。ctx 取消即停（循环退出路径）。节拍 = 轮询节拍本身
   // （每轮一次），不设秒级退避：T1 回路背靠背跑 3 轮必须再试 3 次，「按节拍
   // 重试」的节拍就是 automationPollInterval。
   func (s *Server) retryPendingWakes(ctx context.Context) {
       s.automationMu.Lock()
       due := make([]string, 0, len(s.automationRetry))
       for card := range s.automationRetry {
           due = append(due, card)
       }
       s.automationMu.Unlock()
       sort.Strings(due)
       for _, card := range due {
           if err := ctx.Err(); err != nil {
               return
           }
           s.automationMu.Lock()
           st := s.automationRetry[card]
           s.automationMu.Unlock()
           _, wakeErr := s.wakeCoordinatorRound(ctx, card, st.evs)
           if wakeErr == nil {
               s.automationMu.Lock()
               delete(s.automationRetry, card)
               s.automationMu.Unlock()
               s.log.Info("待重试唤醒成功", "card", card)
               continue
           }
           if !admissionStalled(wakeErr) {
               s.automationMu.Lock()
               delete(s.automationRetry, card)
               s.automationMu.Unlock()
               s.log.Error("待重试唤醒遇非准入错误，停止重试", "card", card, "cause", wakeErr)
               continue
           }
           s.scheduleWakeRetry(card, st.evs)
       }
   }

   // clearWakeRetry 在唤醒成功后清掉该卡的重试态与连续失败计数。
   func (s *Server) clearWakeRetry(card string) {
       s.automationMu.Lock()
       delete(s.automationRetry, card)
       s.automationMu.Unlock()
   }
   ```

   c) `internal/agentd/wakeconsumer.go`——失败分支（`wakeconsumer.go:421-458`）里，把「吞事件」改成「入重试表后仍推进游标（防止同一条重复触发新的 launchRound，B274），由 retryPendingWakes 负责节拍重试」：

   ```go
   result, wakeErr := s.wakeCoordinatorRound(ctx, card, evs)
   if wakeErr != nil {
       s.log.Error("自动化事件批次唤醒失败", "card", card,
           "event_count", len(evs), "cause", wakeErr)
       // R1：准入满员是可恢复排队态——登记进重试表按节拍重试 + 升级可见；
       // 其它错误维持既有终局处置（标 seen、推进游标、不再试）。
       if admissionStalled(wakeErr) {
           s.scheduleWakeRetry(card, evs)
       } else if result.Escalated {
           // 既有自生 needs_human 标 seen 逻辑保持不变（B389 前的分支码）。
           generated, readErr := s.autoLedger.EventsFromAsc(nil, maxProcessed, 500)
           if readErr != nil {
               s.log.Warn("升级后读取自生等人事件失败", "card", card, "cause", readErr)
           } else {
               for _, generatedEvent := range generated {
                   if generatedEvent.Type != ledger.EvNeedsHuman || generatedEvent.CardID != card {
                       continue
                   }
                   s.automationMu.Lock()
                   s.automationSeen[generatedEvent.Seq] = struct{}{}
                   s.automationMu.Unlock()
                   if generatedEvent.Seq > maxProcessed {
                       maxProcessed = generatedEvent.Seq
                   }
               }
           }
       }
       for _, item := range batch {
           s.automationMu.Lock()
           s.automationSeen[item.seq] = struct{}{}
           s.automationMu.Unlock()
           s.log.Debug("自动化 pending 事件唤醒失败，推进 cursor", "seq", item.seq,
               "card", card, "type", item.typ, "cursor", from,
               "cursor_candidate", maxProcessed, "cause", wakeErr)
       }
       cursorErr := s.advanceAutomationCursor(maxProcessed)
       if cursorErr != nil {
           return processed, escalated || result.Escalated, errors.Join(wakeErr, cursorErr)
       }
       return processed, escalated || result.Escalated, wakeErr
   }
   s.clearWakeRetry(card)
   ```

   d) `internal/agentd/wakeconsumer.go`——在 `consumeAutomationEventsOnce` 里、读取 `events` 之前（既有 `from` / `automationSeen` 初始化之后）加：

   ```go
   // R1：先重试停摆的唤醒，再读新事件；重试成功即从表中移除。
   s.retryPendingWakes(ctx)
   ```

   e) `internal/agentd/scheddrain.go`——`runAutomationPass`（`scheddrain.go:83`）加成功节拍可见（不改全局日志级别，走 Info 会被 warn 吞；故用 Warn 只在「本轮有事发生」时打，正常空轮不打）：

   ```go
   func (s *Server) runAutomationPass(ctx context.Context) {
       processed, escalated, err := s.consumeAutomationEventsOnce(ctx)
       if err != nil {
           s.log.Error("自动化事件消费轮失败", "cause", err)
       } else if processed > 0 || escalated {
           s.log.Warn("自动化事件消费轮完成", "processed", processed, "escalated", escalated)
       }
       if _, err := s.drainQueuesOnce(ctx); err != nil {
           s.log.Error("自动化队列清队轮失败", "cause", err)
       }
   }
   ```

3. **跑绿**：`go test ./internal/agentd/ -run TestB390WakeStallRedLoop -count=1 -v` → `PASS`（三条断言全绿）；再跑 `go test ./internal/agentd/ -run 'TestB390|TestB3583|TestAutomation' -count=1` → `ok`（未误伤既有）。
4. **日志/注释步骤**：入口带输入（`card`/`attempts`）、外部调用（`MarkNeedsHuman`/`wakeCoordinatorRound`）前后有日志、错误分支带上下文、成功路径不静默——代码块已含；生产 logger 为 `s.log`（slog），无 `fmt.Print`。
5. **提交**：`git add internal/agentd/wakeconsumer.go internal/agentd/scheddrain.go internal/agentd/server.go internal/agentd/b390_wake_stall_test.go && git commit -m "fix(B390): 准入持续失败按节拍重试并落 needs_human，消费轮不再静默"`。

**判据**（钉行为）：同一条唤醒在准入恒满时，N 轮内 `LaunchAdmit` 被尝试 ≥ N 次；账本出现该卡 `needs_human`；非准入错误不进入重试表。

### 缺陷族对抗审查（本 task）

| 族 | 结论 |
|---|---|
| 生命周期/状态机中断 | 中途 agentd 重启：`automationRetry` 是内存态即丢（跨重启不自愈 = R3，已在 §3 回 spec）；不入队的重试不产生孤儿进程/目录。 |
| 静默失败/误导报错 | 准入满员与真实故障分流（`admissionStalled` 只认 `ErrNoSlot`）；非准入错误停止重试并 Error 留因；`MarkNeedsHuman` 失败再 Error。 |
| 跨平台假设 | 无，纯内存 map + time，不涉路径/进程组。 |
| 假红/假绿 | 回路断言 `admitCount` 与账本事件，非中途副产物；反面：非准入错误不得进重试（T2 可选补一条反例，见下）；`admitCount ≥ passes` 在只跑 1 次时必红，不会稳定假绿。 |
| 门禁绕过 | 未新增写路径；`MarkNeedsHuman` 走既有账本写；重试复用 `wakeCoordinatorRound` 的既有席位/准入校验，无旁路。 |

---

## Task 3：名额键残留观察面（CLI 可看，红→绿）

**为什么**：spec §4.3「名额键残留可经 CLI 观察（是否有租约 TTL、是否能列出占用者）」。本 task 只做**读面**（不清理，R3 的自愈回 spec）。

**文件集**：`internal/scheduling/registry_read.go`、`internal/agentd/scheduling_client.go`、`internal/proto/scheduling.go`、`internal/agentd/schedapi.go`、`cmd/squad.go`、`web/src/api/scheduling.ts`、及各自测试。

### Interfaces

Consumes（既有，不改）：`kindRunning = "sched_running"`（`scheduling.go:30`）；`Service.repo.List`；`RegistryEntry.Body`（`{count:N}`）；`proto.CarrierView`/`SquadView`/`SquadsResp`（`internal/proto/scheduling.go:18/38/47`）；`handleSquadsGet`（`schedapi.go:63`）；`cmd/squad.go` 的 `squadListCmd`。

Produces（新）：

- `func (s *Service) RunningCounts() (map[string]int, error)`——按 `sched_running` 行 id 返回 `{id: count}`。
- **`SchedulingClient` 接口增一行**（`internal/agentd/scheduling_client.go` 的「登记读」组内）：
  ```go
  // —— 运行计数读面（schedapi，B390 名额键残留观察）——
  RunningCounts() (map[string]int, error)
  ```
  **注意契约触碰**：`scheduling_client.go` 头注写「新增方法先回 contract 节点」（`scheduling_client.go:23`）。本方法是只读观察面、不加任务状态、不改 wire 语义之外的写路径，属 contract 已授权的 `schedapi` 读面延伸；**执行者动手前须把此接口增量记入本卡台账并在 plan 审核时请协调者确认**（若协调者判为契约面新增，则退回 contract 节点，T3 暂缓）。
- `proto.SquadsResp` **增一个显式占用清单字段**（不用 `CarrierView.Running int`：`int` 无法表达「一个载体分别被哪些小队占用、各占几个」，且多成员小队一个 int 有歧义）：
  ```go
  // RegistryRunningView 是一条 sched_running 占用行（key + 当前计数）。
  // key 形如 squad/<squad>/<carrier> 或 carrier/<carrier>（见 scheduling.OccupancyKeys）。
  type RegistryRunningView struct {
      Key   string `json:"key"`
      Count int    `json:"count"`
  }
  ```
  并在 `SquadsResp` 加 `Running []RegistryRunningView \`json:"running"\``（空库 = `[]`，与既有空数组纪律一致）。
- `web/src/api/scheduling.ts` 的 `SquadsResp` 镜像加 `running: { key: string; count: number }[]`。
- `cmd/squad.go` 的 `squadListCmd`：表格在载体/小队行之后追加「运行位」段（每行 `key  count`）；`--json` 自动带出（结构体字段）。

### 步骤（红绿）

1. **判据先在基线跑**：`go test ./internal/scheduling/ -run TestRunningCounts -count=1` → `undefined: ...RunningCounts`（基线红，先写测试）。wire 基线：`go test ./internal/agentd/ -run TestSquadsGetReportsRunning -count=1` 红（字段不存在）。
2. **写测试（断言逐条列全，复用既有 harness）**：
   - `internal/scheduling/registry_read_test.go`（照抄既有 `TestQueueSnapshotMatchesDrainOrder` 的内存 registry 夹具形态）：种 `sched_running|squad/pro/cmd`=`{"count":2}`、`sched_running|carrier/cmd`=`{"count":1}`，断言 `RunningCounts()` 返回恰 `{"squad/pro/cmd":2,"carrier/cmd":1}`；缺行不出现（缺失≠零，用 map 中不出现区分）。
   - `internal/agentd/schedapi_test.go`（照抄既有 `GET /api/squads` 用例的 `ledgerGet` 形态）：先 `PUT` 载体 `cmd`+小队 `pro`（成员 `cmd`），再直接向账本写 `sched_running|squad/pro/cmd`=`{"count":2}`、`sched_running|carrier/cmd`=`{"count":1}`，`GET /api/squads` 断言 `resp.Running` 含这两行且 count 精确、`carrier/cmd` 与 `squad/pro/cmd` 各自独立成行（同载体两个键都在，证明能列占用者而非一个 int）。
   - `cmd/squad_test.go`（照抄 `TestSquadListRendersTableAndJSON`）：断言表格含「运行位」段与键值、`--json` 输出含 `"running":[{...}]`。
3. **最小实现**：

   a) `internal/scheduling/registry_read.go`：

   ```go
   // RunningCounts 读出全部 sched_running 计数（id → count）。只读：不清零、
   // 不判断归属——名额键残留的 TTL/自愈是架构级议题（B390 台账 R3），本读面
   // 只让操作者看得见「谁占着、多少」。
   func (s *Service) RunningCounts() (map[string]int, error) {
       recs, err := s.repo.List(kindRunning)
       if err != nil {
           return nil, err
       }
       out := make(map[string]int, len(recs))
       for _, rec := range recs {
           var body struct {
               Count int `json:"count"`
           }
           if err := json.Unmarshal(rec.Body, &body); err != nil {
               return nil, fmt.Errorf("运行计数 %s 解码失败: %w", rec.ID, err)
           }
           out[rec.ID] = body.Count
       }
       return out, nil
   }
   ```

   b) `internal/proto/scheduling.go`：加 `RegistryRunningView` 类型（定义见 Interfaces），并在 `SquadsResp` 加 `Running []RegistryRunningView \`json:"running"\``。

   c) `internal/agentd/schedapi.go` `handleSquadsGet`——在原有 carrier/squad 组装后统一投影（**排序**保证输出稳定，排序键用 `sort.Strings`）：

   ```go
   counts, err := s.scheduling.RunningCounts()
   if err != nil {
       s.log.Error("读运行计数失败", "cause", err)
       writeErr(w, http.StatusInternalServerError, err)
       return
   }
   keys := make([]string, 0, len(counts))
   for key := range counts {
       keys = append(keys, key)
   }
   sort.Strings(keys)
   resp.Running = make([]proto.RegistryRunningView, 0, len(keys))
   for _, key := range keys {
       resp.Running = append(resp.Running, proto.RegistryRunningView{Key: key, Count: counts[key]})
   }
   ```

   d) `web/src/api/scheduling.ts`：`SquadsResp` 加 `running: { key: string; count: number }[]`。

   e) `cmd/squad.go` `squadListCmd`：表格在两组行之后追加「运行位」段（`fmt.Fprintf(w, "运行位\t%s\t%d\n", row.Key, row.Count)`，表头对应）；`--json` 无需改（结构体带出）。
   > `cmd/squad.go` 的 `--json` 分支现在只 enc `resp.Carriers` 与 `resp.Squads`；若要 `--json` 也带 running，需按既有「一行一对象」风格追加 `for _, r := range resp.Running { enc.Encode(r) }`——执行者按既有 JSON 形态落地，或改为整包 enc `resp`（二选一必须在测试里体现，别静默省略）。
   > **import 提示**：`internal/agentd/schedapi.go` 现有 import 块（`schedapi.go:23-34`）没有 `sort`，c) 用到需补 `"sort"`；`cmd/squad.go` 若用于排序也同理。

4. **跑绿**：三个包各自绿；`go build ./...` 绿；`web/` 下 `npm run typecheck` 绿（若改 TS）。
5. **日志/注释步骤**：`RunningCounts` 导出带参数/返回/边界注释（代码块已含）；`handleSquadsGet` 读计数失败 Error 带 cause；成功路径既有 `已读取编制登记面` Info 已含 carriers/squads，可加 `running_keys` 计数。
6. **提交**：`git add internal/scheduling/registry_read.go internal/agentd/schedapi.go internal/proto/scheduling.go cmd/squad.go web/src/api/scheduling.ts <测试> && git commit -m "feat(B390): 名额键残留观察面——GET /api/squads 与 squad list 暴露运行计数"`。

**判据**：`RunningCounts` 返回与账本键逐一对应；HTTP/CLI 读面把 `squad/pro/cmd`、`carrier/cmd` 等占用键显示为可读「运行位」；缺失键不出现（区分缺失与零）。

### 序列化边界设问（本 task）

新增字段 `running` 从 scheduling map → `CarrierRow/SquadRow` → `carrierView/squadView` → `proto.CarrierView/SquadView`（JSON）→ `client.Squads` → CLI 表格/`--json` → `web/src/api/scheduling.ts` 类型。**每一处投影都列进文件清单**；回归测试穿过真实 JSON 边界（`GET /api/squads` 的 `ledgerGet` 解到 `proto.SquadsResp` 断言 `Running`），用 `int` 的「缺省 0」在测试里与「有值」用不同夹具区分（种 1/2，断言非 0 行精确，另断言未种行的 0）。无 roundtrip 属性适用（单向投影，非对称编解码）。

---

## 6. 回 spec / 另立卡（不在本 DAG 的实施任务）

以下三项**只出结论与证据，不写实现**，按 spec §2.4 回本 spec 重新定级或另立卡：

1. **R1 重试语义的架构级半边**：真正「不吞事件 + 每次失败可重试」需改游标/认领水位语义（B389 `advanceAutomationCursorWatermark` 的「在飞认领挡推进」是正确载体，但需决定失败时**不 `completeWakeBatch`** 以保留重试——本计划 T2 的甲案是内存重试的等价最小形态）。回 spec 定级。
2. **R3 名额键 TTL/owner/自愈**：给 `sched_running` 加 owner+expires、启动对账从「只读保留」改为「按租约回收本机可用位」——改 schema 与对账语义，回 spec 定级。**严禁在排查现场清键**（用户红线）。
3. **R2 `@卡号` 前缀**：寻址语义归 d_orchestration/契约 §3.8；倾向另立卡（P3）。

---

## 7. 五项检查自审

1. **缺陷族对抗审查**：见 T2 表；T3 的族结论——生命周期（只读，无中断风险）、静默失败（读失败 Error 上浮）、跨平台（无）、假红假绿（断言账本键与 JSON 字段值，非计数代理）、门禁（只读端点，无写路径）。R3 的自愈缺失是**已识别未修**，已在 §3/§6 明示，不冒充已修。
2. **序列化边界设问**：见 T3 节。新增字段 `running` 的全部手写投影已列，回归测试穿 `GET /api/squads` 真实 JSON 边界。
3. **上下文预算检查**：T0 两文件；T1 一文件；T2 三文件；T3 六文件 + 测试——每个 task 圈得出有界文件集。✓
4. **类型标注**：本卡是 `internal/agentd` 生命周期/可见性，非边界型新面；真机清单见 §8（观察面 CLI 需真机核）。
5. **接缝覆盖（双向）**：
   - **测试 → 缝**：T1 入口 `runAutomationPass`（缝1）✓；T3 入口 `GET /api/squads` + `squad list`（缝2）✓。
   - **缝 → 测试**：缝1 由 `TestB390WakeStallRedLoop` 锁；缝2 由 `TestRunningCounts` + `TestSquadsGetReportsRunning` + `TestSquadListRendersTableAndJSON` 锁。✓
   - **内部锁**：仅 T0 一条（编译前置），理由已按唯一形状声明（§Task 0）。其余无内部锁，无未声明退路。

---

## 8. 真机清单（归协调者执行，不派发）

本计划全部任务在仓内单测可闭环，无需驱动派发系统自身。以下项需真机核（**由协调者执行**）：

1. 在**不清理**共享账本键的前提下，跑 `handoff squad list --json`（T3 落地后）确认 `squad/leader/cmd`、`carrier/cmd` 等占用键以「运行位」可见——本机/远程账本各一次（该键在本机 sqlite 与远程 PG 都存在，见台账）。
2. 造一次真实准入满员（占满 `carrier/cmd` 物理位）后注入一条 `--mention <裸卡号>` 唤醒，确认：日志出现 `自动化唤醒准入失败，进入重试节拍`（Warn，默认级别可见）且连续 3 次后账本落 `needs_human`。
3. **`@卡号` 前缀**：用 `--mention @<卡号>` 复现不唤醒（P3 另立卡前不改）；裸卡号应唤醒——这是卡上 `seq=16861` 验收链的复现条件。
4. R3 的「换版重启后依旧静默」：需在协调者点头清理残留后核自愈（本计划不做）。

---

## 9. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N」：每个 task 给了精确路径、完整代码块、Interfaces、步骤与判据。
- **自我声明的例外/内部锁**：① T0 的编译前置按内部锁声明（理由见 §Task 0）；② T3 的 `CarrierRow/SquadRow.Running` 填充位置标注「二选一（建议 `handleSquadsGet` 单点），拍板时定」——这是可判定的实现选择，非占位符；③ T2 的失败分支实现按 P2 甲案给出完整代码，乙/丙案明确「只替换失败分支实现，断言与测试不变」——退路改变了实现但不改变任何测试入口符号，故不构成未声明内部锁。
- 跨仓判据：无（纯本仓 Go/TS）。

## 10. 红线自检

- 未写实现代码（本文件是计划；红色回路/诊断件均为临时件，跑完已删，台账留原文）。
- 未碰 handoff CLI、未派发、未起 executor。
- **未清理任何共享账本键**（R3 只读观察，清理属运营动作，等用户点头）。
