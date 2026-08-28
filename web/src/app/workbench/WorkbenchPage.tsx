// WorkbenchPage.tsx —— 全局 group 下的列/窗格渲染与真实拖放接缝。
//
// 职责：顶层组栏、布局条、每列最多两格的 pane，以及 task/dir/tab 的 MIME 投放。
// 边界：内容由 renderContent 注入；布局迁移交给 WorkbenchApi/tabs.ts；不按 BaseDir 切换布局。
import { Fragment, useState, type ReactNode } from 'react'
import { BlankTab, type PickKind } from './BlankTab'
import { GroupDivider } from './GroupDivider'
import { TabBar } from './TabBar'
import { TaskPickerDialog } from './TaskPickerDialog'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME, dropZoneAt, readDragBase, readDragTab, type DropZone } from './paneDrop'
import { MAX_PANES_PER_COLUMN, MIN_PANE_PX, nextTerminalSeq, tabTitle, type BaseDir, type PaneTarget, type Tab, type TabContent } from './tabs'
import type { Launcher, ProjectTreeResp, Task } from '../../api/types'
import type { WorkbenchApi } from './useWorkbench'
import { createUntitledFile } from './newFile'
import { errorMessage } from '../lib/format'
import { cn } from '@/lib/utils'

export interface WorkbenchPageProps {
  api: WorkbenchApi
  onAddProject: () => void
  renderContent: (content: TabContent, base: BaseDir, groupId: string, tabId: string, active?: boolean) => ReactNode
  terminalUnavailable?: string
  onBeforeClose?: (content: TabContent, tabId: string, base: BaseDir) => boolean
  tree: ProjectTreeResp | null
  tasks: Task[]
  onFileCreated?: () => void
  launchers?: Launcher[]
}

function tabCount(group: { columns: Array<{ panes: Array<Tab | null> }> }): number {
  return group.columns.reduce((count, column) => count + column.panes.filter(Boolean).length, 0)
}

