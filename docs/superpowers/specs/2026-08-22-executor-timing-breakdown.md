# 拆解提案：执行耗时的采集、落账与展示（需求 A）

日期 2026-08-22 · 前置 [spec §A](2026-08-22-executor-timing-and-custom-launchers-design.md) + [契约冻结物](2026-08-22-executor-timing-contract.md)（提交 `8ec25bd9`） · 节点 `charter:breakdown`

**形态声明**：本会话有「未经用户要求不调 Agent」的约束，无法走「subagent 出稿 / 主会话拍板」的标准形态，故走 skill 的**单上下文兜底**——出稿者即本上下文，**一切岔口一律「待拍板」、无自批**，出稿即停。

---

## 0. 待拍板清单（拍板者按这张表裁决，正文只展开理由）

| # | 岔口 | 选项 | 出稿者倾向 |
|---|---|---|---|
| **P1** | 模型段（api）的切分逻辑放哪一层 | (a) 四家 adapter 各自打点 (b) `turn` 包出一个共用段切分器，adapter 只喂信号 | **(b)**，但它要求先补一个 `EndTurn`——见 §5.1 |
| **P2** | 四张 provider 卡的推进方式 | (a) 四张平行派 (b) 先 claudecode+codex 验口径，再 grok+opencode | **(b)**，理由见 §4.2 |
| **P3** | `turn` 条目（回合墙钟）的刷新频率 | (a) 每段结束刷一次（运行中实时可读） (b) 只在回合开始/结束各写一次 | **(a)**，代价见 §5.3 |
| **P4** | Buckets 下钻层的「命令首词」规则 | (a) 空白切第一段（`go test ./...` → `go`） (b) 前两段（→ `go test`） (c) 不下钻，只按工具名 | **(b)**，理由见 §5.4 |
| **P5** | 前端耗时的入口形态 | (a) 页头新增独立「耗时」chip (b) 并进现有 `UsageChip` 的弹出，加一节 | **(a)**，理由见 §5.5 |
| **P6** | `d_cli` 要不要单独立卡 | (a) 不立卡，只进真机清单 (b) 立一张验证卡 | **(a)**，理由见 §3 |

**六个岔口全部未自批。** 拍板前不得扇出。

### 0.1 拍板记录（2026-08-22，协调者 + 用户）

**六条全部按出稿者倾向定案：P1=(b) / P2=(b) / P3=(a) / P4=(b) / P5=(a) / P6=(a)。**

两条附带的执法条款（拍板的一部分，不是建议）：

1. **P1 的退出闸**：T2、T3 完成后**立刻复核**「共用切分器里有没有长出 per-provider 分支」。长出了就当场退回 (a)（各 adapter 自打），**不许拖到 T4/T5**——拖到那时四家都已按 (b) 写完，退回的成本从「改一处」变成「返工四处」。这条复核是 T3 的验收项，不是可选动作。
2. **P4 未选 (c)**，因此 spec §A.4 用户故事 3（按命令首词聚合的排行）**留在范围内**，T6 的 Buckets 必须做下钻层。

`EndTurn` 随 P1=(b) 一并批准：它是 `d_executor` 的包内 API（`internal/executor/turn` 与 provider 子包同属该子系统），加方法不触发退回 contract——判定依据见 §2 末段。

---

## 1. 触及子系统清单

子系统 id 取自 `codegraph/target.json` 的 `domains`；类型取自[实例化清单 §2](2026-08-21-handoff-instantiation-checklist.md) 的域表（有图以图为准，类型以清单为准）。

| 子系统 | 类型 | 本次触及 | 派卡资格四条 |
|---|---|---|---|
| **d_executor** Executor 适配域 | **边界型**（对面是真实 executor 进程与其协议） | 打点采集 + 补 grok/opencode 工具帧 | 见 §1.1 —— **不按子系统派一张卡，按有界文件集派 5 张** |
| **d_ledger** 账本域（`internal/store`） | 逻辑型 | `TaskTiming` 聚合实现 + `GetTask` 接线 | 四条全过 ✅ |
| **d_controlplane** 控制面 API 域 | 逻辑型（对 TS 的 wire 是接缝） | **本次零改动** —— 见 §1.2 | 不适用 |
| **d_web** Web 控制台域 | 逻辑型（webview 差异是独立风险族） | 工具卡耗时 + 任务级耗时面板 | 四条全过 ✅ |
| **d_cli** CLI 域 | 逻辑型 | **本次零改动** —— 见 §3 | 不适用 |
| **d_contract** 契约域 | 逻辑型 | **已在 contract 节点落定**，本次不再动 | 不适用 |

### 1.1 d_executor 的派卡资格核查（四条逐条）

