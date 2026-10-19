# B444 台账

## P1（S5 push 驻场者续轮）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **转交收件端过滤口径**（plan §3「coordapi.go 只保留 `Card==id` 的卡唤醒」）：实际实现为「丢弃驻场唤醒（`WakeEvent.IsResident()`），其余卡唤醒照旧」。理由：转交批里 task_mirrored 等卡事件的 `WakeEvent.Card` 是**事件卡**（子卡），发送端经 `resolveWakeCard` 冒泡到父卡席位后才转交；按 `Card==id` 过滤会把冒泡的子卡事件全部丢掉（回归）。A1.4 的承重点（驻场唤醒不进卡回合）不变。授权：§6「驻场唤醒在 agentd 内的载体形态……只要满足 A1.1–A1.5」——属执行者可定；与 plan 字面不一致，故列此。
2. **A1.6 的 HTTP 投影落点**：`GET /api/sessions/{id}` 现有响应里没有 `proto.SessionResident`，驻场者只以 `SessionMember{kind:resident}` 出现。为让序列化链走真实入口，给 `proto.SessionMember` 加四键 `driver_mode/carrier/carrier_machine/model`（omitempty，只驻场者行填），`collab.membersOf` 从驻场行填写。HOME/workdir/push 会话 id 不投影到 Web。改动面超出 P1 文件集（`internal/collab/sessions.go` 成员投影、`proto.SessionMember`）；属 plan §8「成员与驻场投影新增 driver_mode 及 bearing 键（omitempty）」的版本化增量，不改既有键。
3. **`pointer_gate_test.go` 白名单加一条**（`internal/agentd/residentpush.go` `Server.pointResidentFailure`）：该守卫要求「新的正当引用在同一提交显式加进白名单并写明理由，不许放宽断言」；plan B3/A1.9 明定失败可见面走 `collab.Service.Pointer`。只新增白名单数据行，未改断言。对照 §6「删除或改写任何既有测试的断言」——未删改断言，但改了既有测试文件，提请审查确认。
4. **迁移只改基表**：`cmd` 包两条既有测试（`TestB435CardWaitOneShotExitsOnRoomHit`、`TestB435CardShowHintReadFailureSilent`）把 `session_residents` 换成同名视图，约定「Open 成功、读面在查询期报错」。新加列迁移对视图 ALTER 必败、会让 Open 整体失败。处置：`migrateResidentPushColumns` 先查对象类型（SQLite `sqlite_master.type`、PG `information_schema.tables.table_type`），非基表 Warn 跳过。未改那两条测试。授权：迁移内部实现，§6 未列；不涉及回填/改写存量行。
5. **驻场回合的小队取自驻场身份**：`agent:<名>` 的名字段即 `AdmitSeatCarrier` 的小队名（spec：push 驻场者身份记 `agent:<小队名>`）；未新增 squad 列。若身份名不是协调小队，准入报错 → 非准入失败路径（指针行 + 退避）。属 §6「驻场唤醒在 agentd 内的载体形态」。
6. **驻场回合注入席位 env：沿用不特判**（§6 明授执行者定）：`coordinatorRunner.Resume` 照旧注入 `HANDOFF_SESSION_CLI/ID`=驻场载体自己的 cli/会话对；驻场者按简报用 `--agent` 发言（`--agent` 不读环境键），该对不对应任何卡席位。
7. **驻场者续轮失败不重建**（含 `ErrSessionNotFound`）：重建要换 push 会话 id、重写驻场行，属编排层（P2 `resident/launch`）。keystone `WakeResident` 只透传错误，agentd 落指针行 + 退避。plan 未规定此分支；属 keystone 私有组织。
8. **push 行同时带桌面端两列也拒**（A1.7 之外加严一条）：push 行带 `cli_session`/`desktop_session` 会让门铃与 keystone 双路唤醒（R2）。同一哨兵 `ErrResidentIdentity`。
9. **新文件拆分**：agentd 驻场批与回合放 `internal/agentd/residentpush.go`（plan 写「scheddrain.go 新增函数」）；keystone 驻场方法放 `internal/keystone/resident.go`。§6「私有函数与文件拆分」。
10. **`proto.SessionUntitledTitle = "新会话"`** 提前在 P1 落 proto（简报 CAS 改名指令要用，A1.8）；P2 自动命名复用同一常量。

### 事实与命令

- 基线：`cards/B444-charter-1` @ `7500df76`。
- 载体选择（§6 第 1 条）：扩 `keystone.WakeEvent` 加 `Session`/`Resident` 两字段 + `IsResident()`；消费循环把驻场唤醒分进 `pendingByResident`，在门铃之后、卡批之前处理（卡批 attach 暂缓会提前 return）。
- 认领键 `resident:<会话>`；退避复用 `recordWakeBackoff/shouldSkipByBackoff`（以认领键为 key）；准入满复用 `admissionStalled` 判据，但**不**走 `recordAdmissionStall`（那条会落卡级 needs_human）。
- 指针行去重：`residentPushState.pointed[会话]=批次最大 seq`，成功即清。
- 先红后绿记录：
  - ledger：`TestB444*` 编译红 → 加结构字段后断言红（读回不符）→ 改 SELECT/INSERT 后红（`no such column: driver_mode`，迁移缺失）→ 加 DDL + 迁移绿。
  - collab：`TestB444SitResidentPushShape` 10 个拒绝子例断言红 → `validateResidentDriver` 绿。
  - keystone：编译红 → 空壳断言红（4 测）→ 实现绿。
  - agentd：编译红（类型错）→ 断言红 7 测（A1.1/A1.2 对照/A1.4 push_resident/A1.5 对照/A1.6 model 键/A1.9×2）→ 实现绿。
- 跑过的命令与结果：
  - `go test ./internal/ledger/... ./internal/collab/... ./internal/keystone/... ./internal/agentd/... ./internal/proto/...` → 7 包全 ok（agentd 88s）。
  - `go test -count=1 -v -run TestB444 ./internal/{ledger,collab,keystone,agentd}/` → 17 PASS、1 SKIP（PG）。
  - `go build ./...` ok；`go vet ./internal/...` ok；`gofmt -l` 触及目录为空。
  - `go test ./cmd/...`：首跑两条视图用例红（见偏离 4）→ 修后仅 `TestSessionSendActorResolution` 红；去掉 `CLAUDE_CODE_SESSION_ID/HANDOFF_SESSION_*` 单跑 ok（已知环境敏感，memory 有记）。
- **未验证**：PG 腿 `TestB444ResidentPushMigrationPG`——本环境无 `LEDGER_TEST_PG_DSN`，按指令不找凭据，SKIP。真机 R1–R3 归协调者。

