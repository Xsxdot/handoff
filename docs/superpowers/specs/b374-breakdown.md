# B374 拆解稿：房间列表强制分页 + attach 刷新限域 + 日志三件套

状态：**出稿待拍板**（2026-09-15；handoff 派发形态——本稿由 executor 出稿，本地协调者拍板）
卡：B374
标题：房间 attach 刷新风暴拖慢事件流与页面
定级：**L3 轻档**；路由：contract → breakdown → implement → review → acceptance → finish
有效基线：**未设置**（工作分支 `cards/B374-charter-5`；merge-base origin/main `41a284746501c90aa31ee72e7389851f8c2acb62`；不假定 main，需要合并时先向协调者确认）
上游 spec：`docs/specs/2026-09-15-b374-rooms-pagination.md`（头部：**已批准**（2026-09-15，用户审批）；实读台账条目 4）
冻结 contract：`docs/superpowers/specs/b374-contract.md`（提交 `32756584`；头部「上游状态：已批准」「冻结状态：…同批冻结」；实读台账条目 5）
本稿台账：`docs/superpowers/ledgers/2026-09-15-b374-breakdown-ledger.md`
图依据：`codegraph/best.json`（`parent` 为空的顶层领域即子系统清单，类型取 `type`）；分支视图 `codegraph/diffs/cards-B374-charter.json`
角色边界：本文是**提案**；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。扇出与拍板归协调者。

---

## 0. 待拍板岔口清单（集中，拍板者按此裁决）

本稿在契约增量核对中发现 4 处**边界澄清**（C-1..C-4，已回写契约 §9，非自由偏好、不退回 contract），以及以下 P-1..P-6 真岔口，一律**待拍板**，本稿不自行选定往下写。

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **P-1** | **欠账 9（b358 §4.4 修订）的目标文档在本工作树不可达** | 契约 §7 欠账 9 要「修订 b358 §4.4『列表全量』表述」。实读：`b358.md`/`b358-contract.md` 在 HEAD、origin/main、merge-base **均不存在**（只在 `5318092f` 等 B358 沿革分支可达）；且 `5318092f:b358-contract.md` 的 `§4.4 房间形态与只读`（条目 32–35）**不含「列表全量」字面**，`5318092f:b358.md` 的 `§4.4` 是「详情页投影」。全仓「列表全量」字面只在 `b156.2-breakdown.md:39`、`b156.2.8-plan.md:1449`。**方案甲（推荐）**：本卡只落一张「文档修订」子卡，把修订目标**降级为 b156.2.8-plan.md:1449 与 b156.2-breakdown.md:39 两处「列表全量」字面 + 在 b358 文档回到本工作树后可及的下一节点补 b358 §4.4**，并在本卡回写该可达性事实；**方案乙**：把 b358 文档从 `5318092f` 拉进本分支再改（引入本卡不该承担的文档搬运与可能的语义冲突）；**方案丙**：本卡不修，另开文档卡。 | **甲**。欠账 9 的**语义**（旧房间形态/只读/历史可查不变，仅列表访问分页化）可在本卡落；但其**字面载体**不在本树，需协调者确认修订对象，避免改错文档。 |
| **P-2** | **欠账 8（镜像发现超时定性）是否可在本卡机内闭环** | 契约 §7 欠账 8：`linux-01 context deadline exceeded` 是刷新风暴次生症状还是独立故障。实读：`internal/agentd/mirror.go:47-48` 是 `mirrorDiscoverBudget = 3 * time.Second`、`:186` 对**全部 target 共享一个预算**。**方案甲（推荐）**：本卡只交付「机内可验的确定性判据」——镜像发现是**独立于房间列表刷新**的 30s 周期循环（`mirrorDiscoveryTick`），二者无共享锁/共享 goroutine，故 code-path 上**不是**刷新风暴的次生；「真实 linux-01 是否为 3s 预算耗尽/网络抖动」属真机行为事实，进真机清单归协调者；**方案乙**：本卡内给 `discoverOnce` 加 per-target 预算或加大预算（改行为，超出 spec「本期排查定性」的范围，且无真机复现前属拍脑袋）。 | **甲**。定性拆两层：**通路独立性（机内可判，就是本卡结论）** + **真实超时归因（真机）**。乙会越出 spec「排查定性」承诺。 |
| **P-3** | **F9 legacy 426 落地后，13 处既有 agentd 测试请求要改成带参** | 实读台账条目 21：`roomsapi_test.go` 有 14 处 `/api/rooms` 列表请求无 `limit`/`cursor`（623 处走未装配 503 分支不算），F9 落地后**13 处将命中 426 而变红**。**方案甲（推荐）**：T1 同批把这 13 处请求改成带 `limit=50`（保留断言语义），并**新增**一支无参请求断言 426 + 冻结文案——「旧客户端被阻断」本身有能变红的测试；**方案乙**：让测试改走 `ListRoomsPage` service 层直调，绕开 HTTP（丢掉 handler 级 426 覆盖）。 | **甲**。乙会让「F9 在 handler 上可观测」失去测试面（契约 §3.3 明写 F9 的 HTTP 路径只有真解析落地后才可达）。 |
| **P-4** | **F17/F18 刷新限域的判定输入形状** | 契约 P1 定「按本页房间 ID 集合」，`enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary)` 签名不变。**方案甲（推荐）**：保留 `linksByRoom` 全量建索引（一次 `AllTaskLinks()` 读取，成本不变），但**只遍历 `rooms`（本页）取子集**用于投影，并把**该子集**传 `startRoomAttachRefresh`；**方案乙**：改 `AllTaskLinks` 读取面为按房间过滤（需查账本是否支持按 card 过滤，且会改动 `ledger.Store` 面）。 | **甲**。乙触及 `d_ledger` 读面与预算，越出本卡 B374 的「只收窄入参」形状（契约 P1 明确「调用方收窄入参即限域，刷新体不改」）。 |
| **P-5** | **F19（默认级别改 warn）会打红既有 `TestSetupWritesJSONToFile`** | 变异实测（台账条目 22）：默认改 warn 后 `internal/logx/logx_test.go:24` FAIL（它用 Info 写文件并断言落盘）。**方案甲（推荐）**：改测试为**显式设置** `HANDOFF_LOG_LEVEL=info` 后再断言落盘内容（该测试的意图是验 JSON 落盘，不是验默认级别），并**新增** `TestSetupDefaultLevelWarn` 断言 `parseLevel("")==slog.LevelWarn`（把默认级别这条产品语义单独钉住）；**方案乙**：把既有测试的 `log.Info` 改成 `log.Warn` 迁就新默认（测试变绿但丢失「默认级别」的独立断言面）。 | **甲**。乙是典型「改测试迁就实现」的假绿温床；契约 F19 要求默认级别是**产品语义**，须有独立能变红的测试锁。 |
| **P-6** | **F20 单写形态对现有测试无牙，验收怎么落** | 变异实测（台账条目 23）：去掉 stderr 或文件任一路，`internal/logx/ -count=1` 仍 `ok`。**方案甲（推荐）**：新写一支单写回归——带 `logPath` 时，用可注入的 stderr 目标（或 `os.Pipe` 捕获）断言「同一条记录在文件恰出现一次、且不落 stderr」；**方案乙**：只断言文件内容恰一次（不覆盖 stderr 那一路，漏掉 F20 的「不挂 stderr」半边）。 | **甲**。契约 F20 说的是两件事（落盘恰一次 **且** 不挂 stderr handler），只测一边会给「顺手把 stderr 也挂回去」留假绿。 |

