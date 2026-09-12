# B358 契约增量：会话即工作单元——房间锚点从卡翻转到会话

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b358.md`，头部状态行
「**状态**：**已批准**（用户 2026-09-12：「我觉得可以，就这么定吧」）」——本轮已对工作树复核一致，无需回写）
**级别：L3 ｜ 选档复核：重档确认**（会话模型重做 × 账本域宿舍与归属 × 控制面唤醒路径 × CLI room 命令族 × `d_web` 内容面，远超流程固定成本且可并行；直通竖切按重档法定步骤执行，见 §7）
**冻结状态：本提交随 `codegraph/target.json`、`codegraph/diffs/cards-B358-charter.json`、Ticket 0 骨架、直通竖切与本台账冻结**
**有效基线：** `cards/B233.1-charter-7` @ `94246fc8`
**架构形态：** 按子系统分域的平铺领域包，无横向 controller/service/dao 分层（沿用 `codegraph/best.json`；本卡不新增领域）
**命名警示（沿用 B156.2）：** 会话（群）域 `d_collab` 与终端会话域 `d_sessions` 同名不同物；本卡的「会话」是 IM 群工作单元，不是 PTY 会话。
**交棒：** breakdown。

本文档把已批准 spec 的会话语义翻译成现状代码可接的签名、账本事件词表、wire 形状与依赖方向。本节点只落空壳与直通镜像（§7）；唤醒路径改接、旧规则删除、HTTP/CLI/控制台接线全部列入交棒欠账（§8），不能被「已有空壳」冒充完成。

---

## 1. 现状查证

### 1.1 已查证签名与代码事实

下表是本轮对工作树的事实查证；行号只是本轮读数，未来以符号锚决议。

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| 账本事件词表 | 事件类型字符串字面量的唯一定义点是 ledger 包；文件头注释明文「状态骨架锚点、关系类型、事件类型的字符串字面量以本文件为唯一定义点」 | `internal/ledger/types.go:1-3`、常量块 `:44-88` |
| 落事件唯一入口 | `appendEvent` 事务内落一条事件并（PG）`pg_notify`；禁止绕过裸 INSERT | `internal/ledger/events.go#appendEvent`（`:19`） |
| 写事务包裹 | `Store.mutate` 统一写事务（PG advisory lock / SQLite 单写者） | `internal/ledger/store.go#Store.mutate`（`:156`） |
| 两方言 DDL | `ddlStatements(pg bool)` 返回同集合两方言建表语句；`ensureSchema` 幂等执行；`TestDDLDialectParity` 逐表名比对 | `internal/ledger/store.go#ddlStatements`（`:196`）、`#ensureSchema`（`:376`）、`internal/ledger/ddl_parity_test.go#TestDDLDialectParity`（`:26`） |
| 单流升序读 | `EventsFromAsc` 升序、fromSeq 排他、limit<=0 取 1000；cardIDs 空=全流含无卡事件 | `internal/ledger/events.go#Store.EventsFromAsc`（`:63`） |
| 房间消息载荷 | `RoomMessage` 定义在 proto（wire 唯一定义处），ledger 只存 RawMessage 不解释字段 | `internal/proto/rooms.go#RoomMessage`（`:24`） |
| 会话房间投影 | `RoomMessage.Room` 值域注释「卡号 \| project:<name> \| global」；`RoomSummary.ID` 同域 | `internal/proto/rooms.go:25`、`:76` |
| 房间解析 | `Resolve` 只认卡房间（GetCard 命中）与旧群房间（`isGroupRoom`：project:<name>/global）；会话房间无解析 | `internal/collab/room/room.go#Resolve`（`:55`）、`#isGroupRoom`（`:179`） |
| 书写者执法 | `VerifyWriter` 按 kind 分权限（协调者类/relay/user 三种矩阵） | `internal/collab/room/room.go#VerifyWriter`（`:102`） |
| kind 白名单 | `KindAllowed` 七值词表 | `internal/collab/room/room.go#KindAllowed`（`:154`） |
| 房间同属判定 | `SameRoom` 群级看载荷 `Room`、卡级看 `card_id` | `internal/collab/room/room.go#SameRoom`（`:193`） |
| 唤醒路径 | `automationWakeEvent` 首行 `if ev.CardID == "" { return false }`——群/会话消息谁都不醒 | `internal/agentd/wakeconsumer.go#automationWakeEvent`（`:144-146`） |
| 唤醒广播形状 | `decodeHumanRoomMessage` 对 `kind==user && !by_system` 无条件唤醒本卡协调者；`mentions` 未被读 | `internal/agentd/wakeconsumer.go#decodeHumanRoomMessage`（`:201-220`） |
| 席位身份 | `ValidateSeat`/`ParseSeatIdentity`/`EncodeSeatIdentity`：`cli:<cli>#<session_id>`，来源 bind\|coordinate | `internal/proto/seat.go#ValidateSeat`（`:20`）、`#ParseSeatIdentity`（`:53`）、`#EncodeSeatIdentity`（`:41`） |
| 席位权威 | 席位写入 `cards.driver_session`/`driver_source`，CAS 写面 `BindSeat`/`RebindSeat` | `internal/ledger/binding.go#Store.BindSeat`（`:30`）、`#Store.RebindSeat`（`:68`） |
| 出站接口 | `LedgerClient` 定义在会话侧（接口归使用方）；`var _ client.LedgerClient = (*Facade)(nil)` 编译期执法 | `internal/collab/client/client.go#LedgerClient`（`:26`）、`internal/ledger/api/api.go:28` |
| 事件直通投影 | `eventWire`（账本门面）与 `ledgerEventWire`（控制面 HTTP）两处，均逐字段映射 | `internal/ledger/api/api.go#eventWire`（`:147`）、`internal/agentd/ledgerapi.go#ledgerEventWire`（`:111`） |
| HTTP 房间面 | 五端点注册在 `registerLedgerRoutes`；handler 在 `roomsapi.go` | `internal/agentd/ledgerapi.go:49-53`、`internal/agentd/roomsapi.go` |
| CLI room 命令族 | `room list/read/send/inbox` 直调 `collab.Service` | `cmd/room.go:49/78/118/159` |
| TS 契约镜像 + 双侧金样本 | `rooms.ts` 与 Go 结构体逐字段对应，金样本锁形状 | `web/src/api/rooms.ts`、`internal/proto/rooms_fixture_test.go`、`web/src/api/rooms.test.ts` |
| 房间 cursor 介质 | `cursor.Store` 文件介质 `room-cursors.json`，tmp+rename 原子写 | `internal/collab/cursor/cursor.go`、`internal/agentd/server.go:2692` |

