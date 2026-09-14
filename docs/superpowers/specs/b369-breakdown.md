# B369 拆解提案：移动端 App（webview 薄壳 + gomobile 连接核）

**状态：已拍板（2026-09-14）——P1–P6 全案 A（P3 约束：键条不得再造 `terminal*` 第 6 个源文件；P5 壳工程不进本仓，S1 壳侧孪生归真机）**
**卡：** B369（L3 重档；contract 已冻结 @ `6f10ba7c`，Ticket 0 骨架与直通竖切在工作树）
**上游 spec：** `docs/superpowers/specs/2026-09-13-mobile-app-design.md`（头部状态：**已批准**，本稿逐字核对过文件头）
**冻结 contract：** `docs/superpowers/specs/b369-contract.md`（头部状态：**已冻结**；本稿 §2.2 核对结论 = **无退回项**，§2.4 三条边界澄清已回写契约末尾修订记录一行）
**有效基线：** `cards/B233.1-charter-7` @ `977720e2`（本卡合并目标）
**本稿台账：** `docs/superpowers/ledgers/2026-09-14-b369-breakdown-ledger.md`
**图依据：** `codegraph/best.json`（`parent` 为空的顶层领域 = 子系统清单）；Ticket 0 新符号在 `codegraph/diffs/cards-B369-charter.json`（view=`cards-B369-charter`）
**角色边界：** 本文全部是提案。不写实现代码、不建卡、不派发、不调派发工具、不做卡号分配；拍板与扇出归协调者。

---

## 0. 待拍板岔口（集中清单）

拍板者按本表逐条裁决；正文岔口一律回指本表。裁决后逐条回写裁决与理由，头部状态改「已拍板（日期）」。

1. **P1｜移动端底栏四 tab（会话/卡/项目/设置）的形态归属。**
   spec 实现决定把底栏定为「会话 | 卡 | 项目 | 设置」四 tab，「卡」升一级镜像桌面工作项账本 v2（`pages/board.html`），「设置」含配对清单入口。现状 web 无移动专用 IA：`App.tsx` 只有 `*`→`Shell` 一条路由，`Shell.tsx` 的 `fullPageRoute = ['/cards','/flows','/settings','/machines','/codegraph']` 是桌面整页路由集合（`web/src/app/shell/Shell.tsx:491`），四个一级 tab 全是新增内容面。
   - **方案 A（推荐）：新增移动壳内容子卡，作为 web 响应式断点谱系的一部分，不新开路由树**——底栏四 tab 用既有 `/cards`、`/settings`、会话左栏、项目树复用；移动断点下切换为底栏导航（同一份 Shell，条件渲染）。理由：spec「UI 一份、桌面/移动永不漂移」；`/cards` 已是工作项账本页，会话/项目已有真实页面，移动端只是布局变体。
   - **方案 B：另开 `/m` 移动路由树**（独立 Screen 组件族）。理由：移动 IA 与桌面分叉更干净；代价是两套 IA 并行、违背「一份产物」论据（ADR 0009），且 spec 明言「响应式覆盖」不是新树。
   - 归属：本岔口影响 S6 的有界文件集，拍板前 S6 只能写到「响应式断点与底栏导航」形态层。
   - **裁决（2026-09-14 协调者）：A。** spec 明言「UI 一份、响应式覆盖」；另开 `/m` 树是第二套 IA，违背 ADR 0009。底栏是同一份 Shell 的条件渲染，复用 `/cards`、`/settings`、会话左栏、项目树。
2. **P2｜「卡」tab 的「需要你」浮顶 + 验收通过走 card move gate 的操作面是否属本期。**
   spec 用户故事 4 明列「进卡详情逐条对照证据验收、评论 #引用成链、验收通过走 card move 过工作流 gate（`cmd/card_records.go:6` 边界）」。现状 `/cards` 页操作面在 `web/src/app/cards/`（`CardsPage`/`flows`），非移动专用；移动端是否只做只读镜像 + 已存在的 move 动作，还是要新增移动验收交互，规格未定。
   - **方案 A（推荐）：本期只做移动端布局适配 + 复用既有卡片操作（move/评论入口），不新增账本写入面。** 理由：spec「卡 tab 镜像桌面工作项看板 v2」是呈现层承诺；账本写入面（move gate）是网关/账本域既有能力，移动端零改动即达。
   - **方案 B：为移动端新增专用验收/评论组件与接口。** 理由：触屏交互更顺手；代价是新增 d_web 写路径与 d_gateway 端点，触发门禁族复审，且 spec 未点名这是移动端增量。
   - **裁决：A。** 用户故事 4 的 move gate 是网关/账本既有能力，移动端零改写入面即达。本期不新开 d_web 写路径、不新开 d_gateway 端点。
3. **P3｜移动终端输入层（特殊键条 + IME 组合）的载体与验收基。**
   spec 实现决定要求「终端输入层含特殊键条与 IME 组合，沿用 08-11 评审积累的输入法补丁认知（`docs/superpowers/reviews/2026-08-11-desktop-form-factor-assessment.md:102`）」。现状 `terminalInput.ts` 只处理 WKWebView Option 组合键（`web/src/app/workbench/terminalInput.ts:18`），无移动 IME/键条组件；`TerminalTab.tsx` 732 行，无响应式键条。
   - **方案 A（推荐）：S6 内新增移动键条组件 + IME 输入路径，验收以 `terminalInput` 既有测试体系 + 新增组件测试收口；真机 IME 走真机清单。** 理由：spec 明列一期主干；与既有终端输入修正同文件族，属 d_web_workbench 有界文件集。
   - **方案 B：拆独立子卡（web 终端输入层专卡）。** 理由：终端输入是脊柱级风险点、可独立验；代价是 S6 与输入卡共享 `TerminalTab.tsx` 同一文件，需 plan 定文件边界。
   - 归属：影响 S6 是否拆分为 S6a（响应式与 IA）+ S6b（终端输入层）。
   - **裁决：A。** 终端输入是一期主干，但与四 tab 同属一份 UI 产物、同碰 `TerminalTab.tsx`；拆 S6b 会造两张卡抢同一文件。约束：键条做成独立组件且**不得**再造第 6 个 `terminal*` 前缀源文件（用 `MobileKeybar` 等非 terminal 前缀）；IME 真机走 §5 #4。
4. **P4｜程序化 ticket→cookie 兑换与 cookie jar 桥接的实现载体归属（移动核 vs 壳）。**
   contract §3.4 明言「本节点只落反代与端口隔离；程序化兑换与 cookie jar 桥接是越过空壳的可观测行为，列欠账 §8.2」，且「每机独立回环端口 + 切机清 webview 会话罐」在 spec 选定的语义。但兑换发生在 Go 核（`internal/mobilecore`）还是壳侧（Kotlin/Swift 的 cookie jar / WKWebView 的 `WKHTTPCookieStore` / Android `CookieManager`）属未定实现选择。
   - **方案 A（推荐）：兑换逻辑在 Go 核（`internal/mobilecore` 新增 ticket→cookie 程序化兑换），cookie 注入 webview 由绑定面（`mobile/bind`）暴露给壳，壳调平台 cookie API。** 理由：协议逻辑零重实现、核侧可机内测试（fake agentd 返回 Set-Cookie）；壳侧只做薄桥接。
   - **方案 B：核只吐 ticket URL，壳自行 HTTP 兑换并管 cookie jar。** 理由：壳与 webview cookie 同进程更方便；代价是协议逻辑（302 语义、cookie 属性、每机一罐）落到两侧壳，各自重实现。
   - 归属：影响 S2（连接核）与 S4（壳绑定面）的文件边界；拍板前 S2 不含兑换实现。
   - **裁决：A。** 协议逻辑零重实现是 spec 硬约束；302 / Set-Cookie / 每机一罐若落到 Kotlin+Swift 就是两侧重实现。核内兑换可机内测；壳只调平台 cookie API。S2 含兑换实现，S4 暴露桥接方法、不暴露 token。
