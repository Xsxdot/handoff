// Breadcrumb —— 顶部面包屑：项目 / 开发机 / 焦点内容。
//
// 职责：回答「我现在看的 pane 在哪、在看什么」。Shell 传入 active group 的
// focused pane base，并在有焦点内容时把第三段替换成内容名（tail）。
//
// 边界：
//   - **只显示不导航，整行没有任何可点元素**。三段都不可点——上级（项目、
//     开发机）在这套 IA 里不是可以「进入」的东西，做成链接会承诺一个不存在
//     的页面。这条「零交互」现在还是承重的：桌面薄壳把同一份内容画进窗口
//     顶部那 28px，而那条带子里的左键会被 AppKit 拿去拖窗口、传不到页面
//     （见 lib/desktopShell.ts）。往这里加按钮 = 在薄壳里加一个点不动的按钮
//   - 未选中目录时不渲染（由 Shell 判断）
//   - 分屏按钮**不在这里**，在顶部标签条的右端（TabBar）
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { BaseDir } from '../workbench/useWorkbench'

// breadcrumbSegments 把基准目录拆成要显示的几段。
// 导出是为了让桌面薄壳的标题栏（DesktopTitleBar）用同一份拆法——两处各写
// 一遍就会出现「窗口顶上写的和页面里写的不一样」。
// tail 是焦点窗格的内容名（tui=任务原名 / file=文件名 / terminal=终端标题），
// 非空时替换第三段（目录名）；home 基准不属于任何项目/机器，只有一段，tail 不参与。
export function breadcrumbSegments(base: BaseDir, tail?: string): string[] {
  // home 基准不属于任何项目/机器，只显示一段
  if (base.kind === 'home') return ['home']
  return [base.projectName, base.machine === '' ? '本机' : base.machine, tail || base.label]
}

export function Breadcrumb({ base, tail }: { base: BaseDir; tail?: string }) {
  // 形态真源：option-1 的 .workspace-context——28px 高、13px、行高 1、#7c7c7c、
  // 白底、下边框 #e7e7e7、0 18px 内边距、单行省略，整行 title 带全文。
  const segments = breadcrumbSegments(base, tail)
  const full = segments.join(' / ')
  return (
    <div
      title={full}
      className="flex h-7 min-w-0 items-center border-b border-[#e7e7e7] bg-white px-[18px] text-[13px] leading-none text-[#7c7c7c]"
    >
      <BreadcrumbSegments base={base} tail={tail} />
    </div>
  )
}

// BreadcrumbSegments 只画那几段文字，不带外框——外框由调用方给
//（页面里是一整行，薄壳里是窗口顶部那条 28px）。
//
// tone 决定形态：
//   - 'row'（默认）：页面里那一行。段间分隔是字面「 / 」文本（原型如此），
//     整行同一灰（#7c7c7c 由外层行给），末段不再加粗
//   - 'titlebar'：薄壳窗口顶部。**逐字保留重绘前的形态**——ChevronRight 分隔、
//     末段染色、字号再小半档；那条带子由 AppKit 拖窗口逻辑管辖，本卡不动
export function BreadcrumbSegments({ base, tail, tone = 'row' }: { base: BaseDir; tail?: string; tone?: 'row' | 'titlebar' }) {
  const segments = breadcrumbSegments(base, tail)
  const titlebar = tone === 'titlebar'
  if (titlebar) {
    return (
      <nav aria-label="当前位置" className="flex min-w-0 items-center gap-1.5 text-[11.5px]">
        {segments.map((s, i) => (
          <span key={i} className="flex min-w-0 items-center gap-1.5">
            {i > 0 && <ChevronRight className="size-2.5 shrink-0 text-muted-foreground" />}
            <span className={cn('truncate', i === segments.length - 1 ? 'text-foreground' : 'text-muted-foreground')}>
              {s}
            </span>
          </span>
        ))}
      </nav>
    )
  }
  return (
    <nav aria-label="当前位置" className="min-w-0 truncate whitespace-nowrap">
      {segments.map((s, i) => (
        <span key={i}>
          {i > 0 && <span>{' / '}</span>}
          <span>{s}</span>
        </span>
      ))}
    </nav>
  )
}
