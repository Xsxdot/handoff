// persist.test.ts —— 工作台状态编解码、校验、会话清理与差分的纯函数测试。
//
// 职责：钉住落盘格式的往返、坏数据整行丢弃、草稿隔离、死会话清理与 payload 差分。
// 边界：不测试 React、HTTP 或 localStorage；这些由状态容器和同步层负责。
import { describe, expect, it } from 'vitest'
import { EMPTY_WORKBENCH, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'
import {
  PERSIST_VERSION,
  decodeBase,
  diffPayloads,
  encodeBase,
  isEmptyWorkbench,
  pruneDeadSessions,
} from './persist'

const base: BaseDir = {
  key: '/repo/a@linux-01',
  kind: 'workspace',
  path: '/repo/a',
  label: 'feature/x',
  projectName: 'handoff',
  machine: 'linux-01',
}

// wbSample 造一个两栏、含三种 tab 的工作台。
function wbSample(): Workbench {
  return {
    groups: [
      {
        tabs: [
          { id: 't1', content: { kind: 'terminal', seq: 1, sessionId: 'S1' } },
          { id: 't2', content: { kind: 'file', rel: 'src/a.ts' } },
        ],
        activeId: 't2',
      },
      {
        tabs: [
          { id: 't3', content: { kind: 'tui', taskId: 'TASK-1' } },
          { id: 't4', content: { kind: 'blank' } },
        ],
        activeId: 't3',
      },
    ],
    active: 1,
    sizes: [2, 1],
  }
}

describe('encodeBase / decodeBase', () => {
  it('往返之后逐字段相等', () => {
    const raw = encodeBase(base, wbSample())
    expect(typeof raw).toBe('string')
    const out = decodeBase(base.key, raw)
    expect(out).not.toBeNull()
    expect(out!.base).toEqual(base)
    expect(out!.wb).toEqual(wbSample())
  })

  it('key 由行本身提供，不从 payload 里读', () => {
    const raw = encodeBase(base, wbSample())
    const out = decodeBase('/somewhere/else', raw)
    // 存下来的 base 元数据照用，但 key 必须是调用方给的那个——
    // key 是行的身份，payload 里再存一份就有了两个真相
    expect(out!.base.key).toBe('/somewhere/else')
    expect(out!.base.path).toBe('/repo/a')
  })

  it('文件 tab 的草稿不落盘', () => {
    const wb: Workbench = {
      groups: [{ tabs: [{ id: 't1', content: { kind: 'file', rel: 'a.ts', draft: '改了一半', baseSha: 'abc' } }], activeId: 't1' }],
      active: 0,
      sizes: [1],
    }
    const out = decodeBase(base.key, encodeBase(base, wb))
    const c = out!.wb.groups[0].tabs[0].content
    expect(c).toEqual({ kind: 'file', rel: 'a.ts' })
  })

  it('终端 tab 的 incompatible 不落盘——它是服务端此刻的结论，不是布局', () => {
    const wb: Workbench = {
      groups: [{ tabs: [{ id: 't1', content: { kind: 'terminal', seq: 1, sessionId: 'S1', incompatible: true } }], activeId: 't1' }],
      active: 0,
      sizes: [1],
    }
    // 直接看 payload 原文：解码端会丢掉不认识的字段，只比往返结果的话，
    // 「写进去了但读不出来」这种情形会被盖住
    expect(encodeBase(base, wb)).not.toContain('incompatible')
    const out = decodeBase(base.key, encodeBase(base, wb))
    expect(out!.wb.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
  })

  it.each([
    ['不是 JSON', 'not json at all'],
    ['版本号不认识', JSON.stringify({ v: 99, base: {}, wb: EMPTY_WORKBENCH })],
    ['缺 wb', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' } })],
    ['kind 不是三种之一', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'bogus', path: '/a', label: 'a', projectName: '', machine: '' }, wb: EMPTY_WORKBENCH })],
    ['sizes 与 groups 不等长', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [], activeId: null }], active: 0, sizes: [1, 1] } })],
    ['active 越界', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [], activeId: null }], active: 5, sizes: [1] } })],
    ['tab content 种类不认识', JSON.stringify({ v: PERSIST_VERSION, base: { kind: 'workspace', path: '/a', label: 'a', projectName: '', machine: '' }, wb: { groups: [{ tabs: [{ id: 'x', content: { kind: 'video' } }], activeId: 'x' }], active: 0, sizes: [1] } })],
  ])('坏数据「%s」整行丢弃', (_name, raw) => {
    expect(decodeBase('/k', raw as string)).toBeNull()
  })
})

describe('isEmptyWorkbench', () => {
  it('所有组都没有 tab 才算空', () => {
    expect(isEmptyWorkbench(EMPTY_WORKBENCH)).toBe(true)
    expect(isEmptyWorkbench({ groups: [{ tabs: [], activeId: null }, { tabs: [], activeId: null }], active: 0, sizes: [1, 1] })).toBe(true)
    expect(isEmptyWorkbench(wbSample())).toBe(false)
  })
})

describe('pruneDeadSessions', () => {
  it('死会话的 id 被抹掉，tab 留在原位', () => {
    const out = pruneDeadSessions(wbSample(), new Set<string>())
    expect(out.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1 })
    expect(out.groups[0].tabs).toHaveLength(2)
    expect(out.groups[0].activeId).toBe('t2')
  })

  it('活会话原样保留', () => {
    const out = pruneDeadSessions(wbSample(), new Set(['S1']))
    expect(out.groups[0].tabs[0].content).toEqual({ kind: 'terminal', seq: 1, sessionId: 'S1' })
  })

  it('没有 sessionId 的 tab 与其它种类不受影响', () => {
    const out = pruneDeadSessions(wbSample(), new Set<string>())
    expect(out.groups[0].tabs[1].content).toEqual({ kind: 'file', rel: 'src/a.ts' })
    expect(out.groups[1].tabs[0].content).toEqual({ kind: 'tui', taskId: 'TASK-1' })
    expect(out.groups[1].tabs[1].content).toEqual({ kind: 'blank' })
  })
})

describe('diffPayloads', () => {
  it('分出新增、变更、删除三类', () => {
    const prev = { a: '1', b: '2', c: '3' }
    const next = { a: '1', b: '9', d: '4' }
    const { changed, removed } = diffPayloads(prev, next)
    expect(changed.sort()).toEqual(['b', 'd'])
    expect(removed).toEqual(['c'])
  })

  it('完全相同时两边都是空数组', () => {
    const same = { a: '1' }
    expect(diffPayloads(same, { ...same })).toEqual({ changed: [], removed: [] })
  })
})
