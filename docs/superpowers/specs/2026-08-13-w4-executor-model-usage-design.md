# W4d 实际模型名与 context 用量回读设计（B80）

**背景**：`Task.Model` 今天是**纯入参**——handoff 发下去什么就记什么，空串表示
「用执行者自己的默认」。四家 adapter 没有一家读回执行者**实际**在用的模型，
更没有一家读用量。原型 TUI 顶栏有这两样，真实产品没有。

**证据基础**：[2026-08-12-w4-tui-model-token-probe.md](../notes/2026-08-12-w4-tui-model-token-probe.md)
及其 08-13 的四次补验（§1.1 codex、§3.1 opencode、§4.1/§4.2 grok）。
本文所有报文均为**实抓**，不是读上游文档。探针笔记 §4 的原始结论（「grok 没有」）
已被 §4.1 推翻，本文以补验为准。

---

## 1. 目标与非目标

**目标**：任务详情页能回答两个问题——

1. 这个任务**实际**跑在哪个模型上？（不是我们请求的那个，是执行者报回来的那个）
2. 它现在的 context 占用是多少？有分母的执行者一并给出百分比。

**非目标**（本轮明确不做，理由见 §9）：累计消耗与花费、模型→窗口对照表、
OTel 接入、CLI 渲染改动、原型改动。

---

## 2. 取数规则

### 2.1 一条贯穿四家的原则

**分子一律取「最后一次模型调用的输入侧」，绝不取回合或会话的累加。**

这条是从 grok 的 §4.2 事故里长出来的，但四家都适用。一个回合里执行者可能调用
模型很多次（每次工具往返一次），累加值随工具调用无限增长——grok 那次
`modelCalls: 4` 的回合，累加值 138637 而真实占用约 34752，**差 4 倍**，
长回合会轻易超过 100%。累加值不是「占用」，是「消耗」，两者各有各的帧。

**「输入侧」= 送进模型的那部分**，不含本次的 output 与 reasoning。
缓存怎么算**每家不同**，必须逐家照 §2.2 的公式，不能按字段名类推。

### 2.2 四家的落点与公式

| executor | 分子来源 | 分子公式 | 分母来源 | 模型名来源 |
|---|---|---|---|---|
| **codex** | 通知 `thread/tokenUsage/updated` | `tokenUsage.last.inputTokens`（缓存**已含在内**） | 同帧 `modelContextWindow` | `thread/start` 响应顶层 `model` |
| **grok** | 通知 `_x.ai/session_notification`，`update.sessionUpdate == "response_completed"`，取**本回合最后一条** | `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`（缓存**要相加**） | 通知 `_x.ai/models/update`：`availableModels[modelId == currentModelId]._meta.totalContextTokens` | 同帧 `currentModelId`；也可从 `session/prompt` 响应的 `result._meta.modelId` 取 |
| **claudecode** | stream-json 的 `assistant` 消息，取**最后一条** | `usage.input_tokens + usage.cache_read_input_tokens + usage.cache_creation_input_tokens`（缓存**要相加**） | 无 | `system`/`subtype=init` 的 `model` |
| **opencode** | SSE `message.updated`，`info.role == "assistant"`，取**最后一条满足取数条件的** | `tokens.input + tokens.cache.read + tokens.cache.write`（缓存**要相加**） | 无 | 同帧 `info.modelID` |

**codex 是唯一「缓存已含在内」的一家**，实抓佐证：
`last.totalTokens 24673 = inputTokens 24668 + outputTokens 5`，
所以 `cachedInputTokens 9984` 是 `inputTokens` 的子集，再加一次就是重复计数。

### 2.3 每家的实抓报文与注意事项

#### codex（codex-cli 0.144.1）

```json
{"method":"thread/tokenUsage/updated","params":{
  "threadId":"019ffb3d-…","turnId":"019ffb3d-…",
  "tokenUsage":{
    "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
             "outputTokens":5,"reasoningOutputTokens":0},
    "last":{"…同结构…"},
    "modelContextWindow":258400}}}
```

- 取 `last` 不取 `total`：`total` 是整个 thread 的累加（§2.1）。
- 这条通知**排在 `turn/completed` 之前**到达，回合结束时数据已经在手。
- `turn/completed` 的报文里**没有**用量字段，别去那儿找。
- 模型名在 `thread/start` 的响应顶层：`{"model":"gpt-5.6-sol","modelProvider":"openai",
  "serviceTier":"default","reasoningEffort":"high",…}`。
  现在 `openThread` 只解 `thread.id`，其余整块丢弃
  （[codex/adapter.go:279](../../../internal/executor/codex/adapter.go:279)）。
