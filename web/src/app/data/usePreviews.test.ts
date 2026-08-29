import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchPreviews, openPreview } from '../../api/preview'
import { connectPreviewEvents, type PreviewWsOptions } from '../../api/ws'
import type { PreviewEvent, PreviewSession } from '../../api/types'
import { normalizePreviewOrigin, usePreviews } from './usePreviews'

vi.mock('../../api/preview', () => ({
  fetchPreviews: vi.fn(),
  openPreview: vi.fn(),
}))

vi.mock('../../api/ws', () => ({
  connectPreviewEvents: vi.fn(),
}))

const session = (over: Partial<PreviewSession> = {}): PreviewSession => ({
  id: 'p1', entry_url: 'http://localhost:5173', cwd: '/repo', created_at: '', ttl_seconds: 7200, ...over,
})

describe('usePreviews', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchPreviews).mockResolvedValue({ sessions: [session()], machines: [] })
    vi.mocked(openPreview).mockResolvedValue({ opened: true })
  })

  it('首次拉取 all 并订阅本机 preview WS，created/closed 按 machine+id 投影', async () => {
    const options: PreviewWsOptions[] = []
    const close = vi.fn()
    vi.mocked(connectPreviewEvents).mockImplementation((next) => {
      options.push(next)
      return { close }
    })
    const { result, unmount } = renderHook(() => usePreviews())
    await waitFor(() => expect(result.current.data?.sessions).toHaveLength(1))
    expect(fetchPreviews).toHaveBeenCalledWith('all')
    expect(options).toHaveLength(1)
    expect(options[0]).not.toHaveProperty('machine')
    await act(async () => { await result.current.open('p1', '') })
    expect(result.current.isOpen('p1', '')).toBe(true)

    act(() => options[0].onEvent({
      type: 'preview.created', session: session({ id: 'p2', machine: 'devbox' }), machine: 'devbox',
    }))
    expect(result.current.data?.sessions.map((x) => `${x.machine ?? ''}:${x.id}`)).toContain('devbox:p2')
    act(() => options[0].onEvent({
      type: 'preview.closed', session: session({ id: 'p2', machine: 'devbox' }), machine: 'devbox',
    }))
    expect(result.current.data?.sessions.map((x) => `${x.machine ?? ''}:${x.id}`)).not.toContain('devbox:p2')
    act(() => options[0].onEvent({
      type: 'preview.closed', session: session({ id: 'p1' }),
    }))
    expect(result.current.isOpen('p1', '')).toBe(false)
    unmount()
    expect(close).toHaveBeenCalledTimes(1)
  })

  it('同一 session 并发 open 去重，成功后才进入 isOpen；失败暴露错误且不留态', async () => {
    let finish!: (value: { opened: boolean }) => void
    vi.mocked(openPreview).mockImplementation(() => new Promise((resolve) => { finish = resolve }))
    vi.mocked(connectPreviewEvents).mockReturnValue({ close: vi.fn() })
    const { result } = renderHook(() => usePreviews())
    await waitFor(() => expect(result.current.data).not.toBeNull())
    let first!: Promise<void>
    let second!: Promise<void>
    act(() => {
      first = result.current.open('p1', '')
      second = result.current.open('p1', '')
    })
    expect(openPreview).toHaveBeenCalledTimes(1)
    expect(result.current.isOpen('p1', '')).toBe(false)
    finish({ opened: true })
    await act(async () => { await Promise.all([first, second]) })
    expect(result.current.isOpen('p1', '')).toBe(true)

    vi.mocked(openPreview).mockRejectedValueOnce(new Error('desktop unavailable'))
    await act(async () => { await expect(result.current.open('p2', 'devbox')).rejects.toThrow('desktop unavailable') })
    expect(result.current.isOpen('p2', 'devbox')).toBe(false)
    expect(result.current.error).toContain('desktop unavailable')
  })

  it('未知 preview 事件不改变投影', async () => {
    const options: PreviewWsOptions[] = []
    vi.mocked(connectPreviewEvents).mockImplementation((next) => {
      options.push(next)
      return { close: vi.fn() }
    })
    const { result } = renderHook(() => usePreviews())
    await waitFor(() => expect(result.current.data).not.toBeNull())
    const before = result.current.data
    act(() => options[0].onEvent({ type: 'other' as PreviewEvent['type'], session: session({ id: 'unknown' }) }))
    expect(result.current.data).toEqual(before)
  })

  it('origin 归一化与项目身份同口径，保留路径大小写', () => {
    expect(normalizePreviewOrigin('git@github.com:Xsxdot/handoff.git')).toBe('github.com/Xsxdot/handoff')
    expect(normalizePreviewOrigin(' HTTPS://github.com/Xsxdot/handoff/ ')).toBe('github.com/Xsxdot/handoff')
  })
})