### 1.2 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 真正生产者 | 真正消费者 | 结论 |
| --- | --- | --- | --- |
| `ledger.EvRoomMessage="room_message"` | `Store.RecordRoomMessage`（`internal/ledger/rooms.go:17`） | `Service.Send/History/Pending/Mentions`、`wakeconsumer`、`cmd/card_wait.go` | 活跃事实源；本卡沿用，不新增消息事件类型 |
| `ledger.EvMessageConsumed="message_consumed"` | `Store.RecordMessageConsumed`（`internal/ledger/rooms.go:60`） | `Service.Consume/Pending/Mentions` | 活跃事实源；本卡不动恰好一次语义 |
| `proto.RoomMsgUser="user"` / `RoomMsgPointer="pointer"` | `cmd/room.go`、`roomsapi.go`、`Service.Pointer` | `decodeHumanRoomMessage`、`cardWaitEventActionable` | 活跃；本卡沿用 `user` 为会话语义默认，不动 pointer |
| `proto.SeatSourceBind/Coordinate` | `Store.BindSeat/RebindSeat`、CLI 三按钮 | `keystone.Wake`、room 执法 | 活跃；本卡不变更席位来源词表 |
| `EvDriverTakeover="driver_takeover"` | `Store.RebindSeat` | timeline、测试 | 活跃；会话语义的席位变更 timeline 消费它，不新增换绑事件 |
| `RoomSummary.Live` / `driver_leases` | 生产写入者缺失（`docs/roadmap.md` 记债） | `ListRooms` 活性列 | **已知债**：生产恒 false。本卡在详情页成员状态上改为「只报可证实的」（Working/LastActive/Empty），不把这个债照抄成事实 |

本表无零使用死常量被当作事实源。`driver_leases` 是已知漂移债，本卡显式标注、不引用为在线判据的正面事实。

### 1.3 图覆盖债

本轮亲自执行 `codegraph sym collab.Service`、`sym collab.Service.Send`、`sym room.Resolve`、`sym wakeconsumer.go`，均返回原文 `Error: 符号 "..." 不在图中（图未覆盖或名字有误）；近似候选: []`。因此本文对这些符号只用 `file#Symbol` 源码锚，不冒充图节点；Ticket 0 新增符号随本分支视图 diff 落盘（§6）。

