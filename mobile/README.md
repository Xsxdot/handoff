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

    func Pair(bundleJSON string) (mobilecore.PairResult, error)
    func Origin(machine string) (string, error)
    func MachineNames() []string
    func Close() error

导出面**不含** `Token` / `Dial` / `Credential` 等可绕过回环门禁的入口（回环反代
不注入凭据是承重安全属性，见 contract §4.2/§6）。

## 构建

前置：go 1.26.1、gomobile、Xcode（iOS）、Android SDK + NDK 30.0.16248370。

    go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260908204917-8b95e45f8d3e
    go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260908204917-8b95e45f8d3e

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
AAR/XCFramework。真正的 bind 只能在装了 Xcode/NDK 的机器上跑；本仓 linux 开发机
跑不了，别把 `go build` 全绿当成绑定产物已验证。

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