> 若协调者对 P-3/P-5/P-6 的裁决改变既有测试基线或契约 §5 判据，应先回写本稿 §5 与契约 §9 修订记录，再扇出实现；本稿不自行吸收。

---

## 1. 触及子系统清单与派卡资格核

顶层领域以 `codegraph/best.json` 中 `domains` 的 `parent` 缺省项为准（台账条目 6），容器→域映射实读（台账条目 7）。**子系统按 best.json：`d_gateway` / `d_collab` / `d_web` / `d_policy`**；协作房间是 `d_collab`（**不是** `d_sessions`，那是终端 PTY）；logx 归 `d_policy`（**不是** `d_runtime_config`，该域在 best.json 不存在——`codegraph sym` 单点输出仍报的残留 id 见契约 §9 C-4）。

每行按架构法第一条逐项核四个派卡资格：①有界文件集；②暴露面可枚举；③依赖 DAG/冻结契约已指明；④类型与验收方式明确。

| 子系统（图 id） | 图类型 | 本卡角色 | ①有界文件集 | ②暴露面可枚举 | ③DAG/契约 | ④验收分流 |
| --- | --- | --- | --- | --- | --- | --- |
| `d_gateway` 控制门面 | boundary | HTTP 入口：参数解析、426 阻断、错误映射、attach 限域调用点 | `internal/agentd/roomsapi.go`、`internal/agentd/roomsapi_test.go`、`internal/agentd/roomslist_status_test.go`；装配只读 `internal/agentd/ledgerapi.go`（路由）、`internal/agentd/server.go#Server.withRooms` | 契约 §3.3：`parseRoomsListParams`/`roomsListParams`/`roomsListErrorStatus` + `handleRoomsList` 组装形状；F5–F10、F17/F18 | 复用既有 `d_gateway→d_collab`（entry「collab 入站门面」，budget 0→1 legacy）、`d_gateway→d_orchestration`（被调方 `k_agentd_fn`）；不改 target 口径 | boundary（接缝对面是浏览器/CLI 的 HTTP 现实）。机内 httptest 可闭环 handler 形状、426、503、限域入参；真实旧桌面端 426 呈现走真机清单 |
| `d_collab` 协作房间 | logic | 分页组装与真裁剪：游标定位、页切、has_more/next | `internal/collab/service.go`、`internal/collab/roomcursor_test.go`、新增分页测试文件（`internal/collab/` 内） | 契约 §3.2：`ListRoomsPage`（签名已冻结）+ `trimRoomPage`/`encodeRoomCursor`/`decodeRoomCursor`（私有）；F11–F16 | 依赖 `d_protocol`（`k_proto_model`，entry「proto 实体」，budget 1）与同域 `listRooms`；`ListRoomsForMember` 签名不变 | logic（对面是同仓 `listRooms` 输出与 `proto.RoomSummary`，`t.TempDir`/fake 可机内闭环） |
| `d_web` Web 控制台 | logic | TS wire 镜像 + 会话列表懒加载 | `web/src/api/rooms.ts`、`web/src/api/rooms.fetch.test.ts`、`web/src/api/rooms.test.ts`（如加孪生样本）、`web/src/app/rooms/RoomPanel.tsx`、`web/src/app/rooms/RoomPanel.test.tsx`、`web/src/app/shell/Shell.test.tsx`（mock 同步） | 契约 §3.5 已冻结形状：`RoomsPage` interface + `fetchRooms(opts?)`；F22 | 复用既有 `k_web_api_rooms→d_web_contract`；不新增跨域边 | logic（TS 类型/组件可机内闭环）。本工作树**无** `web/node_modules`：vitest/tsc 本轮不可执行（台账条目 24，未验证），真机清单承载 |
| `d_policy` 运行策略与配置 | logic | 日志三件套：默认级别、单写、轮转 | `internal/logx/logx.go`、`internal/logx/logx_test.go`；调用点只读 `cmd/agentd.go:69`、`cmd/wait.go:111`、`cmd/card_wait.go:101`（不带 logPath 语义不变） | 契约 §3.4：`Setup` 签名不变 + 内部 handler 形态；F19–F21 | 无新增跨域边（`k_logx_*→d_policy` 同域） | logic（日志 handler 行为机内可闭环：临时文件 + pipe 捕获） |

