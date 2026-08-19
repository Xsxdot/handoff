# Windows 桌面薄壳：人工走查清单

本文件只覆盖**必须有交互式桌面才能验的那部分**。能无头验的已由协调者在
win-b37 上验完，结论列在最后一节，不用重做。

走查需要一个能看到桌面的 Windows 会话。win-b37（`47.80.243.155`）的
`query session` 显示 console 会话虽 Connected，但**无登录用户、无 explorer**，
所以必须 RDP 登进去，或者换一台自己的 Windows 机器。

## 准备

从 Release 页下载 `handoff-desktop_<tag>_windows_amd64.zip`（rc4 起有此资产），
解压得到 `handoff-desktop.exe`。

**它没有签名**（macOS 那份有签名+公证，Windows 这份没有）。双击时
SmartScreen 会拦一次，要点「更多信息 → 仍要运行」。这是已知代价，不是缺陷。
解压后跑一次 `Get-ChildItem -Recurse <目录> | Unblock-File` 可以去掉
Mark of the Web，SmartScreen 就不再拦——**只对自己确认过来源的产物这么做**。

**必须是 rc6 或更新的版本。** rc5 及更早的薄壳在这台机器上启动会把 agentd 的
计划任务删掉重建，从而进入「每分钟弹一个控制台窗口 + 无法启动 agentd 弹框」
的状态（根因见下面「一个已经修掉的坑」）。

如果这台机器上已经装过 handoff CLI，薄壳会走「已有安装」分支、不释出内嵌的
那份。想验「首次安装」的完整路径，用一个干净账户，或者按下面的隔离办法跑。

## 五项走查

