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
import { installTerminalInputFix, type TerminalInputFix, type TerminalInputHooks } from './terminalInput'

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
// hooks 直通安装的第 4 参（B369.10 粘滞 Ctrl 用例需要）。
function makeRig(withFix: boolean, hooks?: TerminalInputHooks): Rig {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const term = new Terminal({ cols: 80, rows: 24 })
  term.open(host)
  const ta = term.textarea as HTMLTextAreaElement
  const data: string[] = []
  term.onData((d) => data.push(d))
  const fix = withFix ? installTerminalInputFix(term, host, 'test', hooks) : null
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

async function exitAlternateBuffer(r: Rig): Promise<void> {
  await new Promise<void>((resolve) => {
    r.term.write('\x1b[?1049l', () => resolve())
  })
  expect(r.term.buffer.active.type).toBe('normal')
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

  it('交替屏退出后 Shift+Enter 不走补发，仍由 xterm 产生 CR', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    await exitAlternateBuffer(rig)
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

// B369.6：移动 IME 组合输入路径（合成 IME event）。
//
// 为什么这条必须在这里：移动端终端输入层验收要求「IME 组合输入路径有单元测试」，
// 而组合落定在 xterm 5.5.0 里是**异步**的——CompositionHelper._finalizeComposition
// 用 setTimeout(…,0) 从 textarea.value 取串发 onData（探针实测：不 await 拿不到）。
// 不 await 的测试会假绿（断言空数组==空数组），所以下面的用例显式等一个宏任务。
function composeAndSettle(r: Rig, text: string): Promise<void> {
  r.ta.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true, composed: true }))
  r.ta.value = text
  const end = new CompositionEvent('compositionend', { bubbles: true, composed: true })
  r.ta.dispatchEvent(end)
  return new Promise((resolve) => setTimeout(resolve, 10))
}

describe('③ 移动 IME 组合输入（B369.6）', () => {
  it('组合落定后完整上屏一次，补漏不双发', async () => {
    rig = makeRig(true)
    rig.ta.focus()
    await composeAndSettle(rig, '你好')
    expect(rig.data).toEqual(['你好'])
  })

  it('不装补漏时同一序列也恰好一次（补漏不接管 composition 通道）', async () => {
    rig = makeRig(false)
    rig.ta.focus()
    await composeAndSettle(rig, '世界')
    expect(rig.data).toEqual(['世界'])
  })
})

// —— B369.10：粘滞 Ctrl（独占槽位的转发消费者）——
// armed 期间的无修饰单字母键由本模块经 term.input 合成控制字符（Ctrl+A=行首、
// Ctrl-D=EOF…），大写按小写；非字母键与 Option/meta 组合一律不拦走 xterm 原路径
//（Option Meta 优先级不变）；consume 在合成后必被调、armed 复位后字母不再被拦。
describe('B369.10 粘滞 Ctrl：armed 期间字母键合成控制字符', () => {
  // makeArmedRig 起一个 armed=true 的靶子；consume 翻转 armed（模拟宿主 state）。
  function makeArmedRig() {
    let armed = true
    const consume = vi.fn(() => {
      armed = false
    })
    const r = makeRig(true, { stickyCtrl: { armed: () => armed, consume } })
    return { r, consume }
  }

  it('armed + 字母 a → \\x01、consume 被调、不双发', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'a', keyCode: 65 }))
    expect(r.data).toEqual(['\x01'])
    expect(consume).toHaveBeenCalledTimes(1)
  })

  it('armed + 大写 A → 同 \\x01（大写按小写合成）', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'A', keyCode: 65 }))
    expect(r.data).toEqual(['\x01'])
    expect(consume).toHaveBeenCalledTimes(1)
  })

  it('armed + Enter → 不拦（xterm 原路径 CR），consume 不被调', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'Enter', keyCode: 13 }))
    expect(r.data).toEqual(['\r'])
    expect(consume).not.toHaveBeenCalled()
  })

  it('armed + 方向 → 不拦（xterm 原路径 CSI），consume 不被调', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'ArrowUp', keyCode: 38 }))
    expect(r.data).toEqual(['\x1b[A'])
    expect(consume).not.toHaveBeenCalled()
  })

  it('未 armed 字母 → 不拦（xterm 原路径）', () => {
    let armed = false
    const consume = vi.fn(() => {
      armed = false
    })
    const r = makeRig(true, { stickyCtrl: { armed: () => armed, consume } })
    r.ta.dispatchEvent(key('keydown', { key: 'a', keyCode: 65 }))
    expect(r.data).toEqual(['a'])
    expect(consume).not.toHaveBeenCalled()
  })

  it('armed + Option 组合 → Option Meta 优先（发 ESC+b），粘滞不接管', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'b', keyCode: 66, altKey: true, code: 'KeyB' }))
    expect(r.data).toEqual(['\x1bb'])
    expect(consume).not.toHaveBeenCalled()
  })

  it('armed + meta 组合 → 不拦（⌘← 行首语义不变）', () => {
    const { r, consume } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'a', keyCode: 65, metaKey: true }))
    expect(r.data).not.toContain('\x01')
    expect(consume).not.toHaveBeenCalled()
  })

  it('consume 之后（armed 复位）字母不再被拦', () => {
    const { r } = makeArmedRig()
    r.ta.dispatchEvent(key('keydown', { key: 'a', keyCode: 65 }))
    expect(r.data).toEqual(['\x01'])
    r.data.length = 0
    r.ta.dispatchEvent(key('keydown', { key: 'b', keyCode: 66 }))
    expect(r.data).toEqual(['b'])
  })

  it('不传 hooks 的既有安装路径不受影响（第 4 参缺席 = 纯补漏）', () => {
    const r = makeRig(true)
    r.ta.dispatchEvent(key('keydown', { key: 'a', keyCode: 65 }))
    expect(r.data).toEqual(['a'])
  })
})

