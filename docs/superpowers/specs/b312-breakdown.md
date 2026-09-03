# B312 拆解：开卡绑当前会话（linux-01/codex 对照）

**状态：拆解提案，待协调者拍板**（2026-09-03）

**卡：** B312

**标题：** 开卡绑当前会话（linux-01/codex 对照）

**有效基线：** `acc/b156.2-156.3`（不越过该基线）

**上游 spec：** `docs/superpowers/specs/2026-09-03-b307-bind-current-session-design.md`（头部状态：已批准）

**冻结 contract：** `docs/superpowers/specs/b312-contract.md`（头部状态：已冻结）
**图依据：** `codegraph/best.json`；目标视图：`cards-B312-charter`

## 待拍板岔口（集中）

以下岔口不是本稿自行选择的实现偏好；未裁决前不得据此派发实现。

1. **D1｜退回 contract：会话列表席位字段的 wire 面。** `b312-contract.md §6.2` 要求“会话列表”展示 `driver_session` 与 `driver_source`，但实读现状确认 `/api/cards` 返回 `proto.CardView`，而 `proto.CardView`、`internal/agentd/ledgerapi.go#ledgerCardViewWire`、`web/src/api/ledger.ts#CardView` 均没有这两个字段。方案甲是在 contract 中冻结 `CardView` 的两个可选字段并补齐 Go→TS 投影/金样本；方案乙是把列表改成逐卡读详情，避免扩展列表 wire，但会引入 N+1 读取和新的列表→详情接缝。两者取舍由 contract 节点拍板；本稿不把方案乙当成既定事实。
2. **D2｜退回 contract：`rebind --self` 的 HTTP 身份证明。** 冻结 DTO 有 `Identity` 且要求规范编码，但现有 HTTP 请求只有可由调用方填写的 body，未冻结 CLI→agentd 的不可伪造证明；`web:<addr>` 只能是普通 actor，不能直接当席位。方案甲冻结一条 CLI 专用的可信会话证明并让 agentd 校验；方案乙明确 `mode=self` 不经 HTTP、另行定义同机控制面调用。前者要新增契约载体，后者要改写当前“通过控制面 rebind”的契约；在 contract 未补齐前，不能声称“禁止浏览器伪造”已可验。
3. **D3｜Launch→CAS 冲突后的新会话处置。** 冻结时序是先 Launch、成功后以返回 session id 做 `BindSeat`/`RebindSeat` CAS，但尚未冻结 CAS 冲突后如何终止或登记新启动的会话。方案甲立即终止/回收该新会话，避免孤儿；方案乙把它登记为待回收并由后续清扫，保留恢复现场但扩大孤儿生命周期。需协调者确认承载层可提供的收尾能力及可观察错误；未裁决前只验“不得 200 报已绑定、不得覆盖现席位”。

## 1. 触及子系统清单与派卡资格

顶层领域以 `codegraph/best.json` 中 `domains` 的 `parent == null` 为准；本次真正改动/消费的顶层领域如下。每行按架构法第一条逐项核对四个派卡资格：①有界文件集；②暴露面可枚举；③依赖 DAG/冻结契约已指明；④类型与验收方式明确。

