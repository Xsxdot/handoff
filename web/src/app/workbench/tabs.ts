// tabs.ts —— 全局工作台的纯布局模型。
//
// 职责：定义 group/column/pane/tab 的身份、去重与布局迁移；所有写入函数都复制输入。
// 边界：不依赖 React、API 或 DOM。每个 Tab 自带 BaseDir，以支持跨项目同组与精确去重。
// 不变式：至少一个 group；每个 group 至少一列；每列一或两格；空列只有一个 null；
// sizes 与 columns 等长且为正；focus 与 activeGroupId 始终指向现存位置。

export interface BaseDir {
  key: string
  kind: 'workspace' | 'home' | 'scratch'
  path: string
  label: string
  projectName: string
  machine: string
}

export type TabContent =
  | { kind: 'blank' }
  | { kind: 'terminal'; seq: number; sessionId?: string; rel?: string; incompatible?: boolean; launcher?: string }
  | { kind: 'file'; rel: string; draft?: string; baseSha?: string }
  | { kind: 'tui'; taskId: string }

export interface Tab {
  id: string
  base: BaseDir
  content: TabContent
}

export interface TabColumn {
  panes: Array<Tab | null>
}

export interface TabGroup {
  id: string
  name: string
  autoName: boolean
  columns: TabColumn[]
  sizes: number[]
  focus: [number, number]
}

export interface Workbench {
  groups: TabGroup[]
  activeGroupId: string
}

export const MAX_PANES_PER_COLUMN = 2
export const MIN_PANE_PX = 240

export interface PaneTarget {
  groupId: string
  column: number
  row: number
  zone: DropZone
}

export type WorkbenchSource =
  | { kind: 'new'; base: BaseDir; content: TabContent }
  | { kind: 'tab'; groupId: string; tabId: string }

export interface OpenedWorkbenchItem {
  tabId: string
  groupId: string
  column: number
  row: number
  base: BaseDir
  content: TabContent
  label: string
}

export type DropZone = 'left' | 'right' | 'top' | 'bottom' | 'center'

export const EMPTY_WORKBENCH: Workbench = {
  groups: [{
    id: 'g1', name: '组 1', autoName: true,
    columns: [{ panes: [null] }], sizes: [1], focus: [0, 0],
  }],
  activeGroupId: 'g1',
}

export function availablePaneWidth(parentWidth: number, separatorWidths: number[]): number {
  return Math.max(0, parentWidth - separatorWidths.reduce((total, width) => total + width, 0))
}

/** 返回内容的全局去重键；file 需要 baseKey，blank/无 session 终端永不去重。 */
export function dedupKey(baseKey: string, content: TabContent): string | null {
  switch (content.kind) {
    case 'file': return `file:${baseKey}:${content.rel}`
    case 'tui': return `tui:${content.taskId}`
    case 'terminal': return content.sessionId ? `pty:${content.sessionId}` : null
    case 'blank': return null
  }
}

function cloneTab(tab: Tab): Tab {
  return { id: tab.id, base: { ...tab.base }, content: { ...tab.content } as TabContent }
}

function cloneGroup(group: TabGroup): TabGroup {
  return {
    id: group.id,
    name: group.name,
    autoName: group.autoName,
    columns: group.columns.map((column) => ({ panes: column.panes.map((tab) => tab ? cloneTab(tab) : null) })),
    sizes: [...group.sizes],
    focus: [...group.focus] as [number, number],
  }
}

function cloneWorkbench(wb: Workbench): Workbench {
  return { groups: wb.groups.map(cloneGroup), activeGroupId: wb.activeGroupId }
}

function emptyGroup(id: string, name: string, autoName: boolean): TabGroup {
  return { id, name, autoName, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] }
}

function groupIndex(wb: Workbench, groupId: string): number {
  return wb.groups.findIndex((group) => group.id === groupId)
}

function targetGroup(wb: Workbench, groupId?: string): [TabGroup, number] | null {
  const id = groupId ?? wb.activeGroupId
  const index = groupIndex(wb, id)
  return index < 0 ? null : [wb.groups[index], index]
}

