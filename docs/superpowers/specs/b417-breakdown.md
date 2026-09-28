# B417 拆解稿：Android/iOS 原生移动壳（WebView + 配对 + 安全存储）

**状态：待拍板**（本稿为提案；拍板权与扇出归协调者。裁决后逐岔口回写 §0、头部改「已拍板（日期）」）
**卡：** B417（L3 重档；contract → breakdown → 拆 Android/iOS 两子卡 → 父卡 integrate）
**标题：** Android/iOS 原生移动壳（WebView + 配对 + 安全存储）
**上游 spec：** `docs/superpowers/specs/b417.md` —— 头部「**状态：已批准（2026-09-28）**」实读在位
（本工作树内，spec 提交 `a9ba59b5` 为 HEAD 祖先，`git merge-base --is-ancestor` 实测 YES）
**冻结 contract：** `docs/superpowers/specs/b417-contract.md` —— 头部「**冻结状态：本提交随本文件…冻结**」
在位；「**有效基线：** `cards/B417-spec` @ `a9ba59b5`」与远端 `origin/cards/B417-spec` 实读同号
**有效基线：** `origin/cards/B417-spec` @ `a9ba59b5`（本卡合并目标）；当前工作分支 `cards/B417-charter-2`
@ `5894f5ac`（含 contract 提交；不切换、不越过）
**图依据：** `codegraph/best.json`（`domains` 中 `parent` 空 = 顶层子系统）。本分支**无**
`codegraph/diffs/<分支>.json`（合法：壳工程整体图外、本卡零 Go 符号，不造空视图）
**本稿台账：** `docs/superpowers/ledgers/2026-09-28-b417-breakdown-ledger.md`
**角色边界：** 本文**全部是提案**。不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。
扇出与拍板归协调者。

---

## 0. 待拍板岔口清单（集中；拍板者按本表逐条裁决）

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **P1** | **壳子卡的执行落地方案**（spec 备注 `b417.md:180-183`、contract §8#1 点名要求正面回答） | **甲（推荐）：回落本地 macOS Subagent-Driven** —— 两张壳子卡**不进 charter/handoff 小队派发**，由协调者在装了完整 Xcode + NDK 30 + JDK 21 的 macOS 本机（即 spec 备注「工具链 gate 已验（2026-09-28，本机）」的同一台机）按 `superpowers:subagent-driven-development` 逐 task 派 subagent 实现；产出提交到本卡目标分支。先例：B288（spec 备注「用户授权无人值守，不经 handoff 派发，由协调者在本会话派 subagent」，`b288.md:153`；执行台账 `2026-08-29-b288-execution-ledger.md`）。**乙：小队改绑 macOS 开发机** —— 把 charter 派发列的小队成员换成一台已配对的 macOS 机器（squad 成员绑机器，须新建 `pro`/`runner` 指向该机；`effectiveCovers` 强制 target/executor 空，故无法在卡上用 `--target` 定点）。**丙：分平台/其他** —— ① Android 子卡留 linux 若该机有 Android SDK；② 壳工程由协调者真机侧手动另办（B369 P5 原案，已被本卡 spec 显式推翻）。 | **甲**。理由：① 唯一被 gate 实测过的工具链就在 macOS 本机（spec §备注双端 PASS）；② 零新增基础设施、零等待；③ 丙①在 linux 上编不了 iOS、且 `mobile/build.sh` 自述「只在 darwin 跑」（`build.sh` 头注释），分平台执行会引入工具链分叉；丙②被 spec 推翻。乙是**升级路径**：若日后有 macOS 开发机配对进集群，可把小队的 pro/runner 改绑过去，DAG 不变、只是执行载体变化。**代价（须认账）**：甲不享 charter 状态机/工单/断点续跑，依赖协调者会话不中断；对策见 §3.0 收口纪律。 |
| **P2** | 两端壳子卡 **并行 vs 串行** | **甲（推荐）：顺序执行 A → B（或 B → A），同一 macOS 主机同一工作树**。理由：同一台机、同一工作树；`./mobile/build.sh all` + `gradlew` + `xcodebuild` 均为重活；subagent-driven 纪律本身要求实现 subagent 不并行。**乙：并行两卡** —— 先由协调者一次性跑通 `./mobile/build.sh all` 产出 `dist/mobile/*`，之后 A/B 触碰互不重叠的工程目录（`mobile/android/` vs `mobile/ios/`）、各自 DerivedData/build 目录，可并行。取舍：乙省墙钟，但需要预置核心产物并隔离构建输出目录，否则争用 `dist/mobile/` 与同一工作树。 | **甲**。单机单工作树下并行收益小、风险大于收益；若协调者以省时为重，取乙须显式预置核心产物。 |
| **P3** | 跨端共享常量（`handoff_session` / cookie 属性 / I3 调用序）是否需要**第三张跨端 golden 卡** | **甲（推荐）：不单开**，两端各自子卡验收栏内锁（contract §4.5 条 31 source guard + 各自单测断言调用序与逐键属性）；共享事实源是 contract §3.4 表格本身，两卡都引它。**乙：单开一张跨端 golden 卡**（共享 JSON/常量 fixture + 两端各自读同一 fixture 断言）。取舍：乙把「两端漂移」变成一条可红的跨端测试，但两端语言不同、fixture 读取代码本身就是重复劳动，且 contract §3.4 已逐键冻结。 | **甲**。契约表格即唯一事实源；再开一卡等于给同一常量造第二处副本。若实施中真出现漂移，回 spec/contract 补跨端 golden。 |
| **P4** | **父卡 integrate 的执行机与范围** | **甲（推荐）：integrate 归协调者在 macOS 本机执行**（与 P1 甲同机），范围 = ① 核心 `./mobile/build.sh all` → 两端壳构建闸（APK + simulator `.app`）；② spec 测试决定 2 的**直通竖切**（粘贴真实 `handoff console --bundle` → Pair → 列表 → 进入 → webview 加载已认证控制台，跑在 iOS 模拟器 + 运行中的 agentd）。**乙：integrate 由壳子卡各自代验** —— contract §7.3 明文否决（「不得由并行壳子卡各自伪造——它跨两端与 agentd」）。**丙：只做构建闸、竖切另开卡** —— spec 验收线明确要求模拟器跑通，不取。 | **甲**。竖切是最高缝且跨两端 + agentd，必须独立一轮；构建闸无 GUI 可 headless 复现，模拟器走查为半人工（见 §6）。 |
| **P5** | **`minSdk 21`（contract §4.9 条 41）× `EncryptedSharedPreferences`（contract §4.3 条 12）疑似冲突** | 本机无 Android SDK，**未能核对** `androidx.security:security-crypto`（`EncryptedSharedPreferences`）的 `minSdk` 下限；公开事实是该库要求较高 API（且其已被标记弃用，contract §8.4 已认账）。**甲（推荐）：子卡在 macOS 核对真实版本后，若确认 `minSdk > 21`，暂停并回 contract 裁决**（选项：抬 minSdk / 对旧 API 走 Keystore 手写兜底 / 声明 21–22 不受支持）。**乙：先按 contract 字面实现，冲突在实际构建时暴露再处理。** | **甲**。contract 内部两条冻结项可能互斥，属「冻结物自相矛盾」，不应由子卡静默择一；子卡先核对、命中冲突即回 contract。本机不写死结论（见 §6）。 |
| **P6** | **明文回环许可的最小放行姿态**（Android `usesCleartextTraffic` / network-security-config；iOS ATS） | 壳 webview 要加载 `http://127.0.0.1:<port>` 明文回环。**甲（推荐）：最小放行——仅 loopback（Android 用 network-security-config 只对 `127.0.0.1` 放行；iOS 用 `NSAllowsLocalNetworking` 或等价最小例外），不得全局 `usesCleartextTraffic=true` / `NSAllowsArbitraryLoads=true`。** **乙：全局放行**（省事，但把「明文回环」这一已知约束扩散成任意明文）。 | **甲**。这是承重安全姿态的最小化；具体 API 形状归子卡在 macOS 核对（本机无 SDK）。 |
| **P7** | **契约回写 & `mobile/README.md` 旧读数订正** | 本稿做了若干边界澄清（§2.4），纪律要求「即便结论是不退回 contract 也要回写契约文档留一行修订记录」。**甲（推荐）：在 `b417-contract.md` 末尾新增「修订记录（breakdown 出稿轮，2026-08-28）」小节，逐条记澄清；同批把 `mobile/README.md` 里「iOS XCFramework 尚未在装了完整 Xcode 的机器上跑过（真机清单未验项）」的旧读数订正为 gate 已验（spec 备注 2026-09-28 双端 PASS），或由子卡在 macOS 复验后订正。** **乙：不回写，澄清只活在本稿。** | **甲**。澄清只活在拆解稿里，review 的冻结物触碰行会对不上账。README 旧读数与 spec 备注直接矛盾，是既存文档漂移，须消除。 |

