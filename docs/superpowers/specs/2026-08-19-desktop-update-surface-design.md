# 更新这件事该在哪儿做（B166）设计

> 状态：待实现
> 前身：`2026-08-19-desktop-cli-sync-and-update-notice-design.md`（下称「同步 spec」）。
> 本 spec 推翻它的非目标「agentd 自查 + 控制台通知」，但**保留**它的另一条非目标
> 「自动下载并替换 `.app` 自身」——理由见 §5。同步路（内嵌二进制 → agentd）本身不动。

## 1. 触发

rc12 真机走查后，用户装上 v0.3.0 桌面端、发布 v0.3.1，然后问「怎么没显示更新」。
提示确实在——在 macOS 菜单栏图标的下拉菜单里。用户的原话：

> 「但是放那里干嘛呀，我不要托盘。打开的时候右下角弹出一个提示框就行，点击直接更新。
> 在设置页也加一个页面，更新页面，可以升级执行机，可以直接下载安装。」

两个独立的问题被同时点破：

1. **提示放错了地方。** 菜单栏图标是「常驻但没人看」的位置。用户一天里盯着的是控制台窗口。
2. **提示到此为止。** 点了「有新版可下载」只会打开 GitHub release 页，剩下的下载、校验、
   拖进应用程序、重开，全是手工活。

顺带确认的一个缺陷：`desktop/main.go:206-207` 只 `SetLabel("handoff")`、从不设图标，
所以 macOS 菜单栏里显示的是「handoff」四个字而不是标志。

## 2. 目标与非目标

**目标**

- 有新版时，在控制台窗口内右下角弹提示；点主按钮由 agentd 下载安装包、校验 sha256、
  下完自动打开——用户只剩「拖进应用程序 + 重开」这一下。
- 控制台设置区新增「更新」页：看得到本机与各执行机的版本、同步状态，能一键升级执行机。
- 托盘只剩「打开控制台」「退出（agentd 继续运行）」，并显示标志而非文字。
- 三条原本挂在托盘上的动态提示（有新版 / 有更新待应用 / 上次同步失败）全部迁到控制台。

**非目标（明确排除）**

- **桌面端自我替换。** 见 §5：它要求一条控制台→薄壳的指令通道，而通道本身比它服务的
  那一个动作还贵。用户对此的原话是「下载完自动打开 dmg 算了，让用户自己点剩下的」。
- **自动更新。** 不做静默后台替换。任何下载都由用户点一次按钮触发。
- **强制同步入口。** 托盘上那条（同步 spec D4）随托盘瘦身一并删除，**不在控制台重建**。
  等效动作已经存在且更好解释：重开一次桌面端就会重走 `SyncOnOpen`。
- **改同步路本身。** 内嵌二进制 → agentd 的两道闸、等回来的判据一概不动。
- **给纯浏览器用户装桌面端。** 更新页的「桌面应用」整块只在薄壳里渲染。

## 3. 承重现状（改之前必须知道的）