5. **P5｜壳骨架（Kotlin/Swift 工程）是否在本仓与本期落盘。**
   spec 说壳职责四件（webview 容器、扫码配对、凭据入安全存储、推送 token 预留），但本仓现状零 Kotlin/Swift（`find` 无 `.kt`/`.swift`；`mobile/` 只有 `bind/bind.go`）。spec 真机验证形态是 sideload/TestFlight，商店上架 OOS。
   - **方案 A（推荐）：壳工程不进本仓本卡**——`mobile/bind` 是唯一交接面；壳（Android Studio / Xcode 工程）由协调者在真机侧另办，本卡只保证绑定面可编（AAR/XCFramework 形态由工具链 gate 回执支撑）。理由：有界文件集圈不出跨工具链的壳工程；contract §5 明言「移动核组装点是 `mobile/bind/bind.go`，图外」，壳侧未列组装点。
   - **方案 B：本卡新增 `mobile/android/` + `mobile/ios/` 壳骨架（各自构建文件 + 极薄源码）。** 理由：交付更完整；代价是引入 Gradle/Xcode 构建链，机内 Linux 无法验证，且远超「有界文件集」。
   - 归属：拍板前 S4 只写绑定面（`mobile/bind`），不承诺壳工程。
   - **裁决：A。** 圈不出跨 Gradle/Xcode 的有界文件集，linux 工作树也无法验。`mobile/bind` 是本卡唯一交接面；壳工程由协调者真机侧另办。后果：S1 的 Kotlin/Swift 解码测试**不进本仓**，只落共享 JSON fixture；壳侧孪生归真机清单 #1。
6. **P6｜token 轮换操作文档的归属（spec 用户故事 8 / contract §8.9）。**
   contract §8.9 明言「token 泄露唯一处置 = 双端轮换节点 token 并重启；现状无文档，随本期 contract/文档段产出」，但 contract 本节点未产出该文档，列为欠账。本稿 §4 行为闭环核对发现它是唯一无归属的 spec 承诺。
   - **方案 A（推荐）：并入 S3 的 `mobile/README.md`（与本卡交付物同批提交）。** 理由：与构建/凭据卫生文档同处，`mobile/` 是本卡最自然的凭据主题落点；不新开子卡。
   - **方案 B：单开一张纯文档子卡（`docs/superpowers/` 下独立文档）。** 理由：文档生命周期独立、便于检索；代价是多一张卡的开销。
   - 归属：拍板前该承诺在闭环表标注「无归属（待 P6）」。
   - **裁决：A。** 不单开文档卡。轮换操作文档并入 S3 `mobile/README.md`（与构建/凭据卫生同处）。§4 闭环该行归属改为 S3。

---

## 1. 触及子系统清单与派卡资格核验

子系统 id 与类型逐字取自 `codegraph/best.json` 的 `domains`（`parent` 为空 = 子系统；`type` 即逻辑/边界标注）。本卡**不新增顶层子系统**——contract §2.1 已拍板移动连接核沉进 `d_transport`（具体容器 `d_transport_channel`），配对 wire 沉进 `d_protocol`；本稿复核后**不推翻该拍板**（见 §1.1 末条）。扇出前按架构法第一条**派卡资格四条**逐个核。

| 子系统（best id） | 类型 | 本卡有界文件集与暴露面 | 派卡资格四条核验 |
|---|---|---|---|
| `d_protocol` | 逻辑型 | `internal/proto/pairing.go`（Ticket 0 已落）+ `pairing_fixture_test.go`；本卡实现节点若动，仅补 Kotlin/Swift 孪生金样本的 Go 侧对照（§8.4）。暴露面 = `PairBundle`/`PairRelay`/`PairMachine`/`PairTicket` + `Encode/DecodePairBundle` + `ErrPairVersion`/`ErrPairMalformed`（已冻结）。 | ①一条路径规则圈得出（`internal/proto/pairing*.go`）；②导出面已冻结且金样本锁形状；③对同级域无新边（proto 是 wire 唯一处）；④Go 金样本 roundtrip + 拒收面机内闭环。 |
| `d_transport` / `d_transport_channel`（移动核容器） | 边界型（接缝对面是 relay 服务端与对端 agentd 的外部现实） | `internal/mobilecore/core.go`、`proxy.go`（Ticket 0 已落）+ `core_test.go`；实现节点新增兑换与切机清罐（§8.2 欠账）。暴露面 = `New`/`Core.Pair`/`Core.Origin`/`Core.MachineNames`/`Core.Close` + `DefaultDial`/`DialFunc` + `PairResult`/`PairedMachine`（已冻结）。 | ①按目录规则圈定（`internal/mobilecore`）；②导出面已冻结；③对 `internal/relay`/`internal/client` 均同属 `d_transport`，域内边不执法；对 `d_protocol` 走既有方向（target 已补 entry）；④契约形状机内可验（fake relay 竖切），真实 relay/agentd 行为归真机。 |
| `d_transport` / 绑定面（图外） | 边界型（gomobile 工具链 + 各平台壳的运行时现实） | `mobile/bind/bind.go`、`mobile/go.mod`、`mobile/go.sum`（嵌套独立 module，图外）；实现节点补 `tool golang.org/x/mobile/cmd/gobind` 与构建脚本/文档（§8.1/§8.6）。暴露面 = `Pair`/`Origin`/`MachineNames`/`Close`。 | ①有界（`mobile/` 整个嵌套 module）；②导出面四方法；③反向依赖禁止（根模块不 import `mobile/`）；④编译形状机内可验（`go build ./...`），AAR/XCFramework 与真机归真机清单。 |
| `d_gateway` | 边界型（HTTP 面，对面是浏览器/webview 与 CLI） | 本卡若为 §8.2 兑换加测试夹具不改网关；`internal/agentd/authroutes.go`（既有 ticket/cookie 路由，本卡只读复核，不改）。**本卡无网关新增端点**。 | ①无本卡新增文件（兑换走既有 `/api/auth/tickets` + `/console`）；②路由与 cookie 属性已冻结；③无新边；④既有 auth 测试体系；真实 webview cookie 行为归真机。 |
| `d_cli` | 逻辑型 | `cmd/console.go`（改：加 `--qr`/`--bundle` 与组装）+ `cmd/console_test.go`（改）；暴露面 = cobra 命令 stdout/退出码契约（`--print-url` 恰好一行 URL，`cmd/console_test.go:14`）。 | ①命令文件集有界；②命令面可枚举（flag 增补）；③直调 `newTargetClient`（`cmd/root.go:237`）与 `proto.EncodePairBundle`，DAG 无新边（`d_cli→d_protocol` 预算 1、`d_cli→d_transport` 预算 12 已在 target）；④Go 命令测试闭环。 |
| `d_web`（含 `d_web_shell`/`d_web_workbench`/`d_web_command`/`d_web_cards`/`d_web_admin`） | 逻辑型 | `web/src/app/shell/Shell.tsx`（响应式断点 + 底栏导航）、`web/src/app/rooms/`（会话移动镜像）、`web/src/app/board|cards/`（卡 tab 镜像）、`web/src/app/tree|projects|files/`（项目 tab）、`web/src/app/settings/`（设置·配对入口）、`web/src/app/workbench/terminalInput.ts` + `TerminalTab.tsx`（终端输入层）、`web/src/app/lib/desktopShell.ts`（壳探测，移动端探针）；各自 `*.test.ts(x)`。暴露面 = 页面/组件与既有 API 模块。 | ①按组件族圈定（`web/src/app/` 下四族）；②消费既有 `/api` 冻结 DTO；③只依赖 gateway HTTP，DAG 无新边；④vitest 组件/金样本机内闭环，真实浏览器/webview 归真机。 |

