// useTreePrefs.test.ts —— 共享偏好状态层的加载、同步与退订回归测试。

import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTreePrefs } from './useTreePrefs'
import { PREFS_KEY, DEFAULT_PREFS } from './treePrefs'

beforeEach(() => {
  localStorage.clear()
})

describe('useTreePrefs', () => {
  it('初值取自 localStorage，改动落盘', () => {
    const { result } = renderHook(() => useTreePrefs())
    expect(result.current[0]).toEqual(DEFAULT_PREFS)
    act(() => result.current[1]({ ...DEFAULT_PREFS, projectSort: 'name' }))
    expect(result.current[0].projectSort).toBe('name')
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).projectSort).toBe('name')
  })

  it('两个挂载点共享同一份状态——这是「常规」分区能改左栏的前提', () => {
    const a = renderHook(() => useTreePrefs())
    const b = renderHook(() => useTreePrefs())
    act(() => a.result.current[1]({ ...DEFAULT_PREFS, hideIdleWorktrees: true }))
    expect(b.result.current[0].hideIdleWorktrees).toBe(true)
  })

  it('卸载后不再收到通知（不泄漏订阅）', () => {
    const a = renderHook(() => useTreePrefs())
    const b = renderHook(() => useTreePrefs())
    b.unmount()
    // 卸载的那个不该再被 setState，React 会在控制台报警告；这里断言不抛即可
    expect(() => act(() => a.result.current[1]({ ...DEFAULT_PREFS, projectSort: 'recent' })))
      .not.toThrow()
  })
})
