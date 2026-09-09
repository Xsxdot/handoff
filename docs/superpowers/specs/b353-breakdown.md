# B353–B355 唤醒面拆解提案

**状态：拆解提案，待协调者拍板（2026-09-09）**
**卡：** B353（B354、B355 并入；不另建实现卡）
**标题：** 监听者唤醒面失控：审批链自动裁决等纯审计事件逐行唤醒主会话
**定级：** L3 轻档；实现一轮收口
**有效基线：** `cards/B233.1-charter-7 @ 48f2736a`（当前分支不切换、不越过）
**上游 spec：** `docs/superpowers/specs/b353.md`（头部状态：已批准）
**冻结 contract：** `docs/superpowers/specs/b353-contract.md`（头部状态：已冻结；本轮核对修订记录见 §9）
**本稿台账：** `docs/superpowers/ledgers/2026-09-09-b353-breakdown-ledger.md`
**图依据：** `codegraph/best.json`；父为空的顶层领域以 best 为准
**角色边界：** 本文是提案；不写实现代码、不建卡、不派发、不调用 handoff CLI。实际扇出与拍板归协调者。

## 0. 待拍板岔口（集中）

以下选择均不是本稿自行批准的实现事实；未裁决前不得据此扇出实现。正文按方案 A 的单卡布局书写，只为让接缝保持在一个可审阅上下文内。

1. **P1｜外部子卡形态。**
   - **方案 A：一张跨域实现子卡** `B353-impl`，内部以 T0→T1→T2/T3→T4 的 DAG 串行/受控并行推进；优点是“同一张分类表 × 三条消费链”的接缝集中，缺陷族与真实机清单只有一份；代价是实现卡文件面横跨 CLI、client、ledger 读侧、agentd 与文档。
   - **方案 B：三张功能子卡**（任务 wait/client、card wait、wakeconsumer）再加一张串行接缝回归卡；优点是每张文件面更窄；代价是三处策略容易各自变绿而接缝漂移，且本卡 L3 轻档要多一轮协调。
   - **待拍板：** 选择 A 或 B；若选 B，三张功能卡仍必须共享本稿冻结的 T4 接缝验收，不得各自发明事件白名单。

2. **P2｜操作文档同步面。**
   - **方案 A：** 同批更新 `skills/handoff/SKILL.md` 与 `README.md`，分别固定两类 harness 的挂法和用户可见事件表；优点是技能与 README 不漂移，代价是文档改动面稍大。
   - **方案 B：** 只更新 `skills/handoff/SKILL.md`，README 留到后续文档轮；优点是实现提交更小，代价是 README 继续把任务事件/卡事件的唤醒口径说窄或说旧。
   - **待拍板：** 是否本轮同时更新 README；无论选项，不能把“文档已同步”当成运行时行为已验证。

3. **P3｜项目缺陷族清单的基线标记治理债。**
   - 已实读 `docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md`：顶部没有纪律要求的“基线版本：charter@<commit>”行，但清单正文已包含通用五族、序列化边界、新枚举白名单、webview 候选族；本稿另加承重安全属性回答。
   - **方案 A：** 协调者在本卡拍板前先补清单顶部基线行，再以该版本作为本轮族清单基线；优点是治理状态可追溯，代价是本轮多一个项目级文档变更。
   - **方案 B：** 本轮不改项目清单，只把缺标事实和采用的族集合落入本稿/台账，另立治理修复；优点是卡面不扩，代价是清单顶部债继续存在。
   - **待拍板：** 选择 A 或 B；本稿不把缺标写成“已对齐”。

## 1. 触及子系统清单与派卡资格核验

`codegraph/best.json` 的 `domains` 中 `parent` 为空的顶层领域是项目子系统清单；类型直接取其 `type`。本卡的直接消费面与必要的外部行为依赖如下。`d_gateway`、`d_execution` 没有本卡实现文件，只列为边界依赖，不据此扩大实现卡。

