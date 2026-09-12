# B358 拆解提案：会话即工作单元——房间锚点从卡翻转到会话（群 + 席位入群 + 寻址投递）

**状态：已拍板（2026-09-12）——P1–P7 全案 A；R1/R2/R3 已由 contract 补签名轮处置（契约 §3.8–3.9、§4.6–4.8），S2/S3 受阻条目解除阻塞**
**卡：** B358（L3 重档；contract 已冻结 @ `ef8257bb`，Ticket 0 骨架在工作树）
**上游 spec：** `docs/superpowers/specs/b358.md`（头部状态：**已批准**，本稿逐字核对过文件头）
**冻结 contract：** `docs/superpowers/specs/b358-contract.md`（头部状态：**已冻结**；本稿核对产生三条退回项与三条边界澄清，澄清已回写该文末尾修订记录一行）
**北极星：** `prototypes/b358-session-groups/pages/sessions.html`（形态）、`pages/flow.html`（过程）、`README.md`（W1–W6）
**本稿台账：** `docs/superpowers/ledgers/2026-09-12-b358-breakdown-ledger.md`
**图依据：** `codegraph/best.json`（`parent` 为空的顶层领域 = 子系统清单）；本卡触及面整体不在图中，见 §6 图覆盖债
**角色边界：** 本文全部是提案。不写实现代码、不建卡、不派发、不调派发工具、不做 git 提交；拍板与扇出归协调者。

---

## 0. 待拍板岔口（集中清单）

拍板者按本表逐条裁决；正文岔口一律回指本表。裁决后逐条回写裁决与理由，头部状态改「已拍板（日期）」。

1. **P1｜退回项 R1：外部会话（人 / 主 agent）订阅通道缺签名。**
   spec §4.3 明言「形态（扩既有 wait 的事件类型，或另立一条）归 contract 定签名」，契约冻结物通篇未定义；keystone 唤醒是卡锚（`keystone.Wake(ctx, card, …)`），外部身份无机内载体；北极星 flow.html 图 3 明确「@ 主 agent…只唤醒主 agent」「判不了 → @ 人（唤醒人）」是承诺行为。
   - **方案 A（推荐）：按纪律退回 contract 补签名**——由 contract 节点定通道形态（候选：扩 `handoff card wait` 族成会话维度的阻塞等待；或 gateway 事件流只推「寻址命中我」）。理由：这是 spec 点名交给 contract 的载体，缺了它用户故事 5/7 与升级链的「@主 agent / @人」两档全是哑的；补签名的增量小（一条等待/订阅面 + 唤醒消费的一分支）。
   - **方案 B：降级为纯拉**——主 agent 与人的「被 @ 醒来」改为巡场拉（收件箱/会话列表未读），需回 spec 改承诺并走用户裁决。理由：省一条通道；代价是推翻级升级从「推」退化成「等巡场」，违背「推只在升级那一刻」的定档。
   - **裁决（2026-09-12 协调者）：A。**通道形态方向由用户同日探讨定调：外部会话（人 / 主 agent，用户自启）走事件流订阅（对应 spec §4.3 候选「扩既有 wait 的事件类型」）；handoff 内部 agent 的 print/continue 唤醒属「参战形态 v2」另卡，不入 B358。contract 增补按此定签名，S3 外部半边随之解除阻塞。
2. **P2｜退回项 R2：协调者入群（初始坐下）无账本事件。** `internal/ledger/binding.go#BindSeat` 明言「不落事件」；只有换绑落 `EvDriverTakeover`。冻结常量 `proto.SessionEventSeatBound` 无生产者，timeline 无法呈现 spec §4.3 承诺的「协调者入群」。
   - **方案 A（推荐）：退回 contract**——账本词表 +1 条席位事件（`BindSeat` 同事务落事件），timeline 消费。理由：微增量；「谁在何时接手现场」是用户故事 9 的明确承诺，接手者读 timeline 接现场是本卡卖点。
   - **方案 B：降级**——timeline 不呈现入群，成员列表已能看见当前席位；需回 spec 改 §4.3 承诺。
   - **裁决：A。**词表 +1，`BindSeat` 同事务落席位事件；「谁在何时接手现场」是用户故事 9 的承诺，降级砍的是本卡卖点。
3. **P3｜退回项 R3：卡收口/终止的 timeline kind 缺值。** `proto.SessionEvent*` 词表没有「卡收口」值；载体可用既有 `EvStatusMoved`（payload `{to}`），缺的只是 timeline kind 词表常量。
   - **方案 A（推荐）：退回 contract 补一个常量**（如 `SessionEventCardClosed`），timeline 消费既有事件。理由：微增量，复用既有载体。
   - **方案 B：降级**——卡收口只在详情页卡条状态（`SessionCard.Status`）可见，不进 timeline；需回 spec 改 §4.3 / 用户故事 9。
   - **裁决：A。**+1 常量复用既有 `EvStatusMoved` 载体，微增量。
4. **P4｜会话成员身份记法（人 / 主 agent 的外部会话身份字符串口径）。** 现状三套 actor 注入：CLI=`cli:<user>@<host>`、gateway=`web:<hostOnly>`（机器位，换浏览器即变）、席位=`cli:<cli>#<session_id>`；契约对外部身份格式未冻结（`internal/collab/sessions.go#sessionMembers` 以 `cli:` 前缀近似 kind，注释明言「精确种类归 plan」）。`@` 命中要求 mention 与成员身份逐字相等，跨面口径漂移 = 寻址静默失效。
   - **方案 A（推荐）：统一人=`user:<name>`、主 agent=`agent:<name>`**，开会话/入会时经 `AddSessionMember` 落显式成员；gateway/CLI 既有 actor 注入面不动，仅在会话成员匹配与 mention 解析层使用统一记法。理由：`web:<host>` 是机器位不是人位，会话成员要跨机器、跨载体稳定；`@人` 的 mention 也得打得出来。
   - **方案 B：沿用既有 actor 记法直接当成员身份**（零映射）。理由：改动最小；代价是 web 成员身份随 host 漂、CLI 与 web 两套口径在 @ 命中上互不相认。
   - 拍板后归 plan 定稿注入点；不涉及契约 wire 形状变更（身份串在契约里本就是不透明字符串）。
   - **裁决：A。**成员身份要跨机、跨载体稳定，`web:<host>` 是机器位不是人位；注入点归 plan，不动 wire。
5. **P5｜旧 `project:<name>` / `global` 群房间的处置。** spec 实现决定 5 只点名「326 个卡房间只读归档」；契约冻结条目 34 把旧形态变更整体留给实现节点。
   - **方案 A（推荐）：同批只读归档**——旧群房间拒绝会话发言（pointer 豁免不适用——pointer 本就只写卡房间），`room list`/读面保留对质。理由：锚点翻转彻底，留一套可写但新 UI 不可见的僵尸群面是扇出与误解的温床。
   - **方案 B：保留 B156.2 语义**（user 可写 + 寻址化唤醒），自然消亡。理由：改动更小；代价是两套群形态长期并存。
   - **裁决：A。**可写但新 UI 不可见的僵尸群面是扇出与误解的温床；同批只读归档，读面对质保留。
