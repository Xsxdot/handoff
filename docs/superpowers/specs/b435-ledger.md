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

## 五、contract 节点台账（2026-10-18，contract 执行者 subagent；工作树 `cards+B435-charter-1` @ `7f860685`）

边干边记：命令、原始读数、放弃的尝试、判断。行号以本工作树为准（spec 行号取自 c944b6b7，逐条复核）。

### 五.1 入口核对
- `git log --oneline -1` → `7f860685 docs(B435): spec r2 已批准——协调者统一模型（两种驱动模式、一套 wait、驻场者与项目房间组织者）`；`git status --short` 空。
- spec 头部「上游状态：r2 已批准（用户 2026-10-18 批准…）」——批准状态成立；冻结理由 = spec §2 四条缝（生产者/消费者见契约文档 §2）。
- 已读：`b435.md` 全文、本台账 §一–§四、`b358-contract.md` §1–§6、`b434-contract.md` §3–§9、`2026-09-25-session-bounded-candidate-read-contract.md` 冻结清单（下称 B409r2 契约）。

### 五.2 存量签名复核（sed/grep，本工作树行号）
- `cmd/card_wait.go`：`Use: "wait <id>"` :67；`runCardWait` :84，起点 `st.MaxSeq()` :106，**无 `--since`、无持久游标**；建连先输出 `card_snapshot` 行（`encodeCardWaitSnapshot` :352，键 type/card_id/subtree/from_seq/members/actionable/needs，结构 :41-63）；事件行 = `ledger.Event` 原样 JSON（`enc.Encode(e)` :191；键 seq/card_id/type/actor/payload/source_target?/source_task?/source_seq?/created_at，`internal/ledger/types.go:173-183`，`card_id` 无 omitempty）；`st.Follow(ctx, members…)` :160；`cardWaitEventActionable` :233，needs_human/needs_cleared/decision_* 恒可动作 :235-237，room_message 只判 `kind==user && !by_system` 且 `by_system:null` 不可动作 :238-253；超时 124 :211-218；全终态 stderr「子树已全部完成」退 0 :154-158。spec 行号 :235/:249-253/:160 与本树一致。
- `cmd/session.go`：`Use: "wait <member>"` :67；默认读共享交付水位 `SessionDeliveryCursorContext(ctx, member)` :307；积压单行 `proto.SessionBacklog` :352 起、成功输出后 `AdvanceSessionDeliveryCursor(member, ToSeq)` :389；实时逐条 `SessionWake` 输出后推进 :456；`sessionWaitMatch` :501，`target == member` :517（spec 写 :516，漂移 1 行）；`buildSessionWake` :534；`--since` 显式 = 只读回放不读写游标（:299-310、:452）。
- `cmd/wait.go:91` `Use: "wait <task>"`——顶层任务 wait，不动（spec D2 一致）。
- `internal/ledger/session_delivery_cursor.go:14-52`：表 `session_delivery_cursors(member PK, last_seq, updated_at)`，只升不降 upsert :44-47——**游标键当前 = 成员身份串**。
- `internal/ledger/session_candidates.go:62-135`：候选超集只有两支——mentions（身份精确 ∪ 当前席位卡号）与 reply（reply_to→member 作者），均限 `card_id IS NULL`；**无「无寻址消息」分支** → 默认收件人落地必须扩候选超集（B409r2 契约条 2–5 修订，见契约文档 §6）。
- `internal/collab/sessions.go`：`MessageWakeTargets` :220（by_system/pointer → nil；`replyAuthorOf` + `resolveMessageTargets`）；`resolveMessageTargets` :233（@卡号 → 该卡 `DriverSession`，**不限本会话**）；`AddressesCard` :258；`sessionMembers` WARN 原文 :393-394。
- `internal/collab/room/delivery.go`：`ResolveDelivery` :30（返回 `[]string`，可多人）、`IsAddressed` :66。
- `internal/agentd/wakeconsumer.go`：`automationWakeEvent` :216（needs_human/seat_bearing_missing 不唤醒 :261-264）；`decodeRoomMessageForWake` :275；`automationWakeEvents` :301（needs_human → `desktopNeedsHumanWakes` :315）；`roomMessageWakeEvents` :337（`MessageWakeTargets` :352、`bySeat` 只收**本会话卡** :366-386、外部身份只 Info :397）；`resolveWakeCard` :502（bind 分支 :521-539）；房间消息唤醒同样经 `resolveWakeCard`（:748）——上走对房间消息也生效（D1 要求改）。
- `internal/agentd/scheddrain.go`：`wakeCoordinatorRoundID` :448，bind 分支 :463-490（桌面端 queue_release 放行 :478-484；「bind 席位跳过协调者唤醒」:489）；远端 coordinate queue_release 显式失败 :509-520；`StartAutomation` :54 每台 agentd 一个自动化循环（共享账本 + wake_claims 去重）。
- `internal/keystone/keystone.go`：`Wake` :105，bind → Forget :123-126；`LaunchForCard` :201 断言 source==coordinate :202；`Locate` :251 读 source==coordinate。
- `internal/ledger/binding.go`：`BindSeat` :34（同事务落 `EvDriverSeatBound` payload `{to}`）；`RebindSeat` :89；`SetSeatBearing` :163 只收 coordinate :175；`validateSeatBearing` :216 按 source 判承载 :217；`clearSeatTx` :282（含 `deleteSeatDesktopTx`）。
- 读席位来源词的判定点全集（`grep -rn "SeatSourceBind\|SeatSourceCoordinate" cmd internal desktop main.go`，排除测试）：keystone.go:123/:202/:251；scheddrain.go:463；wakeconsumer.go:521；desktopwake.go:1053；seat_desktop.go:85；binding.go:175/:217；写入点 card_driver.go:65/:163、scheddrain.go:325/:328/:608。
- `internal/proto/seat.go`：`SeatSource` :10-15，`EncodeSeatIdentity` :41 / `ParseSeatIdentity` :53（`cli:<cli>#<sid>`，注释 :39-40「旧 cli:user@host 不会被误认成合法席位」）。
- `internal/proto/identity.go`：`IdentityKindUser/Agent` :18-21；`ParseMemberIdentity` :57；注释 :6-8「web:<host> / cli:<user>@<host> 是旧传输层临时脸，不属本词表」。
- 作者身份生产者：控制台 `handleRoomSend`（`internal/agentd/roomsapi.go:469`）actor = `id.Member` = `user:<console_user>`；CLI `session send`（`cmd/session.go#sessionSendActor` :881）三形态：`--agent` → `agent:<n>`，`--cli/--session` → 席位 `cli:<cli>#<sid>`，缺省 → `ledgerActor()` = `cli:<USER>@<host>`（`cmd/ledgercli.go:44-51`）。**发现**：CLI 人形发言作者不是 `user:` 记法——按 D4 字面不算人类作者（契约文档退回项 R3）。
- `internal/proto/ledger.go`：`Card.DriverSession/DriverSource` :32-33；`CardView` :176-177；`CoordinatorRebindReq.Mode`（wire 键 `mode`，取值 launch，`ledger_wire_test.go:56-63`）——**`mode` 键名已被换绑请求占用**，席位模式 wire 键取 `driver_mode`/`seat_mode` 以免同名异义。
- `cmd/card.go:108-143` `card show` = 单 JSON `{card: ledger.Card, …}`，`ledger.Card` json 标签 `internal/ledger/types.go:156-157`。
- `plugins/claude-desktop-wake/hooks/register.ts`：`WAKE_MARKER='[handoff]'` :18、`WAKE_DIR_REL` :20、`isOwnLetter` :34-38（前缀 `${sessionId}.` + `/^[0-9]{19}\.md$/`）、`drainBox` 只搬运正文不解析 :94-123、`submitPrefill` :85-91。mod 对正文只认首行前缀——**信件正文改形不需改 mod**。
- `internal/agentd/desktopwake.go`：`writeDesktopLetter` :437（文件名 `%s.%019d.md` :457）；`desktopLetter` :217（首行「卡 X 有 N 件新事」:240）；`desktopMailboxHome` :976（写信进程本机 HOME）；`desktopNeedsHumanWakes` :1043、bind 判定 :1053；`openViaDesktopLauncher` :1224；围栏渲染层 :1377 起。
- 路由：`coordapi.go:52/:55`（desktop-launch GET/POST）；`server.go:929` `POST /api/desktop/deliver`；client `internal/client/coordinator.go:81` `DesktopLauncherOpen`；CLI `cmd/card.go:600` `--desktop`、`cmd/card_driver.go:203-205` 三 flag、`cmd/card_seat_desktop.go`、`cmd/launcher.go`。
- 守卫测试：`internal/collab/room/delivery_gate_test.go:25/:81/:118`；`internal/agentd/wakeconsumer_b358_test.go:62/:96/:121/:164/:186/:219/:253/:274/:326/:356`。
- 金样本：`internal/proto/sessions_fixture_test.go:182` `TestSessionWakeFixture`；`internal/proto/session_backlog_contract_test.go:22` 读 `testdata/session_backlog.json`；`ledger_wire_test.go:26` 席位两键 omitempty。**`card wait` 行格式无既有逐字金样本**（`cmd/card_wait_test.go` 只做行为断言）→ 本轮补 `TestB435CardWaitLineShapes`。

