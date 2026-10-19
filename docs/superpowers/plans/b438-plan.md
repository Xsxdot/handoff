# B438 plan：传输层——直连优先、NAT 打洞次之、relay 兜底

> 卡 B438（L3，epic）→ B438.1（桌面，首故事，单卡单轮）→ B438.2（移动端）· 节点 charter:plan
> spec `docs/superpowers/specs/b438.md`（r1 已批准）· 台账 `docs/superpowers/plans/b438-plan-ledger.md`（全部亲跑命令与原始输出）
> 起手 HEAD `f5e1d443`，分支 `claude/objective-meitner-b695a6`。本节点只出计划与台账，不写实现。
> 行号是 `f5e1d443` 的现状读数，动手前重核，漂了以符号为准。**未验证** = 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓与 pion 零上下文。

- 状态：**已拍板（2026-10-19）**（出稿者 = 本地 subagent；拍板者 = 协调会话 local_30aaf694）。P1–P5 全部按建议采纳；两处派单事实更正认可。P3 补充：DisableKeepAlives 只作用于 relay Dialer 自己的 Transport，不改 http.DefaultTransport 或其它客户端；测试断言升级后的新请求确实走直连。

---

## 0. 待拍板清单

| # | 岔口 | 本计划的建议 | 不采纳的后果 / 影响面 |
|---|---|---|---|
| **P1** | preview SOCKS 的 `RawDialContext` 本期走不走直连 | **不走，留 relay**。raw 流今天是「每次一条独立 CONNECT + E2E + previewRawMagic」（`dialer.go:62-133`），直连会话上只有 app-yamux 承载 HTTP，要让 raw 走直连得在直连会话里另开一种子流模式——新增一条跨进程 wire，超出 spec 冻结范围 | 采纳「走直连」= 本卡多冻结一种直连子流协议、listener 多一个模式判别点，B438.1 工作量 +1 个单元。预览流量照旧计入 relay 配额（落 roadmap） |
| **P2** | CLI 一次性 Dialer（`targetclient.New`，每条命令一个进程）要不要立刻打洞 | **延迟起跳**：CLI 路径的 Dialer 在 relay 隧道建好后等 **5 s** 才起升级；进程在此之前退出则完全不碰 STUN/ICE/信令。Pool（agentd 常驻）与移动端立即起跳 | 不延迟：每条短命令（`status`/`dispatch`）都会多开一条信令 CONNECT、发 STUN、让对端 listener 白建一个 ICE agent（占 15 s 的待连名额），`handoff wait` 这类循环会把对端 listener 的并发上限刷满。延迟值是可逆参数 |
| **P3** | HTTP keep-alive 让升级后旧连接继续走 relay | **relay Dialer 交出的 `http.Transport` 设 `DisableKeepAlives: true`**——每个 HTTP 请求一条新 app 流（yamux 开流零 RTT：SYN 随首帧），路径选择因此逐请求生效 | 现状 `Transport()` 是 keep-alive 缺省开、`IdleConnTimeout` 0（`dialer.go:387-389`），升级前开的空闲连接**永不过期**，持续负载下 S1「relay 字节不再增长」必红。备选「切路时 `CloseIdleConnections()`」只清空闲连接，切换瞬间在忙的连接回池后照旧走 relay，持续轮询下残留不消 |
| **P4** | B438.1 的改动边界是否含 `mobile/go.mod`、`desktop/go.mod`（只 tidy 依赖，不改代码） | **含**。两个嵌套 module 都 `replace ../` 且经 `internal/client` 传递依赖 `internal/relay`（台账），根仓引入 pion 后不 tidy 就编不过 | 不含 = B438.1 合入后 mobile/desktop 构建红，直到 B438.2 才修，期间桌面壳/移动端无法出包 |
| **P5** | spec 「代理语义」一句与代码事实不符 | **改 spec 与 PRIVACY 措辞，判据与 INFO 文案不变**。事实：config `proxy` 键本来就**不**作用于协调者↔agentd 链路（`config.go:83-99`、`proxycfg` 包注释），relay WSS 走 `websocket.Dial(ctx,url,nil)` → `http.DefaultClient`，只吃 `HTTPS_PROXY` 环境变量（coder/websocket `dial.go:75-76`）。spec 写「relay 的 WSS 照旧经代理」不成立 | 不改：PRIVACY 会对用户承诺一个不存在的行为（「配了 proxy 就全程走代理，只有 UDP 例外」）。建议文案见 §4.6。INFO 判据仍按 spec：`proxy` 非空且 p2p 开 |

其余岔口本计划已按 spec 职权决断（§4 逐条带依据）；拍板推翻 §4 任一条，改动只落在对应工作单元。

**派单事实更正（非岔口，已按实测写进本稿）**：
- vnet 取 `github.com/pion/transport/v5/vnet`，**不是 v3**：ice/v4@v4.4.7 依赖 transport/v5 + stun/v4（`ice/v4@v4.4.7/go.mod`、`agent_options.go:15-16`），v3 的 `transport.Net` 与之类型不兼容。
- handoff-server 账本是 **SQLite**（`internal/ledger/store.go:14,226`，配置键 `db_path`），不是 PG：真机隔离 relay 用临时目录 SQLite，无需在 linux-01 建 PG 库建号。

---

## 1. 触及子系统清单（L3 增段①）

| 子系统 | 类型 | 本卡责任 | 派卡资格四条 |
|---|---|---|---|
| `internal/p2p`（**新**） | **边界型**（对面是真实 NAT/UDP/STUN 运营方） | ICE 代理（pion/ice v4）、候选收集、sctp 可靠流 + 字节流适配、信令消息编解码（含魔数与帧）。不认识 relay/E2E/yamux/HTTP，不读配置 | 有界（新目录）；契约面 = 信令 wire（§6 冻结）+ 包导出面（同单元内部，不冻结）；依赖仅 pion；机内只验契约形状 + vnet 行为，真行为走 §9 真机 |
| `internal/relay` | 逻辑型（编排者；接缝对面是 p2p 与自有 listener/dialer） | Dialer 升级/回落/退避状态机；Listener 第三种会话模式（打洞信令）+ 直连会话服务 + 并发上限；Transport keep-alive（P3） | 有界（dialer.go/listener.go + 新 p2p 编排文件 + 测试）；契约面 = `NewDialer`/`NewListener` 选项（§6 F3 冻结 Dialer 侧）；依赖 relay → p2p 单向 |
| `internal/config` | 逻辑型 | 顶层 `p2p:` 块、缺省 STUN 列表、取值校验、代理绕行提示判据 | 有界（config.go + 新 p2p 配置文件）；契约面 = yaml 键（§6 F2 冻结）+ 访问器 |
| `internal/targetclient` | 逻辑型 | 把配置翻成 relay P2P 选项；Pool 立即起跳、CLI 延迟起跳（P2）；CLI 路径打代理提示 | 有界（targetclient.go、pool.go）；签名变化见 §5.3 |
| `cmd`（agentd 组装点） | 逻辑型 | `cmd/agentd.go:268-273` Listener 带 P2P 选项；启动打代理提示；`cmd/root.go:265` CLI 传 P2P 选项 | 有界（agentd.go、root.go 两处） |
| `internal/mobilecore` | 逻辑型（B438.2） | `DefaultDial` 带 P2P（缺省值来自 config）、前后台/Resume 后重升级 | B438.2 子卡 |
| `mobile/`（gomobile 绑定） | 边界型（对面是 gomobile/平台网络栈，B438.2） | 绑定面**不变**；产物构建、Android 接口枚举取证 | B438.2 子卡；B438.1 只 tidy go.mod（P4） |
| `desktop/` | 不触及代码 | 只 tidy go.mod（P4） | — |
| handoff-server | **不触及**（spec：relay 服务端零改动） | 真机用其构建隔离实例 | — |
| 文档 | — | `PRIVACY.md` §2、`README.md`/`README.zh-CN.md` 配置节、spec 措辞（P5） | — |

