import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePtySupport } from './usePtySupport'

const fetchMachines = vi.fn()
vi.mock('../../api/client', () => ({ fetchMachines: () => fetchMachines() }))

beforeEach(() => vi.clearAllMocks())

describe('usePtySupport', () => {
  it('三态原样转达：true / false / 没上报', async () => {
    fetchMachines.mockResolvedValue({
      machines: [
        { name: '', pty_supported: true },
        { name: 'winbox', pty_supported: false },
        { name: 'oldbox' }, // 老 agentd：字段缺席
      ],
    })
    const { result } = renderHook(() => usePtySupport())
    await waitFor(() => expect(result.current.supported('')).toBe(true))
    expect(result.current.supported('winbox')).toBe(false)
    // 缺席必须是 null 而不是 false：老 agentd 很可能是支持的，只是没上报
    expect(result.current.supported('oldbox')).toBeNull()
  })

  it('还没拉到、或机器不在列表里，一律 null（不猜）', async () => {
    fetchMachines.mockResolvedValue({ machines: [] })
    const { result } = renderHook(() => usePtySupport())
    expect(result.current.supported('')).toBeNull()
    await waitFor(() => expect(fetchMachines).toHaveBeenCalled())
    expect(result.current.supported('ghost')).toBeNull()
  })

  it('拉取失败时能力全 null，且给出原文', async () => {
    fetchMachines.mockRejectedValue(new Error('连不上'))
    const { result } = renderHook(() => usePtySupport())
    await waitFor(() => expect(result.current.error).toContain('连不上'))
    expect(result.current.supported('')).toBeNull()
  })
})