**不列为本卡实现域：** `d_orchestration`、`d_ledger`（卡账本写入在既有网关/账本面，移动端只消费）、`d_sessions`（PTY 引擎在 agentd，移动端经既有 WS 消费）、`d_policy`（配置与纪律不因移动端改）、`d_scheduling`、`d_execution`、`d_keystone`、`d_collab`、`d_workspace`、`d_maintenance`。

### 1.1 竖切债检查（架构法第三条）

- `internal/mobilecore` 源文件 = 2（`core.go`/`proxy.go`）+ 测试 1，远低于第三条阈值（前缀 ≥5、单包 ≥40、单元 >2~3 万行），**无竖切债**。
- `web/src/app/workbench/` 已是独立子目录包（非层式），`terminal*` 前缀 10 个文件**含测试**——非测试源 `terminalInput.ts`/`terminalWheel.ts`/`terminalDebug.ts`/`terminalHostResponse.ts`/`terminalOsc52.ts` = 5 个，命中第三条判据 1。**须显式回答**：`terminal` 家族仍能圈出有界文件集（全部在 `web/src/app/workbench/` 一个目录、单一职责组「终端 I/O 修正」），故**不插竖切还债卡**；但 S6 动 `TerminalTab.tsx` 与 `terminalInput.ts` 时必须保持该目录单一职责，不得把移动键条塞进 `terminalInput.ts` 之外再造第 6 个 `terminal*` 源文件（plan 落地时按此约束）。
- `cmd/`、`internal/agentd/` 扁平大包：本卡在 `cmd/` 只新增 `--qr`/`--bundle` 两个 flag（文件集 = `cmd/console.go` 一个），不触发升格；`internal/agentd` 本卡零新增源文件。
- **mobile 核不新立 `d_mobile`**：contract §2.1 + §6 命中 1 已拍板沉进 `d_transport_channel`。本稿复核确认：`internal/mobilecore` 就 2 个源文件、职责 = 选路客户端包（与 `internal/client` 同构），四条派卡资格齐、无独立子系统职责（壳生命周期/推送/多设备凭据均 OOS 或欠账）。**本稿结论 = 不新立域，不推翻 contract 拍板**；若协调者主张新立 `d_mobile`，那是对 contract §2.1/§6 的复议（须先回 contract，含全量 baseline 重扫代价），不得由本拆解静默变更。

### 1.2 图覆盖债

- 本卡触及面主体**已入图**：Ticket 0 新符号在 `codegraph/diffs/cards-B369-charter.json`（view 相对 diff，base `977720e2`）。实测 `codegraph sym <名> --view cards-B369-charter`：`Core`/`Core.Pair`/`EncodePairBundle`/`DecodePairBundle`/`newReverseProxy` 均命中。
- `mobile/`（`mobile/bind`、`mobile/go.mod`）是嵌套独立 module、**图外**（与 `desktop/` 同例），扫描配方已排除 `mobile` 前缀（`scripts/codegraph-rescan/main.go:231`）。实现节点的图对账（recon）须把本卡新增/修改符号补进视图 diff；`mobile/bind` 永在图外，不补。
- 实现节点新增符号（兑换函数、CLI `--qr` 组装、web 移动组件）为**未来首建**，图覆盖债随各自子卡记：web 与 `cmd` 符号入视图 diff；`internal/mobilecore` 新符号入 `best.json` 的 `k_mobilecore_*` 容器（或视图 diff）。
- **边界澄清（不退回 contract，已回写契约修订记录）**：`mobile/bind/bind.go` 是 contract §5 明言的移动核组装点且图外——其导出面变更（如为 §8.2 加兑换桥接方法）不触发 `graph check`；这是「组装点在目标图登记、其出边豁免」之外的**图外组装点**情形，记入契约修订供后续图对齐时补登记。

---

## 2. 契约增量核对

### 2.1 上游状态位

- spec 头部 `2026-09-13-mobile-app-design.md:4`「状态:**已批准**(2026-09-13 用户批准形态;同日三轴独立审计,修订已落)」——文件头逐字核对通过，引用有效。
- 契约头部 `b369-contract.md:3`「**上游状态：已批准**」+ `:6`「**冻结状态：本提交随 … 冻结**」+ `:7`「**有效基线：** `cards/B233.1-charter-7` @ `977720e2`」——与 HEAD `6f10ba7c`（契约提交）一致，核对通过。
- `codegraph/target.json`：`d_transport→d_protocol` 的 entry 已补 `"proto（包级函数）"`、B369 注记在案（`codegraph/target.json:402`）；预算 `legacyBudget:0` 未改。实测 `python3` 读数：`d_transport→d_protocol budget=0 entries=['proto 实体','proto（包级函数）']`——与契约 §2.2/§5 一致，无新方向、无预算变化。
- `codegraph/diffs/cards-B369-charter.json` 在库（view=`cards/B369-charter`，base=`977720e2`，24 nodesAdded + 5 containersAdded + 8 edges），`codegraph validate --view cards-B369-charter` issues=null、`check --view` fails=0、基线 `check` fails=0（本稿实测，见台账）。

### 2.2 契约 §4 原子冻结清单逐条对照（拆解吸收位置）

| 契约 §4 条目 | 类型 | 本稿归属 / 越界结论 |
|---|---|---|
| 1–2 信封版本键 / roundtrip 恒等 | T0 已锁 | S1（wire 层回归保持绿）；不越界。 |
| 3–9 版本/畸形/双形态/缺端点拒收 | T0 已锁 | S1；不越界。 |
| 10 Go 与 Kotlin/Swift 解码逐键一致 | 台账锚在 Go 侧 | S1（Go 金样本）+ S4（壳侧孪生样本，§8.4 欠账）；不越界——契约明言孪生样本归欠账。 |
| 11 token 熵闸在 `DefaultDial` 执法 | 实现 | S2；契约已冻结执法位置，不越界。 |
| 12 `New(nil,…)` 用 `DefaultDial` | T0 已锁（源码守卫） | S2；不越界。 |
| 13 relay 形态走 `NewDialer`+`NewRelay` | T0 竖切 | S2；不越界。 |
| 14 直连形态走 `client.New` | T0 | S2；不越界。 |
| 15 单机失败标 offline、不整单失败 | T0 | S2；不越界。 |
| 16 在线机 `Origin` 形如 `http://127.0.0.1:<port>` 每机独立 | T0 | S2；不越界。 |
| 17 反代不注入 Authorization | T0（变异可红） | S2；不越界。 |
| 18 上游 Host 是 loopback 名 | T0 | S2；不越界。 |
| 19 重复扫码幂等、不泄漏旧资源 | 实现 | S2；不越界。 |
| 20 `Close` 幂等、关闭后拒绝 | 实现 | S2；不越界。 |
| 21 解码失败不登记任何机器 | 实现 | S2；不越界。 |
| 22 `mobile/` 独立嵌套 module、根 `go list` 不含 | 事实 | S4；不越界——实测根 `go list ./...` 不含 `/mobile`。 |
| 23 根模块零 import `mobile/` | 事实 | S4；不越界。 |
| 24 `internal/mobilecore` 不 import `agentd`/`collab`/`ledger` | 事实 | S2；不越界——实测 `mobilecore` import 仅 relay/client/proto/stdlib。 |
| 25–27 图三闸 + diff 记录 | T0 | S5（本节点不产代码，归实现节点对账）；不越界。 |
| 28–29 工具链 gate / `tool gobind` 指令 | gate 已回执（darwin） | S4（§8.1 欠账）；不越界。 |
| 30–32 回环门禁与多机会话域（语义冻结） | 欠账 | S2（兑换与清罐，§8.2）+ S4（cookie jar 桥接）；不越界——契约明言归欠账。 |

