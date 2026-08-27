import { describe, expect, it } from 'vitest'
import { isTerminalHostResponse } from './terminalHostResponse'

describe('isTerminalHostResponse', () => {
  it('认出 xterm.js Secondary DA：ESC [ > 0 ; 276 ; 0 c', () => {
    expect(isTerminalHostResponse('\x1b[>0;276;0c')).toBe(true)
  })

  it('认出 Primary DA 与 CPR', () => {
    expect(isTerminalHostResponse('\x1b[?1;2c')).toBe(true)
    expect(isTerminalHostResponse('\x1b[24;80R')).toBe(true)
  })

  it('认出 OSC 11 颜色回包（BEL 与 ST 两种收尾）', () => {
    expect(isTerminalHostResponse('\x1b]11;rgb:0b0b/0b0b/0c0c\x07')).toBe(true)
    expect(isTerminalHostResponse('\x1b]10;rgb:ffff/ffff/ffff\x1b\\')).toBe(true)
  })

  it('认出 DECRPM / DECRQSS 失败形态', () => {
    expect(isTerminalHostResponse('\x1b[2;0$y')).toBe(true)
    expect(isTerminalHostResponse('\x1b[?12;2$y')).toBe(true)
    expect(isTerminalHostResponse('\x1bP2$y\x1b\\')).toBe(true)
  })

  it('整段拼接的回放突发也算——切 tab 重放时 xterm 会一次吐出一串', () => {
    const burst =
      '\x1b[2;1R' +
      '\x1b[>0;276;0c' +
      '\x1b]11;rgb:0b0b/0b0b/0c0c\x07' +
      '\x1b]10;rgb:ffff/ffff/ffff\x07' +
      '\x1b]12;rgb:ffff/ffff/ffff\x07' +
      '\x1b[2;0$y'
    expect(isTerminalHostResponse(burst)).toBe(true)
  })

  it('SGR 鼠标滚轮报告不是回包，必须上送 PTY', () => {
    expect(isTerminalHostResponse('\x1b[<64;50;10M')).toBe(false)
    expect(isTerminalHostResponse('\x1b[<65;50;10M')).toBe(false)
  })

  it('方向键、普通输入、功能键不是回包', () => {
    expect(isTerminalHostResponse('\x1b[A')).toBe(false)
    expect(isTerminalHostResponse('\x1b[B')).toBe(false)
    expect(isTerminalHostResponse('\x1b[C')).toBe(false)
    expect(isTerminalHostResponse('\x1b[11~')).toBe(false)
    expect(isTerminalHostResponse('ls\r')).toBe(false)
    expect(isTerminalHostResponse('\x03')).toBe(false)
    expect(isTerminalHostResponse('')).toBe(false)
  })

  it('回包后面若粘了用户输入，整段放行——不能把人敲的字一起吞掉', () => {
    expect(isTerminalHostResponse('\x1b[>0;276;0cls')).toBe(false)
  })
})
