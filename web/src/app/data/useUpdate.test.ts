// 更新数据流的契约测试。
//
// 职责：钉住桌面状态的 204 语义，避免浏览器把「没有薄壳」误当成空对象。
// 边界：只测 API 解码契约；轮询节奏由 usePoll 的既有测试覆盖。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchDesktopState } from '../../api/client'

afterEach(() => vi.unstubAllGlobals())

describe('fetchDesktopState', () => {
  it('把 204 解成 null', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    await expect(fetchDesktopState()).resolves.toBeNull()
  })
})
