# B351 spec 审查

审查对象：`docs/superpowers/specs/b351.md`（状态：待用户批准；自称 **L3 轻档**）  
对照台账：`docs/superpowers/ledgers/2026-09-09-b351-spec-ledger.md`（只作线索，证据以本审查复跑/读码为准）  
冻结对照：`docs/superpowers/specs/b233.6.md` / `b233.6-contract.md` / `b233.6-breakdown.md` / `docs/superpowers/plans/b233.6-plan.md` §6.2 项 1；B39 `internal/agentd/manager.go` `executorStarted` / `deleteCreatedBranch`；B77 `internal/agentd/reclaim.go` 包头；B233.4 `Manager.Stop`；B349 / B353；`docs/roadmap.md`「来自 B233.6 真机验收」远端孤儿段  
对照代码：工作树 `/Users/xushixin/workspace/handoff` 分支 `cards/B233.1-charter-7` @ `6097892bd14c5a16c60e2b2abead100da2887d65`（与 spec 自称基线同戳）  
审查者：独立 spec 审查人（charter 流；与作者无会话史）  
日期：2026-09-09

行号按当前工作树，会漂。本审查未改任何文件除本报告。图覆盖债见 §6（无 shell，未跑 `codegraph` CLI；以 `codegraph/baseline.json` grep + 源码为准）。

## 1. 总判

**修订后再批。**

方向对：用户选 B（永不删分支、耗费轮次改名、Stop+强制收树）、守 B77 / B39 `executorStarted` / B233.4 留树、不新造事件、不扩 `WaitDeliveryPolicy`、不全机扫未挂账、生产两处装配 Dispatcher、`--step` 只 POST 本机 agentd——与活代码和冻结物同向。不能批的原因是一处承重计数语义接缝没锁死：`PurposeRounds = 挂账行 + 耗费` 的「加法」若被读成 `max` /「成功挂账后耗费作废」，接缝 2 现有正例仍绿，但「失败→成功→再派」第三轮会撞上第二轮的 `-2` 分支。最小补丁见文末。

## 2. Findings

### Critical

#### C1. `PurposeRounds`「挂账 + 耗费」加法未钉死；接缝只锁「一次耗费后立刻再派 = -2」，`max`/替代读法会在第三轮撞名

- **位置**：方案 `b351.md:66-68,86`、契约增量 `b351.md:108`、接缝 2 `b351.md:121`；活代码 `internal/ledger/events.go#PurposeRounds:610-621`（今日只数 `TasksOf`）；分支公式 `internal/ledgerstep/dispatch.go:248-256`（`rounds>0` → `rounds+1`）
- **事实**：
  1. 正文写「挂账行 + 已耗费未挂账轮次」「只增不减」，自然读法是**加法**：1 次耗费 + 1 次成功挂账 ⇒ `PurposeRounds==2` ⇒ 第三次 ViaTemplate 分支为 `…-3`。
  2. 「已耗费**未挂账**轮次」也可被零上下文读成：耗费只在「还没有对应挂账」时计入，或 `PurposeRounds = max(挂账数, 耗费数)`；成功挂账后耗费被替代/作废 ⇒ 失败后再成功一次后计数仍为 1 ⇒ 第三轮仍取 `-2`。
  3. 接缝 2 只断言「一次耗费之后，同 purpose **第二次** Transport 的 Branch 为 `-2` / 审阅 `review-(n+1)`」，**没有**「耗费 1 + 成功挂账 1 之后第三次为 `-3`」。`max`/替代实现能过现写接缝，却在真机第三轮与仍留在目标机上的第二轮分支撞 `already exists`——正是本卡要消的症状。
- **为什么承重**：故事 1 的可观察结果是「再派不再撞名」。两个实现者：
  - A：加法，第三轮 `-3`，残留 `-2` 不挡。
  - B：`max`/替代，第三轮 `-2`，与成功第二轮分支撞名；卡标题「自动回收 + 重试改名」在第二轮成功后再度失守。
  用户选 B 的「耗费轮次只增不减」若在接缝落空，contract/implement 会把错误计数冻进去。
