# B435 拆解稿：协调者统一模型——两种驱动模式、一套 wait、驻场者与项目房间组织者

**状态：已拍板（2026-10-18）**（r0 出稿 2026-10-18；协调者同日逐岔口回写裁决列；R-K1 以 spec r4 勘误解决；P6 退回 contract 补签第 ⑤ 条缝）
**卡：** B435（L3 重档；spec → contract → **breakdown** → 首故事子卡 K0 → 其余子卡按故事批次扇出 → 父卡 integrate）
**上游 spec：** `docs/superpowers/specs/b435.md` —— 头部「**上游状态：r3 已批准范围内修订**（r2 用户 2026-10-18 批准…）」实读在位
**冻结 contract：** `docs/superpowers/specs/b435-contract.md` —— 头部「**上游状态：已批准**」「冻结状态：…冻结条目 137 → 145」实读在位；§4 原子冻结清单 145 条（101–129、131–151 / 201–245 / 301–328 含 313a/313b/314a/314b / 401–418）
**有效基线：** `cards/B435-charter-1` @ `0004b815`（含 Ticket 0 与契约 r2 补签）；**尚无 Wave 0 基线**——本稿裁定首故事子卡 K0，其合入父卡分支的 `--no-ff` 合并提交即其余子卡的共同基线
**图依据：** `codegraph/best.json`（`domains` 中 `parent` 空 = 顶层子系统，`type` 实读）；本轮 `codegraph validate` → `issues: null`，视图含 `cards-B435-charter-1`
**台账：** `docs/superpowers/specs/b435-ledger.md` §七（本稿每条读数的命令与原始输出）
**角色边界：** 本文**全部是提案**。不写实现代码、不建卡、不派发、不调 `handoff card`。符号锚用 `file#Symbol`。

---

## 0. 待拍板岔口清单（集中；拍板者按本表逐条裁决）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向（不替拍） | 裁决 |
| --- | --- | --- | --- | --- |
| **P1** | **首故事子卡 K0 是一张还是两张** | **甲：一张 K0**（spec §7 字面：匹配函数 + mode + `card wait` 改造 + 消费循环改接同批）。好处：spec §7 的五个承重未知（同一判定不破守卫、一处 pull 判定盖四处跳过、根卡可判、card wait 行格式兼容、身份记法同一）只有两端同时落地才验得出；一次 Wave 0 验收。代价：跨 5 个子系统、约 20 个文件，单卡偏大。**乙：拆 K0a（d_ledger + d_collab + d_keystone：mode 列、匹配函数、候选超集）→ K0b（d_gateway + d_cli：消费循环、`card wait`）**，K0b 合入才算 Wave 0。代价：K0a 没有真实生产消费者（breakdown 纪律「没有真实消费者的接口不单独派卡」），且两卡之间父卡分支上有一段「函数已落、没人调」的中间态。 | **甲**。§3.1 给出 K0 内部顺序，plan 可分 task 不分卡。 | **甲**。一张 K0；plan 内分 task。 |
| **P2** | **Wave 0 要不要落驻场者的读路径** | 契约判定表第 5 行要读 `SessionResident`；Ticket 0 的 `internal/ledger/api/api.go#Facade.SessionResident` 恒报未实现，C-228 又规定读账本失败须返回错误、不得当未命中——K0 若不落读路径，匹配函数对一切无寻址人类消息都报错；以「恒无驻场者」常量顶替被契约 §10.1 明令禁止。**甲：K0 落驻场者表 DDL（两方言）+ `Store` 只读查询 + Facade 映射**，无写入口（表恒空 = 真实的「无驻场者」）；写面、`session sit/stand` 归 R。**乙：把 S3 的写面与 `session sit/stand` 拉进 Wave 0**。代价：违背 spec §7「驻场者不在 Wave 0」（r1 审计指出的循环依赖正是这个）。 | **甲**。collab 单测以 `fakeLC.SessionResident` 覆盖判定表第 5 行与 C-214/C-219，账本真实写入在 R。 | **甲**。K0 落 DDL + 只读 + Facade；写面归 R。 |
| **P3** | **驻场者存储形态**（P2=甲 时由 K0 落 DDL，必须先定） | **甲：新表 `session_residents`（session 主键 + identity/desktop_session/cli_session/machine/sat_at）**，一行即一房间的驻场者；主键直接在库层兑现 C-414「至多一名」，消掉「先查后写」的并发窗口；`desktop_launchers` 在 X 的迁移中删表。**乙：沿用 `desktop_launchers` 表做语义替换**（spec D10 / §9 字面「表改为驻场者桌面端地址」），驻场登记另立一张身份表，桌面端地址挂在旧表上。代价：旧表主键是 `project`（`store.go:348`），驻场者按会话登记，主键要换，等于重建；且「身份」与「桌面端地址」两表两步写，重现 B434 矛盾 10 的半态。 | **甲**。spec 的「语义替换」按语义兑现（旧语义退役、新语义有落点），不按表名兑现；拍板后在 spec 记一行勘误。 | **甲**。新表 `session_residents`（session 主键）；`desktop_launchers` 在 X 删表；spec D10 r4 勘误。 |
| **P4** | **K0 之后、X 之前，B434 桌面端 bind 席位怎么办** | K0 把 `resolveWakeCard`/`wakeCoordinatorRoundID` 改按 mode 判，桌面端 bind 席位迁移后是 pull。**甲：K0 直接切断**桌面端分支与 `desktopNeedsHumanWakes`（C-238 归 K0），B434 的桌面端唤醒测试在 K0 删除。代价：K0 文件集扩到 `desktopwake.go` 与一批 B434 测试，与 X 抢同一批删除；父卡分支上桌面端唤醒从 K0 起变暗，直到 O2 才有替代。**乙：K0 保留「pull + 有桌面端记录 → 走既有 `wakeDesktopSeat`」的过渡分支**（读的是桌面端记录，不读来源词，不违 C-318），C-238 与分支删除统一归 X。代价：K0 的 `resolveWakeCard` 多一个注定要删的分支；X 必须带一支「过渡分支已删」的源码守卫。 | **乙**。K0 保持 B434 测试全绿，删除集中在 X 一处做。 | **乙**。K0 保留过渡分支（读桌面端记录），C-238 与删除归 X，X 带守卫。 |
| **P5** | **存量 `seat_desktops` / `desktop_launchers` 行的迁移去向**（S7「迁为驻场者桌面端地址或清除（迁移留痕）」） | 读数：`seat_desktops.identity` 是席位身份 `cli:claude#<sid>`，`desktop_launchers` 行没有身份；C-413 要求驻场者身份为 `user:/agent:` 统一记法 → 存量行**无法无损映射**为驻场者。**甲：一律清除并留痕**（迁移在同一事务里把被删行逐条写进 `ledger_meta` 或一条迁移事件，字段原样；升级后 `card show`/`handoff status` 不再出现它们；用户在项目房间重新 `session sit --desktop-session`）。**乙：按 project 把 `desktop_launchers` 行映射成项目房间驻场者**，身份取一个约定值（如 `agent:organizer`）。代价：发明了一个身份；迁移要先建项目房间（X 对 O1 变硬依赖）。**丙：迁移遇存量行即拒绝升级**，提示用户手动 `stand`/清除。代价：升级卡死在用户机上。 | **甲**。留痕载体（`ledger_meta` 键 vs 迁移事件）归 X 的 plan。 | **甲**。一律清除并留痕；载体归 X plan。 |
| **P6** | **三条用户可见文案没有冻结载体**（spec §9：「无收件人（@ 指定）」「需 @」「项目无组织者」；另 spec §8 d_web「项目房间入口」） | 契约只冻结了 `NoRecipientReason` 词表（C-209），没有把它投影到房间消息视图（d_gateway 产出 → d_web 消费），也没有 `card show` 的载体，控制台找项目房间也没有端点或键。**甲：退回 contract 补冻结第 ⑤ 条缝「投递投影」**：会话消息视图增可省略键（如 `no_recipient`，取值即 `NoRecipientReason`），会话列表/详情给项目房间标 `project`；`card show` 的「项目无组织者」走 **stderr 一行**（不动 stdout JSON，外部承诺不变）。生产方归 O1（单一写者 `roomsapi.go`），消费方归 W。**乙：不冻结，把 Go 投影与 TS 消费放进同一张 W 卡**，接缝在一个工作单元内部。代价：W 跨 d_gateway 与 d_web 两个子系统；跨语言序列化边界（缺陷族「序列化边界」）只靠一张卡自证。**丙：只做 `card show` 与 CLI 侧文案，控制台标注降为 roadmap**（改 spec 文案，需用户）。 | **甲**。按 contract 判据它是两个独立工作单元共同依赖的跨进程 wire。 | **甲**。退回 contract 补冻结第 ⑤ 条缝「投递投影」（`no_recipient` 可省略键、会话 `project` 标、`card show` 无组织者走 stderr）；O1/W 投影部分在补签后开工，K0 不等。 |
| **P7** | **系统通知（D8）的「恰一条」由谁保证** | 每台 agentd 各跑一个自动化消费循环（`internal/agentd/scheddrain.go#Server.StartAutomation` `:54-66`），都会看到同一条 `card_created`；契约 C-243–245 没写哪台机器发。**甲：只由驻场者所在机（`SessionResident.machine`）的 agentd 发**，与 C-415 写信同判据。代价：`session sit` 在不跑 agentd 的机器上执行时（纯 CLI 驻场者）没人发通知。**乙：任一 agentd 发，经既有 `wake_claims` 按（通知种类, 源事件 seq）认领去重**，沿用消费循环已有的恰一次机制。代价：认领键空间新增一类，需要与卡唤醒认领键不撞。**丙：通知由写源事件的进程同事务落账**（CLI `card add` 自己写通知）。代价：通知生产者散到 d_cli 与 d_gateway 多处，违背 D8「agentd 以 `agent:handoff` 发」。 | **乙**。 | **乙**。任一 agentd 发，经 `wake_claims` 按（通知种类, 源事件 seq）认领去重；认领键类加前缀与卡唤醒键隔离。 |
| **P8** | **S8「游标丢失 → 从房间头回放并标『回放』」的标注载体** | **甲：读者侧判定**——`SessionBacklog.from_seq`（`internal/proto/sessions.go:189-195`）为 0 即回放，组织者简报写明「`from_seq=0` 视作回放，逐条先查人类是否已答」；wire 不变（C-138 金样本不改）。**乙：退回 contract 给 `SessionBacklog` 增可省略键 `replay`**。代价：动 C-138 冻结的金样本。 | **甲**。 | **甲**。`from_seq=0` 即回放，组织者简报写明；wire 不变。 |
| **P9** | **S10 是否另立实现卡** | S10 的机内行为（pull 席位零回合、消息落账、游标不推进、之后 `card wait` 一次性续收）全是 K0 的条目（C-115、C-118、C-119、C-122、C-230、C-328）；控制台成员状态按 B358 条 17 报 `last_active` 是既有行为。**甲：不另立卡**，S10 指向 K0 合入提交，最终回归归 I。**乙：另立一张验证卡**。代价：纯测试卡，纪律默认归故事卡或父卡最终验收。 | **甲**。 | **甲**。不另立卡。 |
| **P10** | **W 卡（d_web）的执行位置** | 本机 `web/node_modules` 为空（台账 §七.4），tsc/vitest 跑不了；契约欠账 11 就因此未编译验证。**甲：派到有 Node 工具链、能 `npm ci` 的执行机**，跨语言 fixture 的 Go 侧 `-update` 也在那里跑。**乙：协调者本机先装依赖**（下载需人许可）再本机执行。 | **甲**。 | **乙优先、甲兜底**：W 开工前请用户授权本机 `npm ci`；不授权则派有 Node 的执行机。 |
| **P11** | **`skills/handoff/SKILL.md` 与锁文本测试的写者** | 五张卡都想改它（K0：`card wait`/`card bind --mode`；R：`session wait/sit/stand`；O1：`project room`；O2：组织者与授权规则；X：删「占座」节）。**甲：按节归属**，每卡只改自己那一节，串行合并、后卡 rebase。**乙：单列一张收尾文本卡 K**，排在全部卡之后。代价：文本在各批演示时与行为不一致；其他 agent 依赖 skill 文本走 `card wait`（spec §10），K0 的文本不能晚到最后。 | **甲**。 | **甲**。按节归属，串行合并、后卡 rebase。 |
| **P12** | **架构法第三条信号命中：是否插竖切还债卡** | 命中：`internal/agentd` 64 个非测试源文件无子包（信号 2）、**20,511 行（信号 3 的 2 万下沿；B434 拆解时 19,037）**；`cmd` 61 个无子包（信号 2）、`card*` 前缀 13 个（信号 1）。**甲：不插**——每张卡都写得出显式文件子集（§3），且本卡净减：X 删掉 `desktopwake.go` 1,518 行中的大半、`cmd/card_seat_desktop.go` 216 行、`cmd/launcher.go` 302 行，O2 的新桥落在新文件。**乙：插一张还债卡**把唤醒消费循环抽成 `internal/agentd/wake/` 子包。代价：消费循环全是 `Server` 方法、读 `s.ledger/s.rooms/s.mgr`，抽包要新造接口（第四条「单实现 interface 降档」会反咬），且改契约户口。 | **甲**；X 验收记 agentd 退役后行数读数，命中与回答写进 K0/X 的图对账备注。 | **甲**。不插还债卡；X 验收记 agentd 行数读数。 |