> 若协调者裁决改变契约冻结面（如 P5 选乙或 P1 选乙需改派发机制），须先按对应流程回写/改绑定，再进入 plan。

---

## 1. 触及子系统清单与派卡资格核

子系统 id 与类型取自 `codegraph/best.json` 的 `domains`（`parent` 空 = 顶层子系统，`type` 即逻辑/边界标注）。
**本卡新增的两个壳子系统整体在 Go 代码图外**（Kotlin/Gradle 与 Swift/Xcode 两条独立工具链，与 `desktop/`、
`mobile/` 同例：扫描配方显式排除 `mobile` 前缀，`scripts/codegraph-rescan/main.go:231`），故并列「图外」条目。
扇出前按架构法第一条**派卡资格四条**（有界文件集 / 契约面可枚举 / 同级依赖可排 DAG / 逻辑型·边界型标注）逐个核。

| 子系统 / 载体 | 图类型 | 本卡角色 | 有界文件集 / 暴露面 | 派卡资格四条核验 |
| --- | --- | --- | --- | --- |
| **`mobile/android/`（图外·新建）** | **逻辑型 + 边界型**（壳内 I1–I5 时序/错误态可单测闭环=逻辑；webview cookie jar / Keystore / Gradle 构建=边界） | **实现面（新建）**：Kotlin 薄壳（单 Activity + 两屏 chrome + webview + cookie 桥接 + 安全存储） | 有界：`mobile/android/**`（Gradle wrapper、`app/src/main/**`、`app/build.gradle.kts`、`settings.gradle.kts`、`AndroidManifest.xml`、`app/src/test/**`、本目录 README）。暴露面/接缝：`WebviewSessionBinder`、`PairEntry.pair(bundleJSON): Result<Unit>`（contract §3.3）。消费 `bind.Bind`/`bind.Machine`。 | ①文件集可圈（见 §3.1④）；②契约为 contract §3.3/§3.4 冻结物，可枚举；③依赖 `mobile/bind` 的 AAR（单向，图外）；④已标逻辑/边界双型。**通过。** |
| **`mobile/ios/`（图外·新建）** | **逻辑型 + 边界型**（同上；Keychain / WKWebView / Xcode 构建=边界） | **实现面（新建）**：Swift 薄壳（单 `UIViewController` + 两屏 chrome + webview + cookie 桥接 + 安全存储） | 有界：`mobile/ios/**`（`.xcodeproj`/`Package.swift`、`Sources/**`、`Info.plist`、`Tests/**`、本目录 README）。暴露面/接缝：`CookieBridge`、`PairingService.pair(bundleJSON:)`（contract §3.2）。消费 `Handoff-Mobile` 框架的 `Bind*` C 函数。 | ①文件集可圈（见 §3.2④）；②契约同冻结；③依赖 XCFramework（单向，图外）；④已标逻辑/边界双型。**通过。** |
| `mobile/bind`（图外·既有嵌套 module） | 边界型（对面是 gomobile 生成面这一外部现实） | **只读消费方（零改动）**：壳唯一的核接缝；七函数 + `Machine` DTO，形状由 `shell_api_golden_test.go` 逐行冻结 | 只读：`mobile/bind/bind.go#Pair`、`session.go`、`adapter.go`、`types.go`、`export_surface_test.go`、`shell_api_golden_test.go`。 | ①不派卡；②导出面已冻（本轮 `go test ./bind/` 实测绿）；③无新边（图外）；④gomobile 面需 macOS 真产物核对（§6）。 |
| `d_transport` → `d_transport_channel` | 边界型（对面是跨机 relay/直连 agentd 现实） | **核侧能力供应方（零改动）**：`internal/mobilecore` 的 `Core.Pair/Activate/Session/ActiveMachine/Origin`、`Core.startLoopback` 供绑定面消费 | 只读：`internal/mobilecore/core.go`（`Core.Activate` `:208`、`Core.Origin` `:333`、`Core.startLoopback` `:393`）、`internal/mobilecore/session.go`（`exchangeTicket` `:76`、`toSessionCookie` `:128`）。 | ①不派卡；②签名由 B369/B392 冻结；③无新边；④核侧行为由既有测试背书，本卡零改。**注意 `codegraph check` 对 `k_mobilecore_*` 报 best-dangling warn（基线既存）。** |
| `d_gateway` | 边界型（HTTP 面，对面是 webview/浏览器） | **cookie 属性唯一生产者（零改动）**：`handoff_session` 名与 `Path/HttpOnly/SameSite/Secure` 出处 | 只读：`internal/agentd/auth.go#sessionCookieName`（`:28`）、`internal/agentd/authroutes.go#sessionCookie`（`:343`）。 | ①不派卡；②wire 不变；③无新边；④服务端签发面已冻结，壳按 contract §3.4 硬编码。 |
| `d_protocol` | 逻辑型 | **配对 wire 编码方（零改动）**：`proto.EncodePairBundle` 产出壳整体透传的 bundle JSON | 只读：`internal/proto/pairing.go#EncodePairBundle`（`:111`）。 | ①不派卡；②B369 冻结；③无新边；④壳不解析 bundle（contract §4.2 条 4 负向断言）。 |
| `d_cli` | 逻辑型 | **配对载体生产者（零改动）**：`handoff console --qr` / `--bundle` 产 QR 载荷/粘贴串 | 只读：`cmd/console.go#runConsolePair`（`:121`）、`#renderBundleQR`（`:279`）、flags `:46-48`。 | ①不派卡；②命令面已冻；③无新边；④B369 已交付，本卡只消费其输出格式。 |
| `d_web` | 逻辑型 | **控制台内容面（零改动）**：webview 加载的已认证控制台由 agentd `-tags embedweb` 提供 | 只读（消费）：`internal/webui` + `web/` 产物。 | ①不派卡；②B369 移动 IA 已确认；③无新边；④内容面不重画，壳只加载回环源。 |
| 构建工具链（Xcode / NDK 30 / JDK 21 / Gradle）——外部现实 | 边界型 | **构建闸对面**：`./mobile/build.sh` + `gradlew` + `xcodebuild` | 外部，不可圈为仓内文件 | ①不派卡；②contract §3.5 冻结命令与版本；③—；④机内可 headless 跑（macOS），真实产物可读（§6）。 |

