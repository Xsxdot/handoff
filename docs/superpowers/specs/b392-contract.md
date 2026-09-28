# B392 契约增量：mobile bind 生产接线 SessionCookie / SwitchMachine

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b392.md`，头部状态行「状态：已批准（用户 2026-09-24 批准；D1 锚点与 S1–S3 建议已吸收）」——本轮对工作树复核一致，无需回写）
**级别：L3 轻档**（spec §2.3）
**冻结状态：本提交随 Ticket 0 骨架、能变红的适配器契约测试与本台账冻结**（`codegraph/target.json` / `best.json` / 本分支视图 diff 无变更——见 §4；本卡不引入根模块新符号）
**基线链（第 2 轮回炉后）：**
- spec 父基线 = `cards/B233.1-charter-7` @ `a8e60208`（spec §6 显式基线；B392 spec 冻结提交的直接父）
- 第 1 轮 contract 实际基线 = `cards/B392-spec` @ `400ee3c4`（spec 冻结提交，亦是任务卡注明的本卡合并目标分支）
- 第 2 轮续接点 = 已发布 `cards/B392-charter` @ `d2cd3246`（第 1 轮 contract 冻结提交）。本轮回炉 amend 该冻结提交，故本分支新冻结提交与 `d2cd3246` 同父 `400ee3c4`。
**架构形态：** 按子系统分域的平铺领域包，无横向 controller/service/dao 分层（沿用 `codegraph/best.json`）。本卡不新增顶层子系统，也不改任何根模块归属。
**交棒：** breakdown。

本契约不重开 B369 已冻结的 gomobile 七函数与 HTTP wire（spec §7/§12），只把已批准 spec 的**私有适配签名、单 Core 组装不变量、调用顺序、cookie 属性与测试接缝**翻成现状代码可接的精确签名，并把 Ticket 0 空壳落到 `mobile/bind`。

---

## 0. 工具链 gate 回执（spec 测试决定的前置）

B369 台账与 `mobile/README.md:82-84` 已记录：Android AAR 于 2026-09-19 在 darwin 机（NDK 30.0.16248370 + JDK 21 齐）一次构建通过；iOS XCFramework 未在完整 Xcode 机器上跑过（**真机清单未验项**）。钉版 gobind/`gobindPinned` 见 `mobile/bind/gobind_surface_test.go:18`（`v0.0.0-20260908204917-8b95e45f8d3e`）。

本工作树为 linux，无 Android SDK/NDK/Xcode，**本轮不重跑 gomobile/NDK/Xcode**；`TestGomobileSurfaceHasNoSkips` 在缺 `gobind` 时按既有约定 skip（`gobind_surface_test.go:37-39`），移动 CI/真机机器上必须实跑（spec §9.5，欠账 §9.3）。

---

## 1. 现状查证（签名与代码事实）

行号仅供本轮核对；`mobile/` 与 `internal/mobilecore` 在旧 baseline 中未入图（§3），故用精确 `file:line`；已入图符号用 `file#Symbol`。

### 1.1 绑定面（`mobile/bind`，图外嵌套模块）

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| 配对消费面 | `coreAPI`：`Pair(ctx,bundleJSON)`、`Origin(machine)`、`MachineNames()`、`Close()`；`*mobilecore.Core` 直接满足 | `mobile/bind/bind.go:25-32` |
| 生产 Core 装配 | 包级 `core coreAPI = mobilecore.New(nil, log)`（B392 后改为 `liveCore` 单实例 + `core coreAPI = liveCore`） | `mobile/bind/bind.go:34-43` |
| 会话导出面 | `SessionCookie(machine string) (string, error)`、`SwitchMachine(machine string) (string, error)`（string-only） | `mobile/bind/session.go:24`、`:37` |
| 会话消费面（测试缝） | `sessionAPI`：`SessionCookie`/`SwitchMachine`；`swapSessions` 替换 | `mobile/bind/session.go:11-16`、`mobile/bind/fakes_test.go:109-113` |
| B392 前占位 | `errSessionsNotWired` + `notWiredSessions{}` + `var sessions sessionAPI = notWiredSessions{}`——**本卡删除** | 旧 `mobile/bind/session.go:20-30` |
| 冻结导出面 | 七函数逐字钉住（`wantBindSurface`） | `mobile/bind/export_surface_test.go:21-29` |
| 生产文件 import 边界 | 只许标准库 + `internal/mobilecore`；禁 `relay`/`client`/`proto` | `mobile/bind/export_surface_test.go:90-94` |

