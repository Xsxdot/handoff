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