| 子系统（图 id） | 图类型 | ①有界文件集 | ②暴露面可枚举 | ③DAG/契约 | ④验收分流 |
|---|---|---|---|---|---|
| `d_protocol` | 逻辑型 | `internal/proto/ledger.go`、`scheduling.go`、`contract_fixture_test.go`、`ledger_wire_test.go` | `Card.DriverSource`、`CoordinatorRebindReq`、`SeatSource` 及既有 identity helper | 依赖 contract §2/§6/§9；先于所有消费方 | Go 编译 + JSON roundtrip/fixture，可机内闭环 |
| `d_ledger` | 逻辑型 | `internal/ledger/store.go`、`types.go`、`cards.go`、`binding.go`、`move.go`、`tasks.go`、`wire_test.go`、`ddl_parity_test.go`、`binding_test.go`、`api/api.go`、`api/api_test.go` | DDL/ALTER、`cardColumns/scanCard`、`BindSeat/RebindSeat`、旧 Claim/Takeover/Release 负例、两个 Card 投影 | 依赖 protocol；为 keystone/gateway/collab 的权威事实 | 双方言/真实 SQLite、事务/CAS/事件断言，可机内闭环 |
| `d_transport` | 边界型 | `internal/client/coordinator.go`、`squads.go`、相关 `internal/client/client_test.go` | coordinator launch/rebind HTTP client 与 400/409/502/503 原文传播 | 复用既有 client→gateway 形状，不新增目标图边 | 机内只验 request/response 形状；真实远端/网络列真机清单，归协调者执行 |
| `d_gateway` | 边界型 | `internal/agentd/coordapi.go`、`scheddrain.go`、`cardstep.go`、`server.go`、`ledgerapi.go`、`roomsapi.go` 及对应 `coordapi_test.go`、`scheddrain_test.go`、`ledgerapi_test.go`、`roomsapi_test.go` | launch/status/rebind 路由、Launch→CAS 组装点、step actor 门、Card/列表 wire、旧 rebind 路由退役 | 复用 contract §6/§7 已冻结的 gateway→ledger/keystone；不加旁路 | 进程内 HTTP 可验契约形状；真实 agentd、跨机转发、重启与承载失败归真机清单 |
| `d_keystone` | 逻辑型 | `internal/keystone/keystone.go`、`keystone_spec_test.go`、`slice_test.go` | `Wake` 来源分支、`LaunchForCard`、`Locate`、`SessionRef`/Resume 环境 | 依赖 ledger view；被 gateway 唤醒/拉起；契约 §7 | fake runner 可机内闭环验证调用次数和 ref；真实 CLI/session/HOME 行为归真机清单 |
| `d_collab` | 逻辑型 | `internal/collab/service.go`、`room/room.go`、`service_test.go`、`readmodel_test.go`、`cursorfile_test.go` | `VerifyWriter`、`Send`、`Pending`、`Consume`、`ListAllCards` 投影比较 | 依赖 ledger `GetCard/ListAllCards` 与 protocol；不扩 LedgerClient | 真实投影 + 反例可机内闭环，跨卡/父卡矩阵逐项验 |
| `d_cli` | 逻辑型 | `cmd/card.go`、`card_coordinate.go`、`card_driver.go`、`card_dispatch.go`、`card_node.go`、`ledgercli.go`、`room.go`、对应 `card_test.go`、`card_coordinate_test.go`、`card_driver_test.go`、`card_dispatch_test.go`、`card_node_test.go`、`room_test.go` | bind/coordinate/rebind 三按钮、旧 flag、step/room actor、release/takeover/wait 负例 | 依赖 protocol/ledger/client；所有身份出示共用 helper | `runLedgerCLI`/真实 SQLite/HTTP fake 的行为化命令断言，可机内闭环 |
| `d_web` | 逻辑型（含 webview 追加审查） | `web/src/api/ledger.ts`、`scheduling.ts`、`scheduling.fetch.test.ts`、`contract.test.ts`、`app/cards/NewCardDialog.tsx`、`CoordinatorPanel.tsx`、`CardDrawer.tsx`、`ListView.tsx` 及对应测试 | source-free launch、rebind-launch、卡新建去 launch、席位/来源文案与按钮可见性 | 依赖 protocol wire 与 gateway HTTP；D1 裁决前列表投影不封口 | Vitest/tsc 可机内闭环；Chromium/WKWebView/Wails 手势与跨平台显示归真机清单 |

**邻接但不派卡：** `d_scheduling`（逻辑型）只继续提供协调者小队/载体选择，现有 `resolveCoordinatorSquad`、`LaunchAdmit`、carrier→`SessionSpec` 语义不改，没有本卡独占文件；`d_execution`、`d_sessions`、`d_workspace`、`d_policy`、`d_maintenance` 也不改生产文件。外部承载器属于 `d_keystone` 的边界依赖，按真机清单验，不借此扩大本卡文件集。

