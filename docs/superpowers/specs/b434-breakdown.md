# B434 拆解稿：Claude 桌面端会话当协调者——深链创建、信箱唤醒、会话内坐席

**状态：已拍板（2026-10-16）**（P1–P6、P8、P10 协调者裁；P7=d、P9=丁 用户裁，契约 r7 @ `30979524` 已补签。修订轨迹：r0 出稿；r1 补 G1 缺陷族、回写协调者拍板、真机清单分三批；r2 回写 P7/P9 裁决并同步契约 r6/r7 新条目）
**卡：** B434（L3 重档；contract → **breakdown** → 子卡扇出 → 父卡 integrate）
**上游 spec：** `docs/superpowers/specs/b434.md` —— 头部「**状态**：**已批准**（r2；用户 2026-10-16 …）」实读在位，r2.1/r2.2 勘误在位
**冻结 contract：** `docs/superpowers/specs/b434-contract.md`（r0 出稿时为 r5，§4 条 1–222；r2 同步到 r7，§4 条 1–280）—— **头部「冻结状态」行只写到 r4，缺 r5，且理由仍写「本机无 Go/Node 工具链」**（r3 起已有）；状态位失真，见 §2.1，须协调者回写后再扇出
**有效基线：** `main` @ `e020380a`；工作分支 `cards/B434-charter-1` @ `580b20c1`（含 Ticket 0 与契约 r5）
**图依据：** `codegraph/best.json`（`domains` 中 `parent` 空 = 顶层子系统，`type` 即逻辑/边界）；本轮 `codegraph check` exit 0（fails 0 / warns 28）、`validate` exit 0
**台账：** `docs/superpowers/specs/b434-ledger.md`「breakdown 节点」小节（本稿每条读数的原始输出在那里）
**角色边界：** 本文**全部是提案**。不写实现代码、不建卡、不派发、不调 `handoff card`。符号锚一律用裸名 `file#Symbol`（图内 `Server.X` 形式多数 `vanished`，见台账）。

---

## 0. 待拍板岔口清单（集中；拍板者按本表逐条裁决；r1 已回写裁决列）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向（不替拍） | 裁决（2026-10-16） |
| --- | --- | --- | --- | --- |
| **P1** | **子卡切法** | **甲：按子系统切、同子系统内按故事排序**（§3：L 账本 / C 命令面 / G1·G2·G3 网关按 S1·S2·S3 / W 控制台 / D 薄壳 / M mod 与安装；V、I 归协调者）。好处：每张卡只落一个子系统（M 例外，见 §1），共享文件（`CoordinatorPanel.tsx`、`internal/ledger/seat_desktop.go`、`cmd/*`）各只有一个写者，并行冲突面只剩 G1∥G2 的两个文件且函数体已由 Ticket 0 分好。代价：故事级真机验收要等多张卡齐。**乙：按 spec 三段故事切 S1/S2/S3**（spec「实现决定」候选）。好处：每张卡自带一条真机故事。代价：三卡共写 `desktopwake.go`、`coordapi.go`、`seat_desktop.go`、DDL，S1 与 S3 共写 `CoordinatorPanel.tsx`；且**S3 实际依赖 S1**（`renderLauncherLetter` = S1 的 `desktopLaunchMaterial` + 模板，契约 §3.9.1），spec 写的「S3 只依赖 S2」不成立；S2 的手填路径 `card seat desktop set` 契约归在 S1（欠账 9），「S2 不依赖 S1」也只在账本层先落时成立。 | **甲**。故事级验收改由 §4 行为闭环 + §6 真机清单按故事组织，协调者在 I 卡按故事逐条走。 | **甲**（协调者）。按故事切会让 `desktopwake.go`/`coordapi.go` 各有三个写者；S3 实际依赖 S1。补两个中途真机检查点（§6 分批）。 |
| **P2** | **直通竖切 `TestB434DesktopWakeVerticalSlice` 的时序**（契约 §9.3 欠账，归协调者，不进并行子卡） | **甲：V 卡在 L 之后、G2 之前**，以最小真实实现打通主缝（`resolveWakeCard` 分支 → `wakeCoordinatorRoundID` 分支 → `wakeDesktopSeat` 本机分支 → `desktopLetter` 单事件 → `writeDesktopLetter` → `claudeContinueURL` → opener spy），G2 在其上补全失败路径、跨机与 needs_human。符合重档「竖切先于扇出」的本意；代价是协调者（或 contract 执行者）要写 G2 约四成的生产代码。**乙：V 在 G2 之后作集成测试**，G2 写全部生产代码，V 只加测试。代价：主缝在 G2 交付前没有端到端证据，G2 自证自验。 | **甲**（同批顺带清欠账 5 DTO fixture）。 | **甲**（协调者）。V 在 L 之后、G2 之前，由协调者执行，顺带清 DTO fixture。 |
| **P3** | **「plan 前必须钉」三条外部事实 + Wave 0 launchd 一条的归属** | 本稿已取得三条读数（台账）：§10.7 Wails `OpenURL` darwin = `exec.Command("open", url).Start()`，不过滤 scheme、不回报 `open` 退出码；§10.6 `CLAUDE_CODE_PLUGIN_DIRS` 用平台路径列表分隔符（darwin `:`），只读进程环境或 `~/.claude/settings.json` 的 `env`；§10.8 mod 有 `$.env.get("HOME")`。**甲：协调者在扇出前认定这三条读数并回写契约 §10 销账**（D/M 卡直接按读数做 plan）；launchd 一条由协调者在**本机**真机实测，放在 **G2 派发前**（结论若为「到不了 GUI」，G2 的 opener 设计要改，属退回 contract）。**乙：下放到 D/M/G2 各自 plan 前步**。代价：远端 executor 没有 Claude 桌面端，launchd 与 PLUGIN_DIRS 真机项它们验不了。 | **甲**。 | **甲**（协调者）。三条外部读数由协调者认定、回写契约 §10 销账；launchd 一条已于 18:43 真机验通（台账）。 |
| **P4** | **架构法第三条信号命中：是否插竖切还债卡** | 命中：`internal/agentd` 64 个非测试源文件且无子包（信号 2）、`cmd` 59 个且无子包（信号 2）、`cmd/card*` 前缀 12 个（信号 1，C 卡再加 `card_seat_desktop.go` 成 13）。未命中信号 3（agentd 19,037 行 < 2 万）。**甲：不插**——每张触及卡都能写出显式文件清单（§3），桌面端新代码已集中在一个新文件 `internal/agentd/desktopwake.go`，`desktop*` 前缀只有 2 个；契约 §2.1 已冻结户口。**乙：插一张还债卡**把 `desktopwake.go` 抽成 `internal/agentd/desktopwake/` 子包。代价：桌面端函数全是 `Server` 方法、要读 `s.ledger`/`s.rooms`/`s.mgr`，抽包要新造接口（架构法第四条「单实现 interface 降档」会反咬），且改契约户口，等于退回 contract。 | **甲**；把命中与回答写进 G2 验收的「图对账」备注，留给后续卡按绞杀式还债。 | **甲**（协调者）。不插还债卡；命中与回答写进 G2 图对账备注。 |
| **P5** | **M 卡（mod 正式版与安装）的执行位置** | **甲：本机执行**（协调者本机 subagent 或 handoff 本机执行器）。理由：spec 测试缝 6 用 `claude plugin test`，本机已确认有 Claude Code 2.1.295 的 bundled plugin-authoring，远端机器是否有同版本 CLI 未验；mod 还要在本机真机挂载。**乙：远端写代码 + 跑 Go 侧安装测试**，`claude plugin test` 用例与挂载全部转真机清单。代价：mod 的逻辑用例（条 112–126）退化成真机项，失去机内闭环。 | **甲**。 | **甲**（协调者）。M 卡本机执行。 |
| **P6** | **skill 文本（「占座」「主 agent 回路」、CLAUDE.md 常设授权一行）归属** | **甲：并入 C**（命令名由 C 落地，文本描述的就是 C 的命令；契约 §3.10 已冻结命令名）。**乙：独立小卡 K**，排在 C 与 G3 之后。**丙：协调者在 I 卡写**。取舍：甲最少卡数；乙让文本对齐最终行为；丙让文本跟真机验收一起改。 | **甲**；G3 若改了文案相关行为，I 卡复核一遍。 | **甲**（协调者）。skill 文本并入 C；G3 改了文案相关行为则 I 卡复核。 |
| **P7** | **R-B1：spec 故事 6 没有触发者（需用户裁决，因为触碰 P4）** | 契约 §3.12（P4=推荐）规定：项目未登记拉起器时**不渲染**「让拉起器代开」。这样 UI 上没有任何入口会调 `POST …/desktop-launch`，冻结的「409 + `MarkNeedsHuman(无拉起器…)`」路径没有触发者，故事 6「卡上亮起『需要你：无拉起器』」就不会发生。**a：改 spec 故事 6** 为「面板显示『本项目未登记拉起器』提示」（新文案常量 + 新条目，退回 contract）。**b：材料端点 200 时总是渲染「让拉起器代开」**，未登记时点击 → 409 + needs_human（原样用冻结语义，只改条 203/204/201 的显示条件；推翻 P4 的一半，需用户）。**c：保持不变**，故事 6 只能经 API 触发，spec 记勘误。**d（审阅提出）：不动 P4 显示条件**，给主 agent 一条 CLI（如 `handoff card coordinate <卡> --desktop` 或 `handoff launcher open <卡>`）去调 `POST …/desktop-launch`；无拉起器 → 409 + needs_human，故事 6 的触发者变成主 agent（或人在终端）。代价：新增一条 CLI 契约面进 r6，C 卡加对应条目。 | 原倾向 **b**（与 spec 故事 5/6 逐字吻合，只改显示条件）；d 不推翻用户的 P4，也可接受。裁决前 W 与 G3 都可开工，只有条 203/204（b）或 C 的新命令（d）待定。 | **d**（用户）。不动 P4 显示条件；故事 5 本就落在主 agent 回路，故事 6 的触发者交给主 agent 的 `handoff card coordinate <卡> --desktop`。契约 r7 §4.16 条 252–262。 |
| **P8** | **R-B2：「用 Claude Code 坐下」按钮的 UI 行为没有原子条目** | 契约 §3.12 正文写了：409 → 禁用并逐字显示服务端 `error`；点击 → `buildClaudeNewSessionURL` → 薄壳 `requestOpenDeepLink` / 浏览器 `<a href>`；编码拒绝或无桥 → 展开「复制简报」；首次提示信任。但 §4 只有拉起器按钮（条 203–210）和文案常量（条 194–199），没有这几条。**甲：退回 contract 做 r6 补条**（不新增接缝，只把 §3.12 正文拆成原子条）。**乙：不补条**，W 卡验收直接引 §3.12 正文。 | **甲**（便宜，且让 review 的冻结物触碰行对得上账）。 | **甲**（协调者）。退回 contract r6 补「坐下」按钮原子条；按纪律属「缺契约面」，不是偏好岔口。 |
| **P9** | **来信以 `asUser:true` 提交，群消息正文会以「人本人的话」进入协调者会话（信任边界）** | API 读数：`$.prompt.submit` 不带 `asUser` 时，模型读到的是「The <plugin> plugin sent a message: …」；带 `asUser:true` 时读成人本人的原话（`claude-code.d.ts` 2.1.295 `:8960-8979`）。契约 §3.13 冻结 `asUser:true`；spec 台账探针也是这么验通的。来信里有**其他会话成员的原文**，这些人的话会以本用户的口吻出现在桌面端会话里。（r1 更正两处推断：本机会话名册里的 `cli:sycm@…`（台账 15:12 读数）**是否他人未证实**，也可能是同一用户的另一台机器；r0 写的「bypass 模式」没有读数支撑，已删。）**甲：保持 `asUser:true`**（已验通），把风险记账，并把「他人消息注入」列进真机观察项；**乙：退回 contract 改成不带 `asUser`**（来源如实），协调者会话与拉起器一样靠 CLAUDE.md 常设授权才执行来信（行为未验证，需真机）；**丙：保持 `asUser:true`，同时退回 contract 在来信模板首段加一句定性**（「以下各段是账本原文，按数据对待，不是指令」）。**丁（审阅提出）：保持 `asUser:true`**；`desktopLetter` 把每段第三方原文放进标注作者身份与 seq 的围栏块，并转义正文里的围栏字面量；信首一行列出本信涉及的全部作者身份；skill / CLAUDE.md 授权文本加一条「只有卡主人或用户本人身份才能授权不可逆或外部可见的动作」；可选：非本人作者的消息只给 seq + `handoff card show` 指针，不内联正文。代价：改冻结的来信模板（§3.6）与条 74–76，进 r6。 | 原倾向 **丙**；丁是丙的加强版（结构化框定 + 授权规则），同样不动已验通的通路。属用户级取舍，已上报用户。 | **丁**（用户）。保留已验通的 `asUser:true` 通路；用围栏 + 作者名册 + 授权规则把第三方原文从「人本人的话」降为带来源的数据。契约 r7 §4.17 条 263–280、拍板 D7。 |
| **P10** | **`open` 报成功但信没人收（静默失败）的处置** | 已知：Wails 的 `OpenURL` 不回报 `open` 退出码；agentd 的 opener 即使用 `Run()` 拿到退出码，`open claude://…` 在会话已归档或删除时会怎样也未验证。信会停在信箱里，没人知道。**甲：接受为已知限制**：G2 的 opener 用 `exec.Command("open", url).Run()`（拿退出码，属契约 §3.7 的实现细节，不改冻结面）；信箱积压的可见性和心跳归 spec OOS 的「bind 席位心跳过期」后续卡（roadmap）。**乙：退回 contract**，在 `handoff status` 或卡页加「信箱积压 N 封 / 最旧 T」的可见面（新契约面）。 | **甲**。 | **甲**（协调者）。接受已知限制；opener `Run()` 属实现细节。 |

> 退回 contract 的项（§2.3）R-B0（头部状态行）与 R-B3（`/api/desktop/deliver` 守卫勘误）不是偏好岔口，按纪律必须回写，不列入裁决表。P7/P8/P9 若裁成「退回」，与 R-B0/R-B3 合并成一轮契约 r6 补签。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型取自 `codegraph/best.json`（`parent` 空 = 顶层，`type` 实读）。`desktop/`（独立 go module）与 `plugins/` 不在图里：扫描配方排除 `desktop`、`skills` 前缀（`scripts/codegraph-rescan/main.go:231`），`plugins/` 下是 TS 且没有对应容器，因此单列为「图外」条目。