- 通知落在 `default: a.log.Debug("codex 未处理的通知", …)`
  （[codex/adapter.go:785](../../../internal/executor/codex/adapter.go:785)），加一个 case 即可。

#### grok（grok 1.0.3）

分子（**每次模型调用后各一条，取本回合最后一条**）：

```json
{"method":"_x.ai/session_notification","params":{"sessionId":"…","update":{
  "sessionUpdate":"response_completed",
  "usage":{"input_tokens":64,"output_tokens":34,"cache_read_input_tokens":34688,
           "cache_creation_input_tokens":0,"reasoning_tokens":19}}}}
```

分母（会话建立后即到，回合开始前）：

```json
{"method":"_x.ai/models/update","params":{"currentModelId":"grok-4.6",
  "availableModels":[{"modelId":"grok-4.6","name":"Grok 4.6",
    "_meta":{"totalContextTokens":500000,…}}]}}
```

- **同一条线上两套命名，缓存算法相反**：snake_case 的 `response_completed`
  里 `input_tokens` 不含缓存、要相加；camelCase 的 `turn_completed` 里
  `inputTokens` 已含缓存。**按字段名模糊匹配必错。**
- `turn_completed` / `session/prompt` 响应的 `_meta.usage` 是**整回合跨调用累加**
  （§4.2 实证：`inputTokens 138637` = 四次调用的 `input_tokens` 之和加
  `cache_read` 之和），本轮**不取**它做分子。它是将来做累计消耗的正确来源。
- 两条都是 `_x.ai/*` 私有通知。**连接层不丢它们**：`ACPClient.readLoop` 的
  `case msg.Method != "": h.OnNotify(...)`
  （[grok/acp.go:255](../../../internal/executor/grok/acp.go:255)）会原样交给 adapter。
  真正丢掉它们的是 **`acpHandler.OnNotify` 的第一行早返回**：
  `if method != "session/update" { return }`
  （[grok/adapter.go:816](../../../internal/executor/grok/adapter.go:816)）——
  私有通知在这里就没了，压根到不了 `feedRaw`。
  **改动点是这个早返回，不是 `feedRaw`**：`feedRaw` 的职责是拼正文，
  用量不该进正文缓冲，它一行都不改。
- `availableModels` 数组可能含多个模型，**必须按 `currentModelId` 匹配**，
  不能取第 0 个。
- **分子与分母来自不同的帧**：`models/update` 在会话建立后立刻到（回合尚未开始），
  `response_completed` 在每次模型调用后到。adapter 必须把窗口值暂存在 runState 里，
  在发出用量事件时一并带上——只发分子会让分母永远补不上（manager 侧「nil=不更新」
  保护的是已落库的值，不是没落过库的值）。codex 不存在这个问题（同一帧里两样都有）。

#### claudecode

```json
{"type":"assistant","session_id":"…","message":{
  "model":"k3-256k","content":[…],
  "usage":{"input_tokens":121801,"cache_creation_input_tokens":0,
    "cache_read_input_tokens":0,"output_tokens":0,"service_tier":"standard"}}}
```

- **`model` 与 `usage` 在 `message` 对象内部，与 `content` 同级**——不在顶层。
  `mapAssistant(r, m.Message)` 收到的正是这个 `message` 对象
  （`streamMsg.Message` 是 `json.RawMessage`，见
  [claudecode/stream.go:46](../../../internal/executor/claudecode/stream.go:46)），
  在它现有的匿名结构体里加两个字段即可，不需要改 `streamMsg`。
- 模型名优先取 `system`/`init` 的 `model`；`result` 行的 `model` 是 `null`，别去那儿取。
- `assistant` 消息一个回合有几百条，manager 侧靠 §3.3 的去重防写库风暴。
- 现有 `mapAssistant` 只解 `content`
  （[claudecode/adapter.go:567](../../../internal/executor/claudecode/adapter.go:567)），
  需要多解 `model` 与 `usage`。

#### opencode

```json
{"type":"message.updated","properties":{"sessionID":"ses_…","info":{
  "id":"msg_…","role":"assistant","cost":0.0001408596,
  "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
            "cache":{"write":0,"read":46464}},
  "modelID":"deepseek-v4-flash","providerID":"opencode-go",
  "time":{"created":…,"completed":…},"finish":"tool-calls"}}}
```

- **取数条件（必须有）**：`info.role == "assistant"` **且**
  `tokens.input + tokens.cache.read + tokens.cache.write > 0`。
  实测同一条消息会被推多次，且**新建的 assistant 消息 tokens 全是 0**
  （探针 §3.1）。没有这个条件，界面会在每条新消息开头闪回 0。
