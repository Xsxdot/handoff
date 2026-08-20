import { StrictMode } from 'react'
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePtyRestore } from './usePtyRestore'

const fetchPtySessions = vi.fn()
vi.mock('../../api/client', () => ({ fetchPtySessions: () => fetchPtySessions() }))

function session(over: Record<string, unknown> = {}) {
  return {
    id: 's1', machine: '', base_path: '/home/dev/handoff', base_kind: 'workspace',
    shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40,
    attached: 0, pid: 1, bytes_out: 0, foreground: false, ...over,
  }
}

beforeEach(() => vi.clearAllMocks())

describe('usePtyRestore', () => {
  it('把工作树会话恢复到与左栏同一个基准键上——否则会长出第二个「同一个目录」', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    const [base, id] = restore.mock.calls[0]
    // key 必须与 ProjectTree.workspaceBase 一样是绝对路径
    expect(base).toMatchObject({ key: '/home/dev/handoff', kind: 'workspace', path: '/home/dev/handoff', machine: '' })
    expect(id).toBe('s1')
  })

  it('本机 home 会话落在 HOME_BASE 上', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session({ base_kind: 'home', base_path: '/Users/dev' })] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    expect(restore.mock.calls[0][0]).toMatchObject({ key: '~', kind: 'home', path: '~' })
  })

  it('远端 home 会话不与本机 home 混在一起', async () => {
    fetchPtySessions.mockResolvedValue({
      sessions: [session({ base_kind: 'home', machine: 'devbox' })],
    })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    expect(restore.mock.calls[0][0].key).toBe('~@devbox')
  })

  it('已退出的会话不恢复——tab 里放一个死会话只会让人以为它还能用', async () => {
    fetchPtySessions.mockResolvedValue({
      sessions: [session({ id: 'dead', exit_code: 0 }), session({ id: 'alive' })],
    })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalledTimes(1))
    expect(restore.mock.calls[0][1]).toBe('alive')
  })

  it('协议不兼容的活会话照样恢复，但带上降级标记而不在列表阶段丢掉', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session({ id: 'old-v99', incompatible: true })] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    expect(restore.mock.calls[0][1]).toBe('old-v99')
    expect(restore.mock.calls[0][2]).toBe(true)
  })

  it('只跑一次：重渲染不会把同一批会话反复往回灌', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    const { rerender } = renderHook(() => usePtyRestore(restore))
    await waitFor(() => expect(restore).toHaveBeenCalled())
    rerender()
    rerender()
    expect(fetchPtySessions).toHaveBeenCalledTimes(1)
  })

  it('拉不到列表时给出原文，不静默', async () => {
    fetchPtySessions.mockRejectedValue(new Error('会话过期'))
    const { result } = renderHook(() => usePtyRestore(vi.fn()))
    await waitFor(() => expect(result.current.error).toContain('会话过期'))
  })

  it('StrictMode 双调用 effect 时仍然恢复——上一轮的 cleanup 不该取消这一轮的请求', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore), { wrapper: StrictMode })
    await waitFor(() => expect(restore).toHaveBeenCalledTimes(1))
    expect(fetchPtySessions).toHaveBeenCalledTimes(1)
  })

  it('真的卸载之后不再回灌——组件都没了还往里写就是脏写', async () => {
    let resolve!: (v: unknown) => void
    fetchPtySessions.mockReturnValue(new Promise((r) => { resolve = r }))
    const restore = vi.fn()
    const { unmount } = renderHook(() => usePtyRestore(restore))
    unmount()
    resolve({ sessions: [session()] })
    await Promise.resolve()
    expect(restore).not.toHaveBeenCalled()
  })
})
