# B167 跨流迁移拆解——域清单、契约增量、子卡 DAG

> 状态：提案，供协调者与用户拍板。本文只冻结拆解、接缝和验收判据，不写实现代码。
> 契约层骨架应在 Ticket 0 冻结后再进入域子卡。凡存在两种可行形状的地方都保留方案，
> 标为「待拍板」，下游不得把提案文字当成已拍板的产品决定。

### 基准契约复述（不可删改）

本拆解对照工作台基准 §3 的三个场景：

- **场景 A 升级**：建卡时以为是 bug，走 L1 到「进行中」，查出要动三个域的契约，迁到
  `domain` 流「拆解」列；timeline 根因记录随卡带走。
- **场景 B 降级**：L2 feature 走到「已出spec」，发现是一行修，迁到 `bug` 流「进行中」列；
  spec 附件仍留在卡上。
- **场景 C 直通**：单域小改进直建 L2（或不需要 spec 时直建 L1），全程不迁移；这是多数卡
  的路径，迁移是纠错通道而不是常规路径。

五条迁移语义是本卡不可删改的验收判据：

1. **显式落点**：迁移必须同时指定目标流与落点列，不做“自动映射到同名列”的猜测。
2. **防悬空**：落点列必须存在于目标流钉住的版本中，沿用现有防悬空校验逻辑。
3. **事件留痕**：timeline 落一条迁移事件，记录从哪条流哪列到哪条流哪列及操作者。
4. **在飞环节先收口**：卡有派发中/等裁决环节时拒绝迁移，先等回合结束或 stop。
5. **子卡不随迁**：父卡换流不影响子卡各自 workflow；聚合闸与父卡在哪条流无关。

## 1. 触及域清单

域类型严格取自实例化清单 §2。本需求实际触及五个逻辑域；远端连接、Executor 适配、宿主
进程与 PTY、换版与发布、本机集成五个域不参与本次实现。

| 域 | 类型 | 本需求一句话职责 | 触及入口 |
|---|---|---|---|
| 账本域 | 逻辑域 | 将空 workflow 建卡解析为 `triage`，seed 纯人工定性流，并以一次事务完成跨流、跨列、跨版本迁移和事件留痕。 | `internal/ledger/cards.go`、`workflows.go`、`types.go` |
| 控制面 API 域 | 逻辑域 | 提供建卡可省略 workflow 和跨流迁移 HTTP 端点；在调用账本前读取 agentd 进程内在飞状态并拒绝冲突。 | `internal/agentd/ledgerapi.go`、`cardstep.go`、`server.go` |
| CLI 域 | 逻辑域 | 让 `card add` 缺省走 triage，并提供同时写明目标流和落点列的迁移命令；旧版本迁移调用要有兼容收口。 | `cmd/card.go`、`cmd/workflow.go` |
| Web 控制台域 | 逻辑域 | 建卡表单允许“尚未定性”，并消费强类型迁移请求/响应与 timeline 事件。 | `web/src/api/ledger.ts`、`web/src/app/cards/*` |
| 契约域 | 逻辑域 | 把本需求触及的账本 wire DTO 接入既有 Go fixture + TS 镜像机制，消灭新增手搭 map 的无强制路径。 | `internal/proto`、`web/src/api/contract.test.ts`、`web/src/api/testdata` |

### 1.1 agentd 运行态接缝归属

“卡有环节在跑时拒绝迁移”属于控制面 API 域，不属于账本域，原因是事实来源是
`internal/agentd/cardstep.go` 的 `cardStepInFlight(cardID)`：它是进程内 map，账本中没有这项
事实，也不应把它伪造为持久字段。接缝是 `handleCardMigrate` → agentd 的卡环节互斥锁 →
`ledger.Store.MigrateCardTo`。仅先读 bool 再放锁会留下竞态，因此提案要求迁移调用与
`claimCardStep` 共用同一把 `cardStepMu` 的短临界区：迁移持锁完成账本事务时，新的 step 不能
抢占；已有 step 则迁移在锁内立即得到 409。账本域只负责事务和业务状态，不 import agentd。

这项保证只覆盖当前 agentd 进程；现有 `cardstep.go` 已明确 agentd 重启会清空在飞集合且不做
恢复。若要让“重启后远端 task 仍在跑”也阻止迁移，需要另立持久运行态契约，本卡只在第 4 节
记录风险和协调者真机清单，不把运行态写进账本。

## 2. 各域契约增量（精确到可编译）

以下是 Ticket 0 的契约提案。类型名、JSON 键和方法签名按此写即可形成可编译 stub；具体
错误文案不是 wire 契约。`workflow`、状态列和事件类型仍是字符串，不新造 Go enum，以保持
现有工作流“数据不是代码”的约束。

### 2.1 账本域：建卡默认流、迁移目标和事件

#### 建卡

`internal/ledger/cards.go` 的内部请求保留现有 Go 形状，改变的是空值语义：

```go
type NewCard struct {
	Title, Project, Priority, Parent, Workflow, BaseBranch, Actor string
}
```

- `Workflow == ""` 时，`CreateCard` 在取工作流前解析为常量/数据名 `triage`；调用方不再各自
  猜默认值。
- 非空 workflow 仍按原逻辑取最新版本，故形态明确时可以直建 `bug`、`feature` 或
  `domain`，场景 C 不经过迁移。
- `CreateCard` 的返回值和 `card_created` 事件必须带最终的 `triage` 名称与版本，而不是把
  空字符串透传出去。
- `EnsureDefaultWorkflows` 的 seed 新增 `triage`：节点严格为
  `待办 → 定性中 → 已定性`，每个节点 `Dispatch=false`、`Verdict=false`、无模板、无 gate；
  seed 幂等且不覆盖用户已有同名流。它是数据 seed，不增加引擎节点类型。

#### 迁移进程内类型

建议在 `internal/ledger/types.go` 增加以下公开契约类型；它们只描述账本域的进程内接缝，
不是 JSON wire DTO：

```go
type WorkflowTarget struct {
	Name    string
	Version int    // 提案 α：0=事务内取该流最新版本；提案 β改为必须 >0
	Status  string // 目标落点列，必须显式提供
}

type WorkflowLocation struct {
	Workflow string
	Version  int
	Status   string
}

type WorkflowMigration struct {
	CardID string
	From   WorkflowLocation
	To     WorkflowLocation
	Event  Event
}
```

新增事件类型常量：

