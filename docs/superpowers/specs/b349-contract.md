# B349+B352 契约增量：消费端 source identity 闸与自动化水位

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b349.md`，头部状态行已回写）
**级别：L3；档位：轻档**（B352 并入 B349，不另写契约）
**冻结状态：本提交随 `codegraph/target.json` 与 Ticket 0 骨架/直通镜像冻结**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（沿用 `codegraph/best.json`）
**基线：** `cards/B233.1-charter-7`；**实现入口：** `docs/superpowers/specs/b349-contract.md`

本契约只冻结接缝断言与跨子系统决定。Ticket 0 只落 `proto.LedgerEvent` 的空壳字段、两处投影直通和能变红的投影测试；身份闸与自动化 cursor 的可观测行为交给 breakdown/implement。B233.6 plan §5.2 的 L493/L503（禁止改三处投影、禁止 source 字段、把 source identity 退给 store 去重）在本卡范围内废止；不退回 `b233.6-contract` §2.4，也不退回拆解 P3“闸在消费者、ledger 保持薄”。

## 1. 现状查证

### 1.1 已查证签名与类型

下表的签名是本轮对现状代码的事实查证；代码出处使用符号锚，括号内行号只是本轮读数。

| 接缝 | 现状代码事实 | 本卡冻结后的精确形状 |
| --- | --- | --- |
| 账本事件实体 | `internal/ledger/types.go#Event`（`:140-150`）已有 `SourceTarget string`、`SourceTask string`、`SourceSeq int64`，三字段均为 `json:"...,omitempty"` | 签名不变；三列仍由账本事件承载，卡原生事件保持零值 |
| Go wire 事件 | `internal/proto/ledger.go#LedgerEvent`（`:119-127`）当前只有 `Seq int64`、`CardID string`、`Type string`、`Actor string`、`Payload json.RawMessage`、`CreatedAt time.Time` | 增加 `SourceTarget string json:"source_target,omitempty"`、`SourceTask string json:"source_task,omitempty"`、`SourceSeq int64 json:"source_seq,omitempty"`；其它字段与顺序不变 |
| 账本薄门面读事件 | `internal/ledger/api/api.go#Facade.EventsFromAsc`（`:81-90`）：`func (f *Facade) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)` | 签名不变；逐事件调用 `eventWire`，三列原样透传 |
| 房间门面投影 | `internal/ledger/api/api.go#eventWire`（`:147-156`）：`func eventWire(ev ledger.Event) proto.LedgerEvent`，当前丢三列 | 签名不变；补齐 `SourceTarget/SourceTask/SourceSeq`，不做业务判断、不改 payload |
| 卡详情 HTTP 投影 | `internal/agentd/ledgerapi.go#Server.handleCardDetail`（`:325-376`）读取 `s.ledger.EventsFromAsc([]string{id}, 0, 500)`，逐事件调用 `ledgerEventWire` | 路径仍为 `GET /api/cards/{id}`；响应 `events[]` 的三列由 `ledgerEventWire` 直通，Web TypeScript 类型不改 |
| HTTP 事件投影 | `internal/agentd/ledgerapi.go#ledgerEventWire`（`:111-116`）：`func ledgerEventWire(event ledger.Event) proto.LedgerEvent`，当前丢三列 | 签名不变；补齐三列，卡原生事件零值因 `omitempty` 不出键 |
| 原始事件读 | `internal/ledger/events.go#Store.EventsFromAsc`（`:63-103`）：`func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error)` | 签名不变；继续按 `seq ASC`、`fromSeq` 排他读数据库的 source 三列 |
| 派发快照 | `internal/ledger/events.go#DispatchSnapshot`（`:115-139`）已有 `Target string`、`TaskID string`、`Node string`、`Attempt string` | 签名不变；当前身份只取最新合格 `EvDispatched` 快照的 `Target+Attempt`，并要求 `TaskID==Attempt` |
| 派发生产 | `internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`（`:365-373`）以一次 Transport 返回的 `taskID` 同时写 `TaskID` 与 `Attempt`，并写有效 `Target` | 签名不变；该事实是身份闸的唯一快照来源，不从 `card_tasks` 或 task type 猜身份 |
| 镜像生产 | `internal/ledger/mirror.go#Store.AppendMirroredEvent`（`:34-100`）的 `func (s *Store) AppendMirroredEvent(cardID string, ev MirroredEvent) (bool, error)` 将 source 三列写入 `card_events`，source 不复制进 envelope JSON | 签名不变；source 三列继续是权威身份；唯一索引仍只负责 `(source_target, source_task, source_seq)` 去重 |
| 镜像消费 | `internal/ledgermirror/mirror.go#Mirror.subscribe`（约 `:517-520`）把 `TaskLink.Target/TaskID` 和源 `e.Seq` 传给 `AppendMirroredEvent` | 签名不变；不扩 `mirrorSkip`，不在镜像层做当前派发判断 |
| 镜像 envelope 解码 | `internal/agentd/wakeconsumer.go#decodeMirroredTaskEnvelope`（`:29-40`）当前签名 `func decodeMirroredTaskEnvelope(ev proto.LedgerEvent) (mirroredTaskEnvelope, error)`；`Node`/`Attempt` 是 `*string` | 签名不变；缺失、`null`、空字符串继续可区分为“不具备 workflow 身份”；source 三列从 wire DTO 读取，不从 envelope 猜 |
| 自动化当前派发 | `internal/agentd/wakeconsumer.go:54-92` 当前签名 `func (s *Server) currentWorkflowAttempt(cardID, node string) (attempt string, found bool, err error)`，只返回最新 `Attempt`；该私有符号尚未被基线图覆盖 | 改为 `func (s *Server) currentWorkflowAttempt(cardID, node string) (snapshot ledger.DispatchSnapshot, found bool, err error)`；仍分页读该卡全事件，返回该 node 最新合格快照 |
| 自动化身份闸 | `internal/agentd/wakeconsumer.go:97-130` 当前签名 `func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error)`；该私有符号尚未被基线图覆盖 | 签名不变；比较 envelope node/attempt、快照 `Attempt/Target/TaskID` 与 wire `SourceTask/SourceTarget` |
| 自动化映射 | `internal/agentd/wakeconsumer.go#automationWakeEvent`（`:133-188`）当前签名 `func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error)` | 签名不变；身份闸在 `consumeAutomationEventsOnce` 调用它之前执行；任务类型仍唯一交给 `client.WaitDeliveryPolicy` |
| 自动化一轮 | `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`（`:211-392`）当前签名 `func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error)` | 签名不变；批量、同卡合并、attach 暂缓、失败升级和 `automationSeen` 语义不改；成功/失败推进 cursor 后持久化 |
| 卡等待分类 | `cmd/card_wait.go#cardWaitEventActionable`（`:195-233`）当前签名 `func cardWaitEventActionable(ev ledger.Event) (bool, error)` | 改为 `func cardWaitEventActionable(st *ledger.Store, ev ledger.Event) (bool, error)`；`task_mirrored` 先走同一身份规则，再走 `WaitDeliveryPolicy` |
| 卡等待主缝 | `cmd/card_wait.go#runCardWait`（`:56-190`）当前签名 `func runCardWait(cmd *cobra.Command, cardID string, subtree, follow bool, timeout time.Duration) error` | 签名不变；回调将 `st` 传入分类器；事件所属 `e.CardID` 是查派发快照的卡号，`--subtree` 不改成根卡 |
| 自动化装配 | `internal/agentd/server.go#Server.SetupAutomation`（`:2554-2602`）当前签名 `func (s *Server) SetupAutomation(st *ledger.Store)` | 签名不变；在此装配并读回独立自动化 cursor，不把读回放到 `StartAutomation` |
| 自动化启动 | `internal/agentd/scheddrain.go#Server.StartAutomation`（`:51-63`）当前签名 `func (s *Server) StartAutomation(ctx context.Context)` | 签名不变；只启动既有循环，不拥有 cursor 文件装配 |