| 子系统 / 载体 | 图类型 | 本卡角色 | 有界文件集（路径规则） | 派卡资格四条 |
| --- | --- | --- | --- | --- |
| `d_ledger` 卡片账本 | 逻辑型 | 实现：`seat_desktops`、`desktop_launchers` 两表与 DDL（两方言），6 个 Store 方法，`RebindSeat`/`clearSeatTx` 联动删行，`EvSeatDesktopSet` 落账 | `internal/ledger/{seat_desktop.go,binding.go,store.go}` 及其 `_test.go`、`ddl_parity_test.go`；另含 `internal/proto/desktop_coordinator.go#ValidateDesktopSessionID` 函数体（唯一跨包触点） | ①可圈；②契约面可枚举：§3.3/§3.4 六个方法 + 一个事件 + 两张表；③只被 C/G 依赖，自身只依赖 `d_protocol`（既有边）；④逻辑型（真 SQLite 测试闭环）。**通过** |
| `d_cli` 命令面 | 逻辑型 | 实现：`card bind --desktop-session/--machine/--via`、`card seat desktop set`、`launcher register/show/unregister`、`card needs` actor 改席位优先、`handoff status` 拉起器段；skill 文本（P6=甲时） | `cmd/{card_driver.go,card_bearing.go,card_records.go,status.go}`、新 `cmd/card_seat_desktop.go`、新 `cmd/launcher.go`、对应 `_test.go`、`skills/handoff/SKILL.md` | ①可圈；②§3.10 五条命令面；③`d_cli → d_ledger` 既有边，不调 agentd 新路由；④逻辑型。**通过**（信号 1/2 命中见 P4） |
| `d_gateway` 控制门面 | 边界型（HTTP 面 + 本机出站 I/O：文件信箱、`open`） | 实现：材料端点与简报（G1）；信箱、continue、唤醒分流、失败段、needs_human 唤醒（G2）；拉起器代开与 `/api/desktop/deliver`（G3） | `internal/agentd/{desktopwake.go,coordapi.go,wakeconsumer.go,scheddrain.go,server.go}` + 对应测试（`desktopwake_*_test.go` 新建为主）；G1/G2/G3 各自子集见 §3 | ①按三张卡切开后各自可圈；②§3.5–§3.9.1 签名 + 三条路由；③依赖 `d_ledger`/`d_protocol`/`d_transport`（既有边），对 `d_keystone` 零新增调用（条 162）；④边界型：机内用临时 HOME + opener spy + 假转交端验契约形状，`open` 真实行为与 launchd 走真机。**通过** |
| `d_protocol` 协议契约 | 逻辑型 | 只读消费（Ticket 0 已落字面量与 DTO）；V 卡补 DTO fixture（欠账 5） | `internal/proto/contract_fixture_test.go` + `internal/proto/testdata/`（V）；`ValidateDesktopSessionID` 函数体（L） | ①可圈；②已冻结；③无新边；④逻辑型。不单独派卡 |
| `d_web` → `d_web_cards` / `d_web_shell` / `d_web_contract` | 逻辑型（父域 logic；深链真打开是外部现实） | 实现：`buildClaudeNewSessionURL`、`requestOpenDeepLink`、面板三按钮与复制兜底；`contract.test.ts` 读 fixture 归 V | `web/src/app/lib/{claudeDeepLink.ts,desktopShell.ts}`、`web/src/app/cards/{CoordinatorPanel.tsx,desktopSeatCopy.ts}`、`web/src/api/scheduling.ts`（只用不改 DTO）、对应 `*.test.ts(x)` | ①可圈；②§3.12；③只消费 `/api/cards/{id}/coordinator/desktop-launch`（G1/G3 提供，DTO 已冻结，可先对假 API 开发）；④逻辑型，`claude://` 真打开归真机。**通过** |
| `desktop/` 薄壳（图外，独立 go module） | 边界型（Wails webview ↔ macOS `open`） | 实现：`HandleExternalDeepLinkMessage`/`validateClaudeDeepLink` 与 `desktop/main.go` 第三处理器接线 | `desktop/internal/shell/{external_deeplink.go,external_deeplink_test.go}`、`desktop/main.go`（RawMessageHandler 一处） | ①可圈；②§3.11 一个前缀 + 一个处理器；③与主模块零 import；④边界型。**通过** |
| `plugins/claude-desktop-wake/`（图外，新交付物）+ `d_maintenance`（`k_skill_*`）+ `d_cli` 组装点两文件 | 边界型（对面是 Claude Code mod 运行时与 `~/.claude/settings.json`） | 实现：mod 正式版；`InstallClaudeMod`/`ClaudeModStatus`；`main.go` embed；`cmd/skill.go` install/status 各加一行 | `plugins/claude-desktop-wake/**`、新 `internal/skill/claudemod.go` + 测试、`main.go`（embed 一处）、`cmd/skill.go`（两处追加） | ①可圈；②§3.13；③mod 零 import 主模块（条 163），`d_cli → d_maintenance` 既有边；④边界型。**通过**——注意 M 跨 `d_maintenance` 与 `d_cli` 的**组装点**两文件（`main.go`、`cmd/skill.go`），与 C 的文件集不重叠 |
| `d_collab` 协作房间 | 逻辑型 | 只读消费：`MessageReference`、`ListSessions(seat).Unread`、`MessageWakeTargets` | 零改动 | 不派卡 |
| `d_keystone` | 逻辑型 | 只读消费：`Service.Decide`（attach 暂缓判定）、`WakeTaskTerminal` 种类复用；`Service.Wake` **不变** | 零改动 | 不派卡（条 91/92/162 锁「零新增调用」） |
| `d_orchestration` / `d_workspace` | 逻辑型 / 边界型 | 只读消费：`Manager.ListProjectLocations`、`workspace.ResolveProject` | 零改动 | 不派卡 |
| `d_transport` | 边界型 | 只读消费：`clientForTarget` → `pool.For`、`MarkForwarded` | 零改动 | 不派卡 |
| Claude 桌面端 / `open` / launchd（外部现实） | 边界型 | 深链与 mod 的对面 | 不可圈为仓内文件 | 不派卡；行为全部进 §6 |

**不触及：** `d_orchestration` 内部、`d_execution*`、`d_sessions`、`d_policy`、`d_scheduling`（`AdmitSeatCarrier` 在桌面端分支之后，不经过）、`d_web_command`/`d_web_workbench`/`d_web_admin`。

### 1.1 竖切债核对（架构法第三条，必答）

- `internal/agentd`：64 个非测试源文件，无子包（信号 2 命中）；19,037 行（信号 3 未命中）。**能圈出有界文件集吗？能。**G1/G2/G3 各自是 5 个文件里的显式子集，桌面端新逻辑全在 Ticket 0 建好的 `desktopwake.go` 一个文件里，其余是既有函数里的分支改接（契约 §3.8 逐处列出）。不插还债卡（P4）。
- `cmd`：59 个非测试源文件，无子包（信号 2）；`card*` 前缀 12 个（信号 1）。**能圈出吗？能。**C 卡改 4 个既有文件、新建 2 个文件，都按命令一文件的既有惯例。不插。
- `internal/ledger`：33 个（< 40），不触发。
- 结论写进 G2 与 C 的验收备注：「架构法第三条命中，已答：可圈；未插还债卡（P4 裁决）」。

### 1.2 图覆盖债

见 §8。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- **spec**：`b434.md` 头部「**状态**：**已批准**（r2；用户 2026-10-16 …）」在位；r2.1（信箱文件名）、r2.2（needs_human 唤醒桌面端席位）勘误都在头部修订轨迹里。✅
- **contract**：`b434-contract.md` 头部「**上游状态：已批准**」✅；「**冻结状态**」行**只写到 r4**（r4、r2、r1 三段），**r5 补签（2026-10-16，`DesktopDeliverReq` 改带 `card`、`renderLauncherLetter`）没写进头部**，而且尾句「分支视图 diff 与直通竖切因本机无 Go/Node 工具链未落」从 r3 起就不对了（r3 已装工具链，§9.2 记了编译证据；未落的真正原因是欠账 §10.3/§10.4）。**→ R-B0**：协调者在扇出前回写头部冻结状态行（加 r5，并把未落原因改为「列欠账 §10.3/§10.4」）。
- **图门禁（本轮新鲜）**：`codegraph check` exit 0（fails 0 / warns 28 / crossDomainEdges 1261）；`validate` exit 0。

### 2.2 契约 §4 原子冻结清单逐条对照（222 条 → 归属）

「T0」= Ticket 0 已写并在 r3–r5 跑过 PASS 的条目，子卡只需保持绿。

| 契约 §4 分组 | 条目 | 归属 / 越界结论 |
| --- | --- | --- |
| §4.1 拉起材料 | 1–14 | **G1**。不越界。条 13（不套 `withCoordinator`）与条 14（GET 零写）在 G1 验收里各有一支反面断言。 |
| §4.2 深链编码 | 15–23 | **W**。不越界。 |
| | 24–27 | T0（TS 24/25、Go 26/27）。W/G1 保持绿。 |
| §4.3 薄壳通道 | 28 | T0（desktop）。 |
| | 29 | T0（web）。 |
| | 30–41 | **D**。不越界。条 41（日志不含完整 URL）落 D 的反面断言。 |
| §4.4 席位桌面端身份 | 42–48、60–61、64 | **C**（CLI 面）。 |
| | 49–59、62–63、65 | **L**（Store 写面、校验函数、生命周期联动）。 |
| | 66 | **G2**（`automationWakeEvents` 对 `seat_desktop_set` 返回空；现状 default 分支已如此，G2 加一支锁定测试）。 |
| §4.5 唤醒信箱 | 67–70、83–84 | **V**（主缝最小实现）+ **G2**（补全并发与非法 id）。P2=乙时全归 G2。 |
| | 71–82 | **G2**（来信渲染：命中条、引用条、未读数、工单、失败不写信、不落账、不推游标）。 |
| §4.6 唤醒分流与失败路径 | 85、89–92 | **V**（快乐路径）+ **G2**。 |
| | 86–88、93–111、165–174 | **G2**。 |
| | 175–176 | T0。 |
| §4.7 mod | 112–127 | **M**。条 127（mod 字面量与 Go 侧逐字一致）是跨语言对侧执法，M 验收里用「Go 测试读 `register.ts` 源码比对字面量」或 mod 用例回放 Go 侧导出的 golden 二选一，归 M 的 plan。 |
| §4.8 mod 安装 | 128–138 | **M**。条 129/130 的「合并」按 §10.6 读数（darwin `:` 分隔）实现。 |
| §4.9 拉起器 | 139–143、159–161 | **C**（CLI 面，Store 由 L 提供）。 |
| | 144–158 | **G3**。 |
| §4.10 依赖与图 | 162 | **G1/G2/G3** 各自验收（每卡 `git diff` 中 `internal/agentd` 新增 `keystone.` 选择器调用为 0）。 |
| | 163 | **M**。 |
| | 164 | **图对账列**（协调者；欠账 §10.3），不派子卡。 |
| §4.11 needs_human 唤醒 | 177–179、182–188、191–193 | **G2**。 |
| | 180–181 | T0。 |
| | 189–190 | **C**。 |
| §4.12 拉起器代开入口 | 194–199 | T0。 |
| | 200–202 | **G1**（材料端点填 `launcher`；条 202 读拉起器失败仍 200，在 L 落地前也能用 Ticket 0 空壳返回的错误验证）。 |
| | 203–210 | **W**（P7=d，显示条件不变）。 |
| §4.13 拉起器异机解析 | 211–212 | T0。 |
| | 213–222 | **G3**。 |
| §4.14 坐下按钮（r6） | 223–242 | **W**。 |
| §4.15 deliver 账本守卫（r6） | 243–251 | **G3**。 |
| §4.16 主 agent 代开 CLI（r7） | 252–260、262 | **C**（`card coordinate --desktop`、client 方法、SKILL.md 一句；契约欠账 18 记 CLI flag 与 client 方法尚无空壳）。 |
| | 261 | **G3**（代开端点空座前置 409，先于拉起器查找）。 |
| §4.17 来信围栏与授权规则（r7） | 263–275、280 | **V** 实现（契约欠账 18：`desktopLetter` 组装 sections 与 `renderDesktopLetter`，落码即激活三支金样本与 golden）；**G2** 扩展来信时保持全绿。 |
| | 276 | T0（`internal/skill/desktop_rule.go` 常量）。 |
| | 277–278 | **C**（SKILL.md「占座」与 CLAUDE.md 片段含授权规则逐字）。 |
| | 279 | **M**（mod 用例断言 `prompt.submit` 实参 `asUser: true`）。 |

**契约欠账逐条归属**：§10.3 分支视图 diff → 图对账列（协调者）；§10.4 直通竖切 → **V**；§10.5 DTO fixture → **V**；§10.6/§10.7/§10.8 → 本稿已给读数，协调者认定后销账（P3）；§10.9–§10.11 → 按上表分到各卡；§10.13 launchd → **已验（2026-10-16 18:43，台账），销账**；§10.14 → G2；§10.15 → C；§10.16 → G1 + W。**§11 移交 plan 附区**：11.1 → G1（`resolveCoordWorkdir` 改返回 `(string, error)`，单一解析点）；11.2 → C（`card bind` 在 hostname 不是任何已知 target 名时给提示）+ C 的 status 段告警；11.3、11.4 → G2；11.5 → G1（可选，不在冻结面）。**没有一条被静默带走。**

### 2.3 需退回 contract（不许边拆边加）