6. **P6｜升级三档纪律文本的载体路由（contract §8.6）。** 内容横跨两仓：本仓 `skills/handoff/SKILL.md` 协作房间纪律节（:578 起）+ `docs/roadmap.md` OOS 登记（§8.8）；charter 套件半边（协调者/主 agent discipline 的三档升级规则）在 `~/.agents/skills/` 与 `~/workspace/charter` 仓，**出不了本仓的有界文件集**。
   - **方案 A（推荐）：本仓半边出一张文档子卡**（S7），charter 套件半边由协调者走 charter 仓自己的流程另办（`scripts/regen_discipline.py` 同步）。理由：本仓半边可派发可验收（grep 断言可写）；charter 半边硬塞进本仓卡违反有界文件集。
   - **方案 B：全部留协调者本地办**，不出子卡。理由：省一张卡；代价是 repo 半边的落账与证据链游离在卡流程外。
   - **裁决：A。**S7 出本仓半边；charter 半边（协调者/主 agent discipline 三档升级规则）是协调者义务——改 `~/workspace/charter` 并跑 `scripts/regen_discipline.py`，随本卡推进另办，记卡上跟进，不出子卡。
7. **P7｜CLI 命令族命名。** 锚点翻转后产品词是「会话」。
   - **方案 A（推荐）：新开 `handoff session` 命令族**（list/detail/create/archive/join/leave/send），旧 `room list/read` 保留为旧房间只读对质面，`room send` 随旧房间归档自然失效（保留报错），`room inbox` 不动。理由：旧 `room send` 的目标（卡房间）已死，扩进 room 族会让新旧语义挤在一族。
   - **方案 B：扩既有 `room` 族**（`room sessions`、`room join` 等）。理由：命令树不长大；代价是 `room send <session>` 与 `room send <卡号>` 同形不同命（一个活一个死），靠房间形态区分。
   - **裁决：A。**旧 `room send` 的目标已死，新词立新族；旧 room 留只读对质面。

---

## 1. 触及子系统清单与派卡资格核验

子系统 id 与类型逐字取自 `codegraph/best.json` 的 `domains`（`parent` 为空 = 子系统；`type` 即逻辑/边界标注）。扇出前按架构法第一条**派卡资格四条**（有界文件集 / 契约面可枚举 / 依赖可排 DAG / 有类型标注）逐个核。

| 子系统（best id） | 类型 | 本卡有界文件集与暴露面 | 派卡资格四条核验 |
|---|---|---|---|
| `d_collab` | 逻辑型 | `internal/collab/service.go`、`sessions.go`、`room/room.go`、`room/delivery.go`（Ticket 0 已落，本卡只在废止时触碰）+ 四个测试文件；暴露面 = 入站门面 `collab.Service`（会话生命周期/投影/寻址判定）。 | ①文件集一条路径规则圈得出（internal/collab）；②门面方法与哨兵（ErrNoRoom/ErrReadOnly/ErrNotWriter）已冻结；③对 d_ledger 只走既有 LedgerClient 接口缝、对 d_protocol 只走既有 entries，DAG 无新边；④测试可全机内闭环。 |
| `d_ledger` | 逻辑型 | Ticket 0 已落 `internal/ledger/sessions.go`、`types.go`、`store.go`、`api/api.go`；本卡**无新增实现文件**，仅 R2 若拍板方案 A 需回 contract 后动 `binding.go`。 | ①账本文件集有界；②事件词表与八方法已冻结；③对 d_protocol 既有边；④SQLite 机内闭环，PG 归真机清单。 |
| `d_protocol` | 逻辑型 | `internal/proto/sessions.go`、`rooms.go`（Ticket 0 已落）；本卡无新增字段；R3 若拍板方案 A 仅 +1 常量（先回 contract）。 | ①两文件有界；②DTO 键集与词表金样本已冻结；③无新边；④Go 金样本 roundtrip 机内闭环。 |
| `d_gateway` | 边界型 | `internal/agentd/wakeconsumer.go`（改接）、`roomsapi.go` 或新 `sessionsapi.go`（会话端点）、`ledgerapi.go`（路由注册行）、`server.go` 仅组装行；暴露面 = HTTP 路由 + 既有哨兵映射。 | ①按文件圈定，不吞整个 agentd；②路由形状以冻结 DTO 为载荷、错误映射沿用 collabErr；③对 d_collab 走既有入站门面 import、对 keystone 既有调用，DAG 无新边；④httptest 可验契约形状，真实浏览器/CLI 对端归真机。 |
| `d_orchestration` | 逻辑型 | 即 `internal/agentd/wakeconsumer.go` 的消费循环切片（契约 §2.1：wakeconsumer 域归属 d_orchestration、代码在 agentd）；与 d_gateway 同文件集，按切片分卡不按目录分卡。 | ①切片有界（唤醒路径一个职责）；②寻址判定唯一入口 `MessageWakeTargets`/`AddressesCard` 已冻结；③keystone 调用既有；④真账本 + fake keystone 机内闭环。 |
| `d_cli` | 逻辑型 | `cmd/room.go`（或新 `cmd/session.go` + `room.go` 瘦身）、`cmd/card_dispatch.go` 不动（pointer 路径不碰）、`ledgercli.go` actor 注入不动；暴露面 = cobra 命令族 stdout/退出码。 | ①命令文件集有界；②命令面可枚举；③直调 collab 门面（`roomServiceFor` 既有组装点），DAG 无新边；④Go 命令测试闭环。 |
| `d_web` | 逻辑型 | `web/src/api/rooms.ts` + `rooms.test.ts` + `testdata/RoomsFixture.json`、`web/src/app/rooms/`（新会话组件族）、`web/src/app/shell/Shell.tsx`（挂载行）、`web/src/app/homedock/`（入口）；暴露面 = 控制台会话页三态。 | ①按组件族圈定；②消费面 = 冻结 DTO 键集（双侧金样本）；③只依赖 gateway HTTP，DAG 无新边；④vitest 组件/金样本机内闭环，真实浏览器走查归真机。 |
| `d_keystone` | 逻辑型（仅护栏） | 不新增实现；`internal/keystone/keystone.go` 的 `WakeEvent{Kind,Card,Summary}` 与 `WakeMessage` 作为既有载体护栏。 | ①无本卡实现文件；②WakeKind 词表已冻结且含 `WakeMessage`；③不读 transport 流；④fake runner 可观测，真实协调者回合归真机。 |

**不列为本卡实现域：** `d_workspace`、`d_sessions`（终端 PTY，与协作会话同名不同物——沿用 B156.2 命名警示）、`d_transport`、`d_policy`、`d_maintenance`、`d_scheduling`、`d_execution`。执行者不入群不写账（spec 永不做项），`d_execution` 零触及。

### 1.1 竖切债检查（架构法第三条）

`internal/collab`（5 源文件 + room 子包）、`internal/agentd`（扁平大包）、`cmd/`（扁平大包）均未触发第三条升格信号的新增——本卡**不插竖切还债卡**。约束：agentd 与 cmd 的文件集按职责切片圈定（唤醒切片 / 会话端点切片 / 会话命令切片），不得按目录整包切。

---

## 2. 契约增量核对

### 2.1 上游状态位