### 1.2 核侧会话能力（`internal/mobilecore`，S2 已落地）

| 接缝 | 现状精确签名 | 现状出处 |
| --- | --- | --- |
| 切机清槽/重兑换 | `func (c *Core) Activate(ctx context.Context, machine string) (SessionCookie, error)`——`exchMu` 串行化领票+兑换，清罐在兑换前显式执行；同机命中缓存 | `internal/mobilecore/core.go:219`、`:236-263` |
| 当前会话 | `func (c *Core) Session() (SessionCookie, error)`——无活动会话/失效返回错误 | `internal/mobilecore/core.go:266` |
| 活动机 | `func (c *Core) ActiveMachine() string`——无活动会话时空串 | `internal/mobilecore/core.go:279` |
| 回环源 | `func (c *Core) Origin(machine string) (string, error)`——未配对/离线返回错误 | `internal/mobilecore/core.go:333` |
| 会话 DTO | `type SessionCookie struct{Name,Value,Path string; HttpOnly,Secure bool; SameSite string; MaxAge int}`（gomobile 可绑形状） | `internal/mobilecore/session.go:42-50` |
| 兑换失败闭合 | 代领失败/地址不可解析/非 302/无 `handoff_session`/cookie 值为空**均返回错误**，绝不空值冒充 | `internal/mobilecore/session.go:69-119` |
| 兑换超时 | `exchangeTimeout = 5 * time.Second`（Core 自带，绑定层无需再设） | `internal/mobilecore/session.go:35`、`:81-82` |
| 已选路客户端 | `Client.IssueAuthTicket(ctx, deviceName)` → `POST /api/auth/tickets` | `internal/client/client.go#Client.IssueAuthTicket` |

### 1.3 平台 cookie 属性对侧

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| cookie 名 | `sessionCookieName = "handoff_session"` | `internal/agentd/auth.go:28-29` |
| cookie 构造 | `sessionCookie(r,value,maxAge)`：`Path="/"`、`HttpOnly=true`、`SameSite=Lax`、`Secure=r.TLS!=nil` | `internal/agentd/authroutes.go#sessionCookie`（`:343`） |
| 移动核镜像 | 移动核不 import agentd，cookie 名以本包常量镜像并由源对齐断言锁定 | `internal/mobilecore/session.go:26-30` |

---

## 2. 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 真正生产者 | 真正消费者 | 结论 |
| --- | --- | --- | --- |
| `handoff_session` | `handleConsole` 经 `sessionCookie` 发 `Set-Cookie`（`authroutes.go:343`） | 浏览器 / webview cookie jar；`sessionFromRequest` 读回（`auth.go:83`）；移动核在 `exchangeTicket` 按名解析 | 活跃；B392 不重定义，只透传 Core 的兑换结果 |
| `Path=/` / `HttpOnly` / `SameSite=Lax` / `Secure=r.TLS!=nil` | 同上，唯一在 `sessionCookie` 定义 | webview `WKHTTPCookieStore` / `CookieManager`（壳侧按 §4.4 写） | 活跃；明文 loopback 下 `Secure=false`，B392 冻结为壳侧写入属性 |
| `SessionCookie.Value` | `exchangeTicket`（`session.go:103-116`，非空才成功） | 绑定 `SessionCookie` 导出面 → 壳写 jar | 活跃；B392 只返回 `Value` |
| `errSessionsNotWired` | 旧占位（`notWiredSessions`） | 无——仅占位自产自消 | **已死常量，本卡删除**；不作文档事实源 |

本表无零使用死常量被当事实源；`errSessionsNotWired` 标注为已删占位，不作为契约依据。

---

## 3. 图覆盖债

