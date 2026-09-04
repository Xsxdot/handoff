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

  it('restoreTerminal 不 select、不抢 active group，hydrate 整体替换布局', () => {
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
})