**按卡派，不按子系统派**：p2p 只有 relay 一个直接消费者，单独派卡没有可独立验收的用户行为；B438.1 = p2p + relay + config + targetclient + cmd 一个工作单元（spec 首故事选择已定）。p2p 派卡资格四条满足，但「没有真实消费者的基础设施不单独派卡」（plan L3 增段②）。

---

## 2. 承重事实（全部亲读/亲跑，原始输出见台账）

### 2.1 依赖库既成行为（契约，带出处，版本 ice/v4@v4.4.7、sctp@v1.12.0、transport/v5@v5.1.1）

| 事实 | 出处 | 对本卡的约束 |
|---|---|---|
| 构造走 `NewAgentWithOptions(opts...)`，`AgentConfig` 已 Deprecated | `agent.go:201`、`agent_config.go:79` | 只用 option API |
| `WithNet(transport.Net)` 注入网络；未注入用 `stdnet.NewNet()` | `agent_options.go:523`、`agent.go:472` | 唯一测试缝 |
| `WithUrls([]*stun.URI)`，URI 用 `stun/v4.ParseURI` | `agent_options.go:357`、`stun/v4 uri.go:140` | 空切片 = 零 STUN（spike 实测 STUN 包 0） |
| 缺省：Keepalive 2s、Disconnected 5s、Failed 25s、STUNGather 5s、Check 200ms | `agent_config.go:17-49` | 本卡改 Failed=5s、STUNGather=3s（§4.3） |
| 初始 checking 截止 = disconnected+failed，到点置 Failed | `agent.go:741-753, 686-688` | Failed 后 agent 不再发检查 |
| **`AwaitConnect`/`Dial`/`Accept` 不因 Failed 返回**，只认 ctx/loop 关闭/connected | `transport.go:31-41` | p2p 必须 ctx 限时 **且** 监听 `OnConnectionStateChange(Failed)` 早退（spike：Failed@10.1s，Dial 拖到 ctx 12s 才返回） |
| 收集完成 = `OnCandidate(nil)` | `agent_handlers.go:23-25` | 不 trickle：等 nil 再发信令 |
| 缺省 mDNS = QueryOnly；缺省网络类型含 TCP4/6 | `agent.go:441`、`networktype.go:21-27` | 显式 `MulticastDNSModeDisabled` + `[UDP4, UDP6]` |
| `Close()` 不等 goroutine；`GracefulClose()` 等，回调内调需另起 goroutine | `agent.go:1585-1594` | 一切关闭路径用 GracefulClose（泄漏族） |
| `Candidate.Marshal()` 串内含 `ufrag <本端 ufrag>` | `candidate_base.go:655` + spike 输出 | **日志只记 `Type()` 与地址族，绝不记候选串**（S6） |
| `*ice.Conn` 满足 net.Conn；`Conn.Close()` = `agent.Close()` | `transport.go:127-301, 261` | sctp 的 NetConn 直接用它 |
| sctp `ClientContext(ctx, opts...)`（ctx 只管建立）/ `ServerWithOptions`；`WithBlockWrite` 缺省 false | `association.go:454,525`；`association_options.go:69` | 用 BlockWrite=true（否则写侧无背压，内存无界） |
| `OpenStream` 不发包；`AcceptStream` 等对端首个 DATA | `association.go:2804-2827` | 角色须对齐：先写的一方（E2E SecureClient 先写 salt，`e2e.go:81-88`）= OpenStream 一方 = coordinator |
| **`*sctp.Stream` 不是 net.Conn**（无 Local/RemoteAddr）；消息语义：缓冲小于消息返回 `io.ErrShortBuffer` 且消息不出队；单条 > 65536 报 `ErrOutboundPacketTooLarge`；`Stream.Close` 只关写方向 | `stream.go:121-156, 306-316, 466`；`reassembly_queue.go:575,618`；`association.go:84` | **必须字节流适配层**：读侧 64 KiB 消息缓冲按需切给调用方；写侧按 MaxMessageSize 切片；Close 连带 association + ICE agent |
| vnet NAT：`EndpointIndependent / EndpointAddrDependent / EndpointAddrPortDependent`，`RouterConfig{CIDR,StaticIPs,NATType}`，`AddChunkFilter` 可计数/丢包 | `vnet/nat.go:35-64`、`vnet/router.go:47,397` | 竖切夹具：端口受限锥（EI 映射 + APD 过滤 ≈ Linux MASQUERADE）与对称（APD 映射） |
| vnet STUN = `turn/v5.NewServer` 绑 `wanNet.ListenPacket`，无 AuthHandler 即纯 STUN | `ice/v4 connectivity_vnet_test.go:146-180`；`turn/v5 examples/stun-only-server` | turn/v5 已是 ice 的依赖，不新增 module |
| Android：stdnet 用 `wlynxg/anet` 枚举接口 | `transport/v5 stdnet/net.go:14,43,51` | B438.2 不自写枚举，只需模拟器取证 |
| 旧 listener（yamux.Server）读到首字节非 0 → `ErrInvalidVersion` 关会话、不发 GoAway | `yamux@v0.1.2 session.go:561-563` | 发起方读应答遇 EOF = 对端不支持（§6 W8） |

### 2.2 spike 结论（vnet，已验证；原始输出见台账「spike 亲跑」）

| 场景 | 结果 |
|---|---|
| 两端端口受限锥 NAT（EI 映射 + APD 过滤）+ STUN | **连通 600 ms，srflx↔srflx；sctp 60 KB 回显一致；其上 yamux 8 并发流 × 1 MiB 回显 8/8** |
| 两端全锥 | 连通 1 s |
| 一端对称 | Failed@10.1s；`Dial` 到 ctx 12s 才返回 `connecting canceled by caller` |
| 已连后 WAN 全丢包 | Disconnected@+6.2s，Failed@+10.2s（Disconnected 5s + Failed 5s） |
| `stun: []` | 只 host 候选，WAN 上 STUN 包 0 |
| GracefulClose 后 goroutine | 1 → 1（回到基线） |

**未验证**：真 Linux 内核 NAT（§9）、E2E 叠在适配层上（scratch 无法 import internal/relay；E2E 只依赖字节流语义，适配层已证字节流语义——实现首步在仓内补证）、公网 STUN 可达性、移动平台。

### 2.3 仓内承重事实

- **假 relay 只支持每次注册一条 CONNECT**：`listener_test.go:147-202` 的 `registered` 容量 1，第一条 CONNECT 取走 executor session 后不放回，且无字节计数 → 竖切要新夹具（§8.1）。
- **Listener 的会话 goroutine 归「本次 Run」**：`listener.go:98-115` 计入 Run 局部 WaitGroup，退出前 `sessions.Wait()`。直连会话若挂在这里，relay 断线后 `Run` 会一直等直连会话结束才重连——**直连会话必须归 Listener 生命周期（RunWithReconnect 的 ctx），不归单次 Run**。
- **relay Transport keep-alive 永不过期**（P3 依据）：`dialer.go:387-389`。
- **config 三态纪律**：`EnvForward` 注释（config.go 约 106-118）：「nil=内置默认、[]=显式空；不要在 Load 里填默认值，否则 Save 落盘顶死旧 agentd」。`p2p.stun` 同款，且普通 `[]string` + omitempty 会在 Save 时把显式 `[]` 吞成「未配置」→ 用 `*[]string`（§4.5）。
- **严格解析**：`decodeStrict` KnownFields(true)（config.go:681-694），旧版 agentd 读到 `p2p:` 键**启动失败**；已知键清单文案（:692）要加 `p2p{enabled,stun}`。
- **嵌套 module**（P4）：`mobile/go.mod`、`desktop/go.mod` 均 `replace ../`；`go list -deps ./internal/client` 含 `internal/relay`。

