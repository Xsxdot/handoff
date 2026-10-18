# B435 台账：协调者统一模型（spec 节点）

卡：B435「协调者统一模型：两种驱动模式、一个 wait、驻场者与项目房间组织者」（建于 2026-10-18 13:51 KST，`handoff card add` → `B435`，workflow=charter v13；13:52 `card move B435 spec`；note seq 24314 记来源与目标形态）。

本文件只记过程与原始读数；裁决与结论在 `b435.md` 正文。

## 一、来源对话（2026-10-18，本会话，逐条裁决）

1. 「print 一次性 vs ACP 对话」不是二选一：机器协调者留 print（续跑同一 session 的有界回合），对话形态只给有人在场的席位。依据：寿命不匹配（卡活数周、进程活不了）、grok ACP §5.2 实测（未决权限随连接消亡、`session/load` 不重发、回合永久挂死，`internal/orchestration/manager.go:3681` 为此引入 `volatilePermitter`）。
2. 用户期望形态：开房间选协调者（载体或小队，print 模式）→ 像对 CLI 说话一样说话 → 房间里只见重点与结果、同事口吻 → 协调者点火/派发 → 大任务建子卡配协调者、只与子卡协调者沟通 → **协调者被房间唤醒，不自己挂订阅**。
3. 桌面端：不当卡协调者，改作「组织者」——项目级、无目标、常驻可恢复、自己不工作，只排期/建卡/开协调者。B434 的信箱/mod/登记复用为桌面端的 wait 实现，卡级席位退役。
4. 两种驱动模式是必然（其他 agent 只能靠 `card wait` 与 handoff 交互）：驱动式（agentd 推 print 回合）与自驱式（agent 自己 `wait`）。桌面端 mod = 自驱式的桥。
5. `session wait` 当初的用途是「驻场」：在 handoff 不认识的 agent 里开会话、接到房间，手机上通过房间和它说话。裁决：保留能力、删第二个命令——一个 `wait` 动词按地址匹配；驻场者是房间级无席位收件人。
6. 分界三条（用户点名第三条「非常有用」，解决「每次 @ 很麻烦」与「怕其他协调者往群里丢信息频繁唤醒」）：驻场无席位不起回合；驻场可 `card bind` 升格；无寻址人类消息只送一个默认收件人（有驻场者送驻场者，否则送目标卡协调者；找别人显式 @）。
7. 组织者持久态三样：规则文件（git，人写）、记忆目录（组织者写、人可改）、项目房间游标。会话上下文允许丢，三样不许丢。
8. 组织者回答协调者问题：组织类（先后、等待、基线、机器、卡间冲突）直接答回卡房间；内容类（设计、验收、范围）转问人；人已在房间答了则不答。
9. 与组织者沟通 = 项目房间（每项目一个，不挂目标卡，组织者是其驻场者）；handoff 给组织者的「信」= 系统往项目房间发消息；UI 只是把项目房间嵌进项目页。
10. 用户 13:4x：「OK，我觉得没有问题了」→ 进 spec。spec 在本会话出稿（非拉群叫协调者）：本卡改的正是协调者唤醒回路本身，且 spec 对话已在本会话完成；卡生命周期照走。

## 二、现状读数（原始出处；行号为 2026-10-18 工作树 `c944b6b7` + 未提交 ledger 改动）

### 2.1 print 回合与简报
- `internal/keystone/keystone.go:334-350` `briefing`：原文「你是本卡的机器协调者。**你是一次性回合**：没有交互窗口，本轮跑完即结束；下一次有事会被再次唤醒（续跑同一会话）。需要人知道的事**发到 IM 房间**…不适合现在推就在房间说明原因并结束本轮」。
- `internal/hostapi/driver.go:240-249` `buildArgv` = `[bin run --format json -m <model> -s <session> -- <prompt>]`——print 模式**续跑同一 session**，不是无状态。
- `internal/agentd/scheddrain.go:272,281,306` 注释三处「print 一次性回合」；`:233-343` `launchCoordinatorRound*`。
- B399 spec（`docs/superpowers/specs/b399.md`）：回合上界 30 分钟 + 回合内认领续租；超时保留 session；简报只带增量。

