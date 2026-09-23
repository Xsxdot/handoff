# B380 拆解稿：关单镜像断流修复（ticket_answered 发布 + 终态关单）

状态：**已拍板**（2026-09-23；出稿轮裁决由协调者逐项裁定，回填见 §8；handoff 派发形态——executor 出稿，本地协调者拍板，裁决回填 §8）
卡：B380
标题：card wait 建连快照把 5 天前已答工单当未决：卡流缺关单镜像（ticket_answered）
定级：**L2 单子系统**（spec/contract 定级；本稿 §1 按 `codegraph/best.json` 实测触及域，见 §2.4 澄清 1）
路由：contract → breakdown →（**代码单轮闭合**，无实现子卡；仅一张文档收口子卡）→ review → acceptance → finish
有效基线：`cards/B233.1-charter-7`（本卡合并目标）；当前工作分支 `cards/B380-charter-2`（不切换、不越过）
上游 spec：`docs/superpowers/specs/b380.md` —— **不在本工作树 / 本分支**；可达于 `origin/cards/B380-charter-1`（草稿 `68035fa3`、批准回写 `117bbd5a`），头部「上游状态：**已批准**」（2026-09-23 四点裁定）。详见 §2.1、§2.4 澄清 4、P3
冻结 contract：`docs/superpowers/specs/b380-contract.md`（提交 `2a5378ab`；头部有「上游 spec…已批准」与基线，**无显式「冻结状态」行**，见 §2.1 / P3）
图依据：`codegraph/best.json`（`parent` 为空的顶层领域即子系统清单，类型取 `type`）；本分支无 `codegraph/diffs/<分支>.json`（合法：本卡只改既有函数体、未引入新符号）
本稿台账：`docs/superpowers/ledgers/2026-09-23-b380-breakdown-ledger.md`
角色边界：本文是**提案**；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。扇出与拍板归协调者。

---

## 0. 待拍板岔口清单（集中，拍板者按此裁决）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **P1** | 本卡子卡形态：代码是否还有可扇出的实现子卡 | **甲（推荐）：单轮闭合**——contract 的 Ticket 0 已落**全部运行时代码**（三发布点接线 + `OpenTickets` 终态关单）与 6 支金样本测试；本稿复核 `go build ./...` 与三包子集测试**本轮新鲜绿**（§3.0、台账 13/14），无新增代码可派。**乙**：另立一张实现复核子卡，把 build + 6 测试作为验收判据重跑（多一次往返，产出与 Ticket 0 逐字重复）。 | **甲**。修复面只有「3 处一行 publish + 1 个 case 分支 + 注释」，contract 冻结时已一并落地并跑过红→绿；再开实现卡是复跑已绿的判据。 |
| **P2** | 文档收口的归属（唯一剩余活） | **甲（推荐）：独立小子卡 S1**（有界文件集 `skills/handoff/SKILL.md` + `docs/roadmap.md`，可选 `README.md`），可被 plan 排期、验收可 grep。**乙**：并入 implement 节点收尾，不单独建卡（更省，但文档面与「代码关闭」的验收混在一起、无独立文件边界）。 | **甲**。契约 §8 已把「文档同步」单列为移交项；给它一个有界文件集的最小卡更可追溯。 |
| **P3** | spec 落点与 contract 状态位缺失如何处置 | 事实（§2.1）：`b380.md` 不在本工作树/本分支/cards/B233.1-charter-7，只在 `origin/cards/B380-charter-1`；contract 头部无「冻结状态」行（对比 b370/b374）。**甲（推荐）**：本稿引用 `origin/cards/B380-charter-1 @117bbd5a` + 路径；回写 `b380-contract.md` 末尾一行修订记录（记下状态位缺失与 spec 可及性事实）；**合并时是否把 spec 并进合并目标由协调者定**（否则合并后上链引用悬空）。**乙**：把 spec 复制进本分支（引入本卡不该承担的文档搬运）。**丙**：退回 contract 重冻补状态位（重开冻结节点）。 | **甲**，本条同时把「状态位失真」显性化（纪律：引用状态位失真的上游 = 把流程状态托付给会话记忆）。 |
| **P4** | 是否机内补一条「publish→`/ws/events`→mirror→`OpenTickets`」端到端回归 | **甲（推荐）：不补**——contract C-3 明示**无新字段、词表不变**，`ticket_answered` 本就在订阅重放窗口被镜像过（spec §1 `c99a527d` 两条即为反证）；现测试已逐包锁住发布点与投影（`b380_publish_test.go`、`taskstate_test.go`、`client_test.go`），端到端跨机行为已列真机清单（§6 #2）。**乙**：补一条机内集成回归（真 store + 真 hub + `/ws/events` httptest + fake mirror source → `OpenTickets`），穿过 store→hub→WS 的手写序列化边界。 | **甲**，但此判断依赖「C-3 无新字段」；若拍板者要更强链路背书，乙的成本是新增一个 agentd/ledgermirror 集成测试文件，不触碰生产代码。 |

