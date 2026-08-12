# follow 积压对账：重连/补挂时把未读事件折成一行摘要

> 对应 backlog：**B22**（原题「handoff wait 重放已处理的历史事件」，08-11 因 B56 落地两度收窄）

## 1. 问题

B56（`wait --follow`）让审核者的订阅从「一事件一退出、每轮重挂」变成常驻，消灭了「回合之间的订阅真空」。但常驻不等于不断线，有两个间隙无法根除：

- **断网**。合上笔记本通勤、切网络、对端重启——`FollowEvents` 会退避重连，间隙期 agentd 照常运行、事件照常积累。
- **忘挂**。agent 会话没挂上 `wait --follow`（或挂在了错的形态上），过一段时间经人工提醒才补挂。

两种情形下 cursor 都**存在且正确**，重连/补挂时服务端从 cursor 之后逐条补发积压事件。B56 之前这只是刷屏；B56 之后审核者会话配 Monitor `persistent:true`，**stdout 每一行 = 一次会话唤醒**，于是一次重连炸出 N 次唤醒。08-11 B56 派发期间一小时内积了 14 条 `permission_request` + 2 条 `completed`，即是这个量级。

### 1.1 不是「起点选择」问题

B22 原先记的是「本机没有 cursor 时从 seq 0 重放」，方向错了。真实场景里 cursor 都在，问题出在**积压的交付形态**而非起点。

顺带记录一条在本次设计中查实、足以淘汰原备选方案的事实：**「无 cursor 时默认从当前水位起」会丢事件**。`cursor` 文件只由 `WaitEvent`/`FollowEvents` 在交付事件时写（`internal/client/client.go` 的 `writeCursor` 仅有两个调用点），`dispatch` 完全不碰它。因此「文件不存在」同时覆盖两种相反处境：

- 刚 `dispatch` 完、第一次挂 follow（本机日常主路径）——派发到挂上之间的窗口里最可能到达的正是 `permission_request`，自动跳过等于任务静默挂死；
- 换机接管一个已跑很久的任务——历史几十条早被别处处理过，从 0 重放才是问题。

客户端看这两种情况完全一样。所以自动推断起点在主路径上不安全，该方案作废。本设计不改起点。

### 1.2 换机接管

原被计划「只写进 skill 说明」，但本设计的机制天然覆盖它（见 §2.4），不需要单独处理。

## 2. 设计

### 2.1 开场对账

`FollowEvents` 在**每次建立 WS 连接之前**做一次对账，而不是让服务端把积压逐条推过来：

1. 读本机 cursor `C`；
2. 调一次 `Client.Attach`（`GET /api/tasks/{id}`，即 `handoff show` 的数据源）取快照；
3. 令 `W` = 快照 `RecentEvents` 中的最大 `seq`（空数组时为 0）；
4. `W > C` → 有积压：向 stdout 吐**一行**摘要，把 cursor 推到 `W`，随后带 `from_seq=W` 建 WS；
5. `W == C` → 无积压：不吐任何东西，行为与改动前逐字一致。

**积压事件根本不被拉取。** 摘要不是「N 条事件的压缩」，而是用权威快照直接回答「你现在该做什么」。

对账挂在**首次连接与每次重连之前**——§1 的两个场景一个走重连路径、一个走首连路径，同一机制覆盖。1 秒闪断且无新事件时 `W == C`，对账自动静默。

### 2.2 为什么摘要比逐条重放信息更全

逐条重放里混着**已经被审批链答掉**的历史工单——那些 `permission_request` 事件已无对应的可回复工单，补 `reply` 会 404。而快照的 `pending_tickets` 只含真正还欠的，且每张带完整 `Request` 原文（`proto.Ticket.Request` 与事件 payload 是同一份数据）。

由此得到一个重要推论：**摘要内嵌完整待办工单，因此积压只有 1 条时折叠也不丢内容**。这消除了「小间隙应该逐条、大间隙才折叠」的阈值需求——不设阈值，一律折叠。

### 2.3 不需要改 agentd

摘要所需的三样东西全在 `GET /api/tasks/{id}` 的现有响应里：

| 需要 | 来源 | 权威性 |
|---|---|---|
| 当前状态 | `Task.State` | 权威 |
| 还欠哪些工单 | `PendingTickets`（每张带 `Request`） | 权威 |
| 积压区间与构成 | `RecentEvents`（最近 100 条，`recentEventsLimit`） | 窗口内精确，超窗降级为「≥」 |

本设计改动范围：`internal/client`（机制）、`cmd/wait.go`（文档串）、handoff skill（一段说明）。**agentd 一行不改**，因此没有版本错配面。

### 2.4 换机接管落在同一条路径上

