import { describe, expect, it } from 'vitest'
import {
  DRAG_BASE_MIME,
  DRAG_GROUP_MIME,
  DRAG_TAB_MIME,
  dropZoneAt,
  readDragBase,
  readDragGroup,
  readDragTab,
} from './paneDrop'

describe('dropZoneAt', () => {
  it('28% 四向边缘与 center 的坐标投影逐项正确', () => {
    expect(dropZoneAt(20, 200, 400, 400, true, true)).toBe('left')
    expect(dropZoneAt(380, 200, 400, 400, true, true)).toBe('right')
    expect(dropZoneAt(200, 20, 400, 400, true, true)).toBe('top')
    expect(dropZoneAt(200, 380, 400, 400, true, true)).toBe('bottom')
    expect(dropZoneAt(200, 200, 400, 400, true, true)).toBe('center')
  })

  it('不可加列或不可加第二格时，边缘投放退化为 center', () => {
    expect(dropZoneAt(20, 200, 400, 400, false, true)).toBe('center')
    expect(dropZoneAt(380, 200, 400, 400, false, true)).toBe('center')
    expect(dropZoneAt(200, 20, 400, 400, true, false)).toBe('center')
    expect(dropZoneAt(200, 380, 400, 400, true, false)).toBe('center')
  })

  it('无布局尺寸时一律 center，并严格区分合法与坏 JSON 源', () => {
    expect(dropZoneAt(0, 0, 0, 0, true, true)).toBe('center')
    const base = {
      key: '/repo/a', kind: 'workspace', path: '/repo/a', label: 'a', projectName: 'handoff', machine: '',
    }
    expect(readDragBase(JSON.stringify(base))).toEqual(base)
    expect(readDragBase('not-json')).toBeNull()
    expect(readDragBase(JSON.stringify({ ...base, path: 1 }))).toBeNull()
    expect(readDragTab(JSON.stringify({ groupId: 'g1', tabId: 't1' }))).toEqual({ groupId: 'g1', tabId: 't1' })
    expect(readDragTab(JSON.stringify({ groupId: 'g1' }))).toBeNull()
    expect(readDragGroup(JSON.stringify({ groupId: 'g1' }))).toEqual({ groupId: 'g1' })
    expect(readDragGroup('null')).toBeNull()
  })
})

it('拖放 MIME 常量保持为工作台专用边界', () => {
  expect(DRAG_BASE_MIME).toBe('text/handoff-base')
  expect(DRAG_TAB_MIME).toBe('text/handoff-tab')
  expect(DRAG_GROUP_MIME).toBe('text/handoff-group')
})
