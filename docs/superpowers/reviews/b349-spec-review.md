# B349 spec 审查

审查对象：`docs/superpowers/specs/b349.md`（状态：待用户批准；自称 **L3 轻档**）  
对照台账：`docs/superpowers/ledgers/2026-09-09-b349-spec-ledger.md`（只作线索，证据以本审查复跑/读码为准）  
冻结对照：`docs/superpowers/specs/b233.6-contract.md` §2.4 / §3；`docs/superpowers/specs/b233.6-breakdown.md` P3；`docs/superpowers/plans/b233.6-plan.md` §5.2 约 L493–503；`docs/superpowers/specs/b353.md`（已批准）；`docs/roadmap.md`「来自 B233.6 真机验收」「来自 B233.7 验收」  
对照代码：工作树 `/Users/xushixin/workspace/handoff` 分支 `cards/B233.1-charter-7` @ `f2129a251c8fe37cfd4ee3ca25684936b5b426cd`（与 spec 自称基线同戳；工作树另有未跟踪 spec/台账，本审查不改）  
审查者：独立 spec 审查人（charter 流；与作者无会话史）  
日期：2026-09-09

行号按当前工作树，会漂。`codegraph resolve --doc docs/superpowers/specs/b349.md` 六个 `file#Symbol` 锚均为 `ok`/`moved`，无坏锚。图覆盖债见 §6。

## 1. 总判

**修订后再批。**

方向对：B233.6 §2.4 已经冻过「消费镜像必须核 node + attempt + source identity」，实现只核 envelope；B233.6 plan S3 把 source 推给 store 去重、禁止扩 proto，正是本卡要补的缺口。闸留在消费点、ledger 保持薄、水位进这台 agentd 的 DataDir、不改 `WaitDeliveryPolicy` 假集合——与 P3 / B353 同向。不能批的原因是一处按「接缝 + 现有 card wait 形状」落地会做错题：card wait 今天只看单条 `ledger.Event`，正文虽定义了「当前派发身份」却没把取数路径钉死；现网 B353 夹具又没有 `EvDispatched`。零上下文会在「只比列与 envelope 自洽」和「无快照就不叫醒」之间分叉，故事 2 或 B353 回归必有一红。最小补丁见文末。

## 2. Findings

### Critical

#### C1. card wait 的「当前派发」取数路径未钉死；按接缝只改 `cardWaitEventActionable` 会让旧 attempt 继续打 stdout，按闸严格执行又会让无 `EvDispatched` 的 B353 夹具假红

- **位置**：方案 `b349.md:63-72`、接缝 3 `b349.md:119`、现状 `b349.md:48`、实现决定 `b349.md:112`；活代码 `cmd/card_wait.go:144-229`、`cmd/card_wait_test.go:156-170`；wake 对照 `internal/agentd/wakeconsumer.go:51-129,254-270`
- **事实**：
  1. 正文把当前派发身份定义为「该卡该节点最新一条带 Node/Attempt 的 `DispatchSnapshot` 的 `(Target, TaskID)`」，`task_mirrored` 要通过闸必须 envelope.attempt **与** `source_task` 都等于这个当前 TaskID。旧 attempt 的镜像列与 envelope **自洽**（`source_task` == envelope.attempt == 旧 task），这正是迟到事件的形状，所以闸的信息必须来自 **另一条** `EvDispatched`，不能来自本条镜像自己。
  2. `cardWaitEventActionable` 今天只收一条 `ledger.Event`，对 `task_mirrored` 只解 `task_type` 再套 `WaitDeliveryPolicy`（`card_wait.go:216-229`）。`runCardWait` 除终态检查外不扫快照（`card_wait.go:124-165`）。接缝 3 的符号仍是这对组合，没写「必须读该事件 CardID 的 `EvDispatched`」。
  3. 活 B353 夹具 `card_wait_test.go:156-170` 写入 `Node:"node" / Attempt:"attempt"` 的 `delivery_failed` 镜像，**没有** `RecordDispatch`。按本卡闸，`currentWorkflowAttempt` 同类逻辑会 `!found` → 不 Encode。接缝 3 同时要求「当前派发的可动作 `task_mirrored` Encode」和「B353 类型表回归仍成立」，两句在这份夹具上互斥。
