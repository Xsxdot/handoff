# B417 契约增量：Android/iOS 原生移动壳（WebView + 配对 + 安全存储）

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b417.md`，头部「**状态：已批准（2026-09-28）**」——
本轮复核一致，无需回写）
**级别：L3 重档**（contract → breakdown → 拆 Android/iOS 两子卡 → 父卡 integrate）
**冻结状态：本提交随本文件、`docs/superpowers/ledgers/2026-09-28-b417-contract-ledger.md`、
`mobile/bind/shell_api_golden_test.go` 冻结。壳工程是图外子系统，`codegraph/target.json`、
`best.json`、分支视图 diff 本轮零变化（§5、§7）。**
**有效基线：** `cards/B417-spec` @ `a9ba59b5`（= 本工作树 HEAD；依赖 B392 已在基线 `10ccde73`）
**架构形态：** 两端**薄壳**——单入口 UI（Android 单 Activity / iOS 单 `UIViewController`）+ 三条窄缝服务
（`CookieBridge` / `WebviewSessionBinder` / `pair`），壳内**无横向分层框架**；Go 核为 App 级单例，
壳不复制协议状态。壳工程与 `mobile/`、`desktop/` 同例，位于 **Go 代码图外**（§2）。
**交棒：** breakdown。

本文档把已批准 spec 的壳↔核接缝与共享壳不变式（I1–I7）翻译成现状代码可接的精确签名、调用序、
常量与构建接线。B417 **零修改**绑定面（spec：「B417 不新增、不修改任何绑定面符号」）——本节点冻结的是
**壳消费语义**与**壳编译期 API 形状**。

---

## 0. 工具链 gate 回执（spec 前置）

上游 spec 台账 `docs/superpowers/ledgers/2026-09-28-b417-spec-ledger.md` 记 darwin 机（Xcode 27 /
NDK 30.0.16248370 / JDK 21）**双端 PASS**：`./mobile/build.sh ios` → `handoff-mobile.xcframework`
（device arm64 + simulator arm64/x86_64），`android` → `handoff-mobile.aar`（37178071 字节，四 ABI）。

**本工作树是 linux、无 JDK/Xcode/Android SDK**（台账 §1 原始读数），不重跑该 gate；以 darwin 台账为据。
但「壳编译期看到的 API 形状」由钉版 gobind 在**任何机器**可生成——本节点已生成并冻结（§3.1、§7）。

---

## 1. 现状查证

### 1.1 已查证签名与代码事实

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| 绑定面导出函数（7 个） | `Pair(bundleJSON string) error`、`MachineCount() int`、`MachineAt(index int) *Machine`、`Origin(machine string) (string, error)`、`SessionCookie(machine string) (string, error)`、`SwitchMachine(machine string) (string, error)`、`Close() error` | `mobile/bind/bind.go#Pair`（`:47`）、`#MachineCount`（`:59`）、`#MachineAt`（`:70`）、`#Origin`（`:85`）、`#Close`（`:95`）；`mobile/bind/session.go#SessionCookie`（`:24`）、`#SwitchMachine`（`:37`）；逐字冻结于 `mobile/bind/export_surface_test.go#wantBindSurface`（`:21-29`） |
| Machine DTO | `Machine{Name, Origin string; Online bool}`，本包定义（gomobile 只放行绑定集合内命名类型） | `mobile/bind/types.go#Machine`（`:18-22`） |
| Online 语义 | `MachineAt` 的 `Online` 取 `core.Origin(name)` 是否成功（真值非猜）；离线机 `Online=false` 且 `Origin=""`；越界/负索引返回 `nil` | `mobile/bind/bind.go#MachineAt`（`:70-81`） |
| SessionCookie 只导出 value | 适配器先校验活动机，再取核内会话，**只返回 `ck.Value`**（string-only），空值返回 error；**绝不返回 token/origin/DTO** | `mobile/bind/adapter.go`（`:39-53`，`return ck.Value` 在 `:52`） |
| SwitchMachine 只返回 origin | `Activate`（切机清旧槽并重兑换）→ 校验 cookie 非空 → `Origin`；任一步失败返回 `("", err)`，壳不得导航 | `mobile/bind/adapter.go`（`:58-73`）；`internal/mobilecore/core.go`（`Core.Activate` `:208`） |
| 回环源形状 | `startLoopback` 用 `net.Listen("tcp","127.0.0.1:0")`，返回 `"http://"+ln.Addr().String()` = `http://127.0.0.1:<port>`（**无尾斜杠**；每机独立端口） | `internal/mobilecore/core.go`（`Core.startLoopback` `:393-401`） |
| Origin fail-closed | 未配对 / 离线 / 核已关 → `("", err)`；**不返回空串冒充成功** | `internal/mobilecore/core.go`（`Core.Origin` `:333-356`） |
| cookie 名 | `sessionCookieName = "handoff_session"`（agentd 侧） | `internal/agentd/auth.go:29`；核侧镜像 `internal/mobilecore/session.go:30` |
| cookie 属性（服务端签发面） | `Name=sessionCookieName`、`Path="/"`、`HttpOnly=true`、`SameSite=Lax`、`Secure=r.TLS!=nil`（明文回环下 false）、`MaxAge=会话寿命` | `internal/agentd/authroutes.go#sessionCookie`（`:343-354`）、`#Server.handleConsole`（`:167`，`:211` `SetCookie`） |
| 会话寿命 | `sessionLifetime = 30 * 24 * time.Hour`；`handleConsole` 按其剩余秒数设 `MaxAge` | `internal/agentd/auth.go:32`、`internal/agentd/authroutes.go:211` |
| 核内兑换 | `exchangeTicket`：token 代领 ticket → 经回环反代请求 `/console`（**不跟随 302**）→ 取 `Set-Cookie` 的 `handoff_session`；空值/非 302/无 cookie 均 error | `internal/mobilecore/session.go#exchangeTicket`（`:76`）、`#toSessionCookie`（`:128`）、`SessionCookie` DTO（`:42-51`） |
| 清罐与单槽语义 | 核内单槽罐 + 每机独立端口；切机清旧槽（`cookie 不按端口隔离，RFC 6265`） | `internal/mobilecore/core.go`（`Core.Activate` `:208`、`cookieValidLocked` `:286`） |
| 壳侧会话调用序（交接文档） | `SwitchMachine(target)` → 清 host 旧 cookie（`name=handoff_session + host + Path=/`，不区分端口）→ `SessionCookie(target)` → 写回 jar → `load(origin)`；任一步失败不导航 | `mobile/README.md:40-50`，由 `mobile/bind/readme_test.go#TestReadmeDocumentsShellCallOrder`（`:15`）逐字锁定 |
| 配对载体输出 | `handoff console --qr`（终端二维码）/`--bundle`（**一行 JSON**）：读本机 targets → 逐机代领 ticket → `proto.EncodePairBundle` | `cmd/console.go#runConsolePair`（`:110`）、`#renderBundleQR`（`:279`）、flags `consoleQR/consoleBundle`（`:47-48`） |
| 配对 wire 版本 | `proto.PairVersion=1`，`EncodePairBundle` 产紧凑 JSON；壳不解析 | `internal/proto/pairing.go#EncodePairBundle`（B369 冻结，contract §3.1） |
| 核心构建 | `./mobile/build.sh android\|ios` 产 `dist/mobile/handoff-mobile.{aar,xcframework}`；Android 显式 `-androidapi 21`；`dist/` 已被 `.gitignore` 的 `/dist/` 覆盖 | `mobile/build.sh`（`:62`、`:68`）、`.gitignore` `/dist/` |
| 嵌套模块隔离 | `mobile/` 独立 module（`replace ../`），根模块 `go list ./...` 不含它；扫描配方排除 `mobile` 前缀 | `mobile/go.mod`、`mobile/moduleisolation_test.go`、`scripts/codegraph-rescan/main.go:231` |