| 编号 | 内容 | 性质 | 处置建议 |
| --- | --- | --- | --- |
| **R-B0** | 契约头部「冻结状态」行缺 r5，未落原因写错（§2.1） | 状态位失真 | 协调者回写头部，扇出前完成（不涉冻结物）。 |
| **R-B1** | 故事 6 没有触发者（P7） | spec 触发条件与 P4 显示条件冲突 | 按 P7 裁决：a/b 需 r6 补签（b 只改条 201/203/204 的显示条件），c 只做 spec 勘误。 |
| **R-B2** | 「用 Claude Code 坐下」按钮的 UI 行为没有原子条目（P8） | 冻结物缺条（接缝已有） | P8=甲时，r6 把 §3.12 正文拆成原子条（建议条 223 起：409 禁用 + 逐字原因；点击在薄壳内恰一次 `requestOpenDeepLink`；浏览器内渲染 `<a href>`；编码拒绝 → 复制兜底；无桥 → 复制兜底展开；信任提示可见）。 |
| **R-B3** | `/api/desktop/deliver` 的守卫写「不需要账本」，和 r5 的「收件机按卡读账本渲染」矛盾；卡号不存在时回什么码也没写 | 冻结物内部矛盾 | **必须回写**（不是偏好问题）：守卫改为 token 中间件 + `withLedger`（未配置账本 → 503，沿用 `internal/agentd/ledgerapi.go#withLedger` 文本）；卡不存在 → 404（`ledgerErr`）；新增两条原子条。G3 在此之前可以先做其余部分。 |
| （条件性）P9=乙/丙 | 改 `asUser` 或来信模板首段 | 冻结的模板文本或 prose | 按 P9 裁决。 |

**r2 状态**：R-B0、R-B2、R-B3 已由契约 r6（`35334dbb`）回写（条 223–251）；R-B1 随 P7=d 由契约 r7（`30979524`）补签（条 252–262）；P9=丁 由 r7 补签（条 263–280）。本节各项均已关闭。

**没有发现「spec 承诺了行为，冻结物里却没有载体」的新接缝**（R-B1 缺的是触发者，不是载体）。

### 2.4 边界澄清（不退回；按纪律回写契约修订记录一行）

1. **Wails `OpenURL` 处理 `claude://`（销 §10.7）**：v3.0.0-beta.8 在 darwin 上是 `exec.Command("open", url).Start()`，不过滤 scheme。§3.11「若不处理则改 exec」的条件不成立，D 按契约用 `app.Browser.OpenURL` 即可。副作用：薄壳侧拿不到 `open` 的退出码 → D 的 opener 只会因进程起不来而失败，归 P10/§5。→ 归 D。
2. **`CLAUDE_CODE_PLUGIN_DIRS`（销 §10.6）**：分隔符是平台路径列表分隔符（Go 里是 `filepath.ListSeparator`，darwin 为 `:`），允许 `~`，只认进程环境或 `~/.claude/settings.json` 的 `env`（项目 settings 不算）。`InstallClaudeMod` 按 `filepath.SplitList` 拆分、去重、追加，不删其它项（条 130）。→ 归 M。
3. **mod 取 HOME（销 §10.8）**：用 `$.env.get("HOME")`，不需要安装期占位符替换；`ClaudeModStatus` 直接对内嵌内容做逐文件哈希；契约 §6 第 3 行「mod 源码 / 安装期替换」改为「mod 源码经 `$.env.get("HOME")` 拼 `proto.DesktopWakeDirRel` 的字面量」。→ 归 M。
4. **桌面端分支不占协调者小队名额**：`internal/agentd/scheddrain.go#wakeCoordinatorRoundID` 的 bind 早退在 `resolveCoordinatorSquad`/`AdmitSeatCarrier` 之前，桌面端分支插在 bind 位置，所以天然不经过准入。G2 必须保持这个顺序（加一支断言：桌面端席位唤醒时 `AdmitSeatCarrier` 调用 0 次）。→ 归 G2。
5. **故事 9 的载体**：`EvSeatDesktopSet` 载荷 `{identity, desktop_session, machine, via}`，卡 timeline（`web/src/app/cards/CardDrawer.tsx#eventSummary` 兜底 `JSON.stringify(payload)`、`handoff card show` 原样列事件）可读出 `via=page|launcher` 和时间。会话 timeline 的结构事件白名单（`collab/sessions.go` switch 与 `ledger/store.go` 两处 `type IN (…)` 索引）**不收**它，这是有意为之（spec 说的是「卡 timeline」）。无改动。→ 归 L（事件）+ C（`--via` 取值）。
6. **opener 的退出码**：契约 §3.7 只写 `exec.Command("open", url)`，没有写 `Start` 还是 `Run`。建议 G2 用 `Run()`：只有拿到退出码，「opener 失败 → unopened → needs_human」这条冻结路径才有机会真正触发。→ 归 G2（属实现细节，不改冻结面）。
7. **G1 与 G2 并行写同两个文件**（`desktopwake.go` 不同函数体；`coordapi.go`：G1 改 `registerCoordRoutes` 与 `resolveCoordWorkdir`，G2 改 `handleCoordWake`）：Ticket 0 已把函数空壳分好，冲突只会落在各自函数体内。合并顺序建议 G1 先合。→ 协调者排合并。
8. **`b23328` 源码守卫**：G1 新增的材料 handler 若写在 `coordapi.go`，就在 `TestB23328GatewayNoDirectStoreTaskProjectCalls` 的执法面内，`ListProjectLocations` 必须经 `s.mgr`（契约 §3.5 已写，这里只确认归属）。→ 归 G1。
9. **G1 与 V 并行，两者都写 `desktopwake.go`**（G1：`desktopLaunchMaterial`、`desktopBrief`；V：`wakeDesktopSeat` 本机分支、`desktopLetter`、`writeDesktopLetter`、`claudeContinueURL`）：**G1 先合，V 在 G1 之后 rebase**。函数空壳已由 Ticket 0 分好，冲突只会落在各自函数体里。→ 协调者排合并。
10. **V → G2 → G3 三张卡都写 `internal/agentd/server.go`**，按字段或函数各归一张卡，写进各卡禁改清单：**V** 只加 `Server` 结构体上的 opener 注入字段（V 定名）与测试注入点；**G2** 只加 opener 的生产装配（darwin `open`，在 `Server` 构造处）和失败段计数表字段（建议 `desktopWakeStall`）；**G3** 只在 `internal/agentd/server.go#Handler` 里注册 `/api/desktop/deliver` 一行。三者串行，后一张 rebase 前一张。→ 协调者排合并。

---

## 3. 子卡清单与依赖 DAG

子卡编号是提案编号，真实卡号由协调者扇出时分配。下文按 P1=甲（子系统切法）、P2=甲 展开；P1=乙时的对照见 §3.11。

### 3.0 DAG

```text
（前置：R-B0 回写头部；P3 认定三条外部事实读数）
                     ┌───────────────────────── 第 1 波（并行）─────────────────────────┐
                     │  L 账本层      D 薄壳深链通道      M mod 正式版与安装      W 控制台入口  │
                     └──┬──────────────────────────────────────────────────────────────┘
                        │ L 完成
        ┌───────────────┼──────────────────────┐
        ▼               ▼                      ▼            （launchd 已于 18:43 真机验通）
   C 命令面        G1 拉起材料端点      V 直通竖切 + DTO fixture（协调者）
        │               │                      │
        │               │                      ▼
        │               │               G2 唤醒信箱与分流（S2 网关）
        │               │                      │
        │               └──────────┬───────────┘
        │                          ▼
        │                 G3 拉起器代开与 deliver（S3 网关；需 R-B3 回写）
        └──────────────────────────┤
                                   ▼
               I 父卡 integrate（协调者，本机真机：§6 按故事逐条走）
```

- **边**：L → {C, G1, V, G3}；V → G2；{G1, G2} → G3；{L, C, G1, G2, G3, W, D, M} → I。
- **为什么 G2 不依赖 G1、C**：G2 只读 `SeatDesktopOf`（L 提供）；机内测试直接用 Store 写入夹具。spec「S2 不依赖 S1」在这个切法下成立，前提是 L 先落。
- **为什么 G3 依赖 G1**：`renderLauncherLetter` = `desktopLaunchMaterial(card, via=launcher)` + 拉起器模板（契约 §3.9.1），材料函数在 G1。
- **W、D、M 没有前置**：DTO、前缀、字面量都已由 Ticket 0 冻结并有金样本；W 对假 API 开发，跟真端点的连接在 I 卡验。
- **最长链**：L → V → G2 → G3 → I。
- **G1 与 V 并行**，都写 `desktopwake.go`：G1 先合，V rebase（§2.4 第 9 条）。V/G2/G3 写 `server.go` 的分工见 §2.4 第 10 条。
- **中途真机检查点**（P1 裁决）：L/C/G1/W/D 齐 → 第一批真机（故事 1、2、7、8）；G2/M 齐 → 第二批（故事 3、4）；G3 齐 → 第三批（故事 5、6），由 I 卡执行并做全量回归。见 §6。

### 3.1 B434-L：账本层——桌面端席位记录与拉起器登记（`d_ledger`，逻辑型）

**①契约引用**：§2.1（户口）、§3.3（`SeatDesktop`、`SetSeatDesktop`、`SeatDesktopOf`、`EvSeatDesktopSet`、`seat_desktops` DDL、生命周期联动）、§3.4（`DesktopLauncher` 四个方法、`desktop_launchers` DDL）、§3.1 `ValidateDesktopSessionID`；§8 D1（另立表，不扩 `seat_bearings`）、D2（独立 CAS 写面）、D4（拉起器在账本）；条 49–59、62–63、65；条 139–143（Store 一侧，CLI 一侧归 C）。

**②意图与为什么**：桌面端身份和拉起器登记都是**共享账本里的事实**（任意 agentd 都要能读到：唤醒消费轮在任一机认领，拉起请求可能从手机连的那台发起）。把两张表和写面集中在一张卡里，后面所有卡（C、G1、G2、G3）都只读它的方法，账本文件只有一个写者。生命周期联动（换绑、清座时删行）必须和席位写在**同一个事务**里，否则会留下「旧席位的桌面端 id 指着新席位」的孤儿行。

**③验收（逻辑型，真 SQLite + 现有 Postgres 方言 parity 测试）**：
- `SetSeatDesktop` 成功 → 同一事务恰好写一行、落一条 `EvSeatDesktopSet`，载荷键集恰为 {identity, desktop_session, machine, via}（条 49/50）；对 coordinate 席位、空座、CAS 不符、非法 id **各自返回错误，且行数与事件数都不变**（条 51–54，反面断言用 count 查询，不只断言 err）。
- `ValidateDesktopSessionID` 表驱动（条 55–59）。
- `RebindSeat` 成功后 `SeatDesktopOf` 为 ok=false；`clearSeatTx` 走终态关单和人工清座两条入口后，都是 ok=false（条 62/63）；`BindSeat` 不写也不删该行。
- 桌面端 bind 席位在 `seat_bearings` 里没有行（条 65，B389 规则 4 的既有测试不改仍绿）。
- `RegisterDesktopLauncher` 覆盖式；`UnregisterDesktopLauncher` 幂等；`ListDesktopLaunchers` 按 project 升序；登记不落任何卡事件（条 140/142/143 的 Store 一侧）。
- 两方言 DDL 对齐：`ddl_parity_test.go` 覆盖两张新表。
- 缺陷族结论（§5）：生命周期——联动删行与席位写同事务（能变红：把删行挪出事务后，并发换绑测试会读到孤儿行）；门禁——`SetSeatDesktop` 的 CAS 是唯一写闸；序列化——`bound_at`/`registered_at` 的时间往返测试两方言都要有。

**④入口指针与有界文件集**：`internal/ledger/seat_desktop.go#SetSeatDesktop`、`internal/ledger/binding.go#RebindSeat`、`internal/ledger/binding.go#clearSeatTx`、`internal/ledger/store.go#ddlStatements`、`internal/proto/desktop_coordinator.go#ValidateDesktopSessionID`（只改函数体）；测试 `internal/ledger/*_test.go`、`internal/ledger/ddl_parity_test.go`。**禁止**碰 `internal/agentd/**`、`cmd/**`。

### 3.2 B434-C：命令面——桌面端坐席、拉起器登记、等人标 actor、status 段（`d_cli`，逻辑型）

**①契约引用**：§3.10 全节；§3.8.1「席位自打的前提」；§8 D2；条 42–48、60–61、64、139–143（CLI 一侧）、159–161、189–190；**§4.16 条 252–260、262**（P7=d：`handoff card coordinate <卡> --desktop` 只调代开端点、零调 `coordinator/launch`、stderr 逐字打印服务端 `error`、CLI 不打 needs_human；对应 client 方法 `DesktopLauncherOpen`）；**§4.17 条 277–278**（P9=丁：`internal/skill/desktop_rule.go` 的 `DesktopLetterAuthorizationRule` 文案常量逐字进 SKILL.md「占座」节与「主 agent 回路」的 CLAUDE.md 常设授权片段）；欠账 §10.15、§10.18（CLI 一侧）；§11.2；P6=甲时含 spec「实现决定」skill 文本两节与 CLAUDE.md 常设授权一行。

**②意图与为什么**：新开出来的桌面端会话靠 `card bind --desktop-session` 把自己挂到卡上（故事 2），拉起器靠 `launcher register` 登记自己（故事 5/6），人和主 agent 靠 `handoff status` 看拉起器在哪、通不通。`card needs` 改用席位身份作 actor，是 P5=B 能把「协调者自己打的标」排除出唤醒的**唯一前提**——不改的话规则 4 永远不会命中，协调者会被自己打的标叫醒（回声环）。

