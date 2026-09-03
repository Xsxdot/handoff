# B312 契约增量：开卡绑当前会话（linux-01/codex 对照）

**上游状态：已批准**（源 spec：`docs/superpowers/specs/2026-09-03-b307-bind-current-session-design.md`；头部状态行已核实）
**级别：L3 轻档**
**有效基线：`acc/b156.2-156.3` @ `5f8b71b34`**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（`codegraph/best.json`）
**冻结状态：本提交随 `codegraph/target.json` 与 `codegraph/diffs/cards-B312-charter.json` 冻结**

本节点只落契约、协议身份编码骨架和该编码的金样本；不读取 B307 muse-spark 产出，不实现业务入口，不新造对外路由以外的旁路。L3 轻档没有直通竖切，运行时最薄路径由 plan 节点承接。

## 1. 现状查证与边界

上游 spec 的问题主语是“这张卡的一个协调者席位”，不是人尺度 actor，也不是 keystone 内存中的某个 `SessionRef`。本契约把同一席位字符串贯穿账本、房间、派发和看板；小队仍只负责叫机器人时的载体选择。

已对当前工作树逐项查证的签名和生产/消费位置如下。表中的 `file#Symbol` 是代码事实锚；带“新增”字样的行是本契约冻结的后续签名，不表示当前已经有该业务实现。

| 现状签名/字面值 | 代码事实出处 | B312 契约变化 |
| --- | --- | --- |
| `func (s *Store) ClaimCard(id, owner string) error` | `internal/ledger/move.go#Store.ClaimCard`（`:146-148`） | 保留人尺度旧 API 名称，但不得再写协调者席位；`card dispatch --step` 不得用它占座 |
| `func (s *Store) ClaimCardAs(id, owner, carrier string) error` | `internal/ledger/binding.go#Store.ClaimCardAs`（`:27-69`） | 保留作为兼容符号；本卡三颗按钮不调用它写席位，`carrier` 不再是席位身份 |
| `func (s *Store) ReleaseCard(id, session string) error` | `internal/ledger/move.go#Store.ReleaseCard`（`:150-182`） | 保留命令面；成功/幂等路径不清协调者席位，非空席位指向 `rebind` |
| `func (s *Store) TakeoverCard(id, session, actor string) error` | `internal/ledger/tasks.go#Store.TakeoverCard`（`:96-121`） | 保留命令面；不得覆写协调者席位，不再把人尺度接管当作换绑 |
| `func (s *Store) RebindDriver(id, toSession, carrier, expect string) error` | `internal/ledger/binding.go#Store.RebindDriver`（`:71-113`） | 用户面废止 `--to/--carrier/--expect`；内部 CAS 由新的 `BindSeat`/`RebindSeat` 承接 |
| `type Card struct { ... DriverSession string; DriverHeartbeatAt time.Time }` | `internal/ledger/types.go#Card`（`:109-126`） | `DriverSession` 改为规范席位身份字符串；增加 `DriverSource`，心跳列仍不是席位来源 |
| `type Card struct { ... DriverSession string; DriverHeartbeatAt time.Time }` | `internal/proto/ledger.go#Card`（`:15-36`） | Go wire 镜像增加 `DriverSource string \`json:"driver_source,omitempty"\``，不另发第二套身份字段 |
| `cardColumns` / `scanCard(row rowScanner) (Card, error)` | `internal/ledger/cards.go#cardColumns`（`:348-371`） | 读写列同步加入 `driver_source`；旧 `driver_session` 非空但不能按新语法解析时保留原值并标非法，不自动升级 |
| `func (f *Facade) BindDriver(id, session, carrier, expect string) error` | `internal/ledger/api/api.go:93-97`（图未提供该方法锚） | 旧协作接口不扩方法；控制面席位写入直接走 `ledger.Store` 的新原子写面，门面仍只做既有镜像 |
| `type SessionSpec struct { CLI, HomeDir, Model, Workdir string; Env []string }` | `internal/keysclient/keysclient.go#SessionSpec`（`:13-21`） | 叫机器人仍由小队解析该规格；当前会话席位不从 `SessionSpec` 猜出 |
| `type SessionRef struct { CLI, SessionID, Machine, HomeDir, Workdir, Model string }` | `internal/keysclient/keysclient.go#SessionRef`（`:23-33`） | 叫机器人席位从 `CLI+SessionID` 形成；续接时由 `SessionRef` 注入当前会话环境 |
| `func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)` | `internal/keystone/keystone.go#Service.Wake`（`:94-126`） | 先读账本来源；`bind` no-op，`coordinate` 才 Resume/重建；内存缺失不得把坐下席位当成可 Launch |
| `func (s *Service) LaunchForCard(ctx context.Context, card, source string, spec keysclient.SessionSpec) (RoundResult, error)` | `internal/keystone/keystone.go#Service.LaunchForCard`（`:128-136`） | 生产调用的 `source` 只允许 `coordinate`；它只承接叫机器人回合，座位写入由 agentd 组装点在结果成功后 CAS |
| `func (s *Service) Locate(card, workdir string) (keysclient.AttachInfo, error)` | `internal/keystone/keystone.go#Service.Locate`（`:158-168`） | `GET /coordinator.bound` 不再以该内存定位结果为真相；Locate 可由账本席位重建最小 `SessionRef` |
| `func VerifyWriter(r *Room, kind, actor string) error` | `internal/collab/room/room.go#VerifyWriter`（`:102-146`） | 签名和矩阵不变；比较值改为规范席位字符串 |
| `func (s *Service) Pending(consumer string) ([]proto.LedgerEvent, error)` | `internal/collab/service.go#Service.Pending`（`:162-201`） | consumer 改为规范席位字符串；`@B号` 仍按该卡当前席位入队 |
| `func (s *Service) Consume(seq int64, consumer string) error` | `internal/collab/service.go#Service.Consume`（`:204-224`） | 签名不变，消费者使用同一规范席位字符串 |
| `func ledgerActor() string` | `cmd/ledgercli.go#ledgerActor`（`:42-50`） | 仍可用于人尺度事件审计；禁止用于 bind/rebind-self/有席位 step/协调者 kind 的席位出示 |
| `func (s *Server) launchCoordinatorRound(ctx context.Context, card, source string) (keystone.RoundResult, error)` | `internal/agentd/scheddrain.go:212-246`（图未提供该符号锚） | 保留调度编排；成功后由同组装点把返回 `SessionID` 与 `spec.CLI` 写成 `coordinate` 席位 |
| `func (s *Server) handleCoordLaunch(w http.ResponseWriter, r *http.Request)` | `internal/agentd/coordapi.go:93-148`（图未提供该符号锚） | 端点语义收窄为“叫机器人”；空座才拉起并写席位，`manual/card_create` 均退役 |
| `export const launchCoordinator = (cardId: string, source?: CoordinatorLaunchSource)` | `web/src/api/scheduling.ts#launchCoordinator`（`:231-241`） | 去掉 `manual/card_create` source；新建卡不再调用，抽屉按钮改叫“叫机器人” |
| `func coordinateAfterCreate(cmd *cobra.Command, cardID string) error` | `cmd/card_coordinate.go#coordinateAfterCreate`（`:18-36`） | 删除开卡后拉起接线；`card add --coordinate` 不注册，旧 flag 必须失败并指向 `card coordinate` |

