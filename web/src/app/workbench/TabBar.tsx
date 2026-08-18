// TabBar —— 一组 tab 的标签条。
//
// 职责：渲染标签、标出激活项、提供关闭与「新建标签页」。
// 边界：不持有状态，全部经回调上抛；不认识 tab 内容的语义，标题由 tabTitle 算。
//
// 「新建」为什么是菜单而不是直接开一个空白 tab：先开空白页再在页面中间选种类，
// 等于把一次选择拆成两屏。+ 直接把三种去处列出来，选完就位——与浮窗 tab 条上
// 那个 + 是同一套动作（IconMenu）。
import { Plus, X } from 'lucide-react'
import { tabTitle, type Tab } from './tabs'
import { pickItemsFor, type PickKind } from './BlankTab'
import { IconMenu } from '../lib/IconMenu'
import type { BaseDir } from './useWorkbench'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  group: number
  tabs: Tab[]
  activeId: string | null
  base: BaseDir // 决定 + 菜单里列哪几种（home 只有终端）
  onActivate: (group: number, tabId: string) => void
  onClose: (group: number, tabId: string) => void
  // onNew 收到的是用户在 + 菜单里选的种类，由调用方决定怎么把它变成 tab。
  onNew: (group: number, kind: PickKind) => void
  // terminalUnavailable 非空 = 这台机器开不了终端，菜单里摘掉终端项（不置灰）
  terminalUnavailable?: string
}

export function TabBar({ group, tabs, activeId, base, onActivate, onClose, onNew, terminalUnavailable }: TabBarProps) {
  const baseLabel = base.label
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
      <IconMenu
        label="新建标签页"
        icon={<Plus className="size-4" />}
        className="flex items-center px-2 text-muted-foreground hover:text-foreground"
        items={pickItemsFor(base, terminalUnavailable).map((item) => ({
          key: item.kind,
          label: item.label,
          hotkey: item.hotkey,
          icon: <item.icon className="size-3.5 text-muted-foreground" />,
          // 包一层箭头：不包的话 onSelect 会把参数直接漏进 onNew
          onSelect: () => onNew(group, item.kind),
        }))}
      />
    </div>
  )
}