**③验收（逻辑型，命令级测试走真账本，`env -u CLAUDE_CODE_SESSION_ID` 环境下跑）**：
- 不带 `--desktop-session` 的 `card bind`：stdout、席位两列、事件数与 `e020380a` 逐字相同（条 42–44，用基线 golden 对比）。
- 带 `--desktop-session` → `SeatDesktopOf` 命中；`--machine` 缺省 = `os.Hostname()`；`--via` 缺省 `manual`（条 45–48）。
- `--desktop-session` 配非 `claude` 席位 → 报错且无桌面端记录；`--machine/--via` 不带 `--desktop-session` → 报错且**没有坐下**（条 60/61，反面断言查席位列）。
- 第二步 `SetSeatDesktop` 失败（注入 CAS 冲突）→ 命令非零退出，但席位保持已坐下（D2）。
- `card seat desktop set` 以当前 `driver_session` 作 expect；`card rebind --self` 之后旧行已删，新会话可以重设。
- `launcher register` 在 cli 段不是 `claude` 时报错且不写（条 141）；重复 register 覆盖（条 140）；`launcher show` 输出 JSON。
- `card needs <id> <原因>`：能出示席位身份时 actor = 席位身份；出示失败时 actor = `ledgerActor()` 且**退出码 0**（条 189/190）；`--clear` 行为不变。
- `handoff status` 拉起器段列出 project / desktop_session / machine / 可达（条 159–161）；hostname 不在已知 target 名里时给出告警（§11.2）。
- **P7=d**：条 252–260 按契约测试名逐支（httptest 假 agentd 只收到一次请求；200 打一行 JSON 退出 0；409/502 stderr 含服务端 `error` 原文、不带 JSON 外壳、退出非零；不带 `--desktop` 行为不变）。
- **P9=丁**：条 277/278，SKILL.md 两处含 `DesktopLetterAuthorizationRule` 逐字（测试读常量比对，不在测试里另写一份文案）；条 262「主 agent 回路」含 `handoff card coordinate <id> --desktop`。
- P6=甲：`skills/handoff/SKILL.md` 含「用 Claude Code 坐下」、`card bind … --desktop-session`、`launcher register`、拉起器职责、CLAUDE.md 常设授权一行（`grep` 可断言；授权的真实效果见 §6）。
- 缺陷族结论（§5）：静默失败——两步提交的半态必须让命令非零退出并打印「已坐下但未记录桌面端 id，可用 card seat desktop set 补」；门禁——`launcher register` 任何 claude 会话都能覆盖，属已知面（§5.5）。
- 架构法备注：`cmd` 第三条信号命中，已答可圈（§1.1）。

**④入口指针与有界文件集**：`cmd/card_driver.go#cardBindCmd`、`cmd/card.go#cardCoordinateCmd`（`card coordinate` 加 `--desktop`；r4 更正：原误写 `card_driver.go`，契约 §3.10 本来就对）、`internal/skill/desktop_rule.go`（授权规则常量，只读引用）、client 侧新增 `DesktopLauncherOpen` 方法（落点按既有 `client.Client` 协调者方法同文件，plan 定）、`cmd/card_bearing.go#cardSeatCmd`、`cmd/card_records.go#cardNeedsCmd`、`cmd/status.go#statusCmd`、`cmd/card_seat.go#currentSeatIdentity`（只调用，不改）；新建 `cmd/card_seat_desktop.go`、`cmd/launcher.go`；测试 `cmd/*_test.go`；`skills/handoff/SKILL.md`（P6=甲）。**禁止**碰 `cmd/skill.go`、`main.go`（归 M）、`internal/**` 生产代码。

### 3.3 B434-G1：拉起材料端点与简报（S1 网关，`d_gateway`，边界型）

**①契约引用**：§3.5（`desktopLaunchMaterial`、`desktopBrief`、简报模板、`Launcher` 字段）、§3.9 GET 行、§3.2 `DesktopLaunchMaterial`；条 1–14、200–202；欠账 §10.16（端点一侧）；§11.1、§11.5。

**②意图与为什么**：拉起材料（本机项目根目录 + 简报）由控制面产出、不由前端拼——深链、复制兜底、拉起器来信三处用的是同一份材料（spec「契约语义」第一条）。「本机」以 agentd 自己登记的项目位置为准，所以材料必须在 agentd 侧按 `ListProjectLocations` 解析。

**③验收（机内：httptest + 真账本 + 假 `mgr` 项目位置）**：
- 项目无本机位置 → 409，`error` 含 project 名，**账本事件数不变**（条 1–3）。
- 有位置 → 200，`Folder` 等于 `resolveCoordWorkdir` 同款解析结果（条 4/5；§11.1 建议把解析收成一个返回 `(string, error)` 的函数，两处共用）。
- `Brief` 以 `"[handoff] "` 开头、含卡号 / 基线（或「未设」）/ 会话（或「未入群」）/ `--desktop-session` / `--via page`，**不含**「一次性回合」（条 6–12）；未入群时第 3 步末句换成 needs_human 版本。
- keystone/scheduling 未装配时仍 200（条 13）；GET 前后账本与进程内状态快照相等（条 14）。
- `launcher`：已登记 → 等于 `DesktopSession`；未登记 → JSON 里没有该键；读登记失败 → 仍 200、没有该键、记一条 Warn（条 200–202）。
- `b23328` 源码守卫仍绿（§2.4 第 8 条）；新增代码对 `keystone.` 零新调用（条 162）。
- 缺陷族结论（§5.9，逐族）：生命周期——无状态读端点，无中断风险；静默失败——409 文本逐字可行动（「handoff project add 登记后可用」），读拉起器失败只降级、不吞成 500 也不吞成「未登记」以外的结论（Warn 可见）；跨平台——`Folder` 是 agentd 机器上的绝对路径，换一台机器的前端不能复用（故事 8 已覆盖）；假绿——条 4/5 的 `Folder` 必须和 `resolveCoordWorkdir` 走同一个解析函数，不能拿夹具常量比对；门禁——只套 `withLedger`、不套 `withCoordinator`，所以没有协调者装配时也能读，但仍在 `/api` token 中间件之后；**序列化**——`launcher` 是可省略键（omitempty），缺键与空串的区分由 V 卡 DTO fixture（带与不带 `launcher` 两条样本，TS 侧可空类型）锁住，G1 自己的 httptest 断言「未登记时 JSON 里没有 `launcher` 键」（解析成 `map[string]json.RawMessage` 查键）；**承重安全属性**——GET 不写任何状态，由条 14 的反面断言锁住（请求前后账本事件数、`seat_desktops`/`desktop_launchers` 行数、进程内失败段计数表快照全部相等）。

**④入口指针与有界文件集**：`internal/agentd/desktopwake.go#desktopLaunchMaterial`、`internal/agentd/desktopwake.go#desktopBrief`、`internal/agentd/coordapi.go#registerCoordRoutes`（只加 GET 路由一行）、`internal/agentd/coordapi.go#resolveCoordWorkdir`；测试新建 `internal/agentd/desktopwake_material_test.go`。**禁止**碰 `wakeconsumer.go`、`scheddrain.go`、`handleCoordWake`。

### 3.4 B434-V：直通竖切 + DTO fixture（协调者 / contract 执行者；不进并行子卡）

**①契约引用**：§9.3 全节（测试名 `internal/agentd/desktopwake_slice_test.go` 的 `TestB434DesktopWakeVerticalSlice`）；欠账 §10.4、§10.5、§10.18（来信渲染一侧）；§6 DTO 形状行；条 67、83、85、89–92（快乐路径）；**§3.6 来信模板（r7 改写）与 §4.17 条 74–79、191、263–275、280**（P9=丁：`desktopLetter` 组装 sections、`renderDesktopLetter` 围栏 / 名册 / 转义；落码即激活 `desktopwake_b434_test.go` 里经 `skipUntilDesktopQuoteImplemented` 跳过的三支金样本与 `internal/agentd/testdata/b434_desktop_letter.golden`）。

**②意图与为什么**：重档的法定步骤——在扇出 G2 之前，用最小真实实现把主缝（唤醒信箱）从消费循环一路打通到 opener，证明契约的形状接得上。之后 G2 只是在一条已经通的路上补分支。DTO fixture 是 Go↔TS 跨语言序列化边界的唯一闭环测试，放在同一轮里一起清。

**③验收（机内）**：
- `TestB434DesktopWakeVerticalSlice`：真 SQLite 账本、一张 bind 席位卡、经 L 的 `SetSeatDesktop` 写入桌面端记录（`Machine` = 本机）、一条 `decision_opened` → 跑一轮 `consumeAutomationEventsOnce` → 断言 `t.TempDir()` 作 HOME 的信箱里**恰好一个**文件且文件名匹配 `^<CLI id>\.[0-9]{19}\.md$`、opener spy **恰好**被调用一次且 URL 等于 `claude://code/continue?session=<id>`、`keystone.Wake`/`LaunchForCard` 调用 0 次、该批 seq 已标 seen。
- **反面**：同一夹具把桌面端记录删掉 → 零文件、零 opener、日志里仍有「bind 席位跳过协调者唤醒」（条 93 的快乐路径对照）。
- DTO fixture：`contract_fixture_test.go` 加 `DesktopLaunchMaterial`（带与不带 `launcher` 两条）、`DesktopLauncherOpenResp`、`DesktopDeliverReq`、`DesktopDeliverResp` 样本，`-update` 生成 `web/src/api/testdata/*.json`，`contract.test.ts` 读取并逐键比对 TS 类型（用可空类型区分「缺键」和「零值」）。
- 缺陷族结论（§5）：假绿——竖切必须走真的 `consumeAutomationEventsOnce`，不许直调 `wakeDesktopSeat`（否则测不到 `resolveWakeCard` 那一段）。

**④入口指针与有界文件集**：`internal/agentd/wakeconsumer.go#resolveWakeCard`、`internal/agentd/scheddrain.go#wakeCoordinatorRoundID`（只加 bind+桌面端分支）、`internal/agentd/desktopwake.go#wakeDesktopSeat`（本机分支）、`internal/agentd/desktopwake.go#desktopLetter`（单事件）、`internal/agentd/desktopwake.go#writeDesktopLetter`、`internal/agentd/desktopwake.go#claudeContinueURL`、`internal/agentd/server.go`（opener 注入字段）；新建 `internal/agentd/desktopwake_slice_test.go`；`internal/proto/contract_fixture_test.go`、`internal/proto/testdata/`、`web/src/api/testdata/`、`web/src/api/contract.test.ts`。**合并顺序**：在 G1 之后 rebase（§2.4 第 9 条）。**`server.go` 禁改**：除 opener 注入字段外不动（生产装配归 G2，路由归 G3）。

### 3.5 B434-G2：唤醒信箱、分流改接与失败路径（S2 网关，`d_gateway`，边界型）

**①契约引用**：§3.6（信箱 wire、来信模板、`desktopLetter`/`writeDesktopLetter`）、§3.7（continue、投递、两类类型化错误、失败段计数、opener）、§3.8（六处改接）、§3.8.1（needs_human 唤醒，P5=B）；§8 D3、D5、D6；条 66–111、165–174、177–179、182–188、191–193；**§4.17 条 263–280**（P9=丁：围栏渲染层由 V 落码（契约欠账 18），G2 把来信扩到全部事件种类时必须保持三支金样本与 golden 全绿——工单、needs_human、其它事件分别进 `kind=ticket`/`needs_human`/`event` 围栏块，段标题不含 actor（条 275），名册与围栏作者集合双向相等（条 266））；欠账 §10.14；§11.3、§11.4。

**②意图与为什么**：这是本卡的主缝——卡上有事时，控制面把本来经 `session wait`/`card wait` 交付的投影写成一封信放进该会话的信箱，再用 continue 深链叫醒会话（不管它活着还是已被回收）。失败处置的不对称（信没落盘就不标 seen、下轮重试；信已落盘就标 seen、不重发）是 D5 的核心，反过来写会丢唤醒或发重复信。

**③验收（机内：临时 HOME + opener spy + httptest 假转交端 + 真账本；P2=甲时以 V 的竖切为基）**：
- **信箱**：两次写产生两个文件、都能读回；用 `.tmp` + `rename` 写，并发读者永远读不到半封信（用并发读者循环断言，**负载下也不翻红**）；目录不存在时以 0o700 创建（条 67–70）。（残留 `.tmp` 的清理：plan 定，不改冻结面，不作验收硬判据。）
- **来信**：首行 `"[handoff] "`；含卡号与每个 seq；会话消息正文**超过 400 字也不截断**（夹具用 1000 字正文，专门防 `Summary` 截断回归）；引用条等于 `MessageReference` 的返回；没有 reply_to 就没有引用段；未读数等于 `ListSessions(seat)` 里该会话的 `Unread`；工单载荷原文全文；needs_human 段的形状（条 71–79、191）。读引用或未读失败 → 不写信、返回错误（条 80）；写信不落账、不推游标（条 81/82，查事件数与游标表）。
- **分流**：`resolveWakeCard` 四种情形（条 85–88）；桌面端本机 → 一封信、一次 opener、`keystone.Wake`/`LaunchForCard` 0 次、`AdmitSeatCarrier` 0 次（条 89–92 + §2.4 第 4 条）；无记录的 bind → 零写零开、日志仍在（条 93）；coordinate 席位既有唤醒测试不改仍绿（条 94）。
- **跨机**：异机且未被转交 → 恰好一次 `transferCoordinatorWake`、本机零写（条 95）；被转交端带转交标记（条 96）；被转交端本机写信并开 opener（条 97）；一跳封顶（零出站、502、不打标，条 98/99）。
- **失败段**：异机转交失败 / 本机写信失败 → 不标 seen、不 complete、认领留在飞，段内首次落 needs_human、前缀「桌面端协调者唤醒失败：」（条 100–102、106–108）；opener 失败 → 信还在、标 seen 并 complete（条 109–111）；被转交端 opener 失败 → 200 + `letter_unopened=true` + `woke=false`、不打标；发起端据此标 seen 并按失败段落标（条 165–170）；同段只落一条，成功即清零（条 103/104）；对端恢复后保住的事件出现在信里（条 105）；被转交端与 `wakeDesktopSeat` 零 `MarkNeedsHuman`（条 171/172）；不调 `recordAdmissionStall`、不产生「准入」理由的标（条 173/174）。**互斥断言**（审阅轮 3 建议 1）：同一次失败不会同时被判成 undelivered 和 unopened。
- **needs_human 唤醒（P5=B）**：条 177–179、182–188、192–193 逐条；**回声环反面**：本卡自己落的失败段 needs_human（actor `agentd`）**不会**唤醒该席位（条 187）——这是 D6 的承重安全属性，必须有一支能变红的测试。
- `seat_desktop_set` 不唤醒（条 66）；`keystone.` 零新增调用（条 162）。
- opener 生产实现：非 darwin 返回「本平台不支持 Claude 桌面端深链」（硬判据）。用 `Run()` 拿退出码属 plan 定的实现细节（§2.4 第 6 条），不改冻结面、不作验收硬判据。**注意**：`open claude://…` 交给 LaunchServices 后，退出码很可能恒为 0（launchd 探针读数 `open exit=0`），所以「opener 失败 → unopened」在真机上能不能被触发，以真机第二批 #9 为准，不写成预期；机内的 unopened 路径照常用 opener spy 注入失败来测。
- **自唤醒现状锁定（代码事实，机内测）**：`internal/collab/room/delivery.go#ResolveDelivery` 不排除作者，`internal/agentd/wakeconsumer.go#decodeRoomMessageForWake` 只过滤系统消息（`by_system`/pointer）→ 坐在卡上的桌面端会话 @ 自己的卡或 reply_to 自己的消息，会把自己算进唤醒目标。G2 加一支测试锁住**当前行为**（作者 = 席位身份的消息 → 产出该卡的 `WakeMessage` → 写一封信），让这件事在测试里可见；要不要改成排除作者属 spec 外，按真机观察（§6 #18）结果另开卡。
- 架构法备注：`internal/agentd` 第三条信号命中，已答可圈（§1.1）。
- 缺陷族结论见 §5.3（G2 是风险最集中的卡）。