> 若协调者裁决改变契约冻结面（如 P3 选丙、P4 选乙需改验收判据），应先回写 `b380-contract.md` 修订记录再扇出；本稿不自行吸收。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型逐字取自 `codegraph/best.json` 的 `domains`（`parent` 缺省 = 顶层子系统，`type` 即逻辑/边界标注）。容器→域映射实读（`codegraph --repo . sym`，台账 11）。

| 子系统（图 id） | 图类型 | 本卡角色 | 有界文件集 / 暴露面 | 派卡资格四条核验 |
| --- | --- | --- | --- | --- |
| `d_orchestration` | 逻辑型 | **主实现面**：三发布点接线（C-1a/b/c 中的 a/b + approval 面） | 已落：`internal/orchestration/facade.go`（`Manager.AnswerTicket` 补 `m.hub.Publish`）、`internal/orchestration/manager.go`（`Manager.approvePermission` 同款）、`internal/approval/client.go`（`Client.consult` 经 `Hooks.Hub.Publish`）；测试 `internal/orchestration/b380_publish_test.go`、`internal/approval/client_test.go`。暴露面**零变化**（只改函数体，无新导出符号/字段）。 | ①一条路径规则圈得出（前述文件）；②导出面零变化；③无新跨域边；④真实 `*Hub` 订阅者收事件的机内闭环（两个编排测试）+ `testHub` 假替身（approval 面）。 |
| `d_ledger` | 逻辑型 | **主实现面**：镜像投影层终态关单（C-2） | 已落：`internal/ledger/taskstate.go`（`Store.OpenTickets` 增 `completed/failed/archived` 清单）；测试 `internal/ledger/taskstate_test.go`。暴露面零变化（`OpenTickets`/`OpenTicketCounts` 签名不变）。 | ①单文件规则；②签名已冻结；③无新边；④真 SQLite 夹具机内闭环（3 子例 + seq 颠倒反例）。 |
| `d_protocol` | 逻辑型 | 相邻：仅事件注记（C-3） | 已落：`internal/proto/proto.go`（`EventTypeTicketAnswered` 注记改述为「会 Publish，但客户端不可交付」）。**词表/payload/wire 一字不改**。 | ①单文件注释；②无导出面变化；③无新边；④无行为可测（注释）。 |
| `d_cli` | 逻辑型 | **消费面（零改动）**：`cmd/card_wait.go` 快照欠单 | 只读：`cmd/card_wait.go`（`encodeCardWaitSnapshot` → `st.OpenTickets()`，:357）。本卡不改。 | ①不派卡；②既有 stdout 契约不变；③→`d_ledger` 既有 entry；④行为由 `d_ledger` 投影闭环 + 真机 #1。 |
| `d_gateway` | **边界型**（对面是浏览器/CLI 的 HTTP 与 WS 现实） | **消费面 + 发射面（零改动）**：`/ws/events` 实时流、卡详情计数 | 只读：`internal/agentd/handlers.go`（`Server.handleEvents`，:1480 起「先订阅后补发」）、`internal/agentd/ledgerapi.go`（`Server.handleCardsList` → `OpenTicketCounts`）。本卡不改。 | ①不派卡；②既有路由/帧契约不变；③无新边；④机内 httptest 面既有，本卡沿用；真实浏览器/CLI 归真机。 |
| `d_transport` / `d_transport_channel` | **边界型**（对面是跨机 relay/直连 agentd 的现实） | **链路（零改动）**：镜像订阅走 `StreamEventsOnce`，**不**经 `WaitDeliveryPolicy` | 只读：`internal/client/delivery.go#WaitDeliveryPolicy`（冻结 false）、`internal/client/client.go#Client.StreamEventsOnce`（:1647，传输交付全部帧）、`internal/ledgermirror/mirror.go`（`Mirror.subscribe` 用 `m.opt.Source`/`DefaultSource`）。本卡不改。 | ①不派卡；②`WaitDeliveryPolicy` 语义冻结；③无新边；④`streamOnce` 不过滤是机内可读事实（台账 9）；跨机端到端归真机 #2/#3。 |