图门禁事实：本轮按平台不变量运行 `GOMODCACHE=/root/.handoff/tmp/01535cc5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check --view cards-B312-charter`，实得 `fails=[]`（已有 legacy 计数保留）；`validate --view cards-B312-charter` 实得 `issues:null`、`edgeIssues:null`。当前图仍报告 6 个未扫描入口，故任何“查询为空即无调用方”的推断均不成立。

## 2. 契约增量核对

### 2.1 上游状态与冻结范围

- spec 头部已经写明“已批准（用户 2026-09-03：同意；含 r1）”，基线为 `acc/b156.2-156.3`；状态不是会话记忆，本稿引用的是文件状态。
- contract 头部已经写明“上游状态：已批准”与“冻结状态”，并明确本节点只落身份骨架，不实现业务入口；本稿不引用未写回的会话裁决。
- contract §12 已冻结既有图边：`d_cli→d_ledger/protocol`、`d_gateway→d_ledger/keystone`、`d_keystone→d_ledger`、`d_collab→d_ledger/protocol`；本稿不新增跨域边。Web scheduling 的图缺口仍按 contract §12 记录的现状处理，不伪造图锚。

### 2.2 对 contract §9 原子清单逐组核对

| 冻结条目 | 本稿吸收位置 | 结论 |
|---|---|---|
| §9 #1–#8：identity 编码、`SeatSource` 白名单 | T1 protocol/CLI；行为闭环 1、2、4、7 | 全部纳入；沿用已冻结 `cli:<cli>#<session_id>`，不新增身份格式 |
| §9 #9–#15：PG/SQLite `driver_source`、迁移、读写与非法旧座位 | T2 ledger；序列化边界验收 | 全部纳入；但“列表 CardView 展示”另触及 D1，当前 contract 未冻结该 wire 面，退回 contract，不在本稿补字段 |
| §9 #16–#30：Bind/Rebind CAS、事件、旧 API 不改座位 | T2 ledger；行为闭环 2、3、4、6 | 全部纳入；写入口只有新原子面，旧 API 作为反例 |
| §9 #31–#39：CLI 三按钮、旧 `--coordinate`、新 session id | T3/T4；行为闭环 1–7 | 全部纳入；`card add` 不调用 launch，任意 session id 不出用户 flag |
| §9 #40–#47：HTTP source/rebind/status 与账本权威 | T3 gateway/transport；行为闭环 3、8 | 全部纳入；`manual/card_create` 400，`bound` 不再由内存单独决定 |
| §9 #48–#58：Wake 来源、Pending、房间矩阵、step、wait | T3/T4；行为闭环 5–10 | 全部纳入；`bind` no-op，`coordinate` 才 Resume/重建，wait 仍 Follow |
| §9 #59–#62：Web 新建、source-free launch、rebind-launch、禁止 Web 伪造 | T5 Web；行为闭环 11–13 | 全部纳入；但会话列表 source/session 的载体是 D1，未冻结前不通过该项 |

### 2.3 是否产生新接缝

本稿没有把新的业务路由、第二席位表、第二 identity 字段、LedgerClient 新方法或调度域新接口加入方案；这些均明确不产生新接缝。唯一在核对时发现的新增 wire 需求是 D1：contract 已写产品结果，却没有冻结 `CardView` 载体，故按纪律退回 contract。D2 是现有 `CoordinatorRebindReq.Identity` 的信任来源未被冻结，D3 是已冻结时序的失败清扫载体未被冻结；二者同样列为退回/待拍板，不在本稿擅自添加字段或 API。

## 3. 子卡清单与依赖 DAG

B307 §4 明确“不扇出子系统子卡”；因此本稿只提出一张跨域实现子卡，用内部票据 DAG 保持接缝在同一上下文中可见。下列不是已创建或已派发的卡。

### 子卡 B312-impl｜跨域落地席位权威与当前会话绑定（提案）

#### ①契约引用

`b312-contract.md` §2 identity/source、§3 storage/wire/session injection、§4 `BindSeat/RebindSeat`、§5 CLI 非按钮入口、§6 HTTP/Web、§7 Keystone/Wake/room、§8 白名单与依赖、§9 #1–#62、§12 plan 附区；上游 spec `2026-09-03-b307-bind-current-session-design.md` §3–§7、§10–§12。D1–D3 是本卡前置裁决，不是可由实现子卡自批的内容。