**不列为本卡实现域（零改动）：** `d_orchestration`、`d_workspace`、`d_sessions`、`d_execution*`、`d_ledger`、
`d_collab`、`d_policy`、`d_scheduling`、`d_keystone`、`d_maintenance`、`d_transport_tunnel`、`d_web_*` 各子域。

### 1.1 竖切债核对（架构法第三条）

- 本卡**零 Go 符号新增/修改**（contract §7.1），不新增任何 Go 前缀家族；`internal/agentd`、`cmd` 的既存超大包
  在本卡**零改动**，不触发升格信号，**不插竖切还债卡**。
- 两个新壳目录是**新建的独立工程目录**（`mobile/android/`、`mobile/ios/`），每个工程内文件虽多，但按
  「一个工具链工程 + 单一职责（薄壳）」可圈出有界文件集；这不是 Go 包的前缀家族，第三条判据不适用。
  **回答：能圈出有界文件集，不插竖切还债卡。**
- 约束：壳子卡**不得**碰 `mobile/bind/**` 生产文件、`internal/mobilecore/**`、`internal/agentd/**`、`cmd/**`、
  `web/**`、`codegraph/**`；若实施中需要，停止并退回协调者重核边界。

### 1.2 图覆盖债

- `mobile/` 全部符号**图外**（扫描排除 `mobile` 前缀），新增的 `mobile/android`、`mobile/ios` 同样图外。
- `internal/mobilecore` 属 `d_transport_channel`，但其符号只在视图 `codegraph/diffs/cards-B369-charter.json`，
  baseline 未吸收；B392 新增的 `Core.Activate`/`Core.Session`/`Core.ActiveMachine`、`mobilecore.SessionCookie`
  **从未入图**——本稿实测 `codegraph resolve "internal/mobilecore/core.go#Core.Activate"` → `vanished`，
  故该符号**不使用 `#Symbol` 锚**，改用普通路径 + 行号。
- 本稿不新增 `codegraph/diffs/<branch>.json`（合法：零 Go 符号，不造空文件）。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- **spec**：`docs/superpowers/specs/b417.md:3` 「**状态：已批准（2026-09-28）**」——逐字核对通过；
  文件在本工作树内，`git merge-base --is-ancestor a9ba59b5 HEAD` 实测 YES。
- **contract**：`docs/superpowers/specs/b417-contract.md:3` 「**上游状态：已批准**」+ `:6` 「**冻结状态：本提交随
  本文件…冻结**」+ `:9` 「**有效基线：** `cards/B417-spec` @ `a9ba59b5`」——与远端 `origin/cards/B417-spec`
  `a9ba59b5` 实读一致；冻结事实由提交 `5894f5ac` 承载（= 当前 HEAD）。
- **图门禁**（本轮新鲜）：`codegraph --repo . check` → exit 0，`fails=[]`（warns 为基线既存 `best-dangling`/
  `budget-raised`/`legacy` 等）；`codegraph --repo . resolve --doc docs/superpowers/specs/b417-contract.md`
  → exit 0（锚均为 `moved`，图外合法）。

### 2.2 契约 §4 原子冻结清单逐条对照（48 条 → 归属）

| 契约 §4 分组 | 条目号 | 归属 / 越界结论 |
| --- | --- | --- |
| §4.1 绑定面消费（壳↔核） | 1–2 | `[T0]` 生成面金样本，既有测试锁定；本稿**不越界**（本轮 `go test ./bind/` 绿）。 |
| | 3–6 | 归 A/B 两卡：①只出现七函数调用；②`Pair` 入参整份、负向无 bundle JSON 解析；③`load` 逐字取绑定面返回值、不拼源；④列表按 `0..<count` 迭代、nil 跳过。 |
| §4.2 I1 配对载荷 | 7–10 | 归 A/B：扫码与粘贴归一入口；`Pair` error 不进列表/不落存储/不加载；原始串透传（仅允许 trim）。 |
| §4.3 I2 凭据持久化 | 11–18 | 归 A/B：iOS Keychain（`WhenUnlockedThisDeviceOnly`，负向 `Synchronizable`）/ Android Keystore 支撑存储、`allowBackup=false`、启动有/无 bundle 分支、`Pair(stored)` 失败回配对页、存储往返逐字节相等、负向不落日志。**条 12 与条 41 的 API 下限疑冲突见 P5。** |
| §4.4 I3 时序（承重） | 19–22 | 归 A/B：严格调用序、切机后旧机 cookie 不再随旧源请求发出、任一步失败不 `load`、同机重复进入不误报。 |
| §4.5 I4 cookie 属性 | 23–31 | 归 A/B：逐键断言 name/path/domain/Secure/HttpOnly/SameSite/无持久 Max-Age；value 逐字来自 `SessionCookie`；条 31 源码常量 guard。 |
| §4.6 I5 fail-closed | 32–35 | 归 A/B：离线不可进入、空值/error 不加载、过期错误态可重试、四类错误按失败调用点 + `Machine.Online` 归类（不解析文案）。 |
| §4.7 I6 零协议逻辑 | 36–37 | 归 A/B（源码 guard）：无自建 HTTP/隧道/ticket；无 bundle 编解码。 |
| §4.8 I7 核生命周期 | 38–39 | 归 A/B：Go 核 App 级单例、启动只 `Pair` 一次；终止 `Close()`；进程回收后走启动路径。 |
| §4.9 构建接线 | 40–46 | 归 A（40/41/42）、B（43/44）、I（45/46）：gradle wrapper、minSdk/compileSdk/签名、`assembleDebug` 产 APK；Xcode 依赖 XCFramework、`xcodebuild` 无 GUI 产 `.app`；构建顺序核心→壳、产物可读。 |
| §4.10 模块与图 | 47–48 | 归 I（对账）：壳目录图外、根 `go list` 不含；`[T0]` 本分支零 `target.json`/`best.json`/视图 diff。 |

