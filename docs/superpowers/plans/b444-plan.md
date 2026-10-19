# B444 实现计划：项目内新建会话（会话属项目、驻场者由 handoff 拉起）

- 状态：**已拍板（2026-10-19）**（出稿者 = plan subagent；拍板者 = 主会话协调者；8 岔口全取推荐项，裁决见下）
- 输入：`docs/superpowers/specs/b444.md`（已批准，L3，单卡一轮不扇出，主会话亲自盯）
- 基线：分支 `cards/B444-charter-1` @ `88be981f`
- 读者：零上下文执行者。文末「核实读数」是本稿引用的代码事实及出处，动手前可复核。

---

## 待拍板（已拍板 2026-10-19）

> **裁决汇总**：Q1 甲（`home_project` 新键，`project` 原义不动——复用会让每场新会话变项目房间且无测试变红）；Q2 甲（push bearing 存新列，不复用 desktop 的 `cli_session`/`machine`——防门铃双唤醒）；Q3 甲（按 spec ③ 同步，首轮失败仍 200 带 `resident.error`）；Q4 甲（本轮只做 HTTP+CLI 重试，Web 按钮落 roadmap）；Q5 甲（撤 `SessionSidebar` 的「＋ 新建会话」——spec「入口改到项目上」与人原话「底部不再留新建会话了」的直接推论，无项目上下文的入口与「项目已定不可改」的对话框不相容）；Q6 甲（`expect_title` CAS）；Q7 甲（服务端逐卡校验含项目相符）；Q8 甲（隔离 M 跑 linux-01 本机 127.0.0.1:7792，独立 datadir/SQLite/配置，载体同机 opencode 免费模型，SSH 转发看 Web；不碰生产 7777、不动在役 agentd）。

> 每条给出推荐项（**荐**）与理由；拍板后逐条回写裁决，并把头部状态改为「已拍板（日期）」。

1. **Q1 会话「所属项目」的 wire 键名。** 现有 `SessionSummary.project` 已被占用，含义是「项目房间标记」：Web 用它挂「项目房间」徽章（`SessionSidebar.tsx:122`）、做驻场者的「组织者」角色标签（`sessionModel.ts:89`），collab 的书写者集也按它扩大（`service.go:242-300` 读 `SessionProject`）。
   - **荐 甲**：新事实用新键 `home_project`（`sessions.home_project` 列；`proto.Session` 与 `proto.SessionSummary` 都加 `home_project,omitempty`）；`project` 保持「项目房间」原义，一字不动。Web `sessionProjects` 改成 `project ∪ home_project ∪ 卡项目`。
   - 乙：复用 `project` 键。这会让每个新会话都显示「项目房间」徽章、驻场者被标成组织者、书写者集按项目房间扩大。**否决理由**：语义冲突，而且没有任何现有测试会因此变红。
2. **Q2 push 驻场者的 bearing 存在哪。** `session_residents` 已有 `cli_session` 和 `machine` 两列，含义是**桌面端**会话的 CLI id（门铃信箱键）和桌面端所在机器（`session_resident.go:20-27`）。门铃只靠 `r.CLISession == ""` 来跳过（`residentwake.go:318`）。
   - **荐 甲**：新增几列：`driver_mode`（`''` 为旧行，等同 desktop；`push`）、`push_cli`、`push_session`、`carrier`、`home_dir`、`workdir`、`model`；push 行的机器写进新列 `carrier_machine`，**不复用** `cli_session`/`machine`。门铃另加一条显式判据：`driver_mode=push` 就跳过，原因记 `push_resident`。
   - 乙：复用 `cli_session` 存 push 会话 id。这样门铃会给 push 驻场者写信、调用 continue 深链，形成双重唤醒（缺陷族 R2）。否决。
3. **Q3 建会话请求是同步跑完驻场者首轮，还是首轮转后台。** spec ③ 写的是「对人是一次同步请求，首轮时长同卡叫机器人」，但 S1 又写「创建后会话**立即**出现」。
   - **荐 甲（按 spec ③ 字面）**：HTTP 同步。会话行、拉卡、首轮全部跑完才返回，对话框显示「正在拉起驻场者…」，返回后刷新列表并开 tab。首轮失败也返回 200：会话已建好，驻场位为空，同时带回 `resident.error`。
   - 乙：会话和拉卡完成即返回，首轮由 agentd 后台 goroutine 跑。UX 更「立即」，但后台失败只能靠会话里的系统指针行被看到，而且需要另立一个进程内在途表，防止同一会话被重复拉起。
4. **Q4 「叫驻场者」重试入口。** spec 写「首轮失败：驻场位空，可重试『叫驻场者』」，但 S1–S9 没有给这个重试安排 Web 入口。
   - **荐 甲**：本轮只做 HTTP `POST /api/sessions/{id}/resident/launch {squad}` 和 CLI `handoff session resident launch <session> --squad`（与建会话共用同一编排函数）；Web 只在系统指针行文案里指路这条 CLI。Web 按钮落 roadmap。
   - 乙：会话详情驻场位为空时，加一个「叫驻场者」按钮（需要补原型确认）。
5. **Q5 会话列表 `SessionSidebar` 里的「＋ 新建会话」按钮**（`SessionSidebar.tsx:55`，B358.9 起就有，**不是** B442 加的）。新对话框的项目是「已定、不可改」，而这里没有项目上下文。
   - **荐 甲**：一并撤掉，会话只能从项目上建（与 S9「入口改到项目上」一致）。
   - 乙：保留，对话框补一个项目下拉。（spec 没定义这种形态，要回 spec。）