#### ②意图与为什么

让一张卡只有一个由账本权威管理的协调者席位，且席位的规范身份同时被 CLI、无头机器人续接、房间书写权、`@` 定向和 step 出示消费。开卡、领卡、随手记录不再偷偷占座；“坐下”和“叫机器人”保持两个可观察入口；来源决定 Wake 是 no-op 还是 Resume/重建。HTTP 与 Web 只复用同一语义，不再以 keystone 内存或人尺度 actor 代替账本事实。

这张卡同时收口失败窗口：座位写入必须和 CAS/事件处置一致，旧 `ClaimCard`/`TakeoverCard`/`ReleaseCard` 不能覆盖新席位，重启后不能因内存缺失把坐下席位误判为空座。D1–D3 解决前，卡的实现边界保持冻结状态，不把未定义的载体当作完成条件。

#### ③验收

**机内逻辑验收（完成后分别运行，命令退出码为 0；当前未宣称这些运行时行为已通过）：**

- `go test ./internal/proto/... -run 'TestSeatIdentity|Test.*Card.*Wire|Test.*Coordinator.*Wire' -count=1`：规范 identity/source/mode 能区分缺失与零值；旧 `cli:user@host`、未知 enum、重复 `#` 均失败。
- `go test ./internal/ledger/... -run 'Test(OpenMigrates|BindSeat|RebindSeat|ClaimCard|TakeoverCard|ReleaseCard|.*DriverSource|.*CardWire)' -count=1`：PG/SQLite schema 与 ALTER 幂等，`BindSeat/RebindSeat` 同事务 CAS，冲突保留两列，成功恰一条 `EvDriverTakeover`，旧 API 不改座位。
- `go test ./internal/ledger/api/... -run 'Test.*Projection|Test.*Following' -count=1`：Facade 的现有接口继续只投影账本事实，不扩 LedgerClient；所有新增 Card 字段穿过真实 JSON 边界。
- `go test ./internal/keystone/... -run 'Test.*Wake|Test.*Resume|Test.*Launch|Test.*Locate|Test.*Slice' -count=1`：来源为 bind 时 `Resume/Launch` 调用次数均为 0；来源为 coordinate 时内存 ref 优先 Resume，Resume 失败才 Launch；self 换绑后旧 ref 不再 Resume。
- `go test ./internal/agentd/... -run 'Test.*Coord|Test.*CardStep|Test.*Wake|Test.*Room|Test.*Ledger' -count=1`：launch 缺省 coordinate，manual/card_create/未知值 400；空座仅一次 Launch 并 CAS，非空座在 Launch 前冲突；`GET bound` 只认合法账本席位；step 不匹配拒绝且不改座位；旧 `/api/cards/{id}/rebind` 不再提供新席位旁路。
- `go test ./internal/collab/... -run 'Test.*Writer|Test.*Pending|Test.*Consume|Test.*Room' -count=1`：同一规范席位可写协调者 kind，跨卡和平级 relay 反例失败，一级父 relay 保留，`@B号` 的 consumer 比较同一字符串。
- `go test ./internal/client/... -run 'Test.*Coordinator|Test.*HTTP|Test.*Error' -count=1`：launch 固定 `{}`，rebind mode/identity 按 contract 编码，非 2xx 状态与可行动错误原样传播。
- `go test ./cmd/... -run 'Test(Card|Room|Dispatch|Node|.*Seat)' -count=1`：`card bind/coordinate/rebind --self|--launch` 只走冻结入口；旧 `card add --coordinate` 非零并指向 `card coordinate`；空座 `--step` 不占座，有座 mismatch 拒绝；takeover/release 不改席位；room send 的协调者 kind 不用 `ledgerActor()`。
- 在 `web` 工作目录运行 `npm test -- src/api/scheduling.fetch.test.ts src/api/contract.test.ts src/app/cards/NewCardDialog.test.tsx src/app/cards/CoordinatorPanel.test.tsx src/app/cards/CardDrawer.test.tsx` 与 `npm run typecheck`：新建不触发 launch，抽屉 source-free launch/launch-rebind 请求正确，按钮/来源词正确，Web 不产生 bind/self 身份。脚本名已由现状 `web/package.json` 核对；未运行前不宣称命令通过。

