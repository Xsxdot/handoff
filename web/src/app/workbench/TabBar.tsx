// TabBar.tsx —— 顶部组标签条（tab = 组，基线语义，option-1 chrome 换皮）。
//
// 形态真源：prototypes/base/pages/project-tree-option-1.html 的
// .workspace-tabbar / .workspace-tab / .workspace-tab-surface / .workspace-tab-close
//（样式数值一比一对照移植；新建入口不放在标签栏内）。
//
// 职责：显示组标签（autoName 组显示焦点内容名，显式命名组显示组名）、激活态
// 药丸面、组间短节奏分隔线、每组关闭、组拖动（DRAG_GROUP_MIME）。
// 边界：布局迁移（激活/关闭/移动/新建）全部经回调交给 WorkbenchPage/tabs.ts；
// 本组件不持有布局状态（dropWarning 告警文案除外）。pane 内没有 tab row；
// 每列最多两格的布局约束由 WorkbenchPage/tabs.ts 负责。
import { Fragment, useState, type DragEvent } from 'react'
import { FileText, Plus, Terminal, X } from 'lucide-react'
import dispatchTaskUrl from '../../assets/dispatch-task.png'
import { launchersFor, pickItemsFor, type LauncherItem, type PickKind } from './BlankTab'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import { DRAG_GROUP_MIME, readDragGroup } from './paneDrop'
import { tabTitle, type BaseDir, type Tab, type TabContent, type TabGroup } from './tabs'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  groups: TabGroup[]
  activeGroupId: string
  base: BaseDir | null
  // taskName 把 tui 的 taskId 解析成任务原名（autoName 组的焦点 tui 显示原名）；
  // 解析不到（任务已删除等）时 tabTitle 自己回退 TUI · 前 8 位，所以可缺席
  taskName?: (taskId: string) => string | undefined
  onActivateGroup: (groupId: string) => void
  onCloseGroup: (groupId: string) => void
  onNew: (groupId: string, kind: PickKind) => void
  onNewLauncher?: (groupId: string, name: string) => void
  launchers?: LauncherItem[]
  terminalUnavailable?: string
  onNewGroup: () => void
  onMoveGroup: (sourceGroupId: string, targetGroupId: string, zone: 'left' | 'right' | 'center') => void
}

function groupContentCount(group: TabGroup): number {
  return group.columns.reduce((count, column) => count + column.panes.filter((pane) => pane !== null).length, 0)
}

function groupDropZone(event: DragEvent<HTMLElement>): 'left' | 'right' | 'center' {
  const rect = event.currentTarget.getBoundingClientRect()
  if (rect.width <= 0) return 'center'
  if (event.clientX - rect.left < rect.width * 0.28) return 'left'
  if (event.clientX - rect.left > rect.width * 0.72) return 'right'
  return 'center'
}

// focusedPaneOf 取组焦点格里的 tab（图标与 autoName 组名的来源）。
function focusedPaneOf(group: TabGroup): Tab | null {
  const [column, row] = group.focus
  return group.columns[column]?.panes[row] ?? null
}

// groupLabel 是组标签的显示名。基线语义：显式命名的组显示组名；autoName 组
// 一旦有焦点内容就显示焦点内容名（tui=任务原名，修复 B288 问题 1 的 tab 条
// 落点）——显示层推导，不改布局模型，持久化的组名不受影响。
function groupLabel(group: TabGroup, focused: Tab | null, taskName?: (taskId: string) => string | undefined): string {
  if (focused && group.autoName) return tabTitle(focused.content, focused.base.label, taskName)
  return group.name
}

// TabTypeIcon 按焦点内容种类映射图标：tui 用 dispatch-task 资产图标（与原型的
// 图标映射一致），terminal/file 用同形线性图标，空组/空白仍用 + 作为内容类型提示。
function TabTypeIcon({ content }: { content: TabContent | null }) {
  switch (content?.kind) {
    case 'tui': return <img src={dispatchTaskUrl} className="size-[15px]" alt="" />
    case 'terminal': return <Terminal className="size-[15px]" />
    case 'file': return <FileText className="size-[15px]" />
    default: return <Plus className="size-[15px]" />
  }
}

// TabIconSlot 对应原型的 15px 图标槽位（.workspace-tab .tab-icon）。
function TabIconSlot({ content }: { content: TabContent | null }) {
  return (
    <span aria-hidden className="inline-flex h-[15px] w-[15px] shrink-0 items-center justify-center opacity-[0.85]">
      <TabTypeIcon content={content} />
    </span>
  )
}

// TabClose 对应原型的 .workspace-tab-close：15px 槽位、13px 图标、#777 → hover #222。
// 关闭按钮放在标签的状态面内，保证选中态与 hover 态的视觉边界完整包住 x。
function TabClose({ label, onClose }: { label: string; onClose: () => void }) {
  return (
    <button
      type="button"
      aria-label={'关闭 ' + label}
      onClick={onClose}
      className="inline-flex h-[15px] w-[15px] shrink-0 self-center border-0 bg-transparent p-0 text-[#777777] opacity-[0.9] hover:text-[#222222]"
    >
      <X className="size-[13px]" />
    </button>
  )
}