- **建议**：① 方案/契约段写死：`PurposeRounds(card, purpose) = count(TasksOf 中该 purpose) + count(该卡该 purpose 的耗费记录)`，禁止 `max`、禁止成功挂账后删改耗费、禁止「有挂账就忽略耗费」。② 接缝 2 加正例：一次耗费 + 一次成功挂账后，`PurposeRounds==2`（审阅同理），第三次 Transport 的 `Branch` 为 `…-3` / `…-review-3`（在已有 0 成功基线上）。③ 保留「只有耗费、无成功快照时 WorkBranch 仍 not found」「`CountRounds` 不增」。

### Important

#### I1. 耗费落盘自己失败时，返回值与是否仍补偿未钉

- **位置**：方案 `b351.md:65-70,80`、接缝 1 `b351.md:120`；活代码失败出口 `dispatch.go:358-363`（写闸）、`:377-382`（落账）
- **事实**：正文要求耗费在回滚事务**之外**先记，补偿失败不顶替落账原错；接缝 1 锁了「钩子返回错误 → 仍记耗费，返回仍是落账原错」。但**耗费写入自己失败**（磁盘/PG/约束）时：是否仍 best-effort 补偿、返回值是落账原错还是耗费错、是否算进 OOS 崩溃窗口——正文未写。OOS 只覆盖「耗费落盘前进程死去」。
- **为什么承重**：实现者 A 用耗费错替换落账原错 → CLI/haltForHuman 文案变成挂号失败，掩盖真因；B 因耗费失败跳过 Stop → 孤儿继续跑且重试仍可能撞首轮名。可观察错误串与是否停 task 会分叉。
- **建议**：写死三句：耗费写入失败只进日志；**不**替换落账/写闸原错；补偿仍 best-effort；耗费失败与崩溃窗口同类，不在本卡保证「重试必改名」，但不得因此少调钩子。

#### I2. 「不新造事件类型」未显式禁止复用会叫醒的既有类型当耗费载体

- **位置**：方案 `b351.md:72`、OOS `b351.md:129`、故事 1 `b351.md:100`；活代码 `cmd/card_wait.go#cardWaitEventActionable:241-244`（`EvNeedsHuman` 可动作）；`wakeconsumer.go#automationWakeEvent:190-195`（同样叫醒）；裸模板失败出口 `cmd/card_dispatch.go:354-356`（今日只 `return err`，不写 `needs_human`）
- **事实**：正文禁新类型，理由是避免撞 B353、叫醒 `card wait --follow`。落点「表/列/函数名」归 contract。未写：不得用 `EvNeedsHuman` / `EvDispatched` / 其它可动作既有类型作为耗费计数载体。裸 `card dispatch`（无 `--step`）今日失败不落 `needs_human`；若 contract/实现把耗费写成 `EvNeedsHuman`，follow/自动化会被多叫醒一次，故事 1 未授权该增量。
- **为什么承重**：L3 下一节点是 contract。字面「不新造」挡不住「复用 NeedsHuman」。与「人读审计走现网派发失败出口」的意图一致，但载体约束要写到可观察面上。
- **建议**：契约语义加一句：耗费是 PurposeRounds 可读的**非叫醒**事实；禁止写入 `EvNeedsHuman` / `EvDispatched` / `EvTaskMirrored` / 任何使 `cardWaitEventActionable` 或 `automationWakeEvent` 为真的事件；允许的落点形态（侧表/列等）仍归 contract，但必须满足本句。

#### I3. 接缝 3 未锁本机空 target；Stop 非 2xx（含 409）后仍 Reclaim 只在方案段