- spec 头部 `b358.md:5`「**状态**：**已批准**（用户 2026-09-12 …）」——文件头逐字核对通过，引用有效。
- 契约头部 `b358-contract.md:6-7`「冻结状态：本提交随 … 冻结」「有效基线：cards/B233.1-charter-7 @ 94246fc8」——与 HEAD `ef8257bb`（契约提交）一致，核对通过。
- `codegraph/target.json` 三条既有方向的 B358 注记已随契约提交落盘（grep 到 d_collab→d_protocol、d_ledger→d_protocol、d_collab→d_ledger 各一条），无新方向、无预算变化——与契约 §5 一致。
- `codegraph/diffs/cards-B358-charter.json` 在库（view=cards/B358-charter，base=94246fc8，39 nodesAdded）。

### 2.2 契约 §8 欠账逐条对照（拆解吸收位置）

| 契约 §8 欠账 | 本稿归属 | 越界结论 |
|---|---|---|
| 1 唤醒路径改接寻址 | S3 | 不越界：只动 wakeconsumer 切片，寻址判定唯一入口已冻结（§3.5）。 |
| 2 旧规则废止（kind 矩阵/白名单/卡:房间 Resolve） | S1 | 不越界：契约明言「本轮只加不改，旧矩阵仍在」，废止是欠账非接缝；`KindAllowed`/`VerifyWriter` 删除不新增面。 |
| 3 控制面 HTTP/CLI 接线 | S4、S5 | 不越界：路由载荷 = 冻结 DTO，错误映射沿用 collab 哨兵；路由名未冻结（plan 定稿，见 §3.4 守卫）。 |
| 4 控制台内容面 | S6 | 不越界：TS 镜像与孪生金样本是 §7.3 点名欠账；形态对照北极星。 |
| 5 详情页投影精细判据 | S2 | 不越界：词表已冻结（含 `SessionEventNeedsHuman`）；Ticket 0 实现偏差（§2.4 发现 A）是实现欠账非契约缺口。 |
| 6 升级三档纪律文本 | S7 + 协调者（P6） | 不越界：非代码面；charter 半边出不了本仓文件集，路由待拍板 P6。 |
| 7 旧 326 卡房间只读归档 | S1 | 不越界：契约条目 34 明言「旧房间只读归档在实现节点」；pointer 写路径豁免是既有事实（`Service.Pointer` 是 B274/B156.3 沿用入口），非新接缝。 |
| 8 OOS 项 roadmap 登记 | S7 | 不越界：纯文档。 |

### 2.3 退回 contract（不许边拆边加）

三条，均为「spec 承诺了行为、冻结物里没有载体」：

- **R1 外部会话订阅通道缺签名**（→ P1）。spec §4.3：「外部会话的订阅通道…归 contract 定签名」。契约 §3 无此方法、§8 无此欠账。机内事实：`keystone.Wake(ctx, card, events, spec)` 卡锚签名 + `WakeEvent{Kind,Card,Summary}` 装不下外部身份；北极星 flow.html 图 2/图 3 的「唤醒主 agent」「唤醒人」无承载。**在 R1 处置前，S3 的外部半边一律「入未读、不唤醒、留日志」，不得按成员集合广播兜底。**
- **R2 协调者入群无账本事件**（→ P2）。`BindSeat` 不落事件（`binding.go:28` 注释明言）；`proto.SessionEventSeatBound` 无生产者；spec §4.3 把「协调者入群」列为 timeline 结构事件，用户故事 9「谁进群」含席位成员入场时刻。
- **R3 卡收口/终止 timeline kind 缺值**（→ P3）。spec §4.3「卡收口或终止…归详情页 timeline」、用户故事 9「卡何时收口」；`proto.SessionEvent*` 词表无对应值（既有 `EvStatusMoved` 可作载体）。

R2/R3 若拍板降级（方案 B），则不是 contract 增量而是 spec 承诺变更——同样须先回 spec 备案，不得在 S2 里静默缩水。

### 2.4 边界澄清（不退回，已回写契约修订记录一行）

1. **会话书写执法归门面**（发现 C）：spec §6 接缝 #1 要求「非成员不能发言」，契约原子清单无此条。澄清：执法规则 = actor ∈ 显式成员 ∪ 会话内各卡当前席位，在 `Service.Send` 路径执法；`room.Resolve` 只解析形态（§3.7 既有分工不变）。→ S1 验收。
2. **归档只读接进 Send**（发现 B）：`Service.Send` 今天不查会话本体，归档会话发言会成功（`TestSessionArchiveReadOnly` 只覆盖 JoinCard）。澄清：§3.7「归档只读判定由门面在会话本体上做」覆盖发言路径。→ S1 验收。
3. **listening 词表位留白**（发现 D）：`RenewDriverLease`/`DropDriverLease` 生产零调用方（grep 全仓非测试无命中），租约只有到期时刻、无状态语义 → 生产成员状态只报 `working`（有未过期租约时）/`last_active`/`empty`；`listening` 保留作词表位，随 OOS 心跳路径启用。→ S2 验收与真机清单。
4. **（实现偏差，随 S2 修，非契约问题）** Ticket 0 `sessionTimeline` 的 driver_takeover 分支用全量卡表判归属（`internal/collab/sessions.go#sessionTimeline` 的 `byCard` 检查），任何卡的换绑会落进每场会话的 timeline；契约 §9 明文「仅当事件所属卡在该会话内时归入」。测试未钉住（`internal/collab/sessions_test.go:117` 只断言非空）。

以上四条已回写 `b358-contract.md` 末尾「修订记录（breakdown 出稿轮，2026-09-12）」一行。

---

## 3. 子卡清单与依赖 DAG

序号 S1–S7 是提案编号，真实卡号由协调者扇出时分配。**S3 的外部半边与 S2 的两个 timeline 条目被 R1/R2/R3 阻塞**——退回项处置优先于受阻塞子卡的对应条目，不阻塞其余条目并行。

### 3.1 DAG

```text
（协调者：R1/R2/R3 退回处置）
        │（R1 处置后）           （R2/R3 处置后）
        └──> S3 外部半边          └──> S2 的 seat_bound / 卡收口条目
S1 会话语义收口 ──┬──> S4 会话 HTTP 端点 ──> S6 控制台内容面
                  └──> S5 CLI session 命令族
S2 投影精化（其余条目） ──────> S4（详情断言）──> S6
S3 唤醒改接（席位半边） ──────────────────────> integrate（全量 + 回旋镖）
S7 纪律与 roadmap（无代码依赖） ──────────────> integrate
```

### 3.2 S1（d_collab）：会话语义收口与旧规则废止

**①契约引用**：contract §3.5/§3.7、§4.1（条目 6/7/13）、§4.4（条目 34/35）、§8.2、§8.7；spec §4.1/§4.2/§4.5、§6 接缝 #1；本稿 §2.4 澄清 1/2；岔口 P4/P5。

**②意图与为什么**：把「发言自由、身份执法」落成机内事实——kind 书写者矩阵与白名单废止后，发言权只由两件事决定：房间形态（旧房间只读归档，pointer 豁免）与成员身份（会话 = 显式成员 ∪ 会话内卡当前席位）。同时把归档只读从「Join 被拒」补齐到「发言被拒」，把 B287「回复即清提及」延伸到会话房间（否则会话 @ 在收件箱 mention 源只进不出，违背「@ 只是提醒」）。**pointer 豁免是硬约束**：`cmd/card_dispatch.go:434` 与 `internal/agentd/server.go:2951`（roomNarrator）两条派发指针路都靠卡房间的 pointer 可写活着，动不得。