**不列为本卡实现域（零改动）**：`d_sessions`、`d_workspace`、`d_policy`、`d_scheduling`、`d_execution`、`d_keystone`、`d_collab`、`d_maintenance`、`d_web`。

### 1.1 竖切债核对（架构法第三条）

- `internal/orchestration`（`Manager.approvePermission`/`Hub.Publish` 所在）与 `internal/ledger` 均为既有包，本卡**只改既有函数体**，不新增源文件、不扩前缀族。
- `internal/agentd`（图内 57 文件平铺大包）与 `cmd`（57 文件、`card` 前缀族 10 文件）在 `codegraph check` 里被标「须回答还能否圈出有界文件集」（waren，台账 15）——但**本卡在这两个包零改动**（消费面只读），不触发升格。**能圈出有界文件集，不插竖切还债卡。**
- 实现若需改上表「只读」面之外的生产文件，必须退回协调者重核边界，不得以「同目录」放宽。

### 1.2 图覆盖债

- 未入图（`codegraph sym` 判「不在图中」，与 contract §7 一致，台账 12）：`Store.OpenTickets`（本卡投影主缝）、`Manager.AnswerTicket`（发布点之一）、`EventTypeTicketAnswered`、`cmd/card_wait.go#encodeCardWaitSnapshot`。本稿对这些**只用普通路径、不带 `#Symbol` 锚**。
- 已入图可用锚见各节 `file#Symbol`；本稿收口前亲跑 `codegraph resolve --doc docs/superpowers/specs/b380-breakdown.md`（结果落台账 18）。
- 本稿不新增 `codegraph/diffs/<branch>.json`：breakdown 不改生产符号。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- **spec**：`git show 117bbd5a:docs/superpowers/specs/b380.md` 实读头部「上游状态：**已批准**（审批者 2026-09-23 批准，四点裁定：①关单进实时流但不叫醒协调者；②终态任务清掉未决单；③水位逐 seq 对账本期不做；④L2，词表和 wire 不动…）」——**逐字核对通过**。但该文件**不在本工作树/本分支/合并目标**（`git merge-base --is-ancestor 117bbd5a HEAD` → exit 1，仅 `origin/cards/B380-charter-1` 含之）。
- **contract**：`b380-contract.md:1-7` 有「上游 spec…已批准」「级别」「基线」「图」「本节点产出」，**无「冻结状态」行**。冻结事实由提交 `2a5378ab`（message 含「契约冻结」）承载；状态位缺失属文档债，回写修订记录（P3）。
- **图门禁**（本轮新鲜）：`codegraph --repo . check` → exit 0；`codegraph --repo . validate` → exit 1，`issues` 恰为 contract §6 所记两条他分支视图问题（`cards-B272-charter`、`cards-B374-charter`），**非本卡引入**。

### 2.2 契约 §1 冻结条目逐条对照（现状代码位置）