### 1.2 依赖库既成行为（钉版 gobind 生成面 + 平台 API）

**钉版**：`golang.org/x/mobile@v0.0.0-20260908204917-8b95e45f8d3e`（`mobile/build.sh:21`、`mobile/go.mod:38` 的
`tool` 指令、`mobile/bind/gobind_surface_test.go:18` 三处一致）。生成面即壳的**编译期契约**；

- **Java/Kotlin 面**：Go 包名 `bind` → Java 包 `bind`（前缀空时取包名，`bind/genjava.go:1002-1013`）；
  `package` 级函数 → `abstract class Bind` 的 **static native** 方法，名 = `lowerFirst(GoName)`；
  有 `error` 返回的函数带 **`throws Exception`**（`bind/genjava.go:808-855`）；结构体 → `final class` +
  字段访问器 `get%s`（`bind/genjava.go:317`）。
- **ObjC/Swift 面**：ObjC 前缀 = `strings.Title(pkg.Name())` = **`Bind`**（`bind/genobjc.go:101-106`；
  `gomobile bind` 未传 `-prefix`，`cmd/gomobile/bind.go:155-166`）；package 级函数 → **C 函数**
  `FOUNDATION_EXPORT <ret> Bind<Name>(...)`，`error` → `NSError**` 末参（`bind/genobjc.go:540-566`、
  `:634-642`）；结构体 → `@interface BindMachine` + `@property (nonatomic)` 字段（`bind/genobjc.go:1038-1120`）。
