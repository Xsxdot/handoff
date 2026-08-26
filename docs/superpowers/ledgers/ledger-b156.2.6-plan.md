# 台账 ledger-b156.2.6-plan.md（B156.2.6 C6 gateway HTTP 面与收件箱编排）

> 本文件是 C6 plan 轮的边干边落台账（charter-plan 纪律块红线）。每确立一个事实
> 追加一行，含命令与原始输出；放弃的尝试与判断同记。与产出物
> `docs/superpowers/plans/b156.2.6-plan.md` 同批提交。

## L1 基线确认（06647d442）

- 分支 `cards/B156.2.6-charter`，HEAD=06647d44（C5 已合入），工作树干净。
- `git log --oneline -1` → `06647d44 merge(b156.2): C5 消费恰好一次与注意力读模型...`
- `go build ./...` EXIT=0。
- `go test ./internal/agentd/...` → `ok github.com/Xsxdot/handoff/internal/agentd 62.027s`。
- `go test ./internal/collab/...` → `ok`（client/cursor/room 无测试文件）。
- `go vet ./internal/agentd/...` EXIT=0；`gofmt -l internal cmd` 零输出。

## L2 图闸基线

- `go run . graph validate --repo .` EXIT=0（fails 空）。
- `go run . graph check --repo . --view cards-B156.2-charter-4` EXIT=0：
  `fails: 0`；`legacyHits["d_gateway->d_ledger"]=1`（预算内直调 1/17）；
  bestCoverage 257/257/1020/114。
- 注：check 报「预算棘轮判据已跳过（基准 70d243f5 无 target.json）」——不是判据，
  是警告。

## L3 legacy 调用点计数（岔口二条件 1 的事实调查）

- 代码调用点口径：`grep -roE "s\.ledger\.[A-Za-z]+\(" internal/agentd/*.go | grep -v _test.go`
  = **37 处调用点（34 个不同方法）**。新增收件箱 decision 源的一处 `s.ledger.ListDecisions(...)`
  后 = **38 处**。改动前后计数 = 37 → 38（+1）。
- 图口径：新增的 `handleInbox → Store.ListDecisions` 边目标容器 `k_ledger_Store`
  （label `ledger.Store`，在 d_gateway→d_ledger 契约 entries 内），**不进 legacyHits**——
  所以 `graph check` 的「预算内直调 1/17」改动前后**都保持 1**。债 +1 只体现在代码
  调用点口径，图闸看不见。判据来源：读 charter/graph@v0.8.0/codegraph/check.go:100-107
  （entries 覆盖的边不进 LegacyHits）。
- 结论写进计划：岔口二条件 1 的「债要可见」按代码调用点口径记 37→38，同时写明图口径不变。

## L4 unsafe.Pointer 计数（路由缺席断言实现陷阱）

- `grep -rn "unsafe.Pointer(" --include="*.go" . | grep -v _test.go | wc -l` = **6**。
- 分布：`internal/prochost/taskmark_darwin.go` 1 处、`internal/prochost/platform_windows.go` 5 处
  （`grep -c` 确认 1 / 5）。与协调者给的事实一致。

## L5 Service.Pointer 仓内引用点（白名单判据现状）

- 全仓非测试 `.go` 里 `.Pointer(` 且非 `unsafe.Pointer(` 的调用**只有一处**：
  `internal/agentd/server.go:2349`（`roomNarrator.Say` 的 `n.c.Pointer(cardID, proto.RoomMessage{Body: text})`）。
- `roomNarrator.Say` 在 server.go:2348-2351，类型 `roomNarrator{c *collab.Service}`（:2344-2346）。
- 故判据 (a) 在基线**今天就是绿的**；「它会不会红」只能靠正控 (b) 证明。

## L6 collab.New 无图节点（事实 + 闸门行为探针）

- `go run . graph sym --repo . collab.New`（无 view / 带 charter-4 view 都）：
  `Error: 符号 "collab.New" 不在图中 ... 近似候选: []` → exit 1。与协调者事实一致。
- 悬空引用探针（临时 diff，验完删除，git status 干净）：
  - 注入 `edgesAdded: [n_agentd_Server_handleDecisions, n_probe_DoesNotExist_Node]` +
    `nodesAdded: n_probe_New_Node`（k_agentd_Server 下）。
  - `graph validate --repo .` EXIT=1，报文：`[probe-dangling] diff 边
    n_agentd_Server_handleDecisions→n_probe_DoesNotExist_Node 引用未知节点 n_probe_DoesNotExist_Node`。
  - `graph check --repo . --view probe-dangling` EXIT=1，报文：`视图 probe-dangling
    引用不完整: [diff 边 ... 引用未知节点 ...]`。
  - 基线两者均 EXIT=0。探针文件已删，工作树干净。