```go
const EvWorkflowMigrated = "workflow_migrated"
```

事件的 `Event.Actor` 是操作者；`Event.Payload` 是稳定 JSON 对象，字段固定为：
`from_workflow`、`from_version`、`from_status`、`to_workflow`、`to_version`、`to_status`。
不得复用普通 `comment`，否则前端和审计工具无法区分“迁移”与普通说明。

新增主方法的提案签名：

```go
func (s *Store) MigrateCardTo(
	cardID string, target WorkflowTarget, actor string,
) (WorkflowMigration, error)
```

语义和原子性：

1. 在同一写事务内读取卡和目标流版本；`target.Name`、`target.Status` 非空，否则拒绝。
2. 目标版本按方案 α 的 `Version==0` 取事务内最新版本；若指定正版本则钉该版本。目标
   `WorkflowDef.States` 必须含 `target.Status`，不做同名列自动映射。方案 β把“版本也要显式”
   作为请求层硬约束，见下方待拍板。
3. 对目标落点执行与 `MoveCard` 相同的 gate 判据；通过后一次性更新
   `workflow_name`、`workflow_version`、`status`、`updated_at`，并追加一条
   `EvWorkflowMigrated`。卡和事件必须同事务提交，提交后才通知事件监听者。
4. 不触碰 `parent_id`、子卡行、关系边、附件、验收判据和已有事件；父卡迁移不改变子卡的
   workflow 或版本。重复请求若当前 location 已等于目标，提案要求返回幂等成功且不再追加
   第二条迁移事件；具体返回是否带空 `Event` 标为待拍板。

#### 既有 `MigrateCardWorkflow` 的兼容方案（待拍板）

现有签名是：

```go
func (s *Store) MigrateCardWorkflow(cardID string, toVersion int, actor string) error
```

当前仓内调用方实测为 `internal/ledger/workflows_test.go` 和 `cmd/workflow.go`，另有
`types.go`/`ledgerapi.go` 注释引用；没有 HTTP 调用。存在两种方案：

- **方案 A：新增 `MigrateCardTo`，保留旧方法为 deprecated wrapper（推荐兼容性）**。旧方法只
  服务同名工作流版本迁移，内部把当前 status 作为目标 status，继续使用旧 `error` 返回；
  新 CLI/HTTP 一律不调用它。优点是仓内/外 Go 调用方不立刻编译断裂；缺点是旧方法本身仍
  能绕过“调用方显式写落点”和 agentd 在飞门禁，必须明确不再作为新迁移入口。
- **方案 B：直接改签名为 `MigrateCardTo` 的参数形状或把 `MigrateCardWorkflow` 改成
  `(cardID, toWorkflow string, toVersion int, toStatus, actor string) error`**。优点是单一入口、
  没有旧语义残留；缺点是已有调用方全部破坏性更新，外部包也失去源兼容。

本文后续按方案 A 画接缝，但这是契约拍板项；若选 B，Ticket 0 必须同时更新所有引用和测试，
不得保留一个未标记的旧路径。

#### 目标版本选择（待拍板）

- **方案 α**：请求可省略 `version`，0 表示迁移事务内取目标流最新版本；适合工作台的目标
  流选择，且仍把最终版本写入事件和卡。
- **方案 β**：请求必须给 `version`，迁移者对版本也作出显式决定；避免“点击时最新、提交时
  另一版”的时间竞争，但 UI/CLI 需要先读流版本并处理过期错误。

两种方案都满足五条基准语义；不得在实现中悄悄以“目标流同名列”或隐式旧版本代替。

### 2.2 契约域：账本 wire DTO 与 fixture 范围

新增 `internal/proto/ledger.go`，使用现有 proto 包的 JSON tag/`time.Time` 习惯。为避免
`ledger` 反向依赖 `proto`，这些是 wire DTO，agentd 负责从账本类型显式投影；不要直接把
`map[string]any` 塞回 `writeJSON`。本需求触及、应进入既有夹具机制的类型如下。

```go
type Attachment struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type NewCardReq struct {
	Title      string `json:"title"`
	Project    string `json:"project"`
	Workflow   string `json:"workflow,omitempty"` // 缺省/空值 = triage
	Priority   string `json:"priority,omitempty"`
	Parent     string `json:"parent,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
}

type MigrateCardReq struct {
	Workflow string `json:"workflow"`
	Status   string `json:"status"`
	Version  int    `json:"version,omitempty"` // 方案 α：0=latest；方案 β去掉 omitempty
}

type CardWorkflowLocation struct {
	ID              string `json:"id"`
	Workflow        string `json:"workflow"`
	WorkflowVersion int    `json:"workflow_version"`
	Status          string `json:"status"`
}

type MigrateCardResp struct {
	OK    bool                  `json:"ok"`
	ID    string                `json:"id"`
	From  CardWorkflowLocation  `json:"from"`
	To    CardWorkflowLocation  `json:"to"`
	Event LedgerEvent           `json:"event"`
}