- **形状铁律**：x/mobile 的 `bind/gen.go` 中 `isSupported`（`:480`，slice 只放行 `[]byte`，`:499`）对不合规签名**静默跳过**
  整个函数——故列表用「计数 + 按索引取指针」，DTO 字节段只用 `string/bool`；由
  `mobile/bind/gobind_surface_test.go#TestGomobileSurfaceHasNoSkips`（`:35`）执法。

**本轮亲自生成的精确签名**（`go tool gobind`，原始输出见台账 §2；由
`mobile/bind/shell_api_golden_test.go` 冻结）：

```
# ObjC 头（Swift 消费）
FOUNDATION_EXPORT BOOL        BindPair(NSString* _Nullable bundleJSON, NSError* _Nullable* _Nullable error);
FOUNDATION_EXPORT long        BindMachineCount(void);
FOUNDATION_EXPORT BindMachine* _Nullable BindMachineAt(long index);
FOUNDATION_EXPORT NSString* _Nonnull BindOrigin(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);
FOUNDATION_EXPORT NSString* _Nonnull BindSessionCookie(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);
FOUNDATION_EXPORT NSString* _Nonnull BindSwitchMachine(NSString* _Nullable machine, NSError* _Nullable* _Nullable error);
FOUNDATION_EXPORT BOOL        BindClose(NSError* _Nullable* _Nullable error);
@interface BindMachine : NSObject  // properties: name, origin, online

# Java/Kotlin 面
public static native void   pair(String bundleJSON) throws Exception;
public static native long   machineCount();
public static native Machine machineAt(long index);
public static native String origin(String machine) throws Exception;
public static native String sessionCookie(String machine) throws Exception;
public static native String switchMachine(String machine) throws Exception;
public static native void   close() throws Exception;
// bind.Machine: getName()/getOrigin()/getOnline()
```

**平台 API（本机无 SDK 源码，未核，列欠账 §8.3/§8.4）**：iOS `WKHTTPCookieStore.setCookie/delete`、
`HTTPCookie(properties:)`、Keychain `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`；Android
`CookieManager.setCookie/removeAllCookies/flush`、`androidx.security.crypto.EncryptedSharedPreferences`。
本文档只冻结它们必须达成的**可观测语义**（§4），具体 API 调用形状归子卡在 macOS 上核对。

### 1.3 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 真正生产者 | 真正消费者 | 结论 |
| --- | --- | --- | --- |
| `handoff_session` | agentd `sessionCookie`（`authroutes.go:343`） | 浏览器 / **webview cookie jar**（本轮新增消费方）；`sessionFromRequest` 读回 | 活跃；壳注入的 cookie 名必须与之一致（§4.5 条 23） |
| `Path="/"`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`（loopback） | 同上（`authroutes.go:343-354`） | webview cookie jar | 活跃；壳按 §3.4 硬编码（**HttpOnly 除外**，客户端注入不可置，见 §11 修订 1） |
| `sessionLifetime=30d` | `auth.go:32` | `handleConsole` 设 `MaxAge`；核 `expiryFrom` 换算 | 活跃；壳注入**进程内会话 cookie**（不带 `Max-Age`），服务端到期仍是权威 |
| 回环源 `http://127.0.0.1:<port>` | `Core.startLoopback`（`core.go:393`） | 壳 `load(origin)` | 活跃；壳**只加载绑定面返回值** |
| `proto.PairVersion=1` 与 bundle JSON | `console --qr/--bundle`（`cmd/console.go`） | 壳扫码/粘贴 → `Pair()` | 活跃；壳整体透传，不解析 |
| **零使用死常量** | — | — | 本轮未发现被当作事实源死常量（`--qr` 已实现于 `cmd/console.go`，非 B369 时的「已设计未实现」） |

### 1.4 图覆盖债

- `mobile/` 全部符号**图外**（扫描排除 `mobile` 前缀，`scripts/codegraph-rescan/main.go:231`）；本节点
  文档对其用 `file#Symbol` 锚，`codegraph resolve` 接受为 `moved`（不等于落图）。
- `internal/mobilecore` 属 `d_transport_channel`，但其符号只在视图 `cards-B369-charter.json`，baseline 未吸收；
  **B392 新增的 `Core.Activate`/`Core.Session`/`Core.ActiveMachine`、`mobilecore.SessionCookie` 从未入图**
  （本轮 `codegraph sym Core.Activate` 未命中）。属既存图覆盖债，本卡不新增 Go 符号，不承担补图。

