# B432 implement 节点台账

- 卡：B432（iOS 系统推送「需要你」APNs MVP；L2）
- 节点：implement（T1–T7，按 `docs/superpowers/plans/b432-plan.md`）
- 工作树：`/root/.handoff/worktrees/0b16710e`，分支 `cards/B432-charter-2`
- 起点 HEAD：`a91e6677`（plan 已在库）
- 记法：每条 = 确立的事实 / 亲跑命令 + 原始输出摘要 / 放弃的尝试 / 判断。

## L1 环境与基线

- `which codegraph` → `/usr/local/bin/codegraph`（已安装，按纪律只用该二进制）。
- `echo $TMPDIR` → `/root/.handoff/tmp/0b16710e`；临时文件（变异备份）都写在 `$TMPDIR`。
- `go version` → `go version go1.26.1 linux/amd64`。
- `go build ./...`（基线）→ 退 0（输出：仅 `go: downloading github.com/Xsxdot/charter/graph v0.10.2`）。
- `git status --short` 起点为空；`git log --oneline -3` → `a91e6677` plan / `845a7e80` spec / `dc3c882e` 上卡合并。

## L2 图查询（有图先查图）

- `codegraph sym handleInbox` → 命中 `n_agentd_Server_handleInbox`，`internal/agentd/roomsapi.go:532`，domain `d_gateway`。
- `codegraph sym PushFanout` → **未命中**（`符号 "PushFanout" 不在图中`）。按纪律回落 grep，记**图覆盖债**：本卡新增符号 `PushFanout/PushSender/APNsSender/registerPushRoutes/collectInboxItems` 均未入图。
- **图覆盖债（承重）**：图视图 `baseline` 的 `handleInbox` 签名读数与工作树不一致（图里 `s.st.ListTasks()`/`s.roomUserActor(r)`，工作树 `s.mgr.ListTasks()`/`id.Member`）——图相对本工作树陈旧，一切以工作树真码为准（与 plan §0.5 结论一致）。

## L3 T1 — proto wire 类型（`internal/proto/push.go`）

1. 先写 `internal/proto/push_fixture_test.go` → `go test ./internal/proto/... -run TestPush` → **RED（编译红）**，原文：
   `internal/proto/push_fixture_test.go:22:23: undefined: PushDeviceRegisterReq`（另有 `PushPlatformIOS`/`PushDevice`/`PushNotification` 共 10 条 undefined）+ `FAIL github.com/Xsxdot/handoff/internal/proto [build failed]`。
2. 写 `internal/proto/push.go`（§3.1 全文）→ `go test ./internal/proto/... -run TestPush -count=1` → `ok 0.001s`；`go test ./internal/proto/... -count=1` → `ok 0.011s`。

## L4 T2 — push_devices 表 + store CRUD + 登记 REST（缝 S1/S2）

1. 先写 `internal/store/push_test.go` + `internal/agentd/pushapi_test.go` → `go test ./internal/store/... ./internal/agentd/... -run Push -count=1` → **RED（编译红）**：
   `internal/store/push_test.go:40:15: st.UpsertPushDevice undefined (type *Store has no field or method UpsertPushDevice)`（同法 9 条 + agentd 1 条）+ `FAIL ... [build failed]`。
2. 加 DDL（`store.go` DDL 切片尾部 `push_devices`）、`internal/store/push.go`、`internal/agentd/pushapi.go`、`server.go` 一行 `s.registerPushRoutes(api)` → 同命令 → `ok .../store 0.116s`、`ok .../agentd 2.544s`。
3. **变异自验（S1 入参白名单）**：把 `pushapi.go` 的
   `req.DeviceID == "" || req.APNSToken == "" || req.Platform != proto.PushPlatformIOS`
   断言命中唯一（`count==1`）→ 替换成 `false` → `go build ./...` 退 0（**先确认可编译**）→
   `go test ./internal/agentd/... -run Push -count=1` → **`--- FAIL: TestPushDeviceRegisterRejectsBadInput`**，原文：
   `platform 非 ios: POST → 200（{"ok":true}），期望 400`；失败计数 `grep -c '^--- FAIL'` = **1**。
   复原后同命令 `ok .../agentd 1.297s`、`ok .../store 0.190s`。

## L5 T3 — 分类 + PushFanout（缝 S3）+ collectInboxItems 抽取