- `mobile/` 是独立嵌套 module，扫描配方显式排除 `mobile` 前缀（`scripts/codegraph-rescan/main.go:231`），`mobile/bind` 全部符号**图外**。本轮亲跑 `codegraph sym coreSessions`、`sym newCoreSessions` 均「不在图中」（退出 1）——符合预期，非覆盖债。
- `internal/mobilecore` 已在扫描映射（`scripts/codegraph-rescan/main.go:2140`，`internal/mobilecore → d_transport_channel`），但其 S2 新符号未进旧 baseline（`codegraph sym Core.Activate`/`Core.Pair` 未命中，见 spec 台账）；本卡**不**偷带全图重扫，沿用 `docs/roadmap.md` 的图债卡处理。
- 本卡未新增根模块符号，故**无新视图 diff**（分支未引入新符号则合法无视图，不造空文件）。

---

## 4. 契约增量：精确签名与组装不变量

本卡不新增或修改 wire / gomobile 契约；只兑现既有契约的组合与行为。以下签名是 `mobile/bind` 图内私有面，是 B392 的接缝契约（spec §7）。

### 4.1 唯一生产组装点（`mobile/bind/bind.go`）

```go
var (
    log      = slog.Default()
    liveCore = mobilecore.New(nil, log) // 唯一真实 *mobilecore.Core
    core     coreAPI = liveCore          // 配对面视图
)
```

```go
// mobile/bind/session.go
var sessions sessionAPI = newCoreSessions(liveCore) // 会话视图，同一实例
```

- 配对面、会话面与 `Close` 必须观察同一机器表、同一活动会话槽与同一生命周期（spec §4.1）。
- 删除旧占位 `errSessionsNotWired` / `notWiredSessions{}`；`sessionAPI` 只保留为测试缝（`swapSessions`）。
- 禁止为会话另起第二个 `mobilecore.New`。

### 4.2 会话适配器（`mobile/bind/adapter.go`，本分支新建）

```go
// sessionCoreAPI 是会话适配器消费的核面（窄消费，duck typing）；*mobilecore.Core 满足。
type sessionCoreAPI interface {
    Activate(ctx context.Context, machine string) (mobilecore.SessionCookie, error)
    Session() (mobilecore.SessionCookie, error)
    ActiveMachine() string
    Origin(machine string) (string, error)
}

type coreSessions struct {
    mu   sync.Mutex
    core sessionCoreAPI
}

func newCoreSessions(c sessionCoreAPI) *coreSessions

func (a *coreSessions) SessionCookie(machine string) (string, error)
func (a *coreSessions) SwitchMachine(machine string) (string, error)
```

**`SwitchMachine(machine)` 可观察顺序（spec §4.2）**：加锁 → `core.Activate(ctx, machine)` → 校验兑换 `Value != ""` → `core.Origin(machine)` → 只有两步都成功才返回 origin；任一步失败返回 `("", err)`，壳不得导航。`Activate` 成功而 `Origin` 失败时**不伪造回滚**（Core 可能已持目标会话），壳也不得导航。

**`SessionCookie(machine)` 可观察语义（spec §4.3）**：加锁（与 `SwitchMachine` 同一把）→ 校验 `core.ActiveMachine() == machine`，不等即 error → `core.Session()` → 校验 `Value != ""` → 只返回 cookie value；string-only 导出面不变，不返回 token / origin / 完整 DTO。

**`ctx` 来源（spec §4.2 明示由 contract 选择）**：`context.Background()`。绑定层不引入取消/超时策略；单次兑换由 Core 内 `exchangeTimeout=5s` 兜底（`internal/mobilecore/session.go:35`）。此选择不影响导出签名。

### 4.3 壳侧完整消费顺序与 cookie 属性（spec §4.4，交接给 Android/iOS 壳）