| 事实 | 出处 | 为什么承重 |
|---|---|---|
| 控制台在薄壳里是**外链页面**（agentd 伺服），Wails 运行时不注入 | `web/src/app/lib/desktopShell.ts` 文件头 | 控制台**够不着**壳，壳也**推不动**控制台。§5 的整个取舍都建在这一条上 |
| UA 后缀 `handoff-desktop` 是唯一不改握手协议就能传进控制台的信号 | 同上；`desktop/main.go:51` | 「是不是桌面端」只能这么判 |
| `GET /api/machines` 已经扇出探活，返回每台的 `version` / `active_tasks` / `reachable` | `internal/agentd/machines.go:38`、`internal/proto/projects.go:109` | 更新页的执行机块不需要任何新端点 |
| `handoff upgrade` 的编排（七种结论、两道闸、部分失败不中断、本机排最后）在 CLI 里 | `cmd/upgrade.go` 文件头 | 控制台要升级执行机，必须复用它而不是重写 |
| agentd 已经会下载并校验发布资产（`release.Installer` + 代理配置） | `internal/release/install.go`、`cmd/upgrade.go` 的 `newReleaseFetcher` | 下载桌面端安装包是既有能力的一个新调用点，不是新能力 |
| 新版检查有 24h 缓存，与 CLI 共用一个文件 | `desktop/internal/shell/latest.go:40`、`internal/selfupdate/clicheck.go` | GitHub 匿名限流 60 次/小时/IP；多消费者各查各的正是触发限流的方式 |
| 发布物：`handoff-desktop_<tag>_darwin_arm64.dmg` / `_windows_amd64.zip`（单个 exe）/ `_linux_amd64.{AppImage,deb}`，sha256 都在 `checksums.txt` 里 | `.github/workflows/release.yml:676,769,809` | 下载与校验有现成素材 |
| 既有的 `POST /api/workspaces/reveal` 是**工作树内**揭示，`revealTarget` 会硬拒绝跑出工作树的路径 | `internal/agentd/reveal.go:84` | 打开下载目录里的安装包**不能**复用它——那正是它设计上要拒绝的事 |
| Wails v3 beta.8 的 `SystemTray` 有 `SetTemplateIcon` / `SetIcon` / `SetDarkModeIcon`，且不注入任何默认图标 | `pkg/application/systemtray.go:231,242,275` | 现在显示文字纯粹是因为没人设过图标 |

## 4. 形态（已确认的验收基准）

形态经 fork 副本 `prototypes/desktop-update/` 走查确认（2026-08-19）。真实页面开发完成后
**对照该副本验收**，不另起标准。

**控制台右下角提示框**（`index.html`）

- 打开控制台后弹出，三种：`有新版 <tag> 可下载` / `有更新待应用（N 个任务进行中）` / `上次同步失败`。
- 每条：标题 + 一句说明 + 主按钮 + 次按钮（关闭本条）+ 右下角「查看详情」跳更新页。
- 「有新版」的主按钮是**「下载」**，就地转成进度条，完成后文案变
  「已下载 <文件名>，已在访达中打开」，不跳窗口。
- 「有更新待应用」的主按钮是「知道了」——它不需要下载任何东西，只是说明为什么 agentd 还没换版。
- 可堆叠；与 home 悬浮窗共用右下角，**home 打开时整摞上移让位**（原型实测撞出来的，
  基准站里这一格本来就被占着）。

**设置 → 更新页**（`pages/settings.html`）

分区导航在「Env 文件」之后加「更新」，有可用更新时挂一个琥珀点。页面三块：

1. **桌面应用**：当前/最新版本、发布时间、体积、折叠的变更摘要、「下载安装包」+「重新检查」。
   下载完给一行「已下载到 <路径>，请拖进「应用程序」后重开」+「再次打开」。只在薄壳里渲染。
2. **同步状态**：待应用（含活跃任务数）与上次同步结果各一行。**不给「立即应用」按钮**
   （§2 非目标），待应用那行的说明写清「会在任务结束后自动应用；想立刻应用就重开一次桌面端」。
   这里是提示框消失后这些状态**唯一的常驻落点**。
3. **执行机**：机器 / agentd 版本 / 状态 / 操作。本机那行标「随桌面应用一起更新」且**不给按钮**
   （理由见 §6.5），其余可升级的给「升级到 `<tag>`」。

**托盘**：只剩「打开控制台」与「退出（agentd 继续运行）」，图标换成标志。

## 5. 架构：为什么不做自我替换

问题的核心是 §3 第一行：控制台与薄壳互相够不着。**动作放在哪一端，决定了要不要造一条通道。**

