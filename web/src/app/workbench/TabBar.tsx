// TabBar —— 一组 tab 的标签条。
//
// 职责：渲染标签、标出激活项、提供关闭与「新建标签页」。
// 边界：不持有状态，全部经回调上抛；不认识 tab 内容的语义，标题由 tabTitle 算。
import { Plus, X } from 'lucide-react'
import { tabTitle, type Tab } from './tabs'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  group: number
  tabs: Tab[]
  activeId: string | null
  baseLabel: string
  onActivate: (group: number, tabId: string) => void
  onClose: (group: number, tabId: string) => void
  onNew: (group: number) => void
}

export function TabBar({ group, tabs, activeId, baseLabel, onActivate, onClose, onNew }: TabBarProps) {
  return (
    <div role="tablist" className="flex min-h-9 items-stretch border-b bg-background">
      {tabs.map((t) => {
        const title = tabTitle(t.content, baseLabel)
        const active = t.id === activeId
        return (
          <div key={t.id} className={cn('group flex items-center border-r', active && 'bg-muted/60')}>
            <button
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => onActivate(group, t.id)}
              className="max-w-48 truncate px-3 py-1.5 text-[13px]"
            >
              {title}
            </button>
            <button
              type="button"
              aria-label={`关闭 ${title}`}
              onClick={() => onClose(group, t.id)}
              className="mr-1 rounded p-0.5 text-muted-foreground opacity-0 hover:bg-accent group-hover:opacity-100"
            >
              <X className="size-3.5" />
            </button>
          </div>
        )
      })}
      <button
        type="button"
        aria-label="新建标签页"
        onClick={() => onNew(group)}
        className="flex items-center px-2 text-muted-foreground hover:text-foreground"
      >
        <Plus className="size-4" />
      </button>
    </div>
  )
}
