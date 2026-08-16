// 本文件是桌面薄壳的入口：装配窗口、托盘与启动序列。
//
// 职责：只做装配。
// 边界：**不放任何业务逻辑**——定位 agentd、握手、判断 agentd 起没起，
// 全在 internal/shell 里，那里不 import Wails，因而可以用普通 go test 覆盖。
// 往本文件里写 if/else 之前先问：它能不能挪进 internal/shell。
package main

import (
	"embed"
	"log"
	"log/slog"
	"os"

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
			// 承重：关掉最后一个窗口时进程必须活着，否则托盘常驻无从谈起
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff",
		Width:  1200,
		Height: 800,
		URL:    "/",
	})
	logger.Info("主窗口已创建")

	if err := app.Run(); err != nil {
		logger.Error("薄壳运行失败", "cause", err)
		log.Fatal(err)
	}
	logger.Info("薄壳正常退出")
}
