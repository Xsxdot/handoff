# B432 本地签名与真机验收记录

- 日期：2026-10-11（Asia/Taipei）。
- 接管席位：`codex#01a129dd-dc00-7b12-b254-3ae908545f61`，`handoff card bind B432 --cli codex --session ...` 返回 `{"ok":true}`。
- 被测基线：`46c77cd944a99f0e82235094e8fbfda0f85a5f3d`；工作树 `/Users/xushixin/.handoff/worktrees/manual/B432`；主线未合并。
- 卡仍在 acceptance，本记录不是最终验收通过。

## 编译缺陷与修复

付费 Team 构建在 `PushRegistrar.swift:88` 报错：

```text
error: reference to property 'deviceID' in closure requires explicit use of 'self' to make capture semantics explicit
```

根因是 OSLog 插值闭包要求显式属性捕获。一行修改为 `self.deviceID`，保留原日志与行为；修后相同构建命令通过。

## 本轮已跑证据

1. `xcodebuild -version`：Xcode 26.6，Build 17F113。
2. Arc 中 Apple Developer 门户显示 `zhu xuemei - FZ8VD4RF7B`；Keys 列表为空。
3. `xcodebuild -project mobile/ios/HandoffMobile.xcodeproj -scheme HandoffMobile -configuration Debug -destination 'generic/platform=iOS' -derivedDataPath /private/tmp/b432-paid-build DEVELOPMENT_TEAM=FZ8VD4RF7B CODE_SIGNING_ALLOWED=YES CODE_SIGN_STYLE=Automatic -allowProvisioningUpdates build`：修后 `** BUILD SUCCEEDED **`。日志 `/private/tmp/b432-paid-build-fixed.log`。
4. `codesign -dv --verbose=4`：TeamIdentifier=FZ8VD4RF7B；Authority=Apple Development: zhu xuemei (K5Y96U5FV6)。`codesign -d --entitlements -`：application-identifier=FZ8VD4RF7B.dev.handoff.HandoffMobile，aps-environment=development。
5. `xcrun devicectl device install app --device 10B67598-3DC1-518F-8A1B-FE730726A1DC /private/tmp/b432-paid-build/Build/Products/Debug-iphoneos/HandoffMobile.app`：`App installed: bundleID: dev.handoff.HandoffMobile`。随后 `device info apps` 确认 HandoffMobile 1.0 / build 1。
6. `xcrun devicectl device process launch --device ... dev.handoff.HandoffMobile`：失败，系统明确返回 `Locked (Unable to launch ... because the device was not, or could not be, unlocked)`；已请求用户解锁手机、打开 App 并允许通知。
7. `go test ./internal/agentd ./internal/store ./internal/client ./internal/mobilecore -run 'Push|APNs|APNS' -count=1`：四包均 `ok`，日志 `/private/tmp/b432-push-tests.log`。
8. `xcodebuild ... -destination 'platform=iOS Simulator,id=D46C1CEA-1A44-41A6-819F-0EC47780F965' -derivedDataPath /private/tmp/b432-push-simulator CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- -only-testing:HandoffMobileTests/PushRegistrarTests -only-testing:HandoffMobileTests/PushPresentationTests -only-testing:HandoffMobileTests/PushDeepLinkTests test`：`** TEST SUCCEEDED **`，20 个测试通过。日志 `/private/tmp/b432-swift-push-tests.log`；xcresult 位于该 derivedDataPath 的 Logs/Test 下。这是模拟器单测，不是 APNs 真机投递证据。

## APNs 草稿与待验

- Arc 门户已准备密钥 `Handoff APNs B432`：Sandbox，Topic Specific，仅 `dev.handoff.HandoffMobile`。未保存/提交，未创建凭据；工具要求创建持久安全凭据时当场确认，已向用户请求确认。
- 密钥创建获确认后，下载 `.p8`，保存到本机 `~/.handoff/secrets/`（不入仓），再填 `push.apns_key_id`、`apns_team_id`、`apns_bundle_id`、`apns_key_file`，本次开发签名对应 `apns_host: api.sandbox.push.apple.com`。
- 本机 `handoff version` 显示 46c77cd944a9；当前 config 未有 `push:`。
- SuperDev `list_services(project_name=handoff)` 返回 agent_unavailable，127.0.0.1:57017 connection refused；尚未通过 SuperDev 变更服务配置或重启。
- 未验：真实授权与 APNs device 登记、APNs 接受 JWT / 真实通知、后台横幅、前台抑制、点击深链、角标、拒权降级、承重变异；完整最终验收尚未通过。
- 首次设备构建命令误用了未经验证的 destination id，返回找不到设备；随后改用 generic/platform=iOS 完成构建，并以 devicectl 实际返回的设备 ID 安装。该失败不算设备或签名失败。