1. 先写 `internal/agentd/pushfanout_test.go` → `go vet ./internal/agentd/` → **RED（编译红）**：
   `pushfanout_test.go:100:7: undefined: NewPushFanout`、`classifyLedgerEvent`、`classifyTaskEvent`、`ErrPushUnregistered`（共 9 条）。
2. 写 `internal/agentd/pushfanout.go`；`roomsapi.go` 抽 `collectInboxItems(member)`（`handleInbox` 改调它，`errInboxManagerNotReady` 哨兵保住 503 分流）+ `decisionTitle` → `bodyTitle` 复用。
3. `go test ./internal/agentd/... -run 'Push|Inbox' -count=1` → **`ok 4.126s`**；`-run Inbox -v` → **9 支 `--- PASS`**（抽取回归绿）；`-run Push -v` → 新增 12 支 Push 相关全 PASS（含 5 支登记、7 支 fanout/分类）。
4. **变异自验 M1（mention 反例）**：`if !containsMember(...)` → `if false {`（`count==1`）→ `go build ./...` 退 0 →
   `-run PushFanoutClassifyLedger` → **`--- FAIL: TestPushFanoutClassifyLedger/反例：@别人`**（父+子 2 行 FAIL）→ 复原。
5. **变异自验 M2（badge 同源）**：`badge := f.badge(n.Member)` → `badge := 0`（`count==1`）→ `go build ./...` 退 0 →
   `-run PushFanoutDeliversToRegisteredDevices` → **`--- FAIL ... pushfanout_test.go:124: badge = 0，期望 7`**（×2）→ 复原，`go build ./...` 退 0。

## L6 T4 — APNs sender + PushConfig（缝 S3 / APNs 边界）

1. 先写 `internal/agentd/apns_test.go`（6 支）+ `internal/config/config_test.go::TestPushConfigOmitemptyRoundTrip` → `go vet ./internal/agentd/ ./internal/config/` → **RED（编译红）**：
   `apns_test.go:52:52: undefined: APNsSender`、`config.PushConfig`、`NewAPNsSender`、`loadAPNsKey`（共 8 条）；config 侧 `config_test.go:979:7: full.Push undefined (type *config.Config has no field or method Push)`。
2. 加 `config.PushConfig` + `Config.Push`（`yaml:"push,omitempty"`）+ `internal/agentd/apns.go`（stdlib ES256 JWT、`/3/device/<token>`、410→`ErrPushUnregistered`）→
   `go test ./internal/agentd/... -run APNs -count=1 -v` → **6 支全 PASS**；`go test ./internal/config/... -run Push -v` → `TestPushConfigOmitemptyRoundTrip PASS`。
3. **变异自验（三发，均先断言命中唯一 + `go build ./...` 退 0 再数红）**：
   - M3a header 名 `apns-topic`→`x-apns-topic`：`--- FAIL: TestAPNsSenderRequestShape`，fails=1。
     **过程瑕疵（如实记）**：第一次跑用 `set -e` + 命令替换，测试非零退出导致函数提前返回、文件**未复原**；下一发把已变异文件当基线又复制进备份。已发现并 `grep` 确认残留后按 `x-apns-topic → apns-topic` 还原、复跑 `ok`。
   - M3b `StatusGone`→`StatusAccepted`（隔离重跑）：`--- FAIL: TestAPNsSenderMaps410`，fails=1；复原后 `ok .../agentd 0.006s`。
   - M3c `PushConfig` 去 `omitempty`：`--- FAIL: TestPushConfigOmitemptyRoundTrip`，fails=1。
4. **踩坑（既有门禁）**：全量 `go test ./internal/agentd/` 首跑 `--- FAIL: TestNoDirectHttptestServers`——本包禁直调 `httptest.NewServer`，须用 `internal/testhttp.NewServer(t, h)`。改 3 处后复跑全量 **`ok .../agentd 267.333s`**。
5. 触及包收口：`go build ./...` 退 0；`go test ./internal/config/... ./internal/store/... ./internal/proto/... ./cmd/... -count=1` → 全 `ok`（config 0.019s / store 6.808s / proto 0.015s / cmd 86.553s）。

## L7 T5 — 装配：合成钩子 + 自动化循环旁路 + bootstrap（缝 S3 集成）

1. 先写 `internal/agentd/pushfanout_wiring_test.go`（4 支）→ `go vet ./internal/agentd/` → **RED（编译红）**：
   `pushfanout_wiring_test.go:39:10: env.srv.pushFanout undefined`、`StartPush undefined`、`pushBadgeCount undefined`（共 7 条）。
