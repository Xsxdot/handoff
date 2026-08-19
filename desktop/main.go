// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配与错误呈现，外加 Wails 事件收发（向导表单的传输层）。
// 边界：
//   - **不放业务逻辑**。定位、握手、生命周期、路径校验全在 internal/shell，
//     那里不 import Wails，因而可以用普通 go test 覆盖
//   - **首次引导的字段表与校验属于 internal/initflow**。main.go 只装配
//     wizard-form / wizard-submit / wizard-error 的收发通道，不在
//     这里加任何字段、不定义任何默认值
//   - **不在退出路径上停 agentd**（spec §4.3 承重）
//   - 托盘不提供「停止 agentd」。**不做「停止 agentd」**：
//     service.Manager 没有 Stop，用 Uninstall 冒充是错的语义
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/pathenv"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/service"
	"github.com/Xsxdot/handoff/internal/toolchain"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/trayicon.png
var trayIconTemplate []byte

//go:embed build/appicon.png
var trayIconColor []byte

// desktopTopInset 是无标题栏窗口顶部那条隐形拖动区的高度（点）。
//
// 承重：与前端 DESKTOP_TOP_INSET 必须一致。这条区域内的左键会被 AppKit 拿去
// 拖窗口、不传给页面，前端不让出等高的空白就会出现「看得见点不动」的控件。
const desktopTopInset = 28

// userAgentTag 是附在 webview UA 末尾的标记，前端据此判断自己跑在桌面壳里。
// 改它要同步改 web/src/app/lib/desktopShell.ts。
const userAgentTag = "handoff-desktop"

// logger 是包级日志入口。main() 里装配成 TextHandler 写 stderr；
// 包级小函数（readInstalledVersion 等）复用同一实例，避免混用默认 logger
// 导致日志格式不统一。
var logger = slog.Default()

// wizMu/wizActive 防重入：一个向导进行中时，再次从托盘打开控制台不另起一个
// 表单流程，否则两份交表在同一个页面上互相覆盖。
var (
	wizMu     sync.Mutex
	wizActive bool
)

// runtimeReadyCh 在前端运行时挂载（WindowRuntimeReady）后关闭。
//
// 首个 wizard-form 必须等它：webview 加载完成前发出的 Go 事件会被
// Wails 事件模板里 `if(window._wails&&window._wails.dispatchWailsEvent)`
// 的守卫静默丢弃（见 Wails 源码 inlineEventJS），早发等于把整张表单吞掉，
// 向导从此卡在空白页。所以向导 goroutine 先等这个 channel 再发表单。
var runtimeReadyCh = make(chan struct{})

// closeRuntimeReady 保证只 close 一次：WindowRuntimeReady 若重发，
// 直接 close 一个已关闭的 channel 会 panic。
var closeRuntimeReady sync.Once

// openConsoleFn 指向 main() 里那个 openConsole 闭包。
//
// 为什么要这个间接层：openConsole 捕获了 app/win/runtimeReadyCh 三个 main()
// 的局部变量，提不成包级函数；而 rebuildTray 与托盘回调都在包级作用域里，
// 引用不到闭包。用一个包级变量把它导出到包作用域是最小的接法。
var openConsoleFn func()

// trayState 是托盘菜单要展示的动态状态。
//
// 为什么用包级变量 + 重建而不是让菜单项自己刷新：Wails v3 的托盘菜单没有
// 「改一项的文案」这种接口，改动只能整体重建。状态集中在一处，重建函数才
// 能是幂等的。
var (
	trayMu      sync.Mutex
	traySync    shell.SyncOutcome // 最近一次对账的结果，也是状态上报的数据源
	traySyncErr error             // 最近一次同步失败的原因，也是状态上报的数据源
	trayLatest  string            // 发现的新版 tag，空串表示没有；与上报状态共用启动生命周期，勿删
	trayApp     *application.App
	trayTray    *application.SystemTray
)

