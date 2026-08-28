// Breadcrumb —— 顶部面包屑：项目 / 开发机 / 目录。
//
// 职责：回答「我现在看的 pane 在哪」。Shell 传入 active group 的 focused pane base；
// 没有焦点内容时由 Shell 不渲染本组件。
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
import { cn } from '@/lib/utils'
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
//
// tone 决定字重与字号：
//   - 'row'（默认）：页面里那一行，末段加粗，它是这一行的主角
//   - 'titlebar'：薄壳窗口顶部。**不加粗、字号再小半档**——原生标题栏是轻的，
//     末段一条长分支名加粗之后会把整条带子压得很重（走查原话「有些丑」的另一半）
export function BreadcrumbSegments({ base, tone = 'row' }: { base: BaseDir; tone?: 'row' | 'titlebar' }) {
  const segments = breadcrumbSegments(base)
  const titlebar = tone === 'titlebar'
  return (
    <nav
      aria-label="当前位置"
      className={cn('flex min-w-0 items-center', titlebar ? 'gap-1.5 text-[11.5px]' : 'gap-1 text-xs')}
    >
      {segments.map((s, i) => (
        <span key={i} className="flex min-w-0 items-center gap-1.5">
          {i > 0 && (
            <ChevronRight className={cn('shrink-0 text-muted-foreground', titlebar ? 'size-2.5' : 'size-3')} />
          )}
          <span
            className={cn(
              'truncate',
              i === segments.length - 1
                ? titlebar
                  ? 'text-foreground'
                  : 'font-medium'
                : 'text-muted-foreground',
            )}
          >
            {s}
          </span>
        </span>
      ))}
    </nav>
  )
}
