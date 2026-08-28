// restore.test.ts —— 落盘工作台状态与服务端会话列表合成恢复结果的纯函数测试。
//
// 职责：覆盖死会话抹除、孤儿归组、悬浮窗现场恢复与坏数据降级；B283 红色回路的转正锁。
// 边界：不测试 React、网络请求或项目树选中动作；这些由同步层和 Shell 负责。
import { describe, expect, it } from 'vitest'
import type { MachineStatus, PtySession, WorkbenchStateResp } from '../../api/types'
import type { HomeTab } from '../homedock/useHomeDock'
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

const baseM: BaseDir = {
  key: '/repo/m@mac-02',
  kind: 'workspace',
  path: '/repo/m',
  label: 'm',
  projectName: 'p',
  machine: 'mac-02',
}

// machine 造一条扇出应答行。MachineStatus 四个字段全必填（types.ts:173-178）。
function machine(name: string, ok: boolean): MachineStatus {
  return { name, ok, fetched_at: '2026-08-28T00:00:00Z', error: '' }
}

// homeSess 造一条 home 会话；machineName 缺省 = 本机。
function homeSess(id: string, machineName = ''): PtySession {
  return sess(id, { machine: machineName, base_kind: 'home', base_path: '' })
}

// dockRaw 把一组悬浮窗 tab 编成落盘 payload；activeId / windowOpen 可显式指定。
function dockRaw(tabs: HomeTab[], activeId: string | null = tabs[0]?.id ?? null, windowOpen = false): string {
  return encodeDock({ tabs, activeId, windowOpen, geom: { x: 10, y: 10, w: 620, h: 340 }, maximized: false })
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

  it('机器扇出 ok 时本行死会话照常剥——门控只挡「没答上来」，不挡真死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [sess('S1', { machine: 'mac-02', base_path: '/repo/m', exit_code: 0 })],
      machines: [machine('', true), machine('mac-02', true)],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(r.pruned).toBe(1)
  })

  it('机器扇出没答上来（ok=false）时它名下基准行的引用不剥——缺席不判死', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [], // mac-02 没答上来：S1 可能活着，只是没进名单
      machines: [machine('', true), machine('mac-02', false)],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })

  it('machines 整个缺席时同样保守：远端基准行的引用不剥', () => {
    const r = buildRestore({
      state: state({ bases: [{ base_key: baseM.key, payload: encodeBase(baseM, wbWith('S1')), updated_at: 1 }] }),
      sessions: [],
      ...VIEW,
    })
    expect(r.entries[0].wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
    expect(r.pruned).toBe(0)
  })

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
    // 活着的外来 home 会话不再被收编回来（方案1，Task 2 已落地）
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
