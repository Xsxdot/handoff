# B92 根因排查：`failed` 事件已落库，任务状态却没迁移，永久卡在 `running`

排查日期：2026-08-15。纯只读排查，未改任何生产代码。
现场机：mac-02。日志时区均为 +08:00；数据库时间均为 UTC（Z）。

---

## 0. 根因一句话

**seq 660 那条 `failed` 事件并不是「没迁移状态」落库的——它落库时状态正确迁到了 `waiting_review`。**
**真正让任务「永久卡 running」的是 `handleResult` 之后的协调者 `continue`：grok adapter 的
`emitFailed` 在回合失败时把事件通道永久关闭（`closeEvents`），而 `Send`（continue 续接）在同一
runstate 上复用这条已关闭的通道开新回合；新回合的一切事件被 `emit` 静默丢弃（`evClosed` 短路），
manager 的 mediate 循环也早已随通道关闭退出——续接回合永远到不了 `handleResult`，任务于是停在
`running` 直到 2h 看门狗 `stalled`（而 stalled 只告警、不修复）。**

即：不存在「追加 failed 事件但状态不迁移」的单一原子缺口；观察到的失配是 **事件迁移正确 → 协调者
continue 回迁 running → 续接回合事件被关闭的通道吞掉** 这一连串动作的累计结果。两例都是 grok，
是 adapter 结构性差异，不是巧合。

---

## 1. 结论先行：任务的核心矛盾前提不成立，用日志钉死

计划预设的「核心矛盾」是：`handleResult` 先 `transitToReview` 再 `AppendEvent`，迁移失败时连事件
都不追加，那 seq 660 是怎么「没迁状态就落库」的？

日志给出的事实是：**seq 660 落库时迁移发生了**（`running → waiting_review`），而且在后续约 94 秒内
任务一直是 `waiting_review`。失配是在更晚的 `continue` 之后才出现的。

标本 `398259b7` 的 agentd 日志（`~/.handoff/agentd.log`，+08:00）：

```
12:33:33.515064 ERROR  msg="grok 任务失败" task=398259b7… reason="回合非正常收尾 stopReason=cancelled"
12:33:33.515120 INFO   msg=执行结果事件 task=398259b7… ok=false branch="" commit=""
12:33:33.515988 INFO   msg=任务状态迁移 task=398259b7… from=running to=waiting_review reason=result
12:33:33.516131 WARN   msg=回合以失败收尾 task=398259b7… reason="回合非正常收尾 stopReason=cancelled" … void_reason="executor 已终结"
12:33:33.516782 WARN   msg="拒绝原因未下发：回合已终结，用 continue 自己把话带上" task=398259b7…
12:33:33.516994 INFO   msg=中介循环结束，开始对账 task=398259b7…
12:33:33.517086 INFO   msg="executor 已不在，开始对账" task=398259b7… state=waiting_review reason="executor 事件流已终结（进程退出或连接断开）"
12:33:33.517105 INFO   msg=任务无需状态对账，仅清扫残留 task=398259b7… state=waiting_review
12:33:33.565797 INFO   msg=任务详情完成 task=398259b7… state=waiting_review pending=0 watchers=1   ← 此刻确认在 waiting_review
12:34:02.072561 INFO   msg=任务详情完成 task=398259b7… state=waiting_review pending=0 watchers=0   ← 仍为 waiting_review
12:35:07.343195 INFO   msg=任务状态迁移 task=398259b7… from=waiting_review to=running reason=continue  ← 协调者 continue 回迁
12:35:07.343495 INFO   msg="grok 续接回合" task=398259b7… session=019ffe88-236a-7a52-8c07-5b52a35f0fc0
12:35:14.426316 INFO   msg="grok 探活：serve 存活" task=398259b7… port=50007                       ← serve 还活着，无事件产出
```

对应数据库（`~/.handoff/handoff.db`，只读查询，seq 640–661 全量）：

