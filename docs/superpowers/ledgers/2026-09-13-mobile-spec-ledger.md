# 移动端 spec 台账(2026-09-13)

## 对话裁决留痕

- 用户发起:「我想做移动端了,你有什么方案吗?」→ 进入 spec 节点。
- 第一轮盘点(事实):ADR 0009 已定 agentd 托管 Web UI、移动端响应式留待后续;ticket→cookie 已为手机设计、`console --qr` 设计未实现;形态评估排序看板/总览最佳、终端「可看不宜敲」;workbench 状态存服务端为移动端预埋;relay 已完整落地(ledger-relay.md,Noise E2E + yamux + raw 预览管道)。
- 用户拍板一:外出也能用是硬需求。→ 触达范围定为「任意网络」。
- 用户拍板二:终端能用非常重要。→ 推翻 08-11 评估「终端可看不宜敲」,终端输入层升为一期主干。
- 用户拍板三:形态响应式(手机/pad/折叠屏谱系)。协调者据此初推「自建 Web 网关」路线。
- 用户质疑:「为什么不直接用 relay?」→ 答:浏览器说不出 Noise/yamux;网关本质是 relay 的浏览器适配头,且 relay 服务端在仓外、揉入 TLS 终结会摧毁 relay 零知识属性(密钥位置决定的硬属性)。
- 用户拍板四(方向修正):「我就是需要 APP,内部是 webview,以后可能还需要加通知之类的。」→ 网关路线撤回;主选改为原生薄壳 + gomobile 移动核(E2E 直达设备);推送列 OOS 但架构不挡。

## 关键读数(出处)

- relay 服务端不在本仓:`internal/relay/frame.go:28`(控制错误码与服务端 wire 契约共享)。
- 协调者 relay 拨号 + HTTP Transport 存量:`internal/client/client.go:208` NewRelay(codegraph sym 命中,anchor: moved);`docs/ledger-relay.md` Task 4。
- ticket→cookie:`cmd/console.go:1`;会话吊销:`cmd/sessions.go:28`;`--qr` 设计未实现:`docs/superpowers/plans/2026-08-11-agentd-browser-auth.md:56`。
- 公网暴露红线(明文 HTTP+Bearer 禁公网):`README.zh-CN.md:140`。
- desktop 模块先例:独立 go.mod + 根模块依赖,noise/yamux 在依赖闭包(`desktop/go.mod`);desktop/main.go 未见 relay 直呼——连接核是否已抽出,待 contract 复核。
- codegraph sym:NewRelay 命中(anchor moved,行号以查询时决议为准),NewListener 命中(anchor ok)。图覆盖债:无。

## 放弃的尝试

- 云端 Web 网关(曾作推荐上桌):因用户形态决定(App)撤回;文档化为弃选项,留「免安装浏览器触达」的未来立项口。
- 纯浏览器 JS/WASM 移植 relay 协议:反模式,未上桌即否(留痕备查)。
- Flutter/RN 单壳:FFI 接 Go 核代价大于薄壳收益,弃。

## 2026-09-13 第二轮:原型打回重做(用户四条反馈)

- **relay 怎么连接?** → 一图流补「②relay 到底是怎么连上的」六步表:agentd 出站注册(register credential 长连接、断线重连)→ 手机 Go 核出站 Dial(同一 credential + 节点名)→ relay 配对两头开流 → 流内 Noise 握手(PSK 由 token+账号+节点 HKDF 导出,relay 无 token 故零知识)→ yamux 复用(HTTP/WS/PTY 各占一条流)→ 此后普通 HTTP/WS。读数出处:`internal/relay/listener.go` 注释、`docs/ledger-relay.md` Task 3/6。
- **B358 之后会话是一等公民** → IA 锚从 CONTEXT.md「全局任务面板」换到 B358/B361 会话列表为家;初版 mobile-board(任务面板)废弃删除。
- **机器不是一等公民,项目才是** → 底栏改「会话 | 项目 | 设置」;项目 tab:项目列表 → 项目详情(位置切换/工作树/打开终端/浏览文件)→ 文件查看/项目终端;机器只在设置(配对管理)与项目位置里出现。
- **原型断裂不可走通** → 重做为九屏可点通流程,屏间全部相对链接直跳:会话 mobile-home → mobile-session(群聊/详情/本会话的卡)→ mobile-task(裁决横幅可点、停止二次确认、Ctrl 粘滞可点);项目 mobile-projects → mobile-project → mobile-files → mobile-file / mobile-terminal;设置·配对 mobile-pairing(列表→扫码→配对中→完成可点流转)。
- spec 同步修订:用户故事 2–8 重写(会话为家 + 项目中心);实现决定新增 IA 镜像 B358/B361 一条;备注记 CONTEXT.md 文档漂移(对齐归 finish)与 08-11 评估 IA 锚被 B358 取代。
- 原型载体:fork `prototypes/mobile-app/`(gitignore 临时工作区);确认后按 prototyping-in-brainstorm ③ 往 `prototypes/base/README.md` 落「确认中」行。

