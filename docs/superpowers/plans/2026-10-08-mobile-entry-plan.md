# 移动端入口与设置实现计划 v1

对应 spec v1 的 S1–S5，用户批准于 2026-10-08。执行者：本地 gpt-6-luna 子 agent；协调者负责文档、审查与独立复跑。分支 `codex/mobile-entry-settings`，基线 3e52eb462b7e0213db4c4b8988db91dd3083e84c。不挂入已完成 B417；本轮是独立的用户授权实现请求。

## 边界与顺序

执行者统一拥有 `mobile/android`、`mobile/ios`、`web/src/app/settings` 及直接必要的资源/测试和窄壳能力模块。父 agent 拥有本 spec/contract/plan、台账与批准视觉文件。没有其他实现工作者；仍须保护所有无关修改。复用现存 app、prefs、导航与样式，不启动新 Product Design 模板。

1. 先为真实启动/进入消费入口加红色行为反馈：记忆恢复、离线 A 不切 B、切机失败保留 A、失败主框不落选择。先见断言红再写行为代码；不得写仅服务测试的协调对象。
2. 在两端增加 app-local 当前机器偏好，接入既有进入/恢复/cookie 生产链。主框成功加载后保存；重配成功重新进入首次选择；失败不清理上一有效选择。取消切机可回当前工作台。
3. 按批准稿实现品牌启动、欢迎/配对、机器选择与失败状态。品牌标志复用现有 appicon；欢迎插图由父 agent 使用 image_gen 生成并提供，执行者复制到两端资源，不使用手工 SVG/CSS 拼插图。无摄像头保留粘贴；粘贴用单独输入而不是欢迎页巨型 JSON 框。
4. 接入精确同源导航和 DOM 显示上下文，严格按新 contract；两端同源/异端口/相似路径/query/fragment/非主框反例须穿过实际导航策略。不得追加任意命令协议。
5. 重排移动 SettingsHub，工作方式/显示/项目可见性独立子导航；桌面旧设置完整保留。相关组件测试验证所有旧入口与真实 prefs 消费，不为纯样式写镜像测试。
6. 局部 tests 绿后做两端完整编译、Web build/typecheck。协调者检查 diff，集成时运行全部触及子系统 tests，并留实际视觉证据或明确运行阻碍。

每项行为实现均包含：

- [ ] 在恢复、尝试进入、成功加载保存、切机请求、失败出口加平台现有结构化日志；只记状态/非敏感标识，绝不输出 bundle/cookie/token。Web 控件沿现有观测设施，不加 console.log。
- [ ] 新文件头注明职责/边界；导出 API 有文档；说明导航严格匹配、选择晚提交与离线不回落的原因。

## 具体入口与验证

Android：AppGraph / StartupController / PairingActivity / MachineListActivity / WebviewActivity / EnterCoordinator / WebviewSessionBinder；iOS：RootViewController / PairingViewController / MachineListModel / MachineListViewController / ConsoleWebViewController / CookieBridge；Web：SettingsPage / SettingsHub / GeneralPage / useWebPrefs 与既有 tree prefs。

当前已核：Web scripts 为 `npm run build`、`npm run typecheck`、`npm test`（vitest run）；Android `./gradlew :app:testDebugUnitTest :app:assembleDebug`，需要根 dist/mobile AAR；iOS xcodebuild -project HandoffMobile.xcodeproj -scheme HandoffMobile -sdk iphonesimulator -configuration Debug -derivedDataPath build CODE_SIGNING_ALLOWED=NO build，需要根 XCFramework。核心不存在时用现有 `mobile/build.sh android|ios`，不能改绑定面。当前本机已核 Xcode / JDK17 存在，尚无 booted simulator，工作树未发现核心构建产物；工具链可用性由实际构建决定，不采信旧台账。

视觉验收使用批准截图；不得以测试全绿替代真实截图对照。SuperDev agent 当前 connection refused，服务/浏览器状态未能核实；禁止绕过它随意启动受管服务。先继续源码、编译、组件测试与原生可用运行验证，环境限制单独记录。

## 决策权限与交棒

执行者可调私有结构与资源布局，不能省掉已批准当前机器名、任何旧设置入口或任一平台。同一失败修复三次仍无根因则上报。禁止 commit/push/部署/安装；交付文件清单、准确运行命令/结果、剩余阻碍与截图。图覆盖债沿 spec 记账；日志/注释、架构与故事同时纳入审查。