### 1.2 依赖与对侧常量查证

- `ledger.EvDispatched = "dispatched"` 的生产者是 `Store.RecordDispatch`/`RecordDispatchAndLinkTask`，当前身份读取方是 `currentWorkflowAttempt`；该常量活跃，不是死常量。
- `ledger.EvTaskMirrored = "task_mirrored"` 的生产者是 `Store.AppendMirroredEvent`，消费方是 `wakeconsumer` 与 `card wait`；该常量活跃，不改变事件类型。
- `source_target/source_task/source_seq` 的生产者是 `AppendMirroredEvent` 与数据库事件读面；投影消费方是 `Facade.EventsFromAsc`、`handleCardDetail`、`wakeconsumer` 和 `card wait`。`source_seq` 的真实消费者还包括 source 三元组唯一索引/镜像 watermark；它不属于当前派发身份比较。
- `client.WaitDeliveryPolicy`（`internal/client/delivery.go#WaitDeliveryPolicy`）仍是任务类型可动作性的唯一事实源。`task_mirrored` 的 source identity 闸通过后才调用它；不在 ledger、mirror 或 CLI 复制任务类型集合。
- `TaskLink.Target` 允许空串：`internal/ledgermirror/mirror_test.go:546-548` 实际断言本机镜像的 `SourceTarget==""`；`DispatchSnapshot.Target` 也允许空串：`internal/ledgerstep/runner_test.go:733-739` 实际断言本机 Transport 的 target 为空。因此两边同时为空必须匹配，一边为空一边非空必须拒绝。

