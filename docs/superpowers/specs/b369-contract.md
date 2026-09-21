# B369 契约增量：移动端连接核与配对载体 wire

**上游状态：已批准**（源 spec：`docs/superpowers/specs/2026-09-13-mobile-app-design.md`，头部状态行
「状态:**已批准**(2026-09-13 用户批准形态;同日三轴独立审计,修订已落)」——本轮已对工作树复核一致，无需回写）
**级别：L3 重档**
**冻结状态：本提交随 `codegraph/target.json`、`codegraph/best.json`、`codegraph/diffs/cards-B369-charter.json`、Ticket 0 骨架、直通竖切与本台账冻结**
**有效基线：** `cards/B233.1-charter-7` @ `977720e2`（第三次派发起点；前两轮 c99a527d 空转、82965bef 冻在旧基线 9686ca23 已 stop）
**架构形态：** 按子系统分域的平铺领域包，无横向 controller/service/dao 分层（沿用 `codegraph/best.json`）。**本卡不新增顶层子系统**——移动连接核沉进既有 `d_transport` 子系统（见 §2）。
**交棒：** breakdown。

本文档把已批准 spec 的连接核/配对契约语义翻译成现状代码可接的签名、wire 形状与依赖方向。本节点落空壳与直通竖切（§7）；壳侧（Kotlin/Swift）、webview 内容面、CLI `console --qr` 接线、ticket→cookie 程序化兑换全部列入交棒欠账（§8），不能被「已有空壳」冒充完成。

---

## 0. 工具链 gate 回执（spec 测试决定：contract 启动前出结果）

上游 B369 台账 `docs/superpowers/ledgers/2026-09-14-b369-contract-ledger.md` 已记录 gate **双端 PASS**：
go 1.26.1 + gomobile@2026-09-08 + Xcode 26.6 + NDK 30.0.16248370（`-androidapi 21`），
`internal/relay` + `internal/client` 传递闭包可绑，产出 `spike.xcframework`（ios-arm64 两 slice）
与 `spike.aar`（classes.jar + 四 ABI `libgojni.so`）。**本结果以台账为据，本轮不重跑工具链**
（本工作树为 linux，无 Xcode/NDK，重跑不可行；台账的 darwin 读数即 gate 依据）。

**两条写进交棒的工程约束**（台账原文）：
1. `mobile/` 模块的 go.mod **必须带 `tool golang.org/x/mobile/cmd/gobind` 指令**（go 1.24+），
   否则新版 gomobile 报 `golang.org/x/mobile` 不在本模块依赖图内。
2. Android 构建**必须显式 `-androidapi 21`**（NDK 30 移除 API<21 platform，而 gomobile 缺省 minsdk=16）。

---

## 1. 现状查证

