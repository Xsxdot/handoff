# 契约增量：执行耗时的采集、落账与展示（需求 A）

日期 2026-08-22 · 前置 [spec](2026-08-22-executor-timing-and-custom-launchers-design.md) §A · 节点 `charter:contract`

**冻结物**：本文档 + `codegraph/target.json` 的契约条目 + Ticket 0 骨架提交。此后契约变更 = 重走 contract 节点。

每个签名都对着现状代码查证过，出处以 `文件:行` 标注（基线为本轮工作树，非引用早前的读数）。

---

## 1. 现状事实（查证结果）

| 事实 | 出处 | 对本次的意义 |
|---|---|---|
| `SpendEntry{Key, InputTokens, CachedTokens, OutputTokens, CostTicks, CostState}` | [proto.go:206](internal/proto/proto.go:206) | 耗时账目要同构的原型 |
| `Cumulative` 由 `Store.TaskCumulative` 对 `task_usage_ledger` 求和产出 | [proto.go:187](internal/proto/proto.go:187)、[store.go:613](internal/store/store.go:613) | 聚合结果的落点原型 |
| `Task.Cumulative *Cumulative` `json:"cumulative,omitempty"` | [proto.go:286](internal/proto/proto.go:286) | 聚合结果挂在 Task 上的先例 |
| `GetTask` 填 `Cumulative`；`ListTasks` **刻意不填** | [store.go:361](internal/store/store.go:361)、[store.go:369](internal/store/store.go:369) 注释 | 耗时聚合沿用同一条纪律 |
| `task_usage_ledger` 主键 `(task_id, entry_key)`，`CREATE TABLE IF NOT EXISTS` 列在迁移数组里 | [store.go:108](internal/store/store.go:108) | 新表同形建法 |
| `Store.UpsertSpend(taskID string, e proto.SpendEntry) error`，Key 为空即报错，同键**覆盖** | [store.go:579](internal/store/store.go:579) | 新写入方法同形 |
| `AdapterEvent.Spend *proto.SpendEntry`，Type 取值含 `"usage"` | [executor.go:195](internal/executor/executor.go:195)、[executor.go:167](internal/executor/executor.go:167) | 耗时走同一条事件通道 |
| `m.handleUsage(taskID, ev); m.handleSpend(taskID, ev)` 并列在 `adapterEventUsage` 分支 | [manager.go:1640](internal/agentd/manager.go:1640) | `handleTiming` 的插入点 |
| `handleSpend` 只写库、不追加事件行、不广播；落库失败仅 Warn | [manager.go:2898](internal/agentd/manager.go:2898) | 耗时入账同款处置 |
| `Frame` 结构，字段按 `Type` 取用、无关字段 `omitempty` 缺席 | [frames.go:44](internal/proto/frames.go:44) | `dur_ms` 的挂载处 |
| `FrameWriter.ToolCall(part, tool, input string) error` | [turn/frames.go:187](internal/executor/turn/frames.go:187) | 签名不变 |
| `FrameWriter.ToolResult(part, status, output string) error` | [turn/frames.go:202](internal/executor/turn/frames.go:202) | **本次要改签名** |
| `ToolCall`/`ToolResult` 全仓只有两个调用方 | [claudecode/adapter.go:685](internal/executor/claudecode/adapter.go:685)、[:726](internal/executor/claudecode/adapter.go:726)、[codex/adapter.go:1128](internal/executor/codex/adapter.go:1128)、[:1137](internal/executor/codex/adapter.go:1137) | 改签名的爆炸半径只有 2 处 |
| grok 把 `tool_call` / `tool_call_update` 归 `updateNone` | [grok/adapter.go:727](internal/executor/grok/adapter.go:727) | 要补工具帧的落点 |
| opencode 的 `frameKind` 对 tool part 返回 `kindSkip` | [opencode/adapter.go:1516](internal/executor/opencode/adapter.go:1516) | 同上 |
| `handoff show` 单行输出 `AttachInfo` JSON，无任何旗标 | [cmd/show.go:22](cmd/show.go:22) | **CLI 侧零改动即可拿到**（见 §5） |

### 1.1 依赖库既成行为查证