依赖库既成行为也是本契约的一部分：

| 行为 | 依赖源码出处 | 冻结影响 |
| --- | --- | --- |
| `encoding/json` 的 `omitempty` 对空字符串、整数 `0` 省略 | `/usr/local/go/src/encoding/json/encode.go:107-110` 的 tag 说明；`:352-362` 的 `isEmptyValue` | `LedgerEvent` 零值 source 三列不改变既有 JSON 金样本；非零 source 三列按 snake_case 出键 |
| `encoding/json` 按字段 tag 编码 struct 字段 | `/usr/local/go/src/encoding/json/encode.go:1176-1182` | 不把 source 三列放进 envelope `Payload`；wire DTO 的三个 tag 是唯一 JSON 键定义 |
| `os.WriteFile` 会先截断且多系统调用中途失败可能留下半文件 | `/usr/local/go/src/os/file.go:928-946` | cursor 先写 `<path>.tmp`，不得直接写主文件 |
| `os.Rename` 同目录替换与平台原子性受实现限制 | `/usr/local/go/src/os/file.go:433-438` | 沿房间 cursor 的 tmp+rename 形态；实现记录平台限制，不把“跨平台绝对原子”写成事实 |
| 账本 asc 读以 `fromSeq` 排他、升序、每页上限 500 | `internal/ledger/events.go#Store.EventsFromAsc`（`:60-103`） | 两处当前派发扫描必须分页到尾，不因 500 截断而误判“无当前快照” |

## 2. 精确契约形状

### 2.1 wire DTO 与两处直通镜像

`internal/proto/ledger.go#LedgerEvent` 冻结为：

```go
type LedgerEvent struct {
	Seq          int64           `json:"seq"`
	CardID       string          `json:"card_id"`
	Type         string          `json:"type"`
	Actor        string          `json:"actor"`
	Payload      json.RawMessage `json:"payload"`
	SourceTarget string          `json:"source_target,omitempty"`
	SourceTask   string          `json:"source_task,omitempty"`
	SourceSeq    int64           `json:"source_seq,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}