### 1.1 已查证签名与代码事实

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| relay 懒拨号 | `NewDialer(relayURL, credential, node, token, account string, log *slog.Logger) *Dialer`；`DialContext` 每次开一条 app-yamux 流；`Transport() *http.Transport`（`Proxy:nil`）供 HTTP 复用；`Ensure(ctx)` 主动建隧道；`Close()` 幂等 | `internal/relay/dialer.go#NewDialer`（`:47`）、`#Dialer.Transport`（`:385`）、`#Dialer.Ensure`（`:380`）、`#Dialer.Close`（`:390`） |
| relay 拨号时序 | `ensureTunnel`：WSS dial → 发 `CONNECT` → 收 `CONNECT_OK` → `SecureClient`（E2E Noise）→ `yamux.Client`；顺序是 E2E 在 app-yamux 之外，与 executor `listener.serveSession` 逐行一致 | `internal/relay/dialer.go:267-340`、`internal/relay/listener.go:149-186` |
| relay 控制线格式 | `FrameType` 字面值 `CONNECT/CONNECT_OK/...` 全大写，与仓外 relay server 逐字节一致；`Encode/Decode` 拒未知 type | `internal/relay/frame.go#Frame`（`:40`）、`#Encode`（`:50`）、`#Decode`（`:62`） |
| relay E2E 密钥派生 | HKDF-SHA256，`info="handoff-e2e-v1\|"+account+"\|"+node`，32B 每会话 salt；PSK 源自 token | `internal/relay/e2e.go#derivePSK`（`:58`）、`#SecureClient`（`:35`）、`#DerivePSKForTest`（`:47`） |
| relay token 熵闸 | `CheckTokenEntropy` 要求 ≥32 hex 字符 | `internal/relay/entropy.go#CheckTokenEntropy`（`:8`） |
| 已选路客户端 | `NewRelay(d *relay.Dialer, token string) *Client`：`baseURL="http://localhost"`（占位、过对端 hostGuard），Transport 取 `d.Transport()`；`New(addr, token) *Client` 直连形态 | `internal/client/client.go#NewRelay`（`:208`）、`#New`（`:195`） |
| token 即主 Bearer | `NewRelay` 注释明文「token remains the agentd Bearer credential」；`do`/`wsDialOptions` 把 token 置 `Authorization: Bearer` | `internal/client/client.go:199-207`、`:398-399`、`:310-311` |
| 客户端基址/传输暴露 | `BaseURL()`（relay 形态恒 `http://localhost`）、`HTTPClient()`（转发基座复用其 Transport） | `internal/client/client.go#Client.BaseURL`（`:273`）、`#Client.HTTPClient`（`:287`） |
| ticket 寿命 | `ticketLifetime = 60 * time.Second` | `internal/agentd/auth.go:34` |
| ticket 一次性原子消费 | `ConsumeAuthTicket` 用条件 `UPDATE ... WHERE id=? AND consumed_at IS NULL`，`n!=1 → ErrNotFound`；**过期不在此判**，调用方在 Go 比较 | `internal/store/auth.go#Store.ConsumeAuthTicket`（`:94`…`:96`） |
| ticket→cookie 兑换 | `handleConsole`：原子消费 → 建 session → `Set-Cookie` → **302** 到合法 `next` 或 `/`；失败 401 文本 | `internal/agentd/authroutes.go#Server.handleConsole`（`:167`）、`http.Redirect(..., StatusFound)`（`:225`） |
| cookie 属性 | name=`handoff_session`、`Path=/`、`HttpOnly`、`SameSite=Lax`、`Secure=r.TLS!=nil`（明文 loopback 下必须非 Secure） | `internal/agentd/authroutes.go#sessionCookie`（`:343-354`）、`sessionCookieName`（`internal/agentd/auth.go:29`） |
| CLI 领 ticket | `handleIssueTicket` 由主令牌签发（会话身份 403）；`consoleURL` 用 `r.Host` 拼 `/console?ticket=` | `internal/agentd/authroutes.go#handleIssueTicket`（`:130`）、`#consoleURL`（`:86`） |
| 客户端领 ticket | `Client.IssueAuthTicket(ctx, deviceName) (*proto.AuthTicketResp, error)` → `POST /api/auth/tickets` | `internal/client/client.go#Client.IssueAuthTicket`（`:1267`）、`internal/proto/auth.go#AuthTicketResp`（`:16`） |
| Target 互斥语义 | `Target{Addr,Token,User,Relay,Credential,Node}`；`IsRelay()=Relay!=""`；`Validate` 硬拒 relay 与 addr 同时非空 | `internal/config/config.go#Target`（`:234`）、`#Target.Validate`（`:249-272`） |
| 会话吊销 | `handoff sessions revoke` 只吊销 cookie 会话（`DELETE /api/auth/sessions/{id}`），**不能吊销主令牌** | `cmd/sessions.go#sessionsRevokeCmd`（`:46`）、`internal/client/client.go#Client.RevokeSession`（`:1305`） |
| hostGuard | Host 白名单中间件，恒含回环三件套 `127.0.0.1/localhost/::1`；relay 投递要过它 | `internal/agentd/hostguard.go#loopbackHosts`（`:27`）、`#Server.hostGuard`（`:162`） |
| 组装点/浏览器归属 | `handleConsole` 挂在 auth 之外、hostGuard 之内；控制台静态资源挂在 auth 内层 mux | `internal/agentd/server.go:802-820` |
| 嵌套模块先例 | `desktop/` 独立 go.mod（module `.../desktop` + `replace ... => ../`）；根模块测试禁止其进 `go list ./...` | `desktop/go.mod`、`moduleisolation_test.go#TestDesktopModuleStaysOutOfParentBuildGraph`（`:17`） |
| 配对载体 | spec 只有语义（`docs/superpowers/specs/2026-09-13-mobile-app-design.md:53`）；**仓内零实现**——`--qr` 仅存在于 `docs/superpowers/plans/2026-08-11-agentd-browser-auth.md:56`（设计未实现），无 QR 库、无 `--qr` flag | 本轮实测：`grep` 全仓无 QR 实现；`cmd/console.go` 无 `--qr`（flag 仅 print-url/device/no-open，`:75-80`） |