| 契约条目 | 现状核对 | 越界结论 |
| --- | --- | --- |
| C-1a `Manager.AnswerTicket` 发布 | `internal/orchestration/facade.go`：`AppendEvent` 成功后 `m.hub.Publish(evt)`；append 失败只 Warn、不发布（幂等 `applied=false` 不 append 不发布） | **不越界**，逐字在位 |
| C-1b `Manager.approvePermission` 发布 | `internal/orchestration/manager.go`：同款 `if err != nil { Warn } else { m.hub.Publish(evt) }` | **不越界** |
| C-1c `Client.consult` 经 `Hooks.Hub` 发布 | `internal/approval/client.go`：`else if c.hooks.Hub != nil { c.hooks.Hub.Publish(evt) }`；`Hooks` 无新字段（复用既有 nil 安全事件缝） | **不越界**；spec→落地偏差（复用 vs 新增函数）已在 contract §1 C-1c 记录 |
| C-2 `OpenTickets` 终态关单 | `internal/ledger/taskstate.go:188`：`case evTicketsVoided, "completed", "failed", "archived":` 清该任务全部 key | **不越界**；`completed` 与 `mirrorTaskTerminal`（只 archived/failed）集合不同且注释写明「不得合并」 |
| C-3 明示冻结 | `WaitDeliveryPolicy`（`delivery.go:23`）false 不变；`MirrorWatermark`（`mirror.go:104`）`MAX(source_seq)` 不变；镜像信封（`mirror.go:62`）不变；`tickets_voided` 无 Publish（`ticketvoid.go:9` + grep）；payload schema 不变（`contracts.go:115` / `approval/client.go:551`） | **不越界**；`proto.go` 注记已随骨架修订 |

### 2.3 退回 contract（不许边拆边加）

**无。** 逐条核对 C-1a/b/c、C-2、C-3 后，未发现「spec 承诺了行为、冻结物里没有载体」的新接缝；本卡所需的行为载体（三个写入点、`OpenTickets` 重放、镜像链、消费面）全部已有冻结出处。P4 若选乙只是加测试，不是新接缝。

### 2.4 边界澄清（不退回，已回写 `b380-contract.md` 末尾修订记录一行）

1. **域归属按图修正**：`internal/approval` 在 `best.json` 归 `d_orchestration`（`n_approval_Client_consult → d_orchestration`）。故 contract 所称「L2 单子系统（orchestration + approval 发布点 + ledger 投影面）」在图上实为**两个逻辑顶层域**（`d_orchestration`、`d_ledger`）+ 一处 `d_protocol` 注释改动。**不改 spec/contract 定级**（定级是上游拍板），仅记图事实。
2. **消费面域归属补记**：contract 把 `cmd/card_wait.go` 与 `internal/agentd/ledgerapi.go` 写作消费方但未标域——图上分别是 `d_cli`（`encodeCardWaitSnapshot` 在图外）与 `d_gateway`（`Server.handleCardsList`）。两者本卡零改动。
3. **C-1 必要性的链路证据**：本地/远端镜像走 `StreamEventsOnce`/`ForwardedStreamEventsOnce`（`internal/client/client.go#Client.StreamEventsOnce`、`capabilities.go:37`），该路径**不过** `WaitDeliveryPolicy`（`client.go:1669` 注释：「策略在应用谓词，传输 streamOnce 必须仍见到该帧」），`internal/ledgermirror/mirror.go` 的 `mirrorSkip` 也**不含** `ticket_answered`。→ 发布即进镜像，C-1 与 C-2 两层修复不冲突。
4. **上游 spec 可及性 + contract 状态位**：见 §2.1 与 P3；回写契约末尾修订记录（不只活在稿内）。

---

## 3. 子卡清单与依赖 DAG

### 3.0 DAG

```text
[Ticket 0 已落，不重开]  contract @2a5378ab：
    三发布点（facade.go / manager.go / client.go）+ OpenTickets 终态关单 + 6 支金样本测试
    代码实现面单轮闭合（本稿复核 build + 3 包子集测试本轮新鲜绿）
         │
         └──> S1 文档收口（skills/handoff/SKILL.md + docs/roadmap.md[+README.md]）  ← 唯一扇出面
                  │
                  └──> review / acceptance / finish
真机验收（跨机镜像端到端）────────────────────────────> 协调者执行（§6）
```

Ticket 0 **不是**可独立派发的子卡——它是已完成的 contract 冻结物，本稿只复核、不重开（P1=甲）。除 S1 外无功能子卡。

### 3.1 S1（文档面，图外）：事件交付性文档同步与后继卡落账

**①契约引用**：contract C-3、§8（文档同步移交项）、§5 显式「不做」；spec §5（`proto.go` 注记对齐 `delivery.go`）、§7（Out of Scope 落 `docs/roadmap.md`）；本稿 §2.4。

