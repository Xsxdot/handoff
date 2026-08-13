# W4e 累计用量与花费设计（B83）

**背景**：B80 已经让任务详情页回答「现在占用多少 context」。本轮回答**另一个口径**
的问题：**这个任务一共烧了多少 token、多少钱**。

两个口径各有各的帧，混用是静默错误——B80 的探针笔记 §4.2 已经踩过一次
（grok 的回合级累加值是真实占用的 4 倍）。本文所有取数落点都与 B80 分开列，
**不复用 B80 的公式**，哪怕字段名看起来一样。

**证据基础**：[2026-08-13-w4-cumulative-usage-probe.md](../notes/2026-08-13-w4-cumulative-usage-probe.md)
（四家各跑三个真实回合，外加 claudecode 的 `--resume` 补验）。
**形态基准**：`prototypes/desktop-console/`，用户已于 08-13 确认，
决策记录在该目录的 `AGENTS.md`。

---

## 1. 目标与非目标

**目标**：任务详情页的「执行器」行加一个切换按钮，切过去后显示五项累计值——
**总量、输入、缓存输入、输出、花费**。四家 executor 都要有，跨 agentd 重启、
executor 换进程、任务 resume 都不能算错。

**非目标**（本轮明确不做）：

- **逐回合明细表与图表**。只显示一个当前总数。
- **跨任务汇总、账单、导出**。那是另一个产品面，不是任务详情页。
- **CLI 渲染改动**。与 B80 一致，CLI 只是把新字段随 JSON 带出。
- **价目表配置化**。内置一份，改价要发版；真有人要改再说（YAGNI）。
- **原型改动**。形态已确认，本轮照着实现。

---

## 2. 架构决策：四家统一由 handoff 自己累加

探针笔记 §0 的表格显示 claudecode 与 codex「白给会话累计」。**那一列不能用**，
理由在 §2.2：

> 用同一个 session id `--resume` 之后，claudecode 的 `modelUsage` 累计**从零重新开始**。
> 前一个进程收尾时 in=3095 / cacheRd=95232 / out=63 / $0.064666，
> 新进程第一轮是 in=98 / cacheRd=32768 / out=14 / $0.017224——一点都没带过来。
> 会话内容本身恢复了（第一轮 `cache_read` 就有 32768），**归零的只是用量计数器**。

handoff 的任务恰恰是跨进程活着的：agentd 重启、executor 崩溃、任务 resume，
每一次都换一个进程。所以那些字段的真实语义是「**进程**累计」，不是「会话累计」。

**决策：四家一律由 handoff 逐次累加，一个 executor 的累计字段都不依赖。**

代价是必须落库且幂等；收益是一个算法覆盖四家，且正确性不随进程生命周期变化。
这也顺带作废了「哪家给不给累计」这个分类——它不再是设计里的一个维度。

---

## 3. 口径归一化：三分法

四家的字段含义互不相同，并排显示前必须归一化。**先定义，再换算**：

| 展示项 | 定义 |
|---|---|
| **输入** | 送进模型、且**未命中缓存**的部分（fresh input） |
| **缓存输入** | 送进模型、命中缓存的部分（读缓存 + 写缓存） |
| **输出** | 模型产出，**含 reasoning**（四家的 reasoning 都已包含在 output 里，见下） |
| **总量** | 输入 + 缓存输入 + 输出 |

选「输入不含缓存」而不是「输入含缓存」，理由：两项并排显示时必须互斥，
否则「输入 34,585 · 缓存 34,432」这种读数会让人以为总共送了 69k，
而真实只有 34.6k。互斥是并排显示的前提。

### 3.1 四家的换算公式

| executor | 帧 | 输入 | 缓存输入 | 输出 | 花费 |
|---|---|---|---|---|---|
| **claudecode** | `result` 行 | `usage.input_tokens` | `usage.cache_read_input_tokens + usage.cache_creation_input_tokens` | `usage.output_tokens` | `total_cost_usd` 的**进程内差分**（§5.2） |
| **codex** | `turn/completed` 时取最近一条 `thread/tokenUsage/updated` | `(total.inputTokens - total.cachedInputTokens)` 的差分 | `total.cachedInputTokens` 的差分 | `total.outputTokens` 的差分 | **不报** → 估算（§6） |
| **grok** | `turn_completed` 通知 | `inputTokens - cachedReadTokens - cacheCreationTokens` | `cachedReadTokens + cacheCreationTokens` | `outputTokens` | `costUsdTicks` |
| **opencode** | SSE `message.updated`（`info.role == "assistant"`） | `tokens.input` | `tokens.cache.read + tokens.cache.write` | `tokens.output` | `info.cost` |