1. **有界文件集**：`internal/executor/**` 写得出 ✅
2. **契约面可枚举**：`executor.Adapter` 五个方法 + `AdapterEvent` ✅
3. **依赖 DAG 无环**：四个 provider 子包互不 import，共同依赖 `turn` 与 `proto` ✅
4. **类型标注**：边界型 ✅

四条全过，**它是一个合法的子系统**。但**派一张卡不成立**——一个上下文要同时吞下 codex appserver 协议、claude stream-json、grok ACP、opencode SSE 四套互不相同的上游协议，这不是上下文预算问题，是四份独立的协议查证工作。

架构法第三条判据逐条核（回答义务在此履行）：

- 判据 1（前缀家族 ≥5 源文件）：各 provider 子包内命中（如 `codex/` 有 `adapter/appserver/items/perm/preflight/proc/…`），但它们**已经是子包**，家族已有户口，不触发竖切；
- 判据 2（单包 ≥40 文件且无子包）：`internal/executor` 根包 5 个文件 + 子包，不命中；
- 判据 3（>2~3 万行）：14,945 行 < 20,000 红线，不命中。

**结论**：三条判据都不命中，**不需要插竖切还债卡**。按 skill 的规则——「不满足四条的，它只是一组文件」的反面同样成立：满足四条但内部天然分成互不依赖的有界文件集时，**按文件集派卡**，一个子系统内 5 张卡（1 张公共 + 4 张 provider）。

### 1.2 d_controlplane 本次为何零卡

契约节点的 Ticket 0 已经把 `Manager.handleTiming` **实现并接线**（`manager.go` 的 `adapterEventUsage` 分支，与 `handleSpend` 并列），处置纪律逐条照抄 `handleSpend`。控制面在本需求里的全部职责就是这一段中介，没有剩余工作。

架构法第三条判据 2（`internal/agentd` 61 文件平铺包）在此**命中**，回答义务履行如下：**本次不需要圈有界文件集，因为本次不改这个包**。竖切欠账仍在（[实例化清单 §6](2026-08-21-handoff-instantiation-checklist.md) 第 2 条），但绞杀式还债的规则是「哪张卡碰到哪个家族随卡还」——本次没碰，不预支。

---

## 2. 契约增量核对（对照冻结物逐条）

| 冻结条目 | 本次拆解是否越界 |
|---|---|
| `TimingEntry{Key,Kind,Turn,DurMS,Label,Detail,OffsetMS}` | 不越界。五张采集卡只**填**这些字段，不加字段 |
| `TimingKind` 三值 `api/tool/turn`，`other` 只在聚合层产生 | 不越界。聚合卡不上报 `other`，采集卡不上报 `other` |
| 幂等键从内容派生（`tool/<turn>/<part>` 等） | 不越界，但 **P1 的选项 (b) 会把键的构造从 adapter 挪进 `turn` 包**——键的**形状**不变，只换生产者。**这不是契约变更**（契约冻的是键的构造规则，不是谁执行它） |
| `Frame.DurMS` 只出现在 `tool_result` | 不越界，已有回归断言锁住（`frames_test.go` 的契约锁） |
| `ToolResult(part,status,output,dur)`，`dur<0` = 未知 | 不越界 |
| `Task.Timing` 由 `GetTask` 填、`ListTasks` 不填 | 不越界 |
| `Detail` 上限 200 rune | 不越界 |
| 走 `Type="usage"` 事件，不新增事件类型 | 不越界 |

**需要新接缝吗？** 有一处**疑似**，必须由拍板者裁决而不是我在这里加：

> **P1 选项 (b) 需要给 `turn.FrameWriter` 加一个 `EndTurn`**（现状只有 `BeginTurn`，见 `frames.go:141`，无收尾方法）。段切分器要算「回合墙钟」就必须知道回合何时结束，而这个事实今天只有 adapter 知道（它发 `Result` 事件）。
>
> 判定：`FrameWriter` 是 **d_executor 的包内 API**，不是跨子系统契约面（`internal/executor/turn` 与 provider 子包同属 d_executor）。加方法**不触发退回 contract**。但如果拍板者选 (a)，这个方法就不需要——所以它挂在 P1 上，不单独成岔口。

**结论：本次拆解不越界，不需要退回 contract 节点。**

---

## 3. 为什么 d_cli 零卡（P6 的理由）

[契约文档 §5](2026-08-22-executor-timing-contract.md) 已查证：`handoff show <task>` 单行输出 `AttachInfo` JSON（`cmd/show.go:22`），其中的 `Task` 由 `GetTask` 产出，因而 `GetTask` 一接线就自带 `timing`。spec §A.4 用户故事 4 因此零改动即满足。

