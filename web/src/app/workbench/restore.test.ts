import { describe, expect, it } from 'vitest'
import type { MachineStatus, PtySession, WorkbenchStateResp } from '../../api/types'
import { encodeDock } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import type { BaseDir, Workbench } from './tabs'
import { encodeWorkbench } from './persist'
import { baseOfSession, buildRestore } from './restore'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'main', projectName: 'handoff', machine: '' }
const baseM: BaseDir = { key: '/repo/m@mac-02', kind: 'workspace', path: '/repo/m', label: 'm', projectName: 'p', machine: 'mac-02' }
const empty = (): Workbench => ({
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] }],
})
const withSession = (id: string): Workbench => ({
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [{ id: 't1', base: baseA, content: { kind: 'terminal', seq: 1, sessionId: id } }] }], sizes: [1], focus: [0, 0] }],
})
const withSessionOn = (base: BaseDir, id: string): Workbench => ({
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [{ id: 't1', base, content: { kind: 'terminal', seq: 1, sessionId: id } }] }], sizes: [1], focus: [0, 0] }],
})
// machine 造一条扇出应答行。MachineStatus 四个字段全必填（types.ts:173-178）。
function machine(name: string, ok: boolean): MachineStatus {
  return { name, ok, fetched_at: '2026-08-28T00:00:00Z', error: '' }
}
// homeSess 造一条 home 会话；machineName 缺省 = 本机。
function homeSess(id: string, machineName = ''): PtySession {
  return session(id, { machine: machineName, base_kind: 'home', base_path: '' })
}
// dockRaw 把一组悬浮窗 tab 编成落盘 payload；activeId / windowOpen 可显式指定。
function dockRaw(tabs: HomeTab[], activeId: string | null = tabs[0]?.id ?? null, windowOpen = false): string {
  return encodeDock({ tabs, activeId, windowOpen, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
}
function session(id: string, over: Partial<PtySession> = {}): PtySession {
  return {
    id, machine: '', base_path: '/repo/a', base_kind: 'workspace', shell: '/bin/bash',
    created_at: '2026-08-20T10:00:00+08:00', cols: 80, rows: 24, attached: 0,
    foreground: false, pid: 1, bytes_out: 0, incompatible: false, ...over,
  }
}
function state(over: Partial<WorkbenchStateResp> = {}): WorkbenchStateResp {
  return { selected: '', dock: '', bases: [], ...over }
}
const VIEW = { vw: 1280, vh: 800, inset: 0 }

describe('baseOfSession', () => {
  it('同一路径的本机与远端有不同 key，home 也按 machine 区分', () => {
    expect(baseOfSession(session('a')).key).toBe('/repo/a')
    expect(baseOfSession(session('b', { machine: 'linux-01' })).key).toBe('/repo/a@linux-01')
    expect(baseOfSession(session('h', { base_kind: 'home', base_path: '', machine: 'linux-01' }))).toMatchObject({ key: '~@linux-01', kind: 'home' })
  })
})

describe('buildRestore', () => {
  it('只解码 global 行，其它状态行进入 legacy，不拼旧布局', () => {
    const r = buildRestore({
      state: state({ bases: [
        { base_key: '__global_workbench__', payload: encodeWorkbench(withSession('S1')), updated_at: 1 },
        { base_key: '/old', payload: encodeWorkbench(withSession('S2')), updated_at: 2 },
      ] }),
      sessions: [session('S1')], ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toMatchObject({ sessionId: 'S1' })
    expect(r.legacy).toEqual(['/old'])
  })

  it('坏 global payload 回到 EMPTY 并记录 dropped', () => {
    const r = buildRestore({ state: state({ bases: [{ base_key: '__global_workbench__', payload: 'bad', updated_at: 1 }] }), sessions: [], ...VIEW })
    expect(r.workbench).toEqual(empty())
    expect(r.dropped).toEqual(['__global_workbench__'])
  })

  it('死 session 清 id 留 pane，活着但 incompatible 保留 id 并打标', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(withSession('dead')), updated_at: 1 }] }),
      sessions: [session('dead', { exit_code: 0 })], ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)

    const compatible = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(withSession('live')), updated_at: 1 }] }),
      sessions: [session('live', { incompatible: true })], ...VIEW,
    })
    expect(compatible.workbench.groups[0].columns[0].panes[0]!.content).toMatchObject({ sessionId: 'live', incompatible: true })
  })

  it('workspace 活孤儿不再收编成新组——B322 泵', () => {
    const layout: Workbench = {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '组 1', autoName: true,
        columns: [
          { panes: [{ id: 't1', base: baseA, content: { kind: 'terminal', seq: 1, sessionId: 'LIVE' } }] },
          { panes: [null] },
        ],
        sizes: [2, 1], focus: [0, 0],
      }],
    }
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(layout), updated_at: 1 }] }),
      sessions: [session('LIVE'), session('S2', { base_path: '/repo/b' }), session('S3', { base_path: '/repo/c' })], ...VIEW,
    })
    expect(r.adopted).toBe(0)
    expect(r.workbench.groups).toHaveLength(1)
    expect(r.workbench.groups[0].columns[0].panes[0]?.content).toMatchObject({ sessionId: 'LIVE' })
  })

  it('home session 继续走 dock，没 dock 现场进入 dockOrphans', () => {
    const noDock = buildRestore({ state: state(), sessions: [session('H1', { base_kind: 'home', base_path: '' })], ...VIEW })
    expect(noDock.workbench).toEqual(empty())
    expect(noDock.dock).toBeNull()
    expect(noDock.dockOrphans[0]).toMatchObject({ sessionId: 'H1' })

    const raw = encodeDock({ tabs: [], activeId: null, windowOpen: true, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
    const withDock = buildRestore({ state: state({ dock: raw }), sessions: [session('H2', { base_kind: 'home', base_path: '' })], ...VIEW })
    expect(withDock.dock?.tabs[0]).toMatchObject({ sessionId: 'H2' })
    expect(withDock.dock?.activeId).toBe(withDock.dock?.tabs[0].id)
  })

  it('selected 原样透传', () => {
    expect(buildRestore({ state: state({ selected: '/repo/a' }), sessions: [], ...VIEW }).selected).toBe('/repo/a')
  })
})