**逐条说明为什么是这个公式**（每一条都有实抓佐证，别按字段名类推）：

- **codex 的 `cachedInputTokens` 是 `inputTokens` 的子集**，所以输入要**减**。
  佐证：`last.totalTokens 24673 = inputTokens 24668 + outputTokens 5`，
  9984 的缓存若是加项，等式不成立（探针笔记 §3）。
- **grok 的 `turn_completed.usage.inputTokens` 含缓存**，所以要**减**。
  佐证：四条 `response_completed` 的 `input_tokens` 之和 29069 加上
  `cache_read_input_tokens` 之和 109568 恰好等于 `turn_completed` 的 138637
  （B80 探针笔记 §4.2）。grok 官方文档也写明只有 headless 投影会把缓存减掉。
- **claudecode 的 `input_tokens` 不含缓存**，所以要**加**。
  佐证：轮 3 的 `input_tokens` 只有 54 而 `cache_read` 有 32768（探针笔记 §2）。
- **opencode 的 `cache.read` / `cache.write` 与 `input` 平行**，所以要**加**（B80 探针笔记 §3.1）。
- **reasoning 一律不单列**：codex 的 `reasoningOutputTokens` 与 grok 的
  `reasoningTokens` 都是 `outputTokens` 的子集（grok 实抓 `outputTokens 56`、
  `reasoningTokens 51`，`totalTokens 34558 = 34502 + 56`）。单列会重复计数。

### 3.2 与 B80 的取数落点**不同**，这是有意的

B80 取 grok 的 `response_completed`（snake_case，缓存是加项），本轮取
`turn_completed`（camelCase，缓存已含在内）。同一个产品两套口径，官方文档已确认。

原因：B80 要的是「最后一次调用的占用」，所以取每次调用的帧；本轮要的是
「整个回合消耗了多少」，`turn_completed` 正好是回合级的 roll-up，且花费也只在那里。
**实现时不要为了「统一」把两处改成同一个帧**——那会同时弄错两个口径。

---

## 4. 幂等与落库

### 4.1 为什么内存去重不够

B80 的去重是内存态（`Manager.lastUsage`），agentd 重启后内存为空、首帧必写一次。
对「当前占用」无害（覆盖成同一个值），对「累计」就是**重复计数**。

更棘手的是重复推送本身是常态：claudecode 三轮实测收到 6 条 assistant（两两同值），
opencode 的 `message.updated` 对同一条 message 会随流式生成推很多次、`info.id` 相同
而 tokens 在增长。所以要的不是「见过就跳过」，而是**同 key 覆盖**。

### 4.2 ledger 表：upsert 明细，读时求和

```sql
CREATE TABLE task_usage_ledger (
  task_id      TEXT NOT NULL,
  entry_key    TEXT NOT NULL,   -- adapter 给的幂等键，见 §4.3
  input        INTEGER NOT NULL DEFAULT 0,
  cached_input INTEGER NOT NULL DEFAULT 0,
  output       INTEGER NOT NULL DEFAULT 0,
  cost_ticks   INTEGER NOT NULL DEFAULT 0,  -- 1 USD = 10^10 ticks，见 §5.1
  cost_state   TEXT    NOT NULL DEFAULT '', -- reported | estimated | unknown
  updated_at   TEXT    NOT NULL,
  PRIMARY KEY (task_id, entry_key)
);
```

写入一律 `INSERT … ON CONFLICT(task_id, entry_key) DO UPDATE SET …`（**覆盖**，不是累加）。
累计值 = 该 task 全部行求和，**读时算**，不在 tasks 表上冗余累计列。