但「`show` 的输出里真的有 `timing`」是一个**行为事实**，不是可 grep 的 API 事实——`AttachInfo` 里那个 `Task` 究竟走不走 `GetTask`，中间有没有别的投影层把它重建过（[实例化清单 §1.1](2026-08-21-handoff-instantiation-checklist.md) 记着 `internal/agentd` 有 15 处手搭 `map[string]any` 回包的先例），**必须真机确认**。

→ 进真机清单第 2 条，**不立卡**（立一张「什么都不改，只跑一次命令」的卡是把验收伪装成开发）。

**若真机发现 `show` 拿不到 timing**，那说明中间有一层手搭投影——届时它是一张**新卡**（d_controlplane 的序列化边界修补），不是本次的遗漏。

---

## 4. 子卡清单与依赖 DAG

### 4.1 DAG

```
        ┌─────────────────────────────────────┐
        │ T1  turn 段切分与打点地基            │  d_executor（公共）
        └──────────────┬──────────────────────┘
                       │
       ┌───────────────┼───────────────┬───────────────┐
       ▼               ▼               ▼               ▼
   ┌────────┐    ┌────────┐     ┌──────────┐    ┌───────────┐
   │T2 claude│    │T3 codex│     │T4 grok   │    │T5 opencode│   d_executor（各 provider）
   └────┬───┘    └────┬───┘     └────┬─────┘    └─────┬─────┘
        └─────────────┴──────────────┴────────────────┘
                       │（任一张完成即可解锁 T6 —— 有真实数据可对账）
                       ▼
              ┌──────────────────┐
              │ T6 聚合与接线     │  d_ledger
              └────────┬─────────┘
                       ▼
              ┌──────────────────┐
              │ T7 TUI 展示       │  d_web
              └──────────────────┘
```

`T2..T5` 互相之间**无依赖**（四个 provider 子包互不 import）。`T6` 只需要**任意一张**采集卡落地即可开工（它要的是「库里有真数据」，不是「四家都齐」）。

### 4.2 P2 的理由：为什么倾向串成两批

claudecode 与 codex **已有工具帧**，卡的内容是「在既有 `ToolCall`/`ToolResult` 调用点两侧加时钟」；grok 与 opencode **一个工具帧都不产**，卡的内容是「先把上游协议的工具事件映射成帧，再打点」——后者要先做一遍协议查证（grok 的 ACP `tool_call`/`tool_call_update` 配对规则、opencode 的 tool part `state` 状态机），工作量差一倍以上。

先做 T2+T3 的价值不是省事，是**先拿到真实数据验证三分法口径**：如果 `other` 在真实任务上占了 60%，说明段边界定错了，此时改的是 T1 的切分器（一处），而不是四家都返工。

反方（选 (a) 平行派）的理由也成立：四张卡彼此无依赖，串行是浪费墙钟。**待拍板。**

---

### T1 · turn 包的段切分与打点地基（d_executor · 边界型）

**①契约引用**：`proto.TimingEntry`（全字段）、`proto.TimingKind` 三值、幂等键构造规则（契约文档 §2.2）、`FrameWriter.ToolResult` 的 `dur` 参数语义（`dur<0`=未知）。

**②意图与为什么**：给四家 adapter 一个**共用的**段切分器，让「一个回合怎么切成 api/tool/turn 三类条目」这条规则只存在一处。四份独立实现的后果不是重复代码，是四种**互不可比的口径**——而 spec 的整个目的就是让两次派发的数字能对照（spec §A.2 弃选 3 的同一条理由）。

时钟必须可注入。这不是测试便利，是[已实证的缺陷族](2026-08-21-handoff-instantiation-checklist.md)：判据依赖真实时间的测试会退化成计时竞态。

> **本卡的形状取决于 P1。** 选 (a) 时本卡缩成「只提供幂等键构造 + 时钟注入的小工具」；选 (b) 时本卡是完整的段切分器 + `EndTurn`。**拍板前不得开工。**

**③验收**（边界型 → 机内只验契约形状，行为走真机）：
- 喂一段固定信号序列（回合开始 → 工具A开始 → 工具A结束 → 工具B开始/工具C开始 → 两者结束 → 回合结束）+ 注入时钟，断言产出的 `TimingEntry` 集合逐字段相等（含 `Key`、`OffsetMS`）；
- 断言**并发场景**：B 与 C 区间重叠时，两条 tool 条目的 `OffsetMS`/`DurMS` 能还原出重叠区间（这是 `ToolSpanMS` 能算对的前提）；
- 断言**幂等键不含进程内计数器**：同一段信号序列跑两遍（模拟重放），产出的 Key 集合完全相同；
- 断言 `dur<0` 时 `Frame` 上不带 `dur_ms`（已有回归锁，复跑即可）；
- **缺陷族结论见 §6，逐条并入本栏。**

**④入口指针**：`internal/executor/turn/frames.go`（`BeginTurn:141` / `ToolCall:187` / `ToolResult:210`）、`internal/proto/timing.go`。

