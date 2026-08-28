# B279：B156.3 前端控制台——编制配置 + 排队呈现 + 协调者面板 + 开卡即绑

- **卡**：B279（B156.3 前端控制台：编制配置页 + 排队呈现 + 协调者面板 + 开卡即绑（原型先行））
- **级别与档位**：**L3 轻档**（对接 kai/K6 已冻结的跨进程 wire 契约——对接即动契约层，见「契约语义与接缝」）；**用户裁决同 B275 判例直接进 plan**（2026-08-28「老样子，按照之前的流程处理」——B275 裁决原文：wire 增量窄到可在 plan/implement 内冻结，不值 contract/breakdown 两个节点；本卡更窄，新增契约为零）。裁决留痕于此，字段级形状由 plan 写死、review 对照 spec 验收。
- **状态**：用户已批准（2026-08-28 原型三轮走查定稿；同日授权按 charter 自主推进到底并合并回 acc/b156.2-156.3）
- **形态权威**：`prototypes/b279-automation-proto/`（随本卡分支强制入库，file:// 直开零依赖）。**实现与验收一律对照原型代码本身；本 spec 与后续 plan 只写决策与接缝，不用文字转述 UI——任何样式/结构歧义以原型代码为准。**（同 B275 规约）

## 问题陈述

B156.3（三期·自动化层）的后端已按子卡完成并各自留有验收证据，但**三线都未并入任何功能线**：

1. `claude/kai-156-3-ba-df7357`（K1-K4 集成线，53 提交 / 57 文件 / +13256）：编制域（载体+小队）、`/api/squads`、`/api/queue`、`coordinator/launch|status`、`cards/{id}/attach`、`handoff squad` CLI、开卡即绑、`NodeDef.Override.Squad` 点火解析（`internal/agentd/cardstep.go:64` 读 `node.Override.Squad`），以及 TS API 层 `web/src/api/scheduling.ts` + 金样本；
2. `cards/B156.3.5-review-1`（K5，基于 kai）：唤醒消费与排空（wakeconsumer / scheddrain / coordapi），纯后端；
3. `cards/B156.3.6-review-1`（K6，基于 kai、与 K5 平行）：attach/release 契约增量（`CoordinatorAttachInfo` / `CoordinatorStatus`（Bound/AttachActive/Attach 三态）/ `CoordinatorAttachReleaseResp`，`internal/proto/scheduling.go` diff 尾部）+ 一版**未经原型走查的** web 实现（独立路由页 SchedulingPage + QueuePanel + CoordinatorPanel，朴素表格风）。

前端形态缺位。原型 `prototypes/b279-automation-proto/` 已三轮走查定稿，本卡把它落进真实前端——而真实前端要对接的后端不在 acc 上，**基线合并是本卡的前置动作**（B275 以 ff 合入 feature 线为基线动作的同类判例；本次三线分叉，是真实合并而非 ff）。

## 方案

**基线动作**：kai → acc、K5 → acc、K6 → acc 按序并入（K5/K6 同改 `internal/agentd/coordapi.go`，后并者解冲突）；**K6 的 `web/` UI 文件在合并中舍弃**（取 acc 侧），其 wire 类型、金样本与测试资产保留。合并验证：全量测试绿 + agentd 真实起服后五端点与 attach/release 应答（真机）。

**前端**：以原型代码为唯一形态权威复刻进 `web/`。kai 的 `web/src/api/scheduling.ts` 为 API 层起点，按 K6 的 proto 增量补 `CoordinatorStatus` / `CoordinatorAttachInfo` / `CoordinatorAttachReleaseResp` 类型与金样本。

弃选：

- **沿用 K6 旧 web UI 改写**：否。信息架构不同（独立路由页 vs 原型的三处嵌入：设置分区 / 看板横带 / 抽屉区），且形态未经走查；保留其契约与测试资产，UI 整体重写（用户 2026-08-28 走查时确认原型路线）。
- **排队做成看板第六列**：否（原型走查裁决）。队列是调度瞬时态，不是卡的生命周期列；形态为 toolbar「⧗ 排队中 N」开关 + 就地横带。
- **节点标签全卡常显**：否（原型走查裁决）。节点即工作流节点（`NodeDef.Name` 即状态名，`internal/ledger/types.go:212`；`card dispatch --step 进行中` 的取值域即节点名，见 `cmd/card_dispatch_test.go:348`），列与节点一一对应时列本身就是标签；只有多对一映射的列（默认映射下「审核中」← 待审阅/待合并）列答不出节点，标签才显形——与看板「保真信号沉默」原则一致。
- **flows 页自造第二套节点清单**：否（原型走查第三轮纠正）。节点就是工作流状态机里已定下的节点，不可下拉、不可增删（改节点 = 编辑工作流产新版本）；「看板列映射」（B265 已落 `web/src/app/flows/FlowsPage.tsx:167`）与小队绑定合并为一张「节点编排」表。