为什么不冗余累计列：冗余就有一致性问题（写 ledger 与写累计列必须同事务，
且任何一次漏写都会永久偏差）。行数是回合数量级（几十到几百），
SQLite 一次 `SUM` 的成本可以忽略。

为什么覆盖而不是累加：流式推送的同一条 message 会推多次且值在增长，
覆盖天然取到最终值；重复推同值则是无操作。两个坑一个语义解决。

### 4.3 各家的幂等键

| executor | entry_key | 唯一性依据 |
|---|---|---|
| claudecode | `result` 行的 `uuid` | 实抓 result 行含 `uuid` 字段，每回合一条 |
| codex | `params.turnId` | 回合级唯一（一个回合多次调用共享它，正好对应回合级差分） |
| grok | `_meta.promptId` | 实测三个回合各不相同 |
| opencode | `info.id` | message 级唯一 |

**粒度不统一是对的**：三家是回合级、opencode 是消息级。粒度由各家协议决定，
统一的是「稳定 key + 覆盖写」这个契约，不是粒度本身。强行把 opencode 折成回合级
需要 adapter 自己判定回合边界，那是凭空造一个可能出错的东西。

### 4.4 清理

任务删除时级联删 ledger 行。**归档（`done`）不删**——归档任务的详情页仍要显示累计。

---

## 5. 花费

### 5.1 内部单位：ticks（1 USD = 10^10）

grok 给的就是整数 ticks，claudecode / opencode 给的是浮点美元。
统一成 ticks 的整数累加，**只在最后一步转美元**。

这条直接照抄 grok 官方文档给的理由：浮点求和对不上服务端的账。
转换：`ticks = round(usd * 1e10)`。`$0.064666 → 646660000`，无有效精度损失。

### 5.2 claudecode 的花费要做差分

claudecode 不给「本回合花费」，只给 `total_cost_usd`（**进程内**累计）。
本回合花费 = 本次 `total_cost_usd` − 上一次的值，差值由 adapter 在 `runState` 上算。

`runState` 天生是进程级的（新进程 = 新 runState），所以新进程里基线自然从 0 起，
第一条 result 的 `total_cost_usd` 就是该进程第一个回合的花费——正确。

**防御**：差值为负说明基线是陈的（不该发生，但若发生），此时取当前值本身并打 Warn，
不要写负数进 ledger。

### 5.3 四种状态，三种显示

| 状态 | 何时产生 | ledger 记法 |
|---|---|---|
| **reported** | 执行器自报了这一次的花费（claudecode / opencode / grok 走 API-key） | `cost_ticks = 实际值`，`cost_state = 'reported'` |
| **estimated** | 执行器从不报，由 handoff 按牌价乘出来（只有 codex） | `cost_ticks = 估算值`，`cost_state = 'estimated'` |
| **unknown** | grok 走 pool/OAuth 路径整块不报；grok 的 `cost_is_partial` 为真；codex 的模型不在牌价表里 | `cost_ticks = 0`，`cost_state = 'unknown'` |

`cost_is_partial` 归到 unknown 是照抄 grok 自己的语义：它为真时 grok **主动**把
所有花费浮点一并省略，就是为了不让消费者把分项加成一份假的完整账单。

**注意 `partial` 不是行级状态**：单独一行只可能是「报了」「估的」「没有」三种。
`partial` 描述的是**求和之后**的结果——有些行有、有些行没有。行级与聚合级共用
`CostState` 类型但取值范围不同，这条要写进类型注释，否则会有人去找「哪个 adapter
产出 partial」而永远找不到。

**聚合规则**（读时算，输入是该任务的全部 ledger 行）：

```
known    = Σ cost_ticks（只算 state != 'unknown' 的行）
missing  = count(state == 'unknown')
estimated= any(state == 'estimated')
```

| 条件 | 展示 | 含义 |
|---|---|---|
| `missing == 0 && !estimated` | `$4.20` | 自报且完整 |
| `missing == 0 && estimated` | `≈$4.20`＋小标「估算」 | 我们按牌价算的，可能不准 |
| `missing > 0 && known > 0` | `≈$4.20`＋小标「不全」 | **下界**：真实值只会更高 |
| `missing > 0 && known == 0` | `—` | 完全不知道 |
| 没有任何 ledger 行 | 不显示花费项 | 还没开始 |