### 2.4 基线绿（本节点亲跑，@f5e1d443）

`go build ./...` OK；`internal/{relay,targetclient,config,mobilecore}` ok；`mobile` ok（`TestGomobileSurfaceHasNoSkips` 本机 SKIP：无 gobind）。

---

## 3. 故事映射与行为闭环（L3 增段②）

| 故事 | 实现责任 | 真实消费者 | 集成与回归负责人 |
|---|---|---|---|
| S1 打洞成功升级 | p2p（ICE+sctp+适配）、relay Dialer 升级、Listener 信令模式 + 直连服务、targetclient/cmd 组装 | agentd Pool（扇出/镜像/派发）、CLI `--target` | B438.1；竖切 T-S1 + 真机 R1 |
| S2 对称/UDP 封 → 有界放弃 + 退避 | p2p 早退（Failed 监听 + ctx）、Dialer 退避 | 同上 | B438.1；T-S2 + 真机 R2 |
| S3 中途断链回落 | p2p 断链信号、Dialer 回落与重升级、P3 | 同上 | B438.1；T-S3 + 真机 R3 |
| S4 直连 target 零 p2p | targetclient 直连分支不碰 relay/p2p | 同上 | B438.1；T-S4 |
| S5 混跑 / 任一端关闭 | Listener `disabled` 拒绝码、Dialer 「对端不支持」长退避 + 一次 INFO | 同上 | B438.1；T-S5a-d + 真机 R5 |
| S6 安全不降级 | 直连路径重跑 E2E；信令只在 E2E 内；日志卫生 | 同上 | B438.1；T-S6a-c + 真机 R1 抓包 |
| S8 代理提示 / enabled=false / 空 stun | config 判据与访问器；cmd/targetclient 打点；p2p 空 URL | agentd 启动、CLI | B438.1；T-S8 + 真机 R6 |
| S7 移动端同语义 | mobilecore 组装 + 前后台 | App `SwitchMachine`/回环反代 | B438.2 |

**行为闭环**（触发者 → 权威事实/载体 → 消费者 → 可观察结果 → 实现责任）：

1. relay 隧道建立（`ensureTunnel` 成功）→ 信令 CONNECT 内的 Offer/Answer（§6）→ executor Listener 应答 + 双端 ICE/sctp/E2E/app-yamux → 新 HTTP 请求走直连、relay 计量不增长、INFO `p2p 直连已建立`（node、本端/对端候选类型、地址族）→ p2p + relay Dialer/Listener + targetclient/cmd 组装。
2. 对称 NAT / UDP 封 → ICE Failed 或 12 s 预算到点 → Dialer → 首个请求起一直走 relay、≤15 s INFO `p2p 打洞失败，继续走 relay`（带 reason）、按 30 s 起翻倍封顶 10 min 重试 → p2p 早退 + Dialer 退避。
3. 直连路径丢包 → ICE Disconnected（≈5 s 起不再选它）/ Failed（≈10 s）或 app-yamux 会话关闭 → Dialer → ≤30 s 内新请求经 relay 成功、INFO `p2p 直连已断开，回落 relay`、退避后重升级 → p2p 状态回调 + Dialer。
4. 旧 listener / `p2p.enabled:false` 的 listener → 应答 EOF 或 `reject:"disabled"` → Dialer → 业务走 relay、每个 Dialer 只一次 INFO `p2p 对端不支持打洞，继续走 relay`、长退避（10 min）→ Listener 拒绝码 + Dialer 判定。
5. 不知 token 的对端完成 ICE → 直连流上 E2E 握手失败 → Listener/Dialer → 该次升级作废、双端各 WARN 一条、继续 relay、handler 零调用 → relay 直连服务路径复用 SecureServer。
6. 配置 `proxy` 非空且 `p2p.enabled` 真 → config 判据 → agentd 启动 / CLI 构造 relay Dialer → 进程内一次 INFO（spec S8 文案）→ config + cmd/agentd.go + targetclient.New。

---

## 4. 已按 spec 职权决断的设计（plan 定稿，可逆的内部细节执行者可调，见 §10）

### 4.1 p2p 包职责与形状（同单元内部，不冻结）

- 输入：`Config{STUN []string（已校验）, Net（测试缝，nil=真网络）, Logger *slog.Logger}` + 角色（发起/应答）。超时常量写在 p2p 内（§4.3），不进配置。
- 能力：收集本端描述（ufrag/pwd/候选列表，等 `OnCandidate(nil)`）；给定对端描述后连通，返回**可靠有序字节流 `net.Conn`**（sctp 单流 + 适配层）、路径信息（本端/对端候选类型、地址族）、一个「直连死了」信号（Failed/Closed 或底层读写错误）；Close = 关流 + 关 association + `GracefulClose` agent，幂等。
- 信令 wire 的编解码（魔数、帧、JSON、版本、拒绝码）归 p2p（一份 wire 一个拥有者）；relay 只用导出的魔数做 Peek 判别。
- pion 日志：`LoggerFactory` 全部丢弃（与 `relay/mux.go` 丢弃 yamux 日志同先例——pion Debug/Warn 会打候选串，含 ufrag）。p2p 自己只打带 node 上下文的结构化事件，**由调用方 relay 打用户可见 INFO**（p2p 不知道 node 名，可以接收 Logger 上已带的 attrs）。
- ICE 选项：`WithNet`、`WithUrls`、`WithNetworkTypes([UDP4,UDP6])`、`WithCandidateTypes([Host, ServerReflexive])`（不做 relay 候选=不做 TURN）、`WithMulticastDNSMode(Disabled)`、`WithDisconnectedTimeout(5s)`、`WithFailedTimeout(5s)`、`WithSTUNGatherTimeout(3s)`、`WithLoggerFactory(丢弃)`。

### 4.2 Dialer 状态机（relay 包）

状态（进程内，Dialer 私有）：`relay 会话`（今天的 `session`）+ 可选 `direct`（直连 app-yamux 会话 + p2p 连接 + 可选标志 selectable）+ `attempting` + `nextAttempt time` + `unsupportedLogged bool` + `backoff`。

- **起跳**：`ensureTunnel` 成功之后（含复用现有隧道的快路径），若 p2p 开、未 closed、无 direct、无 attempting、`now ≥ nextAttempt` → 起后台升级 goroutine（计入 Dialer 的 WaitGroup）。CLI 路径（P2）额外要求隧道已存活 ≥ 起跳延迟（实现为 attempt goroutine 开头 `select{延迟, ctx.Done}`）。起跳判定在锁内 O(1)，不阻塞 DialContext。
- **一次升级**（预算 **12 s**，单一 ctx，从起跳到放弃；Dialer ctx 取消即终止）：开一条新 CONNECT + `SecureClient`（复用 `RawDialContext` 前半段，抽成私有 helper）→ p2p 收集 → 写 Offer → 读 Answer → 关信令会话 → p2p 连通（controlling）→ 在直连流上 `SecureClient`（同 token/account/node）→ `yamux.Client(relayYamuxConfig())` → 置 `direct`，INFO `p2p 直连已建立`，退避复位，起监视 goroutine。
- **路由**（`DialContext`）：`direct` 存在且 selectable → `direct.Open()`；Open 失败 → 判直连死亡（见下）并**本次请求落回 relay**；否则走今天的 `ensureTunnel` + relay `session.Open()`。direct 可用时 relay 隧道不健康也不影响新请求。
- **直连死亡**：p2p 死亡信号（Failed/Closed）或 app-yamux `CloseChan()` 关闭 → 关闭 direct（全部资源）、INFO `p2p 直连已断开，回落 relay`、`nextAttempt = now + 30s`（退避从首档起）。ICE `Disconnected` → selectable=false（新流走 relay），回 `Connected` → 恢复 selectable；不记 INFO。
- **失败分类**（reason 进 INFO attrs，枚举）：`relay_connect`、`signal_io`、`bad_answer`、`busy`、`timeout`、`ice_failed`、`e2e_failed`。退避 30 s → 60 s → … 封顶 10 min；`e2e_failed` 记 **WARN**（S6），其余 INFO。
- **对端不支持**（应答 EOF/魔数不符/`disabled`/`unsupported_version`）：`nextAttempt = now + 10 min`；INFO `p2p 对端不支持打洞，继续走 relay` **每个 Dialer 只打一次**，之后 Debug。
- **Close 终态**：closed=true → 取消 Dialer ctx（中止升级/延迟等待）→ 关 direct → `wg.Wait()`；Close 期间被取消的升级**不打失败 INFO**（Debug）。Close 之后无 goroutine、无 ICE agent（T-leak）。注意 `Pool.Close`/`Pool.For` 在 `p.mu` 内调 cleanup：Close 等待上界 = ICE GracefulClose（spike < 1 s），实现须保证升级 goroutine 对 ctx 取消及时响应。
- **Transport**（P3）：`DisableKeepAlives: true`，`Proxy: nil` 不变。