**§8 欠账逐条对照**：contract §8 六条（壳工程骨架+编译 / 直通竖切 / 平台 cookie store 语义 / 平台安全存储 API /
无 GUI 构建复现 / UI 文案与重试入口）本稿全部有归属——§8#1/#2 归 I + A/B 的 macOS 执行（P1），#3/#4/#5 归
A/B 在其验收栏显式核对并落 §6 真机清单，#6 归 A/B（四类错误归类 + 可重试，文案由子卡 plan 定稿）。**无一条被静默带走。**

### 2.3 退回 contract（不许边拆边加）

**无强退回。** 逐条核对后未发现「spec 承诺了行为、冻结物里没有载体」的新接缝：壳所需的一切载体
（`mobile/bind` 七函数、`Machine` DTO、回环源形状、cookie 名与属性、bundle wire、`console --qr/--bundle`）
均已存在且非本卡新造。**唯一条件性退回点是 P5**（`minSdk 21` × `EncryptedSharedPreferences` 最小 API
可能互斥）——但本机无 SDK **无法核实**，故不能在此断言冲突；子卡在 macOS 核对后若命中，**暂停并回 contract**，
不得边拆边改契约。**边界澄清（不退回）见 §2.4。**

### 2.4 边界澄清（不退回；须按 P7 回写 contract 修订记录一行）

1. **壳工程与 `mobile/bind` 同属图外子系统**：新壳目录不进 Go 构建图、不触发 `graph check`；壳↔核方向的
   契约形状由 gomobile 生成面冻结，属「图外组装点」情形（与 B369 §2.4 澄清 3 同款）。→ 归 A/B 验收。
2. **I3 的「旧 cookie 不再发送」是行为事实，机内只验调用序与 jar 终态**：单测以假 core + 假 cookie store 断言
   `SwitchMachine → 清罐 → SessionCookie → 注入 → load` 的调用序与「清罐后注入前 jar 空、注入后仅一机 cookie」；
   「发往旧机源的请求确实不带旧 cookie」依赖真实 webview/cookie store，**未验证，需真机/模拟器**（§6）。→ 归 I。
3. **I5 条 34「会话过期」的检测面**：壳通过 webview 的 HTTP 错误回调（iOS `WKNavigationDelegate` 的
   navigationResponse / Android `WebViewClient.onReceivedHttpError`）观测 401/无凭据页，**不解析错误正文**
   （与条 35「不解析文案」一致）；具体回调 API 形状归子卡在 macOS 核对。→ 归 A/B。
4. **`handoff_session` 与 cookie 属性的唯一生产者是 `d_gateway`**（`internal/agentd/auth.go:29`、
   `authroutes.go:343`）；string-only 绑定面**不返回**这些属性，壳按 contract §3.4 硬编码——该跨端属性
   无机内序列化测试，归真机/模拟器（§6）。→ 归 A/B + I。
5. **`mobile/README.md` 存在既存文档漂移**：其「iOS XCFramework 尚未在装了完整 Xcode 的机器上跑过（真机清单
   未验项）」与 spec 备注「工具链 gate 已验（2026-09-28，本机）：…`./mobile/build.sh ios` 与 `android` 均通过…
   B369 `mobile/README.md` 里「iOS XCFramework 未验」的旧读数就此闭合」直接矛盾。属既存漂移，按 P7 订正。→ 归 I。
6. **`minSdk 21` 相关的明文回环许可**（Android API 28+ 默认禁明文；iOS ATS）在 spec/contract 均未点名，
   属壳工程配置必需项；最小放行姿态见 P6，具体 API 归子卡在 macOS 核对。→ 归 A/B。

---

## 3. 子卡清单与依赖 DAG

子卡编号 A/B/I 是提案编号，真实卡号由协调者扇出时分配。

### 3.0 DAG

```text
（前置，已由既有交付满足，不重开）
  contract @5894f5ac：绑定面形状冻结（shell_api_golden_test.go）+ I1–I7 语义 + §4.48 条
  mobile/build.sh（B369 交付）：Xcode+NDK 机器上产 dist/mobile/{aar,xcframework}
  cmd/console.go --qr/--bundle（B369 交付）：配对载体输出
        │
        │  P1 裁决：执行机落地方案（甲=协调者 macOS 本机 subagent-driven / 乙=小队改绑 macOS）
        │  P2 裁决：A/B 并行 vs 串行
        ▼
  ┌─────────────────────┐        ┌─────────────────────┐
  │ B417-A 壳 Android    │        │ B417-B 壳 iOS        │   两者文件集不重叠
  │ mobile/android/**    │        │ mobile/ios/**        │   共享 contract §3.4 只读
  └──────────┬──────────┘        └──────────┬──────────┘
             └───────────────┬───────────────┘
                             ▼
              B417-I 父卡 integrate（macOS；构建闸 + iOS 模拟器直通竖切）
                             │
                             ▼
                     review / acceptance / finish
```

- **前置全为既有交付**：核心构建接线（`build.sh`）、配对载体（`console --qr/--bundle`）、绑定面冻结均已就位，
  故**不插前置卡**；两张壳子卡可立即开工。
- A 与 B **无相互依赖**（文件集不重叠、共享契约只读），DAG 上可并行；可否真正并行取决于 P2 与单机资源。
- I 依赖 A 与 B 都完成（要两端构建物），并在运行中的 agentd 上跑竖切。

### 3.1 B417-A：Android 原生壳（`mobile/android/`）

