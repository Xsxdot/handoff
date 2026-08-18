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
//   **往这里放任何按钮 = 放一个按下就变成拖窗口的按钮。** 事件本身到得了页面
//   （那两个 addLocalMonitorForEvents 每条路径都 return event，不吞事件），但
//   第一次按下当场被 performWindowDrag 拿去开拖动会话，点击语义就没了。要加
//   交互，先改 desktop/main.go 的 InvisibleTitleBarHeight，并接受窗口不能拖。
//
//   例外是双击：原生那段刻意用 `clickCount != 1` 给它让路，所以双击最大化
//   补得上（见 requestTitlebarZoom），它也是这里唯一的交互。
//
// 边界：
//   - 只在薄壳里渲染（由 Shell 判断 isDesktopShell），浏览器里根本不挂
//   - 不持有状态；base 为 null（还没选目录）时只显示应用名
import { BreadcrumbSegments } from './Breadcrumb'
import { DESKTOP_TOP_INSET, requestTitlebarZoom } from '../lib/desktopShell'
import type { BaseDir } from '../workbench/useWorkbench'

// TRAFFIC_LIGHTS_WIDTH 是左边留给红黄绿三个按钮的宽度（px）。
// 它们由 AppKit 画在窗口坐标 ~20..70 处，页面管不着，只能让位。
const TRAFFIC_LIGHTS_WIDTH = 78

// 右侧留白取与左侧同宽（而不是随便给个 16）：两侧对称，路径才落在窗口**真正的
// 中线**上。不对称的话它会被交通灯那一侧挤开——实测偏右 31px，肉眼看得出来。

export function DesktopTitleBar({ base }: { base: BaseDir | null }) {
  return (
    <div
      data-testid="desktop-title-bar"
      // select-none：这条带子的点击会被拿去拖窗口，让它看起来可选中是在骗人。
      //
      // 底色 bg-sidebar 只比内容区的纯白深 1.5%（oklch 0.985 vs 1），单靠它分不开
      // 两块，所以那条 border-b 是必须的——去掉之后路径看起来是浮在空中的。
      // 这也正是 Finder 那类原生窗口的做法：同色调 + 一根发丝线
      className="relative flex shrink-0 select-none items-center border-b bg-sidebar"
      style={{ height: DESKTOP_TOP_INSET }}
      // 双击标题栏最大化/还原：这个手势在 Wails 里是 JS 实现的，运行时没注入
      // 就没人发那条消息，双击于是没反应（走查实测）。这里自己补上——桥是通的，
      // 详见 requestTitlebarZoom 的注释（含它用了内部协议、会静默失效的代价）
      onDoubleClick={() => requestTitlebarZoom()}
    >
      {/* 绝对定位居中：像原生标题栏那样把路径摆在窗口正中，而不是从交通灯右边
          起头——那个起点既不对齐左栏文字也不对齐中栏，看着是浮着的。
          左右各留出让位，路径再长也压不到交通灯上 */}
      <div
        className="absolute flex items-center justify-center"
        style={{ left: TRAFFIC_LIGHTS_WIDTH, right: TRAFFIC_LIGHTS_WIDTH, top: 0, bottom: 0 }}
      >
        {base ? (
          <BreadcrumbSegments base={base} tone="titlebar" />
        ) : (
          <span className="text-[11px] text-muted-foreground">handoff</span>
        )}
      </div>
    </div>
  )
}
