# W5 打包与发布：agentd 托管前端 + 桌面薄壳（B111）

> 上游：[Web 控制台总方案](2026-08-11-web-console-master-design.md) §表格把 W5 定义为「`go:embed` + 桌面薄壳」。
> 本文是 W5 的完整设计，并**修正总方案里两处已经过期的前提**（见 §1.2）。

---

## 1. 背景与范围

### 1.1 W5 实际要做什么

总方案给 W5 标的是「接线 / 小」。这个判断**一半成立、一半已经不成立**：

- **「发布」那半边确实已经做完了**，而且不是被 W 线做的——B54 系列（Release 流水线 / `install.sh` / 自更新）、B59（操作者触发更新 + skill 随二进制分发）、B86（CI 验证门 + 签名公证 + Apache-2.0）、B87（proxy + 执行机自拉）。W5 不需要重做任何一件。
- **「薄壳」那半边不是小活**，见 §1.2。

W5 实际的三个交付物：

| # | 交付物 | 量级 |
|---|--------|------|
| ① | agentd 用 `go:embed` 托管前端，`/` 伺服控制台 | 小，真接线 |
| ② | 桌面薄壳 `handoff-desktop`（Wails，三平台） | 大 |
| ③ | 构建链：前端构建接进 release、薄壳三平台原生 runner + 签名公证 + 资产 | 中 |

### 1.2 修正两处过期前提

**这一节存在的理由**：B109 记录过一次「已被证伪并标注作废的安全前提，两天后又被原样复述进新 spec」。下面两条是总方案里**当时正确、现在过期**的表述，在这里显式作废，避免后续 spec 再复述。

**① 「薄壳选型（Tauri vs 裸 Electron）届时裁决」（总方案 §282）——选项集已变。**
Electron 于 2026-08-11 连同 Orca 一起封存（ADR-0009，在归档分支 `archive/desktop-console-2026-08-11` 上），2026-08-16 用户重申「不用翻 electron」。但**封存的是 Electron 这个选型，不是整个薄壳**——ADR-0009 的原话是「agentd 托管的 Web UI，**外加薄壳**」。2026-08-16 裁决：**薄壳要做，用 Wails**（理由见 §4.1）。

**② 「Linux 执行机不需要壳，薄壳只出 macOS/Windows 资产」——作废。**
这条是本次 brainstorm 中间产生的错误推论，源于把「Linux 执行机」当成了 Linux 用户的全部。用户 2026-08-16 明确：**相当一部分用户的桌面就是 Linux**。薄壳出三平台资产。
（仍然成立的是另一半：**agentd 与 CLI 本体保持 `CGO_ENABLED=0` 全平台交叉编译，一行不动**，见 §3.1。）

### 1.3 当前状态（已核实，非推断）

- `internal/agentd/authroutes.go:11` 写着「**不托管前端**：/console 的 302 目标固定为 /，本轮 agentd 尚未 embed 任何页面」。因此 ticket 换完 cookie 后 302 到 `/` **目前是 404**，前端只能靠 Vite dev server 跑。
- `internal/agentd/server.go:303-307`：`root.Handle("/", s.auth(mux))`、`root.HandleFunc("GET /console", …)`、最外层 `s.hostGuard(root)`。即 `/console` 在 auth 之外、hostGuard 之内，其余全在 auth 之内。
- `.github/workflows/release.yml`：三个 job（linux/windows 交叉编译、macOS 签名公证、Release 组装）**没有一个跑 `npm`**，CI 完全不构建前端。
- `handoff` 二进制：`agentd` 是同一个二进制的子命令（`cmd/agentd.go:52`）；release 同款 flags 构建出来 **18MB，gzip 后 7.0MB**。
- `install.sh:22`：安装落点是 `~/.local/bin/handoff`（可用 `HANDOFF_INSTALL_DIR` 覆盖）。
- `cmd/init.go:43`：`newInteractivePrompter` 是 `var` 测试缝，`askAll` / `defaultRole` / `listenPreset` / `executorOptions` / `roleOptions` 均为接 `prompter` 接口的逻辑，**与 TUI 已解耦**——但**七个标识符无一导出**，`package cmd` 外够不着，所以「解耦」不等于「薄壳能复用」，见 §4.4.1。

---

## 2. 全局约束