- 结论：闸门抓「声明之间自相矛盾」，抓不住「现实里有而没申报」。两条正当出路选一——
  **计划裁决选①**（视图 diff 补 collab.New 节点）：包级构造器有户口是本仓惯例
  （cursor.New 就有节点），且 SetupAutomation 装配边需要它才不悬空。

## L7 预定声明 vs 实际 label 探针（本计划最重要的图事实）

- 背景：契约 §2.3 预定声明 entries 原文「collab.Service 与入站门面实体」；实际容器
  label：`k_collab_Service -> "collab 入站门面"`（baseline.json containers 亲读）。
  B156.3 已声明的 `d_keystone→d_collab`（target.json:146-156）entries 用的是实际 label
  「collab 入站门面」。
- 探针 A（逐字照抄）：target.json 加 `d_gateway→d_collab, entries:["collab.Service 与入站门面实体"]`
  → `graph check --view cards-B156.2-charter-4` EXIT=1，fails 两条：
  1. `dead-contract | d_gateway -> d_collab | 契约 d_gateway→d_collab 声明的方向没有活跃
     call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）`
  2. `dead-entry | d_gateway -> d_collab | 契约 d_gateway→d_collab 声明的入口
     "collab.Service 与入站门面实体" 在 d_collab 中不存在（无同 Label 容器...）`
- 探针 B（实际 label）：entries:["collab 入站门面"] → EXIT=1，仅剩 `dead-contract` 一条
  （dead-entry 消失）。
- 结论：
  1. **「同一提交」是承重要求**：dead-contract 证明预定声明没有活跃边必红——必须先有
     消费边（视图 edgesAdded）才能写契约。计划把 target.json 契约 + 视图边同 commit。
  2. **entries 逐字照抄 §2.3 会 dead-entry**：`graph check` 的 entries 按容器 label 精确匹配
     （check.go:135 `container.Label != entry`）。计划写成实际 label `["collab 入站门面"]`
     （B156.3 先例），并在计划「图事实」节显式记录这个偏离与理由。这与我方指示
     「entries 逐字照抄契约 §2.3」冲突——以实测为准，冲突写进 plan 与 verdict notes。
  3. C7（d_cli→d_collab）同文，C7 计划同样要用实际 label。

## L8 SetupAutomation 组装现状

- `internal/agentd/server.go:2236-2246` `SetupAutomation(st *ledger.Store)`：创建
  `facade := ledgerapi.New(st)`、`s.scheduling = scheduling.New(...)`、
  `rooms := collab.New(facade)`（局部变量）、`s.keystone = keystone.New(runner, roomNarrator{c: rooms}, ...)`。
- **全仓无任何调用点**（`grep -rn SetupAutomation --include=*.go . | grep -v _test` 只命中
  定义与注释）。生产/测试都没调——rooms 面不经装配就是死的（handler 需 503 兜底）。
- Server 结构体无 collab 字段（:152-157 只有 scheduling/keystone/autoLedger/ptyGate）。
- 裁决（D-assembly）：C6 在 SetupAutomation 里存 `s.rooms`（复用同一实例给 roomNarrator）、
  接线游标 `SetCursorStore(cursor.New(filepath.Join(s.conf().DataDir, "room-cursors.json")))`；
  新增 `SetRooms` 测试缝（SetScheduling/SetKeystone 同形）；**生产激活 = cmd/agentd.go
  setupLedger 后补一行 `srv.SetupAutomation(lst)`**——有界文件集由 breakdown 的 5 文件扩到
  6（+cmd/agentd.go），理由：SetupAutomation 是全仓唯一无人调用的组装点，不激活则六端点
  生产全 503；scheduling/keystone 构造零副作用（骨架期注释自证），激活安全。
- `s.conf()` 存在（ledgerapi.go:221 用）；`cursor` 包需新 import；`ledgerapi` 已 import。

## L9 六端点与错误映射落点（复用先例核对）

- 路由落点：`registerLedgerRoutes`（ledgerapi.go:30-52），`api.HandleFunc` 字面量直接打在
  `*http.ServeMux` 上（与 GET /api/decisions 同形）。Handler() :544 调用它。
- 投影先例：`ledgerEventWire`（ledgerapi.go:106）、`ledgerDecisionWire`（:157）、
  `ledgerCardViewWire`（:142）、`writeJSON`（server.go:2208）、`writeErr`（ledgerapi.go:82）、
  `ledgerErr`（ledgerapi.go:66，含 ErrCASConflict→409）。