---

## 2. 架构户口与依赖方向

### 2.1 户口（不新增领域）

B358 **不新增顶层领域**。会话语义沉进既有 `d_collab`（协作房间域）：会话（群）是该域从「卡房间」翻转后的工作单元。席位权威仍在 `d_ledger` 的 `cards.driver_session`；消息事实仍在 `d_ledger` 的 `card_events` 单流；控制面唤醒路径仍在 `d_gateway`（`internal/agentd`）与 `d_orchestration`（`wakeconsumer` 域归属见节点容器，实际代码在 `internal/agentd`）。

`codegraph/best.json` 结构树不变；本卡只改 `target.json` 的既有方向注记（§5）。

### 2.2 双向门面（沿用 B156.2 冻结形态）

- **入站 api**：`internal/collab`（package collab）。外界（gateway、CLI）只 import 此包 + proto DTO；会话新方法都落在这里。
- **出站 client**：`internal/collab/client#LedgerClient`（接口归使用方）。新增八个会话方法；具体实现是 `internal/ledger/api.Facade`，组装点绑定。`var _ client.LedgerClient = (*Facade)(nil)` 是编译期执法。
- **哨兵归属**：接口不 import ledger；「对象不存在」用会话侧哨兵 `client.ErrNotFound`（实现侧 Facade 把账本 `ledger.ErrNotFound` 翻译成它），门面再映射 `collab.ErrNoRoom`。

### 2.3 依赖方向

本卡**不新增跨域方向、不改预算**：

- `d_collab → d_ledger`：继续走既有 `interfaces: ["LedgerClient"]`、零调用预算。会话本体与卡↔会话归属经接口读写，collab 生产代码仍零直调 ledger。
- `d_collab → d_protocol`：继续走既有 `entries: ["proto 实体"]`。会话 DTO 加在 proto 唯一定义处。
- `d_ledger → d_protocol`：继续走既有 `entries: ["proto 实体"]`。会话消息仍取 `proto.RoomMessage`；会话 DTO 经 `ledger/api` 投影时由 Facade 层引用。

后续实现节点若为控制面唤醒路径引入**新的**跨域边（例如 `d_orchestration` 或 `d_gateway` 直调 collab 的寻址判定），必须在引入边的同一提交先写明契约声明——本轮不预先声明尚无活跃边的方向（check 的 dead-contract 会把空声明打红）。

---

## 3. 契约增量：精确签名

### 3.1 账本事件词表增量（`internal/ledger/types.go`）

新增四个会话结构事件常量（落既有事件常量块）：

```go
// B358 会话（群）域的结构事件。结构事件只进详情页 timeline、不唤醒任何人，
// 也不进群聊流。
EvSessionCreated    = "session_created"     // 会话建立（群主 = 人或主 agent 会话身份）
EvSessionArchived   = "session_archived"    // 会话显式归档；归档后只读
EvSessionCardJoined = "session_card_joined" // 卡进群（只建讨论面，不等于配人）
EvSessionCardLeft   = "session_card_left"   // 卡移出会话
```

结构事件是**无卡事件**（`card_id` 为空；会话用 `session:<n>` 不冒充卡号），与旧群消息同款：物理上进不了任何执行会话的 `Store.Follow` 多路 wait（`follow.go:13-19` 既成行为）。

### 3.2 会话 wire DTO（`internal/proto/sessions.go`，本分支新建）

```go
const SessionKind = "session"

// 成员种类
const ( SessionMemberHuman = "human"; SessionMemberAgent = "agent"; SessionMemberSeat = "seat" )
// 成员状态（「看板不说谎」：只报可证实的）
const ( SessionMemberWorking = "working"; SessionMemberListening = "listening"
        SessionMemberLastActive = "last_active"; SessionMemberEmpty = "empty" )

type Session struct {
    ID string; Title string; Owner string; Archived bool
    Members []string   // 显式成员（人与主 agent 的外部会话身份）；席位成员派生不落表
    Cards   []string   // 会话内卡号
    CreatedAt time.Time; UpdatedAt time.Time
}

type SessionMember struct {
    Identity string    // 人/主 agent 外部会话身份，或协调者席位身份
    Kind     string    // human|agent|seat
    CardID   string    // 席位成员所属卡号
    CardTitle string
    Status   string    // working|listening|last_active|empty
    LastActive time.Time
}

type SessionCard struct { CardID, Title, Status, Seat string } // Seat 空 = 空座

type SessionSummary struct {
    ID, Kind, Title, Owner string
    Archived bool; Unread int; NeedsHuman bool
    LastActivity time.Time
    Preview *RoomPreview; Members []SessionMember; Cards []SessionCard
}

type SessionNode struct { CardID, Title, Node string; Round int; State, Target string }

const (
    SessionEventCardJoined = "card_joined"
    SessionEventCardLeft   = "card_left"
    SessionEventSeatBound  = "seat_bound"
    SessionEventSeatRebound = "seat_rebound"
    SessionEventNeedsHuman = "needs_human"
    SessionEventArchived   = "archived"
    SessionEventCreated    = "created"
)

type SessionTimelineEvent struct {
    Seq int64; Kind string; CardID string; Detail string; Actor string; CreatedAt time.Time
}

type SessionDetail struct {
    Summary  SessionSummary
    Nodes    []SessionNode
    Timeline []SessionTimelineEvent
}

type SessionCreatedPayload struct { ID, Title, Owner string }
type SessionArchivedPayload struct { Session string }
type SessionCardPayload struct { Session, Card string }
```