| 子系统（best id） | 类型 | 本卡有界文件集与暴露面 | 四条派卡资格核验 |
|---|---|---|---|
| `d_cli` | 逻辑型 | `cmd/card_wait.go`、`cmd/wait.go` 及 `cmd/card_wait_test.go`、相关 CLI 行为测试；暴露面是既有 `handoff wait` / `handoff card wait` 的 stdout、退出码和 `--follow` flag。 | ①只圈两处命令入口与测试；②flag、逐行 JSON、124/0 已枚举；③依赖 `d_transport`、`d_ledger`，不新增方向；④可用 Go 命令测试闭环。 |
| `d_transport` | 边界型 | `internal/client/delivery.go`、`client.go`、`backlog.go`、`follow_test.go`、`execution_test.go`、必要的 `client_test.go`；暴露面是 WS 事件、游标、重连、`WaitDeliveryPolicy` 消费。 | ①文件集有界；②`WaitEvent`/`FollowEvents` 签名与策略调用点已冻结；③只复用既有 client→agentd 通道；④机内验 JSON/WS 形状，真实网络/relay 归真机。 |
| `d_ledger` | 逻辑型 | `internal/ledger/follow.go`、`types.go`、`follow_test.go`，以及仅作不变性护栏的 `internal/ledgermirror/mirror.go`；暴露面是 `Store.Follow`、卡事件类型、现有 `mirrorSkip`。 | ①只读侧与事件词表有界；②`fromSeq` 排他、动态成员、500 批次、2 秒轮询和 mirrorSkip 已枚举；③不新增账本接口/事件总线；④真实 SQLite 可机内闭环，PG LISTEN 为边界清单。 |
| `d_orchestration` | 逻辑型 | `internal/agentd/wakeconsumer.go`、`wakeconsumer_test.go`；暴露面是 `automationWakeEvent` 与 `Server.consumeAutomationEventsOnce` 到 Keystone 的既有入口。 | ①不把整个 `internal/agentd` 目录塞进卡；②task envelope 解包、当前 attempt 闸、同卡合并、cursor/seen、失败升级均已枚举；③不新增自动化 HTTP/存储面；④真实 ledger + fake runner 可闭环，目标 agentd 行为归真机。 |
| `d_keystone` | 逻辑型 | `internal/keystone/keystone.go`、既有 `keystone_spec_test.go`/`slice_test.go` 作为 WakeEvent/Decide 载体护栏；本卡不提议新增 Keystone 类型或持久状态。 | ①只验证既有 `WakeTaskTerminal`/`WakeTicket`/`WakeMessage`；②映射面已冻结；③不让 Keystone 读取 transport 流；④fake runner 可观测，真实 coordinator session/HOME 归真机。 |
| `d_protocol` | 逻辑型 | `internal/proto/proto.go`、`rooms.go` 及现有协议/房间 fixture 测试；本卡不新增 EventType、RoomMessage 字段或 JSON 键。 | ①仅核对已有 `EventType`、`LedgerEvent`、`RoomMessage` 形状；②现有词表与可空/零值区分可枚举；③无新 wire 接缝；④Go JSON roundtrip 可闭环。 |
| `d_gateway` | 边界型（仅依赖） | 既有 agentd HTTP/WS 事件入口，不派本卡生产文件；仅把 `/ws/events` 的真实对端行为列入真机清单。 | ①无新增 gateway 文件；②没有新 HTTP 字段/路由；③继续使用既有 client/agentd 合同；④机内只验 client WS 形状，真实 agentd/跨机归协调者。 |
| `d_execution` | 边界型（仅依赖） | 真实 executor 产生 `question`、`permission_request`、`delivery_failed`、`stalled`、终态等事件；本卡不改 executor。 | ①无本卡实现文件；②任务事件类型已有协议定义；③事件事实权威仍在 source task；④必须标“未验证，需真机”，不得用 fake 证明 executor 真发过事件。 |

**不列为本卡实现域：** `d_workspace`、`d_sessions`、`d_collab`、`d_web`、`d_policy`、`d_maintenance`、`d_scheduling`。其中 `skills/handoff/SKILL.md` 与 `README.md` 是文档交付面，不等于新增运行策略或 Web 子系统；本卡不碰浏览器 API、桌面 webview、工作树、PTY、调度准入或房间写入口。

### 1.1 架构法第三条：有界文件集与竖切债

`cmd` 与 `internal/agentd` 都是扁平大包，但本卡入口只落在 `cmd/card_wait.go`、`cmd/wait.go` 与 `internal/agentd/wakeconsumer.go` 两个职责切片；client、ledger、keystone、proto 也各有明确符号入口。当前能圈出有界文件集，**不插额外竖切还债卡**。若 P1 选 B，拆卡只能沿 T1/T2/T3 文件集切开，不能按整个目录切。

## 2. 契约增量核对

### 2.1 上游状态位

- spec 文件头为“已批准（2026-09-09，用户原话「推进吧」）”；本稿引用该文件状态，不引用会话记忆。
- contract 文件头为“已冻结（本提交冻结）”，并已在本轮核对中追加 §9 修订记录；本稿不把核对澄清藏在会话里。
- contract 与 spec 均声明基线 `cards/B233.1-charter-7 @ 48f2736a`；本分支当前 HEAD 为冻结 contract 提交，不切换、不越过有效基线。
- `WaitDeliveryPolicy` 未被 best 图单独扫描；contract 已将 `internal/client/delivery.go#WaitDeliveryPolicy` 标为图覆盖债，本稿把源码/契约作为权威，不创造第二策略源。

### 2.2 冻结物逐项对照

