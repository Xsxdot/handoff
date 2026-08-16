# W5b P1 探针报告：Wails v2 / v3 实跑结论

> 对应 [W5 设计](2026-08-16-w5-embed-and-desktop-shell-design.md) §8 的 P1。
> spec §4.2 明确写了「不在本文裁决，**必须实跑后定，不凭文档拍**」，本文是那次实跑。
>
> 日期：2026-08-16　机器：macOS 26.5.2 / Apple M1 Pro / arm64
> 工具链：Go 1.26.1、Node 23.11.0、npm 10.9.2、Xcode 26.6
> 被测：`wails` **v2.14.0**、`wails3` **v3.0.0-beta.8**（均 `go install …@latest` 当日版本）

---

## 0. 一句话结论

**选 Wails v3（beta.8）**。v2 出局的原因不是成熟度，而是**它根本没有可用的系统托盘**——
这项在 spec §4.3 里是承重的。同时探针推翻了 spec 的两处设计前提（§4.1/§6.2 的「三条原生 runner」、
§2 的 Linux 基线），详见 §4、§5。

---

## 1. v2 出局：托盘是死代码

spec §4.3 要求「托盘常驻，托盘菜单提供『打开控制台 / 停止 agentd / 退出』」。
这是关窗口不停 agentd 之后**用户唯一还能找到这个程序的入口**，不是装饰。

v2 里托盘的实际状态（读 `v2@v2.14.0` 源码，逐条核对）：

| 查什么 | 结果 |
|---|---|
| `pkg/menu/tray.go` 有没有 `TrayMenu` 类型 | **有**，字段齐全（Label / Image / Menu / OnOpen / OnClose） |
| `internal/menumanager` 有没有管理逻辑 | **有**，`NewTrayMenu` / `AddTrayMenu` / `SetTrayMenu` / `OnTrayMenuOpen` 全套 |
| `options.App` 有没有 Tray 字段 | **没有**。只有 `Menu *menu.Menu`（应用菜单） |
| menumanager 之外谁调 `AddTrayMenu` / `SetTrayMenu` | **零调用者** |
| `internal/frontend/` 三平台有没有托盘渲染 | **没有**。全仓唯一命中是 winc 的一份 README |

即：**类型和管理器都在，但没有任何一条路能把它接到窗口系统上**。这是一段死代码，
不是「需要多写点胶水」。要在 v2 上做托盘，只能外挂第三方库（如 systray），
等于给薄壳引入一个 Wails 之外的原生依赖，与「把薄壳做薄」的取向相反。

v2 的其余两项是有的，记录以免后人重查：

- 目录对话框：`pkg/runtime/dialog.go:33` `OpenDirectoryDialog`
- 应用菜单：`options.App.Menu`

v2 构建实测（macOS）：`wails build` 成功，**21.7s**，产物 `.app` **7.8MB**。
所以 v2 不是不能用，是**不能满足 §4.3**。

---

## 2. v3 在 macOS 上四项全过

### 2.1 构建与打包

| 步骤 | 结果 |
|---|---|
| `wails3 task build` | 成功，**15.5s**，产物 `bin/p3` = **9.0MB** Mach-O arm64 |
| `wails3 task package` | 成功，产出 `.app`，并自动 `codesign --force --deep --sign -`（ad-hoc） |

一个必须记住的坑：**v3 的前端构建耦合了 binding 生成器的产物**。
直接 `npm run build` 会失败在 `wails-typed-events` 插件找不到
`./bindings/github.com/wailsapp/wails/v3/internal/eventcreate`；
删掉模板里的 Service 也会让 `frontend/src/main.ts` 的 `../bindings/changeme` 解析不到。
**前端构建必须走 Taskfile**（它先 `generate bindings` 再 `vite build`），
这一条直接影响 W5b 在 CI 里怎么接前端构建。

### 2.2 应用菜单

`app.Menu.New()` → `AddSubmenu` → `app.Menu.SetApplicationMenu(menu)`，运行无错。

### 2.3 原生目录对话框——通过，且 v3 自己做了主线程派发

探针刻意**从非主 goroutine** 调 `app.Dialog.OpenFile().CanChooseDirectories(true).PromptForSingleSelection()`，
用来试探要不要自己 dispatch 到主线程。

取证（`CGWindowListCopyWindowInfo`，按 pid 过滤）：

