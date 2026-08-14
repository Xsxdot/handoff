import { describe, expect, it } from 'vitest'
import { PROJECT_COLOR_COUNT, projectColorClass } from './projectColor'

describe('projectColorClass', () => {
  it('同一个 id 永远同一色', () => {
    const a = projectColorClass('proj-handoff')
    const b = projectColorClass('proj-handoff')
    expect(a).toBe(b)
  })

  it('与列表顺序无关——插入新项目不会让已有项目换色', () => {
    const before = ['p1', 'p2', 'p3'].map(projectColorClass)
    const after = ['p0', 'p1', 'p2', 'p3'].map(projectColorClass)
    expect(after.slice(1)).toEqual(before)
  })

  it('返回值落在调色板内', () => {
    for (const id of ['a', 'b', 'c', 'x-y-z', '中文项目', '']) {
      const cls = projectColorClass(id)
      const idx = Number(cls.replace('text-project-', ''))
      expect(idx).toBeGreaterThanOrEqual(1)
      expect(idx).toBeLessThanOrEqual(PROJECT_COLOR_COUNT)
    }
  })

  it('不同 id 会用到多于一个色（不是所有东西都撞成同一色）', () => {
    const ids = Array.from({ length: 30 }, (_, i) => `proj-${i}`)
    expect(new Set(ids.map(projectColorClass)).size).toBeGreaterThan(1)
  })
})
