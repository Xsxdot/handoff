// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配与错误呈现。
// 边界：
//   - **不放业务逻辑**。定位、握手、生命周期、路径校验全在 internal/shell，
//     那里不 import Wails，因而可以用普通 go test 覆盖
//   - **不在退出路径上停 agentd**（spec §4.3 承重）
//   - 托盘只有「打开控制台」「退出」两项。**不做「停止 agentd」**：
//     service.Manager 没有 Stop，用 Uninstall 冒充是错的语义
package main

import (
	"context"
	"embed"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/Xsxdot/handoff/internal/service"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
			// 首次引导是 W5b-2 的范围。在它做出来之前，这里必须给一条
			// 能自救的指引，而不是一个空白窗口
			logger.Info("这台机器还没配置过 handoff")
			showError(app, "还没有配置 handoff",
				"请先在终端执行 handoff init 完成配置，然后重新打开本应用。\n"+
					"（图形化首次引导将在后续版本提供）")
			return
		}
		if err := shell.EnsureRunning(logger, specFor(ep)); err != nil {
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

	go openConsole()

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出；agentd 未被触碰")
}

// specFor 组装托管 agentd 所需的路径。
//
// BinPath 取当前可执行文件所在目录旁的 handoff——薄壳与 CLI 用同一份二进制是
// W5b-2 内嵌方案的前提（spec §5.2）。取不到时退回 PATH 上的 handoff。
func specFor(_ shell.Endpoint) service.Spec {
	// 具体路径策略在 W5b-2 内嵌二进制时才完整；本轮先用 PATH 上的 handoff，
	// 并在日志里说清，避免它悄悄变成一个隐藏约定
	slog.Info("本轮用 PATH 上的 handoff 托管 agentd；内嵌与释出策略见 W5b-2")
	return service.Spec{BinPath: "handoff"}
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