**邻接但不派卡**（只消费/零改动）：

- `d_protocol`（logic）：`internal/proto/rooms.go#RoomsPage` 与 `RoomSummary` 已由 Ticket 0 冻结落盘；本卡零改动。`RoomsPage` 金样本 `TestRoomsPageGoldenEnvelope` 已锁（台账条目 11）。
- `d_orchestration`（logic）：`k_agentd_fn`（`parseRoomsListParams`/`roomsListErrorStatus` 容器）承载 `d_gateway→d_orchestration` 既有预算 321；本卡不改其语义。
- `d_cli`（logic）：`cmd/room.go:59` 走 `svc.ListRooms`（全量，无 member、不涉 HTTP attach 刷新）；wire 分页**不改变** CLI 列表语义，零改动。
- `d_workspace`（boundary）：`internal/agentd/mirror.go#Mirror.discoverOnce` 只读，用于欠账 8 的通路独立性判定（P-2）；不改其代码。
- `d_maintenance`（boundary）：`internal/service/launchd.go#plistBody` 只读，用于确认 P2「manager 不改」；零改动。

**竖切债核对**：`internal/agentd` 是 84 文件平铺大包（`ls internal/agentd/*.go | grep -v _test | wc -l` = 84），但本卡只圈 `roomsapi.go` 一文件 + 两支测试；`d_collab` 生产文件仅 `service.go` 一个；`d_web` 圈 `rooms.ts` + `RoomPanel.tsx` 两文件；`d_policy` 圈 `logx.go`。**能圈出有界文件集，不插竖切还债卡**。实现若需改上述集合之外的生产文件，必须退回协调者重核边界。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- spec `docs/specs/2026-09-15-b374-rooms-pagination.md:3`：`状态：已批准（2026-09-15，用户审批）` —— 已批准，核对通过（台账条目 4）。
- contract `docs/superpowers/specs/b374-contract.md:3-8`：`上游状态：已批准`、`冻结状态：本提交随 codegraph/diffs/cards-B374-charter.json 与 Ticket 0 骨架同批冻结`、`本轮性质：contract 重冻（用户拍板 A）` —— 已冻结，核对通过（台账条目 5）。
- **头部分支标签漂移（C-1）**：契约头 §3 写「工作分支 `cards/B374-charter-3`」，本稿在 `cards/B374-charter-5` 开工；冻结提交 `32756584` 两分支均可达。以提交 hash 为准，不退回 contract。已回写契约 §9。

### 2.2 逐条核对本稿是否越界 / 需退回 contract

| 核对项 | 结论 |
| --- | --- |
| §3.1 `proto.RoomsPage` 信封（F1–F4） | 已冻结落盘，Ticket 0 已锁金样本；本稿零改动、零新接缝。T4 复验。 |
| §3.2 `collab` 分页（F11–F16） | `ListRoomsPage`/`trimRoomPage`/`encodeRoomCursor`/`decodeRoomCursor` 签名已冻结；本稿 T2 只填真裁剪正文，不新增符号、不改签名。`ListRoomsForMember` 保持全量（spec 接缝 6 字面**不**回改）。 |
| §3.3 `agentd` 参数与错误映射（F5–F10） | `parseRoomsListParams`/`roomsListParams`/`roomsListErrorStatus` 签名已冻结；本稿 T1 只填真解析 + 426 分支 + 限域调用点，不新增符号。 |
| §3.4 日志三件套（F19–F21） | `logx.Setup` 签名不变；单写形态 P2 已钉死（带 logPath 只挂文件 JSONHandler）；轮转 100MB×5 为冻结建议值。本稿 T4 落正文，不新增导出符号。 |
| §3.5 TS 镜像（F22） | `RoomsPage` interface + `fetchRooms(opts?)` 形状已冻结；本稿 T3 落码 + 懒加载。**TS 孪生金样本未锁**是契约 §7 已记欠账，不隐藏。 |
| §4 图契约面 | 新边均落既有 entry（`d_gateway→d_collab` 的「collab 入站门面」、`d_collab→d_protocol` 的「proto 实体」），`target.json` 无口径增量；分支视图 `validate=null`、`check fails=[]`（台账条目 10）。本稿不新增跨域边。 |
| §5 冻结清单 | 本稿验收栏逐条映射 F1–F22，无新增判据、无改判据。 |
| §7 交棒欠账 1–10 | 1→T1、2→T2（**P5 依赖：1 先于 2**）、3→T1（限域）、4→T4、5→T3、6→保持 `ListRoomsForMember`（T2 反例）、8→T5（P-2）、9→T5（P-1）、10→勘误已在契约 §0，本稿沿用。 |

### 2.3 新接缝判定

**无新增接缝**。本稿引用的全部符号（`handleRoomsList`/`parseRoomsListParams`/`roomsListErrorStatus`/`enrichRoomAttachments`/`startRoomAttachRefresh`/`ListRoomsPage`/`trimRoomPage`/`encodeRoomCursor`/`decodeRoomCursor`/`ListRoomsForMember`/`logx.Setup`/`parseLevel`/`RoomsPage`/`fetchRooms`）均已在契约 §3 冻结或 Ticket 0 落盘，`codegraph resolve --view cards-B374-charter` 逐条 `ok/moved`（台账条目 8）。**边界澄清 4 条（C-1..C-4）已回写契约 §9，不退回 contract。**

---

## 3. 子卡清单 + 依赖 DAG

### 3.0 子卡形态论证

