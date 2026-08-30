// terminalInput 的回归网。
//
// 这里的事件序列**不是编出来的**：它们逐字段抄自一次真实 WKWebView 取证——
// 一个最小 WKWebView 靶子里装同版本 xterm，由人在真实中文输入法与真实 espanso
// 下敲出来，把 DOM 事件按 capture 顺序落盘。字段（key / keyCode / charCode /
// inputType / data / composed）与那份轨迹一致，事件先后也一致（②那条尤其要紧：
// WebKit 把 input 排在 keydown **之前**，顺序写反了这个测试就什么都测不到）。
//
// 每个缺口都配一条「不装补漏」的对照断言。没有对照，这些用例明天被改成永远
// 为真也没人看得出来——它们断言的恰恰是「xterm 自己会漏」。
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { Terminal } from '@xterm/xterm'
import { installTerminalInputFix, type TerminalInputFix } from './terminalInput'

beforeAll(() => {
  // xterm 的 CoreBrowserService 要读 devicePixelRatio，jsdom 没有 matchMedia。
  // 只补这一个函数就够真 xterm 跑起来——键盘路径不碰渲染。
  globalThis.matchMedia = ((q: string) => ({
    matches: false,
    media: q,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof globalThis.matchMedia
})

// 一个装好的靶子：真 xterm + 真 DOM，data 收在数组里。
interface Rig {
  term: Terminal
  ta: HTMLTextAreaElement
  data: string[]
  fix: TerminalInputFix | null
  dispose: () => void
}

// makeRig 起一个终端。withFix=false 用来立对照组，证明缺口确实存在。
function makeRig(withFix: boolean): Rig {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const term = new Terminal({ cols: 80, rows: 24 })
  term.open(host)
  const ta = term.textarea as HTMLTextAreaElement
  const data: string[] = []
  term.onData((d) => data.push(d))
  const fix = withFix ? installTerminalInputFix(term, host, 'test') : null
  return {
    term,
    ta,
    data,
    fix,
    dispose: () => {
      fix?.dispose()
      term.dispose()
      host.remove()
    },
  }
}

// key 造一个键盘事件。jsdom 认 keyCode/charCode/which 的 init 字段，但不保证
// 各版本都认，所以造完再核一遍、对不上就直接定义——这些遗留字段正是 xterm
// 判分支的依据，写不进去测试就会假绿。
function key(
  type: 'keydown' | 'keypress' | 'keyup',
  init: {
    key: string; keyCode: number; charCode?: number
    shiftKey?: boolean; metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean
    code?: string
  },
): KeyboardEvent {
  const charCode = init.charCode ?? 0
  const ev = new KeyboardEvent(type, {
    key: init.key,
    code: init.code ?? '',
    shiftKey: init.shiftKey ?? false,
    metaKey: init.metaKey ?? false,
    ctrlKey: init.ctrlKey ?? false,
    altKey: init.altKey ?? false,
    bubbles: true,
    cancelable: true,
    composed: true,
  })
  for (const [name, value] of [
    ['keyCode', init.keyCode],
    ['charCode', charCode],
    ['which', charCode || init.keyCode],
  ] as const) {
    if ((ev as unknown as Record<string, number>)[name] !== value) {
      Object.defineProperty(ev, name, { get: () => value, configurable: true })
    }
  }
  return ev
}

// input 造一个 InputEvent，并顺手把文本写进 textarea——WebKit 是先改
// textarea 再派事件的，CompositionHelper 的 `_handleAnyTextareaChanges`
// 正是靠比对 textarea 前后值判断该不该补发，少了这一步就不是真实现场。
function input(ta: HTMLTextAreaElement, data: string, composed = true): InputEvent {
  ta.value += data
  return new InputEvent('input', {
    data,
    inputType: 'insertText',
    bubbles: true,
    composed,
  })
}

let rig: Rig | null = null
beforeEach(() => {
  rig?.dispose()
  rig = null
})

// espanso 展开：整串文本被塞进一个键事件（trace: key="git status" charCode=103）
describe('① espanso 把整串文本塞进一个键事件', () => {
  const replay = (r: Rig): void => {
    r.ta.dispatchEvent(key('keydown', { key: 'git status', keyCode: 71 }))
    r.ta.dispatchEvent(key('keypress', { key: 'git status', keyCode: 103, charCode: 103 }))
    r.ta.dispatchEvent(input(r.ta, 'git status'))
  }

  it('不装补漏时 xterm 只发出首字符（这就是被报的故障）', () => {
    rig = makeRig(false)
    replay(rig)
    expect(rig.data).toEqual(['g'])
  })

  it('装上补漏后整串完好地发出去，且只发一次', () => {
    rig = makeRig(true)
    replay(rig)
    expect(rig.data).toEqual(['git status'])
  })

  it('另一条真实展开（:ps）同样完整', () => {
    rig = makeRig(true)
    const text = 'ps -ef | grep  '
    rig.ta.dispatchEvent(key('keydown', { key: text, keyCode: 80 }))
    rig.ta.dispatchEvent(key('keypress', { key: text, keyCode: 112, charCode: 112 }))
    rig.ta.dispatchEvent(input(rig.ta, text))
    expect(rig.data).toEqual([text])
  })
})

// 中文输入法标点：WebKit 把 input 排在 keydown 之前
describe('② 中文输入法下的标点', () => {
  // 按住 Shift 后的第一下：Shift 的 keydown 把 _keyDownSeen 置真，随后到来的
  // input 撞上 `(!composed || !_keyDownSeen)` 这道门被丢弃。
  const firstPress = (r: Rig, ch: string, code: number): void => {
    r.ta.dispatchEvent(key('keydown', { key: 'Shift', keyCode: 16, shiftKey: true }))
    r.ta.dispatchEvent(input(r.ta, ch))
    r.ta.dispatchEvent(key('keydown', { key: ch, keyCode: 229, shiftKey: true }))
    r.ta.dispatchEvent(key('keyup', { key: ch, keyCode: code, shiftKey: true }))
  }

  it('不装补漏时按下 Shift 后的第一个标点整个丢掉', () => {
    rig = makeRig(false)
    firstPress(rig, '？', 191)
    expect(rig.data).toEqual([])
  })

  it('装上补漏后第一下就出得来', () => {
    rig = makeRig(true)
    firstPress(rig, '？', 191)
    expect(rig.data).toEqual(['？'])
  })

  it('¥ 与 % 同样是按住 Shift 的标点，一并覆盖', () => {
    rig = makeRig(true)
    firstPress(rig, '¥', 52)
    firstPress(rig, '%', 53)
    expect(rig.data).toEqual(['¥', '%'])
  })

  // Shift 按住不放的后续几下：前一次 keyup 已把 _keyDownSeen 复位，xterm 自己
  // 就能处理。这条守的是「补漏不会变成双发」——它同时也是上游哪天修好这个洞时
  // 我们自动闭嘴的证明。
  it('Shift 保持按住时的后续几下由 xterm 自己发，补漏不插手（不双发）', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'Shift', keyCode: 16, shiftKey: true }))
    rig.ta.dispatchEvent(input(rig.ta, '？'))
    rig.ta.dispatchEvent(key('keydown', { key: '？', keyCode: 229, shiftKey: true }))
    rig.ta.dispatchEvent(key('keyup', { key: '？', keyCode: 191, shiftKey: true }))
    // 第二下：不再有新的 Shift keydown，_keyDownSeen 已被上面的 keyup 复位
    rig.ta.dispatchEvent(input(rig.ta, '？'))
    rig.ta.dispatchEvent(key('keydown', { key: '？', keyCode: 229, shiftKey: true }))
    rig.ta.dispatchEvent(key('keyup', { key: '？', keyCode: 191, shiftKey: true }))
    expect(rig.data).toEqual(['？', '？'])
  })
})