现状的一个图覆盖缺口也在本轮记录：本地 codegraph 能命中账本/keystone/collab 的部分节点，但 `n_web_api_scheduling` 查询返回“符号不在图中”，所以 Web 行以本轮 `rg`/源码为事实，不凭图推造调用边。

## 2. 席位身份、列名与协议类型

### 2.1 规范身份字符串

席位身份唯一字符串由 `internal/proto/seat.go` 提供的骨架函数编码和解码：

```go
func EncodeSeatIdentity(cli, sessionID string) (string, error)
func ParseSeatIdentity(raw string) (cli, sessionID string, err error)
```

规范语法为：

```text
cli:<cli>#<session_id>
```

规则逐条冻结：

1. `cli` 是 hostapi/RunTurn 使用的物种名，如 `codex`、`claude`、`grok`、`opencode`；不是载体登记名，不是 `cli:<USER>@<hostname>`。
2. `session_id` 是该 CLI 返回/继续使用的会话 id；不得由用户通过 `--to` 等 flag 任意输入。
3. `cli` 与 `session_id` 均非空、无首尾空白、无 Unicode 空白；两者均不得含 `#`；`cli` 不得含 `:`。
4. 编码结果必须是 `cli:` 前缀、一个 `#` 分隔符和两段原值；同一对原值的规范字符串按字节相等。
5. `cli:user@host`、缺少 `#`、空分段、多个 `#` 都是非法旧席位；不能自动迁移、不能当空座。
6. 空字符串只由卡字段表示空座；调用 `ParseSeatIdentity("")` 必须报错，避免缺失身份静默降级。