## 图覆盖债

`codegraph sym PushRegistrar` 未命中，图未覆盖 iOS 壳；本轮回落真码与 Xcode 编译验证。

## 16:44 续接：密钥已创建

- 用户明确确认创建限定密钥，并报告已打开手机 App。
- Arc 完成保存配置、Register、Download；门户 Keys 列表确认 `293LP5343J / Handoff APNs B432 / 1 Topic / Sandbox`。
- `.p8` 安全副本：`/Users/xushixin/.handoff/secrets/AuthKey_293LP5343J.p8`，文件权限 600，secrets 目录权限 700。下载原件也改为 600。`openssl pkey -check -noout` 返回 `Key is valid`，未输出私钥内容。
- 已准备 `/Users/xushixin/.handoff/secrets/b432-push.yaml`（600）：key_id=293LP5343J，team_id=FZ8VD4RF7B，bundle_id=dev.handoff.HandoffMobile，key_file 指安全副本，host=api.sandbox.push.apple.com。尚未写入运行 config。
- `devicectl device process launch` 本轮返回 `Launched application with dev.handoff.HandoffMobile bundle identifier.`，先前锁屏阻塞已解除。
- 只读查询本机 `handoff.db`：push_devices 表存在，设备行仍为空；已请求手机进入 mac-pro 控制台触发上报。
- `handoff service status` 确认由 launchd 托管；plist 的 ProgramArguments 指向 `/Users/xushixin/.local/bin/handoff agentd --config /Users/xushixin/.handoff/config.yaml`。
- SuperDev 再次返回本机 Agent connection refused；桌面 UI 也显示 Agent 未连接。为遵守技能的替代执行限制，已请求用户授权本次直接写入配置并经 `handoff service restart` 重载。尚未重启服务。


## 16:54 更正版原生壳、服务配置与验收边界

- 用户报告第一包仍是旧移动端。根因：B432 基线包含 Web B429，但此前原生 iOS 入口、持久机器选择与设置导航修改仍留在旧工作树未提交，因此未进入第一包。第一包签名/安装成功不等于新版界面正确。
- 原生来源：`/Users/xushixin/.codex/worktrees/882f/handoff`，HEAD `3e52eb46`；iOS tracked diff 加 11 个 untracked 文件与 mobile-workspace-ui 来源一致，摘要 `e6d7b4419b08b3cbd9ae5076d7d940f1a2986ab92bd648fa803f3738136e44a8`。两个来源工作树均未改动。
- 独立集成工作树：`/Users/xushixin/.codex/worktrees/b432-current-ios/handoff`，基线 `46c77cd944a99f0e82235094e8fbfda0f85a5f3d`。三方应用原生 iOS 修改，人工合并 AppComposition 和 project.pbxproj 的冲突，保留原生 currentMachine/selection 与 B432 PushRegistrar/onEntered 补报、APNs entitlements；同步一行 self.deviceID 编译修复。未合并主线、未提交。
- 付费 Team 真机构建通过，日志 `/private/tmp/b432-current-ios-device.log`。
- iOS 全套 simulator 测试新鲜复跑：76 passed / 0 failed / 0 skipped；xcresult `/private/tmp/b432-current-ios-tests/Logs/Test/Test-HandoffMobile-2026.10.11_16-50-13-+0800.xcresult`。这是测试证据，不代表真机通知送达。
- 源 Info.plist 版本为固定 1，因此 build 命令的 CURRENT_PROJECT_VERSION 未改变版本。仅对已构建 .app 的 Info.plist 标记 build 43202，再用同一 Apple Development 身份和原 build.app.xcent 重新签名；codesign --verify --deep --strict 通过。签名 Team FZ8VD4RF7B，application-identifier=FZ8VD4RF7B.dev.handoff.HandoffMobile，aps-environment=development。
- 16:51 devicectl 安装成功；16:54 device info apps 再确认 Handoff 1.0 / build 43202。启动被 iPhone 再次锁屏拒绝，未验证屏幕显示新界面。已请求用户解锁、打开新版并进入 mac-pro。
- 用户授权本次 SuperDev Agent 离线时直接写入配置并用官方 service 命令重载。配置写入前完整安全备份，原配置其余字节保留，添加 sandbox APNs push 段；配置解析预览通过。
- 16:49:26 `handoff service restart` 成功；16:50 `handoff status --json` 确认 reachable=true、degraded=false，CLI/agentd revision 均为 46c77cd944a99，启动时间 16:49:28。配置使用 key_id=293LP5343J / Team FZ8VD4RF7B / topic dev.handoff.HandoffMobile / sandbox host；p8 安全路径及权限同前。
- 16:51 只读查询 push_devices 仍为空。尚无真实设备登记、APNs HTTP 200、后台横幅、前台抑制、点击路由或角标新鲜证据。B432 保持 acceptance，不能据构建与单测标记最终验收通过。