| 动作 | 谁做得了 | 代价 |
|---|---|---|
| 下载安装包、校验、打开 | agentd（它已经会下载校验，且与控制台同源） | 一个新端点 |
| 换掉 `.app` 自身、重启 | 只有薄壳 | **必须新造一条控制台→薄壳的指令通道**（长轮询领指令 + 进度回流 + job 状态机），外加两套平台换版代码（macOS：`hdiutil` 挂载、`codesign`/`spctl` 验签、`ditto` 到 `.new`、两次 `mv` 就位、派脱离进程等本进程退出后 `open`、`.old` 下次启动清；Windows：改名运行中的 exe 再换回来）|

这条通道存在的唯一理由就是让控制台点得动壳的换版。**它比它服务的那一个动作还贵**，
而且贵在最难验的地方：换版失败模式（换到一半、`.app` 不在原位、新版起不来）全部要靠
真机走查覆盖，单元测试只能断言调用序列。用户换回来的是「少拖一下鼠标」。

所以：**下载与校验交给 agentd，最后一下留给用户。** 于是全程只有一条方向——
壳与控制台都只**向 agentd 说话**，谁也不用推动谁：

```
        ┌──────────── 桌面薄壳（Wails） ────────────┐
        │  同步路（内嵌二进制 → agentd）—— 不动它   │
        └──────────────────┬────────────────────────┘
                           │ PUT /api/desktop/state（单向上报，兼心跳）
                           ▼
        ┌──────────────── agentd ───────────────────┐
        │  内存持有壳状态（带 TTL）                 │
        │  GET  /api/desktop/state                  │
        │  GET  /api/update/latest                  │
        │  POST /api/update/desktop/download        │
        │  POST /api/machines/{name}/upgrade（二期）│
        └──────────────────▲────────────────────────┘
                           │ 会话 cookie
        ┌──────────────────┴──── 控制台（外链页面）─┐
        │  右下角提示框 · 设置→更新页               │
        └───────────────────────────────────────────┘
```

**壳只上报、不接指令**，所以没有长轮询、没有指令队列、没有 job 状态机。

**为什么状态放内存而不落盘**：壳没在跑就等于没有壳。落盘会让「上次开过桌面端」的痕迹
在纯浏览器会话里伪装成「现在有个壳」，于是控制台渲染出一个点了没反应的按钮。

**为什么还需要这条上报**：控制台要判断「有没有新版」，就得知道**桌面端**的版本。
同步正常时 agentd 的版本等于桌面端版本，可以直接拿 agentd 的版本比——但同步被拦或失败时
两者恰好不等，而那正是另外两条提示要说的事。少了上报，被拦的用户会被劝去下载一个
**他已经装好了的**版本。一个端点换掉这个错误提示，值。

## 6. 组件

### 6.1 壳的状态上报（`internal/agentd/desktopstate.go`，新建）

```go
// DesktopState 是薄壳向控制台公开的自身状态。字段全部由壳填，agentd 只转发。
type DesktopState struct {
    AppVersion string `json:"app_version"`          // 薄壳自身版本（embedbin.Version）
    SyncPlan   string `json:"sync_plan"`            // skip / blocked / failed / done
    SyncBusy   int    `json:"sync_busy"`            // blocked 时的活跃任务数；-1 = 探不出
    SyncError  string `json:"sync_error,omitempty"`
}
```

- `PUT /api/desktop/state`：壳上报，同时是心跳。壳在启动序列末尾发一次，
  之后每 `desktopStateBeat`（10s）发一次；`noteSyncFailed` / `noteSyncBlocked` 处各补发一次。
- `GET /api/desktop/state`：控制台读。**壳超过 `desktopStateTTL`（30s）没上报就返回 204**，
  控制台据此把「桌面应用」块、「同步状态」块与提示框一起收起来。
- 上报失败一律只记日志重试，**永不影响本地功能**——托盘两项、控制台加载、同步路都不经过它。

### 6.2 下载桌面端安装包（`internal/agentd/updatedownload.go`，新建）

- `GET /api/update/latest`：返回缓存里的最新 tag 与检查时间。读 `selfupdate.LoadCLICheck`，
  陈旧则查一次并回写——与壳、CLI 共用同一个缓存文件，这正是要的（§3 限流）。
  `?refresh=1` 绕过缓存，**只允许用户显式点击时带**。