「估算」与「不全」分开，是因为它们对用户的含义相反：估算是近似值（可能高可能低），
不全是**下界**（只会更高）。合并成一个「≈」会把下界讲成近似值——看到 `≈$4.20`
的人不会想到实际可能是 $8。两者同时成立时按「不全」显示（漏账比不准更要紧），
完整措辞放进 `title` 提示。

**绝不做的事**：把任何一种「不知道」显示成 `$0.00`。
grok 文档的原话是花费缺席意味着 "unreported or incomplete, never free"。

---

## 6. codex 的牌价估算表

codex 是四家里唯一一个花费一个字都不报的，所以牌价表**只服务它一家**。

```go
// modelPrice 是单个模型的三档单价，单位：美元 / 百万 token。
type modelPrice struct {
    Input       float64 // 未命中缓存的输入
    CachedInput float64 // 命中缓存的输入
    Output      float64
}
```

**表的内容**（取自 OpenAI 官方定价页 `developers.openai.com/api/docs/pricing`，
**取价日期 2026-08-13**）：

| model | input | cached input | output |
|---|---|---|---|
| `gpt-5.6-sol` | 5.00 | 0.50 | 30.00 |
| `gpt-5.6-terra` | 2.00 | 0.20 | 12.00 |
| `gpt-5.6-luna` | 0.20 | 0.02 | 1.20 |
| `gpt-5.5` | 5.00 | 0.50 | 30.00 |
| `gpt-5.4` | 2.50 | 0.25 | 15.00 |
| `gpt-5.4-mini` | 0.75 | 0.075 | 4.50 |
| `gpt-5.4-nano` | 0.20 | 0.02 | 1.25 |
| `gpt-5.3-codex` | 1.75 | 0.175 | 14.00 |
| `gpt-5.2` | 1.75 | 0.175 | 14.00 |
| `gpt-5.1` | 1.25 | 0.125 | 10.00 |
| `gpt-5` | 1.25 | 0.125 | 10.00 |
| `gpt-5-mini` | 0.25 | 0.025 | 2.00 |
| `gpt-5-nano` | 0.05 | 0.005 | 0.40 |

**`-pro` 系列刻意不收进表**：官方页对它们的 cached input 是「—」（不适用），
三档缺一档就估不准，宁可 unknown。**`gpt-5-codex` / `gpt-5.1-codex` /
`gpt-5.2-codex` 也不在表里**——官方 API 定价页当天只列了 `gpt-5.3-codex` 一个
codex 型号，其余没有可引的公开单价。缺席按 unknown 处理，不拿同代非 codex
型号的价去顶。

**取价日期必须随表写进代码注释。** 价格会变，表里的值只对写下它的那天负责；
过期的后果是数字偏差，而缺失的后果只是不显示——两种失效模式都不撒谎。

估算式（先按美元算，最后一步转 ticks，避免三次整除的累积误差）：

```
usd   = (input×P.Input + cached×P.CachedInput + output×P.Output) / 1e6
ticks = round(usd × 1e10)
```

**模型不在表里就是 `unknown`，不是用默认价猜。** 这与 B80「分母缺席就只显绝对值、
绝不猜」是同一条纪律——猜错是静默错误，数字照常显示只是错的。

---

## 7. 数据结构与接口

### 7.1 proto