- **为什么承重**：故事 2 的可观察结果是「旧 attempt 不打 stdout、当前 attempt 打」。两个零上下文实现者的合理读法：
  - A：只改 `cardWaitEventActionable`，用 `source_task == envelope.attempt` 当闸 → 旧 attempt 仍 Encode，故事 2 失败（B233.6 已证明 store 去重不回答「是不是当前派发」）。
  - B：严格执行「没有当前 DispatchSnapshot 就不叫醒」，不改 B353 夹具 → 现网 follow 回归里那条 `delivery_failed` 消失，被当成 B353 回退。
  用户刚选的「card wait 同闸」就是为了避免叫醒面再分叉；这里分叉会直接改 stdout。
- **建议**：① 方案段写死：card wait 与 wakeconsumer 一样，当前身份只来自 **该事件 `CardID` 上、envelope.node 对应的最新 `EvDispatched` 快照**；禁止只比本条 envelope 与 source 列是否自洽。`--subtree` 按事件所属卡取快照，不用 wait 根卡。② 接缝 3 把取数算进 `runCardWait` 的生产路径（符号仍可落 contract），并断言：无匹配快照的 `task_mirrored` 不 Encode；旧 attempt 不 Encode；当前 attempt + 可动作类型 Encode。③ 写明 B353 回归夹具必须补一条匹配的当前派发快照；plain / 缺 node/attempt 的镜像从「今日 Encode」变为「不 Encode」是本卡收紧，验收不得拿无节点直派卡当 B353 假红。

### Important

#### I1. 「缺 source_task 闭集拒绝」容易被读成「缺任一 source 列」；两边都空的本机 target 未进接缝

- **位置**：问题陈述 `b349.md:34`、方案 `b349.md:68-72`、接缝 2–3 `b349.md:118-119`；活代码 `internal/ledgermirror/mirror.go:517-518`、`mirror_test.go:546-548`；`internal/ledgerstep/dispatch.go:365-369`（空 target 写入 `DispatchSnapshot.Target`）；`internal/agentd/wakeconsumer.go:104-108`（缺 node/attempt 已是跳过，不是打循环）
- **事实**：本机镜像 `source_target` 取 `TaskLink.Target`，夹具钉空串。ViaTemplate 在请求与模板 target 都空时把 `DispatchSnapshot.Target` 写成 `""`（`dispatch.go:149-152,365-369`）。正文正确写了「两边都空算匹配；一边空一边非空不匹配」和「缺 `source_task` 闭集拒绝」。但接缝 2 只列「`source_task` 或 `source_target` 对不上」「缺 `source_task`」，没有「两边 `""` 的本机提问仍唤醒」。`source_seq` 不参与匹配写在方案里，接缝没锁「`source_seq` 故意不同仍唤醒」。
- **为什么承重**：本机空 target 是 B271 之后的默认派发身份。若实现把闭集写成「任一 source 列为零值即拒」，本机 `question` / `delivery_failed` 会全部不叫醒，故事 1–2 的主路径灭灯。这不是正文没写，是接缝没把正文里那句最容易被闭集吞掉的正例锁住。
- **建议**：接缝 2 与 3 各加一条正例：当前快照 `Target==""` 且镜像 `source_target==""`、`source_task` 等于当前 Attempt 的可动作类型 → 唤醒 / Encode；`source_target="linux-01"` vs 当前 `""` → 不唤醒。另加：`source_seq` 与源序号不一致仍按身份匹配（去重是 store 的事）。

#### I2. 未显式废止 B233.6 plan S3「不得改 proto / source 由 store 去重保证」

- **位置**：spec 允许的契约增量 `b349.md:105`、弃选 `b349.md:86`；冻结 plan `docs/superpowers/plans/b233.6-plan.md:493,503`
- **事实**：plan §5.2 原文：「不得修改 `api.go`、`ledgerapi.go`、`proto/ledger.go`，不得新增 DTO、HTTP endpoint、source 字段或 cursor 文件。」下一条：「source identity 的 `(target, task, seq)` 由 S2/store dedup 保证，S3 不增加或复制这些字段。」这是 B233.6 已冻结范围内的实现范围句，review-3 指出缺口后才开本卡。B233.6 **契约** §2.4 其实要求消费者检查 source identity，与本卡同向；冲突在 plan 范围句，不在 contract 语义。正文用弃选 C 解释了「store 去重不够」，但没有「本卡废止 plan 哪一句」。
- **为什么承重**：L3 下一节点是 contract。不写废止，冻结物回写会把「加三列 + 持久化 cursor」打成对 B233.6 plan 的违约，或反过来不敢改 proto。任务书要求冲突标「需改契约」或「本卡显式废止哪一句」。
- **建议**：契约语义段加一句：**不退回 B233.6 contract**（§2.4 / P3 仍有效）；**本卡废止** plan §5.2 L493 与 L503 对 S3 的范围限制（不扩 proto、不写 source 字段、不写 cursor 文件、source 只靠 store 去重）。P3「闸在消费者、ledger 不揽当前 attempt 判定」继续有效。