- `POST /api/update/desktop/download`：下载本平台的桌面端安装包。
  1. 按 `runtime.GOOS/GOARCH` 拼资产名（新增 `release.DesktopAssetName`，与既有
     `release.AssetName` 并列；两者前缀不同，**不要合并成一个带 flag 的函数**）。
  2. 从 `checksums.txt` 取该资产的 sha256（新增 `Installer.FetchChecksumFor(ctx, rel, assetName)`，
     把既有 `FetchChecksum` 里写死的名字解析提成参数）。
  3. 下载到 `<DataDir>/downloads/<资产名>`，比对 sha256。**不符即删除并报错，绝不留半个文件**。
  4. 成功后在文件管理器里打开：macOS `open <文件>`（DMG 会直接挂载并弹出拖拽窗口）、
     Windows `explorer /select,<文件>`、Linux `xdg-open <目录>`。
     **不复用 `POST /api/workspaces/reveal`**——那个端点的 `revealTarget` 会硬拒绝工作树外的
     路径，那是它的设计目的（§3）。这里新写一个只接受下载目录内路径的小函数。
- 进度：`GET /api/update/desktop/download` 返回当前下载的阶段与百分比（内存，单例）。
  同时只允许一个下载在跑，重复 POST 返回 409。
- 已经下载过同一个 tag 且 sha256 对得上：跳过下载，直接打开。

### 6.3 控制台提示框（`web/src/app/update/UpdateToasts.tsx`，新建）

- 挂在 Shell 顶层，`isDesktopShell()` 为假直接不渲染（浏览器用户永远看不到）。
- 数据来自 `GET /api/desktop/state` + `GET /api/update/latest`，轮询 10s；
  下载进行中时改 1s 轮 `GET /api/update/desktop/download`。
- 三条的出现条件：`app_version < latest` / `sync_plan == "blocked"` / `sync_plan == "failed"`。
  版本比较走 `selfupdate.CompareVersion`——只判「不相等」会造出反向提示（B59 抓到过实例）。
- 「下载」→ `POST /api/update/desktop/download`；进度直接读下载端点，**不自己维护进度状态**。
  只有一个真相源，页面刷新后进度依然接得上。
- **每条按 `(kind, tag)` 记一次「本次已关闭」到 sessionStorage**：关掉之后同一次会话里不再弹。
  不做永久免打扰——用 localStorage 会让「我上个月点过稍后」永久吃掉提示，那正是本 spec 要修的病。
- 与 home 悬浮窗的让位：home 开合时给容器加类，`bottom` 从 20px 抬到 236px（原型里的实测值）。

### 6.4 控制台更新页（`web/src/app/settings/UpdatePage.tsx`，新建）

- `SECTIONS` 加一项 `{ key: 'update', label: '更新' }`，接在 `env` 之后。
- 数据源：桌面应用块与同步状态块 = `GET /api/desktop/state` + `GET /api/update/latest`；
  执行机块 = `GET /api/machines`（已有）+ `GET /api/update/latest`。
- 前两块在 `state` 为 204 时不渲染。**执行机块始终渲染**——它对纯浏览器用户同样有用，
  而且是本页唯一浏览器里也能用的能力。
- 「重新检查」→ `GET /api/update/latest?refresh=1`。

### 6.5 执行机一键升级（二期）

`POST /api/machines/{name}/upgrade`：把一台远端机器升到最新。

- **不重写编排**。把 `cmd/upgrade.go` 里**单台机器**那段（`machineState.remoteUpgrade` 及其
  两道闸判据、pull/push 择路、等新版本上线）原样抽到 `internal/upgrade` 包，
  CLI 与 agentd 各自调它。CLI 侧只保留表格渲染与「本机排最后」的编排。
  抽取时**保持现有的缝**（`releaseFetcher` / `agentdPeer` / `recordOrder`）——那些缝是
  现有测试能替身化整套远端的原因，破坏它们等于 CI 上真的去动机器。