## 用户故事

1. 作为协调者，我在设置页有「自动化」分区（`?section=automation` 直达，沿用 `SettingsPage.tsx:43` 的 update 深链先例），看到载体卡列表（HOME/默认模型/凭据/并发上限/健康点/版本号）与小队卡列表（member、政策位并发、绑定对象说明）。
2. 作为协调者，我能登记/编辑载体与小队（弹窗表单，CAS `expect=version` 语义有明确冲突反馈）。
3. 作为协调者，我在看板工具条点「⧗ 排队中 N」展开调度横带：位次/卡号（可点回卡）/类型 badge（拉起/点火）/节点·小队/优先级/就绪态/入队 actor；排队中的卡带「⧗ 排队 #N」chip。
4. 作为协调者，我在「审核中」列的卡右上角看到当前节点胶囊（待审阅/待合并）；其余列不出现（列即标签）。
5. 作为协调者，我在卡抽屉看到协调者区三态：未绑定（虚线卡 + ▶ 拉起协调者）/绑定中（协调者卡 + 打开终端）/人工接管中（amber badge + 交回无头）。
6. 作为协调者，我点「打开终端」弹确认框（注明 attach 与自动唤醒互斥），确认后在该卡目录打开终端 tab 并填入 attach 命令（复用 B275 的 `init_command` 通道）。
7. 作为协调者，我新建工作项时可勾「创建后拉起协调者并绑定（开卡即绑）」。
8. 作为协调者，我在 flows 页用一张「节点编排」表按节点配看板列与小队（节点列固定；小队下拉含「无（不派发）」），hint 指向设置·自动化。

## 契约语义与接缝

本卡对接的 wire 契约**全部已在 kai/K6 冻结**，本卡新增契约为零；对接侧以金样本钉死形状：

- 编制：`GET/PUT /api/squads`（`CarrierView` / `SquadView` / `CarrierInput` / `SquadInput` / `SquadPutResp`，kai `internal/proto/scheduling.go:11-63`）；CAS 语义：PUT 带 `expect=version`，冲突返回明确错误。
- 队列：`GET /api/queue`（`QueueEntry`，kai `internal/proto/scheduling.go:69-87`）；`position` 全局 1 基，清队顺序恒为拉起先于点火；`ready`/`priority` 为入队快照。
- 协调者：`POST coordinator/launch`（kai）、`GET coordinator status` 与 attach/release（K6 proto 增量：`CoordinatorStatus` 的 Bound/AttachActive/Attach 三态、`CoordinatorAttachInfo` 定位三元组由服务端产生、客户端不得拼接改写）。
- 节点小队绑定：读写 `NodeDef.Override.Squad`（kai 已交付点火解析），工作流版本化语义沿用（改绑定 = 产新版）。
- 看板列映射：B265/B275 已交付（`FlowsPage.tsx:167`、`internal/ledger/workflows.go:17` 默认五列），本卡只在同一张表上扩列，不改其语义。
- **plan 第一刀查明项**：web 建卡请求的 wire 是否已有开卡即绑字段（kai K4 已交付服务端语义）；缺则按 B275 `init_command` 先例做镜像补全（只加不改），形状由 plan 写死。

边界声明：唤醒事件清单、人工门、开场评估、隔离 HOME 等 B156.3 spec（`docs/superpowers/specs/2026-08-26-b156.3-automation-keystone-design.md` §3-§7）已定语义全部沿用，不在本卡重议。

## 实现决定

形态实现**全部以原型代码为准**，以下为决策级条目（用户可见的名字与行为）：