---

### T2 · claudecode 打点（d_executor · 边界型）

**①契约引用**：同 T1，外加 `AdapterEvent.Timing` 的挂载语义（走 `Type="usage"` 事件）。

**②意图与为什么**：claudecode 的工具帧已在 `adapter.go:685`（call）与 `:726`（result）产出，本卡把这两点接上 T1 的切分器，并把 `-1` 的 `TODO(contract Ticket 0)` 换成真耗时。

**打点必须贴着协议事件，不能按写帧时刻倒推**——`out.jsonl` 的轮询间隔是 200ms（`stream.go:33`），两侧各带最多 200ms 抖动，而一次 `Read` 工具本身就在毫秒级。

**③验收**：
- 用 `testdata/turn_success.jsonl` 加时间戳扩展成夹具，断言产出的条目与手算值相等；
- **反向断言一条**：把夹具里的 `tool_result` 行删掉，断言该次调用**不产 tool 条目**（而不是产一条 `DurMS=0`）——「未返回」与「零耗时」必须可区分；
- 真机项见 §7。

**④入口指针**：`internal/executor/claudecode/adapter.go:685,726`、`stream.go`、`testdata/turn_success.jsonl`。

---

### T3 · codex 打点（d_executor · 边界型）

**①契约引用**：同 T2。

**②意图与为什么**：同 T2，落点在 `adapter.go:1128`（call）与 `:1137`（result）。codex 走 appserver，事件到达路径与 claudecode 的文件轮询不同——**这正是「四家口径要可比」的压力测试点**：如果 T1 的切分器在这两家上算出的段结构不同构，说明切分器抽象错了，此时回退 T1 而不是在 codex 里打补丁。

**③验收**：同 T2 的两条，夹具取 codex 的 items 事件序列；外加与 T2 的**口径对照**：同一形状的回合（一次模型输出 + 一次工具调用 + 一次模型输出）在两家产出的条目**种类与数量相同**（`DurMS` 值当然不同）。

**④入口指针**：`internal/executor/codex/adapter.go:1128,1137`、`items.go`、`appserver.go`。

---

### T4 · grok 补工具帧 + 打点（d_executor · 边界型）

**①契约引用**：同 T2，外加 `FrameWriter.ToolCall/ToolResult` 的既有截断语义（`Input`/`Output` 头尾截断、`Bytes` 记原始长度——**沿用，不新造**）。

**②意图与为什么**：grok 今天把 ACP 的 `tool_call` / `tool_call_update` 归 `updateNone`（`adapter.go:727`），一个工具帧都不产。CLAUDE.md 的派发决策表把 grok 列为两个常用执行器之一（bug 排查与纯执行都派它）——**不补这张卡，本需求对一半的实际派发无效**。

`adapter.go:653` 的既有注释给出了当初不产帧的理由：grok 的工具动作「今天只有一行人读摘要（`toolLine`，带 200 截断），拿它当 `tool_call` 帧的 input 会把……」。**这条理由必须在本卡里正面处理**，不能无视：要么找到 ACP 里的结构化 `rawInput`（`perm.go` 的 `permRequestFromToolCall` 已经在从 `rawInput.command` 取完整命令原文，说明结构化入参**是拿得到的**），要么如实标注 input 的质量降级。

**③验收**：
- 断言 ACP 的 `tool_call` → `tool_call_update` 序列产出**一对**配对的帧（`Part` 相同）；
- 断言 `tool_call_update` 多次到达时**不产多条 tool_result**（ACP 的 update 是状态推进，不是结果重发）——这是 grok 侧最可能的重复计数点；
- 断言 input 取的是结构化 `rawInput` 而非 `toolLine` 摘要（若拍板决定降级，则改为断言降级标注存在）；
- 真机项见 §7。

**④入口指针**：`internal/executor/grok/adapter.go:653,727`、`adapter.go:817` 的 `permRequestFromToolCall`、`acp.go`、`testdata/`。

---

### T5 · opencode 补工具帧 + 打点（d_executor · 边界型）

**①契约引用**：同 T4。

**②意图与为什么**：opencode 的 `frameKind`（`adapter.go:1516`）对 tool part 返回 `kindSkip`，同样一个工具帧都不产。`api.go:445` 证明 tool part 的 `state.status` 是拿得到的。

**本卡附带一处必做的订正**：`adapter.go:1511` 的注释称「工具调用本身由 `mapToolPart` 以完整的 `tool_call` 帧上报」，而 `mapToolPart` **在全仓不存在**（契约文档 §1.2 已标「疑似漂移」）。这条假注释必须删掉——它正是「对侧代码里的死常量带着一条假注释存活多年」的同款形状。

