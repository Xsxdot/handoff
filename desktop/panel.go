// 本文件负责升级面板窗口——薄壳的第三个 UI 面（spec §7）。
//
// 职责：
//   - 创建并持有一个独立窗口，加载内嵌前端的 /upgrade.html
//   - 把同步进度与 upgrade 命令的输出逐行推给它
//
// 边界（承重）：
//   - **必须为本窗口单独挂 WindowRuntimeReady，并在发任何事件之前等它就绪。**
//     Wails 的 windowsWebviewWindow.setURL 至今没有 nil 守卫（相邻的 execJS 有
//     if w.chromium == nil { return }），往一个还没建好的 chromium 上动作会让
//     进程**直接消失、没有任何输出**。rc7 就是这么来的，漏挂就是第二次
//   - 不抢主窗口：主窗口此刻正显示控制台外链
//   - 不自己决定显示什么内容，只做通道
package main

import (
	"context"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// panelReadyTimeout 是等面板 webview 就绪的上限。
//
// 与控制台那条（30s）取同一量级：超时只意味着这次不显示面板，不影响任何
// 实际动作——升级照跑，输出照进日志。
const panelReadyTimeout = 30 * time.Second

// upgradePanel 是升级面板窗口的句柄。
type upgradePanel struct {
	app   *application.App
	win   *application.WebviewWindow
	ready <-chan struct{}
	// once 保证 ready 只被 close 一次：WindowRuntimeReady 在窗口重新加载时
	// 会再次触发，close 两次会 panic
	once      sync.Once
	forceOnce sync.Once
	// readyOnce/readyOK 把「等就绪」的结果缓存下来，见 await 的注释
	readyOnce sync.Once
	readyOK   bool
}

// openUpgradePanel 创建并显示升级面板窗口。
//
// 返回的句柄总是可用的：即便窗口没能就绪，Line/State 也只是把内容记进日志
// 而不报错——面板是给用户看的，它坏了不该让升级本身失败。
func openUpgradePanel(app *application.App) *upgradePanel {
	readyCh := make(chan struct{})
	p := &upgradePanel{app: app, ready: readyCh}
	p.win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "handoff 升级",
		Width:  680,
		Height: 480,
		URL:    "/upgrade.html",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: desktopTopInset,
		},
	})
	// 承重：见文件头。漏了这一挂，下面 AwaitWebviewReady 会一直等到超时，
	// 面板永远空白——而这不会有任何报错
	p.win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		p.once.Do(func() { close(readyCh) })
	})
	p.win.Show()
	logger.Info("升级面板窗口已创建")
	return p
}

// await 等面板就绪，**结果只算一次**。
//
// 为什么必须缓存：Line 会被调几十上百次（upgrade 的输出逐行进来）。每次都
// 开一个 30 秒超时，面板万一一直不就绪，整条升级就会被拖成「行数 × 30 秒」
// ——用户看到的是彻底卡死，而根因只是一个没建好的 webview。
func (p *upgradePanel) await() bool {
	p.readyOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), panelReadyTimeout)
		defer cancel()
		if err := shell.AwaitWebviewReady(ctx, p.ready); err != nil {
			logger.Error("升级面板未就绪，此后内容只进日志", "cause", err)
			return
		}
		p.readyOK = true
	})
	return p.readyOK
}

// Line 往面板追加一行输出。
func (p *upgradePanel) Line(s string) {
	logger.Info("升级输出", "line", s)
	if !p.await() {
		return
	}
	p.app.Event.Emit("upgrade-line", s)
}

// State 切换面板的三态。
//
// 参数：
//   - state: running / ok / fail
//   - detail: 显示在标题上的一句话，空串时用默认文案
func (p *upgradePanel) State(state, detail string) {
	logger.Info("升级面板状态", "state", state, "detail", detail)
	if !p.await() {
		return
	}
	p.app.Event.Emit("upgrade-state", map[string]string{"state": state, "detail": detail})
}

// OnForceRetry 注册「带 --force 重试」的回调。只会注册一次。
func (p *upgradePanel) OnForceRetry(fn func()) {
	p.forceOnce.Do(func() {
		p.app.Event.On("upgrade-force-retry", func(*application.CustomEvent) {
			logger.Info("用户点了带 --force 重试")
			go fn()
		})
	})
}