| seq | type | payload | created_at (UTC) |
|-----|------|---------|------------------|
| 645 | progress | `{"text":"grok 会话已就绪"}` | 2026-08-14T04:29:23.184813Z |
| 658 | approver_decision | …call-898677a0…-15（升级人工） | 2026-08-14T04:32:23.43993Z |
| 659 | permission_request | …call-898677a0…-15 | 2026-08-14T04:32:23.44145Z |
| 660 | **failed** | `{"fail_reason":"回合非正常收尾 stopReason=cancelled",…}` | 2026-08-14T04:33:33.516583Z |
| 661 | deny_guidance_dropped | …原因全文 + `cause:"回合在拒绝原因下发前终结（Done/stop/result），未送达 executor"` | 2026-08-14T04:33:33.516844Z |
| 699 | resource_pressure | `{"used":2409,"limit":2400}` | 2026-08-14T05:24:50.936837Z |
| 703 | stalled | `{"last_seq":699,"idle":"2h0m58s"}` | 2026-08-14T07:25:49.333733Z |

tasks 表当前行（标本，与 `handoff show` 一致）：

```
id=398259b7-4124-4b42-9d66-0172646190a5  state=running  executor=grok
updated_at=2026-08-14T04:35:07.342673Z   ← 恰是 continue 时刻，而非失败时刻
```

`updated_at` 停在 04:35:07（continue 那刻）是决定性旁证：失败（04:33:33）之后任务状态曾被迁移到
waiting_review，又被 continue 在 04:35:07 搬回 running，此后 `updated_at` 再没前进过。

---

## 2. `failed` 事件的产生路径（Q1 答案，逐条代码坐标）

生产代码里追加 `failed` 事件的只有三处（另有两处在测试文件里）：

### 2.1 `handleResult`（`internal/agentd/manager.go:2472`）—— 事件「回合结果」
顺序：**先迁移，后追加**。`transitToReview`（`:2504`）失败 → 直接 return，连事件都不追加（`:2505`）。
追加点 `:2530`（failed）`/ :2510`（completed）。迁移成功后才 `voidTicketsWithAudit`、`AppendEvent`、
`clearApproverState`（`:2539`，此处会落 `deny_guidance_dropped`，见 seq 661 来源）。
崩溃语义：崩在两步之间留下「waiting_review 但缺一条 completed/failed」，仍可裁决；反过来
「卡 running 却已有终态事件」在本路径被「先迁移」的顺序设计排除。本路径**不可能**产出「事件已落库、
状态没动」的形态——除非后续有别的东西把状态从 waiting_review 搬走（本 B92 正是这样）。

`transitToReview`（`:2549`）/`transitToReviewRetry`（`:2569`）：waiting_answer 时两跳
`waiting_answer→running→waiting_review`，running 时直跳；其余状态返回错误。日志里未见
`回迁 waiting_review 失败`、`transitToReviewRetry` 相关告警——两例都是首跳即成功。

### 2.2 `Manager.Stop`（`internal/agentd/manager.go:1166`，HTTP 入口 `server.go:974 handleStop`）—— 事件「协调者主动中止」
顺序：**先追加，后迁移**。`AppendEvent(failed)`（`:1196`）→ `transit(taskID, failed, "stop")`（`:1200`）。
若迁移失败（并发已归档等），**事件已落库而状态未进终态**——这是一条真实的「只落事件不迁状态」路径，
但两个现场都不是它（两例的 failed 事件 fail_reason 都不是「协调者主动中止」）。
注意：failed 是终态，`transit` 到 failed 的迁移发生在追加之后，二者同样不是原子的。

### 2.3 `reconcileExecutorGone`（`internal/agentd/reconcile.go:143`）—— 事件「executor 已不在」
顺序：**先追加，后迁移**。`AppendEvent(failed)`（`:163`）→ `recoverTransit`（`:169`）。
迁移失败（`:170`）时事件已落库、状态停留在原处，日志打 `对账迁移 waiting_review 失败`。
同样是一条真实的「只落事件不迁状态」路径。两个现场都不是它：两例失败事件的 fail_reason 都不是
`executor 事件流已终结…`，且两例在对账入口都看到 `state=waiting_review` 走了「任务无需状态对账，仅清扫残留」
分支（`:152-156`）。

### 2.4 小结
三条路径里只有 2.1 是「迁移失败连事件都不发」的干净路径；2.2/2.3 都是「先落事件再迁移」，迁移失败会
留下「有 failed 事件、状态未终态」的形态。**但 B92 的两个现场全都不是这三条路径的原子性缺口造成的**
——两例都先正确迁到 waiting_review，是被后续 `continue` 搬回 running 的。这回答了计划的核心矛盾：
**seq 660 并非「没有状态迁移就落库」，它落库时迁移发生过。**