## 协调者裁决（P1 交回，2026-10-19）
- P1 偏离 1–10 全部认可：均落在 §6 授权内或为加法 wire；pointer_gate_test 加白名单条目属该测试自述的合法扩展，未改断言，认可。
- 真机 R1–R3 顺延到 P2 的 `resident/launch` 落地后与 R4–R6 一并跑（协调者偏离 plan §4 第 6 步的阻塞规则）：今天没有生产入口能坐 push 驻场者，为 R1 造测试专用入口违反 implement「不得新增测试专用入口」；R1 不过则 P2 改动按需回退重议。
- R9 约束下放 P2：SitResident(push) 必须写真实载体机器名（禁空/禁 local/本机 别名），由 P2 编排保证并加断言。
- R3（嵌套抢名额）待真机 R3 判定后裁决。

## P2（S1/S3/S4 服务端、S6、S7 + CLI）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **新增入口而非改签名**（plan §3 P2「`CreateSession` 签名…扩 `HomeProject`」）：账本 `Store`、`api.Facade`、`client.LedgerClient`、`collab.Service` 均**新增** `CreateSessionInProject(title, owner, homeProject, actor)`，旧 `CreateSession` 保留（委托同一 `createSession`/`createSessionTx`）。理由：旧签名有 ~70 处调用（多为测试），改签名只增 churn 不增语义。属内部接口组织，§6 未列须升级项；与 plan 字面不一致，故列此。
2. **项目房间 `home_project` 留空**（协调者裁决 8，plan §3 授权执行者定）：`EnsureProjectRoom` 写空串。理由：项目房间的项目归属唯一真源是 `project_rooms` 绑定（且已投影为 `SessionSummary.project`，P3 的 `sessionProjects` 本就并入它）；再写一份会让 B444 前后建的项目房间两种读数并存。
3. **改名事件进 timeline 走独立部分索引**：`SessionProjectionEventsContext` 多一条 `UNION ALL` 臂（`type='session_renamed'`），配新部分索引 `idx_session_renamed_session_seq`（两方言），**不**扩既有 `idx_session_structure_session_seq` 的 type 集。理由：`CREATE INDEX IF NOT EXISTS` 不改既有库的索引谓词，扩谓词会让旧库详情读退化成全表扫。新增 `TestB444RenameProjectionUsesScopedIndex` 钉查询计划。plan 未细化，属内部实现。
4. **驻场小队可空 = 唯一协调小队**（建会话与 `resident/launch` 同口径，复用 `resolveCoordinatorSquad`：0 个 → 400 指路 `squad create`，≥2 个 → 400 要求显式指定）。spec S1「驻场小队必选、预填唯一协调小队」是 Web 形态；服务端放宽为「缺省取唯一」不改变 Web 行为。显式给名 → 须已登记且 role=coordinator，否则 400。执行者自定（§6 未列）。
5. **`POST /api/sessions` 双形态判据**：`project`/`cards`/`squad` 全缺 = 旧形态（标题必填，空 → 400，响应 `proto.Session`，既有测试不改仍绿）；任一给出 = 新形态（`project` 必填，标题可空）。只给 `cards` 或 `squad` 不给 `project` → 400 零写入。属 plan §8「版本化增量」的落地细节。
6. **CLI `session rename` 两种身份出示**：`--agent <名>` → 本地账本以 `agent:<名>` 经 `collab.Service.RenameSession`（与 `session send --agent` 同一自报信任面，push 驻场者用）；不带 `--agent` → 经 agentd HTTP 以控制台身份（console_user，服务端注入）改名，与 Web 同一身份。理由：HTTP 面「成员身份服务端注入、不经请求体」，驻场者无法经 HTTP 自报 `agent:`；人用 CLI 时本地 `ledgerActor()`（`cli:user@host`）既非群主也非驻场者、必被拒。plan 未定 CLI 身份形态，执行者自定。
7. **`pointer_gate_test.go` 白名单再加一条**（`internal/agentd/sessioncreate.go` `Server.pointResidentLaunchFailure`）。**与 P1 那条不同：本条可由 HTTP 请求间接触发**（`POST /api/sessions`、`/resident/launch` 首轮失败时）。论证：指针行正文全部由服务端拼出（驻场身份、已校验的小队名、会话 id、失败原因），请求方无法经此写任意指针行，不构成契约 §4「可达 Pointer 的入口」；且协调者裁决 6（Q3）明定首轮失败须落系统指针行。只加数据行、未改断言。**提请审查确认**该解读。
8. **R9 真实机器名取值**（协调者裁决 1）：载体登记名不是本机别名（空/`local`/`本机`）→ 原样；是别名 → 本机配置 `name`，其次 `os.Hostname()`；都取不到 → 拒坐（首轮失败可见，不写别名）。cfg.Name 优先的理由：fleet 机器名（载体登记、targets 键）是配置名，P1 唤醒循环的 `IsSelfTarget` 认它，别机的 `IsLocalMachine`/`IsSelfTarget` 都不认它。
9. **重试入口也落指针行**：`launchSessionResident` 是建会话与 `resident/launch` 共用的同一函数（协调者裁决 3），进入名额窗口后的任一失败都落一条指针行（含重试路径）；进入前的请求错（会话不存在/归档/已有驻场者/小队不可用）不落，只回 HTTP 错误。
10. **`resident/launch` 错误映射**：小队不可用 400、会话不存在 404、已归档/已有驻场者/准入满 409、拉起或落行失败 502。plan 未给映射表，执行者自定（同卡 launch 口径）。
11. **预校验后拉卡竞态**：预校验通过、`JoinCard` 时被别处抢走（唯一索引兜底）→ 该卡记入 `rejected_cards`（原因「拉入失败：…」），会话照建；标题按预校验的已接受卡算，不因竞态重算（极少见，记 Warn）。属 §6「先预校验后建」授权。
12. **改动了 P1 文件 `internal/keystone/resident.go`**：按协调者裁决 9 在驻场简报加一句「`card coordinate` 报无名额 / 409 时在会话里如实告诉人，不要反复重试」（`residentBriefingCoordinateNoSlot`），`TestB444ResidentBriefing` 新增一条必含断言（只加不改）。
13. **`resolveLocalProjectRoot` 形参由 `ledger.Card` 改为项目名**（plan §3 P2 已写明须改）；新增 `resolveProjectWorkdir(project, attrs...)` 供卡与驻场者共用，`resolveCoordWorkdir` 改为委托它（日志文案不变）。
14. **collab 层红灯顺序如实记**：collab 的 `TestB444SessionHomeProjectProjection`/`TestB444RenameSessionPermissions` 先见编译红，随后直接写实现转绿；断言红是事后临时去掉权限闸与摘要投影复跑确认的（两条均红，已还原）。其余层（ledger/agentd/keystone）按「编译红 → 空壳或既有缝断言红 → 绿」走。

### 事实与命令

