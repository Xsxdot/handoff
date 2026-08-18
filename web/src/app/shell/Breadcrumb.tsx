// Breadcrumb —— 顶部面包屑：项目 / 开发机 / 目录。
//
// 职责：回答「我现在在哪」。当前目录是唯一的全局选中态（spec §1.2），面包屑
// 就是它的可见形式。
//
// 边界：
//   - **只显示不导航，整行没有任何可点元素**。三段都不可点——上级（项目、
//     开发机）在这套 IA 里不是可以「进入」的东西，做成链接会承诺一个不存在
//     的页面。这条「零交互」现在还是承重的：桌面薄壳把同一份内容画进窗口
//     顶部那 28px，而那条带子里的左键会被 AppKit 拿去拖窗口、传不到页面
//     （见 lib/desktopShell.ts）。往这里加按钮 = 在薄壳里加一个点不动的按钮
//   - 未选中目录时不渲染（由 Shell 判断）
//   - 分屏按钮**不在这里**，在每条 tab 条的右端（TabBar）：那里才知道
//     「在哪一栏的右边分」，而且不受上面那条零交互约束
import { ChevronRight } from 'lucide-react'
import type { BaseDir } from '../workbench/useWorkbench'

// breadcrumbSegments 把基准目录拆成要显示的几段。
// 导出是为了让桌面薄壳的标题栏（DesktopTitleBar）用同一份拆法——两处各写
// 一遍就会出现「窗口顶上写的和页面里写的不一样」。
export function breadcrumbSegments(base: BaseDir): string[] {
  // home 基准不属于任何项目/机器，只显示一段
  if (base.kind === 'home') return ['home']
  return [base.projectName, base.machine === '' ? '本机' : base.machine, base.label]
}

export function Breadcrumb({ base }: { base: BaseDir }) {
  return (
    <div className="flex items-center gap-1 border-b bg-background px-3 py-1.5">
      <BreadcrumbSegments base={base} />
    </div>
  )
}

// BreadcrumbSegments 只画那几段文字，不带外框——外框由调用方给
//（页面里是一整行，薄壳里是窗口顶部那条 28px）。
export function BreadcrumbSegments({ base }: { base: BaseDir }) {
  const segments = breadcrumbSegments(base)
  return (
    <nav aria-label="当前位置" className="flex min-w-0 items-center gap-1 text-xs">
      {segments.map((s, i) => (
        <span key={i} className="flex min-w-0 items-center gap-1">
          {i > 0 && <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
          <span className={i === segments.length - 1 ? 'truncate font-medium' : 'truncate text-muted-foreground'}>
            {s}
          </span>
        </span>
      ))}
    </nav>
  )
}