**§8 欠账逐条对照**：契约 §8 十条欠账本稿全部有归属（S1–S6，见 §3.1 DAG 与各子卡①）；**无一条被静默带走**。

### 2.3 退回 contract（不许边拆边加）

**无。** 本稿逐条核对 contract §4（32 条）与 §8（10 条欠账）后，未发现「spec 承诺了行为、冻结物里没有载体」的新接缝：

- 移动核沉进 `d_transport_channel` 是 contract 拍板（§2.1 + §6 命中 1），本稿复核确认四条派卡资格齐，**不退回**；
- 回环反代不注入凭据是 contract 拍板（§6 命中 2），S2 验收沿用；
- 程序化兑换/cookie jar 桥接、CLI `--qr`、Kotlin/Swift 孪生样本、离线补配、轮换文档、web 响应式与终端输入层全部在 contract §8 有欠账条目，**载体（签名/协议/路由）已冻结或明确归实现节点选择**，无需新接缝。

### 2.4 边界澄清（不退回，已回写契约修订记录一行）

1. **配对 wire 的 JSON 金样本是契约面、不是包内 API**：`PairBundle` 的编码字节形状跨语言（Go 编 / Kotlin·Swift 解），`pairing_fixture_test.go` 中的键集与枚举字面值是**跨进程契约**的一部分（契约条 10）；而 `Validate`/`Encode`/`Decode` 的函数签名是包内 API，可随实现微调（如错误包装）只要哨兵 `errors.Is` 不变。→ 归 S1 验收（键集/哨兵断言），S4 只补孪生样本。
2. **`d_transport→d_protocol` 的 entry「proto（包级函数）」覆盖 `Encode/DecodePairBundle`**：实测 target 中该 entry 已存在且预算 0；`internal/mobilecore` 调 `proto.EncodePairBundle` 属该 entry 覆盖范围，不新增方向、不加预算。→ 归 S2/S5 对账。
3. **`mobile/bind` 是图外组装点**：contract §5 将移动核组装点记为 `mobile/bind/bind.go`（图外）；其导出面变更不触发 `graph check`（嵌套 module 不进根构建图），但绑定面形状是壳的契约面。→ 归 S4 验收（`go build` 与导出面断言），不退回契约。

以上三条已回写 `b369-contract.md` 末尾「修订记录（breakdown 出稿轮，2026-09-14）」一行。

---

## 3. 子卡清单与依赖 DAG

序号 S1–S6 是提案编号，真实卡号由协调者扇出时分配。

### 3.1 DAG

```text
（协调者：P1–P6 拍板）
     │
S1 配对 wire 回归与孪生样本（d_protocol，无前置）
     │
S2 连接核：兑换 + 切机清罐 + 离线补配（d_transport_channel，依赖 S1 的冻结 wire）
     │
S3 工具链 gate 落地与构建脚本/文档（mobile/ 图外，依赖 S2 的绑定面稳定）
     │
S4 壳绑定面收口（mobile/bind，依赖 S2、P4 拍板）
     │
S5 CLI `console --qr`（d_cli，依赖 S1 的 Encode + 既有 IssueAuthTicket）
     │
S6 web 响应式 + 四 tab IA + 终端输入层（d_web，依赖 S2 的 Origin 与 P1/P3 拍板）──> integrate（全量真机走查）
```

S1 与 S5 可并行（S5 只依赖 `proto.EncodePairBundle` 与既有 `IssueAuthTicket`）；S2 依赖 S1 的冻结 wire（当前已冻结，故 S2 可与 S1 并行，S1 只作回归保持）；S6 的内容面与 S2 的 `Origin` 契约可并行（契约已冻结）。

### 3.2 S1（d_protocol）：配对 wire 回归与跨语言孪生样本

**①契约引用**：contract §3.1、§4.1（条 1–11）、§8.4；spec §契约语义（信封版本/容量/双形态/部分 bundle）；本稿 §2.4 澄清 1。

**②意图与为什么**：Ticket 0 已落完整 wire 与 Go 金样本；本卡的 wire 层工作只有两件——把 Go 侧金样本保持为跨语言演进锚（回归不破），并产出 Kotlin/Swift 解码方的**孪生金样本**（同一 JSON 字节、逐键一致），使「Go 编 / 壳解」的整链有测试穿真实序列化边界。不做实现代码，只补测试与样本文件。

**③验收（行为化，逻辑型机内闭环）**：

- `go test ./internal/proto/... -count=1` 退出 0（含 `TestPairBundleEnvelopeVersionKey`/`RoundTrip`/`RejectsUnknownVersion`/`RejectsMalformed`）。
- **roundtrip 属性测试存在且可红**：随机构造合法 `PairBundle`（含空/非空 `Relay`、1..N 机器、relay/直连两形态、有/无 `Ticket`、零值/非零时间），断言 `Decode(Encode(b))` 与输入在**所有键**上恒等；把 `EncodePairBundle` 的版本字段改成常量错误值时测试变红（变异验证）。批判性质：区分「字段缺失」与「值为零」——`Ticket` 为 nil vs 空 `PairTicket{}`、`Credential` 空串 vs 缺键，roundtrip 必须保留可空类型语义。
- **金样本键集逐键断言**：`pairing_fixture_test.go` 的金样本 JSON 文件（或内联字符串）与 `PairBundle` 的 JSON tag 逐键一致；新增/改名 tag 时测试红。
- **共享金样本字节落地于 `mobile/bind/pairing_fixture.json`**（从 Go 侧导出，与 `pairing_fixture_test.go` 同源）。P5=A：Kotlin/Swift 解码测试**不进本仓**；壳工程侧用同一份 JSON 做孪生断言，归真机清单 #1。本卡机内只锁 Go roundtrip + 共享字节文件存在且与 tag 逐键一致。
- `codegraph validate --view cards-B369-charter` 与 `check --view cards-B369-charter` 保持 0 issue/0 fail；`go test ./cmd/ -run TestRepoContractGate` PASS。

**④入口指针与有界文件集**：`internal/proto/pairing.go`（只读，除非发现缺陷）、`internal/proto/pairing_fixture_test.go`（改：加属性测试 + 键集断言）、`mobile/bind/pairing_fixture.json`（新，共享金样本）。P5=A 后无 `.kt`/`.swift` 入库路径。符号锚：`internal/proto/pairing.go#PairBundle`、`#EncodePairBundle`、`#DecodePairBundle`、`#ErrPairVersion`、`#ErrPairMalformed`。

**缺陷族对抗（验收栏）**：

1. **生命周期/状态机中断**：无，因为纯函数编解码无宿主进程状态。
2. **静默失败/误导报错**：未知版本与畸形必须分别返回 `ErrPairVersion`/`ErrPairMalformed`（`errors.Is` 可判），壳侧按哨兵分支渲染**不解析文案**（契约 §9 附区条 5）；存在「编码成功但解码方读不懂」的窗口——由孪生样本整链测试覆盖，Go 侧单独绿不算。
3. **跨平台假设**：JSON 是跨语言中立格式，但 **Go `time.Time` 的 RFC3339 序列化与 Kotlin `Instant`/Swift `Date` 解析**可能不一致（时区/纳秒/格式）；断言必须包含 UTC 非零时间，且壳侧样本钉住字符串格式。真实壳解析**未验证，需真机**（#1）。
4. **假红/假绿**：金样本若用手抄 JSON（非从 Go 导出），两侧各自绿不代表链路通——必须共享同一份样本字节；属性测试必须能因编码错误变红（变异验证）；拒收面必须有反面断言（畸形/未知版本必须**返回错误**，而非解析成功）。
5. **门禁绕过**：不适用，因为 wire 层无权限面；但 `Validate` 的双形态互斥是「不产出歧义载荷」的安全属性——加反例锁住（双形态/两空必拒）。
6. **序列化边界**：本卡主战场——必须有一条测试穿过真实 JSON 编解码边界（`Encode→Decode` 恒等 + 壳侧解码同字节样本），用可空类型区分字段缺失与零值。
7. **枚举新值过既有白名单**：`PairVersion` 是唯一枚举；壳侧 switch/解版本分支必须逐值登记（未知版本走拒收，不得默认分支吞）。两侧入口各自绿、中间白名单挡死是通道分裂的标准形状 → 孪生样本锁住。
8. **承重安全属性**：token 为敏感字段但 wire 层不校验（熵闸在 S2 `DefaultDial`）；本卡只需断言**金样本不落日志/不落测试输出**（样本用假 token），token 熵闸测试归 S2。
9. **webview 候选族**：无，因为不触 webview。

