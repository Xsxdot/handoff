// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配与错误呈现，外加 Wails 事件收发（向导问答的传输层）。
// 边界：
//   - **不放业务逻辑**。定位、握手、生命周期、路径校验全在 internal/shell，
//     那里不 import Wails，因而可以用普通 go test 覆盖
//   - **首次引导的问题集与默认值属于 internal/initflow**。main.go 只装配
//     wizard-question / wizard-answer / wizard-notice 的收发通道，不在
//     这里加任何问题、不定义任何默认值
//   - **不在退出路径上停 agentd**（spec §4.3 承重）
//   - 托盘只有「打开控制台」「退出」两项。**不做「停止 agentd」**：
//     service.Manager 没有 Stop，用 Uninstall 冒充是错的语义
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/embedbin"
	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/initflow"
	"github.com/Xsxdot/handoff/internal/service"
	"github.com/Xsxdot/handoff/internal/toolchain"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// logger 是包级日志入口。main() 里装配成 TextHandler 写 stderr；
// 包级小函数（readInstalledVersion 等）复用同一实例，避免混用默认 logger
// 导致日志格式不统一。
var logger = slog.Default()

// wizAnswers 是 wizard-answer 事件的唯一收件箱。
//
// **回调只在装配处注册一次**（见 main）：绝不能在每次 Ask 里注册——每问注册
// 会累积 handler，第 N 问收到 N 份答案，谁先抢到还不确定，第一问之后的答案
// 全部错位。
var wizAnswers = make(chan string, 1)

// wizMu/wizActive 防重入：一个向导进行中时，再次从托盘打开控制台不另起一个
// AskAll，否则两份问答在同一个页面上互相覆盖。
var (
	wizMu     sync.Mutex
	wizActive bool
)

// runtimeReadyCh 在前端运行时挂载（WindowRuntimeReady）后关闭。
//
// 首个 wizard-question 必须等它：webview 加载完成前发出的 Go 事件会被
// Wails 事件模板里 `if(window._wails&&window._wails.dispatchWailsEvent)`
// 的守卫静默丢弃（见 Wails 源码 inlineEventJS），早发等于把第一题吞掉，
// 向导从此卡在空白页。所以向导 goroutine 先等这个 channel 再发问。
var runtimeReadyCh = make(chan struct{})

// closeRuntimeReady 保证只 close 一次：WindowRuntimeReady 若重发，
// 直接 close 一个已关闭的 channel 会 panic。
var closeRuntimeReady sync.Once

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
	})

	openConsole := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

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

	tray := app.SystemTray.New()
	tray.SetLabel("handoff")
	menu := app.Menu.New()
	menu.Add("打开控制台").OnClick(func(*application.Context) { openConsole() })
	menu.Add("退出（agentd 继续运行）").OnClick(func(*application.Context) {
		// 只退薄壳。agentd 与它拉起的执行者继续跑，这是招牌属性
		logger.Info("用户从托盘退出薄壳；agentd 不受影响")
		app.Quit()
	})
	tray.SetMenu(menu)
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

	// wizard-answer 只注册一次（承重）：若在每次 Ask 里注册，第 N 问会累积
	// N 份 handler，第 N 问收到 N 份答案。这里收敛成把值送进唯一收件箱，
	// 由 wailsTransport.Ask 逐个取走。
	app.Event.On("wizard-answer", func(ev *application.CustomEvent) {
		s, ok := ev.Data.(string)
		if !ok {
			// 前端 emit 的必是字符串；真不是就跳过，不 panic
			logger.Error("wizard-answer 载荷不是字符串，跳过", "type", fmt.Sprintf("%T", ev.Data))
			return
		}
		select {
		case wizAnswers <- s:
		default:
			// 满箱说明这份答案没有对应的等待者（多半是向导已取消），
			// 丢弃它防止污染下一次向导
			logger.Warn("wizard-answer 到达但无人在等待，丢弃")
		}
	})

	// 前端运行时挂载后才发得出向导问题：见 runtimeReadyCh 的注释
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		closeRuntimeReady.Do(func() { close(runtimeReadyCh) })
	})

	// 启动序列必须等应用就绪后再跑：showError 经 app.Dialog → InvokeSync →
	// a.impl.isOnMainThread()，而 a.impl 由 app.Run() 初始化——ApplicationStarted
	// 之前走到 showError（如握手 401）会 nil deref panic（实测复现）。
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go openConsole()
	})

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出；agentd 未被触碰")
}

// wailsTransport 是 shell.Transport 的 Wails 侧实现：Ask 经 app.Event 发题、
// 阻塞等前端答案；Notice 转发面向用户的说明文字。
//
// 为什么放 main.go：internal/shell 约定不 import Wails（才能普通 go test 覆盖），
// 这套收发恰好是 Wails 专有的，只能留在装配层。
type wailsTransport struct {
	app     *application.App
	answers chan string
	ctx     context.Context
}

func (t *wailsTransport) Ask(q shell.Question) (string, error) {
	t.app.Event.Emit("wizard-question", q)
	select {
	case <-t.ctx.Done():
		// ctx 取消（用户关窗）由 EventPrompter 映射成 initflow.ErrCanceled，
		// 上层据此决定不写盘
		return "", t.ctx.Err()
	case s := <-t.answers:
		return s, nil
	}
}

func (t *wailsTransport) Notice(line string) {
	t.app.Event.Emit("wizard-notice", line)
}

