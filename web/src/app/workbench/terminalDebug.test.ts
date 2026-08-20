import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  TERMINAL_DEBUG_KEY,
  describeElement,
  logTermFocus,
  logTermInput,
  logTermResize,
  terminalDebugEnabled,
} from './terminalDebug'

beforeEach(() => {
  localStorage.clear()
  vi.restoreAllMocks()
})
afterEach(() => {
  localStorage.clear()
})

describe('terminalDebugEnabled', () => {
  it('默认关', () => {
    expect(terminalDebugEnabled()).toBe(false)
  })

  it('只认字面量 "1"，别的值一律当关', () => {
    for (const v of ['0', 'true', 'yes', '']) {
      localStorage.setItem(TERMINAL_DEBUG_KEY, v)
      expect(terminalDebugEnabled()).toBe(false)
    }
    localStorage.setItem(TERMINAL_DEBUG_KEY, '1')
    expect(terminalDebugEnabled()).toBe(true)
  })

  it('localStorage 抛异常时当作关，不往上传', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('隐私模式')
    })
    expect(terminalDebugEnabled()).toBe(false)
  })
})

// 这一组是承重的：日志默认必须完全静默。
// 终端输入是每次按键一条，且用户敲的东西可能含密码——「不小心常开」的代价
// 是把凭据留在控制台里，比少一条日志严重得多。
describe('关闭时完全静默', () => {
  it('三个 log 函数一条都不输出', () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {})
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    logTermInput('main', 'ls -al', 'open')
    // 连「输入可能丢了」这条 warn 也不该在关闭时冒出来
    logTermInput('main', 'ls -al', 'connecting')
    logTermFocus('main', 'blur', 'div.foo')
    logTermResize('main', 80, 24, 'attach')

    expect(debug).not.toHaveBeenCalled()
    expect(warn).not.toHaveBeenCalled()
  })
})

describe('打开时的读数', () => {
  beforeEach(() => {
    localStorage.setItem(TERMINAL_DEBUG_KEY, '1')
  })

  it('通道未就绪的输入走 warn，并带上原文与字符数', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    logTermInput('main', 'ps -ef', 'connecting')
    expect(warn).toHaveBeenCalledTimes(1)
    const payload = warn.mock.calls[0][1] as Record<string, unknown>
    expect(payload.字符数).toBe(6)
    expect(payload.状态).toBe('connecting')
  })

  it('原文经 JSON.stringify，退格与 ESC 这类不可见字符看得见', () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {})
    // espanso 的展开动作正是「先退格删掉触发词再打替换文本」，
    // 看不见退格就判不了案
    logTermInput('main', '\x7f\x7f\x1b', 'open')
    const payload = debug.mock.calls[0][1] as Record<string, unknown>
    expect(payload.原文).toBe(JSON.stringify('\x7f\x7f\x1b'))
    expect(payload.字符数).toBe(3)
  })
})

describe('describeElement', () => {
  it('null 有明确的表示，不返回空串', () => {
    expect(describeElement(null)).toBe('(null)')
  })

  it('带 class 时拼成 tag.a.b', () => {
    const el = document.createElement('textarea')
    el.className = '  xterm-helper-textarea  foo '
    expect(describeElement(el)).toBe('textarea.xterm-helper-textarea.foo')
  })

  it('没有 class 时只给 tag', () => {
    expect(describeElement(document.createElement('div'))).toBe('div')
  })

  it('SVG 元素的 className 不是 string，不能把它当串切', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    expect(() => describeElement(svg)).not.toThrow()
    expect(describeElement(svg)).toBe('svg')
  })
})