### 1.2 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 真正生产者 | 真正消费者 | 结论 |
| --- | --- | --- | --- |
| `relay.Connect`/`ConnectOK`/`Connect` 帧 | `Dialer.ensureTunnel`（`dialer.go:296` 发 CONNECT） | 仓外 relay server（`.github` 无关，服务端在仓外） | 活跃；本卡复用 `relay.NewDialer`，不重实现帧 |
| `relay.ConnectOK` | 仓外 server 发 | `Dialer.ensureTunnel` 收（`dialer.go:305`） | 活跃；本卡沿用 |
| `agentd.ticketLifetime=60s` | `handleIssueTicket` 写 `expires_at=now+60s` | `handleConsole` 在 Go 比较（`now.Before(expiresAt)`，`authroutes.go:182`）；`store.ConsumeAuthTicket` 不判过期 | 活跃；bundle 里 ticket 寿命沿用 60s，N 张同时起算 |
| `sessionCookieName="handoff_session"` | `handleConsole` `Set-Cookie` | 浏览器 / webview cookie jar；`sessionFromRequest` 读回 | 活跃；移动核程序化兑换后**桥接入 webview cookie jar**，见 §3.4 |
| `config.Target.Token` | `handoff init` 生成（`randToken` 16B/32hex） | `NewRelay`/`New` 作 Bearer；relay 形态另作 E2E PSK 源 | 活跃；token 副本入设备安全存储即持该机全量 API 权限 |
| `config.Target` relay/addr 互斥 | `Validate` 硬拒两形态同时非空 | 配置加载 | 活跃；bundle 的**每机登记**自有「0..1 relay + 0..1 直连」wire 语义，**不改 Target 本身** |
| `proto.AuthTicketResp{URL,ExpiresAt}` | `handleIssueTicket` | `console` CLI、`IssueAuthTicket` 客户端 | 活跃；bundle 的 `PairTicket` 与之同形，但**另立 wire 类型**（bundle 内嵌，不复用 auth 响应体） |

本表无零使用死常量被当作事实源。spec 引用的 `console --qr`（`docs/superpowers/plans/2026-08-11-agentd-browser-auth.md:56`）是**已设计未实现**，本卡从「ticket URL」扩为「配对 bundle」，是扩展点不是既有事实。

### 1.3 图覆盖债

本轮亲自执行 `codegraph sym NewDialer`/`sym NewRelay`/`sym New`/`sym IssueAuthTicket`，均命中（`n_relay_NewDialer`→d_transport_tunnel、`n_client_NewRelay`/`n_client_New`→d_transport_channel）。
Ticket 0 新增符号（`internal/proto/pairing.go`、`internal/mobilecore/`）为**本轮首建**，随本分支视图 diff 落盘（§6）；
`mobile/` 是嵌套独立模块（与 `desktop/` 同例），**图外**，扫描配方已加 `mobile` 前缀排除（§4.5）。

---

## 2. 架构户口与依赖方向

### 2.1 户口（不新增顶层子系统）

**本卡不新增顶层领域。** 移动连接核沉进既有 `d_transport`（跨机连接）：它是「为协调者与执行机建立可靠通道」这一职责的**设备侧消费者**——
配对载体解析、按可达性选路拨号、每机独立回环透传反代，全部复用 `d_transport` 既有的 `relay` 懒拨号隧道与 `client` 选路客户端，协议零重实现。
配对载体的 wire 类型沉进既有 `d_protocol`（`internal/proto`），与 `RoomMessage`/`Session` 同例。

> **决策留痕（为什么不立 `d_mobile` 顶层域）**：`best.json` 新增顶层领域会改变 `best.SubsystemOf` 归属链，使 baseline 全量重扫成为前置——
> 而重扫是独立重操作（见 `docs/superpowers/notes/b233.26-scan-report.md`），本 contract 节点不夹带。移动核的容器在 `best.json` 登记为
> `d_transport_channel`（既有的「客户端路由」叶子域，`internal/client` 同域），与 client 同构：它就是一个**选路客户端包**。
> 若后续移动核长出独立子系统职责（壳生命周期、推送、多设备凭据），届时另起建图卡立 `d_mobile`。

### 2.2 依赖方向

本卡**新增两条跨子系统入口（entries）**，均落在既有方向上，**不新增方向、不改预算**：

- `d_transport → d_protocol`：既有方向（`legacyBudget:0`，原 entries `["proto 实体"]`）。
  补 entry `"proto（包级函数）"`：移动核复用配对载体的 proto **包级编解码**（`EncodePairBundle`/`DecodePairBundle`）。
- `d_transport → d_transport_channel`：**同子系统内**（`d_transport_channel.parent == d_transport`），域内边不执法，**无需契约条目**。