---

## 3. seq 660 走的路径（Q2 答案）

见第 1 节日志。路径链为：

1. `finishTurn` 判 `stopReason=cancelled` ≠ `end_turn` → `emitFailed`（`grok/adapter.go:441-453`）。
2. `emitFailed` 先 `a.emit(result{OK:false})` 再 `r.closeEvents()`（`grok/adapter.go:350-355`）。
3. result 事件进 evCh → manager `mediate` 的 `handleEvent` 打 `执行结果事件 ok=false`
   （`manager.go:1377-1384`）→ `handleResult`。
4. `handleResult`：`transitToReview` 成功（`12:33:33.515988` running→waiting_review）→
   `AppendEvent(failed)`（seq 660，`12:33:33.516`）→ `clearApproverState` 落 seq 661
   `deny_guidance_dropped`。
5. evCh 随后关闭 → mediate 循环退出 → `reconcileExecutorGone`：`state=waiting_review`，跳过状态对账，
   仅清扫残留。
6. `12:35:07` 协调者 `continue` → `Manager.Continue`（`manager.go:935`）`waiting_review→running` →
   `ad.Send`（`grok/adapter.go:248-268`）在同一 runstate 上 `CallAsync("session/prompt",…)`，
   **不重建事件通道，也不重启 mediate 循环**（`go m.mediate` 只在 `Send` 返回 `ErrTaskNotRunning` 走
   恢复阶梯的分支里出现，`manager.go:967`；本例 Send 成功，所以不重启）。
7. 续接回合开始跑（render.log 出现模型回话「按裁决解冲突：B80 取 HEAD，B81 取 theirs…」），
   但所有 `emit` 都因 `evClosed=true` 在 `grok/adapter.go:333` 短路静默丢弃（Debug 级日志，本机日志
   级别为 INFO 未落盘，故日志里没有「事件通道已关闭，丢弃事件」行，只有「没有后续执行结果事件」这个负证据）。
8. 无任何事件到达 manager → 任务永久 running。serve 进程存活（port 50007），grok 看门狗
   （`grok/resume.go:244-278`）只探 `proc.Alive()` 不判回合进度，判不了死。2h 后 agentd 级 watchdog
   落 `stalled`（seq 703）——只唤醒、不修复。

---

## 4. 例二旁证 `054ca06f`：同一机制的完整闭环

数据库（seq 585–650 全量）：

| seq | type | payload | created_at (UTC) |
|-----|------|---------|------------------|
| 594 | progress | `{"text":"grok 会话已就绪"}` | 2026-08-14T03:19:12.627667Z |
| 595 | **failed** | `{"fail_reason":"回合异常终止: ACP 错误 -32603: Internal error",…}` | 2026-08-14T03:32:05.123965Z |
| 644 | **failed** | `{"fail_reason":"协调者主动中止（handoff stop）",…}` | 2026-08-14T04:29:06.817171Z |

agentd 日志（+08:00）：

```
11:32:05.119522 INFO  msg=任务状态迁移 task=054ca06f… from=running to=waiting_review reason=result
11:33:19.323754 INFO  msg=任务状态迁移 task=054ca06f… from=waiting_review to=running reason=continue  ← 首条 failed 后 74s 就被 continue 回迁
11:43:29.071133 ERROR msg="grok 任务失败" task=054ca06f… reason="回合异常终止: ACP 错误 -32603: Internal error"
        ← 第二回合（续接回合）又失败，emitFailed 打 ERROR；但后面【没有任何】"执行结果事件"日志，
          事件在 emit 层被 evClosed 静默丢弃（负证据）
12:29:06.817983 INFO  msg=任务状态迁移 task=054ca06f… from=running to=failed reason=stop  ← 协调者 stop，进终态
```

**这解释并纠正了计划对例二的推论。** 计划说「若首条 failed 已把任务迁到终态，stop 本应 409 被拒——
没被拒说明那 57 分钟里任务还在 running/waiting_answer」。事实是：首条 failed 迁的是 **waiting_review**
（非终态），此后又被 continue 搬回 running；stop 从 running 迁移到 failed 是合法迁移（
`proto.go:226` `running→failed`），所以 stop 被接受与「failed 事件正确迁移」完全自洽。**不需要
「追加事件而不迁移」的缺口来解释例二**——而第二条 `failed`（seq 644，stop）恰恰证明 11:43:29 那次
续接回合的失败事件被吞了：那条 -32603 只留了日志、没落事件，正是 evCh 已关的直接后果。

