// terminalInput —— 补上 xterm 输入路径在 WebKit 上的两个漏字符缺口。
//
// 职责：在**不 fork xterm** 的前提下，把两类被 xterm 静默丢弃的可打印输入
//       重新喂回终端；并在同一槽位转发 ⌘←/⌘→/⌘K 和 Option 当 Meta
//      （WKWebView 的 Option+B 给的是 ∫ / keyCode=0，xterm 认不到）。
//       只补漏，不接管：xterm 自己发得出去的字符一律不碰。
// 边界：
//   - 可打印文本怎么进 PTY。控制键（方向键/Enter/Ctrl-C）、
//     输入法合成（compositionstart/end）、粘贴，全部原样留给 xterm；唯一例外是
//     alternate buffer 中无额外修饰的 Shift+Enter，转发 CSI u，避免主屏裸 LF/CR
//     被行编辑器当成 accept-line；这不是完整 Kitty keyboard protocol
//   - ⌘K 是仿真器本地清屏，禁止因此往 PTY 写任何 CSI/C0
//   - 不直接往 WS 写字节。补发一律走 `term.input()`，让补发的字符与用户
//     手敲的字符走同一条 onData —— 上层的取证日志、尺寸逻辑因此不必知道本模块存在
//   - 独占 `attachCustomKeyEventHandler`（xterm 只有一个槽位）。将来若有别处
//     要用这个钩子，必须改成本模块转发，而不是各挂各的。粘滞 Ctrl（B369.10
//     岔口 3）就是这条处方的第一个落地：hooks.stickyCtrl 经本槽位转发，armed
//     期间的无修饰字母键由本模块合成控制字符，归宿主持有 armed 状态
//
// ── 为什么需要它（两条都有 WKWebView 实测轨迹佐证，不是推断）──
//
// ① espanso 之类的文本展开工具，展开后只进去首字符（`:gs` → `g`）
//    macOS 上 espanso 用 CGEventKeyboardSetUnicodeString 把**整串**文本塞进
//    一个键事件，WebKit 如实转成 `keydown/keypress`，其 `key` 就是整串：
//        keydown  key="git status" keyCode=71
//        keypress key="git status" charCode=103
//        input    inputType=insertText data="git status"
//    xterm 的两道关卡先后失守：
//      - `evaluateKeyboardEvent` 里那条 `ev.key.length === 1` 判据不认多字符，
//        于是 `_keyDown` 不处理、也不 preventDefault，放行到 keypress；
//      - `_keyPress` 用 `String.fromCharCode(ev.charCode)` 把整串**压成首字符**
//        发出去（就是那个 `g`），并置 `_keyPressHandled = true`；
//      - 随后带着完整 `data="git status"` 的 input 事件，被 `_inputEvent`
//        以「keypress 已处理」为由丢弃。
//    修法：认出「多字符键事件」这个形状，让 `_keyPress` 不要处理它（返回 false
//    走 xterm 官方的 customKeyEventHandler 通道），改由 input 事件补发完整文本。
//
// ② 中文输入法下的标点（？¥%）要敲好几遍才出来一个
//    WebKit 在输入法直接上屏（非合成）时，把 input 事件排在 keydown **之前**：
//        keydown  key="Shift"  keyCode=16      ← _keyDownSeen = true
//        input    inputType=insertText data="？" composed=true
//        keydown  key="？"     keyCode=229     ← 才轮到这一下
//    而 `_inputEvent` 的准入条件是 `(!ev.composed || !this._keyDownSeen)`：
//    composed 恒真、`_keyDownSeen` 又被 Shift 那一下置真，条件为假 → 字符被丢。
//    紧接着的 keyCode 229 走 CompositionHelper 的 `_handleAnyTextareaChanges`，
//    它比对 textarea 前后值——可字符**早就已经插进去了**，前后值相同，也发不出。
//    于是：按下 Shift 后的第一个标点必丢；Shift 按住不放时，后面那些因为前一次
//    keyup 已把 `_keyDownSeen` 复位而侥幸通过——这就是「要敲好几遍」的由来。
//    修法：不去猜 xterm 的准入条件（那会随版本漂），而是**事后看它发没发**：
//    在 xterm 处理完同一个 input 事件后，若一个字节都没发出去，就由我们补发。
//
// ③ Android IME 直接上屏的字母每个字符双发，且残渣在组合提交时整行重打
//    （2026-10-07 真机取证，GBoard + Android 16 WebView，CDP 事件流与调用栈）：
//        keydown  key="Unidentified" keyCode=229   ← IME 桥的占位键
//        input    inputType=insertText data="h" composed=true
//    xterm 的 `_inputEvent` 准入条件是 `(!e.composed || !this._keyDownSeen)`：
//    composed 恒真、`_keyDownSeen` 又被 229 那一下置真 → 条件为假，**既不发送
//    也不 preventDefault** → 字符留在 textarea 里。B369.6 的补发把字符送进
//    PTY（PTY 因此收到一次 ✓），但 textarea 里的残渣没人清；而 `_keyDown`
//    首行把 229 交给 CompositionHelper，其对每个 229 键调用
//    `_handleAnyTextareaChanges()`，把 textarea 相对水位的增量整段
//    `triggerDataEvent` 冲进 PTY——同一个字符于是到第二次，残渣攒多了还会在
//    联想/滑行组合提交时把整段积累一次性重打（真机实录 49 字节整行重发）。
//    修法：认出「229 占位键 + 直接上屏 insertText」这个 Android 形状，在
//    **beforeinput**（可取消、先于默认插入）拦下：preventDefault 取消插入
//    （textarea 零残渣）+ stopPropagation 挡住 xterm 的拒收路径 + 补发一次。
//    判据钉在「本手势见过 229」上——macOS espanso（真键值多字符）与中文标点
//    （keyCode 16 先行）都不带 229，原路径原样保留；Android 物理键（Backspace
//    keyCode=8 实测）也不命中，照走 xterm 原通道。
//
// 判据「xterm 发没发」怎么取：`term.onData` 是 xterm 所有输入出口的汇合点，
// 拿一个自增计数器在「xterm 看这个事件之前」和「之后」各读一次即可。
// 这么做而不是复刻 `_inputEvent` 的条件，是为了让本模块对 xterm 升级免疫——
// 哪天上游把这两个洞补上，计数器会显示「它自己发了」，我们就自动闭嘴，不会变成双发。
//
// 承重次序：capture 快照必须挂在 **host**（textarea 的祖先）上，事后判读挂在
// **textarea** 上。同元素同阶段的监听按注册先后跑，xterm 在 `term.open()` 时
// 就把自己的 capture 监听注册在 textarea 上了，我们只能后到——所以「之前」那一读
// 必须借祖先节点的 capture 阶段才抢得到。
import type { Terminal } from '@xterm/xterm'
import { logTermFix } from './terminalDebug'