移动核 `internal/mobilecore` 的其它跨包调用（`internal/relay`、`internal/client`）均与 `internal/mobilecore` 同属 `d_transport` 子系统，域内边不执法。

**禁止反向依赖**：根模块不得 import `mobile/` 模块（与 `desktop/` 同例，见 §4.5 与 §8）。

---

## 3. 契约增量：精确签名

### 3.1 配对载体 wire（`internal/proto/pairing.go`，本分支新建）

```go
const PairVersion = 1                 // 信封版本；未知/缺省版本拒收
const PairMachineBudgetBytes = 200    // 单机登记 QR 容量预算上界（超界退路=粘贴串/分段码，归后续硬化）

var (
    ErrPairVersion   = errors.New("proto: 配对载体版本不受支持")
    ErrPairMalformed = errors.New("proto: 配对载体畸形")
)

type PairBundle struct {
    Version   int           `json:"v"`
    Relay     *PairRelay    `json:"relay,omitempty"`   // 无 relay 形态机器省键
    Machines  []PairMachine `json:"machines"`
    IssuedAt  time.Time     `json:"issued_at"`
    ExpiresAt time.Time     `json:"expires_at"`
}
type PairRelay struct {
    URL        string `json:"url"`         // relay WSS URL（管道级）
    Credential string `json:"credential"`  // coordinator CONNECT 凭证（管道 credential）
}
type PairMachine struct {
    Name       string      `json:"name"`
    Token      string      `json:"token"`                // agentd 主 Bearer 令牌副本
    Credential string      `json:"credential,omitempty"` // 管道凭证覆盖；通常空，沿用 bundle.Relay
    Node       string      `json:"node,omitempty"`       // relay 形态：relay 上节点名
    Addr       string      `json:"addr,omitempty"`       // 直连形态：agentd 地址
    Ticket     *PairTicket `json:"ticket,omitempty"`     // 离线机可缺
}
type PairTicket struct {
    URL       string    `json:"url"`
    ExpiresAt time.Time `json:"expires_at"`
}

func (m PairMachine) Validate() error
func (b PairBundle) Validate() error
func EncodePairBundle(b PairBundle) (string, error)   // 先 Validate；紧凑 JSON
func DecodePairBundle(raw string) (PairBundle, error) // 畸形→ErrPairMalformed；未知/缺省版本→ErrPairVersion
```

**每机登记语义**：relay 形态（`Node` 非空）与直连形态（`Addr` 非空）**0..1 且恰居其一**（`Validate` 强制）；
这是 bundle 自有 wire 语义，**不改 `config.Target` 的 relay/addr 互斥**（story 7 的同机两态切换由此成立）。
单机配对是 bundle 的退化形态（`Machines` 一条）。

### 3.2 连接核（`internal/mobilecore/core.go`，本分支新建）

```go
// DialFunc 是 Core 与选路实现之间的唯一接缝：按一份 bundle 与一台机器登记造
// 已选路 agentd 客户端与它的收尾函数。生产实现走 relay/client；测试注入假工厂。
type DialFunc func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error)

// DefaultDial 是生产 DialFunc：relay 形态走 relay.NewDialer + client.NewRelay；
// 直连形态走 client.New。relay credential 取机器覆盖，缺省沿用管道 credential。
func DefaultDial(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error)

type Core struct { /* 私有：dial、log、machines、closed */ }
func New(dial DialFunc, log *slog.Logger) *Core

// Pair 解析一份配对 bundle 并登记其中全部机器：可达的机器起 loopback 反代；
// 不可达的机器标 offline（部分 bundle，不整单失败）。重复扫码幂等（重配覆盖）。
func (c *Core) Pair(ctx context.Context, bundleJSON string) (PairResult, error)
func (c *Core) Origin(machine string) (string, error) // webview 应加载的 loopback 源
func (c *Core) MachineNames() []string                 // 设置页配对清单
func (c *Core) Close() error                           // 幂等

type PairedMachine struct { Name, Origin string; Online bool }
type PairResult struct { Machines []PairedMachine }
```

**可达性判据**：relay 拨号是懒建的（`Dialer.DialContext` 首次才建隧道），造出 Dialer **不等于**隧道通。
`Core.Pair` 对每台机器发一次最小请求（`/api/status`，3s 超时）判可达——任何 HTTP 响应（含 404/503）算可达，只把传输层失败判离线。
写死结果（探测只判可达、不解析响应）**不构成子卡的「已有活路径」**。

### 3.3 回环透传反代（`internal/mobilecore/proxy.go`，本分支新建）

```go
func newReverseProxy(machineName string, cl *client.Client, log *slog.Logger) http.Handler
```