- **`claudecode` 的 `out.jsonl` 轮询间隔是 200ms**（`tailPollInterval`，[stream.go:33](internal/executor/claudecode/stream.go:33)）。这是「打点必须在 adapter 内贴着协议事件、不能按写帧时刻倒推」的量化理由：一次工具调用的两端各带最多 200ms 抖动。
- **`FrameWriter` 对 nil 接收者是空操作**（[turn/frames.go:52](internal/executor/turn/frames.go:52) 注释）。`ToolResult` 改签名后这条性质必须保持。

### 1.2 对侧常量查执法

- `CostState` 四个常量（[proto.go:152](internal/proto/proto.go:152)）：`reported` / `estimated` / `unknown` 由 adapter 的 `spend.go` 发出、由 `TaskCumulative` 消费；`partial` **只在求和后产生**，任何 adapter 都不发。本次的 `TimingState`（§2.3）刻意**不复制**这套四态——耗时没有「估算」这一档，照抄会造出一个永远没人发的死常量。
- **疑似漂移一处**：[opencode/adapter.go:1511](internal/executor/opencode/adapter.go:1511) 的注释称「工具调用本身由 mapToolPart 以完整的 tool_call 帧上报」，但 `mapToolPart` 在全仓不存在，opencode 一个工具帧都不产。该注释不得作为事实源头，实现时一并删除。

---

## 2. 契约增量

### 2.1 `proto.TimingKind`（新增，`internal/proto/timing.go`）

```go
type TimingKind string

const (
    TimingKindAPI  TimingKind = "api"   // 模型段
    TimingKindTool TimingKind = "tool"  // 工具段
    TimingKindTurn TimingKind = "turn"  // 回合墙钟（**不是段**，是分母，见 §3.1）
)
```

`other`（未归类）**不是一个 Kind**：它只在聚合层由差额算出，adapter 永不上报（spec §A.3 规则 2）。

### 2.2 `proto.TimingEntry`（新增）

```go
type TimingEntry struct {
    Key    string     // 任务内稳定且唯一的幂等键；空即拒写（同 SpendEntry）
    Kind   TimingKind // api / tool / turn
    Turn   int        // 所属回合号，从 1 开始；与 Frame.Turn 同一编号
    DurMS  int64      // 时长（毫秒）。>= 0；未知不上报该条，不用 0 冒充
    Label  string     // Kind=tool 时的工具名（如 "Bash"）；其余为空
    Detail string     // Kind=tool 时的命令/入参摘要，按 §3.4 截断；其余为空
    // OffsetMS 是 Kind=tool 时相对本回合起点的毫秒偏移；api / turn 恒为 0。
    // 它存在的唯一理由是让聚合层算得出「工具占用的墙钟跨度」（§3.3）——
    // 只有 DurMS 算不出并集。删掉它，并发工具任务的 OtherMS 会静默变成 0。
    OffsetMS int64
}
```

**同 Key 重复上报按覆盖，不累加**——与 `SpendEntry` 同一条幂等语义（[proto.go:202](internal/proto/proto.go:202) 注释），不另立一套。

**幂等键构造（内容派生，不用进程内计数器）**：

| Kind | Key |
|---|---|
| tool | `tool/<turn>/<part>` —— `part` 即 `Frame.Part`，回合内唯一（[frames.go:56](internal/proto/frames.go:56)） |
| api | `api/<turn>/<n>` —— `n` = 本段之前**已完成的工具批次数**（首段为 0） |
| turn | `turn/<turn>` |

`api` 的 `n` 刻意从内容派生而非用计数器：agentd 重启或 SSE 重放后计数器归零，会把第二段写成第一段的键、覆盖掉真数据。

### 2.3 `proto.TaskTiming`（新增，聚合结果）

```go
type TaskTiming struct {
    TotalMS int64 `json:"total_ms"` // Σ turn 条目
    APIMS   int64 `json:"api_ms"`
    ToolMS  int64 `json:"tool_ms"`  // Σ 工具段（并发时可 > ToolSpanMS）
    // ToolSpanMS 是工具占用的**墙钟跨度**，OtherMS 用它算。
    ToolSpanMS int64 `json:"tool_span_ms"`
    OtherMS    int64 `json:"other_ms"` // TotalMS − APIMS − ToolSpanMS，下限 0
    // Partial 为真表示至少有一个回合缺 api 或 tool 条目，OtherMS 因此偏大。
    // 界面必须能读出这一点，不得把 Partial 的 OtherMS 当作真实空档展示。
    Partial bool `json:"partial"`
    // Buckets 按工具名聚合，降序，最多 TimingBucketCap 条。
    Buckets []TimingBucket `json:"buckets,omitempty"`
}

type TimingBucket struct {
    Label string `json:"label"` // 工具名；下钻层为命令首词
    DurMS int64  `json:"dur_ms"`
    Count int    `json:"count"`
    Sub   []TimingBucket `json:"sub,omitempty"` // 仅一层下钻，同样 Cap 条
}

const TimingBucketCap = 20
```

