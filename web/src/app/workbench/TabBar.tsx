// TabBar.tsx —— 顶部 chrome 的标签栏。
//
// 形态真源：prototypes/base/pages/project-tree-option-1.html 的
// .workspace-tabbar / .workspace-tab / .workspace-tab-surface / .workspace-tab-close /
// .workspace-tab-add（样式数值一比一对照移植）。
//
// 职责：把全部已打开内容 tab 画成一行——类型图标、任务原名/内容名、关闭钮、
// 激活药丸面、tab 间 20px 短节奏分隔线；行尾 + 钮承接「新建内容」菜单。
// 边界：布局迁移（激活/关闭/新建）全部经回调交给 WorkbenchPage/tabs.ts，
// 本组件不持有布局状态；group 仍是布局单位，只是不再画成 tab（组模型的重构归 B264）。
// pane 内没有 tab row；每列最多两格的布局约束由 WorkbenchPage/tabs.ts 负责。
import { Fragment } from 'react'
import { FileText, Plus, Terminal, X } from 'lucide-react'
import dispatchTaskUrl from '../../assets/dispatch-task.png'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import { launchersFor, pickItemsFor, type LauncherItem, type PickKind } from './BlankTab'
import { tabTitle, type BaseDir, type Tab, type TabGroup } from './tabs'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  groups: TabGroup[]
  activeGroupId: string
  base: BaseDir | null
  // taskName 把 tui 的 taskId 解析成任务原名；解析不到（任务已删除等）时
  // tabTitle 自己回退 TUI · 前 8 位，所以这个 resolver 可以缺席
  taskName?: (taskId: string) => string | undefined
  onActivateTab: (groupId: string, tabId: string) => void
  onCloseTab: (groupId: string, tabId: string) => void
  onNew: (groupId: string, kind: PickKind) => void
  onNewLauncher?: (groupId: string, name: string) => void
  launchers?: LauncherItem[]
  terminalUnavailable?: string
}

// BarTab 是标签条上的一行：内容 tab 加上它所属的 group（激活/关闭都要 group id）。
interface BarTab {
  tab: Tab
  groupId: string
}

// barTabs 把全局布局摊平成标签条的行序：group 序 → 列序 → 行序。
// 空窗格不占行——标签条只列真实内容，与左栏「已打开项」同一口径。
function barTabs(groups: TabGroup[]): BarTab[] {
  const rows: BarTab[] = []
  for (const group of groups) {
    for (const column of group.columns) {
      for (const tab of column.panes) {
        if (tab) rows.push({ tab, groupId: group.id })
      }
    }
  }
  return rows
}

// TabTypeIcon 按内容种类映射图标：tui 用 dispatch-task 资产图标（与原型的
// 图标映射一致），terminal/file 用同形线性图标，blank 用 +。
function TabTypeIcon({ tab }: { tab: Tab }) {
  switch (tab.content.kind) {
    case 'tui': return <img src={dispatchTaskUrl} className="size-[15px]" alt="" />
    case 'terminal': return <Terminal className="size-[15px]" />
    case 'file': return <FileText className="size-[15px]" />
    case 'blank': return <Plus className="size-[15px]" />
  }
}

// TabIconSlot 对应原型的 15px 图标槽位（.workspace-tab .tab-icon）。
function TabIconSlot({ tab }: { tab: Tab }) {
  return (
    <span aria-hidden className="inline-flex h-[15px] w-[15px] shrink-0 items-center justify-center opacity-[0.85]">
      <TabTypeIcon tab={tab} />
    </span>
  )
}

// TabClose 对应原型的 .workspace-tab-close：15px 槽位、13px 图标、#777 → hover #222。
// 放在 tab 按钮内部（原型如此，药丸面要把关闭钮一起包进去），所以是 span 而非
// button——HTML 不允许按钮嵌套；role="button" + aria-label 保住可达性。
function TabClose({ title, onClose }: { title: string; onClose: () => void }) {
  return (
    <span
      role="button"
      aria-label={'关闭 ' + title}
      onClick={(event) => {
        event.stopPropagation()
        onClose()
      }}
      className="inline-flex h-[15px] w-[15px] shrink-0 cursor-pointer items-center justify-center text-[#777777] opacity-[0.9] hover:text-[#222222]"
    >
      <X className="size-[13px]" />
    </span>
  )
}