```
T=2s（对话框尚未触发）  该进程窗口 1 个：699x451        ← 主窗口
T=7s（t=4s 已触发）     该进程窗口 2 个：699x451 + 880x448  ← 多出来的就是 NSOpenPanel
```

结论：**面板真的弹出来了，且不需要调用方自己派发主线程**。既没崩溃也没死锁。

### 2.4 系统托盘——通过（取证过程本身值得记下来）

这一项绕了三轮，因为**第一种取证方法是错的**，记下来避免后人重走：

**① 截图取证失败。** 本机没给 Screen Recording 权限，`screencapture` 返回
`could not create image from display`。视觉证据这条路直接断了。

**② CGWindowList 取证失败——而且差点得出错误结论。**
按 pid 查窗口清单，A 组（有托盘）与 B 组（`PROBE_NO_TRAY=1`）**窗口数都是 14，差集为 0**，
看上去像「托盘没生效」。但对照本机其它 app 后发现：**macOS 的状态栏项本身就不出现在窗口清单里**
（`控制中心` / `Magnet` / `iStat Menus` 在 layer 25 的那些是它们的浮层，不是菜单栏图标）。
**所以「清单里没有」不构成「托盘没生效」的证据**，这个方法对托盘无效。

**③ 用托盘的真实几何取证——通过。**
`SystemTray.PositionWindow(win, 0)` 在原生侧读的是 `NSStatusItem.button.window` 的屏幕矩形。
调用**返回成功**，主窗口被摆到 `x=0, y=39`。

`x=0` 起初可疑（菜单栏项应在屏幕右侧，居中算出来该是个大 x），所以又写了一个
**不经 Wails 的纯 AppKit 复现**（`traytest.m`），结果解释了一切：

```
button.frame  = (0, 0, 85, 22)          ← 状态项按钮真实存在
屏幕坐标        = (-4407, 1290, 85, 22)   ← 落在这台机器负 x 方向的副屏
window.screen = nil
```

代入 v3 的原生实现 `windowX = frame.origin.x + (frame.size.width - windowFrame.size.width) / 2`
= `-4407 + (85-699)/2` ≈ `-4714` → 触发左边界钳制 → `x=0`。**与实测完全吻合**。

另外纠正一条容易读错的读数：`y=39` **证明不了托盘**——原生实现里
`windowY` 只由 `screen.visibleFrame` 算出，与托盘无关。

**结论**：v3 的托盘会创建真实的 `NSStatusItem`，有 button、有 window、有屏幕矩形。

**仍未做的一项**：托盘图标的**视觉**确认（图标长什么样、菜单点开是否正常）。
本机无截屏权限，需人工看一眼，或授权后补。**不要把本节当成视觉验收已通过**。

---

## 3. 关窗口不退进程（§4.3 承重属性的预演）

探针设了 `ApplicationShouldTerminateAfterLastWindowClosed: false`，进程在整个观测期内存活。

这**不等于 P4 通过**。P4 要验的是「薄壳拉起 agentd 后，关掉薄壳窗口时**执行者**是否存活」，
需要真的有 agentd 和任务在跑。P4 仍未验。

---

## 4. 推翻 spec §4.1 / §6.2：交叉编译不是全都不可行

spec §4.1 写「三平台都要原生 runner，交叉编译都不可行」，§6.2 据此排了三条原生 runner。
实测**只有一半成立**：

| 目标 | 从 macOS 交叉编译 | 实测 |
|---|---|---|
| Windows amd64 | **可以** | `GOOS=windows CGO_ENABLED=0 go build -tags production -ldflags="-w -s -H windowsgui"` → **10.3MB PE32+ GUI exe** |
| Linux amd64 | **不行** | `CGO_ENABLED=0` 编不过（webkit2gtk 经 cgo，`undefined: pointer`） |

必须带 `-H windowsgui`：不加会得到 console 子系统的 exe（会弹黑框）。

另外 v3 beta.8 **自带官方交叉编译方案** `internal/commands/build_assets/docker/Dockerfile.cross`：
Zig + macOS SDK + 内置 mingw，覆盖 darwin/linux/windows × amd64/arm64。

**边界（重要）**：能编出来 ≠ 能跑。**Windows 产物没有在 Windows 上运行过**（本地无 Windows 机）。
在拿到真机运行证据之前，不要把 Windows runner 从流水线里去掉——
本报告只支持「编译不必用 Windows runner」，不支持「不需要 Windows 验证」。
macOS 侧同理：签名与公证仍然只能在 macOS 上做（B86 已有该 job）。

