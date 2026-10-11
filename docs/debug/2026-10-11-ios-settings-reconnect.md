# iPhone 项目显示偏好与前台恢复排查

基线：bf1976f1df860b39dc818991a6ef72f9e79628a6；工作树初始干净。用户确认 iPhone App、设置内项目勾选。连接设备：iPhone 15 Pro，已装 dev.handoff.HandoffMobile 1.0 (43203)。SuperDev list_services(handoff) 返回 0 service(s)，该工作树未登记运行服务。

## 根因与证据

- 偏好路径：GeneralPage → useTreePrefs → localStorage[handoff.tree.prefs]；MobileWorkspace 订阅同一状态并过滤 hiddenProjects。当前源码存在立即生效路径，尚不能把用户的立即不生效现象归因为它。
- iOS CookieBridge 未提供跨回环源的设备偏好保存；Core.startLoopback 使用 127.0.0.1:0，新进程产生新端口；浏览器存储按 origin 隔离。
- `npm test -- src/app/data/usePoll.test.ts`：新增前台恢复测试失败（expected calls 2, got 1）；新增 401 恢复测试失败（expected calls 1, got 2）；既有 12 条通过。
- `go test ./internal/relay -run TestTunnelOutlivesHandshakeContext -count=1`：失败 `tunnel lost after handshake cancellation: ... session shutdown`。ensureTunnel 将握手 ctx 交给共享 websocket.NetConn，取消握手 ctx 关闭整条隧道。

## 本地修复计划（bug 流）

目标：iPhone 勾选立即影响工作台，重开后保留；后台挂起请求不堵住前台重拉；握手结束不结束共享隧道；真实 401 保持终止态。

边界：iOS CookieBridge 的设备视图偏好存取、Web usePoll 的请求生命周期、relay 的共享隧道生命周期。认证 cookie、主令牌与配对协议沿用既有核，不从网页向原生暴露它们。iOS 仅备份 handoff.tree.prefs 一个设备显示偏好键，限定当前回环源主框；这是既有设备偏好持久化的内部适配，不新增服务 API。

顺序：先新鲜红测试 → 最小修复 → 触及包测试与全量编译 → iOS 构建与模拟器 WebKit roundtrip → 真实手机可验证范围。真机接入属于本地环境，故本会话 inline 执行；不把手机验收派远程。

观测：偏好恢复/保存带长度与结果、不记值；轮询取消/恢复；relay 既有握手/连接日志保留。新 helper 文件注释限定职责、来源和键范围。

## 图覆盖债

codegraph sym Shell 命中 n_web_app_shell_Shell；MobileWorkspace、WebviewActivity、ConnectCore 未覆盖，已回落源码。新增/触及 iOS 偏好适配、usePoll 的图覆盖待核对。

## 验证台账

以上命令已实际跑到结果；尚无修复绿灯、部署或真机故事通过结论。

- 已完成最小修复：iOS 仅镜像 handoff.tree.prefs 到 UserDefaults，documentStart 在新源恢复，当前主框限定写入；CookieBridge.load 前安装。sceneDidBecomeActive 通知现存网页恢复轮询。Web 恢复时取消旧请求、丢弃旧结果，项目树与任务 HTTP 透传 signal，401 不因恢复再次请求。relay 共享连接不再绑定握手 ctx。
- 原生新缝先编译红 cannot find WebDisplayPreferences，再空实现运行断言红（设备保存值与第二端口恢复值均 nil）；实现后 WebKit 跨端口 roundtrip 通过。
- 同源重载新反例：保存 newer 后再次导航实际恢复成 initial，XCTest 断言红；现已改为仅在源内缺键时恢复备份（等待最终全量结果）。独立只读审查也指出此问题，已核销源码，待最终绿灯核销行为。
- `go build ./...`、`go test ./...` 完整结果 exit 0；`go test -race ./internal/relay ./internal/mobilecore` 两包通过；`cd mobile && go test ./bind` 通过。
- Web 最终 `npm test`：144 files / 1855 tests 全绿（/private/tmp/handoff-web-full-final.log）；`npm run typecheck -- --force` 与生产 build 成功。构建保留已有的大 chunk 提醒。
- `PATH=... ./mobile/build.sh ios` 成功，产物来自当前工作树（起初用既有框架检查 Swift 红绿，正式构建前已移除本任务创建的软链接后重编）。
- iOS 全量初跑80条：79通过，1失败。失败原文为空核期望0却得到3、MachineAt(0)为已有linux-01；现复用模拟器带旧配对，非本次偏好/连接回归断言失败。未清空用户模拟器；创建全新任务模拟器 F74258BA-6D7A-45A3-99EC-5D2D9770E18E 重跑。
- `codegraph check --base bf1976f1`：fails=[]，既有warns保留；`git diff --check` 与触及Go文件gofmt通过。usePoll与ensureTunnel已查sym/who-calls；图中usePoll消费者边尚缺，不能把图的空调用边当无影响证据，实际消费者以源码与全量Web测试核查。
- 初次iphoneos build实际未签名（工程CODE_SIGNING_ALLOWED=NO），签名verify拒绝；已再用显式YES进行签名构建，不把首次build成功称可安装。