**③验收**：
- 断言 tool part 的 `part.updated` 序列产出配对的帧；
- 断言**流式重复推送不重复计数**：opencode 对同一条 message 会随生成推很多次（`SpendEntry` 的注释即为此而写），断言同一 tool part 推 N 次只产一对帧、且 Key 恒定；
- 断言 `mapToolPart` 字样已从注释中消失（一条 grep 断言，防止改完又被复制回来）；
- 真机项见 §7。

**④入口指针**：`internal/executor/opencode/adapter.go:1511,1516`、`api.go:445`、`testdata/`。

---

### T6 · 聚合与接线（d_ledger · 逻辑型）

**①契约引用**：`Store.TaskTiming(taskID) (*proto.TaskTiming, error)` 的返回语义（无账目返回 `(nil,nil)`）、`TaskTiming` 全字段语义、`TimingBucketCap=20`、`OtherMS = max(0, Total − API − ToolSpan)`、`GetTask` 填 / `ListTasks` 不填。

**②意图与为什么**：把耗时账目求和成三分法结果，并接进 `GetTask`。

**接线与实现必须同一轮完成**（契约文档 §7 已写进交棒事实）：先接线后实现，前端会读到一个恒为 nil 的字段——那看起来像「功能上线了只是还没数据」，是最难查的一类假象。

聚合是**纯函数**（账目集合 + 回合分组 → 结果），SQL 只负责取行。把区间并集算进 SQL 会让这段逻辑失去穷举测试的可能。

**③验收**（逻辑型 → 机内闭环）：
- 穷举纯函数：空集 → `(nil,nil)`；单回合无并发；**单回合并发工具（Σtool > toolSpan）**；跨回合求和；某回合缺 api 条目 → `Partial=true`；`Total − API − ToolSpan < 0` → `OtherMS=0` 且 `Partial=true`；
- Buckets：超过 `TimingBucketCap` 时按耗时降序截断，**断言被截断的是最小的那些**（反向：断言第 21 名不在结果里）；
- 接线：`GetTask` 填、`ListTasks` **不填**（后者是反向断言，且是 `ListTasks:396` 注释里写死的既有纪律——本卡不得顺手"优化"它）；
- 真实 SQLite 上跑（本域的既有做法）。

**④入口指针**：`internal/store/store.go`（`UpsertTiming` 已实现、`TaskTiming:661` 是空壳、`GetTask:371`、`ListTasks:396`）、`internal/proto/timing.go`。

---

### T7 · TUI 展示（d_web · 逻辑型）

**①契约引用**：`Frame.dur_ms`（只在 `tool_result` 上、缺席≠0）、`TaskTiming` / `TimingBucket` 的 TS 投影（已落 `web/src/api/types.ts`）、`partial` 的展示义务。

**②意图与为什么**：两处落点（spec §A.4 已定，**回合级汇总不做**，在 Out of Scope）——

1. `ToolCard` 右上角单次耗时；
2. 页头的任务级耗时面板（P5 决定入口形态）。

`buildBlocks` 今天把 `Frame` 聚合成 `Block` 时**丢掉了 `ts` 与将来的 `dur_ms`**（`frames.ts:88` 的 `ToolBlock` 无时间字段）——本卡要把 `dur_ms` 带进 `ToolBlock`，这是纯函数层的改动，可穷举测试。

**`partial=true` 时界面必须能读出「未归类偏大」**，不得直接把 `other_ms` 当真实空档画成一段色块——这条是 spec §A.3 规则 1 在展示层的延伸。

**③验收**（逻辑型 → 机内闭环，但 webview 差异见 §6.3）：
- `buildBlocks` 纯函数：`dur_ms` 从 `tool_result` 帧带进 `ToolBlock`；`tool_call` 帧的 `dur_ms` 被忽略（反向断言）；缺席时 `ToolBlock` 上是 `undefined` 而非 `0`；
- 组件：`timing` 缺席时面板整体不渲染（**不画空表**——与 `UsageChip` 的既有做法一致：`usage` 与 `cumulative` 都缺席时返回 null）；
- 组件：`partial=true` 时有可见标注；
- 组件：`tool_ms > tool_span_ms` 时两个数**同时可见**，不取其一；
- 契约夹具：按[实例化清单 §1.1](2026-08-21-handoff-instantiation-checklist.md) 的机制，`TaskTiming` 要进 `internal/proto` 的 fixture 生成器与 `web/src/api/contract.test.ts` —— **这是本卡最容易漏的一项**，见 §6.6。

**④入口指针**：`web/src/app/task/frames.ts:88,154`、`ToolCard.tsx`、`TuiHeader.tsx`、`UsageChip.tsx`、`web/src/api/types.ts`、`web/src/api/contract.test.ts`、`internal/proto/contract_fixture_test.go`。

