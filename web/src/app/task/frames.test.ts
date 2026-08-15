// frames.ts 的边界测试：半行切分、坏行、保活空行。
//
// 这些是本期唯一有真实逻辑复杂度的地方，所以按穷举写：一行坏数据不该让整条
// 时间线白屏，但也不能静默——每条降级都要能从返回值里数出来。
import { describe, expect, it } from 'vitest'
import { scanLines } from './frames'

const line = (o: Record<string, unknown>) => JSON.stringify(o) + '\n'

describe('scanLines 半行与坏行', () => {
  it('完整行全部解析，rest 为空', () => {
    const text = line({ seq: 1, ts: 't', turn: 1, type: 'turn_start', reason: 'dispatch' }) +
      line({ seq: 2, ts: 't', turn: 1, type: 'text', part: 'p01', delta: '你好' })
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([1, 2])
    expect(r.bad).toBe(0)
    expect(r.rest).toBe('')
  })

  it('一帧被拆在两次 chunk 之间：先留在 rest，补齐后解析', () => {
    const whole = line({ seq: 7, ts: 't', turn: 1, type: 'text', part: 'p01', delta: '半行' })
    const cut = Math.floor(whole.length / 2)
    const first = scanLines('', whole.slice(0, cut))
    expect(first.frames).toHaveLength(0)
    expect(first.bad).toBe(0)
    expect(first.rest).toBe(whole.slice(0, cut))

    const second = scanLines(first.rest, whole.slice(cut))
    expect(second.frames.map((f) => f.seq)).toEqual([7])
    expect(second.rest).toBe('')
  })

  it('非 JSON 的坏行：跳过并计数，不影响同批其余帧', () => {
    const text = line({ seq: 1, ts: 't', turn: 1, type: 'text', delta: 'a' }) +
      '这不是 JSON\n' +
      line({ seq: 2, ts: 't', turn: 1, type: 'text', delta: 'b' })
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([1, 2])
    expect(r.bad).toBe(1)
  })

  it('是 JSON 但缺 seq / 缺 type / 不是对象：都算坏行', () => {
    const text = line({ ts: 't', turn: 1, type: 'text' }) +
      line({ seq: 3, ts: 't', turn: 1 }) +
      '[1,2,3]\n' +
      'null\n'
    const r = scanLines('', text)
    expect(r.frames).toHaveLength(0)
    expect(r.bad).toBe(4)
  })

  it('保活空行与纯空白行不计坏行（agentd follow 空闲每 20s 发一个换行）', () => {
    const text = '\n' + '   \n' + line({ seq: 9, ts: 't', turn: 1, type: 'text', delta: 'x' }) + '\n'
    const r = scanLines('', text)
    expect(r.frames.map((f) => f.seq)).toEqual([9])
    expect(r.bad).toBe(0)
  })

  it('没有任何换行时整段留在 rest，不误判为坏行', () => {
    const r = scanLines('', '{"seq":1')
    expect(r.frames).toHaveLength(0)
    expect(r.bad).toBe(0)
    expect(r.rest).toBe('{"seq":1')
  })
})

import { buildBlocks, toolState, turnsOf, type ToolBlock } from './frames'
import type { Frame } from '../../api/types'

const f = (o: Partial<Frame> & { seq: number; type: string }): Frame =>
  ({ ts: '2026-08-12T10:00:00+08:00', turn: 1, ...o }) as Frame

describe('buildBlocks delta 合并', () => {
  it('同 (turn, type, part) 的连续帧拼成一块', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: '我来' }),
      f({ seq: 2, type: 'text', part: 'p01', delta: '实现它。' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'text', text: '我来实现它。' })
  })

  it('part 变化开新块', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'text', part: 'p02', delta: 'b' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['text', 'text'])
  })

  it('turn 变化开新块（part 跨回合会重复）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, turn: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, turn: 2, type: 'text', part: 'p01', delta: 'b' }),
    ])
    expect(blocks).toHaveLength(2)
  })

  it('type 变化开新块：思维链绝不并进正文', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'reasoning', part: 'p01', delta: '先想想' }),
      f({ seq: 2, type: 'text', part: 'p01', delta: '我说' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['thinking', 'text'])
  })

  it('中间插入其它帧后同 part 不再续接（规则是「连续」）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'tool_call', part: 'p02', tool: 'bash', input: 'ls' }),
      f({ seq: 3, type: 'text', part: 'p01', delta: 'b' }),
    ])
    expect(blocks.map((b) => b.kind)).toEqual(['text', 'tool', 'text'])
  })
})