6. **Q6 自动命名与人工改名的竞态。** 零卡会话由驻场者首轮改名；如果人在首轮结束前已经手动改过名，驻场者会把人的名字覆盖掉。
   - **荐 甲**：改名 API 带可选的 `expect_title`（CAS，不相等返回 409）。驻场者简报要求它用 `--expect-title 新会话` 改名；人在 Web 上改名不带 expect。
   - 乙：不做 CAS，最后一次写入生效。
7. **Q7 拉入卡的项目校验。** 对话框只列本项目的卡，服务端是否也要拒绝 `card.project ≠ home_project` 的卡？
   - **荐 甲**：服务端逐卡校验：卡存在、未终态、项目相符、未属其他会话；不满足的进 `rejected_cards`，并点名原因。
   - 乙：只校验存在与归属，CLI 可以借此建跨项目会话。
8. **Q8 真机靶子拓扑。** 首轮拉起只接受本机载体：`launchCoordinatorRoundWithExpect` 对非本机载体 fail-closed（`scheddrain.go:279-286`）。所以「M 跑在 127.0.0.1:7792」和「真载体在 linux-01」**不能分在两台机器上**。
   - **荐 甲**：隔离 agentd M 跑在 **linux-01** 上（`127.0.0.1:7792`，独立 datadir、SQLite、独立配置），载体用 linux-01 本机的 opencode 免费模型；Web 经 SSH 端口转发访问 M。
   - 乙：M 跑在本 Mac、载体也在本 Mac（要求本机装 opencode 并有可用模型）。
   - 两种方案都**严禁**碰生产 7777，也不替换、不重启在役 agentd。

---

## 0. 交付目标

本轮交付 spec 全部必交故事 S1–S9，按下面顺序串行，一个工作单元：

| 段 | 故事 | 本段做到哪 | 留待后续 |
|---|---|---|---|
| P1（首故事，承重） | S5 | 驻场者行能表达 push 驱动与 bearing；唤醒循环对「push 驻场者地址」走 keystone 续轮，同一 CLI 会话；`@卡号` 不唤醒驻场者；失败落系统指针行，消息不丢 | — |
| P2 | S1/S3/S4（服务端）、S6、S7 | 会话所属项目；建会话编排（建行 → 拉卡 → 拉起驻场者首轮 → 写驻场者行）；自动命名；改名动作与事件；CLI `session create --project/--card/--squad`、`session rename` | 换载体 UI、改项目归属、批量归属工具（spec Out of Scope） |
| P3 | S1/S2/S8/S9（Web） | 桌面项目行 icon、移动端操作面板一项、新建会话对话框、`sessionProjects` 并入 `home_project`、撤 B442 两处入口（以及 Q5 定的那一处） | 原型回流 `prototypes/base/` 归 finish 节点 |

「原 E11 等完成」由 S1+S3+S6 覆盖（spec 已定），不另立实现。

## 1. 触及子系统与类型

| 子系统 | 类型 | 本卡改动面 |
|---|---|---|
| 账本 · 会话领域（`internal/ledger`、`internal/ledger/api`） | 逻辑型 | `sessions.home_project` 列，`session_residents` 增驱动与 bearing 列，改名，迁移 |
| 协作门面（`internal/collab`、`internal/collab/client`、`internal/proto`） | 逻辑型 | wire DTO 加字段，`SitResident` 校验 push 形状，`RenameSession`，摘要投影 `home_project` |
| keystone（`internal/keystone`） | 逻辑型（对面是 Runner 接口，可 fake） | 驻场者键的 launch/resume 与驻场者简报 |
| agentd 编排与唤醒（`internal/agentd`） | **边界型**（对面是真载体 CLI 与多机共库） | 唤醒分流、建会话编排、HTTP 端点 |
| CLI（`cmd`、`internal/client`） | 逻辑型 | `session create` 扩 flag、`session rename`、`session resident launch` |
| Web（`web/src/api`、`web/src/app/{rooms,tree,shell}`） | 逻辑型 + 真机截图 | 入口、对话框、项目归属推算、撤入口 |

单卡一轮，不派子卡。派卡资格四条不适用。

## 2. 故事映射与行为闭环

### 2.1 故事 → 实现责任 → 真实消费者 → 集成/回归负责人

| 故事 | 实现责任 | 真实消费者 | 集成与回归 |
|---|---|---|---|
| S5 | ledger 驻场列 + collab 校验 + agentd 唤醒分流 + keystone 驻场键 | 人在 Web 会话里无 @ 发言 | 本卡执行者；真机清单 R1–R3 |
| S1/S2 | agentd 建会话端点 + Web 对话框与入口 | 人从项目行建会话 | 本卡执行者；真机清单 R4、R7 |
| S3 | agentd 编排里的命名与逐卡拉入 | 同上 | 单测 + 真机清单 R4 |
| S4 | 驻场者简报 + 改名动作 | 驻场者首轮 | 真机清单 R5 |
| S6 | 不改代码；驻场者简报指路 `card coordinate` | 驻场者 | 真机清单 R6 |
| S7 | collab `RenameSession` + HTTP/CLI | 人、驻场者 | 单测 |
| S8 | Web `sessionProjects` 兼容 `home_project` 缺键 | 左栏 | 单测 |
| S9 | 撤入口 | 左栏、移动端 | 单测 + 截图 |

### 2.2 行为闭环（每条跨子系统行为一行）