### 五.3 代码图
- `codegraph` 不在 PATH；`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate` 首跑被会话的 git 形态守卫拒（命令串含 `github.com` 被当作 git 调用）——放弃 `cd && …` 复合写法，改 `go build -o <scratchpad>/codegraph github.com/Xsxdot/charter/graph/cmd/codegraph` 后直调二进制。
- `codegraph --repo <树> validate` → `{"containers":354,"domains":26,"edges":7037,"issues":null,"nodes":5625,"views":["cards-B383-charter"]}`。
- `sym` 命中：`Service.MessageWakeTargets`（n_collab_Service_MessageWakeTargets，d_collab，sessions.go:220，**图中签名陈旧**：仍是无 error 旧体）、`Service.AddressesCard`、`ResolveDelivery`、`Server.roomMessageWakeEvents`（d_gateway）、`Server.resolveWakeCard`、`runCardWait`、`cardWaitEventActionable`、`writeDesktopLetter`、`Store.BindSeat`、`Server.desktopNeedsHumanWakes`（:1043）、`Server.wakeCoordinatorRoundID`（:448）、`Service.Wake`（keystone.go:105）。
- **未命中（图覆盖债）**：`runSessionWait`、`sessionWaitMatch`（cmd/session.go）——`who-calls Service.MessageWakeTargets` 因此漏掉 CLI 调用方，只返回 agentd 两个。
- 归属读数：`codegraph/best.json` containers `k_agentd_*` → **d_gateway**（spec §8 把 agentd 消费循环记作 d_scheduling；按图，消费循环在 d_gateway，`internal/scheduling` 才是 d_scheduling）。spec §8 禁令「d_scheduling 不得持有事件白名单」按实际落点施于 d_gateway 的消费循环——命名差，非归属分歧，不退回。
- `target.json` 既有方向：`d_gateway→d_collab`（entries「collab 入站门面」，预算 0）、`d_cli→d_collab`（「collab 入站门面」「collab 包级函数」，0）、`d_gateway/d_cli/d_collab/d_ledger→d_protocol`（含「proto 实体」「proto（包级函数）」）。判断：新匹配函数做成 `collab.Service` 方法（门面容器）、地址类型放 proto（实体）→ 零新方向、零预算变化，`target.json` 不改。

### 五.4 判断与放弃的尝试（按出现顺序）
- **`≤ 1 人` 的作用域**：spec §4 E / D4 / §9.1 条 30 写「解析结果恒 ≤ 1 人」，同表又写条 20–25「留」（`ResolveDelivery` 显式多 @ 返回多人）。两读法：(a) 一切消息 ≤1——会推翻条 23/25 且 spec 未给多 @ 的处置；(b) 只约束默认收件人分支——与「留」一致。取 (b)，记拍板 D-B（契约 §8），并在交棒报告请协调者确认。
- **人类作者**：D4 字面 `user:` 记法；CLI 人形 `session send` 作者是 `cli:<USER>@<host>`（ledgercli.go:44-51），S1 剧本用 CLI 发言 → 按字面不触发默认收件人。不擅自扩大判据（会把旧传输层脸重新认作人），冻结 `IsHumanAuthor ⇔ user:`，S1 冲突列退回项 R3。
- **`card bind --mode push`**：push 需承载（carrier+machine）才能起回合；B389 规则 1/2（`binding.go:217`）按来源词执法「bind 必零承载」、`SetSeatBearing` 只收 coordinate（:175）→ bind+push 在现有写面上不可运行；改法要么扩 CLI flag、要么改 B389 按 mode 执法、要么拒绝该组合——跨 d_ledger（承载形态）与 d_gateway（push 驱动）两处规则，spec 未定 → 退回项 R1，不冻结该组合。
- **信件内容**：B434 卡级信内嵌原文 + 围栏；若驻场者信也内嵌原文，则「信」与 `session wait` 成两条交付通道、两套进度——组织者恢复（S8）从驻场者游标续读会重放已经由信交付的消息（B434 条 82 明令写信不推进游标）。取「门铃信」：信只报会话、条数、seq 区间与取信命令，交付唯一经 `session wait`。记拍板 D-A。
- **跨机写信**：spec D10「异机投递沿既有 transferCoordinatorWake + LetterUnopened」——该端点是卡路径 `/api/cards/{id}/coordinator/wake`、以 `card.DriverSession == req.Seat` CAS（coordapi.go:178-195），驻场者不是卡席位，无从 CAS → 保留项与驻场者语义不相容。候选「只由驻场者桌面端地址所在机的 agentd 写信（每台 agentd 都跑消费循环，scheddrain.go:54-66）」可行但改了 spec 明文 → 退回项 R2，不冻结。
- **card wait 游标起点**：席位地址游标无行时若从 0 补收，候选读按**当前**席位解析 `@卡号`（session_candidates.go:44-48），换绑后新持有者会收到全部历史 `@卡号`；取「下界 = 该身份最近一次坐下/换绑事件 seq」。可逆（单包 CLI 读法），不立拍板，冻结为条目。
- **card wait 卡事件段**：D2「卡事件照旧」→ 卡事件段起点仍是启动时 MaxSeq，不进持久游标；持久游标只管房间命中段。
- **游标键**：席位键 = 席位身份原样（与旧 `session wait cli:…#…` 行同键同义，可续用）；驻场者键 = `resident:<session>/<identity>`（同一身份驻场多房间不串游标；旧 `user:/agent:` 行作废）。
- **地址类型落点**：放 proto 而非 collab——d_gateway→d_collab 只允许「collab 入站门面」容器（target.json），collab 自有类型会成为门面外的跨域引用；放 proto 走既有「proto 实体」entries，零方向变更。
- **CursorKey 先写成方法后改包级函数 `DeliveryCursorKey`**：方法需新建 `k_proto_DeliveryAddress` 容器并改 best.json 归属；包级函数落既有 `k_proto_fn`。
- **wire 键名**：卡上用 `driver_mode`、会话卡行用 `seat_mode`，不用 `mode`（`CoordinatorRebindReq.Mode` 已占，ledger_wire_test.go:56-63）。
- **TS 镜像不进 Ticket 0**：`web/node_modules` 不存在（`ls web/node_modules` 空），tsc/vitest 跑不了；不提交未编译的 TS → 欠账。
- `AddressesCard` 生产调用方为零（`grep -rn "MessageWakeTargets\|AddressesCard" internal cmd`，排除测试与定义处：只剩 wakeconsumer.go:352、cmd/session.go:512 两处 `MessageWakeTargets`）→ 对侧执法：**疑似漂移**，并入匹配函数时直接删除。
- 会话守卫首轮把 `cd … && gofmt … cmd …`、`go test ./cmd/` 判为疑似 git 调用拒绝；改为不带 cd 的单命令、包路径写模块全名 `github.com/Xsxdot/handoff/cmd`。