### 3.3 S2（d_transport_channel）：连接核兑换、切机清罐、离线补配、资源安全

**①契约引用**：contract §3.2、§3.3、§3.4、§4.2（条 12–21）、§4.5（条 30–32）、§6 命中 2、§8.2、§8.8、§9 附区条 2；spec §契约语义（回环门禁/多机会话域）、实现决定（回环源）；P4。

**②意图与为什么**：Ticket 0 的 Core 已能配对、探测、起反代；本卡补齐 spec 承诺的三条可观测行为——(a) **程序化 ticket→cookie 兑换**（Go 核用 token 副本 `POST /api/auth/tickets` 领 ticket → 请求 `/console?ticket=` 拿 `Set-Cookie`，不跟随 302 以免把 cookie 换成对端跳转）；(b) **每机独立端口的会话罐语义**（切机清罐与重兑换的核侧入口）；(c) **资源安全**（重配/关闭不泄漏端口/goroutine；`Close` 后拒绝）。同时把 token 熵闸与 `DefaultDial` 的选路行为按契约条 11/13/14 锁死。**回环反代不注入凭据是承重安全属性**（contract §6 命中 2），任何兑换实现都不得在反代层补 Authorization。

**③验收（行为化，边界型——机内验契约形状 + fake relay/agentd，真实对端归真机）**：

- `go test ./internal/mobilecore/... -count=1` 退出 0，且新增以下行为（穿过真 `Core.Pair`，非只测帮手）：
  - **兑换整链（fake agentd）**：fake agentd 实现 `POST /api/auth/tickets`（回 `PairTicket.URL` 指向自身 `/console?ticket=x`）与 `GET /console`（回 `Set-Cookie: handoff_session=…` 但 **302**）→ Core 兑换后**拿到 cookie 且不跟随 302**（断言 cookie jar 含 `handoff_session`，且没有对 302 目标发第二次请求）。反面断言：把 fake `/console` 改成 200 无 `Set-Cookie` → 兑换返回错误（不得静默成功）。
  - **切机清罐**：对机器 A、B 各兑换一次，`Origin` 返回两个不同 `127.0.0.1:<port>`；切到 B 后 A 的会话罐被清（Core 暴露的罐状态或行为断言），任何时刻一罐只装一机的 cookie（RFC 6265 同 origin 不隔离的反例锁）。
  - **反代仍不注入凭据**：新增的兑换不得让反代路径带上 Authorization（对 fake 上游断言空 Authorization，与竖切同款）。
  - **token 熵闸**：`DefaultDial` 对 relay 形态 token 少于 32 hex 字符 → 返回错误（`relay.CheckTokenEntropy`），`Core.Pair` 据此标 offline 不整单失败。
  - **幂等与资源安全**：同名机器 `Pair` 两次 → 旧 listener 端口被关闭（第二个 Origin 的端口 ≠ 第一个，且旧端口不再可连）；`Close` 两次均 nil；`Close` 后 `Pair`/`Origin` 返回错误。
  - **离线补配入口**：离线机的重试/补配入口调用后，可达时标记变 online（契约 §8.8；spec「上线后补配」）。
- **竞态**：`go test ./internal/mobilecore/... -race -count=1` 退出 0（重配/关闭与并发 Pair 无数据竞争）。
- `go build ./...`（根模块）退出 0；`go test ./internal/relay/... ./internal/client/... -count=1` 保持绿（不破存量隧道/选路）。
- 真实 relay 服务端行为、iOS/Android 真实 cookie jar 桥接 → **未验证，需真机**（#2）。

**④入口指针与有界文件集**：`internal/mobilecore/core.go`（改：兑换、切机、补配、资源收口）、`internal/mobilecore/proxy.go`（只读，除非缺陷）、`internal/mobilecore/core_test.go`（改/新增测试）；可能新增 `internal/mobilecore/session.go`（兑换与罐，若计划判定 core.go 过长）。符号锚：`internal/mobilecore/core.go#Core.Pair`、`#DefaultDial`、`#Core.startLoopback`、`#Core.replaceMachine`、`#stopMachine`、`#Core.Close`、`internal/mobilecore/proxy.go#newReverseProxy`；外部锚：`internal/client/client.go#Client.IssueAuthTicket`、`#Client.HTTPClient`、`#Client.NoRedirect`、`internal/agentd/authroutes.go#Server.handleConsole`、`#sessionCookie`。

**缺陷族对抗（验收栏）**：

1. **生命周期/状态机中断**：兑换中途进程重启 → 领到的 ticket 会过期（60s），下次启动重新兑换即可，无孤儿；回环 listener/goroutine 必须在 `stopMachine`/`Close` 收口——加「重复 Pair 后旧端口不可连」与 `-race` 断言；端口 `net.Listen("127.0.0.1:0")` 由 OS 分配，关闭后立即释放。
2. **静默失败/误导报错**：兑换失败必须返回可行动错误（ticket 过期/无 Set-Cookie/连不上），**禁止「报成功但 cookie jar 为空」**；反代投递失败已有 502 + 日志（`ErrorHandler`），必须保持；探测「任何 HTTP 响应算可达」的宽松判据不构成静默失败（语义是可达非健康）。
3. **跨平台假设**：回环 `127.0.0.1` 在移动端成立；cookie 不按端口隔离（RFC 6265）是**跨平台共同事实**，故每机独立端口 + 切机清罐是承载；**iOS `WKHTTPCookieStore` 与 Android `CookieManager` 的 cookie 注入/清除行为差异未验证，需真机**（#2）；`httputil.ReverseProxy` 在各平台同库。
4. **假红/假绿**：fake agentd 的 `/console` 必须真回 302（否则「不跟随」断言是假绿）；切机清罐断言必须能因「不清罐」变红（变异验证）；`-race` 下若靠时序侥幸全绿，须用同步点（等待 listener 就绪）而非 `time.Sleep`。
5. **门禁绕过**：**承重**——反代不注入凭据是回环门禁的机制本身（contract §6 命中 2）；兑换发生在核内部、必须用 token 副本而**不是**给反代补 Authorization；加源码级守卫断言 `proxy.go` 的 Director 不加凭据（变异：注入 `Authorization: Bearer INJECTED` 必须让竖切红，既有先例）；检查与动作之间无 TOCTOU，因为罐切换在 `Core.mu` 下串行。
6. **序列化边界**：`Set-Cookie` 是 HTTP 头的**手写解析**面——core 侧解析 `Set-Cookie` 须有一条测试覆盖 name 大小写、多个 Set-Cookie 头、属性（Path/HttpOnly/SameSite/Secure）；cookie `Secure` 在明文 loopback 下必须 false（契约条 32）。`PairBundle` 反序列化已在 S1。
7. **枚举新值过既有白名单**：不新增枚举；但 `PairResult.Online` 的 true/false 与 `Origin` 空/非空是两态契约，离线机必须 `Online=false` 且 **Origin 空**（前端据此显示「离线」而非加载空源）——加反例。
8. **承重安全属性**：三条须各有能变红的测试——(i) 反代不注入凭据（变异可红）；(ii) 每机独立端口（同端口即红）；(iii) token 熵闸（弱 token 即红）。没有测试锁住的属性会在后续「顺手优化」中无声失守。
9. **webview 候选族**：核不触 webview；cookie 注入 webview 是 S4/壳的职责——接口处的真实行为**未验证，需真机**（#2）。