> **退回项（不是偏好岔口，按纪律必须回写）**见 §2.3：**R-K1**（spec D8「协调者提问无 @ 落驻场者」与冻结 C-201/C-218 冲突，S6 触发不成立）。P6=甲、P8=乙 若被选中，与 R-K1 合并成一轮 contract r3 补签。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型取自 `codegraph/best.json`（`parent` 空 = 顶层，`type` 实读；台账 §七.3）。`plugins/claude-desktop-wake/` 与 `desktop/` 不在图里，单列。

| 子系统 / 载体 | 图类型 | 本卡角色 | 有界文件集（路径规则） | 派卡资格四条 |
| --- | --- | --- | --- | --- |
| `d_collab` 协作房间 | 逻辑型 | 实现：匹配函数 `Recipients/Matches/defaultRecipient` 落码替换空壳；`AddressesCard` 删除；`MessageWakeTargets` 降为内部步骤；会话详情投影 `SessionCard.seat_mode`、`resident` 成员行 | `internal/collab/{recipients.go,sessions.go}`、`internal/collab/room/delivery_gate_test.go`（守卫）、`internal/collab/*_test.go` | ①可圈；②导出面 = `Service.Recipients`、`Service.Matches`（C-211/212）+ 既有门面；③只依赖 d_ledger（`client.LedgerClient` 接口缝）与 d_protocol；④逻辑型（fakeLC + 真账本闭环）。**通过**（K0、R 各持一段） |
| `d_ledger` 卡片账本 | 逻辑型 | 实现：`cards.driver_mode` 列与回填迁移；`BindSeat/RebindSeat/clearSeatTx` 写 mode；驻场者表与读写；候选超集两条新支；席位地址游标下界查询；项目房间绑定（D6）；B434 两表退役迁移 | `internal/ledger/{store.go,binding.go,cards.go,types.go,session_candidates.go,session_delivery_cursor.go,seat_desktop.go}`、新 `internal/ledger/session_resident.go`、新 `internal/ledger/project_room.go`、`internal/ledger/api/api.go`、对应 `_test.go`（含 `*_pg_test.go`） | ①可圈；②契约面 = C-303/304/308/309/325/326 + `Facade.SessionResident` + 候选读 C-148–151；③被 collab/gateway/cli 依赖，自身只依赖 d_protocol；④逻辑型（真 SQLite；PG 方言 `LEDGER_TEST_PG_DSN` 门控，见真机清单）。**通过**（`store.go` 多写者，见 §3 施工排序） |
| `d_keystone` 协调者 Keystone | 逻辑型 | 实现：`Wake` 对 pull 席位 no-op、`Locate` 按 mode（C-315、C-318）；push 简报房间口吻（D11） | `internal/keystone/keystone.go` + 测试 | ①可圈；②`Service.Wake/Locate` 签名不变；③读 `proto.Card.DriverMode`（既有边）；④逻辑型。**通过**（仅 K0） |
| `d_gateway` 控制门面（agentd：消费循环 + 信箱桥 + HTTP 面） | **边界型**（HTTP 面 + 本机出站 I/O：信箱文件、`open` 深链） | 实现：消费循环改调 `Recipients`、四处来源判定改读 mode、queue_release 三分支（K0）；系统通知发信（O1）；驻场者门铃信桥（O2）；B434 退役（X）；投递投影生产方（O1，P6=甲） | `internal/agentd/{wakeconsumer.go,scheddrain.go,ledgerapi.go,desktopwake.go,coordapi.go,server.go,roomsapi.go}`、新 `internal/agentd/projectnotify.go`（O1）、新 `internal/agentd/residentwake.go`（O2）、对应测试 | ①按卡切开后各自可圈（§3）；②契约面 = C-229–238、C-243–245、C-401–418 + 既有路由（X 删三条）；③依赖 d_collab（`Recipients` 门面，既有方向 `d_gateway→d_collab`）、d_ledger、d_keystone、d_protocol，零新方向（契约 §11）；④边界型：机内用临时 HOME + opener spy + 真账本验形状，信箱被 mod 取走、continue 拉起、多机认领走真机。**通过**（信号 2/3 命中见 P12） |
| `d_cli` 协调者命令面 | 逻辑型 | 实现：`card wait` 改造（K0）；`card bind --mode`、`card rebind` 写 mode、`card show` 投影（K0）；`session wait <session>`、`session sit/stand`（R）；`project room`、`session sit --project`、`card show` 无组织者提示（O1）；B434 命令退役（X） | `cmd/{card_wait.go,card_driver.go,card.go,session.go,project.go,status.go}`、删 `cmd/{card_seat_desktop.go,launcher.go}`、`internal/client/coordinator.go`（X 删两方法）、对应测试 | ①可圈；②契约面 = ①全部 C-101–151 + C-310–314a + C-323；③依赖 d_collab（进程内 `Matches`，沿 `cmd/room.go#roomServiceFor` 组装点）、d_ledger；④逻辑型。**通过**（信号 1/2 命中见 P12；`cmd/session.go` 与 `cmd/card.go` 多写者见 §3） |
| `d_protocol` 协议契约 | 逻辑型 | 只读消费 Ticket 0 已落字面量与 DTO；X 删退役字面量（`ClaudeNewSessionURLPrefix`、`ClaudeNewQMaxChars`、拉起器 DTO）；W 补跨语言 fixture | `internal/proto/desktop_coordinator.go`（X 删段）、`internal/proto/contract_fixture_test.go` + `testdata/`（W） | ①可圈；②已冻结；③无新边；④逻辑型。不单独派卡 |
| `d_web` → `d_web_contract` / `d_web_cards` / `d_web_command` / `d_web_shell` | 逻辑型（`claude://` 真打开与移动端布局是外部现实） | 实现：TS 镜像（`SeatMode`/`driver_mode`、`seat_mode`、`resident`）；`mode` 展示；驻场者/组织者展示；默认收件人提示（P6）；项目房间入口；退役三按钮与 composer 深链 | `web/src/api/{ledger.ts,rooms.ts,scheduling.ts,contract.test.ts,testdata/}`、`web/src/app/cards/{CoordinatorPanel.tsx,CardDrawer.tsx,desktopSeatCopy.ts}`、`web/src/app/rooms/{SessionTab.tsx,SessionChat.tsx}`、`web/src/app/lib/{claudeDeepLink.ts,desktopShell.ts}` + 对应测试 | ①可圈；②契约面 = 契约 §3.3 TS 侧镜像 + P6 载体；③只消费 d_gateway HTTP；④逻辑型。**通过**（执行位置见 P10） |
| `d_maintenance`（`k_skill_*`）+ `skills/handoff/SKILL.md` | 边界型（对面是其他 agent 读 skill 文本、Claude 桌面端 CLAUDE.md） | 实现：授权规则改写为组织者授权（O2）；删「占座」节与 B434 文本测试（X）；各卡命令节（P11） | `internal/skill/{desktop_rule.go,desktop_rule_test.go,skill_text_b434_test.go}`、`skills/handoff/SKILL.md` | ①可圈；②常量 + 文本节；③无新边；④边界型：文本是否被 agent 照做走真机。按 P11 分节归卡，不单独派卡 |
| `d_execution_host`（`internal/hostapi`，print 载体） | 边界型（对面是 opencode CLI） | 只读消费：push 回合沿用既有 `-s <session>` 续跑（S1 复用，不新写）；白名单只有 opencode（`driver.go:52`，载体扩展 OOS） | 零改动 | 不派卡；S1 真机项 |
| `d_scheduling` 编制调度 | 逻辑型 | 只读消费：`IsLocalMachine`（C-415 本机判据）、队列与准入既有 | 零改动 | 不派卡 |
| `desktop/` 薄壳（图外，独立 go module） | 边界型 | 实现：薄壳深链通道 `/new` 形态改为被拒（契约 §7） | `desktop/internal/shell/{external_deeplink.go,external_deeplink_test.go}` | ①可圈；②一个前缀；③零 import；④边界型。归 X |
| `plugins/claude-desktop-wake/`（mod，图外，已装在用户机） | 边界型（对面是 Claude 桌面端 mod 运行时） | **零改动**（C-405 CDC 锁） | 零改动 | 不派卡；门铃信被取走走真机 |
| Claude 桌面端 / `open` / launchd / 移动端浏览器（外部现实） | 边界型 | 信箱与深链的对面、驻场者手机发言 | 不可圈 | 不派卡；行为全进 §6 |

**不触及：** `d_orchestration`、`d_workspace`（项目登记只读）、`d_execution` 其余子域、`d_sessions`、`d_transport`（跨机转交路径不改，C-417）、`d_policy`、`d_web_admin`/`d_web_workbench`。

### 1.1 竖切债核对（架构法第三条，必答）