### 五.5 Ticket 0 落码与跑数
- 新增：`internal/proto/seat_mode.go`、`internal/proto/delivery.go`、`internal/proto/resident_letter.go`、`internal/proto/testdata/b435_resident_letter.golden`、`internal/collab/recipients.go`；改：`internal/proto/ledger.go`（Card/CardView +DriverMode）、`internal/proto/sessions.go`（+SessionMemberResident、SessionCard.SeatMode、SessionResident）、`internal/collab/client/client.go`（LedgerClient +SessionResident）、`internal/ledger/api/api.go`（Facade.SessionResident 空壳恒报未实现）、`internal/collab/readmodel_test.go`（fakeLC 补方法）。
- 测试：`internal/proto/b435_contract_test.go`（9 支）、`internal/collab/recipients_b435_test.go`（2 支 + 编译期签名断言）、`cmd/card_wait_b435_test.go`（1 支，锁现状行格式）。
- `gofmt -l internal/proto internal/collab internal/ledger/api` → 首跑报 `internal/proto/ledger.go`（字段对齐），`gofmt -w` 后空。
- `go build ./...` → 无输出，退出 0。
- `go vet ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` → EXIT=0。
- `go test ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` → `ok internal/proto 2.127s`、`ok internal/collab 2.500s`、`ok internal/collab/room 1.823s`、`ok internal/ledger/api 1.986s`，EXIT=0。
- `go test -run TestB435 -v ./internal/proto/ ./internal/collab/`（改名 DeliveryCursorKey 后复跑 `-count=1`）→ 11 支全 PASS。
- `go test -run 'TestB435|TestB353CardWait|TestB409CardWait' -v github.com/Xsxdot/handoff/cmd` → `TestB435CardWaitLineShapes` PASS，既有 card wait 14 支 PASS，`ok …/cmd 18.833s`。
- `go test -count=1 -run NONE_B435 ./...`（全仓测试二进制编译）→ 无失败行，EXIT=0。
- 视图 diff：`python3 -I <scratchpad>/mkdiff.py <树> 7f860685752a5d4dea43d38b96441be9437a0565` → `15 added; 4 modified; 1 edges`，写 `codegraph/diffs/cards-B435-charter-1.json`（view `cards/B435-charter-1`，文件名已扁平化）。首跑误传了一个手拼的错 base hash，立即以 `git rev-parse HEAD` 的值重跑覆盖。
- `codegraph validate` → `issues: null`，views = [cards-B383-charter, cards-B435-charter-1]。
- `codegraph --view cards-B435-charter-1 sym Service.Recipients` → `status: added`，domain d_collab，line 34。
- `codegraph --view cards-B435-charter-1 check` → 3 处 over-budget（d_cli→d_maintenance 27>25、d_gateway→d_collab 1>0、d_ledger→d_protocol 5>2）；**不带 --view 的基线 check 同样这 3 处** → 存量违规，非本视图引入。

### 五.6 出稿与冻结
- `docs/superpowers/specs/b435-contract.md` 落盘：冻结条目 137 条（① 50：C-101–C-129、C-131–C-151；② 45：C-201–C-245；③ 28：C-301–C-328；④ 14：C-401–C-414）；B358 修订记录 §5；B409r2/B389/B399 连带 §6；B434 处置 §7；拍板 D-A、D-B（另审过不立 5 条）；退回 R1–R3。
- 出稿自检：首稿引用 b434-contract.md 行号时误用了「持久化输出片段」的相对行号（§3.6 `:130-147`、`:217`，§3.10 `:479-489`，§3.8.1 `:436`），grep 原文件后改为 `:249-266`、`:336`、`:483-489`、`:433`；`cmd/session.go` flag 行 `:888-893` → `:897-902`、`sessionSendActor` `:881-897` → `:881-895`（首处）。
- `codegraph --view cards-B435-charter-1 resolve --doc docs/superpowers/specs/b435-contract.md` → 12 个锚全部解析（ok 6 / moved 6，无坏锚）；`resolveWakeCard` 锚解析到 :502（文中原写 :500），契约与台账同改为 :502。
- 终跑：`go build ./...` 无输出；`go test -count=1 ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` 4 包 ok；`go test -count=1 -run TestB435 github.com/Xsxdot/handoff/cmd` ok。
- 冻结提交：`git add <16 个文件>` + `git commit -q -m "docs(B435): contract 冻结四条缝…"`；`git log --oneline -1` → `9e8c7543 docs(B435): contract 冻结四条缝——一套 wait、匹配函数判定表、席位模式 wire、驻场者门铃信`（历史读数；随后 amend 一次收进本行，hash 会变，不回写）。未 push。

## 六、协调者检查点：contract 稿审阅与退回裁决（2026-10-18 15:0x）

- 复跑：`go build ./...` 退出 0；`go test -count=1 ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` 4 包 ok；`go test -count=1 -run 'TestB435|TestB353CardWait|TestB409CardWait' ./cmd/` ok（18.4s）。提交 4099e2c0 工作树干净。
- 条目规模：137 条。协调者判定：4.1 的输出顺序/去重/退出码对其他 agent 可观察，属接缝断言；4.5 的 315–318 是跨包守卫。未要求缩减。
- D-B：确认——「≤1 人」只约束默认收件人分支；显式多 @ 按 B358 条 20–25。
- R1 裁决：本期 `card bind --mode push` 拒绝（非 0 退出，提示 `card coordinate`）；push 席位只经 coordinate/rebind --launch。不放开 bind 承载规则（B389 规则 1/2 不动）。
- R2 裁决：选甲——驻场者信件只由 `SessionResident.machine` 所在机 agentd 写；转交端点只服务 push 席位；不新建会话键转交 DTO。
- R3 裁决：人类作者 = `user:<n>` ∪ `cli:<user>@<host>`（CLI 人形 actor）；席位身份、`agent:*`、`web:*` 非人类。S1 剧本不改。C-201 与 `TestB435IsHumanAuthor` 需补签。
- 户口更正：消费循环按 `best.json` 归 d_gateway，spec §8 r3 更正。
- 以上写入 spec r3（头部修订记录 + D1/D4/D10/§8/§9），交回 contract 节点补签。

### 五.7 补签（2026-10-18，协调者裁决 → spec r3 `0e673c6b`）
- 入口：`git log --oneline -3` → `0e673c6b docs(B435): spec r3——contract 退回 R1–R3 与 D-B 的协调者裁决` / `4099e2c0 …contract 冻结四条缝…` / `7f860685 …`；`git status --short` 空。
- 裁决落条目：D-B 确认（§8 改「协调者 2026-10-18 确认」）；R1 → 新增 C-313a/313b（`bind --mode push` 非 0 拒绝 + 指路 `card coordinate`）、C-314a（push 只经 coordinate / rebind --launch / agentd）、C-314b（B389 不动），§3.3 表与 §6 B389 行同改；R2 甲 → 新增 C-415–C-418（只由 `SessionResident.machine` 所在机写信、他机零动作、转交只服务 push、不新建 DTO），§1.3、§7 保留/退役表同注；R3 → C-201 改为 `user:<n>` ∪ `cli:<user>@<host>`，§3.2 签名注释与判定表第 4 行同改；§2 去掉「d_scheduling」并注 spec r3 已更正；§9 三项标「已裁」；§12 欠账 1、2 改为已裁。冻结条目 137 → 145。
- 代码：`internal/proto/delivery.go` `IsHumanAuthor` 增 CLI 人形分支 + 私有 `isCLIHumanActor`（`cli:` 前缀、余部恰一个 `@` 两侧非空、拒 `#` 与空白——与 `ParseSeatIdentity` 互斥，seat.go:39-40）；`TestB435IsHumanAuthor` 正例加 `cli:sy@mac-pro`、`cli:unknown@linux-01.local`，反例加 `cli:sy@mac#t1`、`cli:@host`、`cli:sy@`、`cli:sy`、`cli:a@b@c`、`cli:s y@h`。
- 契约文档批改走 scratchpad 脚本 `r3edit.py`（每处替换断言恰一次命中），输出 `ok`。
- `gofmt -l internal/proto` 空；`go build ./...` 无输出；`go test -count=1 ./internal/proto/ ./internal/collab/...` → `ok internal/proto 1.291s`、`ok internal/collab 1.730s`、`ok internal/collab/room 1.082s`；`go test -count=1 -run TestB435IsHumanAuthor -v ./internal/proto/` → PASS。
- 视图 diff 重生成：`mkdiff.py` 加 `n_proto_isCLIHumanActor` 与边 → `16 added; 4 modified; 2 edges`；`codegraph validate` → `issues: null`；`resolve --doc b435-contract.md` → 12 锚，0 坏锚。
- 补签提交：`git commit -q -m "docs(B435): contract r2 补签…"` → `git log --oneline -1` = `4d5bc514 docs(B435): contract r2 补签——按 spec r3 裁决补 R1–R3 与 D-B 条目`（历史读数；amend 一次收进本行，hash 会变，不回写）。未 push。

## 七、breakdown 节点台账（2026-10-18，breakdown 出稿者 subagent；工作树 `cards+B435-charter-1` @ `0004b815`）

边干边记：命令、原始读数、放弃的尝试、判断。出稿不提交（拍板即提交归协调者）。