// readInstalledVersion 从既有 handoff 二进制里读版本号，供释出决策用。
// 返回 stdout 首行去空白；任何失败或首行是 unknown 都判不出（空串）——
// 与 shell.DecideRelease 的保守约定一致：判不出就偏保守，用用户已有的。
//
// 为什么要隔离 HOME：handoff CLI 每条命令跑完会经 PersistentPostRun 触发
// maybeNotifyUpdate → config.Load，而 Load 在配置文件不存在时会 firstRun
// 写盘（生成 config.yaml + 随机 token）。若子进程继承桌面壳的 HOME，首次
// 引导期间这一下就把 config.yaml 写进了目标目录——SIGKILL/崩溃后文件仍在，
// 下次启动 Resolve 判「已配置」，向导永不再现（真机实测过）。把子进程的
// HOME 指到一个一次性临时目录，firstRun 写盘就落在那里，目标 HOME 干净。
func readInstalledVersion(path string) string {
	// 一次性临时 HOME：隔离 CLI 的 firstRun 写盘等副作用，用完即删
	tmp, err := os.MkdirTemp("", "handoff-version-probe-*")
	if err != nil {
		logger.Warn("创建版本探测临时目录失败，按判不出处理", "cause", err)
		return ""
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command(path, "version")
	cmd.Env = append(os.Environ(), "HOME="+tmp)
	out, err := cmd.Output()
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
		home, err := os.UserHomeDir()
		if err != nil {
			logger.Error("取不到用户主目录，无法释出，不阻断向导", "cause", err)
			return
		}
		dst := filepath.Join(home, ".local", "bin", "handoff")
		if err := shell.ReleaseBinary(dst, rc); err != nil {
			logger.Error("释出内嵌二进制失败，不阻断向导", "dst", dst, "cause", err)
			return
		}
		logger.Info("已释出内嵌 handoff 二进制", "dst", dst)
	case shell.DecisionUseExisting:
		logger.Debug("直接使用既有 handoff 安装，不释出", "bin", existing)
	case shell.DecisionNotifyOutdated:
		logger.Info("既有 handoff 比内嵌旧，只提示不自动替换", "bin", existing,
			"existing_version", existVer, "embedded_version", embedbin.Version)
		app.Event.Emit("wizard-notice", fmt.Sprintf(
			"检测到已有 handoff 版本 %s 比内嵌的 %s 旧，为不影响正在运行的任务，未自动替换。可稍后手动 handoff upgrade。",
			existVer, embedbin.Version))
	}
}

// startWizard 起 goroutine 跑首次引导：经 EventPrompter 驱动 initflow.AskAll，
// 成功才写盘；取消一律不写盘。
//
// 承重：AskAll 返回错误时**绝不 config.Save**。半截答案落盘会造出一份
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

	tr := &wailsTransport{app: app, answers: wizAnswers, ctx: wizCtx}
	p := shell.NewEventPrompter(wizCtx, tr)
	w := shell.NewNoticeWriter(tr)

	go func() {
		defer func() {
			wizMu.Lock()
			wizActive = false
			wizMu.Unlock()
		}()
		// 等前端运行时挂载再发第一问：webview 加载完成前的 Go 事件会被
		// window._wails 未就绪守卫静默丢弃，早发等于把第一题吞掉。
		select {
		case <-runtimeReadyCh:
		case <-wizCtx.Done():
			return
		}

		// 首次引导不调 config.Load：Load 在文件不存在时会 firstRun 写盘
		//（生成 token + 默认配置落盘），向导中途崩溃/被杀/取消都会留下这份
		// 文件，下次启动 Resolve 判「已配置」，用户回不到向导（真机 SIGKILL
		// 复现过，回滚法依赖进程还活着封不死）。Defaults 是无副作用入口，
		// 问答期间磁盘上不存在任何会让 Resolve 误判的文件，成功后才 Save。
		path := config.DefaultPath()
		cfg := config.Defaults()
		// 桌面壳也要探测工具链：AskAll 里 ExecutorOptions(nil) 会是空选项列表，
		// 用户选执行者时前端渲染空 select，EventPrompter 会拒绝一切非空答案——
		// 这是必要适配，不是可选项。
		results := toolchain.Detect()
		// isExec 不用于 GUI 托管（与 CLI 的 MaybeInstallService 追问不同步，
		// 这是有意为之）：首次引导的 agentd 托管由后面的 EnsureRunning 统一
		// 走 service 路径，避免图形向导里再弹一轮托管问题。只留 role 写盘日志用。
		isExec, role, err := initflow.AskAll(w, p, cfg, results, false)
		_ = isExec
		if err != nil {
			// 承重：AskAll 出错绝不写盘。取消或问答失败都等于放弃本次配置。
			// Defaults 无副作用（从未落盘），此处无需回滚；半截答案随进程丢弃，
			// 下次启动 Resolve 仍判未配置，重开即可重新进入向导。
			if errors.Is(err, initflow.ErrCanceled) {
				logger.Info("首次配置已取消，不写盘", "path", path)
				tr.Notice("已取消，未保存任何配置。关闭窗口重新打开即可重新配置。")
			} else {
				logger.Error("首次配置问答失败，不写盘", "path", path, "cause", err)
			}
			return
		}
		if err := config.Save(path, cfg); err != nil {
			logger.Error("保存配置失败", "path", path, "cause", err)
			tr.Notice("保存配置失败：" + err.Error())
			return
		}
		logger.Info("首次配置已写盘", "path", path, "role", role)
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