function nextId(wb: Workbench, prefix: 't' | 'g'): string {
  let max = 0
  for (const group of wb.groups) {
    if (prefix === 'g') {
      const n = Number(group.id.slice(1))
      if (Number.isInteger(n) && n > max) max = n
    }
    for (const column of group.columns) {
      for (const tab of column.panes) {
        if (!tab || prefix !== 't') continue
        const n = Number(tab.id.slice(1))
        if (Number.isInteger(n) && n > max) max = n
      }
    }
  }
  return `${prefix}${max + 1}`
}

function firstEmpty(group: TabGroup): [number, number] | null {
  const [focusColumn, focusRow] = group.focus
  if (group.columns[focusColumn]?.panes[focusRow] === null) return [focusColumn, focusRow]
  for (let column = 0; column < group.columns.length; column++) {
    for (let row = 0; row < group.columns[column].panes.length; row++) {
      if (group.columns[column].panes[row] === null) return [column, row]
    }
  }
  return null
}

function locationOf(wb: Workbench, tabId: string, requestedGroup?: string): { group: number; column: number; row: number } | null {
  const groups = requestedGroup ? wb.groups.filter((group) => group.id === requestedGroup) : wb.groups
  for (const group of groups) {
    const groupIndexValue = wb.groups.indexOf(group)
    for (let column = 0; column < group.columns.length; column++) {
      const row = group.columns[column].panes.findIndex((tab) => tab?.id === tabId)
      if (row >= 0) return { group: groupIndexValue, column, row }
    }
  }
  return null
}

function findByKey(wb: Workbench, key: string, onlyGroup?: string): { group: number; column: number; row: number; tab: Tab } | null {
  for (let gi = 0; gi < wb.groups.length; gi++) {
    const group = wb.groups[gi]
    if (onlyGroup !== undefined && group.id !== onlyGroup) continue
    for (let column = 0; column < group.columns.length; column++) {
      for (let row = 0; row < group.columns[column].panes.length; row++) {
        const tab = group.columns[column].panes[row]
        if (tab && dedupKey(tab.base.key, tab.content) === key) return { group: gi, column, row, tab }
      }
    }
  }
  return null
}

function warnInvalid(event: string, target: Partial<PaneTarget> & { groupId?: string }): void {
  console.warn(event, {
    groupId: target.groupId,
    column: target.column,
    row: target.row,
    zone: target.zone,
  })
}

/** 在指定 group 的 focus 空格或第一个空格打开；没有空格时追加右侧列。 */
export function openTab(wb: Workbench, base: BaseDir, content: TabContent, groupId?: string): Workbench {
  const target = targetGroup(wb, groupId)
  if (target === null) {
    warnInvalid('workbench.open.invalid_group', { groupId })
    return wb
  }
  const [group, gi] = target
  const localKey = dedupKey(base.key, content)
  const existing = localKey ? findByKey(wb, localKey, group.id) : null
  if (existing) return activateTab(wb, group.id, existing.tab.id)
  const next = cloneWorkbench(wb)
  const nextGroup = next.groups[gi]
  let slot = firstEmpty(nextGroup)
  if (slot === null) {
    nextGroup.columns.push({ panes: [null] })
    nextGroup.sizes.push(1)
    slot = [nextGroup.columns.length - 1, 0]
  }
  const tab: Tab = { id: nextId(next, 't'), base: { ...base }, content: { ...content } as TabContent }
  nextGroup.columns[slot[0]].panes[slot[1]] = tab
  nextGroup.focus = slot
  next.activeGroupId = nextGroup.id
  return next
}

/** 搜索全部 group；命中只聚焦，不命中才在新 group 的首格创建 Tab。 */
export function openOrFocus(wb: Workbench, base: BaseDir, content: TabContent): Workbench {
  const key = dedupKey(base.key, content)
  const existing = key ? findByKey(wb, key) : null
  if (existing) return activateTab(wb, wb.groups[existing.group].id, existing.tab.id)
  const next = cloneWorkbench(wb)
  const id = nextId(next, 'g')
  const group = emptyGroup(id, `组 ${next.groups.length + 1}`, true)
  const tab: Tab = { id: nextId(next, 't'), base: { ...base }, content: { ...content } as TabContent }
  group.columns[0].panes[0] = tab
  group.focus = [0, 0]
  next.groups.push(group)
  next.activeGroupId = id
  return next
}