func main() {
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("桌面薄壳启动")

	app := application.New(application.Options{
		Name:        "handoff-desktop",
		Description: "handoff 控制台桌面壳",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac: application.MacOptions{
			// 承重：关掉最后一个窗口时进程必须活着，托盘才谈得上常驻
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff",
		Width:  1200,
		Height: 800,
		URL:    "/",
		Mac: application.MacWindow{
			// 去掉那条系统标题栏：它是浅灰的、与控制台自身的深色顶栏割裂，
			// 而且白占一条横向边框。MacTitleBarHidden 把内容顶到窗口最上沿，
			// 红黄绿三个按钮保留（不能用 Frameless——那连关窗按钮都没了）。
			TitleBar: application.MacTitleBarHidden,
			// 标题栏没了就没有可拖动的地方，窗口会挪不动。这条给回顶部 28px 的
			// 原生拖动区（native 层实现，不依赖 Wails 运行时注入——控制台是外链
			// 页面，拿不到 --wails-draggable）。
			//
			// 代价：这 28px 会吞掉页面的左键点击，所以前端必须让出同高的空白，
			// 两个数字要一起改（web/src/app/lib/desktopShell.ts 的 DESKTOP_TOP_INSET）。
			InvisibleTitleBarHeight: desktopTopInset,
			WebviewPreferences: application.MacWebviewPreferences{
				// 前端靠 UA 里这个后缀认出「我跑在桌面壳里」，据此让出上面那 28px。
				// 控制台是外链页面，Wails 运行时不会注入，UA 是唯一不用改握手协议
				// 就能传出去的信号。
				ApplicationNameForUserAgent: userAgentTag,
			},
		},
	})

	openConsole := func() {
		ep, state, err := shell.Resolve("")
		if err != nil {
			logger.Error("读取配置失败", "cause", err)
			showError(app, "读取 handoff 配置失败", err.Error())
			return
		}
		if state == shell.StateUnconfigured {
			// 释出决策先行（同步），再亮出向导页。释出失败不阻断向导——
			// 见 releaseEmbedded 的注释。
			releaseEmbedded(app)
			win.Show()
			startWizard(app, win)
			return
		}
		// 对账与同步接在这里，在加载控制台**之前**。此刻窗口还没显示，
		// 重启 agentd 全程不进用户视野——他只会觉得这次打开慢了几秒。
		//
		// 承重：无论 out.Err 是什么，下面加载控制台的代码都必须照常执行
		//（spec D8）。同步失败只是少升一次级，阻断则是「双击打不开应用」。
		// 同步等待有自己的 90 秒上限；不能复用下面给 webview/握手的 30 秒
		// 上下文，否则 Windows 的计划任务还没来得及拉起 agentd 就会被提前取消。
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 90*time.Second)
		out := shell.SyncOnOpen(syncCtx, openSyncDeps(ep))
		syncCancel()
		trayMu.Lock()
		traySync, traySyncErr = out, out.Err
		trayMu.Unlock()
		switch {
		case out.Err != nil && out.Plan == shell.SyncSkip:
			// 这一支是 agentd 根本起不来或定位不到二进制——控制台加载不了，
			// 必须停下并告诉用户。与同步失败不是一回事
			logger.Error("agentd 不可用，无法加载控制台", "cause", out.Err)
			showError(app, "无法启动 agentd", out.Err.Error())
			return
		case out.Err != nil:
			// 同步没做成，但 agentd 在跑（只是版本旧）。如实记录，继续
			logger.Warn("同步未完成，将用现有版本继续", "plan", out.Plan.String(), "cause", out.Err)
			noteSyncFailed(out)
		case out.Plan == shell.SyncBlocked:
			logger.Info("有活跃任务，本次不同步", "busy", out.Busy)
			noteSyncBlocked(out)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// **切外链之前必须等窗口的 webview 真的建好。** 顺序也是承重的：
		// 等在握手**之前**，因为 ticket 只有 60 秒寿命——先握手再等，等待
		// 本身就可能把票等过期。
		//
		// 不等的后果不是报错，是进程当场消失（Wails beta.8 的
		// windowsWebviewWindow.setURL 不判 chromium 是否已建好，详见
		// shell.AwaitWebviewReady 的注释）。首次配置向导那条路径一直没事，
		// 因为它本来就等这同一个信号。
		if err := shell.AwaitWebviewReady(ctx, runtimeReadyCh); err != nil {
			logger.Error("窗口 webview 未就绪，放弃加载控制台", "cause", err)
			showError(app, "窗口未能就绪", err.Error())
			return
		}
		url, err := shell.ConsoleURL(ctx, ep, shell.DefaultDeviceName())
		if err != nil {
			logger.Error("握手失败", "cause", err)
			showError(app, "无法连接 agentd", err.Error())
			return
		}
		// 不打 url：里面带一次性凭据
		logger.Info("加载控制台")
		win.SetURL(url)
		win.Show()
	}
	openConsoleFn = openConsole

	tray := app.SystemTray.New()
	// macOS 用模板图标：系统只取 alpha 通道，自动随明暗菜单栏反色；
	// 其余平台没有这个机制，用彩色图。
	//
	// 尺寸不用操心：Wails 的 systemTraySetIcon 会 setSize 到状态栏厚度（22pt），
	// 44px 是为了 retina 下清晰。
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayIconTemplate)
	} else {
		tray.SetIcon(trayIconColor)
	}
	// 标签清空：之前只设 label 不设图标，菜单栏里显示的是「handoff」四个字。
	tray.SetLabel("")
	trayApp, trayTray = app, tray
	rebuildTray()
	logger.Info("系统托盘已就绪")

	// 目录选择器：暴露给前端，收口 B110 的本机半边
	app.Event.On("pick-project-dir", func(*application.CustomEvent) {
		raw, err := app.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			SetTitle("选择项目目录").
			PromptForSingleSelection()
		if err != nil {
			logger.Error("打开目录选择器失败", "cause", err)
			return
		}
		dir, err := shell.NormalizeProjectDir(raw)
		if err != nil {
			logger.Warn("目录选择未产生可用结果", "cause", err)
			app.Event.Emit("project-dir-error", err.Error())
			return
		}
		logger.Info("目录已选定并回传前端", "path", dir)
		app.Event.Emit("project-dir-picked", dir)
	})

	// 前端运行时挂载后才发得出向导表单：见 runtimeReadyCh 的注释
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		closeRuntimeReady.Do(func() { close(runtimeReadyCh) })
	})

	// 启动序列必须等应用就绪后再跑：showError 经 app.Dialog → InvokeSync →
	// a.impl.isOnMainThread()，而 a.impl 由 app.Run() 初始化——ApplicationStarted
	// 之前走到 showError（如握手 401）会 nil deref panic（实测复现）。
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go openConsole()
		// 上报自身状态供控制台读取。放在 openConsole 之后、独立 goroutine：
		// 它要发 HTTP、可失败，绝不能挡在打开控制台前面（与新版检查同一条纪律）。
		go runDesktopReporter()
		// 新版检查独立于同步：它要出网、可失败，绝不能挡在打开控制台前面。
		// 单开 goroutine，结果到了再刷托盘
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			configPath := config.DefaultPath()
			if _, err := os.Stat(configPath); err != nil {
				// 配置不存在时绝不能调用 config.Load：它会 firstRun 写盘，
				// 把一个尚未完成向导的机器伪装成已配置
				logger.Debug("取不到数据目录，跳过新版检查", "cause", err)
				return
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				logger.Debug("取不到数据目录，跳过新版检查", "cause", err)
				return
			}
			tag, newer := shell.CheckLatest(ctx, cfg.DataDir, embedbin.Version, shell.LatestDeps{
				Fetch: func(ctx context.Context) (string, error) {
					rel, err := release.NewClient(nil).Latest(ctx)
					if err != nil {
						return "", err
					}
					return rel.Tag, nil
				},
			})
			if !newer {
				return
			}
			trayMu.Lock()
			trayLatest = tag
			trayMu.Unlock()
			rebuildTray()
		}()
	})

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出；agentd 未被触碰")
}