**①契约引用**：contract §2（图外）、§3.1（绑定面）、§3.3（`WebviewSessionBinder` / `PairEntry.pair`）、
§3.4（cookie 属性与清罐）、§3.5（构建接线）、§4.1–4.9（条 3–46）、§8#1–#6；spec「方案」「用户故事 1–9」
「测试决定 3/4」「实现决定」；本稿 §2.4 澄清 2–4、P5、P6。

**②意图与为什么**：Android 用户当前**无可安装 App**（全仓零 `.kt`）。本卡从零落一个 Kotlin 薄壳承担四件事——
webview 容器、扫码/粘贴配对入口、原始 bundle 入安全存储、webview cookie 桥接（含切机清罐）——
**零协议重实现**，全部经 `bind.Bind` 七函数。为什么薄：spec I6 硬约束，协议在 Go 核；壳只做平台胶水。

**③验收（行为化，按子系统类型分流）**：

*逻辑型（壳内可单测闭环；假 core + 假 cookie store / 假 Keystore，必须能变红）：*
- **构建闸**：`cd mobile/android && ./gradlew assembleDebug` 退出 0，产出
  `app/build/outputs/apk/debug/app-debug.apk`；`aapt dump badging` 读出包名与 `minSdk=21`（contract §4.9 条 42/46）。
- **调用序（I3 承重）**：单测断言严格序 `switchMachine(m) → 清 jar → sessionCookie(m) → 注入 → load(origin)`；
  **乱序即红**；任一步失败（`switchMachine` error / 清罐失败 / `sessionCookie` error / 注入失败）→ **不调用
  `load`**（反面断言）；同机重复进入不进错误态（条 19–22）。
- **cookie 属性逐键**：注入 cookie 的 name==`handoff_session`、path==`/`、host-only domain==`127.0.0.1`、
  `Secure==false`、`HttpOnly==true`、`SameSite==Lax`、无持久 `Max-Age/Expires`、value 逐字等于
  `SessionCookie` 返回值（条 23–30）。
- **配对归一与持久化**：扫码结果与粘贴文本调**同一** `pair(bundleJSON)`；畸形串 `Pair` error → 不进列表、
  不写存储、不加载（条 7–10）；成功后原始 bundle 写入 Keystore 支撑存储；存→取→`Pair` 收到**逐字节同一串**
  （条 11–18）；启动有/无 bundle 两分支 + `Pair(stored)` 失败回配对页（条 15–16）。
- **fail-closed**：离线机（`Machine.Online==false`）可见不可进入、点击不触发 `switchMachine`/`load`（条 32）；
  空值/error 不复用旧 cookie（条 33）；四类错误按失败调用点 + `Online` 归类，**不解析 error 文案**（条 35）。
- **源码 guard（可 grep 断言）**：壳源码只出现七函数调用，**无** `Token`/`Dial`/`Credential`（条 3 负向）；
  无对 bundle 的 JSON 解析（`Gson`/`kotlinx.serialization` 不用于 bundle，条 4 负向）；`load` 的 URL 逐字取
  绑定面返回值（条 5）；cookie 名常量 == `"handoff_session"`（条 31）；壳无自建 HTTP 客户端访问 agentd（条 36–37）。
- **凭据卫生（负向）**：token / cookie / bundle 值不进日志（条 18）；`allowBackup="false"` 或 backup rules
  显式排除安全存储文件（条 14）。
- **模块与图**：`mobile/android/` 图外；根 `go list ./...` 不含它（条 47）。

*边界型（macOS 复核 / 真机，归 §6）：* Gradle/JDK/SDK 真实构建、`EncryptedSharedPreferences` 的实际
`minSdk` 下限与弃用评估（P5）、`CookieManager` 对 `SameSite=Lax`/host-only/`127.0.0.1` 的实际接受度、
明文回环许可生效、真机 webview 收 cookie 过闸。

**④入口指针与有界文件集**：
- `mobile/android/**`（新建）：Gradle wrapper（`gradlew`/`gradlew.bat`/`gradle/wrapper/**`）、
  `settings.gradle.kts`、`build.gradle.kts`、`app/build.gradle.kts`、`app/src/main/AndroidManifest.xml`、
  `app/src/main/java|kotlin/**`（含 `WebviewSessionBinder`、`PairEntry`）、`app/src/main/res/**`、
  `app/src/test/**`、本目录 `README.md`。
- **禁止**：改 `mobile/bind/**`、`mobile/build.sh`、`internal/**`、`cmd/**`、`web/**`、`codegraph/**`。
- 符号锚：本目录**图外，不带 `#Symbol` 锚**；引用核侧用普通路径（如 `mobile/bind/bind.go:47`）。

**缺陷族对抗（逐族结论，同入验收栏）**：见 §5「A/B 共用答案」与「Android 专项」。

### 3.2 B417-B：iOS 原生壳（`mobile/ios/`）

**①契约引用**：contract §2、§3.1、§3.2（`CookieBridge` / `PairingService.pair`）、§3.4、§3.5、
§4.1–4.9（条 3–46）、§8#1–#6；spec「方案」「用户故事 1–9」「测试决定 2/3/4」「实现决定」；本稿 §2.4 澄清 2–4、P5、P6。

**②意图与为什么**：iOS 用户当前无可安装 App。本卡落一个 Swift 薄壳，职责与 A 镜像（webview 容器 / 配对入口 /
bundle 入 Keychain / cookie 桥接）。**为什么两端同语义**：contract I1–I7 是共享壳不变式，两端必须一字不差；
iOS 落 `mobile/ios/` 与 Android 平级。

**③验收（行为化）**：

*逻辑型（XCTest 单测，假 core + 假 cookie store + 假 Keychain，必须能变红）：*
- **构建闸**：`xcodebuild -project mobile/ios/... -scheme <scheme> -sdk iphonesimulator -configuration Debug build`
  退出 0（无签名），产出模拟器 `.app`；产物内含 `Handoff-Mobile.framework`（contract §4.9 条 43/44/46）。
- **调用序（I3 承重）**：单测断言 `BindSwitchMachine(m) → 清 cookie store → BindSessionCookie(m) → 注入 →
  load(origin)` 严格序；**注意 `WKHTTPCookieStore` 的 delete/set 是异步回调**——断言「清罐完成回调先于注入、
  注入完成回调先于 load」（乱序即红）；任一步失败不 `load`（条 19–22）。
- **cookie 属性逐键**（同 A 的条 23–30，经 `HTTPCookie(properties:)`）。
- **配对归一与持久化**：`PairingService.pair(bundleJSON:)` 唯一入口；error 不落存储/不进列表/不加载；
  Keychain item 带 `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`、**负向**不设 `kSecAttrSynchronizable=true`；
  存→取→`Pair` 逐字节相等；启动有/无 bundle 分支（条 7–18）。
