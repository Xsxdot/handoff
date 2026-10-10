# B432 实现计划 —— iOS 系统推送「需要你」（APNs MVP）

- **卡**：B432（L2；plan → implement → review → acceptance → finish）
- **spec**：`docs/superpowers/specs/b432-spec.md`（已批准；本计划逐字核对过文件头）
- **基线**：当前分支 `cards/B432-charter` @ `845a7e80`（工作树起点；合并目标未设置，需合并时向协调者确认）
- **读者假设**：执行者对代码库零上下文、品味存疑。凡本计划未给代码的实现点，执行者必须先在「§7 占位符扫描」里找到对应声明；找不到即为 plan failure，停下来提问。
- **本节点产出物**：本文件 + `docs/superpowers/plans/b432-plan-ledger.md`。**不写实现代码、不建脚手架。**

---

## 0. 事实基线（plan 节点亲自复核，非记忆）

台账逐条见 `b432-plan-ledger.md`。以下每条都是本轮在 `845a7e80` 工作树上读到的**真码事实**，plan 的每个判据都建立在其上。

### 0.1 两个事件库、两条 append 路径（本卡最大的结构事实）

本仓有**两个独立的持久化库**，各自有事件表与 append 路径；「需要你」四源分居两边：

| 库 | 表 | append 入口 | 现有旁路缝 | 拥有的事件 |
|---|---|---|---|---|
| `internal/store.Store`（`s.st`） | `events`（`proto.Event`） | 导出 `AppendEvent`（`internal/store/store.go:778`） | `SetEventHook(func(proto.Event))`（`store.go:860`，**已被 `EventFrameHook` 占用**） | `permission_request` / `question` / `approver_decision` / `ticket_answered` 及任务生命周期 |
| `internal/ledger.Store`（`s.ledger`） | `card_events`（`ledger.Event`） | **未导出** `appendEvent`（`internal/ledger/events.go:20`） | `OnEvent(func(seq int64))`（`internal/ledger/store.go:189`，**仅 SQLite、只给 seq、当前无生产调用方**）；PG 走 `pg_notify('card_events', seq)`（`events.go:43`） | `decision_opened` / `room_message`（mention 载体）/ `needs_human` / `needs_cleared` / `review_verdict` / `status_moved` / `task_mirrored` |

**推论（承重）**：spec 说「扇出钩在既有账本写成功点旁路」。**只挂 `store.SetEventHook` 会漏掉 decision / mention / needs_human**（它们在 `card_events`）。而 `store.SetEventHook` 是**单回调**（`store.go:860-864`），新挂会顶掉 `EventFrameHook`（`internal/agentd/server.go:349` 注册）。本计划据此定下两条旁路（见 §3.2 / Task T5）。

### 0.2 「需要你」四源与判定现状

- 收件箱三源聚合的唯一现役判定在 `internal/agentd/roomsapi.go:532` `handleInbox`：
  - 源① decision：`s.ledger.ListDecisions(true)`（roomsapi.go:547）——`ListDecisions` 定义 `internal/ledger/decisions.go:93`，写入点 `decisions.go:53` 落 `EvDecisionOpened="decision_opened"`。
  - 源② ticket：`s.mgr.ListTasks()` 过滤 `TaskStateWaitingAnswer` + `PendingTickets`（roomsapi.go:567-593）；工单行写入 `internal/store/store.go:1018` `CreateTicket`（**不落事件**），真正的通知事件在创建后单独 append：`permission_request` 在 `internal/orchestration/manager.go:2151` 与 `internal/approval/client.go:476`；`question` 在 `manager.go:2960`。
  - 源③ mention：`s.rooms.Mentions(member,0,0)`（roomsapi.go:597）——mention 无独立事件类型，是 `room_message` 载荷里的 `Mentions []string`（`internal/proto/rooms.go:33`）；写入点 `internal/ledger/rooms.go:528` 落 `EvRoomMessage="room_message"`。
  - 可选第四源 needs_human：`EvNeedsHuman="needs_human"`（`internal/ledger/types.go:68`），写入点 `internal/ledger/events.go:458`（`MarkNeedsHuman`，定义 `events.go:450`）。
- 事件类型常量：`internal/proto/proto.go` 的 `EventTypePermissionRequest="permission_request"`、`EventTypeQuestion="question"`；`internal/ledger/types.go` 的字符串常量 `EvDecisionOpened/EvRoomMessage/EvNeedsHuman/EvNeedsCleared/...`。**不存在** `mention` 事件类型，也没有 `proto.EventType` 版的 needs_human。
- 成员身份记法：`proto.MemberIdentity(proto.IdentityKindUser, name)` → `"user:<name>"`（`internal/proto/identity.go:19,45`）。收件箱的 member 来自 `requireConsoleIdentity`（`roomsapi.go:52` → `resolveConsoleIdentity`，`internal/agentd/identity.go:38`，读 `cfg.ConsoleUser`）。

### 0.3 协调机账本全流消费（唯一方言无关的全流读点）

- `internal/agentd/scheddrain.go:54` `StartAutomation` → `automationLoop`（`:68`，节拍 `automationPollInterval=2s`，`:25`）→ `internal/agentd/wakeconsumer.go:636` `consumeAutomationEventsOnce`：`s.autoLedger.EventsFromAsc(nil, from, 500)`（wakeconsumer.go:649）**逐条读全流**，在 `automationWakeEvents`（`:300`）里分类。
- `ledger.Store.EventsFromAsc`（`internal/ledger/events.go:64`）PG/SQLite 两方言通用。
- **本计划选它作为 `card_events` 侧的扇出旁路**（理由与代价见 §3.2）。

### 0.4 REST / store / 配置 / 测试的既有约定

- 路由注册：`internal/agentd/server.go:726` `Handler()`，方法+路径模式 `api.HandleFunc("POST /api/...", ...)`；子注册函数 `registerLedgerRoutes`（`internal/agentd/ledgerapi.go:32`）。`writeJSON`（`server.go:997`）、`writeErr`（`ledgerapi.go:98`）。
- 鉴权：`s.auth`（`server.go:899`）先 Bearer（`cfg.Token`）后 cookie。iOS 壳持**主令牌**，走 Bearer，得到空 `identity{}`；`requireConsoleIdentity` 另需 `cfg.ConsoleUser` 配好（fail-closed）。
- store 建表：`internal/store/store.go:84-251` 一个 `[]string` DDL 切片，逐条 `CREATE TABLE IF NOT EXISTS`；`fmtTime`/`parseTime`（`store.go:1406/1411`）；`rowScanner` 接口；`ErrNotFound`（`store.go:40`）。Upsert 先例 `internal/store/workbench.go:95`。
- 配置：`internal/config/config.go:43-152` `Config`；**strict 解码 `KnownFields(true)`，新键必须 `omitempty`**（`config.go:66-69` 反复强调）。`Target`/`RelayConfig` 在 `config.go:181-287`。
- 客户端：`internal/client/client.go:1274` `IssueAuthTicket`（`POST /api/auth/tickets`，`c.do`+`c.httpError`，`do` 未导出 `:382`）。
- 移动核：`internal/mobilecore/core.go:95-105` `machine{... cl *client.Client ...}`；`New`（`:108`）、`Pair`（`:126`）、`Origin`（`:334`）；`probeReachable`（`:307`）是「发一次请求判可达」先例。
- 绑定面：`mobile/bind/bind.go`（`Pair/MachineCount/MachineAt/Origin/Close` + `session.go` 的 `SessionCookie/SwitchMachine`）；**导出面被金样本冻结**：`mobile/bind/export_surface_test.go:21-29`（Go 签名集）、`mobile/bind/shell_api_golden_test.go:20-55`（ObjC/Java 签名集）、`mobile/bind/export_surface_test.go:77-107`（禁 `internal/client|relay|proto` 入 bind）。
- iOS 壳：`mobile/ios/HandoffMobile/`。`AppDelegate.swift:5-16` 无任何推送代码；`ConnectCore.swift:5-13` 是壳↔核协议（7 方法），`:18` `LiveConnectCore` 包 `Bind*`；`CookieBridge.swift:101` `enter` 序为 `switchMachine→clearJar→sessionCookie→set→load`；`AppComposition.swift` 组装单例。
- iOS 源码 guard（**本卡必须处理**）：`mobile/ios/HandoffMobileTests/SourceGuardTests.swift:34-39` 禁止壳源码出现子串 `Token`/`Dial`/`Credential`；`:49` 禁止 `URLSession`；`:42-47` 禁 `JSONDecoder`/`JSONSerialization`；`:6-9` 绑定符号白名单。

### 0.5 依赖事实（判据要在基线上能跑）

