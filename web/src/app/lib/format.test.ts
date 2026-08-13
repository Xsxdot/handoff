// format.ts 的单元测试：这些是纯函数，不需要 DOM，直接喂值断言。
//
// 覆盖重点是「缺省怎么显示」——B80 的产品决策是「如实缺席」：没有分母就不显
// 百分比，没有用量就不显用量，绝不用 0 或 — 占位。
import { describe, expect, it } from 'vitest'

import { formatExecutorLine, formatTokens } from './format'

describe('formatTokens', () => {
  it('千位以上用 k 缩写并保留一位小数', () => {
    expect(formatTokens(24673)).toBe('24.7k')
    expect(formatTokens(258400)).toBe('258.4k')
  })
  it('千位以下显示原值', () => {
    expect(formatTokens(999)).toBe('999')
  })
})

describe('formatExecutorLine', () => {
  const base = { executor: 'codex', model: 'ignored' }

  it('有分子分母时显示百分比', () => {
    expect(
      formatExecutorLine({
        ...base,
        actual_model: 'gpt-5.6-sol',
        usage: { context_tokens: 24673, context_window: 258400 },
      } as never),
    ).toBe('codex · gpt-5.6-sol · 24.7k / 258.4k (10%)')
  })

  it('只有分子时只显绝对值，不猜分母', () => {
    expect(
      formatExecutorLine({
        ...base,
        executor: 'claudecode',
        actual_model: 'k3-256k',
        usage: { context_tokens: 121801 },
      } as never),
    ).toBe('claudecode · k3-256k · 121.8k tokens')
  })

  it('回合未开始时只显执行器与模型名', () => {
    expect(
      formatExecutorLine({ ...base, actual_model: 'gpt-5.6-sol' } as never),
    ).toBe('codex · gpt-5.6-sol')
  })

  it('什么都没有时只显执行器；入参 model 不再显示', () => {
    expect(formatExecutorLine({ executor: 'opencode', model: 'deepseek-v4-flash' } as never))
      .toBe('opencode')
  })

  it('连执行器都没有的老任务显示（缺省）', () => {
    expect(formatExecutorLine({} as never)).toBe('（缺省）')
  })
})
