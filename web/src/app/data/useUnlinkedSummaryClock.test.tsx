import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useUnlinkedSummaryClock } from './useUnlinkedSummaryClock'

afterEach(() => vi.useRealTimers())

describe('useUnlinkedSummaryClock', () => {
  it('rechecks at the 30 second expiry and on visibility restoration', () => {
    vi.useFakeTimers()
    const observedAt = Date.parse('2026-09-26T12:00:00.000Z')
    vi.setSystemTime(observedAt)
    const { result } = renderHook(() => useUnlinkedSummaryClock(new Date(observedAt).toISOString()))
    expect(result.current).toBe(observedAt)

    act(() => { vi.advanceTimersByTime(30_001) })
    expect(result.current).toBe(observedAt + 30_001)

    vi.setSystemTime(observedAt + 60_000)
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    expect(result.current).toBe(observedAt + 60_000)
  })
})