| 约束 | 值 |
|---|---|
| agentd / CLI 构建 | `CGO_ENABLED=0`，交叉编译矩阵**不变** |
| 薄壳框架 | **Wails v3（beta.8）**——2026-08-16 P1 探针实跑后裁决，见 §4.2 |
| Linux 基线 | Ubuntu 22.04 / Debian 12（`webkit2gtk-4.1`），**薄壳的 Linux 构建必须显式 `-tags gtk3`**（见下方注） |
| 前端构建命令（控制台，交付物①） | `npm ci` + `npm run build`（= `tsc -b && vite build`），产物在 `web/dist/` |
| 前端构建入口（薄壳自带前端） | **必须走 Wails 的 Taskfile**，不得裸调 `npm run build`——v3 的 vite 插件依赖 binding 生成器先产出 bindings |
| embed 构建标签 | `embedweb` |
| 二进制释出落点 | `~/.local/bin/handoff`，与 `install.sh` 同一路径 |
| 资产命名 | 不得与 `install.sh` / 自更新的既有契约冲突（`release.yml:8` 头部注释已警告） |

> **`-tags gtk3` 是承重的，不是可选优化。** Wails v3 的**默认** Linux 后端已经是
> `gtk4 + webkitgtk-6.0`（`pkg/application/linux_cgo.go:17`），要求 GTK ≥ 4.14，Ubuntu 22.04 上没有；
> `webkit2gtk-4.1` 只有在 `-tags gtk3` 下才走（`linux_cgo_gtk3.go:18`）。
> 漏掉这个 tag 的症状是：CI 在新发行版上构建通过，而 22.04 用户拿到的包跑不起来，
> 且差异不体现在任何一次代码提交里——与 §6.2 锁 `ubuntu-22.04` 而非 `ubuntu-latest` 属同一类陷阱。

---

## 3. 交付物①：agentd 托管前端

### 3.1 embed 的缺席问题（最重要的一条）

`go:embed` 指向不存在的目录是**编译期错误**。CI 不构建前端、开发者本地也不一定构建，因此「产物缺席时怎么办」必须先裁决，否则 `go build` 与 `go test ./...` 会被整片打挂。

**裁决：用 build tag，两份实现，构建产物全部 gitignore。**

（`web/dist/` **已经**被 `web/.gitignore:11` 忽略，本文只需新增 `internal/webui/dist/`。）

新包 `internal/webui`：

| 文件 | 构建标签 | 内容 |
|---|---|---|
| `embed.go` | `//go:build embedweb` | `//go:embed all:dist` → 真实 `fs.FS` |
| `stub.go` | `//go:build !embedweb` | 返回一个只含单页说明的 `fs.FS` |

stub 页必须**诚实**——写明「此二进制未嵌入前端构建产物，请用 release 版，或开发时跑 `npm run dev` 走 Vite」，不是空白页也不是假装正常的页面。

**为什么不选「提交占位产物到仓库」**：那样一跑 `npm run build` 工作区就变脏，而 **handoff 自己的 `dispatch` 硬要求工作区干净**（脏改动会被污染进任务分支）。用 W5 砸自己派发流程的脚，不划算。

**为什么不选「把真实产物提交进仓库」**：每次前端改动都产生巨大且无法审阅的 diff。

代价是两条代码路径，但 stub 侧只有几行，且 `//go:build` 让二者永不同时编译。

### 3.2 路由与 SPA 回落

SPA handler 挂**内层 `mux`**（即 `s.auth` 之后），不挂 `root`。理由：控制台页面本身应当要求 cookie；`/console` 仍是唯一免鉴权入口，ticket 本身就是它的凭据。

闭环：`/console?ticket=…` → 原子消费 ticket → Set-Cookie → 302 到 `/` → 此时已有 cookie → auth 放行 → SPA 送达。

回落规则：

1. 请求路径命中 embed FS 里的**真实文件** → 直接伺服，带正确 Content-Type。vite 产物文件名带 hash，可给长缓存（`immutable`）。
2. 否则回落 `index.html`，**必须 `no-cache`**（否则换版后浏览器拿着旧 index 引用已不存在的 hash 资源，表现为白屏）。
3. **`/api/*` 未命中绝不回落 HTML**，仍走原有 API 404/405。否则前端把 HTML 当 JSON 解析，报错会面目全非——这是排查成本极高的一类错。
4. `/ws`、`/console` 由 Go 1.22 `ServeMux` 的精确前缀优先天然让路，不需要额外判断。

### 3.3 未鉴权访问 `/` 的表现

现状是裸 401。**决定：对 `Accept: text/html` 的请求返回一个最小说明页**（HTTP 状态仍是 401），写清怎么拿入口（`handoff console` 或从桌面端打开）。非 HTML 请求维持原样。

理由：桌面端与浏览器都会撞到这个路径，一个裸 401 会让用户以为坏了。这是本文范围内唯一一处「顺手做」的增量，成本是一个静态字符串。