#### I3. 「当前派发身份」写成 `DispatchSnapshot.(Target, TaskID)`，现网闸与 B233.6 冻结钉的是 `Attempt` 字段

- **位置**：方案 `b349.md:63-67`；活代码 `wakeconsumer.go:83-86,120`（`currentWorkflowAttempt` 只收 `snapshot.Attempt`）；写入 `dispatch.go:368-369`（生产路径 `TaskID` 与 `Attempt` 都赋 `taskID`）；夹具 `wakeconsumer_test.go:55-61`（`TaskID: task, Attempt: attempt` **可以拆开**）
- **事实**：B233.6 冻结 #7–#10：Transport 返回的 taskID 即 attempt，新快照 `Attempt` 必须等于该 taskID。生产写入二者同值。现网 `acceptsCurrentWorkflowAttempt` 比较的是 envelope.attempt 与 **`snapshot.Attempt`**，从不读 `snapshot.TaskID`。正文却说身份是 `(Target, TaskID)`，闸条件写「attempt 等于当前 TaskID」。`appendWorkflowDispatchForConsumer` 把 `task` 和 `attempt` 当两个参数，B233.6 回归夹具里它们不是同一个字符串。
- **为什么承重**：实现者若把 `currentWorkflowAttempt` 改成返回 `snapshot.TaskID`，现网 `TestB2336StaleAttemptDoesNotWake` 的「当前」attempt 对不上 TaskID，当前提问反而不醒。若继续用 Attempt、忽略正文的 TaskID 字段，card wait 又可能去读 `task_id` JSON 键。生产路径上两字段同值，分叉会被测试夹具放大。
- **建议**：写死：当前身份 = 该节点最新一条 `Node!="" && Attempt!=""` 快照的 `(snapshot.Target, snapshot.Attempt)`；`Attempt` 即这次 task ID（B233.6）。`snapshot.TaskID` 必须与 `Attempt` 同值才算当前派发，不一致的快照不当当前（兼容夹具拆开的情况：以 Attempt 为准，并在日志里打出两字段）。`source_task` 与 envelope.attempt 都与这个 Attempt 比。

#### I4. 水位落盘时序与装配点没钉死：先落盘会变成「恰好零次」；接缝写 `SetupAutomation` 但 cursor 清零在 `NewServer`、循环在 `StartAutomation`

- **位置**：方案 `b349.md:78-80`、接缝 4 `b349.md:121`；活代码 `server.go:198-199,267-291`（`NewServer` 把 `automationSeen` 建成空 map，cursor 零值 0）、`server.go:2554-2567`（`SetupAutomation` 不碰 cursor）、`scheddrain.go:51-88`（`StartAutomation` → `automationLoop` → `consumeAutomationEventsOnce`）、`wakeconsumer.go:217-222,314-324,359-388`（先处理/唤醒，结束才改 `automationCursor`；attach 暂缓 early return 不改）
- **事实**：正文要「至少一次」：崩溃在落盘前，最后一批可能再醒；attach 暂缓不推进。这与现网顺序一致。但「每次真正推进时写入」没说写入发生在 in-memory cursor 赋值之前还是之后。接缝 4 的调用方是 `SetupAutomation`，而生产第一次消费在 `StartAutomation`；单测主路径是 `SetupAutomation` 之后直接调 `consumeAutomationEventsOnce`（`wakeconsumer_test.go:29-31,556`）。
- **为什么承重**：若实现把「即将处理的 seq」在唤醒前写入 DataDir，宕机窗口里的事件永远不醒——这正是用户否决的选项 C（启动 MAX(seq)）的同构缺陷。若把读回挂在 `StartAutomation`，接缝 4 描述的「新 Server、再 SetupAutomation」测不到读回，看起来像没做持久化。
- **建议**：写死三句：① 只在现网会执行 `s.automationCursor = maxProcessed` 的那些出口之后落盘（含唤醒失败推进；不含 attach 暂缓 early return）。② 禁止在本轮唤醒/跳过之前把 cursor 文件前移。③ 读回发生在 `SetupAutomation`（或与之等价、不依赖 `StartAutomation` 的装配点），使「新 Server + SetupAutomation + `consumeAutomationEventsOnce`」就能续拉。文件名仍归 contract。

