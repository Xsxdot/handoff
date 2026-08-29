// desktopShell —— 判断控制台此刻是不是跑在桌面薄壳（Wails）的 webview 里，
// 以及为此要让出的顶部空白。
//
// 为什么需要它：薄壳把系统标题栏去掉了（desktop/main.go 的 MacTitleBarHidden），
// 于是窗口顶部 28px 变成 AppKit 的隐形拖动区——那一条里的左键会被拿去拖窗口，
// **不会传给页面**。页面不让出同高的空白，就会出现「看得见、点不动」的控件；
// 红黄绿三个交通灯也会压在左栏头上。
//
// 边界：
//   - 只读 UA，不发请求、不碰后端。控制台在薄壳里是**外链页面**（agentd 伺服），
//     只拿得到 Wails 注入的 runtime core，前端 bundle 那层的公开 API
//     （window.wails、--wails-draggable 之类）一概没有——UA 后缀是唯一
//     不用改握手协议就能传进来的信号。
//     注意别把这条读成「什么都没注入」：`window._wails` 这个内部对象是**有**的
//     （导航完成后约 1s 出现，实测），拖放通道正是挂在它上面走的，
//     见 desktopFileDrop.ts
//   - 浏览器里打开时一律返回 false / 0，普通页面不受任何影响

// DESKTOP_TOP_INSET 是薄壳窗口顶部隐形拖动区的高度（CSS px）。
//
// 承重：必须与 desktop/main.go 的 desktopTopInset 一致，两处要一起改。
export const DESKTOP_TOP_INSET = 28

// UA_TAG 是薄壳附在 webview UA 末尾的标记（Mac.WebviewPreferences.ApplicationNameForUserAgent）。
const UA_TAG = 'handoff-desktop'

// isDesktopShell 报告当前页面是否运行在桌面薄壳里。
// ua 参数只为测试注入，正常调用不传。
export function isDesktopShell(ua: string = navigator.userAgent): boolean {
  return ua.includes(UA_TAG)
}

// topInset 返回页面顶部必须让出的高度（px）。浏览器里是 0。
//
// 注意：这是**视口坐标**上的让位量，fixed 定位的元素（悬浮窗、弹层）要自己
// 加上它，因为它们不受根容器 padding 的约束。
export function topInset(ua?: string): number {
  return isDesktopShell(ua ?? navigator.userAgent) ? DESKTOP_TOP_INSET : 0
}

// WebkitBridge 是 WKWebView 注入的消息通道。Wails 在 webview 的
// userContentController 上注册了名为 external 的 handler
//（webview_window_darwin.go:55），它是**挂在整个 webview 上的、不是挂在某个
// 页面上**——所以控制台这个外链页面同样够得着，哪怕 Wails 的 JS 运行时没注入。
interface WebkitBridge {
  webkit?: { messageHandlers?: { external?: { postMessage: (msg: string) => void } } }
}

// OPEN_BROWSER_MESSAGE_PREFIX 是 CardsPage 发给桌面宿主的原始消息前缀。
// 必须避开 wails:：Wails 会把 wails: 消息交给自己的窗口手势处理器，不会送到
// RawMessageHandler。URL 作为同一条字符串的后缀传输，桌面侧再做同源校验。
export const OPEN_BROWSER_MESSAGE_PREFIX = 'handoff:open-browser:'

// requestOpenCurrentPageInBrowser 请求桌面宿主用系统浏览器打开当前整页。
// 参数：无；URL 只能从当前 window.location 产生，调用方不能注入任意地址。
// 返回：找到并发出 external bridge 时为 true，否则为 false。
// 注意：它不导航、不改变当前页面状态；浏览器分支没有 bridge 时是安静空操作。
export function requestOpenCurrentPageInBrowser(): boolean {
  const bridge = window as unknown as WebkitBridge
  const post = bridge.webkit?.messageHandlers?.external?.postMessage
  if (!post) return false
  const currentURL = `${window.location.origin}${window.location.pathname}${window.location.search}`
  post.call(bridge.webkit!.messageHandlers!.external, `${OPEN_BROWSER_MESSAGE_PREFIX}${currentURL}`)
  return true
}

// requestTitlebarZoom 请求薄壳把窗口在「最大化 / 还原」之间切换，返回是否发出去了。
//
// 为什么需要它：**双击标题栏最大化在 Wails 里是 JS 实现的，不是原生的**。
// 运行时的 drag.ts 收到 dblclick 后发 `wails:drag:doubleclick`，Go 侧
// （webview_window.go 的消息分发）才调 handleTitlebarDoubleClick；原生的
// handleLeftMouseDown 里那句 `if (event.clickCount != 1) return` 就是专门给它让路的。
// 外链页面没有运行时 → 没人发这条消息 → 双击顶栏没反应（走查实测）。
//
// **这里用的是 Wails 的内部协议**（handler 名 external + 消息字符串常量），
// 不是公开 API。升级 Wails 时若这两样改了，双击会**静默失效**——不会报错、
// 不会崩，只是没反应；届时回到 drag.ts 与 webview_window.go 对一下这两个名字。
// 之所以敢用：它是纯锦上添花的手势，绿色按钮与窗口菜单始终能做同一件事。
export function requestTitlebarZoom(): boolean {
  const bridge = window as unknown as WebkitBridge
  const post = bridge.webkit?.messageHandlers?.external?.postMessage
  if (!post) return false
  post.call(bridge.webkit!.messageHandlers!.external, 'wails:drag:doubleclick')
  return true
}
