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
//     Wails 运行时不会注入，window.wails 之类的探针一律为空——UA 后缀是唯一
//     不用改握手协议就能传进来的信号
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