来源值不是身份字符串的一部分，单独使用以下受控词：

```go
type SeatSource string

const (
	SeatSourceBind       SeatSource = "bind"
	SeatSourceCoordinate SeatSource = "coordinate"
)
```

`driver_source` 只允许上述两个值；非空身份配未知来源是非法席位。空身份必须同时为空来源。

### 2.2 账本存储与 wire 镜像

`cards` 表在 PostgreSQL 和 SQLite 两套 DDL 中都增加：

```sql
driver_source TEXT
```

现有 `driver_session TEXT` 保留，继续承载规范身份字符串；已有 `driver_carrier` 迁移列只作兼容存量，不读、不写、不映射到席位。既有 `driver_heartbeat_at` 仍可保留认领时刻展示，但不参与身份合法性和来源判断。旧表通过幂等 `ALTER TABLE ... ADD COLUMN driver_source` 补列，重复列错误按现有迁移习惯容忍。

Go 类型与 JSON 字段精确为：

```go
// internal/ledger/types.go 与 internal/proto/ledger.go
DriverSession      string    `json:"driver_session,omitempty"`
DriverSource       string    `json:"driver_source,omitempty"`
DriverHeartbeatAt  time.Time `json:"driver_heartbeat_at,omitempty"`
```

不新增 `driver_cli`、`driver_session_id`、`seat` 或第二个席位对象字段。`driver_session` 是当前所有卡房间、Pending、step actor 和状态展示共用的字符串投影；`driver_source` 只补充它来自“坐下”还是“叫机器人”。

席位状态判定：

| `driver_session` | `driver_source` | 状态 | bind/coordinate |
| --- | --- | --- | --- |
| 空 | 空 | 空座 | 可按入口规则占用 |
| 合法规范字符串 | `bind` | 坐下席位 | 两个普通占用入口拒绝 |
| 合法规范字符串 | `coordinate` | 叫机器人席位 | 两个普通占用入口拒绝 |
| 非空但格式非法 | 任意 | 非法旧席位/坏数据，占用态 | bind/coordinate 拒绝，必须显式 rebind |
| 非空合法字符串 | 空或未知 | 非法来源，占用态 | bind/coordinate 拒绝，必须显式 rebind |

“空座”只由两列同时为空判定。读取层保留非法原值供报错显示；绝不把无法解析的旧值变成空座。

## 3. 当前会话注入与共享出示

本仓当前没有 `ledgerSession` 生产符号，也没有已实现的当前 agent 会话注入环境。B312 冻结的新增注入键是：

```text
HANDOFF_SESSION_CLI
HANDOFF_SESSION_ID
```

规则如下：

1. `cmd` 的 `currentSeatIdentity() (string, error)` 读取这两个键，校验非空后调用 `proto.EncodeSeatIdentity`；缺任一键都失败，不回退 `USER`、hostname、PID、`ledgerActor()` 或 `web:<addr>`。
2. CLI `bind`、`rebind --self`、卡已有席位时的 `card dispatch --step`、协调者 kind 的 `room send` 共用这一函数；它们不各自拼接字符串。
3. `coordinatorRunner.Resume` 从 `SessionRef{CLI,SessionID}` 为子进程追加同名两个环境键，值不得写日志；这样叫机器人席位在后续无头 handoff 回合能再次出示。
4. Fresh `Launch` 返回的会话 id 只能在回合结束后落座；首回合不因无法预知新 id 而伪造席位。之后 Resume 使用 `SessionRef` 注入同一对。
5. 浏览器页面没有当前 agent 会话注入；Web 的 bind/rebind-self 必须禁用或返回与普通终端相同的缺失身份错误。Web 只能叫机器人、rebind-launch、读席位。

## 4. 账本原子写面

在 `internal/ledger` 新增以下精确签名；它们是唯一的协调者席位写入口：

```go
func (s *Store) BindSeat(id, identity string, source proto.SeatSource) error
func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string) error
```

语义按入口拆开：

