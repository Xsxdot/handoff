# mobile/ —— 移动端 App 的 Go 连接核绑定模块

本模块是 B369 移动端 App 的 **Go 连接核**：把 `internal/mobilecore`（拨 relay/直连
隧道 + 回环反代 + 配对载体解析）经 gomobile 编成 Android AAR / iOS XCFramework，
供 Kotlin/Swift 薄壳调用。壳侧只做 webview 容器、扫码配对、凭据入安全存储
（Keychain/Keystore），**一切协议逻辑在 Go 核**，壳内零重实现。

## 结构

- `bind/` —— gomobile 绑定面（壳与核的唯一交接面）。导出面见下节，别在别处另加
  导出符号（绑定面积最小 = 攻击面最小）。
- 本模块是**独立嵌套 Go module**（自带 `go.mod` + `replace ../`），与 `desktop/`
  同构：根模块的 `go list ./...` 不含它；反向依赖禁止（根模块不得 import 本模块）。

## 绑定面（壳的契约面）

    func Pair(bundleJSON string) error
    func MachineCount() int
    func MachineAt(index int) *Machine      // Machine{Name, Origin string; Online bool}
    func Origin(machine string) (string, error)
    func SessionCookie(machine string) (string, error)
    func SwitchMachine(machine string) (string, error)
    func Close() error

导出面**不含** `Token` / `Dial` / `Credential` 等可绕过回环门禁的入口（回环反代
不注入凭据是承重安全属性，见 contract §4.2/§6）。

**形状铁律（B386 的教训）**：参数与返回值只许 gomobile 真支持的形状——基本类型、
`error`、以及**本包内定义**的结构体指针。`isSupported`（`bind/gen.go`）对 slice
只放行 `[]byte`，对命名类型只放行 interface/指针且包必须在绑定集合内；违反者
**不报错**，只是整条函数被静默跳过（产物里一行 `// skipped function …`），
`go build`/`go test` 照样全绿而壳拿不到方法。故：

- **列表一律用「计数 + 按索引取指针」表达**（`MachineCount` + `MachineAt`），不用 `[]T`；
- **回给壳的结构体定义在本包**（见 `types.go`），字段只用 string/bool；
- 加/改导出面后跑 `go test ./bind/ -run TestGomobileSurfaceHasNoSkips`——它跑真
  gobind（java + objc）并断言产物里无 `skipped function/field` 且每个导出函数都在
  产物里；`TestBindExportedSurfaceIsFrozen` 逐字钉住上表签名。

## 切机与会话调用顺序（壳侧）

壳切换到一个目标机时，必须严格按以下顺序调用绑定面，任一步失败都**不得导航**：

1. `SwitchMachine(target)` —— 成功返回后核内已清旧会话并为目标机重兑换，壳拿到目标 loopback 源。
2. 清除该 loopback host 的全部旧 cookie：按 `name=handoff_session + host + Path=/` 删除，**不区分端口**（cookie 不按端口隔离，须覆盖该 host 的所有端口）。
3. `SessionCookie(target)` —— 返回目标机当前有效会话的 cookie 值。
4. 把返回值**写回 cookie jar**：host-only `handoff_session`，`Path=/`、`HttpOnly=true`、`SameSite=Lax`、`Secure=false`，不设持久 `Max-Age/Expires`（进程内会话 cookie；服务端到期仍是最终权威）。
5. **导航到第 1 步返回的 origin**。

任一步失败（切机失败、清 jar 失败、取 cookie 失败、写入失败）都不得导航；清 jar / 注入失败由壳呈现可行动错误，可重试。**Go 绑定层只返回 cookie 值，不操作平台 cookie store**——`WKHTTPCookieStore` / `CookieManager` 的写入是壳的职责；绑定面不返回 token / origin 冒充 cookie。

## 构建

**Android 前置**（缺一项就在 bind 时报错，报错信息不一定指路）：

