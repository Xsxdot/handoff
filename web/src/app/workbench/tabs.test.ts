import { describe, expect, it, vi } from 'vitest'
import {
  EMPTY_WORKBENCH,
  MIN_PANE_PX,
  addColumn,
  availablePaneWidth,
  closePane,
  closeGroup,
  closeTab,
  createGroup,
  dedupKey,
  isEmptyWorkbench,
  nextTerminalSeq,
  openOrFocus,
  openTab,
  openedWorkbenchItems,
  placeSource,
  resizeColumns,
  tabTitle,
  type BaseDir,
} from './tabs'

const handoff: BaseDir = {
  key: '/repo/handoff', kind: 'workspace', path: '/repo/handoff', label: 'main',
  projectName: 'handoff', machine: '',
}
const aim: BaseDir = {
  key: '/srv/aim@linux-01', kind: 'workspace', path: '/srv/aim', label: 'eval',
  projectName: 'aim', machine: 'linux-01',
}

describe('dedupKey', () => {
  it('按 base key 区分文件，TUI 和有 session 的终端按全局身份去重', () => {
    expect(dedupKey('/a', { kind: 'file', rel: 'go.mod' })).toBe('file:/a:go.mod')
    expect(dedupKey('/b', { kind: 'file', rel: 'go.mod' })).toBe('file:/b:go.mod')
    expect(dedupKey('/a', { kind: 'tui', taskId: 'T1' })).toBe('tui:T1')
    expect(dedupKey('/a', { kind: 'terminal', seq: 2 })).toBeNull()
    expect(dedupKey('/a', { kind: 'terminal', seq: 2, sessionId: 'S1' })).toBe('pty:S1')
    expect(dedupKey('/a', { kind: 'blank' })).toBeNull()
  })
})

describe('openTab and openOrFocus', () => {
  it('不同项目的 TUI 与终端可以落在同一全局组的左右两列', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'B281' })
    wb = addColumn(wb)
    const groupId = wb.activeGroupId
    wb = placeSource(wb, {
      kind: 'new', base: aim, content: { kind: 'terminal', seq: 1, rel: '' },
    }, { groupId, column: 1, row: 0, zone: 'center' })
    expect(wb.groups[0].columns[0].panes[0]).toMatchObject({
      base: { projectName: 'handoff', machine: '' }, content: { kind: 'tui', taskId: 'B281' },
    })
    expect(wb.groups[0].columns[1].panes[0]).toMatchObject({
      base: { projectName: 'aim', machine: 'linux-01' }, content: { kind: 'terminal', rel: '' },
    })
  })

  it('普通 open 只在目标 group 的 focus 空 pane 落点', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'file', rel: 'a.go' })
    wb = createGroup(wb, '第二组')
    wb = openTab(wb, aim, { kind: 'tui', taskId: 'T2' }, 'g2')
    expect(wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'file', rel: 'a.go' })
    expect(wb.groups[1].columns[0].panes[0]?.content).toEqual({ kind: 'tui', taskId: 'T2' })
  })

  it('openOrFocus 命中全局已有 tab，不创建新组', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
    wb = createGroup(wb, '第二组')
    const before = wb.groups.length
    wb = openOrFocus(wb, handoff, { kind: 'tui', taskId: 'A' })
    expect(wb.groups).toHaveLength(before)
    expect(wb.activeGroupId).toBe('g1')
    expect(wb.groups[0].focus).toEqual([0, 0])
  })

  it('未命中 openOrFocus 新建组，且新 Tab 自带 BaseDir', () => {
    const wb = openOrFocus(EMPTY_WORKBENCH, aim, { kind: 'tui', taskId: 'new' })
    expect(wb.groups).toHaveLength(2)
    expect(wb.groups[1].columns[0].panes[0]).toMatchObject({ base: aim })
  })

  it('没有列数上限，连续加列仍保留每列最小宽度所需的权重', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'terminal', seq: 1 })
    wb = addColumn(wb)
    wb = addColumn(wb)
    wb = addColumn(wb)
    expect(wb.groups[0].columns).toHaveLength(4)
    expect(wb.groups[0].sizes).toEqual([1, 1, 1, 1])
  })
})

