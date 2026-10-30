import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { HOME_BASE, useWorkbench, type BaseDir } from './useWorkbench'

const a: BaseDir = {
  key: '/a', kind: 'workspace', path: '/a', label: 'main', projectName: 'handoff', machine: '',
}
const b: BaseDir = {
  key: '/b@linux-01', kind: 'workspace', path: '/b', label: 'eval', projectName: 'aim', machine: 'linux-01',
}

describe('useWorkbench', () => {
  it('初始选中为空，中央保留一个空 pane', () => {
    const { result } = renderHook(() => useWorkbench())
    expect(result.current.base).toBeNull()
    expect(result.current.wb.groups[0].columns[0].panes).toEqual([null])
  })

  it('切换左栏选中目录不会切换中央全局组，跨项目项仍在原 cell', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.open({ kind: 'tui', taskId: 'local' }))
    const groupId = result.current.wb.activeGroupId
    act(() => result.current.place({
      kind: 'new', base: b, content: { kind: 'terminal', seq: 1, rel: '' },
    }, { groupId, column: 0, row: 0, zone: 'right' }))
    act(() => result.current.select(b))
    expect(result.current.wb.activeGroupId).toBe(groupId)
    expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({
      base: { projectName: 'handoff' }, content: { kind: 'tui', taskId: 'local' },
    })
    expect(result.current.wb.groups[0].columns[1].panes[0]).toMatchObject({
      base: { projectName: 'aim', machine: 'linux-01' }, content: { kind: 'terminal', rel: '' },
    })
  })

  it('左栏未打开任务通过 openOrFocus 只新建一组，第二次点同一任务只聚焦', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.openOrFocus({ kind: 'tui', taskId: 'T1' }, a))
    const firstGroup = result.current.wb.activeGroupId
    act(() => result.current.openOrFocus({ kind: 'tui', taskId: 'T1' }, a))
    expect(result.current.wb.groups).toHaveLength(2)
    expect(result.current.wb.activeGroupId).toBe(firstGroup)
  })

  it('open 缺省使用选中 base，显式 base 只写 Tab 不强制切选中态', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.open({ kind: 'file', rel: 'a.ts' }))
    act(() => result.current.open({ kind: 'tui', taskId: 'remote' }, b))
    expect(result.current.base).toEqual(a)
    expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({ base: a })
    expect(result.current.wb.groups[0].columns[1].panes[0]).toMatchObject({ base: b })
  })

  it('openTerminal 序号递增且 home 可作为显式 Tab base', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.openTerminal())
    act(() => result.current.openTerminal(HOME_BASE))
    const panes = result.current.wb.groups[0].columns.flatMap((column) => column.panes).filter(Boolean)
    expect(panes.map((tab) => tab?.content.kind === 'terminal' ? tab.content.seq : -1)).toEqual([1, 2])
    expect(panes[1]?.base.kind).toBe('home')
  })

  it('openTerminalWithCommand 把服务端命令原样写进 terminal tab', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.openTerminalWithCommand('opencode --session sess-coord', a))
    const pane = result.current.wb.groups[0].columns[0].panes[0]
    expect(pane?.content).toEqual({
      kind: 'terminal', seq: 1, spawn: true, initCommand: 'opencode --session sess-coord',
    })
  })

  it('closePane 暴露空 pane 关闭并委托统一布局生命周期', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.open({ kind: 'tui', taskId: 'A' }, a))
    act(() => result.current.place({ kind: 'new', base: b, content: { kind: 'tui', taskId: 'B' } }, {
      groupId: 'g1', column: 0, row: 0, zone: 'bottom',
    }))
    act(() => result.current.closePane('g1', 0, 1))
    expect(result.current.wb.groups[0].columns[0].panes).toHaveLength(1)
    expect(result.current.wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'tui', taskId: 'A' })
  })

  it('closeById 反查全局坐标，resize 只作用于目标 group', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.open({ kind: 'file', rel: 'a.ts' }))
    const groupId = result.current.wb.activeGroupId
    act(() => result.current.place({ kind: 'new', base: a, content: { kind: 'file', rel: 'b.ts' } }, {
      groupId, column: 0, row: 0, zone: 'right',
    }))
    act(() => result.current.resize(groupId, 0, 0.1, 0.2))
    expect(result.current.wb.groups[0].sizes[0]).toBeGreaterThan(result.current.wb.groups[0].sizes[1])
    const id = result.current.wb.groups[0].columns[0].panes[0]!.id
    act(() => result.current.closeById(id))
    expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({ content: { kind: 'file', rel: 'b.ts' } })
    expect(result.current.wb.groups[0].sizes).toEqual([0.8])
  })

  it('restoreTerminal 不改选中目录；空工作台写入该 pty。hydrate 整体替换布局', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(a))
    act(() => result.current.restoreTerminal(b, 'S1'))
    expect(result.current.base).toEqual(a)
    expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({ base: b, content: { sessionId: 'S1' } })
    act(() => result.current.hydrate({
      groups: [{ id: 'g7', name: '恢复', autoName: false, columns: [{ panes: [{ id: 't7', base: b, content: { kind: 'tui', taskId: 'T7' } }] }], sizes: [1], focus: [0, 0] }],
      activeGroupId: 'g7',
    }))
    expect(result.current.base).toEqual(a)
    expect(result.current.wb.activeGroupId).toBe('g7')
    expect(result.current.wb.groups[0].columns[0].panes[0]).toMatchObject({ base: b })
  })

  it('restore lock closes every layout mutation route, including tab movement and PTY restore', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => {
      result.current.select(a)
      result.current.open({ kind: 'file', rel: 'one.ts' }, a)
      result.current.openInRightSplit({ kind: 'file', rel: 'two.ts' }, a)
      result.current.openInNewGroup({ kind: 'tui', taskId: 'T1' }, b)
    })
    const before = result.current.wb
    const firstTab = before.groups[0].columns[0].panes[0]!
    const firstGroup = before.groups[0].id
    const secondGroup = before.groups[1]
    act(() => {
      result.current.lockLayout()
      result.current.open({ kind: 'file', rel: 'three.ts' }, a)
      result.current.openInNewGroup({ kind: 'file', rel: 'new-group.ts' }, a)
      result.current.openInRightSplit({ kind: 'file', rel: 'right.ts' }, a)
      result.current.openOrFocus({ kind: 'session', sessionId: 'S1', title: 'session' }, a)
      result.current.openTerminal(a)
      result.current.openTerminalWithCommand('sh', a)
      result.current.close(firstGroup, firstTab.id)
      result.current.activate(firstGroup, firstTab.id)
      result.current.focusTab(b, firstGroup, firstTab.id)
      result.current.activateGroup(firstGroup)
      result.current.setContent(firstGroup, firstTab.id, { kind: 'file', rel: 'changed.ts' })
      result.current.addGroup()
      result.current.closeGroup(secondGroup.id)
      result.current.place({ kind: 'tab', groupId: firstGroup, tabId: firstTab.id }, {
        groupId: secondGroup.id, column: 0, row: 0, zone: 'center',
      })
      result.current.closePane(firstGroup, 0, 0)
      result.current.closeById(firstTab.id)
      result.current.resize(firstGroup, 0, 0.1, 0.2)
      result.current.restoreTerminal(b, 'pty-1')
    })
    expect(result.current.wb).toEqual(before)
    // Selection is separate from the authoritative layout and remains available.
    expect(result.current.base).toEqual(b)
    // Authoritative hydration is the one intentional bypass; it replaces state then unlocks.
    act(() => result.current.hydrate(before))
    expect(result.current.wb).toEqual(before)
    act(() => result.current.openInRightSplit({ kind: 'file', rel: 'after-restore.ts' }, a))
    expect(result.current.wb.groups[1].columns).toHaveLength(2)
  })

  it('右侧分栏落入刚建立的文件组，即使同一个文件已在旧组中', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.openInNewGroup({ kind: 'file', rel: 'README.md' }, a))
    const originalGroup = result.current.wb.activeGroupId
    act(() => result.current.openInNewGroup({ kind: 'file', rel: 'README.zh-CN.md' }, a))
    const destinationGroup = result.current.wb.activeGroupId
    expect(destinationGroup).not.toBe(originalGroup)

    act(() => result.current.openInRightSplit({ kind: 'file', rel: 'README.md' }, a))

    expect(result.current.wb.activeGroupId).toBe(destinationGroup)
    const active = result.current.wb.groups.find((group) => group.id === destinationGroup)!
    expect(active.columns).toHaveLength(2)
    expect(active.columns.map((column) => column.panes[0]?.content)).toEqual([
      { kind: 'file', rel: 'README.zh-CN.md' },
      { kind: 'file', rel: 'README.md' },
    ])
    expect(result.current.wb.groups.find((group) => group.id === originalGroup)?.columns).toHaveLength(1)
  })

  it('restoreTerminal 聚焦被点的 pty：离开上次打开的会话或终端，重复点击不复制 tab', () => {
    const { result } = renderHook(() => useWorkbench())
    const room = (id: string): BaseDir => ({
      key: `session:${id}`, kind: 'home', path: '', label: '会话', projectName: '', machine: '',
    })
    const focused = () => {
      const group = result.current.wb.groups.find((candidate) => candidate.id === result.current.wb.activeGroupId)
      if (!group) return null
      const [column, row] = group.focus
      return group.columns[column]?.panes[row]?.content ?? null
    }
    const ptyTabs = (sessionId: string) => result.current.wb.groups.flatMap((group) =>
      group.columns.flatMap((column) => column.panes.filter((tab) =>
        tab?.content.kind === 'terminal' && tab.content.sessionId === sessionId,
      )),
    )

    act(() => result.current.select(a))
    act(() => result.current.openOrFocus({ kind: 'session', sessionId: 'room-1', title: '先开的会话' }, room('room-1')))
    act(() => result.current.restoreTerminal(b, 'pty-old'))
    act(() => result.current.openOrFocus({ kind: 'session', sessionId: 'room-2', title: '后开的会话' }, room('room-2')))
    expect(focused()).toMatchObject({ kind: 'session', sessionId: 'room-2' })

    act(() => result.current.restoreTerminal(b, 'pty-old'))
    expect(focused()).toMatchObject({ kind: 'terminal', sessionId: 'pty-old' })
    expect(ptyTabs('pty-old')).toHaveLength(1)

    act(() => result.current.restoreTerminal(a, 'pty-new'))
    expect(focused()).toMatchObject({ kind: 'terminal', sessionId: 'pty-new' })

    act(() => result.current.restoreTerminal(b, 'pty-old'))
    expect(focused()).toMatchObject({ kind: 'terminal', sessionId: 'pty-old' })
    expect(ptyTabs('pty-old')).toHaveLength(1)
    expect(ptyTabs('pty-new')).toHaveLength(1)
    expect(result.current.base).toEqual(a)
  })
})