`proto.RoomMessage` 增一个投递寻址字段（其余字段不变）：

```go
ReplyTo int64 `json:"reply_to,omitempty"` // 被回复消息的账本 seq；隐式寻址原作者；0=无回复锚
```

`refs/mentions/decision_id/by_system/reply_to` 皆 `omitempty`：缺省不出键（既有最小金样本不受扰；新增键进契约面须新金样本，见 §7）。

### 3.3 出站 client 接口增量（`internal/collab/client/client.go#LedgerClient`）

```go
// 会话侧哨兵（接口归使用方，不 import ledger）
var ErrNotFound = errors.New("collab: 账本对象不存在")

type LedgerClient interface {
    // …既有方法不变…
    CreateSession(title, owner, actor string) (proto.Session, error)
    GetSession(id string) (proto.Session, error)
    ListSessions() ([]proto.Session, error)
    ArchiveSession(id, actor string) error
    JoinCardToSession(sessionID, cardID, actor string) error
    LeaveCardToSession(sessionID, cardID, actor string) error
    SessionOfCard(cardID string) (string, error)
    AddSessionMember(sessionID, identity, actor string) error
}
```

实现 `internal/ledger/api.Facade` 逐方法转调 `Store`；`GetSession` 把 `ledger.ErrNotFound` 翻成 `client.ErrNotFound`。

### 3.4 账本存储增量（`internal/ledger/sessions.go`，本分支新建）

```go
type Session struct { ID, Title, Owner string; Archived bool; Members, Cards []string; CreatedAt, UpdatedAt time.Time }

func (s *Store) CreateSession(title, owner, actor string) (Session, error)   // 落 EvSessionCreated；id=session:<n>
func (s *Store) GetSession(id string) (Session, error)                       // 不存在 → ErrNotFound
func (s *Store) ListSessions() ([]Session, error)                            // 含归档，按创建序
func (s *Store) ArchiveSession(id, actor string) error                       // 幂等；落 EvSessionArchived
func (s *Store) JoinCardToSession(sessionID, cardID, actor string) error     // 一卡只挂一会话；落 EvSessionCardJoined
func (s *Store) LeaveCardToSession(sessionID, cardID, actor string) error    // 幂等；落 EvSessionCardLeft
func (s *Store) AddSessionMember(sessionID, identity, actor string) error    // 幂等
func (s *Store) SessionOfCard(cardID string) (string, error)                 // 空串=不属任何会话
```

DDL（两方言同步加，`ddlStatements`）：

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY, title TEXT NOT NULL, owner TEXT NOT NULL,
    archived <BOOLEAN|INTEGER> NOT NULL DEFAULT <false|0>,
    members <JSONB|TEXT> NOT NULL DEFAULT '[]',
    created_at <TIMESTAMPTZ|TEXT> NOT NULL, updated_at <TIMESTAMPTZ|TEXT> NOT NULL);
CREATE TABLE IF NOT EXISTS session_cards (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    card_id TEXT NOT NULL REFERENCES cards(id),
    created_at <TIMESTAMPTZ|TEXT> NOT NULL,
    PRIMARY KEY (session_id, card_id));