### 4.3 时间参数（spec 判据倒推）

| 参数 | 值 | 依据 |
|---|---|---|
| 单次升级预算 | 12 s | S2 ≤15 s 放弃；双端收集各 ≤3 s + 一个 relay RTT + 检查 ≤6 s；留 3 s 余量 |
| STUN 收集超时 | 3 s（缺省 5 s） | 双端串行收集吃预算 |
| ICE Disconnected / Failed | 5 s / 5 s（缺省 5/25） | S3 ≤30 s；spike 实测断链后 6.2 s / 10.2 s |
| Keepalive | 2 s（缺省） | 维持 NAT 映射 |
| 失败退避 | 30 s 起翻倍、封顶 10 min | spec S2 |
| 对端不支持退避 | 10 min 固定 | spec S5「长退避」 |
| 直连死亡后首次重试 | 30 s | 防抖；spec S3「随后重新打洞」 |
| CLI 起跳延迟 | 5 s（P2） | 短命令不碰网络 |
| listener 读 Offer 超时 | 10 s | 防半开信令会话占名额 |
| listener 待连上限 / 已建上限 | 4 / 16 | spec「具体数值归 plan」：一台 executor 的协调者个位数，每个 Dialer 至多 1 条直连；待连每个占 1 个 agent 的 UDP 套接字至多 12 s |

### 4.4 Listener（relay 包）

- `serveSession` 在 E2E 后三分：`p2p 信令魔数` → 打洞应答；`previewRawMagic` → raw（不变）；其它 → app（不变）。两魔数等长（22 字节），一次 `Peek(22)` 判别（§6 W1 断言等长）。
- 打洞应答：p2p 关 → 回 `reject:"disabled"`；超上限 → `reject:"busy"`；读帧失败/畸形 → `reject:"bad_request"`（能写就写）；版本不认识 → `reject:"unsupported_version"`。接受 → p2p 收集（controlled）→ 写 Answer → 关信令会话 → **在 Listener 生命周期 goroutine 中**（Listener 级 WaitGroup + RunWithReconnect 的 ctx，**不**计入单次 Run 的 `sessions`）连通（预算 12 s）→ 直连流 `SecureServer` → 只走 app 模式（复用今天 app-yamux + `http.Serve(handler)` 那段，抽成私有函数共用，直连上**不再做魔数判别**，§6 W12）。
- E2E 失败 → WARN 一条（不带候选/凭据），关闭全部资源，handler 零调用。
- ctx 取消（agentd 退出）→ 全部直连会话与待连 agent 关闭，Listener 级 WaitGroup 归零后 `RunWithReconnect` 才返回。
- 日志：`p2p 直连已建立`（responder 侧同文案，role=responder）/`p2p 直连已断开`（responder 侧只 INFO「直连会话已关闭」类即可，不用 coordinator 的「回落 relay」文案——listener 没有回落动作）。

### 4.5 配置（config 包）

```yaml
p2p:
  enabled: true      # 省略 = true
  stun: [...]        # 省略/null = 内置缺省列表；[] = 零 STUN（只 host 候选）
```

- 结构：`Config.P2P P2PConfig \`yaml:"p2p,omitempty"\``；`P2PConfig{ Enabled *bool \`yaml:"enabled,omitempty"\`; STUN *[]string \`yaml:"stun,omitempty"\` }`。指针 = 区分「没写」与「显式 false / 显式 []」（`PlatformInvariants *bool` 先例，config.go 约 156-160）。
- **Load 不填缺省值**（EnvForward 教训）；访问器 `P2PEnabled() bool`（nil→true）、`P2PSTUN() []string`（nil→缺省列表副本；非 nil→原样，可为空）。缺省列表 = config 包内唯一定义（`DefaultP2PSTUN()` 返回副本，值 = spec 三条，顺序照抄），p2p/relay 代码零 STUN 字面量（grep 断言）。
- `validate`：每条 `stun:` 前缀 + `host:port` 可拆、端口为数字；拒 `turn:`/`stuns:`/空串。config **不 import pion**（否则 pion 进 desktop 依赖图）——词法校验，p2p 侧再 `stun.ParseURI`。
- 提示判据：`P2PProxyNotice() bool` = `Proxy != "" && P2PEnabled()`；INFO 文案 = spec S8 原文，作为 config 导出常量供两个打点处共用。
- `decodeStrict` 已知键文案加 `p2p{enabled,stun}`。
- **滚动升级约束**（写进 README 配置节）：`p2p:` 键只有新版 agentd 认识，旧版读到即启动失败；缺省开启，不写键即生效；`Save` 不落 `p2p:`（全 nil 时整节 omitempty）。**要关打洞的机器必须先换新版二进制再写键**；混跑期间不要在共享模板/配置分发里预写此键。
- 活配置：Listener 与 Pool 条目在构造时读 p2p 设置；改 `p2p:` 需重启 agentd 生效（Pool 条目判等只比 Target，不因 p2p 变化重建——刻意，避免改配置瞬间全队隧道重建）。

### 4.6 文档

- `PRIVACY.md` §2（Optional cloud relay）后补一段（英文，与全文同语）：relay-form targets attempt a direct peer-to-peer path; STUN queries go to the listed STUN operators (defaults …) and reveal the machine's public egress address to them and to the peer; ICE/STUN is UDP and does **not** use the `proxy` setting or `HTTPS_PROXY`; set `p2p.enabled: false` to keep all traffic on the relay, or point `p2p.stun` to a self-hosted server / `[]`.
- **P5 措辞**：不写「relay WSS 经 proxy」；写「`proxy` 设置本就只管升级与 git；relay WSS 只受 `HTTPS_PROXY` 等环境变量影响；打洞 UDP 两者都不经」。spec r1 同句由协调者按拍板修订。
- `README.md`/`README.zh-CN.md` 配置节加 `p2p:` 示例与「滚动升级约束」一句。
- roadmap 已有 B438 OOS 五条（`docs/roadmap.md:870-874`）；P1 若采纳「留 relay」，追加「预览 raw 流走直连」一条。

---

## 5. 工作单元与依赖（L3 增段③）

### 5.1 子卡与依赖

| 子卡 | 内容 | 依赖 |
|---|---|---|
| **B438.1**（首故事，单卡单轮） | S1–S6、S8；p2p + relay + config + targetclient + cmd + 嵌套 go.mod tidy（P4）+ 文档 | — |
| B438.2 | S7；mobilecore 组装 + 平台适配 + gomobile 产物 | **契约/数据硬依赖** B438.1：消费 §6 F3 冻结的 `relay.WithP2P`/`P2POptions`、F2 的 `config.DefaultP2PSTUN`，以及 B438.1 真机结论（承重假设「pion 在 handoff 连接生命周期里稳定升级/回落」）。另有**施工排序**：同改 `mobile/go.mod`（B438.1 只 tidy） |