1. `BindSeat` 只接受空座；先验证 `identity` 和 `source`，再在同一 mutate 事务内读当前两列、确认两列均空、写入两列和认领时刻。当前有合法或非法席位都返回 `ErrCASConflict`，不自动升级。
2. `RebindSeat` 只接受非空当前席位；`expect` 是当前 `driver_session` 的原始字节值，必须精确相等，否则 `ErrCASConflict` 且不改任何列。`expect` 不出 CLI/HTTP 用户 flag，由服务层从当前账本读数构造。
3. `RebindSeat` 成功在同一事务覆写 `driver_session`、`driver_source`、`driver_heartbeat_at`，并落恰一条现有 `EvDriverTakeover`，payload 仍为 `{"from":旧身份,"to":新身份}`；事件 actor 为新身份字符串。
4. bind 不落新事件，沿用当前 `ClaimCard` 的无认领事件语义；coordinate 的来源由 `BindSeat` 在拉起成功后写入。
5. `ClaimCard`/`ClaimCardAs`、`TakeoverCard`、`ReleaseCard` 不得调用上述写面，也不得改变 `driver_session` 或 `driver_source`。运行锁和席位继续分立。
6. 失败必须保持当前席位和来源不变；终态只读、卡不存在等既有错误分类继续由账本域负责。

`internal/ledger/api/api.go#Facade` 不扩展 `internal/collab/client/client.go#LedgerClient`：协作域仍从 `GetCard/ListAllCards` 消费投影，控制面组装点使用已在目标图中登记的 `ledger.Store` 容器执行席位 CAS。这样不把换绑业务判断下沉进协作门面。

## 5. CLI 三颗按钮及非按钮入口

### 5.1 新增/保留命令

| 命令 | 精确行为 |
| --- | --- |
| `handoff card bind <id>` | 读当前会话身份；不查小队；调用 `Store.BindSeat(id, identity, SeatSourceBind)`；成功输出现有 `{"ok":true}`；身份缺失或座位非空退出非零 |
| `handoff card coordinate <id>` | 复用现有 `Client.CoordinatorLaunch(ctx,id)`，POST 协调者拉起；空座才走小队/载体/Launch 并以返回 `spec.CLI + result.SessionID` 写 `coordinate` 席位；已有任何席位返回冲突 |
| `handoff card rebind <id> --self` | 读当前会话身份；不查小队；通过控制面 rebind 接口以 `mode=self` 执行 `RebindSeat` 并清除 keystone 旧内存项 |
| `handoff card rebind <id> --launch` | 不接受用户 session id；通过控制面 rebind 接口以 `mode=launch` 读取当前席位、查协调者小队、Launch 新会话，再以旧席位 CAS 写入新 `coordinate` 席位 |
| `handoff card release <id>` | 保留命令名；空座幂等成功，非空席位退出非零并打印当前席位及 `rebind` 指引；不清席位 |
| `handoff card takeover <id>` | 保留命令名但不再把任意人尺度字符串写入席位；调用应退出非零并指向 `bind`/`coordinate`/`rebind`，不落 `EvDriverTakeover` |

`rebind` 的 `--self` 与 `--launch` 必须互斥且至少一个；删除 `--to`、`--carrier`、`--expect`，用户不能输入或隐藏传入任意 session id。空座 rebind 失败并指向 `bind` 或 `coordinate`。

### 5.2 建卡、领卡、派发与 wait

1. `card add`、`card move spec`、`note`、普通领卡和 `card wait` 不占座、不改来源、不拉起机器人。
2. `card add --coordinate` 不再注册；仍传旧 flag 必须失败，错误中指向 `handoff card coordinate <id>`。`coordinateAfterCreate` 删除，建卡不调用任何 launch。
3. `card dispatch --step` 先读账本：有合法或非法非空席位时必须用共享出示函数，出示不匹配则拒绝且不变座；空座时仍允许派发，但 actor 只作事件审计且不得写座。
4. `ClaimCard` 不再是派发占座路径；运行锁/节点执行的互斥不改变。
5. `card wait` 继续使用账本 `Follow`，不改为“只投给绑定会话的 wait 进程”。坐下成功只返回，后续由当前对话自己挂 wait。

## 6. HTTP 与 Web 接缝

### 6.1 HTTP

保留端点：

```text
POST /api/cards/{id}/coordinator/launch
GET  /api/cards/{id}/coordinator
POST /api/cards/{id}/coordinator/rebind
```

`POST .../coordinator/launch` 的 body 允许 `{}` 或 `{"source":"coordinate"}`；缺少 source 等同 `coordinate`。`manual`、`card_create` 和任何未知 source 均返回 400；端点只表示“叫机器人”，空座才执行。成功响应继续使用当前 `proto.CoordinatorLaunchResp`：`woke`、`session_id`、`rebuilt`、`escalated`、`output`，HTTP 状态继续 200。