// —— B283 机器门控与外来 tab 清理（自逐行结构合并移植到全局结构，判据不变）——
describe('B283 方案3：机器扇出没答上来时不做死亡判决', () => {
  it('机器扇出 ok 时死会话照常剥——门控只挡「没答上来」，不挡真死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(withSessionOn(baseM, 'S1')), updated_at: 1 }] }),
      sessions: [session('S1', { machine: 'mac-02', base_path: '/repo/m', exit_code: 0 })],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('机器扇出没答上来（ok=false）时它名下 tab 的引用不剥——缺席不判死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(withSessionOn(baseM, 'S1')), updated_at: 1 }] }),
      sessions: [], // mac-02 没答上来：S1 可能活着，只是没进名单
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })

  it('machines 整个缺席时同样保守：远端 tab 的引用不剥', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench(withSessionOn(baseM, 'S1')), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })

  it('本机 tab 不受门控影响：机器没答上来只保远端，本机死会话照剥', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '__global_workbench__', payload: encodeWorkbench({ ...withSessionOn(baseM, 'S1'), groups: [...withSessionOn(baseM, 'S1').groups, { id: 'g2', name: '组 2', autoName: true, columns: [{ panes: [{ id: 't2', base: baseA, content: { kind: 'terminal', seq: 2, sessionId: 'L0' } }] }], sizes: [1], focus: [0, 0] }] }), updated_at: 1 }] }),
      sessions: [],
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    expect(r.workbench.groups[0].columns[0].panes[0]!.content).toMatchObject({ sessionId: 'S1' }) // 远端：保
    expect(r.workbench.groups[1].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 2 }) // 本机：剥
    expect(r.pruned).toBe(1)
  })
})