```go
// Cumulative 是任务的累计消耗快照。
//
// 与 Usage 的区别：Usage 描述「现在占用多少」（最后一次调用的输入侧），
// 本结构描述「一共烧了多少」（跨全部调用累加）。两者数量级完全不同，
// 混用是静默错误——不要因为字段名像就互相赋值。
type Cumulative struct {
    InputTokens  int `json:"input_tokens"`   // 未命中缓存的输入（口径见 §3）
    CachedTokens int `json:"cached_tokens"`  // 命中缓存的输入
    OutputTokens int `json:"output_tokens"`  // 含 reasoning
    TotalTokens  int `json:"total_tokens"`   // 三项之和，由 store 算好，前端不再加
    Cost         *Cost `json:"cost,omitempty"` // nil = 还没有任何花费信息
}

// Cost 是累计花费及其可信度。
//
// 注意：Ticks 只包含**已知**的部分。State 为 partial 时它是下界，不是总额。
type Cost struct {
    Ticks int64     `json:"ticks"` // 1 USD = 10^10 ticks，整数累加避免浮点误差
    State CostState `json:"state"` // reported | estimated | partial | unknown
}

// CostState 是花费的可信度。
//
// 取值范围**分两级**：单条账目（ledger 行）只可能是 reported / estimated /
// unknown；partial 只在**求和之后**产生（部分行有、部分行没有），任何 adapter
// 都不会产出它。别去找「哪个 adapter 报 partial」——没有。
type CostState string

const (
    CostReported  CostState = "reported"  // 执行器自报且完整
    CostEstimated CostState = "estimated" // handoff 按牌价估算
    CostPartial   CostState = "partial"   // 仅聚合级：有已知部分但有调用没拿到——是下界
    CostUnknown   CostState = "unknown"   // 一次都没拿到
)
```

`Task` 上加 `Cumulative *Cumulative \`json:"cumulative,omitempty"\``。

**只在单任务读取（`GetTask`）时填充，列表接口不填**。理由：列表页不显示累计，
为它对每一行做一次 SUM 是纯浪费。这条要写进 `GetTasks` 的方法注释，否则
下一个人会以为是 bug。

### 7.2 adapter → manager

`executor.AdapterEvent` 加一个字段：

```go
// Spend 是这一次调用/回合**新增**的消耗；nil = 本帧不带消耗信息。
//
// 与 Usage 的区别：Usage 是「当前占用」的快照（覆盖语义），Spend 是「新增消耗」
// 的账目（按 Key 覆盖后求和）。一个帧可以两者都带。
Spend *SpendEntry
```

```go
// SpendEntry 是一条待入账的消耗。
//
// Key 必须在同一个任务内稳定且唯一（各家落点见 spec §4.3）——它是幂等的全部依据。
// 同 Key 重复上报按**覆盖**处理，所以流式增长的值可以放心重复报。
type SpendEntry struct {
    Key          string
    InputTokens  int
    CachedTokens int
    OutputTokens int
    CostTicks    int64
    CostState    proto.CostState
}
```

manager 侧新增 `handleSpend`，行为与 `handleUsage` 对齐：**只写库，不追加事件、
不广播**（频率同样高，进事件日志会淹没审核者要看的东西）。落库失败只 Warn。

**硬约束（与 B80 同源的坑）**：`handleSpend` 与 `handleUsage` 走**不同的**写库路径。
`SetTaskUsage` 是整体覆盖三元组的，绝不能拿它写累计；反过来 ledger 也绝不参与
当前占用的计算。

### 7.3 store

```go
// UpsertSpend 记一条消耗账目；同 (taskID, key) 覆盖既有行。
func (s *Store) UpsertSpend(taskID string, e proto.SpendEntry) error

// TaskCumulative 求和该任务的全部账目，按 spec §5.3 定 Cost.State。
// 没有任何账目行时返回 nil（不是零值——0 会被读成「没花钱」）。
func (s *Store) TaskCumulative(taskID string) (*proto.Cumulative, error)
```

### 7.4 前端

`formatExecutorLine` 保持原样不动（它服务「当前占用」视图）。新增
`formatCumulativeLine(task)`，输出：

```
1.2M · 输入 340.2k · 缓存 820.5k · 输出 39.3k · ≈$4.20 估算
```

`TaskHeader` 的「执行器」行右侧加切换按钮，两个视图共用一行、切换不改框高
（形态基准见 §8）。

---

## 8. 界面：照原型实现

原型 `prototypes/desktop-console/` 已于 08-13 确认，三条形态决策必须照做：

1. **切换按钮在标题行右上角**，文案是「要切去的那个视图」的名字
   （当前显占用时写「累计用量」，反之写「当前占用」）。
2. **累计行整行铺开、跨掉标签列**（`grid-column: 1 / -1`），「累计」二字并进
   内容里当前缀。原因：内容比 Context 行长得多，关在第二列时宽度正好卡满、
   多一位数字就折行；跨列后腾出约 16% 余量，而**行数和框高不变**——切换视图时
   下面的正文不会跳。
