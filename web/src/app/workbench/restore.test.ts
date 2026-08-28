import { describe, expect, it } from 'vitest'
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { encodeDock } from '../homedock/dockPersist'
import type { BaseDir, Workbench } from './tabs'
import { encodeWorkbench } from './persist'
import { baseOfSession, buildRestore } from './restore'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'main', projectName: 'handoff', machine: '' }
const empty = (): Workbench => ({
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] }],
})
const withSession = (id: string): Workbench => ({
  activeGroupId: 'g1',
  groups: [{ id: 'g1', name: '组 1', autoName: true, columns: [{ panes: [{ id: 't1', base: baseA, content: { kind: 'terminal', seq: 1, sessionId: id } }] }], sizes: [1], focus: [0, 0] }],
})
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

  it('恢复孤儿不填现有空列，每个工作区 PTY 独立成组', () => {
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
    expect(r.adopted).toBe(2)
    expect(r.workbench.groups).toHaveLength(3)
    expect(r.workbench.groups[0].columns).toHaveLength(1)
    expect(r.workbench.groups[1].columns[0].panes[0]?.content).toMatchObject({ sessionId: 'S2' })
    expect(r.workbench.groups[2].columns[0].panes[0]?.content).toMatchObject({ sessionId: 'S3' })
    expect(r.workbench.groups.every((group) => group.columns.every((column) => column.panes.some(Boolean)))).toBe(true)
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