**缺席即 nil**：一条耗时账目都没有时，`Store.TaskTiming` 返回 `(nil, nil)`——**不返回零值结构**。理由与 `TaskCumulative` 逐字相同（[store.go:601](internal/store/store.go:601) 注释）：`0` 会被读成「一共花了 0」，而真相是「还不知道」。界面据此显示「—」。

### 2.4 `proto.Task` 增一字段

```go
// Timing 是任务的耗时聚合；nil = 没有任何耗时账目（或本次是列表读取）。
// 与 Cumulative 同一条纪律：GetTask 填，ListTasks 不填。
Timing *TaskTiming `json:"timing,omitempty"`
```

### 2.5 `proto.Frame` 增一字段

```go
// DurMS 是 tool_result 帧配对的那次工具调用的耗时（毫秒）。
// 缺席 = 该 executor 没报出耗时；**不用 0 表示未知**。
// 它是账本的投影、不是真相（见 spec §A.5 第 5 条）。
DurMS int64 `json:"dur_ms,omitempty"`
```

只出现在 `tool_result` 帧上。`turn_start` / `text` / `reasoning` / `tool_call` / `event` 一律不带。

### 2.6 `turn.FrameWriter.ToolResult` 改签名

```go
// 现状（turn/frames.go:202）
func (w *FrameWriter) ToolResult(part, status, output string) error
// 契约后
func (w *FrameWriter) ToolResult(part, status, output string, dur time.Duration) error
```

`dur < 0` 表示「不知道耗时」→ 落帧时省略 `dur_ms`。**不用 `0` 表达未知**：0ms 是一次真实可能的极快调用。nil 接收者仍为空操作。

爆炸半径已查证：全仓只有 2 个调用方（§1 表）。补 grok/opencode 工具帧后变成 4 个。

### 2.7 `executor.AdapterEvent` 增一字段

```go
// Timing 是这一段的耗时账目；nil = 本帧不带耗时信息。
// 与 Spend 并列走 Type="usage" 事件（不新增事件类型），语义互不交叉。
Timing *proto.TimingEntry
```

### 2.8 `store` 新增两个方法与一张表

```go
func (s *Store) UpsertTiming(taskID string, e proto.TimingEntry) error
func (s *Store) TaskTiming(taskID string) (*proto.TaskTiming, error)
```

```sql
CREATE TABLE IF NOT EXISTS task_timing_ledger (
  task_id TEXT NOT NULL,
  entry_key TEXT NOT NULL,
  kind TEXT NOT NULL,
  turn INTEGER NOT NULL DEFAULT 0,
  dur_ms INTEGER NOT NULL DEFAULT 0,
  label TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (task_id, entry_key))
```

与 `task_usage_ledger` 同形（[store.go:108](internal/store/store.go:108)）：`CREATE TABLE IF NOT EXISTS` 进同一个迁移数组，无需版本号迁移。

### 2.9 `agentd.Manager` 新增一个处置方法

```go
func (m *Manager) handleTiming(taskID string, ev executor.AdapterEvent)
```

插在 [manager.go:1640](internal/agentd/manager.go:1640) 的 `adapterEventUsage` 分支，与 `handleSpend` 并列。处置纪律逐条照抄 `handleSpend`：只写库、不追加事件行、不广播、落库失败仅 Warn。

---

## 3. 语义细则（本节即冻结的行为契约）

### 3.1 `turn` 条目不是段

`TimingKindTurn` 承载的是**回合墙钟**，是 `other` 的分母，不参与「模型段/工具段/未归类」三分。spec §A.3 的封闭集合 `{api, tool, other}` 指的是**段**，不是 `TimingKind` 的取值域。