- `go.mod` 无 APNs/JWT/HTTP2 专用依赖。APNs 客户端用 **stdlib**：`net/http`（https 自动 HTTP/2）、`crypto/ecdsa`+`crypto/x509`+`crypto/rand`+`crypto/sha256`+`encoding/base64`+`encoding/json` 手搓 ES256 JWT。**不新增依赖**。
- 图覆盖债：`codegraph sym handleInbox / EventFrameHook / OnEvent / IssueAuthTicket / NewServer / startLoopback` 均命中（见台账）。**但图视图 `baseline` 对 `internal/agentd/roomsapi.go` 的 `handleInbox` 读数与工作树不一致**（图里是 `s.st.ListTasks()`/`s.roomUserActor(r)`，工作树是 `s.mgr.ListTasks()`/`id.Member`）——**图相对本工作树已陈旧**，本计划一律以工作树真码为准；记入覆盖债（台账）。`codegraph context push` 被拒（`push` 不在最优树词表），按纪律降级为现状词表，本卡新增符号为未来首建，记入图覆盖债。

---

## 1. 交付目标与 Out of scope

### 1.1 交付目标（spec §验收逐条映射）

| spec 验收 | 做到哪里算完（机内可判） | 真机项 |
|---|---|---|
| ①何时推：仅 iOS；仅当产生「需要你」待办；前台已读不重复弹 | agentd 侧：四源（decision/ticket/mention/needs_human）任一新事实 → 内部 Fanout 产出通知并经 `PushSender` 发出（fake 断言）；壳侧：前台且落在相关面时 `willPresent` 返回空（纯函数单测） | 真机：横幅真实弹出/抑制 |
| ②点进落哪：点通知落对应会话/卡；无深链落工作台「需要你」入口；角标与未处理数一致 | 通知携带 `deep_link`（有 `card_id` → `/cards?card=<id>`，否则 `/`）；`badge` = 该 member 当前收件箱条数（复用 `collectInboxItems` 计数） | 真机：点按跳转、角标刷新 |
| ③站内铃铛：有既有 inbox/角标就对齐计数；拒权/无 token 静默降级，不报假送达 | `badge` 取自既有 `/api/inbox` 同源聚合；无 APNs 配置 / 无设备 / APNs 410 → 不发送且**不报成功**（fanout 只 Warn，接口不返回假送达） | 真机：拒权后 App 不崩、不显示假角标 |

### 1.2 Out of scope（spec §Out of scope 原文，本计划不越界）

安卓 / FCM / 中国区厂商通道 / 聚合推送 / 强达；自建长连；改 bind/配对信封/`Session.DeviceName` 挂 token；为站内铃铛单开范围；事件源收窄到仅 `needs_human`。**本卡不动 bind 的配对语义与配对信封**——只在 bind **新增一个**推送登记导出方法（属于「设备登记」契约面，spec 明列）。

---

## 2. 架构户口与依赖方向

- **不新增顶层子系统**。落点沿用 `codegraph/best.json` 现有域：
  - 设备登记 REST + store：`d_gateway`（`internal/agentd`）+ `d_ledger`/`d_gateway` 共享的 `internal/store`（`internal/store` 现属 `d_gateway` 叶子，见 best.json 既有映射）。
  - 内部 Fanout + APNs 客户端：`d_gateway`（agentd 出站副作用）。
  - 通知 wire 类型：`d_protocol`（`internal/proto`）。
  - 客户端/移动核：`d_transport_channel`（`internal/client`、`internal/mobilecore`，与 B369 同域）。
  - 绑定面：`mobile/bind`（**图外**，嵌套 module，与 B369 同例）。
  - iOS 壳：`mobile/ios`（**图外**，嵌套工具链）。
- **依赖方向**：`d_gateway → d_protocol`（既有方向）、`d_gateway → internal/store`（既有）、`d_transport_channel → d_protocol`（既有，B369 已补 entry）。**不新增跨子系统方向**。
- **反向依赖禁止**：根模块不得 import `mobile/`（`moduleisolation_test.go`）；`internal/mobilecore` 不得 import `internal/agentd`（B369 条 24）。

---

## 3. 契约增量（定签名，供各 task 的 Interfaces 段逐字引用）

### 3.1 通知 wire（`internal/proto/push.go`，本卡新建）

```go
package proto

import "time"

// PushPlatformIOS 是本 MVP 唯一平台。
const PushPlatformIOS = "ios"

// PushDevice 是一台已登记推送的设备（spec DeviceRegistration）。
type PushDevice struct {
	Member        string    `json:"member"`
	DeviceID      string    `json:"device_id"`
	Platform      string    `json:"platform"`
	APNSToken     string    `json:"apns_token"`
	AuthSessionID string    `json:"auth_session_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PushDeviceRegisterReq 是 POST /api/push/devices 请求体。
// member 不由请求体提供——服务端按控制台身份注入（决策 D3）。
type PushDeviceRegisterReq struct {
	DeviceID      string `json:"device_id"`
	Platform      string `json:"platform"`
	APNSToken     string `json:"apns_token"`
	AuthSessionID string `json:"auth_session_id,omitempty"`
}

// PushDeviceDeleteReq 是 DELETE /api/push/devices 请求体。
type PushDeviceDeleteReq struct {
	DeviceID string `json:"device_id"`
}

// PushNotification 是内部 Fanout 的通知载荷（agentd → APNs，不对外）。
type PushNotification struct {
	EventType string `json:"event_type"`
	Member    string `json:"member"`
	Title     string `json:"title"`
	CardID    string `json:"card_id,omitempty"`
	RefID     string `json:"ref_id"`
	DeepLink  string `json:"deep_link,omitempty"`
}
```

### 3.2 内部 Fanout（agentd → APNs）

```go
// PushSender 是 fanout 与 APNs 之间的唯一接缝：把一条通知投递给一个 APNs token。
// 生产实现是 APNsSender；测试注入 fake。返回错误必须可分类（410=设备失效）。
type PushSender interface {
	Send(ctx context.Context, token string, n proto.PushNotification, badge int) error
}

// ErrPushUnregistered 表示 APNs 回 410（token 已失效）——fanout 据此删设备，不重试。
var ErrPushUnregistered = errors.New("push: 设备 token 已失效")

// PushFanout 是「需要你」事实到 APNs 的扇出器。
type PushFanout struct { /* 私有 */ }

func NewPushFanout(st *store.Store, sender PushSender, memberOf func() string, log *slog.Logger) *PushFanout
func (f *PushFanout) Start(ctx context.Context)              // 起 worker goroutine；ctx 取消即停
func (f *PushFanout) Notify(n proto.PushNotification)        // 非阻塞入队；队满丢弃并 Warn（不挡事件写）
func (f *PushFanout) OnLedgerEvent(ev proto.LedgerEvent)     // card_events 侧分类入口
func (f *PushFanout) OnTaskEvent(e proto.Event)              // store.events 侧分类入口
func (f *PushFanout) SetBadgeCounter(fn func(member string) int) // 由装配点后置注入（收件箱同源计数）
```

### 3.3 设备登记 REST（对外）

```
POST   /api/push/devices   body=proto.PushDeviceRegisterReq   → 200 {"ok":true} | 400 | 503
DELETE /api/push/devices   body=proto.PushDeviceDeleteReq     → 200 {"ok":true} | 404
```

两者都经 `s.auth`（Bearer）+ `requireConsoleIdentity`（成员服务端注入）。

### 3.4 客户端 / 移动核 / 绑定面（壳登记 token 的唯一通路）

```go
// internal/client/client.go
func (c *Client) RegisterPushDevice(ctx context.Context, req proto.PushDeviceRegisterReq) error
func (c *Client) DeletePushDevice(ctx context.Context, deviceID string) error

// internal/mobilecore/core.go
func (c *Core) RegisterPush(ctx context.Context, machine, deviceID, token string) error