### 2.2 bind 席位跳过（三处）
- `internal/keystone/keystone.go:123` `if seat.DriverSource == SeatSourceBind { s.Forget(card); return zero, nil }`。
- `internal/agentd/wakeconsumer.go:489-491` `resolveWakeCard` 注释「事件卡是 bind → 空（bind 靠 CLI wait）」。
- `internal/agentd/scheddrain.go:451-454` `wakeCoordinatorRoundID` bind → `keystone.Forget` + 日志「bind 席位跳过协调者唤醒」。
（出处：B434 契约 `b434-contract.md:32,41,42` 与本会话 sed 复核。）

### 2.3 唤醒判定与消费者
- `internal/agentd/wakeconsumer.go:216` `automationWakeEvent`：task_mirrored（策略过滤）、decision_*；`needs_human`/`needs_cleared`/`seat_bearing_missing` **恒不唤醒**（`b434-contract.md:45`）。
- `internal/agentd/wakeconsumer.go:330` `roomMessageWakeEvents` 经 `MessageWakeTargets`。
- `cmd/card_wait.go:238-249` room_message 只判 `kind==user && !by_system`，按本卡 `card_id` 过滤（**不走** `MessageWakeTargets`）。
- `cmd/session.go:501-515` `sessionWaitMatch` 经 `MessageWakeTargets`、member 逐字相等。
- `handoff session wait <member>` 帮助原文：「订阅会话寻址：续收积压摘要或实时 SessionWake（缺省一条即退 0；--follow 常驻）」——按**成员身份**订阅，不按房间。
- `handoff card wait <id>`：「跟随卡的账本事件流（--subtree 跟整棵子树），可动作事件一行一唤醒」。
- B434 契约 `b434-contract.md:78`：`EvNeedsHuman` 两个消费者（`card wait` 可动作 / `automationWakeEvent` 不唤醒）判定相反——退回 spec 候选 R1。

### 2.4 寻址（B358）
- `internal/collab/sessions.go:220` `MessageWakeTargets`：by_system/pointer → 空；`replyAuthorOf` + `resolveMessageTargets`（@卡号 → 该卡 `DriverSession`；解析不到卡按外部身份原样）→ `room.ResolveDelivery`。`:262` `AddressesCard`：卡无席位恒 false「宁缺毋滥，杜绝广播回潮」。
- `internal/proto/rooms.go:28-48` `RoomMessage`：`Room`（会话号 | 旧卡号 | project:<name> | global）、`Mentions`、`ReplyTo`、`BySystem`。注释「两者皆无 => 不唤醒任何人（落账、进未读）」。
- `docs/superpowers/specs/b358.md:27` 冻结规则 2「投递寻址化…无寻址的发言**不唤醒任何人**。系统永不按成员集合投递」；`:74`「无寻址发言：落账、进未读，不唤醒任何人」；`:76` 系统结构事件（含 `needs_human` 亮起）不唤醒；`:110` 源码级守卫。
- 守卫测试：`internal/collab/room/delivery_gate_test.go:25 TestResolveDeliveryShapeHasNoMemberSet`、`:118 TestWakePathSourceUsesAddressing`；`internal/agentd/wakeconsumer_b358_test.go:62 TestB3583NoAddressingDoesNotWake`、`:253 TestB3583MixedHitsNeverMergeOrBroadcast`、`:326 TestB3583CardRoomMessagesDoNotWake`、`:356 TestB3583WakePathSourceGuard`。

### 2.5 会话与房间模型
- `internal/ledger/sessions.go:1-7`：会话是工作单元；一卡同时只挂一个会话；成员由「显式人与主 agent + 会话内各卡当前席位」派生；席位权威仍在 `cards.driver_session`。
- `internal/ledger/store.go:369-379` `sessions(id,title,owner,archived,members,…)` + `session_cards`——**sessions 没有 project 列**（`grep -i project internal/ledger/sessions.go` 零命中）。
- `internal/collab/service.go:189`：旧房间（卡 / `project:<name>` / global）**只读归档，一律 ErrReadOnly**（B358 P5 拍板）；`:830-834` 会话列表仍把 `project:<p>` 与 `global` 作为条目列出。
- `internal/collab/room/room.go:124-145` `isGroupRoom` / `groupRoomKind`。
- `handoff session send` flags：`--mention`、`--reply-to`、`--agent`、`--cli/--session` 成对出示协调者席位。