**D1 封口条件：** 在 contract 明确 `CardView` 列表载体后，补充 `proto.CardView`、`ledgerCardViewWire`、TS `CardView` 与共享 fixture/列表断言；若裁决方案乙，则删除上述列表字段断言并把验收改为独立详情载体，不能两套并存。

**边界型验收：**

- `d_transport/d_gateway`：机内只验 HTTP method/path/body/status/错误形状和 forward contract；真实 linux-01 与远端 agentd 版本差异、token/Host、网络中断、重启后 `Locate/Wake` 的事实为“未验证，需真机”，归协调者执行。
- `d_web`：机内 Vitest/tsc 只证明组件与 fetch 线形状；Chromium、WKWebView、Wails 手势/缓存以及真实卡列表响应为“未验证，需真机”，归协调者执行。
- runner/PTY/HOME：fake runner 只能锁 `SessionSpec/SessionRef` 字段，不证明真实 opencode/codex 进程实际收到环境变量；“未验证，需真机”，归协调者执行。

**缺陷族对抗审查（写入本卡验收）：**

1. **生命周期 / 状态机中断：** `BindSeat/RebindSeat` 事务失败必须保留身份、来源、心跳；agentd 在 Launch→CAS 窗口崩溃/重启时不得把已启动但未 CAS 的会话报告为已绑定，且 D3 的终止或待回收责任未裁决前标“未验证，需真机”。bind 来源重启后 Wake 必须 no-op；coordinate 来源必须从账本恢复 Resume 引用。测试必须覆盖 `sessions[card]` 缺失与 self 换绑删除旧 ref；真实进程孤儿回收仍归真机清单。
2. **静默失败 / 误导报错：** 只有 Launch 成功且座位 CAS 成功才允许成功响应；空座 rebind、非空座 bind/coordinate、非法旧座位、CAS 冲突必须非零/400/409 并指出下一步。Resume 失败只允许进入既有重建/人工升级路径，不能报成功但不写座；错误 body、CLI stderr 和 Web `errorMessage` 必须保留可行动原因。D1–D3 未裁决的路径不得伪装为已完成。
3. **跨平台假设：** identity 不得从 `USER`、hostname、PID、路径或进程组推导；环境变量键固定为 `HANDOFF_SESSION_CLI/ID`，但 env 注入是否真实穿过 linux-01/codex、macOS/WKWebView、Windows agentd 未验证，需真机。HTTP/relay/Host guard 的平台行为只按边界 contract 验收，不用 Linux fake 推广。
4. **假红 / 假绿测试：** 所有 `driver_source`、Card 与 CardView 投影都要有真实 Go JSON→HTTP→TS 消费链路断言，区分字段缺失和空值；每个成功断言配冲突/旧值/错误反断言。Wake 测试锁调用方可观察的 Resume/Launch 行为，不锁内部 helper；并发 CAS、Launch 一次性和座位不变性必须有能变红的测试。D1 若未补齐，列表测试不能以详情 mock 代替。
5. **门禁绕过：** 新增/保留的所有席位写路径集中在 `BindSeat/RebindSeat`；`ClaimCard`、`ClaimCardAs`、`TakeoverCard`、`ReleaseCard`、旧 HTTP rebind 和 Web `web:<addr>` 均有负例，不能从旁路写座。当前座位检查与写入须在同一账本事务/CAS 内，避免并发 TOCTOU；room/step 的门必须共享同一 identity helper。
6. **序列化边界：** 逐处覆盖 `internal/proto/ledger.go#Card`、`internal/ledger/api/api.go#cardWire`、`internal/agentd/ledgerapi.go#ledgerCardWire`、列表 `ledgerCardViewWire`、TS `Card/CardView`、coordinator/rebind DTO，以及 `SessionRef→resumeTurnRequest→hostapi.RunTurn` 的环境投影。新增 `driver_source`、mode/source/identity 必须有 roundtrip/缺失与零值断言；D1 是当前未冻结的列表边界，不能跳过。
7. **枚举新值过既有白名单：** `SeatSource` 的 `bind|coordinate`、launch source 的 `coordinate`、rebind mode 的 `self|launch` 需逐处核对 JSON 解码校验、CLI flag、HTTP handler、TS union、日志/fixture 和 switch；`manual/card_create` 的生产者与消费者必须同时退役并有 400 反例，避免入口绿而中间白名单挡死。
8. **承重安全属性有测试锁住：** “一张卡一个席位”“同一席位可兼任多卡”“CAS 期望值精确匹配”“self 换绑隔离旧机器人”“空座 bind/coordinate 不可重复”均须有可变红测试；不得只因实现当前恰好成立而把这些属性写在注释里。
9. **webview / 平台表现差异：** Web 不产生席位身份，按钮禁用/不可用文案在 Chromium 与 WKWebView 都需真实检查；页面刷新、缓存旧 JS、真实列表响应不能把不存在的 `CardView` 字段假绿。未验证，需真机，归协调者执行。