实现用 `net/http/httputil.ReverseProxy`（**仓内首例**，全仓此前零 `httputil` 引用）：
- `Transport = cl.HTTPClient().Transport`——选路（relay 隧道 vs 直连）与桌面端**同源**；
- `Director` 把 `URL.Scheme/Host` 与 `r.Host` 改写为目标基址的 host（`http://localhost`，恒过对端 hostGuard）；
- **不注入任何凭据**：cookie / Authorization 原样透传。这是回环门禁的核心断言（§4.2）。

### 3.4 会话兑换（归实现节点，本节点只冻结语义）

spec 选定：Go 核用 token 副本**程序化**兑换 ticket→cookie（`handleConsole` 是 302 导航流，App 内无浏览器导航），
并桥接入 webview cookie jar。**多机会话域语义**：cookie 不按端口隔离（RFC 6265），故**每机独立回环端口**（`net.Listen("tcp","127.0.0.1:0")`），
切机即清 webview 会话罐并由 Go 核程序化重兑换，任何时刻一罐只装一机的 cookie。

**本节点只落反代与端口隔离；程序化兑换与 cookie jar 桥接是越过空壳的可观测行为，列欠账 §8.2。**

---

## 4. 原子冻结清单

每条是独立可判 pass/fail 的接缝断言。`[T0]` 标记条目由本轮 §7 的竖切/金样本测试锁住；其余为实现节点对账条目。

### 4.1 配对载体 wire（`internal/proto/pairing.go`）

1. `[T0]` 信封含版本键 `v`，编码值恒为 `PairVersion`（`TestPairBundleEnvelopeVersionKey`）。
2. `[T0]` 枚举编解码恒等：`DecodePairBundle(EncodePairBundle(b))` 保留 `Version/Relay/Machines/IssuedAt/ExpiresAt`（`TestPairBundleRoundTrip`）。
3. `[T0]` 未知版本（`v=999`）与缺省版本（`v=0`）均返回 `ErrPairVersion`（`TestPairBundleRejectsUnknownVersion`）。
4. `[T0]` 不可解 JSON 返回 `ErrPairMalformed`。
5. `[T0]` 空机器列表（`machines:[]`）返回 `ErrPairMalformed`。
6. `[T0]` 机器名空或 token 空返回 `ErrPairMalformed`。
7. `[T0]` 机器同时带 `node` 与 `addr`（双形态）返回 `ErrPairMalformed`。
8. `[T0]` 机器两形态皆空返回 `ErrPairMalformed`。
9. `[T0]` relay 形态机器而 bundle 缺管道端点（`relay` 空或 `url` 空）返回 `ErrPairMalformed`。
10. `PairBundle`/`PairMachine` 的 JSON 键集在 Go 与 Kotlin/Swift 解码方逐键一致（Go 侧金样本为本条可判锚；TS/Kotlin/Swift 孪生样本归欠账 §8.4）。
11. relay 形态机器登记的 `token` 须满足 §3.2 的 token 熵闸（实现节点在 `DefaultDial` 内以 `relay.CheckTokenEntropy` 执法）。

### 4.2 连接核（`internal/mobilecore/`）

12. `[T0]` `New(nil, …)` 用 `DefaultDial`（源码守卫：`dial==nil` 分支赋 `DefaultDial`）。
13. `[T0]` `Core.Pair` 对 relay 形态机器走 `DefaultDial` → `relay.NewDialer` + `client.NewRelay`（竖切真实穿过，见 §7）。
14. `[T0]` `Core.Pair` 对直连形态机器走 `client.New`（`DefaultDial` 分支）。
15. `[T0]` 单机拨号/探测失败标 `Online=false`，**不整单失败**（`TestPairPartialBundle`）。
16. `[T0]` 在线机返回的 `Origin` 形如 `http://127.0.0.1:<port>`，且端口为 `net.Listen` 分配（每机独立）。
17. `[T0]` 回环反代**不注入 Authorization**（竖切中上游见空 Authorization；变异验证见 §7.3）。
18. `[T0]` 反代投递的上游 Host 是 loopback 名（`localhost`），过对端 hostGuard（竖切断言）。
19. `Core.Pair` 重复扫码幂等：同名机器重配覆盖旧客户端与反代，不泄漏旧资源（实现节点以 `replaceMachine`/`stopMachine` 执法）。
20. `Core.Close` 幂等；关闭后 `Origin`/`Pair` 拒绝（实现节点）。
21. `Core.Pair` 对解码失败的 bundle 返回错误，不登记任何机器。

### 4.3 模块与依赖方向