### 七.1 入口核对
- Skill 工具加载 `charter:breakdown` 失败（`Unknown skill: charter:breakdown`）；`~/.claude/skills/charter/skills/` 与 `~/workspace/charter/skills/` 均无 `breakdown/`。改读 charter 仓 worktree 副本 `~/workspace/charter/.claude/worktrees/competent-lalande-a0570f/skills/breakdown/SKILL.md`（三个 worktree 副本 md5 同为 `e334eb40210e2b1af2ca49e7ad7f9fdb`，含 r4 2026-10-17「首故事子卡」口径）。`charter:architecture-law`、`charter:defect-families` 经 Skill 工具正常加载。**发现**：已装 charter 套件缺 breakdown 节点 skill（安装漂移，非本卡问题，记此备查）。
- `git log --oneline -1` → `0004b815 docs(B435): contract r2 补签——按 spec r3 裁决补 R1–R3 与 D-B 条目`；`git status --short` 空。
- 状态可查：spec 头「上游状态：r3 已批准范围内修订（r2 用户 2026-10-18 批准）」；contract 头「上游状态：已批准」「冻结状态：本提交冻结…冻结条目 137 → 145」。
- 已读：`b435.md` 全文、`b435-contract.md` 全文（535 行）、本台账 §一–§六。

### 七.2 规模读数（架构法第三条判据；scratchpad `pkgstat.py`，只计非测试 .go）
- `internal/agentd` files=64 lines=20511 无子包（信号 2 命中；信号 3 处于 2 万下沿——B434 拆解时 19,037，本卡前已 +1.5k）；`cmd` files=61 lines=12286 无子包（信号 2），`card*` 前缀 13（信号 1）；`internal/ledger` 33（<40）；`internal/collab` 3 + 子包 room/cursor/client；`internal/keystone` 2；`internal/proto` 30（<40）；`internal/skill` 5。
- 关键文件行数：`desktopwake.go` 1518、`wakeconsumer.go` 954、`scheddrain.go` 702、`coordapi.go` 470、`server.go` 1631、`roomsapi.go` 716、`keystone.go` 353、`collab/sessions.go` 643、`collab/service.go` 980、`collab/recipients.go` 50（T0 空壳）、`ledger/binding.go` 428、`ledger/seat_desktop.go` 297、`ledger/sessions.go` 381、`session_candidates.go` 184、`session_delivery_cursor.go` 52、`ledger/store.go` 622、`cmd/card_wait.go` 401、`cmd/session.go` 915、`cmd/card_driver.go` 211、`cmd/card.go` 608、`cmd/card_seat_desktop.go` 216、`cmd/launcher.go` 302、`cmd/project.go` 398、`ledger/api/api.go` 336。

### 七.3 图
- `go run …codegraph` 带 `github.com` 串被会话 git 守卫拒（同 §五.3 先例）；改为不带 cd 的 `go build -o <scratchpad>/codegraph github.com/Xsxdot/charter/graph/cmd/codegraph`（退出 0，无输出）后直调。
- `codegraph --repo . validate` → containers 354 / domains 26 / edges 7037 / nodes 5625 / issues null / views [cards-B383-charter, cards-B435-charter-1]。
- `best.json` 顶层域类型实读：logic = d_orchestration、d_protocol、d_ledger、d_collab、d_cli、d_web、d_policy、d_scheduling、d_keystone；boundary = d_gateway、d_workspace、d_execution、d_sessions、d_transport、d_maintenance。子域 d_execution_host（`k_hostapi_*`）、d_web_contract（`k_web_api_*`）、d_web_cards（`k_web_app_cards`）、d_web_command（`k_web_app_rooms`）、d_web_shell（`k_web_app_lib`）。`k_skill_*` 归 d_maintenance；`k_agentd_*` 归 d_gateway；`k_scheduling_*` 归 d_scheduling。
- `codegraph --view cards-B435-charter-1 who-calls Service.MessageWakeTargets` → 只回 d_gateway 两个（`roomMessageWakeEvents` :337、`automationWakeEvents`），**仍漏** `cmd/session.go#sessionWaitMatch`（契约 §12 第 12 条图覆盖债复现）。`who-calls Server.resolveWakeCard` → `automationWakeEvents` :301、`desktopNeedsHumanWakes` :1043 等。

### 七.4 代码读数（grep/sed，本树行号）
- `desktopwake.go` 函数全集 `grep -n '^func '`：保留候选 = `writeDesktopLetter` :437、`writeDesktopLetterOnce` :471、`sweepDesktopLetterTmp` :512、`desktopClock` :542、`claudeContinueURL` :551、`PlatformDesktopOpener` :571、`SetDesktopOpener` :581、`openDesktopDeepLinkDarwin` :590、`desktopMailboxHome` :976、失败段 `desktopWakeRetryDelay`/`desktopWakeRound`/`recordDesktopWakeFailure`/`clearDesktopWakeStall` :1126-1199（卡键，驻场者侧无现成语义——契约欠账 9）；其余（材料/简报/卡级来信/围栏层/拉起器代开/deliver/needs_human 例外/`wakeDesktopSeat`/`wakeRemoteDesktopSeat`/`renderLauncherLetter`）为退役面。
- 账本事件常量 `internal/ledger/types.go:49-110`：`card_created` :49、`status_moved` :50、`needs_human` :68、`driver_takeover` :77、`driver_seat_bound` :83、`seat_desktop_set` :88、`room_message` :93、`session_*` :104-110。D8 通知事件表三类有现成事件。
- DDL：`internal/ledger/store.go` 两方言副本（SQLite :206 起 / PG :396 起），`sessions` :369 无 project 列；加列迁移样式 `ALTER TABLE cards ADD COLUMN driver_source` :608；`seat_desktops` :340、`desktop_launchers` :348（PK=project）。
- PG 测试默认 skip：`internal/ledger/store_pg_test.go:1-26`「未设 LEDGER_TEST_PG_DSN，跳过 PG 冒烟」；已有 `seat_desktop_pg_test.go`、`registry_pg_test.go`、`mirror_pg_test.go`。→ PG 方言迁移机内默认不验，进真机清单。
- `BindSeat(`/`RebindSeat(` 调用点（非 binding.go）：约 130 处、30 个文件，其中生产 5 处（`cmd/card_driver.go` 2、`scheddrain.go` 3），其余全是测试 → 若改签名加 mode 参数，扩散到 25+ 测试文件。判断：plan 须选不扩散签名的形状（归 K0 plan，非岔口）。
- 命令面：`cmd/session.go` `Use: "wait <member>"` :67、`create` :578、`list` :612、`detail` :661、`send` :789、`sessionWaitMatch` :501、`sessionSendActor` :881；`cmd/project.go` `project` :81（add/ls/rm/edit，无 room）；`cmd/launcher.go`、`cmd/card_seat_desktop.go` 整文件退役；`cmd/status.go:87-105` 拉起器段。
- `internal/client/coordinator.go:81` `DesktopLauncherOpen`、`:113` `DesktopDeliver`；路由 `coordapi.go:52/:55`、`server.go:929`；`server.go:217-250` 桌面端字段（`desktopWakeStall`、`desktopOpen`、`desktopHome`；`desktopState` :236 是薄壳状态，**非** B434 退役面）。
- `SessionBacklog` 键 `type/member/from_seq/to_seq/hits`（`internal/proto/sessions.go:189-195`）——S8「游标丢失标回放」可由读者按 `from_seq` 判，不需新键（判断，见拆解稿 P8）。
- web 触点（`grep -rln`）：`web/src/api/{ledger.ts,rooms.ts,scheduling.ts,contract.test.ts,testdata/CardView.json}`、`web/src/app/cards/{CoordinatorPanel.tsx,desktopSeatCopy.ts,CardDrawer.tsx,ListView.tsx}`、`web/src/app/lib/{claudeDeepLink.ts,desktopShell.ts}`、`web/src/app/rooms/{SessionTab.tsx,SessionChat.tsx}`；`ls web/node_modules | wc -l` → 0（本机 tsc/vitest 跑不了，同 §五.4）。
- skill 文本：`skills/handoff/SKILL.md`；锁文本测试 `internal/skill/skill_text_b434_test.go`、`desktop_rule.go`/`desktop_rule_test.go`。