- 状态码：202 已受理 / 404 机器不存在 / 409 闸一（有活跃任务，体带 `busy` 与「可强制」标记）
  / 422 闸二（非托管，**永不给强制入口**）/ 502 够不着。
- 进度：复用 `GET /api/machines` 的版本字段即可——升级完成的判据就是那台的 `version` 变了。
  不为它再造一条进度流。
- **本机不给按钮。** 本机 agentd 的版本由薄壳的同步路决定（同步 spec D1），
  在这里再给一个入口就是第二条换版路径，两条会打架。页面上写清「随桌面应用一起更新」。
  纯 CLI 安装的机器仍然用 `handoff upgrade`，那条路不动。

### 6.6 托盘瘦身与图标（`desktop/main.go`）

- `rebuildTray` 删掉四项（三条动态提示 + 升级执行机），只留「打开控制台」与「退出」，
  随之退化成常量菜单。`traySync` / `traySyncErr` / `trayLatest` 三个包级变量**不删**——
  它们现在是 6.1 上报的数据源。
- 删除：`showBlockedPanel` / `showSyncFailurePanel` / `openReleasePage` / `forceSyncNow`，
  以及 `desktop/remote_upgrade.go` 整个文件（能力移到 6.5）。
- **升级面板 `panel.go` 与 `frontend/upgrade.html` 一并删除。** 上面四个入口是它仅有的调用方；
  留着一个没人调的窗口，下一个读代码的人会以为它还在链路上。
- 图标：`desktop/build/trayicon.png`（单色 + alpha，22×22@2x）经 `//go:embed` 带进来。
  macOS 走 `SetTemplateIcon`（系统自动按明暗菜单栏反色），Windows/Linux 走 `SetIcon`
  的彩色版 `desktop/build/appicon.png`。`SetLabel` 改为空串。

## 7. 一次完整更新的时序

```
壳启动 → SyncOnOpen → PUT state{app_version, sync_plan}
用户打开控制台 → GET state + GET latest → app_version < latest → 右下角弹「有新版 v0.3.1 可下载」
用户点「下载」→ POST /api/update/desktop/download → 202
agentd 下载 → 校验 sha256 → 落到 <DataDir>/downloads/ → open
    控制台 1s 轮 GET download，进度条动 → 完成后文案变「已在访达中打开」
用户把新 .app 拖进「应用程序」，重开桌面端
新版壳起来 → 同步路把 agentd 也换到同一版本 → PUT state{sync_plan:"done"}
下次打开控制台：app_version == latest，提示框不再出现
```

## 8. 错误处理与降级

| 情况 | 处置 |
|---|---|
| sha256 不符 | 删除文件，端点返回 502 + 原文，提示框转红并给「打开发布页」兜底 |
| 下载失败 / 限流 | 同上。`GET /api/update/latest` 的失败沿用既有约定：静默当作没有新版 |
| 打开文件管理器失败 | **下载本身仍算成功**：返回 200 但带 `opened:false` 与文件绝对路径，页面显示路径让用户自己去找 |
| 壳没在跑（纯浏览器） | `GET /api/desktop/state` 返回 204，提示框与前两块整体不渲染 |
| 壳中途被杀 | state 30s 后过期 → 同上。不留半截状态 |
| 控制台里点了升级执行机，那台正忙 | 409 + `busy`，页面就地给「仍要升级」（越闸一），**非托管的 422 不给这个按钮** |

## 9. 日志与注释

按 `instrumenting-code`，以下点位必须有日志（Go 侧 `slog`）：

- 壳：每次上报的结论（版本 + plan）、上报失败（带 cause，Debug 级避免刷屏）。
- agentd：state 上报与过期；下载的开始（tag + 资产名）、校验通过/不通过（带两个 sha 值）、
  落盘路径、打开文件管理器的成败；执行机升级的受理与两道闸的拒绝理由。
