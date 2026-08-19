# 桌面端形态评估：继续基于 Orca，还是另起？

日期：2026-08-11。结论落为 `docs/adr/0009-desktop-console-is-agentd-hosted-web-ui.md`。
本文只记录**证据**——尤其是实测部分，因为这些数据重新获取的成本很高。

## 起因

问题最初提为「基于 Orca 做二次开发是不是个好主意，还是自己写一个桌面端更省事」。分析过程中问题被两次重构：

1. 「二次开发」这个框架不成立（见下），真正的取舍是携带成本。
2. 真正的分歧不是 Orca 与否，而是 **Web UI 与原生桌面端**——用户明确表示不纠结 Orca，纠结的是 Web。

## 一、Orca 不是 fork，是 vendoring 快照

`desktop/UPSTREAM.md` 记载：Orca 以 MIT 许可、`rsync -a --exclude .git` 方式导入，pin 在 `v1.4.177-rc.0`，并显式声明**不回推上游**。

对 Orca 既有代码的实际改动约 **60 行接线**（挂载组件、注册 IPC、expose preload、加类型声明）。1485 行插入中有 1264 行是 handoff 自建文件。

因此经典 fork 税（上游同步、分歧堆积）不适用。真正的成本是**永久携带一个第三方应用**。

另一个改变问题性质的事实：`src/renderer/src/main.tsx:57` 已经是

```tsx
{import.meta.env.DEV && VITE_HANDOFF_ORCA_APP ? <App /> : <HandoffApp />}
```

即 **Orca 自己的 App 已被降级为 DEV fallback，handoff 才是默认渲染根**。现状本就是「拿 Orca 当壳」，不是「往 Orca 里加功能」。

## 二、规模与复用（已亲自复核）

| 项 | 实测值 |
|---|---|
| `desktop/src` TS/TSX 文件数 | 10,409 |
| `desktop/src` 总行数 | **2,417,935** |
| `node_modules` | **2.7 GB** |
| 声明依赖数 | 123 |
| handoff 自有代码 | **10,741 行 = 0.44%** |

> 统计陷阱：`find ... | xargs wc -l | tail -1` 会因 `xargs` 分批而只取到最后一批的小计。必须用 `awk '$2=="total"{t+=$1} END{print t}'` 求和。首次统计因此得出错误的 103,827 行。

**实际复用的 Orca 代码约 2,700 行**：Monaco 胶水链 ~1733、shadcn 组件 527、`browser-page-webview` ~360、`pane-terminal-options` 76、`unread-response-body` 12。

**复用为零的部分**：IPC 脚手架、状态管理、窗口管理、多 tab 工作台系统。handoff 自写了 1835 行工作台。

xterm 终端面也**不是**复用来的——`XtermSurface.tsx`（194 行）由 handoff 自建，Orca 自己的 `TerminalPane` 并不使用它。

## 三、耦合面（已亲自复核）

全部 handoff 代码指向 Orca 内部的 import 共 **18 行**：

| 目标 | 行数 | 性质 |
|---|---|---|
| `@/components/ui/*`（shadcn） | 10 | 可由 shadcn CLI 重新生成 |
| `@/components/terminal-surface/XtermSurface` | 3 | **handoff 自建文件**，仅位置在 Orca 树内 |
| `@/components/editor/MonacoSurface` | 1 | 可替换（用户只需文件编辑） |
| `@/components/browser-pane/browser-page-webview` | 1 | 可选功能（用户已表示可降级为右键系统浏览器打开） |
| `@/lib/pane-manager/pane-terminal-options` | 1 | 76 行配置，可复制 |
| `../lib/unread-response-body` | 2 | 12 行；浏览器下因不走 Node undici 而失去意义 |

**零条指向 Orca 的 store、窗口管理或 IPC 框架。** 这是刻意为之——`src/renderer/src/features/handoff/architecture-boundary.test.ts` 在 CI 层面禁止 import `/ssh`、`store/slices/repos`、`store/slices/worktrees`、`launch-agent`、`native-chat`、`runtime-environment`、`pty-transport.ts`、`port-forward`。

## 四、真正的二次开发税：中心注册表门禁