- **fail-closed**：同 A 的条 32–35（含模拟器无摄像头时粘贴为唯一路径，扫码入口退化，spec 用户故事 3）。
- **源码 guard**：只出现七个 `Bind*` 调用，无 `Token`/`Dial`/`Credential`；无 bundle 的 `JSONDecoder` 使用；
  `load` 逐字取绑定面返回值；cookie 名常量 guard（条 3–5/31/36–37）。
- **凭据卫生（负向）**：bundle/cookie/token 不进日志（条 18）。
- **模块与图**：`mobile/ios/` 图外；根 `go list` 不含（条 47）。

*边界型（macOS 模拟器/真机，归 §6）：* XCFramework 被 Xcode 真实链接、`WKHTTPCookieStore` 对
host-only 非 Secure cookie 的接受度、明文回环 ATS 放行、模拟器 Keychain 行为、无 GUI 构建复现。

**④入口指针与有界文件集**：
- `mobile/ios/**`（新建）：`.xcodeproj`（或确定生成步骤，仓内无 XcodeGen/Tuist，contract §9.5）、
  `Sources/**`（含 `CookieBridge`、`PairingService`）、`Info.plist`、`Tests/**`、本目录 `README.md`。
- **禁止**：同 A。
- 符号锚：本目录图外，不锚；核侧同 A。

**缺陷族对抗**：见 §5「A/B 共用答案」与「iOS 专项」。

### 3.3 B417-I：父卡 integrate（两端构建物 + iOS 模拟器直通竖切）

**①契约引用**：contract §3.5（构建接线）、§4.9 条 45–46、§4.10；§7.3（直通竖切）、§8#1/#2/#5；
spec「测试决定 1/2」「方案」验收线。

**②意图与为什么**：壳子卡各自只能证明「自己那半」；spec 的最高缝是**跨两端 + agentd** 的端到端：
粘贴真实 `handoff console --bundle` 输出 → `Pair` → 机器列表 → 进入 → webview 加载**已认证**控制台
（能过 agentd cookie 闸）。contract §7.3 明文禁止由并行壳子卡各自伪造这条竖切。为什么必须独立一轮：
这条竖切同时压 I1+I2+I3+I4 与真实 agentd，只有它能证明「壳 + 核 + 网关」整链成立。

**③验收（行为化）**：
- **构建闸（边界型，headless 可复现）**：在干净 macOS 上 `./mobile/build.sh all` → 两端壳构建：
  `gradlew assembleDebug` 产 APK（读出 `minSdk=21`）；`xcodebuild` 产 `.app`（内含 `Handoff-Mobile.framework`）。
  两条命令**无人工 GUI 步骤**（条 40–46）。
- **直通竖切（边界型，最高缝）**：运行中的 agentd + 一台已配对机器；`handoff console --bundle` 产一行 JSON；
  iOS 模拟器内粘贴 → Pair → 列表显示机器与在线态 → 进入 → `SwitchMachine` 后 webview 加载**已认证**控制台
  （非登录页）；切到另一机再切回，webview 不残留旧机 cookie（spec 故事 5/6）。
- **产物可读**：APK 包名/`minSdk` 可由 `aapt`/`apkanalyzer` 读出；`.app` 内含框架（条 46）。
- **图对账**：本分支零 `target.json`/`best.json`/视图 diff；`codegraph check` fails=0 保持。
- **失败态可复现**：至少一轮「配对串损坏 / 机器离线」的竖切观察，确认不加载 webview 且错误可重试（spec 故事 8）。

**④入口指针与有界文件集**：无新增源文件；触碰 `dist/mobile/**`（gitignored 产物）、`mobile/README.md`
（构建/竖切记录，按 P7）；验收证据落 spec 约定台账（如 `docs/superpowers/ledgers/` 或 note）。

**缺陷族对抗**：I 是「真机/边界型」集成卡，见 §5「跨端集成专项」。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的跨子系统可观察行为（用户故事 1–9）。五格齐全、归属存在。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| 首次使用，设备无存储凭据 | 安全存储（iOS Keychain / Android ESP，I2） | 壳启动路径 | 无 bundle → 配对屏（非空白、非错误） | A / B |
| 扫 `handoff console --qr` 二维码 | `proto.EncodePairBundle`（`internal/proto/pairing.go:111`，经 `cmd/console.go:121` 输出）→ `bind.Pair` | 壳配对屏 → 机器列表 | 合法 bundle → 列表；损坏 → 明确错误、不落半态 | A / B（+ I 竖切） |
| 粘贴 `handoff console --bundle` 一行 JSON | 同上（同一份 bundle JSON） | 壳配对屏 → 机器列表 | 同扫码（sim 无摄像头时的法定路径） | A / B（+ I 竖切） |
| 显示机器列表 | `bind.MachineCount`/`MachineAt`（Online 取 `mobilecore.Core.Origin` 成功与否） | 壳列表屏 | 显示名 + 在线/离线；离线可见不可入 | A / B |
| 进入机器 | `bind.SwitchMachine` → `mobilecore.Core.Activate`（`core.go:208`）→ 清 webview 罐 → `bind.SessionCookie` → 注入 → `load(origin)`；cookie 由 `d_gateway` 签发 | webview / agentd 会话闸 | loopback 源加载**已认证**控制台；任一步失败不导航 | A / B（单测）+ I（竖切/真机） |
| 切机 A→B→A | 壳按 I3 清罐 + 核 `Core.Activate` 切槽（`cookie 不按端口隔离`，RFC 6265） | webview cookie jar | 任何时刻一罐只装一机的 cookie；旧机 cookie 不再随旧源请求发出 | A / B（序/终态）+ I（行为） |
| 重启免重扫 | 原始 bundle JSON 存安全存储（I2） | 壳启动路径 → `Pair(stored)` | 免扫码直接进列表 | A / B |
| 配对串损坏 / 离线 / 兑换失败 / cookie 过期 | `bind` error + `Machine.Online` + webview HTTP 错误回调 | 壳错误态 | 四类可归因错误 + 可重试；**绝不加载 webview、不复用旧 cookie** | A / B |
| 全程 | 壳日志 | 运维/用户 | token / cookie / 配对串不落日志 | A / B |

**闭环结论**：每条 spec 承诺行为五格齐全、有归属；无「只活在接口、测试或无人认领格子里的承诺」。
`SessionCookie.Value` 的跨端注入与平台 cookie 属性、真实 webview 行为属外部现实，落 §6，不假装机内可达。