type LedgerEvent struct {
	Seq       int64           `json:"seq"`
	CardID    string          `json:"card_id"`
	Type      string          `json:"type"`
	Actor     string          `json:"actor"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}
```

其中 `CardWorkflowLocation` 和 `MigrateCardResp` 的两个空格仅为排版，实际 Go 格式化由
`gofmt` 决定。为覆盖本需求实际触及的既有投影，`ledger.go` 还要声明与当前线格式一一对应
的 `CardView`、`CardDetail`、`NodeDef`、`FlowDetail`（含 `NodeOverride`/`Gate`/`CardBrief`/
`Decision` 等从属 DTO）。字段必须覆盖当前 `ledgerapi.go` 的 13 处手搭投影；没有本需求
数据链路的 `WorkflowWire`/`TemplateWire` 等类型暂不扩围。

最小字段约束如下，作为编译和 fixture 评审的 checklist：

- `CardView`：`id/title/status/priority/project/workflow/parent/base_branch/attachments/
  following/blocked/blocked_by/merged_count/needs/open_decisions/children_total/children_done/
  conflict/open_tickets`，空数组和缺键保持当前约定，不把 0 与缺失混成一类。
- `CardDetail`：`card/relations/events/task_states/effective_base_branch/decisions/needs/children`；
  `card` 改为 wire `Card` 而不是 TS `unknown`，但不重命名当前 `Relation` 的 PascalCase 键，
  避免本需求顺手造成不相关的线格式破坏。
- `NodeDef`：`name/template/override/dispatch/verdict/carry_card_context/max_rounds/next/
  on_fail/gate/human_bases`；`NodeOverride` 和 `Gate` 字段按当前 Go JSON tag 镜像。
- `FlowDetail`：`name/version/nodes/states`。
- `NewCardReq`：如上，`workflow` 是唯一从必填改为可省略的输入字段；其余字段语义不变。

**范围判据**：凡本需求新增或改变的字段，或被本需求新增/改变的 HTTP 响应直接消费的账本
DTO，进入 fixture；仅被同一 handler 顺手读取、线格式未变且不在新字段链路上的模板/流列表
DTO，本卡不扩展。该判据把 `CardView/CardDetail/NodeDef/FlowDetail/NewCardReq` 和迁移
请求/响应纳入，同时明确不要求一次覆盖账本所有历史类型。

### 2.3 控制面 API 域：HTTP 端点和运行态门禁

`registerLedgerRoutes` 新增：

```text
POST /api/cards/{id}/migrate
```

请求体严格为 `MigrateCardReq`：

```json
{"workflow":"domain","status":"拆解","version":1}
```

`workflow` 和 `status` 均必填且不可空白；`version` 按方案 α 为可选，0=最新。响应 200 为
`MigrateCardResp`，示意：

```json
{
  "ok": true,
  "id": "B167",
  "from": {"id":"B167","workflow":"bug","workflow_version":1,"status":"进行中"},
  "to": {"id":"B167","workflow":"domain","workflow_version":1,"status":"拆解"},
  "event": {
    "seq": 9, "card_id":"B167", "type":"workflow_migrated", "actor":"web:127.0.0.1",
    "payload":{"from_workflow":"bug","from_version":1,"from_status":"进行中",
               "to_workflow":"domain","to_version":1,"to_status":"拆解"},
    "created_at":"2026-08-21T10:30:00+08:00"
  }
}
```

响应状态约定：

- 400：JSON 不合法、目标流/列为空、目标版本不存在、目标列不在目标版本、目标 gate 不满足、
  或旧方法式状态不合法；不做同名列猜测。
- 404：卡或目标工作流/版本不存在，沿用现有 `ErrNotFound` 映射。
- 409：`cardStepInFlight(id)` 为真；错误体仍为 `{"error":"..."}`，说明先等环节收口或 stop。
- 200：事务已提交，返回新 location 和唯一迁移事件。

`handleCardCreate` 改为解码 `proto.NewCardReq`，删除 `project 与 workflow 都是必填` 中对
workflow 的强制判断；title/project 仍必填，空 workflow 直接交给 `CreateCard` 解析为 triage。
响应仍是 `{"id":"..."}`，但必须用明确的 `proto` 响应 DTO/fixture 约束，而不是新增 map。
`handleCardsList`、`handleCardDetail`、`handleFlowGet` 等现有手搭投影在本需求触及的链路上
改为对应 DTO；无关的 flows/template 信封不在本次范围，除非选择让 UI 直接复用它们的新增字段。

在 `cardstep.go` 增加一个与 step claim 共用 `cardStepMu` 的内部调用接缝，形状可为：

```go
func (s *Server) withCardStepIdle(cardID string, fn func() error) error
```

它持锁检查 `cardStepFlight[cardID]`，冲突返回可被 handler 翻成 409；无冲突时在同一把锁
保护下运行账本迁移函数，结束后释放锁。不得把 `cardStepInFlight` 的 map 序列化到 API 或
账本。`startCardStep` 的 claim 和此接缝必须使用同一个互斥状态，避免“检查后 step 抢先”。

### 2.4 CLI 域：flags、兼容入口和现实边界

建卡：

- `cmd/card.go` 的 `--workflow` 默认值由 `feature` 改为空字符串，帮助文本明确“空=triage”；
  非空仍可直建目标流。`--project` 仍必填。
- `card add` 输出的卡对象/JSON 必须显示最终 workflow=`triage`，不能显示空值。

新迁移命令形状（推荐）为：

```text
handoff workflow migrate <card-id> --workflow <目标流> --column <目标列> [--version <正整数>] [--yes]
```

`--workflow` 和 `--column` 都必填；`--version` 对应方案 α 的可选目标版本，`--yes` 保留
现有破坏性确认语义。现有 `--to` 是整数版本 flag，不能偷偷改成字符串列名。

旧 CLI 入口有两个待拍板方案：

- **方案 A**：保留旧 `workflow migrate <id> --to <version>` 作为同流版本兼容入口，标记
  deprecated，只调用旧 wrapper；新跨流命令必须同时提供新 flags。兼容强，但旧入口不享受
  agentd 在飞检查。
- **方案 B**：删除旧 `--to` 形状，`workflow migrate` 全部改用新 flags；现有仓内调用方和
  CLI 测试一次更新，只有一个语义完整的入口。收口干净，但下游脚本破坏性更大。

CLI 当前 `openLedger()` 直连账本，不能观察另一进程的 `cardStepInFlight`。因此“所有入口都
必须拒绝 agentd 在飞”还存在一个必须由协调者选择的接缝：

1. 把 CLI 迁移改为调用本机 agentd 的 `POST /api/cards/{id}/migrate`（需新增/复用
   `internal/client` 的 JSON 方法和鉴权），以获得同一门禁；这会触及远端连接域，需另列边界
   域真机清单。
2. 明确 CLI 是本地账本管理员路径，只保证账本事务和自身进程不并发，验收中的“在飞拒绝”
   仅绑定控制面 HTTP；不得在 CLI 帮助或文档中宣称它能看见 agentd 运行态。

本文不替协调者选 1/2；若验收将语义 4 解释为全入口硬约束，选项 1 是承重前置，不能由
域执行者自行弱化。

### 2.5 Web 控制台域：TS 接口和函数

`web/src/api/ledger.ts` 的接口提案：

```ts
export interface NewCardReq {
  title: string
  project: string
  workflow?: string // 缺省 = triage；直建时传 bug/feature/domain
  priority?: string
  parent?: string
  base_branch?: string
}

export interface MigrateCardReq {
  workflow: string
  status: string
  version?: number // 方案 α；方案 β改为 version: number
}

export interface CardWorkflowLocation {
  id: string
  workflow: string
  workflow_version: number
  status: string
}

export interface MigrateCardResp {
  ok: boolean
  id: string
  from: CardWorkflowLocation
  to: CardWorkflowLocation
  event: LedgerEvent
}

export const migrateCard = (id: string, req: MigrateCardReq) =>
  postJSON<MigrateCardResp>(`/api/cards/${encodeURIComponent(id)}/migrate`, req)
```

`CardView`、`CardDetail`、`NodeDef`、`FlowDetail` 迁移为与 `internal/proto/ledger.go` 同一组
JSON 键的 TS 接口；`CardDetail.card` 不再新增字段时可保留结构化 wire `Card`，不能重新退回
`any`。`LedgerEvent.payload` 仍为 `unknown`，只有 `type === 'workflow_migrated'` 的渲染器
将它收窄为迁移 payload。

UI 入口：`NewCardDialog` 增加“未定性（triage）”选项/空 workflow 发送语义；迁移操作应在
`CardDrawer` 或同一详情入口一次提交目标流和目标列，不能先改流再单独改列。提交成功后可
依赖轮询刷新，但响应中的 `from/to/event` 要进入 API 测试，避免只测 fetch 调用没测消费形状。

### 2.6 夹具机制的新增文件和断言

严格沿用 `internal/proto/contract_fixture_test.go`：固定样本、`json.MarshalIndent`、末尾
换行、默认逐字节比对，只有显式 `-update` 才重写；TS 沿用 `web/src/api/contract.test.ts`
的 JSON import + 强类型承接 + 关键字段运行时断言。拟新增/更新：

| fixture | 产生端/消费端 | 必须钉住的断言 |
|---|---|---|
| `NewCardReq.json` | `handleCardCreate`、`ledger.ts` | `workflow` 可缺席；title/project 存在；无 workflow 不被编码成 `null` 或 `feature`。另用真实 HTTP 测试钉住传入 bug 时仍保留。 |
| `MigrateCardReq.json` | HTTP/CLI 请求 DTO、`ledger.ts` | `workflow/status` 必须存在；`version` 的 0/缺席语义按拍板方案固定。 |
| `MigrateCardResp.json` | `handleCardMigrate`、`migrateCard` | `ok/id/from/to/event` 全存在；from/to 各含 flow、version、status；事件 type 为 `workflow_migrated`。 |
| `LedgerEvent.json` | `CardDetail.events`、`CardDrawer` | `actor`、`payload`、RFC3339 `created_at` 不漏；迁移 payload 六个键逐一断言。 |
| `CardView.json` | `handleCardsList`、列表卡片 | 当前 19 个列表键（含 `children_total/done`）逐一存在；0 与缺键按现状区分。 |
| `CardDetail.json` | `handleCardDetail`、抽屉 | `card/relations/events/task_states/effective_base_branch/decisions/needs/children` 形状完整。 |
| `NodeDef.json` | `FlowDetail.nodes`、列渲染 | triage 纯人工节点的能力开关均为 false/缺席，`name/next` 不漏。 |
| `FlowDetail.json` | `handleFlowGet`、目标列选择器 | `name/version/nodes/states` 完整，triage 三列顺序固定。 |

fixture 只钉 DTO 线格式；`internal/agentd/ledgerapi_test.go` 还必须把真实 handler 响应解码
到这些 DTO，覆盖手搭 map 投影，不能只让 Go fixture 和 TS 各自拿自造数据通过。CLI 自己的
`cardViewWire`/show 输出若保留独立形状，要么改用同一 DTO，要么在 CLI 测试中明确列出它是
另一条受控输出；不得以“HTTP fixture 绿”替代 CLI 边界断言。

## 3. 子卡清单 + 依赖 DAG

Ticket 0 是先行契约卡；其余域卡都引用它，且不在自己的上下文里重新发明字段。每张卡按
协议 §7.3 的四段式书写。

### B167-T0：契约冻结（协调者拍板后执行）

1. **契约**：冻结 §2 的 `triage` seed、`WorkflowTarget`/`MigrateCardTo`、
   `workflow_migrated` 事件、HTTP `POST /api/cards/{id}/migrate`、`NewCardReq.workflow?`、
   版本方案 α/β、fixture 类型和 CLI 旧入口方案；同步更新相关域文档条目。
2. **意图与为什么**：把所有跨域字段和错误语义变成可编译的单一事实源，让后续域卡在
   编译期暴露错配；尤其避免账本 wire 再进入手搭 map 黑洞。
3. **验收**：契约域为逻辑域：`internal/proto` fixture 测试和 TS 镜像断言形成闭环；每个
   字段有 JSON 键、缺席/零值语义和产生/消费文件清单；不实现业务行为，不把待拍板项伪装
   成通过。缺陷族结论为：生命周期=fixture 变红先于业务接线；静默失败=逐字段真实边界；
   跨平台=固定时间/时区；假红=禁止 `-update` 偷刷；门禁=DTO 不承载放行；webview=仅 TS
   镜像、不代替真机。各域卡必须逐项引用 §4.8。
4. **入口指针**：`internal/proto/contract_fixture_test.go`、`web/src/api/contract.test.ts`、
   `internal/ledger/types.go`、`internal/ledger/workflows.go`、本文件 §2。

### B167-L：账本域——triage seed 与原子跨流迁移

1. **契约**：引用 Ticket 0 的 `NewCard.Workflow=="" => triage`、`EnsureDefaultWorkflows`
   三节点、`WorkflowTarget`、`MigrateCardTo`、`EvWorkflowMigrated` 和目标 gate/版本语义；
   旧 `MigrateCardWorkflow` 按方案 A/B 处理。
2. **意图与为什么**：让无法在建卡时判形态的卡先进入纯数据 triage，且允许场景 A/B 以明确
   目标 flow+column 纠错；更新卡的 flow/version/status 与 timeline 必须是同一事务，子卡、
   附件、关系和父子聚合不被迁移误伤。triage 不引入引擎能力。
3. **验收**：逻辑域闭环，使用真实 SQLite，不深 mock store；覆盖 triage 幂等 seed、空
   workflow 建卡、直建目标流、A/B/C 三场景、目标版本列存在性、目标 gate、事件 payload、
   子卡不随迁、父卡聚合闸不变、回滚/中断无半状态、终止态按拍板规则、相同目标幂等。缺陷族
   逐项结论为：生命周期=单事务且版本随目标绑定，在飞由 API 门禁；静默失败=真实 SQL/事件
   读取和 wire 断言；跨平台=SQLite+PG 冒烟；假红=不只测返回 Card；门禁=目标 gate 必须重验；
   webview=无账本特有风险。邻域只 mock 契约层，不能 mock `sql.Tx` 内部细节。
4. **入口指针**：`internal/ledger/cards.go`（`NewCard`/`CreateCard`）、
   `internal/ledger/workflows.go`（seed/旧迁移）、`internal/ledger/types.go`、
   `internal/ledger/cards_test.go`、`internal/ledger/workflows_test.go`、`internal/ledger/wire_test.go`。

### B167-A：控制面 API 域——HTTP 投影和 agentd 在飞接缝

1. **契约**：引用 Ticket 0 的 `proto.NewCardReq`、`MigrateCardReq/Resp`、`CardView`、
   `CardDetail`、`FlowDetail` 与 `withCardStepIdle`；注册建卡默认语义和迁移路由及 400/404/409/200。
2. **意图与为什么**：让 Web 请求与 CLI/账本使用同一 wire 形状；迁移之前在 agentd 运行态
   拦截派发中/等裁决，避免旧流回写新列；HTTP 层只做装配、投影和状态码，不在这里复制账本
   状态机或把进程内状态落数据库。
3. **验收**：逻辑域闭环，handler 测试只在契约层 mock 邻域；真实测试环境挂 SQLite ledger。
   覆盖 omitted workflow→triage、显式 workflow 直建、请求字段缺失、目标列/版本错误、目标
   gate 错误、已有 `cardStepFlight` 返回 409、锁内 step/迁移竞态、响应 DTO 和真实事件；进程
   crash 的事务原子性由账本测试闭环，agentd 重启清空在飞的已知限制必须进入协调者清单。
   缺陷族逐项结论为：生命周期=锁内检查、事务内迁移但重启后运行态不恢复；静默失败=真实
   handler JSON 解码 DTO 并登记新值；跨平台=HTTP/UTF-8 机内测加真机清单；假红=并发和
   response body 不能深 mock；门禁=目标 gate/contract 往返必须拒绝；webview=只把桌面表现
   留给协调者，不拿 jsdom 代替。§4 的序列化边界逐个解码真实 HTTP，不允许只看 status code。
4. **入口指针**：`internal/agentd/ledgerapi.go`（route、create、list、detail、flow 投影）、
   `internal/agentd/cardstep.go`、`internal/agentd/server.go`（`cardStepMu`）、
   `internal/agentd/ledgerapi_test.go`、`internal/agentd/cardstep_test.go`。

### B167-C：CLI 域——缺省建卡和显式迁移命令

1. **契约**：引用 Ticket 0 的 `--workflow` 空值语义、新迁移 flags `--workflow/--column/[--version]/--yes`、
   旧 `--to` 兼容方案和 `MigrateCardTo`；若要求全入口在飞拒绝，改引用协调者选定的
   agentd client 端点方案，不自行写第二套门禁。
2. **意图与为什么**：命令行建卡不再强迫人提前把 bug/feature/domain 选对；迁移命令必须
   把目标流和目标列同时写在命令行中，支持 A/B 纠错并保留操作者确认。不会把 `--to` 整数
   误解释成列名。
3. **验收**：逻辑域闭环，使用真实临时 SQLite；邻域仅 mock 契约层 client（若选 agentd
   路径）或直接 mock `Store` 接口，不 mock Cobra 内部。覆盖 help/default、triage JSON、
   直建、缺任一目标参数即拒、目标 flow/version/column 原样传递、确认闸、事件 stdout 形状、
   旧命令兼容方案。缺陷族逐项结论为：生命周期=本地事务闭环但是否观察 agentd 在飞取决于
   待拍板入口；静默失败=flag→请求/Store 的真实值和 JSON 断言；跨平台=shell/SQLite 机内
   测加真机清单；假红=执行命令而非只测 help；门禁=CLI 不得宣称能绕过目标 gate；webview=无
   CLI 特有风险。§4 明确记录 CLI 不能观察 agentd map 时的验收边界，禁止假报语义 4 已全部满足。
4. **入口指针**：`cmd/card.go`、`cmd/workflow.go`、`cmd/card_test.go`、`cmd/workflow_test.go`、
   `cmd/ledgercli.go`（仅确认 openLedger/actor，不擅改其语义）。

### B167-W：Web 控制台域——未定性建卡与迁移消费

1. **契约**：引用 Ticket 0 的 `NewCardReq.workflow?`、`MigrateCardReq/Resp`、
   `CardView/CardDetail/NodeDef/FlowDetail/LedgerEvent` fixture 和 `migrateCard` API 函数。
2. **意图与为什么**：表单把“尚未定性”作为一等选项而非偷偷选择第一条流；迁移是一次性
   选择目标流和目标列，成功后刷新卡并能在 timeline 读出 from/to/actor；场景 C 的直建路径
   保持短，不把多数卡强行送 triage 后再迁移。
3. **验收**：逻辑域闭环；API 测试以真实 JSON 请求断言可省略 workflow 和迁移 body，组件
   测试只 mock API 契约函数，不深 mock React/HTTP 内部。断言目标列选择、双字段不可半提交、
   409/400 原文显示、迁移事件的六个 payload 键和 actor。缺陷族逐项结论为：生命周期=请求
   一次性提交且刷新以响应为准；静默失败=真实 fetch body/fixture；跨平台=JSON 逻辑不依赖 OS；
   假红=组件不能只 mock `{ok:true}`；门禁=展示 400/409、不能自行放行；webview=select/
   focus/toast 必须真机验收。因本机 jsdom 不能证明 Wails WKWebView/Chromium 的表现，§4.7
   的项目进入协调者真机清单。
4. **入口指针**：`web/src/api/ledger.ts`、`web/src/api/ledger.test.ts`、
   `web/src/api/contract.test.ts`、`web/src/app/cards/NewCardDialog.tsx`、
   `web/src/app/cards/CardsPage.tsx`、`web/src/app/cards/CardDrawer.tsx`、对应 `*.test.tsx`。

### B167-I：集成收口（协调者执行）

1. **契约**：引用所有域卡已通过的 Ticket 0 DTO 和统一迁移方法；不新增第二种 wire 或
   旁路门禁。
2. **意图与为什么**：把账本、agentd、CLI、Web 接成可验收的一条链，验证基准场景 A/B/C
   和五条迁移语义；这里才允许处理跨域调配和真机操作。
   3. **验收**：跨域集成卡不把邻域深 mock 当完成。机内跑 Go/Web 全链路和真实 SQLite；执行
   显式真机清单：agentd step 派发/等裁决时迁移 409、agentd 重启后的已知语义、真实 Wails
   WebView、CLI 若选 agentd 路径的鉴权/地址、SQLite 与 PG 冒烟、事件推送顺序。六族收口为：
   生命周期看中断/重启，静默失败看真实 wire/新值，跨平台看 PG/壳/CLI，假红看三场景，门禁
   看 contract 往返，webview 看两类桌面壳。失败要贴原始命令输出，不以子卡全绿替代整体验收。
4. **入口指针**：`internal/agentd/ledgerapi_test.go`、`internal/ledger/store_pg_test.go`、
   `cmd/*ledger*test.go`、`web/src/app/cards/*test.*`、本文件 §4 的真机清单。

### 3.1 依赖 DAG

```text
B167-T0 契约冻结
       ├──> B167-L 账本域
       │       └──> B167-A 控制面 API 域
       ├──> B167-A 控制面 API 域
       │       └──> B167-W Web 控制台域
       ├──> B167-C CLI 域
       └──> B167-W Web 控制台域（fixture/TS 可先并行，HTTP 组件接线等 A）
B167-L + B167-A + B167-C + B167-W ──> B167-I 集成收口
```

- T0 必须先于所有域卡：新字段和新方法若不先冻结，正是协议要消灭的集成时才爆炸。
- L 与 C 可在 T0 后并行；L 是 C 的 store 接缝，但 C 只需契约 stub，行为联调等 L。
- A 依赖 L 的方法和事件；A 也依赖 T0 的 DTO，不能在 ledger 尚未决定版本方案时自定
  `version` 行为。
- W 的 fixture、TS 类型和 API request 测试可在 T0 后并行；真正的 UI 迁移按钮依赖 A 的
  HTTP 状态和响应。
- I 必须串行在全部域卡后，因跨域调配、e2e 和边界真机检查只允许出现在集成卡。

## 4. 缺陷族对抗审查

下面按实例化清单 §3 的五族和项目特有 webview 族逐域过问。序列化边界和新增枚举单列为
静默失败族的强制子问题；每条结论已在 §3 对应子卡的验收段引用。

### 4.1 生命周期/状态机族（高风险）

**卡处于各种状态时：**

- `待办/进行中/待审阅/已出spec/拆解/契约冻结/域实现/集成/终审` 等任意非终止状态都
  允许提出迁移，但目标落点必须是目标版本 `States` 中明确传入的列；不使用当前状态做
  自动映射。迁移可以同时改变 `workflow_name`、`workflow_version` 和 `status`，因此 A 的
  `bug/进行中 → domain/拆解`、B 的 `feature/已出spec → bug/进行中` 都是同一原语。
- 目标列 gate 必须在迁移事务中重新检查；迁移不是绕过目标 gate 的“改数据库”操作。
- 卡有当前进程的 step 在飞时，API 在 agentd 互斥锁内返回 409；账本方法自身不假装知道
  该运行态。已有 step 的远端 task 是否在 agentd 重启后仍跑，是现有 cardstep 不恢复的
  已知边界，必须由协调者真机确认并决定是否另卡补持久运行态。
- 事务中断（进程挂、DB 错误）只能得到“旧卡+无迁移事件”或“新卡+完整迁移事件”两种状态；
  `cards` 更新和 `card_events` 插入不可跨事务。HTTP 在提交未知时不能盲重试，客户端应先
  读卡/事件；同目标幂等规则用来减少重复事件。
- 卡钉版本对应迁移事务选出的目标流版本：`workflow_name=to.Name`、
  `workflow_version=to.Version`、`status=to.Status` 和事件 `to_*` 必须来自同一个读取结果，
  不允许写入新流名却沿用旧版本号。
- **终止态提案：** 默认拒绝 `StatusClosed` 直接迁移，先走现有 `ReviveCard` 回到 `待办`，
  再用显式目标流/列迁移；这样不把审计终点静默变成活动卡。若用户需要“终止卡只改归档流、
  仍保持终止”的审计迁移，须另定允许 `StatusClosed` 作为特殊目标（它不在 States，需新
  规则），标为待拍板，不可由域执行者顺手放开。

### 4.2 静默失败族：序列化边界设问（逐字段）

#### `workflow` 可选字段链

`workflow` 从产生到消费的每一处手写序列化/投影都列在这里，必须有断言：

1. Web `NewCardDialog.tsx`：未定性选项产生缺省键或空字符串；组件测试断言不偷传第一条
   flow，直建选项仍传 `bug/feature/domain`。
2. Web `api/ledger.ts` 的 `NewCardReq`：`workflow?: string`，`ledger.test.ts` 断言 body
   在未定性时不带 `workflow`；`contract.test.ts` + `NewCardReq.json` 断言可缺席而不是 null。
3. `internal/agentd/ledgerapi.go:handleCardCreate`：解码 `proto.NewCardReq`，不再用局部
   map，也不以空值触发 400；API 测试把真实 JSON 解成 DTO 后检查传给 store 的 workflow。
4. `internal/ledger/cards.go:CreateCard`：把空值变成 `triage` 后才 `GetWorkflow`；
   `card_created` payload、返回 Card 和 SQL `workflow_name` 都断言最终名/版本。
5. `cmd/card.go`：flag 默认值和 JSON 输出；CLI 测试断言没有 flag 时最终卡是 triage。

#### 迁移字段链

1. Web `migrateCard` 和 CLI flags 产生 `workflow/status/version`；API/CLI 测试断言三者都
   原样传递，不能把 column 误叫 `to` 或把 version 当 status。
2. `internal/agentd/ledgerapi.go:handleCardMigrate` 解码 `proto.MigrateCardReq`，校验
   必填后转换 `ledger.WorkflowTarget`；真实 HTTP 测试覆盖缺键与 0/缺席。
3. `internal/ledger/workflows.go:MigrateCardTo` 读取目标 workflow/version/status，写
   `cards.workflow_name/workflow_version/status`，并在同一事务生成 `EvWorkflowMigrated`
   payload 六键；ledger 测试直接读回 SQL 语义和事件原文。
4. handler 将 `ledger.WorkflowMigration` 投影为 `proto.MigrateCardResp`；不能用局部 map
   漏 `from/to/event`。`MigrateCardResp.json` 和 handler 解码测试双重钉住。
5. `web/src/api/ledger.ts`、`CardDrawer.tsx`、CardDetail timeline 消费 `event.type` 和
   `payload`；API/组件测试要区分字段缺失、空字符串和 0。
6. `handleCardsList`、`handleCardDetail`、`handleFlowGet`、`cmd/cardViewWire` 等已有手搭
   投影若在本次触及路径中输出上述字段，必须列入同一 diff 与真实报文断言；不能只测 store
   和前端自造 fixture。

#### 序列化链结论

账本 wire 的 fixture 覆盖只是第一层，真实 HTTP/CLI 边界测试是第二层；两端各自测试而没有
“实际 handler JSON → 消费 DTO”的测试视为不通过。特别是 `CardView.children_total`/
`children_done` 的历史漏字段不能重演。

### 4.3 静默失败族：新增枚举/字符串值设问

本需求引入的受控取值和既有检查点：

| 新值 | 既有校验器/白名单/switch | 必须动作 |
|---|---|---|
| `triage` | `EnsureDefaultWorkflows`、`cmd/workflow.go` 当前对 `feature/bug` 的特殊列举、Web `fetchFlows`/`NewCardDialog` | seed 增加且幂等；CLI 不再只硬编码两流；Web 以 API 列表为源并显示 triage；不得在 UI 另写白名单。 |
| `定性中`、`已定性` | `WorkflowDef.withStatesFromNodes`、`validateNodes`、`MoveCard` 的 States/Gates 校验、看板列合并 | 作为 triage 数据 seed 和 FlowDetail fixture 登记；不加入 engine switch，不增加“定性节点类型”。 |
| `workflow_migrated` | `ledger` 事件常量、`CardDetail.events`、Web `CardDrawer.eventKind`、任何按事件 type 过滤/统计的 switch | 新增常量、fixture 和 UI 分类；未知事件不能被静默丢弃；CLI wait 只关心 `status_moved` 的现有逻辑保持。 |

没有新增附件 kind；`contract` 已在当前 `attachmentKinds` 白名单，若目标 gate 使用它，必须
保留既有 `contract` 回归，不另造一个迁移专用 kind。工作流名和节点名原则上是开放字符串，
因此只登记实际硬编码点，不把它们错误收窄成 Go enum。

### 4.4 门禁绕过族（必须有明确结论）

**结论：迁移不能用来跳过目标流的 gate；每个迁移落点都执行目标列的 gate。** 例如迁入
需要 `contract` 附件的 `domain/契约冻结`，没有附件就 400；迁到无该闸的 `bug/进行中` 是
一次明确的形态降级，允许作为场景 B，但它不意味着后来迁回 `domain/契约冻结` 可以绕过
`contract`。迁回时目标 gate 再次拒绝，必须先补附件。该结论是“目标 gate 必须满足”，不是
“保留一个当前未通过 gate 的持久失败标记”。

如果产品还要求“卡曾经尝试过某个 gate 但没过，就永远不能迁到无闸流”，现有账本没有记录
失败尝试的事实，单靠当前五条语义无法实现；这将是另一个“gate attempt”事件/状态契约，
本 B167 不偷偷添加。集成验收必须覆盖：缺 contract → 迁无闸流 → 再迁回有 contract 闸，
确认最后一步仍 400；这就是本提案对问题示例的明确回答。

### 4.5 跨平台假设族

| 域 | 设问与结论 |
|---|---|
| 账本域 | SQLite/PG 的事务、时间和 JSON payload 是否因方言不同？必须用现有 SQLite 域内测试加 `store_pg_test.go` 冒烟；无 OS 特定路径，不能只凭 SQLite 绿。 |
| agentd API 域 | HTTP 状态、UTF-8 中文 flow/column、JSON RFC3339 是否依赖平台？机内 handler 测试覆盖；真实 Windows/macOS agentd 仅验收清单，不在逻辑域声称已验证。 |
| CLI 域 | Cobra flag、中文 stdout 和本地 SQLite 是否受 shell/平台影响？机内测试只证明参数/字节形状；PowerShell、zsh、Windows 路径由协调者真机清单。 |
| Web 域 | JSON/React 逻辑无 OS 路径；但 Wails WebView 的 select/focus/toast 不由 jsdom 证明，转入 4.6 真机清单。 |
| 契约域 | fixture 使用固定时区和固定时间，不依赖运行平台；必须逐字节比较，换 Go 版本导致格式变化也应变红。 |

### 4.6 假红测试族

- 账本测试不得只断言 Card 的内存返回值；必须读回 workflow_name/version/status、事件 payload
  和子卡行，覆盖真实事务。用例 A/B/C 要分别建卡，不能只把现有卡字段改成目标值。
- agentd 测试不得只断言 409；要持有真实 `cardStepFlight`、并发启动 step/迁移，并解码真实
  response JSON。把 `cardStepInFlight` 深 mock 成恒真/恒假会失去接缝价值。
- CLI 测试不能只看 help 文本；要在临时 ledger 上执行命令，验证默认 triage、目标参数、确认
  和 timeline；若选 agentd 路径，要用契约层 HTTP fake，不 mock handler 内部。
- Web 测试必须有真实 `fetch` body/response fixture；组件只各自塞一个 `{ok:true}` 会掩盖
  `from/to/event` 漏字段。fixture test 和 ledger API test 的边界都不能省。
- 契约测试的 `-update` 只能人工显式调用，CI 默认逐字节比对；不能把更新命令放入测试 setup
  让错 wire 自动刷新成绿。

### 4.7 webview / 平台表现差异族

本需求若仅新增 JSON 和 `<select>`/按钮，不增加 clipboard、cookie、File API 等浏览器能力，
故没有新的浏览器 API 假设；但 jsdom 仍不能证明真实桌面壳。协调者真机清单必须在至少
Chromium 与 Wails 使用的 WKWebView/平台 WebView 上检查：

1. 新建对话框的“未定性”选项可见、默认不偷偷选第一条流，键盘提交/取消焦点正常；
2. 迁移目标流改变后目标列列表刷新，不能提交旧流的旧列；
3. 409 在飞和 400 gate 错误能在抽屉中显示，按钮恢复可操作；
4. timeline 的 `workflow_migrated` 事件显示 from/to/actor，而不是落入空白系统项；
5. 断网/agentd 重启后的错误不会把卡假装迁移成功。

### 4.8 逐域审查矩阵（结论回填子卡验收）

| 触及域 | 生命周期/状态机 | 静默失败（序列化+枚举） | 跨平台假设 | 假红测试 | 门禁绕过 | webview 差异 |
|---|---|---|---|---|---|---|
| 账本域 | 单事务完成或全不完成；目标 flow/version/status 同源；终止态先复活，按飞状态由 API 负责。 | 读回 SQL 和事件，不只看返回 Card；triage/定性列/迁移事件登记在 seed、validator、fixture、switch。 | SQLite 必测，PG 冒烟；无 OS 路径假设。 | A/B/C、子卡和事件必须真实回放，不能只造结构体。 | 目标列 gate 每次重验；迁移到无闸是改定性，迁回有闸仍拒。 | 不接浏览器 API；无账本特有 webview 风险。 |
| 控制面 API 域 | `cardStepMu` 保护检查与迁移；agentd 重启清空在飞是已知边界，需真机确认。 | handler 实际 JSON 解码 DTO；`workflow/status/version`、from/to/event 和新 type 都有真实报文断言。 | HTTP/UTF-8 机内测，Windows/macOS agentd 只在真机清单确认。 | 并发 409 和 response body 不能深 mock；真实 SQLite 挂载。 | 409 在飞、400 target gate；contract gate 往返不能靠迁移偷过。 | jsdom 不足，select/焦点/toast 按 WebView 清单。 |
| CLI 域 | 直接 openLedger 只保证本地事务；能否观察另进程在飞取决于 agentd client 方案，不能假报。 | flag→Store/HTTP 参数和 stdout JSON 实跑；旧 `--to` 不与新 column 混淆；新值不硬编码漏项。 | shell、中文输出、SQLite 机内测；PowerShell/zsh/Windows 真机清单。 | 执行 Cobra 命令，不能只查 Usage；确认闸和事件输出都验。 | 不能用旧入口绕过目标 gate；若不经 agentd，明确只保证本地边界。 | 无浏览器接缝。 |
| Web 控制台域 | 一次提交 flow+column，成功后以响应/轮询刷新；网络失败不乐观改卡。 | fetch body、fixtures、timeline payload 和 event kind 全链路断言；不以 `{ok:true}` 自造响应。 | JSON 逻辑与 OS 无关；桌面壳行为另验。 | API/组件/fixture 三层不能互相自造数据替代。 | 400/409 原文展示，UI 不自行把 gate/在飞当成功。 | Chromium/WKWebView 逐项真机验 select、焦点、toast。 |
| 契约域 | fixture 失败必须在提交前暴露线格式变化；`-update` 不自动运行。 | 每个新增字段从产生端到消费端列文件并断言；`triage`、状态名、事件 type 登记既有检查点。 | 固定时区/时间样本，逐字节比对；不依赖本地时区。 | Go fixture 与 TS fixture 不能各自绿而不测真实 handler。 | DTO 不提供绕闸能力；契约只钉形状，gate 行为由账本/API 测。 | 不接 WebView；TS 侧只提供形状，真机属 Web 卡。 |

因此 §3 各域卡的验收栏不是泛泛引用：L/A/C/W 已逐项写出六族结论，T0 负责 wire 形状和
枚举登记，I 负责把矩阵中标出的真机/PG/CLI 现实检查收口。

## 5. 上下文预算检查

实例化清单 §5.1 的额外判据是“单包源文件数 ≥40 且无子包”。`internal/agentd` 命中 61 文件、
19,870 行；本需求没有把它整个当作一个上下文包，而是把入口收敛到 route/handler、step
互斥和 server 字段三处。若执行者发现需要修改 agentd 其他未列文件才能闭环，应先停在
竖切还债卡，不把新增文件偷偷塞进功能卡。

| 子卡 | 有界文件集 | 预算结论/越界信号 |
|---|---|---|
| B167-T0 | `internal/proto/ledger.go`、`contract_fixture_test.go`、新增账本 fixture、`web/src/api/contract.test.ts`、`web/src/api/ledger.ts` 类型区 | 有界；只做 DTO/fixture，不借机覆盖所有账本模型。 |
| B167-L | `internal/ledger/types.go`、`cards.go`、`workflows.go`、`cards_test.go`、`workflows_test.go`、`wire_test.go` | 有界，账本域 6,877 行远低于红线；若要改 `ledgerstep` 引擎或关系/镜像包，说明 seed/事务边界失控，应拆竖切。 |
| B167-A | `internal/agentd/ledgerapi.go`、`cardstep.go`、`server.go`、`ledgerapi_test.go`、`cardstep_test.go` | 当前可收敛；agentd 虽 61 文件，但本需求只触及已定位的五个文件。若为判断在飞而需读/改 watchdog、mirror、executor 全链，先立 agentd 竖切还债卡。 |
| B167-C | `cmd/card.go`、`cmd/workflow.go`、`cmd/card_test.go`、`cmd/workflow_test.go`、必要时 `cmd/ledgercli.go` | 有界；若选择 CLI 经 agentd，则新增 `internal/client` 是边界域扩张，必须升格为独立子卡/真机清单，不能藏在 CLI 卡。 |
| B167-W | `web/src/api/ledger.ts`、`ledger.test.ts`、`contract.test.ts`、`NewCardDialog.tsx`、`CardsPage.tsx`、`CardDrawer.tsx` 及对应测试、`testdata/*.json` | 有界；不改全局路由/状态管理。若目标列选择器需要重做看板列模型，先拆 Web 竖切卡。 |
| B167-I | 上述各卡的集成测试入口、`store_pg_test.go`、真机清单 | 集成卡不承载新业务类型；若需要改远端连接、executor 或桌面壳，说明范围已越过 B167，另立边界卡。 |

### 5.1 真机清单（归协调者执行）

以下不是域执行者的机内验收替代品，而是 B167-I 的显式现实检查：

- agentd step 派发中、等待裁决中、step 完成后分别尝试迁移；确认 409/成功边界；
- agentd 在 step 运行中重启，确认现有“不恢复在飞”的行为是否接受，若不接受立持久运行态卡；
- SQLite 与配置的 PG DSN 各跑 A/B/C，确认事务、事件顺序、中文列名和版本钉定；
- Chromium 与 Wails WebView 完成 4.7 五项；
- CLI 若选经 agentd 方案，验证 token、`--agentd` 地址和断线错误；
- 缺 contract 的 gate 往返场景，确认迁到无闸流后再迁回有闸列仍被拒；
- 父卡迁移后查看子卡、`children_total/done`、聚合闸和关系边，确认子卡不随迁。