- **位置**：故事 3 `b351.md:102`、方案 `b351.md:75-82`、接缝 3 `b351.md:122`；活代码 `Manager.Reclaim:265-266`（非终态拒绝）；`Manager.Stop:1840-1842` + `server.go:1728`（已终态 → 409）；`Client.Stop:1118-1130`；空 target 传输 `cardstep.go#stepTransport:315-326`、`cmd/card_dispatch.go#targetClient:194-203`
- **事实**：Reclaim **硬要求**终态；Stop 成功把任务迁 `failed` 后才能收树；已终态 Stop 返 409，正文正确要求「仍尝试 reclaim」。接缝 3 只写「先 Stop 再 `Reclaim(force=true)`」「两处装配非 nil」，没有：① Stop 返回 409（已终态）仍调用 Reclaim 且 force=true；② Stop/Reclaim 404 视为无对象、不顶替落账错；③ `target==""`（本机）与 Transport 同一客户端解析，Stop/Reclaim 真打到本机。故事 3 有空 target，接缝没有正例。
- **为什么承重**：若生产钩子写成「仅 Stop 成功才 Reclaim」，并发终态窗口会留下 B233.4 留存的 managed 树，故事 1「强制收回」假绿。若空 target 钩子误走远端池/拒绝，本机孤儿只记耗费不停 task。
- **建议**：接缝 3 补：Stop 成功或 409 之后必有一次 `Reclaim(force=true)`；404 软成功；返回值仍是落账/写闸原错。另加空 target 正例（fake HTTP 本机客户端被打到 Stop+Reclaim）。

### Minor

#### M1. 「新事件类型会把 card wait --follow 叫醒」与活代码不完全相符；禁令仍正确

- **位置**：`b351.md:72`；活代码 `card_wait.go:302-304`（`default: return false`）；`wakeconsumer.go:196-197`（未知卡类型不唤醒）
- **事实**：B353 假集合是**任务**事件上的 `WaitDeliveryPolicy`。卡侧未知 `card_events.type` 今日默认不当作可动作，**不会** Encode。新类型仍应禁止（契约膨胀、未来白名单漂移、与「非叫醒耗费」冲突），但「会叫醒 follow」不是当前活代码的必然结果。真正会叫醒的是复用 `EvNeedsHuman`（见 I2）。
- **建议**：改成「不新造事件类型；也不把耗费写成可动作既有类型」。不挡。

#### M2. 现状表行号与符号基本可核对；`dispatch.go:134-135` 的「真机项」注释仍在

- **位置**：现状 `b351.md:42-55`；活代码 `dispatch.go:134-135,355-357` 仍写远端回收属真机项
- **事实**：行号与行为均成立（WriteGate、PurposeRounds、B39、Stop 留树、reclaim 包头、两处装配、`--step` POST）。本卡落地后这些注释应变，属实现债，不是 spec 错。
- **建议**：实现时改注释；spec 可将「本卡接手机内可测」写进实现决定。不挡。

#### M3. 补偿钩子符号名与共享 helper 落点未命名

- **位置**：`b351.md:108,122`
- **事实**：L3 轻档把符号名留给 contract 可接受；接缝已要求两处装配非 nil 且穿过 `Client.Stop`/`Reclaim`。风险是两处各写一份顺序漂移（I3 补强后可测出）。
- **建议**：contract 给钩子字段与生产 helper 各一个稳定名；鼓励 CLI/agentd 共用。不挡批准。

## 3. 定级意见

独立套定级两问，**同意 L3 轻档**。不要降 L2，不要抬重档。

1. **跨几个子系统的契约面？** 账本挂号（`PurposeRounds` 语义扩大）、编排失败出口（`ViaTemplate`）、执行面既有 Stop/Reclaim（经注入钩子）。顶层 ≥2，同一条「Transport 已受理、账本未挂账」规则跨面收口。
2. **动不动契约层？** 动。`PurposeRounds` 对外语义从「只数挂账行」变为「挂账 + 耗费」；Dispatcher 增加与 Transport 同档钩子（进程内契约）。不新 HTTP、不新事件类型——仍是契约语义增量，不是纯实现修补。

