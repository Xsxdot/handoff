# B432 plan 节点台账

- 卡：B432（iOS 系统推送「需要你」APNs MVP）
- 节点：plan
- 工作树：`/root/.handoff/worktrees/1f779e4d`，分支 `cards/B432-charter`，起点 `845a7e802d1fd6619e29533f43dc7650f9156119`
- 产出物：`docs/superpowers/plans/b432-plan.md`（本台账与产出物同批提交）

> 记法：每条 = 一个确立的事实 / 跑过的命令 + 原始输出摘要 / 放弃的尝试 / 做出的判断。行内 `file:line` 为工作树真码读数。

## L1 起点与环境

- `git rev-parse --abbrev-ref HEAD` → `cards/B432-charter`；`git rev-parse HEAD` → `845a7e802d1fd6619e29533f43dc7650f9156119`；`git status --short` → 空。
- `which codegraph` → `/usr/local/bin/codegraph`（已安装，非 go run）。`codegraph --version` 报 `unknown flag`（该二进制无此 flag，改用子命令）。
- `echo $TMPDIR` → `/root/.handoff/tmp/1f779e4d`（本节点未写临时文件）。
- 读 spec：`docs/superpowers/specs/b432-spec.md`（63 行，L2，已批准）。

## L2 图查询（有图先查图）

- `codegraph sym handleInbox` → 命中 `n_agentd_Server_handleInbox`，`internal/agentd/roomsapi.go:532`，domain `d_gateway`。
- `codegraph sym EventFrameHook` → 命中 `n_orchestration_EventFrameHook`，`internal/orchestration/eventframes.go:39`。
- `codegraph sym OnEvent` → 命中 `n_ledger_Store_OnEvent`，`internal/ledger/store.go:189`，`func(seq int64)`。
- `codegraph sym IssueAuthTicket` → 命中 `n_client_Client_IssueAuthTicket`，`internal/client/client.go:1274`，domain `d_transport_channel`。
- `codegraph sym NewServer` → 命中 `n_agentd_NewServer`，`internal/agentd/server.go:268`。
- `codegraph sym startLoopback` → 命中 `n_mobilecore_Core_startLoopback`，`internal/mobilecore/core.go:397`。
- `codegraph context push` → **被拒**：`领域 "push" 不在最优树词表中`，候选 26 个（d_cli…d_workspace）。按纪律降级为现状词表；本卡新增符号为未来首建。
- **图覆盖债（重要）**：图视图 `baseline` 对 `handleInbox` 的签名读数与工作树**不一致**——图里是 `s.st.ListTasks()` / `s.roomUserActor(r)`，工作树真码是 `s.mgr.ListTasks()` / `id.Member`。结论：图相对本工作树已陈旧，本计划一律以工作树真码为准；本卡新增符号（push 相关）未入图，记覆盖债。

## L3 现状事实（工作树真码，逐条复核）

- 两个事件库：`internal/store.Store`（`events` 表，`AppendEvent` `store.go:778`，`SetEventHook` `store.go:860` 单回调）与 `internal/ledger.Store`（`card_events`，未导出 `appendEvent` `events.go:20`，`OnEvent(seq)` `store.go:189` 仅 SQLite、当前无生产调用方）。
- `store.SetEventHook` 现被 `internal/agentd/server.go:349` `registerEventFrameHook` 独占（`s.st.SetEventHook(orchestration.EventFrameHook(...))`）——**单回调，新挂会顶掉帧钩子**。
- 收件箱三源在 `internal/agentd/roomsapi.go:532` `handleInbox`：decision `s.ledger.ListDecisions(true)`（:547）、ticket `s.mgr.ListTasks`+`PendingTickets`（:567-593）、mention `s.rooms.Mentions`（:597）；member 来自 `requireConsoleIdentity`（:52）。
- 写入点：decision `internal/ledger/decisions.go:53`（`EvDecisionOpened`）；mention `internal/ledger/rooms.go:528`（`EvRoomMessage`）；needs_human `internal/ledger/events.go:458`（`EvNeedsHuman`）；permission `internal/orchestration/manager.go:2151` 与 `internal/approval/client.go:476`（`EventTypePermissionRequest`）；question `manager.go:2960`。
- 事件常量：`internal/proto/proto.go` `EventTypePermissionRequest="permission_request"`/`EventTypeQuestion="question"`；`internal/ledger/types.go` `EvDecisionOpened="decision_opened"`/`EvRoomMessage="room_message"`/`EvNeedsHuman="needs_human"`/`EvNeedsCleared="needs_cleared"`/`EvTaskMirrored`/`EvStatusMoved`/`EvReviewVerdict`。**无 `mention` 事件类型**；mention 是 `proto.RoomMessage.Mentions`（`internal/proto/rooms.go:33`）。
- `proto.MemberIdentity`/`IdentityKindUser`：`internal/proto/identity.go:19,45` → `"user:<name>"`。
- 协调机全流消费：`internal/agentd/scheddrain.go:54` `StartAutomation`→`automationLoop`（节拍 `automationPollInterval=2s`）→`internal/agentd/wakeconsumer.go:636` `consumeAutomationEventsOnce`→`s.autoLedger.EventsFromAsc(nil, from, 500)`（:649）；`ledger.EventsFromAsc` 定义 `internal/ledger/events.go:64`（PG/SQLite 通用）。
- REST/鉴权/存储/客户端/移动核/绑定面/壳 的既有约定与行号：见计划 §0.4（逐条已核）。
- iOS SourceGuard：`mobile/ios/HandoffMobileTests/SourceGuardTests.swift:34-39` 禁子串 `Token/Dial/Credential`；`:49` 禁 `URLSession`；`:42-47` 禁 `JSONDecoder/JSONSerialization`。`AppDelegate.swift:5-16` 无推送。`project.pbxproj` `CODE_SIGNING_ALLOWED=NO`，无 entitlements 文件。
- 依赖：`go.mod` 无 APNs/JWT/HTTP2 专用依赖；APNs 客户端须 stdlib 手搓（ES256 JWT + net/http 自动 h2）。
- 绑定面金样本：`mobile/bind/export_surface_test.go:21-29`（7 方法 Go 签名）、`mobile/bind/shell_api_golden_test.go:20-55`（ObjC/Java）。
- Web 深链：`web/src/app/shell/Shell.tsx:921` `/cards?card=<id>` 为既有卡深链；整页路由 `fullPageRoute=['/cards','/flows','/settings','/machines','/codegraph']`（:635）。
- 配置 strict：`internal/config/config.go` `KnownFields(true)`，新键必须 `omitempty`。