- 干净模拟器完整 iOS 测试：80/80 通过；包含实际 WebKit 跨端口持久化与 same-origin reload newer 两条。xcresult：/private/tmp/handoff-ios-0e1b/Logs/Test/Test-HandoffMobile-2026.10.11_20-30-55-+0800.xcresult，日志 /private/tmp/handoff-ios-full-clean.log。
- 显式允许签名后的 iphoneos build 成功（/private/tmp/handoff-ios-device-signed.log）；codesign --verify --deep --strict 实际成功。安装候选：/private/tmp/handoff-ios-0e1b-device/Build/Products/Debug-iphoneos/HandoffMobile.app。当前手机仍是原43203；未安装本候选，也未部署服务端网页。

## 独立只读 review

审查者 ios_bug_review：目标轴阶段通过，真实 iPhone 全故事尚缺证据；架构/生产路径通过，无未解决Critical/Important代码finding。

| 维度 | 裁决与证据 |
|---|---|
| 故事 | 组件证明勾选立即影响工作台；真实WebKit证明跨端口与同源重载；手机强退/锁屏完整故事未验 |
| scope/形态 | 无未经授权扩展；本轮没有页面形态改动 |
| 生产装配 | AppComposition唯一CookieBridge，load前主线程安装偏好适配；SceneDelegate经现存WebView通知usePoll |
| 权威与契约 | 仍是本设备显示偏好，不进入服务/配对协议；I3清罐与注入调用序、I7单例不变 |
| 日志 | 偏好只记长度/结果；前台通知有成功/失败日志；relay沿用既有日志 |
| 序列化 | JS→WK消息→UserDefaults→另一端口WebView真实roundtrip通过；异源/子框门禁源码已核，无独立负向行为测试 |
| Important finding | install时旧备份覆盖same-origin新值；先XCTest断言红，再缺键恢复修复，最终80/80核销 |

审查者曾因使用量限制中断，恢复后完成裁决；不把中断状态视为通过。其Go全仓绿依据本协调者输出，Web与iOS原始日志独立读取。

## 最终手机验收待办

1. 安装候选与部署Web修复后，在原机器勾选/取消项目：回工作台即时变化；强退重新启动仍保留。
2. 保持同一工作台锁屏/退后台，恢复时自动重拉，不关闭重启App；原有打开内容保留。
3. 真实会话失效仍落过期终止态，不无限重试。

当前仅代码/构建/模拟器阶段通过，不宣称手机问题已经验收，也不进入合并与归档。

- 承重变异复验：唯一命中 usePoll 在 await 后的 epoch 守卫，仅保留 stopped（可编译）；真实恢复/晚到旧结果测试断言红 `expected stale to be fresh`；恢复源码后15条usePoll测试全绿。原始结果 /private/tmp/handoff-poll-mutation-red.log、/private/tmp/handoff-poll-mutation-restored.log。
- 打包候选 dist/mobile/HandoffMobile-ios-bugfix.ipa，包版本沿用源内1.0(1)；该本地调试包不是正式发版，不改变仓库版本号。手机当前43203尚未替换。