不是构建速度（typecheck 3.06s、`build:electron-vite` 6.80s、handoff 单测子集 9.64s——**构建速度不构成反对 Orca 的理由**），而是这些必须去中心文件登记的门：

- `global-fetch-call-site-audit.test.ts`：逐文件 pin `fetch(` 出现行数
- 本地化三重门：每条 UI 文案必须登记进 `i18n/locales/*.json`，19 类裸字面量被拒
- `config/max-lines-baseline.txt` 棘轮：300/400 行上限，基线只能缩不能增
- 新代码零豁免：`check-changed-code-quality.mjs` 把未跟踪文件整体视为新增行
- react-doctor 12 条规则、根 oxlint（`no-explicit-any` 为 error、禁 `interface`）、`--deny-warnings`、根目录守卫、preload/shared `.d.ts` 禁令

其中 **`BUILTIN_PARTITIONS` 是唯一一个不红 CI、直接功能静默死的门**——本次实跑踩到的缺陷 #4 正是它。

## 五、实测：xterm 补丁在 Web 下是否可用

`desktop/config/patches/` 有 5 个补丁，文件体积 7.4 MB，此前无人读过其内容，是「离开 Orca 是否会损失终端保真度」的唯一悬念。

### 补丁实际内容

体积大头是重新构建的 `lib/*.js` 与 sourcemap；真正改动的只有 **8 个 `.ts` 文件**。

| 包 | 修的是什么 | 环境相关性 |
|---|---|---|
| `@xterm/xterm` | ① **CompositionHelper 大改 = 输入法（中文）输入正确性**：事务 id、去重、blur 处理、三个自定义事件。② `SortedList.delete()` 上游 bug——marker 被 dispose 时 `line` 重置为 -1，而 `line` 就是排序键，二分查找因此漏掉它，`onDecorationRemoved` 永不触发，装饰永久残留 | 纯浏览器 DOM + 纯数据结构 |
| `@xterm/addon-webgl` | 纹理图集页数超出 shader sampler 预算时的驱逐与重建；shader 补 `else` 分支（不补是未定义行为，表现为花屏） | 纯 WebGL |
| `@xterm/addon-serialize` | SGR 往返修复（bold/dim 共用 reset 参数 22，必须成组 diff）、一个运算符优先级 bug、`\x1b[0C` 非法序列 | 纯算法 |
| `@xterm/addon-ligatures` | 703 字节：`module` 字段指向不存在的 `.mjs`，补 `exports` map | 纯打包器解析 |

**handoff 自己只用 `@xterm/xterm` + `addon-fit` + `addon-web-links`。** webgl / serialize / ligatures 全部是 Orca 自己的 pane-manager 在用。

serialize 尤其无关——它修的是「序列化终端缓冲区做快照恢复」，而 handoff 的断连回放在 Go 侧完成（agentd 的 `?incarnation=&after=` 直接流原始字节）。

### 浏览器实测结果

将打过补丁的包以 `<script type="module">` 直接加载进普通浏览器标签页（无 Electron）：

- **导入与渲染成功**，`xterm.mjs` 348 KB。ANSI 颜色、dim/bold/underline、中文宽字符宽度全部正确。
- **输入法实测**：模拟完整 composition 生命周期（`compositionstart` → `update`×2 → `compositionend '你好'` → `input insertCompositionText`）。结果 `onData` 收到 **`["你好"]`，恰好一次，无重复无丢失**；补丁新增的 `xterm-composition-transaction-accepted` 事件触发 1 次，证明执行的确实是**打过补丁的代码路径**。
- **WebGL**：addon 加载成功，取得真实 WebGL2 上下文，无 context loss，渲染正确。
- 四个包 `dependencies` 中无 electron，产物中搜不到 `require('electron')` 或 `node:` 内建。

**结论：补丁全部可用，且应当带走。** 输入法补丁在 Web 下更重要——需面对 Chrome / Safari（含 iOS）/ Firefox 多个引擎，而非单一 pin 死的 Chromium。

**边界**：测试使用的是合成 composition 事件，非真实输入法。真机（尤其 iOS Safari）行为可能有出入。

**可选瘦身**（未实测）：只 patch `lib/*.mjs`，丢弃 `.js` 与 `.map`——体积大头是 sourcemap。

## 六、实测：终端能否复现用户本人的终端与环境变量

