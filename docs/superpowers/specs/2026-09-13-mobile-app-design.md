# 移动端 App(Webview 薄壳)设计 spec

- 日期:2026-09-13
- 状态:**已批准**(2026-09-13 用户批准形态;同日三轴独立审计,修订已落)
- 级别/档位:L3 重档

## 问题陈述

用户离开电脑时(含外出蜂窝网络)需要随身观察与介入 handoff 任务:审批权限、回答执行器提问、看进度、并能实际使用终端。CONTEXT.md 已定桌面端为「常驻的观察与控制入口」,移动端是同一产品语义的第二个客户端,不引入新事实源。

三个硬约束,全部来自现状或用户拍板(2026-09-13 对话):

1. **外出必达**:agentd 在 NAT 后、无公网监听,且直连是明文 HTTP+Bearer(README 红线禁止公网暴露);现有云 relay 是唯一跨网通道,但浏览器不会说 relay 客户端协议(Noise NNpsk0 + yamux,`internal/relay/frame.go:28` 服务端在仓外)。
2. **形态是 App**:用户明确要求原生 App、内部 webview,后续要长通知等原生能力;web UI 只有一份,响应式覆盖竖屏手机/折叠展开/pad。
3. **终端一等公民**:用户当庭推翻旧评估「终端可看不宜敲」(`docs/superpowers/reviews/2026-08-11-desktop-form-factor-assessment.md:167`),理由是 pad/折叠屏配键盘场景;移动终端输入层升为一期主干。

## 级别与档位

**L3 重档**。改动跨三个子系统的契约面:①新移动端壳子系统(iOS/Android 壳 + Go 移动核);②agentd 认证与配对(新增配对 wire 契约);③web 前端(响应式断点谱系 + 移动终端输入层)。动契约层:新增「配对载体」跨进程契约(QR 载荷语义);relay 客户端协议原样复用不改。各子系统工作量均远超流程固定成本(约 70 分钟),判重档。

## 方案(含弃选与理由)

**主选:原生薄壳 + gomobile 移动核**

- **Go 连接核**:复用 `internal/relay` Dialer 与协调者 HTTP Transport(`internal/client/client.go#NewRelay`,relay E2E/WS 过隧道为存量能力),加一层设备回环反代,经 gomobile 编为 Android AAR / iOS XCFramework。协议零重实现。先例:Tailscale 移动端同构(Go 核 + 薄原生壳)。
- **壳**:Kotlin(Android)/ Swift(iOS)各一,职责仅四件——webview 容器、扫码配对、凭据入安全存储(Keychain/Keystore)、(未来)推送 token 注册。一切协议逻辑在 Go 核。
- **UI 一份**:web/ 现有控制台按断点谱系响应式,webview 内运行;桌面/移动永不漂移(同 ADR 0009「一份产物」论据)。
- **触达与连接形态**:状态层常连协调机一台——各机任务事件经账本镜像已汇总(存量 mirror 机制,`internal/ledgermirror/mirror.go:1`),会话列表/需要你/卡动态一台即全;动作层(开某机的终端、看它的任务现场、读它的文件)按需经 relay 连到那台。审计修正:agentd→agentd 的 REST `?machine=` 转发与 WS/PTY 跨机反代本为存量(`internal/agentd/forward.go:1`、`forward_ws.go:1`,桌面控制台在用的形态),初稿「无转发面」失实;但那是控制台经协调机取数的形态——手机若以协调机为唯一枢纽,协调机(笔记本)合盖即全部远程机失联,尽管它们自身存活;按需逐机连则协调机合盖不影响手机直连执行机。配对一次扫码配全部(bundle),不逐台登记。

**弃选**:

- **云端 Web 网关(TLS 终结 + relay 反代)**:明文落 VPS 网关进程,且与「App」形态决定直接冲突。若未来要「免安装任意浏览器触达」再单独立项。
- **纯浏览器 JS/WASM 移植 relay 协议**:反模式——下发 crypto 代码者即信任锚,浏览器内密码学防不了发代码的人,零信任收益且烧一份协议实现。
- **Flutter / RN 单壳**:对 Go 核只能 FFI,引入新工具链,收益仅是少写一个很薄的壳;Kotlin/Swift 各自重实现 relay 协议的方案直接否。
- **TLS 终结揉进 relay 服务端**:网关须持有节点 token 才能拨号,一旦与 relay 服务端同进程,relay 「只见密文」的零知识属性(由密钥位置保证,非运维承诺)整体死亡;且服务端在仓外不归本仓改。
- **relay 端登录/发钥(手机先找 relay 领 token)**:relay 无账号体系,它谁也不认识;若它能发钥匙,就成为「能授权访问所有机器」的信任锚,零知识属性同样死亡。钥匙只在用户自有设备间经 QR 带外拷贝,relay 对拷贝无感(2026-09-13 用户提问澄清)。
- **枢纽机转发(手机只连协调机、由它代理到其他执行机)**:省逐机拨号,且跨机 REST/WS 转发面为存量(`internal/agentd/forward.go`、`forward_ws.go`;审计修正:初稿「要新造代理面」失实);但枢纽把手机的一切远程可达性抵押给协调机在线——笔记本合盖即全部远程机失联,尽管它们自身存活。以「bundle 一次配对 + 按需逐机连」取得同等体验,不引入枢纽单点(2026-09-13 用户提议后裁决;同日独立审计修正论据,结论不变)。