---

## 2. 架构户口与依赖方向

- **壳子系统不在 Go 图内**。Android（Kotlin/Gradle）与 iOS（Swift/Xcode）是两条独立工具链代码库，落
  `mobile/android/`、`mobile/ios/`；与 `desktop/`、`mobile/` 同例（嵌套 module/图外）。
  `codegraph` 是 **Go 包图**，扫描配方已排除 `mobile` 前缀——不把非 Go 工程塞进领域归属链。
- **依赖方向**：壳 → `mobile/bind`（AAR/XCFramework）。方向单向；壳不向 Go 侧注入任何符号。
  绑定面本身的 `d_transport → d_protocol` 等契约由 B369 冻结，本卡**不改**。
- **组装点**：壳的组装点是进机器流程的入口（Android ViewModel / iOS 机器列表屏）——**图外**，不触发
  `graph check`。
- **预算**：零变化。

---

## 3. 契约增量：精确签名

### 3.1 消费的壳↔核绑定面（已冻结，B417 零修改）

壳**只**经下列 7 个 gomobile 函数消费核（生成签名见 §1.2）：

```go
// mobile/bind —— B417 只消费，不新增/不修改任何符号
func Pair(bundleJSON string) error                       // 整份 bundle JSON 透传
func MachineCount() int
func MachineAt(index int) *Machine                       // 越界/负索引 → nil
func Origin(machine string) (string, error)              // 未配对/离线 → ("", err)
func SessionCookie(machine string) (string, error)       // 只返回 cookie 的 value
func SwitchMachine(machine string) (string, error)       // 返回目标回环源
func Close() error
type Machine struct { Name, Origin string; Online bool }
```

**禁止**：壳不得调用/暴露 `Token`/`Dial`/`Credential` 等绕过 cookie 闸的入口——绑定面本就不导出
（`mobile/bind/export_surface_test.go:66-71` 的负向断言）。

### 3.2 iOS 壳侧接缝（新建符号，子卡落；**图外**）

spec 测试决定 3/4 指定的两个具名缝（壳内实现，但**必须有生产调用方**，不得是未导出的纯函数）：

- **`CookieBridge`** —— 拥有 `WKWebView` 与其 `WKWebsiteDataStore.httpCookieStore`；实现 §3.4 的 I3 时序。
  被「机器列表屏·进入」动作调用。单测以假 core + 假 cookie store 断言调用序、cookie 属性、fail-closed。
- **`PairingService.pair(bundleJSON:) throws`** —— 扫码结果与粘贴文本**归一**到同一入口；成功后把**原始
  bundle 字符串**写入 Keychain（I2），失败不落存储、不进列表。被配对屏调用。

### 3.3 Android 壳侧接缝（新建符号，子卡落；**图外**）

- **`WebviewSessionBinder`** —— 拥有 `WebView` + `CookieManager`；实现 §3.4 的 I3 时序。被 ViewModel 的
  「进入机器」调用。
- **`PairEntry.pair(bundleJSON: String): Result<Unit>`**（或被配对屏直接调用的等价单一入口）——扫码与粘贴
  归一；成功后把原始 bundle 写入 `EncryptedSharedPreferences`。

> 以上符号名是 spec 测试决定钦定的接缝锚；壳内私有细节（类拆分、协程封装）属子卡实现选择，不进冻结清单。

### 3.4 cookie 属性与清罐（I3/I4，承重）

因绑定面只导出 cookie **value**（`mobile/bind/adapter.go:52`），name/path/domain/Secure 必须由壳硬编码：

| 属性 | 壳注入值 | 现状出处 |
| --- | --- | --- |
| name | `handoff_session` | `internal/agentd/auth.go:29` |
| path | `/` | `internal/agentd/authroutes.go:349` |
| domain/host | `127.0.0.1`（host-only，不设 `Domain`） | 回环源 `127.0.0.1`，`mobile/README.md:47` |
| Secure | `false`（明文回环） | `authroutes.go:353`（`Secure=r.TLS!=nil`） |
| HttpOnly | 服务端签发 `true`；**壳客户端注入不可置**（iOS 平台限制，见文末修订记录 2026-09-28） | `authroutes.go:350` |
| SameSite | `Lax` | `authroutes.go:351` |
| Max-Age/Expires | 不设（进程内会话 cookie） | `mobile/README.md:47` |