**②意图与为什么**：C-1 后 `ticket_answered` **会 Publish**（进账本镜像实时流关单），但仍**不可交付**（不唤醒 wait）——两个性质正交。现状文档把七类事件并列称「只入库」，对 `ticket_answered` 已不准确；spec §7 的「watermark 逐 seq 对账」弃选项落 `docs/roadmap.md` 的动作也尚未落。本卡只做文档同步与后继卡落账，**不改任何 Go 生产代码**。

**③验收（行为化）**：

- `skills/handoff/SKILL.md`：可 grep 断言「`ticket_answered` 不再被归入『只入库』」——即该文件里描述 `ticket_answered` 的措辞明确「会进事件流 / 会入库供账本镜像关单，但**不唤醒** `wait`」；反面：`grep -n '只入库' skills/handoff/SKILL.md` 命中的句子里不再把 `ticket_answered` 列为「只入库」。
- `README.md`：`README.md:432-434` 的「filters the same seven audit types at the application consumer」语义（交付过滤）仍准确；如保留「audit」一词，须确保不与「只入库」混读（可选：加半句澄清「published for the ledger mirror, not delivered to wait」）。**判据**：README 不出现「`ticket_answered` 只入库」的等价表述。
- `docs/roadmap.md`：新增一条可检索条目「镜像 watermark 逐 seq 连续性对账（补洞重拉）——来源 B380 spec §3.3 / contract §5；本期不做、后续卡」，`grep -n '逐 seq\|watermark.*对账' docs/roadmap.md` 命中。
- 全量回归（不改代码的前提下仍须成立）：`go build ./...` exit 0；`go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1`、`go test ./internal/orchestration/ -run TestB380 -count=1`、`go test ./internal/approval/ -run TestB380 -count=1` 均 `ok`。
- **缺陷族见 §5**；S1 自身为纯文档面，§5 中「生命周期/静默/跨平台/门禁/序列化/枚举/安全/webview」对 S1 的结论均为「无，因为不触运行时代码」；「假红/假绿」的特殊点在 §5.4 末。

**④入口指针与有界文件集**：`skills/handoff/SKILL.md`（:95-96 七类并列的「只入库」句）、`docs/roadmap.md`（新增一行）、`README.md`（:432-434，可选微调）。符号锚：无（纯文档）。**不得**借此卡改 `internal/**` 生产代码或 `internal/client/delivery.go`。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的跨子系统可观察行为（spec §4 用户故事 1–3 + §1 影响面）。五格齐全，归属存在；「Ticket 0」= contract 已冻结实现 + 本稿复核，非新子卡。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| 已完成任务（如 B369）的已答工单（存量幽灵） | `card_events` 中已有的 `task_mirrored` 终态事件（`task_type ∈ completed/failed/archived`） | `Store.OpenTickets` 重放（`internal/ledger/taskstate.go`）→ `cmd/card_wait.go` 快照 / `Server.handleCardsList` 计数 | 已答/终态任务的工单不再出现在 `card wait` 首行 actionable；`open_tickets` 计数一致 | Ticket 0（d_ledger C-2）+ 真机 #1 |
| 运行中任务被人工 `reply` | `Store.AppendEvent(ticket_answered)` 成功后 `Hub.Publish`（`internal/orchestration/facade.go`，`Manager.AnswerTicket`） | `/ws/events` 实时流（`Server.handleEvents`）→ `ledgermirror` 落 `task_mirrored` → `Store.OpenTickets` 关单 | 数秒内卡流出现 `task_mirrored(ticket_answered)`；`card wait` 不再把它当未决 | Ticket 0（d_orchestration C-1a + d_ledger）+ 真机 #2 |
| 运行中任务被 Manager 中介审批者批准 | `manager.go` `AppendEvent` 后 `m.hub.Publish`（source=`approver`/`reuse` 两路同函数） | 同上一行 | 同上（`answer=allow`） | Ticket 0（C-1b）+ 真机 #3 |
| 运行中任务被 approval 面审批者批准（OpenCode 面） | `internal/approval/client.go` `Hooks.Hub.Publish`（生产装配 `Hub: m.hub`） | 同上一行 | 同上 | Ticket 0（C-1c）+ 真机 #3 |
| 卡详情页查询 | `Store.OpenTicketCounts`（同尺派生自 `OpenTickets`） | `Server.handleCardsList` 徽标 | `open_tickets` 与 `card wait` 快照一致，不把已归档任务算未决 | Ticket 0（d_ledger） |
| 协调者挂 `wait` / `handoff wait` | `WaitDeliveryPolicy`（`internal/client/delivery.go`，冻结 false） | `client.waitOnce` / `FollowEvents` | `ticket_answered` **不唤醒** wait（但它已进 hub 实时流供镜像） | Ticket 0（C-3，零改动） |
| 用户/协调者读事件表 | `skills/handoff/SKILL.md` / `README.md` / `proto.go` 注记 | 人 | 文档表述与 `delivery.go` 现状一致（会进流、不唤醒） | S1 |