## 用户故事

1. 用户在 CLI 执行配对命令生成配对载体(QR),手机 App 扫码完成与一台执行机的配对(relay 或直连形态),凭据落设备安全存储。
2. 用户外出打开 App,首屏是**会话列表**(B358「会话即工作单元」的移动端镜像):「需要你」的会话染底可筛选,行内含未读数、成员、群主。
3. 用户点进需要你的会话,从群聊顶部「本会话的卡」进入任务现场,完成审批/回答/裁决;裁决前可在现场终端里查证(特殊键条、IME 组合输入);停止二次确认(CONTEXT.md「任务现场」语义平移)。
4. 用户在「卡」tab 查看工作项账本(桌面 dock ▤ 工作项看板 v2 的移动端镜像):「需要你」(裁决⚖ + 答复⛔)浮顶,按状态词表筛选(待办/进行中/待审阅/已完成/终止,`internal/ledger/types.go:15`);进卡详情逐条对照证据验收(已验/待真机验)、评论 #引用成链、验收通过走 card move 过工作流 gate(`cmd/card_records.go:6` 边界);「＋ 记一张」随手记需求(POST `/api/cards` 存量先例);卡详情与驾驶会话双跳互通。
5. 用户经「项目」tab 按项目查看其在各机器上的位置(CONTEXT.md「项目位置」语义),浏览目录与文件(只读查看器),并在任意在线位置的工作树里打开终端;离线位置不降级只读、标记状态等重连(CONTEXT.md「项目位置不可用」原文适用)。
6. 用户在 pad / 折叠屏展开态使用任务现场:对话与终端分栏,终端可实际输入,接外接键盘时键条收起。
7. App 与 agentd 同局域网时走直连 target,不绕 relay。
8. 手机丢失,用户在 CLI 吊销该设备的浏览器会话;需要更狠时双端轮换该节点 token 并重启(轮换操作文档随本期产出)。
9. 用户配对多台执行机,在设置页管理配对清单(机器不是一等公民,无独立浏览入口——只出现在设置与项目位置里)。

## 契约语义与接缝(定语义,不定签名)