---

## 5. 岔口详述

### 5.1 P1 · 段切分放哪一层

| | (a) 各 adapter 自打 | (b) turn 包共用切分器 |
|---|---|---|
| 口径一致性 | 四份实现，四种口径漂移的可能 | 一份实现，口径天然可比 |
| 贴协议程度 | 最贴 | 隔一层，但信号本身就是协议事件的直接映射 |
| 前置改动 | 无 | **要给 `FrameWriter` 加 `EndTurn`**（今天只有 `BeginTurn`，`frames.go:141`） |
| 每家的特殊性 | 各自处理，自由度高 | 特殊性要么抽象进切分器（变复杂），要么在 adapter 侧预处理 |

出稿者倾向 **(b)**：spec 的整个目的是让数字可对照，而四份实现最先漂的就是「模型段从哪一刻起算」。`EndTurn` 的成本很小且它本身是个缺失（回合有开始没有结束，这个不对称本来就是账）。

**风险**：四家的「一批工具结束」信号形状可能不同构（claude 是一条 user 消息里多个 `tool_result`，grok 是逐个 `tool_call_update`）。若真不同构，(b) 会被迫在切分器里长出四个分支——那时 (b) 的优势消失。**这是行为事实，未验证**，见 §7 第 1 条。

> 若拍板者选 (b)，建议附加一条：**T2 与 T3 完成后立刻复核**「切分器里有没有长出 per-provider 分支」，长出了就当场退回 (a)，不要拖到 T4/T5。

### 5.2 （并入 P2，见 §4.2）

### 5.3 P3 · turn 条目刷新频率

(a) 每段结束刷一次：运行中的任务也能读到实时 `TotalMS`；代价是每回合多写 N 次库（N=段数，量级几十）。按 `UpsertSpend` 的既有频率参照（「频率与 assistant 消息同级」），这个量级是既有做法之内的。

(b) 只在回合开始/结束各写一次：省写；代价是任务跑到一半时 `TotalMS` 停在上一回合，而**审核者最想看耗时的时刻恰恰是「它怎么还没跑完」的时刻**。

出稿者倾向 (a)。

### 5.4 P4 · 命令首词规则

`go test ./...` 取 `go` 会把 `go build`、`go vet`、`go test` 混成一格，而这三者的耗时特征完全不同；取前两段（`go test`）能分开，代价是 `npm run dev` 与 `npm run build` 这类三段命令仍会混（可接受）。

(c) 不下钻只按工具名：最省，但 spec §A.4 用户故事 3 明确要「按命令首词聚合的排行」——**选 (c) 等于砍需求，需拍板者显式认账**。

出稿者倾向 (b)。

### 5.5 P5 · 前端入口形态

(a) 独立 chip：耗时与 token 是两件事（一个问「花了多少时间」，一个问「花了多少钱」），并进同一个弹出会让弹出变长且两组数字互相干扰。
(b) 并进 `UsageChip`：少一个页头元素；但 `UsageChip` 的既有边界注释写着「usage 与 cumulative 都缺席时整体不渲染」，塞进第三样东西会让这条规则变成三元判断。

出稿者倾向 (a)。

---

## 6. 缺陷族对抗审查

按[实例化清单 §3](2026-08-21-handoff-instantiation-checklist.md)：通用五族 + 项目两条追加设问 + 本项目第六族候选（webview/平台差异）。**逐族正面回答，结论并入对应子卡的验收栏。**

### 6.1 生命周期 / 状态机中断

**问**：打点中途 agentd 重启会怎样？孤儿条目谁回收？

**答**：会留下**半条段**——`tool` 条目的 `tool_call` 已发生、`tool_result` 永不到达。处置：**不写该条目**（没有结束时刻就没有耗时），于是该回合的 `Σtool` 偏小、`OtherMS` 偏大、`Partial=true`。这正是 `Partial` 存在的理由，**不需要额外的回收机制**——账目按 `(task_id, entry_key)` 落库，任务归档时随任务目录走，与 `task_usage_ledger` 同一生命周期。

`turn` 条目在重启后会**继续被覆盖**（键从内容派生，重启不改键），所以 `TotalMS` 自愈。

→ 并入 T1 验收（幂等键重放断言）与 T6 验收（`Partial=true` 分支）。

**未验证**：agentd 重启后 adapter 的 resume 路径是否重放已处理过的上游事件（若重放，`tool` 条目会用同一个 Key 覆盖成同值——无害；但若重放时 `turn` 号重新从 1 开始，键会**撞车覆盖真数据**）。→ 真机清单第 3 条。

### 6.2 静默失败 / 误导报错

**问**：每条错误路径的传播契约？存在「报成功但没做」的窗口吗？

**答**：有三处，各有处置——