`C = 0` 时 `W - C` 即全部历史，同样折成一行。`RecentEvents` 只有 100 条窗口，故 `missed`/`stale` 标记为截断（见 §3.2），但 `actionable` **仍然精确**——`pending_tickets` 是权威，不受窗口影响。这比原计划的「只写进 skill」更好，无需额外机制。

## 3. 摘要行

### 3.1 线格式

单行 JSON，走 stdout（与事件行同一通道，Monitor 按行解析）：

```json
{"type":"backlog_summary","task_id":"…","from_seq":2489,"to_seq":2537,
 "state":"waiting_answer","missed":14,"missed_truncated":false,"stale":11,
 "actionable":[{"id":"…:perm-7","kind":"gate","request":{…}}]}
```

`"type":"backlog_summary"` 是本改动**唯一**触及 stdout 契约的地方。它是客户端合成的行，服务端从不存这个事件类型。沿用 `type` 这个 key（而非另起 `kind`）是为了让既有的按行解析器读到一个不认识的 type 就跳过，而不是撞上一个缺字段的对象。

`actionable` 元素即 `proto.Ticket`（`id` / `kind` / `request` 等原样），审核者可直接据此 `reply --ticket <id>`。

### 3.2 三个计数各自独立，不做减法

| 字段 | 定义 |
|---|---|
| `missed` | 间隙内可交付事件条数：`RecentEvents` 中 `seq > C` 且类型属于 `question`/`permission_request`/`completed`/`failed`/`stalled` 的条数（`progress` 按 follow 的既有过滤口径排除） |
| `actionable` | `pending_tickets` **全量**，不限于间隙内 |
| `stale` | 间隙内的工单类事件（`question`/`permission_request`）中，`ticket_id` 已不在 `pending_tickets` 里的条数 |

**为什么不能写成 `stale = missed - actionable`**：两者定义域不同。存在「断网前你就看见过、但一直没答」的工单——它在 `pending_tickets` 里，但它的事件 `seq ≤ C`，不在间隙内。减法会算出错数甚至负数。分开算是唯一诚实的做法，而且那类工单恰恰是审核者最需要知道的。

`ticket_id` 是 `permission_request`/`question` 事件 payload 的既有契约（`internal/agentd/manager.go` 的 `permissionPayload`/`questionPayload`）。客户端以最小结构体解码该字段。

### 3.3 seq 是全局的，不能拿来做减法

`events.seq` 是 SQLite 的 **全局 AUTOINCREMENT**，跨任务共享同一个计数器（`internal/store/store.go` 的建表 DDL，以及 `EventsFromAsc` 注释中已记明的「跨任务单调递增」）。因此单个任务的 seq **不连续**——并发跑着别的任务时会大段跳号。

两条直接推论，实现时必须守住：

- **`W - C` 不是条数**，它混着其他任务的事件。`missed` 只能靠遍历 `RecentEvents` 逐条判类型来数，不能算差值。
- **不能用 seq 连续性判断窗口是否有缺口**（如 `oldest.Seq > C+1`），那个判据在全局 seq 下恒为真。

### 3.4 「可交付」的口径，以及一处现存的不一致

`missed` 数的是「审核者本该被唤醒的事件」，因此需要一个明确的可交付集合：

```
可交付 = 全部事件类型 − {progress, approver_decision, approver_disabled}
```

这正是 handoff skill 已经写明的契约（「这三类不会唤醒 wait」）。但**代码只挡了 `progress`**（`internal/client/client.go` 的两处过滤仅比对 `EventTypeProgress`）。另两类之所以看起来"没出现过"，是因为服务端对它们**只入库不 Publish**（`internal/agentd/manager.go` 的 `approver_decision` 追加处有明确注释）——实时流里确实见不到。

**但重放路径读的是 store。** WS 补发走 `EventsFromAsc`，会把这两类一并推给客户端，客户端不过滤，于是交付。结果是：一次重连交付的东西比实时流更多，多出来的全是审计噪音；审批链裁决越多，重连时的唤醒风暴越大。

处置：抽一个 `isDeliverable(proto.EventType) bool` 谓词，**同时**用于流过滤（`streamOnce` 的两处）与摘要计数，使二者口径一致，并让代码追上已写明的文档契约。`all=true` 时不做任何过滤，行为不变。

这一条超出本 spec 的原始范围（它同时改变一次性 `wait` 在重放路径上的行为——严格更少），单独成 task，可独立取舍。

### 3.5 截断判据

`missed_truncated` 为 `true` 当且仅当 `RecentEvents` 非空且 `RecentEvents[0].Seq > C`——窗口里最旧的一条仍晚于 cursor，意味着**无法证明**窗口覆盖了整个间隙。此时 `missed`/`stale` 的语义降级为「至少」。