// TerminalInputFix 是安装后的句柄，卸载时必须调用 dispose。
export interface TerminalInputFix {
  dispose: () => void
}

// TerminalInputHooks 是独占槽位的转发接口（B369.10 岔口 3）：别处要用 xterm 的
// 键事件钩子，一律在本模块的 custom handler 里转发，不开第二路监听。
// stickyCtrl：armed() 读宿主的粘滞态；consume() 在合成完成后解除。状态归宿主
//（TerminalTab 的每终端 state），本模块只读/只解除，不拥有它。
export interface TerminalInputHooks {
  stickyCtrl?: {
    armed(): boolean
    consume(): void
  }
}

// NAMED_KEYPRESS_KEYS 是少数「名字比一个字符长、但确实会触发 keypress」的按键。
// 它们不是注入文本，绝不能当多字符串处理。
const NAMED_KEYPRESS_KEYS = new Set(['Enter', 'Tab', 'Escape'])

// isInjectedText 判断一个 keypress 是不是「整串文本被塞进一个键事件」。
//
// 参数：ev 为 keypress 事件。
// 返回：true 表示 `ev.key` 是一整串注入文本，不能按单字符处理。
//
// 判据除了「key 长于一个字符」，还要求 charCode 与 key 的首字符对得上。
// 这一条是**自校验**：WebKit 给注入事件的 charCode 恰好取自被注入文本的首字符
//（实测 "git status"→103='g'、"ps -ef | grep  "→112='p'，两例互证）。对不上
// 就说明这不是我们认识的那个形状，宁可不管，也不要误伤真按键。
function isInjectedText(ev: KeyboardEvent): boolean {
  if (ev.key.length <= 1) return false
  if (NAMED_KEYPRESS_KEYS.has(ev.key)) return false
  const charCode = ev.charCode || ev.which || ev.keyCode
  return charCode > 0 && ev.key.codePointAt(0) === charCode
}