**③验收（行为化，逻辑型机内闭环）**：

- `go test ./internal/collab/... -count=1` 退出 0；其中必须包含下列正反例（穿过 `collab.Service.Send`，不是只测 room 帮手）：
  - 归档会话发言 → `ErrReadOnly`；未归档会话成员（显式成员 / 会话内卡当前席位）发言 → 成功且 `actor` 落账可查。
  - 非成员发言（含会话外卡的席位身份）→ `ErrNotWriter`；换绑后旧席位发言 → `ErrNotWriter`、新席位发言 → 成功（同一会话，前后两条消息）。
  - 旧卡房间 user 发言 → `ErrReadOnly`；同一卡房间 `Service.Pointer` → 成功落账（存在式断言，不数行数——roomNarrator 与派发指针两条上游并存的既有约束）。
  - 会话房间内 user 回复后，该房间 @本人 的未消费提及被消费（`EvMessageConsumed` 存在），收件箱 mention 源不再返回该条。
- `go build ./...` 退出 0 且 `grep -rn "KindAllowed" internal/ cmd/` 零命中（白名单删除）、`VerifyWriter` 不再含按 kind 分权的分支（源码审查项：函数体只剩 actor 非空与成员/形态执法）。
- `Resolve` 的卡:房间 1:1 语义收敛后，`room list` 读面（`ListRooms`/`History`）对旧房间仍可读——历史对质不断。
- 判据均为「跑 X 命令返回 Y」形状；真实多进程并发发言（CLI 与 gateway 同时写同一会话）归真机清单 #8。

**④入口指针与有界文件集**：`internal/collab/service.go`、`internal/collab/room/room.go`、`internal/collab/service_test.go`、`internal/collab/sessions_test.go`、`internal/collab/readmodel_test.go`、`internal/collab/room/delivery_gate_test.go`（守卫保持绿）。符号锚：`internal/collab/service.go#Service.Send`、`internal/collab/service.go#Service.Pointer`、`internal/collab/service.go#consumeRoomMentions`、`internal/collab/room/room.go#VerifyWriter`、`internal/collab/room/room.go#KindAllowed`、`internal/collab/room/room.go#Resolve`。

**缺陷族对抗（验收栏）**：

1. **生命周期/状态机中断**：归档竞态（归档事务与发言事务并发）——账本单写者（`Store.mutate`）串行化，机内以同事务序列反例覆盖；PG 真库并发归**未验证，需真机**（真机清单 #4）。
2. **静默失败/误导报错**：旧房间发言被拒必须返回 `ErrReadOnly` 而非 `ErrNoRoom`（房间在、只是归档），错误文案可行动（「会话已归档/旧房间已只读」）；无「报成功但没做」窗口，因为写路径全走既有 `RecordRoomMessage` 单事务。
3. **跨平台假设**：无，因为本卡不触路径/进程组/webview，纯账本语义。
4. **假红/假绿**：验收测试必须穿 `Service.Send` 真实现 + 真账本，不许只测 room 包帮手；「KindAllowed 零命中」的 grep 断言锁删除而非锁实现；换绑反例（旧席位被拒）必须能变红——否则成员执法只在夹具里成立。
5. **门禁绕过**：成员执法覆盖全部发言入口（HTTP `handleRoomSend`、CLI `room send`、未来订阅通道回复）——同一条规则所有入口共享 `Service.Send` 一道门；CLI 自报席位身份的信任面与 B307 现状同基线（`ValidateSeat` 只验格式），真实防伪**未验证，需真机**且多人身份体系已 OOS 登记。
6. **序列化边界**：无新字段；`RoomMessage` 既有 JSON 链路金样本已锁（`rooms_fixture_test.go`），本卡不改编码。
7. **枚举新值过既有白名单**：废止白名单本身即本卡验收；反向风险是 wakeconsumer/`card_wait` 仍有 `Kind==user` 残留判断——S3/S1 各自 grep 断言无第二白名单（`RoomMsgUser` 在唤醒路径只允许作为寻址前置的形态判断，不得作为唤醒必要条件）。
8. **承重安全属性**：「非成员不能写」必须有一支能变红的测试锁住（反例 3），不能只因当前实现恰好拒绝；pointer 豁免不得扩大成「系统身份可写会话」——`pointerActor` 只在 `Service.Pointer` 内部使用，加反例锁死。
9. **webview 候选族**：无，因为本卡不触 d_web。

### 3.3 S2（d_collab）：详情页投影精化

**①契约引用**：contract §3.2（SessionNode/SessionTimelineEvent/成员状态词表）、§4.2（条目 14–18）、§4.3（条目 31）、§8.5、§9 附区（driver_takeover 归属判据）；spec §4.3 末条、§4.4；本稿 §2.4 澄清 3/发现 A。

**②意图与为什么**：详情页三块的「可证实」与「归属正确」：timeline 只装本会话的结构事实（修 Ticket 0 的全量卡表归属偏差）；needs_human 亮起进 timeline（冻结常量已在，投影未消费）；成员状态逐条按「看板不说谎」执法。被 R2/R3 阻塞的 seat_bound 与卡收口条目**不进本卡**，处置后另补。

**③验收（行为化）**：

- `go test ./internal/collab -run 'TestSession' -count=1` 退出 0，新增反例：卡 A ∈ 会话 1、卡 B ∉ 任何会话，B 落 `driver_takeover` 后，会话 1 的 timeline 不含该行、会话 2 的 timeline 也不含（跨会话污染反例，修复发现 A）。
- `needs_human` 事件（卡 ∈ 会话）后，`SessionDetail.Timeline` 出现 `kind=needs_human` 行；`needs_cleared` 后 `Summary.NeedsHuman` 翻 false（label 消失有测试）。
- 成员状态：测试经 `Store.RenewDriverLease`（测试专属生产者）造未过期租约 + 注入时钟 → `working`；拨钟过期 → `last_active`（**同一注入时钟**，contract 条目 18——时钟不同源的写法判 fail）；空座卡 → `empty`；断言四值词表外不出现任何其它状态串（含「online」）。
- `SessionNode`：卡 ∈ 会话的 `task_mirrored`（含 node 字段）聚合进 `Nodes`，卡 ∉ 会话的不出现；`Round`/`Target` 词表位允许空（填法归 plan，envelope 无 round 字段，不许为填空造新账本读）。
- `go test ./internal/proto -run TestSessionsFixture -count=1` 保持绿（投影不新增 wire 键）。

**④入口指针与有界文件集**：`internal/collab/sessions.go`、`internal/collab/sessions_test.go`；符号锚：`internal/collab/sessions.go#sessionTimeline`、`internal/collab/sessions.go#sessionMembers`、`internal/collab/sessions.go#memberStatus`、`internal/collab/sessions.go#sessionNodes`、`internal/collab/sessions.go#needsHumanByCard`。

**缺陷族对抗**：