// rebuildTray 构建托盘菜单。
//
// 只有两项。三条动态提示（有新版 / 有更新待应用 / 上次同步失败）已经移到
// 控制台右下角的提示框，「升级执行机」并入控制台设置的更新页，强制同步入口
// 直接删除——重开一次桌面端就会重走 SyncOnOpen，不必再造第二个入口（spec §2）。
//
// 保留函数名与加锁：它仍会被启动序列调用一次，且 Wails 的菜单对象不并发安全。
func rebuildTray() {
	trayMu.Lock()
	defer trayMu.Unlock()
	if trayApp == nil || trayTray == nil {
		return
	}
	menu := trayApp.Menu.New()
	// 放 goroutine 里跑：openConsole 里有网络握手，还要等 webview 就绪，
	// 在点击回调里同步跑会把主线程连同整个 UI 一起冻住。
	menu.Add("打开控制台").OnClick(func(*application.Context) { go openConsoleFn() })

	menu.Add("退出（agentd 继续运行）").OnClick(func(*application.Context) {
		// 只退薄壳。agentd 与它拉起的执行者继续跑，这是招牌属性
		logger.Info("用户从托盘退出薄壳；agentd 不受影响")
		trayApp.Quit()
	})
	trayTray.SetMenu(menu)
	logger.Info("托盘菜单已重建", "items", 2)
}