## 2026-09-13 第三轮:relay 连接与「连一台够不够」澄清

- 用户仍不解 relay 连接,并提出两个模型:①手机应先去 relay 取密钥/token(登录模型)?②relay 是否也走配对?③连一台机器即可,执行机之间互联。
- 裁决:①②为误区——relay 无账号体系,不能也不会发任何钥匙;若 relay 能发钥则成为「可授权访问所有机器」的信任锚,零知识属性死亡。凭据只在用户自有设备间经 QR 带外拷贝,relay 无感。一图流第②节重写:前置「不是这个模型」纠正框 + 「钥匙都住在哪」四钥匙分布表(管道 credential / node token / 一次性 ticket / 登录 cookie × relay/agentd/CLI/手机 谁持有),六步连接表保留并在第 2 步加注「relay 只是传话,不是审批」。
- ③半对:执行机间非网状互联,是各机任务事件镜像汇总到协调机账本(存量 mirror);状态层连协调机一台即全(常连),动作层无 agentd↔agentd 转发面(roadmap「远端载体 TUI 转发尚未接线」佐证),需按需逐机经 relay 连。用户真正要的「连一台」体验由「bundle 一次扫码配全部 + 按需逐机连」满足;枢纽机转发方案被裁(新造代理面 + 协调机合盖即远程全失联),记为弃选。
- spec 同步:触达条改「状态层常连协调机 + 动作层按需逐机」;配对契约改 bundle 语义(单机为退化形态,relay 不参与配对);弃选新增「relay 端登录/发钥」与「枢纽机转发」两条。

## 2026-09-13 第四轮:页面来源与 relay host 出处