契约 P5 明定：**先落 T1（Legacy 探测 + 426，F5–F9），再落 T2（真裁剪，F12–F16）——这两张不能并行**。T3（web）与 T4（日志）与 T1/T2 无文件交叠，可并行；T5（欠账）依赖 T1–T4 的实现事实，收口全卡。L3 轻档，无直通竖切。

```
T1 gateway 参数真解析 + legacy 426（d_gateway）──┐  [必须先]
                                                  ├─→ T5 欠账收口（8/9 定性 + 文档）
T2 collab 真裁剪 + 游标（d_collab）──────────────┘  [T1 之后]
T3 web 镜像 + 懒加载（d_web）────────────────────────┘  (可与 T1/T2 并行)
T4 日志三件套（d_policy）────────────────────────────┘  (可与 T1/T2 并行)
```

### 3.1 子卡 T1 —— gateway 参数真解析 + legacy 426 + 限域调用点

**① 契约引用**：契约 §3.3（`parseRoomsListParams` 语义 1–4、`handleRoomsList` 组装形状、426 文案）、§3.4②（roomsapi 逐 task INFO 降 Debug）、§2（旧客户端策略 = 阻断）；F5–F10、F17/F18；拍板 P3/P4/P5/P1。

**② 意图与为什么**：Ticket 0 已把 handler 切到 `ListRoomsPage`，但 `parseRoomsListParams` 仍是空壳（不读 `*http.Request`，`Legacy` 恒 false）。**P5 要求 426 必须先于真裁剪**：若先真裁剪，无参 `GET /api/rooms` 会先命中 `limit=50` 默认、静默返回半页，正是 US5 要消除的「不明不白的半页」——而没有任何既有测试会红。同时把 `enrichRoomAttachments` 的远端 fan-out 从全量收窄到本页房间（P1），这是风暴的正面闸门。

**③ 验收（行为化，判据均可机内红绿）**：
1. 真解析：`/api/rooms?limit=abc` → HTTP 400（F8）；`?limit=0`/`?limit=-1` → 默认 50（F7）；`?limit=500` → 200（F6）；`?cursor=<合法>` 不带 limit → 默认 50（F5 的已判定分页客户端路径）。
2. legacy：`/api/rooms` 与 `/api/rooms?project=p`（**limit 与 cursor 双双缺席**）→ HTTP **426** + 冻结文案 `{"error":"客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。"}`（F9）；断言 426 是**能变红**的（把 legacy 探测写反→返回 200 半页即红）。
3. 游标非法：`/api/rooms?cursor=!!!` → 400 且经 `errors.Is(err, collab.ErrInvalidCursor)` 识别（F10）；非游标组装失败 → 500。
4. 刷新限域（P1/P-4）：`enrichRoomAttachments` 只对本页 rooms 的 links 投影；`startRoomAttachRefresh` 收到的集合 ⊆ 本页房间对应远端挂账（F17/F18）。测试用「页 A 有远端挂账、页切到不含 A 的页 B」断言页 B 请求**不触发** A 的远端 RPC（httptest 计数）。**注意**：F17/F18 的「远端 RPC 未发生」是行为事实，httptest 计数器可机内闭环；真实 relay 行为走真机清单。
5. 既有 13 处无参列表测试同步改带参（P-3），保留原断言语义；新增无参 426 测试；`TestRoomsEndpoints503WithoutLedger`（623 行）保持 503 语义。
6. `roomsapi.go` 逐 task/逐次 INFO（`:205`/`:211`/`:218`/`:278`/`:283`）降 Debug；保留 `:114`/`:411`/`:507`（非高频或用户动作）。降级后 `go test ./internal/agentd/ -run TestRooms` 全绿。
7. 缺陷族结论入栏：族 1（见 §4）、族 2（426 文案可行动；解析失败 400 带原因）、族 5（426/400 与 503 三态在同一 `withRooms` 门内，鉴权代理不绕过）。

**④ 入口指针与有界文件集**：`internal/agentd/roomsapi.go#Server.handleRoomsList`（:93）、`#parseRoomsListParams`（:79）、`#roomsListErrorStatus`（:123）、`#Server.enrichRoomAttachments`（:151）、`#Server.startRoomAttachRefresh`（:223）；路由与门 `internal/agentd/ledgerapi.go:49`、`internal/agentd/server.go#Server.withRooms`（:2473）。有界文件集 = `internal/agentd/roomsapi.go`、`internal/agentd/roomsapi_test.go`、`internal/agentd/roomslist_status_test.go`（既有，预计扩断言）。

### 3.2 子卡 T2 —— collab 真裁剪 + 游标定位（**依赖 T1，不得并行**）

**① 契约引用**：契约 §3.2（裁剪规则 1–4、`ListRoomsPage` 归属说明）、§2 分页语义；F11–F16；拍板 P6；`ListRoomsForMember` 保持全量（spec 接缝 6 字面不回改）。

**② 意图与为什么**：Ticket 0 的 `trimRoomPage` 是直通镜像（返回整表、`hasMore=false`）。真裁剪让每页只返回 ≤ limit 条并给 `next_cursor`。**P6 是承重规则**：主判据必须是「当前扁平序里 `roomID` 的位置」，兜底（房间已不在列表）按 `LastActivity` 时刻跳过，**不比 ID 序**——`listRooms` 的扁平序与 ID 序不同构，ID 序只在同刻条目暴露且常规测试不红。