轻档：一条失败缝；把「计数」和「停 task」拆成并行子卡会拆散验收。契约冻结与拆解照做、实现一轮——同意。不是 L2：语义扩展跨账本与编排。不是 L1：不是三行复述。

## 4. 接缝

| 缝 | 符号 + 调用方 | 判定 |
|---|---|---|
| 1 `Dispatcher.ViaTemplate` × 补偿钩子 | 生产调用方：`cmd/card_dispatch.go` 裸模板；`internal/agentd/cardstep.go#startCardStep` → `StepRunner` → `dispatchNodeWithGate` → `ViaTemplate`（`runner.go:334-348`）。钩子尚不存在，接缝定义的是拟增字段与失败出口。 | **真缝意图。** 禁止只测 helper。覆盖 Transport 失败 0 次、落账/写闸各 1 次、钩子错不顶替原错——够。**漏耗费写入失败（I1）。** |
| 2 `Store.PurposeRounds` / `ReviewRounds` × `ViaTemplate` 挂号 | `PurposeRounds` ← `ViaTemplate:248`；`ReviewRounds` ← `:238`（委托 PurposeRounds）。`CountRounds` 在 `ledgerstep/rounds.go`，只读 `EvReviewVerdict`。`WorkBranch` 只扫 `EvDispatched`（`events.go:536-556`）。 | **真缝。** 首轮无后缀回归、CountRounds/WorkBranch 反面——对。**漏加法语义下「耗费+成功」后第三轮（C1）。** |
| 3 生产装配 × 钩子非 nil + 真 Client.Stop/Reclaim | 生产 Dispatcher 字面量仅两处（全仓 grep `&ledgerstep.Dispatcher{` → `card_dispatch.go:332`、`cardstep.go:121`）。`--step` CLI 走 `runStepDispatch` → `Client.CardStep`（`card_node.go:175-182`），不装配 Dispatcher——正文正确。 | **真缝。** 要求 fake HTTP 穿过 Client 而非只 spy 函数指针——防假绿。**漏空 target、漏 Stop 409 仍 Reclaim（I3）。** |

假缝检查：没有把无生产调用方的纯计数 helper 单列为缝；并明文禁止只测该 helper。合格。

边界型：Stop/Reclaim 不改请求/响应形状；补偿是派发失败路径副作用——与「不改执行面契约形状、只注入调用」一致。

## 5. 缺陷族

- **生命周期 / 状态机中断**：正文对准主缝。Transport 成功 ⇒ `executorStarted==true`（`manager.go:1173`），B39 不删树删分支——成立。Stop 留树（`worktreeRemoved` 恒 false）⇒ 必须再 Reclaim——成立。Reclaim 要求终态 ⇒ 必须先 Stop（或已终态 409）——成立，但接缝未锁 409 路径（I3）。崩溃窗口、补偿网络失败后仍 running —— OOS 已钉。耗费落盘失败与崩溃窗口同类，需 I1 补一句以免实现少调钩子。
- **静默失败 / 误导报错**：补偿失败只日志、返回落账原错——正面有答案。风险是耗费写入失败顶替原错（I1），以及用 `EvNeedsHuman` 当载体造成额外叫醒（I2）。裸 `handoff dispatch` 不经 Dispatcher，不会被本卡钩子误杀——与否决全机扫描一致。
- **跨平台假设**：经既有 `Client` 打目标机；空 target = 本机，与 Transport 同解析。不新依赖 shell/HOME。接缝补空 target 后闭合。
- **假红 / 假绿测试**：禁止只测计数纯函数——对。假绿温床：接缝 2 不锁第三轮加法（C1）；接缝 3 不锁 409/空 target（I3）；只 spy 钩子指针不打 HTTP——正文已禁。
- **门禁绕过**：**无，因为** 不新写入口、不改 B39 守卫去删已接管树、不把删分支塞进 reclaim、不改 `mirrorSkip` / 假集合。
- **序列化边界**：不新事件、不新 HTTP DTO。耗费若误走事件流会进 Follow——I2。金样本/proto 无增量预期。
- **枚举新值过既有白名单**：正文禁新事件类型，与 B353 互锁。**活代码上新卡类型今日不叫醒（M1）**；真正的白名单风险是复用 `EvNeedsHuman`（I2）。
- **承重安全属性**：不删分支（B77）；不写假 `EvDispatched`；重试改名；Stop+force 收树；CountRounds 不被耗费推动。C1 威胁的是「改名」在第三轮失守。

