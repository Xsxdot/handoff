import { describe, expect, it } from 'vitest'
import type { DomainAgg } from './domains'
import { layoutDomains } from './domainlayout'

// 五个领域、两条调用边的最小聚合结果（只用到 layoutDomains 关心的字段）
const agg = {
  cards: {},
  ifaces: {},
  edges: new Map([
    ['a|b', { from: 'a', to: 'b', pairs: [{ from: 'x', to: 'y', status: '' as const }] }],
    ['b|c', { from: 'b', to: 'c', pairs: [{ from: 'y', to: 'z', status: '' as const }] }],
  ]),
} as unknown as DomainAgg
const ids = ['a', 'b', 'c', 'd', 'e']

describe('layoutDomains', () => {
  it('确定性：同样输入两次结果逐位相同（不许用随机数）', () => {
    expect(layoutDomains(agg, ids)).toEqual(layoutDomains(agg, ids))
  })
  it('不重叠：任意两张卡中心距离都拉开', () => {
    const pos = layoutDomains(agg, ids)
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const [x1, y1] = pos[ids[i]]
        const [x2, y2] = pos[ids[j]]
        expect(Math.hypot(x1 - x2, y1 - y2)).toBeGreaterThan(120)
      }
    }
  })
  it('有调用关系的领域比无关领域更近', () => {
    const pos = layoutDomains(agg, ids)
    const d = (p: string, q: string) => Math.hypot(pos[p][0] - pos[q][0], pos[p][1] - pos[q][1])
    expect(d('a', 'b')).toBeLessThan(d('a', 'd') + d('a', 'e'))
  })
  it('seed 生效：给定初始位置时从它继续松弛而不是推倒重来', () => {
    const far = layoutDomains(agg, ids, { a: [4000, 4000] })
    expect(far.a[0]).toBeGreaterThan(layoutDomains(agg, ids).a[0])
  })
  it('不越界：坐标恒在画布内（x≥30, y≥64）', () => {
    const pos = layoutDomains(agg, ids)
    for (const id of ids) {
      expect(pos[id][0]).toBeGreaterThanOrEqual(30)
      expect(pos[id][1]).toBeGreaterThanOrEqual(64)
    }
  })
})