**③ 验收（行为化）**：
1. 金样本与游标：`encodeRoomCursor(Unix(0,1700000000000000000).UTC(), "B42")` = `eyJhIjoxNzAwMDAwMDAwMDAwMDAwMDAwLCJyIjoiQjQyIn0`；往返一致；`""` 解码零值 + nil error（F11，已有 5 支测试保绿）。
2. 不丢不重（常规）：沿扁平序翻完全部页，并集 = 全量、交集 = ∅（F12）；主判据 = 游标 roomID 位置之后起算（F13）。
3. 兜底（F14）：游标所指房间已不在列表 → 跳过所有 `LastActivity` 不早于游标时刻的条目；**测试须显式断言「同刻插入序不可恢复」是已知限制**（构造同刻多条目 + 房间消失，断言不假装不丢不重）——这是 P6 的「反过来写不会红」防线。
4. `has_more=false` ⟺ 末页无剩余；`next_cursor` 非空 ⟺ `hasMore=true`（F15）。
5. 终态卡房间仍可达（分页不剪枝），`ReadOnly` 标记不变（F16）。
6. `ListRoomsForMember` 签名与全量语义不变：`TestListRoomsForMemberScansEventsOnceForUnreadAndActivity`（205 卡、`eventReads==1`）保绿（契约 §7 欠账 6）。
7. 反例（P6 有牙）：把主判据改成 `x.ID > r` 兜底 → 同刻条目用例必须变红（否则测试无牙）。
8. 缺陷族结论入栏：族 1（无状态机中断：纯函数切片，无 goroutine/资源）、族 4（反例见 7）、族 2（非法游标裹 `ErrInvalidCursor`，经 T1 映射 400）。

**④ 入口指针与有界文件集**：`internal/collab/service.go#Service.ListRoomsPage`（:333）、`#trimRoomPage`（:397）、`#encodeRoomCursor`（:354）、`#decodeRoomCursor`（:366）、`#Service.listRooms`（:404，排序 :499-513）；`internal/collab/roomcursor_test.go`、`internal/collab/readmodel_test.go`（`ListRoomsForMember` 反例）。有界文件集 = `internal/collab/service.go` + `internal/collab/` 内新增/扩展分页测试文件（落点命名归 plan）。

### 3.3 子卡 T3 —— web TS 镜像 + 会话列表懒加载

**① 契约引用**：契约 §3.5（`RoomsPage` interface、`fetchRooms(opts?)`、首屏显式带 limit、`next_cursor` 原样回传、`has_more=false` 终止）；F22。

**② 意图与为什么**：现状 `fetchRooms(project='')` 只解包 `rooms` 数组、`RoomPanel` 整表轮询全量渲染——首屏 366 行。改为首屏带 `limit=50` 的一页 + 滚动续载，让页面不再等全量组装。

**③ 验收（行为化；**web 测试本轮不可执行**，下述命令为真机/有 node_modules 时执行，未跑前不宣称通过）**：
1. `rooms.fetch.test.ts`：`fetchRooms({ limit: 50 })` 请求 URL 含 `limit=50` 且**显式带 limit**（不带即 legacy，见 P4）；`fetchRooms({ cursor })` 原样回传游标；`has_more=false` 时不再请求下一页（F22）。
2. TS 类型：`RoomsPage` interface 与 Go 信封逐字段一致；孪生金样本（如加）以缺键/零值可分辨。
3. `RoomPanel.test.tsx`：首屏渲染一页；`has_more=true` 时滚动到底触发 `fetchRooms({ cursor })` 续载；`has_more=false` 终止。
4. `Shell.test.tsx` 的 `vi.mock('../../api/rooms')` 与新签名对齐（否则 mock 失配）。
5. 缺陷族结论入栏：族 2（426 时前端渲染可行动升级提示，非空列表静默）、族 4（「不带 limit」请求形态有反断言）、族 3（见 §4）。

**④ 入口指针与有界文件集**：`web/src/api/rooms.ts#fetchRooms`（:85）、`web/src/app/rooms/RoomPanel.tsx#RoomPanel`（:115，`loadRooms` :171、渲染 :355）；测试 `web/src/api/rooms.fetch.test.ts`、`web/src/api/rooms.test.ts`、`web/src/app/rooms/RoomPanel.test.tsx`、`web/src/app/shell/Shell.test.tsx`。有界文件集 = `web/src/api/rooms.ts`、`web/src/app/rooms/RoomPanel.tsx` + 上述测试。

### 3.4 子卡 T4 —— 日志三件套（默认级别 / 单写 / 轮转）

**① 契约引用**：契约 §3.4① ② ③、§1 表（双写来源、`multiHandler` 广播事实）；F19–F21；拍板 P2。

**② 意图与为什么**：`agentd.log` 涨到 19.6G 的三因——逐 task INFO 刷屏、同文件双写、无轮转。T1 已降 INFO；本卡修单写（带 logPath 只挂文件 JSONHandler，不挂 stderr——**P2 钉死形态**，manager 的 launchd 双重定向不改）与轮转（100MB×5）。

**③ 验收（行为化）**：
1. `parseLevel("")==slog.LevelWarn`（F19）；INFO 仅在显式 `HANDOFF_LOG_LEVEL=info/debug` 下出。**新增独立测试**（P-5 甲）；把既有 `TestSetupWritesJSONToFile` 改为显式设 `HANDOFF_LOG_LEVEL=info` 后验落盘（保留其 JSON 落盘意图）。
2. 单写（F20）：带 `logPath` 时同一记录**只落文件一次**且**不落 stderr**（捕获 stderr 断言为空/不含该记录）；不带 `logPath` 时仅 stderr 文本。**新写一支能变红的单写断言**（P-6 甲；现有测试对 F20 无牙，台账条目 23）。
3. 轮转（F21）：写超 100MB 触发轮转，最多保留 5 份，第 6 份挤掉最旧；轮转 handler 在 logx 内新增（包注释「不管理日志轮转」同步修订）。
4. 调用点语义不变：`cmd/agentd.go:69`（带 logPath）与 `cmd/wait.go:111`/`cmd/card_wait.go:101`（不带 logPath）行为分别正确；`HANDOFF_LOG_LEVEL` 语义不变。
5. 缺陷族结论入栏：族 1（轮转中途进程重启：单写文件 append，轮转改名原子性由实现定，重开文件时机归 plan §6）、族 2（文件不可写仍降级 stderr 并 Warn，既有行为保留）、族 3（路径/改名跨平台，Windows 打开文件占用——见 §4 族 3）。