- 基线：`cards/B444-charter-1` @ `edd0a135`。P2 提交：`916ace1c`（账本+collab+proto）、`efaba046`（抽共用拉起骨架）、`d132cec3`（编排与端点）、`5bdfc467`（CLI）、`0391c5c3`（契约夹具）、`c4813a3f`（简报裁决 9）。
- 抽共用（协调者裁决 2）：`launchCoordinatorRoundWithExpect` 拆成「卡席位前置校验 → `runOnCoordinatorCarrier`（LaunchAdmit → defer 归还 → 读载体 → 远端 fail-closed → spec+workdir 回调 → 规范化 → 名额窗口内回调）→ `bindLaunchedCoordinator`（LaunchForCard → 编码席位 → Bind/RebindSeatMode）」；驻场者走同一 `runOnCoordinatorCarrier`，回调为 `LaunchForResident → 真实机器名 → collab.SitResident(push)`。拆完先跑 `go test ./internal/agentd/ -run 'Coord|Coordinator|Launch|Rebind|Queue'` 绿，再动编排（A2.7）。
- 先红后绿：
  - ledger：编译红 → 空壳（`CreateSessionInProject` 委托旧入口、`RenameSession` 返回零值）断言红 5 测 → 实现 + DDL + 迁移 + 索引绿。
  - agentd：既有缝 `handleSessionCreate` 断言红 8 测（「会话标题不能为空」400）→ 编排与端点绿；首轮全量撞 `TestPointerRouteAbsentFromSource`（见偏离 7）→ 加白名单行绿。
  - keystone：编译红（常量未定义）→ 常量落地未接入简报断言红 → 接入绿。
  - cmd：新测直接对新子命令/flag（编译期即依赖新代码），stub 断言请求形状。
- 跑过的命令与结果：
  - `go build ./...` ok；`go vet ./internal/... ./cmd/...` ok；`gofmt -l internal/{ledger,collab,keystone,agentd,proto,client} cmd` 空。
  - `go test ./internal/ledger/... ./internal/collab/... ./internal/keystone/... ./internal/agentd/... ./internal/proto/... ./internal/client/... ./cmd/...`：除 `cmd` 外 8 包全 ok（agentd 68.8s）；`cmd` 仅 `TestSessionSendActorResolution` 红——本环境设有 `CLAUDE_CODE_SESSION_ID`；`unset CLAUDE_CODE_SESSION_ID` 后单跑 ok、`go test -count=1 ./cmd` 全包 ok（76.6s）。已知环境敏感，非回归。
  - `go test -count=1 -v -run 'TestB444|TestPointerRouteAbsentFromSource|TestCoord|TestContractFixtures|TestSessionCreateCommand|TestSessionsCreateEndpoint'`（ledger/collab/keystone/agentd/proto/cmd）：71 PASS、2 SKIP（两条 PG 腿）、0 FAIL。
  - `go test -run TestContractFixtures ./internal/proto -update`：只新增 4 份 `web/src/api/testdata/*B444.json`，既有夹具零字节变化；`cd web && npx vitest run src/api` → 8 文件 141 测全过（Web 尚未消费新夹具，P3 接）。
- **未验证**：
  - PG 腿 `TestB444SessionHomeProjectMigrationPG`（及 P1 的 `TestB444ResidentPushMigrationPG`）——本环境无 `LEDGER_TEST_PG_DSN`，SKIP。
  - 真机 R1–R6（隔离 M + 真载体）归协调者；特别是：opencode 首轮能否按简报执行 `session send --agent` / `session rename --agent`；R3 嵌套名额。
  - 多机共库下 R9 机器名被别机正确「不认领」只有单测推理（P1 `IsLocalMachine`/`IsSelfTarget` 判据），未在 fleet 上验。

### 真机调用形状（给协调者）

- 建会话（HTTP）：`POST /api/sessions` 体 `{"project":"<项目>","squad":"<协调小队>","cards":["B1","B2"],"title":""}`（title/cards 可省；squad 省 = 唯一协调小队）。200 响应 = `Session` 键集 + `home_project` + 可选 `rejected_cards:[{card,reason}]` + `resident:{identity,carrier,carrier_machine}` 或 `resident:{error}`。
- 建会话（CLI）：`handoff [--target <机>] session create [标题] --project <项目> --squad <小队> [--card B1 --card B2] [--owner user:<名>]`。
- 叫驻场者（HTTP）：`POST /api/sessions/{id}/resident/launch` 体 `{"squad":"<小队>"}`（可 `{}`）→ 200 `{identity,carrier,carrier_machine}`；400/404/409/502 见偏离 10。
- 叫驻场者（CLI）：`handoff [--target <机>] session resident launch <session:N> [--squad <小队>]`。
- 改名（HTTP，控制台身份）：`POST /api/sessions/{id}/rename` 体 `{"title":"<新名>","expect_title":"新会话"}`（expect_title 可省）→ 200 `Session`；400 空标题、403 非群主非驻场者、404、409 CAS 不符或已归档。
- 改名（CLI）：驻场者 `handoff session rename <session:N> <新名> --agent <小队> --expect-title 新会话`（本地账本）；人 `handoff [--target <机>] session rename <session:N> <新名> [--expect-title X]`（经 agentd）。

## P3（S1/S2/S8/S9 Web）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **改写既有测试断言（plan §6「必须升级」项，逐条列出提请审查确认）**：
   - `Shell.test.tsx` B442 两条（桌面左栏「新建会话」、移动「⋯」菜单「新建会话」）→ 改写为 B444 缺席断言 + 项目入口断言（`describe('B444 新建会话入口在项目上')`）。**协调者已批准**（spec S9）。
   - `Shell.test.tsx` B358.9 两条（「对话框只有标题，createSession 只收标题」「建会话后不调用 addSessionMember」）：入口改为项目行 icon，第一条改为断言 `createSession({project, squad, cards, title})`；B358.9 不变式（无群主输入、无 owner 记忆、不补员）原样保留。理由：plan §3 P3 明定 `createSession` 改签名、对话框重写为四字段，旧断言与已批准 plan 直接矛盾。**未经单独授权，提请确认。**
   - `NewSessionDialog.test.tsx` 整体重写（旧「提交只带标题」「空标题禁用创建」与 spec「标题可空」矛盾）；B358.9 两条不变式（无群主输入、未配 console_user 禁用）保留。**提请确认。**
   - `rooms.fetch.test.ts`「createSession POST {title}」→ 改为新形态 `{project, squad, cards?, title?}`，「不带 owner」不变式保留。**提请确认。**
   - `sessionModel.test.ts`「timeline 词表恰八值」→ 九值（加 `renamed`）。必然推论（S7 改名事件进 timeline）。**提请确认。**
   - `SessionSidebar.test.tsx` 只从 `defaultProps` 删掉 `onCreate`（非断言）。