- collab 哨兵再导出在 `internal/collab/service.go:37-43`（ErrNoRoom/ErrKindNotAllowed/
  ErrReadOnly/ErrNotWriter/ErrNotBound）；collabErr 翻译表照 §3.3+§3.5：ErrNoRoom→400
  （History 特例→404）、ErrKindNotAllowed→400、ErrNotWriter→403、ErrReadOnly/ErrNotBound→409。
- actor 注入：`"web:"+hostOnly(r.RemoteAddr)`（hostOnly 见 hostguard.go:236；ledgerapi.go:458
  的 step 补 actor 先例）。
- 收件箱三源数据面：decision=`s.ledger.ListDecisions(true)`（岔口二方案 A，ledgerapi.go:670
  同形）；ticket=`s.st.ListTasks()`+`PendingTickets(taskID)`（store.go:409/:1251）+
  `s.hub.Watchers`（hub.go:118）；mention=`s.rooms.Mentions(成员,0,0)`。recentEventsLimit=100
  （server.go:65）供破坏性判定的事件扫描。

## L10 破坏性工单判定（D-destructive 裁决）

- 现状：proto.Ticket 无任何机械「破坏性」标记（proto.go:374-391 亲读）；permgate 黑名单
  升级（Escalate, Rule 非空）与审批者升级都经 `escalatePermission` 建工单，工单上不落
  分类。唯一可查询的信号 = task 事件流里的 `approver_decision` 事件（proto.go:70
  EventTypeApproverDecision；manager.go:607-615 approverDecisionPayload{TicketID,Decision,
  Reason,ElapsedMS}；decision 取值 approve/escalate/error）。
- 裁决：破坏性 = 该工单 task 事件流存在 `approver_decision`（ticket_id 匹配且 decision∈
  {escalate,error}）。理由：approver 是系统对权限请求的第一道自动风险过滤（fail-closed，
  approver.go:8「一律按 escalate，绝不静默放行」），它不敢放行/裁决失败 = 系统标记的
  「危险请求」的机械代理；「破坏性/不可逆/外部可见一律升级人工」硬纪律在代码里的落点
  就是 escalate/error。黑名单直通升级与 ask 工单不判破坏性、受 Watchers 过滤。
- 已知边界（写进计划）：黑名单直通（未咨询审批者）的工单按此规则不判破坏性——该路径
  无 approver_decision 事件。可接受：这类请求由协调者驱动场景仍经协调者处理，收件箱是
  兜底面；要补需回 contract 动冻结物，不在本期。
- 测试构造：`env.st.AppendEvent("t1", proto.EventTypeApproverDecision, approverDecisionPayload{
  TicketID:"t1:p1", Decision:"escalate"})` 直接在真实 store 造信号。

## L11 换绑端点的合规通道（D-rebind 裁决）

- 矛盾：RebindDriver 是本期新增账本能力 → 岔口二条件 2「一律走门面」；条件 4「gateway
  的 handler 不得引用账本门面（internal/ledger/api.Facade）」。collab.Service 方法集（契约
  §3.3）无 rebind 方法，且 collab client 接口的 BindDriver 是 d_collab 出站口、gateway 引
  用会构成未声明 entry 的 d_gateway→d_collab 边（预算 0 即超）。
- 裁决：gateway 侧定义消费端口 `rebindPort interface { Rebind(id,toSession,carrier,expect string) error }`，
  组装点 server.go 构造适配器 `facadeBindAdapter{f *ledgerapi.Facade}`（单一 Facade 字段、
  方法体只转调 `f.BindDriver`），SetupAutomation 注入 `s.rebind = facadeBindAdapter{f: facade}`；
  测试经 `SetRebind` 替换。条件 4 机械判据三条核对：一个 Facade 字段 ✓、纯转调 ✓、
  由组装点注入消费端口 ✓（「另一个子系统消费侧端口」按「端口接口定义在 gateway 消费侧、
  适配器只出现在组装点」的窄读执行——这是条件 4 的裁决边界，写入计划决策记录）。
- 图：适配器方法文件=server.go（assembly 登记点）→ 其 →Facade.BindDriver 边走组装点豁免
  （check.go:92-96「组装点豁免」），不构成违规新方向。Facade.BindDriver 节点当前不在图
  （C1/C2 覆盖债），视图 diff 需补 `n_ledger_api_Facade_BindDriver`。

## L12 视图 diff 需补的 Facade 节点与覆盖债

- `k_ledger_api_Facade` 现有节点（baseline 亲读）：m_ledger_api_Facade +
  Put/Get/List/Delete/EffectiveBaseBranch/MarkNeedsHuman。**BindDriver 不在**（C1 加了方法没
  补图）。C6 的 rebind 适配器边需要它，视图 diff 补 `n_ledger_api_Facade_BindDriver`。