- `internal/agentd`：64 个非测试源文件、无子包（信号 2）；20,511 行（信号 3 的「2~3 万」下沿）。**能圈出有界文件集吗？能。**K0 触 3 个文件（`wakeconsumer.go` 954 行、`scheddrain.go` 702 行、`ledgerapi.go` 的投影两处），O1/O2 新逻辑各落一个新文件、既有文件只加一处调用，X 以删除为主。单卡上下文远小于 2 万行。是否插还债卡见 P12。
- `cmd`：61 个、无子包（信号 2）；`card*` 前缀 13 个（信号 1）。**能圈吗？能。**K0 改 3 个 `card*` 文件、不新增；X 删 `card_seat_desktop.go`，前缀降到 12。不新增 `card*` 文件。
- `internal/ledger`：33 个（< 40），本卡新增 `session_resident.go`、`project_room.go` → 35，X 删 `seat_desktop.go` → 34。不触发；`session*` 前缀到 4 个，未到信号 1 的 5。
- `internal/proto`：30 个，不触发。
- 结论写进 K0 与 X 验收：「架构法第三条命中，已答：可圈；P12 裁决」。

---

## 2. 边界与冻结核对

### 2.1 上游状态位（读文件头，不靠会话记忆）

- spec `b435.md:3`「上游状态：r3 已批准范围内修订（r2 用户 2026-10-18 批准…）」✅；r3 修订行写明 contract 退回 R1–R3 与 D-B 的裁决。
- contract `b435-contract.md:3`「上游状态：已批准」✅；`:5`「冻结状态：本提交冻结 spec §2 的四条缝…冻结条目 137 → 145」✅（r2 补签与头部一致，无 B434 拆解时那类状态位失真）。
- 图门禁：`codegraph validate` → `issues: null`（台账 §七.3）。

### 2.2 契约 §4 原子冻结清单逐条归属（145 条）

「T0」= Ticket 0 已写并跑过 PASS（契约 §10.2），子卡只需保持绿；**改期望值即越界**。

| 分组 | 条目 | 归属 / 越界结论 |
| --- | --- | --- |
| ① card wait | 101–104、108–129 | **K0**。C-111 删除连带改写 `cmd/card_wait_test.go#TestB353CardWaitFollowFiltersAndContinues` 的期望序列（去掉旧卡房间 `room_message`，台账 §七.5）。 |
| | 105–107 | T0（`TestB435CardWaitLineShapes` 锁现状，K0 改造后期望值不得改）。 |
| ① session wait | 131–144 | **R**。C-138/C-140 金样本（`testdata/session_backlog.json`、`TestSessionWakeFixture`）不改。 |
| ① 共性与候选读 | 145 | **K0**（`cmd/card_wait.go` 半边）+ **R**（`cmd/session.go` 半边，守卫在 R 全绿）。 |
| | 146、151 | **K0**（席位地址）+ **R**（驻场者地址）。 |
| | 147 | **K0** 保持（`cmd/wait.go` 零改动，I 回归）。 |
| | 148、150 | **K0**（席位地址新支）。 |
| | 149 | **R**（驻场者地址新支）。 |
| ② 判定表 | 201、206–213 | T0。 |
| | 202–205、214–228 | **K0**（判定表八行全部落码，驻场者两行以 fakeLC 覆盖，P2=甲）。 |
| | 229–233、235–237、239、240、242 | **K0**。 |
| | 238 | P4=乙 → **X**；P4=甲 → K0。 |
| | 234 | **O2**（新桥）+ **X**（旧桥删除后守卫全绿）。 |
| | 241 | **R**（`cmd/session.go:512` 是 collab 外最后一个调用方）。 |
| | 243–245 | **O1**。 |
| ③ 席位模式 | 301、302、305–307 | T0。 |
| | 303、304、308–328（含 313a/313b/314a/314b） | **K0**。 |
| ④ 驻场者来信 | 401、402、405、406 | T0。 |
| | 413、414 | **R**（`session sit` 写面；P3=甲 时 414 的库层兑现在 K0 的 DDL 主键）。 |
| | 403、404、407–412、415–418 | **O2**。 |

无条目越出 spec §6 故事范围，无条目落在「无人认领」。

**契约 §12 欠账 13 条归属**：

| # | 欠账 | 归属 |
| --- | --- | --- |
| 1 | R1–R3 已裁 | **I**（最终验收核对：C-201/313a/313b/314a/314b/415–418 的实现与测试存在） |
| 2 | D-B 已确认 | **I**（同上，核 C-217 与 B358 条 20–25 金样本未动） |
| 3 | 两个 wait 改造 | **K0**（card wait：C-101–129、145–151 席位半边）+ **R**（session wait：C-131–144、145–151 驻场者半边） |
| 4 | 匹配函数落码、删 `TestB435RecipientsShellNeverSucceeds`、守卫改写三行 | **K0** |
| 5 | `driver_mode` DDL、迁移回填、投影 | **K0** |
| 6 | `card bind --mode`、`rebind` 写 mode、`card show`/`session detail` 投影 | **K0** |
| 7 | 驻场者登记表、Store、Facade、`session sit/stand` | **K0**（DDL + 读，P2/P3）+ **R**（写面与命令） |
| 8 | 消费循环改接、四处来源判定、守卫 C-318 | **K0** |
| 9 | 门铃信桥、写信失败处置、本机判据 | **O2**（失败处置是 O2 plan 的前置问题，见 §5.5；若需要新的可见面则回 contract） |
| 10 | B434 退役与迁移留痕 | **X**（Go/CLI/薄壳/账本）+ **W**（控制台三按钮与 TS 深链） |
| 11 | TS 镜像 | **W** |
| 12 | 图覆盖债（`runSessionWait`、`sessionWaitMatch` 未入图；`MessageWakeTargets` 图签名陈旧） | **R**（重写这两个函数，验收含 `codegraph sym` 命中）+ **I**（全量图对账） |
| 13 | 系统通知与项目房间 D6、needs_human 通知事件表 | **O1** |

**契约 §13 移交 plan 附区五条**：根卡计算复用 `GetSession`+`GetCard` → K0；`session sit` 身份来源 `--as` → R；card wait 单循环按 fence 合并 → K0；门铃按批聚合 → O2；`sessionMembers` 追加 resident 行 → R。**没有一条被静默带走。**

**契约 §5/§6 的 B358、B409r2 修订文本回写**：契约写明「由 finish 节点回写」→ 归父卡 **I** 交 finish；子卡不改 `b358-contract.md` 与 B409r2 契约正文。

### 2.3 需退回（不许边拆边加）

| 编号 | 内容 | 性质 | 候选与倾向 |
| --- | --- | --- | --- |
| **R-K1** | spec D8 通知事件表写「协调者提问（协调者自己 `session send` 项目房间，**无 @ 时按 D4 落驻场者**）」；但协调者发言作者是席位身份 `cli:<cli>#<sid>`，冻结 C-201 判为非人类，C-218 规定「无寻址 + 非人类作者 → 空集」。按冻结判定表，协调者的无 @ 提问**叫不醒组织者**，S6 的触发不成立。 | spec 与冻结物冲突（触发条件说不清） | (a) **spec 勘误**：协调者提问须显式 @ 组织者身份；`handoff project room <p>` 打印驻场者身份，push 简报与 skill 写明「向组织者提问先 `project room` 取身份再 @」——零机制改动，判定表不变（D11 同属简报文本）；(b) **contract 修订**：判定表增一行「项目房间内无寻址非人类作者 → 驻场者」——推翻 C-218 的统一口径，项目房间成为判定特例；(c) d_cli 的 `session send` 发往项目房间时自动补 @ 驻场者——新增 CLI 行为，等于由系统替发送者寻址。**倾向 (a)**。裁决前 K0/R 可开工，只有 O1/O2 的简报与 S6 演示待定。 |
| （条件性）P6=甲 | 补冻结第 ⑤ 条缝「投递投影」 | 冻结物缺载体 | 与 R-K1 合并为 contract r3 补签；O1/W 在补签前不开投影部分。 |
| **裁决（2026-10-18）** | R-K1 → (a) spec r4 勘误：协调者向项目房间提问必须显式 @ 组织者身份；`handoff project room <p>` 打印驻场者身份；push 简报与 skill 写明。C-201/C-218 不动，不进 contract。P6=甲 → contract 补签第 ⑤ 条缝（仅此一条）。P8=甲 → 不动 C-138。 | 已裁 | — |
| （条件性）P8=乙 | `SessionBacklog` 增键 | 动 C-138 金样本 | 同上。 |

**没有发现其他「spec 承诺了行为、冻结物里却没有载体」的接缝。**

### 2.4 边界澄清（不退回；随拍板回写 spec/拆解的边界说明，不制造契约修订）

1. **系统通知不是项目房间的首个写入方**：C-245「无驻场者不发通知」，而驻场者必须先有房间，故 D6「首个写入方（系统通知或 `session sit --project`）惰性创建」中「系统通知」一支永不发生；创建只来自 `session sit --project` 与 `project room`。S5 前置本就要求组织者已坐下，可观察行为不变。→ O1。
2. **来源判定不止契约 §1.1 的「四处」**：`internal/keystone/keystone.go#Service.Wake` 的 `:119`（`DriverSource == ""` 判空座）与 `internal/keystone/keystone.go#Service.Locate` 的 `:251`（`!= coordinate`）同样落在 C-318 守卫面内。K0 改写清单必须列入：判空座改读 `DriverSession`/`DriverMode`，`Locate` 改为「mode=push 才可 attach」（与 coordinate→push 迁移映射等价）。`ValidateSeat(…, proto.SeatSource(card.DriverSource))` 作实参不在禁令内（`scheddrain.go:492`、`wakeconsumer.go:541`、`coordapi.go:405`）。→ K0。
3. **`driver_mode` 的手写序列化点**：`cmd/card.go:562` 以手搭 map 输出 `driver_source`，`internal/agentd/ledgerapi.go:120/:176` 手拼 `CardView`，`internal/ledger/api/api.go:296` 手映射 `proto.Card`——三处都要同加 `driver_mode`，并各有一支穿过真实序列化的断言（缺陷族「序列化边界」）。→ K0。
4. **`BindSeat` 签名扩散**：约 130 个调用点、30 个文件，其中生产 5 处、其余为测试（台账 §七.4）。K0 的 plan 须选不扩散签名的形状传入 mode（C-326 禁止从来源词推导，只许迁移推导）；不得为此批量改 25+ 测试文件来换取「签名好看」。→ K0 plan。
5. **pull 席位游标与旧 `session wait cli:…#…` 行同键**（契约台账 §五.4）：K0 落地后，曾用旧形态 `session wait <席位身份>` 留下的游标行被 `card wait` 房间段直接续用，语义一致；不另做迁移。→ K0。
6. **D11 push 简报口吻**属简报文本，落 `internal/keystone/keystone.go#Service.briefing`；K0 是 `keystone.go` 的唯一写者，顺带改（S1 期望「正文只有重点」）。→ K0。
7. **驻场者桌面端地址的 `machine` 缺省**：C-415「`machine` 空 = 登记时的本机」——`session sit` 在 CLI 进程里执行，「本机」= CLI 所在机的机器名，须与 agentd 的 `scheduling.IsLocalMachine` 同一取值来源，否则门铃信无人写。→ R（写入）+ O2（判定）；行为走真机清单 #14。
8. **新桥放新文件**：O2 的门铃信桥落 `internal/agentd/residentwake.go`，只复用 `desktopwake.go` 的保留函数（`writeDesktopLetter`、`claudeContinueURL`、`desktopMailboxHome`、opener），让 X 在 `desktopwake.go` 里的删除与 O2 的新增不落同一函数体。→ O2/X 施工排序。 **追加裁决（2026-10-18，O2 plan B-2）**：O2 复用的 `desktopwake.go` 符号为六组——`writeDesktopLetter`（含 `writeDesktopLetterOnce`、`errDesktopMailboxKeyUnsafe`）、`claudeContinueURL`、`Server.desktopMailboxHome`、opener（`desktopOpener`、`Server.desktopOpen`、`SetDesktopOpener`、`PlatformDesktopOpener`、`errDesktopDeepLinkUnsupported`）、`Server.sweepDesktopLetterTmp`、`Server.desktopClock`；X 的禁删清单以此为准。

