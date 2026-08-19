# 桌面控制台改为 agentd 托管的 Web UI，外加薄壳

状态：已接受（2026-08-11）。取代基于 Orca 的 Electron 实现路线；不影响 ADR-0001、0004、0005、0007、0008（这些是产品决策，与前端形态无关）。

## 决策

桌面控制台的界面由 **agentd 自己托管**（`go:embed` + `http.FileServer`），浏览器直接访问即可使用。桌面上的「应用」是一个**只做 `loadURL` 的薄壳**（约 200 行，Tauri 或裸 Electron 均可），不承载任何业务逻辑。

原先基于 Orca 快照做 Electron 应用的路线**就此封存**，分支清单见 `docs/archive/2026-08-11-desktop-branches-sealed.md`。

## 为什么

**一、Electron 主进程在本架构下没有工作可做。** 桌面端设计规格（`docs/superpowers/specs/2026-08-09-handoff-desktop-vertical-slice-design.md` §150）已经硬性规定：本机开发资源也必须经过同一套 machine-authority 服务，不得在 Electron 主进程里另走 Node `fs`、本地 PTY 或 Git 捷径，否则本地与远端语义会再次分叉。实测印证这条约束被严格遵守——`src/main/handoff/` 的 2553 行**全部是 HTTP/WS 转发**，没有一行 `fs`。

推论：把界面搬进浏览器，数据路径一个字节都不变，反而少掉 IPC 与 contextBridge 两道序列化边界，`src/main/handoff` + `src/preload/handoff*` + `src/shared/handoff` 约 3400 行可直接删除。

**二、携带成本与实际复用严重不成比例。** 实测：`desktop/src` 共 10,409 个 TS/TSX 文件、**2,417,935 行**，`node_modules` **2.7 GB**，123 个声明依赖；handoff 自有代码 **10,741 行，占 0.44%**。实际复用的 Orca 代码约 2,700 行（Monaco 胶水链 ~1733、shadcn 组件 527、webview 接线 ~360、xterm 配置 76、`unread-response-body` 12）。IPC 脚手架、状态管理、窗口管理、多 tab 工作台的复用量**为零**——handoff 自写了 1835 行工作台。

**三、解绑成本极低。** 全部 handoff 代码指向 Orca 内部的 import 只有 **18 行**：`@/components/ui/*`（shadcn）10 行、`XtermSurface` 3 行（该文件本就是 handoff 自建，只是放在 Orca 目录下）、`MonacoSurface` 1 行、`browser-page-webview` 1 行、`pane-terminal-options` 1 行、`unread-response-body` 2 行。**零条指向 Orca 的 store、窗口管理或 IPC 框架**——`features/handoff/architecture-boundary.test.ts` 早已在 CI 层面禁止这些。shadcn 可由 CLI 重新生成；其余是小工具函数或可替换件。

**四、移动端与云是白送的，而 Electron 路线永远到不了。** agentd **已经是网络服务**：远程派发形态（`targets.devbox.addr = "192.168.x.x:7777"`）就是「agentd 监听网络地址 + Bearer token 鉴权」，跨机访问是既有生产模型，不是要新造的东西。同一份页面手机浏览器直接可用，不需要额外做一遍。Electron 原生应用则形态上无法上手机，想要就得再写一个、零复用。

**五、桌面观感没有损失。** 薄壳提供 dock 图标、独立窗口、Cmd+Tab、开机自启。用户不需要「打开浏览器」。

## 为什么是 loadURL 而不是壳内自带页面

1. **同源**。页面从 `file://` 或 `app://` 加载会让每个 API 请求都变成跨域，agentd 必须配 CORS，WebSocket 也拿不到 cookie。从 `http://127.0.0.1:7777` 加载则同源，该问题不存在。
2. **一份产物，永不漂移**。壳内自带页面等于桌面一份 UI、agentd 给浏览器/手机另一份，两份会漂且需维护版本兼容矩阵。loadURL 使 UI 与其 API 天然同版本，升级 agentd 即升级所有端，壳永不需要更新。
3. **远程开发机**。远程机上本就跑着 agentd，把窗口指向它的 URL 即可拿到它自己的 UI 与 API，无需任何跨域、token 分发或版本匹配处理。这直接服务于「抹平本地与远程观感」这一核心诉求。
4. **壳保持不可变**。无 renderer 构建、无需为 UI 改动走打包与自动更新。

代价：agentd 未启动时壳打不开界面。缓解方式为壳启动时探测端口，未起则拉起 agentd 或显示启动中状态。

## 后果

**删除**：`src/main/handoff/`（2553 行）、`src/preload/handoff*`（325 行）、`src/shared/handoff/ipc-error.ts`（87 行）及其测试。IPC 错误码跨边界丢失这一整类缺陷（见封存清单缺陷 #3）**从构造上消失**。

**保留并迁移**：`src/renderer/src/features/handoff/`（6970 行）约 80% 可直接搬，主要工作是把 `window.handoff.*` 换成 `fetch` / `WebSocket`。

**需要新增的 Go 侧工作**：
- `go:embed` + `http.FileServer` 托管前端，约 20 行。
- **`/ws/events` 支持非 header 鉴权**——浏览器的 `new WebSocket()` 无法设置 `Authorization` 头，而该路由目前在 Bearer 中间件之后。这是**唯一真正承重的改动**，同时挡着浏览器、移动端与上云三条路。

**xterm 补丁全部保留**。实测确认 `config/patches/` 下五个补丁没有一个是 Electron 相关的，详见评估记录。其中输入法补丁在 Web 下**更重要**（面对多个浏览器引擎而非单一 pin 死的 Chromium）。

**移动端**：本决策只要求「兼容」，即同一份页面能在手机浏览器打开。移动端专门的响应式布局不在本次范围内，留待后续。任务看板与 agent 总览是移动端成色最好的两项，也恰好是尚未动工的两项。

## 未决

1. **`/ws/events` 鉴权的具体做法**未定：query 参数、cookie、`Sec-WebSocket-Protocol` 三选一。cookie 方案会影响后续上云的会话模型，应先定形再动手。
2. **上云所需的 TLS** 计划由反向代理或 Tailscale 承担，不写 Go 代码；此路径尚未验证。
3. **薄壳选型**（Tauri vs 裸 Electron）未定。
4. **编辑器选型**：用户明确只需要文件编辑与临时文件暂存，不需要完整代码编辑能力。Monaco 为此付出约 20 MB 构建产物（`ts.worker` 单文件 11 MB）代价过高，应评估 CodeMirror 6 或裁掉 language worker 的 Monaco。CodeMirror 的实际体积与集成成本尚未实测。
5. **临时文件的落点**：建议由 agentd 托管一个 scratch 工作区（如 `~/.handoff/scratch/`），复用既有文件 REST 接口，落在真实磁盘目录上，使其可被 Finder、终端与外部编辑器直接打开。尚未设计。

## 与 ADR-0003 的冲突（未解决，需后续裁决）

ADR-0003「任务现场默认内嵌在桌面端」状态标注为「已接受」，其内容要求「支持原生 TUI 的 executor 保留原生 TUI」。这与桌面端设计规格 §41 以及 `docs/superpowers/specs/2026-08-10-handoff-detmux-prochost-design.md` 的结论「复现原生 TUI 已实证不可行」相矛盾。ADR-0002 与 ADR-0006 都规范标注了取代关系，ADR-0003 没有。本 ADR 不裁决该冲突，仅记录之，以免后续工作被其误导。