## 用户报告复发后的现场取证（20:39截图，20:42起）

- devicectl apps 确认当前安装为1.0(1)，已不是43203；LLDB附加PID17711后主映像UUID为4DDC2B2B-1365-3C99-ACB4-CBA4C05E6209，与上一轮候选匹配。
- SuperDev list_services(handoff)再次为0；本机agentd PID255监听127.0.0.1:7777，运行网页脚本index-BpJTH8w2.js；它监听document上的handoff-foreground，不监听window上的handoff-native-resume。暂未替换正在运行的agentd。
- 已解锁手机现场：现存WKWebView http://127.0.0.1:50323/?tab=projects；实际JS读取hidden=false、disconnected=true，scripts=/assets/index-BpJTH8w2.js。与本机agentd资源相同；当前机器偏好macbook-pro。
- 单独从原网页fetch /api/projects/tree?scope=all（5秒取消上限），7ms返回Load failed：不是仅旧Promise挂住，当前新请求也失败。
- 可证伪假设：1) 挂起使Go回环listener/Serve退出而机器表仍online（原Serve忽略退出错误）；2) WebKit网络进程恢复后自身连接失败，但native URLSession仍能连；3) relay隧道死连接导致代理投递失败（应HTTP502或超时）；4) 前台事件两端不匹配导致未重拉（已证实接线不匹配，但不足解释新fetch立即失败）。先在同一origin无cookie native探测区分1/2，不先重启或改码。
- 同源无cookie native URLSession探测为status=0 NSURLErrorDomain/-1001；调试时线程曾因dyld trace停住，此条有调试干扰，不单独拿它裁根因。正确document前台事件补发后仍disconnected=true。
- App旧PID17711随后已退出，重新打开后PID17863；systemCrashLogs针对HandoffMobile列表为空，不能判定退出原因。新PID网页URL仍50323/。BSD非阻塞connect返回EINPROGRESS(36)，尚未取poll/SO_ERROR终态，不能把它当连接成功/拒绝。
- 事件契约回归先红（期望第二次fetch，实际只有一次），统一原生生产与Web测试/监听为document的handoff-foreground后15/15绿、typecheck成功；尚未安装此版。
- Apple TN2277 Listening Socket指出挂起回收的listener可能恢复后仍不监听，建议后台关闭/前台重开；这是平台支持的假设，不替代该手机现场终态证据。https://developer.apple.com/library/archive/technotes/tn2277/_index.html

## 监听失效根因与最小增量

- 真机PID17863在原页面origin50323：BSD connect返回EINPROGRESS；poll终态revents=16(POLLHUP)，getsockopt SO_ERROR=61(ECONNREFUSED)，check=0。没有可连接的回环listener，既有纯网页重拉恢复不了它。已detach，不留调试暂停。
- Go新回归先见Core.Resume缺席编译红，空实现后见same document cannot reconnect / connection refused断言红；真正同端口重建后绿。覆盖旧监听丢失、origin不变、旧会话cookie继续成功、ticket只签发一次、无cookie仍401。
- 增量在同一工作单元内落实：Core.Resume(ctx,name) → Bind.Resume(machine string) error → ConnectCore.resume(machine)；只恢复当前活动机原有传输/监听，不改权威状态归属、不兑换认证、不解析业务数据。bind与壳的导出面guard同步增加Resume；既有B417门禁负向约束继续保持。document上的handoff-foreground由原生发、usePoll与已部署Web消费。没有新增服务API、独立工作单元或对外协议，contract查入口判据后不造独立Ticket0/冻结提交。
- 生产顺序：后台队列恢复连接 → 主队列核对currentMachine/currentOrigin仍相同 → 现存文档发前台事件；没有load/清罐/重新注入cookie。客户端由DefaultDial重新构造，失效relay连接也被替换；继续按既有代理透传认证。
- Swift首个编译命令与框架重建重叠导致XCFramework missing环境红，不能计为功能红；其后空stub真实编译运行断言待结果。需要在框架生成完成后重新验证。

## 本轮恢复路径验证（21:11起）