- **不要取 `tokens.total`**：`total 47071 = input 131 + output 182 +
  reasoning 294 + cache.read 46464`，含产出侧，不是占用。
- 现有 `mapMessageUpdated` 只解 `info.id` 与 `info.role`
  （[opencode/adapter.go:1390](../../../internal/executor/opencode/adapter.go:1390)），
  需要多解 `modelID` 与 `tokens`。

---

## 3. 数据通路

### 3.1 复用 `SessionID` 那条路，不发明新通道

adapter 已经有一条「报告一个事实、由 manager 落到 Task 上」的成熟路径：
`AdapterEvent.SessionID` → `handleProgress` → `SetTaskField("executor_session")`
（[manager.go:2383](../../../internal/agentd/manager.go:2383)）。
本设计原样沿用这个形状，只是多两个字段、多一个事件类型。

**adapter 的边界不变**：不写 store、不做判断，只把观察到的事实经 Events 通道报出去。

### 3.2 新增内部事件类型 `usage`

```go
// internal/executor/executor.go
type AdapterEvent struct {
    Type         string // "permission" | "question" | "progress" | "result" | "usage"
    PermissionID string
    QuestionID   string
    SessionID    string
    Text         string
    Perm         *PermRequest
    Result       *Result

    // ActualModel 是 executor 报回的**实际**模型名；空=本帧没带模型信息。
    // 它与 Task.Model（入参）是两件事：入参可能为空而实际总有值。
    ActualModel string
    // Usage 是当前 context 占用快照；nil=本帧没带用量。
    // 语义见 proto.Usage：绝不用 0 冒充「没有用量」。
    Usage *proto.Usage
}
```

**为什么是新的 adapter 事件类型而不是新的 `proto.EventType`**：
`usage` 只在 adapter→manager 的进程内通道上存在，manager 收到后**只写任务字段，
不追加事件行、不广播**。任务事件日志是审核者要读的东西，几百条用量刷新进去
只会淹没真正需要看的 permission/question/completed。界面的刷新由既有的 4 秒轮询
（`web/src/app/task/useTaskSession.ts` 的 `DETAIL_POLL_INTERVAL = 4000`）承担，
不需要事件推送。

**为什么不搭在既有的 progress 事件上**：progress 会追加事件行并广播，
频率对不上（claudecode 一个回合几百条 assistant 消息）。

### 3.3 manager 侧

```go
// handleUsage 落 executor 报回的实际模型名与 context 占用。
//
// 与 handleProgress 的区别：只写任务字段，不追加事件、不广播——用量刷新频率
// 高（claudecode 一个回合几百条 assistant 消息），进事件日志会淹没审核者真正
// 要看的 permission/question/completed。界面靠详情轮询自然拿到。
//
// 去重：与内存里上一次的值相同就直接返回，不打库。这是写库风暴的唯一防线。
func (m *Manager) handleUsage(taskID string, ev executor.AdapterEvent)
```

- 去重键：`(ActualModel, ContextTokens, ContextWindow)` 三元组全等则跳过。
- 内存态随任务生命周期存活；agentd 重启后首帧必写一次（内存态空），可接受。
- 落库失败仅 `Warn`，不影响主流程——用量属可修复的辅助字段，与
  `executor_session` 同级。

### 3.4 远程任务

远程任务经 `mirror_tasks.snapshot`（任务体 JSON 的副本）回到本机，
新字段随 JSON 自动过来，**无需改镜像层**。
对端 agentd 版本旧时字段缺席 → `nil` → 界面按 §6 显示缺省，是正确的降级。

---

## 4. 数据形状

### 4.1 `proto.Usage`

```go
// Usage 是任务当前的 context 占用快照。
//
// 「当前占用」= 最后一次模型调用的输入侧（含缓存命中），不是回合或会话的累加。
// 两者差别巨大：一个 4 次模型调用的 grok 回合，累加值是真实占用的 4 倍。
//
// 边界：本结构只描述占用，不描述消耗。累计 token 与花费是另一个口径，
// 将来以新增字段的形式加进来（形状不变，不需要重新设计）。
type Usage struct {
    // ContextTokens 是当前 context 占用的 token 数。永远 > 0——
    // 取不到时整个 Usage 为 nil，不用 0 冒充。
    ContextTokens int `json:"context_tokens"`
    // ContextWindow 是该模型的上下文窗口上限（分母）。
    // nil = 该 executor 不在协议里报窗口（claudecode / opencode），
    // 此时界面只显绝对值不显百分比。**绝不由 handoff 猜**（why 见 spec §9）。
    ContextWindow *int `json:"context_window,omitempty"`
}
```