async function enterAlternateBuffer(r: Rig): Promise<void> {
  await new Promise<void>((resolve) => {
    r.term.write('\x1b[?1049h', () => resolve())
  })
  expect(r.term.buffer.active.type).toBe('alternate')
}

function dispatchEnter(
  r: Rig,
  modifiers: { shiftKey?: boolean; altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean } = {},
): KeyboardEvent {
  const ev = key('keydown', { key: 'Enter', keyCode: 13, ...modifiers })
  vi.spyOn(ev, 'stopPropagation')
  r.ta.dispatchEvent(ev)
  return ev
}

describe('B302：alt-screen 的 Shift+Enter', () => {
  it('交替屏 Shift+Enter 发 CSI u，不再让 xterm 发 CR', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    const ev = dispatchEnter(rig, { shiftKey: true })

    expect(input).toHaveBeenCalledTimes(1)
    expect(input).toHaveBeenCalledWith('\x1b[13;2u')
    expect(rig.data).toEqual(['\x1b[13;2u'])
    expect(rig.data).not.toContain('\r')
    expect(ev.defaultPrevented).toBe(true)
    expect(ev.stopPropagation).toHaveBeenCalledTimes(1)
  })

  it('主屏 Shift+Enter 不走补发，仍由 xterm 产生 CR', () => {
    rig = makeRig(true)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig, { shiftKey: true })

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\r'])
  })

  it('交替屏裸 Enter 仍走 xterm 的 CR', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig)

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\r'])
  })

  it('交替屏 Alt+Enter、Ctrl+Enter 与带额外 Meta 的组合都不走补发', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig, { altKey: true })
    dispatchEnter(rig, { ctrlKey: true })
    dispatchEnter(rig, { shiftKey: true, metaKey: true })

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\x1b\r', '\r', '\r'])
  })
})