**闭环结论**：每条 spec 承诺行为五格齐、有归属；无「只活在接口、测试或无人认领格子里的承诺」。`source_seq`/内部方法不单独造闭环。

---

## 5. 缺陷族对抗审查（逐族正面回答）

覆盖面 = Ticket 0 的 B380 改动（三个发布点 + `OpenTickets` 终态关单）与 S1 文档面。

**1. 生命周期 / 状态机中断**
- 三处发布都在**同进程、`AppendEvent` 成功之后**同步调用 `Hub.Publish`（无返回值、持锁扇出、慢订阅者 `select-default` 丢弃并 Warn，`internal/orchestration/hub.go#Hub.Publish`）；不创建 goroutine/进程/临时目录，宿主中途重启只丢「本次未发布的关单镜像」，无孤儿资源。
- **兜底链**：即便某次发布丢失（append 失败或慢订阅者丢弃），任务终态镜像一到，C-2 即清该任务全部未决单（`VoidTicketsWithAudit` 已在终态/对账收口，`internal/orchestration/ticketvoid.go#VoidTicketsWithAudit`），无永久孤儿工单。
- **有残余风险（真机观察）**：长任务**中途**多轮工单若发布丢失，且任务尚未终态，卡流会暂时残留未决单（C-2 只在终态收口）；镜像断连期水位跳缺口（spec/contract 明示本期不根治）下同样如此。→ 真机 #5，不写成「已根除」。

**2. 静默失败 / 误导报错**
- 传播契约：`AppendEvent` 失败 → `Warn`（带 task/ticket/cause）且**不发布**，但 `AnswerTicket` 仍返回 `applied=true`——因为**应答本身已持久化成功**（`AnswerTicketApplied`），缺的只是审计/关单事件；这与「报成功但没做」不同（做的是应答，未做的是旁路事件），且终态有 C-2 兜底。发布成功路径无错误可处理（`Hub.Publish` 无返回值）。
- 可行动性：`Warn` 日志是关单丢链路的排查入口；用户可见面**无**专门提示——这是既有设计（发布不阻塞主流程），残余观察归真机 #5。
- 存在「报成功但没做」的窗口吗：**有但受限**——hub 满缓冲丢弃时无用户可见信号（只有 Warn）。**无，因为** `Hub.Publish` 语义即「尽力扇出、持久真源在 store」；关单事件的权威副本在 store（发布仅服务镜像），且终态 C-2 收口。

**3. 跨平台假设**
- 改动面为 Go map/JSON/SQLite 与进程内 hub，不依赖路径、进程组、权限模型、webview。**无，因为**不引入平台相关调用。
- 边界风险：镜像链路跨机（relay/直连 agentd、`/ws/events`），真实网络行为**未验证，需真机**（#2/#3）。