---

## 5. 缺陷族对抗审查（逐族正面回答）

覆盖面 = 两个新壳（`mobile/android`、`mobile/ios`）+ 其消费的接缝 + 构建/集成。通用五族 + 追加设问逐族作答；
「无风险」一律写「无，因为……」。

### 5.1 A/B 共用答案（两端同语义部分）

1. **生命周期 / 状态机中断**
   - **有风险，须处置**：壳内 Go 核是 App 级单例，UI 生命周期（iOS `UIViewController` 重建 / Android
     Activity 旋转与进程回收）不得重复 `Pair` 或建第二个核（条 38 负向）。对策：核单例落在 Application/App 层，
     不随 Activity/VC 重建；`Close()` 只在 App 终止调（条 39）；系统回收后前台重启走启动路径。
   - **清罐/注入的异步中间态**：iOS `WKHTTPCookieStore` 与 Android `CookieManager` 的清/写是**异步**的，
     若不清算完成即加载，会出现「旧 cookie 尚在」或「cookie 未写入即导航」的窗口。对策：以回调/协程把
     「清 → 注入 → load」串成严格序，单测断言序（条 19/21）。
   - 孤儿资源：无后台进程/临时目录；webview 与 Go 核由 OS 随进程回收。

2. **静默失败 / 误导报错**
   - 传播契约：每条绑定面 error 必须映射到四类错误 UI（配对/离线/兑换/过期）+ 可重试，**不得吞成成功**
     （条 33/34）。存在「清罐/注入在 Go 层之外、Go 已返回成功 origin 但壳步骤失败」的窗口——对策：任一步
     失败**不调用 `load`**、不导航，壳呈现可行动错误（条 21）。
   - 条 34 会话过期若退化成空白页即静默失败 → 必须由 webview 错误回调转「过期」错误态（§2.4 澄清 3）。
   - 凭据卫生：日志只可有机器名/长度/错误类别，**禁** token/cookie/bundle 值（条 18）。

3. **跨平台假设**
   - **命中**：明文回环（Android API 28+ 默认禁明文 / iOS ATS）→ 须最小放行（P6）。
   - **命中**：`EncryptedSharedPreferences` 的实际 `minSdk` 下限与弃用（P5，contract §8.4 已认账）。
   - **命中（未验证，需真机/模拟器）**：`WKHTTPCookieStore` 对 host-only、`Secure=false`、IP host cookie 的
     接受度；`CookieManager` 对 `SameSite=Lax` 的支持版本；cookie 名区分大小写与 IP host 匹配。→ §6。
   - 两端 locale/时区无关（壳无时间逻辑）；`127.0.0.1` 在两平台均指设备自身（回环语义成立）。

4. **假红 / 假绿测试**
   - **假绿温床**：单测若用「假 core 只 return 固定串」+「假 jar 只记录最后一次写入」，可能不覆盖「清罐后旧
     cookie 真的不在」——对策：假 jar 必须**真持一个可查询的 cookie 集合**，断言清罐后查询为空、注入后仅一机；
     且断言调用序（乱序即红）。
   - **反面断言**：失败路径必须断言**未调用 `load`**（不是只断言 error 抛出）；离线机点击断言**未调用
     `switchMachine`**。
   - **『换实现会不会无意义地红』**：测试锁的是调用方依赖的行为（调用序、cookie 逐键、fail-closed），不是
     内部帮手；换 webview 封装实现只要序与属性不变仍绿。
   - **夹具行为假设须有真机项对应**：假 jar/假 Keychain 的行为假设（如「删除后立即查询为空」）列 §6 真机核对。
   - 并发/负载：壳为单用户 UI，无高频并发；同机重复进入按条 22 断言不误报。

5. **门禁绕过**
   - 壳**不新增绕过 cookie 闸的写/执行路径**：只调七函数，禁 `Token`/`Dial`/`Credential`（条 3，`export_surface_test.go`
     的负向断言已在绑定面执法）。
   - **安全存储不放宽门**：iOS 禁 `Synchronizable`（条 13）、Android `allowBackup=false`（条 14），防止 bundle
     经云备份/跨设备同步泄露。
   - **TOCTOU**：切机时序的「清罐 → 注入」间存在 jar 短暂为空/旧值的窗口；对策是同一次进入动作内串行且失败
     不导航，不留「清完未注入即加载」的可利用窗口（条 19/21）。
   - 回环门禁（同机任意 App 可连 127.0.0.1）是核侧既有承重属性（B369 冻结），本卡不放松。

6. **序列化边界**
   - **bundle JSON**：Go 编 (`EncodePairBundle`) → QR/粘贴 → Kotlin/Swift 字符串 → `Pair`。壳**不得解析/重组**，
     整体透传（条 4/10 负向）；必须有一条「存 → 取 → `Pair` 收到逐字节同一串」的往返断言（条 17）覆盖
     「字段缺失 vs 零值」——bundle 是整体字符串，可空语义由 Go 侧保证，壳侧只验字节相等。
   - **cookie value**：`SessionCookie` 返回 Go string → 平台 cookie 属性构造。value 可能含 `;`/`=`/空格/百分号，
     是**手拼 cookie 串**（Android `setCookie(url, "name=value; ...")`）的高危点。对策：注入 value 必须来自
     绑定面返回、按平台 API 转义/构造（iOS `HTTPCookie(properties:)` 不做手拼），并加一条含特殊字符 value
     的往返/注入断言。
   - **cookie 属性**：由壳侧硬编码（contract §6 命中 1），是跨端手写序列化的**第二处**；逐键断言（条 23–30）
     覆盖「属性缺失 vs 值为 falsy」（如 `Secure=false` 必须显式写入而非省略后平台默认）。

7. **枚举新值过既有白名单**
   - **无，因为**本卡不引入新的跨进程枚举取值；四类错误是壳内部分类（由失败调用点 + `Online` 推出），
     不流经任何既有校验器/白名单/switch。唯一字面量是 cookie 名（既有 `"handoff_session"`，条 31 guard）与
     `SameSite=Lax`（contract §3.4 冻结）。**须保证错误分类的 `when`/`switch` 无 default 吞值**，未知情形
     归入可重试错误而非静默。

8. **承重安全属性有测试锁住**
   - **一罐只装一机 / 不串机**：单测断言清罐后 jar 空 + 注入后仅一机 + 调用序（能红）；真实行为归竖切/真机。
   - **fail-closed（不加载/不复用旧 cookie）**：单测反面断言（能红）。
   - **凭据不落日志 / 不云同步**：源码 guard（grep 无 token/bundle 日志）+ iOS `Synchronizable` 负向 +
     Android `allowBackup=false`（能红：把属性改掉即断言红）。
   - **cookie value 不伪造**：断言 value 逐字来自绑定面（能红）。
   - **结论**：每条属性均需一支**能变红的测试**锁住，而非「实现里恰好为真」。

