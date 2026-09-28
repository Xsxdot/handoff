// webPrefs.test.ts —— Web 客户端偏好的加载、校验与持久化回归（B369.8）。

import { describe, expect, it, beforeEach, vi } from 'vitest'
import {
  PREFS_KEY, DEFAULT_WEB_PREFS, loadPrefs, savePrefs,
  type WebPrefs,
} from './webPrefs'
import { __resetWebPrefsForTest, useWebPrefs } from './useWebPrefs'
import { act, renderHook } from '@testing-library/react'

beforeEach(() => {
  localStorage.clear()
  __resetWebPrefsForTest()
})

const aPrefs: WebPrefs = { v: 1, sessionOpenMode: 'scene', badges: false }

describe('webPrefs', () => {
  it('无落盘时读出默认值（chat / badges 开）', () => {
    expect(loadPrefs()).toEqual(DEFAULT_WEB_PREFS)
  })

  it('save → load 读写回环', () => {
    savePrefs(aPrefs)
    expect(localStorage.getItem(PREFS_KEY)).not.toBeNull()
    expect(loadPrefs()).toEqual(aPrefs)
  })

  it('坏 JSON 回落默认值', () => {
    localStorage.setItem(PREFS_KEY, '{not json')
    expect(loadPrefs()).toEqual(DEFAULT_WEB_PREFS)
  })

  it.each([
    ['形状不认识（缺字段）', JSON.stringify({ v: 1, sessionOpenMode: 'chat' })],
    ['词表外值', JSON.stringify({ v: 1, sessionOpenMode: 'pop', badges: true })],
    ['badges 非布尔', JSON.stringify({ v: 1, sessionOpenMode: 'chat', badges: 'yes' })],
    ['版本不认识', JSON.stringify({ v: 2, sessionOpenMode: 'chat', badges: true })],
  ])('%s → 整份回默认', (_name, raw) => {
    localStorage.setItem(PREFS_KEY, raw)
    expect(loadPrefs()).toEqual(DEFAULT_WEB_PREFS)
  })

  it('localStorage 抛异常（隐私模式）时按默认显示，不向外抛', () => {
    const original = window.localStorage
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('denied') },
      setItem: () => { throw new Error('denied') },
    })
    expect(loadPrefs()).toEqual(DEFAULT_WEB_PREFS)
    expect(() => savePrefs(aPrefs)).not.toThrow()
    vi.unstubAllGlobals()
    void original
  })
})

describe('useWebPrefs', () => {
  it('初值取自 localStorage，改动落盘', () => {
    savePrefs(aPrefs)
    __resetWebPrefsForTest()
    const { result } = renderHook(() => useWebPrefs())
    expect(result.current[0]).toEqual(aPrefs)
    act(() => result.current[1]({ ...aPrefs, sessionOpenMode: 'chat' }))
    expect(result.current[0].sessionOpenMode).toBe('chat')
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).sessionOpenMode).toBe('chat')
  })

  it('两个挂载点共享同一份状态', () => {
    const a = renderHook(() => useWebPrefs())
    const b = renderHook(() => useWebPrefs())
    act(() => a.result.current[1]({ ...DEFAULT_WEB_PREFS, badges: false }))
    expect(b.result.current[0].badges).toBe(false)
  })

  it('__resetWebPrefsForTest 清掉内存单例与订阅；清盘后重新读盘回默认', () => {
    const a = renderHook(() => useWebPrefs())
    act(() => a.result.current[1]({ ...DEFAULT_WEB_PREFS, sessionOpenMode: 'scene' }))
    // 落盘是真实副作用：测试隔离 = 清盘（localStorage）+ 清内存单例，两者都做
    // 才回到「无落盘」基线；只 reset 不清盘会读回刚写入的值（by design）。
    localStorage.clear()
    __resetWebPrefsForTest()
    const b = renderHook(() => useWebPrefs())
    expect(b.result.current[0]).toEqual(DEFAULT_WEB_PREFS)
  })
})
