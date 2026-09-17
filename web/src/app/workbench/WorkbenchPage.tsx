// WorkbenchPage.tsx —— 全局 group 下的列/窗格渲染与真实拖放接缝。
//
// 职责：顶部标签栏（TabBar）、每列最多两格的 pane，以及 task/dir/tab 的 MIME 投放。
// 边界：内容由 renderContent 注入；布局迁移交给 WorkbenchApi/tabs.ts；不按 BaseDir 切换布局。
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { BlankTab, type PickKind } from './BlankTab'
import { GroupDivider } from './GroupDivider'
import { TabBar } from './TabBar'
import { TaskPickerDialog } from './TaskPickerDialog'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_SESSION_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME, dropZoneAt, readDragBase, readDragSession, readDragTab, type DropZone } from './paneDrop'
import { MAX_PANES_PER_COLUMN, MIN_PANE_PX, nextTerminalSeq, spawnTerminalContent, tabTitle, type BaseDir, type PaneTarget, type Tab, type TabContent } from './tabs'
import type { Launcher, ProjectTreeResp, Task } from '../../api/types'
import type { WorkbenchApi } from './useWorkbench'
import { sessionBase } from './useWorkbench'
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
  // taskName 把 tui 的 taskId 解析成任务原名，由持有任务流的 Shell 构建
  // 下传（单一口径：标签条、窗格标题、面包屑共用）；省略时 tabTitle 自己回退。
  taskName?: (taskId: string) => string | undefined
  // B358.8：固定「◫ 分屏」按钮退役——分屏由左栏会话行拖拽承载（同 spec #1）。
}

type DragOver = {
  groupId: string
  column: number
  row: number
  zone: DropZone
}

function tabCount(group: { columns: Array<{ panes: Array<Tab | null> }> }): number {
  return group.columns.reduce((count, column) => count + column.panes.filter(Boolean).length, 0)
}