```

`eventWire` 与 `ledgerEventWire` 都必须逐字段执行下列直通：`Event.SourceTarget → LedgerEvent.SourceTarget`、`Event.SourceTask → LedgerEvent.SourceTask`、`Event.SourceSeq → LedgerEvent.SourceSeq`；不得改写、校验、从 payload 补值或把 source 再写入 envelope。`Facade.EventsFromAsc` 和 `GET /api/cards/{id}` 是测试必须穿过的真实接缝。

### 2.2 自动化身份闸

`currentWorkflowAttempt` 改名不变、返回完整快照：

```go
func (s *Server) currentWorkflowAttempt(
	cardID, node string,
) (snapshot ledger.DispatchSnapshot, found bool, err error)
```

它从 `s.autoLedger.EventsFromAsc([]string{cardID}, from, 500)` 开始，按 `Seq` 升序分页直到尾部；只接受 `EvDispatched` 且 `snapshot.Node == node`、`snapshot.Node != ""`、`snapshot.Attempt != ""` 的快照，最大事件 seq 的一条覆盖旧值。`found=false` 表示没有合格快照。`snapshot.TaskID != snapshot.Attempt` 时不得作为当前身份；该快照只能留在账本审计流。

`acceptsCurrentWorkflowAttempt` 的 `true` 条件是以下每一项同时成立：

1. `task_mirrored` envelope 的 `node` 与 `attempt` 均存在且非空。
2. 存在该事件所属卡、该 node 的当前 `EvDispatched` 快照。
3. envelope `attempt == snapshot.Attempt`。
4. `snapshot.TaskID == snapshot.Attempt`。
5. `ev.SourceTask == snapshot.Attempt`。
6. `ev.SourceTarget == snapshot.Target`；两个值都为空时相等，一空一非空不相等。

`SourceSeq` 是源事件序号，不参与当前派发身份匹配；它与源序号不一致时，只要上面六项成立仍可按身份通过。缺 `source_task` 的镜像自然无法满足第 5 项，返回 `false,nil`、记日志、标记 seen，消费循环继续。没有当前快照、旧 attempt、envelope 缺 node/attempt 或 source target 不匹配的事件留在账本，不进入 `automationWakeEvent`。

### 2.3 `card wait` 同闸

新增私有扫描函数的精确签名：

```go
func cardWaitCurrentWorkflowAttempt(
	st *ledger.Store, cardID, node string,
) (snapshot ledger.DispatchSnapshot, found bool, err error)
```

它与 `Server.currentWorkflowAttempt` 使用同样的 `EvDispatched`、分页、最新 seq、`Node/Attempt/TaskID` 约束；实现可以在 `cmd` 内独立读取 `ledger.Store`，不把“当前 attempt”派生查询下沉到账本 API。`cardWaitEventActionable(st, ev)` 对 `task_mirrored` 先用 `ev.CardID` 和 envelope node 取快照，再逐项比较 `Attempt/Target/SourceTask/SourceTarget`，最后才调用 `client.WaitDeliveryPolicy`。卡原生 `needs_*`、decision、真人 room message 不走 source identity 闸；`--subtree` 的根卡只决定成员集合，不改变事件所属卡。

### 2.4 自动化 cursor

本机文件名冻结为 `<DataDir>/automation-cursor.json`，不复用 `$HOME/.handoff/cursors`、`room-cursors.json` 或共享账本。实现落点为 `internal/agentd/automation_cursor.go`，私有形状冻结为：

```go
type automationCursorDisk struct {
	Seq int64 `json:"seq"`
}

type automationCursorStore struct {
	path string
}