| # | 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 实现责任 |
|---|---|---|---|---|---|
| B1 | 人在会话里无 @ 发言（`user:` 作者） | `room_message` 账本事件 → `collab.Recipients` 判出驻场者地址（`recipients.go:88-97` 有驻场者先于根卡），驻场者行 `driver_mode=push` + bearing | agentd `consumeAutomationEventsOnce` 的驻场分支 → keystone 驻场 resume | 驻场者在同一 CLI 会话续一轮，并用 `session send --agent <小队>` 在会话里回话 | P1：agentd + keystone |
| B2 | 人发 `@B123` | 同上；`room.IsAddressed` 走显式行，只出席位地址 | 席位 push 路径（不变） | 只有卡席位被唤醒，驻场者零回合 | P1 反例断言 |
| B3 | push 驻场者唤醒失败（无名额、载体不可达、resume 失败） | `wake_claims(card="resident:<session>", seq)` 认领留在飞（准入满）或收尾加退避（其他错误） | 会话房间 | 会话里出现**恰一条**系统指针行（谁、为什么、怎么重试）；下一轮重试，消息不丢 | P1：agentd（`collab.Service.Pointer`，`service.go:428-445`） |
| B4 | 人在 Web 项目行点「新建会话」→ 提交 | `POST /api/sessions`（扩字段）→ 会话行（含 `home_project`）、`session_cards`、驻场者行 | Web 会话列表刷新 → `sessionProjects` | 会话出现在该项目下（零卡也在），并以 tab 打开；详情成员里有 kind=resident | P2 服务端 + P3 Web |
| B5 | 建会话时选卡、标题留空 | 编排层按**已接受**卡算标题 | 会话列表 | 1 张 → 卡标题；多张 → 「<首张> 等 N 张」；被拒的卡出现在响应 `rejected_cards`，对话框点名 | P2 |
| B6 | 零卡会话的驻场者首轮 | 简报指示 `handoff session rename <s> --agent <小队> --expect-title 新会话 <名>`，落 `session_renamed` 事件 | Web 列表与 tab 标题（既有列表刷新） | 标题由「新会话」变成概括名；人先改过的名字不被覆盖（Q6） | P2 简报 + 改名 |
| B7 | 人或驻场者改名 | `sessions.title` + `EvSessionRenamed` | 详情 timeline（新 kind `renamed`）、列表 | 改名生效、timeline 多一行；非群主、非驻场者被拒（403） | P2 |
| B8 | 旧会话（无 `home_project`） | `sessions.home_project=''` → wire 缺键 | Web `sessionProjects` | 位置照旧按卡推算，不迁移、不变样 | P2 投影 + P3 |
| B9 | push 驻场者处理会话里的卡 | 驻场者执行 `handoff card coordinate <卡>`，走既有 `POST /api/cards/{id}/coordinator/launch` | 卡席位（B435 不变） | 卡被坐上 push 席位；卡换列照常通知项目房间组织者 | 不改代码，简报指路；真机验（**未验证，需真机**：嵌套回合抢名额，见 R3） |

## 3. 改动边界（按段列有界文件集）

**架构方向**（spec「允许的新依赖方向」）：只允许 agentd → collab / scheduling / keystone 这几个既有方向；**不得**新增 collab → scheduling/keystone 的依赖。collab 和 ledger 只存事实，push 唤醒的执行留在 agentd。keystone 不读驻场表：驻场 SessionRef 由 agentd 读出后传入，`keysclient.LedgerView` 不扩（`keysclient.go:92-97`）。

### P1（S5）文件集

- `internal/ledger/store.go`：PG 分支（`:434-440`）和 SQLite 分支（`:620-626`）的 `session_residents` 内联 DDL 都加新列；在 `migrateDriverMode` 之后（`:754`）挂一个新迁移函数，加列、容忍重复列，沿用 `migrateDriverMode` 的文案判据（`:663-674`）。新列一律 `TEXT NOT NULL DEFAULT ''`，旧行零回填（`''` 即 desktop）。
- `internal/ledger/session_resident.go`：`SessionResident` 结构、`SessionResident()` 的 SELECT/Scan、`SitResident` 的 INSERT 都扩新列。
- `internal/ledger/api/api.go:228-260`：手写的逐字段映射扩新字段（这是一处序列化边界）。
- `internal/proto/sessions.go:80-94`：`proto.SessionResident` 加 `driver_mode,omitempty` 和 bearing 字段（全部 omitempty），并加词表常量 `ResidentDriverDesktop=""`、`ResidentDriverPush="push"`。
- `internal/collab/sessions.go:221-242`：`SitResident` 加 push 形状校验：push 行必须带 `push_cli`、`push_session`、`carrier`，身份必须是 `agent:`；desktop 行不得带 push 字段。
- `internal/agentd/wakeconsumer.go`：
  - `roomMessageWakeEvents`（`:328-366`）目前遇到驻场者地址一律 `continue`（`:350-354`），要改成：push 驻场者产出一条驻场唤醒，desktop 照旧跳过。
  - 唤醒载体：在 `keystone.WakeEvent` 上增加一个能区分「驻场键」与「卡键」的字段，或在 agentd 内另立 pending 结构，由执行者选，但必须满足 §5 的 A1.4/A1.5。
  - `consumeAutomationEventsOnce`（`:643-892`）的卡批循环之外另起一个驻场批：认领键 `resident:<session>`（`wake_claims.card` 无外键，先例见 `projectnotify.go` 的 `notify:<type>`），认领、准入、退避、收尾语义与卡批同形。