**④ 入口指针与有界文件集**：`internal/logx/logx.go#Setup`（:31）、`#parseLevel`（:46）、`#multiHandler.Handle`（:75）；调用点 `cmd/agentd.go:69`、`cmd/wait.go:111`、`cmd/card_wait.go:101`。有界文件集 = `internal/logx/logx.go`、`internal/logx/logx_test.go`（+ 若轮转拆文件则 `internal/logx/` 内新增一文件，落点归 plan）。

### 3.5 子卡 T5 —— 欠账收口（8 镜像超时定性 / 9 文档修订）

**① 契约引用**：契约 §7 欠账 8、9；spec「镜像发现超时定性」「b358 §4.4 修订」；P-1/P-2。

**② 意图与为什么**：spec 对这两条有明确承诺，不能丢。欠账 8 定性两层（机内通路独立性 + 真机归因）；欠账 9 的语义（旧形态/只读/历史可查不变，仅列表访问分页化）要落，但字面载体本树不可达，需拍板。

**③ 验收（行为化）**：
1. **欠账 8（机内层）**：断言/记录镜像发现循环与房间列表刷新的**通路独立性**——`Mirror.discoverOnce`（30s tick，`mirrorDiscoverBudget=3s`）与 `handleRoomsList→enrichRoomAttachments→startRoomAttachRefresh` 无共享锁、无共享 goroutine、无共享远程调用；结论写进卡（「code-path 上不是刷新风暴的次生」）。**真实 `linux-01 context deadline exceeded` 归因**（3s 预算耗尽 vs 网络抖动 vs 刷新风暴次生）标「未验证，需真机」。
2. **欠账 9**：按 P-1 裁决落修订——语义表述（旧房间形态、只读、历史可查语义不变，仅列表访问分页化）写进**可达的载体**（b156.2.8-plan.md:1449 / b156.2-breakdown.md:39 的「列表全量」字面，或协调者指定的 b358 文档）；b358 §4.4 的可达性事实回写卡。
3. 缺陷族结论入栏：族 2（欠账 9 的修订对象若搞错＝静默改错文档；用 P-1 显式裁决堵住）。

**④ 入口指针与有界文件集**：`internal/agentd/mirror.go#Mirror.discoverOnce`（:183）、`#Mirror.Run`（:130）、`mirrorDiscoverBudget`（:47-48）；文档 `docs/superpowers/plans/b156.2.8-plan.md:1449`、`docs/superpowers/specs/b156.2-breakdown.md:39`（或 P-1 指定）。有界文件集 = 文档改动（按 P-1 裁决）+ 卡上定性记录；**不改 mirror.go 行为**（P-2 甲）。

### 3.6 行为闭环核对（spec 跨子系统可观察行为 → 归属）

只核产品行为；每行五格：触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 归属子卡。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属 |
| --- | --- | --- | --- | --- |
| 新 web 打开 IM/工作项列表 | `fetchRooms({limit:50})` → `GET /api/rooms?limit=50` → `ListRoomsPage` 首页 | RoomPanel 列表 | 首屏 ≤ 50 行、`has_more`/`next_cursor` 随信封返回 | T1+T2+T3 |
| 用户滚动到底 | `has_more=true` → `fetchRooms({cursor})` | RoomPanel | 追加下一页；`has_more=false` 终止 | T2+T3 |
| 未升级旧桌面端打开列表 | 无 limit/cursor 的请求形态（P4） | `handleRoomsList` → 426 | HTTP 426 + 可行动升级文案，绝不半页 | T1 |
| 打开列表（任意页） | 本页 rooms（P1）+ 其远端挂账集合 | `enrichRoomAttachments`/`startRoomAttachRefresh` | 只为当前页房间 fan-out Attach RPC；未返回房间零 RPC | T1 |
| 房间发言 / `card wait` | agentd 进程日志（warn 默认 + 单写 + 轮转） | 磁盘 IO / 事件流 | 日志增速常态 <1KB/s、文件上限 5×100MB、同记录落盘一次 | T4 |
| 翻页跨过终态卡房间 | 扁平序 sunk 段（F16） | `trimRoomPage` | 终态房间仍可达、`ReadOnly` 标记不变 | T2 |
| 列表请求命中非法游标 | `decodeRoomCursor` → `ErrInvalidCursor` | `roomsListErrorStatus` | HTTP 400（非 500） | T1+T2 |

每行五格完整，归属子卡均存在；无只活在接口或测试里的承诺。

---

## 4. 缺陷族对抗审查（通用五族 + 追加设问，逐族正面回答）

**族 1 生命周期 / 状态机中断**
- 分页与参数解析是**纯请求内计算**，无 goroutine、无临时资源、无状态机迁移；中途 agentd 重启不残留（请求失败即重试）。**无风险，因为** T1/T2 不新增后台态。
- attach 后台刷新（T1 限域）沿用 Ticket 0 既有 goroutine + `roomAttachRefreshing` 门 + `context.WithTimeout(10s)`；限域只改**入参**，不改生命周期。重启时在飞 goroutine 随进程消亡，缓存是内存态（重启即空，重拉即重建）——既有行为，非本卡新增孤儿。
- 日志（T4）：轮转在单条写入后检查并改名重开，「写入中途崩溃」窗口由实现定（重开时机归契约 §6 plan 区）；**无新增需收尾的运行时资源**（无独立 goroutine，除非实现选后台轮转——本稿倾向同步检查）。轮转中的临时/改名残留属 T4 验收需覆盖项。