func newAutomationCursorStore(path string) *automationCursorStore
func (s *automationCursorStore) Load() (int64, error)
func (s *automationCursorStore) Save(seq int64) error
func (s *Server) advanceAutomationCursor(seq int64) error
```

`Load` 在文件不存在时返回 `0,nil`；其它读取/JSON 错误返回错误，装配点记录错误并以 0 启动，不把错误吞成“已有水位”。`Save` 将 `automationCursorDisk{Seq: seq}` 编成 JSON，确保父目录存在，写 `<DataDir>/automation-cursor.json.tmp` 后同目录 rename 到主文件，文件权限沿既有 cursor 介质为 `0600`，目录为 `0700`。

`SetupAutomation` 在完成 `autoLedger` 装配时创建该 store 并 Load；`StartAutomation` 不读文件。`advanceAutomationCursor` 必须先在现有 `automationMu` 保护下把内存 `s.automationCursor` 更新为更大值，再调用 `Save`；保存错误返回当前消费轮错误。`consumeAutomationEventsOnce` 在现状所有“把 `maxProcessed` 写入内存 cursor”的成功/唤醒失败出口调用它；attach 暂缓、读错、解码错和尚未真正推进 cursor 的出口不写文件。`automationSeen` 仍为进程内 map，不落盘。

## 3. 原子冻结清单

每条是一支可独立判 pass/fail 的断言。

1. `proto.LedgerEvent` 有 `SourceTarget string`。
2. `proto.LedgerEvent` 有 `SourceTask string`。
3. `proto.LedgerEvent` 有 `SourceSeq int64`。
4. `SourceTarget` 的 JSON 键为 `source_target` 且带 `omitempty`。
5. `SourceTask` 的 JSON 键为 `source_task` 且带 `omitempty`。
6. `SourceSeq` 的 JSON 键为 `source_seq` 且带 `omitempty`。
7. `Facade.EventsFromAsc` 镜像事件透传非空 `SourceTarget`。
8. `Facade.EventsFromAsc` 镜像事件透传非空 `SourceTask`。
9. `Facade.EventsFromAsc` 镜像事件透传非零 `SourceSeq`。
10. `GET /api/cards/{id}` 的镜像事件透传非空 `source_target`。
11. `GET /api/cards/{id}` 的镜像事件透传非空 `source_task`。
12. `GET /api/cards/{id}` 的镜像事件透传非零 `source_seq`。
13. `Facade.EventsFromAsc` 的卡原生事件三列保持 `""`、`""`、`0`。
14. `GET /api/cards/{id}` 的卡原生事件三列保持 `""`、`""`、`0`。
15. 零值 `LedgerEvent` JSON 不出现三个 source 键。
16. 镜像 source 三列不复制进 `task_mirrored` envelope JSON。
17. `currentWorkflowAttempt` 只接受有非空 Node 与 Attempt 的 `EvDispatched`。
18. `currentWorkflowAttempt` 选择同 node 最大事件 seq 的合格快照。
19. `currentWorkflowAttempt` 拒绝 `TaskID != Attempt` 的快照。
20. `acceptsCurrentWorkflowAttempt` 要求 envelope node 非空。
21. `acceptsCurrentWorkflowAttempt` 要求 envelope attempt 非空。
22. `acceptsCurrentWorkflowAttempt` 要求 envelope attempt 等于当前快照 Attempt。
23. `acceptsCurrentWorkflowAttempt` 要求 source_task 等于当前快照 Attempt。
24. `acceptsCurrentWorkflowAttempt` 要求 source_target 等于当前快照 Target。
25. 当前快照 Target 与 source_target 同为空时身份闸通过。
26. 当前快照 Target 与 source_target 一空一非空时身份闸拒绝。
27. source_seq 不相等不单独否决其它身份条件成立的事件。
28. 没有当前派发快照的镜像事件不唤醒自动化消费者。
29. 旧 attempt 的镜像事件不唤醒自动化消费者。
30. 缺 source_task 的镜像事件不唤醒且不终止消费循环。
31. `card wait` 使用事件所属卡读取当前派发快照。
32. `card wait --subtree` 不用 wait 根卡替代事件所属卡。
33. `card wait` 在身份闸通过后才调用 `WaitDeliveryPolicy`。
34. 卡原生事件不因缺 source 三列而被身份闸过滤。
35. `EvTaskMirrored` 事件类型字面值保持不变。
36. `EvDispatched` 事件类型字面值保持不变。
37. 本卡不新增 wait 命令。
38. 本卡不新增 HTTP endpoint。
39. 自动化 cursor 文件路径是 `<DataDir>/automation-cursor.json`。
40. 自动化 cursor 在 `SetupAutomation` 读回。
41. 自动化 cursor 不在 `StartAutomation` 读回。
42. 缺少 cursor 文件时起点为 0。
43. 内存 cursor 更新发生在 cursor 文件 Save 之前。
44. attach 暂缓不推进自动化 cursor 文件。
45. `automationSeen` 不写入 cursor 文件。
46. 新 Server 在同一 DataDir 的 SetupAutomation 后读到已保存水位。
47. cursor 之后新落到账本的可动作事件仍可被消费。
48. 崩溃发生在 Save 前允许同一批事件再次唤醒，即至少一次语义。
49. 自动化 cursor 不写共享账本。
50. 自动化 cursor 不复用协调者 wait cursor。

## 4. 依赖方向、组装点与预算

- `d_ledger → d_protocol` 继续使用已有 `proto 实体` 入口；`LedgerEvent` 只是已有 wire DTO 的字段增量，不新增方向或预算。
- `d_cli → d_ledger` 继续使用已有 `ledger.Store` 入口；`card wait` 只在现有消费回调增加快照读取，不新增事件总线或 ledger 派生 API。
- `d_orchestration → d_protocol` 继续使用已有 `proto 实体` 入口；wakeconsumer 已消费 `proto.LedgerEvent`，本卡只补字段读取。
- `d_orchestration → d_transport` 继续使用已有 `client.WaitDeliveryPolicy` 入口；不复制策略集合、不新增预算。
- `Server.SetupAutomation`（`internal/agentd/server.go`）是 cursor 文件的唯一生产组装点；不在 `StartAutomation` 或 `cmd` 另造读回。
- `best.json` 结构树不变。Ticket 0 没有新增接口、类型或事件符号，故不生成空的 `codegraph/diffs/<分支>.json`；本契约引用的 `eventWire`、`currentWorkflowAttempt`、`acceptsCurrentWorkflowAttempt`、`cardWaitEventActionable` 均按源码覆盖债处理，不以缺图代替代码查证。

## 5. 三重闸门拍板记录

只记录同时满足“难逆转、无上下文会惊讶、真取舍”的决定：

1. **source 三列是 wire/账本身份的唯一权威，不复制进 envelope。** 这同时约束 proto、两处投影、mirror 与两个消费点；后人看到 envelope 已有 node/attempt，容易顺手把 target/task 再塞进 payload；被否方案是 envelope 与列双写，代价是两个身份源漂移。明确不做 source JSON 双写。
2. **身份闸同时放在 wakeconsumer 与 card wait，且都从事件所属卡的最新派发快照取身份。** 这会跨自动化编排、CLI、ledger 读面；只在 wakeconsumer 校验会让主会话继续被旧 attempt 叫醒，只在 ledger 派生会把消费决策下沉成第三种规则；明确不做单消费点闸和 ledger 派生查询。
3. **当前身份钉为 `snapshot.Attempt + snapshot.Target`，并强制 `snapshot.TaskID == snapshot.Attempt`；不以 TaskID 替代 Attempt。** 这会影响派发快照生产、镜像列与两个消费点；没有本上下文时后人会把已有 TaskID 当成唯一键；被否方案是只比较 TaskID 或只信 envelope attempt。明确不做单键身份。
4. **自动化 cursor 属于本机 agentd DataDir，且内存 cursor 先更新、再落盘。** 这跨 Server 装配、消费循环和重启恢复；把它写共享 ledger 或先落盘再唤醒都看似更可靠；前者会产生多 agentd 抢同一进度，后者会制造“恰好零次”。明确不做共享账本 cursor、MAX(seq) 启动和先持久化后唤醒。
5. **cursor 文件使用独立 `automation-cursor.json`，不复用 room cursor 或协调者 wait cursor。** 三种 cursor 的 owner 与失败语义不同，后人看到同为 seq 容易合并；被否方案是固定 member/room 键复用 `room-cursors.json` 或 `$HOME/.handoff/cursors`。明确不做跨 owner 复用。

## 6. Ticket 0、可执行冻结与交棒

### 6.1 本提交 Ticket 0

- 已落 `proto.LedgerEvent` 三个 `omitempty` 字段。
- 已落 `internal/ledger/api/api.go#eventWire` 的 source 三列直通。
- 已落 `internal/agentd/ledgerapi.go#ledgerEventWire` 的 source 三列直通。
- 已落 `internal/ledger/api/api_test.go#TestFacadeEventsFromAscPreservesSourceIdentity`：经 `client.LedgerClient`/Facade 验证镜像三列、卡原生零值与 `omitempty` JSON。
- 已落 `internal/agentd/ledgerapi_test.go#TestCardDetailProjectsMirroredSourceIdentity`：经真实 `GET /api/cards/{id}` 验证 HTTP 投影三列与卡原生零值。
- Ticket 0 不实现身份闸、cursor 持久化、card wait 快照扫描或直通竖切；本卡是 L3 轻档，行为由 plan 最薄路径承接。