22. `mobile/` 是独立嵌套 module（`module .../mobile` + `replace ... => ../`），根模块 `go list ./...` 不含 `/mobile`（与 `desktop/` 同例，`moduleisolation_test.go` 形态）。
23. 根模块（非测试）零 import `mobile/`（反向依赖禁止）。
24. `internal/mobilecore` 不 import `internal/agentd`（移动核不含 agentd）；不 import `internal/collab`/`internal/ledger`。
25. `[T0]` `graph check --view cards-B369-charter` 无本卡新增违规（本轮实测 fails=0）。
26. `[T0]` `graph validate --view cards-B369-charter` 零 issue（本轮实测）。
27. `[T0]` `codegraph/diffs/cards-B369-charter.json` 记录 Ticket 0 新增符号与 `pkgDomain` 新增 `internal/mobilecore` 映射；扫描配方排除 `mobile` 前缀。

### 4.4 工具链 gate（spec 前置，已回执 §0）

28. gomobile 编 `internal/relay` + `internal/client` 传递闭包出 AAR/XCFramework 双端可绑（台账 PASS；本工作树 linux 不重跑）。
29. `mobile/` 模块 go.mod 带 `tool golang.org/x/mobile/cmd/gobind` 指令（欠账 §8.1，因工具链 gate 在 darwin 做，本节点落码的 `mobile/go.mod` 尚未加此指令——**本轮 `go build ./...` 在本模块下不依赖它**）。

### 4.5 回环门禁与多机会话域（语义冻结，实现归欠账）

30. 回环监听是设备全局，反代**不注入凭据**（条 17）；无 cookie 的同机其他 App 过不了 agentd 的闸。
31. 每机独立回环端口；切机即清 webview 会话罐并程序化重兑换，任何时刻一罐只装一机的 cookie。
32. 程序化 ticket→cookie 兑换桥接入 webview cookie jar；cookie `Secure` 在明文 loopback 下为 false（沿用 `sessionCookie`）。

---

## 5. 依赖方向、组装点与预算

- `d_transport → d_protocol`：既有方向 `legacyBudget:0`，entries 由 `["proto 实体"]` 补 `"proto（包级函数）"`；文案记 B369 复用配对载体编解码。
- 移动核的 `relay`/`client` 调用与 `internal/mobilecore` **同属 `d_transport` 子系统**，域内边不执法，无需新条目。
- `best.json` 新增五条容器登记（`k_mobilecore_fn`/`k_mobilecore_model`/`k_mobilecore_Core` → `d_transport_channel`；`k_proto_PairBundle`/`k_proto_PairMachine` → `d_protocol`），结构树**不新增领域**。
- **组装点**：移动核的组装点是 `mobile/bind/bind.go`（gomobile 绑定壳），**图外**；根模块无新增组装点，`main.go`/`server.go`/`cmd/agentd.go` 不变。
- **预算**：零变化。

---

## 6. 拍板记录（三重闸门）

只记录同时满足「难逆转 × 无上下文会惊讶 × 真取舍」的决定。

**命中两条：**

- **移动核沉进 `d_transport` 而不新立 `d_mobile` 顶层子系统**。难逆转——顶层领域进 `best.json` 后，`SubsystemOf` 归属链与全部跨域边判定随之变，回头搬迁要动整张图与全量重扫；无上下文会惊讶——「移动端 App 有自己的连接核，为什么不给它一个域」是最自然的直觉；真取舍——被否方案就是「新立 `d_mobile`」，代价是本 contract 节点必须先做一次全量 baseline 重扫（独立重操作）。立。
- **回环反代透传、不注入凭据**。难逆转——反代一旦注入凭据，回环门禁从「agentd cookie 闸」塌成「反代进程自己判」，同机任意 App 可经回环源白拿该机 API 权限，且这是安全属性回头要重审全部移动端鉴权；无上下文会惊讶——「webview 里同源请求当然由反代补上凭据最省事」是被否方案的原话；真取舍——被否方案就是「反代注入 token/cookie」。立。

**不立（探索性/确定性设计，逐条记判据，防「空着与没审过不可区分」）：**
- 配对载体用紧凑 JSON + 版本信封：跨语言编解码的常规形态，无被否的像样方案。不立。
- 每机独立 loopback 端口：由「cookie 不按端口隔离」这一既成事实直接推出，无取舍空间。不立。
- `DialFunc` 接缝使 Core 可测：层内实现选择（对契约对侧不可见的测试缝），不立。

**无其它命中。**

---

## 7. Ticket 0、可执行冻结与直通竖切

### 7.1 Ticket 0 已落（本提交）

