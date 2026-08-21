// useWorkbenchSync.test.ts —— 工作台状态同步 hook 的水合、失败闸门与去抖写回测试。
//
// 职责：验证双请求汇合、水合次序、拉取失败禁写和 500ms 差分写回。
// 边界：不测试真实 HTTP 或 React 工作台状态机；依赖通过 mock 与显式 deps 注入。
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import type { DockSnapshot } from '../homedock/dockPersist'
import { encodeBase } from './persist'

vi.mock('../../api/client', () => ({
  fetchWorkbenchState: vi.fn(),
  fetchPtySessions: vi.fn(),
  putWorkbenchBase: vi.fn(() => Promise.resolve()),
  putWorkbenchSelected: vi.fn(() => Promise.resolve()),
  putWorkbenchDock: vi.fn(() => Promise.resolve()),
}))

import {
  fetchPtySessions,
  fetchWorkbenchState,
  putWorkbenchBase,
  putWorkbenchDock,
} from '../../api/client'
import { useWorkbenchSync, type WorkbenchSyncDeps } from './useWorkbenchSync'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }
const wbA: Workbench = {
  groups: [{ tabs: [{ id: 't1', content: { kind: 'blank' } }], activeId: 't1' }],
  active: 0,
  sizes: [1],
}
const emptyDock: DockSnapshot = { tabs: [], activeId: null, windowOpen: false, geom: { x: 1, y: 1, w: 620, h: 340 }, maximized: false }

function deps(over: Partial<WorkbenchSyncDeps> = {}): WorkbenchSyncDeps {
  return {
    byBase: {},
    baseDirs: {},
    selectedKey: '',
    dockSnapshot: emptyDock,
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
afterEach(() => {
  vi.useRealTimers()
})

describe('useWorkbenchSync 水合', () => {
  it('两个请求都到齐之后才灌入一次', async () => {
    let resolveSessions: (v: unknown) => void = () => {}
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '/repo/a', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockReturnValue(new Promise((res) => { resolveSessions = res as never }) as never)

    const d = deps()
    const { result } = renderHook(() => useWorkbenchSync(d))

    // 只有布局到了，会话列表还没到——此时绝不能灌入，否则终端 tab 会闪一下
    await act(async () => { await Promise.resolve() })
    expect(d.hydrateWorkbench).not.toHaveBeenCalled()

    await act(async () => { resolveSessions({ sessions: [] }); await Promise.resolve() })
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledTimes(1))
    expect(result.current.restoredSelected).toBe('/repo/a')
    expect(result.current.error).toBe('')
  })

  it('拉取失败时报错、不灌入，且此后永不写回', async () => {
    vi.mocked(fetchWorkbenchState).mockRejectedValue(new Error('boom'))
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)

    const d = deps()
    const { result, rerender } = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: d })
    await waitFor(() => expect(result.current.error).toContain('boom'))
    expect(d.hydrateWorkbench).not.toHaveBeenCalled()

    // 用户照常开 tab；这一整个会话都不该有任何写回——
    // 拉不到就等于不知道服务端有什么，写回去就是清空用户的现场
    rerender(deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })
})

describe('useWorkbenchSync 写回', () => {
  async function mounted(initial: WorkbenchSyncDeps) {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const h = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: initial })
    await waitFor(() => expect(initial.hydrateWorkbench).toHaveBeenCalled())
    return h
  }

  it('水合本身不触发写回', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps())
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('连续多次变更只发一次 PUT（去抖）', async () => {
    const { rerender } = await mounted(deps())
    const mk = (label: string) => deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': { ...baseA, label } } })
    rerender(mk('a1'))
    rerender(mk('a2'))
    rerender(mk('a3'))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).toHaveBeenCalledTimes(1)
    expect(vi.mocked(putWorkbenchBase).mock.calls[0][0]).toBe('/repo/a')
    expect(vi.mocked(putWorkbenchBase).mock.calls[0][1]).toBe(encodeBase({ ...baseA, label: 'a3' }, wbA))
  })

  it('tab 全关光的目录 PUT null（删除该行）', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps({ byBase: { '/repo/a': wbA }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    vi.mocked(putWorkbenchBase).mockClear()

    rerender(deps({ byBase: { '/repo/a': { groups: [{ tabs: [], activeId: null }], active: 0, sizes: [1] } }, baseDirs: { '/repo/a': baseA } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).toHaveBeenCalledWith('/repo/a', null)
  })

  it('悬浮窗现场变了就写回', async () => {
    const { rerender } = await mounted(deps())
    rerender(deps({ dockSnapshot: { ...emptyDock, windowOpen: true } }))
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchDock).toHaveBeenCalledTimes(1)
  })
})
