import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EMPTY_WORKBENCH, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import type { DockSnapshot } from '../homedock/dockPersist'
import { GLOBAL_WORKBENCH_KEY, encodeWorkbench } from './persist'

vi.mock('../../api/client', () => ({
  fetchWorkbenchState: vi.fn(),
  fetchPtySessions: vi.fn(),
  putWorkbenchBase: vi.fn(() => Promise.resolve()),
  putWorkbenchSelected: vi.fn(() => Promise.resolve()),
  putWorkbenchDock: vi.fn(() => Promise.resolve()),
}))

import { fetchPtySessions, fetchWorkbenchState, putWorkbenchBase, putWorkbenchDock, putWorkbenchSelected } from '../../api/client'
import { useWorkbenchSync, type WorkbenchSyncDeps } from './useWorkbenchSync'

const base: BaseDir = { key: '/a', kind: 'workspace', path: '/a', label: 'main', projectName: 'handoff', machine: '' }
const wb: Workbench = {
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [{ id: 't1', base, content: { kind: 'tui', taskId: 'T1' } }] }], sizes: [1], focus: [0, 0] }],
}
const dock: DockSnapshot = { tabs: [], activeId: null, windowOpen: false, geom: { x: 1, y: 1, w: 620, h: 340 }, maximized: false }

function deps(over: Partial<WorkbenchSyncDeps> = {}): WorkbenchSyncDeps {
  return {
    workbench: EMPTY_WORKBENCH,
    selectedKey: '',
    dockSnapshot: dock,
    hydrateWorkbench: vi.fn(),
    hydrateDock: vi.fn(),
    adoptDockTab: vi.fn(),
    ...over,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => vi.useRealTimers())

describe('useWorkbenchSync', () => {
  async function mounted(initial: WorkbenchSyncDeps) {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const h = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: initial })
    await waitFor(() => expect(initial.hydrateWorkbench).toHaveBeenCalled())
    return h
  }

  it('双请求到齐后 hydrate 一个 global workbench，sessions 使用 all scope', async () => {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({
      selected: '/a', dock: '', bases: [{ base_key: GLOBAL_WORKBENCH_KEY, payload: encodeWorkbench(wb), updated_at: 1 }],
    })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const d = deps()
    const { result } = renderHook(() => useWorkbenchSync(d))
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledWith(wb))
    expect(fetchPtySessions).toHaveBeenCalledWith('all')
    expect(result.current.restoredSelected).toBe('/a')
    expect(result.current.error).toBe('')
  })

  it('恢复失败不 hydrate，后续变更也不写回', async () => {
    vi.mocked(fetchWorkbenchState).mockRejectedValue(new Error('boom'))
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const d = deps()
    const { result, rerender } = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: d })
    await waitFor(() => expect(result.current.error).toContain('boom'))
    rerender(deps({ workbench: wb }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(d.hydrateWorkbench).not.toHaveBeenCalled()
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('只 PUT global key；旧行删除，空工作台发送 null', async () => {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({
      selected: '', dock: '', bases: [
        { base_key: GLOBAL_WORKBENCH_KEY, payload: encodeWorkbench(EMPTY_WORKBENCH), updated_at: 1 },
        { base_key: '/legacy', payload: 'old', updated_at: 2 },
      ],
    })
    const d = deps()
    const { rerender } = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: d })
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalled())
    rerender(deps({ workbench: wb }))
    await act(async () => { vi.advanceTimersByTime(700) })
    await waitFor(() => expect(putWorkbenchBase).toHaveBeenCalled())
    expect(putWorkbenchBase).toHaveBeenCalledWith(GLOBAL_WORKBENCH_KEY, encodeWorkbench(wb))
    expect(putWorkbenchBase).toHaveBeenCalledWith('/legacy', null)
    vi.mocked(putWorkbenchBase).mockClear()
    rerender(deps({ workbench: EMPTY_WORKBENCH }))
    await act(async () => { vi.advanceTimersByTime(700) })
    expect(putWorkbenchBase).toHaveBeenCalledWith(GLOBAL_WORKBENCH_KEY, null)
  })

  it('selected 与 dock 仍走各自写入口', async () => {
    const d = await mounted(deps())
    d.rerender(deps({ selectedKey: '/a', dockSnapshot: { ...dock, windowOpen: true } }))
    await act(async () => { vi.advanceTimersByTime(700) })
    expect(putWorkbenchSelected).toHaveBeenCalledWith('/a')
    expect(putWorkbenchDock).toHaveBeenCalledTimes(1)
  })
})