export function TabBar({
  groups, activeGroupId, base, taskName, onActivateGroup, onCloseGroup, onNew, onNewLauncher,
  launchers = [], terminalUnavailable, onNewGroup, onMoveGroup,
}: TabBarProps) {
  const [dropWarning, setDropWarning] = useState<string | null>(null)

  // 新建入口从视觉上移出标签栏，但保留原有动作菜单的无障碍/快捷入口，避免
  // 仅做 chrome 换皮时把「在当前组继续开终端/文件」的能力一并删掉。
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

  return (
    <div
      role="tablist"
      aria-label="标签组"
      className="flex h-11 min-w-0 shrink-0 items-stretch overflow-x-auto overflow-y-hidden bg-[#fafaf9] px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {groups.map((group, index) => {
        const active = group.id === activeGroupId
        const focused = focusedPaneOf(group)
        const label = groupLabel(group, focused, taskName)
        const isLast = index === groups.length - 1
        return (
          <Fragment key={group.id}>
            <div
              role="presentation"
              draggable
              onDragStart={(event) => {
                event.dataTransfer.setData(DRAG_GROUP_MIME, JSON.stringify({ groupId: group.id }))
                event.dataTransfer.effectAllowed = 'move'
              }}
              onDragOver={(event) => {
                if (!event.dataTransfer.types.includes(DRAG_GROUP_MIME)) return
                event.preventDefault()
                event.dataTransfer.dropEffect = 'move'
              }}
              onDrop={(event) => {
                if (!event.dataTransfer.types.includes(DRAG_GROUP_MIME)) return
                event.preventDefault()
                const source = readDragGroup(event.dataTransfer.getData(DRAG_GROUP_MIME))
                if (!source || source.groupId === group.id) return
                if (groupContentCount(groups.find((item) => item.id === source.groupId) ?? group) !== 1) {
                  setDropWarning('多窗格标签组不能整体移动，请拖动窗格标题')
                  return
                }
                setDropWarning(null)
                onMoveGroup(source.groupId, group.id, groupDropZone(event))
              }}
              className="group flex shrink-0 items-stretch px-1"
            >
              <div
                role="tab"
                aria-selected={active}
                aria-label={label}
                tabIndex={active ? 0 : -1}
                // TUI 焦点保护：mousedown 默认会把焦点从 xterm 拉走，终端正在
                // 敲字时点一下标签焦点就丢了；preventDefault 让点击只切组不动焦点
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onActivateGroup(group.id)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    onActivateGroup(group.id)
                  }
                }}
                className={cn(
                  'inline-flex h-full min-w-0 shrink-0 cursor-pointer items-center whitespace-nowrap text-[14px] outline-none transition-[color] duration-[140ms]',
                  active ? 'font-semibold text-[#2a2a2a]' : 'font-[450] text-[#6f6f6f] hover:text-[#333333]',
                )}
              >
                {/* 选中态与 hover 态共用同一状态面；x 也在面内，避免视觉上像另一个控件。 */}
                <span
                  data-testid="tab-surface"
                  data-active={active ? 'true' : 'false'}
                  className={cn(
                    'inline-flex h-[calc(100%-12px)] min-w-0 items-center gap-[9px] rounded-[10px] px-[9px] transition-colors duration-[140ms]',
                    active ? 'bg-[#f0f0ef]' : 'bg-transparent group-hover:bg-[#f7f7f6]',
                  )}
                >
                  <TabIconSlot content={focused?.content ?? null} />
                  <span className="max-w-[230px] truncate">{label}</span>
                  <TabClose label={label} onClose={() => onCloseGroup(group.id)} />
                  <IconMenu
                    label="新建内容"
                    icon={<Plus className="size-3.5" />}
                    className="sr-only"
                    items={contentItems(group.id)}
                  />
                </span>
              </div>
            </div>
            {/* 短节奏分隔线：与状态面留出呼吸距离；行尾不再渲染 +。 */}
            {!isLast && (
              <span data-testid="tab-sep" aria-hidden className="mx-[7px] h-[20px] w-px shrink-0 self-center bg-[#e1e1e1]" />
            )}
          </Fragment>
        )
      })}
      {/* 视觉稿不显示行尾 +，保留语义入口以兼容键盘/辅助技术用户。 */}
      <button type="button" aria-label="新建标签组" title="新建标签组" onClick={onNewGroup} className="sr-only">
        <Plus className="size-4" />
      </button>
      {dropWarning && <p role="alert" className="self-center px-2 text-xs text-destructive">{dropWarning}</p>}
    </div>
  )
}