- `internal/agentd/coordapi.go:194-202`：转交收件端只保留 `Card==id` 的卡唤醒，丢弃驻场唤醒（防 R1）。
- `internal/agentd/residentwake.go:309-327`：`residentDoorbellIneligible` 加 `driver_mode=push` → `push_resident` 跳过。
- `internal/agentd/scheddrain.go`：新增驻场者唤醒回合函数，与 `wakeCoordinatorRoundID`（`:439-600`）同骨架：读 bearing → 非本机零动作 → `AdmitSeatCarrier` → 组 spec → keystone 驻场 resume → 释放名额。卡专属的部分（`EnsureComment` 写 wake_round 行、`MarkNeedsHuman`）**不**照搬，改为日志加系统指针行。
- `internal/keystone/keystone.go`：新增驻场键的 `LaunchForResident` / `WakeResident`。进程内 ref 表与卡的 `sessions` 分开存，或用不可能与卡号冲突的键前缀。新增驻场者简报 `residentBriefing`，按 `briefing`（`:401-414`）的方式组织，常量句另立。
- `internal/agentd/server.go:1598-1625`：`coordinatorRunner.Resume` 给卡回合注入席位 env。驻场回合是否注入由执行者定：驻场者用 `--agent` 发言，env 不参与（`cmd/session.go:944-958`）。
- 测试：`internal/ledger/*_test.go`（含 PG 腿，`LEDGER_TEST_PG_DSN`）、`internal/ledger/ddl_parity_test.go` 应自然通过、`internal/agentd/*b444*_test.go`、`internal/keystone/*b444*_test.go`、`internal/proto` 契约夹具。

### P2（S1/S3/S4/S6/S7 服务端 + CLI）文件集

- `internal/ledger/store.go`、`internal/ledger/sessions.go`：
  - `sessions.home_project` 列（两方言 DDL 加迁移）。
  - `Session.HomeProject`，`createSessionTx` 写入，`sessionSelect`/`scanSession` 读出（`sessions.go:58-85,309-317`）。
  - 新增 `RenameSession(id, title, expect, actor)`，落 `EvSessionRenamed`。
  - `EnsureProjectRoom` 走 `createSessionTx`：项目房间的 `home_project` 由执行者决定写该项目还是留空，并在交回物「偏离与自主决定」里说明。
- `internal/ledger/api/api.go:179-185,322-333`：`CreateSession` 签名与 `sessionWire` 扩 `HomeProject`；新增 `RenameSession` 直通。`internal/collab/client/client.go:94-119` 接口同步。
- `internal/proto/sessions.go`：
  - `Session` 和 `SessionSummary` 加 `HomeProject string \`json:"home_project,omitempty"\``（以 Q1 拍板为准）。
  - `SessionEventRenamed = "renamed"`、`SessionRenamedPayload`。
  - 建会话请求和响应 DTO：`SessionCreateReq{title, project, cards[], squad}`；`SessionCreateResp` 内嵌 `Session`，再加 `rejected_cards[{card, reason}]`（omitempty）和 `resident{identity|error}`（omitempty），保证旧客户端仍能按 `Session` 解析。
- `internal/collab/sessions.go`：`CreateSession` 扩 homeProject；`summarizeSession`（`:391-433`）填 `HomeProject`；`RenameSession` 只允许群主或现驻场者，非空标题，其余返回 `ErrNotWriter` 类哨兵；`sessionTimeline` 识别改名事件。
- `internal/agentd/sessionsapi.go:57-97`：`handleSessionCreate` 改成调编排函数，空标题不再 400；新增 `POST /api/sessions/{id}/rename`、`POST /api/sessions/{id}/resident/launch`（Q4），路由登记在 `ledgerapi.go:64-71`。
- `internal/agentd/` 新文件，例如 `sessioncreate.go`：编排「校验小队（角色必须是 coordinator）→ 逐卡预校验（Q7）→ 算标题 → 建会话 → 逐卡 Join → 拉起驻场者首轮 → SitResident(push)」。首轮拉起与卡「叫机器人」**抽共用**：从 `launchCoordinatorRoundWithExpect`（`scheddrain.go:241-330`）里抽出「LaunchAdmit → 读载体 → 本机校验 → 组 spec → keystone launch → 释放名额」这一段，给卡和驻场两个调用方共用，不复制。工作目录用 `home_project` 解析（`resolveLocalProjectRoot` 目前收 `ledger.Card`，`coordapi.go:359-373`，需要改成按项目名解析的形参）。
- `cmd/session.go:640-667`：`session create` 的位置参数 title 改为可选，加 `--project`、`--card`（可重复）、`--squad`。带 `--squad` 时走 agentd HTTP（`internal/client` 新增 `SessionCreate`，对照 `squads.go:78-95` 的 `CoordinatorLaunch` 写法）；不带时保留本地账本直写，向后兼容。新增 `session rename`（`--agent`/`--expect-title`）和 `session resident launch`（Q4）。
- 驻场者简报的内容（spec「驻场者简报」）：角色是回人话；按需为本会话的卡 `handoff card coordinate`；发言命令 `handoff session send --agent <小队> <会话> <正文>`；零卡且标题仍为「新会话」时，首轮按 Q6 用 CAS 改名；首轮没有「席位未落」的问题，因为驻场身份是 `agent:` 而不是席位，首轮即可发言。

### P3（Web）文件集