// ③ Android IME 直接上屏：每个字母双发 + 残渣整行重打
// （trace: 2026-10-07 真机取证，GBoard + Android 16 WebView，CDP 事件流——
//   keydown(229 "Unidentified") → beforeinput(insertText "h") → input → keyup(229)；
//   多字符滑行提交 "from " 单事件整串 insertText。字段逐值抄自那份轨迹。）
describe('③ Android IME 229 占位键 + insertText 直接上屏', () => {
  // androidImeReplay 按 Chromium 真实次序派发：beforeinput 若被 preventDefault，
  // 默认插入不发生（textarea 不动、input 事件不派）——这正是取消语义的真实现场。
  const androidImeReplay = (r: Rig, text: string): void => {
    r.ta.dispatchEvent(key('keydown', { key: 'Unidentified', keyCode: 229 }))
    const before = new InputEvent('beforeinput', {
      data: text, inputType: 'insertText', bubbles: true, cancelable: true, composed: true,
    })
    r.ta.dispatchEvent(before)
    if (!before.defaultPrevented) {
      // 默认插入发生了（无补漏时正是这条路径留下残渣）；input() 助手内建
      // 「先改 textarea 再派事件」，派出的 input 即真实现场。
      r.ta.dispatchEvent(input(r.ta, text))
    }
    r.ta.dispatchEvent(key('keyup', { key: 'Unidentified', keyCode: 229 }))
  }

  it('不装补漏时 xterm 拒收（一个字都不发），残渣留在 textarea 里', () => {
    const r = makeRig(false)
    androidImeReplay(r, 'h')
    expect(r.data).toEqual([])
    expect(r.ta.value).toBe('h')
  })

  it('不装补漏时残渣被 CompositionHelper 水位冲刷重发——这就是双发的第二股', async () => {
    const r = makeRig(false)
    androidImeReplay(r, 'h')
    expect(r.data).toEqual([])
    // 下一个手势的 keydown(229) 触发 _handleAnyTextareaChanges：残渣 'h' 整段冲进 PTY。
    r.ta.dispatchEvent(key('keydown', { key: 'Unidentified', keyCode: 229 }))
    // 该冲刷是 setTimeout(0) 的宏任务（真机上即双发之间的那 ~36ms），等它落地。
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(r.data).toEqual(['h'])
  })

  it('装上补漏后每个字符恰好一次，textarea 零残渣', () => {
    const r = makeRig(true)
    androidImeReplay(r, 'h')
    androidImeReplay(r, 'i')
    expect(r.data).toEqual(['h', 'i'])
    expect(r.ta.value).toBe('')
  })

  it('多字符滑行提交（"from "）同样恰好一次整串', () => {
    const r = makeRig(true)
    androidImeReplay(r, 'from ')
    expect(r.data).toEqual(['from '])
    expect(r.ta.value).toBe('')
  })

  it('Android 物理键（Backspace keyCode=8）不命中形状，照走 xterm 原通道', () => {
    const r = makeRig(true)
    r.ta.dispatchEvent(key('keydown', { key: 'Backspace', keyCode: 8 }))
    expect(r.data).toEqual(['\x7f'])
  })
})
