# 审批工单通道整顿（B57 + B58 + B50）设计

## 1. 范围与动机

三条缺陷，共同点是**审核者的回合被白白烧掉**——要么被同一件事问很多遍，要么被引去答一张答不掉的单，要么答了等于没答。

| 条目 | 一句话 | 本 spec 的处置 |
|------|--------|--------------|
| B57② | 同一个目录授权反复索取，批准不粘，实测同一路径连问 8 次 | 做：任务级裁决复用 |
| B57① | 同一条命令先后触发 `external_directory` 门与 `bash` 门，一个动作两张单 | **不做**，见 §2.3 |
| B58 | agentd 重启后补发的工单是新 id，旧工单永不作废，`pending_tickets` 挂着幽灵单 | 做：question 工单稳定 id |
| B50 | `reply --deny --reason` 的原因文本不透传给 executor，审核者以为能纠偏、实际不能 | 做：原因挂起后经 Send 下发 |

放在一个 spec 里做，是因为三条全部落在 `internal/agentd/manager.go` 的权限/工单同一条代码路径上，且**验收场景是同一个**：派发一个真实 opencode 任务，一次就能同时观测三条（见 §7.2）。

**不在范围内**：

- 工单文案标注 subagent 来源（原 B53，08-11 判定无实际价值，已 shelved）
- 跨任务的裁决复用（见 §3.4）
- 权限应答契约 `RespondPermission(ctx, taskID, permID, decision string)` 的签名——本 spec 一个字不改它

## 2. 事实基础：读码复核结论

三条在 backlog 上记的根因方向，有两条读码后被改写。以下是实现前必须以之为准的事实。

### 2.1 B58 的根因比原记的更干脆

原记：「每张工单由一个 `manager.waitQuestion` goroutine 看着，agentd 重启后旧 goroutine 随进程消失，而 store 里的工单行没有任何一处会把它标废。」

真相：gate 工单 id 是 `taskID + ":" + permID`（[manager.go:1304](../../../internal/agentd/manager.go)），**幂等**；而 ask 工单 id 是 `uuid.NewString()`（[manager.go:1773](../../../internal/agentd/manager.go)），**天生不幂等**。重放必然产出第二张单，与 goroutine 是否存活无关——goroutine 消失只是让旧单没人等，不是它变成幽灵的原因。

修法方向随之改变：不是「重启时把旧单标废」，而是**让第二张单压根不会产生**。

### 2.2 B57② 的「批准不粘」是 handoff 自己的契约限制

`RespondPermission` 的 decision 全链只允许 `once` / `reject`：opencode 客户端在 [api.go:549](../../../internal/executor/opencode/api.go) 显式拒绝其他取值，codex/grok/claudecode 三家的实现同样是「除 `once` 外一律拒绝」。

这条事实排除了「透传上游的持久授权语义」这条路——它需要四家协议逐一探针，而粘性范围由上游定义、handoff 看不见也收不回。08-11 定案：**粘性放在 handoff 侧，任务级，落 store 跨重启存活**。

### 2.3 B57① 不是 handoff 把一条拆成两张

opencode **自己就发了两条 `permission.asked`**：一条 `permission:"external_directory"`、一条 `permission:"bash"`，两条的 `metadata.command` 是同一段脚本，但 perm id 不同，所以 `CreateTicket` 的幂等去不掉。

08-11 定案：**① 不做**。B27 的双层判据（路径走归属、命令走黑名单）是有意设计，两扇门拦的是不同风险；合并等于用「批准了命令」推出「批准了它要碰的越界目录」。量上 ① 只占实测两组，② 才是烧回合的大头。

### 2.4 B50 没有承载位

`gateDecision`（[manager.go:1715](../../../internal/agentd/manager.go)）把非 `allow` 一律折成 `"reject"`，而 `RespondPermission` 的签名里**没有 reason 的位置**。要下发原因只有两条路：扩五动作契约，或另找通道。08-11 定案：走后者——复用 `Send`，即 B54.2 那次 `--answer` 一次就通的同一条通道。

## 3. 设计：裁决指纹（B57② 的载体）

### 3.1 存储

不新建表。`tickets` 加一列：

```sql
fingerprint TEXT NOT NULL DEFAULT ''
```

迁移照 `tickets.delivered_at` 的既有写法：单条 `ALTER TABLE … ADD COLUMN`，容忍 `duplicate column` 错误（SQLite 无 `ADD COLUMN IF NOT EXISTS`）。

gate 工单建单时填 `sha256(权限描述全文)` 的 hex；ask 工单留空。

**为什么不建缓存表**：缓存就是既有的工单历史——工单已经存着「谁在什么请求上做了什么裁决」，再开一张表就是第二个真相源，两边不同步时以哪个为准无法回答。取 sha256 而非原文入列，是因为权限描述可长达 64KB，不适合做索引键。

### 3.2 判定点与顺序

新增 `reuseDecision(taskID, ev, ticketID) bool`，在 `escalatePermission` 的**第一行**调用，命中即走既有的 `approvePermission` 放行并 return true。