| 冻结物 | 本稿吸收位置 | 越界结论 |
|---|---|---|
| contract §1：只冻结消费契约，不改账本落库、传输、镜像可见性 | §1、B353-impl/T2/T3 | **不越界。** 过滤留在 wait stdout 与 wakeconsumer 消费点；`show` 仍可见审计事件。 |
| contract §2 接缝：`runCardWait` 只增内部 `follow` 参数，`Store.Follow` 签名不变 | B353-impl/T2 | **不越界。** `--follow` 是既有 `card wait` 的 flag，不新增命令、Store 方法或事件总线。 |
| contract §2：`WaitEvent`/`FollowEvents` 的 `all=false` 只交付策略为真，`all=true` 保持全量 | B353-impl/T1 | **不越界。** 七项假集合不变；任务流仍由 `WaitDeliveryPolicy` 唯一命名。 |
| contract §2：`automationWakeEvent` 解包 `task_type` 后调用 `WaitDeliveryPolicy` | B353-impl/T3 | **不越界。** 不在 wakeconsumer、CLI、镜像包复制白名单。 |
| contract §3.1 #1–#12：假集合七项、策略外类型、应用消费点 | B353-impl/T1 | **不越界。** `delivery_failed`、`stalled`、`approval_dropped`、`archived`、压力告警等策略外现有类型必须可交付；WS/HTTP 仍可读审计帧。 |
| contract §3.2 #13–#34：卡原生事件、task_mirrored 解包、stdout、终态与 timeout | B353-impl/T2 | **不越界。** 输出仍为原始 `ledger.Event`；`status_moved` 只收尾、不作为唤醒行；`Store.Follow` 起点、动态成员和轮询不变。 |
| contract §3.3 #35–#48：wakeconsumer 类型映射、room message、mirrorSkip、同卡合并与失败升级 | B353-impl/T3 | **不越界。** 只补消费映射；不扩 `mirrorSkip`，不改 attach/seen/cursor/升级语义。 |
| contract §4：PG/SQLite、WS 读限/重连/HTTPClient 等现状依赖 | §5 真机清单、B353-impl/T1/T2 | **不越界。** 机内测试只能证明既有形状；PG、relay、WS 关闭码、跨平台行为不写成机内结论。 |
| contract §5–§8：依赖方向、四项拍板、plan 附区与不新增 Ticket 0 | §3 DAG、B353-impl、§6 | **不越界。** 本稿不生成分支视图 diff，不引入 Ticket 0，不改变 `allDone`/`ExitTimeout`/`Store.Follow` 的责任。 |

**契约增量结论：不退回 contract。** 本稿只排已有三处消费接缝；若协调者在 P1/P2 裁决中要求新增 HTTP 字段、事件类型、第二策略表、镜像过滤责任、Keystone 持久事实或新的 wait 命令，均超出本结论，必须先回退 contract，不得边实现边加接缝。

### 2.3 本轮边界澄清回写

已回写 `b353-contract.md §9` 的澄清为：`runCardWait → Store.Follow` 是已有包内 API 面；`task_mirrored.task_type` 是既有 envelope 输入而非新 wire 字段/枚举；卡原生事件复用既有 Keystone WakeKind；skill/README 只是操作文档同步面。上述均不产生新跨域接缝。

## 3. 子卡清单与依赖 DAG

本节提出子卡，不在本回合创建或派发外部卡。按 P1 方案 A，提案是一张跨域实现子卡，内部段保持接缝同审；若协调者选择 P1 方案 B，T1/T2/T3 可分别变成外部子卡，但必须保留 T4 串行接缝验收。

### 3.1 DAG

```text
T0 边界/冻结物核验
 └──> T1 任务 wait/follow 与唯一策略
        ├──> T2 card wait stdout/退出模型
        └──> T3 wakeconsumer/Keystone 映射
               └──> T4 三链穿缝回归 + skill/README 同步
T2 ────────────────────────────────┘
```

| 内部段 | 前置 | 产出与交棒 |
|---|---|---|
| T0 | spec、contract、best.json | 固定文件集、类型分流、无新接缝结论；不改运行时代码。 |
| T1 | T0 | 证明/补齐任务侧 `WaitDeliveryPolicy` × `WaitEvent`/`FollowEvents` 的真实消费行为；T2/T3 共用该权威。 |
| T2 | T0、T1 | 卡侧只输出可动作事件；默认一条即退出，`--follow` 才连续跟随；保留原始 ledger JSON 和终态收尾。 |
| T3 | T0、T1 | task_mirrored 按同一策略映射到既有 WakeKind；补齐 decision opened/answered 与策略外 task type；保留当前 attempt/attach/seen/cursor/失败升级。 |
| T4 | T2、T3 | 穿过真实 JSON/WS/ledger/consumer 调用方的正反回归，文档操作契约同步，交协调者做真机清单。 |

### 3.2 子卡 B353-impl（提案）：三条消费链共用唤醒分类表

#### ①契约引用

- `docs/superpowers/specs/b353-contract.md` §§1–5、§7–§9；原子冻结项 #1–#48。
- `docs/superpowers/specs/b353.md` §§5–10、用户故事 1–3、Out of Scope、实现/测试决定。
- `codegraph/best.json` 顶层领域：`d_cli`、`d_transport`、`d_ledger`、`d_orchestration`、`d_keystone`、`d_protocol`；边界依赖 `d_gateway`、`d_execution`。
- 入口锚点：`cmd/card_wait.go#runCardWait`、`internal/client/client.go#Client.WaitEvent`、`internal/client/client.go#Client.FollowEvents`、`internal/client/delivery.go#WaitDeliveryPolicy`、`internal/ledger/follow.go#Store.Follow`、`internal/agentd/wakeconsumer.go#automationWakeEvent`、`internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`、`internal/keystone/keystone.go#Service.Decide`。

