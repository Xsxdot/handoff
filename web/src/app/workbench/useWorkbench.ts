// useWorkbench.ts —— 全局 Workbench 的 React 状态容器。
//
// 职责：持有一个全局布局与左栏当前选中 BaseDir，把 tabs.ts 纯函数接成 UI API。
// 边界：不按目录分 Map、不发 HTTP、不认识 ProjectTree；同步与恢复由 useWorkbenchSync 负责。
import { useCallback, useRef, useState } from 'react'
import {
  EMPTY_WORKBENCH,
  activateGroup as activateGroupLayout,
  activateTab,
  appendRestoredTab,
  closePane as closePaneLayout,
  closeGroup as closeGroupLayout,
  closeTab,
  createGroup,
  nextTerminalSeq,
  openOrFocus as openOrFocusLayout,
  openTab,
  openedWorkbenchItems,
  placeSource,
  resizeColumns,
  setTabContent,
  type BaseDir,
  type OpenedWorkbenchItem,
  type PaneTarget,
  type TabContent,
  type Workbench,
  type WorkbenchSource,
} from './tabs'

export type { BaseDir } from './tabs'

/** home 终端的 BaseDir；它可进入某个 pane，但不会被 ProjectTree 当作 workspace。 */
export const HOME_BASE: BaseDir = {
  key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '',
}

/** 为 scratch 文件生成不进入左树的 BaseDir。 */
export function scratchBase(root: string, machine: string): BaseDir {
  return { key: `scratch:${machine}:${root}`, kind: 'scratch', path: root, label: '临时', projectName: '', machine }
}

export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (base: BaseDir) => void
  open: (content: TabContent, base?: BaseDir, groupId?: string) => void
  openOrFocus: (content: TabContent, base?: BaseDir) => void
  openTerminal: (base?: BaseDir, groupId?: string, rel?: string) => void
  close: (groupId: string, tabId: string) => void
  activate: (groupId: string, tabId: string) => void
  activateGroup: (groupId: string) => void
  setContent: (groupId: string, tabId: string, content: TabContent) => void
  addGroup: () => void
  closeGroup: (groupId: string) => void
  place: (source: WorkbenchSource, target: PaneTarget) => void
  closePane: (groupId: string, column: number, row: number) => void
  closeById: (tabId: string) => void
  resize: (groupId: string, dividerIndex: number, delta: number, minRatio: number) => void
  restoreTerminal: (base: BaseDir, sessionId: string, incompatible?: boolean) => void
  hydrate: (workbench: Workbench) => void
  openedItems: OpenedWorkbenchItem[]
}

function targetBase(explicit: BaseDir | undefined, selected: BaseDir | null): BaseDir | null {
  return explicit ?? selected
}

export function useWorkbench(): WorkbenchApi {
  const [base, setBase] = useState<BaseDir | null>(null)
  const [wb, setWb] = useState<Workbench>(EMPTY_WORKBENCH)
  const baseRef = useRef<BaseDir | null>(null)
  baseRef.current = base

  const select = useCallback((nextBase: BaseDir) => {
    baseRef.current = nextBase
    setBase(nextBase)
    console.debug('workbench.select', { baseKey: nextBase.key, project: nextBase.projectName, machine: nextBase.machine, path: nextBase.path })
  }, [])

  const mutate = useCallback((fn: (current: Workbench) => Workbench, explicitBase?: BaseDir) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.action.missing_base', { content: 'layout action has no selected base' })
      return
    }
    setWb((current) => fn(current))
  }, [])

  const open = useCallback((content: TabContent, explicitBase?: BaseDir, groupId?: string) => {
    mutate((current) => openTab(current, targetBase(explicitBase, baseRef.current)!, content, groupId), explicitBase)
  }, [mutate])

  const openOrFocus = useCallback((content: TabContent, explicitBase?: BaseDir) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.open_or_focus.missing_base', { content: content.kind })
      return
    }
    setWb((current) => openOrFocusLayout(current, target, content))
  }, [])

  const openTerminal = useCallback((explicitBase?: BaseDir, groupId?: string, rel?: string) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.open_terminal.missing_base', { groupId, rel })
      return
    }
    setWb((current) => openTab(current, target, {
      kind: 'terminal', seq: nextTerminalSeq(current), ...(rel === undefined ? {} : { rel }),
    }, groupId))
  }, [])

  const close = useCallback((groupId: string, tabId: string) => setWb((current) => closeTab(current, groupId, tabId)), [])
  const activate = useCallback((groupId: string, tabId: string) => setWb((current) => activateTab(current, groupId, tabId)), [])
  const activateGroup = useCallback((groupId: string) => setWb((current) => activateGroupLayout(current, groupId)), [])
  const setContent = useCallback((groupId: string, tabId: string, content: TabContent) => setWb((current) => setTabContent(current, groupId, tabId, content)), [])
  const addGroup = useCallback(() => setWb((current) => createGroup(current)), [])
  const closeGroup = useCallback((groupId: string) => setWb((current) => closeGroupLayout(current, groupId)), [])
  const place = useCallback((source: WorkbenchSource, target: PaneTarget) => {
    setWb((current) => placeSource(current, source, target))
  }, [])
  const closePane = useCallback((groupId: string, column: number, row: number) => {
    setWb((current) => closePaneLayout(current, groupId, column, row))
  }, [])
  const closeById = useCallback((tabId: string) => {
    setWb((current) => {
      for (const group of current.groups) {
        if (group.columns.some((column) => column.panes.some((tab) => tab?.id === tabId))) return closeTab(current, group.id, tabId)
      }
      return current
    })
  }, [])
  const resize = useCallback((groupId: string, dividerIndex: number, delta: number, minRatio: number) => {
    setWb((current) => resizeColumns(current, groupId, dividerIndex, delta, minRatio))
  }, [])
  const restoreTerminal = useCallback((target: BaseDir, sessionId: string, incompatible = false) => {
    setWb((current) => openRestored(current, target, sessionId, incompatible))
  }, [])
  const hydrate = useCallback((next: Workbench) => setWb(next), [])

  return {
    base, wb, select, open, openOrFocus, openTerminal, close, activate, activateGroup,
    setContent, addGroup, closeGroup, place, closePane, closeById, resize, restoreTerminal,
    hydrate, openedItems: openedWorkbenchItems(wb),
  }
}

function openRestored(wb: Workbench, base: BaseDir, sessionId: string, incompatible: boolean): Workbench {
  return appendRestoredTab(wb, base, {
    kind: 'terminal', seq: nextTerminalSeq(wb), sessionId, ...(incompatible ? { incompatible: true } : {}),
  })
}