### 3.4 S3（mobile/ 图外）：工具链 gate 落地与构建脚本/文档

**①契约引用**：contract §0（工具链 gate 回执与两条工程约束）、§4.4（条 28–29）、§8.1、§8.6、§9 附区条 4；spec 测试决定（gomobile spike 列工具链 gate）；P5。

**②意图与为什么**：contract §0 的两条工程约束必须落进 `mobile/` 模块：go.mod 带 `tool golang.org/x/mobile/cmd/gobind`（否则新版 gomobile 报 `golang.org/x/mobile` 不在依赖图内），Android 构建显式 `-androidapi 21`（NDK 30 移除 API<21）。本卡提供可复现的构建脚本与本仓文档，让「怎么出 AAR/XCFramework」不靠记忆。P6=A：token 轮换操作文档（spec 用户故事 8 / contract §8.9）并入同一份 `mobile/README.md`，不另开文档卡。

**③验收（行为化）**：

- `mobile/go.mod` 含 `tool golang.org/x/mobile/cmd/gobind`（grep 断言）；`cd mobile && go build ./...` 退出 0（实测本工作树已 0，加 tool 指令后须仍 0）。
- 构建脚本（如 `mobile/build.sh` 或 Taskfile）存在，Android 分支显式 `-androidapi 21`，iOS 分支 `-target=ios`；脚本注释记录 gomobile 版本与 NDK 版本（contract §0 台账读数）。
- `mobile/README.md` 含 token 泄露唯一处置：双端轮换节点 token 并重启（contract §8.9）；不得把轮换写成「吊销 cookie 会话即够」。
- **真机/移动 CI 验证**：在本工作树（linux，无 Xcode/NDK）**无法**跑 `gomobile bind`；脚本正确性以「本工作树 `go build` 通过 + 脚本内容静态核对 + contract §0 的双端 PASS 台账」为据，**实际 AAR/XCFramework 重跑未验证，需真机/移动 CI**（#3）。不得把 linux 上没跑成的 bind 写成结论。

**④入口指针与有界文件集**：`mobile/go.mod`（改）、`mobile/build.sh`（新）、`mobile/README.md`（新）。符号锚：无（构建文件），引用 contract §0 台账。

**缺陷族对抗（验收栏）**：

1. **生命周期/状态机中断**：无，因为构建是一次性命令。
2. **静默失败/误导报错**：脚本必须 `set -e`（或等价）让 gomobile 失败即非零退出，禁止吞错误继续；`-androidapi` 缺失的症状是 `unsupported API version 16`，脚本必须显式带。
3. **跨平台假设**：本卡全在移动工具链（darwin + NDK / Xcode）——本工作树 linux 无法验证，**未验证，需真机/移动 CI**（#3）；shell 脚本在 macOS 的 bash/zsh 兼容性列入真机项。
4. **假红/假绿**：无测试可跑（机内只 `go build`）；把 `go build` 的全绿当成「能 bind」是假绿——文档必须明说「`go build` ≠ `gomobile bind`」。
5. **门禁绕过**：不适用。
6. **序列化边界**：不适用。
7. **枚举新值过既有白名单**：不适用。
8. **承重安全属性**：无，因为不涉凭据逻辑。
9. **webview 候选族**：不适用（构建卡）。

### 3.5 S4（mobile/bind 图外）：壳绑定面收口

**①契约引用**：contract §3.2（绑定面形状）、§5（组装点）、§8.2/§8.4；spec 方案（壳职责四件、协议零重实现）；P4、P5。

**②意图与为什么**：`mobile/bind` 是壳与 Go 核的唯一交接面。本卡把核对齐 P4 拍板——若选 A（核内兑换），绑定面需暴露「切机/兑换/取 cookie 供壳注入」的方法；若选 B，绑定面只暴露 ticket URL。同时导出面保持最小（gomobile 绑定面积最小，spec 需求锚）。

**③验收（行为化，边界型）**：

- `cd mobile && go build ./...` 退出 0；根模块 `go list ./...` 仍不含 `/mobile`（实测 0 命中）——反向依赖禁止。
- 绑定导出面方法数与签名有源码断言（grep/AST），且不含任何协议逻辑（壳侧协议零重实现）。
- P4=A：新增 cookie/兑换桥接方法有编译期与单元断言（fake 核）；真实 `WKHTTPCookieStore`/`CookieManager` 注入**未验证，需真机**（#2）。导出面不得含 `Token`/`Dial`。
- 文档（`mobile/README.md`，与 S3 共用）写清壳侧接线契约（哪几个方法、cookie 如何注入）。

**④入口指针与有界文件集**：`mobile/bind/bind.go`（改）；可能新增 `mobile/bind/session.go`。符号锚：`mobile/bind/bind.go#Pair`、`#Origin`、`#MachineNames`、`#Close`、`internal/mobilecore/core.go#Core.Pair`、`#Core.Origin`。

**缺陷族对抗（验收栏）**：

1. **生命周期**：`mobile/bind` 的包级 `core` 单例（`bind.go:24`）跨多屏存活——`Close` 后不得再 Pair；壳切后台/杀进程时 Go 核资源由 OS 回收，但显式 `Close` 是正确收口（欠账 §8.2 的一部分）。
2. **静默失败/误导报错**：绑定方法返回 `error` 必须经 gomobile 映射到壳侧异常，不得吞成 nil；`Origin` 对未配对机器返回错误而非空串（现状正确，加断言）。
3. **跨平台假设**：gomobile 生成的 AAR/XCFramework 是平台产物；**未验证，需真机/移动 CI**（#3）。
4. **假红/假绿**：`go build` 通过 ≠ 壳能加载 AAR——文档明说；壳侧无自动化测试（P5 若裁剪壳工程），风险记真机清单。
5. **门禁绕过**：绑定面**不得**暴露可绕过 cookie 门禁的方法（比如「直接取 token 给 webview 用」）——导出面须只有 `Pair`/`Origin`/`MachineNames`/`Close` + 兑换桥接，不含 `Token`/`Dial`；加源码守卫断言无 token 导出。
6. **序列化边界**：`PairResult`/`PairedMachine` 经 gomobile 跨语言映射（struct 字段），加断言字段名/类型稳定（gomobile 对 `time.Time`/`error` 支持有限——须确认只用 string/bool）。
7. **枚举新值过既有白名单**：不适用。
8. **承重安全属性**：导出面不得含 token（见 5）；`Close` 幂等。
9. **webview 候选族**：壳的 webview 行为归真机。

### 3.6 S5（d_cli）：`console --qr` 配对载体产物

**①契约引用**：contract §3.1（wire）、§8.3（CLI 欠账）、§8.4；spec 用户故事 1、§契约语义（配对载体）、扩展点（`docs/superpowers/plans/2026-08-11-agentd-browser-auth.md:56`）；本稿 §2.4 澄清 1。

**②意图与为什么**：给用户一条生成配对载体的命令：`handoff console --qr`（或 `--bundle`）从本机配置读 relay 端点与各 target 登记，逐机 `IssueAuthTicket` 代领 ticket，组装 `proto.PairBundle` 并 `proto.EncodePairBundle`，输出（QR 或粘贴串）。**不得在 CLI 重实现 bundle 编解码**（契约 §8.3 原文），一切走 `proto.EncodePairBundle`。离线机（IssueAuthTicket 失败）标为无 ticket 的部分 bundle，不整单失败。

**③验收（行为化）**：