### 2.6 B434 桌面端（退役候选）
- `b434-contract.md:97-103` 落点：`internal/agentd/desktopwake.go`（写信箱/深链/简报/拉起器代开）、`internal/ledger/seat_desktop.go`（`seat_desktops`、`desktop_launchers`）、`internal/proto/desktop_coordinator.go`（`DesktopWakeMarker="[handoff]"`、`DesktopWakeDirRel=".handoff/desktop-wake"`、`SeatDesktopVia{Page,Launcher,Manual}`）、`cmd/card_seat_desktop.go`、`cmd/launcher.go`、`plugins/claude-desktop-wake/`。
- `b434-contract.md:114`：桌面端席位分支对 `keystone.Wake`/`LaunchForCard` 零调用（绕开 keystone）。
- `b434-ledger.md` 真机批次 1/2：定时任务生的无人值守会话被桌面端拒绝 `start_session`（「Starting sessions requires a user at the keyboard」）；人开的会话可代开；信从落盘到被取走 ≤16s（5s 轮询）；桌面端会话闲置 900s 回收。
- `docs/superpowers/specs/b434.md:33`：拉起器登记 + 项目 CLAUDE.md 常设授权文本（`internal/skill/skill_text_b434_test.go:94-108` 锁定）。

### 2.7 ACP 先例
- `docs/superpowers/specs/2026-08-08-handoff-grok-adapter-design.md:46-51,305-315`：权限请求悬挂、重连不重发、`ask_user_question` 不应答回合永久挂死。
- `internal/orchestration/manager.go:3681-3690` `volatilePermitter`。

### 2.8 图
- `codegraph/best.json` 领域 id：d_orchestration、d_gateway、d_workspace、d_execution、d_sessions、d_transport、d_protocol、d_ledger、d_collab、d_cli、d_web、d_policy、d_maintenance、d_scheduling、d_keystone 及子域。`codegraph` 二进制本机不在 PATH（`command not found`，先例 `docs/ledger-b219.md:13`）；本节点以 grep/sed 复核，**图覆盖债**：本轮未跑 `context/sym/flow`。

## 三、派出的只读调查
- 2026-10-18 13:52 派 Explore subagent 做「席位来源 × 唤醒路径 × 消费者 × 身份记法 × B434 件」五表清单（带 `file:line`）。结果待回填本节。

### 三.1 回填：五表清单（Explore subagent，2026-10-18 13:52–13:57，42 次只读调用；行号已对照实际文件）

#### 表 1 — 席位来源

| 来源 | 定义处 | 写入处（CLI / HTTP） | 在决策中被读取的位置 |
|---|---|---|---|
| `SeatSourceBind="bind"` | internal/proto/seat.go:13 | `card bind` → `BindSeat(..., SeatSourceBind, SeatBearing{})` cmd/card_driver.go:65；`card rebind --self` → `RebindSeat` cmd/card_driver.go:163（之后 `CoordinatorForget` :172） | keystone.go:123；wakeconsumer.go:521（resolveWakeCard）；scheddrain.go:463（wakeCoordinatorRoundID）；desktopwake.go:1053（desktopNeedsHumanWakes）；seat_desktop.go:85（SetSeatDesktop 只允许 bind） |
| `SeatSourceCoordinate="coordinate"` | internal/proto/seat.go:14 | HTTP `POST /api/cards/{id}/coordinator/launch`（coordapi.go:44 → :107 → scheddrain.go:233 → `BindSeat(...SeatSourceCoordinate, coordinatorBearing)` :328）；`.../coordinator/rebind`（coordapi.go:46 → :241 → `RebindSeat` scheddrain.go:325）；CLI `card coordinate`（cmd/card.go:462/475）、`card rebind --launch`（cmd/card_driver.go:187）；launch_queue 出队（scheddrain.go:141-142） | keystone.go:202（LaunchForCard 只接受 coordinate）；scheddrain.go:243；binding.go:175、:217-224（coordinate 必须带 carrier+machine，bind 必须零承载） |
| `SeatBearing`（coordinate 的承载记录） | binding.go:201；写 :231；读 :260 | 随 BindSeat/RebindSeat 同事务（binding.go:64、:130）；补写 `card seat bearing set --carrier`（cmd/card_bearing.go:30/82） | scheddrain.go:496（缺失 → reportSeatBearingMissing :402 → EvSeatBearingMissing + needs_human）；:510（远端机器 → transferCoordinatorWake） |
| 桌面端席位（`seat_desktops` 行，挂在 bind 席位上） | 表 store.go:340；`SeatDesktop` seat_desktop.go:37-39 | `card bind --desktop-session`（cmd/card_driver.go:69-70，两步提交）；`card seat desktop set`（cmd/card_seat_desktop.go:162/199）；删除：RebindSeat binding.go:135，clearSeatTx binding.go:294 | resolveWakeCard wakeconsumer.go:522；wakeCoordinatorRoundID scheddrain.go:467；desktopNeedsHumanWakes desktopwake.go:1057 |
| 空座 | — | clearSeatTx binding.go:282 | keystone.go:119；scheddrain.go:458；openViaDesktopLauncher desktopwake.go:1230（必须空座才能代开） |