// openSyncDeps 装配 SyncOnOpen 的生产依赖。
//
// 单独抽出来只为让 main.go 里的启动序列保持可读——所有「怎么做」都在
// shell 包里，这里只回答「用哪个实现」。
func openSyncDeps(ep shell.Endpoint) shell.OpenSyncDeps {
	c := client.New(ep.Addr, ep.Token)
	return shell.OpenSyncDeps{
		EnsureRunning: func() error {
			spec, err := specFor(ep)
			if err != nil {
				return err
			}
			return shell.EnsureRunning(logger, spec)
		},
		InstalledPath:    func() (string, error) { return shell.ResolveBinPath("") },
		InstalledVersion: readInstalledVersion,
		Busy: func(ctx context.Context) (int, error) {
			st, err := c.Status(ctx)
			if err != nil {
				return 0, err
			}
			return len(st.Active), nil
		},
		EmbedVersion:   embedbin.Version,
		EmbedAvailable: embedbin.Available(),
		Sync: shell.SyncDeps{
			OpenEmbedded: embedbin.Open,
			Activate:     release.Activate,
			SkillInstall: execSkillInstall,
			RestartAgentd: func(ctx context.Context, force bool) error {
				_, err := c.RestartAgentd(ctx, force)
				return err
			},
		},
		Wait: shell.WaitDeps{
			Version: func(ctx context.Context) (string, error) {
				st, err := c.Status(ctx)
				if err != nil {
					return "", err
				}
				return st.Version.Version, nil
			},
			Nudge: nudgeAgentd(ep),
		},
		Progress: emitSyncProgress,
	}
}

// execSkillInstall 在指定二进制上跑 skill install。
//
// 必须 exec **新**二进制：skill 随二进制分发（B59），当前进程内嵌的是旧的。
// hideConsole 是必需的——薄壳是 GUI 进程，不压这一下会在用户屏幕上闪黑窗口。
func execSkillInstall(ctx context.Context, bin string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, "skill", "install")
	hideConsole(c)
	return c.CombinedOutput()
}

