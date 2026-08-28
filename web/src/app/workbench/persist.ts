// persist.ts —— 前端全局工作台 payload 的编解码。
//
// 职责：严格校验/序列化全局 Workbench，并提供 session 清理与 payload 差分。
// 边界：只处理前端 payload，不发 HTTP；draft/baseSha 与 incompatible 是运行时字段，不落盘。
import {
  isEmptyWorkbench as isEmptyLayout,
  type BaseDir,
  type Tab,
  type TabContent,
  type TabGroup,
  type Workbench,
} from './tabs'

export const GLOBAL_WORKBENCH_KEY = '__global_workbench__'
export const PERSIST_VERSION = 2

interface PersistedWorkbench {
  v: number
  wb: Workbench
}

function hasOnly(value: Record<string, unknown>, keys: string[]): boolean {
  return Object.keys(value).every((key) => keys.includes(key))
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isString(value: unknown): value is string { return typeof value === 'string' }
function isFiniteNumber(value: unknown): value is number { return typeof value === 'number' && Number.isFinite(value) }

function stripContent(content: TabContent): TabContent {
  if (content.kind === 'file') return { kind: 'file', rel: content.rel }
  if (content.kind === 'terminal') {
    const out: Extract<TabContent, { kind: 'terminal' }> = { kind: 'terminal', seq: content.seq }
    if (content.sessionId !== undefined) out.sessionId = content.sessionId
    if (content.rel !== undefined) out.rel = content.rel
    if (content.launcher !== undefined) out.launcher = content.launcher
    return out
  }
  return { ...content }
}

function stripTab(tab: Tab): Tab {
  return { id: tab.id, base: { ...tab.base }, content: stripContent(tab.content) }
}

function stripWorkbench(wb: Workbench): Workbench {
  return {
    activeGroupId: wb.activeGroupId,
    groups: wb.groups.map((group) => ({
      id: group.id,
      name: group.name,
      autoName: group.autoName,
      columns: group.columns.map((column) => ({ panes: column.panes.map((tab) => tab ? stripTab(tab) : null) })),
      sizes: [...group.sizes],
      focus: [...group.focus] as [number, number],
    })),
  }
}

/** 编码全局工作台；只去除运行时草稿/不兼容标记，不丢布局或 Tab 的 BaseDir。 */
export function encodeWorkbench(wb: Workbench): string {
  const payload: PersistedWorkbench = { v: PERSIST_VERSION, wb: stripWorkbench(wb) }
  return JSON.stringify(payload)
}

function parseBase(raw: unknown): BaseDir | null {
  if (!isObject(raw) || !hasOnly(raw, ['key', 'kind', 'path', 'label', 'projectName', 'machine'])) return null
  if (!isString(raw.key) || !isString(raw.path) || !isString(raw.label) || !isString(raw.projectName) || !isString(raw.machine)) return null
  if (raw.kind !== 'workspace' && raw.kind !== 'home' && raw.kind !== 'scratch') return null
  return { key: raw.key, kind: raw.kind, path: raw.path, label: raw.label, projectName: raw.projectName, machine: raw.machine }
}

function parseContent(raw: unknown): TabContent | null {
  if (!isObject(raw) || !isString(raw.kind)) return null
  switch (raw.kind) {
    case 'blank':
      return hasOnly(raw, ['kind']) ? { kind: 'blank' } : null
    case 'file':
      return hasOnly(raw, ['kind', 'rel']) && isString(raw.rel) ? { kind: 'file', rel: raw.rel } : null
    case 'tui':
      return hasOnly(raw, ['kind', 'taskId']) && isString(raw.taskId) ? { kind: 'tui', taskId: raw.taskId } : null
    case 'terminal': {
      if (!hasOnly(raw, ['kind', 'seq', 'sessionId', 'rel', 'launcher']) || !isFiniteNumber(raw.seq)) return null
      if (raw.sessionId !== undefined && !isString(raw.sessionId)) return null
      if (raw.rel !== undefined && !isString(raw.rel)) return null
      if (raw.launcher !== undefined && !isString(raw.launcher)) return null
      const out: Extract<TabContent, { kind: 'terminal' }> = { kind: 'terminal', seq: raw.seq }
      if (raw.sessionId !== undefined) out.sessionId = raw.sessionId
      if (raw.rel !== undefined) out.rel = raw.rel
      if (raw.launcher !== undefined) out.launcher = raw.launcher
      return out
    }
    default: return null
  }
}

function parseWorkbench(raw: unknown): Workbench | null {
  if (!isObject(raw) || !hasOnly(raw, ['groups', 'activeGroupId']) || !Array.isArray(raw.groups) || raw.groups.length === 0 || !isString(raw.activeGroupId)) return null
  const groups: TabGroup[] = []
  const groupIds = new Set<string>()
  const tabIds = new Set<string>()
  for (const value of raw.groups) {
    if (!isObject(value) || !hasOnly(value, ['id', 'name', 'autoName', 'columns', 'sizes', 'focus'])) return null
    if (!isString(value.id) || groupIds.has(value.id) || !isString(value.name) || typeof value.autoName !== 'boolean' ||
        !Array.isArray(value.columns) || value.columns.length === 0 || !Array.isArray(value.sizes) ||
        value.sizes.length !== value.columns.length || !value.sizes.every(isFiniteNumber) || value.sizes.some((size) => size <= 0) ||
        !Array.isArray(value.focus) || value.focus.length !== 2 || !value.focus.every((n) => typeof n === 'number' && Number.isInteger(n))) return null
    groupIds.add(value.id)
    const columns = []
    for (const columnValue of value.columns) {
      if (!isObject(columnValue) || !hasOnly(columnValue, ['panes']) || !Array.isArray(columnValue.panes) || columnValue.panes.length < 1 || columnValue.panes.length > 2) return null
      if (columnValue.panes.length === 2 && columnValue.panes.every((pane) => pane === null)) return null
      const panes: Array<Tab | null> = []
      for (const paneValue of columnValue.panes) {
        if (paneValue === null) { panes.push(null); continue }
        if (!isObject(paneValue) || !hasOnly(paneValue, ['id', 'base', 'content']) || !isString(paneValue.id) || tabIds.has(paneValue.id)) return null
        const base = parseBase(paneValue.base)
        const content = parseContent(paneValue.content)
        if (base === null || content === null) return null
        tabIds.add(paneValue.id)
        panes.push({ id: paneValue.id, base, content })
      }
      columns.push({ panes })
    }
    const focus = value.focus as number[]
    if (focus[0] < 0 || focus[0] >= columns.length || focus[1] < 0 || focus[1] >= columns[focus[0]].panes.length) return null
    groups.push({ id: value.id, name: value.name, autoName: value.autoName, columns, sizes: value.sizes, focus: [focus[0], focus[1]] })
  }
  if (!groupIds.has(raw.activeGroupId)) return null
  return { groups, activeGroupId: raw.activeGroupId }
}

/** 解码全局 payload；版本、布局、BaseDir、TabContent 任一字段不合法都返回 null。 */
export function decodeWorkbench(raw: string): Workbench | null {
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!isObject(parsed) || !hasOnly(parsed, ['v', 'wb']) || parsed.v !== PERSIST_VERSION) return null
    return parseWorkbench(parsed.wb)
  } catch {
    return null
  }
}