// 没坏的路径必须原样不动。这一组是补漏的「不许越界」边界。
describe('原有输入路径不受影响', () => {
  it('普通字母仍由 xterm 的 keydown 直接发出，且只发一次', () => {
    rig = makeRig(true)
    // 真实轨迹里普通字母根本不产生 keypress/input：_keyDown 处理完就
    // preventDefault 了，所以这里也只派 keydown。
    rig.ta.dispatchEvent(key('keydown', { key: 'g', keyCode: 71 }))
    expect(rig.data).toEqual(['g'])
  })

  it('大写字母走 xterm 的 CapsLock 旁路（keypress 发），补漏不得再补一次', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'G', keyCode: 71, shiftKey: true }))
    rig.ta.dispatchEvent(key('keypress', { key: 'G', keyCode: 71, charCode: 71, shiftKey: true }))
    rig.ta.dispatchEvent(input(rig.ta, 'G'))
    expect(rig.data).toEqual(['G'])
  })

  it('Enter 名字虽长于一个字符，但不是注入文本，不能被拦', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'Enter', keyCode: 13 }))
    expect(rig.data).toEqual(['\r'])
  })

  it('合成中的 input（输入法拼词）不归补漏管', () => {
    rig = makeRig(true)
    rig.ta.value += '你好'
    rig.ta.dispatchEvent(
      new InputEvent('input', {
        data: '你好',
        inputType: 'insertCompositionText',
        bubbles: true,
        composed: true,
      }),
    )
    expect(rig.data).toEqual([])
  })

  it('粘贴走 xterm 自己的通道，补漏不插手', () => {
    rig = makeRig(true)
    rig.ta.value += 'pasted'
    rig.ta.dispatchEvent(
      new InputEvent('input', {
        data: 'pasted',
        inputType: 'insertFromPaste',
        bubbles: true,
        composed: true,
      }),
    )
    expect(rig.data).toEqual([])
  })
})

describe('dispose 之后不再插手', () => {
  it('摘掉补漏，缺口如实回到 xterm 原样', () => {
    rig = makeRig(true)
    rig.fix!.dispose()
    rig.ta.dispatchEvent(key('keydown', { key: 'git status', keyCode: 71 }))
    rig.ta.dispatchEvent(key('keypress', { key: 'git status', keyCode: 103, charCode: 103 }))
    rig.ta.dispatchEvent(input(rig.ta, 'git status'))
    expect(rig.data).toEqual(['g'])
  })
})