adapter 在回合开始时写一条 `turn/<n>` 条目（`DurMS` 为当前已耗），并在回合推进/终结时**按同键覆盖**刷新——所以运行中的任务也能读到实时的 `TotalMS`，而不必等回合结束。

### 3.2 `other` 只在聚合层产生

`OtherMS = max(0, TotalMS − APIMS − ToolSpanMS)`。取 `max(0,·)` 是防御而非语义：真出现负数说明采集有 bug，此时 `Partial` 必为真，界面据此收敛显示。

### 3.3 并发工具

`ToolMS`（各段之和）与 `ToolSpanMS`（墙钟跨度）**同时给出，互不冒充**。`ToolSpanMS` 由聚合层按同回合内工具段的区间并集算出——**因此 `TimingEntry` 的 `tool` 条目必须能还原区间**。

> **这一条是本节点发现的、spec 没定的语义缺口**：只有 `DurMS` 算不出区间并集。补法已落进 §2.2 的 `OffsetMS`（相对该回合 `turn_start` 的毫秒偏移）；`api` 与 `turn` 条目不用它，恒为 0。拍板记录见 §6.2。

### 3.4 `Detail` 的截断与凭据边界

`Detail` 上限 **200 rune**，超出头尾截断（复用 `turn.headtail` 的既有形态，[turn/headtail.go](internal/executor/turn/headtail.go)）。

它不是新的凭据暴露面：同一份命令原文今天已经以 `Frame.Input` 的形式落在 `frames.jsonl`（[turn/frames.go:196](internal/executor/turn/frames.go:196)）。但**它确实把命令文本引入了 SQLite**，实现时不得放宽这个上限，也不得改存全文。

### 3.5 兼容性

- `Frame.DurMS` 是 `omitempty` 新字段：旧帧读回来是 0 → 「不知道」，与新采集的 0ms 不可区分。**接受这个歧义**——0ms 的工具调用在实测中不存在（最快的 Read 也在毫秒级），而为它引入指针会让每一帧多一次分配。
- `Task.Timing` 为 nil 时前端显示「—」，历史任务恒为 nil（spec §A.8 已列 Out of Scope）。
- 新版 agentd + 旧版前端：多一个 JSON 字段，前端忽略，无影响。

---

## 4. 目标图契约条目（`codegraph/target.json`）

本次**不新增任何跨域方向**——所有调用都落在已有方向上（`graph check` 的 `legacyHits` 实测见下）。但每条方向的 `legacyBudget` 今天都**恰好等于**当前命中数（棘轮咬死），任何新增直调都会 `over-budget`。因此本节点的动作是：**把本次要用到的契约面显式声明成 `entries`，并把对应的 legacy 预算按实测命中数下调**。

实测（本轮 `handoff graph check` 输出 + 按 callee 容器分解）：

| 方向 | 声明的 entries | 该容器命中数 | legacyBudget 改为（原） |
|---|---|---|---|
| `d_executor -> d_contract` | `proto 实体` | 1 | **0**（1） |
| `d_ledger -> d_contract` | `proto 实体` | 29 | **2**（31） |
| `d_controlplane -> d_contract` | `proto 实体` | 165 | **27**（192） |
| `d_controlplane -> d_ledger` | `store.Store` | 109 | **3**（112） |
| `d_controlplane -> d_executor` | `executor 实体` | 14 | **2**（16） |
| `d_cli -> d_contract` | `proto 实体` | 28 | **22**（50） |

这是**棘轮下调**（legacy 只减不增），不是提额：被声明的那部分从「历史欠账」转成「明示的契约面」，剩余欠账数字变小。

---

## 5. CLI 侧：零新增端点、零新增旗标

`handoff show <task>` 单行输出 `AttachInfo` JSON（[cmd/show.go:22](cmd/show.go:22)），其中的 `Task` 由 `GetTask` 产出、因而自带 `timing`。spec §A.4 用户故事 4（命令行拿到同一份结构化数字）**因此无需任何 CLI 改动**。

弃选：新增 `handoff timing <task>` 子命令或 `show --timing` 旗标。弃因——数据本来就在 `show` 的输出里，多一个命令只是多一个会漂移的入口。

---

## 6. 拍板记录

### 6.1 `turn` 作为 `TimingKind` 的第三个取值，但不是「段」