---

## 5. `stopReason=cancelled` 与 grok 的适配器差异（Q3 答案）

- `stopReason` 是 ACP 协议的回合终局字段，全仓 grep 只出现在 `internal/executor/grok/`（`adapter.go:8,254,437-453`，
  以及三处测试）。opencode（SSE idle 事件）、claudecode（stream 解析）都没有这个概念。
  `stopReason=cancelled` 只可能来自 grok 的 `finishTurn`，是 grok 独有。
- **它不绕过 `handleResult`**：`finishTurn` → `emitFailed` → 仍是 `Type:"result", Result:{OK:false}` 的
  AdapterEvent，经同一 evCh → `handleEvent` → `handleResult`，与「正常失败」同一条上报路径，没有第二条
  上报通道。grok 与 opencode 的差异不在「上报路径」，而在 **`emitFailed` 会 `closeEvents()` 关掉整条
  事件通道**——这是 opencode/claudecode 没有的（它们只在订阅/流退出时关通道，见 `opencode/adapter.go:747-768`
  subscribeLoop 的 defer、`claudecode/adapter.go:444`）。
- **为什么 opencode 不受影响（跨任务铁证）**：`failed` 事件之后继续产出事件的 opencode 任务一批：
  - `2abde49c`（opencode，b81）：seq 249 failed → 255+ progress/permission/deny_guidance_relayed/
    permission_reuse → **seq 278 completed、279 archived**。failed 后被 continue 并正常完成。
  - `8bf4eee4`（opencode，b85）：seq 69 failed → 73+ progress/permission/deny_guidance_relayed →
    **93 completed、95 archived**。
  - `6864ab0e`（opencode，b14-b15）：seq 696 failed → 697+ 大量 progress/permission/question →
    **749 completed、750 deny_guidance_dropped**（当前 waiting_review，等待 done）。
  grok 的任务则一律在 failed 后哑火：`398259b7`（660 后只剩 661/699/703）、`054ca06f`（595 后 57 分钟
  零事件，直到 644 stop）、`993e879d`（b12，2026-08-13：482 failed(-32603) → 502 resume--force 播报 →
  571 stalled → 572 stop）。**三条 grok 全哑火，三条 opencode 全复活，判据不是巧合。**

---

## 6. 数据库现状（Q4 答案）

只读方式：`sqlite3 ~/.handoff/handoff.db ".backup /tmp/b92-handoff.db"` 快照后对副本
`sqlite3 -readonly /tmp/b92-handoff.db` 查询；未对原库写入任何东西。

- tasks 行：见第 1 节。`398259b7` state=running、updated_at=continue 时刻、executor=grok。
- events seq 640–661 与 699/703：见第 1 节表。seq 660/661 之外的连续事件流证明：
  `handleResult`（660→661）与 `continue`（回迁 running）在 DB 里都有对应产物。
- 结论：**DB 里任务状态是 `running` 且事件停在 `failed`(660)/`deny_guidance_dropped`(661)/`stalled`(703)——
  这是「continue 把状态从 waiting_review 搬回 running 后，续接回合事件被关闭的通道吞掉」的结果**，
  不是任何一条 `failed` 产生路径单独造成的「事件先落、状态未动」。

---

## 7. grok 凭据问题（Q5 答案）：独立存在，但不是 B92 卡死机制的一部分

- **卡死机制不依赖凭据**：两例的失败原因分别是 `stopReason=cancelled`（协调者拒绝权限引发回合取消）
  与 ACP `-32603`，都与鉴权无关；而「回合失败 → emitFailed 关通道 → continue 后事件被吞」的链条对
  任何失败原因都成立。凭据中途失效若发生，走的收尾路径与正常失败是**同一条**：ACP 调用报错 →
  `finishTurn` 的 `res.Err` 分支 → `emitFailed("回合异常终止: …")`（`adapter.go:442-444`）；连接断开 →
  `onClosed` → `emitFailed("ACP 连接断开…")`（`adapter.go:524-551`）。两者都先 `emit` 再 `closeEvents`，
  随后都会踩进同一个「continue 复用关闭通道」的坑。