// mobile/bind/bind.go（导出面新增一条；goldens 同步）
func RegisterPushDevice(machine, deviceID, pushHandle string) error
```

> **为什么经 Go 核而不是壳直接 HTTP**：`SourceGuardTests.swift:49` 禁壳源码出现 `URLSession`（回环门禁承重属性，B369 条 36）。壳拿不到 agentd 主令牌（guard 禁 `Token`），协议逻辑零重实现（B369 P4=A）——所以 token 必须交给核，由核用它持有的 `client.Client` 发 HTTP。

### 3.5 配置增量（`internal/config/config.go`）

```go
// PushConfig 是 APNs MVP 的服务端凭据与开关。全部 omitempty（strict 解码硬要求）。
type PushConfig struct {
	APNsKeyID    string `yaml:"apns_key_id,omitempty"`
	APNsTeamID   string `yaml:"apns_team_id,omitempty"`
	APNsBundleID string `yaml:"apns_bundle_id,omitempty"`
	APNsKeyFile  string `yaml:"apns_key_file,omitempty"` // .p8 私钥路径
	APNsHost     string `yaml:"apns_host,omitempty"`     // 空=api.push.apple.com；测试指 httptest
}
// Config 增字段：Push PushConfig `yaml:"push,omitempty"`
// 未配齐（key/team/bundle/keyfile 任一空）→ fanout 停用（无 sender），不报错、不发送。
```

### 3.6 计划决策（无 contract 节点，plan 自行裁定并留痕；review 可退）

- **D1｜`card_events` 侧扇出挂在自动化消费循环上**（`consumeAutomationEventsOnce`），而非新增 ledger hook。理由：`ledger.OnEvent` 只给 seq 且仅 SQLite（PG 走 `pg_notify`），要拿全量事件必须再读一次；自动化循环已逐条读全流且方言无关。代价：push 与唤醒循环耦合（循环依赖 keystone/autoLedger 装配）。**退路**：若 review 认为耦合不可接受，改为新增 `ledger.Store.SetCardEventHook(func(proto.LedgerEvent))` 并由 `mutate` 在提交后携带全量事件触发——那是 ledger 契约面增量，须回 contract 复议。
- **D2｜`store.events` 侧扇出与 `EventFrameHook` 合成同一 `SetEventHook` 回调**。`SetEventHook` 单回调，合成是唯一不丢帧钩子的写法。
- **D3｜设备登记的 `member` 服务端注入，不走请求体**。依据 `roomsapi.go` 文件头纪律「actor/成员标识服务端注入，不经请求体」+ `requireConsoleIdentity` 既有门。spec 表把 `member` 列为实体字段（本计划保留在存储/响应实体里），但**谁写**取服务端。**review 若判 spec 要求壳上报 member，则退 D3**（壳须先经 `GET /api/identity` 取 console_user）。
- **D4｜`badge` 与 `deep_link` 由 agentd 计算**。badge 复用收件箱同源计数（`collectInboxItems`），deep_link 有 `card_id` → `/cards?card=<id>`（`Shell.tsx:921` 既有深链），否则 `/`（工作台）。
- **D5｜APNs 载荷 custom key 为 `handoff`**，内嵌 `PushNotification` 的 JSON。iOS 壳从 `userInfo["handoff"]` 取 `deep_link`。

---

## 4. 子任务 DAG

```text
T1 proto wire（d_protocol，无前置）
  ├─> T2 push_devices 表 + store CRUD + REST（d_gateway/store；缝 S1/S2）
  ├─> T6 client + mobilecore + bind 登记通路（d_transport_channel；缝 S1，经核）
  └─> T3 分类 + PushFanout（d_gateway；缝 S3）
        └─> T4 APNs sender + PushConfig（d_gateway/d_policy；缝 S3/APNs 边界）
              └─> T5 装配（合成 store 钩子 + 喂自动化循环 + bootstrap 构造）（缝 S3 集成）
T6,T5 ─> T7 iOS 壳 APNs + SourceGuard 修订（图外；缝 S1，经 bind；真机）
```

T2 与 T3 可并行（T3 只用 T1 的 wire 与 T2 的 store 读方法；若并行，T3 先按 §3.2 签名桩测分类，store 读方法在 T2 落地后接）。T6 只依赖 T1（wire）与 T2（端点存在，集成测试用 fake agentd 不依赖 T2 编译）。为降低并行冲突，**建议串行 T1→T2→T3→T4→T5→T6→T7**。

---

## 5. 任务详情

> 每个实现类 task 均含：①判据基线复核 ②测试范围声明 ③日志步骤 ④注释步骤 ⑤Interfaces（Consumes/Produces 精确签名）。红绿模板只套在锁缝断言步骤上。

### T1 — proto wire 类型与金样本

**①判据基线复核**：`go test ./internal/proto/... -count=1` 在基线绿（`pairing_fixture_test.go`/`rooms_fixture_test.go` 已在）。本 task 新增 `push_fixture_test.go`，基线跑红前先确认 `go test ./internal/proto/...` 无本 task 文件时退出 0。

**②测试范围声明**：只跑 `go test ./internal/proto/... -count=1`。不跑全量。

**③日志步骤**：纯 DTO，无运行时日志（叶子类型，无行为）。本 task 不配日志步骤（显式声明：DTO 无执行路径，无可观测点）。

**④注释步骤**：文件头写职责+边界（wire 唯一定义处、壳不直接解析、member 服务端注入）；每个导出类型写「谁编谁解」。

**⑤Interfaces**
- Consumes：无。
- Produces（`internal/proto/push.go`）：§3.1 全部类型与 `PushPlatformIOS`。

**文件**：`internal/proto/push.go`（新建）、`internal/proto/push_fixture_test.go`（新建）。

**步骤**：
1. 写 `push_fixture_test.go`（先红）：`TestPushDeviceRegisterReqGoldenKeys` 断言 `json.Marshal(proto.PushDeviceRegisterReq{...})` 的键集恰为 `{device_id,platform,apns_token,auth_session_id}`（`auth_session_id` 空则省键，用 `map[string]json.RawMessage` 解回断键）；`TestPushNotificationRoundTrip` 断言 `json.Unmarshal(json.Marshal(n))==n`（含 `CardID`/`DeepLink` 空的省键语义：空值 marshal 后键缺失，解回仍是空串——**用 `map` 断键存在性区分「缺失」与「零值」**）。跑 `go test ./internal/proto/... -run TestPush` → RED（类型不存在）。
2. 写 `internal/proto/push.go`（§3.1 全文）。跑 `go test ./internal/proto/... -run TestPush` → GREEN。
3. 跑 `go test ./internal/proto/... -count=1` 全绿。

**缺陷族对抗**：序列化边界（见 §7）。跨平台：JSON 是中立格式，但 `UpdatedAt time.Time` 的 RFC3339 序列化在壳侧（不直接解，跳过）；Go↔Go 无时区风险。

**内部锁声明**：无（T1 无测试入口在缝上——但 T1 是纯类型，其断言在 T2/T6 的缝级测试里被真实穿过；本 task 的 roundtrip 是**附加**锁，不顶替任何缝级断言）。

---

### T2 — `push_devices` 表 + store CRUD + 设备登记 REST（缝 S1/S2）

**①判据基线复核**：基线 `go build ./...` 退出 0；`internal/store` 现无 `push` 相关符号（`grep` 证实，台账）；`internal/agentd` 路由表无 `/api/push/devices`（`server.go:726-834` 复核）。验收命令 `go test ./internal/store/... ./internal/agentd/... -run 'Push' -count=1` 在基线跑红（符号不存在）——**这就是「最薄路径条」**：本卡要锁的登记行为今天从声明缝（`POST /api/push/devices`）调用**得不到**，故 T1/T2 是点亮它的最薄可跑路径。

**②测试范围声明**：只跑 `go test ./internal/store/... -run Push -count=1` 与 `go test ./internal/agentd/... -run Push -count=1`。

**③日志步骤**：`handlePushDeviceRegister`/`handlePushDeviceDelete` 入口 Debug（method/path/device）、成功 Info（member/device/platform，**不落 apns_token**）、失败 Warn（cause）。store 叶子方法不打成功日志（沿用 store.go:13 纪律），错误由 handler 带上下文记录。

**④注释步骤**：`internal/store/push.go` 文件头写职责+边界（叶子、不判平台合法性）；`UpsertPushDevice` 写「按 (member,device_id) 覆盖」；handler 写「member 服务端注入（D3）」。

**⑤Interfaces**
- Consumes：`proto.PushDevice`/`PushDeviceRegisterReq`/`PushDeviceDeleteReq`/`PushPlatformIOS`（T1）；`s.st`（`*store.Store`，`server.go:110`）；`requireConsoleIdentity`（`roomsapi.go:52`）；`writeJSON`/`writeErr`。
- Produces：
  - `internal/store/push.go`：`func (s *Store) UpsertPushDevice(d *proto.PushDevice) error`、`func (s *Store) DeletePushDevice(member, deviceID string) error`、`func (s *Store) ListPushDevices(member string) ([]proto.PushDevice, error)`。
  - `internal/agentd/pushapi.go`：`func (s *Server) registerPushRoutes(api *http.ServeMux)`、`func (s *Server) handlePushDeviceRegister(w http.ResponseWriter, r *http.Request)`、`func (s *Server) handlePushDeviceDelete(w http.ResponseWriter, r *http.Request)`。
  - `server.go` Handler() 增 `s.registerPushRoutes(api)`（在 `registerCoordRoutes` 后，`server.go:834`）。

**文件**：`internal/store/store.go`（改：DDL 切片加一张表）、`internal/store/push.go`（新建）、`internal/store/push_test.go`（新建）、`internal/agentd/pushapi.go`（新建）、`internal/agentd/pushapi_test.go`（新建）、`internal/agentd/server.go`（改：一行注册）。

**DDL（追加进 `store.go:84-251` 的切片，紧跟 `workbench_singletons`）**：
```sql
`CREATE TABLE IF NOT EXISTS push_devices (
  member          TEXT NOT NULL,
  device_id       TEXT NOT NULL,
  platform        TEXT NOT NULL,
  apns_token      TEXT NOT NULL,
  auth_session_id TEXT NOT NULL DEFAULT '',
  updated_at      TIMESTAMP NOT NULL,
  PRIMARY KEY (member, device_id))`,
```
（`store.Open` 只有一份 DDL 切片，无 PG/SQLite 双份——本表在 `internal/store`，不触发 `internal/ledger` 的 `TestDDLDialectParity`。）

**store 方法骨架（完整实现，无占位）**：见 §3.2 台账之外，此处给全：
```go
const pushDeviceColumns = "member, device_id, platform, apns_token, auth_session_id, updated_at"