**4. 假红 / 假绿测试**
- 冻结的 6 支测试确实锁**调用方可观察行为**：编排侧用真实 `*Hub` 订阅者收 `ticket_answered` 并断言 payload 逐字（`b380_publish_test.go`）；投影侧断言 `OpenTickets` 输出清零（`taskstate_test.go`，含 `completed/failed/archived` 三子例 + 「终态先于答复不复活」反例）；approval 侧断言恰好一条发布且 `ticket_id`/`answer=allow` 逐字 + nil Hub 不 panic。
- 反面断言在位：幂等重答**不得**再发布（编排测试）；终态后答复不复活（投影测试）；nil Hub 不 panic（approval 测试）。
- 假绿温床核查：**approval 发布点用 `testHub` 假替身**（`internal/approval/client_test.go:26`，`Publish` 只 append），故「生产装配 `Hub: m.hub` 真的把该事件送进同一 hub」**无机内断言**（仅代码可读事实 `internal/orchestration/approval_client.go:37`）；`mirrorSkip`/`WaitDeliveryPolicy` 是否意外挡住该事件也**无跨包回归**。→ 真机 #4，并在 P4 给拍板者「是否补集成回归」的选择。
- 负载/并发：hub 扇出持锁、测试不在高并发下跑；真实慢订阅者丢弃行为**未验证，需真机**（#5）。
- 测试锁的是需求行为（hub 收到事件、投影关单），换实现（如改用别的事件缝）只要行为不变仍应绿——**是**，未绑死私有写法。

**5. 门禁绕过**
- 新增写路径/执行路径？**无独立于既有应答门的新门**：三发布点都在既有 `AnswerTicket`/审批者批准路径**内部**、append 成功之后，权限判定（工单归属、`ShouldConsult`/`Decide` fail-closed）**未放松**，发布不改变任何准入。
- 门覆盖全部表面？三个写入点是 `ticket_answered` 的**全部**写入点（非测试 grep 仅 3 处、无第四处，台账 5），故不存在「同规则另一入口漏门」的通道分裂。
- 检查与动作之间的窗口（TOCTOU）：`AppendEvent`→`Publish` 非事务，但发布是尽力扇出、真源在 store；并发下无安全后果。**无门禁绕过风险，因为**本卡不改写路径的准入语义。

**6. 序列化边界**
- 新增字段？**无**（C-3 明示 payload schema 逐字不变）。手写序列化/投影链：`orchestration.TicketAnsweredPayload` / `approval.ticketAnsweredPayload`（`ticket_id`/`answer`）→ `Store.AppendEvent` 的 `json.Marshal` → `proto.Event.Payload`（`json.RawMessage`）→ `/ws/events` 帧 → 镜像信封 `{"node","attempt","task_type","payload"}`（`internal/ledger/mirror.go:62`，payload 原样透传）→ `Store.OpenTickets` 解析 `ticketPayload{ticket_id}`。**无一处手搭 map**；`task_type` 用事件类型字面值。
- 「字段缺失 vs 零值」：本卡无新字段，`ticket_id` 为空时 `OpenTickets`/镜像消费侧的判空（`ticket.TicketID != ""`）不变；该类型本就在订阅重放窗口被镜像过（spec §1 `c99a527d` 两条为反证），wire 形状已被现网验证。
- 两端各自有测试 ≠ 链路有测试：**无一条测试穿过 store→hub→`/ws/events`→mirror→`OpenTickets`**。**无，因为** C-3 无新字段、该事件在重放窗口已跑过同一边界；端到端归真机 #2/#3（P4 给是否补集成回归的选择）。

**7. 枚举新值过既有白名单**
- 新引入枚举值？**无**。既有值 `ticket_answered` 流经的白名单逐处核对：`WaitDeliveryPolicy`（delivery.go:29，**判 false**，不影响镜像因镜像不过该策略）、`mirrorSkip`（mirror.go:179，**不含**之→镜像不跳过）、`OpenTickets` switch（taskstate.go:180，已登记）、`proto.EventType` 词表（已在）。→ 无「通道分裂」；中间无白名单挡死发布→镜像。
- `task_type` 终态字面值 `completed/failed/archived` 在 `OpenTickets` 已登记（C-2），未引入新值、未改 `mirrorTaskTerminal`。

**8. 承重安全属性有测试锁住**
- 本卡不引入一次性 token、唯一性、隔离等安全属性。**无，因为**三发布点不产生凭据/唯一性约束，C-2 是只读投影。
- 相关的「应答一次性」属性（幂等重答不重复发布）由编排测试的「幂等重答不再发布」反面断言锁住。

**9. webview / 平台表现差异候选族**
- **无，因为**不触 `d_web`、Wails、Chromium、cookie、剪贴板或浏览器 API；文档面也无 webview fixture。

---

## 6. 真机清单（全部「未验证，需真机」，归协调者执行）