清罐条件：**旧机 cookie 必须全部失效**。spec I3 取「全清 webview cookie jar」；`mobile/README.md:45` 记
等价最小实现「按 `name=handoff_session + host + Path=/` 删除，不区分端口」。二者以**可观测不变量**为准：
切机后发往旧机源的请求不再携带旧 `handoff_session`。机制（全清 vs 定向删）归子卡。

### 3.5 构建接线与产物（spec「构建接线语义」）

```bash
./mobile/build.sh android   # → dist/mobile/handoff-mobile.aar（+ sources.jar），四 ABI，-androidapi 21
./mobile/build.sh ios       # → dist/mobile/handoff-mobile.xcframework（Handoff-Mobile，两 slice）
# 壳（子卡在 macOS 落）：
cd mobile/android && ./gradlew assembleDebug          # → app/build/outputs/apk/debug/app-debug.apk
cd mobile/ios     && xcodebuild -scheme … -sdk iphonesimulator build   # → *.app（无签名）
```

- 绑定类名：Kotlin `bind.Bind` / `bind.Machine`；Swift 框架 `Handoff-Mobile`，ObjC 前缀 `Bind`（类
  `BindMachine`）——以 §1.2 生成面为据；子卡构建后须核对真实产物（不符即契约错配，回退本节点）。
- 壳不提交核心产物（`dist/` 已 ignore）；**壳构建必须先跑核心构建**。

---

## 4. 原子冻结清单

每条 = **一支可独立判 pass/fail 的断言**（复合语义一律拆开），失败可定位。`[T0]` = 本轮已用
**可执行冻结**锁住（`mobile/bind/shell_api_golden_test.go` 与既有绑定面测试）；其余为子卡实现后
在 **macOS** 上对账的条目。**双向执法**：正向要求 + 负向禁止。

### 4.1 绑定面消费（壳↔核）

1. `[T0]` Java/Kotlin 生成面的 7 个静态方法签名逐字等于 §1.2 冻结面，且产物无 `skipped function/field`。
2. `[T0]` ObjC/Swift 生成面的 7 个 `FOUNDATION_EXPORT Bind*` 签名逐字等于 §1.2 冻结面，且产物无 `skipped function/field`。
3. 壳源码只出现 §3.1 的 7 个绑定调用；**负向**：不出现 `Token`/`Dial`/`Credential` 或任何非导出面调用。
4. `Pair` 入参是整份 bundle 字符串；**负向**：壳内无对 bundle 的 JSON 解析（`JSONDecoder`/`kotlinx.serialization`/`Gson` 不用于 bundle）。
5. 壳不自行拼回环源；`load` 的 URL **逐字取** `SwitchMachine`/`Origin` 的返回值（容忍其无尾斜杠，URL 路径缺省为 `/`）。
6. 机器列表按 `0..<MachineCount()` 迭代，`MachineAt` 返回 `nil` 时跳过；**负向**：越界不崩溃。

### 4.2 I1 配对载荷

7. 扫码与粘贴**归一**到同一个 `pair(bundleJSON)` 入口（唯一归一化点；两条路径共用存储与错误处理）。
8. `Pair` 返回 error（含 bundle 损坏/版本不受支持）时**不**进机器列表。
9. `Pair` 返回 error 时**不**写安全存储、**不**加载 webview。
10. 壳把**原始字符串**整体透传给 `Pair`（允许去首尾空白）；**负向**：不重组/不改写 payload。

### 4.3 I2 凭据持久化

11. `Pair` 成功后，**原始 bundle JSON** 写入安全存储：iOS Keychain item 带 `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`。
12. `Pair` 成功后，Android 用 Keystore 支撑的 `EncryptedSharedPreferences` 存原始 bundle。
13. **负向**：iOS 不设 `kSecAttrSynchronizable=true`（不跨设备同步）。
14. **负向**：Android `android:allowBackup="false"`（或 backup rules 显式排除安全存储文件）。
15. 启动路径：安全存储**有** bundle → `Pair(stored)` → 机器列表；**无** → 配对页。
16. 启动路径：`Pair(stored)` 失败 → 配对页 + 可重试错误（不复用半态）。
17. 存储往返：写入的字节与读回的字节相等（存 → 取 → `Pair` 收到同一串）。
18. **负向**：token / cookie / bundle 值不进日志（壳日志仅可含机器名、长度、错误类别）。

### 4.4 I3 会话桥接时序（承重）