### Minor

#### M1. 现状表「`DispatchSnapshot.Target` 可空见 ledgerstep 本机 runner 夹具」引用行不是快照字段

- **位置**：现状 `b349.md:51`；台账 L7 写 `runner_test.go:733`
- **事实**：`:733` 是 `StepRunner.Target: ""`。空串能进 `DispatchSnapshot.Target` 是因为 `ViaTemplate` 在请求与模板缺省都空时原样写入（`dispatch.go:149-152,368`），`feature-impl` 种子无 `Def.Target`。结论成立，出处偏了一层。
- **建议**：改引 `dispatch.go#Dispatcher.ViaTemplate` 对空 target 的归一，或断言该测试落盘后的 `EvDispatched` payload。不挡批准。

#### M2. 「先 B353 类型表，再身份闸」与现网顺序相反；对叫醒可观察结果通勤

- **位置**：契约语义 `b349.md:103`；活代码 `wakeconsumer.go:254-280`（先 `acceptsCurrentWorkflowAttempt`，再 `automationWakeEvent` / `WaitDeliveryPolicy`）
- **事实**：缺 `source_task` 的 `permission_auto_allow` 在「类型表优先」下不会走到闭集日志，但仍不叫醒。顺序不是承重歧义。
- **建议**：改成「两道闸都为真才叫醒；顺序不承重」或明确保持现网身份优先。不挡。

#### M3. 第三处 `LedgerEvent` 投影在 web TS；正文只覆盖两处 Go eventWire

- **位置**：实现决定 `b349.md:112`（HTTP JSON 多三列、前端不改交互）；图 `m_web_api_ledger_LedgerEvent` = `web/src/api/ledger.ts:205`（字段同样无 Source*）；金样本 `internal/proto/contract_fixture_test.go:133-134` 用的是零值 `workflow_migrated`
- **事实**：omitempty 加列后，现有金样本字节不变（零值不出键），TS 结构体多出来的 JSON 键可忽略，「旧客户端忽略」成立。`TestContractFixtures` 纪律是「增删字段必须同步 Go/TS」——零值 omitempty 不改现样本，不强迫改 TS。
- **建议**：OOS 或实现决定补半句：不改 TS 类型与控制台交互；现有 `LedgerEvent.json` 金样本不得因加列改字节。不挡。

#### M4. attach 暂缓同一 tick 里已经唤醒成功的卡，重启后也会再醒；正文「这批」像只指暂缓那张

- **位置**：方案 `b349.md:80`；活代码 `wakeconsumer.go:304-324`（按卡名排序，B 暂缓则整函数 return，A 的 seen 只在内存、cursor 不前进）
- **事实**：至少一次已经覆盖。重启后 A 再 launch 一轮是现网 attach 语义的推论，不是新缺口。
- **建议**：把「这批」写成「本轮已读但 cursor 未前进的全部 seq（含本 tick 里已经唤醒成功的卡）」。不挡。

#### M5. 「镜像事件三列非空」与「`source_target` 可空」并置；`source_seq` 是 int64 零值

- **位置**：`b349.md:34`
- **事实**：卡原生事件三列经 `sql.Null*` 扫成 `""` / `0`（`events.go:86-97`）。镜像 `source_seq` 来自源事件 Seq，通常 ≥1，但类型上 0 不是「空」。不参与匹配已写清。
- **建议**：「非空」改成「`source_task` 必有；`source_target` 本机可空串；`source_seq` 为源序号，不参与身份匹配」。不挡。

## 3. 定级意见

独立套定级两问，**同意 L3 轻档**。不要降 L2，不要抬重档。