新增 rebind 请求 DTO：

```go
type CoordinatorRebindReq struct {
	Mode     string `json:"mode"`     // self | launch
	Identity string `json:"identity,omitempty"` // mode=self 必填，服务端校验规范编码
}
```

`POST .../coordinator/rebind` 的成功响应复用 `CoordinatorLaunchResp`：`mode=self` 的结果不 Launch，`woke=false` 且 `session_id` 缺席；`mode=launch` 走一次 Launch 并返回新 session。`mode=self` 必须用当前会话身份且禁止浏览器伪造；Web 不调用它。两种 mode 在空座、CAS 冲突、无协调者小队、无名额、承载失败时分别沿现有 400/409/502/503 分类返回，不吞掉冲突。

`GET .../coordinator` 的 `bound` 判定顺序冻结为：读账本 `driver_session`/`driver_source`，只有合法席位才为 true；keystone 内存有 session 但账本无合法席位时为 false；账本有坐下席位而 keystone 内存为空时为 true。现有 `attach_active`/`attach` 字段语义不变，Attach 定位失败不把合法账本席位降为未绑定。

现有 `internal/client/coordinator.go:17-34` 的 `Client.CoordinatorLaunchAs` source 专用方法退役；保留 `internal/client/squads.go:78-94` 的 `Client.CoordinatorLaunch` 签名和 `{}` body 形状作为 CLI/Web 的 coordinate client。新 rebind client 精确为：

```go
func (c *Client) CoordinatorRebind(ctx context.Context, cardID string, req proto.CoordinatorRebindReq) (*proto.CoordinatorLaunchResp, error)
```

### 6.2 Web

TS 镜像新增/修改为：

```ts
export type SeatSource = 'bind' | 'coordinate'
export interface Card {
  // ...现有字段
  driver_session?: string
  driver_source?: SeatSource
  driver_heartbeat_at?: string
}

export type CoordinatorRebindMode = 'self' | 'launch'
export interface CoordinatorRebindReq {
  mode: CoordinatorRebindMode
  identity?: string
}

export const launchCoordinator: (cardId: string) => Promise<CoordinatorLaunchResp>
export const rebindCoordinatorLaunch: (cardId: string) => Promise<CoordinatorLaunchResp>
```

`launchCoordinator` 只发 `{}`；不再暴露 `manual/card_create`。卡抽屉的“一键拉起”改名“叫机器人”，已有席位时点击返回冲突并指向换绑；增加“换绑：叫机器人”调用 `rebindCoordinatorLaunch`。浏览器不展示可用的“坐下/换绑：我来接”动作，或显示不可用及当前会话缺失文案；它不把 `web:<addr>` 变成席位。

`NewCardDialog` 删除 `coordinate` 状态、checkbox、`launchCoordinator(created.id, 'card_create')` 循环和失败降级文案。设置页把“开卡即绑 / 一键拉起”改成“坐下 / 叫机器人”。卡抽屉、会话列表继续以 `driver_session` 展示席位，并同时展示 `driver_source` 的用户词“坐下/叫机器人”。

## 7. Keystone、唤醒与房间

### 7.1 Keystone 来源执法

`Service.Wake` 和 `Service.LaunchForCard` 的现有公共签名保持不变，生产 source 词表只有 `coordinate`；`bind` 不得进入 LaunchForCard。Wake 每次先从 `LedgerView.GetCard` 读卡：

1. 空座或非法旧席位：不因内存缺失自动 Launch；记录可行动错误或按现有人工升级路径返回。
2. `driver_source=bind`：返回 `Woke=false` 的 no-op；不调用 `Runner.Resume`、`Runner.Launch`，不隔离 HOME。送达靠该对话挂的 `card wait`。
3. `driver_source=coordinate`：使用账本身份解析出的 CLI/session；内存有 ref 时 Resume，内存无 ref 时用当前调度解析出的 `SessionSpec` 补 HOME/Workdir/Model 后 Resume；Resume 失败才按现有隔离 HOME 重建并把新 session 结果交回 agentd 做 CAS 更新。
4. `rebind --self` 成功后，组装点必须删除 `sessions[card]`；旧机器人不能再次 Resume。
5. `rebind --launch` 成功后，`LaunchForCard` 的新 ref 与账本新席位一致；agentd 重启后仍以账本席位为源恢复 Resume，而不是把缺失内存当空座。