逐项记「通过 / 不通过 + 现象」。不通过时请把窗口截图和
`%USERPROFILE%\.handoff\` 下的内容一起留下。

### 1. 托盘常驻与菜单

双击 exe。

- [ ] 任务栏通知区出现 handoff 图标（图标应是项目图标，不是默认的空白方框）
- [ ] 左键/右键点它能弹出菜单，菜单项可点
- [ ] 菜单里没有明显坏掉的项（点了什么都不发生、或报错弹框）

图标是空白方框说明 `build/windows/icon.ico` 没被编进资源——那是构建链问题，
不是运行期问题，请记下来。

### 2. 关掉窗口不杀执行者

**这条最要紧。** 它是 B36 / B59 一路保护的招牌属性：薄壳只是个壳，关掉它
不该影响正在跑的任务。

- [ ] 先让 agentd 有活干（派一个任务，或至少确认 `handoff status` 正常）
- [ ] 点窗口右上角的 ×
- [ ] 窗口消失，但托盘图标**还在**（薄壳没退出）
- [ ] `handoff status` 仍正常、正在跑的任务仍在跑
- [ ] 从托盘菜单能把窗口再叫回来

### 3. 首次配置向导的三步交互

需要一台没配过的机器，或用干净账户。

- [ ] 首次启动直接弹出向导页（不是空白页、不是报错页）
- [ ] 问题能逐个前进，填错时有可见的校验提示
- [ ] 答完后 `%USERPROFILE%\.handoff\config.yaml` 生成，内容与所填一致
- [ ] **中途关窗则不落盘**：重开一次，中途点 ×，确认 config.yaml 没有生成
      （半截答案落盘会造出一份「配过但配错」的配置，此后向导再也不出现）

### 4. 目录选择器

向导里凡是要选路径的地方：

- [ ] 点「浏览」能弹出 Windows 原生的目录选择框
- [ ] 选中的路径正确回填到表单里（注意反斜杠有没有被吃掉或转义坏）

### 5. 释出的 CLI 能用

- [ ] `%LOCALAPPDATA%\Programs\handoff\handoff.exe` 存在
- [ ] 在 PowerShell 里跑它：`& "$env:LOCALAPPDATA\Programs\handoff\handoff.exe" version`
      —— 应打出版本号、`platform windows/amd64`
- [ ] 这个路径与 `install.ps1` 装的是同一个位置（所以命令行敲的 handoff 和
      桌面端用的是同一份，不会出现两个版本）

## 想复现「首次安装」而不动现有环境

用隔离的环境变量跑，四样都要换（**PATH 必须一起换**，否则
`exec.LookPath` 会命中你已装的 handoff，薄壳判「已有安装」就不释出了）：

```powershell
$root = "$env:TEMP\hdtry"
Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
New-Item -ItemType Directory "$root\home","$root\local" -Force | Out-Null
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = "C:\path\to\handoff-desktop.exe"
$psi.UseShellExecute = $false
$psi.EnvironmentVariables['USERPROFILE'] = "$root\home"
$psi.EnvironmentVariables['HOME']        = "$root\home"
$psi.EnvironmentVariables['LOCALAPPDATA'] = "$root\local"
$psi.EnvironmentVariables['PATH']        = 'C:\Windows\System32;C:\Windows'
[System.Diagnostics.Process]::Start($psi)
```

跑完看 `$root\local\Programs\handoff\handoff.exe` 是否生成。清理直接删 `$root`。

## 走查前必须先确认：三条前置（版本同步那条路径）

从 2026-08-19 起本文件还覆盖**版本同步与升级**（spec
`2026-08-19-desktop-cli-sync-and-update-notice-design.md`）。那条路径有三条前置，
**缺一条整轮走查都是空转而看着一切正常**：

1. **`handoff-desktop.exe` 必须是 release 构建**（带 `-tags embedbin`）。否则
   `PlanSync` 恒为 `SyncNoEmbed`，同步整条路不跑。判据是日志里
   `同步对账结果` 那行的 `plan` 字段——看到 `plan=no-embed` 就是构建不对
2. **已装 CLI 的版本号必须是 `vX.Y.Z` 形态。** 同步的判据是版本比较，而
   `CompareVersion` 只认三段整数；开发构建自报的是 `web-console-0c75a56c9+b161fix`
   这种分支+短号，解析不出就走保守分支 `use-existing`，**永远不同步**。
   判据 `handoff version`，看到分支名就说明装的是开发构建
3. agentd 是不是 release 构建——见下一节（老条目，仍然有效）

### 取证时别用中文串 grep

ssh 回来的输出里中文会被通道改写，`grep 同步完成` 这类**永远不匹配**，
而不匹配看起来和"没发生"一模一样。判据一律用退出码、英文字段名或数字：

```powershell
# 不要这样
handoff version | Select-String "版本"
# 要这样
handoff version | Select-Object -First 1
```

同理 `$LASTEXITCODE` 不属于「命令没找到」那条判据——命令没找到时它可能压根没被赋值。

---

## 走查前必须先确认：agentd 是不是 release 构建

薄壳只是个壳，控制台页面是 **agentd 伺服的**。而 agentd 只有用
`-tags embedweb` 构建时才含前端——默认标签构建出来的二进制里根本没有页面。

这台机器上如果跑的是开发构建，走查第 1 项会看到窗口打开、但里面是这张页面：

> **此二进制未嵌入前端构建产物** —— agentd 本身工作正常，只是这份二进制是用
> 默认标签构建的，不含 Web 控制台。

这**不是薄壳的缺陷**，握手全程正常（签票 200、消费 200）。判别办法：

```powershell
handoff status          # agentd 可用，但「版本」一栏是个 commit 短号而不是 vX.Y.Z
```

要真验控制台，得把 agentd 换成 Release 版二进制（`handoff_<tag>_windows_amd64.zip`
里的那个），再重启计划任务。

## 一个已经修掉的坑（rc7 修复）：双击快捷方式完全没反应

rc6 上走查第 1 项会看到：**双击桌面快捷方式什么都不发生**，而
`try-first-run.cmd` 却能正常打开向导。

根因是竞态。薄壳在 `ApplicationStarted` 后约 110ms 就完成握手并调
`win.SetURL()` 切到控制台外链，而此时窗口的 chromium 还没建出来。Wails
v3.0.0-beta.8 的 `windowsWebviewWindow.setURL` 直接 `w.chromium.Navigate(url)`，
**不判 chromium 是否已建好**（紧挨着的 `execJS` 判了）；上层 `SetURL` 只判
`w.impl != nil`，而 `w.impl` 早在 `run()` 调 `setupChromium()` 之前就被赋值。

失败形态最难认：**进程当场消失，没有 Go panic 栈**，stderr 只留一行
`Failed to unregister class Chrome_WidgetWin_0`。agentd 侧的取证是 ticket
一张张签发出去却**一张都没有被消费**——webview 从来没去请求那个 URL。

**向导那条路径一直正常**，因为它本来就等 `WindowRuntimeReady`。「向导能开、
快捷方式打不开」正是由此而来。修法是让控制台那条走同一个信号，且等待放在握手
**之前**（ticket 只有 60 秒寿命）。

如果这个现象在 rc7 之后重现，用桌面上的 `DEBUG-run-shell.cmd` 启动，看
`shell-log.txt` 最后一行是不是停在「加载控制台」。

## 一个已经修掉的坑（rc6 修复，留档以防复发）

rc5 上走查第 1 项时会看到：**每分钟弹一个终端窗口**，以及弹框
「无法启动 agentd: 托管并拉起 agentd: 任务已建并已触发，但 5s 内未复核到
agentd 进程存活」。

根因不在薄壳，在 `internal/service/windows.go` 的托管判据：`taskIsRunning` 只
认 `SCHED_S_TASK_RUNNING`（267009），而那个数字在 `schtasks` 的**「上次结果」**
一栏——那是上一次**启动尝试**的结果，不是任务此刻的状态。计划任务为模拟
KeepAlive 用「TimeTrigger 每分钟重复 + IgnoreNew」，于是 agentd 起来后**最多
60 秒**，下一个分钟边界的重复触发被拒，「上次结果」被改写成 `0x800710E0`
（-2147020576），267009 从此不再出现。真机采样（全程 `Status: Running`、
进程一直活着）：

```
[10:34:57] Status: Running | Last Result: 267009
[10:35:02] Status: Running | Last Result: -2147020576   ← 分钟边界，此后永远是它
[10:35:32] Status: Running | Last Result: -2147020576
```

于是 `EnsureRunning` 判 agentd 没跑 → `Install()` → `schtasks /Create /F` 删掉
重建 → 任务与活着的 agentd 失联 → 每分钟真的拉起一个新 agentd → 撞 DataDir
锁退出（控制台窗口就是这么来的）→ Install 自己的 5s 复核失败 → 弹框。

**为什么以前所有验证都没抓到**：每次检查都紧跟安装/启动之后，全落在判据唯一
正确的那 60 秒窗口里。

如果这个现象在 rc6 之后重现，先跑 `handoff service status`：agentd 明明活着却
报「已安装但未运行」，就是这条回来了。

**机器如果已经进了那个状态**，修法是让计划任务重新领养 agentd：

```powershell
Get-CimInstance Win32_Process -Filter "Name='handoff.exe'" | ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
schtasks /Run /TN handoff-agentd
```

几秒后 `schtasks /Query /TN handoff-agentd /V /FO LIST` 应报 `Status: Running`
且 `Last Result: 267009`，`handoff status` 恢复「可用」。

## 一个已知现象，别当缺陷

**无交互桌面下启动薄壳，约 3 秒后必 panic**，报
`edge.(*ICoreWebView2Controller).PutIsVisible(0x0)`，退出码 2。

根因在 Wails v3.0.0-beta.8 的 `pkg/application/webview_window_windows.go:1297`：

```go
w.visibilityTimeout = time.AfterFunc(3*time.Second, func() {
    if !w.webviewNavigationCompleted && w.chromium != nil {
        w.chromium.Show()   // ← 这里
```

守卫只判了 `w.chromium`（包装器）非空，**没判里面的 controller**。没有窗口站
时 controller 永远建不出来，3 秒到点就炸。有真实桌面时 controller 在窗口创建
期就绪、窗口初始 URL 是内嵌前端、导航瞬间完成，这个兜底定时器根本不会开火。

所以：**在 RDP 会话里正常双击如果也 3 秒崩，那才是问题**，请记下来；无头跑
崩掉是预期的。

## P2 换版后 schtasks 真拉起新版，且主动催缩短了等待

**Windows 与 macOS 在这一项上差别很大，是本条单列的理由。** Windows 的 KeepAlive 是
用「TimeTrigger 每分钟重复 + `MultipleInstancesPolicy=IgnoreNew`」模拟的
（`internal/service/windows.go:150` 的 `<Interval>PT1M</Interval>`），**不是 launchd 那种秒回**。
agentd 自更新退出后最坏要等 60 秒才被拉回来，所以代码里在首次探测失败后主动催一次
`schtasks /Run`。

准备：让已装的 CLI 比 `.exe` 内嵌的旧。记下当前版本与计划任务的进程。

```powershell
handoff version | Select-Object -First 1
Get-Process handoff -ErrorAction SilentlyContinue | Select-Object Id, StartTime
```

双击 `handoff-desktop.exe`，然后看壳的日志：

```
已催进程管理器拉起 agentd
agentd 已带新版本回来 version=v0.3.1 attempts=N
```

**判据**：

- 两行都出现，且 **`attempts` 明显小于 120**。120 = 60 秒 ÷ 500ms 的轮询间隔，
  即「完全靠每分钟那次重复触发」的情形。催生效了，`attempts` 应该在几十以内
- `handoff version` 与 `handoff status` 的版本都变成新的
- handoff 进程的 **Id 变了**（证明真的重启过，不是只换了磁盘上的文件）

**判否说明什么**：

- `attempts` 接近或超过 120 = 那次主动催没生效，退化成干等重复触发。
  用户每次升级都要多等最多一分钟
- 只出现「已催」不出现「已带新版本回来」= 催了但没起来，去看
  `schtasks /Query /TN handoff-agentd /V /FO LIST` 的「上次结果」栏
- **「上次结果」是 `0x800710E0`（十进制 `-2147020576`）不代表失败**：
  那是 `IgnoreNew` 抑制重复触发时写进去的正常值，也是 rc5 那个 bug 的同一个值。
  见下面「一个已经修掉的坑（rc6 修复）」

---

## P4 有活跃任务时不同步，强制路径走得通

双击时若本机有活跃任务（`running` / `waiting_answer`）：

- 日志 `同步对账结果 plan=blocked … busy=N`，且**没有** `换版完成`
- 托盘出现「有更新待应用（N 个任务进行中）」

点它 → 面板 → 「带 --force 重试」，四步都要能走到。之后：

- 日志出现 `换版完成` 与 `agentd 已带新版本回来`
- **执行者进程仍然存活**——这是最要紧的一条判据。B59 V3 在 macOS 上实测过执行者
  能活过 agentd 重启（当时 16m29s）；Windows 侧用 `prochost` 承载，本条要单独确认

---

## P5 升级面板窗口能出来

托盘 →「升级执行机…」。**判据**：弹出一个独立窗口，逐行出现 `handoff upgrade` 的巡检表。

**判否说明什么**：如果**进程静默消失、没有任何输出**，那是 rc7 那个 bug 的第二次——
新窗口漏挂了 `WindowRuntimeReady`，`setURL` 导航进一个还没建好的 chromium。
Wails beta.8 的 `windowsWebviewWindow.setURL` 至今没有 nil 守卫（相邻的 `execJS` 有）。
这是本次改动新增的第三个 UI 面，而前两个各自都出过一次真机才照得出的 bug。

窗口出来但一片空白是另一回事：vite 的多页入口没生效、`dist/upgrade.html` 没被打进去。
Go 侧照样编得过，不会有任何构建期报错。

---

## P6 同步失败时控制台**仍然能打开**

**这是本次改动最严重的回归面。** 双击打开应用是整个产品里唯一一个"必须成功"的动作，
而我们在它前面插了一段会换二进制、重启服务的逻辑。

制造一次必然失败的同步。**任何让落点目录不可写的办法都行**——`release.Activate`
开头的 `checkDirWritable` 会在那儿实际建一个临时文件来探，失败时给的是一句
带路径的明确报错，不是扁平的 permission denied。

```powershell
$p = "$env:LOCALAPPDATA\Programs\handoff"
icacls $p /deny "$env:USERNAME:(WD)"        # 拒绝写入
```

> 这条 `icacls` 写法**未在 Windows 上实跑验证过**（清单写于 macOS）。
> 先确认它真的生效再往下走——在该目录里手动 `New-Item -ItemType File t.tmp`
> 应当被拒。不生效就换别的办法（改目录属主、把目录换成同名文件等），
> 判据不变。

双击。**判据**：控制台照常打开；日志 `同步失败，将用现有版本继续打开控制台`；
托盘出现「上次同步失败，查看详情」，点开能看到原因原文。

```powershell
icacls $p /remove:d "$env:USERNAME"          # 验完立刻改回来
```

**判否说明什么**：应用打不开 = spec D8 被破。这是本条路径唯一不可接受的失败模式，
优先级高于同步本身能不能成功。

---

## 已知且刻意如此（不要当缺陷上报）

与 `macos-desktop-acceptance.md` 同一份清单：

- **「带 --force 重试」对非闸一原因的失败点了也没用**（网络不通、目录只读等）。
  按钮只按退出码非零就亮，**不解析输出文本**——输出是给人看的中文表格，解析它会在
  格式一改时静默失效，而失效方式是「按钮再也不出现」，没有任何报错。见 spec §7.2
- **agentd 被锁在你手上这份 `.exe` 的版本。** 要升到更新的版本必须换安装包，
  托盘的「有新版 vX.Y.Z 可下载」就是提醒你去换。见 spec §11 风险③
- **开发构建下同步整条路不跑**，日志里 `plan=no-embed`
- **已装 CLI 版本号不是 `vX.Y.Z` 时同步永不触发**，日志里 `decision=use-existing`。
  这是设计要的保守行为（判不出就不覆盖用户手上的），不是缺陷
- **Windows 上换版后要等最多 60 秒**是计划任务模拟 KeepAlive 的固有代价；
  代码里那次主动催把它压到几秒，但催失败时会退化回干等。见 P2

---

## 已经验过的（不用重做）

以下在 win-b37（Windows Server 2025 / AMD64）上用真实 amd64 薄壳实测取证过：

| 项 | 证据 |
|---|---|
| 释出决策走「安装」分支 | 日志 `释出决策 decision=install existing="" embedded_version=v0.3.0-rc4probe` |
| 释出落点正确且带 .exe | 日志 `内嵌二进制已释出 dst=…\local\Programs\handoff\handoff.exe`，文件 20,665,856 字节 |
| 释出的 CLI 真能跑 | `handoff.exe version` → 版本号 + `platform windows/amd64` |
| 首次向导的数据链路 | 日志 `PATH 无需补全`、`工具链探测完成 count=4`、`首次配置表已生成 fields=9` |
| 配置判定 | 日志 `未找到配置文件，判为未配置 path=…\.handoff\config.yaml` |
| Go 侧运行期单测 | `go test ./internal/prochost/... ./internal/executor/claudecode/... -count=1` 两包 ok |
| install.ps1 单测 | PowerShell 5.1 下 exit 0（该脚本全通过时静默退出） |