#### ②意图与为什么

把“什么事件值得唤醒协调者”收敛成一条可审计的消费语义：任务事件沿用唯一的 `WaitDeliveryPolicy`，卡侧在消费 `task_mirrored` 时解出 `task_type` 后复用它，卡原生事件只在 `card wait` 与 wakeconsumer 入口按冻结集合判定。这样自动审批、权限复用、comment、派发快照等纯审计事实仍留在账本/任务历史中，却不再逐行打到 stdout 或触发 Keystone；`delivery_failed`、真实工单、用户房间消息、等人和裁决应答仍有明确的可动作唤醒出口。

该子卡同时收口 card wait 的退出模型：无 `--follow` 时第一条可动作事件成功编码后退出 0；`--follow` 才持续到当前成员全部进入 `StatusDone`/`StatusClosed`；正 timeout 在默认模式是等待可动作/终态的总时长，在 follow 模式是任意新账本事件之间的空闲上限。审计事件不因过滤从账本删除，现有 `Store.Follow`、动态成员、起点 seq、镜像 skip、Keystone attach 让步和失败升级均不改语义。

本子卡不承担 source task 事实、executor 产生事件、跨机网络、真实 PG/WS/进程回收或 webview 行为的证明；这些是边界域真机清单，不用夹具替代。

#### ③验收

**T1：任务 wait/follow 与唯一策略（逻辑消费 + 边界通道形状）**

- 运行 `go test ./internal/client -run 'Test(B353|WaitDeliveryPolicy|WaitEvent|Follow)' -count=1`，命令退出 0；测试须穿过 `Client.WaitEvent` / `Client.FollowEvents`，断言 progress、approver_decision、approver_disabled、tickets_voided、ticket_answered、permission_auto_allow、permission_reuse 均不交付，`delivery_failed`、`stalled`、`approval_dropped`、`archived`、`resource_pressure`、`task_proc_pressure` 等现有策略外类型交付。
- 同一测试须断言 `WaitEvent(..., all=false)` 返回第一个策略为真的事件；`FollowEvents(..., all=false)` 只调用 `onEvent` 交付策略为真的事件；`FollowEvents(..., all=true)` 对相同 WS/store 帧交付审计事件；被过滤帧仍更新 follow 的“收到任意帧”空闲计时，不能被误判成无帧超时。
- 运行 `go test ./internal/client -run 'Test(B353|FollowFiltersAuditEvents|FollowAllDeliversAuditEvents|Follow.*Store)' -count=1`，命令退出 0；至少一条回归必须从真实 JSON/store 或真实 WS 文本穿过反序列化到 client 消费，而不是只调用策略 helper。
- 失败判据：七项假集合发生增删；CLI 或 wakeconsumer 出现第二份任务白名单；`delivery_failed` 被吞掉；`all=true` 被错误过滤；过滤帧导致 idle 计时不刷新；WS/HTTP 传输路径不再能读到审计帧。

**T2：card wait stdout、过滤和退出模型（逻辑闭环）**

- 运行 `go test ./cmd -run '^TestB353CardWait' -count=1`，命令退出 0；测试通过 `cardWaitCmd`/`runCardWait` 与真实 `ledger.Store.Follow` 回调观察 stdout，而非只测一个过滤 helper。
- 正向事件断言：`needs_human`、`needs_cleared`、`decision_opened`、`decision_answered`、`RoomMessage{Kind: user, BySystem:false}`、以及 `task_mirrored` 解包后策略为真的 `task_type` 各输出一行原始 `ledger.Event` JSON；输出行可被 `json.Unmarshal` 回原事件，不能改写 payload 形状。
- 反向事件断言：`comment`、`dispatched`、`acceptance_recorded`、系统房间消息、策略为假的 `task_mirrored` 均不输出 stdout 行，但之后从账本读取仍能看到它们；非终态 `status_moved` 不输出，也不结束 wait。
- 终态断言：`status_moved` 使所有当前成员成为 `StatusDone` 或 `StatusClosed` 时，先完成终态收尾再退出码 0，且不为该迁移输出唤醒行；单卡与 `--subtree` 均保留当前成员重算语义。
- 退出断言：无 `--follow` 时第一条可动作事件成功 `Encode` 后退出码 0，只有审计事件时继续等待；`--follow` 可连续输出多条可动作事件，直至当前成员全终态后退出码 0；正 timeout 过期使用既有 `ExitTimeout`（124）。
- 计时断言：默认模式的正 timeout 覆盖“等到可动作或终态”的总时长；follow 模式下任意新账本事件（包括被过滤审计事件）刷新空闲计时；ctx 取消、`Store.Follow` 错误、stdout 编码错误均返回非零且不打印成功收尾。
- CLI 形状断言：`go run . card wait --help` 的帮助含 `--follow`，`--subtree` 仍存在；没有新增第三条 wait 命令，`handoff wait --card` 仍由既有反例测试拒绝。