`coordinatorRunner.Resume` 继续经 `resumeTurnRequest` 传 `CLI/SessionID/HomeDir/Workdir/Model`，并追加 `HANDOFF_SESSION_CLI/ID`；环境值不进入日志。`SessionSpec.Env` 既有追加环境语义保留，不增加新的全局超时或连接策略。

### 7.2 房间与 Pending

`VerifyWriter`、`Service.Send`、`Pending`、`Consume` 签名不变，矩阵只替换比较值：

1. 协调者类 kind 仅允许 `actor == card.DriverSession`，且该值必须是合法席位身份。
2. `relay` 仍允许本卡席位或直接父卡席位；平级卡继续拒绝。
3. `user`、群房间非空 actor 规则不变；`web:<addr>` 可以继续是普通 Web 用户/审计 actor，但不是协调者席位。
4. `Pending(consumer)` 与 `ListAllCards` 比较同一个规范字符串；`@B号` 仍把消息投给该卡席位，跨卡平级仍走项目群。

## 8. 常量、对侧执法与依赖行为

| 常量/字面值 | 当前生产者 | 当前消费者 | B312 结论 |
| --- | --- | --- | --- |
| `manual` | `internal/agentd/coordapi.go:93-108` 的 `handleCoordLaunch` 缺省值；`CoordinatorPanel` 和 `Client.CoordinatorLaunch` 的现状调用语义 | 同一 HTTP handler 与现有测试 | 旧 source 退役；新 launch 缺省改为 `coordinate`，不得继续写审计来源 |
| `card_create` | `cmd/card_coordinate.go#coordinateAfterCreate`、`NewCardDialog` | `handleCoordLaunch` source 校验、相关 CLI/Web 测试 | 疑似漂移并正式退役；删除生产者与消费者分支，任何请求均 400 |
| `driver_takeover` | `Store.RebindDriver`、`Store.TakeoverCard`、运行锁抢占 `internal/ledger/runlock.go` | ledger 事件读取与现有测试 | 常量继续存在；仅新 `RebindSeat` 的显式换绑可以落卡席位的 `from/to`，`TakeoverCard` 不再落席位事件；运行锁事件语义不改 |
| `ledgerActor()` | `cmd/ledgercli.go#ledgerActor`；agentd `web:<addr>` 在 `internal/agentd/ledgerapi.go#Server.ledgerActor` | 事件 actor、普通 user 操作、旧审计 | 继续可消费，但从席位出示链移除 |
| `web:<addr>` | `internal/agentd/ledgerapi.go` 与房间/step legacy fallback | 普通 Web 事件和未带 actor 的旧 step | 不是席位；浏览器 bind/rebind-self 不得使用 |
| `driver_leases` / `DriverLeaseTTL` | `internal/ledger/binding.go#RenewDriverLease` 等 | 活性展示和旧测试 | 本卡零新增生产者/消费者，不接新席位身份 |

依赖行为只沿用已查证的现状，不引入新库：`internal/client/client.go#Client.do` 用调用方 context 构造 `http.NewRequestWithContext`（`:398-424`），body 非空时由 `json.Marshal` 编码并设置 `Content-Type: application/json`（`:403-423`）；非 2xx 错误体仍由 `io.LimitReader(..., 4096)` 读取（`:447-454`）。`handleCoordLaunch` 现用 `json.NewDecoder(r.Body).Decode`（`internal/agentd/coordapi.go:95-101`），本卡只增加 source/mode 校验，不凭印象添加全局超时、保活、重定向或请求体策略。

## 9. 原子冻结清单

每一条都是独立 pass/fail 断言：