- 集成 iOS 源文件清单 SHA256：`2389d7451196602178736240726f641bc06e928200d8df5423d4627adb50d07f`。


## 17:03 真机链路已跑通（用户现场确认）

- 用户确认 build 43202 的新版界面正确，并已进入 mac-pro。
- 17:00:25 push_devices 出现 iOS 设备：member=user:sycm，device_id=399D489D-D382-46A6-B91B-7A0188597CEA，APNs device 串长度 64。只读查询未输出 token 明文。
- 用户确认已返回手机桌面，将 App 留在后台。
- 17:01:54 通过正式 CLI 创建 B432 测试裁决 #15，走 PG decision_opened → mac-pro 事件消费 → push fanout → APNs → iOS 系统通知路径；用户现场回复「收到通知，点开进入 B432」。这是实际后台通知及点击卡片落点证据，不是 mock/模拟器证据。当前日志级别未留 HTTP 200 状态，因此不另声称已捕获 APNs HTTP 响应。
- 17:02:46 创建前台抑制测试裁决 #16；等待用户确认系统呈现。两条测试正文中的时间标签仅为标题标识，以上时间以正式 created_at 为准。
- 17:03:06 handoff room inbox 新鲜返回 count=2，且仅包含 #15/#16；角标应对照该计数，尚待用户核对实际数字。
- 待验仍包括前台相关/非相关面呈现、实际角标、无深链兜底、拒权/无 token 降级及验收承重变异；不因后台单条成功标记 B432 最终验收通过。


## 17:04 前台抑制与承重变异

- 用户对 #16 明确回复：「站内有新裁决，没有系统横幅」。相关卡前台抑制实测通过。
- 承重变异选生产账本消费接线：wakeconsumer.go 中唯一的 `if s.pushFanout != nil { s.pushFanout.OnLedgerEvent(ev) }` 改为 `if false && ...`，保持编译有效；命中计数断言为 1。
- 原状态 TestLedgerNeedsHumanTriggersPush 新鲜跑绿；变异后实际运行测试（编译通过），该真实账本写点→消费→fake sender 接缝测试因「等待目标通知超时」失败，exit 1；finally 原字节还原后同测试 exit 0。日志 `/private/tmp/b432-mutation-ledger-hook.log` 与 `/private/tmp/b432-mutation-restored.log`。该变异仅在本地工作树用于验收，未改运行服务。
- 正等待手机实际角标数字；前台别处呈现、无深链工作台落点与拒权降级尚未核销。


## 17:05 角标红色反馈与边界排查

- 用户实际返回桌面后回复「无角标」，当时正式 inbox count=2。角标项未通过，不按 spec 最终收口。
- 单变量实验：保持后台，再创建无 card_id 的正式裁决 #17（17:05:02），应使 count=3；等待用户确认角标及工作台落点。此实验区分后台与前台呈现路径，不在未取证处修改实现。
- 已确认源码无 applicationIconBadgeNumber/setBadgeCount 清零；AppDelegate 前台 completionHandler 仅 [.banner,.list] 或 []，都未含 .badge。Apple 文档明确 .badge 选项用于应用通知的角标值，因此前台角标更新另有待复验的代码缺口；尚不能凭这点解释首条后台后的无角标，需继续核对后台与系统 badge 权限。
- Go 推送四包并发检查本轮新鲜复跑通过：go test -race ./internal/agentd ./internal/store ./internal/client ./internal/mobilecore -run 'Push|APNs|APNS' -count=1；日志 /private/tmp/b432-current-push-race.log。


## 17:06 后台角标与前台角标缺口