- `k_collab_Service` 九方法全在（Send/Pointer/History/Pending/Consume/Mentions/ListRooms/
  MarkRead/Unread）——C4/C5 已登记，handler→Service.X 边可直接引用。
- collab.New 节点补入（L6 裁决①）。SetupAutomation 现有边（baseline 亲读）：→
  scheduling.New/keystone.New/hostapi.New/ptyapi.New；**无 → ledgerapi.New、无 → collab.New**
  （构造器节点缺失的既有覆盖债）。

## L13 白名单守卫测试（判据 (a) 的机制与基线读数）

- 机制设计（协调者复核后改用 go/ast 接收者形状判定，弃用纯文本扫描）：全仓非测试
  `.go` 文件 go/parser 解析，ast 收集全部 `SelectorExpr` 且 `Sel.Name=="Pointer"`；排除
  `X` 为标识符 `unsafe` 的（`unsafe.Pointer(` 类型转换，恰 6 处：taskmark_darwin.go 1、
  platform_windows.go 5）与 `X` 为标识符 `atomic` 的（`atomic.Pointer[` 泛型类型实例化，
  **亲查另有 1 处**：server.go:93 `cfg atomic.Pointer[config.Config]`——协调者提示只列了
  unsafe，atomic 是本轮探针发现的同族排除项）；其余为候选，按所在函数（fd.Recv 推导
  「类型.方法」）与白名单（file+fn+why，当前唯一条目 server.go/roomNarrator.Say）比对。
- 过包含说明（协调者批准的安全方向）：对第三方类型上的同名 `Pointer` 方法是**过包含**
  （不在排除名单即进候选）——过包含是红线的安全方向，判据要求的出口「新的正当引用
  显式加进白名单并写明理由」正是它。计划文档写明此选择。
- **plan 轮实跑证据（probe 全自清理，git status 干净）**：
  - 绿侧：`go test ./internal/agentd/ -run TestPointerGateProbe -v` → `--- PASS`，唯一候选
    `server.go roomNarrator.Say @2349:12`。
  - 红侧**有效读数**：临时在 server.go 白名单函数之外加 `func probeRedPointerCall() error {
    var c *collab.Service; _, err := c.Pointer("B1", proto.RoomMessage{Body:"x"}); return err }`
    （真调 collab.Service.Pointer，非同名方法），同命令跑探针 → **`--- FAIL`**，红线原文：
    `白名单外 Pointer 引用: /Users/sycm/.handoff/worktrees/da43df6b/internal/agentd/server.go probeRedPointerCall`。
  - 还原后复跑 → **`--- PASS`**。
  - **无效读数如实记录（不是正控）**：① 首轮红侧把探针放 `codegraph.go`（缺 collab/proto
    import）→ 整包构建失败 `undefined: collab`——长得像红但什么都没验到，弃用；② 中途
    一次「红侧」命令先把探针测试文件删了再跑 → `no tests to run` **exit 0** 的空跑，长得
    像绿但什么都没验到，弃用。两处无效读数均不作为判据证据；判据的牙由上面「有效读数」
    的 FAIL/PASS 证明。
  - 结论：判据 (a) 基线即绿，且**已证明会红**（白名单外真调 collab.Service.Pointer 必翻红，
    `--- FAIL` 原文在案）。
- C7 交叉依赖：C7 在 cmd/card_dispatch.go 落派发指针（`Service.Pointer`）时必须在同一
  commit 把该引用加进 pointerWhitelist（file=cmd/card_dispatch.go, fn=<函数名>，写明理由
  「C7 派发指针：派发成功落卡房间指针行，B156.2 欠账 #11」），否则守卫翻红——这正是
  「留正当出口」的设计，写进计划跨卡节。

## L14 正控 (b) 的读数形态（交付必报）

- 步骤：临时在 registerLedgerRoutes 加 `POST /api/rooms/{id}/pointer`，handler **真调
  `collab.Service.Pointer`**（`s.rooms.Pointer(...)`，白名单函数之外；协调者复核：拿一个
  `*collab.Service` 值调它的 Pointer 方法，不许用同名方法做正控——同名方法翻红恰说明
  测试在匹配文本而非接收者形状，是判据禁止的形态）；跑 `go test ./internal/agentd/
  -run TestPointerRouteAbsentFromSource` → 必须 FAIL（红线：`白名单外 Pointer 引用:
  internal/agentd/<文件> <函数名>`）；还原后复跑 → PASS。两个读数都进交付。
- **plan 轮已实跑同形探针验证**（L13 有效读数原文）：白名单外真调 collab.Service.Pointer
  必翻红（`--- FAIL` + `白名单外 Pointer 引用: .../server.go probeRedPointerCall`），还原即
  回绿（`--- PASS`）。两次无效读数（codegraph.go 构建失败、no tests to run 空跑）已如实
  记录并弃用，不作为判据证据。