2. **`SessionSidebar` 删 `onCreate` 与 `archivedOnly` 两个 prop**（Q5 授权范围内）：`archivedOnly` 唯一用途是禁用那颗按钮，按钮撤掉后成死 prop，一并删除，Shell 两处调用同步去掉。
3. **结果视图**（执行者自定，§6「Web 组件内部结构」）：建成后若有 `rejected_cards` 或 `resident.error`，对话框不关、转结果视图逐卡点名原因与驻场者失败原因，并给 CLI 重试命令 `handoff session resident launch <id>`（Web 重试按钮按 Q4 落 roadmap）；会话照样刷新并开 tab。无问题则直接关闭。
4. **多个协调小队时不预填**：spec 只定「预填唯一协调小队」；≥2 个时下拉首项「请选择协调小队」，未选前禁用创建（不替人挑）。读小队失败显示原因并禁用创建。
5. **卡候选过滤**放 `sessionModel.pullableCards`（纯函数）：`card.project === 项目名`（与服务端预校验口径逐字一致）、状态不在 `{已完成, 终止}`、不在会话列表快照的任何会话里（含归档）。列表外会话（人不是成员）的占用仍由服务端 `rejected_cards` 点名。
6. **建会话请求体**：trim 后空标题、空卡列表不出键；`squad` 恒出（Web 形态必选）。
7. **`SessionMember.driver_mode` 词表只收 `push`**（`RESIDENT_DRIVER_MODES`）：desktop 在线上是空串 → omitempty 缺键，缺键即 desktop；详情成员行仅 push 驻场者附 `· push · <carrier>@<machine>`。
8. **sessionProjects 顺序**：`home_project, project, 卡项目`（去重保序）。项目房间 `home_project` 留空（P2 偏离 2），两者实际不并存。
9. **项目名**：入口传 `ProjectNode.name`（与 `openProjectCards` 的 `?project=` 同口径）。
10. **第一个 P3 提交 `52461122` 单独不过 tsc**（Shell 仍调旧签名），第二个提交 `f8396c12` 接上；两提交合起来 tsc/全量绿。

### 事实与命令

- 基线：`cards/B444-charter-1` @ `fc2f9e23`。P3 提交：`52461122`（wire/模型/详情）、`f8396c12`（入口/对话框/撤入口）。
- 先红后绿：
  - wire/模型：先写 contract/rooms.fetch/sessionModel 新测 → 10 红（守卫函数缺失、home_project 未并入、renamed 缺标签、createSession/renameSession 形状）→ 实现绿。`rooms.fetch` 两条首次红里有一处是测试自身复用 Response 体（已改 mockImplementation），修后仍红于功能缺失，再实现转绿。
  - Shell 竖切：6 条 B444 用例先红（找不到「在 handoff 新建会话」；移动「⋯」菜单 `新建会话` 仍在）→ 入口/对话框/Shell 接线后绿。
  - `NewSessionDialog.test.tsx` 与 `SessionDetail` B444 用例：对话框测在组件重写后写成，**未见首红**（如实记）；SessionDetail 用例先红（无 renamed 渲染/无载体落点）后绿。
- 命令与结果：
  - `cd web && npx tsc -b` → 无输出（通过）。
  - `npx vitest run src/api src/app/rooms src/app/tree src/app/shell` → 39 文件 743 测全过。
  - `npx vitest run`（全量）→ 145 文件 1950 测全过。
  - `npx eslint .` → 2 errors（`MobileWorkspace.tsx:160` 既有、本次因上方加一行注释由 159 下移；`TerminalTab.test.tsx:37` 既有），无新增 error；`NewSessionDialog.tsx` 无告警（`pullableCards` 移入 sessionModel 以免 fast-refresh 告警）。
- **未验证**：
  - R4/R7 真机（隔离 M + 真载体）：建会话耗时下「正在拉起驻场者…」体感、桌面与 390px 手机宽度截图（项目行 icon 是否挤压行内计数/芯片、操作面板项、底部与「⋯」入口已撤）——归协调者。jsdom 下只断言了结构，未做像素/横向滚动检查。
  - 原型回流 `prototypes/base/` 归 finish。

## P2 返工（真机 R4 F1/F2）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **改了 P1 既有断言（协调者批准）**：
   - collab `TestB444SitResidentPushShape`：「push 行缺 push_session → 拒」改为「push_session 空（首轮在途）→ 通过」（裁决 F1-b）。
   - keystone `TestB444ResidentBriefing`：原按子串全禁 `--cli`/`--session`，因简报现须**点名禁止** `--cli/--session`（裁决 F2），改禁「以席位身份发言」的形态（`send --cli`、`--cli opencode`、`HANDOFF_SESSION_`），并新增「必须点名禁止」的正向断言。
2. **在途行的唤醒判据放在认领之后**（裁决 F1-c）：`consumeResidentBatch` 在认领与退避闸之后、续轮之前判 `push_session==""` → Info「驻场者首轮在途，暂缓续轮」、不收尾不标 seen 不落指针行。必须先认领：水位由 `CursorWatermark` 按在飞认领挡住，不认领就 return 会让水位越过该消息、消息丢失（准入满路径同理）。
3. **补写 push_session 的 CAS 形状**：账本单条条件 UPDATE（`identity=? AND driver_mode='push' AND push_session=''`）；0 行时回读区分「无行/身份不符 → ErrResidentNotSeated」与「已补写或非 push → ErrCASConflict」。collab 侧新哨兵 `ErrResidentPushSessionSet`（= `client.ErrCASConflict`）。不落账本事件、不递增席位版本（驻场身份未变）。
4. **首轮失败与补写失败同一收尾**：`ForgetResident` + `StandResident`（请下在途行）+ 原有指针行 + `resident.error`。请下本身失败只留 Error 日志（在途行会让人话一直暂缓，须人工 `handoff session stand <会话> --as <身份>`）——未自动重试，也未加清扫。
5. **已知残余风险（未修，提请协调者裁决）**：agentd 在首轮在跑时崩溃/重启，在途行（push_session 空）会永久留下：人话被无限暂缓（认领留在飞、无指针行），`resident/launch` 回 409。恢复靠人工 `session stand`。可选修法：启动对账清扫在途行，或在途超过 N 分钟后落一条指针行——均未做。
6. **collab 红绿顺序如实记**：`TestB444InflightResidentWritesAndPushSessionFill` 编译红后，校验放宽与 `SetResidentPushSession` 同步写完再跑即绿，中间未单独见断言红。agentd 两条竖切（首轮发言/改名、首轮在途消息）在旧编排上断言红（「驻场行应已坐下」「补写后应恰一次续轮，得 []」——后者揭示旧顺序下首轮期间的人话直接丢失）；在途唤醒守卫另做了一次「去掉守卫 → 红（准入满报错）」的确认后还原。
7. 范围外（按协调者指示未动）：opencode 不按 SessionSpec.Workdir 运行；协调者回合内自行 `card bind` 与 launch 回合后 bind 相撞。

