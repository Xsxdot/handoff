// restore.ts —— 将全局工作台 payload、悬浮窗现场和服务端会话合成为恢复结果。
//
// 职责：只合成 workbench/dock，并返回 selected/统计；不选树节点、不发请求、不打日志。
// 边界：只认 GLOBAL_WORKBENCH_KEY，旧按目录行进入 legacy，不猜测式迁移。
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import {
  clampGeom,
  decodeDock,
  markIncompatibleDockTabs,
  pruneDeadDockSessions,
  type DockSnapshot,
} from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import {
  decodeWorkbench,
  GLOBAL_WORKBENCH_KEY,
  markIncompatibleSessions,
  pruneDeadSessions,
} from './persist'
import { appendRestoredTab, EMPTY_WORKBENCH, nextTerminalSeq, type BaseDir, type TabContent, type Workbench } from './tabs'
import { HOME_BASE } from './useWorkbench'

export interface RestoreInput {
  state: WorkbenchStateResp
  sessions: PtySession[]
  vw: number
  vh: number
  inset: number
}

export interface RestoreResult {
  workbench: Workbench
  dock: DockSnapshot | null
  dockOrphans: HomeTab[]
  selected: string
  dropped: string[]
  legacy: string[]
  pruned: number
  adopted: number
}

/** 从 PtySession 生成树可复用的 BaseDir；同一路径按 machine 区分。 */
export function baseOfSession(session: PtySession): BaseDir {
  if (session.base_kind === 'home') {
    return session.machine === ''
      ? HOME_BASE
      : { key: `~@${session.machine}`, kind: 'home', path: '~', label: `home@${session.machine}`, projectName: '', machine: session.machine }
  }
  const label = session.base_path.split('/').filter(Boolean).pop() ?? session.base_path
  return {
    key: session.machine === '' ? session.base_path : `${session.base_path}@${session.machine}`,
    kind: 'workspace', path: session.base_path, label, projectName: '', machine: session.machine,
  }
}

function liveIds(sessions: PtySession[]): Set<string> {
  return new Set(sessions.filter((session) => session.exit_code == null).map((session) => session.id))
}

function incompatibleIds(sessions: PtySession[]): Set<string> {
  return new Set(sessions.filter((session) => session.exit_code == null && session.incompatible).map((session) => session.id))
}

function countSessions(wb: Workbench): number {
  return wb.groups.reduce((count, group) => count + group.columns.reduce((columnCount, column) => columnCount + column.panes.filter((tab) => tab?.content.kind === 'terminal' && tab.content.sessionId !== undefined).length, 0), 0)
}

function usedSessionIds(wb: Workbench, dock: DockSnapshot | null): Set<string> {
  const ids = new Set<string>()
  for (const group of wb.groups) for (const column of group.columns) for (const tab of column.panes) {
    if (tab?.content.kind === 'terminal' && tab.content.sessionId !== undefined) ids.add(tab.content.sessionId)
  }
  for (const tab of dock?.tabs ?? []) if (tab.sessionId !== undefined) ids.add(tab.sessionId)
  return ids
}

function orphanContent(session: PtySession, seq: number): TabContent {
  const content: TabContent = { kind: 'terminal', seq, sessionId: session.id }
  return session.incompatible ? { ...content, incompatible: true } : content
}

/** 只恢复 global 行；清理 session、补孤儿，并保持 selected/active 语义。 */
export function buildRestore(input: RestoreInput): RestoreResult {
  let workbench: Workbench = EMPTY_WORKBENCH
  const dropped: string[] = []
  const legacy: string[] = []
  let globalSeen = false
  for (const row of input.state.bases) {
    if (row.base_key !== GLOBAL_WORKBENCH_KEY) {
      legacy.push(row.base_key)
      continue
    }
    if (globalSeen) continue
    globalSeen = true
    const decoded = decodeWorkbench(row.payload)
    if (decoded === null) dropped.push(row.base_key)
    else workbench = decoded
  }

  const live = liveIds(input.sessions)
  const before = countSessions(workbench)
  workbench = markIncompatibleSessions(pruneDeadSessions(workbench, live), incompatibleIds(input.sessions))
  const pruned = before - countSessions(workbench)

  let dock: DockSnapshot | null = null
  let dockPruned = 0
  if (input.state.dock !== '') {
    const decoded = decodeDock(input.state.dock)
    if (decoded !== null) {
      const beforeDock = decoded.tabs.filter((tab) => tab.sessionId !== undefined).length
      const tabs = pruneDeadDockSessions(decoded.tabs, live)
      dock = {
        ...decoded,
        tabs: markIncompatibleDockTabs(tabs, incompatibleIds(input.sessions)),
        geom: clampGeom(decoded.geom, input.vw, input.vh, input.inset),
      }
      // dock session pruning is part of the same user-visible statistic.
      const afterDock = tabs.filter((tab) => tab.sessionId !== undefined).length
      dockPruned = beforeDock - afterDock
    }
  }

  const used = usedSessionIds(workbench, dock)
  const dockOrphans: HomeTab[] = []
  let adopted = 0
  let dockSeq = Math.max(0, ...(dock?.tabs ?? []).map((tab) => tab.seq))
  for (const session of input.sessions) {
    if (!live.has(session.id) || used.has(session.id)) continue
    adopted++
    const base = baseOfSession(session)
    if (base.kind === 'home') {
      const tab: HomeTab = { id: session.id, kind: 'terminal', seq: ++dockSeq, sessionId: session.id, machine: session.machine }
      if (session.incompatible) tab.incompatible = true
      if (dock === null) dockOrphans.push(tab)
      else dock = { ...dock, tabs: [...dock.tabs, tab] }
      continue
    }
    workbench = appendRestoredTab(workbench, base, orphanContent(session, nextTerminalSeq(workbench)))
    used.add(session.id)
  }
  if (dock !== null && dock.activeId === null && dock.tabs.length > 0) dock = { ...dock, activeId: dock.tabs[0].id }

  return {
    workbench,
    dock,
    dockOrphans,
    selected: input.state.selected,
    dropped,
    legacy,
    pruned: pruned + dockPruned,
    adopted,
  }
}