## L15 已裁决的岔口/决策清单（供计划「决策记录」节）

1. D-legacy-count（岔口二条件 1）：代码调用点 37→38；图 legacyHits 1/17 不变。
2. D-assembly：s.rooms 字段 + SetupAutomation 存 + SetRooms 缝 + cmd/agentd.go 激活
   （文件集扩 1）。
3. D-identity：web 成员/actor 标识 = `"web:"+hostOnly(r.RemoteAddr)`。
4. D-destructive：approver_decision escalate/error 事件链判定。
5. D-rebind：rebindPort + facadeBindAdapter（组装点注入）。
6. D-entries：target.json 预定声明 entries 用实际 label `["collab 入站门面"]`，非 §2.3
   逐字（L7 探针证据）。
7. D-collab.New：视图 diff 补节点（选项①），理由见 L6。
8. D-inbox-error：decision/mention 源失败→500（承重）；ticket 源失败→warn+跳过（降级，
   handleCardsList 的 tickets 同族先例）。
9. D-title：inbox 条目标题派生——decision=Body 首行、ticket=按 Kind（gate「权限工单待答复」
   /ask「提问工单待答复」）、mention=`@你：`+Body；ticket/mention 条目不挂 payload
   （金样本 fixture 无 payload，孪生一致以键集为准）。

## L16 收尾核对

- 探针全部自清理：/tmp 备份还原 + 临时 diff 删除，`git status --short` 全程干净（L6/L7
  探针后已核）。
- 本节点产出物 = 计划文档 + 台账，不写实现代码（红绿判据是给 implement 轮的形态说明）。
## L17 计划文档落盘与校验

- 产出物 `docs/superpowers/plans/b156.2.6-plan.md`（1394 行）落盘。
- 结构性校验：代码围栏 22 个（偶数，闭合平衡）；9 个 go 代码块；三个全文件块
  （pointer_gate_test.go / roomsapi_test.go / roomsapi.go）`gofmt -l` 零输出（提取后实跑）。
- 图闸基线与本轮探针全部在案（L1/L2/L6/L7）；工作树只含两个新文档文件，git status 干净。
- 收口自查：① 全部事实均来自亲自跑过的命令（台账有原始输出）；② 本轮未碰 handoff CLI
  写命令、未起新 executor（仅 `go run . graph <只读子命令>` 与 `go test`）。③ 判据 (a) 的
  「基线即绿」与「会红」均已实跑证明（L13 有效读数），不是纸面推断。

# ===== implement 轮（charter-3，2026-08-26；上一轮 charter-2 被截断、零实现代码，本分支干净重来）=====

## L18 基线复核（T6.1 步骤 1）

- 分支 `cards/B156.2.6-charter-3`，HEAD=9ab8312b（merge origin/feature/b156-workbench），
  工作树干净。`git merge-base --is-ancestor 948e4cd8 HEAD` = BASE_OK。
- `go test ./internal/agentd/...` → `ok github.com/Xsxdot/handoff/internal/agentd 73.909s`。
- `go run . graph check --repo . --view cards-B156.2-charter-4` EXIT=0、fails=[]、
  legacyHits["d_gateway->d_ledger"]=1。warns 96 条，按 kind 分类：anchor-off-domain 2 /
  best-dangling 2 / container-misplaced 51 / legacy 34 / oversized-package 2 / prefix-family 5。
  （计划 §4 写「97 与基线一致」——实测是 96，以实跑为准。）
- registerLedgerRoutes 亲读无 rooms 路由（ledgerapi.go:30-52）；Server 无 collab 字段（server.go:92-157）。
- `n_ledger_api_Facade_BindDriver` 不在 baseline.json（k_ledger_api_Facade 无该节点）；`n_collab_New`
  已在视图 diff（C7 落地，协调者发现二，本卡不重复加节点）；`n_agentd_Server_SetupAutomation` 在
  baseline 有节点。

## L19 红文（T6.1 步骤 3）

