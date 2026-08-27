import { describe, expect, it } from 'vitest'
import { altBufferWheelSgr, pointerCell, wheelPixelDeltaY } from './terminalWheel'

describe('pointerCell', () => {
  const rect = { left: 0, top: 0, width: 800, height: 480 }

  it('按格子算 1-based 行列', () => {
    expect(pointerCell(50, 50, rect, 100, 30)).toEqual({ col: 7, row: 4 })
  })

  it('贴边不超过 cols/rows', () => {
    expect(pointerCell(799, 479, rect, 100, 30)).toEqual({ col: 100, row: 30 })
  })
})

describe('altBufferWheelSgr', () => {
  it('凑满一行才发，坐标是指针格子', () => {
    const rem = { value: 0 }
    expect(altBufferWheelSgr(-8, 16, rem, 10, 4)).toBe('')
    expect(altBufferWheelSgr(-8, 16, rem, 10, 4)).toBe('\x1b[<64;10;4M')
  })

  it('一次像素距离换成多格同坐标报告——xterm 原生每次只发一格所以会慢', () => {
    const rem = { value: 0 }
    const seq = altBufferWheelSgr(-80, 16, rem, 12, 5)
    expect(seq).toBe('\x1b[<64;12;5M'.repeat(5))
  })

  it('向下是 65，一次最多 8 格——32 格会把 OpenTUI/Grok 的输入灌爆', () => {
    const rem = { value: 0 }
    const seq = altBufferWheelSgr(1600, 16, rem, 3, 8)
    expect(seq).toBe('\x1b[<65;3;8M'.repeat(8))
  })

  it('非法格子不发——不能再拿假坐标把整屏划走', () => {
    const rem = { value: 0 }
    expect(altBufferWheelSgr(-16, 16, rem, 0, 1)).toBe('')
    expect(altBufferWheelSgr(-16, 16, rem, 1, 0)).toBe('')
  })
})

describe('wheelPixelDeltaY', () => {
  it('像素模式原样返回', () => {
    expect(wheelPixelDeltaY({ deltaY: -160, deltaMode: 0 }, 16, 30)).toBe(-160)
  })

  it('行模式按单元格高度换成像素——触控板是像素、鼠标滚轮常是行', () => {
    expect(wheelPixelDeltaY({ deltaY: -3, deltaMode: 1 }, 16, 30)).toBe(-48)
  })
})