2. 落地：`Server.pushFanout` 字段 + `NewServer` 构造（`cfg.Push` 配齐才建 sender，失败只 Error 并停用）+ `registerEventFrameHook` **合成回调**（D2）+ `wakeconsumer.consumeAutomationEventsOnce` 旁路注入（D1，在 `automationWakeEvents` 之后、seen 判定之外）+ `StartPush`/`pushConsoleMember`/`pushBadgeCount` + `cmd/agentd.go setupLedger` 里 `srv.StartPush(ctx)`。
3. 首跑 **2 支红**（真实断言红，非编译红）：`TestTaskEventHookTriggersPush`、`TestLedgerNeedsHumanTriggersPush` 均 `等待目标通知超时`——**根因是我漏了登记设备**（`deliver` 在 `len(devices)==0` 时按设计静默返回，测试夹具没造设备）。补 `registerDevice(...)` 后 → `go test ./internal/agentd/... -run 'Push|Frame|Automation' -count=1` → **`ok 14.866s`**。
4. **变异自验**：M4a 合成钩子 `frame(e)`→`_ = frame`：`--- FAIL: TestFrameHookStillRuns`（fails=1）；M4b 旁路 `OnLedgerEvent(ev)`→`_ = s.pushFanout`：`--- FAIL: TestLedgerNeedsHumanTriggersPush`（fails=1）。两发均先 `count==1` 断言 + `go build ./...` 退 0，随后复原。
5. 全量：`go test ./internal/agentd/... -count=1` → **`ok 267.333s`**（改 testhttp 后的同一次跑）。

## L8 T6 — client + mobilecore + bind 登记通路（缝 S1 经核）

1. 判据基线：`cd mobile && go test ./... -count=1` → `ok mobile 0.206s`、`ok mobile/bind 7.448s`（改前）。
2. 先写 `internal/client/push_test.go`（2 支）→ vet → **RED（编译红）**：`cl.RegisterPushDevice undefined (type *Client has no field or method RegisterPushDevice)` 等 3 条。
   先写 `internal/mobilecore/core_test.go` 追加两支 → **RED**：`core.RegisterPush undefined (type *Core has no field or method RegisterPush)`。
   先改 bind 三处 golden/fake/测试 → `cd mobile && go vet ./bind/` → **RED**：`bind/bind_test.go:155:12: undefined: RegisterPushDevice`。
3. 落地：`client.RegisterPushDevice/DeletePushDevice`、`mobilecore.Core.RegisterPush`（+ 内部 `clientFor`，取到 client 立即解锁再发请求）、`bind.RegisterPushDevice`（coreAPI 增 `RegisterPush` 一法）。
4. 首跑 `cd mobile && go test ./...` → **编译红**：`*mobilecore.Core does not implement coreAPI (missing method RegisterPushDevice)`——**coreAPI 方法名写成了 RegisterPushDevice，而核侧方法按 plan §3.4 叫 `RegisterPush`**。把 coreAPI/fakeCore 方法名改回 `RegisterPush`（plan 原文「bind.RegisterPushDevice 转调 core.RegisterPush」）后 → `ok mobile 0.208s`、`ok mobile/bind 7.315s`。
5. 金样本实跑核对：`go test ./bind/ -run ... -v` → `TestBindGeneratedShellAPIGolden PASS (7.27s)`（**真 gobind 产物**，Java/ObjC 两行签名逐字命中）、`TestBindExportedSurfaceIsFrozen PASS`、`TestBindRegisterPushDeviceForwardsToCore PASS`；`TestGomobileSurfaceHasNoSkips` → **SKIP（`未找到 gobind`，需真机 CI）**。
6. `go test ./internal/client/... ./internal/mobilecore/... -run Push` → 两包 `ok`；`go build ./...` 与 `cd mobile && go build ./...` → 退 0。
7. **变异自验**：M5a client 路径 `/api/push/devices`→`/api/push/device`：`--- FAIL: TestRegisterPushDevicePostsJSON`（fails=1）；M5b mobilecore 不带 `APNSToken`：`--- FAIL: TestRegisterPushWalksAgentd`（fails=1）。均先唯一性断言 + 编译通过，随后复原。

## L9 T7 — iOS 壳 APNs + SourceGuard 修订（图外；XCTest 未验证）