**T3：wakeconsumer 与既有 Keystone 入口（逻辑闭环）**

- 运行 `go test ./internal/agentd -run '^TestB353Automation' -count=1`，命令退出 0；测试通过真实 `ledger.Store` 事件、`Server.consumeAutomationEventsOnce` 和 fake runner/Keystone 入口观察是否唤醒，不只调用 `automationWakeEvent`。
- `task_mirrored` 的 `task_type` 先经 `WaitDeliveryPolicy`：策略为 false 时 `yes=false`、不产生 Keystone 事件；`permission_request`/`question` 映射 `WakeTicket`；其它策略为真的现有任务类型至少覆盖 `delivery_failed`、`stalled`、`approval_dropped`、`archived`，映射 `WakeTaskTerminal`，使 `delivery_failed` 的用户可观察结果是唤醒协调者执行 `resume` 的入口。
- 卡原生断言：`needs_human`、`needs_cleared`、`decision_opened`、`decision_answered` 产生 `WakeTaskTerminal`；只有 `RoomMessage{Kind:user, BySystem:false}` 产生 `WakeMessage`；`BySystem:true`、`status_moved`、`comment`、`dispatched`、`acceptance_recorded` 和其它未列卡事件不唤醒。
- 消费器断言：当前 attempt 闸、同卡合并、attach 暂缓、seen/cursor 推进、失败升级和“升级产生的 needs_human 不自激”现有行为在新映射下保持；旧 attempt/缺失身份仍只留审计，不移动卡、不重复 launch、不写 verdict/acceptance。
- mirror 断言：运行既有 mirror 回归并检查 `mirrorSkip` 仍只跳 progress、approver_decision、approver_disabled；本卡的消费过滤不能让审计事件从 card ledger 消失。

**T4：操作文档与三链穿缝**