- Swift 空实现编译通过后，真实 WKWebView 恢复测试的 completion 断言红；接入后台队列 Core.Resume，再对现存 document 发 handoff-foreground 后绿。测试使用生产主线程调度；曾用同步测试调度导致 recorder 多出 mainAsync，已改为真实主队列夹具，不把该夹具失败判为产品失败。
- iOS 完整测试 83/83 通过（/private/tmp/handoff-native-resume-all.log）；新增恢复时不 reload/不清罐、core 恢复失败不通知网页、恢复期间切机丢弃旧页面通知三条。该轮随后又重建最终 XCFramework，最终框架复跑记录另附。
- Go 恢复失败反例：构造传输失败保留原服务；端口被占用则明确失败，释放后可继续恢复；重复恢复仍能用原 cookie 请求。失败重绑后旧 cleanup 被重复调用的严格断言先红（/private/tmp/handoff-resume-cleanup-red.log），结束旧资源所有权时置空后 race 绿（/private/tmp/handoff-resume-go-race-final.log）。
- 绑定增量同步实际源导出面、生成 Java/ObjC golden 和当前契约入口；生成面校验绿（/private/tmp/handoff-resume-surface-green.log）。精确边界见本目录 2026-10-11-ios-resume-surface.md。独立 review 指出的 Important 文档/绑定清单缺口已补，等待其本轮复核裁决。
- 图覆盖债：Core.Resume 为新增符号，既有图未覆盖，依源码及真实回环消费测试核查；不将空图边判为无消费者。最终 codegraph check --base bf1976f1 无 fail；既有 warn 保留。
- 当前仍未部署本轮手机候选；手机监听失效现场是根因证据，测试绿不能抵替新包锁屏后的真机故事。

- 独立 review 再指出切机中途窗口：enter 已更新 activeOrigin，currentMachine/currentOrigin 尚未更新；增加暂停清罐的真实 WebKit 测试先红两条断言，成功回调再检查 activeOrigin 后核销。没有新增状态。最终框架完整 iOS 84/84 通过（/private/tmp/handoff-resume-native-final.log），review 最终无剩余代码阻塞项，可进入真机验收。
- 最终框架首次复跑的偏好 reload 测试继承上轮 persistent WebKit newer，第一条 initial 断言失败；测试改为独立 nonPersistent WebKit 罐，仍真实同视图 reload、跨端口 UserDefaults roundtrip，84/84包含这两条。没有清模拟器或改生产存储。
- 最终 Go 全仓 go test ./...、go build ./...、mobile/bind 全包、Web 144 files /1855 tests 及 force typecheck 均 exit 0；代理/relay race 两包通过。源码 diff check 与图闸门无 fail。
- 21:19 devicectl 重试后已安装本轮签名候选到该 iPhone（首试设备调试连接中断，用户接线解锁后成功）。安装 bundleID dev.handoff.HandoffMobile，安装目录 1F38F39B-2105-48C4-A1AD-F9B9533A6CBC，databaseSequenceNumber 3004。原数据保留；没有重部署运行中的 agentd。
- 身份修正：Debug 主 executable 是稳定启动 stub，其 UUID 4DDC2B2B 不能单独证明代码版本；实际业务代码位于 HandoffMobile.debug.dylib。旧候选 UUID 212CACDD-EFBF-3CE4-8393-8DE3FF73A99A，新候选 UUID 9A712FCC-B162-3754-82B9-68F631C686CE；新包 SHA256 与代码 SHA256 记 dist/mobile/ios-bugfix-build.json，等待实际运行映像核对。

## 新候选手机基线与真实锁屏验收