CREATE UNIQUE INDEX IF NOT EXISTS uq_session_cards_card ON session_cards(card_id);
```

`uq_session_cards_card` 是「一卡同时只挂一个会话」的账本级最后一道兜底；门面在事务内先给出可读错误。

### 3.5 会话门面（`internal/collab/sessions.go`，本分支新建）

```go
func (s *Service) CreateSession(title, owner, actor string) (proto.Session, error)
func (s *Service) ListSessions(member string) ([]proto.SessionSummary, error) // 谁需要我
func (s *Service) SessionDetail(id string) (proto.SessionDetail, error)       // 结构与状态
func (s *Service) ArchiveSession(id, actor string) error
func (s *Service) JoinCard(sessionID, cardID, actor string) error             // 进群 ≠ 配人
func (s *Service) LeaveCard(sessionID, cardID, actor string) error
func (s *Service) WakeTargets(ev proto.LedgerEvent) ([]string, error)         // 寻址 → 被唤醒成员集
func (s *Service) MessageWakeTargets(msg proto.RoomMessage) ([]string, error) // 消息级
func (s *Service) AddressesCard(msg proto.RoomMessage, cardID string) bool    // 唤醒路径必要条件
```

### 3.6 投递寻址纯函数（`internal/collab/room/delivery.go`，本分支新建）

```go
// 接收人由发送者写下的寻址决定，与「群里有谁」无关。签名里没有成员集合——
// 任何按成员集合投递的形状都写不出来。
func ResolveDelivery(msg proto.RoomMessage, replyAuthor string,
    resolveSeat func(mention string) (seat string, isCard bool)) []string