1. **生命周期**：会话归档后详情投影仍可读（读侧不受归档影响）——加断言；无孤儿资源，因为纯投影无写。
2. **静默失败**：`task_mirrored` envelope 解码失败/无 node 字段时跳过该行（既有行为）但**不得**静默吞掉整卡节点——逐事件 continue 保留其余；解码失败要留日志。
3. **跨平台**：无，因为纯 Go 投影。
4. **假红/假绿**：跨会话污染反例必须先红后绿（先跑 Ticket 0 代码确认能红）；时钟断言必须证明用的是注入钟（拨钟即翻），否则「可证实」判据在真机上失真——夹具全绿验证假世界正是本族要防的。
5. **门禁绕过**：不适用（只读投影），因为本卡无新写路径。
6. **序列化边界**：`SessionDetail` 的 JSON 键集由金样本锁；`driver_takeover` payload（`{from,to}`）进 `Detail` 字符串是透传——断言其原样保留（可对质），不做二次解释。
7. **枚举白名单**：timeline kind 消费点（switch）新增 `needs_human` 分支必须与 `proto.SessionEvent*` 词表逐值对齐；未知 ledger 事件类型不得被默认分支收进 timeline（保持 continue）。
8. **承重安全属性**：「不报不可证实的」即本卡的安全属性——`working` 只能由未过期租约推出，测试变异（删掉过期判断）必须变红。
9. **webview**：无，因为不触 d_web（渲染在 S6）。

### 3.4 S3（d_gateway / d_orchestration）：唤醒路径改接寻址

**①契约引用**：contract §3.5（WakeTargets/MessageWakeTargets/AddressesCard）、§4.3（条目 29–31）、§8.1、§9 附区（「不要在 agentd 内重造寻址判定」）；spec §4.3 全节、§6 接缝 #2；退回项 R1（外部半边阻塞）。

**②意图与为什么**：扇出禁令的机内兑现——`automationWakeEvent` 首行卡闸摘除后，会话消息进入寻址判定；广播形状（`decodeHumanRoomMessage` 的 kind==user 无条件唤醒）删除，唤醒 = 寻址命中。席位半边机械闭环：@卡号/回复席位作者 → 定位该卡 → 既有 keystone `WakeMessage` 拉起该卡协调者回合；外部半边（@人 / @主 agent）**在 R1 处置前只入未读 + 留可查日志**。载荷最小化按机内可实现读法：`WakeEvent.Summary` 承载命中条（既有 400 rune 截断），引用条与未读数由被唤醒方唤醒后首拉获得——keystone 类型是域内类型不动；若协调者要求三件套机械入载荷，属 keystone 域内改动，随 plan 定。

**③验收（行为化）**：

- `go test ./internal/agentd -run 'TestWake|TestAutomation' -count=1` 退出 0，全部经真 SQLite 账本 + fake keystone 走 `consumeAutomationEventsOnce`，不许只单测 `automationWakeEvent`：
  - 无寻址会话消息（会话房间、user、无 mentions、ReplyTo=0）→ fake keystone 零 Wake 调用，事件进 seen、游标推进。
  - `@卡号`（卡 ∈ 会话、有席位）→ 恰一次 `WakeMessage{Card:该卡}`，Summary 含命中条正文。
  - 空座 `@卡号`、`@非成员`、`@不存在的卡` → 零 Wake。
  - `reply_to` 指向席位作者的消息 → 唤醒该卡；指向已换绑前的旧席位 → 唤醒**当前**席位（作者身份按当前 driver_session 解析的反例：旧席位身份不再命中）。
  - `by_system`/pointer 消息、`session_created/archived/card_joined/card_left` 结构事件 → 零 Wake（结构事件不得被新代码当成 room_message 处理）。
- 源码级守卫（先例 `internal/agentd/pointer_gate_test.go#TestPointerRouteAbsentFromSource`）：唤醒路径测试断言 wakeconsumer 源不再含「`Kind==user` 即唤醒」形状、唤醒调用前必经 `MessageWakeTargets`/`AddressesCard`；`grep -n "decodeHumanRoomMessage" internal/agentd/` 零命中或该函数已重构为寻址前置。
- 既有护栏保持绿：attach 暂缓、seen/cursor、同卡合并、失败升级不自激（`go test ./internal/agentd -count=1` 全量）。
- 外部半边（R1 前收口态）：@外部身份 → 零 keystone Wake + 日志可查 + 未读游标已含该消息（`ListSessions(member)` 未读数 +1）。

**④入口指针与有界文件集**：`internal/agentd/wakeconsumer.go`、新增 `internal/agentd/wakeconsumer_b358_test.go`（或既有 wake 测试文件）、`internal/agentd/server.go` 仅限依赖装配行；符号锚：`internal/agentd/wakeconsumer.go#automationWakeEvent`、`internal/agentd/wakeconsumer.go#decodeHumanRoomMessage`、`internal/agentd/wakeconsumer.go#consumeAutomationEventsOnce`、`internal/collab/sessions.go#MessageWakeTargets`、`internal/collab/sessions.go#AddressesCard`。

**缺陷族对抗**：

1. **生命周期**：唤醒中途 agentd 重启 → 游标/seen 恢复语义走既有机制，本卡不改；重启后不重复拉起回合**未验证，需真机**（真机清单 #2）。
2. **静默失败**：寻址判定读账本失败（`GetCard` 出错）按契约 §9 落「非卡号」处理——@ 不许因一次读错被吞；keystone Wake 失败走既有失败升级（needs_human + 游标推进），不得静默丢命中条。
3. **跨平台**：无，因为纯进程内消费循环。
4. **假红/假绿**：所有唤醒断言穿真账本事件流 + fake keystone 观测点；「外部目标不唤醒」的反例必须存在（防有人顺手加广播兜底让测试全绿）——这是本卡最高危的假绿温床。
5. **门禁绕过**：唤醒本身不是权限门，但「结构事件不唤醒」是注意力配额的承重属性——加反例锁死（session_* 事件零 Wake）；@ 命中判定不得在 agentd 内重造第二份（grep 断言无 `mentions` 字段的本地解析）。
6. **序列化边界**：`RoomMessage.ReplyTo` 从落账 JSON 到 wakeconsumer 解码的链路——`reply_to` 缺省/0/正值的区分要有用例（omitempty 键缺失 ≠ 0）；金样本已锁编码侧，消费侧由本卡用例补。
7. **枚举白名单**：`ledger.EvRoomMessage` 之外的新无卡事件（session_* 四值）必须全部落在「不唤醒」分支——逐值反例（枚举新值过白名单的镜像应用）。
8. **承重安全属性**：「无寻址不唤醒」与「结构事件不唤醒」各一支可变红测试；变异（删掉寻址前置）必须红。
9. **webview**：无，因为不触浏览器面（订阅通道若按 P1 方案 A 走 gateway SSE，属 R1 处置后的新卡，另行过族）。

### 3.5 S4（d_gateway）：会话 HTTP 端点接线

**①契约引用**：contract §3.3/§3.5（门面签名）、§8.3（HTTP 半边）、§9 附区（`ErrBadState`/`ErrNotFound` 网关映射）；spec §4.1/§4.4、§6 接缝 #1/#5 的 gateway 侧。

**②意图与为什么**：把 Ticket 0 已有的门面方法接成控制台可消费的 HTTP 面：会话建/列/详情/归档/拉卡/移出；发言与已读复用既有 `/api/rooms` 端点（会话房间今天已可读写——见台账）。错误映射沿用 `collabErr` 哨兵族，`mapSessionError` 已把 `client.ErrNotFound` 归一为 `collab.ErrNoRoom`。

