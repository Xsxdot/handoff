# B358 台账（breakdown 出稿轮，2026-09-12）

> 出稿者：跨子系统单 agent（breakdown 节点）。每确立一个事实追加一行；本文件与拆解稿同批提交，不单独提交。

## 一、工作区与上游状态位

- `git status --short`：仅无关脏文件 `web/src/app/task/Composer.test.tsx`（M）与未跟踪 `.commandcode/`、`docs/superpowers/plans/b353-probe-follow-harness.md`、`web/design-qa-sidebar-after.png`、`web/design-qa.md`。本节点不触碰。
- `git log --oneline -5`：HEAD = `ef8257bb contract(B358): 会话即工作单元——会话语义/投递寻址/账本会话存储冻结`；契约提交含 Ticket 0 全部文件（proto/sessions.go、ledger/sessions.go、collab/sessions.go、room/delivery.go 等 18 文件，3067 行）。
- spec 头部状态位核对：`docs/superpowers/specs/b358.md:5`「**状态**：**已批准**（用户 2026-09-12…）」——已回写，无需动作。
- 契约头部状态位核对：`docs/superpowers/specs/b358-contract.md:6-7`「冻结状态：本提交随 codegraph/target.json、…冻结」「有效基线：cards/B233.1-charter-7 @ 94246fc8」——已回写，与 HEAD 一致。

## 二、子系统清单（best.json 权威）

- `codegraph/best.json` domains 中 parent 为空的顶层领域共 15 个：d_orchestration / d_gateway / d_workspace / d_execution / d_sessions / d_transport / d_protocol / d_ledger / d_collab / d_cli / d_web / d_policy / d_maintenance / d_scheduling / d_keystone。
- 本卡触及域的 type（直接取 best.json）：d_collab=logic、d_ledger=logic、d_protocol=logic、d_orchestration=logic、d_keystone=logic、d_cli=logic、d_web=logic、d_gateway=boundary。
- 注意：`codegraph domains` CLI 输出与 best.json 文件不一致（CLI 读到 d_coordination/d_runtime 等另一版本图）——按指令以 best.json 文件为准。
- target.json 核对：三条既有方向（d_collab→d_protocol、d_ledger→d_protocol、d_collab→d_ledger）各带 B358 注记（legacyBudgetNote），无新方向、无预算变化——与契约 §5 一致。

## 三、图覆盖债（codegraph sym 探针，均未命中）

- `codegraph sym collab.Service`、`sym internal/collab/room/delivery.go#ResolveDelivery`、`sym internal/collab/sessions.go#WakeTargets`、`sym internal/agentd/wakeconsumer.go#automationWakeEvent`、`sym internal/collab/room/room.go#Resolve`、`sym internal/ledger/sessions.go#CreateSession`、`sym internal/agentd/roomsapi.go` → 全部 `Error: 符号 … 不在图中`。本卡触及面整体不在 best 图中；Ticket 0 新符号只记录在 `codegraph/diffs/cards-B358-charter.json`（39 nodesAdded + 2 nodesModified + 1 lifecycleAdded，view=cards/B358-charter，base=94246fc8）。本稿一律用 `file#Symbol` 源码锚。

## 四、读码事实（Ticket 0 现状 + 唤醒路径现状）