---

## 3. 子卡清单与依赖 DAG

子卡编号是提案编号，真实卡号由协调者扇出时分配。下文按各岔口的倾向展开；倾向被否时的改动在各卡注明。

### 3.0 DAG 与批次

```text
（前置：R-K1 裁决；P2/P3 裁决——K0 的 DDL 依赖它）
批 0（Wave 0，单独先派）
   K0 首故事子卡：匹配函数 + mode + card wait + 消费循环改接     → 阶段验收 → --no-ff 合入 cards/B435-charter-1 = 共同基线
        │硬
批 1    ▼
   R 驻场者：session sit/stand + session wait <session>
        │硬                       │硬
批 2    ▼                         ▼
   O1 项目房间与系统通知  ···施工···  O2 驻场者门铃信与组织者简报
        │硬(P6=甲)                  │施工（desktopwake.go / wakeconsumer.go）
批 3    ▼                           ▼
   W 控制台与 TS 镜像  ···施工(合并序)···>  X B434 退役与迁移
        └──────────────┬──────────────┘
                       ▼
          I 父卡 integrate（协调者；真机清单分批 + 全量回归 + finish 回写）
```

| 边 | 类型 | 理由 |
| --- | --- | --- |
| K0 → R | **硬** | R 的 `session wait` 只经 `Matches`（C-137），匹配函数、驻场者读路径（P2）、候选超集席位支、`DeliveryCursorKey` 游标存取都由 K0 落地。 |
| R → O1 | **硬** + 施工 | 通知 `Mentions=[驻场者身份]`（C-244）与「无驻场者不发」（C-245）需要真实驻场者写面；`session sit --project` 加在 R 重写过的 `cmd/session.go` 上。 |
| R → O2 | **硬** | 门铃信的收件地址 `cli_session/desktop_session/machine` 由 `session sit --desktop-session` 写入（C-406、C-409）。 |
| O1 ↔ O2 | **施工排序** | 两卡各在 `wakeconsumer.go` 加一处调用；新逻辑各在新文件。互不是对方的数据前提，可并行，合并时后者 rebase。S5 的端到端演示要两卡都合入。 |
| O2 → X | **施工排序** | X 删 `desktopwake.go` 的退役函数、删 P4=乙 留下的过渡分支；O2 先落新桥再删旧桥，父卡分支上桌面端唤醒不出现空窗。若 P5=乙，另加 **O1 → X 硬**（迁移要先有项目房间）。 |
| K0、R → W | **硬** | TS 镜像对照 Go 侧 fixture（`driver_mode`、`seat_mode`、`resident` 成员行）生成，Go 投影须已存在。 |
| O1 → W | **硬**（P6=甲）/ 无（P6=乙，生产方并入 W） | 投递投影与项目房间入口的生产方在 O1。 |
| W → X | **施工排序（合并序）** | W 删控制台三按钮须不晚于 X 删 `desktop-launch` 两条路由合入，否则父卡分支上控制台按钮打 404。 |
| 全部 → I | 硬 | 最终集成与真机。 |

**最长链**：K0 → R → O2 → X → I。**批间纪律**：每批合入后协调者跑该批故事演示，并回归已合入批次的故事（批 1 回归 S1/S2/S4a/S10；批 2 回归批 0–1；批 3 回归全部）。

### 3.1 B435-K0：首故事子卡——一个匹配函数、席位模式、`card wait` 改造、push 消费循环改接（Wave 0）

**①故事与契约引用**：spec S2、S4a（Wave 0 选择 §7），并交付 S1（push 回合复用既有 print 路径）与 S10 的机内半边；D1、D2（card wait 半边）、D3、D4（无驻场者分支）、D11、D12（push/pull 与 queue_release）。契约 C-101–129、C-145/146/148/150/151（席位半边）、C-202–205、C-214–233、C-235–237、C-239/240/242、C-303/304、C-308–328；P4=甲 时加 C-238；契约 §5 守卫三行；§12 欠账 3（半）、4、5、6、7（DDL + 读）、8；§13 附区第 1、3 条。

**②意图与为什么**：spec 根因是「协调者是身份还是进程混用」。K0 一次把判定收成「地址 × 消息 → 命中否」一个函数、「push/pull」一个字段：push 消费循环与 pull 的 `card wait` 读同一份判定，四处来源词跳过收成一处「pull 不起回合」。spec §7 的五个承重未知只有两端（消费循环与 `card wait`）同时接上同一函数才验得出，所以 K0 必须跨 d_collab/d_ledger/d_keystone/d_gateway/d_cli 一次落地（P1）。

**③真实生产消费者与本批可演示结果**：消费者 = agentd 自动化消费循环（push）、其他 agent 跑的 `handoff card wait`（pull，按 skill 文本解析 stdout）。演示：同一会话里一张 push 席位卡（opencode）与一张 pull 席位卡（codex/claude 跑 `card wait`）——人在会话 `session send` 无 @ → 只唤醒根卡；@pull 卡 → `card wait` 输出一行、agentd 零回合；`card show` 显示 `driver_mode`。

**④可复核技术验收**：
- **匹配函数**（`internal/collab/recipients.go#Service.Recipients`）：表驱动覆盖判定表八行 + C-214–227（无 @ 人类 `user:`/`cli:<user>@<host>`、无 @ `agent:`、@卡号、@驻场者（fakeLC）、reply_to、结构行、非会话房间、空座根卡、多根卡/零根卡、作者本人）；**反面**：任一用例 `Default=true` 时 `len(Addresses)==1`（C-225）；读账本失败返回错误（C-228，注入 fakeLC 错误断言非 nil 且不当未命中）；`Matches` 与 `Recipients` 等价（C-226，同表回放）。删除 `TestB435RecipientsShellNeverSucceeds`；`TestB435DefaultRecipientReturnsSingleAddress` 保持绿。
- **守卫**：`internal/agentd/wakeconsumer_b358_test.go#TestB3583NoAddressingDoesNotWake` 改为「人类作者无寻址 → 本会话单根卡 push 席位恰一次 Wake；`agent:` 作者 → 零 Wake」；`#TestB3583WakePathSourceGuard` 改为「房间分流函数体须调 `Recipients`、不得调 `MessageWakeTargets`」（C-233）；`internal/collab/room/delivery_gate_test.go#TestWakePathSourceUsesAddressing` 禁用 helper 名单保留、扫描面自动含 `recipients.go`（C-242）；`:253/:274/:326` 与 `internal/collab/room/delivery_gate_test.go#TestResolveDeliveryShapeHasNoMemberSet` 不改仍绿。新增源码守卫：C-318（`keystone.go`/`wakeconsumer.go`/`scheddrain.go` 非测试源码无 `DriverSource ==`/`!=`，含 §2.4 第 2 条的 `:119`、`:251`）；C-145 的 `cmd/card_wait.go` 半边。
- **席位模式**：两方言加列迁移 + 回填（C-325/326），二次执行零改动（迁移跑两遍断言行数与值不变）；`BindSeat/RebindSeat` 同事务写 mode、`clearSeatTx` 置空（C-304/308，注入事务失败断言三列同回滚）；`card bind` 缺省/`--mode pull` → pull，`--mode push` 非 0 退出、stderr 含 `handoff card coordinate <id>`、**席位列未变**（C-313a/313b 反面断言查列）；`card coordinate` 不注册 `--mode`（C-314，`cmd.Flags().Lookup("mode")==nil`）；`card show` 的 `card.driver_mode`（C-310）、会话详情 `seat_mode`（C-327）；三处手写序列化点各一支（§2.4 第 3 条）。
- **消费循环**：C-328 双向断言——同一条命中消息：pull 席位 keystone Wake 0 次且 `card wait` 输出 1 行；push 席位 Wake 1 次且 `card wait` 0 行（真账本 + 真消费循环 `consumeAutomationEventsOnce`，不直调分流函数）；房间消息不上走（C-232：子卡空座时 @子卡零 Wake，**不**落到父卡）；卡自动化事件上走三条（C-235–237）；queue_release 三分支（C-319–321）；驻场者地址零 Wake（C-231，fakeLC 或测试直写驻场者行）。
- **card wait**：`--since` 排他起点、不读不写持久游标（C-102–104）；积压段先于实时段、seq 升序逐条（C-118–121）；无游标行时下界 = 最近 `EvDriverSeatBound`/`EvDriverTakeover(to=该身份)`（C-119，换绑后新持有者**不**收到换绑前的 `@卡号` 历史——反面断言）；stdout 写失败不推进游标且非 0（C-123，注入失败 writer）；游标写失败非 0（C-124）；同进程不重复 seq（C-125）；push 席位/空座/无会话时房间段零输出（C-115）；`--subtree` 不扩展地址（C-114）；退出码 0/124/其他（C-126–129）；`TestB435CardWaitLineShapes` 期望值不改；`TestB353CardWaitFollowFiltersAndContinues` 去掉旧卡房间 `room_message` 期望（C-111）。
- **候选超集**：新支对席位地址（C-148/150），两方言同集同序与有界返回（C-151，SQLite 机内；PG 走真机 #19）。
- **全量**：`go build ./...`、`go vet` 触及包、`go test ./...` 全绿（B434 测试在 P4=乙 下不改仍绿）；`codegraph` 视图 diff 重生成、`validate` 零 issue；`git diff` 中 `internal/proto/` 非测试源码零改动（T0 冻结面）。
- 缺陷族判据见 §5.1。

