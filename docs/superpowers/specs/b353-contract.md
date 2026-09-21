# B353–B355 唤醒面契约增量

状态：已冻结（本提交冻结）  
卡：B353（B354、B355 并入）  
基线：`cards/B233.1-charter-7` @ `48f2736a`  
上游：[`b353.md`](b353.md)（头部状态：已批准）  
档位：L3 轻档；本节点不引入 Ticket 0 符号，不生成分支视图 diff。

## 1. 冻结边界

本卡冻结的是「事件是否唤醒协调者」这一消费契约，不改变账本事件的落库、传输或镜像可见性。

- 唯一任务事件策略是 `WaitDeliveryPolicy`；假集合外一律可动作。
- 卡原生事件只在 `card wait` / Keystone 消费点过滤；`mirrorSkip` 不扩面。
- 不新增命令、HTTP 字段、事件类型或常驻订阅进程；`card wait --follow` 是既有子命令的新 flag。
- `card wait` 的 stdout 仍是逐行 JSON `ledger.Event`；过滤掉的审计事件仍可由账本 `show` 对质。

## 2. 现状签名与冻结后的精确接缝

下表先列现状代码事实，再列实现必须满足的签名/调用形状。现状签名均已对照代码或图查询；冻结后的变化只涉及 `runCardWait` 的内部参数和既有消费谓词的调用，不创建新的对外接口。