1. **工具链事实**：`which swift xcodebuild swiftc` → 全空（本机无 Swift/Xcode）→ **XCTest 无法在本工作树执行，本 task 全部 Swift 测试为「未验证，需 Xcode/真机 CI」**（plan §T7 已声明的例外）。机内判据改用静态形态核对（见 5）。
2. 新增 `PushRegistrar.swift` / `PushPresentation.swift` / `PushDeepLink.swift`；改 `ConnectCore.swift`（协议 + `LiveConnectCore.registerPushDevice` → `BindRegisterPushDevice`）、`AppDelegate.swift`（`UNUserNotificationCenterDelegate` 三回调）、`AppComposition.swift`（`pushRegistrar` 装配 + `onEntered` 接线）、`CookieBridge.swift`（`currentMachine`/`currentOrigin`/`pendingRoute`/`onEntered`/`openRoute`）。
   - **plan 之外的两处必要落点**（判据：spec 验收②要「点进落到对应会话/卡」，plan 只写「经 CookieBridge 加载 origin+path」没给方法名）：`CookieBridge.openRoute(_:)` 与冷启动暂存 `pendingRoute`（enter 成功后补加载）。已在注释里写明「为什么不改 `OriginLoader` 缝协议」。
   - **拒权/无 device/无活动机器/上报失败** 四条路径全部静默降级（只 `Log.shell`，不弹自造提示、不记成已上报）。
3. 测试新增：`PushPresentationTests`（3 支分支全覆盖）、`PushDeepLinkTests`（4 支：有/缺/空/无 handoff 键）、`PushRegistrarTests`（5 支：hex 逐字、暂存补报、幂等 no-op、失败保留可重试、deviceID 稳定）、`CookieBridgeTests` 追加 2 支（深链立即加载 / 冷启动暂存 + `onEntered` 触发）、`ShellSmokeTests` 追加 1 支（`BindRegisterPushDevice` 可链接可调用且空核报错）、`Fakes.swift` 的 `FakeConnectCore.registerPushDevice` 记账。
4. **SourceGuard 修订（必须，否则系统回调名必红）**：`testNoCookieBypassSymbols` 从「禁子串 `Token`」改为
   ①`Dial`/`Credential` 仍禁子串；②`tokenBypassHits(in:)` 用正则 `\b[A-Za-z0-9_]*Token\b` 抓含 Token 的词，
   **白名单只放行** `deviceToken` 与 `…WithDeviceToken`（系统回调名）；
   ③**负向自证**：同一函数对 `BindToken/AccessToken/agentdToken` 必须抓到，对
   `application(_:didRegisterForRemoteNotificationsWithDeviceToken:)` 必须放行（两条断言都在测试里）。
   `allowedBindSymbols` 增 `BindRegisterPushDevice`。
5. **静态形态核对（linux，本机真跑）**：脚本 `$TMPDIR/ios_static_guard.py` 按 SourceGuard 同款规则扫 `HandoffMobile/*.swift`（21 个文件）→ `PASS`；负向自证抓到 `['AccessToken','BindToken','agentdToken']`。
   **首跑 FAIL 三条，全部是我自己的注释踩了子串**：`ConnectCore.swift` 注释里的裸 `Token`、`PushDeepLink.swift` 注释里的 `JSONDecoder/JSONSerialization`、`PushRegistrar.swift` 注释里的 `URLSession`——guard 是**全文子串扫描含注释**。改写措辞后复跑 PASS。
6. **工程文件**：`Support/HandoffMobile.entitlements` 新建（`aps-environment=development`，`plistlib` 解析通过）；`project.pbxproj` 在 app target 的 Debug/Release 两个 `XCBuildConfiguration` 加 `CODE_SIGN_ENTITLEMENTS = Support/HandoffMobile.entitlements`（断言命中 2 处，未动 test target）。新增 `.swift` **无需**改 pbxproj——工程用 `PBXFileSystemSynchronizedRootGroup`（objectVersion 77，README 已记）。**`CODE_SIGN_ENTITLEMENTS` 与真机签名未验证（本机无 Xcode）**。
7. **Info.plist 未改**：MVP 不需要 background 推送模式（plan 判定）；`aps-environment` 走 entitlements。

## L10 收口（本节点亲跑的原始读数）