**⑤入口指针与有界文件集**：
- d_collab：`internal/collab/recipients.go#Service.Recipients`、`#Service.Matches`、`#Service.defaultRecipient`；`internal/collab/sessions.go#Service.MessageWakeTargets`（降为内部步骤）、`#Service.AddressesCard`（删）、会话详情 `SessionCard` 组装处（`seat_mode`）；测试 `internal/collab/recipients_b435_test.go`、`internal/collab/readmodel_test.go`、`internal/collab/room/delivery_gate_test.go`。
- d_ledger：`internal/ledger/store.go#ddlStatements`（两方言：`driver_mode` 列、P2/P3 的驻场者表）与加列迁移段；`internal/ledger/binding.go#Store.BindSeat`、`#Store.RebindSeat`、`clearSeatTx`；`internal/ledger/cards.go`（scan）、`internal/ledger/types.go`（`Card.DriverMode`）；`internal/ledger/session_candidates.go`（席位新支）；席位地址游标下界查询（落点 plan 定，限 `binding.go` 或 `session_delivery_cursor.go`）；新 `internal/ledger/session_resident.go`（**只读**）；`internal/ledger/api/api.go#Facade.SessionResident` 与 `proto.Card` 映射；对应测试。
- d_keystone：`internal/keystone/keystone.go#Service.Wake`、`#Service.Locate`、`#Service.briefing`（D11）。
- d_gateway：`internal/agentd/wakeconsumer.go#Server.roomMessageWakeEvents`、`#Server.resolveWakeCard`、`#Server.automationWakeEvents`（只改分流，P4=乙 保留 `desktopNeedsHumanWakes` 调用）；`internal/agentd/scheddrain.go#Server.wakeCoordinatorRoundID`、BindSeat/RebindSeat 三处调用传 push；`internal/agentd/ledgerapi.go`（CardView 两处投影）；测试 `internal/agentd/wakeconsumer_b358_test.go` 与新守卫测试。
- d_cli：`cmd/card_wait.go#runCardWait`、`#cardWaitEventActionable`；`cmd/card_driver.go#cardBindCmd`（`--mode`）与 rebind `--self`；`cmd/card.go`（`:562` 投影）；`cmd/room.go#roomServiceFor`（只调用）；测试 `cmd/card_wait_test.go`、`cmd/card_wait_b435_test.go`（不改期望）、新 bind mode 测试。
- 文本：`skills/handoff/SKILL.md` 的 `card wait`/`card bind` 节（P11=甲）。
- **禁止**：`cmd/session.go`（R）、`internal/agentd/desktopwake.go`（P4=乙 时；P4=甲 时只许删 `desktopNeedsHumanWakes` 与桌面端分支）、`internal/proto/**` 非测试源码、`web/**`、`plugins/**`。

### 3.2 B435-R：驻场者——`session sit/stand`、`session wait <session>`、驻场者升格（批 1）

**①故事与契约引用**：S3、S4b、S9；D2（session wait 半边）、D4（有驻场者分支的真实写面）、D5；契约 C-131–144、C-145/146/151（驻场者半边）、C-149、C-241、C-413、C-414；§12 欠账 3（半）、7（写面）、12（`runSessionWait`/`sessionWaitMatch` 入图）；§13 附区第 2、5 条；B358 条 14/39 修订语义。

**②意图与为什么**：驻场是 `session wait` 当初的真实用途（在 handoff 不认识的 agent 里接到房间、手机上说话）。现状按成员身份订阅、身份记法与寻址结果对不上（矛盾 5）。R 把订阅地址改成「会话 + 驻场者」，命中只经 `Matches`，并给驻场者一个可写、可撤的登记面；驻场者桌面端地址（`--desktop-session`）是 O2 门铃信的收件前提。

**③真实生产消费者与本批可演示结果**：消费者 = 驻场 agent 跑的 `handoff session wait <session>`（按 `SessionWake`/`SessionBacklog` 解析）、匹配函数的判定表第 5 行（默认收件人落驻场者）、会话详情。演示：S3（人在任意 agent 里 `sit` + `wait`，手机控制台无 @ 发言 → 驻场者收到、同会话根卡协调者不被唤醒；第二次 sit 被拒）、S4b（无 @ 改到驻场者，其余不变）、S9（驻场者 `card bind` 升格，两种地址都收，`session detail` 两行，`stand` 不影响席位）；回归 S1/S2/S4a/S10。

**④可复核技术验收**：
- `session wait` 用法与参数校验（C-131–133：旧 `<member>` 形态非 0 且 ≠124、给形状提示；无驻场者非 0 且提示 `session sit`，**不阻塞**——带短超时断言立即返回）；地址与游标键 `resident:<session>/<identity>`（C-134/135），**不再**以裸成员身份读写游标表（C-136，查游标表无裸身份新行）；`SessionBacklog`/`SessionWake` 金样本不改（C-138/140）；`member` 值 = 驻场者身份（C-139）；`unread` 同投影（C-141）；输出即推进与 `--since` 只读（C-142/143）；退出码（C-144）。
- 驻场者写面：一房间至多一名，第二次 sit 非 0 且原登记不变（C-414）；身份非统一记法拒绝（C-413，表驱动含 `cli:x#y`、`web:h`）；并发两次 sit 恰一成功（库层约束，P3=甲）；`stand` 后 `SessionResident` 为 ok=false；升格后 `stand` 不改卡席位列（S9）。
- 候选超集驻场者支（C-149）两方言同集同序（PG 真机 #19）。
- 源码守卫：`cmd/session.go` 不调 `MessageWakeTargets/AddressesCard/IsHumanAuthor`、不读 `.Mentions`（C-145 后半）；collab 包外 `MessageWakeTargets` 零调用（C-241）。
- 会话详情 `resident` 成员行（C-208、§13 第 5 条）；S9 同一人 resident 与 seat 两行。
- 图：`codegraph sym runSessionWait`（或重写后的入口）在分支视图命中（欠账 12 的前半）。
- 缺陷族判据见 §5.2。

**⑤入口指针与有界文件集**：`cmd/session.go#runSessionWait`、`#sessionWaitRun`、`#sessionWaitMatch`（改为经 `Matches`）、`#sessionWaitPage`、新 `sit`/`stand` 子命令（同文件，按命令一文件惯例也可拆 `cmd/session_resident.go`，plan 定）；`internal/ledger/session_resident.go`（写面）；`internal/ledger/session_candidates.go`（驻场者支）；`internal/collab/sessions.go`（`sessionMembers` 追加 resident 行）；测试 `cmd/session_test.go`、`internal/ledger/*_test.go`、`internal/collab/sessions_test.go`；`skills/handoff/SKILL.md` 的 `session wait/sit/stand` 节。**禁止**：`internal/agentd/**`、`cmd/card_wait.go`、`internal/collab/recipients.go`（判定表已冻结并由 K0 落码；R 发现判定问题只许回报）。

**追加裁决（2026-10-18，用户；K0 阶段验收发现 F-RM1）**：R 卡范围追加 **F-RM1 修复**——push 协调者的 launch 与 resume 回合都不注入 `HANDOFF_SESSION_CLI/ID`（`internal/agentd/server.go#resumeTurnRequest` 自 B299 起无生产调用方；`coordinatorRunner.Launch/Resume` 直走 `executor.Coordinator`），协调者裸 `session send` 的 actor 落 `cli:<user>@<host>`、不在成员集，被 `VerifyWriter` 拒，S1「协调者回房间」在真 opencode 上不成立。为此**对 R 的禁止清单开一条例外**：允许改 `internal/agentd/server.go` 的 `coordinatorRunner.Launch/Resume`（及其直接承载缝）只为注入席位环境变量 `HANDOFF_SESSION_CLI=<cli>`、`HANDOFF_SESSION_ID=<session_id>`（复用或接通 `resumeTurnRequest`），其余 `internal/agentd/**` 仍禁。验收（**2026-10-18 按 R plan 退回项 B-1 修订**）：单测断言 resume 回合环境含两键且值与卡席位身份一致，launch 回合两键均缺（无半注入）；真机：stub `opencode` 在 resume 回合**按简报原文**（成对出示 `--cli/--session`，R plan Q1 乙）`session send` 回房间成功，首轮（launch）回房间显式失败为已知限制。**例外范围按 R plan Q1/Q2/Q4 裁决扩展**：`internal/keystone/keystone.go#Service.briefing`（简报文本）、`internal/executor/coordinator.go` / `internal/keysclient/keysclient.go` / `internal/executor/opencode/coordinator.go`（`Env` 对称字段与透传）、`internal/collab/service.go#Service.sessionWriters`（驻场者入书写者集）；其余禁区不变。读数与复现见 `docs/superpowers/plans/b435.1-impl-ledger.md`「阶段验收：真机批 0」。

### 3.3 B435-O1：项目房间与系统通知（批 2）

**①故事与契约引用**：S5（通知半边）；D6、D7（项目房间游标即驻场者游标）、D8；契约 C-243–245；§12 欠账 13；P6=甲 时补签的第 ⑤ 条缝生产方；P7；§2.4 第 1 条。

**②意图与为什么**：组织者唯一的收件入口是项目房间。D8 把卡结构事件投影成一条 `agent:handoff` 显式 @驻场者的普通消息，让「通知」走与人说话完全相同的寻址通路（不再有独立唤醒类、条 26 不变）。项目房间绑定是账本事实（每项目恰一场），由 `project room` / `session sit --project` 惰性创建。

**③真实生产消费者与本批可演示结果**：消费者 = 匹配函数显式分支（通知 @驻场者 → 驻场者地址）→ 驻场者的 `session wait`（R）与门铃信桥（O2）；控制台项目房间入口（W）。演示：项目房间有驻场者时 `card add` → 项目房间恰一条通知，`session wait <项目房间>` 收到；无驻场者 → 零条、`card show` stderr「项目无组织者」；换列与 `needs_human` 各一条。

**④可复核技术验收**：
- 账本：每项目恰一场项目房间（重复创建返回同一会话，两方言；并发两次创建恰一行）；项目房间不挂目标卡；旧 `project:<name>` 房间仍只读（`collab/service.go:189` 既有测试不改）。
- 通知：`card_created`、`status_moved`、`needs_human` 各产生**恰一条**消息，作者 `agent:handoff`、`by_system=false`、`Mentions=[驻场者身份]`（C-243/244，读回事件载荷逐键断言；`by_system` 用可空类型区分缺失与 false）；无驻场者零条（C-245，反面断言消息数）；按 P7=乙，两台消费循环（同账本两 `Server` 实例）各跑一轮 → 仍恰一条（并发下不翻红）；`needs_human` 本身仍零 keystone Wake（C-238 不回归）。
- 命令：`handoff project room <p>`（不存在则创建并显示，打印驻场者身份——R-K1=(a) 的前提）；`session sit --project <p>`；`card show` 无组织者提示走 stderr、stdout JSON 与 K0 后逐字相同。
- P6=甲：投递投影键的 Go 侧生产 + DTO fixture（W 消费）。
- 缺陷族判据见 §5.3。

**⑤入口指针与有界文件集**：新 `internal/ledger/project_room.go` + `internal/ledger/store.go#ddlStatements`（项目房间绑定表，两方言）；新 `internal/agentd/projectnotify.go`；`internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`（只加一处调用）；P7=乙 时复用 `internal/ledger/wakeclaim.go` 既有认领（不改其语义）；`cmd/project.go`（`room` 子命令）、`cmd/session.go`（`sit --project` flag，R 之后）、`cmd/card.go`（`card show` stderr 一行）；P6=甲 时 `internal/agentd/roomsapi.go`（投影）；测试同包；`skills/handoff/SKILL.md` 的 `project room` 节。**禁止**：`internal/collab/recipients.go`、`internal/agentd/desktopwake.go`、`residentwake.go`（O2）。 **追加裁决（2026-10-18，O1 plan B-1）**：文件集按现状修正四处——① `sit --project` 落 `cmd/session_resident.go`（R 已把 sit 落在此，非 `cmd/session.go`）；② 创建写路径（Q3 甲）与 `project` 装配（Q9 甲）加 `internal/collab/{sessions.go,client/client.go}`、`internal/ledger/api/api.go`、测试替身 `internal/collab/readmodel_test.go#fakeLC`；③ 通知器 handled 集在 `internal/agentd/server.go` 的 `Server` 结构只加一个字段（与 O2 同文件只加字段，施工排序）；④ R-K1 (a) 的 push 简报一句由 O1 承接——`internal/keystone/keystone.go#Service.briefing` 只增一句 + 简报文本测试，O2 不碰 `keystone.go`（O2 plan Q5 甲同口径）。禁改清单不变。