B438.1 以 `--no-ff` 合入父卡分支后的提交 = B438.2 基线。串行，不并行。

### 5.2 B438.1 内部顺序（先验证承重假设，再产生可检查结果）

1. **承重假设复现（阻塞后续一切）**：根 `go.mod` 加 `pion/ice/v4 v4.4.7`、`pion/sctp v1.12.0`、`pion/transport/v5`、`pion/stun/v4`、`pion/turn/v5`（测试用）；建 `internal/p2p`，先写 vnet 测试把 §2.2 六个场景在仓内跑绿，**外加**「适配层上叠 `relay.SecureClient/SecureServer` + yamux」一条（放 relay 包测试，因 E2E 在 relay）。任一不绿 → 停，带原始输出回报协调者，不进入第 2 步。同步 `cd mobile && go mod tidy && go build ./... && go test ./...`、`cd desktop && go mod tidy && go vet ./...`（desktop 缺 `frontend/dist` 时 `go list` 报 embed 错——与本卡无关，以 `go build ./...` 去掉 embed 包或按 desktop 既有构建入口验证，证据写交回物）。
2. **信令 codec**（p2p）：§6 W1–W15 的表驱动 + 金样本 + roundtrip 属性测试。
3. **config**：§4.5 + T-S8 配置部分。
4. **Listener**：信令模式、拒绝码、上限、直连服务路径、生命周期归属。
5. **Dialer**：状态机、退避、Transport（P3）、Close。
6. **竖切**：§8.1 夹具 + T-S1/S2/S3/S5/S6/leak。
7. **组装点**：`cmd/agentd.go` Listener `WithListenerP2P`、启动 INFO；`targetclient`（§5.3）；`cmd/root.go`。
8. 文档（§4.6）→ 全量（implement 三段律）→ 真机（§9 R1–R6）。

### 5.3 构造签名变化与调用方清单

- `relay.NewDialer(relayURL, credential, node, token, account string, log *slog.Logger, opts ...DialerOption)`；`relay.NewListener(..., handler, log, opts ...ListenerOption)`。**变长选项、零值 = p2p 关**：既有调用方不改也能编译且行为不变（fail-safe 到 relay）。
  - 必须传选项的生产组装点：`cmd/agentd.go:270`（Listener）、`internal/targetclient/targetclient.go:45`（Dialer，Pool 与 CLI 共用 `newWithDialer`）、`internal/mobilecore/core.go:65`（**B438.2**）。
  - 保持不传（行为不变）：测试 `internal/relay/{listener_test.go:32,34,88,90; dialer_test.go:23,35,67}`、`internal/client/{client_relay_test.go:11; execution_test.go:210,229,245}`。
- `targetclient.New(name, t, log)` → 增加 P2P 参数（形状由执行者定：如 `New(name, t, p2p relay.P2POptions, log)` 或 options 结构）。调用方：`cmd/root.go:265`（CLI：由已加载的 cfg 翻出选项 + 起跳延迟 + 代理提示）、`cmd/upgrade.go:123`（**传关**：升级是一次性 relay 调用）、`internal/agentd/machines.go:226`（直连探测，**传关**/不涉及）。`newWithDialer` 的 Pool 路径从 `p.conf()` 翻出选项、无延迟、不打代理提示（agentd 启动已打）。
- 「配置 → relay 选项」的翻译只写一处（targetclient 内），CLI 与 Pool 共用；mobilecore（B438.2）直接用 `config.DefaultP2PSTUN()` 拼选项。

### 5.4 B438.2（子卡执行者依本节自写子卡 plan，协调者审）

- **目标**：S7。`mobilecore.DefaultDial` 构造 relay Dialer 时带 `WithP2P(P2POptions{Enabled:true, STUN: config.DefaultP2PSTUN()})`；`Resume`/回前台后若直连已死，触发一次升级（不等退避——用户可感知的恢复时机；需在 relay 加一个「复位退避并尝试」的导出方法时，按 §6 冻结流程补冻结）。
- **边界**：`internal/mobilecore`、`mobile/go.mod|go.sum`、（如需）relay 导出面新增一项。**绑定面不变**：`TestBindExportedSurfaceIsFrozen` 原样绿，不加导出函数。mobilecore 新 import `internal/config`（依赖图 +config/proxycfg/yaml），在交回物写明。
- **验收**：mobilecore 竖切（vnet 注入经 `P2POptions.Net` 测试缝；或本机两 host 候选的回环变体——ICE 缺省排除 loopback，需 `WithIncludeLoopback` 时只在测试缝内开）；`go test ./bind/ -run TestGomobileSurfaceHasNoSkips` **非 SKIP 通过**（需装 gobind）；`./build.sh android` 与 `./build.sh ios` 产物构建通过；Android 模拟器上取接口枚举证据（anet 返回非空、host 候选含模拟器网卡地址）；前后台：模拟器切后台 ≥30 s → 回落 relay 日志 → 回前台重升级日志。**未验**：真手机、运营商 NAT（CGNAT）、iOS 真机后台套接字回收。
- **首步**：定位工具链机器（本机无 gobind / Android SDK / 完整 Xcode，台账「基线亲跑」）；README 记的 2026-09-19 AAR 构建机需向协调者确认是哪台。

---

## 6. 需冻结的外部承诺（条件节）

入口判据命中三处：① 打洞信令 = 新跨进程 wire（coordinator 含移动端 ↔ executor listener，混跑版本兼容）；② `p2p:` 配置键 = 用户可见、跨版本兼容承诺；③ relay Dialer 的 P2P 选项被 B438.2 独立工作单元共同依赖。Listener 选项、p2p 包导出面只在 B438.1 单元内消费，**不冻结**。日志文案已由 spec 冻结，引用不重述。复用契约：E2E（`e2e.go`，`handoff-e2e-v1` 域，不变）、relay 控制帧（`frame.go`，不变）、app-yamux（`relayYamuxConfig`，不变）、previewRawMagic 子协议（不变）。

### F1 打洞信令 wire（v1）——每条一支断言

| # | 断言 |
|---|---|
| W1 | 魔数 = ASCII `handoff-p2p-ice-sig-v1`，长度 22，`== len(previewRawMagic)` 且 `!= previewRawMagic` |
| W2 | 请求 = 魔数 ‖ uint32 大端长度 ‖ JSON 体；长度 ∈ [1, 16384]，越界 listener 回 `bad_request` 并关会话 |
| W3 | 请求 JSON 含 `v`（整数，=1）、`ufrag`（非空串）、`pwd`（非空串）、`candidates`（字符串数组，≤ 32 条） |
| W4 | 未知 JSON 字段被忽略（前向兼容）；`v` 缺失视为 bad_request；`v` ≠ 1 → `unsupported_version` |
| W5 | 应答 = 同一魔数 ‖ uint32 大端长度 ‖ JSON 体；长度 ∈ [1, 16384] |
| W6 | 接受应答 JSON：`{"v":1,"ok":true,"ufrag","pwd","candidates"}`，字段约束同 W3 |
| W7 | 拒绝应答 JSON：`{"v":1,"ok":false,"reject":<码>}`，码 ∈ {`disabled`,`busy`,`bad_request`,`unsupported_version`} |
| W8 | 发起方在收齐 22 字节应答魔数前读到 EOF/连接关闭 → 判「对端不支持」 |
| W9 | 应答前 22 字节 ≠ 魔数 → 判「对端不支持」 |
| W10 | `disabled`、`unsupported_version` → 「对端不支持」；`busy`、`bad_request`、未知拒绝码 → 普通失败退避 |
| W11 | 一问一答后双方关闭该信令会话，会话上不再有任何字节 |
| W12 | 角色：coordinator = ICE controlling = sctp client = `OpenStream(0)` = E2E `SecureClient`；listener = controlled = sctp server = `AcceptStream` = `SecureServer` |
| W13 | 直连流上 E2E 的 token/account/node 与 relay 会话相同（PSK 派生与 `handoff-e2e-v1` 不变）；直连会话 E2E 后只承载 app-yamux，不做魔数判别 |
| W14 | `candidates` 每项 = `ice.Candidate.Marshal()` 输出，接收方 `ice.UnmarshalCandidate` 解析；任一项解析失败 → `bad_request` |
| W15 | listener 自 E2E 完成起 10 s 内收不齐请求 → 关会话（不回应答） |

