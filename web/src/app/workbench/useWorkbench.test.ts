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

  it('resize 只改当前基准的栏宽，切走再切回来比例还在', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    act(() => result.current.split())
    act(() => result.current.resize(0, 0.1, 0.2))
    const widened = result.current.wb.sizes[0]
    expect(widened).toBeGreaterThan(result.current.wb.sizes[1])

    act(() => result.current.select(wsB))
    expect(result.current.wb.sizes).toEqual([1])

    act(() => result.current.select(wsA))
    expect(result.current.wb.sizes[0]).toBeCloseTo(widened)
  })

  it('closeById 按 tabId 关闭，不需要调用方知道它在哪一组', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    act(() => result.current.split())
    act(() => result.current.open({ kind: 'file', rel: 'b.go' }, undefined, 1))
    const firstId = result.current.wb.groups[0].tabs[0].id
    const secondId = result.current.wb.groups[1].tabs[0].id

    act(() => result.current.closeById(secondId))
    expect(result.current.wb.groups.flatMap((g) => g.tabs).map((t) => t.id)).toEqual([firstId])
    expect(result.current.wb.groups[0].tabs[0].content).toEqual({ kind: 'file', rel: 'a.go' })
  })

  it('closeById 对不存在的 id 是空操作，不抛错', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.open({ kind: 'file', rel: 'a.go' }))
    const before = result.current.wb
    act(() => result.current.closeById('missing'))
    expect(result.current.wb).toBe(before)
  })
})

describe('restoreTerminal', () => {
  it('把会话恢复进目标目录的 tab 组，但**不切换当前基准**', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.select(wsA))
    act(() => result.current.restoreTerminal(wsB, 'sess-b'))
    // 选中的仍是 A：页面加载时恢复一批会话，不该把用户的选择拽到最后一条上
    expect(result.current.base?.key).toBe(wsA.key)
    expect(result.current.wb.groups[0].tabs).toHaveLength(0)
    act(() => result.current.select(wsB))
    expect(result.current.wb.groups[0].tabs[0].content).toMatchObject({
      kind: 'terminal', sessionId: 'sess-b',
    })
  })

  it('同一个会话恢复两次只得到一个 tab（dedupKey 生效）', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.restoreTerminal(wsA, 's'))
    act(() => result.current.restoreTerminal(wsA, 's'))
    act(() => result.current.select(wsA))
    expect(result.current.wb.groups[0].tabs).toHaveLength(1)
  })

  it('同目录两个会话各占一个 tab，序号递增', () => {
    const { result } = renderHook(() => useWorkbench())
    act(() => result.current.restoreTerminal(wsA, 's1'))
    act(() => result.current.restoreTerminal(wsA, 's2'))
    act(() => result.current.select(wsA))
    const seqs = result.current.wb.groups[0].tabs.map((t) => (t.content as { seq: number }).seq)
    expect(seqs).toEqual([1, 2])
  })
})