`go test ./internal/agentd/` 预期编译失败。原始报错（截断至 10 行）：
```
# github.com/Xsxdot/handoff/internal/agentd [github.com/Xsxdot/handoff/internal/agentd.test]
internal/agentd/roomsapi_test.go:76:23: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:100:22: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:151:22: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:161:23: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:244:23: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:338:22: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:357:20: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:372:22: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:408:23: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:440:23: env.srv.rooms undefined (type *Server has no field or method rooms)
internal/agentd/roomsapi_test.go:440:23: too many errors
FAIL	github.com/Xsxdot/handoff/internal/agentd [build failed]
```
红因 = 功能缺失（Server.rooms 字段与六 handler 不存在），非拼写错。判定为有效编译红。
- 首跑还有一处助手签名错：`ledgerPost(t, env, ...)` 中 env 是 *ledgerEnv，而 ledgerPost 收
  *testAgentdEnv（ledgerapi_test.go:24/43）。计划代码块 B 直接写 `ledgerPost(t, env, ...)` 编译不过
  ——按既有先例（ledgerapi_test.go:189 `ledgerGet(t, env.testAgentdEnv, ...)`）机械修正为
  `env.testAgentdEnv`；`TestRoomsEndpoints503WithoutLedger` 的 env 本就是 *testAgentdEnv，保持原样。
  这是机械修正非换入口。

## L20 协调者发现一落地（Pointer 归属三种承载形态）

- 亲读功能线原文：`cmd/card_dispatch.go:347` `var roomPointer = func(s *collab.Service, roomID, body string) error {`，
  :348 `_, err := s.Pointer(roomID, proto.RoomMessage{Body: body})`；:329 `writeDispatchPointer` 只
  **调用** roomPointer。故真实 Pointer 调用在包级 var 闭包里，不在任何 FuncDecl 内。
- 全仓非测试 `.go` 候选（排除 unsafe/atomic 前缀后）恰两处：
  1. `internal/agentd/server.go:2349`（roomNarrator.Say）；
  2. `cmd/card_dispatch.go:348`（包级 var 闭包 roomPointer）。
- 守卫归属逻辑按协调者处置增强为三种承载形态：FuncDecl→函数名/「类型.方法名」；包级
  var/const GenDecl→ValueSpec.Names[0]（本仓命中即 roomPointer）；函数内匿名闭包→最内层
  FuncDecl（近似）。多 span 命中取跨度最小（最内层）。
- 白名单两条，第二条函数名**以实测归属输出为准**。实测读数（先不加白名单跑守卫）：
  `pointer_gate_test.go:153: 白名单外 Pointer 引用: cmd/card_dispatch.go roomPointer`——归属逻辑对包级 var
  闭包报出变量名 **roomPointer**（与协调者预期一致，但以实测为准）。加白名单条目后复跑 `--- PASS`。

## L21 协调者裁决①：读侧宽容（GET 不存在房间）

**计划前提失实记录**：计划 §0.8/T6.1 断言「GET 不存在房间历史 → 404」的前提是
`collab.Service.History` 会返回 `collab.ErrNoRoom`。实跑（implement 轮）发现该前提不成立：
`go test ./internal/agentd/ -run TestRoomSendErrMapping` → `roomsapi_test.go:174: GET 不存在房间应
404: 200`。对照契约原文（协调者复核，非转述）：
- 契约 b156.2-contract.md:202-204 History 注释逐字：「读历史：seq 游标分页（beforeSeq 排他、升序截尾
  同 EventsFromAsc 语义），只返回 type==room_message 的事件。limit<=0 取 200。」**只字未提 ErrNoRoom
  与房间解析**。
- 契约 :188-190 Send 注释明写「roomID 必须解析为存在的房间……否则 ErrNoRoom」；:350 验收清单只写
  「Send 返回 ErrNoRoom」。
- 结论：ErrNoRoom 是 **Send 的契约义务，不是 History 的**；§3.5「400 ErrNoRoom→404」是**映射规则**
  不是**行为规定**——条件（History 返回 ErrNoRoom）从不成立。C4 的 History（service.go:125 纯过滤、
  不调 room.Resolve）完全合规，无缺陷需修。排除 ②（改 C4 冻结缝迎合一格从不触发的映射、波及已合入的
  C7 room read、每次多一次 GetCard 读）与 ③（handler 经 ListRooms 判存在，重且 project/global 语义模糊）。

**裁决（协调者选 ① + 三条附加）**：
1. **读写不对称是有意语义**：写侧受守（Send 对不存在房间 ErrNoRoom），读侧宽容（History 对不存在
   房间返回空列表）。读不存在房间与读空房间对渲染方是同一件事。写清楚防后人当 bug「修」掉（那会
   反过来破坏契约）。
2. **handler 的 ErrNoRoom→404 映射保留但今日不可达**：契约要求这格映射，留着零成本。**不写声称验证
   这条映射的测试**——今天无输入能让 History 吐 ErrNoRoom，那样的测试在验不存在的现实。
3. **否定断言必须配同测试正面断言**：`TestRoomMessagesEndpoint` 重构为——存在房间已落 2 条
   room_message → GET 200 且**恰好 2 条**（条数与 seq/正文内容都断言）→ 紧接着不存在 roomID → 200 且
   零条。同一支测试、同一夹具，「History 恒空」的假实现会在正面断言上翻红。`TestRoomSendErrMapping`
   删除「GET 不存在房间应 404」断言，保留 POST→400（Send ErrNoRoom 契约义务，写侧受守）。

