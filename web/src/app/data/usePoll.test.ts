import { StrictMode } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import { usePoll } from './usePoll'

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
})