// nudgeAgentd 催进程管理器把 agentd 拉起来。
//
// 复用 shell.EnsureRunning：它在 agentd 不在跑时会 Install + 拉起，Windows
// 侧正是 schtasks /Run + 500ms 轮询复核（internal/service/windows.go:271），
// 恰是这里要的动作。macOS 的 launchd KeepAlive 秒级自拉，催一次也无害。
func nudgeAgentd(ep shell.Endpoint) func() error {
	return func() error {
		spec, err := specFor(ep)
		if err != nil {
			return err
		}
		return shell.EnsureRunning(logger, spec)
	}
}

// emitSyncProgress 把同步阶段送给 UI。Task 9 之前先只记日志。
func emitSyncProgress(stage string) { logger.Info("同步进度", "stage", stage) }

// noteSyncFailed 记录一次失败的同步并刷新托盘。
func noteSyncFailed(out shell.SyncOutcome) {
	trayMu.Lock()
	traySync, traySyncErr = out, out.Err
	trayMu.Unlock()
	rebuildTray()
	reportDesktopStateNow()
}

// noteSyncBlocked 记录一次被闸一拦下的同步并刷新托盘。
func noteSyncBlocked(out shell.SyncOutcome) {
	trayMu.Lock()
	traySync, traySyncErr = out, nil
	trayMu.Unlock()
	rebuildTray()
	reportDesktopStateNow()
}

// reportDesktopStatePut 保存 reporter 使用的单向 PUT 函数。
//
// reporter 依赖只有在配置可读时才会装配；同步回调可能更早到达，因此用锁保护
// 可选函数，未装配时立即上报安全地退化为 no-op。
var (
	desktopReportMu  sync.RWMutex
	desktopReportPut func(context.Context, proto.DesktopState) error
)

// reportDeps 装配薄壳状态上报的 agentd 客户端依赖。
//
// 参数：ep 是 shell.Resolve 得到的地址与令牌；令牌只进入 client，不进入日志。
// 返回：ReportDeps，供 RunReporter 与立即上报共用。
func reportDeps(ep shell.Endpoint) shell.ReportDeps {
	c := client.New(ep.Addr, ep.Token)
	return shell.ReportDeps{
		Put: func(ctx context.Context, st proto.DesktopState) error {
			return c.PutDesktopState(ctx, st)
		},
	}
}

// runDesktopReporter 在配置完成时启动单向状态上报循环。
//
// 注意：配置缺失或读取失败只记录 Debug 并退出；首次向导与控制台加载不应被
// 这条锦上添花的链路拖住。
func runDesktopReporter() {
	ep, state, err := shell.Resolve("")
	if err != nil {
		logger.Debug("读取上报用配置失败，跳过薄壳状态上报", "cause", err)
		return
	}
	if state != shell.StateConfigured {
		logger.Debug("薄壳尚未配置，跳过状态上报")
		return
	}
	d := reportDeps(ep)
	desktopReportMu.Lock()
	desktopReportPut = d.Put
	desktopReportMu.Unlock()
	shell.RunReporter(context.Background(), logger, snapshotDesktopState, d)
}

// reportDesktopStateNow 立即上报一次最新快照，不阻塞同步回调。
func reportDesktopStateNow() {
	desktopReportMu.RLock()
	put := desktopReportPut
	desktopReportMu.RUnlock()
	if put == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := put(ctx, snapshotDesktopState()); err != nil {
			logger.Warn("立即上报薄壳状态失败", "cause", err)
		}
	}()
}