### 七.5 判断（按出现顺序）
- **载体缺口**：spec §9 文案「无收件人（@ 指定）」「需 @」「项目无组织者」——契约只冻结了 `NoRecipientReason` 词表（C-209），**没有**把它投影到房间消息视图（d_gateway→d_web）或 `card show` 的载体。查是否为已批准边界内的实现缺口：房间消息视图由 d_gateway 产出、d_web 消费，按本稿切法分属两张卡 → 是独立共享接缝 → 列待拍板 P5（含「退回 contract 补冻结」选项）。
- **spec D8 与 C-218 冲突**：D8 写「协调者提问（协调者自己 `session send` 项目房间，无 @ 时按 D4 落驻场者）」，但协调者作者身份是席位 `cli:<cli>#<sid>`（非人类，C-201），无寻址 → `non_human_unaddressed`（C-218）→ 驻场者收不到。S6 的触发因此不成立。→ 退回项 R-K1（不是偏好岔口）。
- **系统通知的惰性创建是死路径**：C-245「无驻场者不发通知」而驻场者必须先有房间，故系统通知永远不会是项目房间的首个写入方；首个写入方只能是 `session sit --project` / `project room`。不改可观察行为（S5 前置就要求组织者已坐下），记边界澄清，不退回。
- **系统通知恰一次 × 每机一个消费循环**：`scheddrain.go:54-66` 每台 agentd 跑自动化循环；契约 C-243–245 没写哪台发。S5/§10 要求「恰一条」→ 去重机制是岔口（P6）。
- **Wave 0 与驻场者表**：`Recipients` 判定表第 5 行要读 `SessionResident`；T0 `Facade.SessionResident` 恒报未实现，且契约 C-228 要求错误不得当未命中 → Wave 0 若不落驻场者读路径，匹配函数对一切无寻址人类消息都报错。不能用「恒无驻场者」常量顶替（T0 明令「不报『无驻场者』冒充成功」）。→ P2。
- **B434 桌面端席位在 Wave 0 后的过渡态**：K0 把 `resolveWakeCard`/`wakeCoordinatorRoundID` 改按 mode 判后，桌面端 bind 席位（= pull）在 S7 落地前要么立刻变暗、要么保留桌面端记录分支到 X 卡一起删。→ P3。
- **迁移身份缺口**：`seat_desktops` 行的 identity 是席位身份 `cli:claude#<sid>`，`desktop_launchers` 行无身份；C-413 要求驻场者身份为 `user:/agent:` 统一记法 → 存量行无法无损映射为驻场者。→ P4。
- `cmd/card_wait_test.go#TestB353CardWaitFollowFiltersAndContinues`（:128）的期望序列含 `ledger.EvRoomMessage`（:217，旧卡房间 `Room: cardID` 的 user 消息，:159-163）——C-111 删 room_message 分支后此期望必改（K0 改写清单）。旧卡房间 B358 已只读（`collab/service.go:189` ErrReadOnly），生产上不再产生此类事件，改写不丢行为。
- `AddressesCard` 引用只剩定义 `collab/sessions.go:256-258` 与 `wakeconsumer.go:336` 一处注释；**无测试引用**（`grep -rln AddressesCard --include=*_test.go` 空）→ C-240 删除无测试连带。
- `internal/skill/skill_text_b434_test.go`：`TestB434SkillSeatHasAuthorizationRule` :87、`…ClaudeMDSnippetHasAuthorizationRule` :95、`…MainAgentDesktopCoordinate` :113、`…SeatDesktopCommands` :122——前两支随授权规则改写（O2），后两支随 D10 退役（X）。
- 薄壳：`desktop/internal/shell/external_deeplink.go` + `_test.go`（契约 §7：`/new` 改为被拒）。
- driver_mode 投影点（照 `DriverSource` 的现有投影点 grep）：`internal/ledger/types.go:157`（Card 字段）、`internal/ledger/cards.go:366`（scan）、`internal/ledger/api/api.go:296`（Facade → proto.Card，keystone 经 `s.ledger.GetCard` 读的就是它）、`internal/agentd/ledgerapi.go:120/:176`（CardView）、`cmd/card.go:562`（`out["driver_source"]` 手搭 map——序列化边界，须同加 `driver_mode`）。
- **契约 §1.1「四处」之外的来源判定**：`keystone.go:119` `seat.DriverSession == "" && seat.DriverSource == ""`、`keystone.go:251` `Locate` 的 `seat.DriverSource != coordinate`——都落在 C-318 源码守卫的禁令面内（守卫按文件扫 `DriverSource ==`/`!=`）。判断：不是契约缺条（C-318 已覆盖整文件），是 K0 的改写清单必须多列两处；`Locate` 改为「mode=push 才可 attach」，与迁移映射（coordinate→push）等价。记 §2.4 边界澄清。`scheddrain.go:492`、`wakeconsumer.go:541`、`coordapi.go:405` 以 `proto.SeatSource(card.DriverSource)` 作 `ValidateSeat` 实参——传参不在禁令内。

### 七.6 出稿与自检
- 落盘 `docs/superpowers/specs/b435-breakdown.md`（状态「待拍板」）：待拍板 P1–P12；退回 R-K1（spec D8 × C-201/C-218）；子卡 K0（首故事，Wave 0）/ R / O1 / O2 / X / W / I；S1、S10 指向 K0 不另立卡；行为闭环 17 行；真机清单 20 条；契约 145 条与欠账 13 条逐条归属。
- `codegraph --view cards-B435-charter-1 resolve --doc docs/superpowers/specs/b435-breakdown.md` 首跑 21 锚，2 个 `file_missing`（`keystone.go#Service.briefing`、`delivery_gate_test.go#TestResolveDeliveryShapeHasNoMemberSet`——写成了短路径）；改为全路径后复跑 → `ok 8 / moved 13`，0 坏锚。
- 未提交（拍板即提交归协调者）；未建卡、未派发、未跑 `handoff card`；未改任何代码文件。

## 八、协调者拍板：breakdown（2026-10-18）

- 出稿者报 `charter:breakdown` 在 subagent 内 Skill 加载失败、改读 charter 仓副本（md5 一致）；协调者在主会话加载同 skill 成功，内容一致，采信。
- 裁决：P1 甲 / P2 甲 / P3 甲 / P4 乙 / P5 甲 / P6 甲 / P7 乙 / P8 甲 / P9 甲 / P10 乙优先甲兜底 / P11 甲 / P12 甲；R-K1 → spec r4 勘误（协调者提问显式 @ 组织者），不动 C-201/C-218。
- P6 理由：`no_recipient` 投影由 d_gateway 产、d_web 消费，是两个工作单元共同依赖的跨进程 wire，按 contract 判据冻结；只补这一条缝，K0 不等补签。
- P7 理由：甲会让无 agentd 机器上的纯 CLI 驻场者永远收不到通知；乙复用消费循环既有恰一次机制。
- spec r4：D6/D8/D10 勘误 + §2 增第 ⑤ 缝。拆解稿状态行改「已拍板（2026-10-18）」。

### 五.8 补签 ⑤ 投递投影（2026-10-18，contract 执行者 subagent；工作树 `cards+B435-charter-1` @ `9824af08`，spec r4 / breakdown P6=甲）