describe('buildBlocks 工具配对', () => {
  it('正常配对：调用与结果合成一张卡', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p03', tool: 'bash', input: 'go test ./...' }),
      f({ seq: 2, type: 'tool_result', part: 'p03', status: 'ok', output: 'ok\t0.2s' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'tool', tool: 'bash', input: 'go test ./...', status: 'ok', output: 'ok\t0.2s' })
  })

  it('结果先于调用到达：仍合成一张卡，字段互补', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_result', part: 'p03', status: 'ok', output: 'done' }),
      f({ seq: 2, type: 'tool_call', part: 'p03', tool: 'bash', input: 'ls' }),
    ])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'tool', tool: 'bash', input: 'ls', status: 'ok', output: 'done' })
  })

  it('不同回合复用同一个 part 不串台', () => {
    const blocks = buildBlocks([
      f({ seq: 1, turn: 1, type: 'tool_call', part: 'p01', tool: 'read', input: 'a.go' }),
      f({ seq: 2, turn: 1, type: 'tool_result', part: 'p01', status: 'ok', output: 'A' }),
      f({ seq: 3, turn: 2, type: 'tool_call', part: 'p01', tool: 'read', input: 'b.go' }),
    ])
    expect(blocks).toHaveLength(2)
    expect((blocks[1] as ToolBlock).status).toBeNull() // 第二回合那次还没有结果
  })

  it('输入与输出的截断各自独立记账', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'tool_call', part: 'p01', tool: 'write', input: '大段…', truncated: true, bytes: 90000 }),
      f({ seq: 2, type: 'tool_result', part: 'p01', status: 'ok', output: '更大段…', truncated: true, bytes: 141882 }),
    ])
    expect(blocks[0]).toMatchObject({
      inputTruncated: true, inputBytes: 90000,
      outputTruncated: true, outputBytes: 141882,
    })
  })
})

describe('buildBlocks 其余帧型', () => {
  it('turn_start 渲染成回合块，reason 原样带出', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'turn_start', reason: 'send' })])
    expect(blocks[0]).toMatchObject({ kind: 'turn', turn: 1, reason: 'send' })
  })

  it('event 渲染成事件块，带 event 类型名', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'event', ref_seq: 88, event: 'permission_request' })])
    expect(blocks[0]).toMatchObject({ kind: 'event', event: 'permission_request' })
  })

  it('未知 type：不丢弃、不抛异常，保留原始 JSON', () => {
    const blocks = buildBlocks([f({ seq: 1, type: 'checkpoint', part: 'p01' })])
    expect(blocks).toHaveLength(1)
    expect(blocks[0]).toMatchObject({ kind: 'unknown', type: 'checkpoint' })
    expect((blocks[0] as { raw: string }).raw).toContain('checkpoint')
  })

  it('缺 delta / 缺 tool 的帧不抛异常，按空串兜底', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01' }),
      f({ seq: 2, type: 'tool_call', part: 'p02' }),
    ])
    expect(blocks[0]).toMatchObject({ kind: 'text', text: '' })
    expect(blocks[1]).toMatchObject({ kind: 'tool', tool: '', input: '' })
  })

  it('每个块的 key 唯一（React 列表键）', () => {
    const blocks = buildBlocks([
      f({ seq: 1, type: 'text', part: 'p01', delta: 'a' }),
      f({ seq: 2, type: 'text', part: 'p02', delta: 'b' }),
      f({ seq: 3, type: 'tool_call', part: 'p03', tool: 'ls' }),
    ])
    expect(new Set(blocks.map((b) => b.key)).size).toBe(blocks.length)
  })
})

describe('toolState', () => {
  it('有结果：ok → ok，其余 → error', () => {
    expect(toolState('ok', 'completed')).toBe('ok')
    expect(toolState('error', 'completed')).toBe('error')
    expect(toolState('上游原文', 'running')).toBe('error')
  })

  it('无结果 + 任务 running/waiting_answer → 进行中', () => {
    expect(toolState(null, 'running')).toBe('running')
    // waiting_answer 归入「进行中」是刻意的：回合被工单挡住了，调用确实还在等
    expect(toolState(null, 'waiting_answer')).toBe('running')
  })

  it('无结果 + 任务已停 → 未返回（不许假装还在跑）', () => {
    for (const s of ['waiting_review', 'completed', 'failed', 'pending']) {
      expect(toolState(null, s)).toBe('gone')
    }
  })
})

describe('turnsOf', () => {
  it('升序去重', () => {
    expect(turnsOf([
      f({ seq: 1, turn: 2, type: 'text' }),
      f({ seq: 2, turn: 2, type: 'text' }),
      f({ seq: 3, turn: 3, type: 'text' }),
    ])).toEqual([2, 3])
  })

  it('空输入返回空数组', () => {
    expect(turnsOf([])).toEqual([])
  })
})