- **凭据确实是坏的，但那是另一件事**：`2026-08-15 01:12` 派发 grok 任务 `839e2a32` 时
  `dispatch 失败 … cause="grok 未登录或凭据已失效，请在本机执行 grok login 后重试: ACP 错误 -32000:
  Authentication required"`。现场核对：`~/.handoff/tasks/839e2a32-…/grokhome/auth.json` 是软链，指向
  `~/.grok/auth.json`，**而该权威文件不存在**（只有 `.lock`）→ 悬空软链 → `-32000`。这是 B92 之外
  的真实缺陷（auth 链路断了），但它发生在 01:12，晚于两例卡死（03:32/04:33）数小时。
- **标本任务 04:33 当时的凭据状态查不回来**：其 `grokhome/auth.json` 现为普通文件、`expires_at`
  `2026-08-14T22:19:26Z`、文件 mtime `08-15 00:19`（+08），而事件发生在其前约 8 小时——期间 grok CLI
  可能刷新过令牌，文件早已不是当时的快照。`serve.log` 里 04:29 之后的工具报错是 `tool_output_error`，
  没有鉴权帧；05:30 起出现 `Settings fetch failed` 与 `BatchSpanProcessor.ExportError: network error`
  （每 5 分钟一次，持续到 05:50）——疑似网络/遥测问题，无法据此断定 04:33 的鉴权状态。**这一条明确
  标为「查不出来」**：B92 卡死机制与凭据无关（两例失败原因都不是鉴权），凭据链路坏是另一个需要单独
  修的缺陷。
- 配置纪律：引用 `~/.handoff/config.yaml` 仅用字段名（`token`/`targets`/`executor.default`/`env.grok`
  等），未输出任何值；grok serve 的 `wsURL` 与 `server-key` 存于 `~/.handoff/tasks/<id>/proc.json`，
  含 secret，本报告一律不引用其值。

---

## 8. 兜底扫描现状（Q6 答案）

- `scanStalled`（`watchdog.go:117-172`）只对 `running`/`waiting_answer` 判「最新事件距今 > stallTimeout
  即发 stalled」；**它不检查**「最新事件是终态事件（failed/completed）而任务状态却不是终态/待审核」这类
  不变量违反。确认：`watchdog.go:125` 的过滤、`:129` 的 `LatestEvent`、`:150` 的「只发一次」都只看时间
  与最新事件类型是否为 stalled，没有状态×事件的交叉校验。`RecoverOnStartup`（`:271-305`）同理，只按
  状态探活，不核对「事件与状态的一致性」。
- 本 B92 里 stalled（seq 703）确实在 2h 后触发并唤醒了协调者——所以「兜底」弱到只负责叫人，不负责
  修复。协调者按 stall 排查时 `handoff resume` 报 `redelivered:0 executor_gone:false reconciled:false`
  （grok 不实现 `reconciler` 接口，`reconcileInto` 在 `manager.go:2325-2331` 直接给「adapter 不支持对账」），
  `resume --force` 才能强制收口 waiting_review。这就是本场景今天的全部逃生通道，且不自动。

### 若加不变量对账扫描，放哪一层
- **watchdog（`scanStalled` 同层、新增一步交叉校验）**：周期扫描时对每个非终态任务
  （running/waiting_answer）检查 `LatestEvent`：若最新事件是 `failed`/`completed` 而状态不是
  `waiting_review`/终态，即状态与事件失配 → 告警（甚至按事件驱动迁移）。成本：周期扫描多一次读；
  收益：**无死角**，无论事件从哪条路径、以什么顺序到达，最终不一致都会被兜住。局限：它只能「事后
  发现+修复/提示」，修复需要决定「以事件为准迁 waiting_review」还是「以状态为准」，需要小心已归档任务的
  竞态。
- **resume / 启动恢复层**：只覆盖「有人主动执行 resume 或 agentd 重启」的时机，覆盖不到长期无人值守的
  running 哑火（本 B92 正是无人值守场景——事件在 04:33、直到 07:25 才被 stalled 唤醒）。建议不放在
  这一层，或只作为次要出口。