### 事实与命令

- 基线 HEAD `0ed8de85`（含 P3）。返工提交：`8ee53cf9`（F1）、`3892b1f7`（F2）。
- F1 新顺序（`launchSessionResident`）：会话锁 → 读会话/归档/已有驻场者（含在途行 → 409，错误文案注明「首轮在途」）→ `runOnCoordinatorCarrier` 名额窗口内：真实机器名（R9）→ `SitResident(push, push_session="")` → `LaunchForResident` → `SetResidentPushSession`。
- 断言（裁决 e，F1 的竖切红）：`b444CreateRunner.onLaunch` 在 Launch 期间经**真实** collab 写面以 `agent:coord` `Send` 与 `RenameSession(expect=新会话)`，二者均须成功；同时读到驻场行已坐下且 push_session 空。
- 测试映射：
  - F1-a/e：agentd `TestB444ResidentSpeaksAndRenamesInFirstRound`；ledger `TestB444SetResidentPushSessionCAS`；collab `TestB444InflightResidentWritesAndPushSessionFill`。
  - F1-b：collab `TestB444SitResidentPushShape`（改断言，见偏离 1）。
  - F1-c：agentd `TestB444MessageDuringFirstRoundDeliveredAfterPushSessionSet`（在途期间 0 续轮 0 指针行；补写后恰 1 次 Resume 到 `ses_b444_create_1`，再排水不重复）。
  - F1-d：agentd `TestB444ResidentLaunchConflictsWhileInflight`（在途行 → 409，0 次 Launch）；既有 `TestB444SessionCreateResidentFailureThenRetry` 仍绿（失败后驻场位为空、指针行恰一条、重试成功）。
  - F2：keystone `TestB444ResidentBriefing`（新增必含：至少 20 分钟、`handoff card show <卡号>`、driver_session、同一张卡绝不重复、只用 `--agent coord` 发言、点名禁止 `--cli/--session`；CAS 改名句保留）。
- 命令：`go build ./...` ok；`go vet ./internal/... ./cmd/...` ok；`gofmt -l` 触及目录空；`unset CLAUDE_CODE_SESSION_ID && go test -count=1 ./internal/{ledger,collab,keystone,agentd,proto,client}/... ./cmd/...` 9 包全 ok（agentd 69.8s、cmd 76.6s）；`go test -count=1 -v -run TestB444`（ledger/collab/keystone/agentd/cmd）43 PASS、2 SKIP（PG 腿）、0 FAIL。proto 无改动，契约夹具未重生成；web/ 未动。
- **未验证**：PG 腿（无 `LEDGER_TEST_PG_DSN`）；真机复跑 R4/R5（首轮发言与改名、card coordinate 长超时行为）归协调者。

## P2 返工二（启动清扫首轮在途行）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **清扫放在自动化循环 goroutine 的开头**（`StartAutomation` 的 Once 内 `go func(){ sweep; automationLoop }`），而非同步跑在启动路径上：清扫再慢也不阻断启动，且首个消费轮在清扫之后。
2. **与本进程真在跑的首轮防撞**：裁决未提。HTTP 可能先于循环开始服务，清扫若撞上本进程刚拉起的首轮会误请下真在途行。处置：每行 `TryLock` 会话的 `coordinatorLock`（`launchSessionResident` 全程持有它）——拿不到即跳过；拿到后重读驻场行，确认仍是同身份 push 在途行才请下。
3. **指针行复用 `pointResidentLaunchFailure`**，原因串为「首轮被 agentd 重启打断（…不会再补写会话 id）」，正文自带 `handoff session resident launch <会话> --squad <小队>` 重试命令；因此 `pointer_gate_test` 白名单无需新增条目。小队名取驻场身份 `agent:<名>` 的名字段。
4. **测试直调 `sweepInterruptedResidents`**，未经 `StartAutomation` 起真循环（避免测试里并发的消费轮/清队轮引入时序噪声）；接线只有一行，未单测。
5. 读面 `ListInflightPushResidents` 不按机器过滤，机器归属由 agentd 用 `IsLocalMachine`/`IsSelfTarget` 判（collab 只存事实）。

### 事实与命令

- 新增：ledger `Store.ListInflightPushResidents` → `api.Facade.InflightPushResidents` → `client.LedgerClient.InflightPushResidents` → `collab.Service.InflightPushResidents`；agentd `sweepInterruptedResidents`/`standInterruptedResident`（`internal/agentd/sessioncreate.go`）。
- 红绿：ledger `TestB444ListInflightPushResidents` 空壳断言红 → 绿；agentd `TestB444StartupSweepStandsInterruptedInflightResidents` 空壳断言红（「本机在途行应被请下」）→ 绿。后者覆盖：本机在途 → 请下 + 恰一条含「重启」与重试命令的指针行；别机在途（R9）→ 不动零指针；已补写本机 push 行 → 不动零指针；再扫幂等。
- 命令：`go build ./...`、`go vet ./internal/... ./cmd/...` ok，`gofmt -l internal cmd` 空；`unset CLAUDE_CODE_SESSION_ID && go test -count=1` 触及 9 包全 ok（agentd 85.1s、cmd 76.7s）。
- 未验证：真机「agentd 首轮中途重启 → 清扫」路径。

## 真机 R4/R5 返工（协调者 inline，2026-10-19）
- R5 首跑：零卡空标题建会话，驻场者首轮（人尚未开口）即改名「驻场值班会话」——违背 S4「人发第一条消息后命名」。根因：简报对零卡未命名一律下发改名指令，含无唤醒事件的 launch 首轮。
- 修：residentBriefing 仅在有唤醒事件时下发 CAS 改名；首轮改为「本轮不要改名」。TestB444ResidentBriefing 改断言（协调者自批：launch 不含改名、含等待句；resume 含改名），旧实现下该用例红（零卡未命名子例），新实现绿。
- 真机 R7：带终态卡的项目内会话（session:1，home_project=b444demo，D1/D2 已完成移出卡流）在桌面左栏落「其他会话」。根因：sessionProjects 把映射缺失的卡判 hasUnknownCard，Shell/MobileWorkspace 据此判未关联。修：有 home_project 时不标未知（spec「所属项目为主」）；新增规则表用例先红后绿；rooms/tree/shell 597 绿，tsc 净。