func IsAddressed(msg proto.RoomMessage, replyAuthor string) bool
```

规则（冻结语义）：

1. **显式 `@`**：`mentions` 逐个解析。`mention` 是卡号 → `resolveSeat` 返回该卡**当前席位**（换绑后指向新席位）；空座（`isCard=true` 但 `seat==""`）该 @ 落空、不唤醒；非卡号（外部会话身份）原样使用。
2. **`reply_to`**：命中时把被回复消息的作者作为隐式目标（一人）；空作者忽略。
3. **系统结构行**（`BySystem` 或 `kind=pointer`）恒不唤醒。
4. **无寻址**（既无 mention 又无有效 reply）返回空集——落账、进未读、不唤醒任何人。
5. 去重保首次出现顺序。

### 3.7 会话房间形态（`internal/collab/room/room.go`）

- 新增 `KindSession = "session"` 与 `IsSessionRoom(roomID) bool`（形如 `session:<n>`）。
- `Resolve` 增会话分支：会话房间作为无卡可比的讨论面恒可写；归档只读判定由门面在会话本体上做（`Resolve` 只解析形态，不查会话表）。
- `SameRoom` 对会话房间看载荷 `Room`（与群房间同款）。

---

## 4. 原子冻结清单

每条是独立可判 pass/fail 的接缝断言。本轮 Ticket 0 已使其可判的条目由 §7 的竖切/守卫测试锁定；其余为实现节点对账条目。

### 4.1 会话本体与生命周期

1. `Store.CreateSession` 分配的 id 形如 `session:<n>`，且单调递增、不复用。
2. `CreateSession` 落恰一条 `EvSessionCreated` 无卡事件（`card_id` 为空）。
3. `CreateSession` 的群主（owner）非空；`Session.Members` 初值含 owner。
4. `GetSession` 对不存在 id 返回 `ErrNotFound`。
5. `ListSessions` 返回全部会话（含归档），按创建序。
6. `ArchiveSession` 幂等；归档后 `Session.Archived=true`，落恰一条 `EvSessionArchived`。
7. 归档会话不得再 `JoinCardToSession`（返回错误，且不落事件）。
8. `JoinCardToSession` 对不存在的卡或会话返回错误，且不写归属行。
9. `JoinCardToSession` 对已属**其它**会话的卡返回错误，且不改归属。
10. 同一卡重复 `JoinCardToSession` 到同一会话幂等（`uq_session_cards_card` 兜底，不落第二条归属）。
11. `LeaveCardToSession` 幂等；实际移出时落恰一条 `EvSessionCardLeft`。
12. `SessionOfCard` 对不属于任何会话的卡返回空串。
13. 卡终态**不**导致会话归档（会话生命周期独立）。

### 4.2 成员派生

14. 会话成员 = `Members`（显式，人与主 agent）+ 会话内各卡的当前席位；不新增成员表。
15. 席位成员的身份等于该卡 `driver_session`；卡换绑后成员身份随之变（同一卡不产生第二个成员条目）。
16. 空座卡产生一条 `Status=empty` 的席位成员（`Identity` 为空）。
17. 成员状态只报可证实的：有未过期租约报 `working`/`listening`，否则报 `last_active`，绝不报「在线」。
18. 成员状态判定的时钟与租约判定同一注入时钟（不得「判据读真实钟、夹具拨别的钟」）。

### 4.3 投递寻址（本卡核心）

19. `ResolveDelivery` 对无 mentions 且 `ReplyTo==0` 的消息返回空集。
20. `ResolveDelivery` 对显式 `@卡号` 解析为该卡当前席位。
21. 换绑后同一 `@卡号` 解析为新席位（旧席位不再命中）。
22. `ResolveDelivery` 对空座卡号 `@` 返回空集（不落空到别人）。
23. `ResolveDelivery` 对非卡号 mention（外部会话身份）原样返回该身份。
24. `ResolveDelivery` 对 `ReplyTo>0` 返回被回复消息作者（隐式寻址）。
25. 同一目标既被 `@` 又被回复时去重为一条。
26. `ResolveDelivery` 对 `BySystem=true` 或 `kind=pointer` 恒返回空集。
27. `ResolveDelivery` 的签名不含成员集合类参数（源码守卫锁定）。
28. `AddressesCard` 仅当消息寻址命中该卡当前席位时返回 true；卡无席位或账本缺失返回 false。
29. 唤醒路径对无寻址消息不唤醒任何人（`wakeconsumer` 实现节点锁定）。
30. 唤醒路径对 `@` 命中才唤醒（广播形状删除）。
31. 系统结构事件（入群/移出/归档/席位变更/needs_human）不唤醒任何人。

### 4.4 房间形态与只读

32. 会话房间 id 形如 `session:<n>`，`IsSessionRoom` 为真。
33. 会话房间消息落为无卡事件（`card_id` 为空、载荷 `Room=会话号`）。
34. 旧卡房间/`project:`/`global` 语义不变（本卡不删旧形态；旧房间只读归档在实现节点）。
35. kind 白名单/书写者矩阵的废止在实现节点完成；本轮不删。

### 4.5 依赖与图

36. `internal/collab`（非测试）零 import `internal/ledger`；`graph check --view cards-B358-charter` 无本卡新增违规。
37. `var _ client.LedgerClient = (*Facade)(nil)` 编译期成立（Facade 缺任一方法即编译失败）。
38. `codegraph/diffs/cards-B358-charter.json` 记录 Ticket 0 新增符号；`codegraph validate` 本视图零 issue。

---

## 5. 依赖方向、组装点与预算

- `d_collab → d_ledger`：既有 `interfaces: ["LedgerClient"]`、零调用预算不变。会话方法只加在接口与 Facade，collab 生产代码仍零直调 ledger。
- `d_collab → d_protocol`：既有 `entries: ["proto 实体"]` 不变。会话 DTO 加在 proto 唯一投影处。
- `d_ledger → d_protocol`：既有 `entries: ["proto 实体"]` 不变。
- 组装点仍是 `main.go`、`internal/agentd/server.go`、`internal/agentd/codegraph.go`；本卡无新增组装点。CLI 侧组装点仍是 `cmd/room.go#roomServiceFor`（`collab.New(ledgerapi.New(st))`）。
- `best.json` 结构树不变；`target.json` 只补三条既有方向的 B358 注记，无新方向、无预算变化。

---

## 6. 拍板记录（三重闸门）

只记录同时满足「难逆转 × 无上下文会惊讶 × 真取舍」的决定。本轮**无命中**：以下三条经审后判为探索性/确定性设计，不满足「后人看到会想修掉」的惊讶条件或不含被否掉的像样方案，故不立拍板记录——但逐条记录其判定依据，防「空着与没审过不可区分」。

- **寻址判定做成纯函数 `ResolveDelivery(msg, replyAuthor, resolveSeat)`**：判据 27 的源码守卫已把形状钉死，改它要同时动 room/collab/agentd 三处测试；但「纯函数 + 注入解析」是层内实现选择（对契约对侧不可见），被否方案（在 Service 内内联判定）不改变对外签名。不立。
- **一卡一会话用唯一索引兜底**：`uq_session_cards_card` 是数据形状决定，被否方案（只靠事务查后写）在 SQLite 单写者下等价；不属「后人顺手优化会反悔」的流程裁决（有测试会变红）。不立。
- **成员状态取值 Working/Listening/LastActive/Empty**：直接来自 spec 实现决定 4 与「看板不说谎」，无取舍空间。不立。

**无其它命中。**（空着与没审过的区别已按上三条审计记录覆盖。）