#### ④入口指针与有界文件集

现状代码锚优先使用已由图解析的符号锚；新增/图未覆盖入口用文件路径和现状行号，不把未存在的符号写成事实。

**生产文件：**

```text
internal/proto/ledger.go
internal/proto/scheduling.go
internal/proto/contract_fixture_test.go
internal/proto/ledger_wire_test.go
internal/ledger/store.go
internal/ledger/types.go
internal/ledger/cards.go
internal/ledger/binding.go
internal/ledger/move.go
internal/ledger/tasks.go
internal/ledger/wire_test.go
internal/ledger/ddl_parity_test.go
internal/ledger/binding_test.go
internal/ledger/api/api.go
internal/ledger/api/api_test.go
internal/keystone/keystone.go
internal/keystone/keystone_spec_test.go
internal/keystone/slice_test.go
internal/agentd/coordapi.go
internal/agentd/scheddrain.go
internal/agentd/cardstep.go
internal/agentd/server.go
internal/agentd/ledgerapi.go
internal/agentd/roomsapi.go
internal/agentd/coordapi_test.go
internal/agentd/scheddrain_test.go
internal/agentd/cardstep_test.go
internal/agentd/ledgerapi_test.go
internal/agentd/roomsapi_test.go
internal/agentd/wakeconsumer_test.go
internal/collab/service.go
internal/collab/room/room.go
internal/collab/service_test.go
internal/collab/readmodel_test.go
internal/collab/cursorfile_test.go
internal/client/coordinator.go
internal/client/squads.go
internal/client/client_test.go
cmd/card.go
cmd/card_coordinate.go
cmd/card_driver.go
cmd/card_dispatch.go
cmd/card_node.go
cmd/ledgercli.go
cmd/room.go
cmd/card_test.go
cmd/card_coordinate_test.go
cmd/card_driver_test.go
cmd/card_dispatch_test.go
cmd/card_node_test.go
cmd/room_test.go
cmd/card_seat.go             # 若共享当前会话 helper 不适合放入现有 CLI 底座，则这是本卡唯一新增 CLI 文件
web/src/api/ledger.ts
web/src/api/scheduling.ts
web/src/api/scheduling.fetch.test.ts
web/src/api/contract.test.ts
web/src/app/cards/NewCardDialog.tsx
web/src/app/cards/NewCardDialog.test.tsx
web/src/app/cards/CoordinatorPanel.tsx
web/src/app/cards/CoordinatorPanel.test.tsx
web/src/app/cards/CardDrawer.tsx
web/src/app/cards/CardDrawer.test.tsx
web/src/app/cards/ListView.tsx
```