## L4 跑过的命令与结果

- `ls -R mobile`、`ls internal`、`cat mobile/go.mod`、`cat go.mod` 等结构读数（输出已吸收进计划 §0）。
- `grep -rn "SetEventHook\|eventHook" internal/` → 仅 `store.go` + `orchestration/eventframes.go:39` + `agentd/server.go:349`。
- `grep -rn "inbox\|Inbox" internal/` → `handleInbox`、`proto.InboxItem`、`InboxOrigin*` 等（已吸收）。
- `grep -rn "needs_human" internal/` → 大量命中（已吸收）。
- `grep -rn "\.OnEvent(\|GetEvent\b" internal/ cmd/`（排除 _test）→ **无生产调用方**（`OnEvent` 当前无人用）。
- 未跑编译/测试（plan 节点不写实现、不建脚手架；本节点完成标准 = 计划落盘且自检通过，非编译输出）。

## L5 放弃的尝试与判断

- **放弃「只挂 `store.SetEventHook`」**：它只覆盖 permission/question，漏 decision/mention/needs_human（在 `card_events`）。判据：§L3 两库事实。
- **放弃「新增 `ledger.OnEvent` 全量事件 hook 直接消费」**：`OnEvent` 只给 seq 且仅 SQLite（PG 走 pg_notify），要全量事件须再读；新增带事件的 ledger hook 属 ledger 契约面增量。**判断（D1）**：改用自动化消费循环（方言无关、已逐条读全流）作为 `card_events` 侧扇出旁路；代价是 push 与唤醒循环耦合，退路（新增 ledger hook）记入计划待 review。
- **放弃「壳直接 HTTP 上报」**：`SourceGuardTests.swift:49` 禁 `URLSession`、`:34-39` 禁 `Token`。**判断（D3/T6）**：token 经 Go 核（`client.Client` 持有主令牌）上报；壳只调 `bind.RegisterPushDevice`。
- **判断（D3）**：设备登记 `member` 服务端注入（`requireConsoleIdentity`），不走请求体——依据 `roomsapi.go` 文件头纪律；spec 表把 `member` 列为实体字段，本计划保留在存储实体，但「谁写」取服务端。review 可退。
- **判断（D2）**：`SetEventHook` 单回调，push 与帧钩子必须合成同一回调，否则顶掉 `EventFrameHook`。
- **SourceGuard 必须修订**：实现 APNs 注册的方法名 `didRegisterForRemoteNotificationsWithDeviceToken` 含子串 `Token`，会命中 `testNoCookieBypassSymbols` 使 guard 红。**判断**：把禁子串改为禁绑定绕过符号（`BindToken` 等），保留负向断言。

## L6 自审

- spec 覆盖：逐条指到 task（计划 §13.1），无遗漏。
- 占位符扫描：唯一例外（T7 XCTest 复用既有 harness）已在计划 §11 声明。
- 跨 task 签名一致性：计划 §13.3 逐条比对一致。
- 自审三查②：本回合未碰 handoff CLI、未起 executor、未派发。