- **配对契约**:CLI 产出配对载体,语义 = **一份 bundle**:relay 端点 + 管道 credential + 各已知执行机的登记 + **每台 agentd 各一张一次性 ticket**(CLI 经各自通路代领,复用现有 ticket→cookie 机制,`cmd/console.go:1` 先例;ticket 一次性原子消费,`internal/agentd/store/auth.go:96`)。每机登记语义 = **0..1 relay 形态 + 0..1 直连形态、按可达性选择**(relay 形态含 node/token)——审计修正:双形态超出 `config.Target` 的 relay/addr 互斥语义(`internal/config/config.go:234`,初稿误引 :48),是 bundle 自有 wire 语义、不改 Target 本身,story 7 的同机两态切换由此成立。App 凭一次扫码完成:全部机器的隧道凭据登记(token 副本入 Keychain/Keystore)+ 各机 HTTP 会话建立;单机配对是 bundle 的退化形态。信封语义(审计补):**版本字段 + 未知版本/畸形载荷拒收**,作跨语言(Go 编码 / Kotlin·Swift 解码)演进锚;容量预算每机约 200B,N 机上界内单码容纳,超界退路 = 粘贴串/分段码(contract 定界)。ticket 寿命 60s(`internal/agentd/auth.go:32`),N 张同时起算,超时处置 = 重新生成;App 持 token 副本可程序化自助续领 ticket(凭据模型条),TTL 只约束首配窗口。有机器离线时 = **部分 bundle**(离线机标记、上线后补配),不整单失败;重复扫码幂等:重配即重领 ticket 并覆盖凭据,旧设备会话留存至显式吊销。已设计未实现的 `console --qr`(`docs/superpowers/plans/2026-08-11-agentd-browser-auth.md:56`)是本契约的扩展点,QR 载荷从「ticket URL」扩为「配对 bundle」。**relay 不参与配对、也不签发任何凭据**——凭据只在用户自有设备间带外拷贝,这是零知识属性的前提。
- **凭据模型**:节点 token 即该机 agentd 的主 Bearer 令牌(`internal/config/config.go:231`;`internal/client/client.go:199` NewRelay 注释「token remains the agentd Bearer credential」)——App 持 token 副本 = 持该机全量 API 权限,relay 使攻击面全网可达(审计修正:初稿「泄露残余风险够不到 API」与代码事实相反)。据此:bundle QR 保护等级**等同主令牌集**——展示卫生:不落日志、即扫即弃;吊销走存量 `handoff sessions revoke`(`cmd/sessions.go:46`)只杀 cookie 会话、救不了 token 泄露,token 泄露唯一处置 = 双端轮换节点 token 并重启(轮换操作文档随本期 contract/文档段产出,现状无文档)。**每设备独立隧道凭据**属 relay 协议增量,列后续硬化,不进一期。
- **产品边界平移**:App 是 agentd 的观察与控制客户端,语义与桌面端完全一致;任务调度、工作树加锁、并发裁决仍归 agentd/执行侧,移动端不拥有(CONTEXT.md 产品边界原文适用)。**手机不是第二台协调机**:App 内只带 gomobile 编译的 Go 客户端连接核(拨隧道 + 回环反代 + 存凭据),不含 agentd——不拉起任务、不托管 PTY、不供页面、不落账本;不持账本/配置/派发权,每个动作都是 API 调用、由 agentd 落账。定位 = 桌面端控制台的孪生兄弟(同构:壳 + Go 核 + webview)。「双协调机对等」是 roadmap 推迟项(B156.1),本方案不复活。
- **允许新增依赖方向**:新建 `mobile/` 仓内模块 → 根模块 `internal/`,先例 = `desktop/`(独立 go.mod + replace 根模块,noise/yamux 已在依赖闭包,`desktop/go.mod` 现状)。禁止反向依赖。
- **连接核收口**:relay 拨号 + 回环反代 + 会话存取的共享逻辑收口在根模块一包,需求锚 = **gomobile 绑定面最小**(审计改写:desktop 今日无 relay 拨号需求、全目录无 relay 直呼属实,「desktop 消费方」降为可选,contract 定夺「抽核/各自持有」),此为 contract 阶段必核接缝。
- **测试接缝(草单,contract/plan 执法前复核)**:
  1. 配对载体生成与解析入口(新建符号;调用方:CLI 配对命令、App 配对流程;导出面归 contract 核验)。
  2. 回环反代 → relay Transport 适配(存量符号 `internal/client/client.go#NewRelay` 一带,contract 对本轮工作树复核)。
  3. 移动终端输入层组件边界(web 侧新建;调用方:任务现场终端面板)。

## 实现决定

- **移动端 IA 镜像 B358/B361**(2026-09-13 用户裁决):首屏 = 会话列表(会话即工作单元);底栏四个一级 tab:会话 | 卡 | 项目 | 设置——「卡」同日二次拍板升一级,镜像桌面 dock ▤ 工作项账本(账本工作项粒度:需求/验收/证据/评论),非 B358 前任务面板语言的复活;「项目」tab 裁决不变——机器不是一等公民,项目才是,从项目进入位置/目录/文件/终端。CONTEXT.md 的「全局任务面板/任务卡片」语言与 B358 存在文档漂移,移动端正文以 B358 形态为准,CONTEXT.md 对齐归 finish 文档对齐。
- 新建 `mobile/` 仓内模块,布局先例 = `desktop/`(独立 go.mod + replace 根模块);壳内无协议代码。
- **webview 固定加载回环源**(`http://127.0.0.1:<端口>/`,Go 核监听):页面资产不打包进壳——控制台前端编译进 agentd 二进制(发布构建 `-tags embedweb`,`internal/agentd/server.go:774` 现状),relay 不供任何页面。relay host 无默认值,由配对 bundle 从协调机配置带入,手机不发现不输入。**回环门禁语义**(审计补):回环监听为设备全局、同机任意 App 可连,故**反代不得注入凭据**——选定透传模式:Go 核用 token 副本程序化兑换 ticket→cookie(`/console` 兑换是 302 导航流,`internal/agentd/authroutes.go:164`,App 内无浏览器导航,由 Go 核程序化完成)并桥接入 webview cookie jar;无 cookie 的同机其他 App 过不了闸。**多机会话域语义**(审计补):cookie 不按端口隔离(RFC 6265),同 origin 多端口不隔离会话——选定「每机独立回环端口(Go 核按端口选路反代)+ 切机即清 webview 会话罐并由 Go 核程序化重兑换」,任何时刻一罐只装一机的 cookie;重兑换走自助续领 ticket,用户无感。统一回环源使 relay/LAN 两形态对页面与 cookie 会话同构。
- web/ 响应式按断点谱系:竖屏手机 / 折叠展开 / pad;终端输入层含特殊键条与 IME 组合,沿用 08-11 评审积累的输入法补丁认知(`docs/superpowers/reviews/2026-08-11-desktop-form-factor-assessment.md:102`)。
- **离线与不可达语义**(审计补):协调机整体不可达时,状态层(会话列表/卡列表)整体标记「数据可能陈旧 · 连接中」,不伪造在场、不降级为可写;执行机个体不可达沿用 CONTEXT.md「项目位置不可用」语义(只标记状态等重连,不展示缓存快照);动作层对已配对执行机的直连不受协调机状态影响。
- 页面形态确认载体:按项目规矩走 `prototyping-in-brainstorm` 分支副本。fork `prototypes/mobile-app/` 十一屏可点通流程(会话 mobile-home → mobile-session → mobile-task;卡 mobile-cards → mobile-card;项目 mobile-projects → mobile-project → mobile-files/mobile-file/mobile-terminal;设置·配对 mobile-pairing;方案沟通页 mobile-architecture),spec 批准前完成形态走查。
- 真机验证形态:sideload / TestFlight 内测即可,商店上架与签名 CI 不在本 spec。