### 机制

`internal/ptyservice/service_unix.go:43-45`：

```go
cmd := exec.Command(shell, "-l")
cmd.Dir = cwd
cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
```

关键事实（实测确认）：**`zsh -l` 挂在 PTY 上是交互式的**（shell flags 含 `i`），因此 `.zprofile` 与 `.zshrc` **都会被完整加载**。zsh 只对交互式 shell 读 `.zshrc`，而那正是用户放 PATH、alias、工具初始化的地方。

### 结果

以 launchd 式极简环境（`PATH=/usr/bin:/bin:/usr/sbin:/sbin`，仅 4 条）启动：

| 项 | 结果 |
|---|---|
| PATH | 从 4 条自行推导出 **40 条** |
| `brew` / `node` | 均正确解析到 `/opt/homebrew/bin/` |
| 提示符 | `.zshrc` 自定义主题完整渲染（颜色、主机名、时间戳） |
| 插件 | 自动补全 / 语法高亮均在运行 |
| `TERM` / `COLORTERM` | `xterm-256color` / `truecolor`（agentd 强制设定，对 xterm.js 正确） |

**唯一缺口：`SSH_AUTH_SOCK`。** 正常交互 shell 为 `/var/run/com.apple.launchd.<id>/Listeners`，PTY 中为 UNSET。缺失后该终端内 `git push`、`ssh`、私有仓库操作全部失败。它由 launchd 按会话注入，不来自 dotfiles，登录 shell 无法推导。

`TERM_PROGRAM` / `ITERM_SESSION_ID` 同样缺失但无实际影响；`LANG` 反被 dotfiles 设为 `C.UTF-8`（优于测试基准的 UNSET）。

**关键：结果随 agentd 的托管方式静默变化。** `os.Environ()` 是 agentd 进程自身环境：手工从交互终端启动则继承完整环境；用户级 LaunchAgent 大概率继承；LaunchDaemon 或远程机由 systemd/ssh 拉起则缺失。这与 B54.2 的服务化托管工作直接相关。

修法：创建终端时显式解析 `launchctl getenv SSH_AUTH_SOCK` 注入，或加一个会话级环境变量转发白名单。

**此项与形态无关**：规格 §150 禁止 Electron 主进程自起本地 PTY，终端必须走 agentd，因此 shell 环境由 Go 进程决定，前端渲染在哪个窗口毫无影响。

## 七、agentd 的网络就绪度（已复核）

- 默认监听 `127.0.0.1:7777`（`internal/config/config.go:130`），但远程 target 配置形态为 `devbox: {addr: "192.168.x.x:7777", token: "..."}`——**跨机网络访问是既有生产模型**。
- 鉴权：`internal/agentd/server.go` 的 Bearer 中间件包住全部 `/api` 与 `/ws`，constant-time 比较，且第 170 行显式做空 token fail-closed（注释说明了不能依赖 `subtle.ConstantTimeCompare("","")==1` 的原因）。此层无需重做。

三个缺口：

| 缺口 | 代价 |
|---|---|
| 无 TLS | 反向代理或 Tailscale 解决，**零 Go 代码** |
| 无 CORS | **agentd 自行托管前端即同源，不需要** |
| **`/ws/events` 在 Bearer header 中间件之后，浏览器 `new WebSocket()` 无法设置请求头** | **唯一真需改动**，约 20–30 行 Go |

最后一条承重：agent 总览与终端都依赖该 WS 流。

## 八、尚未动工的恰是最想要的

用户表述的核心诉求为六项：总览工作台、跨工作树快速查看文件、抹平本地与远程观感、查看所有运行中的 agent、以工作树目录为根快速开终端、任务看板。

其中**任务看板**与 **agent 总览**在代码中 grep 零命中——**一行未写**，且桌面端规格 §65-75 明确将独立任务看板推迟至第二阶段。成品设计图存在于 `prototypes/desktop-console/implementation-task-board-final.png`。

即沉没成本买到的（Monaco 编辑器套件、内嵌浏览器）恰是用户最不在意的部分。

移动端成色排序也指向同一处：任务看板与 agent 总览在手机上最好用，终端可看不宜敲，编辑器不适合。三件事收敛于同一起点。