---

## 4. 交付物②：桌面薄壳 `handoff-desktop`

### 4.1 为什么是 Wails 而不是 Tauri

两家在**关键代价上是平手**，这一点必须先说清楚，否则后人会以为选 Wails 是因为 Linux 更好做：

- Linux 上**两家用的是同一个 webview**（WebKitGTK），同样撞 4.0/4.1 ABI 分裂，同样必须「在要支持的最老发行版上构建」。
- 打包能力平手：deb / rpm / AppImage 两家都有。
- ~~三平台都要原生 runner，交叉编译都不可行。~~ **2026-08-16 P1 探针证伪，作废。**
  实测只有 Linux 成立：Windows 产物能从 macOS 直接交叉编译（`CGO_ENABLED=0` + `-H windowsgui`），
  Linux 因 webkit2gtk 走 cgo 而不能。详见 §6.2 与[探针报告 §4](2026-08-16-w5b-p1-wails-probe-report.md)。
  这条作废**不影响本节的选型结论**——它原本就是「两家平手」的项，作废后依然平手。

**真正的差别在于薄壳并不只是「开个窗口」**——它必须完成鉴权握手：读 `~/.handoff/config.yaml` 拿监听地址与主令牌 → `POST /api/auth/tickets` → 打开 `/console?ticket=…`。

- Go 方案：直接 `import github.com/Xsxdot/handoff/internal/...`，配置解析与 ticket 逻辑**零新增代码**，且与 agentd 永不漂移。
- Rust 方案：要么重写一份（两份实现必然漂移），要么 shell out 调 CLI（多一层进程与错误面）。

附带：B86 的 macOS 签名公证链现在签的就是 Go 二进制，Wails 产物离它更近；维护者审 diff 时不需要多读一门语言。

Tauri 唯一明显更强的是生态成熟度与自动更新——但**自动更新 handoff 已有自己的一套（B59），重复了**。

**记账**：Tauri v2 已 GA，Wails v3 在 2026-08 仍是 beta（桌面 API 已稳定）。这是选 Wails 付出的代价，明确记录，不粉饰。

### 4.2 Wails 版本：裁决为 **v3（beta.8）**

**2026-08-16 P1 探针实跑后裁决**，完整证据见[探针报告](2026-08-16-w5b-p1-wails-probe-report.md)。

**决定性理由只有一条：v2 没有可用的系统托盘。**
`pkg/menu/tray.go` 里 `TrayMenu` 类型和 `internal/menumanager` 的整套管理逻辑都在，但
`options.App` 没有 Tray 字段、menumanager 之外**零调用者**、三平台 frontend 实现里**没有任何托盘渲染代码**——
是一段死代码，不是「多写点胶水就能接上」。而 §4.3 把托盘定为承重（关窗不停 agentd 之后，
它是用户唯一还能找回这个程序的入口）。v3 有三平台各自的原生托盘实现，实测在 macOS 上
能创建出带真实屏幕矩形的 `NSStatusItem`。

macOS 上 v3 的四项（构建 / 应用菜单 / 原生目录对话框 / 托盘）**全过**。

**代价照旧记账**：v3 仍是 beta（beta.8）。**退路不是「退回 v2 裸用」**——v2 满足不了 §4.3——
而是「v2 + 第三方 systray 库」，即给薄壳引入一个 Wails 之外的原生依赖。这条写进 plan 的风险栏。

**仍未验的（不许当成通过）**：Linux 与 Windows 的构建与运行、托盘的视觉确认。见探针报告 §6 与本文 §8。

### 4.3 启动序列

薄壳启动后按顺序判断，三个分支：

| 现状 | 动作 |
|---|---|
| 没有配置 | 图形化首次引导（§4.4） |
| 有配置、agentd 没跑 | 复用 `internal/service` 装并拉起，再连 |
| agentd 在跑 | 直接握手连接 |

**关窗口不停 agentd。** 理由是承重的：执行者不能随关窗陪葬（这正是 B36 setsid、B59 V3 验收所保护的招牌属性）。托盘常驻。

**托盘菜单的实际项：「打开控制台 / 退出（agentd 继续运行）」。**
原文写的「停止 agentd」**暂不做**——写 plan 时核实发现它没有支撑：
`service.Manager` 只有 `Install / Uninstall / Status / Kind / UnitPath`，**没有 `Stop`**
（`internal/service/service.go:50`），CLI 也只暴露 `service install|uninstall|status`。
用 `Uninstall` 冒充「停止」是错的：那会连开机自启一起卸掉，是不同的语义。

