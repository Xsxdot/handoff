import { describe, expect, it } from 'vitest'
import { EMPTY_WORKBENCH, type BaseDir, type Workbench } from './tabs'
import {
  GLOBAL_WORKBENCH_KEY,
  PERSIST_VERSION,
  decodeWorkbench,
  diffPayloads,
  encodeWorkbench,
  isEmptyWorkbench,
  markIncompatibleSessions,
  pruneDeadSessions,
} from './persist'

const a: BaseDir = { key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'main', projectName: 'handoff', machine: '' }
const b: BaseDir = { key: '/repo/a@linux-01', kind: 'workspace', path: '/repo/a', label: 'main', projectName: 'handoff', machine: 'linux-01' }

function sample(): Workbench {
  return {
    activeGroupId: 'g1',
    groups: [{
      id: 'g1', name: '组 1', autoName: true,
      columns: [
        { panes: [
          { id: 't1', base: a, content: { kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '跑测试', incompatible: true } },
          { id: 't2', base: b, content: { kind: 'file', rel: 'same.ts', draft: '未保存', baseSha: 'sha' } },
        ] },
        { panes: [{ id: 't3', base: b, content: { kind: 'file', rel: 'same.ts' } }] },
      ],
      sizes: [2, 1], focus: [1, 0],
    }],
  }
}

describe('encodeWorkbench / decodeWorkbench', () => {
  it('真实 JSON roundtrip 保留 group、机器、空字符串、缺席字段与 launcher', () => {
    const wb = sample()
    const raw = encodeWorkbench(wb)
    const json = JSON.parse(raw) as { v: number; wb: Workbench }
    expect(json.v).toBe(PERSIST_VERSION)
    expect(json.wb.groups[0].columns[0].panes[0]).toEqual({
      id: 't1', base: a,
      content: { kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '跑测试' },
    })
    expect(json.wb.groups[0].columns[0].panes[1]).toEqual({ id: 't2', base: b, content: { kind: 'file', rel: 'same.ts' } })
    expect(json.wb.groups[0].columns[0].panes[1]?.content).not.toHaveProperty('draft')
    expect(decodeWorkbench(raw)).toEqual(wbWithPersistenceFieldsRemoved(wb))
  })

  it('终端 spawn 是运行时字段，不进入 raw 或 decode（B322）', () => {
    const wb: Workbench = {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '组 1', autoName: true,
        columns: [{ panes: [{ id: 't1', base: a, content: { kind: 'terminal', seq: 1, spawn: true } }] }],
        sizes: [1], focus: [0, 0],
      }],
    }
    const out = decodeWorkbench(encodeWorkbench(wb))!
    expect(out.groups[0].columns[0].panes[0]!.content).toEqual({ kind: 'terminal', seq: 1 })
    expect(out.groups[0].columns[0].panes[0]!.content).not.toHaveProperty('spawn')
  })

  it('文件 draft/baseSha 与终端 incompatible 不进入 raw 或 decode', () => {
    const out = decodeWorkbench(encodeWorkbench(sample()))!
    const panes = out.groups[0].columns.flatMap((column) => column.panes).filter(Boolean)
    expect(panes[0]!.content).toEqual({ kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '跑测试' })
    expect(panes[1]!.content).toEqual({ kind: 'file', rel: 'same.ts' })
  })

  it('解码先压缩空列空组，并区分缺失字段与零/空值', () => {
    const source = {
      v: PERSIST_VERSION,
      wb: {
        activeGroupId: 'g1',
        groups: [
          {
            id: 'g1', name: '一组', autoName: true,
            columns: [
              { panes: [null] },
              { panes: [{ id: 't1', base: { key: '', kind: 'workspace', path: '', label: '', projectName: '', machine: '' }, content: { kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '' } }] },
              { panes: [null] },
            ],
            sizes: [2, 3, 4], focus: [1, 0],
          },
          { id: 'g2', name: '空组', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
        ],
      },
    }
    const decoded = decodeWorkbench(JSON.stringify(source))!
    expect(decoded.groups).toHaveLength(1)
    expect(decoded.groups[0].columns).toHaveLength(1)
    expect(decoded.groups[0].sizes).toEqual([3])
    expect(decoded.groups[0].columns[0].panes[0]?.content).toEqual({ kind: 'terminal', seq: 0, sessionId: '', rel: '', launcher: '' })
    expect(decoded.groups[0].columns[0].panes[0]?.base).toEqual({ key: '', kind: 'workspace', path: '', label: '', projectName: '', machine: '' })
  })

  it('所有解码后的组都为空时回到唯一空组', () => {
    const raw = encodeWorkbench({
      activeGroupId: 'g2',
      groups: [
        { id: 'g1', name: '一组', autoName: true, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
        { id: 'g2', name: '二组', autoName: false, columns: [{ panes: [null] }], sizes: [1], focus: [0, 0] },
      ],
    })
    expect(decodeWorkbench(raw)).toEqual(EMPTY_WORKBENCH)
  })

  it.each([
    JSON.stringify({ v: 1 }),
    JSON.stringify({ v: PERSIST_VERSION, wb: { groups: [], activeGroupId: 'g1' } }),
    JSON.stringify({ v: PERSIST_VERSION, wb: { groups: [{ id: 'g1', name: 'x', autoName: true, columns: [], sizes: [], focus: [0, 0] }], activeGroupId: 'g1' } }),
    JSON.stringify({ v: PERSIST_VERSION, wb: { groups: [{ id: 'g1', name: 'x', autoName: true, columns: [{ panes: [{ id: 't1', base: a, content: { kind: 'file', rel: 1 } }] }], sizes: [1], focus: [0, 0] }], activeGroupId: 'g1' } }),
  ])('坏 global payload 返回 null: %s', (raw) => {
    expect(decodeWorkbench(raw)).toBeNull()
  })
})

function wbWithPersistenceFieldsRemoved(wb: Workbench): Workbench {
  return {
    activeGroupId: wb.activeGroupId,
    groups: wb.groups.map((group) => ({
      ...group,
      columns: group.columns.map((column) => ({
        panes: column.panes.map((tab) => {
          if (!tab) return null
          if (tab.content.kind === 'file') return { ...tab, content: { kind: 'file', rel: tab.content.rel } }
          if (tab.content.kind === 'terminal') {
            const content = { ...tab.content }
            delete content.incompatible
            return { ...tab, content }
          }
          return tab
        }),
      })),
    })),
  }
}

describe('session cleanup and diff', () => {
  it('死 session 清 id 但保留 pane，活着的 incompatible 只加标记', () => {
    const dead = pruneDeadSessions(sample(), new Set<string>())
    const deadTab = dead.groups[0].columns[0].panes[0]!
    expect(deadTab!.content).toEqual({ kind: 'terminal', seq: 0, rel: '', launcher: '跑测试' })
    const marked = markIncompatibleSessions(sample(), new Set(['']))
    expect(marked.groups[0].columns[0].panes[0]!.content).toMatchObject({ sessionId: '', incompatible: true })
  })

  it('空判断和差分保持全局 key 语义', () => {
    expect(GLOBAL_WORKBENCH_KEY).toBe('__global_workbench__')
    expect(isEmptyWorkbench(EMPTY_WORKBENCH)).toBe(true)
    expect(diffPayloads({ a: '1', b: '2' }, { a: '1', c: '3' })).toEqual({ changed: ['c'], removed: ['b'] })
  })
})