### 3.4 B435-O2：驻场者门铃信桥与组织者简报（批 2）

**①故事与契约引用**：S5（唤醒半边：桌面端组织者 ≤16s 被 mod 叫醒）、S6、S8；D7、D9、D10（保留项与语义替换）、D11；契约 C-234（新桥）、C-403、C-404、C-407–412、C-415–418；拍板 D-A（门铃信）；§12 欠账 9；§13 附区第 4 条；P8；R-K1 裁决后的简报文本。

**②意图与为什么**：桌面端会话不能自己跑 `session wait` 常驻，B434 的信箱 + mod 是它的 pull 实现。O2 把信改成门铃（只报会话、条数、seq 区间、取信命令），交付唯一经 `session wait`——一条交付通道、一份游标，组织者丢上下文后从游标恢复（S8）不重放。组织者的规则、记忆与回答权限是简报文本，不是机制。

**③真实生产消费者与本批可演示结果**：消费者 = 已装在用户机的 mod（零改动，只认首行 `[handoff]` 与文件名）→ 桌面端组织者会话 → 它执行信中的 `handoff session wait <session>`。演示（真机）：O1 的通知或人在项目房间无 @ 发言 → 驻场者所在机写一封两行门铃信 + continue → 组织者醒来、`session wait` 取信、按简报开会话选协调者（S5）；协调者 @组织者提问 → 组织类直接答回卡所在会话、内容类转问人、人已答则不答（S6）；归档组织者会话、新会话 `sit`、从游标续读（S8）。

**④可复核技术验收**：
- 门铃信：正文逐字节等于 `RenderResidentLetter` 金样本（C-401/402 T0 保持绿），不含命中消息正文/作者/第三方原文（C-403，夹具消息正文用唯一哨兵串，断言信中不出现）；一封信 = 一个驻场者地址的一批命中、`Count/MinSeq/MaxSeq` 覆盖本批（C-407）；触发只经 `Recipients/Matches`（C-408、C-234 源码守卫：`residentwake.go` 与 `desktopwake.go` 不调 `MessageWakeTargets/AddressesCard/IsHumanAuthor`、不读 `.Mentions`）；无 `cli_session` 零写信（C-409）；写信不推进 `session_delivery_cursors`、不落账、不经 keystone（C-410/411，查游标表、事件数、keystone spy）；写信后 opener 恰一次、URL = continue 深链（C-412）；文件名与原子写沿用（C-404，`desktopwake_mailbox_test.go` 保持绿）。
- 本机判据：`SessionResident.machine` 非本机 → 零写信、零 opener、零转交请求、零 needs_human（C-415/416，两 `Server` 实例不同机器名同账本）；转交路径不经驻场者（C-417，转交 spy 零调用）；不新建会话键转交 DTO（C-418，`internal/proto` 无新 DTO）。
- 写信失败处置（欠账 9）：plan 先回答「失败落在哪个可见面」（驻场者没有卡，B434 的卡级 needs_human 失败段不适用）；若答案需要新的对外可见面 → 回 contract，不在卡内发明 wire。
- 简报与授权：`internal/skill/desktop_rule.go` 的授权规则改写为组织者授权，`skill_text_b434_test.go` 的两支授权文本测试随新文案改写（契约 §7「改写」行）；SKILL.md 组织者节含规则文件 `.handoff/organizer.md`、记忆目录 `~/.handoff/organizer/<project>/`、D9 两类问题的处置、D11 口吻、P8=甲 的「`from_seq=0` 视作回放」、R-K1 裁决后的提问方式。
- 缺陷族判据见 §5.4。

**⑤入口指针与有界文件集**：新 `internal/agentd/residentwake.go`；`internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`（一处调用）；`internal/agentd/desktopwake.go#writeDesktopLetter`、`#claudeContinueURL`、`#Server.desktopMailboxHome`、opener（**只调用，不改函数体**）；`internal/agentd/server.go`（若需失败计数字段，plan 定，只加字段）；`internal/skill/{desktop_rule.go,desktop_rule_test.go,skill_text_b434_test.go}`（两支授权测试）；`skills/handoff/SKILL.md` 组织者节；测试新建 `internal/agentd/residentwake_test.go`。**禁止**：`plugins/**`（mod 零改动）、`internal/proto/**` 非测试源码、`desktopwake.go` 中待 X 删除的函数体、`cmd/**`。

### 3.5 B435-X：B434 退役与迁移（批 3）

**①故事与契约引用**：S7；D10 退役与语义替换；契约 §7 处置清单全表（退役 / 语义替换 / 改写 / 保留）；C-238（P4=乙）、C-234（旧桥删后全绿）；§12 欠账 10（Go/CLI/薄壳/账本半边）；spec §9 迁移决定；P5。

**②意图与为什么**：B434 的卡级桌面端席位与拉起器代开被 D10 退役，留着就是第二套协调者传输层（spec 根因）。X 删生产路径与对应测试、迁移存量行并留痕，保留项（信箱目录与原子写、continue 深链、mod、跨机转交 + `LetterUnopened`、`card needs` actor）不动。

**③真实生产消费者与本批可演示结果**：消费者 = 升级用户（旧命令、旧数据）与依赖保留项的 O2/push 跨机转交。演示：升级后旧命令（`card bind --desktop-session`、`card seat desktop set`、`card coordinate --desktop`、`launcher …`）不存在或报改名提示；`seat_desktops`/`desktop_launchers` 行按 P5 处置且有留痕；二次升级零改动；门铃信与 push 跨机转交照常。

**④可复核技术验收**：
- 按契约 §7 逐行：退役面生产代码删除 + 测试删除（文件名列表照契约 §7；混合文件 `desktopwake_remote_test.go` 逐用例拆，转交用例保留）；`TestB434DesktopWireLiterals` 改为删 new/QMax 两断言、保留 marker/dir/prefix/continue；`desktop/internal/shell/external_deeplink_test.go` 改为 `/new` 被拒、其余保留；`TestB434CoordinatorWakeRespLetterUnopenedWire` 保留绿；`desktopwake_mailbox_test.go`、`register.test.ts`、`claudemod_test.go` 保留绿。
- 源码守卫：P4=乙 的过渡分支已删（`resolveWakeCard`/`wakeCoordinatorRoundID` 不再读桌面端记录）；`desktopNeedsHumanWakes` 不存在（C-238）；`seat_desktops`、`SeatDesktop*`、`DesktopLauncher*`、`/api/desktop/deliver`、`desktop-launch` 在非测试源码零出现（grep 断言）。
- 迁移：两方言、幂等（跑两遍断言零改动）、同一迁移内删旧表/旧列；按 P5=甲 被删行逐条留痕且可读回；对一份含存量行的 SQLite 账本副本跑升级（夹具），再对真用户账本副本跑一次（真机 #18）。
- 图对账：退役后 `internal/agentd` 行数读数写进验收（P12）；`codegraph` 视图 diff 记删除节点。
- 缺陷族判据见 §5.5。

**⑤入口指针与有界文件集**：删 `cmd/card_seat_desktop.go`、`cmd/launcher.go` 及其测试；`cmd/card_driver.go`（删三 flag）、`cmd/card.go`（删 `--desktop`）、`cmd/status.go`（删拉起器段）；`internal/client/coordinator.go`（删 `DesktopLauncherOpen`、`DesktopDeliver`）；`internal/agentd/desktopwake.go`（删退役函数，保留 O2 引用的四类）、`coordapi.go`（删两路由）、`server.go`（删 deliver 路由与无用字段）、`wakeconsumer.go`/`scheddrain.go`（删过渡分支与 `EvSeatDesktopSet` 清理段）；`internal/ledger/{seat_desktop.go(删),binding.go,store.go}`；`internal/proto/desktop_coordinator.go`（删退役字面量与 DTO）；`desktop/internal/shell/external_deeplink.go` + 测试；`internal/skill/skill_text_b434_test.go`（删两支命令文本测试）；`skills/handoff/SKILL.md` 删「占座」节。**禁止**：`plugins/**`、`internal/agentd/residentwake.go`、`web/**`（归 W）。

### 3.6 B435-W：控制台与 TS 镜像（批 3）

**①故事与契约引用**：契约 §12 欠账 11（TS 镜像）与 10（控制台半边）；spec §8 d_web 职责（默认收件人提示、项目房间入口、驻场者/组织者展示、`mode` 展示）、§9 用户可见文案；S3/S7 的控制台表现；P6、P10。

**②意图与为什么**：Go 侧新增的 `driver_mode`、`seat_mode`、`resident` 成员种类、（P6=甲）投递投影键要穿过 Go↔TS 序列化边界；控制台是人看「谁会收到这条」的唯一地方（spec §1：用户不敢在群里说话）。B434 三按钮与 composer 深链随 D10 退役。

**③真实生产消费者与本批可演示结果**：消费者 = 人（桌面与手机浏览器）。演示：卡页显示席位 `push/pull`；会话页显示驻场者/组织者与两种地址（S9）；无收件人消息标「无收件人（@ 指定）」、多根卡会话标「需 @」；项目房间可从项目进入；卡抽屉不再出现「用 Claude Code 坐下」「复制简报」「让拉起器代开」。

**④可复核技术验收**：Go 侧 `internal/proto/contract_fixture_test.go` 加 `Card/CardView`（带与不带 `driver_mode`）、`SessionCard`（带与不带 `seat_mode`）、resident 成员行、P6 投影键样本，`-update` 生成 `web/src/api/testdata/*.json`；`web/src/api/contract.test.ts` 逐键比对，TS 用可空类型区分缺键与空串；`tsc` 与 vitest 全绿；`desktopSeatCopy.test.ts`、`claudeDeepLink.test.ts` 随删，`CoordinatorPanel.test.tsx` 对应用例随删、新增 mode 展示用例；文案逐字取自 spec §9（测试读常量，不在测试里另写一份）。缺陷族判据见 §5.6。

**⑤入口指针与有界文件集**：`web/src/api/{ledger.ts,rooms.ts,scheduling.ts,contract.test.ts,testdata/}`；`web/src/app/cards/{CoordinatorPanel.tsx,CardDrawer.tsx,desktopSeatCopy.ts(删)}` + 测试；`web/src/app/rooms/{SessionTab.tsx,SessionChat.tsx}` + 测试；`web/src/app/lib/{claudeDeepLink.ts(删),desktopShell.ts}`；`internal/proto/contract_fixture_test.go`（只加样本）。P6=乙 时另加 `internal/agentd/roomsapi.go`（投影生产方）。**禁止**：`internal/**` 非测试源码（P6=乙 除外）、`desktop/**`。**合并序**：不晚于 X（§3.0）。

### 3.7 B435-I：父卡 integrate（协调者）