要做的话是给 `Manager` 补一个 `Stop()`（launchd `bootout` / systemd `stop`，各约 20 行加测试），
但那是对承重模块的横切改动，而「双击就能用」并不需要它。因此：
**W5b 先不做，托盘不出现这一项**；将来真需要时按上述方式补，并回来改这一节。
附带一条已核实的事实，将来做时用得上：launchd 的 `bootout` **不会**杀掉已拉起的执行者
（`internal/service/launchd.go:5` 的注释与 B36 的 setsid 保证）。

**薄壳绝不把 agentd 内嵌进自己的进程。** 三条理由任一都足够：agentd 必须活过薄壳；agentd 必须能在无 GUI 机器上裸跑；B59 的更新机制假设 agentd 是 service 托管的。

### 4.4 图形化首次引导

引导覆盖 `handoff init` 的同一批决策：角色（协调者 / 执行机）、监听地址、执行者探测结果、是否装 service、target 配对。

~~复用 `cmd` 中已与 TUI 解耦的纯逻辑（`defaultRole` / `listenPreset` / `executorOptions` / `roleOptions`，见 §1.3），只替换 UI 层。**不重构 `init`**，也不动 TUI 路径。~~
**上面这段作废**——写 W5b-2 plan 时核实，它描述的复用路径不存在。原文保留以免后人再照着它排一次。

#### 4.4.1 实证：那批逻辑够不着

§1.3 说这些逻辑「已与 TUI 解耦」是**对的**——它们不碰终端，只依赖 `config.Config`、`toolchain.Result` 和 `runtime.GOOS`。但解耦 ≠ 可复用：它们全都封在 `package cmd` 里且**一个都没导出**。

| 标识符 | 位置 | 导出？ |
|---|---|---|
| `prompter`（Select/Input/Confirm 接口） | `cmd/prompter.go:37` | 否 |
| `promptOption` | `cmd/prompter.go:30` | 否 |
| `askAll`（68 行，问答编排） | `cmd/init.go:205` | 否 |
| `roleOptions` / `defaultRole` / `executorOptions` / `listenPreset`（合计 51 行） | `cmd/init.go:287/303/330/375` | 否 |

`cmd` 包也没有任何可用的导出包装（`grep -n "^func [A-Z]" cmd/init.go cmd/prompter.go` 无输出）。唯一已导出的相关件是 `internal/toolchain` 的 `Detect()` 与 `Result`（`detect.go:101`/`62`）——**工具链探测那半边可以直接复用，问答那半边不能**。

#### 4.4.2 为什么不能靠「就地导出」绕过

最省事的想法是把这七个改成导出名、让 `desktop/` 模块 import `cmd`。**这条路被否掉**，依据是 `cmd` 的包级副作用规模：

```
cmd 包里 init() 函数数量: 31
注册到 rootCmd 的命令数: 31
```

import `cmd` 会把整个 CLI（cobra + 全部 31 个子命令及其注册副作用）链进桌面壳。为了 134 行纯函数付这个代价不成比例，而且它把「薄壳要薄」（§4.7）这条主张从内部拆掉了。

#### 4.4.3 三个选项与本 plan 采用的假设

| 选项 | 含义 | 代价 |
|---|---|---|
| A | 就地导出，薄壳 import `cmd` | 已否 —— 见 §4.4.2 |
| **B（W5b-2 采用）** | 把这 134 行纯逻辑下沉到 `internal/initflow`，`cmd/init.go` 改为薄调用方 | 违反原文「不重构 `init`」的字面 |
| C | 薄壳自己重写一套引导问答 | 正是 §4.4 想避免的漂移：两套 role 默认值、两套 listen 预设，改一边忘一边 |

**按 B 推进**，理由是它保住了原文的**意图**（单一事实来源、不动 TUI 路径、行为不变），只是原文以为这个意图不需要动 `cmd` 而已。下沉面很窄，可核实：

- 生产调用点**只有一处**（`cmd/init.go:118`），其余全是测试；
- 被移动的代码不依赖 cobra、不依赖终端，`cmd/init_test.go` / `init_role_test.go` 随之迁移即可；
- `roleCoordinator` / `roleExecutor` 两个常量一并下沉。

**若用户不接受 B**，回落到 C 并接受漂移风险；这条假设与 §4.6 的 Windows 假设一样，做完要回来确认。

### 4.5 目录选择器（顺带收口 B110）

用 Wails 的原生目录对话框，通过 binding 暴露给前端；新建项目时可选择目录而非只能粘贴路径。