- `internal/proto/pairing.go`：配对载体 DTO 全集 + `Validate` + `Encode/DecodePairBundle`（完整定义，非残缺）。
- `internal/proto/pairing_fixture_test.go`：Go 侧金样本与拒收面（信封版本、roundtrip、7+ 畸形/拒收用例）。
- `internal/mobilecore/core.go`：`DialFunc`/`DefaultDial`/`Core`/`New`/`Pair`/`Origin`/`MachineNames`/`Close`（真实实现，非空壳——拨号经 relay/client，纯逻辑无留白）。
- `internal/mobilecore/proxy.go`：回环透传反代（真实实现）。
- `internal/mobilecore/core_test.go`：直通竖切 + 部分 bundle。
- `mobile/go.mod`、`mobile/bind/bind.go`：gomobile 绑定模块（嵌套独立 module，图外）。
- `codegraph/diffs/cards-B369-charter.json`：24 新符号 + 5 新容器 + 8 边。
- `scripts/codegraph-rescan/main.go`：扫描配方排除 `mobile`；`pkgDomain` 加 `internal/mobilecore → d_transport_channel`。

### 7.2 直通竖切（重档法定步骤）

一次真实调用穿全链，测试钉在主缝（库缝形态 = 夹具直调）：

**路径**：`proto.EncodePairBundle` → `Core.Pair` → `proto.DecodePairBundle` → `DefaultDial` → `relay.NewDialer` + `client.NewRelay`
→ 真 WSS（fake relay 终结 E2E + app-yamux）→ 上游 HTTP handler → 回环反代（`newReverseProxy`）→ 调用方拿到响应。

**测试**：`internal/mobilecore/core_test.go#TestPairVerticalSlice`（库缝：一次真实 HTTP 往返得 `pong`）；
同文件 `#TestPairPartialBundle` 覆盖离线机标 offline 不整单失败。

**竖切的写死结果**（fake relay 返回 `pong`）不构成子卡的「已有活路径」。

### 7.3 可执行冻结

- 配对载体的 JSON wire 编码（跨语言形状）已落 **Go 侧金样本** `internal/proto/pairing_fixture_test.go`，本轮跑过。
- **变异验证**：竖切可红——把「反代不注入凭据」改成注入 `Authorization: Bearer INJECTED`，`TestPairVerticalSlice` 立即失败
  （原文：`core_test.go:163: 反代不得注入 Authorization，上游看到 "Bearer INJECTED"`），还原即绿。这是回环门禁断言的变异证明。
- 无哈希/密钥派生/编码格式的**新**金样本需求（relay PSK 派生是既有 `internal/relay/e2e_psk_golden_test.go`，本卡不改）。

### 7.4 本轮实际跑过的命令

| 命令 | 退出码/结果 |
| --- | --- |
| `go build ./...`（根模块） | 0 |
| `go test ./internal/proto/...` | `ok` |
| `go test ./internal/mobilecore/...` | `ok`（含竖切与部分 bundle） |
| 竖切变异（反代注入 Authorization） | FAIL（原文见 §7.3），还原后 ok |
| `go build ./...`（mobile/ 模块，`replace ../`） | 0 |
| `go run …codegraph validate --view cards-B369-charter` | issues=null、edgeIssues=null |
| `go run …codegraph check --view cards-B369-charter` | fails=0 |
| `go run …codegraph check`（基线） | fails=0 |
| `go test ./cmd/ -run TestRepoContractGate` | PASS（warns 33） |

---

## 8. 本节点欠账（实现节点逐条补齐，不得静默带走）

1. **`mobile/` go.mod 补 `tool golang.org/x/mobile/cmd/gobind`**，并在模块内跑通 `gomobile bind -target=android -androidapi 21` 与 `-target=ios`（工具链 gate 已在 darwin 回执，本节点未在 linux 重跑）。
2. **程序化 ticket→cookie 兑换与 cookie jar 桥接**（spec §实现决定）：Go 核用 token 副本 `POST /api/auth/tickets` 领 `PairTicket.URL` → 请求该 URL（302）→ 取 `Set-Cookie` 注入 webview cookie jar；每机独立端口 + 切机清罐。**越过空壳的可观测行为，须有能变红的测试。**
3. **CLI `console --qr` 接线**（spec 扩展点）：`handoff console --qr` 产 bundle（从本机配置读 relay 端点与各 target 登记，逐机 `IssueAuthTicket` 代领 ticket），编码为 QR。CLI 侧组装点复用 `newTargetClient`；不得在 CLI 重实现 bundle 编解码（调 `proto.EncodePairBundle`）。
4. **Kotlin/Swift 解码方孪生金样本**：按 `pairing_fixture_test.go` 的键集逐键一致；TS 侧若控制台也消费，随 S6。
5. **骨架符号入图**：本分支视图 `cards-B369-charter.json` 已落；实现节点新增符号须补进视图或另开 diff（图覆盖债不该由自己冻结的符号构成）。
6. **`mobile/` 构建脚本/文档**：显式带 `-androidapi 21`；记录 NDK 30 与 gomobile 版本。
7. **回环门禁与多机会话域的端到端测试**：程序化兑换后，验证无 cookie 的同机请求被 agentd 拒（回环门禁成立）。
8. **离线机上线补配**：`Core.Pair` 对离线机只标记；「上线后补配」的重试入口归实现节点（spec：离线机标记、上线后补配）。
9. **轮换操作文档**（spec 用户故事 8）：token 泄露唯一处置 = 双端轮换节点 token 并重启；现状无文档，随本期 contract/文档段产出。
10. **web/ 响应式断点谱系与移动终端输入层**：纯内容面，归 web 实现节点（spec §实现决定），不属连接核契约面。