**必须早于 `transitBestEffort(waiting_answer)`**：否则会先把任务推进 waiting_answer、再放行回迁 running，状态凭空抖动一次，`resumeIfIdle` 的判定面也跟着变复杂。

放在 `escalatePermission` 而不是 `handlePermission` 入口，语义是「**本该叫醒人**的请求，先看人是不是已经就同一件事表过态」。审批者自动批准的请求根本不建工单、不留指纹，也不需要复用——它本来就不烧审核者的回合。

### 3.3 复用条件

四条全满足才复用：

1. 同 `task_id`
2. 同 `fingerprint`
3. `answer` trim 后 == `allow`
4. `delivered_at` 非空

第 4 条是「送达才算数」：应答落库但中继失败的工单（B12 之后 `delivered_at` 独立于 `answer` 的全部意义所在）不构成有效先例——executor 侧那次请求根本没收到批准。

**只复用 allow**。deny 一律照旧逐次问：自动重复拒绝会静默掐死回合，方向与 §5 正好相反。

### 3.4 作用域：任务级

跨任务不复用。不同任务的工作区不同、计划不同、风险面不同，「上个任务批过」不构成本任务的批准依据。这条边界也让复用状态随任务归档自然消失，无需清理策略。

同指纹在一个任务内的复用次数**不设上限**——消灭那 8 次重复正是本条的目的。

### 3.5 审计

每次复用落一条 `permission_reuse` 事件，**只入库不 Publish**（照 `approver_decision` 的先例：没有人需要被唤醒）。payload 带原工单 id、fingerprint 前 8 位、权限描述的展示截断。日志 Info 一条。

审核者 `handoff show` 能看到「这条是复用工单 X 的裁决自动放行的」——复用必须留痕，否则「我明明没批过这个」将无从对质。

### 3.6 并发

同指纹的两条请求同时到达时，两个 goroutine 都会查到同一条已答工单、都放行。结果一致，不加锁。

## 4. 设计：question 工单稳定 id（B58）

### 4.1 契约新增

`executor.AdapterEvent` 增一个**可选**字段：

```go
QuestionID string // 提问的稳定 id（executor 侧有原生 id 时填），空表示无
```

opencode 的 `mapQuestionAsked` 填原生 `qa.ID`（形如 `que_ff048094…`）。claudecode / codex / grok 的 trailer ask 没有原生 id，留空，退回今天的 `uuid.NewString()`，行为完全不变。

### 4.2 建单三岔

`handleQuestion` 有 `QuestionID` 时 ticket id 用 `taskID + ":" + questionID`，与 gate 工单同构。`CreateTicket` 的返回按三岔处理：

| `CreateTicket` 结果 | 含义 | 动作 |
|---|---|---|
| created=true | 首次提问 | 正常建单 + 发事件 + 挂 waiter |
| created=false 且旧单 `answer` 为空 | agentd 重启后的重放 | **跳过建单**，不发第二条事件，但仍重挂 waiter |
| created=false 且旧单已答 | 折算失败后的重发 | 退回 uuid 建新单 |

第二行是 B58 的正解：幽灵单不再产生，因为压根不会有第二张。

### 4.3 第三岔是必须的，不是防御性冗余

opencode 有一条「答复没对上选项 → 重发工单请审核者再答」的路径（[adapter.go:517](../../../internal/executor/opencode/adapter.go) 与 [:528](../../../internal/executor/opencode/adapter.go)），它用的是**同一个 reqID**。

若无脑幂等，这次重发会被 `created=false` 吃掉——审核者答错一次之后**再也答不了**，任务停在 waiting_answer 直到 stall 超时。那比 B58 本身严重得多。第三岔按「重发本就是新一轮提问」处理，用新 uuid 出单。

### 4.4 重放分支为什么仍要重挂 waiter

新 agentd 实例里没有任何 goroutine 在等旧工单。不重挂也不会死锁——`reply` 找不到等待者会走 `RelayAnswer` 自愈中继，ask 工单的中继就是 `Send` 原文，能到达 executor。但重挂让两条路径行为一致（命中 waiter 与走中继的差别只在日志），hub 支持同一 ticket 多等待者，重挂是幂等的。

## 5. 设计：deny 原因下发（B50）

### 5.1 解析

`gateDecision(answer)` 改为返回 `(decision, reason)`：

- trim 后 == `allow` → `("once", "")`
- 其余一律 `("reject", …)`，reason 从 `deny` / `deny: <原因>` 剥前缀取余文，取不出即空串

CLI 已经把 `--deny --reason r` 拼成 `"deny: r"`（[cmd/reply.go:81](../../../cmd/reply.go)），解析与它对齐。

**「非 allow 一律 reject」这条安全语义一个字不动**——本节只新增一条旁路信息，不改裁决本身。

### 5.2 为什么不能 reject 之后立刻 Send