### 6.2 可执行冻结

- 命中的是 JSON 编码格式，不是哈希、密钥派生或加密算法；金样本/穿缝测试为上述 Facade 与 HTTP 两支，必须本轮实际跑过。
- 无哈希、密钥派生金样本：写明“无命中”，不得用“未审”代替。
- 下游必须补 `acceptsCurrentWorkflowAttempt × consumeAutomationEventsOnce` 的正反例：当前身份、旧 attempt、错 target、错 source task、双空 target、source_seq 不参与、缺快照、缺 source_task。
- 下游必须补 `cardWaitEventActionable × runCardWait` 的事件所属卡与 `--subtree` 正反例，并给所有可动作 task_mirrored 夹具一条匹配的当前派发快照。
- 下游必须补“同 DataDir 新 Server + SetupAutomation + consume”恢复水位、cursor 写入顺序与 attach 暂缓不推进测试。

### 6.3 移交 plan 附区

以下是查证期确立的实现级事项，不占冻结清单条目；plan 吸收后须在本节标题标注“已由 plan〈文档〉吸收（日期）”并销区：

- 两处快照扫描的分页 helper、错误上下文与日志字段（card/seq/node/attempt/source/拒绝原因）。
- `cardWaitEventActionable` 调用点的参数透传，以及现有 B353 card wait 夹具补 `RecordDispatch` 的具体顺序。
- `advanceAutomationCursor` 在唤醒成功、唤醒失败与自生 `needs_human` 事件路径的调用位置；attach early return 不调用。
- cursor JSON 的临时文件清理、权限错误和载入错误测试；不把失败降级成伪造已保存水位。
- 现有 `WaitDeliveryPolicy` 的调用保持在身份闸之后；不在 `cmd` 或 `wakeconsumer` 复制假集合。

### 6.4 本节点欠账

1. 身份闸的运行时行为尚未在本节点实现，属于 implement；不得写成当前已生效。
2. `card wait` 当前派发快照扫描尚未在本节点实现，属于 implement。
3. 自动化 cursor 文件的读写与重启恢复尚未在本节点实现，属于 implement。
4. Ticket 0 以外无“已实现但零测试”的行为；本轮新增的 wire/投影行为由两支穿缝测试覆盖。

## 7. 本轮法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处。
- 目标图：`codegraph/target.json`，与本契约同提交冻结；`best.json` 结构不变；无新生产符号视图 diff。
- `codegraph validate` 本轮退出码 0；`codegraph check` 本轮仍以退出码 1 失败，原始失败为既有 `d_execution → d_orchestration` 的 `dead-contract` 与 `dead-interface(ApprovalClient)`，不由本卡新增。
- Ticket 0 编译：本轮运行 `go build ./...`，结果记录在本节与 B349 台账。
- 可执行冻结：本轮运行 Facade/HTTP source 三列测试；无哈希/密钥派生命中。
- 三重闸门：已记录 5 项；无其它命中。