### 4.2 `proto.Task` 新增两个字段

```go
// ActualModel 是 executor 报回的**实际**模型名；空=执行者还没报（回合未开始）
// 或该任务跑在不报模型名的旧版执行者上。
//
// 它与 Model 是两件事：Model 是 dispatch --model 发下去的**入参**（常为空，
// 表示「用执行者的默认」），ActualModel 是执行者**实际**在用的那个。
// 二者不一致时以 ActualModel 为准，界面不并列显示（W4d 决策）。
ActualModel string `json:"actual_model,omitempty"`

// Usage 是当前 context 占用；nil=还没有任何一次模型调用完成。
Usage *Usage `json:"usage,omitempty"`
```

### 4.3 SQLite

三个新列，按既有的「逐列 ALTER + 容忍 duplicate column」迁移写法
（[store.go:183](../../../internal/store/store.go:183) 的 map 里加三项）：

| 列 | 类型 | 0/空 的含义 |
|---|---|---|
| `actual_model` | `TEXT NOT NULL DEFAULT ''` | 执行者还没报 |
| `usage_context_tokens` | `INTEGER NOT NULL DEFAULT 0` | 还没有完成的模型调用 |
| `usage_context_window` | `INTEGER NOT NULL DEFAULT 0` | 该 executor 不报分母 |

**0 可以安全地表示「没有」**：任何一次真实的模型调用输入都 > 0，
任何真实的上下文窗口也 > 0。扫描时按此还原成 `nil`：
`usage_context_tokens == 0` → `Task.Usage = nil`；
`usage_context_window == 0` → `Usage.ContextWindow = nil`。

### 4.4 store 新增方法

`SetTaskField` 只接受字符串且一次一列，用它写三个值要三次 UPDATE、且会把
整数塞进字符串参数。新增专用方法，一次 UPDATE 写完：

```go
// SetTaskUsage 一次性更新任务的实际模型名与 context 占用。
//
// 参数：
//   - model: 实际模型名；空串表示本次不更新该列（保留既有值）
//   - ctxTokens: 当前 context 占用；0 表示不更新
//   - ctxWindow: 上下文窗口；nil 表示不更新（**不是**清零——不报分母的
//     executor 每次都传 nil，清零会把已知的分母抹掉）
//
// 注意：任务不存在时不报错（与 SetTaskField 一致，不影响其他行即返回 nil）。
func (s *Store) SetTaskUsage(id, model string, ctxTokens int, ctxWindow *int) error
```

「空值=不更新」而非「空值=清空」是刻意的：一帧只带模型名、另一帧只带用量，
两者不该互相覆盖。

---

## 5. 界面

改一行：`web/src/app/task/TaskHeader.tsx:61` 的「执行器」行。

| 情况 | 显示 |
|---|---|
| 有模型名 + 有分子分母 | `codex · gpt-5.6-sol · 24.7k / 258k (10%)` |
| 有模型名 + 只有分子 | `claudecode · k3-256k · 121.8k tokens` |
| 有模型名 + 无用量（回合未开始） | `codex · gpt-5.6-sol` |
| 都没有（刚派发 / 旧任务 / 旧 agentd） | `opencode` |
| 连 executor 都没有（老任务） | `（缺省）` ← 现状不变 |

规则：

- **模型名只显实际值**。`Task.Model` 是入参，不再显示；二者不一致也不提示
  （W4d 决策：用户要知道的是「现在实际跑在什么上」，入参是我们自己的事）。
- 数字用 `k` 缩写、保留一位小数（`24673` → `24.7k`）；`< 1000` 显示原值。
- 百分比取整，只在 `ContextWindow != nil` 时出现。
- 每一段都是「有才显示」，不占位、不显示 `—`、不显示 `0`。

---

## 6. 缺席语义

沿用 B69/B70 已确立的纪律，本设计不新增例外：

- 指针 + `omitempty`，`nil` 表示「取不到」。
- **绝不用 0 冒充「没用 token」**——那是编造。
- 分母缺席就不显示百分比，不去猜、不去查表。

**「不猜分母」是本设计的硬约束**，理由在探针笔记里已经写死：
handoff 自己维护一张「模型→窗口大小」表会过时、会漏新模型，而漏了是**静默错误**
（百分比照常显示，只是错的）。§4.2 那个 4 倍偏差就是同一类错误的实例——
它没有报错，它只是错了。

---

## 7. 日志与注释

按 `instrumenting-code` 的要求，实现时必须有：

