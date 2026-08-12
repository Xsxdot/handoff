import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { HOME_BASE, useWorkbench, type BaseDir } from './useWorkbench'

const wsA: BaseDir = {
  key: '/home/dev/handoff',
  kind: 'workspace',
  path: '/home/dev/handoff',
  label: 'main',
  projectName: 'handoff',
  machine: '',
}
const wsB: BaseDir = {
  key: '/home/dev/.handoff/worktrees/w1',
  kind: 'workspace',
  path: '/home/dev/.handoff/worktrees/w1',
  label: 'w1',
  projectName: 'handoff',
  machine: '',
}

describe('useWorkbench', () => {
  it('初始未选中任何目录，tab 组为空', () => {
    const { result } = renderHook(() => useWorkbench())
    expect(result.current.base).toBeNull()
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
  })

  it('切目录时中央整组 tab 一起切换，切回来原样恢复', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'go.mod' }))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)

    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
    act(() => result.current.open({ kind: 'tui', taskId: 'T1' }))

    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'file', rel: 'go.mod' })

    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T1' })
  })

  it('open 带基准目录参数时先切过去再开（左栏点任务、看板点卡片走这条）', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'tui', taskId: 'T9' }, wsB))
    expect(result.current.base?.key).toBe(wsB.key)
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'tui', taskId: 'T9' })
  })

  it('openTerminal 自动取下一个序号', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.openTerminal())
    act(() => result.current.openTerminal())
    const seqs = result.current.wb.groups[0].tabs.map((t) =>
      t.content.kind === 'terminal' ? t.content.seq : -1,
    )
    expect(seqs).toEqual([1, 2])
  })

  it('home 是独立的一套 tab 组，与工作树互不干扰', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.openTerminal())
    act(() => result.current.openTerminal(HOME_BASE))
    expect(result.current.base?.kind).toBe('home')
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
  })

  it('未选中目录时 open 是空操作，不静默造一个基准出来', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    expect(result.current.base).toBeNull()
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
  })
})