export function TabBar({
  groups, activeGroupId, base, taskName, onActivateTab, onCloseTab, onNew, onNewLauncher,
  launchers = [], terminalUnavailable,
}: TabBarProps) {
  const activeGroup = groups.find((group) => group.id === activeGroupId) ?? groups[0]
  const activeTabId = activeGroup
    ? activeGroup.columns[activeGroup.focus[0]]?.panes[activeGroup.focus[1]]?.id ?? null
    : null

  const contentItems = (groupId: string): IconMenuItem[] => {
    if (base === null) return []
    return [
      ...pickItemsFor(base, terminalUnavailable).map((item) => ({
        key: item.kind, label: item.label, hotkey: item.hotkey,
        icon: <item.icon className="size-3.5 text-muted-foreground" />,
        onSelect: () => onNew(groupId, item.kind),
      })),
      ...launchersFor(launchers, terminalUnavailable).map((item) => ({
        key: `launcher:${item.name}`, label: item.name,
        icon: <span className="size-3.5 text-muted-foreground">⌁</span>,
        onSelect: () => onNewLauncher?.(groupId, item.name),
      })),
    ]
  }

  const rows = barTabs(groups)

  return (
    <div
      role="tablist"
      aria-label="工作区标签"
      className="flex h-11 min-w-0 shrink-0 items-stretch overflow-x-auto overflow-y-hidden bg-[#fafaf9] px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {rows.map((entry, index) => {
        const title = tabTitle(entry.tab.content, entry.tab.base.label, taskName)
        const active = entry.tab.id === activeTabId
        const isLast = index === rows.length - 1
        return (
          <Fragment key={entry.tab.id}>
            <button
              type="button"
              role="tab"
              aria-selected={active}
              // TUI 焦点保护：mousedown 默认会把焦点从 xterm 拉走，终端正在
              // 敲字时点一下 tab 焦点就丢了；preventDefault 让点击只切 tab 不动焦点
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => onActivateTab(entry.groupId, entry.tab.id)}
              className={cn(
                'inline-flex h-full min-w-0 shrink-0 items-center gap-[9px] whitespace-nowrap border-0 bg-transparent transition-colors outline-none hover:bg-[#efefee]',
                active
                  ? 'px-[18px] font-semibold text-[#2a2a2a]'
                  : 'px-[15px] font-[450] text-[#6f6f6f] hover:text-[#333333]',
              )}
            >
              {active ? (
                // 药丸面只属于激活 tab（原型 .workspace-tab-surface）
                <span
                  data-testid="tab-surface"
                  className="inline-flex h-[calc(100%-12px)] items-center gap-[9px] rounded-[10px] bg-[#f0f0ef] px-[9px]"
                >
                  <TabIconSlot tab={entry.tab} />
                  <span className="max-w-[230px] truncate">{title}</span>
                  <TabClose title={title} onClose={() => onCloseTab(entry.groupId, entry.tab.id)} />
                </span>
              ) : (
                <>
                  <TabIconSlot tab={entry.tab} />
                  <span className="max-w-[230px] truncate">{title}</span>
                  <TabClose title={title} onClose={() => onCloseTab(entry.groupId, entry.tab.id)} />
                </>
              )}
            </button>
            {/* 短节奏分隔线：1px 宽 20px 高、垂直居中（原型 ::after 的实体化）。
                行尾不渲染，+ 钮前留白 */}
            {!isLast && (
              <span data-testid="tab-sep" aria-hidden className="h-[20px] w-px shrink-0 self-center bg-[#e1e1e1]" />
            )}
          </Fragment>
        )
      })}
      <IconMenu
        label="新建内容"
        icon={<Plus className="size-4" />}
        className="flex w-[42px] shrink-0 items-center justify-center text-[#737373] hover:bg-[#f1f1f0] hover:text-[#2f2f2f]"
        items={contentItems(activeGroup?.id ?? '')}
      />
    </div>
  )
}