**③验收（行为化，边界型——机内验契约形状，真实对端归真机）**：

- `go test ./internal/agentd -run 'TestSession' -count=1` 退出 0（httptest 全链：路由 → handler → `collab.Service` → 真账本）：
  - 建会话 → 200 且响应含 `session:<n>` 形 id；GET 列表含该行、`member` 维度未读数正确（发两条无 @ 消息 → Unread=2）。
  - 拉卡进群 → 详情 `cards` 含该卡；对已属他会话的卡 → `ErrBadState` 映射 4xx（状态码按 §9 映射表，plan 定稿后锁定）；对不存在卡 → 404。
  - 归档后：发言 → 409、拉卡 → 4xx 且响应体含可行动错误文案（S1 语义经 HTTP 呈现）。
  - `GET /api/sessions/{id}` 返回键集与 `proto.SessionDetail` 金样本逐键一致（handler 不得手抖改键）。
- 路由命名在 plan 定稿后写进本卡验收清单作为字面断言（`--help`/路由表快照），定稿前不得两处（handler 注册与测试）各自发明。
- `go test ./internal/agentd -count=1` 全量绿（房间旧端点回归不破）。

**④入口指针与有界文件集**：`internal/agentd/roomsapi.go`（或新 `sessionsapi.go`）、`internal/agentd/ledgerapi.go`（注册行）、对应 `_test.go`；符号锚：`internal/agentd/roomsapi.go#collabErr`、`internal/agentd/roomsapi.go#handleRoomSend`、`internal/collab/sessions.go#mapSessionError`、`internal/agentd/ledgerapi.go:49-53`（注册块读数）。

**缺陷族对抗**：

1. **生命周期**：无新后台资源（handler 无状态），因为投影与游标都在 collab/账本侧。
2. **静默失败**：`ErrNoRoom` 对会话列表/详情必须是 404 且文案含会话 id；`History` 的 404 分支今天不可达（读侧宽容）——不得为本卡新写「声称验证它」的假测试（roomsapi.go:296-300 既有裁决沿用）。
3. **跨平台**：无，因为 httptest 形状与 OS 无关；真实浏览器/跨机转发归真机。
4. **假红/假绿**：测试必须穿真实 mux 注册（不是直调 handler 函数），否则路由没接上测试照样绿——这正是「空壳冒充接线」的形状。
5. **门禁绕过**：actor 一律服务端注入（`roomUserActor` 既有先例），请求体不带身份字段——加反例（请求体塞 actor 被忽略/拒绝）；鉴权沿用 agentd 既有 middleware，不新增旁路。
6. **序列化边界**：Go DTO → HTTP JSON →（S6 的 TS 镜像）跨语言链，机内以金样本锁 Go 侧；跨语言整链回归在 S6 孪生金样本补——两侧各自绿 ≠ 链路有测试，S6 验收含 fixture 逐键比对。
7. **枚举白名单**：错误码映射（哨兵 → HTTP 状态）逐哨兵用例；新增 `ErrBadState` 映射不得与其他哨兵混流。
8. **承重安全属性**：无新权限模型（鉴权沿用）；`member` 维度未读以服务端注入 actor 为准，加「伪造 member 查询参数不影响他人未读」反例。
9. **webview**：无，因为本卡是 HTTP 形状层。

### 3.6 S5（d_cli）：session 命令族

**①契约引用**：contract §3.5、§8.3（CLI 半边）；spec §4.2（以自己名义发言）、§6 接缝 #1 的 CLI 调用方；岔口 P7（命名）、P4（身份记法）。

**②意图与为什么**：给人与主 agent（外部会话）一条不用控制台的会话入口：list/detail/create/archive/join/leave/send。协调者以席位身份「以自己名义」发言要走得通——现有 `room send --kind user` 拒绝 `--cli/--session` 的旧规则随矩阵废止一起收敛。

**③验收（行为化）**：

- `go test ./cmd -run 'TestSession' -count=1` 退出 0（命令级测试，穿 cobra → `roomServiceFor` → 真账本）：
  - `session create --title T` → stdout 含 `session:<n>`；`session list` 行含标题/未读/需要你标签列；`session detail <id>` 三块（成员/节点/timeline）逐列可读。
  - `session join <id> <卡号>` 幂等（重复执行退出 0）；对已属他会话的卡退出非 0 且 stderr 含「已属会话」类可行动文案。
  - `session send <id> "…"`（人）→ actor=`cli:<user>@<host>`；`session send <id> --cli opencode --session <sid> "…"`（协调者）→ actor=席位身份、kind=user——旧守卫的替代形状是「自报身份需成对 flag」，机内断言两形态落账 actor 可查。
  - 归档会话 send/join → 非 0 退出 + `ErrReadOnly` 文案。
- `handoff room read <卡号>`（旧面）对旧房间仍可读；`handoff room send <卡号>` → 非 0 退出（S1 归档语义经 CLI 呈现）。
- 退出码契约：成功 0、用法/存在性错误非 0，沿用 cmd 族既有约定（与 `card wait` 的 124 特例不冲突，本卡不引入新超时）。

**④入口指针与有界文件集**：`cmd/room.go`、新增 `cmd/session.go`、`cmd/room_test.go`/`cmd/session_test.go`（按 P7 拍板定文件名）；符号锚：`cmd/room.go#roomServiceFor`、`cmd/room.go#openRoomService`、`cmd/room.go:124`（旧 kind=user 拒绝 flag 的读数）。

**缺陷族对抗**：

1. **生命周期**：命令一次性进程，无孤儿资源，因为账本连接 `defer st.Close()` 既有。
2. **静默失败**：错误一律非 0 退出 + stderr 可行动文案（会话号打错、卡不在会话、归档只读）；禁止「打印 ok 但账本没写」——send 成功路径以账本 seq 回显为准。
3. **跨平台**：无，因为纯 Go CLI；HOME/actor 注入沿用 `ledgercli.go` 既有约定。
4. **假红/假绿**：穿 cobra 命令对象与真账本（同 S4 理由）；`--cli/--session` 成对校验的反例（只给一个 flag → 拒绝）必须存在。
5. **门禁绕过**：席位自报身份的信任面与 S1 第 5 条同基线；CLI 不提供「冒充他人成员身份」的直通 flag（身份只能来自 `ledgerActor()` 或成对席位 flag），加反例。
6. **序列化边界**：CLI 输出（tabwriter/json）是手写投影——断言 `session list --json`（若提供）可被 `json.Unmarshal` 回 `SessionSummary` 键集，不做无测试的格式发明。
7. **枚举白名单**：不适用，因为本卡不新增枚举值。
8. **承重安全属性**：无新增（身份注入面沿用）。
9. **webview**：无。

### 3.7 S6（d_web）：控制台会话内容面

**①契约引用**：contract §3.2（wire DTO）、§7.3（TS 孪生金样本欠账）、§8.4；spec 实现决定 1–4、§4.4；北极星 sessions.html + README W1–W6；S4 的端点面。