1. `EncodeSeatIdentity("codex", "thread-01")` 返回 `cli:codex#thread-01`。
2. `ParseSeatIdentity("cli:codex#thread-01")` 返回 `codex` 与 `thread-01`。
3. `ParseSeatIdentity("cli:user@host")` 返回错误。
4. `ParseSeatIdentity` 拒绝缺少 `#` 的非空值。
5. `ParseSeatIdentity` 拒绝空 CLI 分段。
6. `ParseSeatIdentity` 拒绝空 session 分段。
7. `ParseSeatIdentity` 拒绝含多个 `#` 的值。
8. `SeatSource` 只接受 `bind` 与 `coordinate`。
9. `cards.driver_source` 在 PostgreSQL DDL 中存在。
10. `cards.driver_source` 在 SQLite DDL 中存在。
11. 存量迁移为旧 `cards` 表补 `driver_source` 列且重复执行幂等。
12. 新规范席位仍写 `driver_session` 列。
13. 新规范席位同时写 `driver_source` 列。
14. 非法旧 `driver_session` 被读作占用态而不是空座。
15. 非空身份与空/未知来源的组合被判为非法席位。
16. `BindSeat` 只接受两列都为空的卡。
17. `BindSeat` 传入合法身份后写来源 `bind`。
18. `BindSeat` 传入合法身份后写来源 `coordinate`。
19. 已有合法席位时 `BindSeat` 返回 `ErrCASConflict`。
20. 已有非法旧席位时 `BindSeat` 返回 `ErrCASConflict`。
21. `RebindSeat` 的 `expect` 按旧 `driver_session` 原始字节比较。
22. `RebindSeat` CAS 不符时不改变身份。
23. `RebindSeat` CAS 不符时不改变来源。
24. `RebindSeat` 成功只落一条 `EvDriverTakeover`。
25. `RebindSeat` 事件 payload 的 `from` 是旧身份。
26. `RebindSeat` 事件 payload 的 `to` 是新身份。
27. `ClaimCard` 不改变席位。
28. `ClaimCardAs` 不改变席位。
29. `TakeoverCard` 不改变席位。
30. 非空席位上的 `ReleaseCard` 不改变席位。
31. `card add` 不调用 coordinator launch。
32. 传入 `card add --coordinate` 失败并指向 `card coordinate`。
33. 空座 `card bind` 不查协调者小队。
34. 空座 `card coordinate` 只调用一次 `LaunchForCard`。
35. 非空席位上的 `card coordinate` 在 Launch 前返回冲突。
36. `rebind --self` 不调用 `LaunchForCard`。
37. `rebind --self` 成功后删除 `sessions[card]`。
38. `rebind --launch` 不接受任意用户 session id。
39. `rebind --launch` 成功后账本身份使用新回合返回的 session id。
40. `POST .../coordinator/launch` 缺 source 时按 `coordinate` 处理。
41. `POST .../coordinator/launch` 收到 `manual` 时返回 400。
42. `POST .../coordinator/launch` 收到 `card_create` 时返回 400。
43. HTTP coordinate 空座成功后账本来源为 `coordinate`。
44. HTTP coordinate 非空座位不启动第二个机器人。
45. `GET .../coordinator` 的 `bound` 只由合法账本席位决定。
46. 只有 keystone 内存而没有账本席位时 `bound=false`。
47. 只有坐下账本席位而没有 keystone 内存时 `bound=true`。
48. `Wake` 读到 `bind` 来源时不调用 `Runner.Resume`。
49. `Wake` 读到 `bind` 来源时不调用 `Runner.Launch`。
50. `Wake` 读到 `coordinate` 来源时内存有 ref 走 `Runner.Resume`。
51. `Wake` 读到 `coordinate` 来源时 Resume 失败才允许 `Runner.Launch`。
52. `Pending` 用同一席位字符串匹配 consumer。
53. 另一张卡席位发本卡协调者 kind 返回 `ErrNotWriter`。
54. 一级父卡席位仍可发子卡 `relay`。
55. 平级卡席位发本卡 `relay` 返回 `ErrNotWriter`。
56. 有席位的 `--step` 出示不匹配时拒绝。
57. 空座的 `--step` 可以成功且不写席位。
58. `card wait` 继续走 `Follow`，不按席位过滤。
59. Web 新建对话框不再调用 `launchCoordinator`。
60. Web 抽屉的叫机器人调用 source-free `launchCoordinator`。
61. Web 抽屉的换绑机器人调用 `rebindCoordinatorLaunch`。
62. Web 不以 `web:<addr>` 作为协调者席位。

## 10. 可执行冻结与本轮验证

本轮已落并实际运行身份编码金样本：

```text
go test ./internal/proto/... -run 'TestSeatIdentity' -count=1
```

结果：`ok github.com/Xsxdot/handoff/internal/proto 0.002s`，退出码 0。该测试锁住第 1–7 条，后续改动编码语法会使它变红。

卡存储/HTTP/房间/唤醒的金样本尚未在本节点实现，不能冒充已验证；实现节点必须逐条补齐第 9 节中对应测试并实际运行。欠账明确为：

- [ ] `driver_source` 双方言 DDL、存量迁移和 `Card` 两处 wire 投影金样本；
- [ ] `BindSeat/RebindSeat` 原子 CAS、旧非法席位及事件 payload 金样本；
- [ ] CLI 三颗按钮、旧 `--coordinate`、takeover/release 负例；
- [ ] HTTP launch/rebind/status 与 Web 新建不拉起金样本；
- [ ] VerifyWriter/Pending/Consume 与 Keystone Wake 来源分支金样本。