- 入口：协调者令「只此一条缝，其余不动」；同时按 spec r4 对齐 D6/D8/D10 措辞。
- 现状读数：`internal/agentd/roomsapi.go:430-457#Server.handleRoomMessages` → `writeJSON(..., {"messages": events})`，`events []proto.LedgerEvent` 取自 `collab.Service.HistoryContext`（`internal/collab/service.go:383`）；TS `web/src/api/rooms.ts:83` `type RoomHistoryItem = LedgerEvent`、`:175` `request<{ messages: RoomHistoryItem[] }>`；`proto.SessionSummary` 在 `internal/proto/sessions.go:97`，由 `internal/collab/sessions.go:317#summarizeSession` 装配、`internal/agentd/sessionsapi.go:145/168` 出 HTTP；`cmd/card.go:108-143#cardShowCmd` stdout 单 JSON；cmd 既有 stderr 提示惯例 `提示：…`（`cmd/card_records.go:120`）。
- 判断：DTO 用 `RoomHistoryItem{LedgerEvent 内嵌; NoRecipient omitempty}`——内嵌平铺使无键行与旧 `LedgerEvent` 逐键同形（C-502 金样本兑现），信封与 `HistoryContext` 返回类型都不动；名字与 TS 既有别名同名，W 镜像只加一个可选键。
- 判断：`project` 装配点（collab `summarizeSession` vs d_gateway 装饰）不由契约选——两者都在既有方向内（d_collab→d_ledger 接口缝 / d_gateway→d_ledger），冻结「只能一处 + 单一写者 O1」，选择归 O1 plan（C-512）。
- 拍板 D-E：`no_recipient` 读时求值（不做发送时快照），三闸门判据记契约 §8。放弃方案：消费循环把 `Delivery` 写回账本——新写面 + 第二写者。
- 判断：`card show` 读失败不显示「项目无组织者」（C-517），避免把读错误呈现为业务事实（同 C-228 口径）。
- 不新增条目（遵令）：r4 D8「协调者提问须显式 @」只在 §4.4 加注，C-201/C-218 不动。
- r4 对齐：§7 `desktop_launchers` 行由「语义替换→驻场者桌面端地址」改为「退役、表删除；驻场者登记落新表（会话主键）」；§7 迁移段与 §12 第 10 条改为两表删除、存量行清除留痕（P5=甲）；§12 第 13 条补项目房间创建来源与显式 @。
- 落码（Ticket 0，零投影逻辑）：`internal/proto/delivery.go` 增 `RoomHistoryItem`；`internal/proto/sessions.go` `SessionSummary` 增 `Project string json:"project,omitempty"`；金样本 `internal/proto/testdata/b435_room_history.json`、`b435_session_summary_project.json`；`internal/proto/b435_contract_test.go` 增 `TestB435RoomHistoryNoRecipientGolden`、`TestB435SessionSummaryProjectGolden`。
- 首次落测试用复合 heredoc 被沙箱拒（「too complex to verify」）→ 改 Write 工具 + scratchpad `python3 -I` 脚本。
- `gofmt -l internal/proto` → 空。
- `go test -count=1 -run TestB435 ./internal/proto/` → `ok  	github.com/Xsxdot/handoff/internal/proto	1.696s`
- `go build ./...` → 无输出，退出 0。
- `go test -count=1 ./internal/proto/ ./internal/collab/...` →
  `ok  	github.com/Xsxdot/handoff/internal/proto	0.190s` / `ok  	github.com/Xsxdot/handoff/internal/collab	3.013s` / `?  	…/collab/client	[no test files]` / `?  	…/collab/cursor	[no test files]` / `ok  	github.com/Xsxdot/handoff/internal/collab/room	2.427s`
- `go test -count=1 ./internal/agentd/` → `ok  	github.com/Xsxdot/handoff/internal/agentd	70.942s`
- 图：`mkdiff.py` 增 `m_proto_RoomHistoryItem`（model）与修改 `m_proto_SessionSummary`（字段 `Project`），base 仍 `7f860685` → `17 added; 5 modified; 2 edges`；`codegraph validate` → `issues: null`；`codegraph --view cards-B435-charter-1 check` → 3 处 over-budget（d_cli→d_maintenance 27/25、d_gateway→d_collab 1/0、d_ledger→d_protocol 5/2），与基线同，无新增。
- `resolve --doc b435-contract.md` 首跑 `roomsapi.go#handleRoomMessages` `file_missing`（短路径）→ 改 `internal/agentd/roomsapi.go#Server.handleRoomMessages` 复跑 → 14 锚 0 坏（ok 6 / moved 8）。`cmd/card.go:108-140` 自查改 `:108-143`。
- 契约 §4 条目计数（awk 数 `^[0-9]+[ab]?\. `）→ 162（145 + C-501–C-517）。
- 未建卡、未派发、未跑 `handoff card`；未 push。
- 提交：`git commit -q -m "docs(B435): contract r3 补签第 ⑤ 缝投递投影（C-501–C-517）+ spec r4 措辞对齐 …"` → 无输出；`git log -1 --format="%H %s"` → `68ac7b6c202918f2e5902068b8dcff0e2e7cf072 docs(B435): contract r3 补签第 ⑤ 缝投递投影（C-501–C-517）+ spec r4 措辞对齐`。随后 amend 一次收进本行（amend 换 hash，不回写）。

### 八.1 第 ⑤ 缝补签复核（2026-10-18）

- 提交 0142dc4f（C-501–C-517，冻结计数 145 → 162）；协调者复跑 `go build ./...` 退出 0、`go test -count=1 ./internal/proto/` ok；工作树干净。
- D-E 裁决：**确认**——`no_recipient` 在读历史时计算、不在发送时快照；理由同契约 §8（快照需新增账本写路径与第二写者）。副作用（旧标签随当前席位状态变化）可接受，写进 W 的控制台文案说明。
- 欠账 §12 第 14 条「⑤ 投影逻辑归 O1/W」已登记；拆解 §2.2 的 O1/W 条目相应吸收 C-501–C-517（O1：501–513 生产方与 514–517；W：TS 镜像与标签）。

### 八.2 K0（B435.1）plan 拍板（2026-10-18）

- 出稿基线 0142dc4f（出稿期间第 ⑤ 缝补签已提交）。裁决：Q1 甲（C-326 读为约束生产代码；契约 §4.5 加澄清行，不改断言）/ Q2 甲 / Q3 甲 / Q4 预授权甲。
- 出稿核出的上游少算：C-318 守卫面 8 处（契约写 4 处 + 拆解澄清 2 处）；`driver_mode` 投影 6 处 + scan 1 处（拆解 §2.4 第 3 条写 3 处，漏 `internal/ledger/api/api.go#cardWire`，是 keystone 读席位必经路径）；P4 乙过渡分支还须覆盖 `roomMessageWakeEvents`。三项均已进计划，不改上游文档（计划以实数为准，review 按计划核）。
- C-112 共享游标知悉，落 roadmap。
- 拟新增 51 支测试、改写 3 支、删 1 支；真机 8 条由协调者执行。

## 九、K0（B435.1）阶段验收与合入（2026-10-18）

- review（独立 opus subagent）pass；Important-1/Minor-3 修复提交 `9c732dc1`、`50cc6694`。
- 阶段验收真机批 0 由协调者本机执行（plan §7），读数全文在 `docs/superpowers/plans/b435.1-impl-ledger.md`「阶段验收：真机批 0」：K0-1 push 恰一轮续同 session 通过；K0-2 pull `card wait` 一行、零回合通过；K0-4 一根一子 / 两根 `needs_mention` / @子卡 / reply_to 四态通过，`session detail` 与根卡判定一致；K0-5 共享游标顺序不丢、反序丢 B（C-112 知悉项，roadmap「席位地址游标键含卡号」）；K0-PG linux-01 PG 18.6 `handoff_b409_test` 三支 PASS。K0-3 / K0-R / K0-D 未验（需真实 agent 会话 / R 合入后回归 / B434 用户机）。
- `card accept B435.1 --evidence …` 已落账（生产账本，已验）；发现 **F-RM1** 落 B435.1 note：push 协调者 launch/resume 回合不注入 `HANDOFF_SESSION_CLI/ID`（`resumeTurnRequest` 自 B299 起无生产调用方），协调者无法以席位身份 `session send` 回房间——存量缺陷，非 K0 改动面，S1「协调者回房间」在真 opencode 上会失败。**待协调者/用户裁决**：随 R 批（B435.2）顺手修（注入席位环境变量 + 真机），或单开修复卡。
- 合入：`git merge --no-ff cards/B435.1-charter-1` → 父分支 `cards/B435-charter-1` 合并提交 **`08c7a158`**；`git merge-base --is-ancestor 3edce8f2 cards/B435-charter-1` 成立。**该提交即其余子卡（R / O1 / O2 / X / W）的共同基线。**
- 合入后复跑（父分支 `08c7a158`）：`go build ./...` 退出 0；`go test -count=1 -run B435 ./cmd/ ./internal/agentd/ ./internal/ledger/ ./internal/collab/... ./internal/keystone/ ./internal/proto/` 全 ok（cmd 21.9s）。
- 卡：B435.1 `finish` → `已完成`（生产账本）；B435 note seq 25182 留痕；B435 仍停 `breakdown` 等聚合。
- **F-RM1 裁决（2026-10-18，用户）**：随 R 批（B435.2）顺手修，不单开卡；拆解 §3.2 追加裁决一段（对 R 禁止清单开 `coordinatorRunner.Launch/Resume` 注入环境变量的例外）。B435.2 已挂拆解附件、移 `plan` 列，plan 由 opus subagent 出稿、协调者拍板。

## 十、R（B435.2）plan → implement → review → 阶段验收 → 合入（2026-10-18）