1. **存量自愈**：升级到含 C-2 的构建后，对真实旧账本挂 `handoff card wait B369 --subtree`，首行 actionable 不再列已答工单；卡详情 `open_tickets` 计数与快照一致（spec §4 用户故事 1）。
2. **运行中人工 reply 跨机**：远端 agentd 任务 `reply` 后数秒内，本机卡流出现 `task_mirrored(ticket_answered)`，`card wait` 不再当未决（穿 `/ws/events` + 真实 `ledgermirror`）。
3. **三类审批者路径**：Manager 中介实时裁决、reuse 复用、approval 面（OpenCode）各一次，确认均落关单镜像。
4. **生产装配核对**：真实 agentd 进程里 `internal/orchestration/approval_client.go#Manager.bindApproval` 的 `Hub: m.hub` 生效——approval 面发布确实进同一 hub（机内无断言，见 §5.4）。
5. **断流/慢订阅残余**：镜像断连期答单，重连后（水位跳缺口、本期不根治）确认 C-2 终态收口；长任务中途多轮工单观察是否残留幽灵单（§5.1 残余风险）。
6. **wait 不唤醒**：真实 `card wait` / `handoff wait` 挂到 `ticket_answered` 时不发唤醒行（`WaitDeliveryPolicy` 冻结）。

---

## 7. 出稿自检

- [x] **产出四样齐全**：§1 子系统清单每个带 best.json 类型；§2 契约增量逐条有结论（§2.3 无退回、§2.4 四条澄清）；§3 子卡（S1）四段式且判据行为化；§5 缺陷族逐族含「无，因为……」。
- [x] **「待拍板」岔口集中**：P1–P4 全在 §0，正文岔口回指。
- [x] **「未验证，需真机」汇总**：§6 六条。
- [x] **每张子卡有界文件集核过**：S1 已圈（`skills/handoff/SKILL.md` + `docs/roadmap.md` [+`README.md`]）；Ticket 0 非扇出卡（P1=甲）。
- [x] **行为闭环每行五格完整**：§4，归属存在；无无人认领格子。
- [x] **契约状态位**：spec「已批准」实读；contract 无「冻结状态」行——已在 §2.1/§2.4 澄清 4 记录并回写修订记录（P3）。
- [x] **未亲自跑到结果的命令未写成结论**：build 与三包子集测试、`codegraph check`/`validate`、`sym` 实测均本轮实跑（台账 13–16）；跨机行为一律标真机。
- [x] **收尾**：`codegraph resolve --doc docs/superpowers/specs/b380-breakdown.md` 亲跑（结果落台账 18）；坏锚即修。

---

## 8. 拍板记录区（协调者回填）

| 编号 | 裁决 | 理由 |
| --- | --- | --- |
| **P1** | **甲（单轮闭合，不再开实现子卡）** | contract Ticket 0 已落全部运行时代码（三发布点 + C-2 终态关单）且红→绿证据齐；再开实现卡只是复跑已绿判据。 |
| **P2** | **乙（S1 并入本卡后续节点收尾，不单独建卡）** | 剩余活仅三个文档文件的措辞同步 + roadmap 一行；本卡工作流下一步即 plan→implement，把它作为 implement 节点的唯一范围即可——有界文件集照 §3.1④ 圈定（`skills/handoff/SKILL.md` + `docs/roadmap.md`，可选 `README.md`），验收判据逐字照抄 §3.1③，不借机改 `internal/**`。独立子卡的可追溯性由 plan 的任务清单承载，不另付一次派发往返。 |
| **P3** | **甲（spec 落点维持 origin/cards/B380-charter-1@117bbd5a；合并时由协调者把 spec 两提交并进合并目标）** | contract 修订记录已回写（本分支），状态位缺失已显性化；合并目标 finish 阶段处理 spec 可及性，不让文档搬运进任何执行者轮。 |
| **P4** | **甲（不补机内端到端回归）** | C-3 无新字段、该事件在重放窗口已跑过同一边界（spec §1 反证）；端到端归真机清单 §6（#1–#6），由协调者在 acceptance 前执行。 |

（回填后头部状态行改为「已拍板（日期）」，并与裁决同批提交。）