**④入口指针与有界文件集**：`internal/agentd/desktopwake.go`（`writeDesktopLetter`、`desktopLetter`、`claudeContinueURL`、`wakeDesktopSeat`、两类错误、`withForwardedWake`、`isSystemNeedsHumanActor`、`desktopNeedsHumanWakes`）、`internal/agentd/wakeconsumer.go#resolveWakeCard`、`internal/agentd/wakeconsumer.go#automationWakeEvents`、`internal/agentd/wakeconsumer.go#consumeAutomationEventsOnce`（只加并列分支）、`internal/agentd/scheddrain.go#wakeCoordinatorRoundID`、`internal/agentd/scheddrain.go#transferCoordinatorWake`、`internal/agentd/coordapi.go#handleCoordWake`、`internal/agentd/server.go`（只动 opener 生产装配与失败段计数表字段，V 的注入字段不改、`Handler` 路由归 G3）；新建 `internal/agentd/desktopwake_wake_test.go` 等；`internal/agentd/testdata/b434_desktop_letter.golden`（整信金样本，只读；改动须经契约修订）。**禁止**碰 `internal/agentd/wakeconsumer.go#automationWakeEvent`（纯函数不改，条 192）、`internal/keystone/**`、`registerCoordRoutes`（G1/G3 的地盘）。

### 3.6 B434-G3：拉起器代开与 `/api/desktop/deliver`（S3 网关，`d_gateway`，边界型）

**①契约引用**：§3.6 拉起器来信模板、§3.9 POST 两行、§3.9.1 全节；§8 D3、D4；条 144–158、213–222；P3=A；**§4.15 条 243–251**（r6，R-B3：deliver 经 `withLedger`、503/404 与发起端 502）；**条 261**（r7，P7=d：代开端点空座前置——席位有人即 409 指向 `handoff card rebind`，先于拉起器查找，零写信零 opener 零打标）。

**②意图与为什么**：人不在电脑前时（故事 5），由已登记的拉起器会话替人开出协调者会话。拉起材料必须在**拉起器所在的机器**上解析（项目根目录是那台机器的路径），发起端只搬运「哪张卡」——同 D3 原则。没有拉起器时要打 needs_human，不伪造（故事 6）。

**③验收（机内：httptest 双 agentd、临时 HOME、opener spy、真账本）**：
- 无拉起器 → 409 逐字「无拉起器，请在桌面端开一场并登记」，同时 `MarkNeedsHuman(card, 同文, "agentd")` **恰一次**，零写信、零 opener（条 144–147）。
- 本机拉起器 → 拉起器 CLI 会话信箱里一封以 `[handoff]` 开头、含 `Folder` 与 via=launcher 简报全文的信；opener 恰一次；不写 room_message（条 148–152）；`renderLauncherLetter` 恰一次、只经本机分支（条 217）。
- 异机拉起器 → 恰一次带 `X-Handoff-Forwarded` 的 `POST /api/desktop/deliver`（条 153）；发起端零调用 `renderLauncherLetter`/`desktopLaunchMaterial`（条 213/214）；收件机恰一次 `renderLauncherLetter(req.Card)`，`Folder` 取收件机自己的登记（夹具让两台的路径不同，条 215/216）。
- `/api/desktop/deliver`：无 token → 401（条 154）；不管有没有转交头都零出站（条 155）；`written:true`（条 156）；opener 失败 → 200 `{written:true, opened:false}`、信还在（条 157）；收件机无本机位置 → 409 含项目名、零写零开，发起端 409 逐字透传、不打标（条 218–221）；`card` 为空 → 400（条 222）；**R-B3 后**：账本未配置 → 503、卡不存在 → 404。
- 异机投递其它失败 → needs_human（前缀同 G2）+ 代开端点 502（条 158）。
- `keystone.` 零新增调用（条 162）。
- 缺陷族结论（§5.4）：门禁——deliver 端点只接受 `ValidateDesktopSessionID` 通过的 id，`open` 不经 shell，URL 不可注入；一跳封顶有反面断言。

**④入口指针与有界文件集**：`internal/agentd/desktopwake.go#openViaDesktopLauncher`、`internal/agentd/desktopwake.go#deliverDesktopLetter`、`internal/agentd/desktopwake.go#handleDesktopDeliver`、`internal/agentd/desktopwake.go#renderLauncherLetter`、`internal/agentd/coordapi.go#registerCoordRoutes`（加 POST 路由）、`internal/agentd/server.go#Handler`（只注册 `/api/desktop/deliver` 一行）；新建 `internal/agentd/desktopwake_launcher_test.go`。**禁止**碰 G2 已交付的唤醒分支函数，以及 `server.go` 里 V/G2 的字段与装配。

### 3.7 B434-W：控制台入口——深链编码、薄壳桥、面板三按钮与复制兜底（`d_web`，逻辑型）

**①契约引用**：§3.12 全节、§3.2 TS 镜像；条 15–23（编码）、203–210（拉起器按钮；P7=d，显示条件不变）、**§4.14 条 223–242**（r6，P8=甲：坐下按钮）；欠账 §10.16（面板一侧）。

**②意图与为什么**：人在卡抽屉里点一下就能把桌面端会话开到项目里（故事 1）；浏览器不认 `claude://` 或桌面端没装时，同一个入口给出简报和「复制」（故事 7）；项目在本机没有位置时，入口禁用并逐字说明原因（故事 8）；手机上点「让拉起器代开」（故事 5）。所有文案取 Ticket 0 的常量，组件里不写字面量。

**③验收（vitest + Testing Library，假 API）**：
- 编码：成功值以 `claude://code/new?folder=` 开头；中文、换行、双引号、`&`、`#` 经 `new URL(url).searchParams.get('q')` 逐字还原；成功值不含 `+`；长度 = 上限 → 成功、上限 + 1 → `brief_too_long` 且**不截断**；非绝对路径 → `folder_not_absolute`；空简报 → `brief_empty`（条 15–23；表驱动）。
- `requestOpenDeepLink`：有桥时恰发一次 `webkit.messageHandlers.external.postMessage('handoff:open-deeplink:'+url)`，返回 true；无桥时返回 false 且不抛（前缀字面量条 29 已由 T0 锁住）。
- 面板「用 Claude Code 坐下」（契约 r6 条 223–242）：材料 409 → 按钮禁用，说明文字逐字等于服务端 `error`；点击在薄壳内恰一次 `requestOpenDeepLink`；浏览器内渲染 `href` 等于编码结果的 `<a>`；编码拒绝或无桥 → 「复制简报」+「到 Claude 桌面端新建会话后粘贴」可见；信任提示可见。
- 「让拉起器代开」：显示条件（条 203–205，P7=d 不变：仅已登记时出现）、三按钮顺序（条 206）、恰一次 POST（条 207）、成功文案（条 208）、失败逐字进 `role="alert"`（条 209）、请求中与断连时禁用（条 210）。
- 文案常量金样本仍绿（条 194–199）；`npm run typecheck`、B434 文件 `eslint` exit 0。
- 缺陷族结论（§5.6）：静默失败——浏览器里点 `<a href="claude://…">` 失败时页面感知不到，所以浏览器形态下复制兜底**始终展开**，不能等失败再出现；序列化——`q` 往返测试即 roundtrip 属性测试的一例，建议加随机串属性测试。

**④入口指针与有界文件集**：`web/src/app/lib/claudeDeepLink.ts#buildClaudeNewSessionURL`、`web/src/app/lib/desktopShell.ts#requestOpenDeepLink`、`web/src/app/cards/CoordinatorPanel.tsx#CoordinatorPanel`、`web/src/app/cards/desktopSeatCopy.ts`（只读常量）、`web/src/api/scheduling.ts`（只用，不改 DTO）；对应 `*.test.ts(x)`。**禁止**碰 `web/src/api/contract.test.ts` 与 `web/src/api/testdata/`（归 V）。

### 3.8 B434-D：薄壳深链通道（`desktop/`，图外边界型）

**①契约引用**：§3.11；§6 第 1 行；条 30–41（28 已由 T0 锁住）；§2.4 第 1 条（销 §10.7）。

**②意图与为什么**：handoff 桌面薄壳里的控制台要能把 `claude://` 交给系统。只给 `claude` 这一个 scheme、`code` 这一个 host、`/new` 与 `/continue` 两个 path 开白名单——spec 明文「薄壳放行任意 scheme」永不做。

**③验收（机内 go test；desktop 子模块 `go build ./internal/...`、`go vet ./internal/...`）**：
- 非本前缀 → false、opener 零调（条 30）；`claude://code/new?…` 与 `claude://code/continue?session=local_x` → true、opener 恰一次且参数逐字相等（条 31/32）；`http(s)`、`file`、`javascript:`、host 不是 `code`、path 不在白名单、含控制字符、来源 frame 非 http(s) → true（已处理）但 opener **零调**（条 33–40）；日志不出现完整目标 URL（条 41，抓日志断言 q 不出现）。
- `desktop/main.go` 中处理器排在剪贴板、系统浏览器之后（源码顺序断言或接线测试）。
- 缺陷族结论（§5.7）：静默失败——Wails `OpenURL` 不回报 `open` 退出码（§2.4 第 1 条），薄壳侧没有可观察的失败；W 的复制兜底是唯一的补救，真机核对见 §6。

**④入口指针与有界文件集**：`desktop/internal/shell/external_deeplink.go#HandleExternalDeepLinkMessage`、`desktop/internal/shell/external_deeplink.go#validateClaudeDeepLink`、`desktop/internal/shell/external_browser.go#HandleExternalBrowserMessage`（样板，只读）、`desktop/main.go`（RawMessageHandler 一处）。**禁止**碰主模块。

### 3.9 B434-M：mod 正式版与 `skill install` 安装（`plugins/` + `d_maintenance` + `d_cli` 组装点，边界型）

**①契约引用**：§3.13；§6 第 2/3 行（按 §2.4 第 3 条修订）；条 112–138、163、**279**；§2.4 第 2/3 条（销 §10.6/§10.8）；P5。**P9=丁 对 mod 行为无改动**：围栏、名册、转义都在 agentd 侧 `desktopLetter` 渲染，mod 只搬运正文；M 只多一支断言锁住 `prompt.submit` 实参仍为 `asUser: true`（条 279，拍板 D7）。

**②意图与为什么**：信箱唤醒的接收端是装在每场桌面端会话里的 mod；它是 handoff 的正式交付物，和 OpenCode monitor 插件一样随 `handoff skill install` 安装，版本与二进制绑定（`main.go` 注释里的 B59 D5 原则：用会漂移的通道修漂移自相矛盾）。

**③验收**：
- *逻辑型（`claude plugin test`，P5=甲时在本机跑）*：有一封本会话的信 → 恰一次 submit、文本等于全文、submit 时文件已不存在（条 112/113）；空信箱 → 零次（条 114）；两封 → 两次、按文件名升序、不合并（条 115–117）；别的会话 id 前缀的信不读、不删（条 118/119，**这是信箱的隔离安全属性**，必须能变红）；点开头与 `.tmp` 不读（条 120）；`[handoff]` 预填 → 清空并恰一次提交，非 `[handoff]` → 零次、内容不变（条 121–124）；首查 1.5s、间隔 5s（条 125/126，用假时钟）；mod 的目录与标记字面量和 Go 侧逐字一致（条 127）。
- *逻辑型（Go）*：`skill install` 在 `~/.claude` 存在时把全部文件写到 `~/.handoff/claude-mods/handoff-desktop-wake/`（条 128）；`env.CLAUDE_CODE_PLUGIN_DIRS` 含该目录，按 `filepath.SplitList` 保留原有项，`settings.json` 其它键逐字不变（条 129–131，用含多目录与其它键的夹具）；`~/.claude` 不存在 → skipped 且不创建（条 132/133）；status 的 in-sync / stale / missing / env 缺目录四态（条 134–137）；内嵌 FS 含 `.claude-plugin/plugin.json`（条 138，`all:` 前缀）；mod 零 import 主模块（条 163）。
- *边界型（归 §6）*：桌面端会话真的加载 mod；`$.env.get("HOME")` 与 agentd 的 HOME 一致；多目录 `:` 分隔真机加载。
- 缺陷族结论（§5.8）：序列化——`settings.json` 是手写 JSON 合并，必须保序保值（用 `json.RawMessage` 透传其它键，往返断言）；门禁——P9。

**④入口指针与有界文件集**：新建 `plugins/claude-desktop-wake/`（`.claude-plugin/plugin.json`、`hooks/hooks.json`、`hooks/register.ts` 及测试）；新建 `internal/skill/claudemod.go` 与测试；`internal/skill/plugin.go#InstallPlugin`、`internal/skill/plugin.go#PluginStatus`（样板，只读）；`main.go`（embed 一处 + `SetClaudeModFS` 调用）；`cmd/skill.go#skillInstallCmd`（install/status 各追加一行）。原型（只读参考）在 `~/.claude/dev-mods/…/handoff-autosend/hooks/register.ts`。**禁止**碰 C 的命令文件。

### 3.10 B434-I：父卡 integrate（协调者，本机真机）

**①契约引用**：spec 用户故事 1–9；契约 §1.2 外部现实表、§10.13；本稿 §4、§6。

**②意图与为什么**：子卡各自只能证明自己那一半；桌面端会话、`open`、launchd、mod 加载、拉起器会话的自主行为都是外部现实，只能在装了 Claude 桌面端的本机上逐个故事走一遍。