**①引用**：spec §6 全部故事、§10 测试决定；契约 §5/§6 回写（交 finish）、§12 欠账 1、2、12（后半）。**②意图**：全部子卡阶段完成后一次父卡全量集成，再交最终故事验收。**③消费者**：用户。**④验收**：父卡分支新鲜 `go build ./...`、`go test ./...`、web `tsc`+vitest；`codegraph validate` 零 issue、`check` 不新增 over-budget（基线三处存量）；§6 真机清单逐批走完（标「已验」与读数）；B358/B409r2 修订文本按契约 §5/§6 交 finish 回写；欠账 1/2 的实现存在性核对。**⑤入口**：本稿 §4、§6。

### 3.8 已由首故事子卡完成、不另造卡的故事

- **S1**（push 协调者）：K0 交付（C-201 CLI 人形作者、C-220 默认收件人、C-229 恰一次 Wake、D11 简报），回归负责人 I。
- **S10**（pull 席位无人监听）：K0 交付（C-115/118/119/122/230/328），控制台 `last_active` 为 B358 条 17 既有行为；按 P9=甲 不另立卡，回归负责人 I。

---

## 4. 跨子系统行为闭环核对

只核 spec §6 承诺的跨子系统可观察行为。实现责任中「K0」指其合入父卡分支的提交，最终回归负责人均为 I。

| # | 故事 | 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 实现责任 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | S1 | 用户在会话 `session send` 无 @（作者 `cli:<user>@<host>`） | `room_message` → `Recipients`（C-201 人类、C-220 根卡 push 席位）→ `wake_claims` → keystone `WakeMessage` → hostapi `-s <session>` 续跑 | opencode print 回合 | 会话出现协调者 `reply`，正文只有重点；`card show` `driver_mode=push` | K0（+ hostapi 既有）；真机 #1 |
| 1b | S1 失败 | 同上，卡无席位 | `Recipients` → 空集 `root_no_seat` | 控制台会话页 | 消息落账进未读，详情页标「无收件人（@ 指定）」 | K0（判定）+ O1/W（投影与文案，P6）；真机 #20 |
| 2 | S2 | 任意 agent `card bind`（缺省 pull）+ `card wait`；人发同一条消息 | `cards.driver_mode=pull` → `Matches(席位地址)` → 房间命中段 → 持久游标（键 = 席位身份） | `card wait` 的 stdout → 调用方 agent | 收到同一条消息一行；卡事件照收；agentd 零回合；`card show` `driver_mode=pull` | K0；真机 #2、#3 |
| 2b | S2 失败 | `card wait` 超时 / 输出失败 | 退出码 124；游标仅在 stdout 成功后推进 | 调用方 agent | 超时 124；下一次 `card wait` 续收未输出消息 | K0 |
| 3 | S3 | 人在任意 agent `session sit` + `session wait <session>`；另一人手机控制台无 @ 发言（`user:<n>`） | 驻场者表 → `Recipients` 判定表第 5 行 → `Matches(驻场者地址)` → `resident:` 游标 | `session wait` stdout → 驻场 agent | 驻场者收到并回复；同会话卡协调者零唤醒 | R（+ K0 判定）；真机 #5、#6 |
| 3b | S3 失败 | 第二人 `session sit` 同会话 | 驻场者表主键 / C-414 | CLI | 非 0 退出，原驻场者不变 | R（P3=甲 库层由 K0 DDL） |
| 4 | S4a | 会话两卡（一根一子）各有席位；发无 @、@子卡、reply_to、`agent:` 无 @；两根卡会话发无 @ | `Recipients` 判定表 3/4/6/8 行 | 消费循环（push）或 `card wait`（pull） | 无 @ 只到根卡；@子卡只到子卡；reply_to 只到原作者；`agent:` 无 @ 零唤醒；两根卡无 @ 零唤醒并标「需 @」；默认分支人数恒 1 | K0（判定与唤醒）+ O1/W（「需 @」投影与文案，P6）；真机 #4 |
| 5 | S4b | S4a 基础上 `session sit` | 判定表第 5 行先于第 6 行 | 驻场者 `session wait` | 无 @ 改到驻场者，其余不变 | R；真机 #6 |
| 6 | S5 通知 | 用户 `card add`（另：换列、needs_human） | `card_created`/`status_moved`/`needs_human` → `projectnotify` → collab `Send`（作者 `agent:handoff`、`Mentions=[驻场者]`）→ 项目房间 `room_message` | 匹配函数显式分支 → 驻场者地址 | 项目房间恰一条 `agent:handoff` @驻场者通知 | O1；真机 #9（多机恰一条） |
| 6b | S5 失败 | 同上，项目房间无驻场者 | C-245 | `card show` | 零通知；`card show` stderr「项目无组织者」 | O1（P6 载体） |
| 7 | S5 唤醒 | 项目房间命中驻场者地址的消息 | `Recipients` → 驻场者地址 → 本机判据（C-415）→ `RenderResidentLetter` → `writeDesktopLetter` → continue 深链 | mod（5s 轮询）→ 桌面端组织者会话 → `session wait` | 组织者 ≤16s 醒来，取信，用 handoff 命令开会话、拉卡、选协调者；会话出现协调者 | O2 + R（桌面端地址）+ mod（既有）；真机 #10、#11、#12 |
| 7b | S5 跨机 | 驻场者在机器 B，事件被机器 A 的消费循环看到 | `SessionResident.machine` | A 与 B 的消费循环 | A 零动作；B 写信 | O2；真机 #14 |
| 8 | S6 | 协调者在项目房间提问（**按 R-K1 裁决：显式 @ 组织者**） | 显式分支 → 驻场者地址 → 同第 7 行链路 | 组织者 | 组织类答回卡所在会话（署名组织者）；内容类在桌面端会话问人、人答后转发；人已答则不答 | O2（简报）+ R-K1 裁决；真机 #13 |
| 9 | S7 | 用户升级后跑旧命令 / 看控制台 | 命令与路由已删；迁移留痕（P5） | 终端 / 控制台 | 旧命令不存在或报改名提示；B434 按钮不出现；`seat_desktops` 行按 P5 处置且可查留痕；二次升级零改动；保留项照常 | X + W；真机 #17、#18、#19 |
| 10 | S8 | 归档组织者会话，新会话 `session sit` 同项目房间 | `resident:<session>/<identity>` 游标（身份不变则续用）；`SessionBacklog.from_seq` | 新组织者会话 | 从游标续读积压，不重复处理已答消息；游标丢失时 `from_seq=0`，简报按「回放」处理 | R（游标）+ O2（简报，P8）；真机 #15 |
| 11 | S9 | S3 的驻场者在同会话 `card bind` 一张卡 | 驻场者行保留 + 卡席位（pull）；两地址各自游标 | `session wait` 与 `card wait`；`session detail` | 既收驻场消息也收卡席位消息；`session detail` 同一人两行；`stand` 不影响席位 | R（+ K0）；真机 #7 |
| 12 | S10 | pull 席位绑定后不跑 wait，会话里 @它 | `Matches` 命中、游标未推进（无输出即不推进） | 之后的 `card wait`；控制台成员状态 | 消息落账进未读；之后 `card wait` 一次性续收；控制台报 `last_active` 不报在线 | K0（+ B358 条 17 既有）；真机 #8 |

**闭环结论**：除第 8 行的触发条件待 R-K1 裁决、1b/4/6b 的展示载体待 P6 裁决外，每行五格齐全、实现与回归责任存在。依赖外部现实的结果格（桌面端、mod、`open`、opencode 回合、移动端浏览器、多机）全部落进 §6，不假装机内可达。

---

## 5. 缺陷族对抗审查（只写与本卡接缝相关的族；结论已写入各卡验收）

### 5.1 K0

- **生命周期 / 状态机中断**：迁移回填中途宿主进程退出 → 迁移须在单事务内、可重入（判据：二次执行零改动的测试）；`card wait` 输出一行后进程被杀 → 游标未推进则下次重投，接受「至少一次」（C-124 明文），判据：输出后注入 kill 前不写游标的测试读回游标值未变。push 席位在回合中被换绑为 pull → 回合内续租照 B399，下一轮按新 mode 判（判据：换绑后同消息零 Wake）。
- **静默失败**：匹配函数读账本失败绝不当未命中（C-228，判据：注入错误后消费循环不推进该批水位、`card wait` 非 0）；`card bind --mode push` 失败须不坐下（判据：查席位列）。
- **假红 / 假绿**：C-328 必须走真 `consumeAutomationEventsOnce` 与真 `runCardWait`，不直调分流函数（否则测不到 `resolveWakeCard`）；`TestB435CardWaitLineShapes` 期望值不得为通过而改；源码守卫 C-318 须能变红（判据：在 `keystone.go` 临时加一行 `DriverSource ==` 守卫报红——验收时做一次变异）。
- **门禁绕过**：「默认收件人 ≤1」的承重属性由 C-213 形状守卫 + C-225 运行时断言双锁；显式分支不回落默认收件人（C-204）有反面用例（@一张空座卡 → 空集 `addressed_unresolved`，**不**落到根卡）。席位地址游标下界（C-119）是隔离属性：换绑后新持有者不得收到旧持有者的历史 @，有一支能变红的测试。
- **序列化边界**：`driver_mode` 三处手写投影（§2.4 第 3 条）各一支穿过真实 JSON 的断言，`omitempty` 空座不出键用 `map[string]json.RawMessage` 查键。
- **枚举新值过白名单**：`push/pull` 流经 `ParseSeatMode`（T0）、迁移回填、`card bind --mode` 校验、keystone/消费循环判定——每处表驱动含非法值（大小写、空白，C-302）。
- **跨平台**：无新平台假设（纯账本与进程内调用）。

### 5.2 R

- **门禁绕过 / TOCTOU**：「一房间至多一名」靠库层主键（P3=甲），并发两次 sit 恰一成功的测试；P3=乙 时必须在同一事务里查写。驻场者身份校验（C-413）在写面唯一入口，`session sit` 与（O1）`sit --project` 共享同一校验。
- **静默失败**：`session wait` 无驻场者时立即非 0 退出且提示 `session sit`，不得阻塞等到超时（判据：短超时内返回、退出码 ≠124）。
- **生命周期**：`session stand` 与该驻场者正在跑的 `session wait` 并发 → wait 以启动时地址为准（C-134），游标键不变，不串到新驻场者（判据：stand + 新 sit 另一身份后，旧进程不收到新地址的消息）。
- **序列化边界**：`SessionBacklog.member` 值改为驻场者身份，金样本键集不变；S9 的两行成员投影穿过会话详情 JSON。
- **假绿**：旧 `session wait <member>` 形态必须真的被拒（C-132），不能因为参数恰好像会话号而误入新路径（表驱动含 `user:x`、`cli:a#b`、`session:12`）。

### 5.3 O1