#### 表 2 — 唤醒路径

| 路径 | 入口函数 | 过滤与判定 | bind 跳过 / 桌面端分叉 |
|---|---|---|---|
| 账本自动化事件 | 消费循环 wakeconsumer.go:722 → `automationWakeEvents` :301 → `automationWakeEvent` :216 | 唤醒：`task_mirrored`（`acceptsCurrentWorkflowAttempt` :705、`WaitDeliveryPolicy` :223；permission_request/question → WakeTicket :230）、`decision_opened/answered` :256。不唤醒：`status_moved` :238、`needs_cleared` :247、`needs_human`/`seat_bearing_missing` :261-264、default :265；无卡事件 :305 | 目标卡由 resolveWakeCard :748 决定，空串时 :749 日志「无 coordinate 席位可叫醒」 |
| needs_human（B434 例外） | automationWakeEvents :309 → desktopNeedsHumanWakes desktopwake.go:1043 | 只唤醒「有桌面端记录的 bind 席位」，排除系统 actor（:1019-1031）与席位自打的标（:1069）；命中后伪装成 WakeTaskTerminal（:1078） | coordinate 目标不唤醒 :1053；无记录的 bind 不唤醒 :1061 |
| 会话房间消息 | roomMessageWakeEvents wakeconsumer.go:337 | `decodeRoomMessageForWake` :275（by_system、pointer、`by_system:null` 不唤醒，**不看 kind** :273）；只处理 `IsSessionRoom` :347；`MessageWakeTargets` :352（collab/sessions.go:220 → room/delivery.go:30）；target 与会话内各卡 `DriverSession` map 匹配 :366-386，不分 bind/coordinate；未命中外部身份只 Info :397 | WakeMessage 仍经 resolveWakeCard，bind 且无桌面端记录时被丢弃 |
| HTTP 手动拉起 | handleCoordLaunch coordapi.go:107 → launchCoordinatorRound scheddrain.go:233 | source 必须 coordinate（:120 / :243）；席位非空 CAS 冲突 :254；LaunchAdmit :268；载体必须在本机 :284 | 不经 Wake 路径 |
| launch_queue | drainQueuesOnce scheddrain.go:102，case :141 | 无位延后回填 :143-148 | 同上 |
| ignition_queue 出队 | drainIgnitionRequest scheddrain.go:178 | keystone.Decide :188 → wakeCoordinatorRoundID :201（raws=nil） | 桌面端席位不写信直接放行 scheddrain.go:479-484 |
| 换绑 | handleCoordRebind coordapi.go:241 → scheddrain.go:237 | 只接受 mode=launch :252；空座 409 :261 | self 换绑只能走 CLI（:248） |
| 跨机转交 | 发起端 transferCoordinatorWake scheddrain.go:361（调用点 :522；桌面端 desktopwake.go:960）；收件端 handleCoordWake coordapi.go:178 | 收件端 CAS `card.DriverSession != req.Seat` → 409 :195；automationWakeEvents 重映射 :202；`withForwardedWake` 一跳封顶 :216；`LetterUnopened` 回 200 :217-223 | 收件端也走 wakeCoordinatorRoundRaw，内含 bind 跳过 |
| 桌面端信箱 | wakeCoordinatorRoundID scheddrain.go:478-486 → wakeDesktopSeat desktopwake.go:882 | 异机 → wakeRemoteDesktopSeat :950；本机：desktopLetter :903 → writeDesktopLetter :911 → claudeContinueURL :922 → desktopOpen :929 | — |
| 拉起器代开 | `POST .../coordinator/desktop-launch` coordapi.go:55 → desktopwake.go:1341 → openViaDesktopLauncher :1224 | 必须空座 :1230；无拉起器打 needs_human :1239 | — |