// snapshotDesktopState 从托盘保护的同步结论组装线上状态。
//
// 返回：只含版本与同步结论的 proto.DesktopState；版本为空时控制台不会提示更新。
// 注意：SyncBusy 只在 blocked 时保留，探测失败沿用 -1，不把未知伪装成 0。
func snapshotDesktopState() proto.DesktopState {
	trayMu.Lock()
	out, syncErr := traySync, traySyncErr
	trayMu.Unlock()

	st := proto.DesktopState{AppVersion: embedbin.Version, SyncPlan: "skip", SyncBusy: 0}
	switch {
	case syncErr != nil:
		st.SyncPlan = "failed"
		st.SyncError = syncErr.Error()
	case out.Plan == shell.SyncBlocked:
		st.SyncPlan = "blocked"
		st.SyncBusy = out.Busy
	case out.Plan == shell.SyncDo:
		st.SyncPlan = "done"
	}
	return st
}

// readInstalledVersion 从既有 handoff 二进制里读版本号，供释出决策用。
// 返回 stdout 首行去空白；任何失败或首行是 unknown 都判不出（空串）——
// 与 shell.DecideRelease 的保守约定一致：判不出就偏保守，用用户已有的。
//
// 为什么不需要隔离子进程 HOME：这里 exec 的 `handoff version` 会经根命令的
// PersistentPostRun 触发 maybeNotifyUpdate，而后者在配置文件不存在时会直接
// return（cmd/root.go maybeNotifyUpdate 的守卫），不会 firstRun 写盘——所以
// 子进程跑 version 不会在目标 HOME 留下 config.yaml。保持原样，不包一层
// 隔离（那既在 SIGKILL 下失效——defer 不执行、孤儿目录越堆越多，又会蒙住
// CLI 侧这条契约的回归信号）。
func readInstalledVersion(path string) string {
	c := exec.Command(path, "version")
	// 薄壳是 GUI 进程：不压这一下，读版本号会在用户屏幕上闪一个黑窗口
	hideConsole(c)
	out, err := c.Output()
	if err != nil {
		logger.Debug("读取已有 handoff 版本失败，按判不出处理", "bin", path, "cause", err)
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" || line == "unknown" {
		logger.Debug("已有 handoff 版本判不出", "bin", path, "raw", line)
		return ""
	}
	return line
}

// releaseEmbedded 在首次引导走向导之前，按三态决策处理内嵌二进制（spec §5.3）。
//
// 任何失败都不阻断向导：释出不是引导的前提，用户仍可继续用已有的安装或
// 手动装——所以这里只记日志/发提示，不回传错误。
func releaseEmbedded(app *application.App) {
	existing, err := shell.ResolveBinPath("")
	if err != nil {
		existing = ""
		logger.Debug("本机没有既有 handoff 安装", "cause", err)
	}
	existVer := ""
	if existing != "" {
		existVer = readInstalledVersion(existing)
	}
	decision := shell.DecideRelease(existing, existVer, embedbin.Version)
	logger.Info("释出决策", "decision", decision, "existing", existing,
		"existing_version", existVer, "embedded_version", embedbin.Version)
	switch decision {
	case shell.DecisionInstall:
		if !embedbin.Available() {
			// 开发构建的正常情况：没内嵌就不释出，直接走向导
			logger.Info("本次构建未内嵌 handoff 二进制，跳过释出", "reason", "开发构建未带 -tags embedbin")
			return
		}
		rc, err := embedbin.Open()
		if err != nil {
			logger.Error("打开内嵌二进制失败，不阻断向导", "cause", err)
			return
		}
		defer rc.Close()
		dst, err := shell.DefaultCLIPath()
		if err != nil {
			logger.Error("算不出 CLI 落点，无法释出，不阻断向导", "cause", err)
			return
		}
		if err := shell.ReleaseBinary(dst, rc); err != nil {
			logger.Error("释出内嵌二进制失败，不阻断向导", "dst", dst, "cause", err)
			return
		}
		logger.Info("已释出内嵌 handoff 二进制", "dst", dst)
	case shell.DecisionUseExisting:
		logger.Debug("直接使用既有 handoff 安装，不释出", "bin", existing)
	case shell.DecisionEmbeddedNewer:
		logger.Info("既有 handoff 比内嵌旧，只提示不自动替换", "bin", existing,
			"existing_version", existVer, "embedded_version", embedbin.Version)
		app.Event.Emit("wizard-notice", fmt.Sprintf(
			"检测到已有 handoff 版本 %s 比内嵌的 %s 旧，为不影响正在运行的任务，未自动替换。可稍后手动 handoff upgrade。",
			existVer, embedbin.Version))
	}
}