1. `SwitchMachine(target)`；
2. 仅在成功返回后，按 `name=handoff_session + host + Path=/` 清理该 loopback host 的旧 cookie（cookie 不按端口隔离，须覆盖该 host 所有端口）；
3. `SessionCookie(target)`；
4. 把返回值写成 host-only `handoff_session`：`Path=/`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`，不设持久 `Max-Age/Expires`（进程内会话 cookie；服务端到期仍是最终权威）；
5. 导航到 `SwitchMachine` 返回的 origin。

任一步失败都不得继续导航；清 jar / 注入失败由壳呈现可行动错误，Go 绑定层不伪装能操作平台 cookie store。

### 4.4 图面（target.json / best.json / 视图）

- 依赖方向：无新增（`mobile/bind` 图外；`internal/mobilecore` 已在 `d_transport`，其 `→ d_protocol` 入口由 B369 声明）。
- 组装点：`mobile/bind/bind.go` 图外（嵌套 module），不触发 `graph check`。
- 预算：零变化。
- **本轮 `target.json` / `best.json` 均不改，无视图 diff**（无根模块新符号）。

---

## 5. 原子冻结清单

每条是独立可判 pass/fail 的接缝断言（可单独打红、失败可定位）。`[T0]` = 本轮 Ticket 0 已落、由 `mobile/bind/adapter_test.go` 锁住；其余为 implement 对账条目。

### 5.1 组装不变量

1. `[T0]` 生产默认装配下，配对面（`core`）与会话面（`sessions`）指向同一 `*mobilecore.Core` 实例（`TestDefaultRuntimeSharesOneCore`）。
2. `[T0]` 生产默认装配不包含 `notWiredSessions` / `errSessionsNotWired`：`sessions` 必须由 `newCoreSessions(liveCore)` 构造，且 `TestDefaultRuntimeSharesOneCore` 锁定类型与同一实例；生产不依赖源码文本零命中。
3. `[T0]` 导入面不变：七函数签名逐字等于 `wantBindSurface`；无 Token/Dial 导出（`TestBindExportedSurfaceIsFrozen` 继续绿）。

### 5.2 SwitchMachine

4. `[T0]` 调用顺序为 `Activate` 先于 `Origin`（`TestCoreSessionsSwitchMachineSequence`）。
5. `[T0]` `Activate` 返回 error → 返回 `("", err)` 且**不调用** `Origin`（`TestCoreSessionsSwitchMachineFailsClosed/activate_失败`）。
6. `[T0]` `Activate` 返回空 `Value` → 返回 `("", err)` 且不调用 `Origin`（`.../兑换空_cookie`）。
7. `[T0]` `Origin` 返回 error → 返回 `("", err)`（`.../origin_失败`）。
8. `[T0]` 两步都成功 → 返回核侧 `Origin` 的字符串，与核侧一致（`TestCoreSessionsSwitchMachineSequence`）。

### 5.3 SessionCookie

9. `[T0]` `ActiveMachine() != machine` → 返回 `("", err)`，绝不返回当前另一台机器的 cookie（`TestCoreSessionsSessionCookieRejectsWrongMachine`）。
10. `[T0]` `ActiveMachine() == machine` 且会话有效 → 逐字返回 `Session().Value`。
11. `[T0]` `Session()` 返回空 `Value` → 返回 `("", err)`。
12. `[T0]` `Session()` 返回 error → 原样上抛。
13. `[T0]` 无活动会话（`ActiveMachine()==""`）时任意请求返回 error。

### 5.4 互斥与失败闭合

14. `[T0]` `SwitchMachine` 与 `SessionCookie` 共用一把锁：「校验活动机 + 读 cookie」与「切机」互不穿插；`Session()` 阻塞期间用同包 `TryLock` 确定性握手断言适配器锁仍被持有（去掉锁则 `TryLock` 成功、测试打红），并保留 A/B 断言——读 A 期间切 B 不得返回 B 的 cookie（`TestCoreSessionsLockSerializesSwitchAndRead`，变异证据 §7.3）。
15. `[T0]` 所有失败路径返回 `("", err)`，无 `("", nil)`、无 token/origin 冒充 cookie（`TestCoreSessions*FailsClosed`、`...RejectsWrongMachine`）。

### 5.5 核侧与协议零重实现

16. 适配器只消费既有 `Core.Activate` / `Core.Session` / `Core.ActiveMachine` / `Core.Origin`，不改其签名（`go build` 编译期锁定）。
17. `mobile/bind` 生产文件不 import `internal/relay` / `internal/client` / `internal/proto`（`TestBindProductionHasNoProtocolLogic` 继续绿）。

### 5.6 图与模块

18. `mobile/` 仍为图外嵌套 module；`codegraph check` 基线 fails=0（无本卡新增违规）。
19. `target.json` / `best.json` 无本卡变更（无新增依赖方向、组装点、预算）。

---

## 6. 拍板记录（三重闸门）

只记同时满足「难逆转 × 无上下文会惊讶 × 真取舍」的决定。

**命中一条：**

- **会话一致性锁放在绑定适配器层，覆盖「校验活动机 + 读 cookie」整段（含 Core 的网络兑换），而不是依赖 Core 内部 `exchMu`/`mu` 的两次独立调用**。难逆转——现有 Core 没有「按机器原子校验并取会话」的方法；若撤掉适配器锁改用 Core，就得给 `internal/mobilecore` 新增跨绑定的原子方法（跨子系统：mobilecore + bind + 壳消费语义），回头要重审 Core 并发契约与 B369 已冻的 Core 导出面。无上下文会惊讶——「Core 已经有 `exchMu`/`mu`，为什么绑定层还要再锁一把」是最自然的直觉；真取舍——被否方案是「把原子性下沉进 Core（新增 `SessionFor(machine)`）」，代价是扩大 Core 公共面、把壳的调用顺序语义塞回提供方（spec §4.5 已弃选同族方案）。立。

**不立（探索性/确定性设计，逐条记判据，防「空着与没审过不可区分」）：**

- **`SwitchMachine` 用 `context.Background()`**：单包内参数默认值，反转（换壳注入 ctx 或加超时）不改任何测试判据，也不触达契约对侧；Core 自带 5s 兑换超时兜底。不立（但作为 §4.2 冻结语义写明，并列入欠账 §9.6 的后续卡候选）。
- **失败路径不伪造回滚**：由 spec §4.2 直接推出，被否方案（回滚/补偿）spec §4.5 已明确弃选，本节点只兑现不重裁。不立。
- **适配器错误文案**：对契约对侧不可见的实现选择。不立。
- **`sessionCoreAPI` 窄消费面拆分**：层内可测性选择。不立。

**无其它命中。**

---

## 7. Ticket 0、可执行冻结与本轮实际跑过的命令

### 7.1 Ticket 0 已落（本提交）

- `mobile/bind/adapter.go`（新）：`sessionCoreAPI` 窄消费面 + `coreSessions` 适配器（§4.2 精确语义）。
- `mobile/bind/bind.go`：新增 `liveCore` 单实例，`core coreAPI = liveCore`。
- `mobile/bind/session.go`：删除 `errSessionsNotWired` / `notWiredSessions`；`sessions` 接到 `newCoreSessions(liveCore)`。
- `mobile/bind/adapter_test.go`（新）：§5 全部 `[T0]` 断言的能变红测试（顺序/失败闭合/错机/并发锁/身份守卫）。
- 无新符号入图（`mobile/` 图外，§3）。

### 7.2 可执行冻结

本卡无哈希 / 密钥派生 / 编码格式的**新**金样本需求（ticket→cookie 兑换的明文协议与 cookie 属性语义由 B369 既有断言与 `internal/mobilecore` 测试背书）。**无命中。**

### 7.3 变异证明（可红）

本轮亲自执行并保存原始输出，改动后立即还原：

| 变异 | 命令 | 原始结果（第 2 轮重跑） |
| --- | --- | --- |
| 保留 `return "", err` 语义、把 `Activate` 失败路径改 `return "", nil` | `go test ./bind/ -count=1 -run TestCoreSessionsSwitchMachineFailsClosed` | FAIL：`adapter_test.go:119: Activate 失败必须返回 ("", err): origin="" err=<nil>` |
| 去掉 `ActiveMachine() == machine` 检查（改 `if false && …`） | `go test ./bind/ -count=1 -run TestCoreSessionsSessionCookieRejectsWrongMachine` | FAIL：`adapter_test.go:157: 错机必须返回 ("", err): value="sess-B" err=<nil>` |
| 去掉适配器 `mu.Lock()/Unlock()`（两处） | `go test ./bind/ -count=1 -run TestCoreSessionsLockSerializesSwitchAndRead` | FAIL：`adapter_test.go:202: Session 阻塞期间适配器锁未被持有：切机与读会互相穿插`（`TryLock` 握手，非定时器猜锁） |

三条变异均在 **test double** 层面确定性打红；生产恢复 `notWiredSessions` 的守卫与真实 Core 变体归 implement（§9.1/§9.2）。变异后已还原，工作树 `go test ./...` 全绿。

### 7.4 本轮实际跑过的命令

| 命令 | 退出码/结果 |
| --- | --- |
| `go version` | `go1.26.1 linux/amd64` |
| `go build ./...`（根模块） | 0 |
| `go build ./...`（`mobile/`） | 0 |
| `go test ./... -count=1`（`mobile/`，含 `mobile` + `mobile/bind`） | 全 `ok` |
| `go test ./bind/ -race -count=1`（`mobile/`） | `ok` |
| `go vet ./...`（`mobile/`） | 0（无输出） |
| `gofmt -l .`（`mobile/`） | 无输出 |
| `go list ./... \| grep -c handoff/mobile`（根模块） | `0`（根模块不依赖 `mobile`） |
| `go list -deps ./... \| grep -c handoff/mobile`（根模块） | `0` |
| 三组变异测试（§7.3） | 各自 FAIL，还原后 `go test ./...` 全 `ok` |
| `codegraph check` | `fails=[]`，退出 0（warns 为基线既存） |
| `codegraph resolve --doc docs/superpowers/specs/b392-contract.md` | 退出 0：两锚 `n_client_Client_IssueAuthTicket`、`n_agentd_sessionCookie` 均 `ok` |
| `codegraph validate` | 退出 1：**2 个既存问题**（`cards-B272-charter` 的 `k_dropdir_fn` 引用不存在领域；`cards-B374-charter` 的 `k_collab_model` 重复）——均非本卡视图（本卡无视图） |
| `codegraph sym coreSessions` / `sym newCoreSessions` | 「不在图中」（预期：`mobile` 图外） |

---

## 8. 本节点欠账（实现节点逐条补齐，不得静默带走）

1. **真实 Core 竖切（spec §8.2）**：`mobile/bind` 自建 `mobilecore.New` + 可控 `DialFunc` + 本地 HTTP 夹具（不复用 `mobilecore` 同包未导出 helper），夹具按机器/ticket 签发可区分的 `Set-Cookie`，覆盖：配对 A/B、`SwitchMachine(A)` 返回 A origin、`SessionCookie(A)` 逐字等于 A 的 `Set-Cookie` value、切 B 后 `SessionCookie(A)` 失败/`SessionCookie(B)` 对得上、切回 A 重新兑换、同机缓存、未配对/离线/无 cookie/关闭全 error、导出 `Close` 后两方法失败、`-race` 并发不串机。
2. **非 fake 生产守卫与生产变异（spec §8.3）**：一条不调用 `swapCore`/`swapSessions`、从默认生产组装直走 `Pair → SwitchMachine → SessionCookie` 的真实 Core 测试；并实测「恢复 `notWiredSessions` → 守卫红」「去掉 `ActiveMachine==machine` → 串机红」「失败路径改 `return "",nil` → 红」「去互斥 → 红」四变异，保存原始红输出后还原。本轮已在 double 层证明后三条可红（§7.3）；生产守卫的 real-Core 版本仍欠。
3. **gobind 真产物验收（spec §9.5）**：在装了钉版 gobind 的机器/CI 跑 `go test ./bind/ -run TestGomobileSurfaceHasNoSkips`，确认产物无 skipped function/field；本 linux 工作树无工具链，**未验**（不得以退出 0 冒充）。
4. **`mobile/README.md` 补齐 §4.4 调用顺序与职责边界（spec §9.8）**：明写「切机成功 → 清旧 jar → 取 cookie → 注入 → 导航」及任一步失败不得导航。
5. **真机证据（spec §9.9）**：移动 CI/真机可用时重建 Android AAR 或 iOS XCFramework，验证切机后 API/WebSocket 带新 cookie、旧机 cookie 不再发送、无 cookie 仍 401；不可用须由用户明确接受「未验」逐项落账。
6. **`context.Background()` 无取消（§4.2/§6）**：若壳需要取消或自定义超时，另开卡。本期接受 Core 内 5s 兑换超时。

> 图债与后续图登记**不属本节点欠账**：`mobile/bind` 是图外组装点（嵌套 module），导出面变更不触发 `graph check`，也不在 implement 补齐；登记由 `docs/roadmap.md`「mobilecore 图覆盖债」（§3 同源）跟踪，本卡不偷带。

---

## 9. 移交 plan 附区

（本区由 plan 出稿时吸收，吸收后在区头标注「已由 plan〈文档〉吸收（日期）」销区。不占冻结条目名额。）

1. **测试替身形态**：`adapter_test.go` 的 `sessionCoreDouble`（`sessionCoreAPI` 可控替身，含 `sessionEntered`/`sessionGate` 阻塞通道）配合同包 `TryLock` 握手，已能确定性复现「无锁串机」；implement 的真实 Core 夹具可沿用其交错控制思路，但不得以它替代真实 Core 验收集合。
2. **夹具签发**：本地 HTTP 夹具按目标机器/ticket 签发不同 `handoff_session` value，使 A/B 差异来自真实 `Set-Cookie` 响应；不得预置与响应无关的假值。
3. **取消策略（已由协调者拍板吸收）**：保留 `context.Background()`；Core 内 `exchangeTimeout=5s` 兜底单次兑换。本卡不注入 `func() context.Context`，若壳未来需要取消/自定义超时，另开卡并重新走契约。
4. **README 段落落点**：`mobile/README.md` 现无「调用顺序」小节，建议置于「绑定面（壳的契约面）」之后。

---

## 10. 本轮法定核对

- 契约增量文档：本文件；每个冻结签名均有现状代码出处（§1）。
- 上游状态位：spec 头部「已批准」已复核一致，无需回写。
- 目标图：`mobile/` 图外、本卡无根模块新符号 → `target.json` / `best.json` 无变更、无视图 diff（§3/§4.4）；`codegraph check` 基线 `fails=0`。
- Ticket 0 编译：本轮根 `go build ./...` 退出 0；`mobile/` `go build ./...` 退出 0；`mobile/` `go test ./... -count=1`、`go test ./bind/ -race -count=1`、`go vet ./...`、`gofmt -l .` 全绿（§7.4）。
- 可执行冻结：无哈希/密钥派生/编码新命中（§7.2）。
- 变异：三条（失败路径 `return "",nil`、去活动机检查、去互斥）本轮实测打红并还原；互斥条改由同包 `TryLock` 握手确定性打红，无 100ms 定时猜锁（§7.3）。
- 基线：spec 父基线 / 第 1 轮 contract 实际基线 / 第 2 轮续接点在头部逐条区分（原「有效基线」单行已拆）。
- 三重闸门：§6 记命中一条 + 不立四条判据，非空着。
- 欠账：§8 逐条列明，无静默带账；图债/后续图登记不占欠账，只留 `docs/roadmap.md` 指针。

## 11. 协调者修订记录

- **2026-09-24 / breakdown P2**：拍板保留 `context.Background()`，吸收 §9.3 的原二选一表述；Core 的 5 秒兑换超时仍是本卡边界。
- **2026-09-24 / breakdown P4**：将冻结项 2 从“源码零命中”改为“生产默认装配不包含占位”，由 `TestDefaultRuntimeSharesOneCore` 的类型/同一实例断言锁住；测试文件允许为真实 Core 夹具 import `internal/client` / `internal/proto`，生产文件仍禁止这些协议实现依赖。
- **2026-09-24 / breakdown P3/P5**：真实行为竖切使用独立真实 Core 与本地夹具；默认生产身份守卫直接检查 `liveCore`/`sessions`；并发同时要求 `-race` 与 `TryLock` 确定性握手。以上均不新增跨语言接缝。
- **2026-09-24 / plan review 对齐**：§8.2-2 的默认生产竖切是必做承重项，必须在不调用 `swapCore`/`swapSessions` 的隔离子进程中直接使用 `liveCore`；独立 Core + swap 仅作补充行为覆盖，不能替代默认守卫。去互斥的确定性红证据以同包 `TryLock` 为准，真实 gate/`-race` 为补充闸门。
