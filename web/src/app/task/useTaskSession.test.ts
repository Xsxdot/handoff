import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { ApiError } from '../../api/client'
import type { TaskDetail } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchTaskDetail: vi.fn(), replyTicket: vi.fn() }
})
vi.mock('../../api/ws', () => ({
  connectEvents: vi.fn(() => ({ close: vi.fn() })),
}))

const { fetchTaskDetail, replyTicket } = await import('../../api/client')
const { connectEvents } = await import('../../api/ws')
const { useTaskSession } = await import('./useTaskSession')

function detailOf(state: string): TaskDetail {
  return {
    task: { id: 'T1', name: 'demo', state, branch: 'x', repo: '/r', executor: 'opencode' } as TaskDetail['task'],
    pending_tickets: [],
    recent_events: [{ seq: 7, task_id: 'T1', type: 'completed', ts: '2026-08-12T00:00:00Z', payload: {} }],
  } as unknown as TaskDetail
}

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => {
  vi.useRealTimers()
  vi.mocked(fetchTaskDetail).mockReset()
  vi.mocked(replyTicket).mockReset()
  vi.mocked(connectEvents).mockClear()
})

describe('useTaskSession', () => {
  it('首拉后以 recent_events 打底，并以其最大 seq 为 WS 起点', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.detail).not.toBeNull())
    expect(result.current.events.map((e) => e.seq)).toEqual([7])
    await waitFor(() => expect(connectEvents).toHaveBeenCalled())
    expect(vi.mocked(connectEvents).mock.calls[0][0]).toMatchObject({ taskId: 'T1', fromSeq: 7 })
  })

  it('4s 轮询续拉详情', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(fetchTaskDetail).toHaveBeenCalledTimes(1))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000)
    })
    expect(fetchTaskDetail).toHaveBeenCalledTimes(2)
  })

  it('首拉失败落终止态 loadError；已有数据后再失败只标已断开并保留数据', async () => {
    vi.mocked(fetchTaskDetail).mockRejectedValueOnce(new ApiError(500, 'agentd 挂了'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.loadError).toBe('agentd 挂了'))
    expect(result.current.disconnected).toBe(false)

    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.detail).not.toBeNull())

    vi.mocked(fetchTaskDetail).mockRejectedValue(new ApiError(500, '连接被拒'))
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.disconnected).toBe(true))
    expect(result.current.disconnectReason).toBe('连接被拒')
    expect(result.current.detail).not.toBeNull() // 保留最后拿到的数据
  })

  it('401 收敛到会话已失效并停轮询', async () => {
    vi.mocked(fetchTaskDetail).mockRejectedValue(new ApiError(401, '会话已失效'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.sessionExpired).toBe(true))
    const calls = vi.mocked(fetchTaskDetail).mock.calls.length
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000)
    })
    expect(vi.mocked(fetchTaskDetail).mock.calls.length).toBe(calls)
  })

  it('WS 的 onTerminal 同样落会话已失效', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('running'))
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(connectEvents).toHaveBeenCalled())
    act(() => vi.mocked(connectEvents).mock.calls[0][0].onTerminal?.())
    expect(result.current.sessionExpired).toBe(true)
  })

  it('应答工单后立即补拉', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue(detailOf('waiting_answer'))
    vi.mocked(replyTicket).mockResolvedValue({ ok: true } as never)
    const { result } = renderHook(() => useTaskSession('T1'))
    await waitFor(() => expect(result.current.detail).not.toBeNull())
    const before = vi.mocked(fetchTaskDetail).mock.calls.length
    await act(async () => {
      await result.current.replyToTicket({ id: 'K1' } as never, '批了')
    })
    expect(replyTicket).toHaveBeenCalledWith('T1', { ticket_id: 'K1', answer: '批了' })
    expect(vi.mocked(fetchTaskDetail).mock.calls.length).toBeGreaterThan(before)
  })

  it('id 为空时什么都不做', () => {
    renderHook(() => useTaskSession(undefined))
    expect(fetchTaskDetail).not.toHaveBeenCalled()
    expect(connectEvents).not.toHaveBeenCalled()
  })
})
