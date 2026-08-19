# 更新这件事该在哪儿做（B166）设计

> 状态：待实现
> 前身：`2026-08-19-desktop-cli-sync-and-update-notice-design.md`（下称「同步 spec」）。
> 本 spec **推翻它的两条非目标**：「自动下载并替换 `.app` 自身」与「agentd 自查 + 控制台通知」，
> 兑现它 §11 风险③ 里点名的「真正的根治」。同步路（内嵌二进制 → agentd）本身不变。

## 1. 触发

rc12 真机走查后，用户装上 v0.3.0 桌面端、发布 v0.3.1，然后问「怎么没显示更新」。
提示确实在——在 macOS 菜单栏图标的下拉菜单里。用户的原话：

> 「但是放那里干嘛呀，我不要托盘。打开的时候右下角弹出一个提示框就行，点击直接更新。
> 在设置页也加一个页面，更新页面，可以升级执行机，可以直接下载安装。」

两个独立的问题被这一句话同时点破：

1. **提示放错了地方。** 菜单栏图标是「常驻但没人看」的位置。用户一天里盯着的是控制台窗口。
2. **提示到此为止。** 点了「有新版可下载」只会打开 GitHub release 页，剩下的下载、拖进
   应用程序、重开，全是手工活。同步 spec 当时把自我替换列为非目标，理由是「另一个量级」——
   这个判断对，但代价现在结算了：用户装了新版才发现提示形同虚设。

顺带确认的一个缺陷：`desktop/main.go:206-207` 只 `SetLabel("handoff")`、从不设图标，
所以 macOS 菜单栏里显示的是「handoff」四个字而不是标志。

## 2. 目标与非目标

**目标**

- 有新版时，在控制台窗口内右下角弹提示；点主按钮即完成「下载 → 校验 → 换版 → 重启」，中途不需要用户去别处。
- 控制台设置区新增「更新」页：看得到本机与各执行机的版本、同步状态，能一键升级执行机。
- 托盘只剩「打开控制台」「退出（agentd 继续运行）」，并显示标志而非文字。
- 三条原本挂在托盘上的动态提示（有新版 / 有更新待应用 / 上次同步失败）全部迁到控制台。

**非目标（明确排除）**

- **Linux 桌面端自更新。** AppImage 能替换、`.deb` 不 sudo 装不了，两条路的形态完全不同，
  而本仓库的 Linux 桌面端至今没有真机走查记录。Linux 上「更新并重启」降级为打开发布页。
- **自动更新。** 不做静默后台替换。任何换版都由用户点一次按钮触发。
- **回滚。** 与 CLI 换版同一条纪律（同步 spec D10）：旧件留一份 `.old`，回退是人工动作。
- **改同步路本身。** 内嵌二进制 → agentd 的两道闸、强制入口、等回来的判据一概不动。
- **给纯浏览器用户装桌面端。** 更新页的「桌面应用」整块只在薄壳里渲染。

## 3. 承重现状（改之前必须知道的）

| 事实 | 出处 | 为什么承重 |
|---|---|---|
| 控制台在薄壳里是**外链页面**（agentd 伺服），Wails 运行时不注入 | `web/src/app/lib/desktopShell.ts` 文件头 | 控制台**够不着**壳，壳也**推不动**控制台。本 spec 一半的复杂度来自这一条 |
| UA 后缀 `handoff-desktop` 是唯一不改握手协议就能传进控制台的信号 | 同上；`desktop/main.go:51` | 「是不是桌面端」只能这么判 |
| 内嵌前端的页面（向导、升级面板）**有** Wails 运行时，`Events.Emit` 可用 | `desktop/panel.go:57` 的 `URL: "/upgrade.html"` | 自更新的进度可以走面板；控制台的进度不行 |
| `GET /api/machines` 已经扇出探活，返回每台的 `version` / `active_tasks` / `reachable` | `internal/agentd/machines.go:38`、`internal/proto/projects.go:109` | 更新页的**读**侧不需要任何新端点 |
| `handoff upgrade` 的编排（七种结论、两道闸、部分失败不中断、本机排最后）在 CLI 里 | `cmd/upgrade.go` 文件头 | 控制台要升级执行机，必须复用它而不是重写 |
| 新版检查有 24h 缓存，与 CLI 共用一个文件 | `desktop/internal/shell/latest.go:40`、`internal/selfupdate/clicheck.go` | GitHub 匿名限流 60 次/小时/IP；多消费者各查各的正是触发限流的方式 |
| 发布物：`handoff-desktop_<tag>_darwin_arm64.dmg` / `_windows_amd64.zip`（单个 exe）/ `_linux_amd64.{AppImage,deb}`，sha256 都在 `checksums.txt` 里 | `.github/workflows/release.yml:676,769,809` | 自更新的下载与校验有现成素材 |
| macOS 上**覆盖写**已存在的可执行文件会被 `com.apple.provenance` 钉住 inode，结果是进程被 SIGKILL | 记忆 `handoff-binary-sigkill-after-rebuild` | 换 `.app` 必须「新建 → 改名就位」，不能原地覆盖 |
| Wails v3 beta.8 的 `SystemTray` 有 `SetTemplateIcon` / `SetIcon` / `SetDarkModeIcon`，且不注入任何默认图标 | `pkg/application/systemtray.go:231,242,275` | 现在显示文字纯粹是因为没人设过图标 |