- `web/src/api/rooms.ts:148-169,271-274`：`SessionSummary`/`Session` 加 `home_project?`；`createSession` 改收 `{title?, project, cards, squad}`，返回 `SessionCreateResp`；新增 `renameSession`；`SessionTimelineKind` 加 `'renamed'`。
- `web/src/app/rooms/sessionModel.ts:136-146`：`sessionProjects` 并入 `home_project`（并集，去重保序）。
- `web/src/app/rooms/NewSessionDialog.tsx`：重写为四个字段。项目只读；标题可空，占位「留空则自动命名」；卡多选，只列本项目、未进任何会话、未终态的卡，空态「本项目没有可拉入的卡」；小队必选，预填 role=coordinator 的唯一小队（`getSquads`，`api/scheduling.ts:116`）；无协调小队时提示去设置建小队，并禁用创建。
- `web/src/app/shell/Shell.tsx:288-289,872-890,1248,1649,1994-1995`：`openCreateSession(project)` 带项目参数；`confirmCreateSession` 成功后刷新列表并 `openSession` 开 tab；展示 `rejected_cards` 和 `resident.error`。
- `web/src/app/tree/ProjectTree.tsx:906-930`：`desktop-project-actions` 在文件夹按钮之后加「新建会话」icon，`aria-label`/`title`「在 <项目> 新建会话」。撤掉 `:1464` 底部入口和 `onCreateSession` 底部用法。
- `web/src/app/tree/MobileWorkspace.tsx:138`：撤掉「⋯」菜单里的 `mobile-create-session`；在 `:178-181` 的「更多项目操作」组里加一项「新建会话」。
- `web/src/app/rooms/SessionSidebar.tsx:55`：按 Q5 处置。
- 测试：`NewSessionDialog.test.tsx`、`Shell.test.tsx`、`ProjectTree`/`MobileWorkspace` 相关测试、`sessionModel` 测试、`api/contract.test.ts` 与 `rooms.test.ts`。

## 4. 工作顺序

**只细化 P1。P2、P3 在 P1 验收通过后由执行者按 §3 的文件集自行展开。**

P1 内部顺序：
1. **先验承重假设 H1**：「push 驻场者可以复用卡席位的 resume 路径，且不会和门铃、转交、卡批互相串」。先写 agentd 层的失败用例 A1.1–A1.5（fake Runner），再动代码。
2. 账本列与迁移（SQLite + PG），api 映射和 proto 字段，A1.6 序列化断言先红后绿。
3. collab `SitResident` 的 push 校验（A1.7）。
4. keystone 驻场 launch/resume 和简报（A1.8）。
5. agentd 唤醒分流、驻场批、门铃跳过、转交过滤、失败指针行（A1.1–A1.5、A1.9）。
6. 真机 R1–R3：P1 的完成标准是真机通过，不是单测通过。
   - **阻塞规则**：R1 不通过则 P2 不开工，回本节重议。若 resume 机制本身不适用于驻场者，回 spec。

P2 的顺序提示：账本 `home_project` 和改名（含迁移）→ collab 投影 → 抽共用拉起函数（先跑卡「叫机器人」的既有测试证明零回归）→ 编排函数与端点 → CLI → 真机 R4–R6。
P3 的顺序提示：wire 类型与夹具 → `sessionProjects` → 对话框 → 入口与撤入口 → 截图 R7。

## 5. 验收方式

测试运行：三段律（触及包 → 相关包 → 全量），沿用 implement。Go 用 `go test ./internal/ledger/... ./internal/collab/... ./internal/keystone/... ./internal/agentd/... ./cmd/...`；Web 在 `web/` 下跑 `npx vitest run`。PG 腿设 `LEDGER_TEST_PG_DSN`，指向 linux-01 上的临时测试库（按全局规则建专属账号，凭据不进对话和提交）。

### P1 断言

- **A1.1 成功**：会话 S 坐 push 驻场者，人（`user:`）发无 @ 消息 → fake Runner 恰好收到一次 `Resume`，`SessionID` 等于驻场行的 `push_session`，`HomeDir`/`Workdir`/`Model` 来自驻场 bearing；认领键是 `resident:S`，收尾后自动化水位越过该 seq。
- **A1.2 反例 `@卡号`**：同会话里有卡 C 坐 push 席位，人发 `@C` → 只有 C 的 `Resume`，驻场者零次。
- **A1.3 反例 desktop 驻场者**：旧驻场行（`driver_mode` 空、有 `cli_session`）→ 零次 keystone 调用，门铃照旧按响（既有门铃测试不改仍绿）。
- **A1.4 反例 双重唤醒**：push 驻场者 → 门铃零写信、零 opener（跳过原因 `push_resident`）；转交收件端收到含驻场消息的批次 → 驻场唤醒不进卡回合。
- **A1.5 反例 自唤醒**：驻场者本人（`agent:<小队>`）发 `@agent:<小队>` → 零次 Resume（作者等于驻场身份时不唤醒）。
- **A1.6 序列化边界**：账本写一行 push 驻场 → `api.Facade.SessionResident` → `proto.SessionResident` → HTTP `GET /api/sessions/{id}` 的 JSON 里有 `"driver_mode":"push"` 及 bearing 键；旧 desktop 行的 JSON **没有** `driver_mode` 键（断言键缺失，不是断言值为空）。走真实转换链，不分两端各自单测。PG 腿：旧库先建无新列的 `session_residents` 并插一行，再 Open → 迁移成功，旧行读出 `driver_mode==""`，重复 Open 幂等。
- **A1.7**：push 行身份为 `user:`、缺 `push_session`、或 desktop 行带 push 字段 → `ErrResidentIdentity` 类拒绝，零写入。
- **A1.8**：驻场简报含 `--agent <小队>` 的发言命令和 `card coordinate` 指路；零卡且标题为「新会话」时含 CAS 改名指令；不含卡席位句 `--cli/--session`。
- **A1.9 失败可见**：准入满 → 认领留在飞、水位不越过；下一轮名额恢复后成功；连续失败期间会话里的系统指针行**存在**且不随 2s 节拍刷屏（同一批至多一条，按「存在」与「不超过 1」断言，不按精确计数断言）。归档会话的驻场唤醒 → 零回合，并标 seen（不活锁）。

### P2 断言