`cmd/card_coordinate.go` 的现有 `coordinateAfterCreate` 是删除/退役入口，不得保留生产调用；`internal/agentd/roomsapi.go` 的旧 `/api/cards/{id}/rebind` 是删除旧旁路的入口。`web/src/app/cards/CardItem.tsx` 当前没有席位字段消费，未列入；若 D1 裁决要求卡片而非列表显示，必须先回到本文件更新有界集，不可临时扩大。

## 4. 行为闭环核对

只核产品可观察行为；每行五格完整，归属均为已列出的 `B312-impl`，没有只活在接口、测试或无人认领格子里的承诺。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
|---|---|---|---|---|
| 当前 CLI 会话执行 `card bind` | `HANDOFF_SESSION_CLI/ID` 经共享 helper 编成 `cli:<cli>#<session>`；账本两列为空 | `Store.BindSeat`、后续 room/step/GET bound | 命令成功；来源为 bind；房间消息/step 可由同一会话出示；Wake 不 Launch/Resume | B312-impl |
| 当前 CLI 会话执行 `card bind` 于已有合法或非法席位 | 账本 `driver_session/driver_source` 非空 | `BindSeat` CAS | 非零/409，旧席位与来源不变，提示 rebind | B312-impl |
| CLI/Web 执行 `card coordinate`/叫机器人于空座 | 唯一协调者小队解析出的 `SessionSpec` + Launch 返回 `SessionID` | gateway 组装点、`LaunchForCard`、`BindSeat` | 只 Launch 一次；成功后账本身份为 `spec.CLI+SessionID`、来源 coordinate；返回可续接结果 | B312-impl |
| CLI/Web 执行 coordinate 于已有席位 | 账本非空席位是唯一占用事实 | gateway 在 Launch 前读座 | 409/可行动冲突；不启动第二机器人、不改席位 | B312-impl |
| `card rebind --self` | 当前会话规范身份 + 账本旧席位原始值 | `RebindSeat`、keystone session map | 成功写 bind 新席位、落一条 from/to 事件、删旧 `sessions[card]`；后续 Wake 不 Resume 旧机器人 | B312-impl |
| `card rebind --launch`/Web 换绑叫机器人 | 账本当前席位 + 新 Launch 返回 session | gateway、keystone、`RebindSeat` CAS | 成功写 coordinate 新席位并返回新 session；冲突不覆盖旧席位；新 ref 可被后续 Resume | B312-impl |
| `card add`、领卡、note、move、普通 wait | 卡创建/状态/事件本身；无席位写入调用 | ledger、Follow | 命令/HTTP 完成但 `driver_session/driver_source` 仍空；旧 `--coordinate` 非零并指向 coordinate | B312-impl |
| 有席位会话执行 `card dispatch --step` | 账本当前席位 + 共享出示身份；运行锁另存 | CLI、agentd step 门、`ClaimCard` 负例 | 匹配才受理；不匹配非零且席位不变；空座可派发但 actor 只作审计、不占座 | B312-impl |
| 协调者会话向卡房间发送协调者 kind | 账本 Card 的规范 `driver_session`，父卡席位为一级 relay 事实 | `VerifyWriter`/`Service.Send` | 本卡席位可写；另一卡和平级 relay 被拒；一级父 relay 保留 | B312-impl |
| 有人 `@B号` 或消费消息 | `ListAllCards` 投影中的同一规范席位字符串 | `Pending(consumer)`/`Consume` | 消息进入该卡席位对应 Pending；消费只清同一 consumer，不按人尺度 actor 猜 | B312-impl |
| agentd Wake 看到 `driver_source=bind` | 账本 Card 两列；memory session 可缺失 | `Service.Wake` | 返回 no-op；不 Launch、不 Resume、不隔离 HOME，送达由该对话的 Follow wait | B312-impl |
| agentd Wake 看到 `driver_source=coordinate` | 账本席位 + memory `SessionRef`（缺失时由账本/当前 spec 补） | `Wake`、Runner Resume/Launch | memory 有 ref 先 Resume；Resume 失败才重建并返回结果；不因 memory 缺失把座位当空 | B312-impl |
| Web 新建/卡列表/卡抽屉 | 真实 Card/CardView wire 的席位字段与 source 词 | NewCardDialog、ListView/CardDrawer、CoordinatorPanel | 新建无 launch；抽屉显示坐下/叫机器人与席位来源；空座叫机器人，有座再点得到换绑指引；D1 裁决前列表载体未封口 | B312-impl |