**族 2 静默失败 / 误导报错**
- legacy 426（T1）是**刻意阻断**：宁可见 426 也不给半页（US5）。文案冻结、可行动。
- 参数非法（`limit=abc`）→ 400 带原因；游标非法 → 400（经哨兵 `errors.Is`）；列表组装失败 → 500。三态互不吞没（F8/F10 + `roomsListErrorStatus` 反例锚 `TestRoomsListErrorStatusDistinguishesServiceFailures` 已锁）。
- **「报成功但没做」窗口**：分页 `has_more`/`next_cursor` 若算错会静默丢页——F12/F15 的「并集=全量、交集=∅」是能变红的堵口；F14 显式承认同刻限制，不假装。
- 限域（F17/F18）：若实现漏传本页 links 或仍传全量，**功能不报错**（attach 照样投影），只是风暴回来——属「报成功但没做」，用 T1 验收第 4 条的 RPC 计数反例堵住。
- 日志（T4）：文件不可写仍降级 stderr + Warn（既有），不静默丢。

**族 3 跨平台假设**
- launchd 双重定向（macOS）是 P2 的**既定不改**面；单写修复在 agentd 侧，跨平台一致（`logx` 纯 Go）。systemd/windows 不重定向 stdout/stderr，`parseLevel`/轮转与平台无关。
- 轮转的文件改名/重开在 **Windows** 有「文件被其他句柄占用」（如 launchd 重定向的 stdout 句柄）导致改名失败的风险——本卡不改 manager，故 Windows 上 agentd.log 由 logx 打开，改名在 logx 自己关闭句柄后可行；但**真实 Windows 句柄竞争机内造不出**，标「未验证，需真机」（真机清单）。
- 路径拼装走 `filepath`；`RoomsPage`/游标是纯字符串，无路径/权限假设。web 侧无 webview 新假设（T3 只改 fetch 与渲染）。
- **无其他**，因为本卡不新增平台专属 API、不碰进程组/权限模型。

**族 4 假红 / 假绿测试**
- 验收判据均为**行为化终态**（状态码/字节计数/页并集/落盘次数），非中途副产物。
- 反例清单（各自能变红）：legacy 写反→200 半页红（T1.2）；ID 序兜底→同刻用例红（T2.7）；`has_more` 算错→并集断言红（T2.2）；限域漏收窄→RPC 计数红（T1.4）；默认级别回 info→`TestSetupDefaultLevelWarn` 红（T4.1）；单写挂回 stderr→单写断言红（T4.2）。
- **T1.2 的 426 断言锁的是产品承诺（旧客户端须被阻断）**，换实现不改需求不会无意义地红。
- 已识别假绿温床：既有 14 处无参列表请求在 426 落地后若**不改**会变红——但若有人把 legacy 探测写成「缺 limit 也按默认」则全绿而 US5 失守（P4/P5 的「反过来写不会红」），故 426 必须有独立断言，不靠既有测试数量。
- 夹具行为假设 vs 真机：httptest 的远端 RPC 计数**不能**证明真实 relay 行为——真机清单第 4/5 条对应。
- 「两端各自有测试」≠「链路有测试」：T3 的 fetch 断言 + T1 的 handler 断言各自绿，**链路**由 T5 收口 + 真机一条穿透（见追加设问一）。

**族 5 门禁绕过**
- `/api/rooms` 在 `s.auth(mux)`（`internal/agentd/server.go:747`）内，并经 `withRooms`（`:2473`）守 503。新增的 426/400 分支都在同一 `handleRoomsList`（同一门内），无旁路入口。T1 验收第 7 条以未鉴权请求断言门仍关。
- **检查与动作之间无窗口**：参数解析、游标解码、页裁剪、限域入参构造**全在单次请求内**做完，无「先检查后动作」的 TOCTOU。
- 刷新限域的「门」是**入参收窄**，无独立开关；全部 attach 刷新入口只有 `enrichRoomAttachments` 一处调 `startRoomAttachRefresh`（`grep` 实证），共享同一收窄。
- 日志写入无权限门语义（本地文件），不涉门禁。

**追加设问一：序列化边界**
- 新增/改动字段：本卡**不新增 wire 字段**（`RoomsPage` 已由 Ticket 0 冻结）。但涉及的手写投影链必须逐处列：Go `proto.RoomsPage` → `writeJSON`（json tag，金样本 `TestRoomsPageGoldenEnvelope` 锁）→ HTTP body → TS `request<{rooms,...}>` 解码 → `RoomsPage` interface → RoomPanel 渲染；游标 `roomCursor{A,R}` → `encodeRoomCursor`（base64url）→ wire → `decodeRoomCursor`（`map[string]RawMessage` 逐键校验）。
- 「两端各自有测试」≠「链路有测试」：**必须有一条穿过真实序列化边界的回归**（T5 收口：httptest 产出真实 JSON 两形状 → `fetchRooms` 解码 → 断言 `has_more` 缺席/零值与 `next_cursor` 区分）。推荐 roundtrip 属性测试（游标 encode∘decode 恒等），覆盖缺失/零值分辨。
- 游标金样本（F11）已在 Ticket 0 锁；TS 孪生金样本**未锁**（无 node_modules），显式记欠账（契约 §3.5、本稿真机清单）。

**追加设问二：枚举新值过既有白名单**
- 本卡**不新增枚举取值**：`RoomsPage` 字段、游标键 `a`/`r`、日志 handler 形态都不是新枚举；不引入新状态名/事件类型/kind。
- `RoomsPage.HasMore` 的布尔与 `NextCursor` 的 omitempty 流经点：Go 编码（金样本）、TS 解码（T3）、RoomPanel 续载判定（T3）——无中间校验器/switch 会挡死。**无风险，因为** 无第三方消费方持白名单。
- `room.Kind`（card/project/global）与 `ReadOnly` 语义不变（F16），既有白名单不受影响。