- **A2.1 成功 S1/S3**：`POST /api/sessions {project:p, cards:[C1,C2], squad:q}`，标题为空 → 会话 `home_project=p`、标题「<C1 标题> 等 2 张」、两卡都在会话里、驻场者为 `agent:q` 且 push；只给 C1 → 标题 = C1 标题；零卡 → 「新会话」。
- **A2.2 反例 卡已属他会话**：C2 已在 S0 → 响应 `rejected_cards=[{card:C2, reason含 S0}]`，C1 照常拉入，会话照建。
- **A2.3 反例 无协调小队、小队角色不符**：返回 400 带指路文案，**零写入**（会话表行数不变）。
- **A2.4 首轮失败**：fake Runner 的 Launch 报错 → 会话和卡保留，驻场行不存在，会话里有系统指针行，响应带 `resident.error`；随后 `resident/launch` 重试成功。
- **A2.5 改名**：群主改名成功，timeline 多一条 `renamed`；现驻场者改名成功；第三方 → 403 且标题不变；`expect_title` 不符 → 409 且标题不变（Q6）。
- **A2.6 序列化边界 S8**：旧会话（`home_project=''`）的 `GET /api/sessions` 行**无** `home_project` 键；新会话有。Go 夹具经 `internal/proto/contract_fixture_test.go` 生成 `web/src/api/testdata/`，Web `contract.test.ts` 读同一份夹具断言键的有无。
- **A2.7 零回归**：抽共用拉起函数后，卡「叫机器人」（`handleCoordLaunch`）的既有测试全部不改仍绿。
- **A2.8 迁移**：PG 与 SQLite 旧库各有若干存量 `sessions` 行 → 加列成功、旧行 `home_project=''`、可重复 Open；`ddl_parity_test` 绿。

### P3 断言

- **A3.1**：桌面项目行有 `aria-label="在 <项目> 新建会话"` 的按钮，点击打开对话框，项目只读且等于该项目。
- **A3.2**：对话框的卡列表排除他项目卡、已在会话里的卡、终态卡；无协调小队时显示提示并禁用创建。
- **A3.3**：创建成功后调用 `openSession`，开 tab；零卡会话出现在该项目分组下（`singleProjectSessions` 命中，`ProjectTree.tsx:435`）。
- **A3.4 S8**：`sessionProjects({cards:[卡∈p1]})` 结果不变；`sessionProjects({home_project:'p2', cards:[卡∈p1]})` → `['p2','p1']`，即跨项目会话。
- **A3.5 S9**：桌面左栏底部无「新建会话」，移动端「⋯」菜单无 `mobile-create-session`（按 testid 断言缺席）；移动端项目操作面板有「新建会话」。

### 真机清单（隔离靶子 M；**本节由协调者执行，不派发**）

环境：隔离 agentd M 的拓扑按 Q8 拍板，`127.0.0.1:7792`，独立 datadir 和 SQLite，独立配置，测试用账本；协调小队 `q` 只有一名成员，是本机 opencode 免费模型载体。**严禁**指向 7777、替换或重启在役 agentd。每条证据（命令输出、M 日志片段、截图）保存到 `docs/superpowers/evidence/b444/`（finish 时定是否入库）。

- **R1（S5，承重）**：手工建会话，`SitResident` 写 push 行（可经 Q4 的 `resident/launch`，或测试用内部入口）→ 人在 Web 发无 @ 消息 → 驻场者在同一 opencode 会话续一轮并在会话里回话。证据：M 日志里两次回合的 `session` 相同。**未验证，需真机**：opencode 的 resume 能否承接 `agent:` 身份发言。
- **R2（S5 反例）**：会话里有一张 push 席位卡，人发 `@卡号` → 只有卡席位回合，驻场者无回合（日志 grep）。
- **R3（S5 失败与 S6 嵌套）**：把小队并发调到 1，驻场者回合中执行 `card coordinate` → 观察是否 409（嵌套回合抢名额）。**未验证，需真机**；若确实 409，记为缺陷，修法升级拍板（见 §7 R3）。另外停掉载体（或改坏 HOME）→ 会话里出现系统指针行，恢复后重试成功、消息不丢。
- **R4（S1/S3）**：Web 项目行建会话：选 2 张卡、标题空 → 标题「首张 等 2 张」，会话在该项目下以 tab 打开，详情可见驻场者；另选一张已在他会话的卡 → 被点名拒绝。
- **R5（S4）**：零卡、标题空 → 显示「新会话」；发第一条消息 → 驻场者回话并改名，左栏和 tab 标题随之更新。再建一场：在驻场者改名前先手动改名 → 手改的名字保留（Q6）。
- **R6（S6）**：驻场者为会话里的卡叫协调者 → 卡席位 push 坐上；卡换列后项目房间收到通知。
- **R7（S2/S9 截图）**：桌面宽度和 390px 手机宽度各截一张：项目行 icon、移动端操作面板项、底部与「⋯」菜单入口已撤。
- **R8（迁移）**：用 M 的一份旧 datadir 副本（含存量会话和驻场行）启动新二进制 → 迁移日志出现，旧会话位置不变。PG 迁移只在 linux-01 的临时测试库上跑（A2.8 的 PG 腿），**不碰**半生产库和生产库。

## 6. 决策权限

**执行者可自行决定**（交回物「偏离与自主决定」节逐条说明）：
- 驻场唤醒在 agentd 内的载体形态（扩 `keystone.WakeEvent` 还是另立结构），只要满足 A1.1–A1.5；
- keystone 驻场 ref 表的组织方式，私有函数与文件拆分；
- 抽共用拉起函数的切法（以 A2.7 零回归为准）；
- 建会话编排是先预校验后建，还是建后逐卡试拉（以 A2.1/A2.2 为准）；
- 驻场回合是否注入席位 env；
- 系统指针行的措辞（必须说清谁、为什么、怎么重试）；
- Web 组件内部结构、样式细节（入口位置与文案按 spec 字面）。

