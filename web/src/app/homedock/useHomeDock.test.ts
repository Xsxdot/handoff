import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useHomeDock } from './useHomeDock'

describe('useHomeDock', () => {
  it('新建终端：进列表、被激活、浮窗打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
    expect(result.current.windowOpen).toBe(true)
  })

  it('seq 递增，且关掉再开不复用旧号', () => {
    // why：标题里的编号是给人认的。复用号会让「home 2」在一次会话里指过两个
    // 不同的终端，用户按标题找会找错
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const second = result.current.tabs[1]
    act(() => result.current.closeTab(second.id))
    act(() => result.current.newTerminal())
    expect(result.current.tabs[1].seq).toBe(3)
  })

  it('收起浮窗不动 tabs——会话还在', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.tabs).toHaveLength(1)
  })

  it('关掉激活项时激活权交给剩下的第一个', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newTerminal())
    const active = result.current.activeId!
    act(() => result.current.closeTab(active))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
  })

  it('关掉最后一个：浮窗自动收起，activeId 归 null', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.closeTab(result.current.tabs[0].id))
    expect(result.current.tabs).toHaveLength(0)
    expect(result.current.windowOpen).toBe(false)
    expect(result.current.activeId).toBeNull()
  })

  it('activate 会把收起的浮窗重新打开', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.collapse())
    act(() => result.current.activate(result.current.tabs[0].id))
    expect(result.current.windowOpen).toBe(true)
  })

  it('adopt 收编既有会话，但不抢焦点也不弹窗', () => {
    // why：恢复是后台动作。页面一加载就弹出浮窗，等于替用户点了一下
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.adopt({ id: 'r1', kind: 'terminal', seq: 1, sessionId: 's1', machine: '' }))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.windowOpen).toBe(false)
  })

  it('newFile 建出一个 file 种类的 tab 并激活它、打开浮窗', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newFile('untitled-1.md'))
    expect(result.current.tabs).toHaveLength(1)
    expect(result.current.tabs[0]).toMatchObject({ kind: 'file', rel: 'untitled-1.md' })
    expect(result.current.activeId).toBe(result.current.tabs[0].id)
    expect(result.current.windowOpen).toBe(true)
  })

  it('终端与文件共用同一个只增不减的 seq 计数器，不会撞号', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newTerminal())
    act(() => result.current.newFile('untitled-1.md'))
    act(() => result.current.newTerminal())
    expect(result.current.tabs.map((t) => t.seq)).toEqual([1, 2, 3])
  })

  it('setDraft 把草稿寄存到 tab 上，切走再切回来还在', () => {
    const { result } = renderHook(() => useHomeDock())
    act(() => result.current.newFile('untitled-1.md'))
    const fileId = result.current.tabs[0].id
    act(() => result.current.newTerminal())
    act(() => result.current.setDraft(fileId, { draft: '临时内容', baseSha: 'sha-1' }))
    expect(result.current.tabs.find((t) => t.id === fileId)).toMatchObject({
      draft: '临时内容',
      baseSha: 'sha-1',
    })
    act(() => result.current.activate(fileId))
    expect(result.current.tabs.find((t) => t.id === fileId)).toMatchObject({
      draft: '临时内容',
      baseSha: 'sha-1',
    })
  })
})