**追加设问三：承重安全属性有测试锁住**
- 「旧客户端被阻断（不得半页）」（T1.2）、「翻页不丢不重」（T2.2）、「同刻限制被显式承认而非假装」（T2.3）、「本页之外零远端 RPC」（T1.4）、「同一日志记录落盘恰一次」（T4.2）——每条均有**能变红的测试**锁着，不是「实现里恰好为真」。
- 一次性 token/唯一性/隔离：**不命中**，因为本卡不新增 token、租约、席位或唯一性约束。

**追加设问四：webview / 平台表现差异（d_web 必答）**
- T3 改 `fetchRooms` 报错路径时，426 在浏览器侧经 `parseResponse`（`resp.ok` 为假 → `ApiError(426, detail)`），detail 取自 agentd 的 `{"error":...}`——RoomPanel 需渲染为可行动提示（族 2）。
- **跨平台**：Chromium/WKWebView/Wails 的 fetch、滚动续载阈值、缓存旧 JS 的真实表现**未验证，需真机**（真机清单）。
- 「页面刷新/缓存旧 JS 命中新 agentd」的 426 呈现须真机核；机内 jsdom 只证组件契约。

---

## 5. 验收映射与交棒

- **冻结清单 → 子卡**：F1–F4（信封）T4 复验；F5–F10（参数/426/游标）T1；F11–F16（游标/裁剪）T2；F17–F18（限域）T1；F19–F21（日志）T4；F22（前端）T3。
- **交棒**：implement（T1→T2 **顺序硬约束**；T3/T4 可并行；T5 收口）。契约 §6 附区（轮转缓冲/重开时机、降级精确行集合、`ListRoomsPage` 是否复用 `listRooms`、web 滚动阈值、`roomsListErrorStatus` 是否并入通用 helper）销区归 plan。
- **本稿不扇出**：子卡清单与 DAG 是提案，建卡与派发归协调者。

## 6. 未验证，需真机清单（归协调者执行）

1. **旧桌面端 426 呈现**：真实未升级桌面 app（agentd 已升级）打开列表 → 看到冻结升级文案而非半页/白屏（US5）。
2. **首屏性能**：本机 366 房间（358 card + 7 project + 1 global）复测首屏 ≤2s（基线 6.8s/止血后 1.06s）；`card wait` 端到端延迟对照，事件秒级到达。
3. **attach fan-out 真量**：翻页后真实远端 Attach RPC 数从 800+/轮降到「本页房间数」量级；未返回房间零 RPC。
4. **日志三件套实况**：真机 `agentd.log` 增速常态 <1KB/s、单条记录落盘恰一次（launchd 重定向下）、100MB 触发轮转且保留 5 份、第 6 份挤掉最旧。
5. **web 懒加载真机**：有 `web/node_modules` 时 `npm test`/`npm run typecheck` 退出 0；Chromium/WKWebView/Wails 的滚动续载、`has_more=false` 终止、缓存旧 JS 表现。
6. **镜像发现超时定性（欠账 8 真机层）**：linux-01 真实 `context deadline exceeded` 归因（3s 预算耗尽 / 网络抖动 / 与刷新风暴的因果）。
7. **TS 孪生金样本**：本工作树无 `web/node_modules`，vitest/tsc 未跑，未新增 `RoomsPage` 孪生样本（契约 §3.5 已记欠账）。
8. **b358 §4.4 修订对象可达性（欠账 9）**：b358 文档不在本工作树/origin/main/merge-base，需协调者确认修订目标（P-1）。

## 7. 图覆盖债

- `codegraph sym` 单点输出对 `n_logx_Setup` 报 `domain=d_runtime_config`（baseline 残留 id），与 `best.json` 的 `k_logx_fn→d_policy` 不一致——属 absorb 未回灌的覆盖债，不归本卡修；子卡归属以 `best.json` 为准（契约 §9 C-4）。
- `codegraph resolve` **必须显式带 `--view cards-B374-charter`** 才识别本卡新符号（不带 view 退出码 1）；本稿 `file#Symbol` 锚在该视图下逐条 `ok/moved`（台账条目 8）。
- 分支视图 `containersAdded {k_collab_model}` 是工具要求（baseline 无、best 有），`validate issues=null`、`check fails=[]`（台账条目 10）。

## 8. 出稿自检（逐项核对）

1. **产出四样齐全**：§1 子系统清单每带类型（best `type` 直取）+ 派卡资格四条；§2 契约增量逐条结论（含 4 条边界澄清回写契约 §9）；§3 子卡四段式且判据行为化、有界文件集核过；§4 缺陷族逐族含「无，因为……」+ 四个追加设问。✔
2. **待拍板岔口集中**：P-1..P-6 全列 §0，正文不散落未标岔口。✔
3. **「未验证，需真机」汇总**：§6 共 8 条。✔
4. **每张子卡有界文件集核过**：§3.1–3.5 ④ 逐条列出；圈不出者已插竖切债结论（§1：无，能圈出）。✔
5. **行为闭环每行五格完整**：§3.6 每行有触发者/载体/消费者/结果/归属，归属子卡存在。✔
6. **契约状态位核对**：spec 已批准、contract 已冻结，从文件头读出（§2.1）；分支标签漂移记 C-1。✔
7. **边界澄清回写**：C-1..C-4 落 `b374-contract.md §9`。✔
8. **未亲自跑到结果的命令未写成结论**：本轮实跑（build/test/graph/变异）见台账 §5–6；未跑（web vitest/tsc、真机项、实现级测试）明确标未验证。✔
9. **`codegraph resolve --doc` 自检**：见台账条目 8/11（本稿收尾亲跑，结果落台账）。✔

## 9. 拍板记录区（留空，待协调者回填）

协调者拍板后，此处逐条回写 §0 P-1..P-6 的裁决与理由，并把头部状态行改为「已拍板（日期）」，与裁决同批提交；状态行与裁决记录不一致视同未拍板。