- plan：opus subagent 出稿 `2645e523`，协调者拍板 `72687032`（Q1 乙 / Q2 甲 / Q3 甲 / Q4 甲 / Q5–Q9 甲 / Q10 乙 / Q11 甲 / Q12 乙 / Q13 甲 / Q14 甲；B-1 回写拆解 §3.2 追加裁决：验收文本与例外范围——keystone 简报、executor/keysclient `Env` 对称字段、opencode 适配器、`collab/service.go#sessionWriters`）。
- implement：opus subagent 于独立工作树 `cards+B435.2-charter-1` 段 0–8 共 8 提交（`d12d8017`…`c15e675a`）；偏离 29 条，协调者检查点 `1104d2e7`（#20/#14 采信、#21/#28 记勘误、#27 归 I）。
- review：独立 opus subagent pass（`bad4bcb9` 记录）；变异 1 主 4 辅全红还原；Minor M1（简报→cmd 无穿透，并入真机 R-5）、M2/M3（披露粒度）、M4（`SessionMessageCandidatesContext` 零调用与两处过时文件头 → I）、M5（diag 测试无 `--timeout` → roadmap）。
- 阶段验收真机批 1（`5677aa5b`，读数全文在 `docs/superpowers/plans/b435.2-impl-ledger.md`「阶段验收：真机批 1」）：R-6/R-5（F-RM1 真路径：resume 两键 = 席位、launch 两键缺、stub 照简报原文 `session send` 落账 actor=席位、书写者拒 0 次）、R-1/R-7/R-2/R-3/R-4/K0-4b/R-9/R-8 全通过；R-PG linux-01 PG 18.6 `-run B435` 全 PASS。R-1m/R-5b 未验（用户裁决跳过，留待配合）。`card accept B435.2` 已验。
- 合入：`git merge --no-ff cards/B435.2-charter-1` → 父分支合并提交 **`69639a7e`**（批 1 基线）；`merge-base --is-ancestor 5677aa5b` 成立。
- 合入后复跑（`69639a7e`）：`go build ./...` 退出 0；`go test -count=1 ./internal/ledger/... ./internal/collab/... ./internal/keystone/ ./internal/agentd/ ./internal/executor/opencode/ ./internal/proto/ ./internal/skill/` 全 ok（agentd 67.7s）；`./cmd/`（环境键 unset）仅存量 `TestRepoContractGate` 红（基线图三处，未变）。
- 卡：B435.2 `finish` → `已完成`；B435 note seq 25709；B435 仍停 `breakdown` 等聚合。
- 残余（finish 统一落 roadmap）：两阶段拉起（R plan Q3 乙）；M5 测试卫生；`MessageWakeTargets`/`SessionMessageCandidatesContext` 删除与过时文件头（I 卡，Q12 乙 + M4）；图覆盖债重扫（#27）。
- 下一批：批 2 = O1（B435.3）与 O2（B435.4），需用户点火；O2 硬依赖 R 的 `--desktop-session` 三列（R-8 已验）。

## 十一、批 2 第一张：O1（B435.3）全节点读数与合入（2026-10-18）

- **拍板**：O1/O2 两稿同批拍板 `58dbecea`（O1：Q1–Q6 甲、Q7 乙、Q8–Q15 甲，B-1 回写拆解 §3.3 ⑤；O2：Q1–Q5 甲，B-1/B-2 回写 §5.4 / §2.4 第 8 条）。施工排序：O1 先，O2 以 O1 合入后的父分支为基线。
- **implement**（opus subagent，`cards/B435.3-charter-1` 自 `58dbecea`）：段 0–7 八个提交 `e106df47..1f1e11af`，26 文件 +3083/−38；偏离 18 条——#4/#5 认（Q8 甲语义补全：认领前读失败先取认领挡水位；持有认领后转不适用则收尾），#13 C-505 措辞转 finish 契约勘误（`roomsapi.go` 基线两处非寻址 `.Mentions`），#16 段 5 自引入 `d_gateway->d_collab` 穿透已根因修正（直接解码 DTO + 守卫），#17 `desktopwake.go:308` 存量穿透转残余；检查点 `cf09d88a`。
- **review**（opus 独立审查）：偏离已核无漏报；7 处变异全红全绿；架构轴通过（禁区零 diff、`check` legacy 计数与基线逐行相同）；**fail 于 Important F1**：`card show` 未装 logx，默认 slog 把账本 INFO 写进程 stderr，真进程非「恰一行」（C-514），测试只捕 cobra 缓冲（夹具编码了错误现实）。修复轮 1 `199b4e8c`（协调者 inline：入口 `slog.SetDefault(logx.Setup("cli",""))` 同 `card wait` 先例 + 接管 fd 2 的真进程测试先红后绿）。Minor m1–m4 记账。
- **阶段验收：真机批 2**（`b435.3-impl-ledger.md` 同名节）：O1-1 两 agentd（7781/7782）共享 linux-01 PG `handoff_b435_k0` → 每事件恰一条、另一边让过（holder 按 pid 不同）；O1-2 三类通知各恰一条、`mentions=[agent:org]`、`by_system` 缺键、`refs` 含 `seq:<n>`、follow 逐条收到、push 卡 +0；O1-3 无驻场者零通知、`card show` stderr 生产默认下恰 1 行 117 字节逐字节 = §3.5、stdout 不变；O1-4 四种 `no_recipient` 值与无键行、`project` 键只在项目房间、一页 5 行 41 ms；O1-5 `TestB435ProjectRoomPG` PASS；O1-6 批 0–1 回归全同（R-6 的「一根一子恰一轮」因本靶子登记两根卡变为 `needs_mention` 零回合，判定正确）；**O1-7 首跑撞接缝缺陷**：协调者席位不是项目房间书写者（书写者集 = 显式成员 ∪ 驻场者 ∪ 会话内卡席位，一卡只挂一个会话），spec D8 的「协调者自己 `session send` 项目房间」在真机不成立——R-K1 (a)「零机制改动」预测错误。**追加裁决 R-K1 (a′)**（拆解 §2.3 R-K1 行）：项目房间书写者集 ∪ 同项目各卡当前席位；修复轮 2 `0f0a87bc`（opus：`collab.Service.sessionWriters` 一函数、三处真路径红绿、简报句改为「到项目房间发言并显式 @」、SKILL 两条）；复跑 O1-7：席位两条落账，`@agent:org` 驻场者收到、不带 @ `non_human_unaddressed` 不收。回旋镖：plan/拆解/contract 都没把项目房间书写者集当接缝核过，校准数据。
- **合入**：`git merge --no-ff cards/B435.3-charter-1` → **`58f7bf52`**（批 2 第一张）；`merge-base --is-ancestor 172cf935` 成立。合入后复跑：`go build ./...` 0；`go test -count=1` ledger/api/collab/room/keystone/agentd(70.0s)/executor 族/proto/skill 全 ok；`./cmd/` 仅存量 `TestRepoContractGate`；`internal/executor/opencode` 的 `TestCoordinatorCancelTurnUsesSessionID` 全包并跑时红（10.16s）、单跑 PASS 0.16s——O1 未触及 executor，记时序抖动（残余）。
- 卡：B435.3 `accept` 已验 → `finish` → `已完成`（charter v15 无「阶段验收」列，阶段验收记台账与 note）。
- **残余（finish 回写清单追加）**：C-505 措辞勘误；项目房间书写者集规则进 SKILL/契约（r3 无对应条目，K0 前不变式）；空库双 agentd 首启 DDL 竞争（`CREATE TABLE IF NOT EXISTS` 并发 → `pg_type_typname_nsp_index`）；`desktopwake.go:308` `room.UnmarshalMessage` 穿透（X/O2 合入后定归属）；m1（换人 + 崩溃窗幂等锚按当前驻场者查可能重发，并入 N3/N4）、m2（已终局认领让过不记 handled 的重复开销）、m3（含空白项目名建不了房）；N5 200 条页耗时未造；opencode executor 测试时序抖动；plan 两处坏锚（`keystone.go#Service.briefing` 简写、`readmodel_test.go#fakeLC`）。
- 下一张：O2（B435.4）以 `58f7bf52` 为基线，子分支 `cards/B435.4-charter-1`。

## 十二、批 2 第二张：O2（B435.4）全节点读数与合入（2026-10-18）