- 若协调者在 P2 选择方案 A，运行 `rg -n 'card wait .*--follow|一次一挂|一次工作流只挂一次|delivery_failed|permission_auto_allow|decision_answered' skills/handoff/SKILL.md README.md`，输出必须能同时定位：Claude Code/grok 使用单条 `card wait --follow`，opencode/Codex 使用默认一次一挂；过滤的审计事件仍可在历史中对质；`delivery_failed` 的动作是 `resume`。若选择方案 B，只对 `skills/handoff/SKILL.md` 执行同等断言，README 不得在本稿中宣称已同步。
- 运行 `go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353|WaitDeliveryPolicy|Follow|CardWait|Automation)' -count=1`，命令退出 0；这条组合命令必须同时覆盖 task policy、card stdout/exit、Store.Follow 调用方、wakeconsumer 调用方的正反行为。
- 运行 `go test -race ./internal/client ./internal/ledger ./internal/agentd ./cmd -run 'Test(B353)' -count=1`，命令退出 0；测试至少覆盖同一 card 批次合并、单一 follow 回调和过滤后仍前进 seq 的并发安全。
- 运行 `git diff --check`，命令退出 0；运行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b353-breakdown.md`，命令退出 0 且所有 file#Symbol 锚为 `ok` 或 `moved`。未运行前不得把上述命令写成通过。

**缺陷族对抗审查（本子卡验收栏）**

1. **生命周期 / 状态机中断：**
   - `card wait` 不创建持久进程、任务或工单；ctx 取消、默认模式首个动作返回、follow 终态返回都必须让 `Store.Follow` 停止，不能留下本地 goroutine 或 PG LISTEN 订阅。`Store.Follow` 的 PG 实际通知连接、SQLite/PG 锁和宿主重启后的收尾**未验证，需真机**。
   - task wait/follow 的既有 WS 重连、游标、归档回收与 executor 资源责任不改；任务 wait 进程重启后的游标/事件间隙、目标 agentd 重启、真实 executor 进程回收**未验证，需真机**。两次默认 wait 之间的订阅真空是 spec 明确 Out of Scope，已由 `docs/roadmap.md` 既有条目承接，不在本卡偷加常驻订阅者。
   - wakeconsumer 仍由既有 agentd 生命周期、automation cursor/seen、Keystone attach/重建/升级负责；新映射不得另起 goroutine 或进程。agentd 重启后对真实卡事件的恢复、孤儿 coordinator session 和 executor 清扫**未验证，需真机**。

2. **静默失败 / 误导报错：**
   - `WaitDeliveryPolicy` 为 false 的事件是“合法审计并过滤”，不是读取失败；必须仍可由 show/全量流对质。未知/损坏 `task_mirrored` envelope、room payload 解码错误、ledger 读取错误、WS 读取错误、`json.Encoder.Encode` 错误、timeout 都必须向调用方返回非零/可行动的 seq/card/task/type 原因，不能打印一行成功 JSON 后吞错。
   - `delivery_failed` 不得被当作无事件；它的可观察结果是唤醒协调者走 `resume` 处置。wakeconsumer 映射错误不得被记成“已唤醒”；同卡失败升级仍遵循现有 cursor/needs_human 语义，避免错误自激。
   - **无“报成功但没做”的新增窗口，因为**本卡只在已有消费点读/过滤/唤醒，不新增写入事实；卡 wait 只有 `Encode` 成功后才以默认模式退出，终态退出也先读当前成员状态。

3. **跨平台假设：**
   - `d_cli`/`d_ledger` 的 JSON、seq、状态字符串不依赖 OS 路径；没有新增进程组、权限、HOME 或 webview 假设，**无，因为**本卡不启动 executor、不写临时目录、不改桌面 UI。
   - `d_transport` 仍依赖 HTTP/WS scheme、Bearer、HTTPClient、关闭码、重连退避；`d_gateway` 的真实 agentd、直连/relay、PG LISTEN/SQLite 轮询、不同 OS 的权限/进程组行为**未验证，需真机**，不能由 Linux httptest 外推。
   - `d_execution` 的真实 executor 协议和“事件确实由该 executor 产生”**未验证，需真机**；本卡只验接收契约形状。

4. **假红 / 假绿测试：**
   - T1 必须穿过真实 WS JSON/store→`WaitEvent`/`FollowEvents`，T2 必须穿过 `runCardWait`→`Store.Follow`→真实 stdout encoder，T3 必须穿过真实 ledger event→`consumeAutomationEventsOnce`→fake Keystone/runner；只测 `WaitDeliveryPolicy`、`automationWakeEvent` 或自造事件 struct 均不足。
   - 每个正断言都有反面：七项假集合不输出、不唤醒；comment/system room/status_moved 非终态不输出；终态 status_moved 不把迁移本身当唤醒行；all=true 仍看见审计；同卡多事件只形成一次合并回合。过滤事件刷新 follow idle、编码失败非零、旧 attempt 不消费均须能变红。
   - 负载/并发下，Store.Follow 的 seq 顺序、follow 单回调、同卡 wake 合并和 seen/cursor 不重复必须有 race/重复回放断言；测试锁的是调用方可观察行为，不锁某个私有 switch 的写法。真实 executor/relay/PG 行为**未验证，需真机**。

5. **门禁绕过：**
   - **无新增写/执行入口，因为**card wait 是账本只读、任务策略是纯谓词、wakeconsumer 只消费已有账本并调用既有 `keystone.Decide`/`wakeCoordinatorRound`；不得新增直接写 ticket、直接执行 resume、旁路写卡或第二事件总线。
   - 既有 `mirrorSkip`、当前 workflow attempt 闸、attach 暂缓、automation seen/cursor、Keystone `Decide` 和 failure escalation 仍是同一套门；所有 task_mirrored 事件先过身份闸再过唤醒策略，不能以新 task type 绕过当前 attempt。
   - 检查与动作之间的真实并发窗口（agentd 重启、attach 变更、目标 task 状态变化）**未验证，需真机**；机内测试必须至少锁住重复回放、attach 暂缓和失败升级反例。

6. **序列化边界：**
   - 本卡没有新增字段，但存在必须逐处核对的既有手写链路：source task Event JSON → `proto.Event` → `WaitDeliveryPolicy`；task mirror envelope JSON 的 `task_type`/`payload` → `mirroredTaskTypeAndPayload`/wakeconsumer；`proto.RoomMessage` JSON → card wait/wakeconsumer；`ledger.Event` → `json.Encoder` stdout；README/skill 的事件名和命令文本。
   - 每条链路至少一支穿过真实边界的回归：task 缺失/空串/非空 `task_type`、envelope 缺失/JSON null payload、RoomMessage `BySystem` 缺失与 false、stdout 原始 payload 均要区分字段缺失、空值与 JSON null；不能用“两端各自单测”替代。
   - `Node`/`Attempt` 等既有 envelope 身份仍按当前 attempt 闸的可空指针语义处理；本卡不把空值猜成当前身份。

7. **枚举新值过既有白名单：**
   - **无新增事件枚举，因为**`--follow` 是 Cobra bool flag，`decision_opened`/`decision_answered`/`needs_*` 是已存在的 ledger event 字面值；本卡不改 proto EventType、ledger event 词表或 mirror event kind。
   - 仍须逐处核对既有七项 `WaitDeliveryPolicy` 假集合、`mirrorSkip` 三项、wakeconsumer 的 task type switch、卡原生 event switch、Keystone WakeKind switch；入口/中间/消费者三处必须同值，未知或未列卡事件不得被错误伪装成新成功类型。

8. **承重安全属性有测试锁住：**
   - 任务过滤“同一事件只交付一次”的 cursor/seq 属性、卡 follow“同一 seq 不重复输出”的属性、wakeconsumer“同一 seq/同一 card 不重复 launch”的 seen/cursor/合并属性，均必须有可变红测试；不能只因当前 map/循环恰好如此。
   - 当前 attempt 隔离、attach 暂缓不推进 cursor、失败升级标记自生 needs_human 不自激，均属于承重安全属性，保留既有反例并覆盖新 task type。
   - **一次性 token/凭据隔离不命中，因为**本卡不新增 token、ticket、HOME、session 或权限凭据；外部 executor/Keystone session 的隔离仍由既有契约与真机清单负责。

9. **webview / 平台表现差异候选族：**
   - **无新增 webview 风险，因为**本卡不触及 `d_web`、Wails、Chromium、cookie、剪贴板、拖放或浏览器 API；CLI stdout 是文本协议，不用浏览器 fixture 冒充。
   - 终端/监控宿主对 stdout 逐行唤醒、管道缓冲、不同 shell 的真实表现仍属外部行为；在 Claude Code/grok 的真实 `card wait --follow` 与协调者现场确认，结果**未验证，需真机**，不以 `go test` 宣称已验证。

#### ④入口指针与有界文件集

有界文件集（允许扩展现有测试，不得扩成目录级改动）：

- `cmd/card_wait.go`
- `cmd/wait.go`
- `cmd/card_wait_test.go`
- `internal/client/delivery.go`
- `internal/client/client.go`
- `internal/client/backlog.go`
- `internal/client/follow_test.go`
- `internal/client/execution_test.go`
- `internal/client/client_test.go`
- `internal/ledger/follow.go`
- `internal/ledger/types.go`
- `internal/ledger/follow_test.go`
- `internal/ledgermirror/mirror.go`
- `internal/agentd/wakeconsumer.go`
- `internal/agentd/wakeconsumer_test.go`
- `internal/keystone/keystone.go`
- `internal/keystone/keystone_spec_test.go`
- `internal/keystone/slice_test.go`
- `internal/proto/proto.go`
- `internal/proto/rooms.go`
- `internal/proto/proto_test.go`
- `internal/proto/rooms_fixture_test.go`
- `skills/handoff/SKILL.md`（P2 选择方案 A 时）
- `README.md`（P2 选择方案 A 时）

入口符号与现状锚点：

- `cmd/card_wait.go#cardWaitCmd`、`cmd/card_wait.go#runCardWait`
- `cmd/wait.go#waitCmd`、`cmd/wait.go#runFollow`
- `internal/client/delivery.go#WaitDeliveryPolicy`
- `internal/client/client.go#Client.WaitEvent`、`internal/client/client.go#Client.FollowEvents`
- `internal/client/client.go#isDeliverable`
- `internal/ledger/follow.go#Store.Follow`
- `internal/agentd/wakeconsumer.go#automationWakeEvent`
- `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`
- `internal/keystone/keystone.go#Service.Decide`
- `internal/proto/proto.go#EventType`
- `internal/proto/rooms.go#RoomMessage`