## 4. 形态（已确认的验收基准）

形态经 fork 副本 `prototypes/desktop-update/` 走查确认（2026-08-19）。真实页面开发完成后
**对照该副本验收**，不另起标准。

**控制台右下角提示框**（`index.html`）

- 打开控制台后弹出，三种：`有新版 <tag> 可下载` / `有更新待应用（N 个任务进行中）` / `上次同步失败`。
- 每条：标题 + 一句说明 + 主按钮 + 次按钮（关闭本条）+ 右下角「查看详情」跳更新页。
- 主按钮就地转成进度条，完成后文案变「已下载 <tag>，正在重启…」，不跳窗口。
- 可堆叠；与 home 悬浮窗共用右下角，**home 打开时整摞上移让位**（原型实测撞出来的，
  基准站里这一格本来就被占着）。

**设置 → 更新页**（`pages/settings.html`）

分区导航在「Env 文件」之后加「更新」，有可用更新时挂一个琥珀点。页面三块：

1. **桌面应用**：当前/最新版本、发布时间、体积、折叠的变更摘要、「下载并重启」+「重新检查」。
   只在薄壳里渲染。
2. **同步状态**：待应用（含活跃任务数）与上次同步结果各一行，待应用那行给「立即应用」。
   这里是提示框消失后这些状态**唯一的常驻落点**。
3. **执行机**：机器 / agentd 版本 / 状态 / 操作。本机那行标「随桌面应用一起更新」且**不给按钮**
   （理由见 §6.5），其余可升级的给「升级到 `<tag>`」。

**托盘**：只剩「打开控制台」与「退出（agentd 继续运行）」，图标换成标志。

## 5. 架构：一条新通道，两条老路

问题的核心是 §3 第一行：控制台与薄壳互相够不着。三种解法比过：

| 方案 | 做法 | 判决 |
|---|---|---|
| A. 壳自己开一个右下角无边框小窗当提示框 | 内嵌前端，运行时齐全，零新端点 | **否**。用户明确选了「控制台页面内的 toast」，另开一个窗口是又一个「放那里没人看」的面 |
| B. 复用 `webkit.messageHandlers.external` 内部协议 | `requestTitlebarZoom` 已经这么干 | **否**。那条用法自我限定为「纯锦上添花的手势，绿色按钮始终能做同一件事」；换版不是锦上添花，Wails 升级后静默失效的代价太大 |
| C. **经 agentd 中转**：壳上报状态、领指令；控制台读状态、下指令 | 三个新端点 + 壳侧一条长轮询 | **采用**。双方都已经是 agentd 的客户端，不引入新协议、新服务、新信任域，且两侧都能用普通 go test 覆盖 |

于是三条通道各司其职，互不侵犯：

```
                  ┌──────────────── 桌面薄壳（Wails） ────────────────┐
                  │  自更新执行器（下载/校验/换版/重启）              │
                  │  同步路（内嵌二进制 → agentd）—— 本 spec 不动它   │
                  └───────▲──────────────────────────┬───────────────┘
                          │ PUT /api/desktop/state   │ GET /api/desktop/commands（长轮询）
                          │ （含心跳）               ▼
                  ┌────────────────────── agentd ──────────────────────┐
                  │  内存持有壳状态（带 TTL）+ 至多一条待领指令        │
                  │  GET /api/update/latest   （缓存的最新 tag）       │
                  │  POST /api/machines/{name}/upgrade（升执行机）     │
                  └───────▲────────────────────────────────────────────┘
                          │ 会话 cookie
                  ┌───────┴──────────── 控制台（外链页面） ───────────┐
                  │  右下角提示框 · 设置→更新页                       │
                  └───────────────────────────────────────────────────┘
```