/** 空布局判断的持久化层导出，空布局由同步层写成删除。 */
export function isEmptyWorkbench(wb: Workbench): boolean { return isEmptyLayout(wb) }

/** 清掉不在 liveIds 的终端 sessionId，但保留原 pane 位置与其它字段。 */
export function pruneDeadSessions(wb: Workbench, liveIds: ReadonlySet<string>): Workbench {
  return {
    ...wb,
    groups: wb.groups.map((group) => ({
      ...group,
      columns: group.columns.map((column) => ({
        panes: column.panes.map((tab) => {
          if (!tab || tab.content.kind !== 'terminal' || tab.content.sessionId === undefined || liveIds.has(tab.content.sessionId)) return tab
          const content = { ...tab.content }
          delete content.sessionId
          delete content.incompatible
          return { ...tab, content }
        }),
      })),
    })),
  }
}

/** 给仍存活但协议不兼容的终端加运行时标记，不改变 sessionId。 */
export function markIncompatibleSessions(wb: Workbench, ids: ReadonlySet<string>): Workbench {
  if (ids.size === 0) return wb
  return {
    ...wb,
    groups: wb.groups.map((group) => ({
      ...group,
      columns: group.columns.map((column) => ({
        panes: column.panes.map((tab) => tab && tab.content.kind === 'terminal' && tab.content.sessionId !== undefined && ids.has(tab.content.sessionId)
          ? { ...tab, content: { ...tab.content, incompatible: true } }
          : tab),
      })),
    })),
  }
}

/** 按序列化字符串区分 changed 与 removed，供同步层决定 PUT 内容。 */
export function diffPayloads(previous: Record<string, string>, next: Record<string, string>): { changed: string[]; removed: string[] } {
  const changed = Object.entries(next).filter(([key, value]) => previous[key] !== value).map(([key]) => key)
  const removed = Object.keys(previous).filter((key) => !(key in next))
  return { changed, removed }
}