**②意图与为什么**：北极星落地：会话页一等公民（不占 tab 条、不挂面包屑、无右栏文件树，中央区整块）、左栏保留项目树、dock 入口；三态（列表/群聊/详情）；列表行 = 谁需要我（未读 + needs_human 标签 + 预览），群聊面只有人话 + 引用条 + @高亮 + 卡 chips（空座虚线），详情页三块（成员状态 / 任务节点 / timeline）。TS 镜像补 Session DTO 全集与孪生金样本（`testdata/RoomsFixture.json`，与 `rooms_fixture_test.go` 逐键一致）。旧 RoomPanel 的退役方式（原地改造 vs 新页面 + 移除旧入口）归 plan，以 W1 形态为准绳。

**③验收（行为化，逻辑型——组件/金样本机内闭环，真实浏览器归真机）**：

- `cd web && npx vitest run src/api/rooms.test.ts` 退出 0：TS `Session/SessionMember/SessionSummary/SessionDetail/SessionTimelineEvent` 与 Go 金样本同源 JSON fixture（`testdata/RoomsFixture.json`）逐键断言，含 `reply_to` omitempty 三态（缺键/0/正数）与成员状态四值词表。
- `npx vitest run src/app/rooms` 退出 0，组件测试至少覆盖：列表行渲染未读数与 needs_human 标签（fixture 驱动）；群聊消息 @mention 高亮与回复引用条（点引用条跳被引用消息）；空座卡 chips 虚线 + 「还没配人」提示；详情页成员状态只渲染词表四值（`online` 字样出现即 fail——反例断言）。
- Shell 结构断言：会话页路由挂载后不渲染面包屑与右栏文件树（对北极星 W1 的机内可验部分）；dock 入口存在且可导航。
- `npx vitest run`（web 全量）退出 0（旧 RoomPanel 测试随退役同步改写，不留孤儿套件）。
- 真实浏览器三态走查对照 sessions.html（W1–W6 逐条）→ **真机清单 #7，归协调者执行**。

**④入口指针与有界文件集**：`web/src/api/rooms.ts`、`web/src/api/rooms.test.ts`、`web/testdata/RoomsFixture.json`（按 web 测试布局定路径）、`web/src/app/rooms/`（新组件族）、`web/src/app/shell/Shell.tsx`（挂载行）、`web/src/app/homedock/`（入口）；符号锚：`web/src/app/shell/Shell.tsx:808`（RoomPanel 挂载点读数）、`web/src/api/rooms.ts`。

**缺陷族对抗**：

1. **生命周期**：轮询卸载时清理定时器（沿用 `pollInterval` 既有模式）——组件 unmount 反例；无其他常驻资源。
2. **静默失败**：端点 4xx/5xx 渲染可行动错误态（空列表 ≠ 加载失败）；fixture 缺键时测试必须红（键集断言），不许 `undefined` 静默渲染空白。
3. **跨平台**：webview/浏览器差异——会话页不引入新浏览器 API（无 websocket/剪贴板新依赖的前提下列为 plan 检查项）；真实浏览器表现**未验证，需真机**（#7）。
4. **假红/假绿**：fixture 与 Go 金样本同源（同一 JSON 文件），不许 TS 侧手抄一份漂移；「成员状态不出现 online」的反例断言锁「看板不说谎」的前端半边。
5. **门禁绕过**：不适用，因为前端无权限面；member 身份由 gateway 注入，前端不得自报（请求不带身份字段——与 S4 第 5 条镜像）。
6. **序列化边界**：本卡的主战场——跨语言整链（Go DTO → HTTP JSON → TS interface → 组件 props）至少一条测试穿真实 fixture JSON，不mock 掉解析层。
7. **枚举白名单**：TS 侧新增的成员状态/kind 字面量联合类型必须与 Go 词表逐值一致（金样本断言）；timeline kind 渲染 switch 对未知 kind 有兜底（显示原始 kind 串，不白屏）。
8. **承重安全属性**：无新增安全属性。
9. **webview 候选族**：命中——dock 入口、路由挂载、三态切换在真实 Wails/Chromium 的表现**未验证，需真机**（#7）；@高亮、引用条跳转的键盘可达性列入真机走查项。

### 3.8 S7（文档 / 纪律，本仓半边）：升级三档纪律文本与 OOS 登记

**①契约引用**：contract §8.6（repo 半边）、§8.8（roadmap 登记）；spec §2.5（三档边界）、§7（OOS 清单）；岔口 P6。

**②意图与为什么**：纪律没接上就没有生产者（spec 问题陈述第 2 条）。本仓半边把 `skills/handoff/SKILL.md` 的协作房间纪律节从「卡房间 + kind 白名单」改写为「会话 + 寻址 + 三档升级」；把 spec §7 的五项 OOS 登记进 `docs/roadmap.md`（主 agent 内部对话面、成员状态心跳写入路径、多人时代、会话模板与 routines、历史翻页 + 富文本——按 spec §7 原清单逐项）。charter 套件半边（协调者/主 agent discipline 的推翻级判据、巡场拉节奏）出不了本仓文件集，按 P6 归协调者。

**③验收（行为化）**：

- `grep -n "escalation\|deviation\|closing\|relay" skills/handoff/SKILL.md` 在协作房间纪律节范围内零命中（kind 名词表去官僚化，spec 实现决定 8）；`grep -cn "@主 agent\|推翻级\|填补级" skills/handoff/SKILL.md` ≥ 3（三档判据在文）。
- `grep -n "B358" docs/roadmap.md` 命中一节，节内五项 OOS 逐条在文（与 spec §7 清单逐条对得上）。
- 无实现代码改动（`git diff --stat` 限于两文档 + 台账）。

**④入口指针与有界文件集**：`skills/handoff/SKILL.md`（协作房间纪律节，:578 起）、`docs/roadmap.md`（追加一节）。