**为什么状态放内存而不落盘**：壳没在跑就等于没有壳。落盘会让「上次开过桌面端」的痕迹
在纯浏览器会话里伪装成「现在有个壳」，于是控制台渲染出一个点了没反应的「下载并重启」。

## 6. 组件

### 6.1 壳 ↔ 控制台通道（`internal/agentd/desktopchan.go`，新建）

**状态**

```go
// DesktopState 是薄壳向控制台公开的自身状态。字段全部由壳填，agentd 只转发。
type DesktopState struct {
    AppVersion   string `json:"app_version"`             // 薄壳自身版本（embedbin.Version）
    LatestTag    string `json:"latest_tag,omitempty"`    // 壳查到的最新版；无更新时为空
    SelfUpdate   string `json:"self_update"`             // supported / unsupported_platform / unwritable
    SyncPlan     string `json:"sync_plan"`               // skip / blocked / failed / done
    SyncBusy     int    `json:"sync_busy"`               // blocked 时的活跃任务数；-1 = 探不出
    SyncError    string `json:"sync_error,omitempty"`
    Job          *DesktopJob `json:"job,omitempty"`      // 正在跑的指令；nil = 空闲
}

// DesktopJob 是一次进行中的壳侧动作，供控制台画进度。
type DesktopJob struct {
    ID     string `json:"id"`
    Action string `json:"action"`             // self_update / force_sync / check_update
    Stage  string `json:"stage"`              // 人话阶段：下载中 / 校验中 / 换版中 / 即将重启
    Percent int   `json:"percent"`            // 0-100；不可知时 -1
    Error  string `json:"error,omitempty"`
}
```

- `PUT /api/desktop/state`：壳上报，同时是心跳。
- `GET /api/desktop/state`：控制台读。**壳超过 `desktopStateTTL`（30s）没上报就返回 204**，
  控制台据此把「桌面应用」整块与提示框一起收起来。
- 壳的长轮询（下条）每次返回时顺带续期，所以正常情况下心跳不需要额外定时器；
  长轮询断了才靠 TTL 兜底。

**指令**

- `POST /api/desktop/commands`：控制台下指令，体 `{"action":"self_update"}`。
  三种 action：`self_update`（换版并重启）、`force_sync`（越过闸一立即同步 agentd）、
  `check_update`（绕过 24h 缓存立刻查一次新版）。
  - 201 + `{"id":"..."}`：已入队
  - 409：已有一条在跑或在队里（同时只允许一条）
  - 503：当前没有壳在线（state 已过期）
- `GET /api/desktop/commands?wait=25s`：壳长轮询领取。有指令立刻返回，没有就挂到超时返回 204。
  **超时用 204 而不是错误**：长轮询的空转是正常态，写成错误会让壳侧日志被刷满、真故障反而看不见。

**壳侧循环**（`desktop/internal/shell/deskchan.go`，新建）：起一个 goroutine，
`领指令 → 执行 → 边执行边 PUT 进度 → 回到领指令`。任何 HTTP 失败都退避重试，
**永不让通道故障影响本地功能**——托盘的两项、控制台加载、同步路都不经过它。

### 6.2 桌面端自更新（`desktop/internal/selfupdate/`，新建）

接口按平台分文件，共用一个入口：

```go
// Apply 下载 tag 对应的桌面端发布物、校验、换版，并安排重启。
// progress 每进一个阶段回调一次；返回 nil 表示换版完成、重启已安排。
func Apply(ctx context.Context, tag string, progress func(stage string, percent int)) error
```

**共同前半段**（`selfupdate.go`）：`release.Client.Latest` 拿 release → 从 `checksums.txt`
取本平台桌面资产的 sha256 → 下载 → 比对。校验不过一律中止，**绝不解包**。

**macOS**（`darwin.go`）

1. 定位自身 bundle：`os.Executable()` 上溯三级到 `*.app`。不是 `.app` 结构就报
   `unsupported_layout`（开发构建直接跑二进制时会走到这里）。
2. 父目录不可写 → 返回 `unwritable`，UI 降级为「打开 DMG，请手动拖进应用程序」。
3. `hdiutil attach -nobrowse -readonly -mountpoint <tmp>/mnt`。
4. **验签**：对挂载点里的 `.app` 跑 `codesign --verify --strict` 与 `spctl -a -t exec`。
   sha256 只证明「和 checksums.txt 一致」，而 checksums.txt 与资产同源；验签是独立的一道。
   任一不过即中止并 detach。