- `go test ./cmd/ -run TestConsole -count=1` 退出 0，新增：
  - `console --qr`（或 `--bundle`）对多 target 配置 → stdout 产出可被 `proto.DecodePairBundle` 解回的 bundle，含全部可达机器（每机 token/形态/ticket），离线机标记无 ticket。
  - **不破坏既有契约**：`console --print-url` 仍恰好一行 URL（`cmd/console_test.go:14`）；`--qr` 与 `--print-url` 语义分工明确（互斥或分工，plan 定）。
  - 无 relay 配置时 → 只出直连形态机器；无任何 target 时 → 明确报错（不产出空 bundle）。
  - 退出码契约：成功 0、配置错误非 0。
- **容量预算**：实现节点实测 N 机 bundle 字节数并定 N 上界（`PairMachineBudgetBytes=200` 声明值）；超界退路（粘贴串/分段码）归该节点/后续硬化（contract §9 附区条 3）——本卡验收只要求「实测字节数写入测试断言」，不要求实现分段。
- `go build ./...` 退出 0；`go test ./cmd/ -count=1` 全量绿（旧 console 测试不破）。

**④入口指针与有界文件集**：`cmd/console.go`（改：加 `--qr`/`--bundle` flag 与 bundle 组装）、`cmd/console_test.go`（改/新增）；组装复用 `cmd/root.go#newTargetClient`（`cmd/root.go:237`）与 `cmd/root.go#TargetEndpoint`。符号锚：`cmd/console.go#consoleCmd`、`cmd/root.go#newTargetClient`、`internal/client/client.go#Client.IssueAuthTicket`、`internal/proto/pairing.go#EncodePairBundle`、`internal/config/config.go#Config`、`#Target`、`#RelayConfig`。

**缺陷族对抗（验收栏）**：

1. **生命周期**：一次性进程，无孤儿；ticket 60s 过期——命令必须在拿到全部 ticket 后立即输出（不得先 sleep/等待），QL 展示后用户扫码窗口由 spec 的「App 持 token 副本自助续领」兜底。
2. **静默失败/误导报错**：某机 `IssueAuthTicket` 失败必须 Warn 并标离线机（部分 bundle），**不整单失败也不静默吞**；无 relay 配置/无 target 必须明确报错；token 绝不进 stdout 以外的日志（contract §8 凭据卫生：不落日志）。
3. **跨平台假设**：QR 渲染若用终端图形（如 half-block 字符）需考虑 Windows 终端——plan 定；`--print-url` 的既有跨平台契约不变。
4. **假红/假绿**：测试必须穿真 cobra 命令与真 `proto.EncodePairBundle`（不许测手抄 JSON）；多 target 夹具必须真存在多个 target；「不破坏一行 URL」断言保持。
5. **门禁绕过**：`--qr` 由主令牌运行（`newTargetClient` 走本机 token）——与 `console` 现状同信任基线；`handleIssueTicket` 对会话身份 403 的既有执法不因新 flag 放松（加断言或沿用既有测试）。
6. **序列化边界**：CLI 产出的 bundle 是**跨语言序列化边界**（Go 编 → QR → Kotlin/Swift 解）——本卡必须有「CLI 产出 → `DecodePairBundle` 解回」测试，且与 S1 共享金样本键集；CLI 输出若是手搭 map 会漂移，故必须走 `EncodePairBundle`（源码守卫 grep 断言）。
7. **枚举新值过既有白名单**：不新增枚举；`--qr` flag 与 `--print-url`/`--no-open` 的互斥关系须在 flag 校验层明确（加反例：两者同给报错或定义分工）。
8. **承重安全属性**：token 副本是主 Bearer 令牌（spec 凭据模型）——命令**不得**把 token 写进日志/历史/临时文件；加源码级断言无 `log.*token`；`--qr` 输出的 QR 泄露等级等同主令牌，文档（S3 `mobile/README.md` 或 `cmd/console.go` 注释）须写明展示卫生。
9. **webview 候选族**：无。

### 3.7 S6（d_web）：响应式断点谱系、四 tab IA 与移动终端输入层

**①契约引用**：spec 实现决定（webview 固定回环源、响应式断点、终端输入层、IA 镜像 B358/B361）、用户故事 2–7；contract §8.10（web 内容面归实现节点）；P1、P2、P3；北极星 `prototypes/base/README.md` 移动端行（`prototypes/mobile-app/` 十一屏，fork 未入库）。

**②意图与为什么**：把 spec 的移动 IA 与终端输入落到真实控制台：底栏四一级 tab（会话/卡/项目/设置），会话为家（B358 镜像），卡 tab 镜像工作项账本 v2，项目 tab 进位置/目录/文件/只读查看器/终端，设置含配对清单；终端输入层加特殊键条与 IME 组合。**一份 UI 产物**（桌面/移动同源，响应式），webview 加载的页面来自 agentd（`-tags embedweb`，`internal/webui`），核只提供回环源。

**③验收（行为化，逻辑型——组件/金样本机内闭环，真实浏览器/webview 归真机）**：

- `cd web && npx vitest run` 退出 0，新增/修改：
  - **响应式断点**：给定手机/折叠/pad 视口宽度，Shell 渲染底栏四 tab（会话/卡/项目/设置）且不渲染桌面右栏文件树；pad 展开态任务现场可分栏。
  - **会话 tab**：会话列表为家（复用 `SessionSidebar`），「需要你」染底可筛选（B358 语义），点进群聊 → 本会话的卡 → 任务现场。
  - **卡 tab**：镜像工作项账本（「需要你」浮顶 + 状态词表 chip，词表逐值与 `internal/ledger/types.go` 一致——反例：出现词表外状态串即 fail）。
  - **项目 tab**：按项目显示各机器位置（CONTEXT「项目位置」），位置不可达只标状态等重连、**不降级只读**（反例：离线位置文件树不渲染内容）。
  - **终端输入层**：移动键条组件存在且可触发对应按键；IME 组合输入路径有单元测试（合成 IME event）；既有 `terminalInput` 桌面行为不破（`terminalInput.test.ts` 保持绿）。
  - **联网面**：移动页面复用既有 `web/src/api/*`（不新增协议/DTO）；卡「验收通过走 card move」复用既有 move 调用。
- `cd web && npx tsc -b` 与 `npx eslint .` 退出 0。
- **真机走查**：真实 iOS/Android webview 内对照十一屏原型（W1–…）逐屏，含底栏、扫码配对入口、终端键条与 IME、外接键盘收起键条 → **未验证，需真机**（#4）。

**④入口指针与有界文件集**：`web/src/app/shell/Shell.tsx`（响应式断点与底栏导航）、`web/src/app/rooms/`（会话移动镜像）、`web/src/app/board/`+`cards/`（卡 tab 镜像）、`web/src/app/tree/`+`projects/`+`files/`（项目 tab）、`web/src/app/settings/SettingsPage.tsx`（配对清单入口）、`web/src/app/workbench/terminalInput.ts`+`TerminalTab.tsx`（IME 路径）+ 非 `terminal*` 前缀的键条组件（P3 约束）、`web/src/app/lib/desktopShell.ts`（移动壳探针，如需）、各自 `*.test.ts(x)`。符号锚：`web/src/app/shell/Shell.tsx#Shell`、`web/src/app/workbench/terminalInput.ts#installTerminalInputFix`、`web/src/app/workbench/TerminalTab.tsx#TerminalTab`、`web/src/app/lib/desktopShell.ts#isDesktopShell`。

**缺陷族对抗（验收栏）**：