/** 关闭一个 pane 中的 tab；group 和 column 是显式布局，不因空了而自动删除。 */
export function closeTab(wb: Workbench, groupId: string, tabId: string): Workbench {
  const loc = locationOf(wb, tabId, groupId)
  if (loc === null) return wb
  const next = cloneWorkbench(wb)
  const group = next.groups[loc.group]
  group.columns[loc.column].panes[loc.row] = null
  if (group.columns[loc.column].panes.length === 2 && group.columns[loc.column].panes.every((tab) => tab === null)) {
    group.columns[loc.column].panes = [null]
  }
  group.focus = [loc.column, Math.min(loc.row, group.columns[loc.column].panes.length - 1)]
  return next
}

/** 聚焦指定 tab 所在的 group/cell；非法 id 返回原对象并记录上下文。 */
export function activateTab(wb: Workbench, groupId: string, tabId: string): Workbench {
  const loc = locationOf(wb, tabId, groupId)
  if (loc === null) return wb
  const next = cloneWorkbench(wb)
  next.groups[loc.group].focus = [loc.column, loc.row]
  next.activeGroupId = groupId
  return next
}

/** 切换当前 group，不改变其内部 focus。 */
export function activateGroup(wb: Workbench, groupId: string): Workbench {
  if (groupIndex(wb, groupId) < 0) return wb
  return wb.activeGroupId === groupId ? wb : { ...wb, activeGroupId: groupId }
}

/** 只替换 tab 内容，不改变焦点；重复目标会关闭当前 tab 并聚焦已有目标。 */
export function setTabContent(wb: Workbench, groupId: string, tabId: string, content: TabContent): Workbench {
  const loc = locationOf(wb, tabId, groupId)
  if (loc === null) return wb
  const current = wb.groups[loc.group].columns[loc.column].panes[loc.row]
  if (!current) return wb
  const key = dedupKey(current.base.key, content)
  const existing = key ? findByKey(wb, key) : null
  if (existing && existing.tab.id !== tabId) return activateTab(closeTab(wb, groupId, tabId), wb.groups[existing.group].id, existing.tab.id)
  const next = cloneWorkbench(wb)
  next.groups[loc.group].columns[loc.column].panes[loc.row] = {
    id: tabId, base: { ...current.base }, content: { ...content } as TabContent,
  }
  return next
}

/** 新建一个空 group 并激活它；显式 name 的 group 不再自动改名。 */
export function createGroup(wb: Workbench, name?: string): Workbench {
  const next = cloneWorkbench(wb)
  const id = nextId(next, 'g')
  next.groups.push(emptyGroup(id, name ?? `组 ${next.groups.length + 1}`, name === undefined))
  next.activeGroupId = id
  return next
}

/** 显式关闭 group；最后一个 group 重置为一个空 group。 */
export function closeGroup(wb: Workbench, groupId: string): Workbench {
  const index = groupIndex(wb, groupId)
  if (index < 0) return wb
  if (wb.groups.length === 1) {
    const next = cloneWorkbench(wb)
    next.groups[0] = emptyGroup(next.groups[0].id, next.groups[0].name, next.groups[0].autoName)
    next.activeGroupId = next.groups[0].id
    return next
  }
  const next = cloneWorkbench(wb)
  next.groups.splice(index, 1)
  if (next.activeGroupId === groupId) next.activeGroupId = next.groups[Math.min(index, next.groups.length - 1)].id
  return next
}

/** 向指定或当前 group 右侧增加一列空 pane，并聚焦该 pane。 */
export function addColumn(wb: Workbench, groupId?: string): Workbench {
  const target = targetGroup(wb, groupId)
  if (target === null) {
    warnInvalid('workbench.add_column.invalid_group', { groupId })
    return wb
  }
  const next = cloneWorkbench(wb)
  const group = next.groups[target[1]]
  group.columns.push({ panes: [null] })
  group.sizes.push(1)
  group.focus = [group.columns.length - 1, 0]
  next.activeGroupId = group.id
  return next
}