**必须升级给协调者**：
- 任一「待拍板」项的反向实施；
- 改变 `proto.SessionSummary.project` 的含义或取值；
- 改变 `collab.Recipients` 的判定表；
- 新增 collab → scheduling/keystone 依赖，或让 keystone 读驻场表；
- 驻场唤醒需要跨机转交（新转交 DTO）；
- 发现 R3 嵌套抢名额需要改调度名额语义；
- 迁移需要回填或改写存量行；
- 建会话响应不再兼容旧 `Session` 解析；
- 删除或改写任何既有测试的断言（而非新增）；
- 撤除 Q5 拍板范围之外的入口。

## 7. 缺陷族风险（本卡确实相关的）

- **R1 驻场者与卡席位路由混淆**：唤醒、认领、keystone ref、wake_round 都按卡键设计（`wakeconsumer.go:668-892`，`keystone.go:107-171`）。驻场键混进卡批，会调 `GetCard` 失败或误唤醒卡。→ 由 A1.2、A1.4 守住；认领键使用独立命名空间 `resident:`。
- **R2 双重唤醒**：门铃（`residentwake.go:177`）和 keystone 两路都会收到驻场地址；转交端（`coordapi.go:194-202`）会重新映射事件。→ A1.4 守住。
- **R3 并发名额泄漏或死锁**：驻场回合要 `defer` 释放名额（照 `scheddrain.go:271`）。驻场回合里同步调 `card coordinate`，在同一小队再申请名额，并发上限为 1 时必然 409。→ 真机 R3 判定；修法（例如驻场回合在等待嵌套调用前先释放名额，或驻场者与卡分别计额）须升级。
- **R4 PG 存量行迁移**：`ALTER TABLE ADD COLUMN ... NOT NULL DEFAULT ''` 在两方言下都必须容忍重复列（先例 `store.go:663-674`）。→ A1.6、A2.8 的 PG 腿与 `ddl_parity_test`。
- **R5 归档会话**：`SitResident` 已拒归档会话（`session_resident.go:92-97`）。在途消息在会话归档后仍会触发驻场唤醒，驻场者回话会被 `ErrReadOnly` 拒。→ 唤醒前判归档，零回合并标 seen（A1.9）。
- **R6 卡已在他会话**：`JoinCardToSession` 返回 `ErrBadState`（`sessions.go:177-210`），唯一索引 `uq_session_cards_card` 兜底。编排必须逐卡收集错误，不能整体回滚。→ A2.2。
- **R7 自动命名与人工改名竞态**：→ Q6 的 CAS 加 A2.5、R5。
- **R8 自唤醒循环**：驻场者显式 @ 自己会命中驻场地址。→ A1.5。
- **R9 多机共库**：fleet 是三台机共库（memory: deferred-agentd-rollout）。非 bearing 所在机的 agentd 不得认领驻场唤醒，以免不转交又占认领；每机自动化游标独立，所以本机仍能读到该事件。→ 单测覆盖「非本机零认领」；真机只在单机 M 上验，多机行为标「未验证，需真机」，留在 finish 前由协调者决定是否上 fleet 复核。
- **R10 「缺失 vs 空」**：`home_project` 和 `driver_mode` 的旧值都是空串。wire 层 omitempty 后，Web 必须把缺键当旧数据处理。→ A1.6、A2.6。

「无风险」的族：本卡不新增鉴权模型，`--agent` 自报身份沿用现有信任面，不扩大。

## 8. 需冻结的外部承诺

**结论：无新增冻结对象。** 本卡单卡一轮，不扇出，新接缝（驻场唤醒分流、编排函数、keystone 驻场方法）只被同一工作单元消费，都是可逆的内部实现，不构成冻结。

对外 wire 有变更，按**版本化增量**处理，由 `internal/proto` 与 `web/src/api/testdata` 双侧夹具锁定（这是既有契约机制，不另立冻结文档）：
- `POST /api/sessions`：请求新增可选键 `project`、`cards`、`squad`；`title` 允许为空。响应在原 `Session` 键集上新增 `home_project`、`rejected_cards`、`resident`（均 omitempty），旧客户端按 `Session` 解析不受影响。
- `GET /api/sessions`、`GET /api/sessions/{id}`：`SessionSummary` 新增 `home_project`（omitempty）；成员与驻场投影新增 `driver_mode` 及 bearing 键（omitempty）；timeline kind 词表新增 `renamed`。
- 新端点：`POST /api/sessions/{id}/rename {title, expect_title?}`、`POST /api/sessions/{id}/resident/launch {squad}`（以 Q4 为准）。
- 账本事件新增 `session_renamed`，载荷 `{session, title, from}`。
- CLI：`session create` 加 flag，`session rename` 与 `session resident launch` 为新增命令；现有调用形式不变。

拍板记录候选（满足三重闸门）：Q1（复用 `project` 键在测试里不会变红，但语义错）、Q2（复用 `cli_session` 会导致门铃双唤醒，同样不会有测试变红）。拍板后在本节补记被否方案。

---

## 核实读数（出稿时在基线 `88be981f` 上核对）