## 4. 跨子系统行为闭环核对

只核 spec 承诺的产品行为；每行五格，归属子卡确实存在。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
|---|---|---|---|---|
| source task 产生 `delivery_failed`、`stalled`、`approval_dropped`、`archived` 或压力告警 | agentd task event store/WS 帧中的既有 `proto.EventType` 与 seq | `Client.WaitEvent`、`Client.FollowEvents`；卡侧 task_mirrored 另由 wakeconsumer 解包后复用策略 | 默认任务 wait/follow 交付一行事件；小队自动化得到既有 WakeTaskTerminal 入口，其中 `delivery_failed` 可驱动协调者 `resume`；卡侧 stdout/Keystone 不再漏掉策略外可动作事件 | B353-impl/T1、T3 |
| source task 产生纯审计类型或审批链自动裁决 | task event store/WS JSON，或 card ledger 中保留的 `task_mirrored` envelope | `WaitDeliveryPolicy` 消费点、`card wait` 消费点、wakeconsumer | 不打 stdout、不产生 Keystone wake；仍可由全量流/账本 show 对质；字段与 seq 未被删除 | B353-impl/T1、T2、T3 |
| 卡上出现 `needs_human`、`needs_cleared`、`decision_opened` 或 `decision_answered` | `ledger.Event` 的既有 event type、card id、seq | `runCardWait` 与 `automationWakeEvent` | card wait 输出一行原始事件（除终态 status_moved 特例）；Keystone 得到 WakeTaskTerminal，协调者被叫醒处理 | B353-impl/T2、T3 |
| 卡房间收到真人用户消息 | `ledger.EvRoomMessage` 的 `proto.RoomMessage{Kind:user, BySystem:false}` JSON | card wait 与 wakeconsumer | stdout 输出原始 ledger event；Keystone 得到 WakeMessage；系统 pointer/BySystem 用户形状不唤醒 | B353-impl/T2、T3 |
| card wait 收到非终态或终态 `status_moved` | ledger card status 是成员集合的权威状态，事件只作检查触发 | `runCardWait` 的 `checkDone` | 非终态迁移不输出且继续等待；所有当前成员为 Done/Closed 时退出码 0，status_moved 自身不占 stdout 唤醒行 | B353-impl/T2 |
| 协调者选择默认 card wait 或 `--follow` | Cobra flag + `Store.MaxSeq` 起点 + `Store.Follow` seq 流 | `cardWaitCmd` / `runCardWait` | 默认第一条可动作成功编码后退出 0；`--follow` 连续输出并到全员终态收尾；正 timeout 分别按总时长/空闲上限执行，超时为 124 | B353-impl/T2 |
| wakeconsumer 批次包含同卡多个可动作事件、attach 或唤醒失败 | ledger seq、automationSeen/cursor、Keystone.Decide 与既有 RoundResult | `Server.consumeAutomationEventsOnce` | 同卡事件合并为一次协调者回合；attach 暂缓不推进应留事件；失败按既有 escalation 留痕并避免自激重复 | B353-impl/T3 |