- **金样本**：固定 ufrag/pwd/候选的请求与两种应答的十六进制字节写成 golden 测试（`frame_golden_test.go` 同款）；encode∘decode roundtrip 属性测试（随机字段，含空候选数组与未知字段）。
- **旧 listener 回放**：用今天 `serveSession` 的 app 分支（SecureServer → yamux.Server → http.Serve）原样做「旧 listener」夹具，断言 W8（yamux 读到 `'h'` 版本字节即关，台账出处）。
- **拍板记录（三重闸门）**：*信令会话一问一答即关，不保活作 ICE 期间的控制通道*。被否方案：保持信令会话直到 ICE 结果，用于「发起方放弃时通知 listener 立即释放 agent」。否决理由：listener 侧已有 12 s 预算与待连上限兜底，保活方案要多冻结一组控制消息；**反过来写不会有任何现有测试变红**，故记此段。代价：发起方 Close 后 listener 的待连 agent 最多空占 12 s。

### F2 配置键（用户可见）

| # | 断言 |
|---|---|
| C1 | 无 `p2p` 键 → `P2PEnabled()==true`，`P2PSTUN()` == 缺省列表（spec 三条，顺序一致） |
| C2 | `p2p: {enabled: false}` → `P2PEnabled()==false` |
| C3 | `p2p: {stun: []}` → `P2PSTUN()` 为空且非缺省；Save→Load 往返后仍为空（不被 omitempty 吞） |
| C4 | `p2p: {stun: null}` / 省略 → 缺省列表 |
| C5 | 缺省配置 `Save` 的 yaml 不含 `p2p` 键（旧版 agentd 可读） |
| C6 | `stun` 项非 `stun:host:port` 形态 → Load 校验失败，错误文本含该项下标 |
| C7 | `P2PProxyNotice()` 真值表：proxy 空/非空 × enabled 真/假，仅（非空, 真）为真 |
| C8 | p2p、relay、targetclient、mobilecore 源码不含任何 STUN 主机字面量（grep 断言，缺省值只在 config） |

### F3 relay Dialer P2P 选项（B438.2 共同依赖）

- `type P2POptions struct { Enabled bool; STUN []string; Net p2p.Net }`——`Net` 注释标「测试缝：nil = 真网络」（架构法第四条例外出口）；`p2p.Net` 为 `transport/v5.Net` 的类型别名，生产调用方不需 import pion。
- `func WithP2P(P2POptions) DialerOption`；零值/不传 = 不打洞，行为与 f5e1d443 逐点一致。
- 断言：不传选项的 Dialer 在整个生命周期内不开第二条 CONNECT、不创建 ICE agent（T-S5c）。
- 冻结即提交：B438.1 首个提交即含该类型与选项的可编译骨架（不写业务行为）。

---

## 7. 风险对抗审查（defect-families，逐族）

| 族 | 设问与结论 → 验收责任 |
|---|---|
| **生命周期 / 状态机中断** | Dialer.Close 发生在升级任一阶段（收集中/等 Answer/ICE 检查中/E2E 中）→ 必须无 goroutine、无 agent 残留：T-leak 对每个阶段注入 Close（用 p2p 测试缝的阶段钩子或 vnet 丢包让其停在该阶段）。agentd 退出 → Listener 级 WaitGroup 归零才返回。relay 断线重连**不**拖住在役直连会话、也不被直连会话拖住（§2.3）：T-S3 变体「relay 断线时直连仍服务、`RunWithReconnect` 照常重连」。孤儿：listener 待连 agent 由 12 s 预算回收（F1 拍板记录）。 |
| **静默失败 / 误导报错** | 每条失败路径要么 INFO（带 reason）要么 WARN（e2e_failed），Close 取消路径只 Debug。「报成功但没做」窗口：`p2p 直连已建立` 只在 app-yamux 建好后打；T-S1 断言打出该日志后的请求确实不经 relay（计数器）。「对端不支持」只打一次（T-S5a 断言两次尝试后仍只一条）。 |
| **跨平台假设** | 接口枚举（Android → anet，B438.2 取证）；Windows/macOS 防火墙弹窗——Windows 上 agentd 首次监听 UDP 可能触发防火墙提示（**未验证，需真机**，Windows 桌面验收清单 `docs/windows-desktop-acceptance.md` 补一项）；IPv6-only / 无 IPv4 网卡：`[UDP4,UDP6]` 两类都收集。 |
| **假红 / 假绿测试** | 「relay 字节不增长」必须用 fake relay 的**真实 splice 计数**，不能用 Dialer 内部标志（否则锁的是帮手不是行为）；反面断言：S2/S5 下 relay 计数**确实增长**（证明计数器是活的）。时间判据（≤15 s、≤30 s）用真实时钟测一次 + 退避序列用注入时钟单测，避免竖切里真等 30 s/10 min。vnet 是夹具编码的世界 → 真 NAT 行为归 §9。并发：T-S1 含 4 并发请求。 |
| **门禁绕过** | 直连会话进 `http.Serve(handler)` 的路径必须与 relay 会话同样先过 E2E（token PSK）——同一 handler、同一前置门；直连上禁止 raw/信令模式（W13），否则直连成了绕过 preview-raw 网络白名单（`listener.go:202` 只允许 tcp*）的旁路。TOCTOU：p2p.enabled 在 Listener 构造时读定，无运行期切换窗口。上限计数在锁内增减，拒绝在占位之前判定。 |
| **序列化边界** | 信令 JSON 是手写跨进程编码：金样本 + roundtrip（F1）；`stun` 显式 `[]` vs 缺失穿过 yaml Save→Load 真实链路（C3/C5）。 |
| **枚举新值过白名单** | 新会话模式（信令魔数）流经的判别点只有 `serveSession` 一处：三分测试覆盖；拒绝码枚举在发起方 switch 有 default 分支（W10 未知码）。 |
| **承重安全属性有测试锁住** | ① 直连 E2E（错 token 失败，T-S6a，变异：把直连路径的 SecureServer 换成裸 conn 必红）；② ICE 凭据/候选不出现在 relay 可见字节（T-S6b，fake relay 记录信令 CONNECT 的全部 splice 字节，断言不含 `ufrag` 子串与任一 vnet 内网/外网 IP 串）；③ 日志不含凭据/候选串（T-S6c）。 |

---

## 8. 验收（B438.1）

### 8.1 竖切夹具（relay 包测试内）

- **多 CONNECT 假 relay**：在 `startBridgeRelay` 旁新写（不改旧的，旧测试照用）：一次 REGISTER 后保留 executor session 供任意多条 CONNECT 各 `Open()` 一条流；每条 CONNECT 一个计数器（两向 splice 字节）与可选「记录全部字节」开关；可一键关掉 relay（模拟 relay 断线）。relay 腿走真回环 TCP（httptest），ICE 腿走 vnet。
- **vnet 世界**：一个 WAN 根路由 + 两个 LAN 路由（NATType 可选：端口受限锥 / 对称）+ WAN 上 vnet STUN（turn/v5）；WAN ChunkFilter：计 STUN 包、一键全丢（断链）。照 spike `build()`，可放 p2p 包的 `p2ptest` 测试辅助子包供 relay 与（B438.2）mobilecore 复用（测试辅助，非生产导出面）。
- **日志捕获**：slog 测试 handler 收全部级别记录（含 Debug）。

### 8.2 测试清单（成功 + 关键反例）