1. **落库失败**：`handleTiming` 仅 Warn 不中断（照抄 `handleSpend`）。后果是账目缺一条 → `Partial=true`。**可接受且可见**。
2. **`TaskTiming` 返回 `(nil,nil)`**：这是「没有账目」的正常表达，但它与「聚合出错被吞掉」形状相同。处置：**聚合出错必须返回 error，绝不返回 `(nil,nil)`**——写进 T6 验收。
3. **最危险的一处**：`Store.TaskTiming` 现在是**空壳**（返回 `nil,nil`）且未接线。若 T6 只接线不实现，前端会显示「—」，看起来完全正常。→ T6 的「接线与实现同一轮」是硬要求，不是建议。

→ 并入 T6 验收。

### 6.3 跨平台假设 / webview 差异（项目第六族）

**问**：本改动哪些假设在其他平台不成立？

**答**：

- **后端**：无。时钟、SQLite、JSON 无平台差异。`time.Duration` 的毫秒折算在所有目标平台一致。
- **前端**：耗时展示只用文本与数字格式化，**不碰任何 webview 敏感 API**（无剪贴板、无 cookie、无拖放、无下载）——[已实证的三类 webview 差异](2026-08-21-handoff-instantiation-checklist.md)（WKWebView 扣 Strict cookie、Wails 真实手势剪贴板被拒）**均不适用**。
- **结论：无，因为本次前端改动的表面只有文本渲染，不触及任何在 WKWebView 与 Chromium 之间有过实证差异的 API。** 故 T7 不需要逐平台真机验。

### 6.4 假红 / 假绿测试

**问**：判据是不是中途副产物？有没有反面断言？并发下翻红吗？

**答**：本需求的假绿风险**高于平均**，三处——

1. **时钟未注入 → 计时竞态**：判据依赖真实时间的测试在 CI 负载下会偶发翻红，而「偶发红都是代理条件」。→ T1 硬要求时钟注入。
2. **反面断言集中**（`tool_call` 不带 `dur_ms`、`ListTasks` 不填 `Timing`、超 Cap 的桶不在结果里、`mapToolPart` 字样已消失）——**反面断言是稳定假绿的温床**：被断言不存在的东西一旦搬了家，断言照样绿。处置：每条反面断言**必须配一条正面断言锁住它搬去了哪**（如「`tool_call` 不带 dur_ms」配「`tool_result` 带 dur_ms=1500」——已在契约锁里成对写好，四张卡照此办理）。
3. **夹具能编码一个不存在的世界**：T2..T5 的夹具是**手写的上游事件序列**。若手写的序列与真实协议不同构，四张卡会全绿地验证一个编出来的现实。→ 每张 provider 卡的夹具**必须有一条真机项对应**（§7 第 1 条），且真机跑出的事件序列要与夹具比对。

→ 并入 T1/T2/T3/T4/T5/T6/T7 各自验收栏。

### 6.5 门禁绕过

**问**：新增的写路径过没过权限门？

**答**：**无，因为本次没有新增任何用户可触发的写路径或执行路径。** 耗时账目由 adapter 单向产出、manager 落库，全程无用户输入参与；读路径复用 `GetTask` 的既有鉴权（与 `Cumulative` 同一条）。`permgate` 不涉及。

唯一沾边的是 `Detail` 字段把命令文本引入 SQLite——那不是门禁问题，是凭据边界问题，见 6.7。

### 6.6 序列化边界（项目追加设问一）

**问**：新字段从产生到消费之间，每一处手写序列化/投影都列进文件清单并加断言了吗？

**答**：**这是本次最高风险的一族**，因为[实例化清单 §1.1](2026-08-21-handoff-instantiation-checklist.md) 记着 `internal/agentd` 有 **15 处手搭 `map[string]any` 回包**，而 2026-08-21 的两次 wire 缺陷**全部**落在这类地方（`CardView.ChildrenTotal` 派生字段被手搭 map 漏掉，两侧测试各自绿、端到端不通）。

本次的链路与每一处投影：

| 环节 | 是否手写投影 | 处置 |
|---|---|---|
| adapter → `AdapterEvent.Timing` | 否（结构体直传） | — |
| manager → `store.UpsertTiming` | 否 | — |
| SQLite 行 → `TaskTiming` | **是**（T6 的聚合） | T6 穷举测试 |
| `Task.Timing` → REST JSON | 否（`encoding/json` tag） | 但**必须进契约夹具**，见下 |
| REST JSON → TS `TaskTiming` | **是**（手写 interface） | `contract.test.ts` 强类型承接 |
| `Frame` → `frames.jsonl` → TS `Frame` | 否 + 手写 interface | 同上 |
| **`AttachInfo` 里的 Task（`handoff show`）** | **未知——可能命中那 15 处之一** | **真机清单第 2 条** |