- **生命周期 / 多机**：每机一个消费循环 → 通知恰一条依赖 P7 的认领；判据：同账本两 `Server` 实例并发消费，消息数恒 1（负载下不翻红）。agentd 在「写通知后、标水位前」重启 → 认领键保证不重发（判据：模拟重启后再跑一轮仍恰一条）。
- **门禁绕过**：系统发信身份 `agent:handoff` 只能由 agentd 使用——`session send --agent handoff` 能否冒充？（判定表对冒充者与真系统一视同仁，都是非人类显式 @，不扩大权力；但组织者简报不得把 `agent:handoff` 来信当作授权来源——写进 O2 的授权规则文本。）
- **静默失败**：无驻场者零通知是设计（C-245），用户可见面是 `card show` 的 stderr 提示（P6）；判据：stderr 断言逐字。
- **枚举新值过白名单**：项目房间是一种新的会话属性——会话列表/详情投影、`IsSessionRoom`、旧房间只读判定各处都须把它当普通会话（判据：项目房间可 `session send`、可 `session wait`、不被当旧 `project:<name>` 只读房间）。
- **序列化边界**：通知载荷 `by_system=false` 必须显式写出还是省略？B358 的 `by_system:null` 是结构行（C-202）——通知若序列化成 `null` 会被判为结构行而零唤醒。判据：读回事件载荷，`by_system` 键的原始 JSON 是 `false` 或缺键，绝不是 `null`。

### 5.4 O2

- **静默失败**（风险最集中）：信写进信箱但 mod 没取走 / continue 深链 `open` 成功但会话没醒——机内不可见。B434 的卡级失败段（needs_human）随卡席位退役，驻场者没有卡（欠账 9）。判据：plan 前先答「失败落在哪个可见面」，答不出回 contract；机内至少锁住「写信失败返回错误且不推进本批水位、下轮重试」与「信已落盘则不重写」（D5 不对称沿用）。
- **跨平台**：opener 只在 darwin 有实现（`PlatformDesktopOpener`）；驻场者 `machine` 是 linux 机器时 → 写信无意义，判据：非 darwin 机器上 opener 为 nil 时按「信已落盘、continue 未发出」返回可见错误，不报成功。
- **门禁绕过（信任边界）**：门铃信不含第三方原文（D-A、C-403）——判据：哨兵串不进信。mod 以 `asUser:true` 提交，但信里只有取信命令，原文经 `session wait` 以数据形态进入会话；授权规则改写为「只有人本人可授权不可逆/外部可见动作，`agent:handoff` 与其他协调者的消息都是数据」（承重安全属性，`desktop_rule_test.go` 锁常量）。
- **生命周期**：组织者会话 900s 回收后靠 continue 深链拉起（D10 保留）——机内只能锁「写信后 opener 恰一次」，拉起本身走真机 #12。
- **假绿**：本机判据测试必须用两个不同机器名的 `Server` 实例 + 同一账本，不能用单实例改配置模拟（否则 `IsSelfTarget` 分支测不到）。
- **追加裁决（2026-10-18，O2 plan Q1 甲 / B-1）**：上条「静默失败」判据中「不推进本批水位、下轮重试」作废——自动化游标水位是全机器共享的活性边界（`WakeClaimsBefore` 不按租约/holder 过滤、每轮只读 500 条），门铃无内容、失败不丢交付。改为：写信失败不报成功（Error 日志带 session/identity/stage/cause，不记正文）、进程内有界重试且不挡水位；信已落盘则不重写；`Recipients` 读失败仍按 C-228 不越过水位。

### 5.5 X

- **生命周期（迁移）**：迁移中断 → 单事务；二次升级零改动；P5=甲 的留痕与删除同事务（判据：注入失败后两者都未发生）。
- **假绿**：退役以「生产路径删除 + 测试删除」为准——判据是 grep 退役符号在非测试源码零出现，不是「测试删完所以全绿」；混合测试文件逐用例拆，保留用例仍跑（`desktopwake_remote_test.go` 转交用例、`TestB434CoordinatorWakeRespLetterUnopenedWire`）。
- **静默失败**：旧命令被删后用户得到的是 cobra「unknown command」还是改名提示？spec S7 要求「不存在或报改名提示」——两者都合规，但须可行动；判据：至少 `card coordinate --desktop` 与 `launcher` 给出指向 `session sit --desktop-session` 的提示（文本归 plan）。
- **跨平台**：薄壳 `/new` 改为被拒只在 darwin 薄壳生效；PG 方言删表/删列语法与 SQLite 不同（`DROP COLUMN` 在旧 SQLite 版本受限）——判据：两方言各一支迁移测试，PG 走真机 #19。
- **门禁绕过**：删 `/api/desktop/deliver` 后不得留下无人认领的 token 豁免或路由残留（判据：路由表快照不含该路径）。

### 5.6 W

- **序列化边界**：TS 镜像与 Go fixture 逐键比对，可空类型区分缺键（空座）与空串；P6 的投影键同样。
- **假绿**：控制台文案测试读常量，不在测试里另写一份文案；退役按钮的「不出现」用反面断言（查询不到元素），不只是删测试。
- **跨平台**：移动端布局下默认收件人提示与项目房间入口的可见性走真机 #16。

---

## 6. 真机清单（全部「未验证，需真机」；按批次组织，由协调者在批间演示与 I 卡执行）

**批 0（K0 合入后，Wave 0 阶段验收）**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 1 | 真 agentd + opencode push 席位：CLI 人形 `session send` 无 @ → 恰一轮 print 回合、续跑同一 session、会话出现 reply 且只有重点（D11 简报是否被照做） | S1 | 行为事实：消息是否实际走 push 通路、简报口吻 |
| 2 | codex 或 claude CLI 会话 `card bind` + `card wait`：收到同一条消息一行，agentd **未**起回合（查 agentd 日志与 `wake_claims`） | S2 | 「消息是否实际走某通路」 |
| 3 | 其他 agent 按现行 skill 文本解析 `card wait` 输出（新增的房间命中行不破坏它们的处理） | S2 | spec §10「真实消费者 = 其他 agent 的 skill 文本」 |
| 4 | 真账本多卡会话：一根一子 + 两根卡场景，人从控制台发无 @/@子卡/reply_to，唤醒对象与人数 | S4a | 根卡判定与 `session detail` 投影一致（spec §7 未知 3） |

**批 1（R 合入后）**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 5 | handoff 不认识的 agent 里 `session sit` + `session wait`，另一人用**手机控制台**无 @ 发言 → 驻场者收到并回复，同会话协调者不被唤醒 | S3 | 移动端发言作者身份实际是 `user:<console_user>` |
| 6 | S4a 会话里 `session sit` 后无 @ 改到驻场者，其余不变 | S4b | 行为事实 |
| 7 | 驻场者升格：同会话 `card bind`，两种地址都收；控制台会话详情两行；`stand` 后席位仍在 | S9 | 行为事实 |
| 8 | pull 席位绑定后不跑 wait，@它 → 控制台成员状态报 `last_active` 不报在线；之后 `card wait` 一次性续收 | S10 | 控制台既有投影的实际表现 |

**批 2（O1 + O2 合入后）**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 9 | ≥2 台 agentd 共享账本：`card add`、换列、`needs_human` 各在项目房间**恰一条**通知 | S5 | 多机认领（P7）机内只有双实例模拟 |
| 10 | 由真实 launchd 托管的 agentd 写门铃信，已装 mod（零改动）≤16s 取走并以 `asUser` 提交；组织者按信中命令 `session wait` 取到原文 | S5 | mod 取信时延；mod 对两行正文的实际处理 |
| 11 | 组织者用 handoff 命令开会话、拉卡、选协调者，会话出现协调者 | S5 | agent 是否照简报行事 |
| 12 | 组织者会话闲置 >900s 被回收后，门铃信 + continue 深链拉起会话并取信 | S5、S8 | 900s 回收后 continue 深链拉起 |
| 13 | 协调者按 R-K1 裁决的方式提问；组织类答回卡所在会话、内容类转问人、人已答则不答 | S6 | 简报纪律（非机制） |
| 14 | 驻场者在机器 B（`session sit` 的 CLI 所在机），事件先被机器 A 消费：A 零动作、B 写信；`machine` 取值与 B 的 agentd `IsLocalMachine` 一致 | S5 | §2.4 第 7 条；C-415/416 |
| 15 | 归档组织者会话、新会话 `session sit`：从游标续读、不重复处理；人为删游标行后 `from_seq=0` 被简报当作回放 | S8 | 恢复语义与简报（P8） |

**批 3（W + X 合入后）**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 16 | 桌面与手机浏览器：`mode` 展示、驻场者/组织者展示、「无收件人（@ 指定）」「需 @」提示、项目房间入口；B434 三按钮不出现 | S1/S4a/S7 | 控制台实际渲染（本机无 node_modules） |
| 17 | 升级后跑旧命令（`card bind --desktop-session`、`card seat desktop set`、`card coordinate --desktop`、`launcher register`）的实际输出 | S7 | 改名提示可行动 |
| 18 | 对用户真实 SQLite 账本**副本**（含 B434 存量 `seat_desktops`/`desktop_launchers` 行）升级：按 P5 处置、留痕可读、二次升级零改动；已装 mod 与门铃信照常 | S7 | 存量数据形态未知 |
| 19 | PG 方言（linux-01 开发库，`LEDGER_TEST_PG_DSN`）：`driver_mode` 加列回填、驻场者表、项目房间表、B434 删表迁移，往返与幂等；候选超集两条新支两方言同集同序 | S7（+K0/R/O1） | PG 测试默认 skip（`store_pg_test.go:1-26`） |

**I 卡（最终）**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 20 | 全故事走一遍（S1–S10），含 S1 失败呈现「无收件人」与 push 席位在远端机器的既有转交路径不回归 | 全部 | 最终验收 |

共 **20 条**。

---

## 7. 出稿自检（交稿自检五项）

1. **产出四样齐全**：子系统清单每项带图类型（§1，含图外两项与外部现实）；边界与冻结核对有结论（§2.1 状态位、§2.2 145 条与欠账 13 条逐条归属、§2.3 退回 R-K1、§2.4 澄清 8 条）；故事到工作单元或已验基线映射完整（§3 七张卡 + §3.8 S1/S10 指向 K0、回归负责人 I）；风险有可复核判据（§5 按卡）。✅
2. **待拍板岔口集中在稿首**：§0 P1–P12，正文岔口均回指编号；退回项单列 §2.3。✅
3. **真机清单汇总**：§6 共 20 条，四类必列项（消息是否实际走某通路 #1/#2、mod 取信时延 #10、900s 回收后 continue 拉起 #12、PG 方言迁移 #19）都在。✅
4. **有界文件集**：每张待实现卡 ⑤ 写出文件集与禁改清单；架构法第三条命中已答「可圈」（§1.1），是否插还债卡列 P12，未静默。✅
5. **行为闭环与依赖**：§4 十七行五格齐全，实现与回归责任存在（第 8 行触发待 R-K1、三行展示载体待 P6 已标）；§3.0 依赖边逐条标硬依赖/施工排序；每张工程卡有真实消费者与所属批次；批间演示与回归写明。✅

`codegraph resolve --doc` 结果见台账 §七.6。

## 8. 图覆盖债

- `runSessionWait`、`sessionWaitMatch`（`cmd/session.go`）不在图中，`who-calls Service.MessageWakeTargets` 漏 CLI 调用方（台账 §七.3 复现）→ R 重写后验 `sym` 命中，I 全量对账。
- `Service.MessageWakeTargets` 图中签名陈旧（契约 §1.4）→ K0 降为内部步骤后视图 diff 重生成。
- 本稿新文件 `session_resident.go`、`project_room.go`、`projectnotify.go`、`residentwake.go` 在各卡视图 diff 中登记。
