// DesktopTitleBar —— 桌面薄壳里替代系统标题栏的那条 28px。
//
// 职责：把窗口顶部那条本来就要让出来的空白，变成「我现在在哪」——左边给
// macOS 的红黄绿留位，右边接面包屑的那几段文字。
//
// 为什么它只能是纯展示（这是本组件存在的全部约束）：
//   薄壳把系统标题栏关掉后，用 InvisibleTitleBarHeight 换回顶部 28px 的原生
//   拖动区（desktop/main.go）。那条带子里的左键被 AppKit 拿去 performWindowDrag
//   并**吞掉**，根本不传给页面——判定是纯几何的（`y > frame.height - 28`），
//   不查 DOM。所以 Orca 那种「tab 就摆在顶栏还能拖窗口」这里做不到：那依赖
//   Electron 的 -webkit-app-region 命中测试，而控制台在薄壳里是外链页面，
//   Wails 运行时不注入。
//
//   **往这里放任何按钮 = 放一个点不动的按钮。** 要加交互，先改
//   desktop/main.go 的 InvisibleTitleBarHeight，并接受窗口不能拖。
//
// 边界：
//   - 只在薄壳里渲染（由 Shell 判断 isDesktopShell），浏览器里根本不挂
//   - 不持有状态；base 为 null（还没选目录）时只显示应用名
import { BreadcrumbSegments } from './Breadcrumb'
import { DESKTOP_TOP_INSET } from '../lib/desktopShell'
import type { BaseDir } from '../workbench/useWorkbench'

// TRAFFIC_LIGHTS_WIDTH 是左边留给红黄绿三个按钮的宽度（px）。
// 它们由 AppKit 画在窗口坐标 ~20..70 处，页面管不着，只能让位。
const TRAFFIC_LIGHTS_WIDTH = 78

export function DesktopTitleBar({ base }: { base: BaseDir | null }) {
  return (
    <div
      data-testid="desktop-title-bar"
      // select-none + cursor-default：这条带子的点击会被拿去拖窗口，
      // 让它看起来可选中/可点是在骗人
      className="flex shrink-0 select-none items-center overflow-hidden border-b bg-sidebar pr-3 text-xs"
      style={{ height: DESKTOP_TOP_INSET, paddingLeft: TRAFFIC_LIGHTS_WIDTH }}
    >
      {base ? (
        <BreadcrumbSegments base={base} />
      ) : (
        <span className="text-muted-foreground">handoff</span>
      )}
    </div>
  )
}