| 接缝 | 现状代码事实 | 冻结后的精确形状 |
|---|---|---|
| CLI 卡等待 | [`cmd/card_wait.go#runCardWait`](../../../cmd/card_wait.go#runCardWait)，`func runCardWait(cmd *cobra.Command, cardID string, subtree bool, timeout time.Duration) error`；`cardWaitCmd.RunE` 在 `cmd/card_wait.go:32-41` 校验负 timeout 后调用它 | `func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error`。`cardWaitCmd` 增加 `--follow` 布尔 flag，并把该值传入；`--subtree` 保持原义 |
| 卡账本读侧 | [`internal/ledger/follow.go#Store.Follow`](../../../internal/ledger/follow.go#Store.Follow)，`func (s *Store) Follow(ctx context.Context, members func() ([]string, error), fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error` | 签名不变。`runCardWait` 继续以 `fromSeq` 排他、按事件 seq 消费；过滤和退出判定在回调消费点，不在账本存储层 |
| 卡起点与成员 | [`internal/ledger/cards.go#Store.GetCard`](../../../internal/ledger/cards.go#Store.GetCard)、`internal/ledger/events.go#Store.Subtree`、[`internal/ledger/mirror.go#Store.MaxSeq`](../../../internal/ledger/mirror.go#Store.MaxSeq) | 签名与含义不变；单卡成员为 `[cardID]`，`--subtree` 成员仍每轮动态重算；起点仍取当前最大 seq |
| 任务一次等待 | `internal/client/client.go:1468` 的 `func (c *Client) WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error)` | `all=false` 只返回第一个 `WaitDeliveryPolicy(type)==true` 的任务事件；`all=true` 保持全量行为；游标、重连和返回类型不变 |
| 任务持续等待 | [`internal/client/client.go#Client.FollowEvents`](../../../internal/client/client.go#Client.FollowEvents)，`func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error` | 签名不变；`all=false` 的 `onEvent` 只收到策略为真的事件，`all=true` 绕过策略；`idle` 仍按收到任意 WS 帧计时，包含被过滤帧 |
| 任务策略 | `internal/client/delivery.go:9-40` 的 `func WaitDeliveryPolicy(t proto.EventType) bool`；`internal/client/client.go:123-126` 的 `isDeliverable` 是包内别名 | 签名不变，三条消费链共用这一函数；不得在 CLI、wakeconsumer 或镜像包复制任务事件白名单 |
| 自动化映射 | [`internal/agentd/wakeconsumer.go#automationWakeEvent`](../../../internal/agentd/wakeconsumer.go#automationWakeEvent)，`func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)` | 签名不变；`task_mirrored` 解包 `task_type` 后先调用 `WaitDeliveryPolicy(proto.EventType(taskType))`，再决定是否生成 `WakeEvent` |
| 自动化消费 | [`internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`](../../../internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce)，`func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)` | 签名不变；批量、同卡合并、seen/cursor、attach 让步与失败升级语义保持现状，仅替换事件映射结果 |

图查询记录：`WaitDeliveryPolicy` 当前未被代码图单独扫描，生产事实以 `internal/client/delivery.go:23-40` 为准；这是图覆盖债，不是第二个策略源。

## 3. 原子冻结清单

每条是独立可判 pass/fail 的接缝断言。

### 3.1 任务事件策略

1. `WaitDeliveryPolicy(proto.EventTypeProgress)` 返回 `false`。
2. `WaitDeliveryPolicy(proto.EventTypeApproverDecision)` 返回 `false`。
3. `WaitDeliveryPolicy(proto.EventTypeApproverDisabled)` 返回 `false`。
4. `WaitDeliveryPolicy(proto.EventTypeTicketsVoided)` 返回 `false`。
5. `WaitDeliveryPolicy(proto.EventTypeTicketAnswered)` 返回 `false`。
6. `WaitDeliveryPolicy(proto.EventTypePermissionAutoAllow)` 返回 `false`。
7. `WaitDeliveryPolicy(proto.EventTypePermissionReuse)` 返回 `false`。
8. 对任意不在上述七项中的现有 `proto.EventType`，`WaitDeliveryPolicy` 返回 `true`；至少包括 `delivery_failed`、`stalled`、`approval_dropped`、`archived` 与压力告警类型。
9. `WaitEvent(..., all=false)` 不把策略为 `false` 的事件交给调用方。
10. `FollowEvents(..., all=false)` 不把策略为 `false` 的事件交给 `onEvent`。
11. `FollowEvents(..., all=true)` 仍把审计事件交给 `onEvent`，不使用 `WaitDeliveryPolicy` 过滤。
12. 任务事件的过滤发生在应用消费点；WS/HTTP 流仍可读到策略为 `false` 的存储事件。

任务常量出处：`internal/proto/proto.go:42-126`；策略现状实现：`internal/client/delivery.go:23-40`。

### 3.2 `handoff card wait` stdout 与退出

13. `card wait` 新增 `--follow`，不新增第三条 wait 命令。
14. `card wait` 对 `needs_human` 事件编码一行原始 `ledger.Event` 到 stdout。
15. `card wait` 对 `needs_cleared` 事件编码一行原始 `ledger.Event` 到 stdout。
16. `card wait` 对 `decision_opened` 事件编码一行原始 `ledger.Event` 到 stdout。
17. `card wait` 对 `decision_answered` 事件编码一行原始 `ledger.Event` 到 stdout。
18. `card wait` 对 payload 为 `proto.RoomMessage` 且 `Kind == proto.RoomMsgUser`、`BySystem == false` 的 `room_message` 编码一行原始 `ledger.Event` 到 stdout。
19. `card wait` 对 `task_mirrored` 先解包 `task_type`，且仅在 `WaitDeliveryPolicy(proto.EventType(task_type)) == true` 时编码一行原始 `ledger.Event` 到 stdout。
20. `card wait` 不为策略为 `false` 的 `task_mirrored` 编码 stdout 行。
21. `card wait` 不为 `comment` 事件编码 stdout 行。
22. `card wait` 不为 `dispatched` 事件编码 stdout 行。
23. `card wait` 不为 `acceptance_recorded` 事件编码 stdout 行。
24. `card wait` 不为 `EvRoomMessage` 中 `BySystem == true` 的用户形状消息编码 stdout 行。
25. `status_moved` 非终态迁移不编码 stdout 行。
26. `status_moved` 迁移后所有当前成员均为 `ledger.StatusDone` 或 `ledger.StatusClosed` 时，命令以退出码 0 结束且不为该事件编码 stdout 行。
27. 非上述可动作事件仍留在账本中，不因 `card wait` 过滤而从审计流删除。
28. 默认模式（无 `--follow`）在第一条可动作事件编码成功后以退出码 0 结束。
29. 默认模式收到仅审计事件时继续等待，不因该事件退出。
30. `--follow` 模式可连续编码多条可动作事件，并在所有当前成员进入 `StatusDone`/`StatusClosed` 后以退出码 0 结束。
31. 默认模式的正 `--timeout` 是等待可动作事件（或终态收尾）的总时长。
32. `--follow` 模式的正 `--timeout` 是无新账本事件的空闲上限；任意收到的账本事件都刷新空闲计时，包括被过滤事件。
33. `--timeout` 到期仍使用既有 `ExitTimeout`（124）错误出口。
34. `--subtree` 的动态成员重算和 `Store.Follow` 的 2 秒生产轮询保持不变。

卡事件常量出处：`internal/ledger/types.go:46-78`；房间载荷出处：`internal/proto/rooms.go:11-35`；终态常量出处：`internal/ledger/types.go:12-20`。

### 3.3 自动化 wakeconsumer

35. `task_mirrored` 的 `task_type` 被策略判为 `false` 时，`automationWakeEvent` 返回 `yes=false` 且不产生 Keystone 唤醒。
36. `task_mirrored` 的 `task_type` 为 `permission_request` 且策略为真时，映射为 `keystone.WakeTicket`。
37. `task_mirrored` 的 `task_type` 为 `question` 且策略为真时，映射为 `keystone.WakeTicket`。
38. `task_mirrored` 的其它策略为真的任务类型映射为 `keystone.WakeTaskTerminal`，至少覆盖 `delivery_failed`、`stalled`、`approval_dropped` 与 `archived`，从而 `delivery_failed` 会唤醒协调者执行 `resume`。
39. `needs_human` 产生 `keystone.WakeTaskTerminal`。
40. `needs_cleared` 产生 `keystone.WakeTaskTerminal`。
41. `decision_opened` 产生 `keystone.WakeTaskTerminal`。
42. `decision_answered` 产生 `keystone.WakeTaskTerminal`。
43. 只有 `RoomMessage{Kind: proto.RoomMsgUser, BySystem: false}` 的 `room_message` 产生 `keystone.WakeMessage`。
44. `RoomMessage{BySystem: true}` 不产生 Keystone 唤醒。
45. `status_moved` 仍只用于终态协调者窗口收尾，不产生 Keystone 唤醒。
46. `comment`、`dispatched`、`acceptance_recorded` 与其它未列卡事件不产生 Keystone 唤醒。
47. `mirrorSkip` 仍只过滤 `progress`、`approver_decision`、`approver_disabled`；本卡不把消费过滤扩展成镜像过滤。
48. 自动化消费者仍按卡合并待唤醒事件，并沿用当前 attempt 闸、attach 暂缓、seen/cursor 推进和失败升级行为。

## 4. 现状依赖行为（契约的一部分）

这些是实现依赖的既成行为，不能用凭印象的默认值替换：

- `Store.Follow` 在 `internal/ledger/follow.go:20-21` 将 `pollInterval <= 0` 归一为 `2*time.Second`；`internal/ledger/follow.go:55` 每次读取上限为 500，`fromSeq` 排他且由 `EventsFromAsc` 升序取数。
- PG 路径在 `internal/ledger/follow.go:24-45` 用独立 `pgx.Connect(ctx, dsn)` 建连、执行 `LISTEN card_events`，`WaitForNotification(ctx)` 只作唤醒铃且通知内容不解析；`internal/ledger/follow.go:65-70` 的 ticker 始终作为兜底。`pgx/v5@v5.10.0/conn.go:413-430` 规定已缓冲通知先返回，等待受 ctx 约束。
- `coder/websocket@v1.8.15/read.go:20-49` 的 `Conn.Read(ctx)` 读取一个完整消息并受 ctx 限制；该库 `read.go:60-61` 的读路径负责处理 ping/pong/close；默认单消息读限为 `32768` 字节（`read.go:88-107`），本卡不提高此上限。
- `internal/client/client.go:49-54` 的 WS 单次拨号/握手超时为 `10s`；`internal/client/client.go:42-46,1497-1519` 的重连退避为 `1s` 起、`60s` 封顶，连接存活至少 `5s` 后复位；`FollowEvents` 的 `idle` 计时跨重连累计。
- WS 拨号继续使用 `internal/client/client.go#Client.wsDialOptions` 的本 Client `HTTPClient` 与 Bearer 头；不退回 `http.DefaultClient`，不引入代理路径。

## 5. 依赖方向、组装点与预算

- `cmd/card_wait.go#runCardWait` → `ledger.Store`：复用既有 `d_cli → d_ledger` 契约，不新增账本接口或第二事件总线。
- `cmd/wait.go#runFollow` → `client.Client.FollowEvents`：复用既有 `d_cli → d_transport` 契约，策略只从 `client` 消费，不在 CLI 复制。
- `agentd` 自动化消费 → `client.WaitDeliveryPolicy`：复用既有编排到传输客户端的契约面，不新增领域方向或预算；`WaitDeliveryPolicy` 未入图，缺口已在本文「图覆盖债」标注。
- `agentd` 自动化消费 → `ledger.Store` / `keystone.Service`：继续走现有组装与消费点；本卡不把唤醒策略下沉到账本，不让 Keystone 读取传输流。
- 组装点仍是现有 `main.go`、`internal/agentd/server.go`、`internal/agentd/codegraph.go`；本卡无新增组装点。

目标图 `codegraph/target.json` 随本提交补入 B353 冻结说明；既有方向、预算与 `best.json` 结构树不变。

## 6. 三重闸门拍板记录

1. **三处消费共用 `WaitDeliveryPolicy`，卡侧只在解包 `task_mirrored` 后复用它。** 这是跨 CLI、传输客户端、自动化编排的难逆契约；后人看到各自闭集会自然想修掉漂移；被否掉的方案是三处各维护白名单，显式不做「只补 `delivery_failed` 两条」。
2. **过滤留在消费点，不扩 `mirrorSkip`。** 这会同时影响账本镜像、卡 wait 和自动化 wake，且审计保留在 `show` 的行为对后人并不直观；被否掉的方案是镜像时删除审计事件，显式不做「用传输/镜像过滤代替应用过滤」。
3. **`card wait` 默认一次一挂，只有 `--follow` 长挂。** 这是 CLI 退出模型与两个 harness 的难逆用户契约；旧的「一直挂到卡终态」实现会让一次一挂的协调者无法工作；被否掉的方案是保持长挂只过滤行（选项 A），显式不做第三条 wait 命令。
4. **`needs_cleared` 与 `decision_answered` 本期列为可动作。** 这是三条消费链共同的用户可见唤醒行为，过多时后续可收紧；被否掉的方案是只保留 needs/decision opened，显式不做本期提前收紧。

## 7. 移交 plan 附区

以下是查证期确定、但不新增冻结条目的实现级交棒事项：

- 在 `runCardWait` 回调中先判 `status_moved` 的终态退出，再对可动作事件执行 `json.Encoder.Encode`；不能沿用当前「先 Encode、后判断」顺序。
- 卡侧 task envelope 解包只取 `task_type` 作为策略输入，输出仍为原始 `ledger.Event`；不得把任务 payload 改写成新的 stdout 形状。
- 复用现有 `allDone` 哨兵、`ExitTimeout` 和 `Store.Follow`，不新造对外命令、常驻 goroutine 或事件总线。
- 实现阶段必须补齐 spec 测试决定中的三条穿缝测试：任务 wait/follow、卡 wait、自动化 consumer；禁止只测一个 helper。

## 8. 法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处。
- 目标图：`codegraph/target.json`，随本提交冻结；本卡无新符号，故无分支视图 diff。
- Ticket 0 编译：轻档不适用（无骨架代码变更）。
- 金样本：本卡无哈希、密钥派生或编码格式新增；原始 `ledger.Event` JSON 形状不变。
- 三重闸门：已记录四项拍板；无其它命中。

## 9. breakdown 核对修订记录

- **2026-09-09（breakdown 核对）**：`cmd/card_wait.go#runCardWait` 读取
  `ledger.Store.Follow` 属于已有 `d_cli → d_ledger` 包内 API 面，不新增账本接口、HTTP
  字段或事件总线；本卡只在 CLI 消费回调过滤并判断退出。
- **2026-09-09（breakdown 核对）**：`task_mirrored` 载荷中的既有 `task_type` 只作为
  `WaitDeliveryPolicy` 的输入，仍输出原始 `ledger.Event`；它不是新增协议字段或事件
  枚举，`mirrorSkip` 不因此扩面。
- **2026-09-09（breakdown 核对）**：`keystone.WakeEvent` 的既有
  `WakeTaskTerminal` / `WakeTicket` / `WakeMessage` 形状承接新增卡原生可动作事件；
  `decision_opened`、`decision_answered`、`needs_*` 复用已有 `WakeTaskTerminal`，不新增
  Keystone 对外入口或持久事实。
- **2026-09-09（breakdown 核对）**：`skills/handoff/SKILL.md` 与 `README.md` 仅是
  已有 CLI 消费契约的操作文档同步面，不构成新的运行时接缝；若实现提案要扩展命令、
  wire 字段或镜像责任，必须退回 contract，而不是在文档变更中隐含扩面。
