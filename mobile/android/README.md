# Handoff Android 原生壳（B417.1）

Kotlin 单 app 工程，承担四件职责：**webview 容器、扫码/粘贴配对入口、原始 bundle 入
Keystore 的安全存储、webview cookie 桥接（含切机清罐）**。壳内**零协议逻辑**——一切经
gomobile 生成面 `bind.Bind` 的七个静态方法调用。

- 命名空间 / applicationId：`dev.gosuper.handoff.mobile`
- SDK：`minSdk 21` / `compileSdk 36` / `targetSdk 36`
- 入口 Activity：`dev.gosuper.handoff.mobile.ui.PairingActivity`
- 文档基线：`docs/superpowers/specs/b417.md`、`b417-contract.md`、
  `docs/superpowers/plans/b417.1-plan.md`

## 职责与边界

| 层 | 文件 | 职责 |
| --- | --- | --- |
| 组装点 | `HandoffApp.kt` / `AppGraph.kt` | 进程级单例：构造绑定网关与安全存储，`onCreate` 跑一次 `startup.ensureStarted()`，`onTerminate` 收核 `Bind.close()` |
| 端口 | `core/CoreGateway.kt`、`web/CookieStore.kt`、`web/WebviewNavigator.kt` | 把绑定面与平台 webview 收敛成可替换窄接口 |
| 生产端口 | `core/BindCoreGateway.kt`、`core/EncryptedSecretStore.kt`、`ui/AndroidCookieStore.kt`、`ui/AndroidWebviewNavigator.kt` | 唯一触碰 `bind.Bind` / `CookieManager` / `EncryptedSharedPreferences` 的地方 |
| 接缝 | `pair/PairEntry.kt`（配对归一）、`web/WebviewSessionBinder.kt`（I3 会话桥接） | 两条具名壳缝 |
| UI | `ui/PairingActivity.kt`、`ui/MachineListActivity.kt`、`ui/WebviewActivity.kt` | 两屏 + webview 容器；扫码与粘贴两入口都归一 `PairEntry.pair` |

**边界（红线）**：不得改 `mobile/bind/**`、`mobile/build.sh`、`internal/**`、`cmd/**`、
`web/**`、`codegraph/**`。壳不提交核心产物（`dist/mobile/` gitignored）；不解析 bundle
JSON、不自建 HTTP 客户端、不暴露 JS 桥、凭据不落日志；明文仅对回环 `127.0.0.1` 放行。

## 依赖：核心 AAR 必须先构建

壳依赖 `dist/mobile/handoff-mobile.aar`（gomobile 产物，gitignored）。
`../../mobile/build.sh android` 是唯一合法来源；壳工程不提交该产物。

## 环境变量

```bash
export JAVA_HOME=$(ls -d ~/Library/Java/JavaVirtualMachines/*/Contents/Home | head -1)
export ANDROID_HOME=$HOME/Library/Android/sdk
export ANDROID_NDK_HOME=$HOME/Library/Android/sdk/ndk/30.0.16248370
export PATH="$JAVA_HOME/bin:$HOME/go/bin:$PATH"
```

需要 JDK 17+、Android SDK（build-tools 36.0.0 / platforms android-36）、NDK
30.0.16248370、`gomobile` 与 `gobind`（`go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20260908204917-8b95e45f8d3e`）。

## 构建（核心 → 壳，一条命令）

```bash
bash mobile/android/build.sh
```

它先跑 `mobile/build.sh android` 生成 AAR，再在 `mobile/android` 里
`./gradlew :app:assembleDebug`，产物：
`mobile/android/app/build/outputs/apk/debug/app-debug.apk`。

只重建壳（AAR 已在位）：

```bash
cd mobile/android && ./gradlew :app:assembleDebug
```

> 首次构建需联网拉 Gradle 8.13 发行包与依赖；依赖已入本地缓存后可 `--offline`。

## 单元测试

```bash
cd mobile/android && ./gradlew :app:testDebugUnitTest
```

壳内 JVM 单测（无 Robolectric）覆盖：I3 调用序、cookie 逐键属性与 fail-closed、配对归一与
逐字节往返、离线守卫、源码 guard（只七函数 / 无 Token·Dial·Credential / 无 JSON 库 /
无自建 HTTP / 无 JS 桥 / 凭据不入日志 / 最小明文放行）。

## 依赖钉版理由（minSdk ≤ 21）

AndroidX 最新稳定线已全线 `minSdk 23`，为保证 `minSdk 21` 不冲突，钉在仍支持 21 的近期版本：

| 依赖 | 版本 | minSdk |
| --- | --- | --- |
| `androidx.appcompat:appcompat` | `1.7.1` | 21 |
| `androidx.core:core-ktx` | `1.13.1` | 19 |
| `androidx.lifecycle:lifecycle-runtime-ktx` | `2.8.7` | 19 |
| `androidx.security:security-crypto` | `1.1.0` | 21（`1.0.0` 是 23，不得用） |
| `com.journeyapps:zxing-android-embedded` | `4.3.0` | 19 |

**不得**升到 appcompat 1.8.0 / core 1.19.1 / lifecycle 2.11.0 等 minSdk 23 版本。

## 已知边界 / Out of Scope

- **Android 真机运行时验收属 Out of Scope（spec）**：本期只到构建物（APK）+ 壳内 JVM 单测。
  真机清单（`CookieManager` 对 host-only/`Secure=false`/`127.0.0.1`/`SameSite=Lax` 的接受度、
  `EncryptedSharedPreferences`/`MasterKey` 在 API 21–22 的运行时行为、明文回环实载）**未验证**。
- **扫码入口**依赖摄像头；无摄像头/模拟器设备以**粘贴**路径为准（两入口共用同一配对归一）。
- `AndroidManifest.xml` 显式 `allowBackup="false"`（配对 bundle = 主令牌集，禁云备份外泄）；
  明文仅 `127.0.0.1` 放行，不开 `usesCleartextTraffic` 全局放行。