| ID | 断言 |
|---|---|
| T-S1 | 两端口受限锥：首请求立即经 relay 成功（不等打洞）；≤5 s 出 INFO `p2p 直连已建立`，attrs 含 node、local/remote 候选类型 ∈ {host,srflx,prflx}、地址族；之后 20 条顺序 + 4 并发请求全部成功且 **app-tunnel CONNECT 计数增量 = 0**；直连侧字节 > 0 |
| T-S2 | 一端对称：首请求 relay 成功且耗时与无 p2p 基线同量级；自起跳 ≤15 s 出 INFO `p2p 打洞失败，继续走 relay`（reason ∈ {timeout, ice_failed}）；全程无 WARN；期间请求全成功；relay 计数增长（反面：计数器活着）；退避序列单测：30s,60s,120s,…,600s,600s |
| T-S3 | 已直连后 WAN 全丢：≤30 s（预期 ≈10 s）出 INFO `p2p 直连已断开，回落 relay`；之后新请求成功且 relay 计数增长；恢复 WAN + 注入短退避 → 再次 `p2p 直连已建立`；Disconnected 期间新请求不走直连 |
| T-S3b | 直连在役时关掉假 relay：新请求仍经直连成功；Listener 照常重连 relay（`RunWithReconnect` 不被直连会话阻塞） |
| T-S4 | `targetclient` 直连形态：构造不产生 relay Dialer、无任何含 `p2p` 的日志记录、零 STUN（扩展现有构造路径测试） |
| T-S5a | 新 coordinator ↔ 旧 listener 夹具：请求全经 relay 成功；INFO `p2p 对端不支持打洞，继续走 relay` 恰一条（两次尝试后仍一条）；无 WARN |
| T-S5b | listener `enabled:false`：应答 `reject:"disabled"`；coordinator 行为同 T-S5a；listener 未创建 ICE agent |
| T-S5c | coordinator 不传/关 P2P：全程 relay CONNECT 数 = 1、STUN 包 = 0 |
| T-S5d | 新 listener ↔ 旧 coordinator（不发信令）：既有 `TestListenerServesHandlerThroughFakeRelay`/`RawDial` 原样绿 |
| T-S6a | 对端完成 ICE 但直连 E2E 用错 token：握手失败、handler 零调用、listener 与 coordinator 各 WARN 一条、业务仍经 relay |
| T-S6b | 信令 CONNECT 的全部 splice 字节不含 `ufrag` 子串、不含任一 vnet IP 串 |
| T-S6c | T-S1/T-S3 全程（含 Debug）日志记录的 msg 与 attrs 不含 `ufrag`、` typ `、本次会话的 ufrag/pwd 值（p2p 单测用 `WithLocalCredentials` 固定值断言；竖切用子串断言） |
| T-S8 | C1–C8；p2p 单测 STUN 空 → WAN STUN 包 0；agentd/CLI 打点：判据函数单测 + 真机 R6 grep 日志 |
| T-codec | W1–W15 表驱动 + 金样本 + roundtrip |
| T-mode | `serveSession` 三分：信令 / preview-raw / app 各一例 |
| T-leak | 升级各阶段 Close、直连在役 Close、Listener ctx 取消：goroutine 回到基线（`github.com/pion/transport/v5/test.CheckRoutines` 或计数比较），ICE agent 全部 GracefulClose |
| T-adapter | 适配层：小缓冲读（< 消息长度）拼回原字节；> 64 KiB 单次写被切片；Close 后读写报错；其上 SecureClient/Server + yamux 8 并发流 1 MiB 回显（承重假设仓内复证） |

### 8.3 运行入口

- 定向：`go test ./internal/p2p/... ./internal/relay/... ./internal/config/... ./internal/targetclient/...`（竖切含真实秒级等待，单包 ≤ 90 s；超时先查 vnet 夹具而非放宽判据）。
- 竞态：`go test -race ./internal/p2p/... ./internal/relay/...`。
- 嵌套 module：`cd mobile && go build ./... && go test ./...`；desktop 按 P4 验证。
- 全量按 implement 三段律；已知环境敏感测试见 memory（`CLAUDE_CODE_SESSION_ID`/嵌套 worktree 引起的两个 Go 测试红，非回归）。

---

## 9. 真机清单（边界型子系统；B438.1 验收必做，证据存 `docs/superpowers/evidence/b438/`）

**硬禁**：不碰在役 agentd（linux-01:7777 那个）、不碰 relay.chanliu.net、不碰共享账本（隔离 agentd 不配 `ledger.dsn` → 单机 SQLite 落各自 datadir）、不碰 `/etc` 网络配置之外的宿主状态。全部资源在 `/root/b438-real/` 与 netns 内，用完拆。

**环境**：linux-01（`ssh root@100.81.114.119`）；已核 `ip`/`iptables(nf_tables)`/`nft`/`tcpdump`/`go1.26.1` 在、`ip_forward=1`、无现存 netns；**`conntrack` 缺**（S3 改用丢包规则，不靠清 conntrack）。

### 9.1 构建（本机或 linux-01）

```bash
# handoff（B438.1 分支）与「旧版」handoff（main @ 合入前，供 R5）
GOOS=linux GOARCH=amd64 go build -o /tmp/b438/handoff-new .
git worktree add /tmp/b438-old <合入前 main 提交> && (cd /tmp/b438-old && GOOS=linux GOARCH=amd64 go build -o /tmp/b438/handoff-old .)
# handoff-server（只读构建，不改该仓）
(cd ~/workspace/handoff-server && GOOS=linux GOARCH=amd64 go build -o /tmp/b438/handoff-server ./cmd/handoff-server)
# STUN（pion turn/v5 自带纯 STUN 示例；在任一 require 了 turn/v5 的 module 内构建——B438.1 后根仓即是，
#  本节点已在 scratch module 亲跑 `GOOS=linux GOARCH=amd64 go build github.com/pion/turn/v5/examples/stun-only-server` 通过）
GOOS=linux GOARCH=amd64 go build -o /tmp/b438/stun github.com/pion/turn/v5/examples/stun-only-server
scp /tmp/b438/* root@100.81.114.119:/root/b438-real/bin/
```

### 9.2 拓扑（netns）

```
ns wan  : br-wan 198.51.100.1/24（relay :8443 明文 ws、STUN :3478）
ns nat-a: wan0 198.51.100.11 ── lan0 10.10.1.1/24   POSTROUTING -o wan0 -j MASQUERADE            （端点无关映射）
ns nat-b: wan0 198.51.100.12 ── lan0 10.10.2.1/24   POSTROUTING -o wan0 -j MASQUERADE [--random-fully]（R2 时加 = 对称）
ns host-a: 10.10.1.2/24 via 10.10.1.1   agentd A（coordinator，targets: b 为 relay 形态）
ns host-b: 10.10.2.2/24 via 10.10.2.1   agentd B（executor，relay: 注册 node b438-b）
```

建法（执行者写成 `/root/b438-real/topo.sh up|down|sym|cut|uncut`，脚本与输出入证据）：`ip netns add` ×5；`ip link add … type veth peer …` 连线；`ip netns exec wan ip link add br-wan type bridge` 挂 nat-a/nat-b 的 wan 端；各 ns 内 `ip link set lo up`、`sysctl -w net.ipv4.ip_forward=1`（nat ns）、默认路由；nat ns 内 `iptables -t nat -A POSTROUTING -o wan0 -j MASQUERADE`。netns 内 iptables 规则与宿主隔离，不影响 Docker/在役服务。

### 9.3 隔离 relay 与账户（handoff-server，SQLite）