## 6. 图覆盖债

独立核对（无 `codegraph` CLI，grep `codegraph/baseline.json` + 源码）：

| 查询 | 结果 |
|---|---|
| `PurposeRounds` | baseline 含 `n_ledger_Store_PurposeRounds`；活代码 `events.go:610`，与 spec 备注一致（moved）。 |
| `ViaTemplate` | baseline 含 `n_ledgerstep_Dispatcher_ViaTemplate`；活代码 `dispatch.go:139`。 |
| `Client.Stop` | baseline 含 `n_client_Client_Stop`（`d_transport_channel`）。 |
| `Client.Reclaim` | baseline 含 `n_client_Client_Reclaim`；spec 备注未点名，接缝 3 需要它。 |
| 补偿钩子 | **尚不存在**（与 spec 备注一致）。 |
| 生产 `Dispatcher{` 装配 | grep 仅两处，与 spec 一致。 |
| `who-calls` | 未跑；未作行为证据。 |

现状以源码为准。未把图行号写成代码事实。

## 7. 与已冻契约 / OOS / 架构法

- **B233.6 plan §6.2 项 1**：远端孤儿回收原标真机；本卡接手为机内可测（钩子 + fake HTTP），跨机真机仍 OOS——**与 roadmap「远端孤儿 task 无自动回收」同向收口，无冲突。**
- **B39**：不放宽 `executorStarted`；不走 `deleteCreatedBranch` 删已接管分支——正文弃选与方案一致。
- **B77 / B233.4**：reclaim 不删分支；Stop 留树——本卡用 Stop+force Reclaim 组合，不改二者语义。
- **B349**：身份闸只认 `EvDispatched`；本卡不写该事件——耗费不可变「当前派发」。成立。
- **B353**：不改假集合、不扩 `mirrorSkip`、不新造事件——成立；I2/M1 补「复用可动作类型」与「新类型今日不叫醒」的精度。
- **OOS**：崩溃窗口、补偿失败后仍 running、全机扫描、删分支、pending、新 wait/HTTP/事件、改 B39/Stop 删树——均钉死且未混进本期故事。裸 `handoff dispatch` 误杀风险已否决。合格。
- **用户裁决 B**：永不删分支、耗费改名、Stop+强制收树；C 不做——正文一致。
- **架构法**：耗费权威在账本；Stop/Reclaim 在执行面；编排只在 ViaTemplate 串联且不 import client/agentd——依赖方向正确。不穿透改 reclaim 删分支。第十条：展开到失败出口 + 既有 Client 一层，未写 agentd 内部 git 细节为落点。

## 8. 现状表核对（亲手对源码）