- #17 用户现场确认「角标 3，点开进入工作台」。后台角标==正式 inbox 未处理数、无 card_id 落点均实测通过。
- 原始前台回调返回 [.banner,.list] 或 []，均不含 .badge；系统前台呈现选项不应用新的角标值。参考 Apple 官方 UNNotificationPresentationOptions 文档：https://developer.apple.com/documentation/usernotifications/unnotificationpresentationoptions 。后台路径已经显示 3，排除了通知 badge 权限整体关闭与服务端计数总为 0 的假设。
- 将现有前台策略到系统选项的转换提取为 PushPresentation.foregroundOptions 并接入 AppDelegate，提取阶段保留旧语义；新增两个回归用例分别检查相关面/别处均须含 badge。原行为真实 XCTest 编译通过，两个新增用例因 badge 缺失失败（xcodebuild exit65），其余策略测试通过；红证据 /private/tmp/b432-badge-red.log。
- 随后小修为相关面 [.badge]、别处 [.banner,.list,.badge]；抑制只针对重复横幅，角标继续采用 APNs 计数。正在全套 XCTest 与付费 Team 构建复跑，尚未换装或真机核销此修复。
- 第一处用户反馈「无角标」记录保留，不改写为曾显示 2；当前已证明后台 3 正常，修复前前台不会应用新计数。初始后台通知点开后为何没有保留 1，未另行取证，不把代码缺口冒充全部系统行为的唯一原因。


## 17:09 前台角标修复构建复跑

- 修复后完整 iOS XCTest 78 passed / 0 failed / 0 skipped，TEST SUCCEEDED；结果 /private/tmp/b432-current-ios-tests/Logs/Test/Test-HandoffMobile-2026.10.11_17-07-57-+0800.xcresult，日志 /private/tmp/b432-badge-green.log。
- 付费 Team 真机构建 BUILD SUCCEEDED，日志 /private/tmp/b432-badge-device.log。
- 仍仅对已构建 artifact 标记 CFBundleVersion=43203，用既有 Apple Development 身份与原 app.xcent 重新签名；codesign --verify --deep --strict 通过。
- 17:09:15 devicectl install app 成功。正在启动手机 App；尚未据安装结果声明前台角标修复的真机行为通过。

- 17:10 devicectl info apps 再确认 Handoff 1.0 / build 43203。启动再次被 Locked 拒绝，已请求用户解锁打开 mac-pro 首页，前台角标修复真机项等待该动作。
- build 43203 集成 iOS 源文件清单 SHA256：`a378e0a1cd99d098b9f64e0d4ff846d0d2c6ac0aa34fe67fc528d74604377f99`。
- 保留测试裁决 #15/#16/#17 未答复以固定下一轮角标基准 count=3；验收后仅清理本轮这些测试裁决，不改真实裁决。


## 17:18 build 43203 真机复验继续

- 用户解锁后明确回复「已打开首页」，在 mac-pro 工作台前台开始复验。
- 17:18:45 正式创建 B432 测试裁决 #18；发送前沿用旧基准误报预期角标 4，随即 `handoff room inbox` 新鲜返回 count=3（#16/#17/#18，#15 已不在未处理列表），已当场向用户更正：当前预期为 3。#18 的标题文字仍含旧预计 4，验收以正式 inbox 当前值为准，不能据标题误判。
- 正等待用户确认 build 43203 前台无横幅与图标角标=3。仍未核销前台别处横幅、拒权降级，B432 保持 acceptance。


## 17:23 用户撤回误选，重新建立真机反馈

- #18 的问答曾选「没有横幅，但角标不对」，随后用户明确更正：「我点错了，刚才我没看到，再来一遍」。该选项不作为角标失败或成功证据；保留撤回痕迹，build 43203 前台角标仍待验证。
- 重测前正式 room inbox count=2，仅 #16/#18；此前 #17 已不在未处理列表。本轮不沿用旧计数。
- 用户重新明确确认「已就绪」，保持 mac-pro 首页前台。17:23:35 正式创建重测裁决 #19；立即查询 room inbox 返回 count=3（#16/#18/#19），已向用户说明本轮实际预期角标 3。标题不再嵌入固定计数，防止期间处理其他裁决造成误导。
- 等待用户对 #19 的前台横幅及图标角标反馈；尚未把修复标为真机通过。


## 17:24 build 43203 前台角标已实测通过

- #19 用户现场明确回复：「没有横幅，角标 3」。与创建后正式 room inbox count=3 一致；新版前台角标修复及首页重复横幅抑制同时通过真机复验。
- 此结果替代的是 #18 未观察的重测，不覆盖或篡改最初 build 43202 的无角标反馈。
- 下一项为前台其他页面（mac-pro 设置页）的横幅呈现，等待用户准备。


## 17:29 真机清单核销与通知恢复