```bash
# /root/b438-real/relay/config.yaml
listen_addr: "198.51.100.1:8443"
admin_master_key: "<openssl rand -hex 32，只在本文件>"
admin_cookie_secure: false
public_base_url: "http://198.51.100.1:8443"
email_dev_mode: true
db_path: "/root/b438-real/relay/ledger.db"
meter_flush_interval: "1s"
log_level: "debug"
ip netns exec wan /root/b438-real/bin/handoff-server -config /root/b438-real/relay/config.yaml &> /root/b438-real/relay/log &
ip netns exec wan /root/b438-real/bin/stun -public-ip 198.51.100.1 -port 3478 &> /root/b438-real/stun.log &
# admin（照 handoff-server docs/deploy/README.md:178-205 逐字跑通的 curl，BASE=http://198.51.100.1:8443，均在 ns wan 内执行）
PUT  /admin/slot-specs                 {"id":"basic","name":"b438","included_bytes":1073741824,"throttle_bps":0,"grace_bytes":1073741824,"grace_days":7}
POST /admin/accounts                   {"id":"b438"}
POST /admin/accounts/b438/credentials  {"role":"register","node":"b438-b"}   → 记 id、secret
POST /admin/accounts/b438/credentials  {"role":"connect"}                     → 记 secret
POST /admin/accounts/b438/slots        {"spec_id":"basic","valid_days":1}     → 记 slot id
POST /admin/slots/<slot>/rebind        {"credential_id":"<register id>","node":"b438-b"}
```
（限速只在超出 `included_bytes` 后的档位生效，1 GiB 额度内不会触发；R1 的计量读数前后各等 ≥2 个 `meter_flush_interval`。凭据只进 `/root/b438-real/` 下的配置，不进证据、不进日志摘录。）

### 9.4 隔离 agentd

两份 `/root/b438-real/{a,b}/config.yaml`：独立 `datadir`、`listen: 127.0.0.1:1777x`、`name: b438-a|b438-b`、`sync: {auto: false}`、不配 `ledger.dsn`、随机 token（两端 B 的 token 一致写进 A 的 target）、B 配 `relay: {url: ws://198.51.100.1:8443/relay, credential: <register secret>, node: b438-b}`、A 配 `targets: {b438-b: {relay: ws://…/relay, credential: <connect secret>, node: b438-b, token: <B token>}}`。启动：`ip netns exec host-b /root/b438-real/bin/handoff-new --config /root/b438-real/b/config.yaml agentd`，A 同理。CLI：`ip netns exec host-a /root/b438-real/bin/handoff-new --config /root/b438-real/a/config.yaml --target b438-b status`。

### 9.5 用例（每条存：命令、日志 grep、计量读数、必要时 pcap）

| # | 步骤 | 判据 |
|---|---|---|
| R1 (S1/S6) | 起 A、B；A 日志等 `p2p 直连已建立`；记 `GET /admin/slots/<slot>/usage`（或 slot 列表 `used_bytes`）基线；A 侧 `for i in $(seq 50); do handoff … --target b438-b status; done`（注意 P2：CLI 每条命令新进程，若采纳延迟则短命令不升级——**流量判据用 A 的 agentd Pool 路径**：经 A 的 `/api/machines` 或控制台扇出触发对 B 的请求）；再读计量 | 日志 attrs 候选类型 srflx；计量增量 ≈ 0（仅 keepalive 量级）；`ip netns exec wan tcpdump -i br-wan udp` 见 10.10.x 映射后的 198.51.100.11↔.12 UDP 流；`tcpdump` 抓信令期间 relay WS 明文载荷中 grep 不到 `ufrag`/`typ host`（S6）；A/B 日志 grep 不到 `ufrag` |
| R2 (S2) | `topo.sh sym`（nat-b 加 `--random-fully`），重启 A | 首请求不多等；≤15 s `p2p 打洞失败，继续走 relay`；无 WARN；计量随请求增长；观察 30 s 后第二次尝试 |
| R3 (S3) | R1 状态下 `topo.sh cut`（nat-a 内 `iptables -I FORWARD -p udp -j DROP`，不动 TCP，relay 照通） | ≤30 s `p2p 直连已断开，回落 relay`；新请求成功、计量增长；`topo.sh uncut` 后退避期满重新 `p2p 直连已建立` |
| R4 (S4) | spec 定 S4 只验 go test（T-S4）；真机顺带：host-a ns 内再起一个直连形态 target 指向 A 自己 ns 内第二个隔离 agentd（`127.0.0.1:17773`） | 对其请求无任何 p2p 日志、该 ns tcpdump 无 3478 包 |
| R5 (S5) | B 换 `handoff-old` 重启 | A 一条 `p2p 对端不支持打洞，继续走 relay`，10 min 内不再打；业务走 relay；B（旧）无 panic。再换回新版 B 写 `p2p: {enabled: false}` → 同判据。再反向：A 旧、B 新 → 一切同今天 |
| R6 (S8) | A 配 `proxy: socks5://127.0.0.1:1` + `p2p.enabled` 缺省 | 启动一条 INFO 代理绕行提示；改 `p2p.enabled:false` → 无提示、tcpdump 无 STUN/ICE；改 `p2p.stun: []` → tcpdump 端口 3478 零包，仍可能 host 直连失败（跨 NAT）→ 打洞失败 INFO |
| R7 (泄漏) | 每个用例结束 `kill -TERM` agentd | 进程 10 s 内退出；`ss -uanp` 在该 ns 内无残留 UDP 套接字 |

**拆除**：`topo.sh down`（删 5 个 netns 即删其全部 veth/iptables）；杀 relay/STUN/agentd 进程；`rm -rf /root/b438-real`。证据里写拆除命令与 `ip netns list` 空输出。

**未验，需另行真机**：真实家用路由器 / 运营商 CGNAT、公网 STUN 三条缺省地址的可达性（真机 R1 用的是 netns 内自建 STUN）、Windows 防火墙行为、跨洲 RTT 下 12 s 预算是否够。

---

## 10. 决策权限（交回物「偏离与自主决定」的对照基准）

**执行者可自行决定（交回物逐条列出即可）**：p2p 包内部命名与文件拆分、适配层实现细节、§4.3 中除 S2/S3 判据倒推值（12 s 预算、Failed 5 s）以外参数的 ±50% 调整（须给依据）、日志 attrs 的键名（文案不可改）、测试夹具组织、`targetclient.New` 新参数的具体形状、listener 上限值在 [2,8]/[8,32] 内调整。

**须升级协调者（停下问，不得自批）**：
- 任一 §0 待拍板项在拍板前需要落地实现时（按建议实现并在交回物显著标注）；
- §6 冻结项的任何变更（魔数、帧形状、字段、拒绝码、角色、配置键语义、`P2POptions`/`WithP2P` 形状）；
- 用户可见日志文案与 spec 不一致；
- 需要改 handoff-server、relay 控制帧、E2E、app-yamux 配置；
- §5.2 第 1 步承重假设任一场景在仓内不绿；
- 改动越出 §1 文件集（尤其触碰账本、devices 表、B437 改动面）；
- 真机步骤需要碰在役 agentd / 生产 relay / 共享账本，或需要在 linux-01 宿主（非 netns）装包（如 `conntrack`）。

---

## 11. 自审

- 故事覆盖：S1–S6、S8 → B438.1（§3、§8.2、§9）；S7 → B438.2（§5.4）。✔
- 待验证假设有动作与阻塞范围：承重假设仓内复现为 §5.2 第 1 步、阻塞后续；E2E-on-adapter 未验 → T-adapter；真 NAT → §9；移动平台 → §5.4 标未验。✔
- 决策权限：§10。✔
- L3：待拍板清单在稿首（P1–P5）；行为闭环六条五格齐（§3）；依赖分清硬依赖（B438.2 ← F2/F3 + 真机结论）与施工排序（mobile/go.mod）；真机清单已汇总（§9）；冻结节有结论（F1/F2/F3，Listener 选项与 p2p 导出面明确不冻结）。✔
- 判据钉行为不钉计数：「relay 字节不增长」用真实 splice 计数 + 反面断言；无文件数/覆盖率判据。✔