19. 进入/切机严格按 `SwitchMachine(m)` → **清 webview cookie jar** → `SessionCookie(m)` → 注入 cookie → `load(origin)`；单测断言调用序（乱序即红）。
20. 切机后旧机 cookie 不再随发往旧机源的请求发出（一罐只装一机）。
21. 任一步失败（`SwitchMachine` error / 清罐失败 / `SessionCookie` error / 注入失败）→ **不调用 `load`**、不导航。
22. 同机重复进入不进入错误态（可命中核内缓存；重复 `SwitchMachine` 等价重兑）。

### 4.5 I4 cookie 属性（逐键原子）

23. 注入 cookie 的 name == `handoff_session`。
24. 注入 cookie 的 path == `/`。
25. 注入 cookie 为 host-only，domain == `127.0.0.1`（不设 `Domain` 通配）。
26. 注入 cookie 的 `Secure` == `false`（明文回环）。
27. 注入 cookie 的 `HttpOnly`：**服务端签发面**为 `true`（`authroutes.go:350`）；**壳客户端注入不可置**——iOS `HTTPCookie` 的 `HttpOnly` 只读、`NSHTTPCookiePropertyKey` 无对应键，Android 若平台接受则置。记为**已知限制**（不判 pass）：客户端注入的会话 cookie 非 HttpOnly，影响面限于 webview 内 JS 读取，**不削弱 agentd 回环 cookie 闸**（闸只校验 cookie 名/值有效性，与 HttpOnly 无关）。裁决见文末修订记录（2026-09-28）。
28. 注入 cookie 的 `SameSite` == `Lax`。
29. 注入 cookie 不设持久 `Max-Age`/`Expires`（进程内会话 cookie）。
30. 注入 cookie 的 value 逐字来自 `SessionCookie` 返回值；**负向**：壳不构造/不伪造 value，不用 origin/token 冒充。
31. 子卡侧 source guard：壳内 cookie 名常量 == `"handoff_session"`（与 `internal/agentd/auth.go:29` 对齐）。

### 4.6 I5 fail-closed

32. 离线机（`Machine.Online == false`）列表可见但**不可进入**；点击不触发 `SwitchMachine`/`load`。
33. `SessionCookie` 空值/error 或 `SwitchMachine` error → 错误 UI + 可重试；**负向**：不复用旧 cookie、不加载 webview。
34. 已加载后会话过期（webview 收到 401 / 无凭据页）→ 「过期」错误态 + 可重试（重走 I3）；**负向**：不退化成静默空白。
35. 四类错误可归因：「配对 / 离线 / 兑换 / 过期」，按**失败调用点 + `Machine.Online`** 归类，**不解析 error 文案**（跨 gomobile 面无结构化错误哨兵）。

### 4.7 I6 零协议逻辑

36. 壳不构造隧道/CONNECT/ticket/兑换请求；除 `load(origin)` 与控制台同源请求外，壳无自建 HTTP 客户端访问 agentd。
37. 壳不实现 bundle 编解码、不实现会话兑换（一切在 Go 核）。

### 4.8 I7 核生命周期

38. Go 核为 App 级单例：进程内初始化路径只 `Pair` 一次；**负向**：不并发建第二个核（gomobile 面即包级单例）。
39. App 终止调 `Close()`；系统回收后前台重启走「启动」路径重建（不假设核状态存活）。

### 4.9 构建接线

40. Android 工程 `mobile/android/` 提交可用的 gradle wrapper。
41. Android 依赖 `dist/mobile/handoff-mobile.aar`，`minSdk 21`、`compileSdk/targetSdk 36`、debug 签名。
42. `./gradlew assembleDebug` 产出 `app-debug.apk`。
43. iOS 工程 `mobile/ios/` 依赖 `dist/mobile/handoff-mobile.xcframework`（framework `Handoff-Mobile`）。
44. `xcodebuild` 无 GUI 复现模拟器 `.app`（无签名）。
45. 构建顺序 = 核心 `./mobile/build.sh` → 壳；壳不提交 `dist/` 产物。
46. 产物可读：APK 包名与 `minSdk 21` 可由 `aapt`/`apkanalyzer` 读出；`.app` 内含 `Handoff-Mobile.framework`。

### 4.10 模块与图

47. `mobile/android/`、`mobile/ios/` 与 `mobile/` 同属图外；根模块 `go list ./...` 不含它们（`mobile/moduleisolation_test.go` 形态）。
48. `[T0]` 本分支零 `target.json`/`best.json`/视图 diff 变化；`codegraph check` 无本卡新增违规（本轮实测 fails=0）。

---

## 5. 依赖方向、组装点与预算

- 壳 → 绑定面（AAR/XCFramework）单向；**根模块构建图不含壳**，无跨域边、无预算变化。
- `codegraph/target.json` / `best.json` / 分支视图：**零变化**（本卡零 Go 符号、壳图外）。这是纪律允许的
  「未引入新符号则合法无视图，不造空文件」情形，非降档。