1. **基线合并**：kai + K5 + K6 三线并入 acc（顺序 kai→K5→K6）；K6 的 `web/` UI 文件（SchedulingPage/QueuePanel/CoordinatorPanel 及其对 CardsPage/CardDrawer 的旧式改动）舍弃取 acc 侧，wire/金样本/契约测试保留。分支保留不删。
2. **设置·自动化分区**：设置页 nav 加「自动化」（位于执行纪律之后），`?section=automation` 直达；载体卡列表 + 小队卡列表 + 登记/编辑弹窗，字段与 mock 反馈文案照原型 `pages/settings.html`（含 CAS 版本号语义）。
3. **看板排队横带**：工具条「⧗ 排队中 N」开关就地展开/收起横带（行结构照原型 `pages/board.html`），卡片「⧗ 排队 #N」chip；数据 `GET /api/queue`，轮询节奏归 plan。
4. **节点标签**：列→节点多对一映射的列才在卡右上角出深色胶囊（节点名），抽屉头部同规则；多对一的判定从看板列映射**派生**，不写死「审核中」。
5. **卡抽屉协调者区**：三态重写（无绑定拉起 / 绑定中打开终端 / 接管中交回无头），wire 用 K6 `CoordinatorStatus` 三态 + launch/release 端点；attach 确认框注明互斥，确认后开终端 tab 填命令。
6. **新建工作项**：弹层加「创建后拉起协调者并绑定（开卡即绑）」checkbox + 开场评估说明文案（照原型）。
7. **flows 节点编排表**：B275 的「看板列映射」表扩为「节点编排（工作流节点 → 看板列 · 派发小队）」一张表——节点列固定（读工作流 def），小队列下拉（「无（不派发）」+ 执行者小队；拉起通道行只能绑协调者队），读写 `NodeDef.Override.Squad`；形态照原型 `pages/flows.html`。
8. **API 层**：引入 kai 的 `web/src/api/scheduling.ts` 与金样本，按 K6 proto 增量补类型；fetch 层错误语义（CAS 冲突 / 未绑定 / 网络）照原型 mock 反馈文案呈现，不许静默。

## 测试决定（接缝清单）

最高可测缝 = **web api fetch 层**（金样本，既有先例 `rooms.fetch.test.ts`、kai 已带 `web/src/api/testdata/*.json`）与 **React 组件层**（vitest + testing-library）。契约零新增，对接形状由金样本双侧钉死。

- 缝 1：`web/src/api/scheduling.ts` fetch 层 —— 调用方设置·自动化分区、排队横带、抽屉协调者区；kai/K6 金样本过 TS 镜像解析。
- 缝 2：设置·自动化分区组件 —— 调用方 SettingsPage section 渲染（`SettingsPage.tsx:93-97` 同位扩展）；列表渲染、弹窗 CAS 反馈、`?section=automation` 直达钉死。
- 缝 3：排队横带与卡 chip —— 调用方看板页；横带行渲染、开关显隐、卡号回跳、`⧗ 排队 #N` 与位次一致性钉死。
- 缝 4：节点标签显形逻辑 —— 调用方看板卡与抽屉头部；多对一列派生判定（改映射后显形集随之变化）钉死。
- 缝 5：抽屉协调者区三态 —— 调用方 CardDrawer；`CoordinatorStatus` 三态到三 UI 态的映射、attach 确认框互斥文案、交回无头回执钉死。
- 缝 6：flows 节点编排表 —— 调用方 FlowsPage（既有「看板列映射」段 `FlowsPage.tsx:167` 同位扩展）；节点列固定不可选、小队读写 `Override.Squad`、拉起通道行只列协调者队钉死。
- 缝 7（契约）：kai 合入后 Go 侧既有金样本（`internal/proto/contract_fixture_test.go` 等）全绿即对接凭证；若查明项触发建卡字段补全，双侧金样本同步锁。

假缝核对：面板内部纯呈现 helper 不占缝名额，归 plan 的内部锁候选。

## Out of Scope

- **永不做**：K6 旧 web UI 形态（独立 SchedulingPage 路由页 / 朴素表格风）——资产保留、形态不复活。
- **本期不做**：规则引擎、唤醒事件流、approver 链路的任何前端呈现（K5 的 wakeconsumer 无前端面）；排队横带之外的调度可视化（历史队列、清队动画）。
- **本期不做**：载体/小队登记的机器发现与自动预填（表单手填，照原型）；小队成员拖拽排序。
- **本期不做**：B156.3 其余子卡的 integrate/finish 节点仪式本身——本卡只做基线合并这一动作并留痕，B156.3 的收口归属其自身流程。

## 备注

- 基线合并留痕：kai 与 acc 分叉点 06647d442（acc 侧 40 提交 = B275 等，kai 侧 53 提交）；K5/K6 均基于 kai、互相平行，K5 纯后端、K6 带被舍弃的旧 UI。冲突高发面：`internal/agentd/coordapi.go`（K5/K6 同改）、`web/src/app/cards/*`（K6 旧改动遇 B275 新形态——一律取 acc 侧）。
- 原型随卡分支入库用 `git add -f`（`prototypes/.gitignore` 只放行 base/；B275 已破例先例）。
- 过程台账：`docs/superpowers/ledgers/2026-08-28-b279-spec-ledger.md`。