1. **跨几个子系统的契约面？** 定稿范围同时动：`d_protocol`（`proto.LedgerEvent` 加列）、账本门面投影（`internal/ledger/api#eventWire` + HTTP `ledgerEventWire`）、编排消费（`wakeconsumer` / DataDir 水位）、协调者命令面（`card wait` stdout）。顶层领域 ≥2，且是同一条「`task_mirrored` 是否叫醒」规则的跨面收口。
2. **动不动契约层？** 动。给已有跨进程 wire DTO 加 `source_*`（omitempty）。skill：对接已存在的跨仓/跨进程 wire 即便本侧零修改也按动契约层计；本卡是本侧真改。

轻档判据：单子系统工作量未超过流程固定成本到需要扇出并行子卡的程度；身份闸与游标拆开反而把「至少一次」和「当前派发」拆散。契约冻结与拆解照做、实现一轮——同意。不是 L2：加列是契约层。不是 L1：plan 不会只复述三行。

web TS 类型（`d_web`）按正文不改交互，不把本卡抬成还要扇出前端子卡。

## 4. 接缝

| 缝 | 符号 + 调用方 | 判定 |
|---|---|---|
| 1 `eventWire` / `ledgerEventWire` × `Facade.EventsFromAsc` 与卡详情 HTTP | `eventWire` ← `Facade.EventsFromAsc`（`api.go:81-90`）；`ledgerEventWire` ← `handleCardDetail`（`ledgerapi.go:333,367-375`） | **真缝。** 两处都是生产投影，不是假缝。禁止只测 helper 写对了。漏：穿过真实 JSON 的 roundtrip（缺席 vs `""` vs 非空；`source_seq` 缺席 vs 0）；web TS 明确不改（M3）。 |
| 2 `acceptsCurrentWorkflowAttempt` × `consumeAutomationEventsOnce` | 未导出；唯一生产调用方 `consumeAutomationEventsOnce:254` | **真缝，不是假缝。** 有生产调用方。覆盖当前/旧 attempt、缺 `source_task` 不打死循环——够。**漏两边都空正例、漏 `source_seq` 不参与、漏列与 envelope 不一致**（I1）。函数本身无图节点（§6）。 |
| 3 `cardWaitEventActionable` × `runCardWait` | 未导出；唯一生产调用方 `runCardWait:144` | **真缝。** B353 类型表回归是正当锁。**取数路径漏 EvDispatched（C1）；漏两边都空；「B353 回归仍成立」与无快照夹具互斥。** |
| 4 `consumeAutomationEventsOnce` × `SetupAutomation` 启停 | `SetupAutomation` 装配 Facade；真正循环在 `StartAutomation` | **真缝意图对**（重启续拉必须打到生产装配，不能只测改字段）。**调用方写窄了（I4）**：读回必须在不依赖 `StartAutomation` 的点完成；落盘时序必须锁在 cursor 赋值之后。attach 暂缓不推进写对了。 |

假缝检查：没有为走满分支抽出的无调用方纯函数占名额。`currentWorkflowAttempt` 未单独列缝——它今天只被 `acceptsCurrentWorkflowAttempt` 调用，作为内部锁可以，但不能代替缝 2/3 的调用方断言。

边界型：`proto.LedgerEvent` JSON 加列属 wire，缝 1 的 Facade/HTTP 编码入口即缝上符号，成立。card wait stdout 走 `json.Encoder` 编码 `ledger.Event`（**已经带** source 三字段），身份闸改变的是 **是否 Encode**，不是新 DTO；不要另造第三套投影。

## 5. 缺陷族