**为什么走薄壳而不是 agentd 端点**（这一条在 brainstorm 中被 Linux 需求反转过，记下来避免后人重走）：
最初倾向做成 agentd 端点（复用 B108 `open -R` 那套回环校验 + 三态能力位），好处是普通浏览器里也能用。但**agentd 弹原生框在三平台要三套实现**（macOS `osascript` / Windows PowerShell / Linux `zenity` 或 `kdialog`），而 **Linux 那套还依赖 zenity/kdialog 装没装**，不可靠。Wails 自带跨平台原生对话框，一套解决。

**降级**：非薄壳环境（普通浏览器）用 B107/B108 已建立的三态能力位（`*bool` + `omitempty`；`nil` = 对端没上报，**不得当成 false**；前端 `null → 放行`）灰掉，理由文案写「需要桌面端」。

**遗留的不对称，与 B108 一致**：薄壳跑在人所在的机器上，选出来的是**本机**路径。给远程开发机加项目时这个路径没有意义，远程仍然只能粘贴。这与 B108「Reveal in Finder 只做本机半边」是同一个不对称，已被接受。

### 4.6 Windows 薄壳的定位：**待用户裁决**，W5b 先不做

写 plan 时核实 §4.3 的启动序列，发现 Windows 这一路在架构上是断的。三条实证：

| 事实 | 出处 |
|---|---|
| `service.New` 对 Windows **直接返回错误**，不返回 Manager | `internal/service/service.go:76`，报文：「暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37），托管起来也跑不了任务」 |
| B37（prochost 的 Windows 实现）是 **🚫 已评估·暂不做** | backlog B37，附[成本清单](2026-08-10-handoff-windows-port-cost.md)。不做的依据是一次真机探路 + 一次全仓静态扫描 |
| `handoff` 二进制本身**能**为 Windows 编译 | 实测 `GOOS=windows CGO_ENABLED=0 go build .` 出 28.8MB exe |

于是 §4.3 的三分支在 Windows 上第二支走不通：「有配置、agentd 没跑 → 复用 `internal/service` 装并拉起」
拿不到 Manager。更根本的是，即使 agentd 起来了，**Windows 上也跑不了任务**（B37）——
那么 Windows 薄壳唯一说得通的形态是**纯协调者**：本机 agentd 只做控制台伺服与远程汇总，
执行全部落在远程 target 上。这与「双击就能用」的默认心智不同，也牵动首次引导的分支设计。

**这不是能在 plan 里顺手定的事**，它决定 §6.2 的 Windows runner、§6.3 的 Windows 安装包、
§4.4 引导的分支数量都要不要做。三个候选：

| 选项 | 含义 |
|---|---|
| A（W5b 当前采用的假设） | **Windows 薄壳暂不做**，W5b 只出 macOS + Linux 资产。与 B37「已评估·暂不做」一致 |
| B | 做 Windows 薄壳，但明确定位为**纯协调者**，引导里就不出现「本机执行」这一支 |
| C | 先做 B37（Windows prochost），再谈薄壳 |

**W5b 按 A 推进**，理由是 A 与仓库既有决策（B37）一致，且选 A 不会浪费任何工作——
薄壳代码本身是跨平台的，将来若改选 B，增量只是构建链与引导分支，不需要返工已写的部分。
**这条假设需要用户确认**；确认前不要把 Windows 资产写进 release 流水线。

### 4.7 版本错配

前端由 agentd 伺服，薄壳只是窗口 + 引导 + 对话框。因此薄壳与 agentd 版本不一致时**基本无害**：用户看到的界面永远来自 agentd 自己那一份。这是把薄壳做薄换来的红利，应当保持——**不要往薄壳里放业务逻辑**。

---

## 5. 内嵌 `handoff` 二进制

### 5.1 裁决：内嵌

薄壳内嵌对应平台的 `handoff` 二进制。三条理由：

1. **它是 §4.3 的逻辑后果。** 既然薄壳负责拉起 agentd，「机器上没有二进制」就必然是第一个分支。不内嵌的话这个分支只能弹「请先去装 CLI」，「双击就能用」当场破功。
2. **代价不成比例地小。** 18MB（打包压缩后约 +7MB），且那个平台的二进制在同一条流水线里本来就在编，零额外构建成本。
3. **首次启动不能押在联网上，本项目有实证。** B59 验收记录：本机 `github.com:443` **连续两次 75s 超时**而 `api.github.com` 正常。首次启动才去下载，等于把「能不能用」押在一条被记录过会断的链路上，且失败时机恰好是用户第一次打开。

### 5.2 释出到哪：`~/.local/bin/handoff`