function removeTabAt(next: Workbench, loc: { group: number; column: number; row: number }): { tab: Tab; columnRemoved: boolean } | null {
  const group = next.groups[loc.group]
  const column = group.columns[loc.column]
  const tab = column?.panes[loc.row] ?? null
  if (!column || !tab) return null
  column.panes[loc.row] = null
  if (column.panes.length === 2 && column.panes.every((pane) => pane === null)) column.panes = [null]
  // 移动 tab 后，完全空的源列没有可承载的 pane；移除它才能让目标列索引与
  // 后续 center/边缘投影保持一致。最后一列仍保留空列，满足 group 的最小布局不变式。
  if (column.panes.length === 1 && column.panes[0] === null && group.columns.length > 1) {
    group.columns.splice(loc.column, 1)
    group.sizes.splice(loc.column, 1)
    const focusedColumn = group.focus[0] > loc.column
      ? group.focus[0] - 1
      : Math.min(group.focus[0], group.columns.length - 1)
    const focusedPanes = group.columns[focusedColumn].panes
    group.focus = [focusedColumn, Math.min(group.focus[1], focusedPanes.length - 1)]
    return { tab, columnRemoved: true }
  }
  return { tab, columnRemoved: false }
}

/** 将新内容或现有 tab 投影到 center/四向区域，整个操作只产生一次新布局。 */
export function placeSource(wb: Workbench, source: WorkbenchSource, target: PaneTarget): Workbench {
  const targetIndex = groupIndex(wb, target.groupId)
  if (targetIndex < 0) {
    warnInvalid('workbench.place.invalid_group', target)
    return wb
  }
  const targetGroupBefore = wb.groups[targetIndex]
  if (target.column < 0 || target.column >= targetGroupBefore.columns.length || target.row < 0 ||
      target.row >= targetGroupBefore.columns[target.column].panes.length) {
    warnInvalid('workbench.place.invalid_target', target)
    return wb
  }
  const sourceLoc = source.kind === 'tab' ? locationOf(wb, source.tabId, source.groupId) : null
  if (source.kind === 'tab' && sourceLoc === null) {
    warnInvalid('workbench.place.invalid_source', target)
    return wb
  }
  if (source.kind === 'tab' && source.groupId === target.groupId && sourceLoc &&
      sourceLoc.column === target.column && sourceLoc.row === target.row && target.zone === 'center') {
    return activateTab(wb, target.groupId, source.tabId)
  }
  const next = cloneWorkbench(wb)
  let sourceTab: Tab
  let sourceColumnRemoved = false
  if (source.kind === 'new') {
    sourceTab = { id: nextId(next, 't'), base: { ...source.base }, content: { ...source.content } as TabContent }
  } else {
    const moved = removeTabAt(next, sourceLoc!)
    if (!moved) {
      warnInvalid('workbench.place.invalid_source', target)
      return wb
    }
    sourceTab = moved.tab
    sourceColumnRemoved = moved.columnRemoved
  }
  let columnIndex = target.column
  let rowIndex = target.row
  if (sourceColumnRemoved && sourceLoc && sourceLoc.group === targetIndex && sourceLoc.column < columnIndex) columnIndex--
  if (sourceLoc && sourceLoc.group === targetIndex && sourceLoc.column === target.column && sourceLoc.row < rowIndex) rowIndex--
  const group = next.groups[targetIndex]
  const column = group.columns[columnIndex]
  if (!column) {
    warnInvalid('workbench.place.invalid_target', target)
    return wb
  }
  let actualColumn = columnIndex
  let actualRow = rowIndex
  if (target.zone === 'left' || target.zone === 'right') {
    actualColumn = target.zone === 'left' ? columnIndex : columnIndex + 1
    group.columns.splice(actualColumn, 0, { panes: [sourceTab] })
    group.sizes.splice(actualColumn, 0, 1)
    actualRow = 0
  } else if (target.zone === 'top' || target.zone === 'bottom') {
    if (column.panes.length < MAX_PANES_PER_COLUMN) {
      actualRow = target.zone === 'top' ? Math.min(rowIndex, column.panes.length) : Math.min(rowIndex + 1, column.panes.length)
      column.panes.splice(actualRow, 0, sourceTab)
    } else {
      column.panes[Math.min(rowIndex, column.panes.length - 1)] = sourceTab
      actualRow = Math.min(rowIndex, column.panes.length - 1)
      console.warn('workbench.place.pane_limit', { groupId: target.groupId, column: actualColumn, row: actualRow, zone: target.zone })
    }
  } else {
    actualRow = Math.min(rowIndex, column.panes.length - 1)
    column.panes[actualRow] = sourceTab
  }
  group.focus = [actualColumn, actualRow]
  next.activeGroupId = group.id
  return next
}