| spec 声称 | 活代码 | 结论 |
|---|---|---|
| Transport 成功后落账失败回消快照，不重派 | `dispatch.go:134-135,355-382`；`TestB2336DispatchFailureRollsBackSnapshot` | 成立 |
| 写闸在 Transport 后、落账前 | `TemplateDispatch.WriteGate` 注释 `:104-106`；检查 `:358-363` | 成立 |
| 远端孤儿仍标真机项 | 同文件 `:135,:357`；plan §6.2 项 1；roadmap `:726-731` | 成立 |
| `PurposeRounds` 只数 `TasksOf` | `events.go:610-621` | 成立 |
| 非审阅首轮无后缀，`rounds>0` 才 `-N+1` | `dispatch.go:244-256`；`TestViaTemplateSecondRoundGetsNumberedBranch` | 成立 |
| 审阅 `review-(ReviewRounds+1)` | `dispatch.go:229-242` | 成立 |
| B39 仅 `executorStarted==false` | `manager.go:1039-1043`；`deleteCreatedBranch:1438+` | 成立 |
| 建分支在 CreateTask 前 | `manager.go:1015-1026` | 成立 |
| Stop 留树，`worktreeRemoved` 成功路径恒 false | `manager.go:1820-1821,1893` | 成立 |
| reclaim 不删分支、脏树无 force 拒 | `reclaim.go:8-10,265-308` | 成立 |
| 生产装配 Dispatcher 两处 | `card_dispatch.go:332-345`；`cardstep.go:121-127` | 成立 |
| `--step` CLI 不装配 Dispatcher | `card_node.go:175-182` | 成立 |
| 节点派发失败 → haltForHuman | `node.go:269`；`runner.go:349-352` 上抛 | 成立 |
| `Client.Stop` / `Client.Reclaim` 已有 | `client.go:1119+`、`:600+` | 成立 |

未发现「写成仿佛已有的 flag/函数」类问题。

## 9. 二解测试（承重题）

| 陈述 | 读法 A | 读法 B | 承重？ | 正文是否钉死 |
|---|---|---|---|---|
| PurposeRounds = 挂账 + 耗费 | 加法，耗费永久累计 | `max` / 成功后耗费作废 | **会**（C1） | 「+」「只增不减」偏 A；接缝未锁第三轮 |
| 耗费落盘失败 | 仍补偿，返回落账原错 | 返回耗费错或跳过补偿 | **会**（I1） | 未钉 |
| 耗费载体 | 非事件侧表/列 | `EvNeedsHuman` 等既有可动作类型 | **会**（I2） | 只禁新类型 |
| Stop 409 后 | 仍 Reclaim(force) | 仅 Stop 成功才 Reclaim | **会**（I3） | 方案有；接缝无 |
| 本机空 target | 与 Transport 同客户端补偿 | 只对非空 `--target` 补偿 | **会**（I3） | 故事 3 有；接缝无 |
| 新事件类型 vs follow | 禁止（契约） | 今日 default 不 Encode | 否（禁令同向） | 禁令钉了；叫醒理由略过（M1） |
| Transport 失败 | 不记耗费、不补偿 | 也记 | 否 | 已钉 |
| 删分支 | 永不 | 复用 A/B39 | 否 | 用户 B + OOS 已钉 |
| CountRounds | 不因耗费增加 | 跟着 PurposeRounds 走 | 否 | 接缝 2 已锁 |

## 10. 批准前最小补丁（只改 spec 正文，不是代码）

1. **C1**：写死 `PurposeRounds`/`ReviewRounds` 为挂账计数与耗费计数的**和**；禁止 `max`/替代/成功后删耗费；接缝 2 增加「1 耗费 + 1 成功挂账 ⇒ 计数 2 ⇒ 第三轮分支 `-3` / `review-3`」。
2. **I1**：耗费写入失败只日志、不顶替落账/写闸原错、仍调用补偿钩子。
3. **I2**：耗费不得写成任何使 card wait / wakeconsumer 叫醒的事件（含复用 `EvNeedsHuman`）；非叫醒载体，具体表列归 contract。
4. **I3**：接缝 3 锁 Stop 成功或 409 后必 `Reclaim(force=true)`、404 软成功、空 target 正例。

I1–I3 与 C1 一并写入后可以再送批。M1–M3 不挡。方向保持：ViaTemplate 一处串联耗费挂号 + Stop/force Reclaim；不删分支；不新事件/HTTP；不全机扫描；B39/B77/B233.4/B349/B353 冻结不动。