**与 `install.sh` 同一落点**，不是 app bundle 内部，也不是私有目录。

理由：释出到私有位置会造成**最难排查的那类错配**——桌面端用的 `handoff` 和用户命令行敲的 `handoff` 是两个版本，B59 更新了一个另一个不知道。同一落点则是一份二进制、CLI 与桌面端共用、B59 更新一次两边同时受益；launchd plist 指向的也是这个稳定路径，不会因为用户移动或删除 `.app` 而断。

### 5.3 释出规则：绝不覆盖用户已有的安装

| 现状 | 动作 |
|---|---|
| 已有且能跑（`~/.local/bin/handoff` 或 PATH 上） | 直接用，**不释出** |
| 没有 | 释出内嵌的那份，`chmod 0755` |
| 已有但比内嵌的旧 | **提示，不自动换** |

第三条的理由：换版要重启 agentd。复用 B59 已有的闸一语义（有活跃任务时拒绝换版，并给出可复制的强制命令），不要另造一套。

### 5.4 macOS 签名顺序（承重）

释出到 `~/.local/bin/` 的二进制**脱离了 `.app` bundle 的签名覆盖**，Gatekeeper 可能拦。

缓解办法：内嵌的必须是**已单独签名并公证过**的那份 handoff（B86 本来就在签它）。于是 CI 顺序变为：

```
签 handoff → 嵌进薄壳 → 签 + 公证薄壳 bundle
```

**这条不能靠推理拍板，必须真机探针**（§8）。

### 5.5 一条已知的连带后果

`release.yml:167` 的注释指出：macOS 上 `CGO_ENABLED=0` 是承重的——开了 CGO 会让产物动态链接系统库并被打上构建机的最低系统版本约束，二进制会在更老的 macOS 上拒绝启动，而症状要等到用户机器上才出现。

**薄壳必须开 CGO**（Wails 绑 WKWebView），因此**薄壳会带上最低 macOS 版本约束**。这是无法避免的，但必须：①在 Release 说明里写清薄壳的最低 macOS 版本；②**不要因此去动 handoff 本体的 `CGO_ENABLED=0`**——那一行保护的是 CLI/agentd，与薄壳无关。

---

## 6. 交付物③：构建链与分发

### 6.1 前端构建接进 release

在需要 embed 的构建步骤前插入 `npm ci && npm run build`，产物喂给 `go build -tags embedweb`。

注意 `npm run build` 已包含 `tsc -b`，类型错误会直接让 release 失败——这是想要的行为。

### 6.2 薄壳的三条原生 runner

| 平台 | runner | 产物 | 为什么是原生 runner |
|---|---|---|---|
| Linux | `ubuntu-22.04`（锁定，**不用 `ubuntu-latest`**），**装工具与构建两步都带 `-tags gtk3`** | AppImage + deb | **编译就必须原生**：webkit2gtk 经 cgo，`CGO_ENABLED=0` 交叉编译编不过（实测） |
| macOS | `macos-latest`（搭现有签名公证 job） | 签名公证过的 `.app` / dmg | **签名与公证只能在 macOS 上做**，与能否交叉编译无关 |
| Windows | `windows-latest` | 安装包 | **编译不需要它**（实测可从 macOS 交叉编出 GUI exe），保留它是为了**运行验证**与安装包制作。⚠ **整行按 §4.6 暂缓**，用户裁决 Windows 定位前不要接进流水线 |

关于 Windows 那一行：P1 探针实测 `GOOS=windows CGO_ENABLED=0 go build -tags production -ldflags="-w -s -H windowsgui"`
在 macOS 上直接产出 10.3MB PE32+ GUI exe（少了 `-H windowsgui` 会得到 console 子系统的 exe，会弹黑框）。
但**「能编出来」不等于「能跑」**——该产物没有在 Windows 上运行过。
因此本表**保留** Windows runner：拿到真机运行证据之前不要把它去掉。

另记：Wails v3 自带官方交叉编译方案 `internal/commands/build_assets/docker/Dockerfile.cross`
（Zig + macOS SDK + 内置 mingw，覆盖 darwin/linux/windows × amd64/arm64）。
它的 Linux 基线是 Debian 13 + GTK4，与本文 §2 锁的 22.04 基线**不一致**，
所以**不直接采用**，只作为将来若要收敛 runner 数量时的备选，且届时必须重验 §2 基线。

**Linux runner 的两处实测约束**（2026-08-17 容器探针，见[探针报告](2026-08-16-w5b-p1-wails-probe-report.md) §5.1）：