| 依赖 | 说明 |
| --- | --- |
| go 1.26.1 | — |
| JDK 21 | gomobile 要 `javac`/`jar`；装到 `~/Library/Java/JavaVirtualMachines/` 并把 `$JAVA_HOME/bin` 进 `PATH` |
| Android cmdline-tools | `~/Library/Android/sdk/cmdline-tools/latest`（`sdkmanager` 入口） |
| `platform-tools`、`build-tools;36.0.0` | `sdkmanager --install` |
| **`ndk;30.0.16248370`** | 2.8G；gate 钉死这版 |
| **`platforms;android-36`** | gomobile 找 `$ANDROID_HOME/platforms`；缺了报 `failed to find android SDK platform` |
| `gomobile`/`gobind` | 下方 `go install`；装到 `$HOME/go/bin` |

**iOS 前置**：完整 Xcode（CommandLineTools **不够**，`-target=ios` 会报 `requires Xcode`）。

    go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260908204917-8b95e45f8d3e
    go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260908204917-8b95e45f8d3e

    export JAVA_HOME=$(ls -d ~/Library/Java/JavaVirtualMachines/*/Contents/Home | head -1)
    export PATH="$JAVA_HOME/bin:$HOME/go/bin:$PATH"
    export ANDROID_HOME=~/Library/Android/sdk
    export ANDROID_NDK_HOME=~/Library/Android/sdk/ndk/30.0.16248370

    ./build.sh android      # → ../dist/mobile/handoff-mobile.aar
    ./build.sh ios          # → ../dist/mobile/handoff-mobile.xcframework
    ./build.sh all

**两条硬约束**（写死在 `build.sh` 里，改动前先读它的注释）：

1. **Android 必须显式 `-androidapi 21`**：NDK 30 移除了 API<21 的 platform，而
   gomobile 缺省 minsdk=16，不加会在 bind 时报 `unsupported API version 16`。
2. **`mobile/go.mod` 必须带 `tool golang.org/x/mobile/cmd/gobind` 指令**：新版
   gomobile 要求 `golang.org/x/mobile` 在本模块依赖图内，否则报
   `missing golang.org/x/mobile dependency`。

**`go build ./...` ≠ `gomobile bind`**：前者只证明 Go 源码可编译，不证明能产出
AAR/XCFramework，更不证明绑定面**完整**（B386：`Pair`/`MachineNames` 被 gomobile 静默
跳过，`go build` 与 `go test` 全绿而壳拿不到那两个方法）。两道真闸：
`go test ./bind/ -run TestGomobileSurfaceHasNoSkips`（形状，任何机器可跑）与
`./build.sh android|ios`（产物，需真工具链）。

**已知真机读数**：Android AAR 于 2026-09-19 在 darwin 机（NDK 30.0.16248370 + JDK 21 齐，
无 Xcode）一次构建通过 → `handoff-mobile.aar`（35MB，`classes.jar` + 四 ABI
`libgojni.so`）；iOS XCFramework 尚未在装了完整 Xcode 的机器上跑过（**真机清单未验项**）。

## 凭据卫生与 token 轮换

配对 bundle / QR 的保护等级**等同主令牌集**：不落日志、即扫即弃。手机丢失先吊销
浏览器会话；需要更狠时按下述流程轮换。

- **先吊销浏览器会话**：`handoff sessions revoke <session-id>` 只杀 cookie 会话
  （`DELETE /api/auth/sessions/{id}`），**不能吊销节点 token**——它救不了 token 泄露。
- **token 泄露的唯一处置：双端轮换节点 token 并重启。**

  1. 在泄露的节点机上把 `~/.handoff/config.yaml` 的 `token:` 改成新的随机值
     （32 位十六进制；可用 `openssl rand -hex 16` 生成）。`handoff init` 重跑**不会**
     换 token（默认取当前配置），必须手改。
  2. 重启该机 agentd：`handoff service restart`（未托管时手工重启进程）。
  3. 在协调机 `~/.handoff/config.yaml` 对应 target 里把 `token:` 改成同一个新值。
     relay 形态下 token 同时是 E2E PSK 源，两端必须一致；改完无需重启 CLI（每次
     调用现读配置）。
  4. 手机上的旧配对 bundle 立即失效，需重新扫码配对。

> 为什么不是「吊销 cookie 即够」：cookie 会话是 ticket 兑换出来的短命凭证，吊销它
> 不影响 token 本体；token 是 agentd 的主 Bearer 令牌，持它 = 持该机全量 API 权限
> （contract §3.2 凭据模型）。只有把 token 换成新值，泄露的那份副本才作废。