---

## 7. Ticket 0、可执行冻结与直通竖切

### 7.1 Ticket 0 已落（本提交）

- `internal/proto/sessions.go`：会话 DTO 全集（完整定义，非残缺）。
- `internal/proto/rooms.go`：`RoomMessage` 增 `ReplyTo int64 omitempty`。
- `internal/ledger/types.go`：四个会话事件常量。
- `internal/ledger/sessions.go`：`Session` 实体 + 八个 Store 方法 + 两方言 DDL（`sessions`/`session_cards`）。
- `internal/ledger/api/api.go`：Facade 八个会话直通镜像 + `translateNotFound`。
- `internal/collab/client/client.go`：`LedgerClient` 八个会话方法 + `ErrNotFound` 哨兵。
- `internal/collab/room/delivery.go`：`ResolveDelivery`/`IsAddressed` 纯函数（真实实现，非空壳——纯函数无接线依赖）。
- `internal/collab/room/room.go`：`KindSession`/`IsSessionRoom`/`Resolve` 会话分支/`SameRoom` 会话分支。
- `internal/collab/sessions.go`：`Service` 会话方法全集（真实实现，读侧纯投影 + 账本写经接口缝）。
- `codegraph/diffs/cards-B358-charter.json`：39 新符号 + 2 修改。

### 7.2 直通竖切（重档法定步骤）

一次真实调用穿全链，测试钉在主缝（库缝形态 = 夹具直调）：

**路径**：`collab.Service.CreateSession` → `client.LedgerClient.CreateSession` → `internal/ledger/api.Facade` → 真 SQLite `Store.CreateSession` 落 `EvSessionCreated` → `collab.Service.JoinCard` → `Store.JoinCardToSession` 落卡↔会话归属 → `collab.Service.Send`（会话房间，无卡事件）→ `Store.RecordRoomMessage` → `collab.Service.History` 读回 → `ListSessions`/`SessionDetail` 投影。
**测试**：`internal/collab/sessions_test.go#TestSessionVerticalSlice`；同文件 `#TestWakeTargetsAddressing` 覆盖换绑后 @ 命中新席位、`#TestResolveDeliveryPurity` 覆盖寻址纯函数、`#TestSessionArchiveReadOnly` 覆盖归档只读。
**源码守卫**：`internal/collab/room/delivery_gate_test.go`（ResolveDelivery 无成员集合形参、必引用 Mentions+ReplyTo）。

竖切的写死结果不构成子卡的「已有活路径」。

### 7.3 可执行冻结

- 命中 JSON wire 编码（会话 DTO 与 `RoomMessage.ReplyTo` 的跨语言形状）。本轮已落 **Go 侧金样本**：`internal/proto/sessions_fixture_test.go`（`reply_to` 的 omitempty 零值/非零/往返、SessionSummary/SessionDetail 键集、成员状态词表），本轮跑过。
- **TS 孪生金样本**（`web/src/api/rooms.ts` + `testdata/RoomsFixture.json`）属越过空壳的可观测行为（前端内容面），列入 §8 欠账，由实现节点按 `rooms_fixture_test.go`/`rooms.test.ts` 既有形态补，届时两侧逐键一致。
- 无哈希、密钥派生、加密编码（写明「无命中」，不适用）。
- 唤醒路径的运行时行为（无寻址不唤醒 / @ 命中唤醒 / reply 命中原作者 / 结构事件不唤醒）由 `ResolveDelivery`/`AddressesCard` 的纯函数测试 + 源码守卫先行锁定；`wakeconsumer` 接线本身属实现，见 §8。

### 7.4 本轮实际跑过的命令（原文见台账与交棒报文）

- `go build ./...` → 退出码 0。
- `go test ./internal/collab/...` → `ok`；`go test ./internal/ledger/...` → `ok`。
- `codegraph validate --view cards-B358-charter` → 本视图 0 issue；`codegraph check --view cards-B358-charter` → fails=6（与基线逐条相同）。

---

## 8. 本节点欠账（实现节点逐条补齐，不得静默带走）