- 用户问:webview 页面的 URL 是 relay 的还是编译进壳的?relay host 去哪找?
- 答(已核实):webview 固定加载手机回环源(http://127.0.0.1:<端口>/,Go 核回环反代);页面资产既非 relay 供给(relay 只搬字节)也非打包进壳,而是 agentd 供给——前端编译进 agentd 二进制(-tags embedweb,internal/agentd/server.go:774,webui.Embedded(),stub 兜底有 Warn 日志)。统一回环源使 relay/LAN 对页面与 cookie 同构。
- relay host 无默认值(项目不运营公共 relay):由配对 bundle 从协调机 handoff 配置带入手机,手机不发现不输入;未配 relay 则只剩 LAN 直连形态。
- 一图流第①节下补两个 Q&A 块(webview 地址栏 / relay host 出处);spec 实现决定补「webview 固定加载回环源」一条(含 embedweb 现状出处)。

## 2026-09-13 第五轮:「App 里带 Go?带 agentd?算另一台协调机?」

- 用户心智模型试探:App 里带着一个 Go?或者说一个 agentd?移动端相当于另一种形式的协调机?
- 裁决三段:①带 Go 对——gomobile 把根模块几个客户端包(relay 拨号 / HTTP Transport / 回环反代 / 会话存取)编成一个库塞进壳,不是通用 Go 环境;②是 agentd 错——agentd 的职责(拉起任务/托管 PTY/供页面/落账本)手机一样没有,App 只带客户端那一小半;③算协调机半对——站在协调者一侧但不是「机」,不持账本/配置/派发权,每个动作都是 API 调用、由 agentd 落账;正确定位 = 桌面端控制台的孪生兄弟(同构:壳 + Go 核 + webview),「双协调机对等」为 roadmap 推迟项 B156.1,本方案不复活。
- 一图流第②节后补第三个 Q&A 块;spec 产品边界平移条追加非协调机澄清(不持账本/不供页面/B156.1 不复活)。

## 2026-09-13 第六轮:「卡」升第四个一级 tab(用户拍板)

- 用户拍板:「工作项,也就是卡,也应该和项目、会话、设置同级。」→ 底栏改四个一级 tab:会话 | 卡 | 项目 | 设置。
- 定性:卡 tab 镜像桌面 dock ▤「工作项账本」(fork 内 pages/board.html = 工作项看板 v2:生命周期列、「需要你」= 裁决⚖+答复⛔ 合一、知识流主视觉、保真信号沉默);不是初稿被砍的 mobile-board 复活——那是任务粒度的孤岛(B358 前语言),卡是账本工作项粒度(需求/验收/证据/评论),会话仍是执行面,两者平级双跳。
- 原型增量两屏:mobile-cards(「需要你」浮顶 + 状态词表筛选 chip + 卡流 + ＋记一张)、mobile-card(验收区开关/关系区/双跳条:当前节点→任务现场、驾驶会话→群聊/timeline/评论 #引用/验收通过二次确认);底栏三屏(home/projects/pairing)与全部 mnav 同步加 tab;home 注 ② 改写;一图流头注记四 tab 布局。
- 读数出处:状态词表 待办/进行中/待审阅/已完成/终止 `internal/ledger/types.go:15-19`;卡 wire DTO `internal/proto/ledger.go:15`;GET/POST `/api/cards` 先例 `internal/agentd/ledgerapi_test.go:246`;状态流转边界(card move 过 gate)`cmd/card_records.go:6`。
- spec 同步:用户故事新增第 4 条(卡 tab)并重编号至 9 条;IA 镜像条记四 tab;形态载体条九屏→十一屏;备注决策留痕补第六轮、任务看板条补「非复活」一句。

## 2026-09-13 第七轮:三轴独立审计与修订(用户批准后)

- 用户批准形态(「没什么问题了」)并点名派子 agent 按纪律审核。三轴并行(explore 子 agent,只读):事实核验轴 / 纪律架构轴(先读 spec·defect-families·architecture-law·review 四 skill 正文再审)/ 原型一致轴。
- **事实核验轴**:27 条引用,22 属实、3 漂移、2 失实。失实①(承重):「现状无 agentd↔agentd 转发面」——`internal/agentd/forward.go:1`(REST ?machine= 转发基座)与 `forward_ws.go:1`(WS/PTY 跨机反代,端到端测试在案)均为存量;roadmap:765 只支撑「远端载体 TUI 转发未接线」窄条。该失实作过弃选论据,但枢纽机弃选第二理由(协调机合盖单点)独立成立,**结论不变、论据修正**。失实②(轻):「按文档轮换节点 token」无文档承载。漂移三处:sessions.go:28→:46(revoke 符号)、config.go:48→:234(Target)、fake-relay 先例 e2e_test.go→dialer_test.go:29。
- **纪律架构轴**:骨架/定级/弃选论证/架构法对照全过;must-fix 9 条——①spec 状态行回写已批准(skill 实测过的失败模式);②**凭据威胁模型与代码事实相反**(Critical):node token 即主 Bearer 令牌(config.go:231、client.go:199 注释),token+管道 credential 泄露 = 该机 API 全控,sessions revoke 只杀 cookie 会话;③**回环门禁盲区**(Critical):127.0.0.1 设备全局、同机任意 App 可连,反代注入凭据即免认证过闸;④**多机会话域盲区**:cookie 不按端口隔离(RFC 6265),同 origin 切机即 401 死局;⑤双形态登记与 story 7 矛盾(Target relay/addr 互斥,config.go:249);⑥bundle 信封版本语义与容量姿态未定;⑦ticket TTL 60s(auth.go:32)× N 机窗口与部分 bundle 语义未定;⑧gomobile × go 1.26.1 spike 未列验证项;⑨roundtrip 属性测试未列。
- **原型一致轴**:9/9 用户故事有屏承接、零断链、四 tab IA 全对、mock 诚实六项全过;修订三处:cards 筛选缺「终止」chip(与自述词表及 story 4 双矛盾)、pairing 只呈现单机退化形态缺 bundle 主形态、8 屏引用标与 notes 编号不一一对应(+ home 筛选 chip 无 JS 与注①矛盾)。
- **处置(协调者,全接受无驳回)**,spec 落修订:状态行→已批准;触达条与枢纽机弃选条修正转发面事实;凭据模型条重写(token=主令牌、QR 保护等同主令牌集、展示卫生、唯一处置=双端轮换、文档归 contract/文档段);配对契约条补双形态登记/版本信封+拒收/容量预算与退路/TTL+自助续领/部分 bundle/重复扫码幂等;回环源条补门禁语义(选定透传:Go 核程序化兑换 ticket→cookie 桥接入 webview 罐,反代不注入凭据)与多机会话域语义(每机一端口 + 切机清罐重兑换);连接核条需求锚改写(gomobile 绑定面最小,desktop 消费方降为可选);新增「离线与不可达语义」条;测试决定补 roundtrip 属性测试与 gomobile spike 工具链 gate、先例引用改 dialer_test.go:29;story 8 改「双端轮换…(操作文档随本期产出)」;备注补第七轮留痕。roadmap 段头同步「已批准」。
- **原型落修订**:cards 加「终止」chip 与一张终止卡(B91);pairing 注②改 bundle 主形态、配对中/完成视图改 bundle 文案、理念注 ④ 改 ※ 无编号;home 补筛选 JS 与引用标 ③④;session/task/cards/projects/files/file/terminal 引用标补齐(file 屏补 .ref 样式)。div 平衡与 node --check 全过。
- **base/README.md 落行**:fork `mobile-app/` 十一屏,确认状态「确认中」(2026-09-13 走查拍板 + 审计修订),推进「已确认」待真实页面对照验收。
- 审计方法留痕:三轴分工 = 事实核验(引用逐条对真代码)/ 纪律架构(缺陷族对抗设问 + 架构法)/ 原型一致(故事→屏对账 + 断链 + mock 诚实);Critical 两条(凭据威胁模型、回环门禁)均出自缺陷族「门禁绕过族」设问——若漏过,contract 写凭据条款按错误前提放松保护、implement 随手选注入方案致同机任意 App 免认证过闸。