1. **装 `wails3` 工具那一步也要带 tag**：`go install -tags gtk3 github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.8`。
   不带的话在 22.04 上直接 `No package 'gtk4' found` 退出 1——卡点在准备工具阶段，比构建期更早，
   报错却和「薄壳代码有问题」长得很像。
2. **`build/linux/nfpm/nfpm.yaml` 的 `depends` 段目前整段被注释**（53-66 行，内含现成的 `gtk3` / `webkit2gtk-4.1`）。
   产物实际链的就是这套库，deb/rpm 必须声明它们，否则包在干净机器上装得上、跑不起来。

Linux 必须锁 `ubuntu-22.04` 而非 `ubuntu-latest`：AppImage 要「在最老的目标发行版上构建」，`ubuntu-latest` 会随 GitHub 滚动，某天静默把 glibc 基线抬高，症状是老发行版用户突然跑不起来——且这个变化不会体现在任何一次代码提交里。

### 6.3 资产命名

不得与 `install.sh` 及自更新的既有契约冲突。薄壳资产用**独立前缀**（如 `handoff-desktop_*`），确保 `install.sh` 的资产匹配逻辑不会误抓到薄壳包。

---

## 7. 非目标

| 不做 | 理由 |
|---|---|
| 薄壳自更新 | B59 已有操作者触发的机制；AppImage/deb 自更新是另一个泥潭。薄壳只检查版本并提示 |
| 扩到 `webkit2gtk-4.0`（RHEL/Alma/Rocky 8-9、Ubuntu 20.04、Fedora ≤39） | 4.0 是退役路线（soup2），长期逆着上游走。记 backlog，触发条件：出现真实 RHEL 系用户报不能用 |
| 远程机的目录选择器 | 与 B108 同一不对称，本质无解（需要远程机上有 GUI 宿主） |
| 重构 `handoff init` 的 TUI | 逻辑已解耦，只需补 UI 层。动 TUI 是范围外 |
| 把 agentd 内嵌进薄壳进程 | 见 §4.3 的三条理由 |
| 移动端布局 | 仍留给 W6 |

---

## 8. 必须真机探针的项

以下四条**不能靠推理或读文档拍板**，plan 里各自是独立 task，探针不过就不进入依赖它的后续 task：

**P1 已于 2026-08-16 完成 mac 半边**（详见[探针报告](2026-08-16-w5b-p1-wails-probe-report.md)），
它裁决了 §4.2 的选型，并推翻了 §4.1、§2 的两处表述。Linux / Windows 半边仍未验，逐项状态见下表。

| # | 探什么 | 在哪探 | 不过的后果 | 状态 |
|---|---|---|---|---|
| P1-mac | v3 能否构建出可运行产物；原生目录对话框、托盘、菜单是否可用 | 真 mac | 选型作废，退回重裁 | ✅ **过**（构建 15.5s / 9.0MB；对话框实测弹出；托盘有真实 `NSStatusItem` 矩形）。托盘的**视觉**确认仍欠 |
| P1-linux（构建） | v3 能否在 Ubuntu 22.04 上构建，且须带 `-tags gtk3` | Ubuntu 22.04 容器 | 选型作废，退回重裁 | ✅ **过**（2026-08-17）：无 tag 退出码 1（`No package 'gtk4' found`），带 tag 退出码 0、产物 16.3MB 且 `ldd` 链到 `libwebkit2gtk-4.1` + `libgtk-3`。被构建的是 W5b-1 交付的真实 `desktop/` 模块。详见探针报告 §5.1 |
| P1-linux（运行） | 产物能否运行；托盘 / 原生对话框在 Linux 桌面上是否可用 | 真 Linux 桌面 | Linux 那半边的可用性无依据 | ⛔ **未验**：容器无显示服务，只能验构建。**不得因为构建过了就当 Linux 通过** |
| P1-win | 交叉编出的 exe 能否运行；托盘/对话框是否可用 | 真 Windows | §6.2 的 Windows 行须改回原生构建 | ⛔ **未验**：无 Windows 机器。**编译侧已过** |
| P2 | 从 `.app` 释出到 `~/.local/bin/` 的**已公证**二进制能否通过 Gatekeeper | 真 mac | §5.4 的签名顺序方案作废，须改为 bundle 内运行 | ⛔ 未验（需真实 Developer ID + 向 Apple 提交公证，属外部可见操作，须用户授权） |
| P3 | AppImage 在非 Ubuntu 发行版（至少 Fedora 40+、Arch）能否运行 | 真 Linux | Linux 基线判断有误，须重定 | ⛔ 未验（同 P1-linux） |
| P4 | 薄壳拉起 agentd 后，关闭薄壳窗口时执行者是否存活 | 任一平台 | §4.3 的承重属性被破坏，须改进程组处理 | ⛔ 未验（须薄壳实现到能拉起 agentd）。**注**：探针只验证了「关掉最后一个窗口时薄壳进程本身不退」，那不是 P4 |