这是实现节点欠账，不是本节点静默放行；本节点没有宣称这些运行时行为已完成。

## 11. 三重闸门拍板记录

1. **复用 `driver_session` 作为规范身份字符串，并以 `#` 编码 CLI/session 对。** 这会同时牵动账本 DDL/投影、房间比较、step actor、CLI 和 Web，回改需要多域迁移；没有本上下文时后人会自然新增 `driver_cli`/`driver_session_id` 或保留人尺度字符串；被否方案是新席位表和双真相字段，它们会让 CAS、房间和列表再次分叉。不做第二席位真相源，不自动迁移 `cli:user@host`。
2. **叫机器人先 Launch、成功后以返回 session id 做账本 CAS，坐下完全不经小队。** 新会话的 id 由承载 CLI 在 Launch 后产生，无法在启动前用当前人身份预占；这个时序跨调度、keystone、账本和失败回收，后人看到“空座仍需先 Launch”会想把它改成隐式 reserve；被否方案是预写载体名/占位 session 或让坐下走小队。不做用户可见任意 session id；冲突以账本 CAS 和控制面错误呈现。
3. **当前会话只由 `HANDOFF_SESSION_CLI` + `HANDOFF_SESSION_ID` 注入，不从 USER/hostname/PID 推导。** 这是 CLI、无头机器人后续 handoff 和房间写权的跨进程身份边界；没有本上下文时复用 `ledgerActor()` 看似兼容但会让同机多会话碰撞；被否方案是人尺度 actor、`web:<addr>` 或隐式 PID。不做浏览器伪造席位，不做第三种 wait-only 投递身份。

## 12. 图与移交 plan 附区

### 图冻结

`codegraph/target.json` 本轮更新了既有 `d_cli→d_ledger`、`d_cli→d_protocol`、`d_gateway→d_ledger`、`d_gateway→d_keystone`、`d_keystone→d_ledger`、`d_collab→d_ledger`、`d_collab→d_protocol` 契约说明：席位写入归 ledger.Store；keystone/collab 只经既有接口消费；身份 helper 归 proto。无新的 Web 图边可冻结，因为当前图没有 Web target contract，且 Web scheduling 符号未被图覆盖。

Ticket 0 新符号 `proto.SeatSource`、`proto.EncodeSeatIdentity`、`proto.ParseSeatIdentity`、校验 helper 已随本提交写入 `codegraph/diffs/cards-B312-charter.json`；未创建空视图文件之外的其它 diff。`best.json` 结构树未改。

本轮本地 codegraph 检查命令：

```text
codegraph --repo . check --view cards-B312-charter | jq -c '{fails,legacyHits}'
```

结果 `fails=[]`；当前已有的 legacy 计数和前端容器归属 warnings 未由 B312 改写。官方 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo .` 因模块缓存所在文件系统只读而失败，原始错误已记入 `docs/superpowers/ledgers/2026-09-03-b312-contract-ledger.md`，本轮使用本机已有二进制并未把失败命令写成成功结论。

### 移交 plan 附区（不计冻结条目）

- 由 plan 吸收：`driver_source` 双方言 DDL/ALTER 迁移与 `cardColumns/scanCard` 的机械同步。
- 由 plan 吸收：控制面按卡串行化 Launch→座位 CAS 的冲突/孤儿会话处置；不得把未成功 CAS 的返回结果当作已绑定。
- 由 plan 吸收：CLI 当前会话环境读取、coordinator Resume 环境合并、失败文案和旧 flag 删除的文件级拆分。
- 由 plan 吸收：房间/列表/卡抽屉中 `driver_source` 的用户词投影；不另造新页面或新布局。

## 13. 本节点法定产出与欠账声明

- 契约增量文档：本文件；每个现状签名均带 `file#Symbol` 与现状行号。
- 目标图：`codegraph/target.json` 已更新，并与本文件同提交冻结。
- Ticket 0：协议身份编码骨架已编译，`TestSeatIdentity` 金样本本轮通过；L3 轻档无直通竖切。
- 金样本：仅身份编码第 1–7 条本轮实跑；第 9 节其余运行时条目逐项列为实现节点欠账。
- 三重闸门：本文件第 11 节记录 3 项命中决定；没有其它未记录的三重闸门决定。