**硬要求**：`TaskTiming` 必须接进 `internal/proto` 的 fixture 生成器（`contract_fixture_test.go:52` 的 `TestContractFixtures`，逐字节比对）+ `web/src/api/contract.test.ts` 的强类型承接。**「两端各自有测试」≠「这条链路有测试」**——这正是账本域两次踩坑的形状。

→ 并入 T7 验收（已列），并在 T6 验收里加一条：`Task.Timing` 出现在 fixture 里。

### 6.7 枚举新值过既有白名单（项目追加设问二）

**问**：新引入的枚举取值流经的每一处既有校验器/白名单/switch 都登记了吗？

**答**：本次新增枚举 `TimingKind`（`api`/`tool`/`turn`）。流经的位置：

| 位置 | 是否有白名单/switch | 结论 |
|---|---|---|
| `store.UpsertTiming` | 无（`kind` 原样存 TEXT） | 不拦 |
| `TaskTiming` 聚合 | **有 switch**（按 kind 分流） | T6 必须处理**未知 kind**：跳过并计入 `Partial`，**不 panic、不静默当成 0** |
| REST 层 | 无（字段不出现在响应里，只出现在聚合结果） | 不拦 |
| TS 侧 | 无（`TimingKind` 不进前端类型——前端只见聚合结果） | 不拦 |

**另一个方向的检查**：`FrameType` **没有新增取值**（`dur_ms` 挂在既有的 `tool_result` 上），所以 `buildBlocks` 的 `switch`（`frames.ts:154`，`default: unknown` 在 `:204`）与它的 `default: unknown` 分支都不受影响。✅

`AdapterEvent.Type` **没有新增取值**（走既有的 `"usage"`），所以 `manager.go` 的事件 switch 与所有对事件类型做穷举断言的测试都不受影响 —— 这正是契约文档 §6.3 那条拍板记录要买到的东西。✅

→ 并入 T6 验收（未知 kind 分支）。

### 6.8 凭据边界（非标准族，本次特有，一并回答）

`TimingEntry.Detail` 把命令文本引入 SQLite。**不是新的暴露面**（同一份原文今天已在 `frames.jsonl` 里），但**是一个新的存放地点**。硬要求：200 rune 上限、头尾截断、不存全文。截断在**采集侧**做（`UpsertTiming` 明确不做截断——契约文档已写死，防止两处都以为对方管了）。

→ 并入 T1 验收（截断在采集侧）与 T6 验收（store 不做截断）。

---

## 7. 真机清单（未验证，需真机 —— 归协调者执行）

d_executor 是**边界型**子系统，接缝对面是真实 executor 进程与其协议，机内只能验契约形状。以下四条是**行为事实**，不得按推测写成结论：

1. **四家的段边界真实形状**（承 P1 的风险）——对每家各跑一个含「模型输出 → 工具调用 → 模型输出」的真实回合，抓上游事件序列，确认：(i) 「一批工具结束」的信号形状是否四家同构；(ii) T2..T5 的手写夹具与真实序列同构（防 6.4 第 3 条的「夹具编码不存在的世界」）。
2. **`handoff show <task>` 的输出里真的带 `timing`**（承 §3 与 6.6）——跑一个真实任务，`handoff show <id> | jq .task.timing`。拿不到即说明中间有手搭投影层，另立卡。
3. **agentd 重启后的回合号是否延续**（承 6.1）——跑到一半重启 agentd，确认 resume 后的 `turn` 号不从 1 重新开始（否则幂等键撞车覆盖真数据）。
4. **并发工具真的会出现**（承 T1 的并发断言与 `ToolSpanMS` 的存在理由）——跑一个会让 claude 并行发多个 `tool_use` 的任务，确认 `ToolMS > ToolSpanMS` 在真实数据上成立。若真机上从不出现并发，`OffsetMS` 这个字段的存在理由要重新审视（但**不因此删它**——契约已冻结，删要重走 contract）。

---

## 8. 交稿自检

1. **产出四样齐全** ✅ —— 子系统清单每个带类型（§1）、契约增量核对逐条有结论（§2）、7 张子卡全部四段式且判据行为化（§4）、缺陷族逐族有答案含两条「无，因为……」（§6）
2. **「待拍板」岔口集中列在稿首** ✅ —— §0，六条，无一自批
3. **「未验证，需真机」已汇总** ✅ —— §7，四条
4. **每张子卡的有界文件集核过** ✅ —— T1..T5 各自一个子包/文件、T6 是 `internal/store`、T7 是 `web/src/app/task` + `api`；架构法第三条的三条判据在 §1.1 与 §1.2 逐条回答，**均不命中，无需插竖切还债卡**

**红线遵守**：未写实现代码、未建卡、未派发、未调派发工具。扇出归协调者。