- #20（17:24:40）：用户先确认位于 mac-pro 设置页，收到测试裁决后明确反馈「看到了系统横幅」。前台别处仍呈现通过；不是只在已读面一律抑制。
- 用户在 iPhone 系统设置关闭 Handoff 允许通知并重新进入 mac-pro。17:26:49 devicectl --terminate-existing 后实际启动成功，走 App 启动授权链；push_devices 的更新时间仍为 09:22:18 UTC，没有把拒权重新记成获权登记。
- #21（17:27:34）：正式创建拒权降级测试裁决，正式 inbox count=5（#16/#18/#19/#20/#21）。用户明确反馈「都符合，站内能正常处理」：App 正常、站内测试裁决可见、没有系统通知/角标/推送成功提示。拒权降级通过。
- 用户明确确认「通知已恢复」，包含允许通知与角标开启、进入 mac-pro。
- 清理前逐一核对 #15–#21 均为本轮 B432 测试内容，且已经由用户处理为 answered；没有覆盖这些答案，没有修改真实裁决。正式 room inbox 新鲜返回 count=0。
- 本轮真机 T7 清单：授权与登记、后台通知、前台相关面抑制/别处呈现、卡片与无卡片工作台落点、后台/前台角标同源、拒权降级均已有本轮证据。APNs 出站真实事件源实测为 decision；ticket/mention/needs_human 的分类与生产接线覆盖依赖 Go 集成/并发测试，不声称这些事件类别已逐条在手机上发送。

## 当前树全量检查

- go test ./... -count=1：exit 0，日志 /private/tmp/b432-final-go-tests.log。
- go vet ./...：exit 0，日志 /private/tmp/b432-final-go-vet.log（空输出）。
- gofmt -l 对所有入库 Go 文件：exit 0，unformatted=[]。
- mobile 子模块 go test ./... -count=1：exit 0，mobile 与 mobile/bind 均 ok，日志 /private/tmp/b432-final-mobile-tests.log。
- iOS 全套 78 passed / 0 failed / 0 skipped 与真机构建、签名证据见 17:09 节；检查期间没有进一步代码修改。
- 图覆盖债仍为既有 iOS 壳图外覆盖缺口；不声称 Swift 新文件已被 codegraph 纳入。代码变动仅在 mobile/ios；APNs 服务端与绑定契约沿用已审 46c77cd9，未新增 REST/Go/export 字段。

## 本地 main 集成与收尾（2026-10-11）

- 用户选择「本地合并进 main」。主工作区 `/Users/xushixin/workspace/handoff` 合前干净，`git merge --ff-only codex/b432-native-apns` 成功，main 从 `845a7e80` 快进到 `2ed0e1f1`；未推送远端。
- 合后根模块 `go test ./... -count=1` exit 0，65 个包通过；`go vet ./...` exit 0；全部 tracked Go 文件 `gofmt -l` 为空；`git diff --check` 通过。日志 `/private/tmp/b432-merged-go-tests.log`、`/private/tmp/b432-merged-go-vet.log`。
- 合后 mobile 模块 `go test ./... -count=1` exit 0，mobile 与 bind 两包通过。日志 `/private/tmp/b432-merged-mobile-tests.log`。
- 首轮合后 iOS 测试 exit 65，原因为 main 工作区忽略的旧 XCFramework 缺少 `BindRegisterPushDevice`，两切片头文件均确认缺失。先备份到 `/private/tmp/b432-main-xcframework-before-merge`，再从当前 main 执行 `PATH="$HOME/go/bin:$PATH" ./mobile/build.sh ios`，exit 0，两切片均生成该接口。未为产物问题修改源代码。
- 重建后同一 main 上完整 `xcodebuild ... test` exit 0，`TEST SUCCEEDED`，78 测试通过、0 失败。日志 `/private/tmp/b432-merged-ios-tests-rebuilt.log`；xcresult `/private/tmp/b432-merged-ios-tests/Logs/Test/Test-HandoffMobile-2026.10.11_17-41-11-+0800.xcresult`。
- 本卡没有 codegraph 视图 diff，按卡流直接 acceptance → finish；没有可 absorb 的本卡图增量。Go 推送路径与 Swift 壳的已知图覆盖债均已收进 `docs/roadmap.md`。未变更 Web 页面或原型副本，不做原型基准回灌。
- 文档事实对齐：mobile 绑定契约列表补 `RegisterPushDevice`，iOS README 的 Bind 函数计数由七改为八。收尾只有文档改动，业务源代码仍为已验 `2ed0e1f1`。
- 五个挂卡远程任务均经 `handoff show --target linux-01` 确認 state=completed，无待归档任务。原有移动端工作树与签名/密钥现场保留；设备继续使用已验 build 43203，通知已由用户恢复。
- 整体仍未合并主线或归档；集成方式须由用户裁定。本地生产配置与私钥不入 Git，原生 UI 来源工作树未改动。