- 新候选启动 PID18008，真实 LLDB 映像 HandoffMobile.debug.dylib UUID 为 9A712FCC-B162-3754-82B9-68F631C686CE，与最终产物一致。
- 当前真实页面 origin http://127.0.0.1:64817，hidden=false、disconnected=false；现存页面 fetch /api/projects/tree?scope=all 返回 HTTP200。调试只读结果不含项目数据或认证 cookie。
- 给现存文档设置临时 marker same-document-21-20，随后 detach/quit 调试器。请求用户保持相同工作台、不切机器、不强退，正常锁屏至少2分钟再回来；等待前后同页 marker、同 origin 及请求/断开状态核对。锁屏期间不挂调试器，不用模拟器代替。
- 一个诊断表达式直接返回 Promise 导致 WebKit unsupported type；改为异步回调存入页面临时变量再读，得到上述 HTTP200。此诊断调用错误未当作产品失败。
- 21:26:35 devicectl lockState 实际读取 passcodeRequired=true、unlockedSinceBoot=true；手机处于锁定态，未重新挂调试器。当前等待用户解锁回到 Handoff 的结果；未取得锁屏后同页 HTTP200/marker，最终真机故事尚未通过。临时页面 marker 随文档结束消失，后续验收后应删除。

### 锁屏恢复真机结果（用户解锁后，21:29起）

- 用户按锁屏至少2分钟、不强退重开的请求回复“好了”；21:29:31 lockState passcodeRequired=false。回连调试器前 devicectl 读取 App 仍 PID18008，最终代码映像 UUID 仍 9A712FCC-B162-3754-82B9-68F631C686CE。
- LLDB 首次 Core Device lookup 超时，重选同设备后成功附加；不是 App 请求失败，也没有重启 App。
- 现存 WKWebView URL 仍 http://127.0.0.1:64817/。在现存文档用 WebKit callAsyncJavaScript 发起新项目请求，实际结果：{"origin":"http://127.0.0.1:64817","hidden":false,"disconnected":false,"status":200,"marker":"same-document-21-20"}。
- 与锁屏前完全相同的进程、origin 和文档 marker，且实际 UI 没有断开提示、项目 API 新请求 HTTP200；没有重新 load、没有补发前台事件、没有重新领票。原生前台恢复→已部署网页自主轮询的真实消费已在这次故事核销。
- 清理临时页面 __handoffResumeProbe/__handoffProbeResult 及 native associated object 后 detach/quit，不留调试暂停。
- 裁决：本轮“锁屏返回，无需强退重新打开，原页面自动恢复连接”真机故事通过一次。没有计时测量恢复延迟，也不外推为长时断网/切网络全部场景。项目显示设置的手机点击/强退故事仍没有新增本轮实测；保留其原代码/WebKit回归证据，不把本条锁屏验收抵替设置或所有移动端故事。

## 合并收尾（用户已授权 main 合并与推送）

- 本轮修改保存在 codex/ios-settings-reconnect，main 工作区干净，fetch 后 main 与 origin/main 均 bf1976f1；不清理其他工作树。
- 合并前重新跑 Web 144 files/1855 tests、生产 build、iOS 84 tests、mobile/bind 全包，均通过。Go 全仓首次高并发运行 TestGrokToolTimingPaired 出现0ms计时断言失败；该测试连续3次独立复跑通过，全仓以 -p 2 重新执行，完整最终结果另记。没有修改 Grok 源码或断言。
- CI desktop tidy 前置闸发现既有 graph v0.10.0 校验和未跟随主模块 v0.10.2；只同步 desktop/go.sum，两条旧校验和替换为当前版本，tidy -diff 已为空。Windows 两架构 build 与 amd64 vet 已通过；全仓 tracked Go 文件 gofmt 无输出。
- 图对账新增3节点、3处签名修改及6条实际同语言调用边；Core.Resume 和 cancelPending 查询均命中。系统 devel codegraph 拒绝既有嵌套 cards/B233.28-charter.json，改用仓库钉版 graph v0.10.2，validate issues=null；原文件保持不变。图 check 的 fails=[]、28条 warn 与修复前逐项相同。
- 设置手机点击/强退故事及原生壳/绑定图覆盖残余已落 docs/roadmap.md；不把锁屏结果外推为全部移动端验收。本轮没有前端形态变化，无需修改原型基准。
- 合并前 Go 全量 -count=1 -p 2 最终 exit0（/private/tmp/handoff-finish-pre-go-recheck.log），此前偶发红保留原始日志；此次完整复跑没有任何失败。图对账代码改动与视图核对通过，校验使用仓库钉版工具。