5. 换版三步，**不原地覆盖**（§3 provenance）：
   `ditto <mnt>/handoff-desktop.app <dir>/handoff-desktop.app.new`
   → `mv <dir>/handoff-desktop.app <dir>/handoff-desktop.app.old`
   → `mv <dir>/handoff-desktop.app.new <dir>/handoff-desktop.app`。
   两次 `mv` 同目录、近乎原子，失败窗口里旧件始终存在。
6. `hdiutil detach`；`.old` 不当场删（进程正跑在它里面），留给下次启动清。
7. 重启：`SysProcAttr{Setsid:true}` 派一个 `/bin/sh -c 'while kill -0 <pid>; do sleep 0.2; done; open <app>'`，
   然后 `app.Quit()`。**用 Setsid 系统调用，不是 `setsid` 命令**——macOS 没有那个命令
   （记忆 `plan-acceptance-vs-dispatch-discipline`）。

**Windows**（`windows.go`）

1. zip 里只有 `handoff-desktop.exe`，解到同目录 `handoff-desktop.exe.new`。
2. 运行中的 exe **可以改名**：`handoff-desktop.exe` → `handoff-desktop.exe.old`，
   再把 `.new` 改名就位。
3. 重启：`cmd /c timeout /t 2 /nobreak >nul & start "" "<exe>"`（`hideConsole` 压掉黑窗），
   然后退出。没有单实例守卫（已确认），两秒延迟只为让旧进程先走干净。
4. `.old` 下次启动时删。

**Linux**：`Apply` 直接返回 `ErrUnsupportedPlatform`，壳把 `self_update` 报成
`unsupported_platform`，控制台把主按钮换成「打开发布页」。

**启动时清理**：壳启动后台跑一次，删掉同目录的 `*.old`。删不掉只记日志——那只是占盘。

### 6.3 控制台提示框（`web/src/app/update/UpdateToasts.tsx`，新建）

- 挂在 Shell 顶层，`isDesktopShell()` 为假直接不渲染（浏览器用户永远看不到）。
- 数据来自 `GET /api/desktop/state` 轮询（空闲 10s；`job` 非空时 1s）。
- 三条的出现条件：`latest_tag` 非空 / `sync_plan == "blocked"` / `sync_plan == "failed"`。
- 主按钮 → `POST /api/desktop/commands`；此后进度直接读 `state.job`，**不自己维护进度状态**。
  只有一个真相源，壳重启或页面刷新后进度依然接得上。
- **每条按 `(kind, tag)` 记一次「本次已关闭」到 sessionStorage**：关掉之后同一次会话里不再弹。
  不做永久免打扰——用 localStorage 会让「我上个月点过稍后」永久吃掉提示，那正是本 spec 要修的病。
- 与 home 悬浮窗的让位：home 开合时给容器加类，`bottom` 从 20px 抬到 236px（原型里的实测值）。

### 6.4 控制台更新页（`web/src/app/settings/UpdatePage.tsx`，新建）

- `SECTIONS` 加一项 `{ key: 'update', label: '更新' }`，接在 `env` 之后。
- 三块的数据源：桌面应用块 = `GET /api/desktop/state`；同步状态块 = 同上；
  执行机块 = `GET /api/machines`（已有）+ `GET /api/update/latest`（新，见 6.5）。
- 桌面应用块在 `state` 为 204 时整块不渲染，同步状态块同理。**执行机块始终渲染**——
  它对纯浏览器用户同样有用，而且是本页唯一浏览器里也能用的能力。
- 「重新检查」→ `POST /api/desktop/commands{check_update}`，由壳重跑一次 `CheckLatest`
  并回写共享缓存，结果经 `state.latest_tag` 回来。**不走 agentd 自己查**：
  壳才是那个要被更新的东西，让它自己去问，结论与它接下来要下载的资产必然同源。
  **只在用户显式点击时才绕过缓存**，自动路径一律走缓存（§3 限流）。

### 6.5 执行机一键升级

**`GET /api/update/latest`**（属计划一）：返回缓存里的最新 tag 与检查时间。
读 `selfupdate.LoadCLICheck`，陈旧则查一次并回写——与壳、CLI 共用同一个缓存文件，
这正是要的。执行机块靠它判断「可升级」，浏览器用户同样需要，所以它不能等到计划二。