/** 将恢复的会话放入第一个空 pane；无空 pane 时追加 group，但保留 active/focus。 */
export function appendRestoredTab(wb: Workbench, base: BaseDir, content: TabContent): Workbench {
  const next = cloneWorkbench(wb)
  let groupIndexValue = 0
  let slot: [number, number] | null = null
  for (let gi = 0; gi < next.groups.length && slot === null; gi++) {
    const found = firstEmpty(next.groups[gi])
    if (found) { groupIndexValue = gi; slot = found }
  }
  if (slot === null) {
    const id = nextId(next, 'g')
    next.groups.push(emptyGroup(id, `组 ${next.groups.length + 1}`, true))
    groupIndexValue = next.groups.length - 1
    slot = [0, 0]
  }
  const group = next.groups[groupIndexValue]
  group.columns[slot[0]].panes[slot[1]] = { id: nextId(next, 't'), base: { ...base }, content: { ...content } as TabContent }
  return next
}

/** 调整同一 group 的相邻 columns；minRatio 由 DOM 容器换算。 */
export function resizeColumns(wb: Workbench, groupId: string, dividerIndex: number, delta: number, minRatio: number): Workbench {
  const index = groupIndex(wb, groupId)
  if (index < 0) return wb
  const group = wb.groups[index]
  const right = dividerIndex + 1
  if (dividerIndex < 0 || right >= group.sizes.length || group.sizes.length !== group.columns.length) {
    warnInvalid('workbench.resize.invalid_divider', { groupId, column: dividerIndex, row: 0, zone: 'center' })
    return wb
  }
  const total = group.sizes.reduce((sum, size) => sum + size, 0)
  const leftRatio = group.sizes[dividerIndex] / total
  const rightRatio = group.sizes[right] / total
  const minimum = Math.max(0, minRatio)
  if (minimum * 2 > leftRatio + rightRatio) return wb
  let change = delta
  if (leftRatio + change < minimum) change = minimum - leftRatio
  if (rightRatio - change < minimum) change = rightRatio - minimum
  if (change === 0) return wb
  const next = cloneWorkbench(wb)
  next.groups[index].sizes[dividerIndex] = (leftRatio + change) * total
  next.groups[index].sizes[right] = (rightRatio - change) * total
  return next
}

/** 计算所有 group 的最大终端序号加一。 */
export function nextTerminalSeq(wb: Workbench): number {
  let max = 0
  for (const group of wb.groups) for (const column of group.columns) for (const tab of column.panes) {
    if (tab?.content.kind === 'terminal') max = Math.max(max, tab.content.seq)
  }
  return max + 1
}

/** 只判断 pane 是否全部为空；空 group 仍是有效布局。 */
export function isEmptyWorkbench(wb: Workbench): boolean {
  return wb.groups.every((group) => group.columns.every((column) => column.panes.every((tab) => tab === null)))
}

/** 投影树需要的打开项清单，坐标来自全局 group/column/row。 */
export function openedWorkbenchItems(wb: Workbench): OpenedWorkbenchItem[] {
  const items: OpenedWorkbenchItem[] = []
  for (const group of wb.groups) for (let column = 0; column < group.columns.length; column++) {
    for (let row = 0; row < group.columns[column].panes.length; row++) {
      const tab = group.columns[column].panes[row]
      if (!tab) continue
      items.push({ tabId: tab.id, groupId: group.id, column, row, base: { ...tab.base }, content: { ...tab.content } as TabContent, label: tabTitle(tab.content, tab.base.label) })
    }
  }
  return items
}

/** 生成 pane/左树显示的短标题。 */
export function tabTitle(content: TabContent, baseLabel: string): string {
  switch (content.kind) {
    case 'terminal': return content.launcher ?? (content.seq <= 1 ? `bash · ${baseLabel}` : `bash · ${baseLabel} (${content.seq})`)
    case 'file': return content.rel.split('/').pop() || content.rel
    case 'tui': return `TUI · ${content.taskId.slice(0, 8)}`
    case 'blank': return '新建标签页'
  }
}
