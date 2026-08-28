// TabBar.tsx —— 顶层全局 group 栏。
//
// 职责：显示 group 名称、激活态、关闭与新建内容入口，并承接 group 拖放。
// 边界：pane 内没有 tab row；column 数量没有上限、每列最多两格的布局由 WorkbenchPage/tabs.ts 负责。
import { useState, type DragEvent } from 'react'
import { Plus, X } from 'lucide-react'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import { launchersFor, pickItemsFor, type LauncherItem, type PickKind } from './BlankTab'
import { DRAG_GROUP_MIME, readDragGroup } from './paneDrop'
import { type BaseDir, type TabGroup } from './tabs'
import { cn } from '@/lib/utils'

export interface TabBarProps {
  groups: TabGroup[]
  activeGroupId: string
  base: BaseDir | null
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

export function TabBar({
  groups, activeGroupId, base, onActivateGroup, onCloseGroup, onNew, onNewLauncher,
  launchers = [], terminalUnavailable, onNewGroup, onMoveGroup,
}: TabBarProps) {
  const [dropWarning, setDropWarning] = useState<string | null>(null)

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
    <div role="tablist" aria-label="标签组" className="flex min-h-9 min-w-0 items-stretch overflow-x-auto border-b bg-background">
      {groups.map((group) => {
        const active = group.id === activeGroupId
        return (
          <div
            key={group.id}
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
            className={cn('group flex shrink-0 items-center border-r', active && 'bg-muted/60')}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => onActivateGroup(group.id)}
              className="max-w-48 truncate px-3 py-1.5 text-[13px]"
            >
              {group.name}
            </button>
            <button
              type="button"
              aria-label={`关闭 ${group.name}`}
              onClick={() => onCloseGroup(group.id)}
              className="mr-1 rounded p-0.5 text-muted-foreground opacity-0 hover:bg-accent group-hover:opacity-100"
            >
              <X className="size-3.5" />
            </button>
            <IconMenu
              label="新建内容"
              icon={<Plus className="size-3.5" />}
              className="flex items-center px-1.5 text-muted-foreground hover:text-foreground"
              items={contentItems(group.id)}
            />
          </div>
        )
      })}
      <button type="button" aria-label="新建标签组" title="新建标签组" onClick={onNewGroup} className="flex items-center px-2 text-muted-foreground hover:text-foreground">
        <Plus className="size-4" />
      </button>
      {dropWarning && <p role="alert" className="self-center px-2 text-xs text-destructive">{dropWarning}</p>}
    </div>
  )
}