- `internal/collab/sessions.go`：Service 会话方法全集为真实实现（非空壳）：CreateSession/ListSessions/SessionDetail/Archive/Join/Leave/WakeTargets/MessageWakeTargets/AddressesCard；memberStatus 走 `s.lc.DriverLease` + 注入时钟 `nowFn`（service.go:31）。
- `internal/collab/room/delivery.go`：ResolveDelivery/IsAddressed 纯函数已实现；系统行（BySystem/pointer）恒空集；@卡号 经注入 resolveSeat；去重保序。
- `internal/ledger/sessions.go`：Store 八方法 + session:<n> 单调分配 + session_cards 唯一索引；CreateSession owner 非空校验、Members 初值含 owner。
- `internal/proto/sessions.go`：DTO 全集 + 成员/状态/timeline 三套词表已冻结；`SessionEventSeatBound`、`SessionEventNeedsHuman` 常量已存在。
- **发现 A（timeline 席位变更归属偏差）**：`internal/collab/sessions.go#sessionTimeline` 的 driver_takeover 分支用 `byCard`（ListAllCards 全量卡）判存在性，未判「该卡在本会话内」——任何卡的换绑会落进每场会话的 timeline；契约 §9 明文「仅当事件所属卡在该会话内时归入」。测试只断言 `len(detail.Timeline)>0`（sessions_test.go:117），未钉住该行为。→ 归投影精化子卡修正，非契约问题。
- **发现 B（归档只读未接进 Send）**：`internal/collab/service.go#Send` 只查 `room.Resolve` 的 ReadOnly；会话分支恒 ReadOnly=false（room.go 注释「归档只读判定由门面在会话本体上做」），而 Send 未查会话本体 → 今天向归档会话发言会成功。`TestSessionArchiveReadOnly` 只覆盖 JoinCard。→ 归会话语义收口子卡；属边界澄清回写。
- **发现 C（会话书写执法缺失）**：`room.VerifyWriter` 对 kind=user 且 `r.Card==nil`（群/会话房间）直接放行任意非空 actor；spec §6 接缝 #1 要求「非成员不能发言」。契约原子清单无此条。→ 边界澄清回写：会话书写执法 = actor ∈ 显式成员 ∪ 会话内卡当前席位，门面职责。
- **发现 D（listening 无生产载体）**：`memberStatus` 只产 working/last_active；`RenewDriverLease`/`DropDriverLease` 生产代码零调用（grep 全仓非测试无命中）→ 生产环境租约恒无，成员状态生产只报 last_active/empty。listening 保留作词表位（OOS 心跳路径）。→ 边界澄清回写。
- **发现 E（初始坐下无事件）**：`internal/ledger/binding.go#BindSeat` 注释明言「不落事件也不写 driver_carrier」；只有 RebindSeat 落 EvDriverTakeover。⇒ 「协调者入群」（首次坐下）在账本无事件，timeline 无法呈现；冻结常量 `SessionEventSeatBound` 无生产者。spec §4.3 把「协调者入群」列为 timeline 结构事件。→ **退回 contract**（R2）。
- **发现 F（卡收口 timeline kind 缺值）**：proto SessionEvent* 词表无「卡收口/终止」值；spec §4.3 与用户故事 9（「卡何时收口」）要求 timeline 呈现。载体可用既有 EvStatusMoved（payload {to}），缺的只是 timeline kind 词表值。→ **退回 contract**（R3，微增量）。
- **发现 G（外部订阅通道缺签名）**：spec §4.3 明言「外部会话的订阅通道…形态（扩既有 wait 的事件类型，或另立一条）归 contract 定签名」；契约冻结物通篇未定义该通道（§3 无方法、§8 无欠账条目）。keystone.Wake 是卡锚（`Wake(ctx, card, …)`，WakeEvent{Kind,Card,Summary}），外部身份（人/主 agent）无机内唤醒载体；北极星 flow.html 图 3 明确「@ 主 agent…只唤醒主 agent」「判不了 → @ 人（唤醒人）」是承诺行为。→ **退回 contract**（R1）。
- `internal/agentd/wakeconsumer.go`：`automationWakeEvent:145` 首行 `if ev.CardID == ""` 直接不唤醒（会话消息全哑）；`decodeHumanRoomMessage:216` 对 `Kind==user && !BySystem` 无条件唤醒本卡协调者（mentions 未读，广播形状）。消费循环 `consumeAutomationEventsOnce` 按 `pendingByCard` 分组、经 `keystone.Decide`/`wakeCoordinatorRound` 唤醒。
- `cmd/room.go`：room list/read/send/inbox 直调 collab.Service；`room send --kind user` 拒绝 `--cli/--session`（:124-127）→ 协调者今天无法以席位身份在会话里以 user kind 发言。
- `internal/agentd/roomsapi.go`：房间五端点（list/messages GET+POST/read/inbox）；`collabErr` 哨兵映射；`roomUserActor = "web:"+hostOnly`。会话端点（list/detail/create/archive/join/leave）尚无。
- `internal/collab/service.go#consumeRoomMentions`：「回复即清提及」只处理卡房间（`ev.CardID != roomID` 过滤掉无卡事件）→ 会话房间的 @ 无消费出口，收件箱 mention 源只进不出。
- `internal/collab/service.go#Mentions`：扫全量 room_message 含会话消息 → 会话 @ 会进收件箱 mention 源（但如上无消费出口）。
- web 侧：`web/src/api/rooms.ts` 无任何 Session DTO；RoomPanel 挂载于 `web/src/app/shell/Shell.tsx:808`；app/rooms/ 七文件。北极星形态（sessions.html）未落地。
- roadmap 核对：`docs/roadmap.md` 无 B358 OOS 登记节（契约 §8.8 欠账成立）。
- 身份记法现状：CLI actor=`cli:<user>@<host>`（ledgerActor）、web actor=`web:<host>`（roomUserActor）、席位=`cli:<cli>#<session_id>`；sessionMembers 以 `cli:` 前缀近似 kind（sessions.go:224-228 注释「精确种类归 plan」）。外部会话身份格式契约未冻结。