---

## 9. 移交 plan 附区

（本区由 plan 出稿时吸收，吸收后在区头标注「已由 plan〈文档〉吸收（日期）」销区。）

1. **移动核可测性**：`DialFunc` 注入使 `Core` 免真隧道测试；实现节点沿用 `core_test.go` 的 fake-relay 夹具形态。
2. **回环端口生命周期**：`startLoopback` 用 `net.Listen("127.0.0.1:0")`；实现节点须保证重配/关闭不泄漏端口与 goroutine（`stopMachine` 已收口，补竞态测试）。
3. **bundle 容量预算**：`PairMachineBudgetBytes=200` 是声明值；实现节点在 `console --qr` 侧实测 N 机 bundle 字节数并定 N 上界，超界退路（粘贴串/分段码）归该节点。
4. **`proto` 包结构**：配对 wire 独立成 `pairing.go`（与 `rooms.go`/`sessions.go` 同例），不塞 `proto.go`。
5. **错误哨兵**：`ErrPairVersion`/`ErrPairMalformed` 归 `internal/proto`（wire 层），CLI/壳侧按 `errors.Is` 分支渲染，不解析文案。

---

## 10. 本轮法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处（§1）。
- 上游状态位：spec 头部「已批准」已复核一致，无需回写。
- 目标图：`target.json` 补 `d_transport→d_protocol` 的 proto 包级函数 entry（既有方向）；`best.json` 新增五条容器登记（结构树不新增领域）；Ticket 0 新符号写入 `codegraph/diffs/cards-B369-charter.json`——**随本提交冻结**。
- Ticket 0 编译：本轮 `go build ./...`（根模块）退出码 0；`mobile/` 模块 `go build ./...` 退出码 0；`go test ./internal/proto/...`、`./internal/mobilecore/...` 全绿。
- 直通竖切：`TestPairVerticalSlice`、`TestPairPartialBundle` 本轮跑过；变异可红（§7.3）。
- 可执行冻结：Go 侧配对载体金样本本轮跑过；无新哈希/密钥派生命中。
- 图三闸：`validate --view cards-B369-charter` 0 issue；`check --view cards-B369-charter` fails=0；基线 check fails=0；`TestRepoContractGate` PASS。
- 三重闸门：§6 记两条命中 + 三条不立的判据，非空着。

---

## 修订记录（breakdown 出稿轮，2026-09-14）

拆解稿 `docs/superpowers/specs/b369-breakdown.md` 逐条核对 §4（32 条）与 §8（10 条欠账），**无退回项**；以下三条边界澄清即便结论是「不退回 contract」也留痕，供 review 冻结物触碰对账：

1. **配对 wire 的 JSON 金样本属契约面、非包内 API**：`PairBundle` 编码字节形状跨语言（Go 编 / Kotlin·Swift 解），`err_is` 哨兵与键集是契约的一部分（§4 条 10）；`Validate`/`Encode`/`Decode` 的 Go 函数签名是包内 API，可随实现微调（错误包装）只要哨兵语义不变。
2. **`d_transport→d_protocol` 的 entry「proto（包级函数）」覆盖 `Encode/DecodePairBundle`**：`internal/mobilecore` 调用属该既有 entry 范围，不新增方向、不加预算（实测 target budget=0）。
3. **`mobile/bind/bind.go` 是图外组装点**：contract §5 记为移动核组装点且图外（嵌套 module 不进根构建图）；其导出面变更不触发 `graph check`，但绑定面形状是壳的契约面。后续图对齐时补登记。