## 测试决定

最高缝 = 配对契约端到端:CLI 产出载体 →(模拟)App 解析 → 经 fake-relay 与 agentd 建立 E2E 隧道 → ticket→cookie 会话 → `/api` 可达,一支缝级测试打穿(先例:`internal/relay/dialer_test.go:29` TestDialerHTTPRoundTripThroughFakeRelay,审计修正引用)。**bundle 序列化边界 roundtrip 属性测试**(审计补):编码↘解码恒等 + 反面断言(畸形载荷、未知版本信封拒收)——跨语言(Go 编码 / Kotlin·Swift 解码)边界的法定锁。**工具链 gate**(审计补,contract 启动前出结果):gomobile × go 1.26.1 编通 `internal/relay` + `internal/client` 传递闭包出 AAR/XCFramework 的 spike——方案脊柱级风险,不通过则回炉方案选型。壳层(Kotlin/Swift 极薄)以真机走查为主;web 响应式与终端输入层在 web/ 既有测试体系内收口。集成全量归 integrate 节点。

## Out of Scope

- **推送通知(APNs/FCM)**:后续要做;一期架构不挡(壳预留 push token 注册位,配对载体预留通道语义),一期内用户主动打开 App 才看到等待介入。落 `docs/roadmap.md`。
- **每设备独立隧道凭据**:硬化项,落 `docs/roadmap.md`。
- **云端 Web 网关 / 免安装浏览器外出触达**:本期不做;除非产品方向反转,届时需改本 spec 与 ADR 0009 语义。
- **编辑器移动端适配**:沿用旧评估结论(不适合),不做。
- **桌面端行为变更**:零改动目标。
- **应用商店上架、签名 CI、推送服务端**:实现/运维细节,后续单独立项。

## 备注

- 现状读数(出处即上文行内引用):relay 客户端栈 `internal/relay/`、`internal/client/client.go:208`;console ticket 流 `cmd/console.go`;会话吊销 `cmd/sessions.go`;relay 服务端在仓外(`internal/relay/frame.go:28`);README 公网暴露红线 `README.zh-CN.md:140`。
- 决策留痕:2026-09-13 用户三连拍板——外出必达、终端一等公民、App+webview 形态;网关路线(本轮曾推荐)因形态决定撤回。同日第四轮裁决:IA 以 B358 会话一等公民为锚(首屏=会话列表)、项目优先于机器(项目 tab 进位置/目录/文件/终端)——初版原型「任务面板」因 IA 陈旧且三屏断裂被用户打回重做。同日第六轮裁决:「卡」升为与项目/会话/设置同级的第四个一级 tab——卡是 charter 流的工作项(账本面),会话是执行面,平级双跳。同日第七轮:三轴独立审计(事实核验/纪律架构/原型一致)后修订——凭据威胁模型按代码事实重写(token=主 Bearer 令牌)、回环门禁与多机会话域补语义、bundle 信封/TTL/部分 bundle/双形态补全、「无 agentd↔agentd 转发面」失实修正(结论不变)、gomobile spike 列工具链 gate、roundtrip 测试入列;全文「审计修正/审计补」字样即该轮留痕,详见台账第七轮。
- 台账:`docs/superpowers/ledgers/2026-09-13-mobile-spec-ledger.md`。
- 图覆盖债:无(codegraph sym 命中 NewRelay/NewListener;desktop 连接核现状待 contract 复核,已列入契约语义段)。
- 08-11 形态评估「任务看板/agent 总览最适手机」的结论已被 B358 的会话一等公民取代为 IA 锚——移动端不再有独立任务看板,等待态挂在会话上。「卡」tab 是账本工作项面(桌面工作项看板 v2 的镜像,`pages/board.html`),不是该任务看板的复活——任务粒度的等待态仍挂在会话上。