- **冻结物**：契约增量即本文件；可执行冻结即 `mobile/bind/shell_api_golden_test.go`（随本提交）。

---

## 6. 拍板记录（三重闸门）

只记同时满足「难逆转 × 无上下文会惊讶 × 真取舍」的决定。

**命中两条：**

1. **cookie 属性（name/path/domain/Secure/SameSite；HttpOnly 除外，客户端注入不可置，见 §11 修订 1）由壳侧硬编码，绑定面继续只导出 value。**
   难逆转——日后要改由核导出完整 cookie，需同时动 Go 绑定面、gomobile 生成面与两端壳；
   无上下文会惊讶——「核内明明有完整 `mobilecore.SessionCookie` DTO，壳为什么手拼属性」是最自然的直觉；
   真取舍——被否方案 = 让绑定面导出完整 cookie（`SessionCookie` 返回 `*Cookie`）。立。
2. **壳把原始 bundle JSON 整体透传 `Pair` 并整体存安全存储，不解析。**
   难逆转——存储格式与 wire 耦合，日后要改存储结构得处理已装机的旧数据；
   无上下文会惊讶——「存下来时顺手解析成机器名/token 字段更省事」是被否方案的原话；
   真取舍——被否方案 = 解析 bundle 存结构化字段（会随 wire 漂移、把 token 散落多处副本）。立。

**不立（探索性/确定性设计，逐条记判据，防「空着与没审过不可区分」）：**

- 清罐用「全清 jar」而非「定向删 `handoff_session`」：两者在专用回环 webview 下等价，无被否的像样方案。不立。
- 失败态按「失败调用点 + `Online`」归类而不解析文案：由「跨 gomobile 面无可结构化错误」这一既成事实推出，无取舍空间。不立。
- 壳工程纳入本仓（非拆仓）：spec 已拍板（`docs/superpowers/specs/b417.md:38-48`），非本节点新决定。不立。

**无其它命中。**

---

## 7. Ticket 0、可执行冻结与直通竖切

### 7.1 Ticket 0（本提交）

- **B417 不新增、不修改任何 Go/绑定面符号**，壳工程按 spec 结构「子卡从零起」（`docs/superpowers/specs/b417.md:24-27`），
  壳骨架归 macOS 子卡——本节点不落跨语言源码骨架（本机无 JDK/Xcode/NDK，落非编译通过的骨架违反纪律）。
- 本节点新增**一个图外测试符号**：`mobile/bind/shell_api_golden_test.go#TestBindGeneratedShellAPIGolden`
  —— 用钉版 gobind 生成 Java/ObjC 面并逐行冻结，把「壳编译期 API 形状」在本机变成能变红的闸门。
- **无分支视图 diff**（`mobile/` 图外、零 Go 符号）；`codegraph/target.json`/`best.json` 零变化。

### 7.2 可执行冻结

- **生成面金样本**：`TestBindGeneratedShellAPIGolden` 本轮跑过，且**变异可红**——把 Java 侧 `pair` 的
  `throws Exception` 抹掉，测试立即 `FAIL … 生成面缺少冻结签名`，还原即绿（原始输出见台账 §4）。
- 无哈希/密钥派生/编码格式的**新**金样本需求（bundle 编码是 B369 既有 `pairing_fixture_test.go`；壳不解析）。
- 平台 cookie store / 安全存储的**行为**金样本需真 SDK，列欠账 §8.3。

### 7.3 直通竖切（spec 主缝 = iOS 模拟器端到端）

spec 测试决定 2 的最高缝是「粘贴真实 `console --bundle` → `Pair` → 列表 → 进入 → webview 加载**已认证**
控制台」。该竖切需 Xcode + iOS 模拟器 + 运行中的 agentd，**本 linux 工作树不可行**，列欠账 §8.2，归
macOS 集成轮（不得由并行壳子卡各自伪造——它跨两端与 agentd）。

---

## 8. 本节点欠账（交棒前逐条声明，请协调者显式认账）

1. **壳工程骨架 + 编译**（Kotlin/Gradle、Swift/Xcode）：本机无工具链，未就地补齐；须在 macOS 执行机落
   `mobile/android/`、`mobile/ios/` 并跑通 §4.9 的两条构建闸。**这是本节点最大的显式认账项。**