// optionMetaLetter 把 Option+字母换成 readline 认识的 Meta 字母。
//
// WKWebView 上 Option+B 的 key 是「∫」、keyCode 经常是 0，xterm 的
// macOptionIsMeta 靠 keyCode 65–90 判，走不到 ESC+b，符号就当普通字打进去。
// 物理键 ev.code（KeyB）在这条路径上是稳的。
function optionMetaLetter(ev: KeyboardEvent): string | null {
  if (!ev.altKey || ev.metaKey || ev.ctrlKey) return null
  if (!ev.code.startsWith('Key') || ev.code.length !== 4) return null
  const letter = ev.code.slice(3)
  if (letter < 'A' || letter > 'Z') return null
  return ev.shiftKey ? letter : letter.toLowerCase()
}

// installTerminalInputFix 给一个已经 open 过的终端装上补漏逻辑。
//
// 参数：
//   - term: 已调用过 `term.open(host)` 的终端；未 open 时 `term.textarea`
//     还不存在，本函数直接返回一个空句柄（不抛，调用方不必分情况处理）
//   - host: 传给 `term.open()` 的那个容器元素，必须是 textarea 的祖先
//   - label: 取证日志里用来分辨是哪个终端 tab
//
// 返回：句柄；`dispose()` 摘掉所有监听。**必须**在终端卸载时调用，否则
// 监听会连同闭包一起把已 dispose 的终端留在内存里。
//
// 注意：本函数会占用 `term.attachCustomKeyEventHandler`。
export function installTerminalInputFix(
  term: Terminal,
  host: HTMLElement,
  label: string,
  hooks?: TerminalInputHooks,
): TerminalInputFix {
  const ta = term.textarea
  if (!ta) {
    console.warn('终端输入补漏未安装：textarea 尚不存在，说明 term.open() 还没调用', label)
    return { dispose: () => {} }
  }

  // dataSeq 是「xterm 到目前为止发出过多少批数据」。只看变化，不看数值。
  let dataSeq = 0
  const dataSub = term.onData(() => {
    dataSeq++
  })

  // keypressEmitted 记「本次按键链里 keypress 已经把字符发出去了」。
  // 它是 input 事件的免打扰位：大写字母（xterm 的 CapsLock HACK 把 A-Z 交给
  // keypress 处理）走的正是这条路，此时 input 事件里那份 data 是重复的，补发即双字。
  let keypressEmitted = false
  // optionMetaConsumed：刚用物理键发过 ESC+字母。WKWebView 仍可能再丢
  // keypress / insertText（∫/ƒ），两条路径都必须闭嘴。
  let optionMetaConsumed = false
  // optionHeld：Option 已经按下。字母 keydown 还没到时 WebKit 可能先丢
  // insertText；此时 optionMetaConsumed 还是 false，补发会先打出 ∫ 再跳词。
  let optionHeld = false
  // androidImeGesture：本手势见过 keyCode=229 的占位键（Android IME 桥的
  // 指纹）。形状判据见头部 ③——只有它命中 beforeinput 拦截，其余平台零接触。
  let androidImeGesture = false
  // seqBeforeKeypress / seqBeforeInput 是 xterm 看到该事件之前的计数快照。
  let seqBeforeKeypress = 0
  let seqBeforeInput = 0

  const isAltKey = (ev: KeyboardEvent): boolean =>
    ev.key === 'Alt' || ev.code === 'AltLeft' || ev.code === 'AltRight'

  // 一次新的按键链开始：清掉上一链的残留状态。
  // 注意 ② 那条路径里 input 排在 keydown 之前，此处的复位对它是无害的空操作。
  const onKeyDownCapture = (ev: KeyboardEvent): void => {
    keypressEmitted = false
    optionMetaConsumed = false
    androidImeGesture = ev.keyCode === 229
    if (ev.altKey || isAltKey(ev)) optionHeld = true
  }
  const onKeyUpCapture = (ev: KeyboardEvent): void => {
    if (isAltKey(ev)) optionHeld = false
  }
  const onKeyPressCapture = (): void => {
    seqBeforeKeypress = dataSeq
  }
  // xterm 的 input 监听挂在 textarea capture，比 host capture 晚。
  // `_inputEvent` 在 `!composed` 时会自己 triggerDataEvent——只在事后不补发
  // 拦不住已经发出去的 ∫，必须在下传到 textarea 之前停掉。
  const dropOptionGeneratedText = (ie: InputEvent): boolean => {
    if (ie.inputType !== 'insertText' || ie.isComposing || !ie.data) return false
    return optionMetaConsumed || optionHeld
  }
  const onInputCapture = (ev: Event): void => {
    seqBeforeInput = dataSeq
    const ie = ev as InputEvent
    const text = ie.data
    if (!text || !dropOptionGeneratedText(ie)) return
    ev.stopPropagation()
    ev.preventDefault()
    logTermFix(label, 'Option Meta', text)
  }

  // xterm 处理完 keypress 之后：它发了东西没有？
  const onKeyPressAfter = (): void => {
    keypressEmitted = dataSeq !== seqBeforeKeypress
  }

  // Android IME 形状拦截（③）：beforeinput 可取消、先于默认插入。在此取消
  // textarea 插入（零残渣，CompositionHelper 的水位冲刷从此无东西可冲），
  // stopPropagation 短路 xterm 的拒收路径，补发恰好一次。只认「本手势见过
  // 229 + 直接上屏 insertText」；其余平台/形状零接触，原路径原样。
  const onBeforeInputCapture = (ev: Event): void => {
    const ie = ev as InputEvent
    if (!androidImeGesture) return
    if (ie.inputType !== 'insertText' || ie.isComposing || !ie.data) return
    ev.preventDefault()
    ev.stopPropagation()
    logTermFix(label, 'Android IME', ie.data)
    term.input(ie.data)
  }

  // xterm 处理完 input 之后：该补发吗？
  const onInputAfter = (ev: Event): void => {
    const ie = ev as InputEvent
    // 只管「直接上屏的文本」。合成中的中间态（insertCompositionText）、
    // 合成落定（insertFromComposition）、粘贴（insertFromPaste）、删除，
    // 都有 xterm 自己的通道，插手只会重复。
    if (ie.inputType !== 'insertText' || ie.isComposing || !ie.data) return
    if (optionMetaConsumed || optionHeld) {
      optionMetaConsumed = false
      return
    }
    if (keypressEmitted) {
      // keypress 已经把这段文本发过了（大写字母那条路），这里的 data 是同一份。
      keypressEmitted = false
      return
    }
    if (dataSeq !== seqBeforeInput) {
      // xterm 自己处理了这个 input 事件。上游哪天把缺口补上也会走到这里，
      // 我们就此闭嘴——这是本模块不会退化成「双发」的保证。
      logTermFix(label, '让给 xterm', ie.data)
      return
    }
    logTermFix(label, '补发', ie.data)
    term.input(ie.data)
  }

  // 多字符键事件：拦在 xterm 的 `_keyPress` 之前，别让它压成首字符。
  // 走官方 customKeyEventHandler 而不是 DOM 的 stopPropagation，是因为返回 false
  // 的位置恰在 `_keyPressHandled = true` **之前**——xterm 因此既不发首字符，也不会
  // 给随后的 input 事件留下「已处理」的标记，补发路径才走得通。
  //
  // disposed 这道闸不能省：`attachCustomKeyEventHandler` 只有一个槽位、也没有
  // 「摘除」接口，dispose 时若不自己失效，拦截会在补漏已经卸掉之后继续生效——
  // keypress 被拦、又没人补发，字符就凭空消失了。不用「dispose 时重新 attach
  // 一个恒真处理器」来复位，是因为那会连带覆盖掉别人后来挂上去的处理器。
  // TUI 约定的单键 CSI u：13 是 Enter，2 是 Shift；只在 alternate buffer 发送。
  // 这是一个单键兼容补漏，不是完整 Kitty keyboard protocol。
  const TUI_SHIFT_ENTER = '\x1b[13;2u'

  let disposed = false
  term.attachCustomKeyEventHandler((ev) => {
    if (disposed) return true
    if (ev.type === 'keypress') {
      // macOptionIsMeta 只在 keypress.altKey 时跳过；WKWebView 翻译成 ∫ 后
      // 可能不再带 altKey，xterm 会按 charCode 再发一个符号。
      if (optionMetaConsumed || optionHeld || optionMetaLetter(ev) !== null) {
        ev.preventDefault()
        ev.stopPropagation()
        return false
      }
    }
    if (ev.type === 'keydown') {
      // 粘滞 Ctrl 合成（B369.10 岔口 3，挂在本模块独占槽位的转发面上）：armed
      // 期间的无修饰单字母键按 Ctrl+字母合成控制字符（Ctrl-A=行首、Ctrl-D=EOF、
      // Ctrl-R=反搜…），大写按小写。armed 期间非字母键（Enter/方向/物理组合）
      // 一律不拦走 xterm 原路径——「不误伤真按键」的保守方向与注入判据同款。
      // 合成只走 term.input（合流纪律），完成后 consume() 解除粘滞态。
      const sticky = hooks?.stickyCtrl
      if (
        sticky?.armed() &&
        ev.key.length === 1 &&
        /[a-z]/i.test(ev.key) &&
        !ev.altKey &&
        !ev.metaKey
      ) {
        ev.preventDefault()
        ev.stopPropagation()
        const code = ev.key.toLowerCase().codePointAt(0)! - 0x60
        logTermFix(label, '粘滞 Ctrl', `\\x${code.toString(16).padStart(2, '0')}`)
        term.input(String.fromCharCode(code))
        sticky.consume()
        return false
      }
      if (
        ev.key === 'Enter' &&
        ev.shiftKey &&
        !ev.altKey &&
        !ev.ctrlKey &&
        !ev.metaKey &&
        term.buffer.active.type === 'alternate'
      ) {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, 'Shift+Enter 换行', TUI_SHIFT_ENTER)
        term.input(TUI_SHIFT_ENTER)
        return false
      }
      const metaLetter = optionMetaLetter(ev)
      if (metaLetter !== null) {
        ev.preventDefault()
        ev.stopPropagation()
        optionMetaConsumed = true
        logTermFix(label, 'Option Meta', metaLetter)
        term.input(`\x1b${metaLetter}`)
        return false
      }
      if (ev.metaKey && !ev.ctrlKey && !ev.altKey && ev.key === 'ArrowLeft') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘← 行首', '\x01')
        term.input('\x01')
        return false
      }
      if (ev.metaKey && !ev.ctrlKey && !ev.altKey && ev.key === 'ArrowRight') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘→ 行尾', '\x05')
        term.input('\x05')
        return false
      }
      // 不上送 \x0c：TUI 会当成输入（B267 方向键同类）。term.clear() 把当前行
      // 留作第 0 行并丢掉 scrollback（xterm Terminal.clear）。
      if (ev.metaKey && !ev.ctrlKey && !ev.shiftKey && ev.key.toLowerCase() === 'k') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘K 清屏', '')
        term.clear()
        return false
      }
    }
    if (ev.type !== 'keypress') return true
    if (!isInjectedText(ev)) return true
    logTermFix(label, '拦下注入键事件', ev.key)
    return false
  })

  host.addEventListener('keydown', onKeyDownCapture, true)
  host.addEventListener('keyup', onKeyUpCapture, true)
  host.addEventListener('keypress', onKeyPressCapture, true)
  host.addEventListener('input', onInputCapture, true)
  host.addEventListener('beforeinput', onBeforeInputCapture, true)
  ta.addEventListener('keypress', onKeyPressAfter, true)
  ta.addEventListener('input', onInputAfter, true)

  return {
    dispose: () => {
      disposed = true
      host.removeEventListener('keydown', onKeyDownCapture, true)
      host.removeEventListener('keyup', onKeyUpCapture, true)
      host.removeEventListener('keypress', onKeyPressCapture, true)
      host.removeEventListener('input', onInputCapture, true)
      host.removeEventListener('beforeinput', onBeforeInputCapture, true)
      ta.removeEventListener('keypress', onKeyPressAfter, true)
      ta.removeEventListener('input', onInputAfter, true)
      dataSub.dispose()
    },
  }
}
