import { describe, expect, it } from 'vitest'
import {
  altBufferWheelReports, mouseEncodingOf, pointerCell, wheelForcesSelection,
} from './terminalWheel'

describe('pointerCell', () => {
  const rect = { left: 0, top: 0, width: 800, height: 480 }
  it('按格子算 1-based 行列', () => {
    expect(pointerCell(50, 50, rect, 100, 30)).toEqual({ col: 7, row: 4 })
  })
  it('贴边不超过 cols/rows', () => {
    expect(pointerCell(799, 479, rect, 100, 30)).toEqual({ col: 100, row: 30 })
  })
})

describe('wheelForcesSelection', () => {
  it('Mac 上 Option 划词，Shift 不划', () => {
    expect(wheelForcesSelection({ altKey: true, shiftKey: false }, true)).toBe(true)
    expect(wheelForcesSelection({ altKey: false, shiftKey: true }, true)).toBe(false)
  })
  it('非 Mac 上 Shift 划词', () => {
    expect(wheelForcesSelection({ altKey: false, shiftKey: true }, false)).toBe(true)
    expect(wheelForcesSelection({ altKey: true, shiftKey: false }, false)).toBe(false)
  })
})

describe('mouseEncodingOf', () => {
  it('读 xterm core 的 activeEncoding，读不到当 SGR', () => {
    expect(mouseEncodingOf({ _core: { coreMouseService: { activeEncoding: 'SGR_PIXELS' } } })).toBe('SGR_PIXELS')
    expect(mouseEncodingOf({ _core: { coreMouseService: { activeEncoding: 'DEFAULT' } } })).toBe('DEFAULT')
    expect(mouseEncodingOf({})).toBe('SGR')
  })
})

describe('altBufferWheelReports', () => {
  const base = {
    cellWidth: 8, cellHeight: 16, remainder: { x: 0, y: 0 },
    col: 10, row: 4, pixelX: 80, pixelY: 64,
    shift: false, alt: false, ctrl: false, encoding: 'SGR' as const,
  }
  it('凑满一行才发，坐标是指针格子', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -8 })).toBe('')
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -8 })).toBe('\x1b[<64;10;4M')
  })
  it('一次像素距离换成多格同坐标报告', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -160, col: 12, row: 5 }))
      .toBe('\x1b[<64;12;5M'.repeat(10))
  })
  it('向下是 65，一次最多 32 格', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: 1600, col: 3, row: 8 }))
      .toBe('\x1b[<65;3;8M'.repeat(32))
  })
  it('横滑是 66/67，与纵滑分开累计', () => {
    const rem = { x: 0, y: 0 }
    // cellWidth=8：-160px → 20 格；+16px → 2 格。plan 原稿写成 repeat(10)/单格，
    // 是把纵滑 cellHeight=16 的除法套到了横滑上；公式与纵滑同一条 ticksFromDelta。
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: -160, deltaY: 0 }))
      .toBe('\x1b[<66;10;4M'.repeat(20))
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 16, deltaY: 0 }))
      .toBe('\x1b[<67;10;4M'.repeat(2))
  })
  it('Ctrl 加进按钮码（64+16=80）', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -16, ctrl: true }))
      .toBe('\x1b[<80;10;4M')
  })
  it('SGR_PIXELS 用像素坐标不是格子', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({
      ...base, remainder: rem, deltaX: 0, deltaY: -16, encoding: 'SGR_PIXELS', pixelX: 80, pixelY: 64,
    })).toBe('\x1b[<64;80;64M')
  })
  it('非法格子不发', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -16, col: 0, row: 1 })).toBe('')
  })
})