- **难逆转**：改了要动 adapter（发）、store（存）、聚合（算）、前端（显）四处。
- **无上下文会惊讶**：后人看到 `TimingKind` 有三个值、而 spec 说段只有三种（api/tool/other），会想「修掉」这个不一致——把 `turn` 当段加进汇总，于是总时长被计两遍。
- **真取舍**：被否的方案是让聚合层从 `frames.jsonl` 重建回合边界。否因——聚合层读 SQLite 不读帧文件，为一个分母把整条帧读路径接进 store，接缝比收益大得多。

### 6.2 `TimingEntry.OffsetMS` 与「工具墙钟跨度」

- **难逆转**：字段一旦不存，已采集的历史数据永远算不出并集，只能重跑任务。
- **无上下文会惊讶**：只看 `DurMS` 的人会觉得 `OffsetMS` 是冗余（「时长不就够了吗」）而顺手删掉——删掉之后 `OtherMS` 在并发工具的任务上系统性变成 0，且不报错。
- **真取舍**：被否的方案是只存 `ToolMS` 之和、`OtherMS` 用 `Total − API − ToolMS`。否因——claude 并行发多个 `tool_use` 时该式必然算出负数，而负数会被 `max(0,·)` 吞掉，变成一个静默为 0 的假结论。

### 6.3 耗时走 `Type="usage"` 事件而不是新增事件类型

- **难逆转**：新增事件类型要动 manager 的 switch、四家 adapter 的 emit、以及所有对事件类型做穷举断言的测试。
- **无上下文会惊讶**：「usage 事件里装着耗时」读起来别扭，后人会想拆开。
- **真取舍**：被否的是新增 `adapterEventTiming`。否因——`Usage`（当前占用）与 `Spend`（累计消耗）今天已经共用这一个事件类型（[manager.go:1640](internal/agentd/manager.go:1640)），耗时是同一族的第三样东西；拆出去会让「一次模型调用结束」这件事产出两个事件，而两个事件之间可以插进 agentd 重启。

### 6.4 `ToolResult` 改签名而不是新增 `ToolResultDur`

- **难逆转**：签名是包内契约，两种并存后调用方会随机挑一个。
- **无上下文会惊讶**：「为什么不加个重载省事」。
- **真取舍**：被否的是新增变体方法。否因——变体让「忘记传耗时」成为一个不报错的选项，而全仓调用方只有 2 个（补完 grok/opencode 后 4 个），改签名的成本远低于让一半调用方静默不报耗时。

---

## 7. 交棒声明

本轮新鲜证据，逐项列出：

| 法定产出 | 证据 |
|---|---|
| 契约增量文档落盘，签名带现状出处 | 本文件 §1、§2，出处全部在本轮工作树复核过 |
| 目标图已更新**且已提交** | 提交 `d12594ee`，`codegraph/target.json` 6 行改动（§4） |
| 契约闸门本轮通过 | `handoff graph check` → `fails=0`（改动前后各跑一次，改前 legacyHits 六条恰好咬死预算） |
| Ticket 0 骨架本轮编译通过 | `go build ./...` 退出码 0；`go vet ./...` 无输出；`gofmt -l` 无输出 |
| 契约错配在编译期暴露（骨架的存在理由） | `ToolResult` 改签名后 `go build` 当场报出 2 个调用方 + `go vet` 报出 2 个测试调用，与 §1 表里「爆炸半径只有 2 处」的查证一致 |
| 既有测试未被本次改动打红 | `go test ./internal/proto/ ./internal/store/ ./internal/executor/... ./internal/agentd/` 全 ok |
| 可执行冻结条目（哈希/密钥派生/编码格式） | **无命中** —— 本次契约无跨实现的字节级一致性要求 |
| 三重闸门拍板记录 | 4 条，见 §6 |

**欠账：无。**

**留给 breakdown 的两个已知事实**（不是欠账，是要带进下一节点的输入）：

1. `Store.TaskTiming` 是空壳且**未接线**（`GetTask` 不填 `Task.Timing`）。接线与实现必须同一轮完成——先接线后实现会让前端读到一个恒为 nil 的字段，看起来「功能上线了只是没数据」。
2. `claudecode` / `codex` 两处 `ToolResult` 调用当前传 `-1`（不知道耗时），带 `TODO(contract Ticket 0)` 标注。这两处是耗时采集的第一批落点。

交棒：`charter:breakdown`。
