// useWorkbench.ts —— 全局 Workbench 的 React 状态容器。
//
// 职责：持有一个全局布局与左栏当前选中 BaseDir，把 tabs.ts 纯函数接成 UI API。
// 边界：不按目录分 Map、不发 HTTP、不认识 ProjectTree；同步与恢复由 useWorkbenchSync 负责。
import { useCallback, useRef, useState } from 'react'
import {
  EMPTY_WORKBENCH,
  activateGroup as activateGroupLayout,
  activateTab,
  focusRestoredTab,
  closePane as closePaneLayout,
  closeGroup as closeGroupLayout,
  closeTab,
  createGroup,
  nextTerminalSeq,
  openOrFocus as openOrFocusLayout,
  openTab,
  openInNewGroup as openInNewGroupLayout,
  openInRightSplit as openInRightSplitLayout,
  openedWorkbenchItems,
  placeSource,
  resizeColumns,
  setTabContent,
  spawnTerminalContent,
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

/** 会话工作台 tab 的 BaseDir（B358.6）：kind 取白名单内的 'home'——会话不挂目录，
 * path 留空；key 唯一化使 openItems 已打开行互不撞键。 */
export function sessionBase(sessionId: string): BaseDir {
  return { key: `session:${sessionId}`, kind: 'home', path: '', label: '会话', projectName: '', machine: '' }
}

export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  /** Hold layout mutations until authoritative startup hydration finishes. */
  lockLayout: () => void
  /** Allow local layout changes after hydration succeeds or fails visibly. */
  enableLayout: () => void
  select: (base: BaseDir) => void
  open: (content: TabContent, base?: BaseDir, groupId?: string) => void
  openInNewGroup: (content: TabContent, base?: BaseDir) => void
  openInRightSplit: (content: TabContent, base?: BaseDir) => void
  openOrFocus: (content: TabContent, base?: BaseDir) => void
  openTerminal: (base?: BaseDir, groupId?: string, rel?: string) => void
  // attach 等边界入口使用服务端提供的首次命令；命令原样进入 terminal tab。
  openTerminalWithCommand: (command: string, base?: BaseDir, groupId?: string) => void
  close: (groupId: string, tabId: string) => void
  activate: (groupId: string, tabId: string) => void
  // focusTab 是左栏「已打开行」的聚焦入口：切基准 + 激活 tab 一次完成。
  // 为什么不用 open：open 的去重键对无会话终端是 null，重复 open 会开出第二个
  // 终端而不是聚焦——聚焦必须走 activateTab 语义。
  focusTab: (b: BaseDir, group: string, tabId: string) => void
  activateGroup: (groupId: string) => void
  setContent: (groupId: string, tabId: string, content: TabContent) => void
  addGroup: () => void
  closeGroup: (groupId: string) => void
  place: (source: WorkbenchSource, target: PaneTarget) => void
  closePane: (groupId: string, column: number, row: number) => void
  closeById: (tabId: string) => void
  resize: (groupId: string, dividerIndex: number, delta: number, minRatio: number) => void
  restoreTerminal: (base: BaseDir, sessionId: string, incompatible?: boolean) => void
  /** Authoritative restore entry; deliberately bypasses the action lock and re-enables layout. */
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
  const layoutEnabledRef = useRef(true)
  baseRef.current = base

  const lockLayout = useCallback(() => { layoutEnabledRef.current = false }, [])
  const enableLayout = useCallback(() => { layoutEnabledRef.current = true }, [])
  const updateLayout = useCallback((update: (current: Workbench) => Workbench) => {
    if (!layoutEnabledRef.current) {
      console.debug('workbench.action.held_during_restore')
      return
    }
    setWb(update)
  }, [])

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
    updateLayout(fn)
  }, [updateLayout])

  const open = useCallback((content: TabContent, explicitBase?: BaseDir, groupId?: string) => {
    mutate((current) => openTab(current, targetBase(explicitBase, baseRef.current)!, content, groupId), explicitBase)
  }, [mutate])

  const openInNewGroup = useCallback((content: TabContent, explicitBase?: BaseDir) => {
    mutate((current) => openInNewGroupLayout(current, targetBase(explicitBase, baseRef.current)!, content), explicitBase)
  }, [mutate])
  const openInRightSplit = useCallback((content: TabContent, explicitBase?: BaseDir) => {
    mutate((current) => openInRightSplitLayout(current, targetBase(explicitBase, baseRef.current)!, content), explicitBase)
  }, [mutate])

  const openOrFocus = useCallback((content: TabContent, explicitBase?: BaseDir) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.open_or_focus.missing_base', { content: content.kind })
      return
    }
    updateLayout((current) => openOrFocusLayout(current, target, content))
  }, [updateLayout])

  const openTerminal = useCallback((explicitBase?: BaseDir, groupId?: string, rel?: string) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.open_terminal.missing_base', { groupId, rel })
      return
    }
    updateLayout((current) => openTab(current, target, spawnTerminalContent(nextTerminalSeq(current), rel === undefined ? {} : { rel }), groupId))
  }, [updateLayout])

  const openTerminalWithCommand = useCallback((command: string, explicitBase?: BaseDir, groupId?: string) => {
    const target = targetBase(explicitBase, baseRef.current)
    if (target === null) {
      console.warn('workbench.open_terminal_with_command.missing_base', { groupId })
      return
    }
    updateLayout((current) => openTab(current, target, spawnTerminalContent(nextTerminalSeq(current), { initCommand: command }), groupId))
  }, [updateLayout])

  const close = useCallback((groupId: string, tabId: string) => updateLayout((current) => closeTab(current, groupId, tabId)), [updateLayout])
  const activate = useCallback((groupId: string, tabId: string) => updateLayout((current) => activateTab(current, groupId, tabId)), [updateLayout])
  // focusTab：先切基准再激活，两个状态更新在同一次点击事件里批处理完成
  const focusTab = useCallback((b: BaseDir, group: string, tabId: string) => {
    select(b)
    updateLayout((current) => activateTab(current, group, tabId))
    console.debug('workbench.focus_tab', { baseKey: b.key, groupId: group, tabId })
  }, [select, updateLayout])
  const activateGroup = useCallback((groupId: string) => updateLayout((current) => activateGroupLayout(current, groupId)), [updateLayout])
  const setContent = useCallback((groupId: string, tabId: string, content: TabContent) => updateLayout((current) => setTabContent(current, groupId, tabId, content)), [updateLayout])
  const addGroup = useCallback(() => updateLayout((current) => createGroup(current)), [updateLayout])
  const closeGroup = useCallback((groupId: string) => updateLayout((current) => closeGroupLayout(current, groupId)), [updateLayout])
  const place = useCallback((source: WorkbenchSource, target: PaneTarget) => {
    updateLayout((current) => placeSource(current, source, target))
  }, [updateLayout])
  const closePane = useCallback((groupId: string, column: number, row: number) => {
    updateLayout((current) => closePaneLayout(current, groupId, column, row))
  }, [updateLayout])
  const closeById = useCallback((tabId: string) => {
    updateLayout((current) => {
      for (const group of current.groups) {
        if (group.columns.some((column) => column.panes.some((tab) => tab?.id === tabId))) return closeTab(current, group.id, tabId)
      }
      return current
    })
  }, [updateLayout])
  const resize = useCallback((groupId: string, dividerIndex: number, delta: number, minRatio: number) => {
    updateLayout((current) => resizeColumns(current, groupId, dividerIndex, delta, minRatio))
  }, [updateLayout])
  const restoreTerminal = useCallback((target: BaseDir, sessionId: string, incompatible = false) => {
    updateLayout((current) => openRestored(current, target, sessionId, incompatible))
  }, [updateLayout])
  const hydrate = useCallback((next: Workbench) => {
    layoutEnabledRef.current = true
    setWb(next)
  }, [])

  return {
    base, wb, lockLayout, enableLayout, select, open, openInNewGroup, openInRightSplit, openOrFocus, openTerminal, openTerminalWithCommand, close, activate, focusTab, activateGroup,
    setContent, addGroup, closeGroup, place, closePane, closeById, resize, restoreTerminal,
    hydrate, openedItems: openedWorkbenchItems(wb),
  }
}

function openRestored(wb: Workbench, base: BaseDir, sessionId: string, incompatible: boolean): Workbench {
  return focusRestoredTab(wb, base, {
    kind: 'terminal', seq: nextTerminalSeq(wb), sessionId, ...(incompatible ? { incompatible: true } : {}),
  })
}