**③验收**：前两批真机检查点（§6 第一批、第二批）在对应子卡齐时由协调者执行；I 卡只做**第三批**（故事 5、6）与**全量回归**（前两批复跑一遍），原始读数落台账；按故事 1–9 各留一条端到端证据（截图或日志行 + 卡 timeline seq）；图对账（欠账 §10.3：分支视图 diff `codegraph/diffs/cards-B434-charter-1.json`，`validate --view`、`resolve --doc` 本稿与契约零 vanished/file_missing）。

**④入口指针**：无新增源文件；证据落 `docs/superpowers/specs/b434-ledger.md`。
- **r3 补项（跨卡审计 2026-10-16）**：B434.1 T6 的 PG 方言往返测试（`LEDGER_TEST_PG_DSN` 门控）本机无 PG，归 I 卡在专用测试库跑一次并贴读数；G2 plan 须显式吸收：信箱文件名 UnixNano 段 19 位零填充（`%019d`，mod 正则 `^<id>\.[0-9]{19}\.md$`）、`writeDesktopLetter` 对 cliSession 做文件名安全校验（拒 `/`、`\`、以 `.` 开头，失败走条 106）；C/V plan 须显式吸收：`SetSeatDesktop` 的 `Via` 非空且在词表、`RegisterDesktopLauncher` 的 `Actor` 非空、`Project` 无首尾空白。

### 3.11 P1=乙（按故事切）时的映射（供对照）

S1 = L 的 `seat_desktops` 部分 + C 的 bind/seat 部分 + G1 + W 的坐下按钮 + D；S2 = V + G2 + M + C 的 `card needs`；S3 = L 的拉起器表 + C 的 launcher/status/skill 文本 + G3 + W 的代开按钮。共享文件冲突：`desktopwake.go`（三卡）、`coordapi.go`（三卡）、`seat_desktop.go`/`store.go` DDL（S1/S3）、`CoordinatorPanel.tsx`（S1/S3）、`server.go`（S2/S3）。依赖：S3 依赖 S1 与 S2，S2 的手填路径依赖 S1 的账本部分。

---

## 4. 跨子系统行为闭环核对

只核 spec 用户故事 1–9 承诺的跨子系统可观察行为。五格齐全、归属卡存在。

| # | 触发者 | 权威事实 / 载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- | --- |
| 1 | 人在卡抽屉点「用 Claude Code 坐下」（薄壳内） | `GET …/desktop-launch` → `desktopLaunchMaterial`（`ListProjectLocations` 本机根目录 + 简报）→ `buildClaudeNewSessionURL` → `handoff:open-deeplink:` → `HandleExternalDeepLinkMessage` → `open` | Claude 桌面端 | 项目目录里出现草稿会话，q 预填简报；首次弹信任框；人按一次发送 | G1 + W + D；真机第一批 #1/#2/#3 |
| 1b | 同上（浏览器内） | 同一份材料 → `<a href="claude://…">` | 浏览器 → 桌面端 | 同上；浏览器可能弹「打开 Claude?」 | G1 + W；真机第一批 #4 |
| 2 | 新会话按简报执行 `card bind <卡> --desktop-session <local_…> --via page` | `BindSeat`（`driver_seat_bound`）+ `SetSeatDesktop`（`seat_desktops` 行 + `seat_desktop_set`） | 协调者状态端点 → `CoordinatorPanel`（5s 轮询） | 卡页从「未绑定」变「已绑定」，席位 cli = claude | L + C；状态显示为既有行为；真机第一批 #5（会话能读到自己的 local_ id） |
| 3 | 人或主 agent 在群里 `@B434 …` | `room_message` → `roomMessageWakeEvents` → `resolveWakeCard`（桌面端 bind 命中）→ `wakeCoordinatorRoundID` → `wakeDesktopSeat` → `desktopLetter`（命中条全文 + 引用条 + 未读数）→ `writeDesktopLetter`（`~/.handoff/desktop-wake/<CLI id>.<ns>.md`）→ `claudeContinueURL` + opener | mod（`session.start` 后或 5s 轮询）→ `$.prompt.submit` | 会话几秒内醒来（含已被回收的），读到那条和被引用的那条，在群里回复 | V + G2 + M；真机第二批 #8/#9/#11/#12（launchd 一条已验） |
| 3b | 同上，席位在另一台机器 | 发起端 `transferCoordinatorWake` → 对端 `handleCoordWake`（带转交标记）→ 对端本机分支 | 对端 mod | 同上；对端不可达 → 卡上「需要人：桌面端协调者唤醒失败：…」，信保住，对端恢复后补投 | G2；真机第三批 #17 |
| 4 | 卡上来工单（`task_mirrored`）或决策 | 同 3 的链路；来信「工单」段载荷原文 | mod → 会话 | 会话被叫醒，按 skill 分诊表处置 | G2 + M；真机第二批 #13 |
| 4b | 人或其它会话 `handoff card needs <卡> <原因>` | `EvNeedsHuman`（actor = 打标者席位身份或 `cli:<USER>@<host>`）→ `desktopNeedsHumanWakes`（排除系统 actor 与席位自打）→ 同 3 的链路 | mod → 会话 | 会话被叫醒，读到「需要人」段原因全文；协调者自己打的标和 agentd 打的标**不会**叫醒它 | C（actor）+ G2 |
| 4c | 唤醒投递失败（写信失败、对端不可达、`open` 失败） | 桌面端失败段计数 → 发起端 `MarkNeedsHuman(desktopWakeFailReasonPrefix+…)` | 卡页「需要人」、手机推送（`internal/agentd/pushfanout.go#OnLedgerEvent`） | 每个失败段恰一条可行动的等人标；不会触发准入理由的标 | G2；静默失败的残余见 P10 |
| 5 | 人在手机上点「让拉起器代开」 | `POST …/desktop-launch` → `openViaDesktopLauncher` → `DesktopLauncherOf` → `deliverDesktopLetter`（本机直写 / 异机 `POST /api/desktop/deliver {card}`）→ 收件机 `renderLauncherLetter` → 拉起器信箱 + continue | 拉起器会话（mod 提交来信 + CLAUDE.md 常设授权）→ 桌面端会话管理工具开新会话、发简报 → 新会话 `card bind … --via launcher` | 面板显示「已请拉起器代开…」；几分钟后卡页显示已绑定 | W + G3 + L + C（register、skill 授权文本）+ M；真机第三批 #15/#16 |
| 6 | 主 agent `handoff card coordinate <卡> --desktop`（手机 / 异地，P7=d） | `POST …/desktop-launch` → 空座前置（条 261）→ 无拉起器 → 409 + `MarkNeedsHuman("无拉起器，请在桌面端开一场并登记")`；CLI stderr 逐字打印该 `error`、退出非零 | 卡页「需要人」、主 agent 终端 | 卡上亮起「需要你：无拉起器」 | C（CLI，条 252–260）+ G3（端点，条 144–147、261）；真机第三批 #19 |
| 7 | 人的浏览器不认 `claude://` / 桌面端没装 | 同一份材料；编码拒绝或无桥 | 面板 | 「复制简报」+「到 Claude 桌面端新建会话后粘贴」可见、可复制 | W（+ G1 材料）；真机第一批 #4 |
| 8 | 项目在本机没有位置 | `GET …/desktop-launch` → 409 `errNoLocalProject` 包装文本 | 面板 | 「用 Claude Code 坐下」禁用，原因逐字显示；「让拉起器代开」不出现 | G1 + W（UI 条目见 R-B2/P8） |
| 9 | 接班者看卡 timeline | `driver_seat_bound`（时间）+ `seat_desktop_set`（`via=page|launcher|manual`、machine、desktop_session） | `CardDrawer` timeline（`eventSummary` 兜底 JSON）/ `handoff card show` | 看得出席位是卡页拉起还是拉起器代开、何时坐下 | L + C（`--via`，简报里已带）；无 UI 改动（§2.4 第 5 条） |
| 附 | 人或主 agent 跑 `handoff status` | `ListDesktopLaunchers` + 目标探活 | 终端 | 逐项目显示拉起器 id、机器、可达 | C |

**闭环结论**：除故事 6（触发者缺失，R-B1，退回待裁）外，每行五格齐全、归属卡存在。依赖外部现实的结果格（桌面端、`open`、mod 加载、拉起器自主行为）都落了 §6 真机条目，不假装机内可达。

---

## 5. 缺陷族对抗审查（逐族正面回答；结论已写入各卡验收栏）

基线：`charter@defect-families`（通用五族 + 三条追加设问）。项目内没有更严格的清单。

### 5.1 L 账本层

1. **生命周期 / 状态机中断**：有风险。`seat_desktops` 行必须随换绑、清座**同事务**删除，否则 agentd 在两步之间重启会留孤儿行，信被投到旧会话。处置：契约 §3.3 已冻结同事务联动，L 验收里换绑、清座各一支断言。拉起器登记没有生命周期联动（会话被删后登记仍在），属已知面，由 `handoff status` 的「可达」与 `launcher unregister` 兜底。
2. **静默失败**：无，因为每个写面都返回错误，而且反面断言查的是行数和事件数，不只是 err。
3. **跨平台**：无，因为两方言 DDL 有 parity 测试，时间列按方言分型；需要锁的是时间往返（已列入验收）。
4. **假红 / 假绿**：风险在「只断言 err」。验收用 count 查询做反面断言；生命周期测试走真实的 `RebindSeat`/终态关单入口，不手写 SQL 模拟。
5. **门禁绕过**：`SetSeatDesktop` 的 CAS（expect = 当前席位）是唯一写闸，coordinate 和空座各一支拒绝测试。TOCTOU：CAS 在事务内比较，无窗口。
- **序列化边界**：`EvSeatDesktopSet` 载荷是 map 手搭 → 键集断言（条 50）。
- **枚举新值**：`seat_desktop_set` 流经卡 timeline（兜底渲染，无白名单）、会话 timeline 白名单（有意不收）、`card wait` 可动作集（default false）、`automationWakeEvent`（default 不唤醒，条 66 锁）、推送分类（不推）——各处都已核过，没有被白名单挡死的消费者。`via` 三个取值只由 CLI 写、只被人读。
- **承重安全属性**：「换绑后旧桌面端 id 失效」由条 62/63 锁。

### 5.2 C 命令面

1. **生命周期**：`card bind` 两步提交的中间态（已坐下、没有桌面端记录）是合法态（D2），不算中断风险；但命令必须非零退出并打印补救命令，人才知道要补。
2. **静默失败**：有风险——`card needs` 回落 `ledgerActor()` 时不报错（契约要求退出 0）。如果所有协调者都回落了，规则 4 会静默失效。处置：回落时打 Warn 到 stderr（不改退出码）；G2 的条 184 测试与 C 的条 189 测试组合锁住正路径。
3. **跨平台**：`--machine` 缺省用 `os.Hostname()`，macOS 上可能带 `.local` 后缀，和 agentd 的 target 名对不上 → 异机转交失败（§11.2）。处置：C 给提示，status 段给告警；真机第三批 #17 核对。
4. **假红 / 假绿**：已知坑——`CLAUDE_CODE_SESSION_ID` 在桌面端会话里已设，会让 `cmd` 包里依赖出示的测试在开发者机器上翻红（台账 r3 读数）。验收统一在 `env -u CLAUDE_CODE_SESSION_ID` 下跑，测试里显式设、清环境变量。
5. **门禁绕过**：`launcher register` 任何 claude 会话都能覆盖项目的拉起器（覆盖式，契约冻结）——后果是拉起信被投到攻击者会话。在单用户信任域内可以接受；actor 可追溯。记为已知面，不改。
- **序列化边界**：`launcher show` 是 CLI 拼的 JSON → 加一支字段键集断言。
- **枚举新值**：`--via` 只接受三个值，其余报错（加一支表驱动）。
- **承重安全属性**：无新增（「席位自打排除」的锁在 G2）。

### 5.3 G2 唤醒信箱与失败路径（风险最集中）

1. **生命周期 / 状态机中断**：有风险，契约已逐项处置——(a) agentd 在写信后、opener 前重启：信已落盘，认领未收尾，下轮重试会产第二封同批信（契约 D5 的取舍只覆盖 opener 失败，不覆盖进程崩溃）——**残余风险，可接受**：mod 逐封提交，多一封重复信，账本是事实源；(b) 失败段计数在进程内，重启后同一失败段可能多落一条 needs_human（契约已写明可接受）；(c) 会话被回收：continue 拉起是正路径。孤儿：`.tmp` 文件在 rename 之前崩溃会留下——mod 排除点开头文件，所以不会误读，但会一直留着；建议 G2 在写信前清理超过 1 小时的同前缀 `.tmp`（plan 定，不改冻结面）。
2. **静默失败 / 误导报错**：**最高风险族之一**。(a) `open claude://…` 对已归档或删除的会话、对不存在的 id 可能照样退出 0 → 信留在信箱、没有任何人知道（P10，真机第二批 #9/#10）；(b) opener 用 `Start()` 会吞掉退出码（§2.4 第 6 条，验收要求 `Run()`）；(c) 来信渲染失败时宁可不发，也不发残信（条 80）。
3. **跨平台**：opener 只支持 darwin；`desk.Machine` 是 Linux agentd → 返回「本平台不支持」→ unopened → needs_human（可行动）。`$HOME` 由 agentd 进程决定：launchd 用户域 LaunchAgent 与桌面端是同一 OS 用户（台账读数）；launchd 内 `open` 到达 GUI 已验（18:43），mod 读到的 HOME 是否一致见真机第二批 #11。
4. **假红 / 假绿**：(a) 并发写信的原子性测试必须有并发读者循环，不能只顺序写两次；(b) 「不标 seen」要断言游标表与 `automationSeen`，而不是只看返回值；(c) 来信全文测试必须用超过 400 字的正文，否则 `Summary` 截断回归会假绿；(d) 夹具里的「`open` 成功 = 会话醒来」是行为假设，对应真机第二批 #8（launchd 送达已验）。
5. **门禁绕过**：(a) 信箱的隔离靠「文件名以 CLI 会话 id 为前缀 + mod 只读自己的前缀」：即使有人用 `card seat desktop set` 把别的会话的 `local_` id 写到某卡上，continue 只会叫醒那场会话，它的 mod 读不到这张卡的信（文件前缀是席位身份里的 CLI id）——这条隔离属性由 M 的条 118/119 锁住；(b) 目录 0o700；(c) **群消息以人的口吻进会话**（P9=丁 已裁：围栏 + 作者名册 + 转义 + 授权规则，契约 r7 条 263–280 锁住渲染侧；会话是否真的按数据对待见真机 #20）——这是本卡新开的信任边界，**最高风险族之二**；(d) 准入：桌面端分支在准入之前，不占小队名额（有意，§2.4 第 4 条）；(e) TOCTOU：被转交端用既有 `handleCoordWake` 的席位 CAS 防止旧席位收信。
- **序列化边界**：来信是 Go 手拼的 Markdown，消费者是模型，不是解析器——没有字段级反序列化；需要锁的是「正文全文不截断」和段落形状（条 74/191）。`letter_unopened` 键已由 T0 金样本锁住。
- **枚举新值**：(a) needs_human 以 `WakeTaskTerminal` 进 `keystone.Decide`——复用既有种类，正是为了不被清单外种类挡住（契约 §3.8.1 第 5 条）；(b) 新的错误类型 `desktopUndelivered`/`desktopUnopened` 要在消费循环里与 `admissionStalled` 并列判定，default 分支（「非准入错误一律终局」）会吞掉它们——G2 必须加「两类错误各走各的分支」的断言。
- **承重安全属性**：(a) 回声环防护（系统 actor 与席位自打不唤醒，条 182–187）——每条一支能变红的测试；(b) 一跳封顶（条 98）；(c) 同段只落一条标（条 103）。**另记一处现状缺口**（不在本卡改）：`internal/collab/room/delivery.go#ResolveDelivery` 不排除作者自己，`decodeRoomMessageForWake` 只过滤系统消息——协调者回复自己的消息（reply_to 自己）或 @ 自己的卡会叫醒自己。这是可 grep 的代码事实，r1 改为 G2 的机内测试（锁住现状，§3.5）；coordinate 席位今天也一样。桌面端会话被叫醒成本低，**会不会形成循环**只能真机观察（§6 #18），确认后开 roadmap 卡。