## 五、缺陷族与 skill 装载

- Skill 工具装载 `charter:breakdown`、`charter:architecture-law`、`charter:defect-families` 成功；项目无自己的缺陷族清单文件（find 无命中）→ 以 charter 基线五族 + 序列化边界/枚举白名单/承重安全属性/webview 候选族作答。

## 六、退回项与岔口（详见拆解稿 §0/§2）

- R1 外部订阅通道缺签名 → 退回 contract（spec §4.3 明言归 contract 定签名）。
- R2 协调者入群无账本事件（BindSeat 不落事件）→ 退回 contract。
- R3 卡收口 timeline kind 缺值 → 退回 contract（微增量）。
- 边界澄清（不退回）：会话书写执法归门面；归档只读判定接进 Send；listening 词表位留白。→ 已回写 `b358-contract.md` 末尾修订记录一行。

## 七、收尾自检记录

- 补记（写稿期新确立事实）：`Service.Pointer` 有两个生产调用方——`cmd/card_dispatch.go:434`（派发指针）与 `internal/agentd/server.go:2951`（roomNarrator.Say）；⇒ 旧卡房间「只读归档」必须豁免 pointer 写路径，否则 B274 派发指针与 B156.3 房间叙事两条既有路同时断。已写进 S1 意图与验收（pointer 存在式断言，不数行数——server.go:2940 注释明言两条上游并存时计数式断言互变偶发红）。
- 补记：`internal/collab/service.go#Mentions` 扫全量 room_message 含会话消息（会话 @ 会进收件箱 mention 源），但 `consumeRoomMentions` 只消费卡房间提及（`ev.CardID != roomID` 过滤）⇒ 会话 @ 无消费出口。已写进 S1 验收（B287「回复即清」延伸到会话房间）。
- `codegraph resolve --doc docs/superpowers/specs/b358-breakdown.md` 第一轮：23 锚中 3 坏——`sessions.go#sessionMembers`/`sessions.go#sessionTimeline`（短路径 file_missing）、`internal/collab/sessions.go#Service.MessageWakeTargets`（vanished，图未覆盖 + 方法名带前缀 grep 不中）。修法：全部补全路径、方法锚去 `Service.` 前缀。复跑：23 锚全 ok/moved，无 error 输出（原始输出存 /tmp/b358-resolve2.txt 读数：`"anchor": "ok"` ×23）。
- 改动面核对（本节点全部产出）：新增 `docs/superpowers/specs/b358-breakdown.md`、新增 `docs/superpowers/ledgers/2026-09-12-b358-breakdown-ledger.md`、`docs/superpowers/specs/b358-contract.md` 末尾 +1 行修订记录；无代码改动、无卡操作、无提交。