**恒空变异（协调者顺带要求，合成单条命令执行）**：
- 变异：`internal/collab/service.go` History 的 `return out, nil`（锚含 SameRoom 过滤体，count==1 唯一）
  → `return nil, nil`。备份/变异/测/还原/复验一条命令完成（防截断把 C4 冻结代码的变异态留在分支）。
- 读数：MUT_EXIT=1，红在**正面断言**：`roomsapi_test.go:119: 历史应恰好两条: []`（恒空实现翻红，非红在
  否定半）；还原后 RESTORE_EXIT=0 PASS；`git status --short` 无 collab 文件（service.go 未在改动清单）。

## L22 Pointer 守卫正控读数（T6.3，两个正控红绿全入交付）

- 正控①（FuncDecl 形态）：临时注册 `POST /api/rooms/{id}/pointer` +
  `func (s *Server) handleRoomPointer` 真调 `s.rooms.Pointer(...)`。
  - 红侧：`--- FAIL` + `白名单外 Pointer 引用: internal/agentd/roomsapi.go Server.handleRoomPointer`。
  - 还原后：`--- PASS`。
- 正控②（包级 var 闭包形态）：临时 `var probeRoomPointer = func(s *collab.Service) error {
  _, err := s.Pointer("probe", proto.RoomMessage{Body:"x"}); return err }`。
  - 红侧：`--- FAIL` + `白名单外 Pointer 引用: internal/agentd/roomsapi.go probeRoomPointer`——归属逻辑对
    第 2 种承载形态报出变量名、也翻红。
  - 还原后：`--- PASS`。
- 两处临时改动均已还原；`git status --short` 复核无残留。

## L23 裸门 go test ./cmd/（吸收前固有红，记录不改）

- commit 1 落盘前实测：`go test ./cmd/` → `--- FAIL: TestRepoContractGate`，red 原文：
  - `graph_gate_test.go:38: 契约违规 [dead-contract] 契约 d_cli→d_collab 声明的方向没有活跃 call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）`
  - `graph_gate_test.go:38: 契约违规 [dead-entry] 契约 d_cli→d_collab 声明的入口 "collab 包级函数" 在 d_collab 中不存在...`
- 这是 C7 的 d_cli→d_collab 契约在未 absorb 视图时的固有形态（TestRepoContractGate 只读 baseline，
  Merge(g,nil) 不带 diffs）。本卡 commit 2 加 d_gateway→d_collab 契约后此数会变（新增 dead-contract +
  dead-entry），照实记录、**不修**。

## L24 commit 1（T6.1-T6.4 红绿收口）

- 触及测试全绿：`go test ./internal/agentd/ -run 'TestRooms|TestRoomSend|TestRoomMessages|TestInbox|TestCardRebind|TestRoomsEndpoints|TestPointerRouteAbsent' -count=1 -v` → 15 支全 PASS。
- `go build ./...` BUILD_OK；`go vet ./internal/agentd/...` VET_OK。
- gofmt 闸：初扫 `gofmt -l internal cmd` 命中三文件（roomsapi.go/roomsapi_test.go/pointer_gate_test.go，
  均为 EOF 缺换行 + 一处注释空行），`gofmt -w` 后 `gofmt -l internal cmd` 零输出。
- 占位残留扫描：`grep -rn "占位\|strings.Repeat(\"\", 0)" internal/agentd/roomsapi_test.go internal/agentd/roomsapi.go`
  零命中；`grep -n "strings.Repeat\|json.Number\|占位" roomsapi_test.go` 零命中。
- 提交 1 范围：roomsapi.go、ledgerapi.go（6 行注册）、server.go（字段/缝/SetupAutomation/适配器/
  withRooms）、cmd/agentd.go（激活行）、roomsapi_test.go、pointer_gate_test.go、台账。**不含 target.json
  契约**（无消费边声明，view 无新增边，graph check 保持绿）。

## L25 图增量与图闸（T6.5）

**target.json**：追加 `d_gateway→d_collab` 预定声明（entries `["collab 入站门面"]`，实际 label，§0.1）。
**视图 diff cards-B156.2-charter-4.json**：nodesAdded +14、edgesAdded +12。**n_collab_New 不重复补**（C7
已落地，协调者发现二）；补录覆盖债 `n_ledger_api_Facade_BindDriver`（k_ledger_api_Facade）。边含
`n_agentd_Server_SetupAutomation → n_collab_New`（组装边）。