触发条件、载体、消费者和结果均已在 spec/contract 冻结；没有新增字段或接缝。真实 source task 是否由 executor 发出指定事件、跨机/重启/relay 是否保留 seq，见 §5，不在机内结论中冒充已验证。

## 5. 未验证，需真机：协调者执行清单

以下项目依赖行为事实，不可由 fake、grep、SQLite 单测或本稿推测替代：

1. 在真实 agentd/真实 executor 上产生 question、permission_request、`delivery_failed`、stalled、completed、turn_failed、failed、archived 与压力告警，确认 task wait、card mirror、wakeconsumer 三条链实际收到的是正确 source task/seq，而不是夹具补写。
2. 真实回复送达失败后，`delivery_failed` 确实唤醒协调者，协调者按既有 `resume` 恢复；`reply`/resume 期间工单、executor waiting_answer 与进程状态不被错误消耗或伪成功。
3. 运行真实 `card wait --follow` 与 `handoff wait --follow`，观察 Claude Code/grok Monitor 按一行动作事件醒来，自动审批/comment 不逐行醒来；观察 opencode/Codex 默认一次一挂，并确认两次 wait 间的偶发真空仍是已接受 Out of Scope。
4. 断开/恢复直连与 relay、重启 agentd、重启协调者、重启 executor，确认 WS/PG/SQLite 的 seq 回放、cursor、automation seen/cursor、卡事件保留、无重复 wake，以及进程/临时目录/会话资源由既有责任方回收。
5. 真实 PG LISTEN 通知只作叫醒铃、ticker 兜底；真实 WS 关闭码、单消息大小、重连退避和 idle 跨重连符合 contract 现状；不把本机 SQLite/httptest 外推到 PG/跨机。
6. 在项目支持的 Linux/macOS/Windows 目标上确认 HOME、权限、shell、进程组、网络、stdout pipe/缓冲与终端宿主不改变逐行 JSON、124/0 退出和 Keystone 唤醒事实；没有 webview 新接口，但宿主 Monitor 的逐行通知仍需现场确认。
7. 并发产生同卡 task_mirrored、decision、room message、needs 事件并切换 attach，确认真实 `consumeAutomationEventsOnce` 不重复 launch、不跳过当前 attempt、不把自生 needs_human 变成下一次自激唤醒。

## 6. 图覆盖债与交棒

- `codegraph/best.json` 顶层 parent 为空的领域及类型已在 §1 列明；基线视图 `domains` 的子域 id（如 `d_coordination_cli`、`d_transport_channel`）只用于解释已查询到的符号归属，不替换 best 顶层清单。
- `WaitDeliveryPolicy` 未单独入图；以 `internal/client/delivery.go#WaitDeliveryPolicy` 源码为权威，不能因为 `codegraph sym` 无独立节点就另造策略。
- 本卡不新增 Ticket 0 符号；不生成 `codegraph/diffs/<分支>.json`。若实现新增生产符号或改变领域边，须在实现/图对账节点按项目流程处理，不在本 breakdown 偷加。
- contract §9 已回写本轮边界澄清；本稿与 contract、spec、台账须同批入库。协调者拍板后需将本文首部状态改为“已拍板（日期）”，逐项把 P1–P3 的裁决与理由回写到 §0，再提交后扇出。

## 7. 出稿自检

- [x] 触及子系统均引用 `best.json` 顶层 parent 为空的 id，并标明 logic/boundary；`d_gateway`/`d_execution` 的边界依赖没有被伪装成实现域。
- [x] 上游 spec“已批准”、contract“已冻结”均从文件头核对；契约冻结项按组逐条给出不越界结论；本轮边界澄清已回写 contract §9。
- [x] 待拍板岔口 P1–P3 集中在稿首；正文没有把候选方案自批成事实。
- [x] 子卡提案具备契约引用、意图与为什么、行为化验收、入口指针/有界文件集；若外部拆卡，T4 仍是接缝闸。
- [x] 行为闭环逐行具备触发者、权威事实/载体、消费者、可观察结果、归属子卡五格。
- [x] 真机清单逐项使用“未验证，需真机”；没有把 fake/grep/机内测试写成真实 executor、网络、重启或宿主行为结论。
- [x] 通用五族、序列化边界、枚举白名单、承重安全属性、webview 候选族均逐项回答；无风险处说明“无，因为……”。
- [x] `codegraph resolve --repo . --doc docs/superpowers/specs/b353-breakdown.md` 与 `git diff --check` 已实际运行，结果与命令记录已落入台账；提交事实按收口纪律在首次提交后追加，再只 amend 一次。