## P2 返工三（驻场者不得坐卡席位，真机 R6）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. **撤销 P1 偏离 6**（裁决 1）。标记位选在 `keysclient.SessionRef.Resident bool json:"-"`（回合属性，同 Env 不落盘不进 wire），由 `keystone.WakeResident` **强制置位**（不论调用方是否置位），`coordinatorRunner.Resume` 见标记则 `Env=nil`、否则照旧 `seatEnv`。`LaunchForResident` 记下的驻场 ref 也带 Resident。launch 回合本就不注入（Q3 甲），未加 SessionSpec 标记，只加断言锁住。
2. **互斥按会话 id 判，不看 CLI 标签**（裁决 2 原文「seat identity's session id equals push_session」）：`cli:claude#<驻场会话>` 同样拒。ledger 不调 proto 包级函数（既有预算约束），席位会话 id 用本地 `seatSessionID`（取最后一个 `#` 后段）。
3. **反向查询用 `substr`/`length` 后缀比较，不用 LIKE**：会话 id 常含 `_`（LIKE 通配符）。长度按 rune 计，两方言 `length` 均按字符。
4. **并发**：检查与写在同一 `mutate` 事务内；PG 下 mutate 取全局账本咨询锁，SQLite 单写者，故两方向无写偏斜。为此 `SetResidentPushSession` 从裸 `db.Exec` 改进 `mutate`（条件 UPDATE 仍是事务第一条语句，撞席位则回滚，原值不变）；未递增席位版本（同原行为）。
5. 反向文案（裁决只给了正向）：「该 CLI 会话正坐卡 <卡> 的席位，不能当会话 <会话> 的驻场者」，同一哨兵 `ErrResidentSeatConflict`；facade 翻 `client.ErrResidentSeatConflict`，collab 再导出同名。卡席位写面（cmd `card bind/rebind`、agentd coordinate）直连 ledger，原文案直达 CLI 出口。
6. 简报句（裁决 3）点名 `card bind` / `card rebind` / `card takeover` / `card seat …` 四个命令族，放在 coordinate 同步说明之后（放前面会让「这条命令是同步的」指代错位）。
7. 首轮在途行（push_session 空）坐下时不判，补写 push_session 时判——在途行无可比会话。

### 事实与命令

- 席位写者盘点：`grep "driver_session *="` 只有 `BindSeatMode`、`RebindSeatMode`（另一处是 `clearSeatTx` 清空），旧名 `BindSeat/RebindSeat` 只是委托垫片——两处都已加检查。
- 红绿：
  - 裁决 1：agentd `TestB444ResidentResumeHasNoSeatEnv`（真 opencode.Coordinator + PATH 假 CLI）先编译红（无 Resident 字段）；实现后临时把判断改成 `ref.Resident && false` 复跑红「HANDOFF_SESSION_CLI="opencode"，want <unset>」，恢复后绿；同用例卡续轮仍注入 `ses_card`。agentd `TestB444PushResidentResumesOnHumanMessage`/`TestB444SessionCreateWithCardsSeatsPushResident` 加 Resident=true 与首轮 spec.Env 无席位键断言；`TestB444AtCardWakesSeatNotResident` 加卡续轮 Resident=false。keystone `TestB444WakeResidentResumesGivenRef` 加 Resident 断言。
  - 裁决 2：ledger `TestB444ResidentSeatExclusion`（PG 孪生 `TestB444ResidentSeatExclusionPG`）先编译红（无哨兵）；实现后分别临时短路两个方向的检查复跑，各红在对应断言（正向「bind 坐驻场会话…得 <nil>」；反向「SitResident(push) 坐卡席位会话…得 <nil>」），恢复后绿。覆盖 bind/换 CLI 标签/coordinate/rebind 四条正向、SitResident 与 SetResidentPushSession 两条反向、请下后可坐。api `TestB444FacadeResidentSeatConflictTranslated`；cmd `TestB444CardBindRejectsResidentSession`（非 0，文案点名会话，席位仍空）。
  - 裁决 3：keystone `TestB444ResidentBriefing` 加必含「绝不自己坐卡席位」「card bind」「card seat」「绝不亲手做卡的活」「只经 `handoff card coordinate <卡号>`」；首跑红（期望串少了 `<卡号>`，改断言串后绿——测试串笔误，非行为）。
- 命令：`go build ./...` ok；`go vet ./internal/... ./cmd/...` ok；`gofmt -l` 触及目录空；`go test ./internal/agentd/... ./internal/ledger/... ./internal/collab/... ./internal/keystone/...` 全 ok（agentd 77.6s）；`unset CLAUDE_CODE_SESSION_ID HANDOFF_SESSION_CLI HANDOFF_SESSION_ID; go test ./cmd/...` ok（77.2s）。proto/web 未动。
- **未验证**：PG 腿（无 `LEDGER_TEST_PG_DSN`）；真机复跑 R6。
- 残余：agentd 进程自身若带 `HANDOFF_SESSION_*` 环境会经 `os.Environ()` 继承给驻场子进程（本次只停注入，未主动剥离继承值；在役 agentd 由服务管理器拉起，正常不带）。
- 提交：`28388b40 fix(B444): 驻场回合不注入卡席位对，简报禁驻场者自坐卡席位（真机 R6，返工三 裁决 1/3）`；`83d7ec6b fix(B444): 账本执法同一 CLI 会话不能既是 push 驻场者又坐卡席位（真机 R6，返工三 裁决 2）`（本行随后 amend 收进后者，hash 会变）。


## 偏离与自主决定（协调者，最终验收前汇总，2026-10-19）

- **P3 偏离 1 改写既有测试断言**（B358.9 Shell 两条、NewSessionDialog 重写、rooms.fetch、timeline 八值→九值）：协调者批准。理由：均为已批准 plan §3 契约变更（`createSession` 改签名、标题可空、新增 `renamed` kind、撤 B442/SessionSidebar 入口）的直接后果；保留了 B358.9 不变式（无群主输入、无 owner 记忆、未配 console_user 禁用）。
- **P2 偏离 7 / P1 偏离 3 的 pointer_gate 白名单两行**：协调者批准（指针行正文全由服务端拼，请求方不能注入；B444 独立审查 M5 同读）。
- **协调者 inline 返工**：f6113731（首轮不改名）、d4a107d0（sessionProjects home_project 为主）、f7d89494（review C1 断言对齐、M4 注释）——均改断言或新增断言，协调者自批；新鲜复跑见下「验收复跑」。
- **R1 真机顺延**：P1 完成后未立刻真机（无生产入口能坐 push 驻场者，不造测试专用入口），顺延至 P2 `resident/launch` 落地后与 R4–R8 一并跑。
- **R3 裁决**：驻场回合嵌套叫协调者在小队并发 1 时 409——本期保持语义，落 roadmap。
- **plan §5 证据目录 `docs/superpowers/evidence/b444/` 未建**：真机证据（命令与原始输出摘录）改记本台账下节，截图只在会话里，未入库。