| 命令 | 原始结果 |
| --- | --- |
| `gofmt -l internal cmd mobile` | 空（零未格式化文件） |
| `go vet ./...` | 退 0（无输出） |
| `go build ./...` | 退 0 |
| `go test ./... -count=1`（第一次，加 badge 断言前） | `GO_TEST_EXIT=0`，`ok` 包数 **65**，无 `FAIL`/`--- FAIL` |
| `go test ./... -count=1`（收尾复跑，含新增 badge 断言） | `GO_TEST_EXIT=0`，`ok` 包数 **65**，无 `FAIL` |
| `cd mobile && go test ./... -count=1` | `ok mobile 0.164s`、`ok mobile/bind 10.544s`，`MOBILE_EXIT=0` |
| `cd mobile && go build ./...` | 退 0 |
| `codegraph --repo . check` | `"fails": []`（exit 0）；`git status --porcelain codegraph/` 空 |
| `python3 $TMPDIR/ios_static_guard.py` | `PASS: iOS 壳静态 guard 全过`（21 个壳源文件；负向自证抓到 BindToken/AccessToken/agentdToken） |
| XCTest（`PushPresentationTests` 等 6 处 Swift 测试改动） | **未验证——本机无 swift/xcodebuild（`which` 三者皆空），需 Xcode/真机 CI** |

## L11 判断与偏差（review 可退）

1. **mention 深链按 plan §3.6 决策 D4（有 card_id → `/cards?card=<id>`），与 plan §T3 分类表的 mention 行（写 `/`）不一致——两处 plan 自相矛盾时取 D4**：D4 是标了「计划决策」的一般规则，spec 验收②也要求「落到对应会话/卡」；表里的 `/` 只当无卡兜底。测试 `room_message @我 卡级` 断 `/cards?card=B2`、`无卡` 断 `/`。
2. **ticket（permission/question）深链落 `/`**：事件载荷里没有卡号，`PushFanout` 按 plan §3.2 只持 `*store.Store`，拿不到 task→card 映射——走 spec 验收②的兜底分支（落工作台「需要你处理」）。RefID 用 `e.TaskID`（plan 原文）。
3. **`errInboxManagerNotReady` 哨兵**：`collectInboxItems` 抽取后要同时服务 HTTP（503）与 fanout badge（降级 0），故用哨兵区分，`handleInbox` 仍回 503、文案与日志与原实现一致。
4. **`decisionTitle` → `bodyTitle` 拆分**：分类函数要复用「首行+80 截断」投影，不复制第二份截断逻辑（plan 要求「复用 decisionTitle」）。
5. **mention 标题截断**：plan 表写「`@你：<body 截断>`」，收件箱原实现是原样 body——推送侧按 plan 截断（横幅需要有界长度），收件箱行为未改。
6. **`coreAPI` 方法名取 `RegisterPush`**（plan §3.4 原文「`bind.RegisterPushDevice` 转调 `core.RegisterPush`」）；首版写成 `RegisterPushDevice` 导致 `*mobilecore.Core does not implement coreAPI` 编译红，按 plan 改回。
7. **`mobilecore` 新增私有 `clientFor`**：取 client 后立即解锁再发 HTTP，不把网络往返压在核锁上（`Origin` 不涉及 I/O，未动）。
8. **额外测试**（plan 清单之外，均为缝级/验收级）：`TestPushFanoutNilSenderIsSilent`、`TestPushFanoutClassifyRequiresMember`、`TestPushBadgeCountMatchesInbox`（验收②角标同源）、`TestPushDeviceRegisterRequiresConsoleUser`（plan T2 负面项）、CookieBridge 深链两支、ShellSmoke 的 `BindRegisterPushDevice`。
9. **未验证项（不写成结论）**：①XCTest 全套；②APNs 真实投递/HTTP/2/Apple 接受 JWT；③iOS 真机授权、横幅抑制、点进跳转、角标；④`CODE_SIGN_ENTITLEMENTS` 生效性；⑤`TestGomobileSurfaceHasNoSkips`（PATH 无 gobind，SKIP；`go tool gobind` 的 shell golden 已真跑 PASS）。
10. **图覆盖债**：`PushFanout/PushSender/APNsSender/registerPushRoutes/pushBadgeCount/collectInboxItems/RegisterPush` 均未入图（`codegraph sym PushFanout` 未命中）；图的 `handleInbox` 读数与工作树不一致（陈旧）。`codegraph check` 对存量边零违规。

## L12 提交事实（历史读数，随后 amend 一次收进同批）

- `git add -A && git commit` → 产出提交 **`849c72fe`**（44 文件，+3064/−36），分支 `cards/B432-charter-2`，`git status --short` 为空（工作树干净）。
- 提交后追加本节并 `git commit --amend --no-edit`（HEAD 换 hash 属 git 事实，不回写台账 chase）。