// startWizard 起 goroutine 跑首次引导：一次性交表（wizard-form → wizard-submit），
// 校验通过才写盘；取消或校验失败一律不落盘。
//
// 承重：ApplyAnswers 或 Save 返回错误时**绝不落盘**。半截答案落盘会造出一份
// 「配过但配错」的配置，下次启动 Resolve 会认为这台机器已配置，用户再也
// 回不到向导。
func startWizard(app *application.App, win *application.WebviewWindow) {
	wizMu.Lock()
	if wizActive {
		wizMu.Unlock()
		logger.Warn("首次配置向导已在运行，忽略重复打开")
		return
	}
	wizActive = true
	wizMu.Unlock()

	wizCtx, wizCancel := context.WithCancel(context.Background())
	// 用户关窗 = 取消向导。薄壳继续常驻托盘，配置未落盘，重开即重配。
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		logger.Info("向导窗口关闭，取消首次配置")
		wizCancel()
	})

	go func() {
		defer func() {
			wizMu.Lock()
			wizActive = false
			wizMu.Unlock()
		}()

		// 等前端运行时挂载再发第一笔：webview 加载完成前的 Go 事件会被
		// window._wails 未就绪守卫静默丢弃（W5b-2 已验）。
		select {
		case <-runtimeReadyCh:
		case <-wizCtx.Done():
			return
		}

		// 首次引导不调 config.Load：Load 在文件不存在时会 firstRun 写盘，向导中途
		// 崩溃/被杀/取消都会留下文件，下次启动 Resolve 判「已配置」（W5b-2 缺陷 A）。
		// Defaults 是无副作用入口，问答期间磁盘上不存在任何会让 Resolve 误判的文件。
		path := config.DefaultPath()
		cfg := config.Defaults()

		// 先补 PATH 再探测：双击启动的 GUI 继承 launchd 的默认 PATH
		//（/usr/bin:/bin:/usr/sbin:/sbin），四家 executor 全部装在它之外，
		// 不补就会全部报「未安装」——而「双击就能用」正是薄壳的立项理由。
		pathenv.Apply(wizCtx, pathenv.Options{}, logger)
		results := toolchain.Detect()
		logger.Info("工具链探测完成", "count", len(results))

		fields := shell.BuildForm(cfg, results, runtime.GOOS)
		logger.Info("首次配置表已生成", "fields", len(fields))

		// wizard-submit 监听必须先于 wizard-form 发出注册：否则前端秒填秒交时
		// Go 侧还没监听，事件直接丢。waitAnswers 内部注册监听并返回结果通道，
		// 注册完成后才轮到下面的 Emit。
		answersCh := waitAnswers(wizCtx, app)
		app.Event.Emit("wizard-form", fields)

		var answers map[string]string
		select {
		case ans := <-answersCh:
			// 载荷非对象时 waitAnswers 送 nil：与取消同等处理。
			// 不判空的话 nil map 会被 Apply 当作「无答案」放行、默认配置落盘，
			// 一个畸形 wizard-submit 就静默宣告配置成功（承重，必须不落盘）。
			if ans == nil {
				logger.Warn("wizard-submit 载荷异常，按取消处理")
				return
			}
			answers = ans
		case <-wizCtx.Done():
			logger.Info("首次配置被取消", "cause", wizCtx.Err())
			return // 承重：不落盘
		}

		if err := shell.ApplyAnswers(cfg, fields, answers); err != nil {
			logger.Error("首次配置答案校验失败", "cause", err)
			app.Event.Emit("wizard-error", err.Error())
			return // 承重：不落盘
		}
		if err := config.Save(path, cfg); err != nil {
			logger.Error("保存配置失败", "path", path, "cause", err)
			app.Event.Emit("wizard-error", "保存配置失败："+err.Error())
			return
		}
		logger.Info("首次配置已写盘", "path", path, "role", answers["role"])
		app.Event.Emit("wizard-done")

		ep := shell.Endpoint{Addr: cfg.Listen, Token: cfg.Token}
		spec, err := specFor(ep)
		if err != nil {
			logger.Error("解析 agentd 二进制路径失败", "cause", err)
			showError(app, "无法定位 handoff", err.Error())
			return
		}
		if err := shell.EnsureRunning(logger, spec); err != nil {
			logger.Error("确保 agentd 运行失败", "cause", err)
			showError(app, "无法启动 agentd", err.Error())
			return
		}
		// 向导可能跑了很久，openConsole 入口的 30s 上下文早已过期，这里另起一个
		ictx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url, err := shell.ConsoleURL(ictx, ep, shell.DefaultDeviceName())
		if err != nil {
			logger.Error("握手失败", "cause", err)
			showError(app, "无法连接 agentd", err.Error())
			return
		}
		// 不打 url：里面带一次性凭据
		logger.Info("加载控制台")
		win.SetURL(url)
	}()
}

