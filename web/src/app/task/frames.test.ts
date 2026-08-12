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