**缺陷族对抗**：文档卡逐族——1 生命周期：无（纯文档）；2 静默失败：无，因为无运行时行为，但「文档说的与代码做的必须一致」由 integrate 的对账核对（本稿 §4 行为闭环表即对账底稿）；3 跨平台：无；4 假红/假绿：grep 断言锁「旧词清除 + 新词在文」，不锁文风；5 门禁绕过：无，因为纪律文本是门禁本身（charter 半边归协调者是 P6 的显式残余，不是遗漏）；6–7 序列化/枚举：不适用；8 承重安全属性：无；9 webview：无。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的产品行为（北极星 flow.html 四图 + spec §3 用户故事逐条映射）。五格齐全，归属子卡真实存在；「R1/R2/R3」格 = 退回项处置，处置前该行为**不得扇出**。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
|---|---|---|---|---|
| 人/主 agent 开会话说需求 | `Store.CreateSession` → `EvSessionCreated`（无卡事件，Ticket 0） | 会话列表（S4 端点 → S6 页面） | 列表新行（标题/群主）；无人被唤醒 | S4、S6 |
| 人/主 agent 拉卡进群 | `JoinCardToSession` → `EvSessionCardJoined` + `session_cards` 唯一行（Ticket 0） | 详情页卡条（S6）、`SessionOfCard`（归属查询） | 卡条出现，空座虚线「还没配人」；不唤醒（进群≠配人） | S4、S6 |
| 群里无寻址发言 | `Service.Send` → `EvRoomMessage`（`Room=session:<n>`、CardID=""） | wakeconsumer（S3）+ 列表未读游标 | 全员未读 +1、零 Wake；不唤醒任何人 | S1（写）、S3（不唤醒）、S6（未读角标） |
| 协调者/人 发 `@卡号`（有席位） | mentions → `ResolveDelivery` → `cards.driver_session` 当前席位（Ticket 0 纯函数） | wakeconsumer（S3）→ keystone `WakeMessage{Card}` | 该卡协调者回合被拉起，载荷含命中条 | S3 |
| `@空座卡号` / `@非成员` | `resolveSeat` 返空 / 非卡号按外部身份处理 | wakeconsumer | 空座：零 Wake；非成员外部身份：入未读不唤醒（R1 前收口态） | S3（+R1） |
| 人 回复协调者消息（reply_to） | `ReplyTo` → 原作者隐式寻址 | wakeconsumer | 席位作者：唤醒该卡；外部作者：R1 通道 | S3（+R1） |
| 换绑后有人再 `@卡号` | `RebindSeat` 落 `EvDriverTakeover`；解析读当前 `driver_session`（B307 不动） | wakeconsumer | 新席位被唤醒、旧席位不命中（换绑剥权） | S3、S1（旧席位发言被拒） |
| 推翻级：协调者 `@主 agent` | 会话消息 mentions=[主 agent 身份] | **R1 通道（缺）** | 主 agent 醒来看到命中条+引用条+未读数 | **R1 处置后补卡**；R1 前只入未读 |
| 主 agent 判不了 `@人` | 同上（人 = 群主或显式成员身份） | **R1 通道（缺）** | 人被唤醒 | 同上 |
| needs_human 亮起 | 既有 `EvNeedsHuman`（账本词表） | 详情 timeline（S2）+ 列表标签（S2 投影 → S6 渲染） | timeline `needs_human` 行；列表「需要你」标签 | S2、S6 |
| 卡收口或终止 | 既有 `EvStatusMoved`（载体在，kind 值缺） | 详情 timeline | 「卡何时收口」行 | **R3 处置后 S2** |
| 协调者入群（坐下） | **无事件（BindSeat 不落账）** | 详情 timeline | 「谁在何时接手」行 | **R2 处置后 S2** |
| 换绑（席位变更） | `EvDriverTakeover`（{from,to}） | 详情 timeline（S2 修归属） | 仅本会话卡的 `seat_rebound` 行 | S2 |
| 归档会话 | `EvSessionArchived`（Ticket 0） | S1 执法 → S4 状态码 → S6 只读态 | 发言/拉卡被拒；UI 只读 | S1、S4、S6 |
| 看成员状态 | `driver_leases`（生产零写者）+ 注入时钟 | 详情页成员块 | 生产只报 `last_active`/`empty`，绝不报在线 | S2、S6（+真机 #5） |
| 看协调者派发的任务节点 | `task_mirrored` envelope（既有） | 详情 Nodes（S2）→ S6 渲染 | 会话内各卡的派发节点列表 | S2、S6 |
| 会话 @ 的提醒与清理 | `MentionsMember` + 游标 + `EvMessageConsumed` | 收件箱 mention 源、会话未读 | @ 入未读/收件箱；回复即清（B287 延伸） | S1、S3 |
| 主 agent 定时巡场（拉） | `ListSessions(member)` / `SessionDetail` / 卡状态 | 主 agent 外部会话（纪律层） | 标签/成员状态/节点/卡状态可查；不被动作叫醒 | S4、S5、S7（纪律） |

闭环核对结论：除三处 R1/R2/R3 缺载体（已在 §2.3 退回）外，每条承诺行为五格齐、有归属；没有只活在接口、测试或无人认领格子里的承诺。

---

## 5. 未验证，需真机：协调者执行清单

1. **R1 通道（若 P1 方案 A）**：真实主 agent 外部会话被 `@` 后经新通道醒来，看到命中条+引用条+未读数——机内只能验形状与「不唤醒」半边，推醒事实必须真机。
2. **真实协调者回合被 `@卡号` 拉起**：真 keystone + 真 coordinator session + 真 HOME 下，WakeMessage 路径把回合拉起、载荷最小化现场核对（fake keystone 只证明调用形状）。
3. **换绑真机链**：B307 三按钮真机 rebind 后，旧席位真实会话再发被拒、不再被唤醒，新席位命中（跨进程身份事实）。
4. **PG 真库**：`sessions`/`session_cards` 两方言 DDL 幂等迁移；结构事件（无卡）不进 `Store.Follow` 多路 wait 的 PG LISTEN 行为（机内只有 SQLite 佐证）。
5. **成员状态生产诚实性**：真实账本（租约零写者）上详情页成员全部 `last_active`/`empty`、无任何「在线」字样；listening 不出现。
6. **旧房间归档真机读数**：326 个旧卡房间发言被拒、读史可对质、派发指针（pointer）继续落账、旧控制台面退役无回归。
7. **控制台走查 W1–W6**：对照 `sessions.html` 逐条（一等公民页面/dock 入口、群聊干净度、@与引用呈现、详情三块密度、空座卡、与看板/工作台分工）——归协调者执行。
8. **未读游标并发**：CLI 与 gateway 两进程并发 `MarkRead`/读未读下 `room-cursors.json`（tmp+rename）真机行为。

---

## 6. 图覆盖债

`codegraph sym` 对本卡全部触及符号返回「不在图中」（探针记录见台账 §三）：`collab.Service`、`Service.Send`、`Service.Pointer`、`Service.WakeTargets`、`sessionTimeline`、`room.Resolve`、`room.VerifyWriter`、`room.KindAllowed`、`ResolveDelivery`、`wakeconsumer.automationWakeEvent`、`decodeHumanRoomMessage`、`consumeAutomationEventsOnce`、`ledger sessions.go#CreateSession`、`roomsapi.go`、`cmd/room.go` 族、`web/src/api/rooms.ts`。本稿因此一律用 `file#Symbol` 源码锚，收尾跑 `codegraph resolve --doc` 修坏锚。Ticket 0 新符号只存在于 `codegraph/diffs/cards-B358-charter.json`（39 节点，view 相对 diff），`best.json` 结构树未含——实现节点的图对账（recon）须把本卡新增/修改符号补进视图 diff，§3 各子卡的新测试符号同样入债。

---

## 7. 出稿自检

- [x] 产出四样齐全：§1 子系统清单每个带 best.json 类型并过派卡资格四条；§2 契约核对逐条有结论（含 §2.3 三条退回、§2.4 四条澄清）；§3 七张子卡全部四段式且判据行为化；缺陷族逐族有答案（含「无，因为……」，分布各子卡验收栏）。
- [x] 「待拍板」岔口 P1–P7 集中列于稿首 §0，正文岔口一律回指。
- [x] 「未验证，需真机」汇总为 §5 八条。
- [x] 每张子卡有界文件集核过（§3 各④；S3/S4/S5 的 agentd/cmd 切片约束见 §1.1）——圈不出文件集的内容（charter 纪律半边、R1 通道）没有硬塞进功能卡，分别按 P6 路由与 R1 处置。
- [x] 行为闭环 §4 每行五格完整；三条缺载体行为明确标「R1/R2/R3 处置后」，未冒充已归属。
- [x] 收尾动作：`codegraph resolve --doc docs/superpowers/specs/b358-breakdown.md` 跑行与结果记台账（见台账 §六）；坏锚即修（首轮 3 坏锚已修，复跑 23 锚全 ok/moved）。
