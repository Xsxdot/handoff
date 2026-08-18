import { describe, expect, it } from 'vitest'
import { dropZoneAt } from './paneDrop'

describe('dropZoneAt', () => {
  it('左侧边缘算 left，右侧边缘算 right，中间算 center', () => {
    expect(dropZoneAt(10, 400, true)).toBe('left')
    expect(dropZoneAt(390, 400, true)).toBe('right')
    expect(dropZoneAt(200, 400, true)).toBe('center')
  })

  it('边缘区取 25% 与 120px 的较小者——宽栏上 25% 会让人频繁误触发分屏', () => {
    // 800px 宽：25% 是 200px，但上限 120px 生效
    expect(dropZoneAt(150, 800, true)).toBe('center')
    expect(dropZoneAt(110, 800, true)).toBe('left')
    expect(dropZoneAt(690, 800, true)).toBe('right')
  })

  it('窄栏上 25% 小于 120px，此时 25% 生效', () => {
    // 200px 宽：25% 是 50px
    expect(dropZoneAt(40, 200, true)).toBe('left')
    expect(dropZoneAt(60, 200, true)).toBe('center')
  })

  it('不能再分屏时边缘退化成 center，不给一次落空的拖拽', () => {
    expect(dropZoneAt(10, 400, false)).toBe('center')
    expect(dropZoneAt(390, 400, false)).toBe('center')
  })

  it('宽度为 0（还没布局完）时一律 center，不做除法', () => {
    expect(dropZoneAt(0, 0, true)).toBe('center')
  })
})