- **生命周期 / 状态机中断**：有风险，正文已对准一半。agentd 重启从 DataDir 续拉、attach 暂缓不推进、崩溃窗口至少一次——方向对。缺口是落盘时序与装配点（I4）：先落盘会丢事件；读回挂错函数会让接缝 4 假绿。`automationSeen` 不落盘：cursor 已经跨过的 seq 不会再读到，B274 自生 `needs_human` 仍靠当轮内存 seen——与现网 `wakeconsumer.go:336-352` 同构，**无新的自激窗口，因为** 失败路径仍把自生事件标 seen 并推进 cursor，持久化若跟这条赋值走则重启不会重放那条。同一 tick 先醒 A 再 attach-暂缓 B，重启 A 会再醒一轮（M4），至少一次已覆盖，不构成新孤儿。card wait 仍从 `MaxSeq()` 挂、不重放历史（`card_wait.go:78-83`），B352 只打自动化——成立。
- **静默失败 / 误导报错**：缺 `source_task` 闭集拒绝叫醒、打日志、不打死循环——正面设问有答案。malformed envelope 现网仍返回 error 打断消费（`acceptsCurrentWorkflowAttempt:97-102` → `consumeAutomationEventsOnce:256-257`），正文没放宽，应保持。风险是闭集被做成「空 target 也拒」从而本机叫醒静默消失（I1）。card wait 分类失败现网是命令失败不是静默降级（`card_wait.go:144-148`），同闸后应保持：无法判断当前派发时不得假装「审计」。
- **跨平台假设**：水位用 DataDir 文件，与 `room-cursors.json` 同类，不进共享 PG、不抢 `$HOME/.handoff/cursors`——与 B233.6 §3.3 镜像不得抢协调者 cursor 同向。多 agentd 共用 PG 时各持私有进度、可能各自叫醒：这是用户否决「水位写进共享账本」的直接推论，**无，因为** 正文把它写成进程私有且弃选已否决共享账本。路径分隔依赖现网 `filepath.Join`，与房间游标相同，本卡不新引入 Windows 假设。
- **假红 / 假绿测试**：接缝禁止只测 helper，方向对。假绿温床：缝 3 不扫 `EvDispatched` 仍能让「source 自洽」用例通过（C1）；缝 2 没有两边都空正例（I1）；缝 4 只 `SetupAutomation` 不读回文件（I4）。反面断言「旧 attempt 不唤醒」现网 B233.6 已有，但只锁 envelope.attempt，不锁 source 列——本卡必须在调用方上加列不一致的反面，不能只扩 helper。B353 无快照夹具会假红（C1）。
- **门禁绕过**：本卡不新增写/执行入口，不改 `mirrorSkip`、不改 `WaitDeliveryPolicy` 假集合（OOS 永不做）。过滤仍在消费点。**无，因为** 没有新的权限门或 TOCTOU 写路径；identity 闸失败只是不叫醒，不删账本行。
- **序列化边界（追加）**：必须穿过的手写投影：`eventWire`、`ledgerEventWire`、HTTP `CardDetail.Events`、card wait `Encode(ledger.Event)`（已有三列）。`proto.LedgerEvent` 加列后 Facade 才把列送到 wakeconsumer——这是本卡存在的原因，缝 1 覆盖。漏：缺席/`""`/非空三态 roundtrip；`source_seq` JSON omitempty 的 0 vs 缺席。web TS 可不加字段（M3），但 Go 金样本零值不得改字节。
- **枚举新值过既有白名单**：**无，因为** 不新增事件类型；加的是已有列的 wire 投影。`WaitDeliveryPolicy` 假集合本卡不得改（与 B353 OOS 互锁）。
- **承重安全属性**：旧 attempt 不叫醒、缺 `source_task` 不叫醒且循环继续、两边空 target 仍叫醒、cursor 至少一次——每条都需要能变红的调用方测试。现网只锁了 envelope 旧 attempt。本卡若不锁 source 列不一致，属性会在「顺手只信 envelope」时失守。

## 6. 图覆盖债

独立复跑（`--repo /Users/xushixin/workspace/handoff`）：

| 查询 | 结果 |
|---|---|
| `sym LedgerEvent` | 命中 `m_proto_LedgerEvent`（`internal/proto/ledger.go:119`，字段无 Source*）与 `m_web_api_ledger_LedgerEvent`（`web/src/api/ledger.ts:205`，同样无 Source*）。spec 备注只写了前者。 |
| `sym acceptsCurrentWorkflowAttempt` | **不在图中**（与 spec 备注一致）。活代码 `wakeconsumer.go:97`。 |
| `sym currentWorkflowAttempt` | **不在图中**。活代码 `wakeconsumer.go:54`。 |
| `sym eventWire` | **不在图中**；近似候选 `n_agentd_ledgerEventWire`。活代码 `internal/ledger/api/api.go:147`。 |
| `sym ledgerEventWire` | 命中 `n_agentd_ledgerEventWire`（`ledgerapi.go:112`）。 |
| `sym WaitDeliveryPolicy` | **不在图中**（B353 已记）。活代码 `internal/client/delivery.go:23`。 |
| `who-calls n_agentd_Server_consumeAutomationEventsOnce` | 节点命中，但 **edges 为空**，并警告 5 个未扫描入口。图签名行号 **85**（活代码 **211**），基线图陈旧。调用方以 grep 为准：`scheddrain.go#runAutomationPass`、wakeconsumer 测试。 |
| `resolve --doc docs/superpowers/specs/b349.md` | 退出 0。六个 `file#Symbol` 锚 `ok`/`moved`，无坏锚。`#currentWorkflowAttempt` / `#cardWaitEventActionable` / `#SetupAutomation` 等未写成符号锚，resolve 不报。 |

