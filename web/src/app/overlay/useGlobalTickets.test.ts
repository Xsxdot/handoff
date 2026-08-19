import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { Task } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchTaskDetail: vi.fn() }
})
const { fetchTaskDetail } = await import('../../api/client')
const { useGlobalTickets } = await import('./useGlobalTickets')

afterEach(() => vi.mocked(fetchTaskDetail).mockReset())

const waiting = (id: string, work_dir = '') => ({ id, state: 'waiting_answer', name: id, work_dir }) as unknown as Task
const running = (id: string) => ({ id, state: 'running', name: id }) as unknown as Task

describe('useGlobalTickets', () => {
  it('只对 waiting_answer 的任务取详情', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }] } as never)
    const { result } = renderHook(() => useGlobalTickets([waiting('A'), running('B')]))
    await waitFor(() => expect(result.current.count).toBe(1))
    expect(fetchTaskDetail).toHaveBeenCalledTimes(1)
    expect(fetchTaskDetail).toHaveBeenCalledWith('A')
  })

  it('一个任务挂多张工单时全部计入', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }, { id: 'K2' }] } as never)
    const { result } = renderHook(() => useGlobalTickets([waiting('A')]))
    await waitFor(() => expect(result.current.count).toBe(2))
    expect(result.current.items.map((i) => i.ticket.id)).toEqual(['K1', 'K2'])
    expect(result.current.items[0].task.id).toBe('A')
  })

  it('某个任务取详情失败不影响其余任务', async () => {
    vi.mocked(fetchTaskDetail).mockImplementation(async (id: string) => {
      if (id === 'A') throw new Error('炸了')
      return { pending_tickets: [{ id: 'K9' }] } as never
    })
    const { result } = renderHook(() => useGlobalTickets([waiting('A'), waiting('B')]))
    await waitFor(() => expect(result.current.count).toBe(1))
    expect(result.current.items[0].ticket.id).toBe('K9')
  })

  it('没有 waiting_answer 任务时不发请求，count 为 0', () => {
    renderHook(() => useGlobalTickets([running('B')]))
    expect(fetchTaskDetail).not.toHaveBeenCalled()
  })

  it('waiting_answer 的任务集合没变时不重复取数', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }] } as never)
    const { rerender, result } = renderHook(({ ts }: { ts: Task[] }) => useGlobalTickets(ts), {
      initialProps: { ts: [waiting('A'), running('B')] },
    })
    await waitFor(() => expect(result.current.count).toBe(1))
    // 任务流每 2.5s 换一个新数组，但 waiting_answer 的 id 集合没变
    rerender({ ts: [waiting('A'), running('B')] })
    expect(fetchTaskDetail).toHaveBeenCalledTimes(1)
  })
})

describe('byWorkDir', () => {
  it('按 work_dir 归集工单张数，一个任务多张工单要累加', async () => {
    vi.mocked(fetchTaskDetail).mockImplementation(async (id: string) => {
      if (id === 'T1') return { pending_tickets: [{ id: 'K1' }, { id: 'K2' }] } as never
      return { pending_tickets: [{ id: 'K3' }] } as never
    })
    const { result } = renderHook(() => useGlobalTickets([
      waiting('T1', '/r/a'),
      waiting('T2', '/r/b'),
    ]))
    await waitFor(() => expect(result.current.byWorkDir.get('/r/a')).toBe(2))
    expect(result.current.byWorkDir.get('/r/b')).toBe(1)
  })

  it('空 work_dir 的任务不进表——它归主目录，而这里不知道谁是主目录', async () => {
    vi.mocked(fetchTaskDetail).mockResolvedValue({ pending_tickets: [{ id: 'K1' }] } as never)
    const { result } = renderHook(() => useGlobalTickets([waiting('T1')]))
    await waitFor(() => expect(result.current.count).toBe(1))
    expect(result.current.byWorkDir.size).toBe(0)
  })

  it('没有挂起工单时是空表，不是 undefined', () => {
    const { result } = renderHook(() => useGlobalTickets([running('B')]))
    expect(result.current.byWorkDir).toBeInstanceOf(Map)
    expect(result.current.byWorkDir.size).toBe(0)
  })
})