2. **直通竖切**（§7.3，iOS 模拟器 E2E）在 macOS 上跑。
3. **平台 cookie store 语义核对**：`WKHTTPCookieStore` 注入 host-only 非 Secure cookie 是否被接受、Android
   `CookieManager.setCookie` 对 `SameSite=Lax` 的支持版本——本机无 SDK 源码，**未验证**，子卡在 macOS/真机核对。
4. **平台安全存储 API 核对**：`kSecAttrAccessibleWhenUnlockedThisDeviceOnly` 与
   `androidx.security:crypto` 版本（`EncryptedSharedPreferences` 已弃用通知需评估）——**未验证**。
5. **无 GUI 构建复现**：§4.9 两条命令在干净 macOS 上的可复现性（含 gradle wrapper 完整性）。
6. **UI 文案与重试入口**：spec 归 plan 定稿（`b417.md:129-130`）；contract 只冻结「四类错误可归因 + 可重试」。

---

## 9. 移交 plan 附区

（本区由 plan 出稿时吸收，吸收后在区头标注「已由 plan〈文档〉吸收（日期）」销区。）

1. **壳内类结构**：`CookieBridge`/`WebviewSessionBinder` 之外的类拆分与命名归子卡；两屏（配对/列表）+ webview 容器 + 错误态是既定 UI 骨架。
2. **构建脚本编排**：壳构建前先调 `./mobile/build.sh`；gradle wrapper 版本、`compileSdk/targetSdk 36`、iOS deployment target 归子卡。
3. **错误态文案**：四类（配对/离线/兑换/过期）文案与重试按钮归 plan。
4. **多机列表交互**：离线机展示名 + 禁用进入态；扫码入口在模拟器/无摄像头时的隐藏或降级（粘贴为准）。
5. **iOS 工程生成**：仓内无 XcodeGen/Tuist；提交 `.xcodeproj` 或确定生成步骤（`xcodebuild` 可复现）——子卡定。
6. **Android Compose vs View**：壳「极薄」即可，选型归子卡。

---

## 10. 本轮法定核对

- 契约增量文档：本文件；每个签名/常量均有现状代码出处（§1）。
- 上游状态位：spec 头部「已批准」已复核一致，无需回写。
- 目标图：**零变化**（壳图外、零 Go 符号），`codegraph check` fails=0；无视图 diff（合法无符号）。
- Ticket 0 / 可执行冻结：`mobile/bind/shell_api_golden_test.go` 本轮 `go test ./bind/ -run TestBindGeneratedShellAPIGolden` 绿，变异可红（台账 §4）。
- 可执行金样本：生成面签名冻结（§7.2）；无新哈希/密钥派生命中。
- 三重闸门：§6 记两条命中 + 三条不立判据，非空着。
- 图覆盖债：§1.4 记 `mobile/` 图外与 `mobilecore` 会话符号未入图（既存）。

---

## 11. 修订记录

### 修订 1（2026-09-28，B417.2 iOS 实现轮；协调者裁决）

- **受影响冻结项**：§3.4 cookie 属性表 `HttpOnly` 行、§4.5 条 27。
- **原因**：B417.2 plan 出稿时经平台核验发现——iOS `HTTPCookie` 的 `HttpOnly` 为只读属性，
  `NSHTTPCookiePropertyKey` 无对应键，**客户端注入的 cookie 无法置 HttpOnly**。原冻结值
  `HttpOnly == true` 在 iOS 平台不可达成。属「计划阶段发现冻结物与平台现实冲突」。
- **裁决**：接受平台限制，记为已知限制（不判 pass）。理由：①HttpOnly 只约束 webview 内 JS
  读取 `document.cookie`，**不参与 agentd 的回环 cookie 闸**（闸只看 cookie 名/值与有效性），
  放松它不削弱门禁；②webview 只加载受信 agentd 控制台同源内容；③替代方案（改由服务端
  `Set-Cookie` 路径注入）要动 B369 冻结的「核程序化兑换 + 壳注入」设计，代价远超收益。
- **影响面**：iOS 壳注入的会话 cookie 非 HttpOnly（页面 JS 可读）；风险前提是控制台存在 XSS，
  当前控制台为受信同源内容。Android 若 `CookieManager` 接受则仍置 HttpOnly。
- **回写内容**：§3.4 表 `HttpOnly` 行、§4.5 条 27 已按此修订；子卡测试不得为条 27 判 pass。
- **另附**：条 13（Keychain 不设 `kSecAttrSynchronizable=true`）的测试断言在实现轮被细化为
  「不存在 `Synchronizable == true` 的条目」（模拟器把未设值回读为 `0` 而非 `nil`）；契约文字
  「不设 `=true`」语义未变，无需修订契约，记此备查。