现状以源码为准。未把图行号写成代码事实。

## 7. 与已冻契约 / OOS / 架构法

- **B233.6 contract §2.4**：消费者必须同时检查 node、attempt、source identity。本卡是补实现，**不需改契约语义**。plan §5.2 L493/L503 的范围句必须由本卡废止（I2）。
- **P3 拍板 A**：闸在 wakeconsumer（现扩到 card wait 同正文），ledger 保持薄、不提供「当前 attempt」派生查询。正文 `b349.md:102` 写明。**无冲突。** 把身份规则下沉成 ledger 派生查询是用户否决项，审查同意。
- **B353**：过滤在消费点；假集合不改；三处共用类型表。本卡在类型表之外加身份闸，OOS 写明不改 `WaitDeliveryPolicy`。**无冲突。** 收紧的是 card wait 对缺 node/attempt / 无当前快照镜像的 Encoded 行为，必须写进故事以免验收假红（C1）。
- **B192**：本卡比较的是镜像 `source_target` 与 `DispatchSnapshot.Target`，不碰 `WorkBranchInfo.Target` 的「空 = 不能证明同机」。**未触及。**
- **B274**：自生 `needs_human` 仍用当轮内存 seen；不持久化 seen。与现网失败路径一致。**未改假集合、未改自激防护归属。**
- **OOS**：本期不做的 B351 远端孤儿、B233.8 迁包、跨机真机均已在 `docs/roadmap.md`（B351 以「远端孤儿 task 无自动回收」行存在，B233.6 真机验收节；B233.8 / 跨机真机在 B233.7 验收节）。B352 的「游标未持久化」是本卡要做的，未误标本期不做。永不做（新 wait 命令、守护进程、扩 `mirrorSkip`、改假集合、source 进 envelope、水位进共享账本、启动 MAX(seq)、持久化 seen、控制台身份闸）未混进 roadmap。合格。
- **架构法**：规则归数据所有者——「是不是当前派发」读的是卡事件流上的快照与镜像列，判定留在消费点，不下沉 ledger 派生查询（第五条）。水位是这台 agentd 的消费进度，不进共享账本（不让 ledger 偷消费者状态）。不穿透两级改 store 唯一索引语义。拒绝「绕过 Facade 直读 Store」保持门面。**无违法。** 第十条：spec 展开到消费点 + 投影门面 + 已冻契约一层，没有把 ledgermirror 内部订阅细节写成实现落点。

## 8. 现状表核对（亲手对源码）

| spec 声称 | 活代码 | 结论 |
|---|---|---|
| 契约已要求核 source identity | B233.6 contract §2.4；P3=A | 成立 |
| 实现只核 envelope node/attempt | `acceptsCurrentWorkflowAttempt:104-125` 只比 `*envelope.Attempt` 与 `currentWorkflowAttempt` 返回值 | 成立 |
| 当前 attempt 只扫 `EvDispatched` | `currentWorkflowAttempt:74-86` | 成立；且 **不读** Target/TaskID/source 列 |
| `ledger.Event` 有三列；`proto.LedgerEvent` 没有 | `types.go:141-149`；`proto/ledger.go:120-127` | 成立 |
| 两处投影丢列 | `api.go:147-155`；`ledgerapi.go:111-115`（注释仍说「照抄全字段形状」但字段没抄） | 成立 |
| wakeconsumer 经 Facade 拿 proto | `autoLedger *ledgerapi.Facade`；`EventsFromAsc` 走 `eventWire` | 成立 |
| `card wait` 拿得到三列，分类只用 `WaitDeliveryPolicy` | `Store.Follow` 回调 `ledger.Event`；`cardWaitEventActionable:216-229` | 成立 |
| store 唯一索引只防同一源事件重插 | PG `store.go:233-235` `WHERE source_target IS NOT NULL`；SQLite `:312-313` | 成立 |
| 列是权威身份，不进 envelope JSON | `mirror.go:60-63` | 成立 |
| `source_target` = `MirroredEvent.Target` = `TaskLink.Target` | `ledgermirror/mirror.go:517-518`；挂账 `events.go:190-191` 用 `snap.Target` | 成立 |
| 本机 target 可空 | `mirror_test.go:546-548` | 成立 |
| `DispatchSnapshot.Target` 可空 | 生产写入允许 `""`；spec 引 `runner_test.go:733` 偏（M1） | 事实成立，出处偏 |
| cursor/seen 纯内存 | `server.go:198-199,291`；`SetupAutomation` 不读盘 | 成立 |
| 镜像水位已持久、不是叫醒水位 | `Store.MirrorWatermark` | 成立 |
| `card wait` 从 `MaxSeq()` 起挂 | `card_wait.go:78-83` | 成立 |
| DataDir 房间游标先例 | `server.go:2567` `room-cursors.json` | 成立 |
| B353 已并入；类型表三处共用 | `delivery.go:23-34`；wake `automationWakeEvent:143`；card wait `:229`；`--follow` 已在 | 成立；**不是**把将做的事写成已有 |