**`POST /api/machines/{name}/upgrade`**：把一台远端机器升到最新。

- **不重写编排**。把 `cmd/upgrade.go` 里**单台机器**那段（`machineState.remoteUpgrade` 及其
  两道闸判据、pull/push 择路、等新版本上线）原样抽到 `internal/upgrade` 包，
  CLI 与 agentd 各自调它。CLI 侧只保留表格渲染与「本机排最后」的编排。
  抽取时**保持现有的缝**（`releaseFetcher` / `agentdPeer` / `recordOrder`）——那些缝是
  现有测试能替身化整套远端的原因，破坏它们等于 CI 上真的去动机器。
- 状态码：202 已受理（体带 job id）/ 404 机器不存在 / 409 闸一（有活跃任务，体带
  `busy` 与「可强制」标记）/ 422 闸二（非托管，**永不给强制入口**）/ 502 够不着。
- 进度：复用 `GET /api/machines` 的版本字段即可——升级完成的判据就是那台的 `version` 变了。
  不为它再造一条进度流。
- **本机不给按钮。** 本机 agentd 的版本由薄壳的同步路决定（同步 spec D1），
  在这里再给一个入口就是第二条换版路径，两条会打架。页面上写清「随桌面应用一起更新」。
  纯 CLI 安装的机器仍然用 `handoff upgrade`，那条路不动。

### 6.6 托盘瘦身与图标（`desktop/main.go`）

- `rebuildTray` 删掉四项（三条动态提示 + 升级执行机），只留「打开控制台」与「退出」。
  函数随之退化成常量菜单，`traySync` / `traySyncErr` / `trayLatest` 三个包级变量**不删**——
  它们现在是 6.1 上报给控制台的数据源，改喂 `PUT /api/desktop/state`。
- `showBlockedPanel` / `showSyncFailurePanel` / `openReleasePage` 三个入口删除；
  `forceSyncNow` 保留，改由 `force_sync` 指令触发，进度打进 `DesktopJob.Stage`。
  `runRemoteUpgrade` 与 `desktop/remote_upgrade.go` 整体删除（能力移到 6.5）。
  升级面板 `panel.go` 保留——自更新失败时仍用它显示原文（它有 Wails 运行时，能显示多行）。
- 图标：`desktop/build/trayicon.png`（单色 + alpha，22×22@2x）经 `//go:embed` 带进来。
  macOS 走 `SetTemplateIcon`（系统自动按明暗菜单栏反色），Windows/Linux 走 `SetIcon`
  的彩色版 `desktop/build/appicon.png`。`SetLabel` 改为空串。

## 7. 一次完整更新的时序

```
壳启动 → CheckLatest（24h 缓存）→ 有新版 → PUT state{latest_tag}
用户打开控制台 → 轮询 GET state → 右下角弹「有新版 v0.3.1 可下载」
用户点「更新并重启」→ POST commands{self_update} → 201
壳的长轮询返回 → selfupdate.Apply
    ├ PUT state{job:{stage:"下载中",percent:37}}   ← 控制台 1s 轮询，进度条动
    ├ PUT state{job:{stage:"校验中"}}
    ├ PUT state{job:{stage:"换版中"}}
    └ PUT state{job:{stage:"即将重启"}} → 派重启进程 → app.Quit()
控制台轮询转 204（壳没了）→ 提示框定格在「正在重启…」
新版壳起来 → 同步路把 agentd 也换到同一版本 → PUT state{sync_plan:"done"}
```

## 8. 错误处理与降级

| 情况 | 处置 |
|---|---|
| 校验/验签不过 | 中止，`job.Error` 带原文，控制台提示框转红并给「打开发布页」 |
| 目录不可写 | `self_update: "unwritable"`，按钮文案直接就是「打开安装包」，不假装能自动装 |
| 换版成功但新版起不来 | 不自动回滚。`.old` 还在，文档给一行手工恢复命令 |
| 通道整条不通（agentd 挂了） | 控制台本来也加载不出来。壳的托盘两项与同步路不受影响 |
| 壳在 job 中途被杀 | state 30s 后过期 → 控制台收起提示框。下次开壳重新检查，不留半截状态 |
| 控制台里点了升级执行机，那台正忙 | 409 + `busy`，页面就地给「仍要升级」（越闸一），**非托管的 422 不给这个按钮** |
| GitHub 限流 | `CheckLatest` 既有约定：任何失败一律静默当作没有新版 |