## 真机验收记录（协调者执行，隔离靶子 M：linux-01 127.0.0.1:7792，独立 HOME/datadir/SQLite，载体 opencode/deepseek-v4.1-flash-free；生产 7777 未碰）

- 环境：`/root/b444-iso`（setup.sh、m.sh）；小队 lead（coordinator）成员 free；项目 b444demo（`/root/b444-iso/proj/b444demo`）。二进制按提交逐次重部署：fc2f9e23 → 6de79f13 → f6113731 → d4a107d0 → 3051d2b2。
- **R4 首跑（fc2f9e23）**：`POST /api/sessions {project,cards:[D1,D2]}` → 200，标题「演示卡一：给 README 加一行说明 等 2 张」，`resident.error`「小队内没有已上线且有空的载体」（载体未 detect），会话 seq 7 系统指针行含重试命令；detect 后 `resident/launch` → 200 `{identity:agent:lead,carrier:free,carrier_machine:linux-01}`（5m32s）。**发现 F1**：首轮 `session send --agent lead` 被拒「书写者被拒：非会话成员」，模型改用 D1 席位 `--cli/--session` 冒名发言 → 返工（先坐下后拉起）。**发现 F2**：`card coordinate` 同步超 120s 被载体 shell 杀 → 简报补超时与查席位。
- **R5（f6113731 后）**：零卡建会话 session:5 → 标题保持「新会话」；人发「帮我看下 b444demo 仓库里现在有哪些文件…」→ seq 33 `agent:lead` 回话，标题改为「b444demo 仓库文件盘点」。f6113731 前两次（session:3/4）首轮即自起名「驻场值班会话/驻场待命会话」→ 修。人先手改 session:7「人手起的名字」→ 驻场者未覆盖，timeline `renamed {from:新会话}`。
- **R1**：同 R5，人无 @ 发言 → push 驻场者续轮回话（`agent:lead`）。
- **R2**：session:1 人发 `@D1`（`mentions:["D1"]`，seq 50）→ 驻场者零回合（3 分钟无 `agent:lead` 消息）；D1 已终态，卡唤醒按终态闸跳过。正向半：驻场者 seq 36 `@D1` → D1 push 席位 seq 48 回话。注：HTTP 发言不自动解析 @，须带 mentions（Web 编辑器即如此）。
- **R3**：载体并发 1，session:6 人叫「帮 D3 叫个协调者」→ seq 56 驻场者如实报 409「协调者并发已满（小队 lead）」未重试。下半（续轮失败指针行）未造出，见 roadmap。
- **R6（3051d2b2）**：session:9 建会话带 D5 → 驻场者首轮 `card coordinate D5` → 协调者会话 ses_eac5ea14 坐席并推完到「已完成」；驻场者未自坐席位（D5 driver_session = 协调者会话）。返工前（d4a107d0）session:8 实测驻场者 `card bind` 自坐 D4 席位并亲自做完卡 → 返工三（不注入席位环境、账本互斥、简报禁止）。项目房间坐组织者 agent:org 后，D4 六次换列 + needs_human 通知 @agent:org 全数落项目房间（seq 83–89，于驻场回合结束后批量到达，见 roadmap「消费循环同步跑回合」）。协调者首轮自坐后 launch 收尾撞席 → 驻场者收 409 + needs_human，属 B446。
- **R7**：桌面 1280 宽：项目行「在 b444demo 新建会话」icon → 对话框（项目只读、标题占位「留空则自动命名」、卡只列 D4、小队预填 lead）→ 创建中「正在拉起驻场者…」→ 会话出现在 b444demo 下并开 tab；左栏底部仅「归档会话」。手机 360 宽：项目 ⋯ 操作面板含「新建会话」，对话框适配。发现带终态卡的项目内会话落「其他会话」→ d4a107d0 修，复看 session:1 归 b444demo。
- **R8**：在役版 8d10ea74 于隔离 HOME/7793 造旧库（会话「旧会话甲」+ desktop 驻场者 agent:old-desk）→ 3051d2b2 前一版 d4a107d0 打开：200，摘要无 `home_project` 键，驻场成员无 `driver_mode` 键；二次打开 200。
- 范围外另立卡：B445（opencode 回合不在 SessionSpec.Workdir）、B446（协调者首轮自坐后 launch 收尾撞席）。

## P2 返工四（review C2：不碰 B233.2 冻结镜像）——实现者 subagent，2026-10-19

### 偏离与自主决定

1. 撤掉 `keysclient.SessionRef.Resident`（文件回到 f7d89494 原样）及 keystone 两处置位、全部断言该字段的测试行（agentd 三处、keystone 一处）；`executor TestCoordTypesMirrorKeysclientFields` 复绿。
2. 驻场判定读口走既有分层：ledger `Store.IsPushResidentSession(pushSession)`（`driver_mode=push AND push_session=?`，空串恒 false）→ `api.Facade` 直通 → `client.LedgerClient` 新方法（fakeLC 补桩）→ `collab.Service.IsPushResidentSession`；cmd 组装点把 `rooms.IsPushResidentSession` 作为 `NewCoordinatorRunner` 第 4 个参数注入（签名变更，三个调用点同改）。keystone 不碰账本。
3. **依赖为 nil 时 Resume 拒跑**（`errResidentLookupUnwired`），不默认为「非驻场」照旧注入——那等于猜。Launch 不受影响。读失败包上下文后经 `classifyRunnerErr`（非哨兵即原样透传）返回，子进程不启动，Warn 记 cli/session/cause。
4. 测试夹具：`testCoordRunner` 保留旧签名，默认判定恒 false（既有卡回合用例语义不变）；新增 `testCoordRunnerWithResidents` 显式给读口。
5. 全量复跑没有经 `gotest.sh` 包装（worktree 守卫拒绝 `bash <脚本>` 形态），改为同效的 `unset CLAUDE_CODE_SESSION_ID HANDOFF_SESSION_CLI HANDOFF_SESSION_ID; go test -count=1 -json ./...`（与脚本内容逐条相同：脚本也只做 unset + cd 本 worktree + go test），计数由 json 事件逐包统计。

### 事实与命令

- 红绿：
  - agentd `TestB444ResidentResumeHasNoSeatEnv` 改为真账本坐 push 驻场行（push_session=ses_resident）→ `ledgerapi.New` → `collab.New` 的读口；临时把 `if resident` 改成 `if resident && false` 复跑红「HANDOFF_SESSION_CLI="opencode"，want <unset>」，恢复后绿；卡续轮 ses_card 仍注入。
  - 新增 agentd `TestB444ResumeResidentLookupErrorFailsRound`：读失败 → 错误含原因、未装配 → `errResidentLookupUnwired`、两种情形子进程均未启动（捕获文件不存在）；临时把读失败判断短路复跑红「驻场判定读失败应让本轮失败并带原因，得 <nil>」，恢复后绿。
  - ledger `TestB444ResidentSeatExclusion` 场景加 `IsPushResidentSession` 三值断言（命中 true、空串 false、卡席位会话 false）。