未发现「写成仿佛已有的 flag / 函数」类 Critical（B353 那次的同类问题本卡没有）。

## 9. 二解测试（任务书点名的承重题）

| 陈述 | 读法 A | 读法 B | 承重？ | 正文是否钉死 |
|---|---|---|---|---|
| 当前派发身份 | 最新 `EvDispatched` 的 `(Target, Attempt)` | `snapshot.TaskID` 字段，或「本条 source_task == envelope.attempt」 | **会**（C1、I3） | 定义有，取数路径与字段名没钉 |
| 两边都空的 target | 匹配，本机叫醒 | 空 = 缺身份，闭集拒绝 | **会**（I1） | 方案钉了 A；接缝没锁 |
| 缺 source_task vs 缺 source_target | 缺 task 闭集拒；缺 target 走相等（双空匹配） | 缺任一列都拒 | **会**（I1） | 方案钉了 A；接缝只写缺 task |
| source_seq 是否参与匹配 | 不参与 | 三元组全等才算当前 | 会（弱） | 方案钉了不参与；接缝没锁 |
| card wait 要不要读 EvDispatched | 要，按事件 CardID | 不要，Follow 已经有三列 | **会**（C1） | 规则暗示要；接缝没写 |
| cursor 文件 vs 共享 PG | DataDir 进程私有 | 写进 ledger / 多 agentd 抢一条 | 否（已钉） | 弃选 + 方案钉死私有 |
| 至少一次 vs 恰好一次 | 落盘前崩溃再醒 | 先落盘再唤醒 = 丢 | **会**（I4） | 「至少一次」有；落盘先后没钉 |
| 没有当前快照的 task_mirrored | card wait 不 Encode（同 wake） | 仍按 B353 类型表 Encode | **会**（C1） | 同闸暗示 A；故事/B353 回归没写 |

## 10. 批准前最小补丁（只改 spec 正文，不是代码）

1. **C1**：写死 card wait 当前身份的取数路径（事件所属卡 × envelope.node 的最新 `EvDispatched`）；接缝 3 锁无快照 / 旧 attempt 不 Encode、当前可动作 Encode；声明 B353 夹具要补匹配快照，plain 镜像不 Encode 是本卡收紧。
2. **I1**：接缝 2/3 加两边都空正例，以及一边空一边非空、`source_seq` 不参与的反例/正例。
3. **I2**：显式废止 B233.6 plan §5.2 L493 与 L503 的 S3 范围限制；声明不退回 B233.6 contract / P3。
4. **I3**：当前身份字段钉死为 `snapshot.Attempt`（即 task ID）+ `snapshot.Target`。
5. **I4**：落盘发生在 in-memory cursor 赋值之后；读回挂在 `SetupAutomation`（不依赖 `StartAutomation`）。

I1–I4 与 C1 一并写入后可以再送批。M1–M5 不挡。方向保持：消费点身份闸 + proto 加列补投影 + DataDir 私有 cursor；不扩 `mirrorSkip`、不改假集合、不下沉 ledger 派生查询、不把水位写入共享账本。