describe('placeSource', () => {
  it('一列最多上下两格，第三次上下投放替换目标格', () => {
    const baseA: BaseDir = { key: '/a', kind: 'workspace', path: '/a', label: 'a', projectName: 'A', machine: '' }
    const baseB: BaseDir = { key: '/b', kind: 'workspace', path: '/b', label: 'b', projectName: 'B', machine: '' }
    let wb = openTab(EMPTY_WORKBENCH, baseA, { kind: 'tui', taskId: 'A' })
    const groupId = wb.activeGroupId
    wb = placeSource(wb, { kind: 'new', base: baseB, content: { kind: 'tui', taskId: 'B' } }, {
      groupId, column: 0, row: 0, zone: 'bottom',
    })
    wb = placeSource(wb, { kind: 'new', base: baseA, content: { kind: 'tui', taskId: 'C' } }, {
      groupId, column: 0, row: 1, zone: 'bottom',
    })
    expect(wb.groups[0].columns[0].panes).toHaveLength(2)
    expect(wb.groups[0].columns[0].panes[1]).toMatchObject({ content: { kind: 'tui', taskId: 'C' } })
  })

  it('跨列 center 拖走 tab 后移除空源列并替换目标 pane', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
    wb = placeSource(wb, { kind: 'new', base: aim, content: { kind: 'terminal', seq: 1 } }, {
      groupId: 'g1', column: 0, row: 0, zone: 'right',
    })
    const tab = wb.groups[0].columns[0].panes[0]
    expect(tab).not.toBeNull()
    wb = placeSource(wb, { kind: 'tab', groupId: 'g1', tabId: tab!.id }, {
      groupId: 'g1', column: 1, row: 0, zone: 'center',
    })
    expect(wb.groups[0].columns).toHaveLength(1)
    expect(wb.groups[0].columns[0].panes).toHaveLength(1)
    expect(wb.groups[0].columns[0].panes[0]).toMatchObject({ content: { kind: 'tui', taskId: 'A' } })
  })

  it('源列仍有另一格时跨列拖动不递减未移除的源列索引', () => {
    const baseB: BaseDir = { key: '/b', kind: 'workspace', path: '/b', label: 'b', projectName: 'B', machine: '' }
    const baseC: BaseDir = { key: '/c', kind: 'workspace', path: '/c', label: 'c', projectName: 'C', machine: '' }
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
    wb = placeSource(wb, { kind: 'new', base: baseB, content: { kind: 'tui', taskId: 'B' } }, {
      groupId: 'g1', column: 0, row: 0, zone: 'bottom',
    })
    const moved = wb.groups[0].columns[0].panes[1]!
    wb = addColumn(wb)
    wb = placeSource(wb, { kind: 'new', base: baseC, content: { kind: 'tui', taskId: 'C' } }, {
      groupId: 'g1', column: 1, row: 0, zone: 'center',
    })
    wb = placeSource(wb, { kind: 'tab', groupId: 'g1', tabId: moved.id }, {
      groupId: 'g1', column: 1, row: 0, zone: 'center',
    })

    expect(wb.groups[0].columns).toHaveLength(2)
    expect(wb.groups[0].columns[0].panes[0]).toMatchObject({ content: { kind: 'tui', taskId: 'A' } })
    expect(wb.groups[0].columns[0].panes[1]).toBeNull()
    expect(wb.groups[0].columns[1].panes[0]).toMatchObject({ content: { kind: 'tui', taskId: 'B' } })
  })
})

describe('group lifecycle and projection', () => {
  it('关闭 pane 收列、收组，唯一组只重置为空组', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
    const groupId = wb.activeGroupId
    wb = addColumn(wb)
    wb = placeSource(wb, { kind: 'new', base: aim, content: { kind: 'tui', taskId: 'B' } }, {
      groupId, column: 1, row: 0, zone: 'center',
    })
    wb = closeTab(wb, groupId, 't1')
    expect(wb.groups[0].columns).toHaveLength(1)
    expect(wb.groups[0].columns[0].panes[0]).toMatchObject({ content: { kind: 'tui', taskId: 'B' } })
    wb = closeTab(wb, groupId, 't2')
    expect(wb.groups).toHaveLength(1)
    expect(wb.groups[0].columns[0].panes).toEqual([null])
    expect(wb.activeGroupId).toBe(groupId)
  })

  it('空 pane 也可关，关闭非法坐标不改变布局', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'tui', taskId: 'A' })
    wb = placeSource(wb, { kind: 'new', base: aim, content: { kind: 'tui', taskId: 'B' } }, {
      groupId: 'g1', column: 0, row: 0, zone: 'bottom',
    })
    wb = closePane(wb, 'g1', 0, 1)
    expect(wb.groups[0].columns[0].panes).toHaveLength(1)
    expect(wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'tui', taskId: 'A' })
    const before = wb
    expect(closePane(wb, 'g1', 9, 0)).toBe(before)
  })

  it('closeGroup 最后一组重置为空组', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'file', rel: 'a' })
    const groupId = wb.activeGroupId
    wb = closeGroup(wb, groupId)
    expect(wb.groups).toHaveLength(1)
    expect(wb.activeGroupId).toBe('g1')
  })

  it('openedWorkbenchItems 返回全局坐标与带 base 的内容', () => {
    const wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'file', rel: 'README.md' })
    expect(openedWorkbenchItems(wb)).toEqual([expect.objectContaining({
      tabId: 't1', groupId: 'g1', column: 0, row: 0, base: handoff, label: 'README.md',
    })])
  })
})

describe('resize and helpers', () => {
  it('只调整同一 group 的相邻 column', () => {
    let wb = openTab(EMPTY_WORKBENCH, handoff, { kind: 'file', rel: 'a' })
    wb = addColumn(wb)
    wb = resizeColumns(wb, 'g1', 0, 0.1, 0.2)
    expect(wb.groups[0].sizes[0]).toBeCloseTo(1.2)
    expect(wb.groups[0].sizes[1]).toBeCloseTo(0.8)
  })

  it('非法目标记录上下文并返回原对象', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(placeSource(EMPTY_WORKBENCH, { kind: 'new', base: handoff, content: { kind: 'blank' } }, {
      groupId: 'missing', column: 0, row: 0, zone: 'center',
    })).toBe(EMPTY_WORKBENCH)
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('空工作台、序号、宽度与标题保持契约', () => {
    expect(isEmptyWorkbench(EMPTY_WORKBENCH)).toBe(true)
    expect(nextTerminalSeq(EMPTY_WORKBENCH)).toBe(1)
    expect(availablePaneWidth(1060, [5, 5])).toBe(1050)
    expect(MIN_PANE_PX).toBe(240)
    expect(tabTitle({ kind: 'terminal', seq: 2 }, 'main')).toBe('bash · main (2)')
  })
})

describe('immutability', () => {
  it('写入不修改入参', () => {
    const before = EMPTY_WORKBENCH
    const after = openTab(before, handoff, { kind: 'blank' })
    expect(before.groups[0].columns[0].panes).toEqual([null])
    expect(after).not.toBe(before)
  })
})
