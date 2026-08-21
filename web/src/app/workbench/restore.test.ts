// restore.test.ts —— 落盘工作台状态与服务端会话列表合成恢复结果的纯函数测试。
//
// 职责：覆盖死会话抹除、孤儿归组、悬浮窗现场恢复与坏数据降级。
// 边界：不测试 React、网络请求或项目树选中动作；这些由同步层和 Shell 负责。
import { describe, expect, it } from 'vitest'
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { encodeBase } from './persist'
import { encodeDock } from '../homedock/dockPersist'
import type { BaseDir } from './useWorkbench'
import type { Workbench } from './tabs'
import { buildRestore } from './restore'

const baseA: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'p', machine: '' }

function wbWith(sessionId?: string): Workbench {
  return {
    groups: [{ tabs: [{ id: 't1', content: sessionId ? { kind: 'terminal', seq: 1, sessionId } : { kind: 'terminal', seq: 1 } }], activeId: 't1' }],
    active: 0,
    sizes: [1],
  }
}

function sess(id: string, over: Partial<PtySession> = {}): PtySession {
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

describe('buildRestore', () => {
  it('活着的会话原样保留', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1')],
      ...VIEW,
    })
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
    expect(r.adopted).toBe(0)
  })

  // 这一组是承重的：合并 A（PTY 出进程）时，协议错配的降级链路原本挂在
  // usePtyRestore 上，而那个文件在 B 里被删掉了。链路重新落到本层之后，
  // 它的判据必须跟着搬过来，否则「合并之后功能还在、测试没了」不会有人发现。
  it('不兼容的会话打标记而不是抹 id——它还活着，抹了等于把旧 shell 丢在后台', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1', { incompatible: true })],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({
      kind: 'terminal', seq: 1, sessionId: 'S1', incompatible: true,
    })
    // 它没死，所以不该被计进「抹掉的死会话」
    expect(r.pruned).toBe(0)
  })

  it('补进来的孤儿会话不兼容时也带标记', () => {
    const r = buildRestore({
      state: state(),
      sessions: [sess('S9', { incompatible: true })],
      ...VIEW,
    })
    expect(r.adopted).toBe(1)
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({
      kind: 'terminal', seq: 1, sessionId: 'S9', incompatible: true,
    })
  })

  it('悬浮窗里的不兼容会话同样打标记', () => {
    const dock = encodeDock({
      tabs: [{ id: 'h1', kind: 'terminal', seq: 1, sessionId: 'H1', machine: '' }],
      activeId: 'h1', windowOpen: true, geom: { x: 100, y: 100, w: 400, h: 300 }, maximized: false,
    })
    const r = buildRestore({
      state: state({ dock }),
      sessions: [sess('H1', { base_kind: 'home', base_path: '', incompatible: true })],
      ...VIEW,
    })
    expect(r.dock?.tabs[0].incompatible).toBe(true)
    expect(r.dock?.tabs[0].sessionId).toBe('H1')
  })

  it('已退出的会话被抹掉 id，tab 留在原位', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1', { exit_code: 0 })],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('列表里完全没有的会话同样被抹掉', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('孤儿工作树会话被补进对应目录', () => {
    const r = buildRestore({ state: state(), sessions: [sess('S9')], ...VIEW })
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].base.key).toBe('/repo/a')
    expect(r.entries[0].wb.groups[0].tabs[0].content).toMatchObject({ kind: 'terminal', sessionId: 'S9' })
    expect(r.adopted).toBe(1)
  })

  it('孤儿会话补进**已有**目录时不覆盖既有 tab', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1'), sess('S9')],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs).toHaveLength(2)
    expect(r.adopted).toBe(1)
  })

  it('home 会话不进工作台，落到悬浮窗', () => {
    const dockRaw = encodeDock({ tabs: [], activeId: null, windowOpen: true, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
    const r = buildRestore({
      state: state({ dock: dockRaw }),
      sessions: [sess('H1', { base_kind: 'home', base_path: '' })],
      ...VIEW,
    })
    expect(r.entries).toHaveLength(0)
    expect(r.dock).not.toBeNull()
    expect(r.dock!.tabs).toHaveLength(1)
    expect(r.dock!.tabs[0]).toMatchObject({ kind: 'terminal', sessionId: 'H1' })
    // 有 tab 就必须有激活项，否则浮窗一片空白
    expect(r.dock!.activeId).toBe(r.dock!.tabs[0].id)
    expect(r.dockOrphans).toHaveLength(0)
  })

  it('没有落盘的悬浮窗现场时，孤儿 home 会话走 dockOrphans（不 hydrate、不开窗）', () => {
    const r = buildRestore({
      state: state(),
      sessions: [sess('H1', { base_kind: 'home', base_path: '' })],
      ...VIEW,
    })
    expect(r.dock).toBeNull()
    expect(r.dockOrphans).toHaveLength(1)
    expect(r.dockOrphans[0].sessionId).toBe('H1')
  })

  it('悬浮窗几何按当前视口夹紧', () => {
    const dockRaw = encodeDock({ tabs: [], activeId: null, windowOpen: true, geom: { x: 2000, y: 1400, w: 900, h: 700 }, maximized: false })
    const r = buildRestore({ state: state({ dock: dockRaw }), sessions: [], ...VIEW })
    expect(r.dock!.geom.x + r.dock!.geom.w).toBeLessThanOrEqual(1280)
    expect(r.dock!.geom.y + r.dock!.geom.h).toBeLessThanOrEqual(800)
  })

  it('坏行整行丢弃，其余行照常恢复', () => {
    const r = buildRestore({
      state: state({
        bases: [
          { base_key: '/repo/bad', payload: 'not json', updated_at: 1 },
          { base_key: '/repo/a', payload: encodeBase(baseA, wbWith()), updated_at: 2 },
        ],
      }),
      sessions: [],
      ...VIEW,
    })
    expect(r.dropped).toEqual(['/repo/bad'])
    expect(r.entries).toHaveLength(1)
    expect(r.entries[0].base.key).toBe('/repo/a')
  })

  it('坏的悬浮窗现场整份丢弃，不影响工作台', () => {
    const r = buildRestore({
      state: state({ dock: '{{{', bases: [{ base_key: '/repo/a', payload: encodeBase(baseA, wbWith()), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.dock).toBeNull()
    expect(r.entries).toHaveLength(1)
  })

  it('selected 原样透传', () => {
    const r = buildRestore({ state: state({ selected: '/repo/a' }), sessions: [], ...VIEW })
    expect(r.selected).toBe('/repo/a')
  })
})