opencode 收到 reject 会**当场终结回合**，随后 `mapIdle` 看到零文本 + `turnRejected` 非空，emit 一条「上一步操作因权限被拒而终止了本回合……请给出下一步指令」的兜底 question（[adapter.go:1608](../../../internal/executor/opencode/adapter.go)）。

立刻 Send 会撞上正在终结的回合；更糟的是那张兜底工单照样出——审核者刚说完要怎么改，又被问一遍「请给出下一步指令」，回合反而多烧一个。

### 5.3 挂起—消费

1. reply 带 reason 的 deny → manager 记 `denyGuidance[taskID] = reason`：任务级、内存、**取走式**（与 opencode/codex 已有的 `askedViaTool` 标记同构）。
2. `RespondPermission(reject)` 照发，回合按既有方式终结。
3. `handleQuestion` 入口：本任务有挂起 guidance 时**不建工单**，改为取走 guidance 并 `Send` 组装文本，开新回合：

   > 你请求的操作已被审核者拒绝。原因：`<reason>`。请据此调整做法后继续，不要重复发起同一请求。

   落一条 `deny_guidance_relayed` 审计事件。模型收到后若仍要问，会再发一次 question，那时 guidance 已消费，正常出单。

   **这条分支不得触碰状态机**：`handleQuestion` 今天的第一步是 `transitBestEffort(waiting_answer)`，而本分支不建工单——落 waiting_answer 会造出「等你回答却零挂起工单」这个 U-1 专门修掉的死形态（reply/continue/done 三条路全封死）。任务保持 running，`Send` 直接开新回合。

`waitPermission`（命中 waiter）与 `RelayAnswer`（自愈中继）两条路都要登记 guidance——审核者的 reply 走哪条取决于时序，行为不能有差别。

### 5.4 为什么敢吞掉任何一条 question

manager 没有可靠办法区分「被拒终止的兜底提问」与「模型真的在问问题」。文本前缀匹配太脆（兜底文案一改就失效），加 cause 字段则把这条修法扩成了改契约——而契约不动是本 spec 的前提。

取舍：吞错的代价是**模型的**一个回合（它会重问），漏抑制的代价是**审核者的**一个回合。后者正是本 spec 要消灭的东西。

### 5.5 边界

- **回合终结成 result**（模型没走提问就直接收尾）：guidance 没机会用上。任务离开 running / waiting_answer 时清空，并落一条审计事件写明「拒绝原因未下发（回合已终结）」，审核者可用 `continue` 自己带上。
- **agentd 重启**：guidance 是内存态，重启即丢，退化为今天的行为。事件里有痕迹可查。不进 store——它的生命周期是「从 deny 到下一个 question」，通常秒级到分钟级，为它加一张表不划算。

## 6. 不做什么

| 诉求 | 为什么不做 |
|---|---|
| B57①（双层门合并） | §2.3。B27 的双层判据是有意设计，合并等于放宽安全语义 |
| 跨任务复用裁决 | §3.4。不同工作区不同风险面 |
| 复用 deny | §3.3。自动重复拒绝会静默掐死回合 |
| 扩 `RespondPermission` 带 reason | §2.4。四家协议承载能力不齐，没原生位的最后仍要退回 §5 的做法 |
| guidance 持久化 | §5.5。生命周期太短，不值一张表 |
| 权限白名单配置 | 与 §3 的复用重叠——复用是「人批过一次」，白名单是「人预先批一类」。前者不新增安全策略面，后者新增。真有需求时另立条目 |

## 7. 验收

### 7.1 单测

| 面 | 用例 |
|---|---|
| `gateDecision` | 表驱动：`allow` / `deny` / `deny: 原因` / 任意文本 / 空串 → decision 与 reason 双返回值 |
| 指纹复用 | 同指纹已 allow → 复用；已 deny → 不复用；跨任务同指纹 → 不复用；`answer=allow` 但 `delivered_at` 为空 → 不复用 |
| question 三岔 | 首次建单 / 未答重放不建第二张且重挂 waiter / **已答后重发建新单** |
| guidance | 取走式消费；第二条 question 正常出单；任务落终态时清空并落审计事件 |

question 三岔的第三条必须有独立用例钉住——它正是 §4.3 那个「把 B58 修成更严重挂死」的反例。

### 7.2 真机验收

一次 opencode 派发同时观测三条，不必分三轮：

| 条目 | 观测点 |
|---|---|
| B57② | 批准一次 `external_directory: <mod cache 路径>` 后，后续同路径请求**不再出工单**；`show` 里有 `permission_reuse` 事件 |
| B58 | 提问挂起时重启 agentd → 重放不产生第二张单；`pending_tickets` 全程只有一个 id，答复后正常清空 |
| B50 | `--deny --reason "改用 X"` → 模型**换了做法**而非原样重发；`show` 里有 `deny_guidance_relayed` |

公共门槛照三期惯例：`go build ./...` + `go vet ./...` + `gofmt -l .`（无输出）+ `go test ./...` 全绿 + `go test -race ./internal/agentd/ ./internal/store/ ./internal/executor/opencode/` 全绿。