describe('mac 终端键：⌘←/⌘→/⌘K', () => {
  it('⌘← 发出 0x01，⌘→ 发出 0x05，且不经普通字母路径双发', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'ArrowLeft', keyCode: 37, metaKey: true }))
    rig.ta.dispatchEvent(key('keydown', { key: 'ArrowRight', keyCode: 39, metaKey: true }))
    expect(rig.data).toEqual(['\x01', '\x05'])
  })

  it('⌘K 调用 clear 且 onData 没有任何字节', () => {
    rig = makeRig(true)
    const clear = vi.spyOn(rig.term, 'clear')
    const ev = key('keydown', { key: 'k', keyCode: 75, metaKey: true })
    rig.ta.dispatchEvent(ev)
    expect(clear).toHaveBeenCalledTimes(1)
    expect(rig.data).toEqual([])
    expect(ev.defaultPrevented).toBe(true)
  })

  it('Ctrl+K 不走清屏（readline 删到行尾仍归 xterm）', () => {
    rig = makeRig(true)
    const clear = vi.spyOn(rig.term, 'clear')
    rig.ta.dispatchEvent(key('keydown', { key: 'k', keyCode: 75, ctrlKey: true }))
    expect(clear).not.toHaveBeenCalled()
  })
})

describe('Option 当 Meta：WKWebView 的 key 是符号、keyCode 经常是 0', () => {
  it('Option+B / Option+F 发出 ESC+b / ESC+f，即使 key 是 ∫/ƒ 且 keyCode=0', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: '∫', keyCode: 0, altKey: true, code: 'KeyB' }))
    rig.ta.dispatchEvent(key('keydown', { key: 'ƒ', keyCode: 0, altKey: true, code: 'KeyF' }))
    expect(rig.data).toEqual(['\x1bb', '\x1bf'])
  })

  it('随后的 insertText（∫）不得再补发——否则 zsh 收到 ESC+b 又吃一个符号', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: '∫', keyCode: 0, altKey: true, code: 'KeyB' }))
    rig.ta.dispatchEvent(input(rig.ta, '∫'))
    expect(rig.data).toEqual(['\x1bb'])
  })

  // xterm `_inputEvent` 准入是 `!composed || !_keyDownSeen`。WKWebView 的
  // Option 符号经常 composed=false，xterm 会自己再发一遍 ∫；只拦我们的补发不够。
  it('insertText composed=false 时 xterm 自己发的 ∫ 也必须吞掉', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: '∫', keyCode: 0, altKey: true, code: 'KeyB' }))
    rig.ta.dispatchEvent(input(rig.ta, '∫', false))
    expect(rig.data).toEqual(['\x1bb'])
  })

  // 可打印键的标准三件套：keydown / keypress / input。macOptionIsMeta 下
  // keypress 若没带 altKey，xterm 会把 charCode 当成普通字发出去。
  it('随后的 keypress（charCode=∫、altKey=false）不得再出符号', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: '∫', keyCode: 0, altKey: true, code: 'KeyB' }))
    rig.ta.dispatchEvent(key('keypress', { key: '∫', keyCode: 8747, charCode: 8747, code: 'KeyB' }))
    expect(rig.data).toEqual(['\x1bb'])
  })

  // Option 先按下时 WebKit 可能先丢 insertText，字母 keydown 还没到。
  // 补发路径会把 ∫ 喂进去，然后 keydown 再发 ESC+b——真机就是「跳词了但出现 ∫」。
  it('Option 按下后、字母 keydown 前的 insertText 不得补发', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'Alt', keyCode: 18, altKey: true, code: 'AltLeft' }))
    rig.ta.dispatchEvent(input(rig.ta, '∫'))
    rig.ta.dispatchEvent(key('keydown', { key: '∫', keyCode: 0, altKey: true, code: 'KeyB' }))
    expect(rig.data).toEqual(['\x1bb'])
  })
})