// waitAnswers 注册 wizard-submit 监听并返回结果通道。
//
// 必须在 Emit("wizard-form") 之前调用：监听先就绪，前端即使秒填秒交也不会丢事件。
// 载荷是 JS 对象，Wails 按 JSON 解码成 map[string]interface{}（不是 map[string]string），
// 逐值转字符串；结构不符时记日志并返回 nil（调用方按取消处理，不落盘）。
func waitAnswers(wizCtx context.Context, app *application.App) <-chan map[string]string {
	ch := make(chan map[string]string, 1)
	app.Event.On("wizard-submit", func(ev *application.CustomEvent) {
		raw, ok := ev.Data.(map[string]interface{})
		if !ok {
			logger.Error("wizard-submit 载荷不是对象", "type", fmt.Sprintf("%T", ev.Data))
			// 与成功路径同用 select/default：有效答案已占满缓冲时畸形载荷
			// 若裸送 ch <- nil 会永久阻塞 Wails 事件回调（goroutine 泄漏）。
			select {
			case ch <- nil:
			default:
				logger.Warn("wizard-submit 载荷异常但已有答案，丢弃")
			}
			return
		}
		answers := make(map[string]string, len(raw))
		for k, v := range raw {
			answers[k] = fmt.Sprint(v)
		}
		select {
		case ch <- answers:
		default:
			logger.Warn("wizard-submit 到达但已有答案，丢弃重复提交")
		}
	})
	return ch
}

// specFor 组装托管 agentd 所需的路径。
//
// 为什么必须绝对路径：launchd/systemd 都解析不了相对路径，BinPath 给相对名
// 会把 agentd 装成一个永远起不来的 service，而且症状只在用户机器上出现。
// 因此这里统一交给 shell.ResolveBinPath 解析，失败就向用户报错，绝不回退。
func specFor(_ shell.Endpoint) (service.Spec, error) {
	bin, err := shell.ResolveBinPath("")
	if err != nil {
		return service.Spec{}, err
	}
	logger.Info("已定位 agentd 二进制", "bin", bin)
	return service.Spec{BinPath: bin}, nil
}

// showError 用原生对话框呈现错误。
//
// 为什么不是往页面里写：此刻页面很可能还没加载出来（握手就是失败在加载之前），
// 往一个空白 webview 里写字用户看不到。
func showError(app *application.App, title, detail string) {
	d := app.Dialog.Error()
	d.SetTitle(title)
	d.SetMessage(detail)
	d.Show()
}