**bind 席位跳过——四处（非三处）**：

| # | 位置 | 判定 | 措辞 |
|---|---|---|---|
| 1 | keystone.Wake keystone.go:123-126 | bind → Forget，返回 nil | 不打日志（函数注释 :100-101） |
| 2 | resolveWakeCard wakeconsumer.go:521-539 | bind：有桌面端记录 → 返回本卡；起始卡无记录 → `""`；祖先无记录 → 继续沿 parent 上走 | 上层日志 :750「无 coordinate 席位可叫醒」（已过时：有记录的 bind 也会被返回） |
| 3 | wakeCoordinatorRoundID scheddrain.go:463-490 | bind：有记录 → 桌面端分叉；无记录 → Forget | :489「bind 席位跳过协调者唤醒」 |
| 4 | desktopNeedsHumanWakes desktopwake.go:1053 | 非 bind → 不唤醒 | 「目标是 coordinate 席位（B394）」 |

resolveWakeCard 在 needs_human 路径被调两次（desktopwake.go:1044、wakeconsumer.go:748）。

#### 表 3 — 消费者

| 消费者 | 匹配函数 | 规则 | 比较的身份记法 |
|---|---|---|---|
| agentd 消费循环 | wakeconsumer.go:722→748→806（ClaimWake wakeclaim.go:31）→841（Decide keystone.go:87）→863 | Decide 只因 attach 或未知 kind 拒绝（keystone.go:90-95） | 卡上 `DriverSession` 原值 `cli:<cli>#<sid>`（wakeconsumer.go:374-382） |
| `handoff card wait` | cardWaitEventActionable cmd/card_wait.go:233 | `needs_human / needs_cleared / decision_*` 全部可动作 :235-237；room_message 要求 `kind==user && !by_system`，`by_system:null` 不可动作 :249-253；task_mirrored 走 JudgeMirroredWake + WaitDeliveryPolicy :269-285。卡过滤：`st.Follow(ctx, members…)` card_wait.go:160 → `EventsFromAsc(ids…)` follow.go:55，只取 card_id 属于成员集的事件（follow.go:13-14） | 不比较身份（只按卡号成员集过滤） |
| `handoff session wait <member>` | sessionWaitMatch cmd/session.go:501 | 只处理 room_message 且 `IsSessionRoom` :509；`MessageWakeTargets` :512；`target == member` 严格相等 :516-517；候选读 :143，seat revision 护栏 :136/:162 | 位置参数原样比较（session.go:67/74）；可能传 `user:/agent:`，也可能传 `cli:<cli>#<sid>` |
| 桌面端 mod | plugins/claude-desktop-wake/hooks/register.ts:40-73；`isOwnLetter` :34 | 文件名 `<sessionId>.<19 位数字>.md` :37；先删再提交 :14；`submitPrefill` 草稿以 `[handoff]` 开头 :87 | `$.session.id()` :44 = CLI 会话 id，与席位身份 `#` 后半段相同（desktopwake.go:899） |

#### 表 4 — 身份记法

| 记法 | 生成处 | 校验 / 比较处 |
|---|---|---|
| 席位 `cli:<cli>#<session_id>` | `EncodeSeatIdentity` proto/seat.go:41；`currentSeatIdentity` cmd/card_seat.go:23（flag → `HANDOFF_SESSION_CLI/ID` :45-46 → `GROK_SESSION_ID`/`CLAUDE_CODE_SESSION_ID` :67-68）；agentd scheddrain.go:320 | `ParseSeatIdentity` seat.go:53；`ValidateSeat` seat.go:20；wakeconsumer.go:375/382；coordapi.go:195；desktopwake.go:1069 |
| 无头续接注入环境 | agentd/server.go:1592-1593 | card_seat.go:45-65 |
| CLI 审计 actor `cli:<user>@<host>` | `ledgerActor` cmd/ledgercli.go:44-50 | 不作席位（seat.go:39-40）；`session send` 默认 actor（session.go:892） |
| 成员 `user:<n>` / `agent:<n>` | `MemberIdentity` proto/identity.go:45；`session send --agent` session.go:884 | `ParseMemberIdentity` identity.go:57；`session create --owner` session.go:587；正文 @ 解析 session.go:865 |
| 桌面端 `local_<…>` | 手填 `--desktop-session`（card_driver.go:203、card_seat_desktop.go:211、launcher.go:190） | `ValidateDesktopSessionID` proto/desktop_coordinator.go:93；深链 desktopwake.go:552 |
| 拉起器 `cli_session` | launcher.go:74/79/104 | 信箱键 desktopwake.go:1246 |
| 转交 holder `<host>#<pid>` | wakeClaimHolder wakeconsumer.go:552-557 | 日志与认领 |
| hostapi 回合 session id | hostapi/driver.go:324-325；`-s` 续接 :245-246；**CLI 白名单只有 opencode :52** | scheddrain.go:320 编码成席位身份 |

