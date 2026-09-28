import { StrictMode } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import { usePoll } from './usePoll'

// —— B413 回归锁：轮询续帧（await 之后）的状态写入必须经 startTransition ——
// jsdom 观测不到 lane 调度（spec §7 承认真机权威），取行为等价的「写路捕获锁」：
// captureOnly=true 时 startTransition 只捕获回调不执行——
//   漏包（某写入在 transition 外）⇒ captured 为空或状态立即落地；
//   错包（refresh 误降级进 transition）⇒ refresh 反例断言红。
const { captured, mockState } = vi.hoisted(() => ({
  captured: [] as Array<() => void>,
  mockState: { captureOnly: false },
}))

vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>()
  return {
    ...actual,
    startTransition: (fn: () => void) => {
      if (mockState.captureOnly) { captured.push(fn); return }
      return actual.startTransition(fn)
    },
  }
})

describe('usePoll', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('立即首拉，然后按间隔续拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v1')
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.data).toBe('v1'))
    expect(fetcher).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('页面隐藏时停表，可见时立即补拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v')
    renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))

    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).toHaveBeenCalledTimes(1) // 停表期间一次都没打

    Object.defineProperty(document, 'hidden', { value: false, configurable: true })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2)) // 立即补拉
  })

  it('失败时保留上一次数据并标断开', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce('good').mockRejectedValue(new ApiError(0, '连不上'))
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.data).toBe('good'))
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    await waitFor(() => expect(result.current.disconnected).toBe(true))
    expect(result.current.data).toBe('good') // 断线不清空
    expect(result.current.errorText).toContain('连不上')
  })

  it('401 停表并落终止态', async () => {
    const fetcher = vi.fn().mockRejectedValue(new ApiError(401, '会话失效'))
    const { result } = renderHook(() => usePoll(fetcher, 1000))
    await waitFor(() => expect(result.current.sessionExpired).toBe(true))
    const calls = fetcher.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(fetcher).toHaveBeenCalledTimes(calls) // 不再重试
  })

  it('enabled=false 时一次都不拉', async () => {
    const fetcher = vi.fn().mockResolvedValue('v')
    renderHook(() => usePoll(fetcher, 1000, { enabled: false }))
    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).not.toHaveBeenCalled()
  })

  it('上一轮未结束时不启动重叠请求', async () => {
    let resolveFirst!: (value: string) => void
    const first = new Promise<string>((resolve) => { resolveFirst = resolve })
    const fetcher = vi.fn().mockReturnValueOnce(first).mockResolvedValue('v2')
    renderHook(() => usePoll(fetcher, 1000))

    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirst('v1')
      await first
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2))
  })

  it('请求一直不返回时达到上限并重试，恢复后显示新数据', async () => {
    let firstSignal: AbortSignal | undefined
    const fetcher = vi.fn()
      .mockImplementationOnce((signal: AbortSignal) => {
        firstSignal = signal
        return new Promise<string>(() => {})
      })
      .mockResolvedValue('recovered')
    const { result } = renderHook(() => usePoll(fetcher, 1000, { timeoutMs: 2500 }))

    await act(async () => { await vi.advanceTimersByTimeAsync(3000) })
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(firstSignal?.aborted).toBe(true)
    expect(result.current.data).toBe('recovered')
  })

  it('StrictMode effect 重放不丢失首拉结果', async () => {
    let resolveFirst!: (value: string) => void
    const first = new Promise<string>((resolve) => { resolveFirst = resolve })
    const fetcher = vi.fn().mockReturnValueOnce(first).mockResolvedValue('v2')
    const { result } = renderHook(() => usePoll(fetcher, 1000), { wrapper: StrictMode })

    expect(fetcher).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveFirst('v1')
      await first
    })
    await waitFor(() => expect(result.current.data).toBe('v1'))
  })

  // —— B413 捕获组：captureOnly 下只捕获 transition 回调不执行 ——
  // 锁「每处续帧写入都进 transition」，不锁回调拆并粒度（断言个数用 >=）。
  describe('续帧写入 transition 捕获锁', () => {
    beforeEach(() => {
      mockState.captureOnly = true
      captured.length = 0
    })
    afterEach(() => {
      mockState.captureOnly = false
    })

    it('首拉与定时续拉写入经 startTransition，flush 后数据落地', async () => {
      // 首拉先断线：把 disconnected 抬成 true，随后成功拉取的 setDisconnected(false)
      // 若漏包（写在 transition 外）会立即翻转——无泄漏断言才抓得住成功对的部分泄漏
      const fetcher = vi.fn()
        .mockRejectedValueOnce(new ApiError(0, '连不上'))
        .mockResolvedValue('v1')
      const { result } = renderHook(() => usePoll(fetcher, 1000))
      await act(async () => { await Promise.resolve() })
      // 首拉续帧（断线对）：写入被捕获，未直接落地（无 sync 泄漏）
      expect(captured.length).toBeGreaterThanOrEqual(1)
      expect(result.current.disconnected).toBe(false)
      // flush：断线落地
      act(() => { captured.splice(0).forEach((fn) => fn()) })
      expect(result.current.disconnected).toBe(true)
      // 定时续拉续帧（成功对）：同样进捕获队列，data 与 disconnected 不得被漏包提前落地
      await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
      expect(captured.length).toBeGreaterThanOrEqual(1)
      expect(result.current.data).toBeNull()
      expect(result.current.disconnected).toBe(true)
      // flush：数据与断线标志同帧落地
      act(() => { captured.splice(0).forEach((fn) => fn()) })
      expect(result.current.data).toBe('v1')
      expect(result.current.disconnected).toBe(false)
    })

    it('401 终止态写入经 startTransition，flush 后落终止态', async () => {
      const fetcher = vi.fn().mockRejectedValue(new ApiError(401, '会话失效'))
      const { result } = renderHook(() => usePoll(fetcher, 1000))
      await act(async () => { await Promise.resolve() })
      expect(captured.length).toBeGreaterThanOrEqual(1)
      expect(result.current.sessionExpired).toBe(false)
      act(() => { captured.splice(0).forEach((fn) => fn()) })
      expect(result.current.sessionExpired).toBe(true)
    })

    it('断线写入经 startTransition，flush 后标断开', async () => {
      const fetcher = vi.fn().mockRejectedValue(new ApiError(0, '连不上'))
      const { result } = renderHook(() => usePoll(fetcher, 1000))
      await act(async () => { await Promise.resolve() })
      expect(captured.length).toBeGreaterThanOrEqual(1)
      expect(result.current.disconnected).toBe(false)
      expect(result.current.errorText).toBe('')
      act(() => { captured.splice(0).forEach((fn) => fn()) })
      expect(result.current.disconnected).toBe(true)
    })

    it('refresh 的 nonce 写入保持 sync，不进 transition 捕获（反例）', async () => {
      const fetcher = vi.fn().mockResolvedValue('v1')
      const { result } = renderHook(() => usePoll(fetcher, 1000))
      await act(async () => { await Promise.resolve() })
      const before = captured.length
      act(() => { result.current.refresh() })
      // 同步断言（无 await）：setNonce 若被误包 startTransition，此刻已在捕获队列里
      expect(fetcher).toHaveBeenCalledTimes(2)
      expect(captured.length).toBe(before)
    })
  })
})
