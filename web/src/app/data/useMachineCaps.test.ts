import { StrictMode } from 'react'
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useMachineCaps } from './useMachineCaps'

const fetchMachines = vi.fn()
vi.mock('../../api/client', () => ({ fetchMachines: () => fetchMachines() }))

beforeEach(() => vi.clearAllMocks())

describe('useMachineCaps', () => {
  it('三态原样转达：true / false / 没上报', async () => {
    fetchMachines.mockResolvedValue({
      machines: [
        { name: '', pty_supported: true, scratch_root: '/data/scratch' },
        { name: 'winbox', pty_supported: false },
        { name: 'oldbox' }, // 老 agentd：字段缺席
      ],
    })
    const { result } = renderHook(() => useMachineCaps())
    await waitFor(() => expect(result.current.pty('')).toBe(true))
    expect(result.current.pty('winbox')).toBe(false)
    // 缺席必须是 null 而不是 false：老 agentd 很可能是支持的，只是没上报
    expect(result.current.pty('oldbox')).toBeNull()
    expect(result.current.scratchRoot('')).toBe('/data/scratch')
    expect(result.current.scratchRoot('oldbox')).toBe('')
  })

  it('还没拉到、或机器不在列表里，一律 null（不猜）', async () => {
    fetchMachines.mockResolvedValue({ machines: [] })
    const { result } = renderHook(() => useMachineCaps())
    expect(result.current.pty('')).toBeNull()
    await waitFor(() => expect(fetchMachines).toHaveBeenCalled())
    expect(result.current.pty('ghost')).toBeNull()
  })

  it('拉取失败时能力全 null，且给出原文', async () => {
    fetchMachines.mockRejectedValue(new Error('连不上'))
    const { result } = renderHook(() => useMachineCaps())
    await waitFor(() => expect(result.current.error).toContain('连不上'))
    expect(result.current.pty('')).toBeNull()
  })

  it('StrictMode 双调用 effect 时能力表仍然落表——否则三态门永远停在 null', async () => {
    fetchMachines.mockResolvedValue({ machines: [{ name: 'devbox', pty_supported: false }] })
    const { result } = renderHook(() => useMachineCaps(), { wrapper: StrictMode })
    await waitFor(() => expect(result.current.pty('devbox')).toBe(false))
    expect(fetchMachines).toHaveBeenCalledTimes(1)
  })

  it('reveal 能力位与 pty 各自独立，一次请求两张表', async () => {
    fetchMachines.mockResolvedValue({
      machines: [
        { name: '', pty_supported: true, reveal_supported: false },
        { name: 'devbox', pty_supported: true },
      ],
    })
    const { result } = renderHook(() => useMachineCaps())
    await waitFor(() => expect(result.current.reveal('')).toBe(false))
    expect(result.current.pty('')).toBe(true)
    // devbox 没上报 reveal → null（**不是 false**）
    expect(result.current.reveal('devbox')).toBeNull()
    expect(fetchMachines).toHaveBeenCalledTimes(1)
  })

  it('启动项能力位保留 true / false / 缺席三态', async () => {
    fetchMachines.mockResolvedValue({
      machines: [
        { name: 'new', launchers_supported: true },
        { name: 'old', launchers_supported: false },
        { name: 'unknown' },
      ],
    })
    const { result } = renderHook(() => useMachineCaps())
    await waitFor(() => expect(result.current.launchers('new')).toBe(true))
    expect(result.current.launchers('old')).toBe(false)
    expect(result.current.launchers('unknown')).toBeNull()
  })
})
