// streamGroups.test.ts —— 连续工具块分组：≥3 折组、打断重计、失败/未回音计数。
import { describe, expect, it } from 'vitest'
import { groupBlocks, minGroupSize } from './streamGroups'
import type { Block } from './frames'

// tool 造一个工具块；status 缺省 'ok'
function tool(key: string, status: string | null = 'ok'): Block {
  return {
    kind: 'tool', key, turn: 1, tool: 'commandExecution', input: 'x',
    inputTruncated: false, inputBytes: 0, status, output: '',
    outputTruncated: false, outputBytes: 0,
  }
}
const text = (key: string): Block => ({ kind: 'text', key, turn: 1, text: '正文' })
const quietEvent = (key: string, event = 'progress'): Block => ({
  kind: 'event', key, turn: 1, event, ts: '2026-08-17T00:00:00Z',
})

describe('groupBlocks', () => {
  it('连续 ≥3 个工具块折成一组，计数正确', () => {
    const items = groupBlocks([tool('a'), tool('b', 'error'), tool('c', null)])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ kind: 'toolGroup', failed: 1, pending: 1 })
    expect((items[0] as { tools: unknown[] }).tools).toHaveLength(3)
  })
  it('不足 minGroupSize 不折组，原样透出', () => {
    const items = groupBlocks([tool('a'), tool('b')])
    expect(items.map((i) => i.kind)).toEqual(['tool', 'tool'])
    expect(minGroupSize).toBe(3)
  })
  it('被非工具块打断的两段各自独立计数', () => {
    const items = groupBlocks([tool('a'), tool('b'), tool('c'), text('t'), tool('d'), tool('e')])
    expect(items.map((i) => i.kind)).toEqual(['toolGroup', 'text', 'tool', 'tool'])
  })
  it('组 key 取首个成员 key，稳定不随重渲染变化', () => {
    const items = groupBlocks([tool('f9'), tool('f10'), tool('f11')])
    expect((items[0] as { key: string }).key).toBe('g-f9')
  })
  it('忽略不可见 event，夹在工具之间仍折成一个组且不输出 event', () => {
    const items = groupBlocks([tool('a'), quietEvent('e1'), tool('b'), quietEvent('e2'), tool('c')])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ kind: 'toolGroup' })
    expect((items[0] as { tools: Block[] }).tools.map((t) => t.key)).toEqual(['a', 'b', 'c'])
  })
  it('有正文的 text 仍打断连续工具块', () => {
    const items = groupBlocks([tool('a'), text('body'), tool('b')])
    expect(items.map((i) => i.kind)).toEqual(['tool', 'text', 'tool'])
  })
})