WARN 原文 internal/collab/sessions.go:393-394：`log().Warn("会话成员身份非统一记法，kind 按 human 兜底", "session", session.ID, "identity", identity, "cause", err)`——`session.Members` 某项 `ParseMemberIdentity` 失败（如历史 `web:` 或 `cli:user@host`）。

#### 表 5 — B434 桌面端部件

| 部件 | file:line |
|---|---|
| `desktop_launchers` 表 | store.go:348（方言副本 :532） |
| Register / Of / Unregister / List | seat_desktop.go:201 / :229 / :247 / :272 |
| `launcher register/show/unregister` | cmd/launcher.go:69（必须 claude 会话 :84，写入 :102）/ :126 / :166；注册 :197-198 |
| `seat_desktops` 表 | store.go:340；`SetSeatDesktop` seat_desktop.go:64（只允许 bind :85，CAS :90）；`SeatDesktopOf` :130；via 词表 proto/desktop_coordinator.go:44-46 |
| `card bind --desktop-session/--machine/--via` | cmd/card_driver.go:203-205，两步提交 :65 / :70 |
| `card seat desktop set` | cmd/card_seat_desktop.go:162-208，只允许 claude（:32） |
| `card coordinate --desktop` | cmd/card.go:600、:468、:491/:499 → `POST desktop-launch` coordapi.go:55 → desktopwake.go:1341/1224 |
| 拉起材料 | `GET desktop-launch` coordapi.go:52 → desktopwake.go:186 / :88；简报原文 :172 |
| 信箱目录 | proto/desktop_coordinator.go:23；register.ts:20 |
| 信件文件名 / 写入 | `<cliSession>.%019d.md` desktopwake.go:454；tmp + O_EXCL + Link :428-456；拒不安全键 :439-444 |
| 信件内容 | 首行 `[handoff] 卡 X 有 N 件新事（账本 seq a–b）` desktopwake.go:238；尾行 :257-258 |
| mod 轮询 | `FIRST_CHECK_MS=1500` register.ts:22；`POLL_MS=5000` :24；plugin.json:4、hooks.json:1；内嵌 main.go:36/40 |
| 深链 | `claude://code/continue?session=` proto :34 → desktopwake.go:551；`claude://code/new?` proto :33、web/src/app/lib/claudeDeepLink.ts:21 |
| 失败段 / 退避 | desktopwake.go:1126-1199；wakeconsumer.go:830、:882-908；换绑清除 :731-734 |

#### 矛盾与重复（subagent 观察，本会话采信）

1. `needs_human` 三个口径：card wait 可动作（card_wait.go:235）；automationWakeEvent 排除（:261-264）；automationWakeEvents :309 为桌面端 bind 开例外（desktopwake.go:1043）。`needs_cleared` 同。
2. bind 跳过四处，措辞与行为各异；resolveWakeCard 起始卡 bind 返回空、祖先 bind 继续上走，不对称。
3. **两套 room_message 判据，且 `card wait` 永远收不到会话房间消息**：会话房间消息 card_id 恒为空（collab/service.go:271），card wait 按 card_id 成员集过滤（follow.go:13-14）→ 其 room_message 分支只对旧卡房间有效。
4. 三份「谁被唤醒」匹配：agentd `bySeat[DriverSession]` map（wakeconsumer.go:366-386）；session wait `target == member`（session.go:516）；`AddressesCard`（collab/sessions.go:258-275）。
5. 身份记法混杂：session wait 的 `<member>` 按设计是 `user:/agent:`（session.go:3、:562），寻址解析出的 target 却是 `cli:<cli>#<sid>`（delivery.go:52）；bind 协调者要收会话消息只能 `session wait cli:claude#…`，该值不在成员记法里（identity.go:6-8）。
6. 「coordinate」一词两义：`card coordinate` 产生 SeatSourceCoordinate；`card coordinate --desktop` 产生的是 bind 席位（简报要求 `card bind --via launcher`，desktopwake.go:172）。
7. launch_queue 无首次生产者（只有 requeueAutomation 自回填 scheddrain.go:115、:221；Enqueue 唯一首次调用是 ignition_queue scheddispatch.go:107）。
8. **CLI 物种互斥**：coordinate 席位经 hostapi 拉起只支持 opencode（driver.go:52）；桌面端席位只接受 claude（card_seat_desktop.go:32、launcher.go:84）。
9. queue_release 分支不一致：桌面端 bind 直接放行（scheddrain.go:479-484），远端 coordinate 显式失败（:511-520）。
10. 桌面端记录两步写入（card_driver.go:65/:70，可能半态）、三个删除点（binding.go:135、:294；wakeconsumer.go:731-734）。