### 5.2 Android 专项

- **跨平台**：`minSdk 21` 的实际运行面窄（release 前小）→ 但 contract 冻结，不扩权。
- **门禁**：`android:allowBackup="false"`；`usesCleartextTraffic` 最小化（P6）；`WebView` 须 `setJavaScriptEnabled`
  仅对回环源、禁 `addJavascriptInterface` 暴露凭据；`WebViewClient` 的 `onReceivedHttpError` 用于过期检测。
- **生命周期**：Activity 旋转不得重建 Go 核或重复清罐/注入——核与配对态放 `Application`/`ViewModel`；
  `WebView` 在配置变更时妥善处理（避免 `destroy()` 后继续 load）。
- **假红/假绿**：`EncryptedSharedPreferences` 在单测里不可用（需真实 Keystore）→ 单测以接口替身锁调用与
  往返语义，真机/仪器测试验真实存储（§6）。

### 5.3 iOS 专项

- **跨平台**：ATS/明文回环放行（P6）；`WKWebsiteDataStore.httpCookieStore` 的异步语义；模拟器无摄像头 →
  粘贴为唯一配对路径（spec 故事 3）。
- **门禁**：Keychain `kSecAttrAccessibleWhenUnlockedThisDeviceOnly` + 禁 `Synchronizable`；`Info.plist`
  不放行任意明文。
- **生命周期**：`WKWebView` 与 `PairingService` 生命周期绑 App/单 VC；后台恢复不重 `Pair`；进程终止调 `Close()`。
- **假红/假绿**：模拟器 Keychain 与真机 Keychain 行为可能不同（如模拟器 keychain 不持久化于设备锁语义）→
  模拟器测试只锁调用与属性设置，真机行为列 §6。

### 5.4 跨端集成（I）专项

- **假绿**：竖切若只断言「webview 有内容」而不校验**已认证**（能过 cookie 闸），会假绿——必须断言加载的是
  已认证控制台（非登录页）。
- **夹具**：竖切必须用**真实** `handoff console --bundle` 输出与运行中的 agentd，禁手抄 bundle。
- **生命周期**：模拟器/agentd 中断后重跑须可复现；构建产物与 `dist/` 不被提交。
- **门禁**：竖切全链不得出现绕过 cookie 闸的注入（协议零重实现的端到端证明）。

---

## 6. 真机清单（全部「未验证，需真机/模拟器」或「机内欠跑」，按归属执行）

1. **壳构建闸（macOS，P1 决定执行机）**：`./mobile/build.sh all` + `gradlew assembleDebug` + `xcodebuild`
   在干净 macOS 上无 GUI 复现；gradle wrapper 完整性（contract §8.5）。**本工作树 linux 无 JDK/Xcode/SDK，未跑。**
2. **iOS 模拟器直通竖切**（contract §7.3 / §8.2）：真实 bundle → Pair → 列表 → 进入 → 已认证控制台；含切机
   不串 cookie 与四类失败态。**未跑，归 B417-I。**
3. **平台 cookie store**：`WKHTTPCookieStore` 对 host-only、`Secure=false`、IP host（`127.0.0.1`）cookie 的
   接受度；`CookieManager.setCookie` 对 `SameSite=Lax` 的支持版本；清罐尽调和 `load` 的时序（contract §8.3）。**未验证。**
4. **平台安全存储**：Keychain `kSecAttrAccessibleWhenUnlockedThisDeviceOnly` 实际行为；`androidx.security:security-crypto`
   版本、`EncryptedSharedPreferences` 的 `minSdk` 下限与弃用评估（P5 / contract §8.4）。**未验证。**
5. **明文回环许可**：Android network-security-config / iOS ATS 放行使 `http://127.0.0.1` webview 加载生效（P6）。**未验证。**
6. **Android 真机运行时**（spec Out of Scope：本期只到构建物；装起来跑/切机/失败 UX 挪后续期）——**明确 OOS**，不计入本卡验收，但记账。
7. **gobind 真产物形状**：`TestGomobileSurfaceHasNoSkips` 需 gobind；本轮 `go test ./bind/` 在 linux 实测绿（8.9s），
   但**真实 AAR/XCFramework 生成与壳链接**归 macOS 构建闸（#1）。

---

## 7. 出稿自检

- [x] **产出四样齐全**：§1 子系统清单每个带 best.json 类型（图外新建两项并标逻辑+边界双型）；§2 契约增量
      逐条有结论（§2.3 无强退回、P5 条件性退回、§2.4 六条边界澄清）；§3 A/B/I 三张卡全部四段式且判据行为化；
      §5 缺陷族逐族含「无，因为……」。
- [x] **「待拍板」岔口集中**：P1–P7 全在 §0，正文回指。
- [x] **「未验证，需真机」汇总**：§6 七条（含 1 条显式 OOS）。
- [x] **每张子卡有界文件集核过**：A=`mobile/android/**`、B=`mobile/ios/**`、I=无新增源（产物+验收记录）；
      圈得出，无需插竖切还债卡。
- [x] **行为闭环每行五格完整**：§4，归属存在；无无人认领格子。
- [x] **契约状态位**：spec「已批准」、contract「冻结状态」实读在位（§2.1）。
- [x] **未亲自跑到结果的命令未写成结论**：本轮亲跑 `go test ./bind/`（绿）、`codegraph check`（fails=0）、
      `codegraph resolve`（contract 锚 moved / `Core.Activate` vanished）、`git merge-base`（YES）、
      `find`（零 `.kt`/`.swift`）；平台 API 事实（P5/P6/§6）一律标未验证。
- [x] **收尾**：`codegraph resolve --doc docs/superpowers/specs/b417-breakdown.md` 亲跑（结果落台账）；坏锚即修。

---

## 8. 拍板记录区（协调者回填；本稿当前「待拍板」）

| 编号 | 裁决 | 理由 |
| --- | --- | --- |
| **P1** | 待回填 | 待回填 |
| **P2** | 待回填 | 待回填 |
| **P3** | 待回填 | 待回填 |
| **P4** | 待回填 | 待回填 |
| **P5** | 待回填 | 待回填 |
| **P6** | 待回填 | 待回填 |
| **P7** | 待回填 | 待回填 |

（裁决后逐条回写；头部状态行同步改「已拍板（日期）」；裁决与回写同批提交。状态行与裁决记录不一致视同未拍板。）