- `sessions` 表：PG 在 `internal/ledger/store.go:421-424`，SQLite 在 `:609-612`，没有项目列；`session_residents` 在 PG `:434-440`、SQLite `:620-626`，列为 `session, identity, desktop_session, cli_session, machine, sat_at`。
- 加列迁移先例：`migrateDriverMode` 在 `store.go:663-695`（`ALTER TABLE cards ADD COLUMN driver_mode TEXT`，容忍 `duplicate column`/`already exists`），挂载点 `store.go:754`。另有双方言对等检 `internal/ledger/ddl_parity_test.go`；PG 测试由 `LEDGER_TEST_PG_DSN` 门控（`b409_final_acceptance_test.go:95-97`）。
- `ledger.SessionResident` 结构和读写在 `session_resident.go:20-27,31-55,72-117`，`SitResident` 拒归档会话（`:92-97`）。
- 账本到 wire 的手写映射：`internal/ledger/api/api.go:228-260`（驻场）、`:322-333`（`sessionWire`，无项目字段）。
- `proto.SessionSummary.Project` 已有，含义是项目房间标记：`internal/proto/sessions.go:105-108`，由 `collab/sessions.go:397-407` 经 `SessionProject`（读 `project_rooms`，`ledger/project_room.go:105-118`）填写。Web 在 `SessionSidebar.tsx:122`（徽章）和 `sessionModel.ts:89,136-146` 消费。
- `collab.Service.SitResident` 在 `internal/collab/sessions.go:221-242`，身份须为 `user:`/`agent:` 且不是 `agent:handoff`。
- `Recipients`：`recipients.go`，显式寻址先判（`IsAddressed` → `explicitRecipients`）；非人类作者无 @ → 无收件人；默认收件人有驻场者即归驻场者（`:88-97`）。
- 书写者集含驻场者：`collab/service.go:242-254`。
- 唤醒消费：`roomMessageWakeEvents` 在 `wakeconsumer.go:328-366`，驻场地址 `continue` 在 `:350-354`；`consumeAutomationEventsOnce` 在 `:643-892`，门铃调用在 `:762`，卡批按 `GetCard` 终态闸、`claimWakeBatch`、`keystone.Decide`、`wakeCoordinatorRoundID`。
- 门铃跳过判据：`residentwake.go:309-327`，依赖 `CLISession==""` 和 machine。
- `wake_claims` 主键 `(card, seq)`，`card` 无外键：`store.go:413-419`；非卡键先例 `notify:<type>` 见 `projectnotify.go` 头注释。
- 转交收件端会重新映射全部事件：`coordapi.go:194-202`。
- 拉起：`launchCoordinatorRoundWithExpect` 在 `scheddrain.go:241-330`（`LaunchAdmit`、`defer` 释放、非本机载体 fail-closed `:279-286`、`keystone.LaunchForCard`、`BindSeatMode` + `coordinatorBearing`）；唤醒回合 `wakeCoordinatorRoundID` 在 `:439-600`（读 `SeatBearingOf` → 远端转交 → `AdmitSeatCarrier` → `keystone.Wake`）。
- `resolveCoordinatorSquad`：`coordapi.go:67-93`（0 个 → `errNoCoordinatorSquad`，≥2 个 → 歧义）；`handleCoordLaunch` 同步在 `:95-170`；`resolveCoordWorkdir`/`resolveLocalProjectRoot` 按卡解析，在 `:317-373`。
- `seat_bearings` 表在 `store.go:398-406`，`ledger.SeatBearing` 在 `binding.go:246-252`。
- keystone：`WakeEvent{Kind, Card, Summary}` 在 `keystone.go:32-36`；`Wake` 以卡席位为真源，用 `runner.Resume(ref, briefing)` 续同一会话（`:107-171`）；`LaunchForCard` → `launchRound` 用 `runner.Launch(spec, briefing(card, nil, 首轮句))`（`:212-222,307-327`）；简报构造 `briefing` 在 `:401-414`，常量句在 `:337-383`。`keysclient.LedgerView` 只有 `GetCard/EventsFromAsc/EffectiveBaseBranch/MarkNeedsHuman`（`keysclient.go:92-97`）。
- Runner 的 resume 注入席位 env：`agentd/server.go:1598-1625`，`seatEnv` 在 `:1670`。
- `session send --agent <名>` 编码为 `agent:<名>`，与 `--cli/--session` 互斥：`cmd/session.go:853-866,944-958`。
- 系统指针行入口：`collab.Service.Pointer`（`service.go:428-445`，`kind=pointer`、`BySystem=true`，Recipients 判为结构行不唤醒）。
- 建会话 HTTP：`handleSessionCreate` 在 `sessionsapi.go:57-97`，标题为空返回 400，返回 `proto.Session`；路由在 `ledgerapi.go:64-71`。CLI `session create <title> --owner` 直写本地账本：`cmd/session.go:640-667`，组装点 `cmd/room.go:38-58`。agentd HTTP 客户端先例 `internal/client/squads.go:78-95`。
- `JoinCardToSession`：`ledger/sessions.go:177-210`（归档 → `ErrBadState`，已属他会话 → `ErrBadState`）。
- Web：`createSession(title)` 在 `api/rooms.ts:271-274`；`SessionSummary` 在 `:148-162`；`NewSessionDialog.tsx` 只有标题字段；`Shell.tsx` 的 `openCreateSession` 在 `:288-289`，`confirmCreateSession` 在 `:872-890`（只刷新、不开 tab），对话框挂载在 `:1994-1995`；`ProjectTree.tsx` 项目行动作在 `:906-930`，底部入口在 `:1464`；`MobileWorkspace.tsx` 的「⋯」菜单入口在 `:138`，项目操作面板在 `:163-184`；`SessionSidebar.tsx:55` 另有一个「＋ 新建会话」；`getSquads` 在 `api/scheduling.ts:116`。
- 契约夹具：Go `internal/proto/contract_fixture_test.go` 生成 `web/src/api/testdata/*.json`（`-update`），Web `api/contract.test.ts` 消费。
- 机器：linux-01 是开发/测试机，PG 18 在 5432（`~/.claude/infra/machines.md`）。