## 四、独立审计（2026-10-18 14:0x，general-purpose subagent，只读，19 次调用；本会话逐条核实后采信）

### 四.1 审计裁决摘要
1 承重歧义 fail；2 骨架 pass（roadmap 口径一处存疑）；3 定级 pass（漏冻结 `card wait` 输出与信箱格式）；4 架构存疑（wait 走 HTTP 改写了 B358 条 45/49；§7/§8/D10 现状引用缺行号）；5 读数分界 pass；6 抽查 14 条读数相符、1 条偏差（`manager.go:3681` 应为 `:3685`）；7 契约相容 fail（B358 条 14/26/31/39–46/47 未列；B434 退役漏路由、控制台按钮、薄壳深链、`LetterUnopened`、`continue` 深链；B399 约束未写）；8 假缝 pass（桌面端登记入口无名）；9 故事存疑（缺升格、无人监听、模式可见性）；10 Wave 0 fail（S4 隐含依赖 S3；矛盾 7/9/10 未处置）。

### 四.2 本会话核实与处置（必须改九项）
| # | 审计主张 | 核实 | 处置（r2） |
|---|---|---|---|
| 1 | `handoff wait` 与 `cmd/wait.go:91` 任务 wait 撞名 | 相符：`Use: "wait <task>"` | D2 改为沿用 `card wait` / `session wait` 两个命令名、一套实现；`session wait` 参数改会话号 |
| 2 | D4/D8 矛盾（系统消息 by_system 恒不唤醒 vs 通知要叫醒组织者） | 相符（条 26） | D8：系统通知是 `agent:handoff` 发的普通寻址消息，`by_system=false`，显式 @驻场者；条 26 留 |
| 3 | B358 修订清单不全 | 读 `b358-contract.md:354-440` | §9.1 逐条改/废/留 |
| 4 | `card wait` 别名语义 | 相符：现状交付 needs_human/decision_*/task_mirrored | D2：卡事件流 ∪ 寻址命中；stdout 形状进 contract ① |
| 5 | 游标语义 | 相符：`session.go:307` 按 member 读水位、`:389` 输出即推进 | D12：游标键 = 地址，输出即推进；push/pull 模式互斥故无双处理；pull 不认领 |
| 6 | B434 退役清单漏项 | 读 `b434-contract.md:361-373,441-442,484,499-546` 相符 | D10 补全；`desktop_launchers` 改为语义替换；`continue` 深链与异机转交保留 |
| 7 | Wave 0 依赖 S3；矛盾 7/9/10 未处置 | 相符；`scheddrain.go:479-484` vs `:511-520` 复核 | S4 拆 S4a/S4b，Wave 0 = S2+S4a+模式字段；矛盾 7 → D13 不动，9 → D12，10 → D10 |
| 8 | D6 创建归谁；D4 无父卡定义 | — | D6 账本惰性创建；D4 会话根卡 = parent 为空或 parent 不在本会话 |
| 9 | §12 待裁决清零；未知 2 由 spec 裁 | — | 别名问题随 D2 消失；未知 2 裁为「上走保留且仅限卡自动化事件」；载体扩展仍留用户 |

建议改：`--mode` 字面量 `push|pull` 已定；桌面端登记入口 = `session sit --desktop-session`；信箱格式进 contract ④；补 S9/S10 与 `card show mode`；§4 A 改 `:3685`；B399 约束写入 §9；roadmap「`claude --bg`」条加注。wait 走 HTTP 撤回（§4 H）：CLI 进程内调匹配函数，条 45/49 读侧不改。
