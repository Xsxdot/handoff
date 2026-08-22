// format.ts 的单元测试：这些是纯函数，不需要 DOM，直接喂值断言。
//
// 覆盖重点是「缺省怎么显示」——B80 的产品决策是「如实缺席」：没有分母就不显
// 百分比，没有用量就不显用量，绝不用 0 或 — 占位。
import { describe, expect, it } from 'vitest'

import { formatCost, formatCumulativeLine, formatDuration, formatExecutorLine, formatTokens } from './format'

describe('formatTokens', () => {
  it('千位以上用 k 缩写并保留一位小数', () => {
    expect(formatTokens(24673)).toBe('24.7k')
    expect(formatTokens(258400)).toBe('258.4k')
  })
  it('千位以下显示原值', () => {
    expect(formatTokens(999)).toBe('999')
  })
  it('百万以上升到 M、十亿以上升到 B', () => {
    expect(formatTokens(9_489_200)).toBe('9.5M')
    expect(formatTokens(1_000_000)).toBe('1.0M')
    expect(formatTokens(2_300_000_000)).toBe('2.3B')
  })
  it('四舍五入后满 1000 的直接升档，不出现 1000.0k', () => {
    expect(formatTokens(999_950)).toBe('1.0M')
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

describe('formatCost', () => {
  it('自报且完整：直接显金额，无 ≈、无小标', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'reported' }))
      .toEqual({ text: '$4.20', hint: '' })
  })
  it('估算：带 ≈ 与「估算」小标', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'estimated' }))
      .toEqual({ text: '≈$4.20', hint: '估算' })
  })
  it('不全：带 ≈ 与「不全」小标——它是下界不是近似值', () => {
    expect(formatCost({ ticks: 42_000_000_000, state: 'partial' }))
      .toEqual({ text: '≈$4.20', hint: '不全' })
  })
  it('未知：显 — 而不是 $0.00', () => {
    expect(formatCost({ ticks: 0, state: 'unknown' }))
      .toEqual({ text: '—', hint: '' })
  })
  it('金额不足一分也不显示成 $0.00', () => {
    // 0.004 美元 → 保留两位会变 $0.00，必须换更细的位数
    const r = formatCost({ ticks: 40_000_000, state: 'reported' })
    expect(r.text).not.toBe('$0.00')
  })
})

describe('formatCumulativeLine', () => {
  const base = { cumulative: {
    input_tokens: 340_200, cached_tokens: 820_500,
    output_tokens: 39_300, total_tokens: 1_200_000,
    cost: { ticks: 42_000_000_000, state: 'estimated' as const },
  } }
  it('五项齐全时按原型顺序排列', () => {
    expect(formatCumulativeLine(base as never))
      .toBe('1.2M · 输入 340.2k · 缓存 820.5k · 输出 39.3k · ≈$4.20')
  })
  it('没有累计时返回空串，由调用方决定不渲染', () => {
    expect(formatCumulativeLine({} as never)).toBe('')
  })
  it('没有花费信息时只显四项 token', () => {
    const noCost = { cumulative: { ...base.cumulative, cost: undefined } }
    expect(formatCumulativeLine(noCost as never))
      .toBe('1.2M · 输入 340.2k · 缓存 820.5k · 输出 39.3k')
  })
})

describe('formatDuration', () => {
  it('毫秒档：不足一秒直接给 ms', () => {
    expect(formatDuration(0)).toBe('0ms')
    expect(formatDuration(1)).toBe('1ms')
    expect(formatDuration(999)).toBe('999ms')
  })
  it('秒档：保留一位小数', () => {
    expect(formatDuration(1000)).toBe('1.0s')
    expect(formatDuration(1500)).toBe('1.5s')
    expect(formatDuration(59_940)).toBe('59.9s')
  })
  it('分档与时档：升档判据用四舍五入后的值', () => {
    expect(formatDuration(59_950)).toBe('1m0s')
    expect(formatDuration(60_000)).toBe('1m0s')
    expect(formatDuration(90_000)).toBe('1m30s')
    expect(formatDuration(3_599_000)).toBe('59m59s')
    expect(formatDuration(3_600_000)).toBe('1h0m')
    expect(formatDuration(7_500_000)).toBe('2h5m')
    expect(formatDuration(7_530_000)).toBe('2h6m')
  })
  it('负数夹到 0：调用方用「缺席」表达未知，不用负数', () => {
    expect(formatDuration(-1)).toBe('0ms')
  })
})
