import { act, renderHook, waitFor } from '@testing-library/react'
import { createElement, StrictMode, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EMPTY_WORKBENCH, openTab, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import type { DockSnapshot } from '../homedock/dockPersist'
import { GLOBAL_WORKBENCH_KEY, encodeWorkbench } from './persist'
import { useWorkbench } from './useWorkbench'
import { DEVICE_WORKBENCH_KEY, encodeDeviceWorkbench } from './deviceState'

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
    lockWorkbench: vi.fn(),
    enableWorkbench: vi.fn(),
    hydrateDock: vi.fn(),
    adoptDockTab: vi.fn(),
    ...over,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
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
    expect(d.enableWorkbench).toHaveBeenCalled()
    expect(fetchPtySessions).toHaveBeenCalledWith('all')
    expect(result.current.restoredSelected).toBe('/a')
    expect(result.current.error).toBe('')
  })

  it('StrictMode effect replay只提交一次restore结果', async () => {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const d = deps()
    const wrapper = ({ children }: { children: ReactNode }) => createElement(StrictMode, null, children)
    renderHook(() => useWorkbenchSync(d), { wrapper })
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledTimes(1))
  })

  it('holds layout actions until the initial restore finishes, then enables them', async () => {
    let resolveState!: (value: { selected: string; dock: string; bases: { base_key: string; payload: string; updated_at: number }[] }) => void
    let resolveSessions!: (value: { sessions: never[] }) => void
    vi.mocked(fetchWorkbenchState).mockReturnValue(new Promise((resolve) => { resolveState = resolve }))
    vi.mocked(fetchPtySessions).mockReturnValue(new Promise((resolve) => { resolveSessions = resolve }) as never)
    const base: BaseDir = { key: '/repo', kind: 'workspace', path: '/repo', label: 'main', projectName: 'repo', machine: '' }
    const serverWorkbench = openTab(EMPTY_WORKBENCH, base, { kind: 'file', rel: 'README.md' })
    const { result } = renderHook(() => {
      const api = useWorkbench()
      const sync = useWorkbenchSync({
        workbench: api.wb,
        selectedKey: api.base?.key ?? '',
        dockSnapshot: dock,
        hydrateWorkbench: api.hydrate,
        lockWorkbench: api.lockLayout,
        enableWorkbench: api.enableLayout,
        hydrateDock: vi.fn(),
        adoptDockTab: vi.fn(),
      })
      return { api, sync }
    })
    act(() => {
      result.current.api.select(base)
      result.current.api.openInNewGroup({ kind: 'file', rel: 'README.md' }, base)
      result.current.api.openInRightSplit({ kind: 'file', rel: 'README.md' }, base)
    })
    expect(result.current.api.wb).toEqual(EMPTY_WORKBENCH)

    await act(async () => {
      resolveState({ selected: '', dock: '', bases: [{ base_key: GLOBAL_WORKBENCH_KEY, payload: encodeWorkbench(serverWorkbench), updated_at: 1 }] })
      resolveSessions({ sessions: [] })
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.api.wb.groups).toHaveLength(1))
    expect(result.current.api.wb.groups[0].columns).toHaveLength(1)
    expect(result.current.sync.restoring).toBe(false)
    act(() => result.current.api.openInRightSplit({ kind: 'file', rel: 'README.md' }, base))
    expect(result.current.api.wb.groups[0].columns).toHaveLength(2)
  })

  it('恢复失败不 hydrate，后续变更也不写回', async () => {
    vi.mocked(fetchWorkbenchState).mockRejectedValue(new Error('boom'))
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const { result } = renderHook(() => {
      const api = useWorkbench()
      const sync = useWorkbenchSync({
        workbench: api.wb,
        selectedKey: api.base?.key ?? '',
        dockSnapshot: dock,
        hydrateWorkbench: api.hydrate,
        lockWorkbench: api.lockLayout,
        enableWorkbench: api.enableLayout,
        hydrateDock: vi.fn(),
        adoptDockTab: vi.fn(),
      })
      return { api, sync }
    })
    await waitFor(() => expect(result.current.sync.error).toContain('boom'))
    expect(result.current.sync.restoring).toBe(false)
    act(() => {
      result.current.api.select(base)
      result.current.api.open({ kind: 'file', rel: 'local.md' }, base)
    })
    expect(result.current.api.wb.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'file', rel: 'local.md' })
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('本地空布局重载不被旧服务端布局回填，不删除原服务端记录', async () => {
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
    expect(putWorkbenchBase).not.toHaveBeenCalled()
    rerender(deps({ workbench: EMPTY_WORKBENCH }))
    await act(async () => { vi.advanceTimersByTime(700) })
    const next = deps()
    renderHook(() => useWorkbenchSync(next))
    await waitFor(() => expect(next.hydrateWorkbench).toHaveBeenCalledWith(EMPTY_WORKBENCH))
    expect(fetchWorkbenchState).toHaveBeenCalledTimes(1)
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('selected 与 dock 在页面离开时保存本地，重载不再请求共享布局', async () => {
    const d = await mounted(deps())
    d.rerender(deps({ selectedKey: '/a', dockSnapshot: { ...dock, windowOpen: true } }))
    act(() => { window.dispatchEvent(new Event('pagehide')) })
    d.unmount()
    const restored = deps()
    const h = renderHook(() => useWorkbenchSync(restored))
    await waitFor(() => expect(h.result.current.restoredSelected).toBe('/a'))
    expect(fetchWorkbenchState).toHaveBeenCalledTimes(1)
    expect(putWorkbenchSelected).not.toHaveBeenCalled()
    expect(putWorkbenchDock).not.toHaveBeenCalled()
  })

  it('两份设备存储各自恢复文件分屏与目录，共享终端引用不变且零服务端PUT', async () => {
    const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')!
    function memoryStorage(): Storage {
      const data = new Map<string, string>()
      return { get length() { return data.size }, clear: () => data.clear(), getItem: key => data.get(key) ?? null,
        key: index => [...data.keys()][index] ?? null, removeItem: key => { data.delete(key) }, setItem: (key, value) => { data.set(key, value) } }
    }
    const desktop = memoryStorage(), phone = memoryStorage()
    const computerWorkbench = openTab(openTab(EMPTY_WORKBENCH, base, { kind: 'terminal', seq: 1, sessionId: 'shared-pty' }), base, { kind: 'file', rel: 'computer.md' })
    const phoneWorkbench = openTab(EMPTY_WORKBENCH, base, { kind: 'file', rel: 'phone.md' })
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [{ id: 'shared-pty', exit_code: null, base_kind: 'workspace', base_path: '/a', machine: '' }] } as never)
    try {
      Object.defineProperty(window, 'localStorage', { configurable: true, value: desktop })
      const cd = deps()
      const c = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: cd })
      await waitFor(() => expect(cd.hydrateWorkbench).toHaveBeenCalled())
      Object.defineProperty(window, 'localStorage', { configurable: true, value: phone })
      const pd = deps()
      const p = renderHook((d: WorkbenchSyncDeps) => useWorkbenchSync(d), { initialProps: pd })
      await waitFor(() => expect(pd.hydrateWorkbench).toHaveBeenCalled())
      c.rerender(deps({ workbench: computerWorkbench, selectedKey: '/computer' }))
      p.rerender(deps({ workbench: phoneWorkbench, selectedKey: '/phone' }))
      await act(async () => { vi.advanceTimersByTime(700) })
      c.unmount(); p.unmount()
      const before = vi.mocked(fetchWorkbenchState).mock.calls.length
      Object.defineProperty(window, 'localStorage', { configurable: true, value: desktop })
      const cr = deps()
      const ch = renderHook(() => useWorkbenchSync(cr))
      await waitFor(() => expect(cr.hydrateWorkbench).toHaveBeenCalledWith(computerWorkbench))
      expect(ch.result.current.restoredSelected).toBe('/computer')
      Object.defineProperty(window, 'localStorage', { configurable: true, value: phone })
      const pr = deps()
      const ph = renderHook(() => useWorkbenchSync(pr))
      await waitFor(() => expect(pr.hydrateWorkbench).toHaveBeenCalledWith(phoneWorkbench))
      expect(ph.result.current.restoredSelected).toBe('/phone')
      expect(fetchWorkbenchState).toHaveBeenCalledTimes(before)
      expect(putWorkbenchBase).not.toHaveBeenCalled()
      expect(putWorkbenchSelected).not.toHaveBeenCalled()
      expect(putWorkbenchDock).not.toHaveBeenCalled()
      ch.unmount(); ph.unmount()
    } finally { Object.defineProperty(window, 'localStorage', descriptor) }
  })

  it('损坏本地记录保留原值，放开当前页面操作但不向服务端回退或覆盖', async () => {
    window.localStorage.setItem(DEVICE_WORKBENCH_KEY, '{broken')
    const d = deps()
    const h = renderHook((p: WorkbenchSyncDeps) => useWorkbenchSync(p), { initialProps: d })
    await waitFor(() => expect(h.result.current.restoring).toBe(false))
    expect(h.result.current.error).not.toBe('')
    expect(d.enableWorkbench).toHaveBeenCalled()
    h.rerender(deps({ workbench: wb }))
    await act(async () => { vi.advanceTimersByTime(700) })
    expect(window.localStorage.getItem(DEVICE_WORKBENCH_KEY)).toBe('{broken')
    expect(fetchWorkbenchState).not.toHaveBeenCalled()
    expect(putWorkbenchBase).not.toHaveBeenCalled()
  })

  it('本地写入失败仍展示迁移布局，并提示内存模式，不回退共享写入', async () => {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '/a', dock: '', bases: [{ base_key: GLOBAL_WORKBENCH_KEY, payload: encodeWorkbench(wb), updated_at: 1 }] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] } as never)
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota') })
    try {
      const d = deps()
      const h = renderHook(() => useWorkbenchSync(d))
      await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledWith(wb))
      expect(h.result.current.error).toContain('保存失败')
      expect(h.result.current.error).toContain('quota')
      expect(d.enableWorkbench).toHaveBeenCalled()
      expect(putWorkbenchBase).not.toHaveBeenCalled()
    } finally { spy.mockRestore() }
  })

  it.each([false, true])('本地记录存在=%s：PTY列表断开仍保留文件与终端引用', async (hasLocal) => {
    const saved = openTab(openTab(EMPTY_WORKBENCH, base, { kind: 'terminal', seq: 1, sessionId: 'keep-pty' }), base, { kind: 'file', rel: 'local.md' })
    if (hasLocal) window.localStorage.setItem(DEVICE_WORKBENCH_KEY, encodeDeviceWorkbench(saved, '/a', dock))
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '/a', dock: '', bases: [{ base_key: GLOBAL_WORKBENCH_KEY, payload: encodeWorkbench(saved), updated_at: 1 }] })
    vi.mocked(fetchPtySessions).mockRejectedValue(new Error('offline'))
    const d = deps()
    const h = renderHook(() => useWorkbenchSync(d))
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledWith(saved))
    expect(h.result.current.restoring).toBe(false)
    expect(h.result.current.error).toContain('offline')
    expect(fetchWorkbenchState).toHaveBeenCalledTimes(hasLocal ? 0 : 1)
  })

  it('共享PTY中新增home终端不自动加入本设备的标签布局', async () => {
    window.localStorage.setItem(DEVICE_WORKBENCH_KEY, encodeDeviceWorkbench(wb, '/a', dock))
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [{ id: 'other-device', exit_code: null, base_kind: 'home', machine: '' }] } as never)
    const d = deps()
    renderHook(() => useWorkbenchSync(d))
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledWith(wb))
    expect(d.adoptDockTab).not.toHaveBeenCalled()
    expect(d.hydrateDock).toHaveBeenCalledWith(expect.objectContaining({ tabs: [], activeId: null, windowOpen: false }))
  })

  it('首次迁移也不把旧布局以外的共享home终端自动打开', async () => {
    vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [{ id: 'other-device', exit_code: null, base_kind: 'home', machine: '' }] } as never)
    const d = deps()
    renderHook(() => useWorkbenchSync(d))
    await waitFor(() => expect(d.hydrateWorkbench).toHaveBeenCalledWith(EMPTY_WORKBENCH))
    expect(d.adoptDockTab).not.toHaveBeenCalled()
  })
})
