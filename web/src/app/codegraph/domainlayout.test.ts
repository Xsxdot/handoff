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
  // 卡片真实尺寸（DomainPanorama 的 CARD_W，高度实测 89~108，取上界）
  const CARD_W = 252
  const CARD_H = 108
  const overlaps = (pos: Record<string, [number, number]>, ids: string[]) => {
    const bad: string[] = []
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const a = pos[ids[i]]
        const b = pos[ids[j]]
        const ox = Math.min(a[0] + CARD_W, b[0] + CARD_W) - Math.max(a[0], b[0])
        const oy = Math.min(a[1] + CARD_H, b[1] + CARD_H) - Math.max(a[1], b[1])
        if (ox > 0 && oy > 0) bad.push(`${ids[i]}×${ids[j]}`)
      }
    }
    return bad
  }
  // 断言的是「矩形不相交」本身，不是「中心距离拉开」——后者弱得多：卡宽 252，
  // 两张卡中心差 130 就能满足「拉开 120」，实际却压在一起 122px。36 张卡的真机
  // 走查里 36 张有 23 对重叠，而当时的中心距断言全绿。
  it('不重叠：任意两张卡的矩形都不相交', () => {
    expect(overlaps(layoutDomains(agg, ids), ids)).toEqual([])
  })
  it('不重叠：领域多到 36 个时仍不相交（纵向重力不该把卡压成一条带）', () => {
    const many = Array.from({ length: 36 }, (_, i) => `m${i}`)
    const edges = new Map(many.slice(1).map((id, i) => [`m${i}|${id}`,
      { from: `m${i}`, to: id, pairs: [{ from: 'p', to: 'q', status: '' as const }] }]))
    const agg36 = { cards: {}, ifaces: {}, edges } as unknown as DomainAgg
    expect(overlaps(layoutDomains(agg36, many), many)).toEqual([])
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
