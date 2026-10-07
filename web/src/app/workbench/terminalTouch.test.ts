import { describe, expect, it } from 'vitest'
import { altBufferCursorReports } from './terminalWheel'
import { touchPastSlop, touchWheelDelta, tuiTouchIntent, tuiTouchLocksViewport, tuiTouchMoveAction } from './terminalTouch'

describe('touchWheelDelta', () => {
  it('手指上滑与滚轮向下同号（正），左滑与滚轮向右同号', () => {
    expect(touchWheelDelta({ x: 100, y: 200 }, { x: 100, y: 120 })).toEqual({ deltaX: 0, deltaY: 80 })
    expect(touchWheelDelta({ x: 100, y: 200 }, { x: 60, y: 200 })).toEqual({ deltaX: 40, deltaY: 0 })
  })
  it('手指下滑是滚轮向上（负）', () => {
    expect(touchWheelDelta({ x: 0, y: 10 }, { x: 0, y: 50 })).toEqual({ deltaX: 0, deltaY: -40 })
  })
})

describe('tuiTouchIntent', () => {
  const alt = {
    bufferType: 'alternate' as const,
    mouseTracking: true,
    altKey: false,
    shiftKey: false,
    isMac: false,
    touches: 1,
  }

  it('开了鼠标追踪就走滚轮报告，不管是不是备用屏', () => {
    expect(tuiTouchIntent(alt)).toBe('wheel')
    expect(tuiTouchIntent({ ...alt, bufferType: 'normal' })).toBe('wheel')
  })

  it('备用屏但没开追踪，跟 xterm 的滚轮回落一样发方向键', () => {
    expect(tuiTouchIntent({ ...alt, mouseTracking: false })).toBe('arrows')
  })

  it('主屏且没开追踪，留给 xterm 滚 scrollback', () => {
    expect(tuiTouchIntent({ ...alt, bufferType: 'normal', mouseTracking: false })).toBe('passthrough')
  })

  it('多指不截，划词修饰键放行', () => {
    expect(tuiTouchIntent({ ...alt, touches: 2 })).toBe('passthrough')
    expect(tuiTouchIntent({ ...alt, shiftKey: true, isMac: false })).toBe('passthrough')
    expect(tuiTouchIntent({ ...alt, altKey: true, isMac: true, shiftKey: false })).toBe('passthrough')
  })
})

describe('tuiTouchMoveAction', () => {
  it('滚轮或方向键时，死区内也要取消浏览器滚动，但先不写入 PTY', () => {
    // 第一下 touchmove 若不 preventDefault，WebView 把整段手势收走，
    // 后面的事件不可取消，慢滑看起来和没拦截一样。
    expect(tuiTouchMoveAction({ intent: 'wheel', pastSlop: false })).toEqual({ prevent: true, emit: false })
    expect(tuiTouchMoveAction({ intent: 'arrows', pastSlop: false })).toEqual({ prevent: true, emit: false })
    expect(tuiTouchMoveAction({ intent: 'wheel', pastSlop: true })).toEqual({ prevent: true, emit: true })
  })

  it('主屏放行时不取消，shell 回滚仍交给浏览器', () => {
    expect(tuiTouchMoveAction({ intent: 'passthrough', pastSlop: true })).toEqual({ prevent: false, emit: false })
  })
})

describe('touchPastSlop', () => {
  it('10px 以内当点击，超过才当滑动', () => {
    expect(touchPastSlop({ x: 0, y: 0 }, { x: 5, y: 5 })).toBe(false)
    expect(touchPastSlop({ x: 0, y: 0 }, { x: 0, y: 10 })).toBe(true)
  })
})

describe('tuiTouchLocksViewport', () => {
  it('鼠标追踪或备用屏时禁止浏览器滚 viewport，否则整屏 TUI 跟着手指走', () => {
    expect(tuiTouchLocksViewport({ bufferType: 'alternate', mouseTracking: false })).toBe(true)
    expect(tuiTouchLocksViewport({ bufferType: 'normal', mouseTracking: true })).toBe(true)
    expect(tuiTouchLocksViewport({ bufferType: 'normal', mouseTracking: false })).toBe(false)
  })
})

describe('altBufferCursorReports', () => {
  it('手指下滑（负 delta）是上方向，凑满一行才发', () => {
    const rem = { y: 0 }
    expect(altBufferCursorReports({ deltaY: -8, cellHeight: 16, remainder: rem, applicationCursorKeys: false })).toBe('')
    expect(altBufferCursorReports({ deltaY: -8, cellHeight: 16, remainder: rem, applicationCursorKeys: false })).toBe('\x1b[A')
  })
  it('正 delta 是下方向，应用光标键用 SS3', () => {
    const rem = { y: 0 }
    expect(altBufferCursorReports({ deltaY: 32, cellHeight: 16, remainder: rem, applicationCursorKeys: true })).toBe('\x1bOB\x1bOB')
  })
  it('一次最多 8 格', () => {
    const rem = { y: 0 }
    expect(altBufferCursorReports({ deltaY: -1600, cellHeight: 16, remainder: rem, applicationCursorKeys: false }))
      .toBe('\x1b[A'.repeat(8))
  })
})