### 5.4 G3 拉起器代开

1. **生命周期**：拉起器会话被回收：continue 拉起是正路径。拉起器在开新会话途中被回收：新会话没开出来，卡一直未绑定、没有标——**残余风险**，人看面板文案「几分钟内…」没兑现时会发现；真机第三批 #15/#16 观察。
2. **静默失败**：(a) 同 G2 的 `open` 退出码问题（P10）；(b) R-B3：收件机没配账本时，若不加 `withLedger`，会在 `GetCard` 里报 500 或 panic → R-B3 回写后验收里有 503 与 404 两条。
3. **跨平台**：`Folder` 在收件机解析（r5），发起端与收件机路径不同也能对上（条 216 用两台不同路径的夹具）。
4. **假红 / 假绿**：异机测试必须是两个 httptest agentd，各自有项目位置表；只用一个 agentd 自己转发给自己会假绿（一跳封顶判据走不到）。
5. **门禁绕过**：deliver 端点有 token；`desktop_session` 过 `ValidateDesktopSessionID`（字符集白名单），`open` 用 `exec` 参数而不经 shell，URL 不可注入；一跳封顶对带或不带转交头都成立（条 155）。拉起请求是信而不是 `by_system` 结构行（条 152），不会经房间寻址扩散。
- **序列化边界**：`DesktopDeliverReq` 键集由 T0 锁住（条 211/212）；跨机 409 文本逐字透传（条 220）需要一条穿过真实 HTTP 的测试（双 httptest 满足）。
- **枚举新值**：无，因为没有新的种类或状态，`via=launcher` 只写进简报文本。
- **承重安全属性**：「无拉起器时恰落一条标」（条 145）锁住；「发起端不渲染」（条 213/214）锁住，防止有人「顺手优化」回 r4 的做法。

### 5.5 V 直通竖切

1. **生命周期**：无，因为是单进程测试，夹具用 `t.TempDir()` 自动回收。
2. **静默失败**：无，因为断言是精确计数（恰好一个文件、恰好一次 opener）。
3. **跨平台**：opener 用 spy，不依赖 darwin；测试在 Linux CI 上同样可跑。
4. **假红 / 假绿**：风险——直调 `wakeDesktopSeat` 会绕开 `resolveWakeCard`，所以验收要求从 `consumeAutomationEventsOnce` 进；反面对照（删记录后零写）防稳定假绿。
5. **门禁绕过**：无，因为是测试与 fixture，不新增执行路径。
- **序列化边界**：DTO fixture 就是这条族的武器——一份 Go 生成的 JSON 被 TS 读取并逐键比对，可空类型区分缺键和零值（`launcher` omitempty 两条样本）。

### 5.6 W 控制台入口

1. **生命周期**：无，因为是无状态 UI；请求进行中断连由既有 `disabled` 逻辑覆盖（条 210）。
2. **静默失败**：有风险——浏览器里点 `<a href="claude://…">`，没装桌面端时什么也不会发生，页面感知不到。处置：浏览器形态下复制兜底**始终可见**；薄壳形态下 `requestOpenDeepLink` 返回 true 也不代表桌面端收到了（D 拿不到退出码）——薄壳形态下复制兜底建议也常驻成次要链接（plan 定）。
3. **跨平台**：手机浏览器（故事 5）里只有「让拉起器代开」有意义，「用 Claude Code 坐下」在手机上点了也到不了桌面端——建议在移动布局下直接展示复制兜底（plan 定，不改冻结面）；真机第一批 #7。
4. **假红 / 假绿**：编码测试用 `URL.searchParams` 还原，而不是比对手写的期望串；加随机串属性测试（roundtrip）。
5. **门禁绕过**：无，因为前端只发既有鉴权的 API，深链前缀由薄壳白名单再把一道。
- **序列化边界**：`q` 的编码与解码是一条跨进程边界（控制台 → 薄壳 → `open` → 桌面端），机内只到 `searchParams` 还原，桌面端侧解码见真机第一批 #2（14,336 上限）。
- **枚举新值**：`ClaudeDeepLinkRejectReason` 四值——组件的 switch 必须穷尽（TS `never` 检查），不留 default 吞值。

### 5.7 D 薄壳通道

1. **生命周期**：无，因为是同步处理器，opener 是 fire-and-forget（Wails `Start()`）。
2. **静默失败**：有——拿不到 `open` 退出码（§2.4 第 1 条）；只能靠 W 的兜底。
3. **跨平台**：Wails 在 Linux 上走 `xdg-open`、Windows 上另有实现；本卡只在 macOS 真机验收，其它平台没有 `claude://` 处理器，白名单仍然生效，只是打不开。
4. **假红 / 假绿**：拒绝路径必须断言 opener 零调用，而不是只看返回值（返回值对已处理的拒绝也是 true）。
5. **门禁绕过**：白名单是 scheme + host + path 三段，加控制字符与来源 frame 校验；和 `external_browser.go` 是两个独立前缀、两道独立的门，不共享校验函数——需要核对两者对来源 frame 的判据一致（加一支测试）。
- **承重安全属性**：「只放 `claude://code/{new,continue}`」由条 33–40 锁住。

### 5.8 M mod 与安装

1. **生命周期**：mod 随会话进程被回收，靠 `session.start` 后首查补上（已验）。先删后提交：提交失败则这封信丢失——契约「重复投递以账本为准」接受这个代价；可观察面是来信里附的 `handoff card show` 提示。
2. **静默失败**：有风险——`settings.json` 合并失败或 JSON 不合法时，安装必须报 skipped + Note，不能写坏用户配置（加一支「非法 JSON 不改写」的断言）。
3. **跨平台**：分隔符按 `filepath.ListSeparator`；Windows 不在验收范围。`$.env.get("HOME")` 返回的是桌面端宿主进程的 HOME（真机第二批 #11）。
4. **假红 / 假绿**：轮询节拍用假时钟；「其它会话的信不被删」必须用真的两个前缀夹具。
5. **门禁绕过**：P9（asUser）；`[handoff]` 预填代发对任何以该前缀开头的输入框内容都生效——前缀不是鉴权，但它只作用于本人输入框，新草稿会话没有进程，所以深链 q 不会被自动发出（spec 已验）。
- **序列化边界**：`settings.json` 手写合并 → 往返断言（其它键逐字不变，条 131）。
- **承重安全属性**：信箱前缀隔离（条 118/119）。

---

### 5.9 G1 拉起材料端点（r1 补；审阅阻塞 B1）

1. **生命周期 / 状态机中断**：无，因为 GET 端点无状态、不写账本、不起进程、不建临时文件；请求中途 agentd 重启，客户端只会拿到连接错误，重试即可，没有需要谁来收尾的中间态。唯一的「状态」是读到的项目位置与拉起器登记，它们的生命周期归 L 与 `d_orchestration`。
2. **静默失败 / 误导报错**：有风险，已处置。(a) 无本机位置 → 409，文本逐字可行动（「项目在本机没有登记位置：<project>（handoff project add 登记后可用）」，条 1/2），W 原样显示（故事 8）；(b) 读拉起器登记失败 → 按契约降级为「无 `launcher` 键」+ Warn 日志（条 202）——这是**有意的降级**，用户看到的是「让拉起器代开」不出现；为了不让它变成静默失败，验收要求 Warn 日志带卡号与错误原因（测试抓日志断言）；(c) 卡不存在 → 404（`ledgerErr`），不能落成 500 或 409。
3. **跨平台假设**：有一处，已覆盖。`Folder` 是本 agentd 所在机器的绝对路径；手机或另一台机器上的控制台连到这台 agentd 时，拿到的路径对它自己的桌面端没有意义——故事 8 的禁用态和深链只在本机桌面端有效，W 在移动布局下展示复制兜底（§5.6）。路径分隔符：只在 darwin 真机验收，`Folder` 原样透传，不在服务端改写。
4. **假红 / 假绿测试**：有风险，已处置。(a) 条 4/5 的 `Folder` 期望值必须来自 `resolveCoordWorkdir` 同一解析函数（§11.1 收成一个函数），不能在测试里写死常量——否则两处解析各自漂移时仍然全绿；(b) 条 12「不含一次性回合」是反面断言，同时要有正面断言（简报含三步与 `--via page`），防止「空简报也通过」；(c) 条 13 要在 keystone/scheduling **真的未装配**的 Server 上跑，不能用装配了的 Server 再假设没走 `withCoordinator`；(d) 锁的是调用方依赖的行为（HTTP 码、JSON 键、简报内容），换掉内部模板实现不会无意义地变红。
5. **门禁绕过**：无新增绕过，因为路由挂在 `/api` 既有 token 中间件之后，只套 `withLedger`（契约 §3.9 有意不套 `withCoordinator`，条 13）；它只读，不提供写路径或执行路径，没有检查与动作之间的窗口。需要核对的一点：简报里会带上卡标题和项目根目录——它们本来就对持 token 的控制台可见，不扩大暴露面。
- **序列化边界（追加设问）**：`DesktopLaunchMaterial.launcher` 是可省略键（`omitempty`）。从 Go 结构体到 JSON 再到 TS `launcher?: string` 是一条手写镜像链，「缺键」与「空串」必须区分（W 的显示条件按「非空串」判，条 203）。锁法：**V 卡 DTO fixture** 带与不带 `launcher` 两条样本，Go 生成、TS 读取，逐键比对且用可空类型（§3.4）；G1 自己的 httptest 再断言「未登记 / 读登记失败时 JSON 里没有 `launcher` 键」（解析成 `map[string]json.RawMessage` 查键，而不是解到结构体后看零值）。
- **枚举新值过既有白名单**：无，因为 G1 不引入新的状态名或事件类型；`via=page` 只写进简报文本，不流经任何校验器。新路由 `desktop-launch` 挂在 `registerCoordRoutes` 里，与既有六条路由并列，没有路由白名单要登记。
- **承重安全属性（追加设问）**：「GET 不写任何状态」是承重属性（材料端点会被 5s 轮询反复调，若顺手写了事件或打了标，就会刷爆账本或误唤醒）。锁法：**条 14 的反面断言**——请求前后账本事件总数、`seat_desktops`/`desktop_launchers` 行数、进程内失败段计数表快照全部相等；把任意一处改成「顺手写」都会变红。

## 6. 真机清单（r1 按三批组织；除标「已验」者外全部「未验证，需真机」）

按 P1 裁决设两个中途检查点：子卡齐一批，协调者在本机走一批；I 卡只做第三批与全量回归。

**已验（不再待验）**

| # | 条目 | 读数 |
| --- | --- | --- |
| 0 | launchd 托管的 agentd（LaunchAgent gui 域、ppid=1、最小 PATH）里执行 `open claude://code/continue?session=…` 能到达 GUI 会话、拉起被回收会话，mod 取信提交 | **已验（2026-10-16 18:43，台账「Wave 0 末条真机验」）**：`open exit=0`，会话 70ms warmed，mod 约 4 秒内取信提交，约 8 秒回话。契约欠账 §10.13 与 r0 真机清单第 1 条销账；G2 派发前置条件随之清掉。 |

**第一批：L / C / G1 / W / D 齐 → 故事 1、2、7、8**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 1 | 薄壳内 `handoff:open-deeplink:` → Wails `OpenURL` → `open` → 桌面端草稿（含首次信任框），q 预填简报 | 1 | §2.4 第 1 条只读了源码 |
| 2 | q 在 14,336 字符边界被桌面端完整接收（上限读数未逐字节复核） | 1 | 契约 §1.2 |
| 3 | 桌面端未运行时 `code/new` 深链能否启动应用并进入草稿 | 1 | D 静默失败 |
| 4 | 浏览器（Safari/Chrome）里 `<a href="claude://…">` 的提示与没装桌面端时的表现；复制兜底可见可用 | 1b、7 | W 静默失败 |
| 5 | 新开的桌面端会话能用会话管理工具读到自己的 `local_` id，并按简报完成 `card bind --desktop-session --via page`；卡页变已绑定、timeline 出现 `seat_desktop_set` 且 `via=page` | 2、9 | spec 方案 1 |
| 6 | 项目在本机没有位置时，「用 Claude Code 坐下」禁用且原因逐字显示 | 8 | G1 + W |
| 7 | 手机控制台上「用 Claude Code 坐下」的表现（移动布局下复制兜底） | 1、7 | W 跨平台 |