**手写 JSON 双判据（python object_pairs_hook，非 grep）**：
- 逐对象重复键：改前 target.json NONE / 视图 NONE；改后 target.json NONE / 视图 NONE。
- 解析条数 vs 声明条数：target.json contracts 46→47（MATCH）；视图 nodesAdded 26→40（MATCH）、
  edgesAdded 42→54（MATCH）。

**图闸**：
- `go run . graph check --repo . --view cards-B156.2-charter-4` EXIT=0、fails=[]、
  legacyHits["d_gateway->d_ledger"]=1（新增 ListDecisions 边目标 k_ledger_Store 在 entries 内，不进
  legacyHits——§0.4 图口径不变）、warns 96（anchor-off-domain 2 / best-dangling 2 / container-misplaced 51
  / legacy 34 / oversized-package 2 / prefix-family 5，与基线同分类同数）。
- `go run . graph validate --repo .` EXIT=0。

**逐容器点数自检**（协调者实况：抽查符号名会命中别的容器同名符号，逐容器数）：
- k_agentd_Server 新增 = 9（六 handler + roomUserActor + ticketDestructive + withRooms）。计划稿写
  「=10」为算术笔误（6+1+1+1=9），实际源码新增 Server 方法 = 9，一致。
- k_agentd_fn 新增 = 4（collabErr + decisionTitle + ticketTitle + facadeBindAdapter.Rebind）。
- k_ledger_api_Facade 新增 = 1（BindDriver）。k_collab_fn 新增 = 0（n_collab_New 已由 C7 落地）。

**裸门 go test ./cmd/（吸收前固有红，记录不改）**：
- 改后读数：`--- FAIL: TestRepoContractGate`，violations = dead-contract 2 条（d_cli→d_collab +
  d_gateway→d_collab）+ dead-entry 1 条（d_cli→d_collab 入口 "collab 包级函数"）= 总数 3。
- 改前基线（L23）= 2 条（dead-contract d_cli→d_collab + dead-entry "collab 包级函数"）。**+1 恰为
  d_gateway→d_collab 的 dead-contract**——裸门只读 baseline、不吸收视图 diff（Merge(g,nil)），与
  §0.2/§1 #4 语义一致。照实记录，不修。

## L26 变异四靶（T6.5 步骤 4，全部单条命令合成、逐一还原、复验绿、git status 干净）

| 靶 | 变异 | 红侧断言（原文） | 还原 |
|---|---|---|---|
| ① Watchers 判据牙 | `if watchers > 0 && !destructive` → `if false && ...`（恒上浮） | `roomsapi_test.go:336: Watchers>0 应排除工单: [{Origin:ticket ...}]` | PASS |
| ② 破坏性不受限牙 | ticketDestructive 命中分支 `return true` → `return false` | `roomsapi_test.go:362: 破坏性工单不受 Watchers 限制应上浮: []` | PASS |
| ③ 错误映射牙 | collabErr 去掉 `ErrReadOnly` 分支（→500） | `roomsapi_test.go:221: 终态房间应 409: 500` | PASS |
| ④ rebind 牙 | facadeBindAdapter.Rebind `return a.f.BindDriver(...)` → `return nil` | `roomsapi_test.go:238: expect 不符应 409: 200 {"ok":true}` | PASS |

四靶全部满足：变异可编译（go build ./internal/agentd/ 过）、施加位置==执行位置、anchor count==1
（python assert）、还原后 `go test ./internal/agentd/ -run <单靶>` 复绿、`git status --short` 无残留。
另：恒空变异（History return nil）在 L21 已验（红在正面断言「历史应恰好两条: []」）。

## L27 收尾自审（charter-3 轮）

- 提交：commit 1 = 6e623acf（feat agentd 房间面六端点+收件箱+Pointer 守卫）；commit 2 = 074fda97
  （graph 增量）。工作树干净。
- 全量编译 `go build ./...` BUILD_OK；`go vet ./internal/agentd/...` VET_OK；
  `go test ./internal/agentd/...` ok（63.4s）；`gofmt -l internal cmd` 零输出。
- 图闸（commit 2 后复跑语义不变）：graph check --view EXIT=0 fails=[] legacyHits 1/17；validate EXIT=0。
- 每条错误分支带上下文日志、成功路径有出口日志：roomsapi.go 逐 handler 符合；新文件头注释（职责+边界）、
  导出/关键函数注释齐。
- 与 plan Interfaces 签名一致（六端点 + rebindPort + collabErr/decisionTitle/ticketTitle 等逐字符核对）。
- §12 明确不做全部守住：proto/ledger 生产代码/金样本 fixture/registerLedgerRoutes 既有行零改动；
  未 absorb；未加真机清单项。
- 本轮未碰 handoff CLI 写命令、未起新 executor；graph 仅用只读子命令。全部结论来自亲自跑过的命令。