## L10 修刀（charter-3：抑横幅按路由判定对应面）

- 修刀令（2026-10-10 产品+架构）：抑横幅必须按路由/对应面 onRelevantScreen（接到当前路由：对应卡或工作台「需要你」入口），禁止用 currentMachine!=nil 近似。别处前台仍弹系统横幅。基线 cards/B432-charter-2；只改 iOS 壳/桥接。
- 图查询：`codegraph sym PushPresentation` / `sym currentRoute` / `sym CookieBridge` 均**未命中**（壳 mobile/ios 图外）——记图覆盖债，按纪律回落直接读文件。
- 真码事实：`AppDelegate.swift:43` 现为 `let onRelevantScreen = AppComposition.current?.cookieBridge.currentMachine != nil`（进入控制台即抑制，别处前台也弹不了——正是修刀禁的近似）。
- 路由事实：壳 SPA 走 BrowserRouter（`web/src/App.tsx:29`）；工作台首页 path=`/`（缺省 tab=projects，MobileWorkspace 顶部「需要你处理」行即聚合入口）；`/?tab=projects` 与 `/` 同面；卡对应面=`/cards?card=<id>`（`Shell.tsx:921` 深链形状）。WKWebView 的 `url` 属性随 SPA pushState 同步。
- 判定语义（本刀）：onRelevantScreen = （当前路由与深链逐字一致）OR（当前路由是工作台首页 `/` 或 `/?tab=projects`——聚合对应面在场）。别处（别的卡、`/cards` 列表、`/?tab=settings`、未加载）前台仍弹。
- XCTest 本机无 toolchain（`which swift swiftc xcodebuild` → command not found）——与 plan T7 同判：机内只做静态核对 + mobile Go 模块回归；XCTest 红绿归 Xcode/CI，如实记未验证。

## L11 修刀落地与收口

1. 测试先行（先红，未能本机见红——无 Xcode toolchain，如实记）：
   - `PushPresentationTests` 增 8 支 `isRelevantRoute` 用例（对应卡/别的卡/工作台首页/无深链各处/空串/残参）。
   - `CookieBridgeTests` 增 4 支 `currentRoute` 记账用例（进入无暂存=「/」、带暂存=深链、openRoute 跟随、未进入=空串）。
   - **未验证**：`which swift swiftc xcodebuild` → command not found（linux 无 Apple toolchain）。XCTest 红绿与变异自验归 Xcode/CI，与 plan T7 同判。
2. 实现：
   - `PushPresentation.isRelevantRoute(notificationRoute:currentRoute:)`（纯函数）：空串→别处；`isWorkbenchHome`（path=/ 且 tab 缺省或 projects）→在场；否则深链与当前路由同为 /cards 宿主且 card 参数相等才算对应卡。
   - `CookieBridge` 增 `private(set) var currentRoute`：enter 成功（inject）落「/」或暂存深链；openRoute 跟随；另挂 `webView.url` KVO 镜像 SPA pushState（`NSKeyValueObservation`，deinit 自动失效）。
   - `AppDelegate.willPresent` 改为 `PushDeepLink.route(userInfo)` + `cookieBridge.currentRoute` → `isRelevantRoute` → `shouldPresent`；**删除** `currentMachine != nil` 近似。日志带 route/current/decision 三元。
   - 修一处测试笔误：`/?card=B432` 宿主仍是首页（card 只在 /cards 宿主有语汇）→ 应判在场 true，已改断言并注释理由。
3. 亲跑命令与原始输出：
   - `go build ./...` → 退 0（BUILD_OK）。
   - `go vet ./...` → 退 0（VET_EXIT=0）。
   - `cd mobile && go test ./... -count=1` → `ok mobile 0.160s`、`ok mobile/bind 7.589s`；`go vet ./...` 退 0。
   - 静态 SourceGuard（grep 复刻）：URLSession/JSONDecoder/JSONSerialization 零命中；Bind 符号全在白名单；Token 语境仅 APNs device；`handoff_session` 常量在场——四项 PASS。
4. 逻辑桌核（isRelevantRoute 对 8 支用例逐条走查，全符预期；非执行证据，仅记推演）。
5. 变异自验：**未验证**（Swift 测试本机不可执行）。
6. 图覆盖债：`PushPresentation/isRelevantRoute/currentRoute/CookieBridge` 均不在图（壳图外）——本节点持续记债。