export function WorkbenchPage({
  api, onAddProject, renderContent, terminalUnavailable, onBeforeClose, tree, tasks, onFileCreated, launchers = [], taskName,
}: WorkbenchPageProps) {
  const { wb, base } = api
  const activeGroup = wb.groups.find((group) => group.id === wb.activeGroupId) ?? wb.groups[0]
  const [picking, setPicking] = useState<{ groupId: string; tabId: string | null } | null>(null)
  const [newFileError, setNewFileError] = useState('')
  const [dragOver, setDragOver] = useState<DragOver | null>(null)
  const [dropWarning, setDropWarning] = useState('')

  // dragging 标记「有左栏内容（任务/会话）正在被拖」。拖动期间每个窗格的内容层
  // pointer-events 关闭（原型 body.dragging .pane-body 规则）：xterm 的 canvas 会吃掉
  // dragover，落点预览根本出不来；从内容层放行才能保证落点指示在黑底终端上也稳定出现。
  // dragstart 只认带 data-drag-task / data-drag-session 的来源；dragend 与 drop
  // 双保险复位，防泄漏。
  const [dragging, setDragging] = useState(false)
  useEffect(() => {
    const onDragStart = (event: DragEvent) => {
      if ((event.target as HTMLElement | null)?.closest?.('[data-drag-task], [data-drag-session]')) setDragging(true)
    }
    const reset = () => setDragging(false)
    window.addEventListener('dragstart', onDragStart)
    window.addEventListener('dragend', reset)
    window.addEventListener('drop', reset)
    return () => {
      window.removeEventListener('dragstart', onDragStart)
      window.removeEventListener('dragend', reset)
      window.removeEventListener('drop', reset)
    }
  }, [])

  const dropContext = (source: BaseDir | null | undefined = base) => ({
    project: source?.projectName ?? '',
    machine: source?.machine ?? '',
    path: source?.path ?? '',
  })

  const launcherItems = launchers.map((launcher) => ({ name: launcher.name, envMissing: launcher.env_missing }))

  const pickForTab = (groupId: string, tab: Tab, kind: PickKind) => {
    if (kind === 'terminal') {
      if (!terminalUnavailable) api.setContent(groupId, tab.id, spawnTerminalContent(nextTerminalSeq(wb)))
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
    if (base !== null && !terminalUnavailable) api.open(spawnTerminalContent(nextTerminalSeq(wb), { launcher: name }), base, groupId)
  }

  const closeTab = (groupId: string, tab: Tab) => {
    if (onBeforeClose && !onBeforeClose(tab.content, tab.id, tab.base)) {
      console.warn('workbench.close.rejected', { groupId, tabId: tab.id, baseKey: tab.base.key })
      return
    }
    api.close(groupId, tab.id)
  }

  const dropTargetAt = (event: React.DragEvent<HTMLElement>, groupId: string, column: number, row: number): {
    target: PaneTarget
    requestedZone: DropZone
    canAddPane: boolean
  } | null => {
    const targetGroup = wb.groups.find((group) => group.id === groupId)
    const targetColumn = targetGroup?.columns[column]
    if (!targetGroup || !targetColumn) return null
    const rect = event.currentTarget.getBoundingClientRect()
    const offsetX = event.clientX - rect.left
    const offsetY = event.clientY - rect.top
    const canAddPane = targetColumn.panes.length < MAX_PANES_PER_COLUMN
    const requestedZone = dropZoneAt(offsetX, offsetY, rect.width, rect.height, true, true)
    const zone = dropZoneAt(offsetX, offsetY, rect.width, rect.height, true, canAddPane)
    return { target: { groupId, column, row, zone }, requestedZone, canAddPane }
  }

  // findSessionTab 全局找已开的会话 tab（去重键 session:<id>）。placeSource 的
  // kind:'new' 不查重，会话投放必须先在这里查——tabs.ts「一会话一 tab」不变式
  // 由 drop 分支前置保证（B358.8 #1）。
  const findSessionTab = (sessionId: string): { groupId: string; tabId: string } | null => {
    for (const group of wb.groups) {
      for (const column of group.columns) {
        for (const tab of column.panes) {
          if (tab?.content.kind === 'session' && tab.content.sessionId === sessionId) {
            return { groupId: group.id, tabId: tab.id }
          }
        }
      }
    }
    return null
  }

  // dropSessionIntoGroup 是组标签接收会话投放的落点：组内去重激活、他组移动
  // （复用 openTab 的选格逻辑——先填空格、无空格在末列右侧追列）、未开才新开。
  const dropSessionIntoGroup = (source: { sessionId: string; title: string }, groupId: string) => {
    const existing = findSessionTab(source.sessionId)
    if (existing !== null) {
      if (existing.groupId === groupId) {
        api.activate(groupId, existing.tabId)
      } else {
        const targetGroup = wb.groups.find((group) => group.id === groupId)
        const emptyAt = targetGroup?.columns
          .flatMap((column, index) => column.panes.map((tab, row) => ({ tab, column: index, row })))
          .find((slot) => slot.tab === null)
        const target: PaneTarget = emptyAt
          ? { groupId, column: emptyAt.column, row: emptyAt.row, zone: 'center' }
          : { groupId, column: (targetGroup?.columns.length ?? 1) - 1, row: 0, zone: 'right' }
        api.place({ kind: 'tab', ...existing }, target)
      }
      console.debug('workbench.drop.session_group_dedup', { sessionId: source.sessionId, groupId })
      return
    }
    api.open({ kind: 'session', sessionId: source.sessionId, title: source.title }, sessionBase(source.sessionId), groupId)
    console.debug('workbench.drop.session_group', { sessionId: source.sessionId, groupId })
  }

  const placeFromDrop = (event: React.DragEvent<HTMLElement>, groupId: string, column: number, row: number) => {
    const types = event.dataTransfer.types
    const ours = types.includes(DRAG_TASK_MIME) || types.includes(DRAG_DIR_MIME) || types.includes(DRAG_TAB_MIME) || types.includes(DRAG_SESSION_MIME)
    setDragOver(null)
    if (!ours) return
    event.preventDefault()
    const calculation = dropTargetAt(event, groupId, column, row)
    if (calculation === null) {
      console.warn('workbench.drop.invalid_target', { ...dropContext(), groupId, column, row, zone: 'center', reason: 'pane target disappeared before drop' })
      return
    }
    const { target, requestedZone, canAddPane } = calculation
    setDropWarning('')
    // 半区预览必须覆盖真实落点，列满时上下区才退化为 center 替换，因为没有第三格可插入。
    if ((requestedZone === 'top' || requestedZone === 'bottom') && !canAddPane) {
      setDropWarning('这一列最多两格，已替换当前窗格')
      console.warn('workbench.drop.pane_limit', {
        ...dropContext(), groupId, column, row, zone: requestedZone, reason: 'column already has two panes',
      })
    }
    if (types.includes(DRAG_SESSION_MIME)) {
      const source = readDragSession(event.dataTransfer.getData(DRAG_SESSION_MIME))
      if (!source) {
        console.warn('workbench.drop.invalid_mime', {
          ...dropContext(), groupId, column, row, zone: target.zone, reason: 'session MIME payload is missing or invalid',
        })
        return
      }
      const existing = findSessionTab(source.sessionId)
      if (existing !== null) {
        // 已开在本组只激活；开在别的组整支移动过去（一会话一 tab，不复制）。
        if (existing.groupId === groupId) api.activate(groupId, existing.tabId)
        else api.place({ kind: 'tab', ...existing }, target)
        console.debug('workbench.drop.session_dedup', { sessionId: source.sessionId, groupId, column, row, zone: target.zone })
        return
      }
      const content: TabContent = { kind: 'session', sessionId: source.sessionId, title: source.title }
      api.place({ kind: 'new', base: sessionBase(source.sessionId), content }, target)
      console.debug('workbench.drop.session', { sessionId: source.sessionId, groupId, column, row, zone: target.zone })
      return
    }
    if (types.includes(DRAG_TAB_MIME)) {
      const source = readDragTab(event.dataTransfer.getData(DRAG_TAB_MIME))
      if (!source) {
        console.warn('workbench.drop.invalid_mime', {
          ...dropContext(), groupId, column, row, zone: target.zone, reason: 'tab MIME payload is missing or invalid',
        })
        return
      }
      api.place({ kind: 'tab', ...source }, target)
      console.debug('workbench.drop.tab', { groupId, column, row, zone: target.zone, tabId: source.tabId })
      return
    }
    const taskId = types.includes(DRAG_TASK_MIME) ? event.dataTransfer.getData(DRAG_TASK_MIME) : ''
    const hasDirectoryMime = types.includes(DRAG_DIR_MIME)
    const hasBaseMime = types.includes(DRAG_BASE_MIME)
    const directoryBase = hasDirectoryMime ? readDragBase(event.dataTransfer.getData(DRAG_DIR_MIME)) : null
    const taskBase = hasBaseMime ? readDragBase(event.dataTransfer.getData(DRAG_BASE_MIME)) : null
    const draggedBase = hasDirectoryMime
      ? directoryBase
      : taskBase
    const invalidTaskBase = types.includes(DRAG_TASK_MIME) && (!hasBaseMime || taskBase === null)
    const invalidDirectoryBase = hasDirectoryMime && directoryBase === null
    if (invalidTaskBase || invalidDirectoryBase || draggedBase === null || (types.includes(DRAG_TASK_MIME) && taskId === '')) {
      console.warn('workbench.drop.invalid_source', {
        ...dropContext(draggedBase ?? base),
        groupId,
        column,
        row,
        zone: target.zone,
        reason: invalidDirectoryBase
          ? 'directory MIME payload is missing or invalid'
          : invalidTaskBase
            ? 'task/base MIME payload is missing or invalid'
            : draggedBase === null
              ? 'directory/base MIME payload is missing or invalid'
          : 'task MIME payload has no task id',
      })
      return
    }
    const content: TabContent = types.includes(DRAG_TASK_MIME)
      ? { kind: 'tui', taskId }
      : spawnTerminalContent(nextTerminalSeq(wb))
    api.place({ kind: 'new', base: draggedBase, content }, target)
    console.debug('workbench.drop.new', {
      ...dropContext(draggedBase), groupId, column, row, zone: target.zone, baseKey: draggedBase.key,
    })
  }

  // moveGroup 把整组拖放投影成 place：只允许单窗格组整体移动（多窗格由 TabBar
  // 的告警拦下），落点 zone 决定它并到目标组的哪一列。
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
      return <BlankTab base={tab.base} onPick={(kind) => pickForTab(groupId, tab, kind)} launchers={launcherItems} onPickLauncher={(name) => api.setContent(groupId, tab.id, spawnTerminalContent(nextTerminalSeq(wb), { launcher: name }))} terminalUnavailable={terminalUnavailable} />
    }
    return renderContent(tab.content, tab.base, groupId, tab.id, active)
  }

  const renderGroup = (group: typeof activeGroup, visible: boolean) => (
    // 后台组叠在原位（inset-0），不移出视口、不 opacity-0（那些会弄死 WebGL）。
    // 但必须 pointer-events-none + inert：z-0 的 WebGL 画布会从激活组抢走滚轮，
    // 眼前那条 TUI 就划不动。激活组从未加过这个类，不走「去掉后命中回不来」。
    // min-w-0 切断设置往返的 min-content 撑越。
    <div
      data-testid="workbench-group"
      className={cn(
        'absolute inset-0 flex min-h-0 min-w-0 flex-col bg-background',
        visible ? 'z-10' : 'z-0 pointer-events-none',
      )}
      aria-hidden={!visible}
      {...(!visible ? { inert: true } : {})}
    >
      {/* 原型 .cols { overflow: hidden }：列压进容器，不出现横向滚动 */}
      <div className="flex min-h-0 flex-1 overflow-hidden bg-border">
        {group.columns.map((column, columnIndex) => (
          <Fragment key={`${group.id}-column-${columnIndex}`}>
          {/* min-w-0 而非 240px 硬下限：列宽下限由拖拽分隔的 minRatio 夹紧负责，
              硬下限会把三列顶出窗口（容器 overflow-hidden 后变成裁切不可达） */}
          <div className="flex min-w-0 min-h-0 flex-1 flex-col" style={{ flexGrow: group.sizes[columnIndex] ?? 1, flexBasis: 0 }}>
            {column.panes.map((tab, row) => (
              <div
                key={tab?.id ?? `${group.id}-${columnIndex}-${row}`}
                data-testid="workbench-pane"
                className="relative flex min-h-0 flex-1 flex-col bg-background"
                onDragOver={(event) => {
                  const types = event.dataTransfer.types
                  if (!types.includes(DRAG_TASK_MIME) && !types.includes(DRAG_DIR_MIME) && !types.includes(DRAG_TAB_MIME) && !types.includes(DRAG_SESSION_MIME)) return
                  event.preventDefault()
                  const calculation = dropTargetAt(event, group.id, columnIndex, row)
                  if (calculation === null) return
                  setDragOver({ ...calculation.target })
                }}
                onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver(null) }}
                onDrop={(event) => placeFromDrop(event, group.id, columnIndex, row)}
                onClick={() => {
                  if (tab === null) api.activateGroup(group.id)
                  else api.activate(group.id, tab.id)
                }}
              >
                {dragOver?.groupId === group.id && dragOver.column === columnIndex && dragOver.row === row && (
                  <span
                    data-testid={`drop-${dragOver.zone}`}
                    data-zone={dragOver.zone}
                    aria-hidden="true"
                    className={cn(
                      'pointer-events-none absolute z-20',
                      // 五区数值 1:1 对照 prototypes/b288-workbench-ux 的
                      // .pane.drop-{left,right,top,bottom,center}::after 规则
                      dragOver.zone === 'left' && 'inset-y-0 left-0 w-1/2 bg-[rgba(37,99,235,0.32)] shadow-[inset_4px_0_0_#2563eb]',
                      dragOver.zone === 'right' && 'inset-y-0 right-0 w-1/2 bg-[rgba(37,99,235,0.32)] shadow-[inset_-4px_0_0_#2563eb]',
                      dragOver.zone === 'top' && 'inset-x-0 top-0 h-1/2 bg-[rgba(37,99,235,0.32)] shadow-[inset_0_4px_0_#2563eb]',
                      dragOver.zone === 'bottom' && 'inset-x-0 bottom-0 h-1/2 bg-[rgba(37,99,235,0.32)] shadow-[inset_0_-4px_0_#2563eb]',
                      dragOver.zone === 'center' && 'inset-[18%] bg-[rgba(37,99,235,0.18)] outline outline-2 outline-[#2563eb]',
                      // 黑底终端窗格（原型 .pane.pane-term.drop-*）：遮罩换浅一档的蓝，
                      // 否则落点指示在 xterm 上对比不足——这正是问题 2 的主场景
                      tab?.content.kind === 'terminal' && 'bg-[rgba(147,197,253,0.5)]',
                    )}
                  />
                )}
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
                    {tab ? tabTitle(tab.content, tab.base.label, taskName) : '空窗格'}
                    {tab?.base.projectName && <span className="ml-2 text-muted-foreground">{tab.base.projectName}{tab.base.machine ? ` · ${tab.base.machine}` : ''}</span>}
                  </div>
                  <button
                    type="button"
                    aria-label={`关闭 ${tab ? tabTitle(tab.content, tab.base.label, taskName) : '空窗格'}`}
                    onClick={(event) => {
                      event.stopPropagation()
                      if (tab) closeTab(group.id, tab)
                      else api.closePane(group.id, columnIndex, row)
                    }}
                    className="rounded p-0.5 text-muted-foreground hover:bg-accent"
                  >×</button>
                </div>
                <div data-testid="pane-content" className={cn('min-h-0 flex-1 overflow-hidden', dragging && 'pointer-events-none')}>{renderTab(group.id, columnIndex, row, tab)}</div>
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
    <div className="relative flex h-full min-h-0 min-w-0 flex-col overflow-hidden bg-border">
      <div className="flex min-h-0 items-stretch">
        <div className="min-w-0 flex-1">
          <TabBar
            groups={wb.groups}
            activeGroupId={wb.activeGroupId}
            base={base}
            taskName={taskName}
            onActivateGroup={api.activateGroup}
            onCloseGroup={api.closeGroup}
            onNew={openNew}
            onNewLauncher={openLauncher}
            launchers={launcherItems}
            terminalUnavailable={terminalUnavailable}
            onNewGroup={api.addGroup}
            onMoveGroup={moveGroup}
            onDropSession={dropSessionIntoGroup}
          />
        </div>
      </div>
      {dropWarning !== '' && <p role="alert" className="bg-destructive/10 px-3 py-1 text-xs text-destructive">{dropWarning}</p>}
      {newFileError !== '' && <p role="alert" className="bg-destructive/10 px-3 py-1 text-xs text-destructive">新建文件失败：{newFileError}</p>}
      <div className="relative isolate min-h-0 min-w-0 flex-1 overflow-hidden">
        {wb.groups.map((group) => (
          // 单槽位条件：visible 翻转只改同一 div 的类名/aria/inert，不换槽位。
          // 写成 `{a && el}{b && el}` 双槽位时，visible 一翻 React 就按索引
          // 卸载重建——xterm 全套 teardown/replay、Viewport 定时器打在已
          // dispose 的 RenderService 上刷 dimensions、PTY 重连定时器变孤儿
          // （B367 回归实测）。纯文件/会话组后台仍照旧卸载（keep-alive 只保终端）。
          <Fragment key={group.id}>
            {(group.id === wb.activeGroupId ||
              group.columns.some((column) => column.panes.some((tab) => tab?.content.kind === 'terminal'))) &&
              renderGroup(group, group.id === wb.activeGroupId)}
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