---

## 5. 推翻 spec §2 的 Linux 基线表述（承重）

spec §2 锁「Linux 基线 = Ubuntu 22.04 / Debian 12（`webkit2gtk-4.1`）」。
在 Wails v3 下这句话**不加限定就是错的**：

```
pkg/application/linux_cgo.go:17       #cgo linux pkg-config: gtk4 webkitgtk-6.0      ← 默认后端
pkg/application/linux_cgo_gtk3.go:18  #cgo linux pkg-config: gtk+-3.0 webkit2gtk-4.1 ← 需 -tags gtk3
```

build tag 是 `//go:build linux && !android && gtk3 && !server` 对
`//go:build linux && !android && !gtk3 && !server`。

即：**v3 的默认 Linux 后端已经是 GTK4 + webkitgtk-6.0**，要求 GTK ≥ 4.14
（v3 自己的 cross 镜像用的是 Debian 13 trixie，并在构建期 `pkg-config --atleast-version=4.14 gtk4` 硬校验）。
Ubuntu 22.04 上没有这个版本的 GTK4。

**所以要守住 spec §2 的 Ubuntu 22.04 基线，Linux 构建必须显式 `-tags gtk3`。**
这条要写进 W5b 的全局约束，漏了的症状是：CI 在新发行版上构建通过，
而 Ubuntu 22.04 用户拿到的包跑不起来——且这个差异不体现在任何一次代码提交里
（与 §6.2 锁 `ubuntu-22.04` 而非 `ubuntu-latest` 是同一类陷阱）。

---

## 6. 未验项（不许当成通过）

| # | 未验的事 | 卡在哪 | 怎么补 |
|---|---|---|---|
| P1-Linux | v3 在 Ubuntu 22.04 上能否 `-tags gtk3` 构建、能否运行、托盘/对话框是否可用 | 本机 **Docker Hub 不可达**（`registry-1.docker.io` 20s 超时、http 000），且没有 Linux 机器 | 有 Linux 机器后跑仓库里的 `Dockerfile.linux-probe`（已写好），或直接在真机上跑 |
| P1-Windows | 交叉编译出的 exe 能否运行、托盘/对话框是否可用 | 无 Windows 机器 | 真机或 CI 的 `windows-latest` |
| P1-托盘视觉 | 托盘图标与菜单的**观感** | 本机无 Screen Recording 权限 | 人工看一眼，或授权后截图 |
| P2 | 从 `.app` 释出到 `~/.local/bin/` 的已公证二进制能否过 Gatekeeper | 需要真实 Developer ID 签名与**向 Apple 提交公证**（外部可见操作，需用户授权） | 用户授权后在 CI 或本机做 |
| P3 | AppImage 在非 Ubuntu 发行版能否运行 | 同 P1-Linux | 同上 |
| P4 | 关掉薄壳窗口后执行者是否存活 | 需要薄壳先实现到能拉起 agentd | W5b 实现到该 task 时验 |

---

## 7. 对 W5b plan 的直接影响

1. **框架定为 Wails v3（beta.8）**，spec §4.2 的悬置项就此裁决。代价照旧记账：v3 仍是 beta。
2. **Linux 构建必须 `-tags gtk3`**，进 plan 的全局约束。
3. **前端构建必须走 Taskfile**（bindings 生成先于 vite build），不能在 CI 里裸调 `npm run build`。
4. **Windows 产物可在非 Windows runner 上编译**，但运行验证仍需 Windows。§6.2 的 runner 表要按这条重写，
   而不是照抄「三条原生 runner」。
5. **托盘是 v3 独有能力**，因此薄壳不能降级到 v2；若将来 v3 出现阻塞性缺陷，
   退路是「v2 + 第三方 systray」，不是「v2 裸用」——这条要写进 plan 的风险栏。

## 8. 探针资产

探针工程与工具留在 scratchpad（不入库，属一次性验证资产）：
`p2/`（v2 模板）、`p3/`（v3 探针，含托盘/菜单/对话框与 `PROBE_NO_TRAY` 对照开关）、
`winlist.m`（CGWindowList 取证）、`traytest.m`（纯 AppKit 托盘复现）、
`Dockerfile.linux-probe`（Ubuntu 22.04 构建探针，**尚未跑通，等 Linux 环境**）。