- 更贴根因的做法是**让 continue 不再复用关闭的通道**（见第 9 节方案 A），把不一致消灭在产生源头；
  对账扫描作为第二道保险仍然值得加，因为它还能兜住 2.2/2.3「先落事件后迁移、迁移失败」的真实缺口。

---

## 9. 修复方向的取舍（只描述，不实现）

### 方案 A：grok 侧修复——continue 别往关闭的通道上开新回合（治本）
- 具体形态之一：`emitFailed` 不再永久 `closeEvents()`，或 `Send` 在发现 `evClosed` 时重建
  `runState.evCh` 并让 manager 重启该任务的 mediate 循环（需要让 manager 感知通道换代）。
- 能覆盖：本 B92 的完整链条——任一失败原因后 continue，续接回合的事件都能送达 `handleResult`，
  状态再次正确迁移。opencode 已经天然具备等价行为（通道不因回合结束而关），此方案是让 grok 对齐它。
- 代价：`evClosed`/`closeEvents` 的「一次性终结去重」设计（`adapter.go:346-349` 注释：断开处置、看门狗
  判死、回合异常三路竞争去重）要重新想清楚；重建通道会引入「旧 close 与新 emit」的竞态，需要类似
  opencode `closeOnce` / `dropIf`（`grok/adapter.go:313-324`）的保护；测试面广（现有 `watchdog_internal_test`、
  `onclosed_drop_internal_test` 等都要过）。

### 方案 B：manager 侧加不变量对账扫描（兜底）
- 形态：watchdog 每轮对 running/waiting_answer 读一次 `LatestEvent`，若为 `failed`/`completed` 且状态
  不符，发一条可操作的告警（或直接按事件迁到 waiting_review 让协调者裁决）。
- 能覆盖：B92 以及一切「事件与状态失配」的形态，包括 2.2/2.3「先落事件、迁移失败」的真实缺口；与
  失败原因、adapter 实现无关。
- 不能覆盖：不消灭「续接回合事件被吞」本身——被吞的事件永远到不了库，扫描无从知道续接回合其实跑过。
  （对 B92 而言，事件 660 在库，扫描能看到「latest=failed 但 state=running」；但对「续接回合正常完成后
  结果被吞」的更坏情形，库里只有 stalled，扫描只能判「长时间无事件」而抓不到「任务其实完成了」。）
  所以 B 是保险丝不是根治。

### 取舍
- A 治本、B 兜底，互补。若只做 B，B92 仍会反复出现「任务完成后被吞、只有 2h stalled 唤醒」的体验；
  若只做 A，2.2/2.3 的「先落事件后迁移」原子缺口仍在（但那两条路径在迁移失败时会打 Error 日志并返回
  错误，是「已发现」的失败，危害远低于 B92 的静默吞事件）。建议 A 为主、B 为辅。
- 顺带可修的真实缺陷：`~/.grok/auth.json` 缺失导致 grok 新任务起不来（01:12 dispatch 失败），这是与
  B92 独立的凭据链路问题。

---

## 10. 未解之处

- **标本续接回合为什么在 04:35 后连 render.log 都不再增长**：续接回合开头模型确实在干活（render.log
  尾部有「按裁决解冲突：B80 取 HEAD，B81 取 theirs…」及后续 python3 片段），随后 04:35 停止产出，
  serve 侧 05:30 起持续 `Settings fetch failed / ExportError: network error`。是模型/会话被卡住还是
  遥测网络问题，查不出来：serve 是黑盒，serve.log 只有这五类错误，无回合级诊断帧。
- **例二 `-32603 Internal error` 在 grok serve 侧的根因**：查不出来，serve.log 无对应记录（该任务
  的 serve 日志未保留或已随 stop 清理），ACP 错误码是 grok 服务端内部错误，agentd 侧只有转述。
- **标本 04:33 时刻的精确凭据状态**：`grokhome/auth.json` 距今已被刷新过（mtime 08-15 00:19），不是
  当时快照；`~/.grok/auth.json` 权威文件现缺失。无法重构 04:33 时令牌是否有效。已确认的是两例失败
  原因与鉴权无关。
- **例二 11:33:19 的 continue 之前（11:32:05→11:33:19 之间 74 秒）协调者做了什么、为什么又 continue**：
  只有 agentd 日志的 continue 行，协调者动作侧无记录，查不出来。