1. **唤醒路径改接寻址**（`internal/agentd/wakeconsumer.go`）：`automationWakeEvent` 去掉首行 `ev.CardID==""` 直接不唤醒；`decodeHumanRoomMessage` 的广播形状改为「寻址命中才唤醒协调者 / 主 agent」；群/会话消息经 `collab.Service.MessageWakeTargets`（或等价寻址判定）决定唤醒；唤醒载荷最小化（命中条 + 引用条 + 未读数）。这是 spec §4.3 的核心行为，**空壳不具备**。
2. **旧规则废止**：`room.VerifyWriter` 的 kind 书写者矩阵、`room.KindAllowed` 的 kind 白名单、卡:房间 1:1 的 `Resolve` 语义在实现节点删除或收敛为会话语义；配套测试改写。本轮只加不改，旧矩阵仍在。
3. **控制面 HTTP/CLI 接线**：会话列表/群聊/详情/归档/拉卡移出/发言端点与 `handoff room` 命令族扩展；错误映射沿用 collab 哨兵。
4. **控制台内容面**（`web/src/app/rooms/*` + `web/src/api/rooms.ts`）：会话列表/群聊/详情三态按北极星 `prototypes/b358-session-groups/pages/sessions.html` 复刻；TS 镜像与 fixture 金样本补会话 DTO。
5. **详情页投影精细判据**：成员状态 Working/Listening/LastActive/Empty 的「可证实」判据逐条断言（续租成员报精确态、外部会话只报最后活跃）；派发节点从 `card_tasks`/`task_mirrored` 聚合的正确形状。
6. **升级三档纪律文本**：协调者与主 agent 的 discipline/skill 修订（协调者自决填补级、@ 主 agent 仅推翻级、人掌三道人工门）——落纪律资源与 skill 层，非本节点代码面。
7. **旧 326 卡房间只读归档**：归档迁移/只读判定（spec §4.5），本节点未动。
8. **OOS 项的 roadmap 登记**：主 agent 内部对话面、成员状态心跳写入路径、多人时代、历史翻页、富文本（spec §7）。

---

## 9. 移交 plan 附区

以下是查证期确立的实现级落点，不占冻结条目；plan 吸收后须在本节标题标注「已由 plan〈文档〉吸收（日期）」并销区：

- `internal/ledger/store.go#ddlStatements` 两方言分支同时加 `sessions`/`session_cards`（`TestDDLDialectParity` 逐表名比对会拦截单边漏加）。
- `Session` 成员列用 `JSONB`(PG)/`TEXT`(SQLite) 存 JSON 数组，编解码 `encodeMembers`/`decodeMembers` 兼容空串。
- `JoinCardToSession` 的门面错误用 `ErrBadState`（已属会话）与 `ErrNotFound`（卡/会话不存在），实现节点按 gateway 映射表补 HTTP 状态；`uq_session_cards_card` 是最后兜底，不是首选错误路径。
- `ResolveDelivery` 的 `resolveSeat` 注入点：门面用 `lc.GetCard(mention)` 判定是否卡号；账本读失败按「非卡号」处理（原样用 mention），避免一次读错把 @ 吞掉。
- `AddressesCard` 是唤醒路径的收窄入口；`wakeconsumer` 接线时优先经它或 `MessageWakeTargets`，不要在 agentd 内重造寻址判定。
- `SessionNode` 从 `task_mirrored` envelope 的 `node`/`task_type` 聚合；不新增执行域依赖。
- 会话 timeline 的席位变更消费 `driver_takeover`，仅当事件所属卡在该会话内时归入。

（区头销账：本区将由 plan〈b358 实现计划〉吸收。）

---

## 10. 本轮法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处（§1）。
- 上游状态位：spec 头部「已批准」已回写，与本文件头一致（无需二次回写）。
- 目标图：`codegraph/target.json` 三条既有方向补 B358 注记；`codegraph/best.json` 不变；Ticket 0 新符号写入 `codegraph/diffs/cards-B358-charter.json`——随本提交冻结。
- Ticket 0 编译：本轮 `go build ./...` 退出码 0；`go test ./internal/collab/...`、`./internal/ledger/...`、`./internal/proto/...` 全绿（原文见台账）。
- 直通竖切：`TestSessionVerticalSlice` 等本轮跑过。
- 可执行冻结：Go 侧金样本 `internal/proto/sessions_fixture_test.go` 本轮跑过（`reply_to` omitempty、会话 DTO 键集、成员状态词表）；TS 孪生金样本由实现节点补（欠账 §8/§9）。无哈希/密钥派生命中。
- 源码守卫：`delivery_gate_test.go` 本轮跑过。
- 三重闸门：§6 记录「无命中」及三条审计依据，非空着。
- 图三闸：`codegraph validate --view cards-B358-charter` 本视图 0 issue；`codegraph check --view cards-B358-charter` fails=6（与基线逐条相同、无本卡新增）。