**第二批：G2 / M 齐 → 故事 3、4**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 8 | 由真实 agentd（非探针脚本）写信 + continue，闲置超过 900s 被回收的会话几秒内醒来，读到命中条与引用条并在群里回复 | 3 | 台账探针用的是脚本 |
| 9 | `open claude://code/continue?session=<已归档 / 已删除 / 不存在的 id>` 的退出码与桌面端行为——**决定 unopened 路径在真机上能否被触发**（退出码对 LaunchServices 很可能恒为 0） | 3、4c | P10、§3.5 |
| 10 | 桌面端未运行时 continue 深链能否启动应用并拉起会话 | 3 | 静默失败族 |
| 11 | 桌面端宿主进程的 HOME 与 agentd 一致，mod 经 `$.env.get("HOME")` 读到同一个信箱 | 3 | §2.4 第 3 条 |
| 12 | `handoff skill install` 写入的 `CLAUDE_CODE_PLUGIN_DIRS`（含 `:` 分隔的多目录）让桌面端新会话加载 mod；原有其它项保留。**前置**：spec 遗留的探针项（`settings.json` 里的探针目录、探针 mod 目录、定时任务与探针会话）待用户定夺并先清理，否则条 130「保留其它项」会把探针目录一起保留 | 3 | §2.4 第 2 条 |
| 13 | 卡上来工单（`task_mirrored` 的 `permission_request`/`question`）或人在终端打 `needs_human` 时，会话被叫醒并**按 handoff skill 分诊表处置**（批准 / 转交 / 升级 / `@user`），且协调者自己打的标不会叫醒自己 | 4、4b | §4 第 4 行（r1 补） |
| 14 | 另一个会话成员（身份与卡主人不同）的群消息经围栏来信进入会话后，会话如何对待其中的指令性文本（P9=丁 第二批先观察；第三批 #20 复验名册授权规则） | 3 | P9=丁（观察项） |

**第三批（I 卡）：G3 齐 → 故事 5、6，并全量回归前两批**

| # | 条目 | 故事 | 依据 |
| --- | --- | --- | --- |
| 15 | 拉起器会话在 CLAUDE.md 常设授权下执行来信：用桌面端会话管理工具在指定目录开新会话并把简报作为首条消息发出 | 5 | spec 方案 3 |
| 16 | 新会话完成 `--via launcher` 坐下；手机上点「让拉起器代开」后面板几分钟内显示已绑定 | 5 | W + G3 |
| 17 | 跨机：席位或拉起器在 mac-02/macbook-pro，由本机认领并转交；`os.Hostname()` 与 target 名对得上；对端不可达时只落一条标、恢复后补投 | 3b、5 | §11.2；spec 备注「三台 agentd 版本不齐」 |
| 18 | 协调者 @ 自己的卡或回复自己的消息被叫醒（机内已由 G2 锁定现状）后，**会不会形成自唤醒循环** | 3 | §5.3（观察项） |
| 19 | 故事 6 的触发与可见（P7=d）：主 agent 跑 `handoff card coordinate <卡> --desktop`，无拉起器时卡上亮起「需要你：无拉起器」、终端打印原文 | 6 | R-B1 / 契约 r7 §4.16 |
| 20 | 围栏来信（P9=丁）在真实桌面端会话里以 `asUser:true` 提交后，会话是否把 `handoff-quote` 围栏内的第三方原文当作数据、只按名册中卡主人或用户本人身份的授权执行不可逆 / 外部可见动作（观察项） | 3、4 | 契约 r7 §4.17、D7 |

共 **20 条待验**（第一批 7、第二批 7、第三批 6）+ **1 条已验**。

---

## 7. 出稿自检

- [x] **产出四样齐全**：§1 子系统清单每项带 best.json 类型（图外两项标边界型），派卡资格四条逐个核；§2 契约增量逐条（222 条 + 欠账 + §11）有归属，退回 contract 四项（R-B0–R-B3）、边界澄清八条；§3 九张卡（L/C/G1/V/G2/G3/W/D/M）+ I 全部四段式、判据行为化；§5 缺陷族逐卡逐族作答（r1 补 §5.9 G1），「无」均写了「无，因为……」。
- [x] **「待拍板」岔口集中**：P1–P10 全在 §0，正文回指。
- [x] **「未验证，需真机」汇总**：§6 按三批共 20 条待验 + 1 条已验（launchd，r1；r2 增 #20）。
- [x] **有界文件集**：每张卡写出了显式文件清单与禁止区；架构法第三条命中已正面回答（§1.1，P4），没有硬塞进功能卡的不可圈集合。
- [x] **行为闭环**：§4 每行五格；故事 6 的触发者缺失已标出并退回（R-B1），没有按已闭环处理。
- [x] **上游状态位**：spec 已批准在位；contract 头部状态行失真已列 R-B0。
- [x] **未亲自跑到结果的命令没有写成结论**：本轮亲跑 `codegraph check/validate/resolve`、`find`/`wc` 规模统计、Wails/plugin-authoring 源码与文档读取、`launchctl list`/`ps`；行为事实一律进 §6。
- [x] **收尾**：`codegraph resolve --doc docs/superpowers/specs/b434-breakdown.md` 亲跑，读数落台账，坏锚已修。

---

## 8. 图覆盖债

- `desktop/`、`skills/`、`plugins/` 在扫描配方里被排除或没有容器，相关锚只能 `moved`（源码文本再锚定）；这是现状，不是坏锚。
- 图内大量 `Server.*`/`Store.*` 方法节点缺失：例如 scheddrain.go 的 Server.wakeCoordinatorRoundID、coordapi.go 的 Server.handleCoordWake、wakeconsumer.go 的 Server.roomMessageWakeEvents、pushfanout.go 的 PushFanout.OnLedgerEvent、binding.go 的 Store.clearSeatTx，带接收者写法查询都是 `vanished`，裸名才能 `moved`。源码都在（台账读数），属**图未覆盖**，和契约 §10.3 同类。
- Ticket 0 的新符号（`internal/agentd/desktopwake.go` 全部、`internal/ledger/seat_desktop.go` 全部、`internal/proto/desktop_coordinator.go` 全部、`web/src/app/lib/claudeDeepLink.ts`、`web/src/app/cards/desktopSeatCopy.ts`）不在基线图里，也没有分支视图 diff。
- 处置：图对账列（I 卡）按 `docs/codegraph-scan-recipe.md` 产出 `codegraph/diffs/cards-B434-charter-1.json`，再跑 `validate --view` 与 `resolve --doc`（本稿 + 契约）到零 vanished/file_missing。

---

## 9. 建议的子卡标题与附件（供协调者 `handoff card split B434 "<标题>"`）

| 提案号 | 建议标题 | 应挂附件 | 执行位置建议 |
| --- | --- | --- | --- |
| L | `B434 账本层：seat_desktops 与 desktop_launchers 两表、CAS 写面与换绑清座联动` | 契约 §3.1（ValidateDesktopSessionID）、§3.3、§3.4、§8 D1/D2/D4、§4.4 条 49–59/62–63/65、§4.9 条 139–143；本稿 §3.1、§5.1 | 任意 |
| C | `B434 命令面：card bind --desktop-session、card seat desktop set、launcher、card needs 席位 actor、status 拉起器段` | 契约 §3.10、§3.8.1（席位自打前提）、§4.4 条 42–48/60–61/64、§4.9 条 139–143/159–161、§4.11 条 189–190、§4.16 条 252–260/262、§4.17 条 277–278、`internal/skill/desktop_rule.go`、欠账 §10.18（CLI 一侧）、§11.2；本稿 §3.2、§5.2；P6/P7 裁决 | 任意（测试在 `env -u CLAUDE_CODE_SESSION_ID` 下跑） |
| G1 | `B434 网关 S1：GET desktop-launch 拉起材料与简报` | 契约 §3.2、§3.5、§3.9 GET 行、§4.1、§4.12 条 200–202、§11.1/§11.5；本稿 §3.3、§2.4 第 7/8/9 条、§5.9 | 任意 |
| V | `B434 直通竖切 TestB434DesktopWakeVerticalSlice + DTO fixture`（协调者 / contract 执行者，不进并行子卡） | 契约 §9.3、§6 DTO 行、§10.4/§10.5/§10.18（渲染一侧）、§3.6（r7 模板）、§4.17 条 263–275/280、`internal/agentd/testdata/b434_desktop_letter.golden`；本稿 §3.4、§2.4 第 9/10 条、§5.5 | 协调者 |
| G2 | `B434 网关 S2：唤醒信箱、continue 深链、分流改接、失败段与 needs_human 唤醒` | 契约 §3.6、§3.7、§3.8、§3.8.1、§4.5、§4.6、§4.11（除 189/190）、§4.17 条 263–280（保持绿）、`internal/agentd/testdata/b434_desktop_letter.golden`、§8 D3/D5/D6/D7、§11.3/§11.4；本稿 §3.5、§2.4 第 4/6 条、§5.3；§6 已验条 0 | 任意（launchd 已验，无前置） |
| G3 | `B434 网关 S3：拉起器代开端点与 /api/desktop/deliver` | 契约 §3.6 拉起器来信模板、§3.9 POST 两行、§3.9.1、§4.9 条 144–158、§4.13、§4.15 条 243–251、§4.16 条 261；本稿 §3.6、§5.4 | 任意 |
| W | `B434 控制台：深链编码、薄壳桥、用 Claude Code 坐下与让拉起器代开、复制兜底` | 契约 §3.12、§4.2 条 15–23、§4.12 条 203–210（P7=d，显示条件不变）、§4.14 条 223–242；本稿 §3.7、§5.6 | 任意 |
| D | `B434 薄壳：handoff:open-deeplink 通道与 claude:// 白名单` | 契约 §3.11、§4.3 条 30–41；本稿 §3.8、§2.4 第 1 条、§5.7 | 任意（desktop 子模块只构建 `./internal/...`） |
| M | `B434 mod：claude-desktop-wake 正式版与 skill install 安装` | 契约 §3.13、§6 第 2/3 行、§4.7、§4.8、条 163、条 279（`asUser:true` 断言；P9=丁 对 mod 行为无改动）；本稿 §3.9、§2.4 第 2/3 条、§5.8；P5 裁决 | 本机（P5=甲） |
| I | 父卡 integrate（不 split；在 B434 本卡执行；只做第三批真机与全量回归） | 本稿 §4、§6、§8 | 协调者本机 |

### 拍板记录区（r1 回填，2026-10-16）

| 编号 | 裁决 | 理由 |
| --- | --- | --- |
| P1 | 甲（协调者） | 按故事切会让 `desktopwake.go`、`coordapi.go` 各有三个写者；S3 实际依赖 S1。补两个中途真机检查点（§6 分三批）。 |
| P2 | 甲（协调者） | V 在 L 之后、G2 之前，由协调者执行，顺带清 DTO fixture。 |
| P3 | 甲（协调者） | 三条外部读数由协调者认定、回写契约 §10 销账；launchd 一条已于 18:43 真机验通。 |
| P4 | 甲（协调者） | 不插还债卡；命中与回答写进 G2 图对账备注。 |
| P5 | 甲（协调者） | M 卡本机执行。 |
| P6 | 甲（协调者） | skill 文本并入 C；G3 改了文案相关行为则 I 卡复核。 |
| P7 | **d**（用户，2026-10-16） | 不动 P4 显示条件（「让拉起器代开」仍只在已登记时出现）；故事 5 本就落在主 agent 回路，故事 6 的触发者改为主 agent 的 `handoff card coordinate <卡> --desktop`，无拉起器 → 409 + needs_human。契约 r7 §4.16 条 252–262。 |
| P8 | 甲（协调者） | 退回 contract r6 补「坐下」按钮原子条；按纪律属「缺契约面」，不是偏好岔口。 |
| P9 | **丁**（用户，2026-10-16） | 保留已验通的 `asUser:true` 通路；`desktopLetter` 把第三方原文放进带作者与 seq 的围栏块并转义，信首列作者名册，skill/CLAUDE.md 授权规则限定只有卡主人或用户本人身份能授权不可逆或外部可见的动作——把第三方原文从「人本人的话」降为带来源的数据。契约 r7 §4.17 条 263–280、拍板 D7。 |
| P10 | 甲（协调者） | 接受已知限制；opener `Run()` 属实现细节。 |

（全部岔口已裁，头部状态行「已拍板（2026-10-16）」与本表一致。R-B0/R-B2/R-B3 已由契约 r6（`35334dbb`）回写；R-B1/P7、P9 已由契约 r7（`30979524`）补签。）

> **修订记录（r3，2026-10-16，跨卡审计回写）**：第一波 plan（L/D/M/W）跨卡审计 pass-with-notes；I 卡补 PG 往返项；G2/C/V plan 前置约束写进 §3.10。D 卡 Task 2（`main.go` 接线）无缝级断言、以源码守卫 + overlay 编译 + 真机 R1 替代，协调者认可豁免（`package main` 闭包从声明缝构造不出）。

> **修订记录（r4，2026-10-16，第二波跨卡审计回写）**：C/G1/V plan 跨卡审计 pass-with-notes；契约 r10 吸收 13 条。本稿更正 §3.2④ `card_driver.go` 笔误。**G2（B434.5）plan 前置约束**：①`desktopWakeStall` 必须并入 G1 的 `b434StallSnapshot`（条 14），G2 文件清单含 `internal/agentd/desktopwake_material_test.go`；②新增「不收尾」分支须照准入停摆分支设 `firstWakeErr`，否则 V 的竖切子例「信箱键不安全 → `desktopUndelivered`」变红；③`writeDesktopLetter` 用 `rename` 覆盖，同会话同纳秒双写丢信（条 68）归 G2 处置；④条 86–88 祖先接手已由 V 实现并锁，G2 首跑即绿须附变异读数；⑤条 266/268/275 命名测试归 G2（契约 r10 第 12 条）；⑥G2 复用 V 的 `Server.desktopHome` / `desktopOpenerSpy.err` 注入点（r10 第 11 条），不另造夹具；⑦队列出队路径对桌面端席位不写信（r10 第 10 条），G2 的异机转交分支同样不得阻断清队。**I 卡**必须在 G2 合入之后（G2 合入前生产 `desktopOpen` 为 nil，写信不发深链）。