1. **生命周期**：轮询/订阅组件卸载时清理（沿用 `pollInterval` 既有模式）；终端 tab 关闭释放 xterm；移动切后台/回前台不重复挂载（真机项 #4）。
2. **静默失败**：连接状态三态（在线/离线/数据可能陈旧·连接中，spec 离线语义）必须区分空列表与加载失败；配对失败/机器离线渲染可行动错误。
3. **跨平台假设**：WKWebView vs Chromium/WebView2 的 cookie、剪贴板、IME、`localStorage` 差异（现状代码已多处处理 WKWebView，`desktopShell.ts`/`clipboard.ts`）；移动端新增的底栏/键条在 iOS 安全区（safe-area-inset）与 Android 导航栏下的布局**未验证，需真机**（#4）。
4. **假红/假绿**：组件测试用 fixture 驱动，fixture 与后端 DTO 同源（`testdata/` 金样本）；「词表外状态串即 fail」是反面断言；断点测试须给定视口宽度而非依赖 jsdom 默认（jsdom 无真实布局——须用 `matchMedia` mock 并断言分支）。
5. **门禁绕过**：前端无权限面；member/actor 由 gateway 注入，前端不自报身份（`web/src/api` 不在请求体塞身份）；**回环门禁是核侧承重属性，前端不得假设有凭据**——移动页面与桌面同源加载，cookie 由 S2/壳注入。
6. **序列化边界**：跨语言整链（Go DTO → HTTP JSON → TS interface → 组件）至少一条测试穿真实 fixture JSON；移动端复用既有 API 模块，不得手抄 DTO。
7. **枚举新值过既有白名单**：卡状态词表、会话成员状态词表的 TS 字面量联合类型必须与 Go 词表逐值一致（金样本断言）；渲染 switch 对未知值有兜底（显示原始串，不白屏）。
8. **承重安全属性**：无新增安全属性；移动端不得引入「绕过回环门禁」的前端路径（不直接连 agentd，只连核回环源）。
9. **webview 候选族**：**命中**——底栏、键条、IME、扫码入口、安全区布局在真实 iOS/Android webview 的表现**未验证，需真机**（#4）；外接键盘收起键条的键盘可达性列入真机走查。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的产品行为（spec 用户故事 1–9 + 方案条逐条映射）。五格齐全，归属子卡真实存在；「待拍板」格 = P1–P6 处置，处置前该行为不得扇出。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
|---|---|---|---|---|
| 用户在 CLI 配对 | `cmd/console --qr` → `proto.EncodePairBundle` | App 扫码（Kotlin/Swift 解码） | QR/粘贴串含 bundle（relay 端点 + 各机登记 + ticket） | S5（产出）、S1（解码样本） |
| App 扫码配对 | `Core.Pair`（`proto.DecodePairBundle` → `DefaultDial` → 探测 → loopback） | App 设置页配对清单（`MachineNames`/`PairResult`） | 在线机有 Origin、离线机标 offline（部分 bundle） | S2、S4、S6（设置 tab） |
| App 打开 webview 源 | `Core.Origin`（`127.0.0.1:<port>`）+ 核程序化兑换 cookie | webview（`-tags embedweb` 的 agentd 页面） | 页面加载成功、`/api` 带会话 cookie 可达 | S2、S4、S6 |
| 无 cookie 的同机其他 App 访问回环源 | 反代不注入凭据（contract 命中 2） | agentd cookie 闸 | 被拒（过不了闸） | S2 |
| 切机 | 每机独立端口 + 清罐重兑换 | webview cookie jar | 任何时刻一罐只装一机的 cookie、不 401 死局 | S2、S4 |
| 用户在「会话」tab 看列表/「需要你」 | B358 会话只读模型（既有 `/api/sessions`） | 移动会话列表组件 | 「需要你」染底可筛选、未读数 | S6 |
| 用户进任务现场审批/回答/裁决/停止 | 既有任务现场 API 与 WS | 移动任务现场组件 | 审批/回答可用、停止二次确认 | S6 |
| 用户在「卡」tab 看账本、验收通过 | 既有 `/api/cards` + card move gate | 移动卡 tab/卡详情组件 | 「需要你」浮顶、状态筛选、move 过 gate | S6（P2 拍板后定范围） |
| 用户在「项目」tab 看位置/目录/文件/终端 | 既有 projects/files/pty API + 离线语义 | 移动项目 tab 组件 | 离线位置只标状态不降级只读；在线位置可开终端 | S6 |
| 用户外出终端输入 | 移动键条 + IME 组合 + xterm | `TerminalTab` 输入路径 | 特殊键/组合输入上屏 | S6（P3 拍板后定拆分） |
| pad/折叠展开态 | 响应式断点（分栏 + 外接键盘收起键条） | Shell 布局 | 对话与终端分栏、键条收起 | S6 |
| 同局域网走直连 | `DefaultDial` 直连分支（`client.New`） | `Core.Pair` | 不绕 relay（Origin 反代走直连 Transport） | S2 |
| 手机丢失吊销 | `handoff sessions revoke`（既有）+ 双端轮换节点 token | agentd 会话表 / 节点配置 | cookie 会话失效；token 泄露须双端轮换并重启（不只吊销 cookie） | S3（`mobile/README.md`，P6=A） |
| 配对多机管理 | `MachineNames` + bundle 各机登记 | 设置页配对清单 | 清单列出已配对机器 | S2、S6（设置） |

**闭环核对结论**：P6=A 后轮换文档归属 S3，每条承诺行为五格齐、有归属。

- **推送通知（APNs/FCM）**：spec Out of Scope，不在闭环表。

未发现「只活在接口、测试或无人认领格子里的承诺」。

---

## 5. 未验证，需真机：协调者执行清单

1. **壳侧 JSON 解码孪生样本**（S1）：Kotlin `Instant`/Swift `Date` 对 Go `time.Time`（RFC3339）的解析一致性，以及 Go↔Kotlin/Swift 逐键解出的值——机内只能验 Go 侧金样本与共享字节，壳测试须在移动 CI/真机跑。
2. **程序化 ticket→cookie 与 cookie jar 桥接**（S2/S4）：真实 agentd + 真实 iOS `WKHTTPCookieStore` / Android `CookieManager` 下，兑换后 webview 是否带 cookie、切机清罐是否彻底、无 cookie 的同机其他 App 是否被拒（回环门禁）。
3. **工具链 gate 重跑**（S3）：`gomobile bind -target=android -androidapi 21` 与 `-target=ios` 在本仓 `mobile/` 模块出 AAR/XCFramework——本工作树 linux 无 Xcode/NDK，以 contract §0 darwin 台账为据，不重跑；真机/移动 CI 补跑。
4. **移动端真机走查**（S6）：iOS/Android webview 内对照十一屏原型——底栏四 tab、会话/卡/项目/设置、终端键条与 IME 组合、外接键盘收起键条、安全区布局、扫码配对流程。
5. **真实 relay 服务端 + 真实 agentd 端到端**（S2）：机内只穿 fake relay，真实 relay 的注册/隧道/主机白名单行为归真机。
6. **离线机上线补配**（S2）：真机上拔网再恢复，`Core.Pair` 的补配入口是否把机器翻回 online（spec「上线后补配」）。
7. **bundle 容量实测**（S5）：真机扫码 N 机 bundle 的实际字节数与二维码可扫性；超界退路归后续硬化。

---

## 6. 出稿自检

- [x] 产出四样齐全：§1 子系统清单每个带 best.json 类型并过派卡资格四条；§2 契约核对逐条有结论（§2.3 无退回、§2.4 三条澄清）；§3 六张子卡全部四段式且判据行为化；缺陷族逐族有答案（含「无，因为……」，分布各子卡验收栏）。
- [x] 「待拍板」岔口 P1–P6 集中列于稿首 §0，正文岔口一律回指。
- [x] 「未验证，需真机」汇总为 §5 七条。
- [x] 每张子卡有界文件集核过（§3 各④）；圈不出的（壳工程按 P5、轮换文档按 P6 路由），未硬塞进功能卡。
- [x] 行为闭环 §4 每行五格完整；P6=A 后轮换文档归属 S3，无无人认领格子。
- [x] 收尾动作：`codegraph resolve --doc docs/superpowers/specs/b369-breakdown.md` 与 `check`/`validate` 结果记台账；坏锚即修。
