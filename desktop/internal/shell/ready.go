// 本文件只回答一个问题：窗口的 webview 建好了没有，可以让它去导航了吗。
//
// 边界：
//   - 不订阅任何 Wails 事件、不碰 window 对象。就绪信号由调用方以 channel 传入
//     （main.go 用 WindowRuntimeReady 关闭它），这里只负责「等，并且等得有头」
//   - 不判断导航目标是否可达。那是 agentd 与 webview 的事
package shell

import (
	"context"
	"fmt"
)

// AwaitWebviewReady 阻塞到 webview 就绪，或 ctx 到期。
//
// 参数：
//   - ctx: 等待上限。**必须带超时**，否则窗口若永远起不来会把调用方挂死
//   - ready: 就绪信号；关闭即表示就绪。已关闭的 channel 立即返回（重复调用安全）
//
// 返回：
//   - nil 表示可以安全导航
//   - error 表示等超时了，调用方**不要**再去导航（见下面为什么这是承重的）
//
// 为什么必须等：Wails v3.0.0-beta.8 的 Windows 后端里
// `windowsWebviewWindow.setURL` 是
//
//	func (w *windowsWebviewWindow) setURL(url string) {
//	    w.webviewNavigationCompleted = false
//	    w.chromium.Navigate(url)      // ← 不判 w.chromium 是否已建好
//	}
//
// 而紧挨着它的 `execJS` **判了**（`if w.chromium == nil { return }`）——同一个
// 后端里两条相邻路径，一条有守卫一条没有。上层 `WebviewWindow.SetURL` 只判了
// `w.impl != nil`，而 `w.impl` 在 `webview_window.go:484` 就被赋值，**早于**
// `run()` 里调用 `setupChromium()`。于是「impl 有了、chromium 还没有」是一个
// 真实存在的窗口期。
//
// 2026-08-19 win-b37 实测：薄壳在 ApplicationStarted 后约 110ms 就完成握手并
// 调 SetURL，此时 chromium 尚未建好，进程当场消失——**没有 Go panic 栈**，只在
// stderr 留下一行 Chromium 的 `Failed to unregister class Chrome_WidgetWin_0`，
// 看起来像「双击没反应」。agentd 侧的取证是：ticket 一张张签发出去，却
// **一张都没有被消费**——webview 根本没去请求那个 URL。
//
// macOS 上同一段代码一直正常，所以这条只在 Windows 上显形；而首次配置向导那条
// 路径不受影响，因为它本来就等 WindowRuntimeReady（见 main.go 的 runtimeReadyCh
// 注释）——「向导能开、快捷方式打不开」正是由此而来。
func AwaitWebviewReady(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等窗口 webview 就绪超时（此时导航会让进程直接消失）: %w", ctx.Err())
	}
}