- 每个新文件写文件头「职责 + 边界」；每个导出函数写参数/返回/注意事项；
  §3 表里的每条承重事实在对应代码处留一句「为什么」的中文注释——
  尤其是「不复用 `workspaces/reveal`」那条，否则下一个人一定会去复用。

## 10. 测试

| 层 | 覆盖 |
|---|---|
| `internal/agentd` | state 的上报/读取/TTL 过期返回 204；下载端点：sha256 不符即删文件并 502、重复 POST 409、同 tag 已存在则跳过下载、打开失败仍 200 且带 `opened:false`；升级端点四种状态码（二期）|
| `internal/release` | `DesktopAssetName` 三平台；`FetchChecksumFor` 按给定资产名解析 |
| `internal/upgrade`（二期抽取后）| 把 `cmd/upgrade_test.go` 里针对单台机器的用例整体迁过来，**一条不减**；CLI 侧保留表格与顺序的用例 |
| `desktop/internal/shell` | 上报循环：失败退避不中断、`noteSyncFailed` 后立即补发一次 |
| `web` | 提示框三种出现条件与关闭后不再弹；`isDesktopShell` 为假时一条都不渲染；更新页在 204 下只渲染执行机块；版本比较用 `CompareVersion`（塞一个 `v0.10.0` vs `v0.9.0` 用例，字典序会判反）|
| 真机 | macOS：从 v0.3.1 点「下载」，确认 DMG 落盘、sha256 一致、访达弹出挂载窗口；Windows：同上走 zip 路径与 `explorer /select,` |

真机那两条**留给审核者本地做，不派发**——它们要驱动桌面端自身，与派发纪律块冲突
（记忆 `plan-acceptance-vs-dispatch-discipline`）。

## 11. 分期

- **计划一（形态与下载）**：6.1 状态上报、6.2 下载端点、6.3 提示框、6.4 更新页、6.6 托盘。
  执行机块此时**只读**：显示版本与「可升级 / 已是最新」，不给升级按钮。
  这一期做完，用户报的那个问题就已经解决了。
- **计划二（执行机升级）**：6.5 的编排抽取 + 端点 + 执行机块的按钮。
  抽取 `cmd/upgrade.go` 是这两期里唯一会碰到既有关键路径的改动，单独一期好回滚。

## 12. 风险

**① 用户仍需手工完成最后一步。** 这是本 spec 明确接受的代价（§5）。缓解：下载完直接把
挂载窗口弹到用户面前，剩下的是一次拖拽；页面上把「拖进应用程序后重开」写成明确的一句话，
而不是让用户猜。**Windows 更糙**：运行中的 exe 覆盖不了，用户必须先退出应用——
页面要为 Windows 单独写这一句提示，不能两平台共用一份文案。

**② 抽取 `cmd/upgrade.go` 会动到一条已经在生产上跑的路径。** 缓解：抽取是纯搬家，
不改任何判据；现有测试整体迁移、一条不减；计划二独立成期，出问题可单独回退。

**③ 提示框可能被当成广告。** 缓解：关掉之后本次会话不再弹；只有真有新版时才出现；
更新页永远在，不靠提示框做唯一入口。

**④ 下载目录会堆积旧安装包。** 每个 20MB 左右。缓解：下载成功后删掉
`<DataDir>/downloads/` 里其它 `handoff-desktop_*` 文件，只留最新这一个。

## 13. 验收基准

1. 形态对照 `prototypes/desktop-update/` 副本：提示框三种形态、更新页三块、托盘两项。
2. macOS 与 Windows 各走一遍真机下载：文件落盘、sha256 与 `checksums.txt` 一致、
   文件管理器确实弹到了前面。
3. 菜单栏显示的是标志不是文字，且在明暗两种菜单栏下都看得清。
4. 浏览器（非薄壳）打开控制台：一条提示框都不出现，更新页只有执行机那一块。
5. 从控制台把一台远端执行机从旧版升到最新，`GET /api/machines` 里那台的 `version` 变了（二期）。
