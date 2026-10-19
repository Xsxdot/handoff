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