## 5. 内部票据 DAG（不扇出，不派发）

```text
D1/D2/D3 contract 裁决与回写
        │
        ▼
T1 protocol / wire DTO / identity 消费与 roundtrip
        │
        ▼
T2 ledger DDL、迁移、Card 投影、BindSeat/RebindSeat 与旧 API 负例
       ┌┴──────────────┐
       ▼               ▼
T3 gateway + keystone  T4 CLI + collab + client
       └──────┬────────┘
              ▼
T5 Web 投影/UI 与跨 seam 回归、真机清单交接
```

- T1 只能消费 contract 已冻结的字段和枚举；已存在的 `internal/proto/seat.go` identity 骨架是输入事实，不重复发明格式。
- T2 是全部运行时席位写入的权威前置；T3/T4 不得在各自域复制 CAS 或 identity parser。
- T3 处理 Launch→CAS、Wake/Resume、HTTP 状态与旧路由；D3 未裁决时只保留“冲突不报绑定成功”的验收边界。
- T4 处理共享出示 helper、三按钮、step/room/Pending，并保留 `ledgerActor()` 仅作普通审计 actor。
- T5 必须穿过真实 Go→HTTP→TS 边界；D1 若改 `CardView`，T5 同批补列表 wire fixture 与反面断言。

## 6. 未验证，需真机清单（归协调者执行）

1. linux-01 上真实 codex 会话产生的 `HANDOFF_SESSION_CLI/ID` 是否能由 CLI 与后续 handoff 读取，并精确生成同一席位；普通终端缺失身份的错误文案与退出码。
2. 真实协调者小队从载体选择到 Launch 返回 session id，再到 agentd CAS 的整条通路；并发 bind/coordinate/rebind 的冲突、Launch 后进程/临时目录孤儿及 D3 处置。
3. agentd 进程重启前后：bind 来源不 Resume/Launch；coordinate 来源从账本恢复 Resume；self 换绑后旧机器人不会再次收到 Resume；HOME 隔离与环境变量真实到达 runner。
4. 真实远端/relay/Host token/网络中断/HTTP 503 与 agentd 版本差异；`GET bound` 只由账本席位决定且 attach 定位失败不降级合法席位。
5. 真实 opencode/codex runner 的 `SessionSpec`、`SessionRef` 和 `resumeTurnRequest` 环境注入；fake runner 无法证明外部进程实际接收。
6. Chromium、WKWebView、Wails 的页面刷新、旧缓存、按钮禁用/错误显示、真实 `/api/cards` 列表字段；尤其 D1 未裁决时不得以详情 mock 代替列表事实。
7. 真实卡房间 `@`、父卡 relay、平级卡拒绝以及当前会话挂 `card wait` 的送达行为；机内夹具只能证明比较函数，不能证明事件真实走到该通路。

## 7. 出稿自检

- [x] 子系统清单按 `codegraph/best.json` 顶层领域列出并标记 logic/boundary；每行完成四项派卡资格核对。
- [x] contract 增量按 §9 #1–#62 分组逐项落到 T1–T5；未冻结的 D1–D3 集中置顶，未边拆边加 seam。
- [x] 一张子卡具备契约引用、意图与为什么、行为化验收、入口指针四段式；文件集显式有界。
- [x] 行为闭环每行具备触发者、权威事实/载体、消费者、结果、归属子卡五格。
- [x] 缺陷族逐族回答，含序列化、枚举白名单、承重安全属性与 webview 追加族；无风险处均说明“无，因为……”，本卡命中的族均记录具体风险。
- [x] “未验证，需真机”已汇总，且未把 fake/grep 事实写成真实行为结论。
- [x] 本稿未写实现代码、未建卡、未派发、未调用 handoff CLI；待协调者裁决后再按“裁决回写 + 状态更新 + 同批提交”收口。
