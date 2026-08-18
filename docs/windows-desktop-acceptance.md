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