## 9. 日志与注释

按 `instrumenting-code`，以下点位必须有日志（Go 侧 `slog`，前端 `console` 不算）：

- 壳：领到指令、每个换版阶段、换版成功（含新旧版本）、任一失败分支（带 cause）、重启已安排。
- agentd：state 上报与过期、指令入队/领取/超时空转（Debug）、执行机升级的受理与两道闸的拒绝理由。
- 每个新文件写文件头「职责 + 边界」；每个导出函数写参数/返回/注意事项；
  §3 表里的每条承重事实在对应代码处留一句「为什么」的中文注释。

## 10. 测试

| 层 | 覆盖 |
|---|---|
| `internal/agentd` | state 的上报/读取/TTL 过期返回 204；指令入队 409、无壳 503、长轮询超时 204；升级端点四种状态码 |
| `internal/upgrade`（抽取后） | 把 `cmd/upgrade_test.go` 里针对单台机器的用例整体迁过来，**一条不减**；CLI 侧保留表格与顺序的用例 |
| `desktop/internal/selfupdate` | 平台无关部分（tag→资产名、checksums 解析、sha256 不符即中止）用真实文件跑；平台相关部分把 `hdiutil`/`ditto`/`mv` 抽成缝，测试注入假实现，断言**调用序列**（先 ditto 再 mv，绝不出现原地覆盖） |
| `desktop/internal/shell` | 通道循环：领到指令即执行、HTTP 失败退避不中断、执行中不再领第二条 |
| `web` | 提示框三种出现条件与关闭后不再弹；`isDesktopShell` 为假时一条都不渲染；更新页在 204 下只渲染执行机块 |
| 真机 | macOS：v0.3.1 → 下一个版本走完整条自更新，确认换版后 `.app` 能被 Gatekeeper 放行且 `.old` 被清；Windows：同上走 zip 路径 |

**真机走查那条留给审核者本地做，不派发**——它要驱动桌面端自身，与派发纪律块冲突
（记忆 `plan-acceptance-vs-dispatch-discipline`）。

## 11. 分期

两个计划，边界是「读」与「写」：

- **计划一（形态与自更新）**：6.1 通道、6.2 自更新、6.3 提示框、6.4 更新页三块、6.6 托盘，
  外加 6.5 里的 `GET /api/update/latest`。执行机块此时**只读**：显示版本与「可升级 / 已是最新」，
  不给升级按钮。这一期做完，用户报的那个问题就已经解决了。
- **计划二（执行机升级）**：6.5 的编排抽取 + `POST /api/machines/{name}/upgrade` + 执行机块的按钮。
  抽取 `cmd/upgrade.go` 是这两期里唯一会碰到既有关键路径的改动，单独一期好回滚。

## 12. 风险

**① 抽取 `cmd/upgrade.go` 会动到一条已经在生产上跑的路径。** 缓解：抽取是纯搬家，
不改任何判据；现有测试整体迁移、一条不减；计划二独立成期，出问题可单独回退。

**② macOS 换版的失败窗口。** `mv` 旧件与 `mv` 新件之间有一瞬 `.app` 不在原位。
两次都是同目录改名，失败概率极低，且失败时 `.old` 或 `.new` 必有一个在——
文档要给出这两种残留的手工恢复步骤。

**③ 提示框可能被当成广告。** 缓解：关掉之后本次会话不再弹；只有真有新版时才出现；
更新页永远在，不靠提示框做唯一入口。

**④ 通道是新的信任面。** `POST /api/desktop/commands` 让任何已鉴权的控制台会话都能触发
本机桌面端换版。与既有的「托盘强制同步」同一级别（都是本机、都要鉴权），不新增暴露面——
但 agentd 可能监听在局域网上，这条要在 README 的安全小节里点名。

## 13. 验收基准

1. 形态对照 `prototypes/desktop-update/` 副本：提示框三种形态、更新页三块、托盘两项。
2. macOS 与 Windows 各走一遍真机自更新，版本号确实变了、`.old` 被清、agentd 随后同步到同版本。
3. 菜单栏显示的是标志不是文字，且在明暗两种菜单栏下都看得清。
4. 浏览器（非薄壳）打开控制台：一条提示框都不出现，更新页只有执行机那一块。
5. 从控制台把一台远端执行机从旧版升到最新，`GET /api/machines` 里那台的 `version` 变了。