- 命令：`go build ./...` ok；`go vet ./...` ok；`gofmt -l $(git ls-files '*.go')`（1385 个文件，经文件列表喂 xargs）输出空。

## 验收复跑

全量：`unset CLAUDE_CODE_SESSION_ID HANDOFF_SESSION_CLI HANDOFF_SESSION_ID; go test -count=1 -json ./...` → exit=0，stderr 空。逐包汇总（json 事件计数，含子测试；无测试文件的包略去）：

```
pass .  pass=26 fail=0 skip=0  3.6s
pass ./cmd  pass=668 fail=0 skip=0  79.1s
pass ./internal/agentd  pass=1085 fail=0 skip=1  72.0s
pass ./internal/approval  pass=35 fail=0 skip=0  0.6s
pass ./internal/approval/internal/decision  pass=4 fail=0 skip=0  0.4s
pass ./internal/buildinfo  pass=17 fail=0 skip=0  0.3s
pass ./internal/client  pass=215 fail=0 skip=0  9.7s
pass ./internal/collab  pass=178 fail=0 skip=0  1.9s
pass ./internal/collab/room  pass=3 fail=0 skip=0  0.5s
pass ./internal/config  pass=126 fail=0 skip=0  0.4s
pass ./internal/diag  pass=4 fail=0 skip=0  0.3s
pass ./internal/discipline  pass=12 fail=0 skip=0  0.3s
pass ./internal/dropdir  pass=11 fail=0 skip=0  0.4s
pass ./internal/envfile  pass=38 fail=0 skip=0  0.4s
pass ./internal/executor  pass=59 fail=0 skip=0  0.4s
pass ./internal/executor/agy  pass=66 fail=0 skip=0  1.9s
pass ./internal/executor/claudecode  pass=84 fail=0 skip=3  5.6s
pass ./internal/executor/codex  pass=87 fail=0 skip=0  7.3s
pass ./internal/executor/fake  pass=3 fail=0 skip=0  0.6s
pass ./internal/executor/grok  pass=142 fail=0 skip=1  2.6s
pass ./internal/executor/opencode  pass=240 fail=0 skip=1  30.8s
pass ./internal/executor/rawtap  pass=6 fail=0 skip=0  0.4s
pass ./internal/executor/turn  pass=67 fail=0 skip=0  0.7s
pass ./internal/hostapi  pass=25 fail=0 skip=0  3.5s
pass ./internal/initflow  pass=36 fail=0 skip=0  0.4s
pass ./internal/keystone  pass=33 fail=0 skip=0  0.5s
pass ./internal/launcher  pass=14 fail=0 skip=0  0.4s
pass ./internal/ledger  pass=283 fail=0 skip=37  6.2s
pass ./internal/ledger/api  pass=14 fail=0 skip=0  0.5s
pass ./internal/ledgermirror  pass=21 fail=0 skip=0  2.7s
pass ./internal/ledgerstep  pass=169 fail=0 skip=0  1.4s
pass ./internal/localsync  pass=4 fail=0 skip=0  1.1s
pass ./internal/logx  pass=8 fail=0 skip=0  0.4s
pass ./internal/mobilecore  pass=29 fail=0 skip=0  4.4s
pass ./internal/orchestration  pass=456 fail=0 skip=0  40.0s
pass ./internal/p2p  pass=12 fail=0 skip=0  22.6s
pass ./internal/pathenv  pass=10 fail=0 skip=0  0.7s
pass ./internal/permgate  pass=407 fail=0 skip=0  0.4s
pass ./internal/prochost  pass=125 fail=0 skip=6  12.3s
pass ./internal/projectid  pass=2 fail=0 skip=0  0.4s
pass ./internal/proto  pass=87 fail=0 skip=0  0.4s
pass ./internal/proxycfg  pass=8 fail=0 skip=0  0.4s
pass ./internal/ptyhost  pass=20 fail=0 skip=0  21.5s
pass ./internal/ptyhost/engine  pass=29 fail=0 skip=0  11.5s
pass ./internal/ptyhost/hostproc  pass=7 fail=0 skip=0  12.9s
pass ./internal/ptyhost/sessdir  pass=7 fail=0 skip=0  0.7s
pass ./internal/ptyhost/wire  pass=7 fail=0 skip=0  0.6s
pass ./internal/ptytestroot  pass=6 fail=0 skip=0  0.4s
pass ./internal/relay  pass=31 fail=0 skip=0  38.2s
pass ./internal/release  pass=38 fail=0 skip=0  1.7s
pass ./internal/scheduling  pass=87 fail=0 skip=0  0.9s
pass ./internal/scheduling/internal/logging  pass=2 fail=0 skip=0  0.4s
pass ./internal/selfupdate  pass=29 fail=0 skip=0  0.4s
pass ./internal/service  pass=84 fail=0 skip=0  0.4s
pass ./internal/skill  pass=56 fail=0 skip=0  0.7s
pass ./internal/store  pass=83 fail=0 skip=0  1.0s
pass ./internal/targetclient  pass=27 fail=0 skip=0  5.6s
pass ./internal/termseq  pass=4 fail=0 skip=0  0.4s
pass ./internal/testhttp  pass=6 fail=0 skip=0  0.5s
pass ./internal/testperm  pass=7 fail=0 skip=1  0.3s
pass ./internal/toolchain  pass=10 fail=0 skip=0  0.4s
pass ./internal/upgrade  pass=22 fail=0 skip=0  0.4s
pass ./internal/webui  pass=2 fail=0 skip=0  0.4s
pass ./internal/workspace  pass=242 fail=0 skip=0  20.6s
pass ./internal/workspace/internal/gitproc  pass=5 fail=0 skip=0  0.4s
TOTAL pass=5730 fail=0 skip=50 packages=65
```

- skip 50 条：ledger 37（27 条测试名含 PG，未设 `LEDGER_TEST_PG_DSN`；其余 10 条为既有环境门控，未逐条核），其他包 13 条为既有环境门控；**PG 腿未验证**。真机复跑 R6 归协调者。
- 提交：`840f0b50 fix(B444): 驻场判定改读账本事实，撤回 SessionRef.Resident（review C2，不碰 B233.2 冻结镜像）`（本行随后 amend 收进该提交，hash 会变）。

## finish（2026-10-19）

- 人裁决：「接受三项未验残余（R3 续轮失败指针、R9 多机、PG 迁移腿）。合入 main 并推送，然后升级三台并真机复核。」
- origin/main = 8d10ea74 为本分支祖先，快进合入；合入树即 cd409485 + 本文档提交，全量见「验收复跑」与独立审查第三轮（65 包 ok）。