describe('B283 方案1+2：悬浮窗是本机面', () => {
  it('home 收编仅限本机：外来机器的 home 会话不进悬浮窗，本机的照常收', () => {
    const r = buildRestore({
      state: state({ dock: dockRaw([], null, false) }),
      sessions: [homeSess('H1', 'mac-02'), homeSess('H2')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0].sessionId).toBe('H2')
    expect(r.adopted).toBe(1)
    expect(r.dockOrphans).toHaveLength(0)
  })

  it('悬浮窗本地 tab 的死会话照常剥引用——门控不挡真死', () => {
    const r = buildRestore({
      state: state({ dock: dockRaw([{ id: 'h1', kind: 'terminal', seq: 1, machine: '', sessionId: 'H1' }], 'h1', true) }),
      sessions: [{ ...homeSess('H1'), exit_code: 0 }],
      machines: [machine('', true)],
      ...VIEW,
    })
    expect(r.dock!.tabs[0].sessionId).toBeUndefined()
    expect(r.dock!.activeId).toBe('h1') // tab 留位，激活不变
    expect(r.pruned).toBe(1)
  })

  it('存量外来 tab 一次性清除：全外来快照清空后 tabs 为空、activeId 为 null、windowOpen 收为 false', () => {
    const r = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H1' },
            { id: 'u2', kind: 'file', seq: 2, machine: 'linux-01', rel: 'notes.md' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H1', 'mac-02')], // 它还活着，也照样清——悬浮窗是本机面
      machines: [machine('', true), machine('mac-02', true), machine('linux-01', true)],
      ...VIEW,
    })
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(0)
    expect(r.dock!.activeId).toBeNull()
    expect(r.dock!.windowOpen).toBe(false)
    expect(r.purged).toBe(2)
    // 清除是整个 tab 走，不是剥引用留壳：活着的 H1 也不计入 prune
    expect(r.pruned).toBe(0)
    // 活着的外来 home 会话不再被收编回来（方案1）
    expect(r.adopted).toBe(0)
  })

  it('清除命中 activeId 时显式置 null，既有兜底把它重指到剩下的第一个 tab', () => {
    const r = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H1' },
            { id: 'h2', kind: 'terminal', seq: 2, machine: '', sessionId: 'H2' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H2')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0].id).toBe('h2')
    expect(r.dock!.activeId).toBe('h2')
    expect(r.purged).toBe(1)
    expect(r.dock!.windowOpen).toBe(true) // 没清空就不收窗
  })
})

// B283 红色回路转正：同一份现场连续两次打开，tab 数与引用都不得漂移
describe('B283 红色回路转正：同一份现场连续两次打开，tab 数与引用都不得漂移', () => {
  it('两次打开之间悬浮窗 tab 数不增长，本地 tab 的引用保得住（修复前 1→2）', () => {
    // 打开 N：快照里一个外来 tab（mac-02 的 H9，扇出没带回它——ok=false）＋一个
    // 本机 tab（H1，活着且在名单里）。
    const open1 = buildRestore({
      state: state({
        dock: dockRaw(
          [
            { id: 'u1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'H9' },
            { id: 'h1', kind: 'terminal', seq: 2, machine: '', sessionId: 'H1' },
          ],
          'u1',
          true,
        ),
      }),
      sessions: [homeSess('H1')],
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    // 外来 tab 整个清除（不是剥引用留壳），本机 tab 原样保引用——修复前这里是
    // 「H9 被剥成壳 + H1 留引用」两个 tab，壳会在两次打开之间自建新会话。
    expect(open1.dock!.tabs).toHaveLength(1)
    expect(open1.dock!.tabs[0]).toMatchObject({ id: 'h1', sessionId: 'H1' }) // 保引用（反转自「被剥」）
    expect(open1.dock!.activeId).toBe('h1') // u1 被清 → 显式置 null → 既有兜底重指
    expect(open1.purged).toBe(1)
    expect(open1.pruned).toBe(0) // mac-02 没答上来：恢复层没有把 H9 判死剥引用

    // 两次打开之间：h1 有 sessionId 可 attach，TerminalTab 不自建（TerminalTab.tsx
    // 的 `if (!id)` 支不进），u1 已随首次写回落盘消失。修复前的循环是：壳自建 H9'
    // 写回 → 下一轮 H9 活着回来被当孤儿收编 → tab +1。

    // 打开 N+1：H9（mac-02 home）活着回到列表——不收编（方案1：悬浮窗是本机面）。
    const open2 = buildRestore({
      state: state({
        dock: dockRaw([{ id: 'h1', kind: 'terminal', seq: 2, machine: '', sessionId: 'H1' }], 'h1', true),
      }),
      sessions: [homeSess('H1'), homeSess('H9', 'mac-02')],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(open2.dock!.tabs).toHaveLength(1)
    expect(open2.dock!.tabs[0].sessionId).toBe('H1')
    expect(open2.adopted).toBe(0)
    expect(open2.purged).toBe(0)
  })
})
