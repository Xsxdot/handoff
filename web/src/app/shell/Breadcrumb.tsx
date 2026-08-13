// Breadcrumb —— 顶部面包屑：项目 / 开发机 / 目录，右侧分屏按钮。
//
// 职责：回答「我现在在哪」。当前目录是唯一的全局选中态（spec §1.2），面包屑
// 就是它的可见形式。
//
// 边界：
//   - 只显示不导航。三段都不可点——上级（项目、开发机）在这套 IA 里不是可以
//     「进入」的东西，做成链接会承诺一个不存在的页面
//   - 未选中目录时不渲染（由 Shell 判断）
import { ChevronRight, Columns2 } from 'lucide-react'
import type { BaseDir } from '../workbench/useWorkbench'

export function Breadcrumb({ base, onSplit }: { base: BaseDir; onSplit: () => void }) {
  // home 基准不属于任何项目/机器，只显示一段
  const segments =
    base.kind === 'home'
      ? ['home']
      : [base.projectName, base.machine === '' ? '本机' : base.machine, base.label]
  return (
    <div className="flex items-center gap-1 border-b bg-background px-3 py-1.5">
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
      <button
        type="button"
        aria-label="分屏"
        onClick={onSplit}
        className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <Columns2 className="size-4" />
      </button>
    </div>
  )
}