为什么用这个判据而不是「窗口满 100 条」：客户端不知道 agentd 的 `recentEventsLimit`，把 100 写进客户端会造成版本耦合，且未来若服务端调小该值，客户端会**漏报**截断——错在危险的方向。现有判据的代价是偶尔**虚报**（例如任务总共只有 3 条事件、`C = 0`，窗口没截断却报「至少 3 条」），错在安全的方向：宁可少声称，不可多声称。

这是全文唯一会说「至少」的地方，必须显式标出，而不是给一个看起来确定的数——与 `LiveUnknown`、`Watchers *int` 同一条纪律：探不出结论时不猜。截断只影响 `missed`/`stale` 两个描述性计数；**驱动行动的 `actionable` 永远精确**，因为 `pending_tickets` 是权威、与窗口无关。

## 4. 边界与失败处置

| 情形 | 处置 | 理由 |
|---|---|---|
| `Attach` 调用失败（对端刚好在重启） | **降级**：不吐摘要，带 `from_seq=C` 连 WS 逐条重放；Warn 一行带 cause | 摘要是优化不是正确性。对账失败退回旧行为，绝不因此中断 follow |
| 快照 `state == failed` | 吐摘要后 `FollowEvents` 返回 nil（退出 0） | 改动前靠收到 `failed` 事件返回；积压被跳过后终结判据必须由 state 接上，否则 follow 会挂在死任务上 |
| 快照 `state == completed`（已归档） | 照常连 WS，服务端以正常关闭码收尾，返回 nil | 复用 B56 已建好的归档路径 |
| `RecentEvents` 为空（`W = 0`） | 无积压，静默 | 新任务的正常形态 |
| cursor 推到 `W` 时写盘失败 | Warn，继续 | 下次对账会重新吐同一行摘要；重复一行摘要无害，比吞掉安全 |
| `Attach` 返回 404 / 401 | **同样降级**，不在对账里判永久性 | `Client.Attach` 的错误是普通 `fmt.Errorf`，不是 `permanentError`，`isPermanent` 认不出它。与其在对账里复制一套永久性判定，不如让紧随其后的 WS 握手去判——404 对应服务端的 `StatusPolicyViolation` 关闭、401 对应握手 `permanentError`，都走既有的、已被测试覆盖的路径。结果等价，判定只有一处 |

## 5. 测试

`internal/client/follow_test.go` 现有 httptest server 只服务 WS，需扩成同时服务 `GET /api/tasks/{id}`。

1. 有积压 → 恰好一行摘要；cursor 被推到 `W`；WS 握手的 `from_seq` 等于 `W`
2. 无积压（`W == C`）→ **一行摘要都不吐**，行为与改动前逐字一致
3. `Attach` 失败（含 404/401）→ 不报错、不吐摘要，逐条重放；永久性由随后的 WS 握手判定
4. 积压含 3 条 `permission_request`、其中 1 张仍 pending → `missed=3`、`actionable=1`、`stale=2`
5. `actionable` 含一张 `seq ≤ C` 的老工单 → 计数不受影响（验证不做减法）
6. **seq 不连续的夹具**：积压事件的 seq 刻意跳号（如 `C=100`，事件为 `104/109/117`，模拟并发任务占号）→ `missed=3` 而非 `17`。这条专防实现时顺手写成 `W - C`
7. 窗口未覆盖到 cursor（`RecentEvents[0].Seq > C`）→ `missed_truncated=true`，且 `actionable` 仍精确
8. 快照 `state=failed` → 吐摘要后返回 nil
9. **重连路径也对账**：两连接的 httptest，第一条连接断开、第二次连接前积压了事件 → 第二次连接前吐摘要
10. `isDeliverable`：`approver_decision` / `approver_disabled` / `progress` 三类既不计入 `missed`，也不被流交付；`all=true` 时三类全部交付（行为不变）

## 6. 日志与注释

按 `instrumenting-code`：

- 对账开始一行 Info（`task` / `from_seq`）、对账结论一行 Info（`from_seq` / `to_seq` / `missed` / `actionable` / `stale` / `truncated`）
- 降级路径一行 Warn 带 cause
- 新函数的文件头与方法注释；两处 why 必须写明：**为什么三个计数不做减法**、**为什么 `Attach` 失败是降级而不是报错**

## 7. 明确不做

- **不改起点语义**。不加 `--since=now` / `--from-seq`：§1.1 已说明自动推断不安全，而显式参数又是「每次接管要记得加」的人工动作——正是 B56 要消灭的那类东西。对账机制让两者都不必要。
- **不加折叠阈值**。§2.2 已说明摘要内嵌工单原文，N=1 时折叠也不丢内容。
- **不动 agentd**。§2.3。
- **不做「只折叠已失效的、有效的仍逐条」**。那需要客户端拿快照与每条事件交叉比对后仍走逐条投递，复杂度和出错面更大，而收益（保留有效事件的原始 payload）已被「摘要内嵌 `Request` 原文」覆盖。
