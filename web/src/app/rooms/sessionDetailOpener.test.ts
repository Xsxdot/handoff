// sessionDetailOpener.test.ts —— 会话详情开启注册表：同会话全局一 tab，
// 按 sessionId 精确投递；未注册 no-op；注销只认身份（tab 重建不带走后来者）。
import { describe, expect, it, vi } from 'vitest'
import { openSessionDetail, registerSessionDetailOpener } from './sessionDetailOpener'

describe('sessionDetailOpener', () => {
  it('注册后可按 sessionId 开启；未注册 no-op 不抛', () => {
    const open = vi.fn()
    const unregister = registerSessionDetailOpener('session:7', open)
    openSessionDetail('session:7')
    expect(open).toHaveBeenCalledTimes(1)
    openSessionDetail('session:other')
    expect(open).toHaveBeenCalledTimes(1)
    unregister()
    openSessionDetail('session:7')
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('后注册覆盖先注册；先注册者的注销不得带走后来者', () => {
    const first = vi.fn()
    const second = vi.fn()
    const unregisterFirst = registerSessionDetailOpener('session:8', first)
    registerSessionDetailOpener('session:8', second)
    openSessionDetail('session:8')
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
    unregisterFirst()
    openSessionDetail('session:8')
    expect(second).toHaveBeenCalledTimes(2)
  })
})