3. **估算/不全的花费必须看得出不是自报值**（`≈` + 小标）。和自报值长得一样，
   就是在暗示一个它没有的精度。

原型里五个数字加得起来，是因为它假设了 §3 的归一化已经做完。**实现时先归一化再显示**，
否则同一个「输入」标签在四家下是四个意思，而且不报错。

---

## 9. 日志与注释

**关键节点日志**（用各自的 structured logger，不用 `fmt.Printf`）：

- adapter 解析出一条 `SpendEntry` 时打 Debug（频率高），带 `task` / `key` / 各分项。
- adapter 算出**负差分**（§5.2）时打 Warn，带 `task` / 前后两个值。
- codex 遇到**牌价表里没有的模型**时打 Warn 一次（同一模型不重复刷），
  带 `task` / `model`——这是用户看不到花费的唯一原因，日志里必须能查到。
- grok 遇到 `cost_is_partial` 或整块无花费时打 Info，带 `task` / 认证路径判定依据。
- `handleSpend` 落库失败打 Warn，带 `task` / `key` / cause。
- store 的 `TaskCumulative` 只在出错时打日志（读路径高频，成功不打）。

**注释**：新文件写文件头（职责 + 边界）；`UpsertSpend` / `TaskCumulative` /
`SpendEntry` / `Cumulative` / `Cost` 写导出注释（参数、返回、空值语义）；
§3.1 每条换算公式在代码里留一行「为什么加/为什么减」并指向本 spec 的行号。

---

## 10. 测试要点

- **归一化**：四家各一条真实报文（照抄探针笔记里的实抓值）→ 断言三分法结果。
  特别是 codex 与 grok 的**减法**、claudecode 与 opencode 的**加法**。
- **幂等**：同 key 报两次同值 → 总和不变；同 key 报两次递增值 → 总和取后者；
  不同 key → 累加。
- **花费聚合**：五种条件（§5.3 的表）各一条用例，含 `known == 0 && missing > 0`
  必须落到 `unknown` 而不是 `$0.00`。
- **claudecode 差分**：连续三个 result 行 → 三个正增量；负差分 → 取当前值 + Warn。
- **牌价缺失**：模型不在表里 → `cost_state = unknown`，token 三项照常入账。
- **没有账目行**：`TaskCumulative` 返回 nil，前端不显示花费项。
- **前端**：切换按钮两个方向都测；累计行在最大数字下不折行（原型量过的余量）。

---

## 11. 未验项与风险

1. **codex 的 `thread/resume` 后 `tokenUsage.total` 归不归零**未验（验一次要花额度）。
   本设计用的是回合级**差分**，所以归零会产生一个负差分——按 §5.2 的防御处理
   （取当前值 + Warn），不会算错，最多在恢复的那一个回合少算一点。
   真要精确，验完这条再改。
2. **opencode 的 SSE 是否存在会话级汇总事件**没有正证也没有反证（探针时它已空闲）。
   本设计按「线上只有单条」实现，这是安全的一侧：真有汇总事件时改成直接读更省事，
   反过来是漏账。
3. **牌价表会过期**。表里每条带取价日期，且不在表里的模型一律 unknown——
   过期的风险是数字偏差，不是错误显示；缺失的风险是不显示，也不是错误显示。
   两种失效模式都不会撒谎。
4. **codex 走订阅额度时，估算值是「等价 API 成本」而不是实际扣费。**
   codex CLI 既可以用 API key 计费，也可以走 ChatGPT 订阅额度；走订阅时按 token
   根本不产生美元扣费，扣的是订阅额度。牌价估算此时给出的是「同样的量按 API 牌价
   要花多少」——仍然是有意义的量级参考，但它不是账单。这正是「估算」小标存在的
   理由，措辞上不要写成「花费」而要能读出是推算值。handoff 无法从协议里判定
   codex 走的哪条计费路径，所以不做区分，也不因此隐藏数字。
5. **grok 判定「走的是 pool/OAuth 还是 API-key」的依据**没有独立字段，只能靠
   「这一帧有没有 `costUsdTicks`」反推。所以实现上不做认证路径判定，
   只做「有就记 reported、没有就记 unknown」——结论一样，且不依赖未验的推断。