export function WorkbenchPage({
  api, onAddProject, renderContent, terminalUnavailable, onBeforeClose, tree, tasks, onFileCreated, launchers = [],
}: WorkbenchPageProps) {
  const { wb, base } = api
  const activeGroup = wb.groups.find((group) => group.id === wb.activeGroupId) ?? wb.groups[0]
  const [picking, setPicking] = useState<{ groupId: string; tabId: string | null } | null>(null)
  const [newFileError, setNewFileError] = useState('')
  const [dragOver, setDragOver] = useState<{ column: number; row: number; zone: DropZone } | null>(null)
  const [dropWarning, setDropWarning] = useState('')

  const launcherItems = launchers.map((launcher) => ({ name: launcher.name, envMissing: launcher.env_missing }))

  const pickForTab = (groupId: string, tab: Tab, kind: PickKind) => {
    if (kind === 'terminal') {
      if (!terminalUnavailable) api.setContent(groupId, tab.id, { kind: 'terminal', seq: nextTerminalSeq(wb) })
    } else if (kind === 'tui') {
      setPicking({ groupId, tabId: tab.id })
    } else if (base !== null) {
      setNewFileError('')
      void createUntitledFile(base).then((rel) => {
        api.setContent(groupId, tab.id, { kind: 'file', rel })
        onFileCreated?.()
      }).catch((error: unknown) => setNewFileError(errorMessage(error)))
    }
  }

  const openNew = (groupId: string, kind: PickKind) => {
    if (base === null) return
    if (kind === 'terminal') {
      if (!terminalUnavailable) api.openTerminal(base, groupId)
    } else if (kind === 'tui') {
      setPicking({ groupId, tabId: null })
    } else {
      setNewFileError('')
      void createUntitledFile(base).then((rel) => {
        api.open({ kind: 'file', rel }, base, groupId)
        onFileCreated?.()
      }).catch((error: unknown) => setNewFileError(errorMessage(error)))
    }
  }

  const openLauncher = (groupId: string, name: string) => {
    if (base !== null && !terminalUnavailable) api.open({ kind: 'terminal', seq: nextTerminalSeq(wb), launcher: name }, base, groupId)
  }

  const closeTab = (groupId: string, tab: Tab) => {
    if (onBeforeClose && !onBeforeClose(tab.content, tab.id, tab.base)) {
      console.warn('workbench.close.rejected', { groupId, tabId: tab.id, baseKey: tab.base.key })
      return
    }
    api.close(groupId, tab.id)
  }

  const placeFromDrop = (event: React.DragEvent<HTMLElement>, groupId: string, column: number, row: number) => {
    const types = event.dataTransfer.types
    const ours = types.includes(DRAG_TASK_MIME) || types.includes(DRAG_DIR_MIME) || types.includes(DRAG_TAB_MIME)
    if (!ours) return
    event.preventDefault()
    const rect = event.currentTarget.getBoundingClientRect()
    const targetGroup = wb.groups.find((group) => group.id === groupId) ?? activeGroup
    const targetColumn = targetGroup.columns[column]
    const canAddPane = targetColumn !== undefined && targetColumn.panes.length < MAX_PANES_PER_COLUMN
    const requestedZone = dropZoneAt(event.clientX - rect.left, event.clientY - rect.top, rect.width, rect.height, true, true)
    const zone = dropZoneAt(event.clientX - rect.left, event.clientY - rect.top, rect.width, rect.height, true, canAddPane)
    const target: PaneTarget = {
      groupId, column, row,
      zone,
    }
    setDragOver(null)
    setDropWarning('')
    if ((requestedZone === 'top' || requestedZone === 'bottom') && !canAddPane) {
      setDropWarning('这一列最多两格，已替换当前窗格')
      console.warn('workbench.drop.pane_limit', { groupId, column, row, zone: requestedZone })
    }
    if (types.includes(DRAG_TAB_MIME)) {
      const source = readDragTab(event.dataTransfer.getData(DRAG_TAB_MIME))
      if (!source) {
        console.warn('workbench.drop.invalid_mime', { groupId, column, row, zone: target.zone })
        return
      }
      api.place({ kind: 'tab', ...source }, target)
      console.debug('workbench.drop.tab', { groupId, column, row, zone: target.zone, tabId: source.tabId })
      return
    }
    const taskId = types.includes(DRAG_TASK_MIME) ? event.dataTransfer.getData(DRAG_TASK_MIME) : ''
    const draggedBase = readDragBase(event.dataTransfer.getData(DRAG_DIR_MIME)) ?? readDragBase(event.dataTransfer.getData(DRAG_BASE_MIME)) ?? base
    if (draggedBase === null || (types.includes(DRAG_TASK_MIME) && taskId === '')) {
      console.warn('workbench.drop.invalid_source', { groupId, column, row, zone: target.zone })
      return
    }
    const content: TabContent = types.includes(DRAG_TASK_MIME)
      ? { kind: 'tui', taskId }
      : { kind: 'terminal', seq: nextTerminalSeq(wb) }
    api.place({ kind: 'new', base: draggedBase, content }, target)
    console.debug('workbench.drop.new', { groupId, column, row, zone: target.zone, baseKey: draggedBase.key })
  }

  const moveGroup = (sourceGroupId: string, targetGroupId: string, zone: 'left' | 'right' | 'center') => {
    const sourceGroup = wb.groups.find((group) => group.id === sourceGroupId)
    const targetGroup = wb.groups.find((group) => group.id === targetGroupId)
    if (!sourceGroup || !targetGroup || tabCount(sourceGroup) !== 1) {
      setDropWarning('多窗格标签组不能整体移动，请拖动窗格标题')
      return
    }
    const source = sourceGroup.columns.flatMap((column) => column.panes).find((tab): tab is Tab => tab !== null)
    if (!source) return
    const column = zone === 'left' ? 0 : zone === 'right' ? targetGroup.columns.length - 1 : targetGroup.focus[0]
    const target: PaneTarget = { groupId: targetGroupId, column, row: targetGroup.focus[1], zone }
    api.place({ kind: 'tab', groupId: sourceGroupId, tabId: source.id }, target)
    console.debug('workbench.drop.group', { groupId: targetGroupId, tabId: source.id, zone })
  }

  const renderTab = (groupId: string, column: number, row: number, tab: Tab | null) => {
    if (tab === null) {
      if (base === null) return <div className="flex h-full items-center justify-center p-4 text-sm text-muted-foreground">请从左栏选择项目或目录</div>
      return <BlankTab base={base} onPick={(kind) => openNew(groupId, kind)} launchers={launcherItems} onPickLauncher={(name) => openLauncher(groupId, name)} terminalUnavailable={terminalUnavailable} />
    }
    const active = wb.activeGroupId === groupId && activeGroup.focus[0] === column && activeGroup.focus[1] === row
    if (tab.content.kind === 'blank') {
      return <BlankTab base={tab.base} onPick={(kind) => pickForTab(groupId, tab, kind)} launchers={launcherItems} onPickLauncher={(name) => api.setContent(groupId, tab.id, { kind: 'terminal', seq: nextTerminalSeq(wb), launcher: name })} terminalUnavailable={terminalUnavailable} />
    }
    return renderContent(tab.content, tab.base, groupId, tab.id, active)
  }

  const renderGroup = (group: typeof activeGroup, visible: boolean) => (
    <div className={cn('flex min-h-0 flex-1 flex-col', !visible && 'pointer-events-none absolute -left-[10000px] top-0 h-full w-full')} aria-hidden={!visible}>
      <div className="flex min-h-0 flex-1 overflow-x-auto bg-border">
        {group.columns.map((column, columnIndex) => (
          <Fragment key={`${group.id}-column-${columnIndex}`}>
          <div className="flex min-w-[240px] min-h-0 flex-1 flex-col" style={{ flexGrow: group.sizes[columnIndex] ?? 1, flexBasis: 0 }}>
            {column.panes.map((tab, row) => (
              <div
                key={tab?.id ?? `${group.id}-${columnIndex}-${row}`}
                data-testid="workbench-pane"
                className="relative flex min-h-0 flex-1 flex-col bg-background"
                onDragOver={(event) => {
                  const types = event.dataTransfer.types
                  if (!types.includes(DRAG_TASK_MIME) && !types.includes(DRAG_DIR_MIME) && !types.includes(DRAG_TAB_MIME)) return
                  event.preventDefault()
                  const rect = event.currentTarget.getBoundingClientRect()
                  setDragOver({ column: columnIndex, row, zone: dropZoneAt(event.clientX - rect.left, event.clientY - rect.top, rect.width, rect.height, true, column.panes.length < MAX_PANES_PER_COLUMN) })
                }}
                onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver(null) }}
                onDrop={(event) => placeFromDrop(event, group.id, columnIndex, row)}
              >
                {dragOver?.column === columnIndex && dragOver.row === row && dragOver.zone !== 'center' && <span aria-hidden="true" className={cn('pointer-events-none absolute z-20 bg-primary', dragOver.zone === 'left' && 'inset-y-0 left-0 w-1', dragOver.zone === 'right' && 'inset-y-0 right-0 w-1', dragOver.zone === 'top' && 'inset-x-0 top-0 h-1', dragOver.zone === 'bottom' && 'inset-x-0 bottom-0 h-1')} />}
                <div className="flex min-h-8 shrink-0 items-center gap-2 border-b px-2 text-xs">
                  <div
                    draggable={tab !== null}
                    onDragStart={(event) => {
                      if (!tab) return
                      event.dataTransfer.setData(DRAG_TAB_MIME, JSON.stringify({ groupId: group.id, tabId: tab.id }))
                      event.dataTransfer.effectAllowed = 'move'
                    }}
                    className="min-w-0 flex-1 truncate"
                  >
                    {tab ? tabTitle(tab.content, tab.base.label) : '空窗格'}
                    {tab?.base.projectName && <span className="ml-2 text-muted-foreground">{tab.base.projectName}{tab.base.machine ? ` · ${tab.base.machine}` : ''}</span>}
                  </div>
                  {tab && <button type="button" aria-label={`关闭 ${tabTitle(tab.content, tab.base.label)}`} onClick={() => closeTab(group.id, tab)} className="rounded p-0.5 text-muted-foreground hover:bg-accent">×</button>}
                </div>
                <div className="min-h-0 flex-1 overflow-hidden">{renderTab(group.id, columnIndex, row, tab)}</div>
              </div>
            ))}
          </div>
          {visible && columnIndex < group.columns.length - 1 && <GroupDivider onResize={(delta, width) => api.resize(group.id, columnIndex, delta, width > 0 ? MIN_PANE_PX / width : 0)} />}
          </Fragment>
        ))}
      </div>
      <div className="hidden" />
    </div>
  )

  return (
    <div className="relative flex h-full min-h-0 flex-col bg-border">
      <TabBar
        groups={wb.groups}
        activeGroupId={wb.activeGroupId}
        base={base}
        onActivateGroup={api.activateGroup}
        onCloseGroup={api.closeGroup}
        onNew={openNew}
        onNewLauncher={openLauncher}
        launchers={launcherItems}
        terminalUnavailable={terminalUnavailable}
        onNewGroup={api.addGroup}
        onMoveGroup={moveGroup}
      />
      {dropWarning !== '' && <p role="alert" className="bg-destructive/10 px-3 py-1 text-xs text-destructive">{dropWarning}</p>}
      {newFileError !== '' && <p role="alert" className="bg-destructive/10 px-3 py-1 text-xs text-destructive">新建文件失败：{newFileError}</p>}
      <div className="flex min-h-8 shrink-0 items-center border-b bg-background px-2">
        <button type="button" aria-label="增加分屏" onClick={() => api.splitColumn(activeGroup.id)} className="text-xs text-muted-foreground hover:text-foreground">＋分屏</button>
      </div>
      <div className="relative flex min-h-0 flex-1">
        {wb.groups.map((group) => (
          <Fragment key={group.id}>
            {group.id === wb.activeGroupId && renderGroup(group, true)}
            {group.id !== wb.activeGroupId && group.columns.some((column) => column.panes.some((tab) => tab?.content.kind === 'terminal')) && renderGroup(group, false)}
          </Fragment>
        ))}
      </div>
      {picking !== null && base !== null && <TaskPickerDialog
        open base={base} tree={tree} tasks={tasks}
        onPick={(taskId) => {
          const content: TabContent = { kind: 'tui', taskId }
          if (picking.tabId === null) api.open(content, base, picking.groupId)
          else api.setContent(picking.groupId, picking.tabId, content)
          setPicking(null)
        }}
        onClose={() => setPicking(null)}
      />}
      <button type="button" className="sr-only" onClick={onAddProject}>添加项目</button>
    </div>
  )
}