- **implement**（opus subagent，`cards/B435.4-charter-1` 自 `58f7bf52`）：段 0–6 七个提交 `771c480a..1a662707`（`residentwake.go` +test、`wakeconsumer.go` +5/−1、`server.go` 一字段、授权规则改写、SKILL 组织者节与三支文本测试、视图 diff）；偏离 13 条——**#9 段内先红后绿顺序偏离**（段 1/2 行为在段 0 一次写完、测试后补；补救红原文在台账）认为纪律偏离不打回、review 变异核牙；**#12 组织者不是卡会话书写者**（spec D9「组织类问题答回卡所在会话」撞书写权，(a′) 的镜像）→ **追加裁决 R-K1 (a″)**（拆解 §2.3 R-K1 行）：含项目 p 的卡的会话书写者集 ∪ p 的项目房间驻场者，寻址不变（答话须 `@<卡号>` 或 `--reply-to`）；修复轮 `081bf5d5`/`eed87677`（`sessionWriters` 一函数 + `LedgerClient.ProjectRoom` 只读面 + collab/cmd/skill 三处红绿）；检查点 `5d2d5a41`。
- **review**（opus 独立审查）：**pass**；偏离已核无漏报；8 处变异全红全绿（本机判据、聚合、去重、写信失败挡水位、opener 失败重写、正文进信、F5 读失败伪装/吞错、授权规则改字）；架构轴通过（调用点位置、依赖方向白名单守卫、禁区零 diff、`check`/`validate` 存量不增）；Minor 6 条：(a″) 第二读失败分支与「新命中并入在途重试」无测试锁、`human` 变量名误导、修复轮越禁区测试文件未单列 F 行、CLAUDE.md 片段过渡窗口旧「正式请求」句与新授权规则并存（转 X）、两支 unopened 测试对重写不敏感。记录 `3014fcac`。
- **阶段验收：真机批 2 T 类**（`b435.4-impl-ledger.md` 同名节；二进制 `cc020b54f31bb8eb`，stub `open`）：T1 两封两行门铃信（120 字节、无正文、哨兵 0 泄漏）、continue 深链各一次、push 卡 +0、游标只因 `session wait` 推进；T2 异机零动作（`skip_reasons={"not_local":1}`）、回本机即写信；T3 信箱不可写 → Error 带 stage/attempts、零信、**活性成立**（`@T1` 一轮、水位 `seq:14` 越过）、恢复后新命中并入在途重试、退避窗到期恰一封、`open` 退出 1 → 「信已落盘、continue 未发出（不重写）」mtime 不变；T4 `sit --project … --desktop-session` → `card add` → O1 通知 → 门铃一封（批 2 #9/#10 衔接）；T5 (a″) 组织者 `@T1` 答回卡会话 → T1 一轮、不带 @ `non_human_unaddressed`、无关驻场者被拒。U1–U6（用户机 mod/桌面端）与 M1（双机）未验。
- **合入**：`git merge --no-ff cards/B435.4-charter-1` → **`21735127`**（批 2 完成基线）；`merge-base --is-ancestor 30752a1b` 成立。合入后复跑：`go build` 0；ledger/api/collab/room/keystone/agentd(68.6s)/skill/proto/executor-opencode(28.4s) 全 ok；`./cmd/` 仅存量 `TestRepoContractGate`。
- 卡：B435.4 `accept` 已验 → `finish` → `已完成`。
- **残余追加**：R-K1 (a″) 书写者集规则进 SKILL/契约回写（与 (a′) 同条）；Minor-1/2/3（I 卡：两支测试补锁 + `human`→`addressable`）；Minor-4 回旋镖（修复轮越禁区测试文件须单列 F 行；派发简报须更强调逐段红绿）；Minor-5 转 X plan；O2-U1…U6、O2-M1、N5 200 条页、R-1m/R-5b、K0-3/K0-D 留用户机。
- **回旋镖（批 2）**：plan/拆解/contract 三份都把「书写者集」当既有不变式而没当接缝核，R-K1 (a)「零机制改动」在两个方向上都错（(a′) 协调者→项目房间，(a″) 组织者→卡会话）；两次都由真机（O1-7）与实现（O2 #12）各撞出一半——下次跨房间的「谁对谁说话」承诺，spec 阶段就该把书写权与寻址分开核。
- 下一批（批 3）：W（B435.6）与 X（B435.5）plan 并行出稿，基线 `21735127`；合并序 W 先于 X。

## 十三、批 3 第一张：W（B435.6）全节点读数与合入（2026-10-19）

- **plan**（opus subagent 出稿 2026-10-18，协调者拍板 2026-10-19，`b24ebc08` 与 X plan 同批）：Q1 甲（符号链接主检出 `web/node_modules` 替代 `npm ci`，拆解 P10 追加裁决）、Q2 乙（TS 金样本经 Go 解码再编码 `-update` 生成）、Q3 甲（W 删 B434 夹具 case/JSON/TS 段，Go DTO 归 X；与 X Q5 甲同口径——「W 不晚于 X」由夹具编译强制）、Q4–Q9 甲（控制台形态：模式读数保留「来源」、驻场者/组织者两标签、项目房间归属规则归 `sessionModel` 一处 + 侧栏徽章、气泡下标注 + 悬停语义提示、`deliveryCopy.ts` 常量 + 穷尽 switch）、Q10 乙（占位句只删错误陈述）；B-1 七处文件集修正回写拆解 §3.6 ⑤。
- **implement**（opus subagent，`cards/B435.6-charter-1` 自 `b24ebc08`）：段 0–7 七提交 `c0d36164..f43858cb`（段 1 单独提交红态——O2 #9 回旋镖未重现），46 files +1346/−1040，全部落在 `web/src/**`、web testdata、`contract_fixture_test.go`、视图 diff；禁区零 diff。偏离 12 条——**#5 既有断言改动**（`sessionModel.test.ts`「kind 标签恰三值」→ 对照 Go 生成词表四值）越出 §8 豁免但已披露，**追认**（改后锁得更紧；§8 豁免漏列属 plan 预测缺口，记回旋镖）；检查点 `4c442406`。
- **review**（opus 独立审查）：**pass**；偏离已核、抽查无漏报（两处表面改动记 Minor）；12 处变异 11 红 1 等价（`CardDrawer` 恒计算 `driverMode` 渲染仍在席位分支内）；架构轴通过（禁区空、归属规则一处、新 HTTP 请求零条）；序列化边界三条穿透断言齐；Minor 4 条：M1 `SessionDetail.tsx:32` 空白改动、M2 `rooms.ts` 两守卫仅测试调用、M3 `SessionChat`/`SessionDetail` 各缺一支词表外值组件用例、M4 `ledger.test.ts` 未进偏离表。记录 `b2c5969e`。
- **阶段验收：真机批 3**（`b435.6-impl-ledger.md` 同名节；靶子 7778 + O2 二进制 `cc020b54f31bb8eb`，vite 5241 反代，桌面 1024×768 + 手机 375×812 仿真）：W-0 ticket 登录；W-1/W-2 列表与抽屉 push/pull/空座（含悬停文案、空座无模式行）；W-3 驻场者行 + 席位行带卡名与 pull、`session stand` 后驻场者行消失；W-4 登记项目后项目房间归到 `handoff` 节点、详情 `agent:org2 组织者`、按项目筛选命中；W-5 无 @ 不标、`@T1.1` 空座「无收件人（@ 指定）」、会话 B「需 @」、`agent:` 作者不标、`card bind` 后标注消失（D-E）；W-6 移动工作台项目下出现项目房间、「全部协作会话」仅项目房间带徽章、375 宽下标注可见；W-7 空座抽屉只有「▶ 叫机器人」、三退役文案零出现、`desktop-launch` 网络与日志零次；W-8 `@` 候选含 `agent:org 驻场者` 与两席位、拉卡进群/换绑/attach 在。真手机浏览器与薄壳留 I 卡。记录 `34bb578d`。
- **合入**：`git merge --no-ff cards/B435.6-charter-1` → **`ea2db47b`**；`merge-base --is-ancestor 34bb578d` 成立。合入后复跑（父卡工作树，`web/node_modules` 链接）：`go build` 0；`go test ./internal/proto/` ok；`tsc -b` 0；`vitest run` 145/1928；`eslint .` 2 errors/38 warnings 存量。
- 卡：B435.6 `accept` 已验 → `finish` → `已完成`；B435.5 → `implement`（基线 `ea2db47b`）。
- **残余追加**：review M1–M4（finish/I 卡）；plan 两处锚指向本卡删除的符号（finish 文档勘误）；W-0 真手机、薄壳内 W-7、X 合入后 W-7 复跑（I 卡）；隔离 registry 登记了 `handoff`（指向主检出路径，只在靶子里）。
- **回旋镖（W）**：plan §8「既有测试需改断言」的豁免只列了删除与 `CardView` 样本——词表加值必然撞「恰 N 值」断言，下次 plan 凡裁定词表扩值，应同批列出受影响的锁断言；拆解 §3.6 ⑤ 文件集漏了 7 处（现状核对不足，与 O1 B-1 同形）。