**日志**（用 `a.log` / `m.log`，禁止 `fmt.Printf`）：

- 每家 adapter 首次解析出实际模型名：`Info`，带 `task` 与 `model`。
- 每家 adapter 解析用量帧失败：`Debug`（对端输出不可信，宽容跳过，
  与既有的「未知消息类型，跳过」同级），带 `task` 与 `cause`。
- manager `handleUsage` 落库失败：`Warn`，带 `task`、`model`、`tokens`、`cause`。
- manager `handleUsage` 去重命中：**不打日志**（高频，打了就是刷屏）。
- 首次为某任务落用量：`Info` 一次，带 `task`、`tokens`、`window`；
  后续变更不打（靠去重后的库值可查）。

**注释**：

- `proto.Usage` 的文件/类型注释写清「占用 ≠ 消耗」的边界（§4.1 已给出正文）。
- 每家 adapter 的取数处写「为什么是这个公式」的中文注释，
  **尤其是缓存加不加**——codex 加了就是重复计数，grok/claudecode/opencode
  不加就是少算，两个方向都会错且都不报错。
- grok 的取数处必须注明「不取 `turn_completed`，它是跨调用累加」，
  并指向探针笔记 §4.2。这是最容易被后人"顺手改对称"改坏的一处。

---

## 8. 测试

单元测试为主，四家 adapter 各一组，**输入用本文 §2.3 的真实报文原文**：

| 测试 | 断言 |
|---|---|
| 四家各自的解析 | 给一帧真实报文，得到期望的 `ActualModel` 与 `Usage` |
| codex 缓存不重复计数 | `last.inputTokens=24668, cachedInputTokens=9984` → `ContextTokens=24668` |
| grok 缓存要相加 | `input_tokens=64, cache_read=34688` → `ContextTokens=34752` |
| grok 不取回合累加 | 喂一条 `turn_completed`（`inputTokens=138637`）→ **不产生** Usage |
| grok 分母按 currentModelId 匹配 | `availableModels` 含多个模型时取对那一个 |
| opencode 零值帧被跳过 | `tokens` 全 0 的 `message.updated` → 不产生 Usage（不覆盖既有值） |
| manager 去重 | 同值连续三次 → 只打库一次 |
| manager 只写字段不发事件 | 处理 `usage` 事件后事件表行数不变 |
| store 空值不清空 | `SetTaskUsage(id,"",0,nil)` 后三列保持原值 |
| 扫描还原 nil | 三列为 0/空的行 → `Task.Usage == nil && ActualModel == ""` |

`go test ./...` 全绿是完工前提。

---

## 9. 不做的

| 不做 | 为什么 |
|---|---|
| **累计消耗与花费** | 用户决策：本轮只显当前占用，累计留字段扩展位。数据源已探明（codex 的 `total`、grok 的 `turn_completed.usage` 含 `costUsdTicks`、opencode 的 `cost`），将来加字段即可，形状不变 |
| **模型→窗口对照表** | 会过时、会漏新模型，漏了是**静默错误**。claudecode 与 opencode 就老实不显百分比 |
| **接 OpenTelemetry** | grok 的 OTel 指标（`grok_code.token.usage`）现在没必要了——协议里就有。为读自己任务的 token 起一个 collector，代价与收益不成比例 |
| **CLI 渲染改动** | `handoff show` 输出任务 JSON，新字段自动可见，够用。`status`/`tasks` 的表格不加列 |
| **动 `feedRaw`** | 它的职责是拼正文，用量不该进正文缓冲。新增处理挂在 `OnNotify` 分流上 |
| **回合中途的实时刷新** | grok 的 `_meta.totalTokens`、codex 的多次 `tokenUsage/updated` 都能做到更细粒度，但每次模型调用后更新一次已经够用，4 秒轮询也追得上 |
| **原型改动** | B80 的「原型/流程图」列是 `—`，本轮不建原型，验收自动免除对照（backlog 证据门规则） |

---

## 10. 验收标准

1. `go test ./...` 全绿，含 §8 的全部用例。
2. 四家 executor 各派一个真实任务，任务详情页的「执行器」行显示实际模型名；
   codex 与 grok 另显 `x / y (z%)`，claudecode 与 opencode 只显绝对值。
3. grok 的百分比在一个含多次工具调用的回合后**仍然合理**（不超过 100%、
   与 `/context` 类的自报口径同量级）——这是 §4.2 那个 4 倍偏差的回归检查。
4. 事件日志里**没有**因用量而新增的事件行。
5. 旧任务（本功能上线前派发的）详情页不报错，按 §5 最后一行显示。