func scanPushDeviceRow(sc rowScanner) (proto.PushDevice, error) {
	var d proto.PushDevice
	var updatedAt string
	if err := sc.Scan(&d.Member, &d.DeviceID, &d.Platform, &d.APNSToken, &d.AuthSessionID, &updatedAt); err != nil {
		return proto.PushDevice{}, err
	}
	d.UpdatedAt = parseTime(updatedAt)
	return d, nil
}

// UpsertPushDevice 按 (member, device_id) 幂等登记/覆盖。UpdatedAt 为零值时取 now。
func (s *Store) UpsertPushDevice(d *proto.PushDevice) error {
	if d.UpdatedAt.IsZero() { d.UpdatedAt = time.Now() }
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO push_devices (member, device_id, platform, apns_token, auth_session_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(member, device_id) DO UPDATE SET
  platform = excluded.platform, apns_token = excluded.apns_token,
  auth_session_id = excluded.auth_session_id, updated_at = excluded.updated_at`,
		d.Member, d.DeviceID, d.Platform, d.APNSToken, d.AuthSessionID, fmtTime(d.UpdatedAt)); err != nil {
		return fmt.Errorf("登记推送设备 %s/%s: %w", d.Member, d.DeviceID, err)
	}
	return nil
}

// DeletePushDevice 删除；不存在返回 ErrNotFound。
func (s *Store) DeletePushDevice(member, deviceID string) error {
	res, err := s.db.ExecContext(context.Background(),
		"DELETE FROM push_devices WHERE member = ? AND device_id = ?", member, deviceID)
	if err != nil { return fmt.Errorf("删除推送设备 %s/%s: %w", member, deviceID, err) }
	n, err := res.RowsAffected()
	if err != nil { return fmt.Errorf("读取删除推送设备影响行数: %w", err) }
	if n == 0 { return fmt.Errorf("推送设备 %s/%s: %w", member, deviceID, ErrNotFound) }
	return nil
}

// ListPushDevices 列出某成员全部设备，按 updated_at 降序。
func (s *Store) ListPushDevices(member string) ([]proto.PushDevice, error) {
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT "+pushDeviceColumns+" FROM push_devices WHERE member = ? ORDER BY updated_at DESC", member)
	if err != nil { return nil, fmt.Errorf("查询成员 %s 的推送设备: %w", member, err) }
	defer rows.Close()
	var out []proto.PushDevice
	for rows.Next() {
		d, err := scanPushDeviceRow(rows)
		if err != nil { return nil, fmt.Errorf("读取推送设备行: %w", err) }
		out = append(out, d)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("遍历推送设备: %w", err) }
	return out, nil
}
```

**handler 骨架（完整实现）**：
```go
// registerPushRoutes 注册推送设备登记端点（spec §契约面 1）。iOS 壳持主令牌
// 走 Bearer；成员由 requireConsoleIdentity 服务端注入（决策 D3）。
func (s *Server) registerPushRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/push/devices", s.handlePushDeviceRegister)
	api.HandleFunc("DELETE /api/push/devices", s.handlePushDeviceDelete)
}

func (s *Server) handlePushDeviceRegister(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok { return }
	var req proto.PushDeviceRegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json")); return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.APNSToken = strings.TrimSpace(req.APNSToken)
	if req.DeviceID == "" || req.APNSToken == "" || req.Platform != proto.PushPlatformIOS {
		writeErr(w, http.StatusBadRequest, errors.New("device_id/apns_token 必填，platform 必须为 ios")); return
	}
	dev := proto.PushDevice{Member: id.Member, DeviceID: req.DeviceID, Platform: req.Platform,
		APNSToken: req.APNSToken, AuthSessionID: req.AuthSessionID, UpdatedAt: time.Now()}
	if err := s.st.UpsertPushDevice(&dev); err != nil {
		s.log.Warn("登记推送设备失败", "device", req.DeviceID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err); return
	}
	s.log.Info("推送设备已登记", "member", id.Member, "device", req.DeviceID, "platform", req.Platform)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePushDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok { return }
	var req proto.PushDeviceDeleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json")); return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("device_id 必填")); return
	}
	if err := s.st.DeletePushDevice(id.Member, req.DeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, errors.New("设备未登记")); return
		}
		s.log.Warn("删除推送设备失败", "device", req.DeviceID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err); return
	}
	s.log.Info("推送设备已删除", "member", id.Member, "device", req.DeviceID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

**测试（缝级 S1/S2，走真 HTTP）**：`internal/agentd/pushapi_test.go`：
- 用 `newTestAgentdEnv(t)`（`w3a_testhelpers_test.go:44`）+ `setConsoleUser(t, env, "sy")`（`identity_face_test.go:31`）。
- `TestPushDeviceRegisterAndList`：POST（Bearer）`{"device_id":"d1","platform":"ios","apns_token":"aabb"}` → 200 `{"ok":true}`；`env.st.ListPushDevices("user:sy")` 得 1 条且字段逐字一致（**缝 S1**）。
- `TestPushDeviceRegisterIsIdempotent`：同 `device_id` 再 POST 换 token → 仍 1 条且 token 为新值（upsert 覆盖）。
- `TestPushDeviceRegisterRejectsBadInput`：platform=`android` → 400；空 `apns_token` → 400。
- `TestPushDeviceDelete`：先登记再 DELETE → 200；`ListPushDevices` 空；再 DELETE → 404（**缝 S2**）。
- 负面：未配 `console_user` 时 POST → 403（fail-closed，`requireConsoleIdentity` 既有行为）。

**store 单测（附加内部锁，走真 SQLite）**：`internal/store/push_test.go`：`TestUpsertPushDeviceOverwrites`（同键覆盖、UpdatedAt 更新）、`TestDeletePushDeviceNotFound`（`errors.Is(err, ErrNotFound)`）、`TestListPushDevicesScopedByMember`（不同 member 不串）。**声明**：这三支入口是 store 方法（不在缝上），属**附加**锁，不顶替上面两支缝级断言——合法理由：REST 缝的断言已覆盖 upsert/delete/list 的端到端行为，store 单测只补「member 隔离」这类从缝构造需多设备多成员的等价断言；**不是**「更短/更纯」的理由，是「member 隔离在单 HTTP 请求里构造不出（一个 agentd 只有一个 console_user）」这一**构造不出**的形状。

**步骤（红绿）**：
1. 写 `push_test.go` + `pushapi_test.go` → `go test ./internal/store/... ./internal/agentd/... -run Push -count=1` RED。
2. 加 DDL + `push.go` + `pushapi.go` + 一行路由注册 → 同命令 GREEN。
3. `go build ./...` 退 0。

---

### T3 — 「需要你」分类 + `PushFanout`（缝 S3）

**①判据基线复核**：`grep -rn "PushFanout\|PushSender" internal/` 基线零命中（台账）。四源写入点行号见 §0.2（本轮复核）。`handleInbox` 现为单函数，`collectInboxItems` 抽取前基线 `go test ./internal/agentd/... -run Inbox -count=1` 绿。

**②测试范围声明**：只跑 `go test ./internal/agentd/... -run 'Push|Inbox' -count=1`。

**③日志步骤**：`Notify` 队满 Warn（丢弃，含 event_type/member）；worker 发送前 Debug（device/event_type，**不落 token 明文，只落 device_id**）、发送失败 Warn（device/status/cause）、410 时 Info（设备已失效，删除）；成功 Info（device/event_type）。分类未命中不打日志（高频）。

**④注释步骤**：`pushfanout.go` 文件头写职责+边界（旁路、非阻塞、不重造判定、复用收件箱源）；`Notify` 写「非阻塞、队满丢弃」；`classify*` 写「映射表对齐 handleInbox 三源 + needs_human（spec §架构边界）」。

**⑤Interfaces**
- Consumes：`proto.PushNotification`/`PushDevice`（T1）；`store.Store.ListPushDevices`/`DeletePushDevice`（T2）；`PushSender`（§3.2，T4 提供生产实现）；`proto.LedgerEvent`/`proto.Event`。
- Produces：
  - `internal/agentd/pushfanout.go`：`type PushSender interface{...}`、`var ErrPushUnregistered`、`type PushFanout`、`NewPushFanout`、`(*PushFanout).Start/Notify/OnLedgerEvent/OnTaskEvent/SetBadgeCounter`。
  - `internal/agentd/roomsapi.go`：抽 `func (s *Server) collectInboxItems(member string) ([]proto.InboxItem, error)`（`handleInbox` 改调它；返回既有三源聚合）。
  - 分类纯函数：`func classifyLedgerEvent(ev proto.LedgerEvent, consoleMember string) (proto.PushNotification, bool)`、`func classifyTaskEvent(e proto.Event, consoleMember string) (proto.PushNotification, bool)`。

**分类映射表（判据锚，逐条可判）**：

| 输入（事件 type） | 触发? | EventType | Title | DeepLink |
|---|---|---|---|---|
| `decision_opened` | 是 | `decision` | 裁决正文首行（复用 `decisionTitle`） | `card_id` 非空→`/cards?card=<id>`，否则 `/` |
| `permission_request` | 是 | `ticket` | `权限工单待答复` | `/cards?card=<task→card>` 若可解析，否则 `/` |
| `question` | 是 | `ticket` | `提问工单待答复` | 同上 |
| `room_message` 且 `Mentions` 含 `user:<console>` | 是 | `mention` | `@你：<body 截断>` | `/` |
| `needs_human` | 是 | `needs_human` | `卡等待人工` | `card_id`→`/cards?card=<id>` |
| `needs_cleared`/`review_verdict`/`status_moved`/`task_mirrored`/`approver_decision`/`ticket_answered`/其它 | 否 | — | — | — |

- `room_message` 的 `Mentions` 解析：`json.Unmarshal(ev.Payload, &proto.RoomMessage)`；`Mentions` 里逐项比对 `user:<consoleMember>`（`proto.MemberIdentity(proto.IdentityKindUser, console)`）。只对含该成员的 mention 产通知（spec：给被 @ 的人推）。
- 成员：decision/ticket/needs_human → `consoleMember`；mention → 命中的那个 `user:*`。
- `RefID`：decision→`strconv.FormatInt(d.ID,10)` 不可得（事件载荷里是 `decision_id`，取 `payload.decision_id`）；mention→`strconv.FormatInt(ev.Seq,10)`；needs_human→`ev.Seq`；ticket→`e.TaskID`（permission/question 事件无 ticket_id 于本计划所需，RefID 用 task 标识）。**（`decision_opened` 载荷 `{"decision_id":..,"body":..,"options":..}` 见 `decisions.go:53`；`needs_human` 载荷 `{"reason":..}` 见 `events.go:458`。）**

**`PushFanout` 行为（完整语义）**：
- `NewPushFanout`：`ch = make(chan proto.PushNotification, 64)`；`badgeCounter` 默认返回 0。
- `Notify`：`select { case f.ch <- n: default: f.log.Warn("推送队列已满，丢弃", ...) }`（**非阻塞**，不挡事件写）。
- `Start(ctx)`：起 worker：`for { select { case <-ctx.Done(): return; case n := <-f.ch: f.deliver(ctx, n) } }`。
- `deliver`：`devices, err := f.st.ListPushDevices(n.Member)`；逐个 `f.sender.Send(ctx, dev.APNSToken, n, f.badgeCounter(n.Member))`；`errors.Is(err, ErrPushUnregistered)` → `f.st.DeletePushDevice(n.Member, dev.DeviceID)`；其余错误只 Warn（**不报假送达**——接口层不感知 fanout 结果）。
- `OnLedgerEvent(ev)`：`n, ok := classifyLedgerEvent(ev, f.memberOf()); if ok { f.Notify(n) }`。
- `OnTaskEvent(e)`：`n, ok := classifyTaskEvent(e, f.memberOf()); if ok { f.Notify(n) }`。

**测试（缝 S3，入口 = `PushFanout.Notify` 与 `OnLedgerEvent`/`OnTaskEvent`）**：`internal/agentd/pushfanout_test.go`：
- `TestPushFanoutDeliversToRegisteredDevices`：真 SQLite + `setConsoleUser` 语义下，`st.UpsertPushDevice` 两台设备；fake `PushSender` 记录调用；`f.Notify(n)` 后（用同步点：`sender.done` channel 或 `f.Start` + 等待）断言 fake 收到 2 次且 `badge` 等于注入的 `badgeCounter`（**缝 S3 主断言**）。
- `TestPushFanoutClassifyLedger`：表驱动，逐条喂 §上表输入，断言 `classifyLedgerEvent` 的 (notification, ok) 与表逐字一致（含 `needs_cleared` 等**不触发**的反例）。
- `TestPushFanoutClassifyTask`：`permission_request`/`question` 触发，`approver_decision`/`ticket_answered` 不触发。
- `TestPushFanoutDeletesOnUnregistered`：fake sender 对某 token 回 `ErrPushUnregistered` → `st.ListPushDevices` 后该设备被删。
- `TestPushFanoutNotifyNeverBlocks`：不 `Start`、填满 64 条后再 `Notify` 一次 → 立即返回（不 panic/不阻塞），Warn 有记录。

**`collectInboxItems` 抽取（不改行为）**：把 `handleInbox`（roomsapi.go:532-616）里三源聚合段原样移入 `collectInboxItems(member)`，`handleInbox` 改为 `items, err := s.collectInboxItems(id.Member); if err != nil { ...500... }; writeJSON(...)`。保留错误分流（decision/mention 失败 500、ticket 源失败降级 continue）。**回归**：`go test ./internal/agentd/... -run Inbox -count=1` 保持绿（`TestInboxThreeSources` 等）。

**步骤（红绿）**：
1. 写 `pushfanout_test.go` + 分类断言 → RED。
2. 写 `pushfanout.go`（含 fake 无关的实现）→ GREEN。
3. 抽 `collectInboxItems` → `-run Inbox` 回归绿。
4. `go build ./...` 退 0。

---

### T4 — APNs sender + `PushConfig`（缝 S3 / APNs 边界）

**①判据基线复核**：`go.mod` 无 JWT/APNs 依赖（本轮复核，台账）；stdlib `net/http` 对 https 自动 HTTP/2（Go 标准行为，`crypto/tls` 的 `NextProtos` 含 h2——本 task 用 `httptest.NewTLSServer` 不强制 h2，判据只钉**请求形状**，真实 h2 归真机）。`internal/config` strict 解码（`config.go` `KnownFields(true)`）——新键必须 omitempty。

**②测试范围声明**：只跑 `go test ./internal/agentd/... -run APNs -count=1` 与 `go test ./internal/config/... -run Push -count=1`。

**③日志步骤**：`APNsSender.Send` 入口 Debug（host/device 索引，**不落 token**）、非 200 Warn（status/body 摘要，**body 不含 token**）、JWT 签名失败 Error。成功不打日志（高频，由 fanout 打）。

**④注释步骤**：`apns.go` 文件头写职责+边界（只发不收、JWT 缓存、不重试）；`Send` 写「410→ErrPushUnregistered」「非 200 原文进错误」。

**⑤Interfaces**
- Consumes：`proto.PushNotification`（T1）；`PushSender` 接口与 `ErrPushUnregistered`（T3）；`config.PushConfig`（本 task）。
- Produces：
  - `internal/config/config.go`：`type PushConfig`（§3.5）+ `Config.Push`。
  - `internal/agentd/apns.go`：`type APNsSender struct{...}`、`func NewAPNsSender(cfg config.PushConfig, hc *http.Client, log *slog.Logger) (*APNsSender, error)`、`func (s *APNsSender) Send(ctx context.Context, token string, n proto.PushNotification, badge int) error`。
  - `func loadAPNsKey(path string) (*ecdsa.PrivateKey, error)`（PEM→PKCS8→`*ecdsa.PrivateKey`）。
  - `func (s *APNsSender) bearerToken(now time.Time) (string, error)`（ES256 JWT，缓存 ≤50min）。

**实现要点（完整，无占位）**：
- `NewAPNsSender`：校验 key/team/bundle 非空、`loadAPNsKey`；`host` 空则 `api.push.apple.com`。
- JWT：header `{"alg":"ES256","kid":keyID}`、claims `{"iss":teamID,"iat":now.Unix()}`；`signingInput=base64url(header)+"."+base64url(claims)`；`sig=ecdsa.Sign(rand.Reader, key, sha256(signingInput))`，**raw r||s 各 32 字节**（P-256）→ base64url；token = `signingInput+"."+sig`。缓存到 `now+50min`。
- `Send`：`POST https://<host>/3/device/<token>`，头 `authorization: bearer <jwt>`、`apns-topic: <bundleID>`、`apns-push-type: alert`、`apns-priority: 10`；body：
```json
{"aps":{"alert":{"title":"<title>"},"badge":<badge>,"sound":"default","thread-id":"handoff-needs-you"},
 "handoff":{"event_type":"...","member":"...","title":"...","card_id":"...","ref_id":"...","deep_link":"..."}}
```
（`handoff` 内嵌 `proto.PushNotification` 的 JSON。）响应：200→nil；410→`ErrPushUnregistered`；其余→`fmt.Errorf("APNs 投递失败 status=%d body=%s", resp.StatusCode, truncate(body,200))`（body 摘要，**不回显 token**）。

**测试（缝级：`APNsSender.Send` 打 httptest 假 APNs）**：`internal/agentd/apns_test.go`：
- `TestAPNsSenderRequestShape`：`httptest.NewServer`，`cfg.APNsHost` 指向它（去掉 scheme，`Send` 用 `http://` 还是 `https://`？→ `Send` 按 host 前缀决定：host 含 `://` 用它，否则拼 `https://`；测试传 `http://127.0.0.1:port`）。断言：路径 `/3/device/<token>`、头 `apns-topic`/`apns-push-type`/`authorization: bearer ` 前缀、body 的 `aps.badge`==badge、`handoff.deep_link`==n.DeepLink。**变异可红**：把 header 名改错即红。
- `TestAPNsSenderMaps410`：假服务器回 410 → `errors.Is(err, ErrPushUnregistered)`。
- `TestAPNsSenderNon200IsError`：回 400 → 非 nil、错误含 status。
- `TestLoadAPNsKeyRejectsBadPEM`：畸形 PEM → 错误。
- JWT 自检（可选，钉算法）：`bearerToken` 解出 header 的 `alg=="ES256"`、claims 的 `iss==teamID`（用 base64url 手工解，不引 JWT 库）。
- `internal/config/config_test.go`：`TestPushConfigOmitemptyRoundTrip`——空 `PushConfig` marshal 后**无 `push` 键**（strict 解码硬要求）。

**步骤（红绿）**：
1. 写 `apns_test.go` + config 测试 → RED。
2. 写 `PushConfig` + `apns.go` → GREEN。
3. `go build ./...` 退 0。

---

### T5 — 装配：合成 store 钩子 + 喂自动化循环 + bootstrap（缝 S3 集成）

**①判据基线复核**：`registerEventFrameHook`（`server.go:348`）现独占 `SetEventHook`；`consumeAutomationEventsOnce`（`wakeconsumer.go:636`）逐条 `events` 循环（`:663`）——本 task 在循环里加一次 `s.pushFanout.OnLedgerEvent(ev)`。基线 `go test ./internal/agentd/... -run 'Frame|Automation' -count=1` 绿。

**②测试范围声明**：只跑 `go test ./internal/agentd/... -run 'Push|Frame|Automation' -count=1`。

**③日志步骤**：`NewServer` 构造 fanout 成功 Info（`push_enabled=true/false`，**不落凭据**）；`registerEventFrameHook` 合成后 Info（一次）。fanout 自身日志见 T3。

**④注释步骤**：合成钩子处写「单回调，必须同时跑帧钩子（D2）」；自动化循环注入点写「旁路：只入队，不挡唤醒（D1）」。

**⑤Interfaces**
- Consumes：`NewPushFanout`（T3）、`NewAPNsSender`（T4）、`store.SetEventHook`、`consumeAutomationEventsOnce`。
- Produces：
  - `Server` 增字段 `pushFanout *PushFanout`（`server.go:101-256` 结构体内）。
  - `NewServer` 内构造（§步骤）：`cfg.Push` 配齐 → `NewAPNsSender` + `NewPushFanout`，否则 fanout 的 sender 为 nil（`Notify` 仍入队但 deliver 时 `sender==nil` 直接返回——**静默降级**）；`SetBadgeCounter(s.pushBadgeCount)` 后置注入（`pushBadgeCount` 调 `collectInboxItems` 取 len，nil-guard）。
  - `registerEventFrameHook` 改为：`frame := orchestration.EventFrameHook(...); s.st.SetEventHook(func(e proto.Event){ frame(e); if s.pushFanout != nil { s.pushFanout.OnTaskEvent(e) } })`。
  - `wakeconsumer.go` 循环内（`automationWakeEvents` 调用后，`:699` 附近）：`if s.pushFanout != nil { s.pushFanout.OnLedgerEvent(ev) }`（**在分类/seen 判定之外，保证 needs_human 这类不唤醒事件也推送**）。
  - `cmd/agentd.go` bootstrap：`srv.StartPush(ctx)`（新方法，转调 `pushFanout.Start(ctx)`）——或复用 `StartAutomation` 附近调用；`srv` 构造在 `NewServer` 已完成 fanout 构造。

**测试（缝 S3 集成：真写点 → fake sender）**：`internal/agentd/pushfanout_wiring_test.go`（或并入 pushapi_test）：
- `TestTaskEventHookTriggersPush`：用 `newTestAgentdEnv` + 注入 fake `PushSender` 到 `env.srv.pushFanout`（white-box，同包测试）；`env.st.AppendEvent("t1", proto.EventTypeQuestion, ...)` → 等待 fake 收到 1 条 `event_type=="ticket"`（**缝 S3 集成**）。
- `TestLedgerNeedsHumanTriggersPush`：`env.ledger.MarkNeedsHuman("B1","reason","test")` 后手动调一次 `env.srv.consumeAutomationEventsOnce(ctx)`（不靠 ticker）→ fake 收到 `event_type=="needs_human"`。**注**：`consumeAutomationEventsOnce` 依赖 `autoLedger`/`keystone` 装配，测试用 `newLedgerEnv`（`ledgerapi_test.go:134`）形态；若装配过重，退路是直接测 `OnLedgerEvent` 的调用点（白盒断言 `pushFanout.OnLedgerEvent` 被调）——但**首选真链**。
- `TestFrameHookStillRuns`：合成后 `AppendEvent` 仍写 frames（回归，防顶掉 `EventFrameHook`）。

**步骤（红绿）**：
1. 写集成测试 → RED（无 `pushFanout` 字段/无注入）。
2. 加字段、合成钩子、循环注入、bootstrap `Start` → GREEN。
3. `go test ./internal/agentd/... -count=1` 全绿；`go build ./...` 退 0。

---

### T6 — client + mobilecore + bind 设备登记通路（缝 S1，经核）

**①判据基线复核**：`client.Client` 无 `RegisterPushDevice`（台账）；`mobilecore.Core` 无 `RegisterPush`；`bind` 导出面金样本 `export_surface_test.go:21-29`/`shell_api_golden_test.go:20-55` 当前 7 方法。基线 `cd mobile && go test ./...` 绿。

**②测试范围声明**：`go test ./internal/client/... ./internal/mobilecore/... -run 'Push' -count=1` 与 `cd mobile && go test ./... -count=1`。

**③日志步骤**：`client.RegisterPushDevice` Debug（path/device，**不落 token**）；`mobilecore.RegisterPush` 入口 Debug（machine/device）、失败 Warn（machine/cause，**不落 token**）；`bind.RegisterPushDevice` 不自行打日志（核已打）。

**④注释步骤**：`RegisterPushDevice`（client）写「POST /api/push/devices，Bearer 即主令牌」；`Core.RegisterPush` 写「经该机已配对 client，与 Origin 同源」；`bind` 写「壳上报 APNs token 的唯一入口，不得导出 Token 字样之外的门禁绕过面」。

**⑤Interfaces**
- Consumes：`proto.PushDeviceRegisterReq`（T1）；`client.Client`（`core.go:101` machine.cl）；`bind` 的 `liveCore`/`coreAPI`。
- Produces：
  - `internal/client/client.go`：`func (c *Client) RegisterPushDevice(ctx context.Context, req proto.PushDeviceRegisterReq) error`（`c.do(ctx, POST, "/api/push/devices", req)`，200→nil，否则 `c.httpError`）；`func (c *Client) DeletePushDevice(ctx context.Context, deviceID string) error`（`c.do(ctx, DELETE, "/api/push/devices", proto.PushDeviceDeleteReq{DeviceID: deviceID})`）。
  - `internal/mobilecore/core.go`：`func (c *Core) RegisterPush(ctx context.Context, machine, deviceID, token string) error`（取 `c.machines[machine].cl`，未配对/离线返回错误，调 `cl.RegisterPushDevice`）。
  - `mobile/bind/bind.go`：`func RegisterPushDevice(machine, deviceID, pushHandle string) error`（转调 `core.RegisterPush`）。
  - goldens 更新：`export_surface_test.go` 的 `wantBindSurface` 加 `"func RegisterPushDevice(machine string, deviceID string, pushHandle string) error"`；`shell_api_golden_test.go` 的 `wantObjCShellAPI` 加 `FOUNDATION_EXPORT BOOL BindRegisterPushDevice(NSString* _Nullable machine, NSString* _Nullable deviceID, NSString* _Nullable pushHandle, NSError* _Nullable* _Nullable error);`、`wantJavaShellAPI` 加 `public static native void registerPushDevice(String machine, String deviceID, String pushHandle) throws Exception;`（**实施时以 `gomobile bind` 真实产物为准核对生成名——若本机无 gomobile，golden 按 B369 冻结形态推定，并记「未验证，需真机 CI」**）。

**测试（缝 S1，经核）**：`internal/mobilecore/core_test.go`（改）：
- `TestRegisterPushWalksAgentd`：用既有 fake-relay 竖切夹具起一台在线机（`core_test.go` 既有 `TestPairVerticalSlice` 形态），其上游 handler 断言收到 `POST /api/push/devices` 且 body 的 `device_id`/`apns_token` 逐字一致、`Authorization` 为 token（**缝 S1**）；`Core.RegisterPush` 返回 nil。
- `TestRegisterPushUnknownMachine`：未配对 machine → 错误。
- `internal/client/push_test.go`：`TestRegisterPushDevicePostsJSON`（httptest 断路径/方法/Bearer/body）、`TestDeletePushDevice`。
- `cd mobile && go test ./...`（含更新后的 `export_surface_test.go`/`shell_api_golden_test.go`）绿。

**步骤（红绿）**：
1. 写测试 + 改 goldens → RED（方法不存在/面漂移）。
2. 加 client/mobilecore/bind 方法 → GREEN。
3. `go build ./...` + `cd mobile && go build ./...` 退 0。

---

### T7 — iOS 壳 APNs + SourceGuard 修订（图外；真机）

**①判据基线复核**：`AppDelegate.swift:5-16` 无推送；`SourceGuardTests.swift:34-39` 禁 `Token` 子串、`:49` 禁 `URLSession`；`project.pbxproj` 无 entitlements、`CODE_SIGNING_ALLOWED=NO`（`:239` 等）。**本 task 无法在 linux 工作树跑 XCTest**（无 Xcode）——机内判据只有：新增 Swift 文件通过 `SourceGuardTests`（**未验证，需 Xcode/真机 CI**）与静态形态核对；真实推送归真机清单。

**②测试范围声明**：XCTest 全套归 Xcode（真机/CI）；linux 工作树不跑。

**③日志步骤**：用既有 `Log.shell`（`Log.swift:5-7`，`os.Logger`）——获权结果 Info、token 上报失败 Warn（**不落 token 明文，只落 device_id 与错误**）、点通知 Info（deep_link）、`willPresent` 抑制 Debug。

**④注释步骤**：新文件头写职责+边界；`AppDelegate` 扩展写「获权→注册→拿 token→交核上报」链；`willPresent` 写「前台且落在相关面 → 不弹（spec 验收①）」。

**⑤Interfaces**
- Consumes：`bind.RegisterPushDevice`（T6，经 `LiveConnectCore`）；`ConnectCore`（`ConnectCore.swift:5-13`）；`AppComposition`；`CookieBridge.enter`。
- Produces（Swift，新增/改）：
  - `ConnectCore` 协议加 `func registerPushDevice(machine: String, deviceID: String, pushHandle: String) throws`；`LiveConnectCore` 转调 `BindRegisterPushDevice(...)`。
  - 新 `PushRegistrar.swift`：`final class PushRegistrar`，`requestAuthorizationAndRegister()`（`UNUserNotificationCenter.requestAuthorization` → 成功则 `UIApplication.shared.registerForRemoteNotifications()`）、`func didRegister(pushHandle: Data)`（转 hex 字符串 → `core.registerPushDevice(machine: activeMachine, deviceID: deviceID, pushHandle: hex)`）、`func didFail(_ error: Error)`。
  - 新 `PushPresentation.swift`：`enum PushPresentation { static func shouldPresent(isForeground: Bool, onRelevantScreen: Bool) -> Bool }`（纯函数：`isForeground && onRelevantScreen` → false，否则 true）。
  - 新 `PushDeepLink.swift`：`static func route(from userInfo: [AnyHashable: Any]) -> String?`（取 `userInfo["handoff"]["deep_link"]`；无则 nil → 落 `/`）。
  - `AppDelegate` 实现 `UNUserNotificationCenterDelegate`：`willPresent` 用 `PushPresentation.shouldPresent`；`didReceive` 取 deep_link 经 `CookieBridge` 加载 origin+path。
  - `Support/Info.plist`：加 `UIBackgroundModes`?（MVP 不需 background；`aps-environment` 走 entitlements）。新 `Support/HandoffMobile.entitlements`：`aps-environment=development`；`project.pbxproj` 设 `CODE_SIGN_ENTITLEMENTS`（**真机项**，签名另办）。
  - **SourceGuard 修订**（必须，否则 `didRegisterForRemoteNotificationsWithDeviceToken` 命中 `Token` 子串使 guard 红）：`testNoCookieBypassSymbols` 从「禁子串 `Token`」改为「禁绑定面绕过符号」——禁 `BindToken`/`BindCredential`/`BindDial`/`AccessToken`/`AgentdToken`（正则 `\b(Bind)?(Token|Credential|Dial)[A-Za-z]*\b` 排除 APNs 语境），并保留一条负向断言：塞入 `BindToken` 即红。同时 `allowedBindSymbols` 加 `BindRegisterPushDevice`。

**测试（缝 S1，经 bind；真机 CI）**：`mobile/ios/HandoffMobileTests/`：
- `PushPresentationTests`：`shouldPresent(true,true)==false`；`(true,false)==true`；`(false,_)==true`（纯函数，XCTest）。
- `PushDeepLinkTests`：给定 userInfo 取 deep_link；缺失→nil。
- `PushRegistrarTests`：`FakeConnectCore` 记录 `registerPushDevice` 调用；`didRegister(pushHandle:)` 断言 machine/deviceID/hex 逐字。
- `SourceGuardTests`：修订后仍绿，且新增负向（`BindToken` 红）。
- **真机清单（未验证，需真机）**：①授权弹窗→拿 token→上报→agentd 收到；②后台收到横幅；③前台在相关面不弹、在别处弹；④点通知落 `/cards?card=<id>` 或工作台；⑤角标==未处理数；⑥拒权后无崩溃、无假角标。

**步骤**：
1. 改 `SourceGuardTests`（负向先红：塞 `BindToken` 临时源码 → RED；删 → GREEN）。
2. 加 Swift 文件 + `ConnectCore` 方法 + Info.plist/entitlements。
3. 静态核对（linux）：`grep` 确认壳源码无 `URLSession`/`JSONDecoder`；无 `Bind` 非白名单符号。
4. Xcode/真机跑 XCTest + 真机清单（**归真机**）。

---

## 6. 接缝覆盖（双向）

### 6.1 缝清单（对照 spec §契约面）

| 缝 | 定义 | 类型 |
|---|---|---|
| S1 | `POST /api/push/devices`（设备登记 REST） | 对外 REST |
| S2 | `DELETE /api/push/devices` | 对外 REST |
| S3 | 内部 Fanout：`PushFanout.Notify` / 事件→通知→`PushSender`（agentd→APNs） | 内部接缝 |

### 6.2 测试 → 缝（看入口符号）

| 测试 | 入口符号 | 缝 |
|---|---|---|
| `pushapi_test.go`（T2） | `http POST/DELETE /api/push/devices` | S1/S2 ✓ |
| `pushfanout_test.go`（T3） | `PushFanout.Notify` / `classify*` | S3 ✓ |
| `apns_test.go`（T4） | `APNsSender.Send` | S3（APNs 边界）✓ |
| `pushfanout_wiring_test.go`（T5） | `store.AppendEvent` / `ledger.MarkNeedsHuman` → fanout | S3 ✓ |
| `core_test.go` / `client/push_test.go`（T6） | `Core.RegisterPush` / `Client.RegisterPushDevice` | S1 ✓ |
| iOS XCTest（T7） | `ConnectCore.registerPushDevice` → `BindRegisterPushDevice` | S1 ✓ |

### 6.3 缝 → 测试（每条缝至少一支缝级断言）

- S1：T2 `TestPushDeviceRegisterAndList` + T6 `TestRegisterPushWalksAgentd` + T7 `PushRegistrarTests`。✓
- S2：T2 `TestPushDeviceDelete`。✓
- S3：T3 `TestPushFanoutDeliversToRegisteredDevices` + T4 `TestAPNsSenderRequestShape` + T5 两支集成。✓

### 6.4 内部锁（默认非法、只能附加）

- T1 roundtrip（`push_fixture_test.go`）：纯类型无缝入口。**声明**：附加锁，其断言被 T2/T6 缝级测试真实穿过；不顶替。
- T2 `push_test.go` store 单测：**声明**见 T2 末（member 隔离从单请求缝构造不出）。
- T3 `classify*` 表驱动：**声明**：分类函数入口 `classifyLedgerEvent`/`classifyTaskEvent` 不在 S1/S2/S3 三条缝的字面入口上，但它是 S3 缝的内部实现；已由 T5 真链测试穿过缝。作为附加锁保留（表驱动逐条可判），不顶替 S3 缝级断言。
- T4 JWT 自检：**声明**：附加锁，不顶替 `TestAPNsSenderRequestShape`。
- **无未声明的内部锁**；**无未声明的退路**（T5 的「首选真链/退路白盒」已在 T5 步骤里显式声明为条件退路，且退路入口仍是 `OnLedgerEvent`（S3 缝）——不构成内部锁）。

---

## 7. 序列化边界设问（新增字段从产生到消费逐处手写序列化/投影）

| 数据 | 产生 | 序列化/投影处 | 消费 | 断言 |
|---|---|---|---|---|
| `PushDeviceRegisterReq` | 壳（strings）→ 核（struct） | `client.do` 的 `json.Marshal`（`client.go:382`） | agentd `json.Decode` | T6 `TestRegisterPushDevicePostsJSON` + T2 端到端 |
| `PushDevice` | agentd handler | store `UpsertPushDevice`（列→值手搭）+ `scanPushDeviceRow` | `ListPushDevices`→fanout | T2 `TestUpsertPushDeviceOverwrites` + T3 deliver |
| `PushNotification` | 分类函数 | ① `client`/HTTP 无（内部）② APNs `handoff` 键 `json.Marshal` | iOS `userInfo["handoff"]` | T4 `TestAPNsSenderRequestShape`（断 `handoff.deep_link`）+ T7 `PushDeepLinkTests` |
| APNs `aps` payload | `APNsSender.Send` 手搭 map | `json.Marshal` | Apple | T4 body 断言 |
| bind 字符串参数 | 壳 | gomobile 生成桥 | 核 | T6 goldens + T7 |

**roundtrip 属性**：T1 `TestPushNotificationRoundTrip` 用 `map` 区分「缺失」与「零值」（`CardID`/`DeepLink` 空 → marshal 后键缺失；解回仍空串）。**一条穿过真实序列化边界的回归**：T4 的 `Send` 断 `handoff` 键内的 `deep_link`（Go 构造→JSON→假 APNs 解回），覆盖「两端各自有测试≠链路有测试」。

---

## 8. 缺陷族对抗审查（`defect-families` 逐族设问，结论入验收栏）

1. **生命周期/状态机中断**：fanout worker goroutine 随 `ctx` 取消退出（`Start(ctx)`）；`Notify` 队满丢弃不泄漏。设备删除幂等（`DeletePushDevice` 不存在→`ErrNotFound`，fanout 410 删两次第二次 404 被忽略——T3 用 `errors.Is` 判）。**验收**：T3 `TestPushFanoutNotifyNeverBlocks` + `-race`（见下）。
2. **静默失败/误导报错**：**承重**——「拒权/无 token 静默降级，不报假送达」（spec 验收③）。三处设问：①无 APNs 配置→`sender==nil`→deliver 直接返回（不报错，也不假装送达）；②设备 token 失效→410→删设备不重试（`ErrPushUnregistered`）；③登记接口不校验 APNs 真伪（只校验非空+platform）——**不返回「已送达」**，只返回「已登记」。**验收**：T4 `TestAPNsSenderMaps410`、T3 `TestPushFanoutDeletesOnUnregistered`。
3. **跨平台假设**：APNs 仅 iOS（platform 白名单强制 ios）；HTTP/2 在 stdlib 自动协商，但 `httptest` 不验 h2——**真实 h2 归真机**；iOS 前台/角标/深链行为**未验证，需真机**（T7 清单）。
4. **假红/假绿**：T4 假 APNs 必须真回 410（否则映射断言假绿）；T3 deliver 断言用同步点（`sender.done` channel）非 `time.Sleep`；T5 真链测试断言 `AppendEvent` 真触发（不是直接调 `Notify`）。**变异**：T4 改错 header 名→红；T5 去掉合成→帧钩子回归红。
5. **门禁绕过**：**承重**——绑定面不得暴露 agentd token（`export_surface_test.go` 已禁 `Token/Dial`；新增 `RegisterPushDevice` 不含 `Token` 字样但**参数 `pushHandle` 是 APNs token**——这是 APNs token 非 agentd token，命名刻意避开 guard）。iOS SourceGuard 修订必须**仍能抓真绕过**（负向 `BindToken` 红）。**验收**：T6 goldens + T7 SourceGuard 负向。
6. **序列化边界**：见 §7。
7. **枚举新值过既有白名单**：新增 `platform` 值白名单（仅 `ios`）；新增 `event_type` 值（decision/ticket/mention/needs_human）——分类函数对未知 type 返回 false（**不默认推送**，防未来新事件类型静默变推送）。**验收**：T3 `TestPushFanoutClassifyLedger` 含未知 type 反例。
8. **承重安全属性**：①token 明文不进日志（三处 Warn/Debug 只落 device_id）——T2/T3/T4 日志断言（源码 grep）；②绑定面无 agentd token 绕过；③回环门禁不受影响（本卡不动 proxy/凭据）。**验收**：`grep` 源码无 `log.*apns_token`/`"token", dev`；goldens。
9. **webview 候选族**：本卡不触 webview 协议；深链经 `CookieBridge` 既有 `load(origin+path)`（不改门禁）。真实 webview 深链行为**需真机**（T7）。

---

## 9. 上下文预算检查（每 task 圈得出有界文件集？）

| Task | 有界文件集 | 圈得出? |
|---|---|---|
| T1 | `internal/proto/push.go`、`push_fixture_test.go` | ✓ |
| T2 | `internal/store/{store.go,push.go,push_test.go}`、`internal/agentd/{pushapi.go,pushapi_test.go,server.go}` | ✓ |
| T3 | `internal/agentd/{pushfanout.go,pushfanout_test.go,roomsapi.go}` | ✓ |
| T4 | `internal/agentd/{apns.go,apns_test.go}`、`internal/config/{config.go,config_test.go}` | ✓ |
| T5 | `internal/agentd/{server.go,wakeconsumer.go,pushfanout_wiring_test.go}`、`cmd/agentd.go` | ✓ |
| T6 | `internal/client/{client.go,push_test.go}`、`internal/mobilecore/{core.go,core_test.go}`、`mobile/bind/{bind.go,export_surface_test.go,shell_api_golden_test.go}` | ✓（`mobile/bind` 图外） |
| T7 | `mobile/ios/HandoffMobile/{AppDelegate,ConnectCore,PushRegistrar,PushPresentation,PushDeepLink}.swift`、`Support/{Info.plist,HandoffMobile.entitlements}`、`HandoffMobileTests/*`、`project.pbxproj` | ✓（图外） |

无 task 需要跨多子系统无边界的文件集；无竖切债（新增文件均落在既有目录）。

---

## 10. 类型标注（边界型子系统的真机清单）

- **APNs 出站（边界型，对面是 Apple）**：机内只验请求形状（T4 httptest）；**真实投递、HTTP/2、JWT 被 Apple 接受 → 未验证，需真机**。
- **iOS 壳（边界型，对面是 iOS 系统）**：**授权/横幅/前台抑制/深链/角标 → 未验证，需真机**（T7 清单 6 条）。
- **设备登记 REST（逻辑型）**：机内闭环（T2）。
- **内部 Fanout（逻辑型，出站被 PushSender 接缝隔离）**：机内闭环（T3/T5）。

---

## 11. 占位符扫描与自我声明

- **无 TBD / 「加适当错误处理」/「同 Task N」/ 描述无代码**：每个实现点均给签名+行为+（机械处）完整代码。APNs JWT 给算法全步骤（stdlib 无库可抄，给足步骤即代码级）。
- **例外声明（允许的正当出口）**：T7 的 XCTest **不逐行给全**，因其必须复用 `HandoffMobileTests` 既有夹具（`Fakes.swift` 的 `FakeConnectCore`/`CallRecorder`、`Fakes+Cookies.swift`）而形态因 Xcode 工程而异——按纪律，此处以「断言逐条列全（§T7 测试段每条可判 pass/fail）+ 指认照抄的既有 harness（`Fakes.swift`）」代替完整测试代码。**T7 是本计划唯一的此类例外，已声明。**
- **内部锁声明**：见 §6.4（4 条，均有理由，无未声明项）。
- **退路声明**：T5「真链首选 / 白盒退路」已在 T5 步骤显式声明，退路入口仍是 S3 缝（`OnLedgerEvent`），非内部锁。
- **未验证项如实标注**：APNs 真实投递、iOS 真机行为、`gomobile bind` 生成名、图陈旧——均标「未验证，需真机/CI」，不写成结论。

---

## 12. 派发前自审

- 本计划**无任何需要驱动派发系统自身的验收步骤**（无「派发/调 handoff CLI」步），故无「由协调者执行」标注项。
- 本节点不写实现、不建脚手架、不调 handoff CLI、不起 executor（自审三查②：本回合未碰 handoff CLI / 未起新 executor）。

---

## 13. 自审三查

1. **spec 覆盖逐条指 task**：①何时推 → T3/T5（四源分类+触发）+ T7（前台抑制）；②点进落哪 → T3（deep_link）+ T4（payload）+ T7（跳转/角标）；③站内铃铛 → T3（badge 复用 `collectInboxItems`）+ T5（装配）；契约面 1 设备登记 → T1/T2/T6/T7；契约面 2 内部 Fanout → T3/T4/T5。**无遗漏**。
2. **占位符扫描**：见 §11，唯一例外已声明。
3. **跨 task 类型/签名一致性**：`PushNotification`（T1 定义）被 T3/T4/T5 消费，逐字一致；`PushSender`（T3 定义）被 T4 实现、T5 注入；`store.ListPushDevices/DeletePushDevice`（T2 定义）被 T3 消费，签名逐字一致；`Core.RegisterPush`（T6）与 `bind.RegisterPushDevice`（T6）参数逐字一致；`PushDeviceRegisterReq`（T1）被 T2/T6 逐字消费。**一致**。

---

## 14. 交付物清单

- `docs/superpowers/plans/b432-plan.md`（本文件）
- `docs/superpowers/plans/b432-plan-ledger.md`（台账：复核事实、跑过的命令与原始输出、图查询与覆盖债、判断留痕）