P4 尤其重要：它是 B36/B59 一路保护下来的招牌属性，薄壳是第一个可能破坏它的新宿主。

---

## 9. 验收标准

**交付物①**
- 未带 `embedweb` 标签时 `go build ./...` / `go test ./...` 全绿，且 agentd 启动后 `/` 返回诚实的 stub 说明页
- 带 `embedweb` 标签时 `/console?ticket=…` → Set-Cookie → 302 → `/` 返回真实控制台，页面可用
- 深链接（如 `/tasks/<id>`）刷新后仍正确回落 `index.html`
- `/api/<不存在的路径>` 返回 JSON 错误而非 HTML
- 无 cookie 访问 `/` 返回 401 + 说明页（HTML 请求）

**交付物②**
- 三平台各自：干净机器上双击 → 引导 → agentd 起来 → 控制台可用，**全程不碰命令行**
- 关闭薄壳窗口后，正在跑的任务的执行者存活（P4）
- 新建项目时目录选择器可用；普通浏览器里该入口灰掉且理由文案正确
- 已装 handoff 的机器上，薄壳**不覆盖**已有二进制

**交付物③**
- release 流水线产出三平台薄壳资产 + 原有 CLI 资产，`install.sh` 不误抓薄壳包
- macOS 薄壳通过 `codesign --verify --strict` 与公证校验（与 B86 现有门同等严格）
- AppImage 在至少两个非 Ubuntu 发行版上可运行（P3）

---

## 10. 分期：两份 plan，不是一份

三个交付物不是一个不可分的整体。**交付物① 自己就能独立上线并产生价值**（agentd 从此能托管控制台，不再依赖 Vite dev server），而它与薄壳之间没有任何依赖方向——薄壳只是打开一个 URL，那个 URL 由 agentd 伺服，与 agentd 是否 embed 无关（dev 环境下薄壳照样可以指向 Vite）。

因此拆成两份 plan：

| Plan | 内容 | 可独立交付 |
|---|---|---|
| **W5a** | 交付物① 全部 + §6.1 前端构建接进 release | 是。做完就能 `handoff console` 直接用，不再需要 Vite |
| **W5b** | 交付物② 全部 + §6.2/§6.3 薄壳 runner 与资产 + §5 内嵌二进制 | 是。依赖 W5a 已落地（薄壳打开的控制台应当来自 embed 版） |

**W5b 再拆三份（2026-08-16 写 plan 时的决定）。** 理由与 W5a/W5b 那次相同：
一份 plan 里塞进「薄壳 + 图形化引导 + 内嵌二进制 + 三平台构建链 + 签名公证」，
既超出一份计划该有的体量，也让「哪一步能独立验收」变得说不清。三份各自可交付：

| Plan | 内容 | 可独立交付 |
|---|---|---|
| **W5b-1** | 薄壳核心：定位 agentd → 握手 → 开控制台 → 托盘常驻 → 关窗不停 agentd → 目录选择器（收口 B110） | 是。**已跑过 `handoff init` 的机器上双击即用**，这已经是完整价值 |
| **W5b-2** | 干净机器路径：图形化首次引导（§4.4）+ 内嵌二进制释出（§5） | 是。做完「没装过 handoff 的机器」也能双击就用 |
| **W5b-3** | 构建链：三平台 runner、签名公证、release 资产（§6.2/§6.3） | 是。做完才有可分发的安装包 |

顺序是 1 → 2 → 3：W5b-1 产出的可执行体是 W5b-3 要打包的对象，
W5b-2 的引导要用 W5b-1 已建立的配置判据。**W5b-3 还额外卡在 §4.6 的 Windows 裁决与 P1 的 Linux 半边**。

W5a 先做还有一个实际好处：它小、风险低，能先把「前端构建接进 CI」这条链路跑通并验证，W5b 再在已验证的链路上加薄壳，而不是两件新事一起上。

W5b 的第一个 task 必须是 §8 的 P1 探针。

---

## 11. 后续分出的 backlog

- 扩到 `webkit2gtk-4.0`（触发条件见 §7）
- 薄壳自更新（若用户实际反馈「提示了但懒得手动更新」）
- B110 由本文 §4.5 收口，完成后应在 backlog 标注其来源
