// terminalDebug —— 终端输入与焦点的取证开关。
//
// 职责：为终端输入相关的故障提供读数，而不是替它们猜一个修法：
//   ② 用 ESC 取消 TUI 里正在跑的命令后，偶现无法再输入（**根因仍未定位**）
//   ③ espanso 之类的文本展开工具在桌面端终端里少字符（根因已定位并已修，
//      见 terminalInput.ts；这里留下的 logTermFix 是那个修法的现场判据）
//
// 边界：
//   - **默认全关**，靠 localStorage 显式打开。终端输入是每次按键一条，
//     常开会把控制台刷成不可用，也会把用户敲的东西（可能含密码）留在日志里
//   - 只读不写：不改任何终端行为，摘掉这个模块功能应当完全不变
//   - 不做聚合、不做上报：它是给人现场看的，不是给采集系统看的
//
// 打开方式（浏览器控制台或桌面端 devtools）：
//   localStorage.setItem('handoff.debug.terminal', '1')  // 然后重开那个终端 tab
//   localStorage.removeItem('handoff.debug.terminal')    // 关掉
//
// 怎么用这些读数：
//   ② 复现失焦后看最后一条 focus/blur —— activeElement 是谁抢走了焦点；
//      若根本没有 blur 记录，说明焦点还在 xterm 上，问题在 TUI 的模式残留
//      （鼠标追踪 / bracketed paste 没有配对关闭），而不在前端焦点管理
//   ③ 触发一次展开（如 `:ps`），看 [term:fix] 有没有「拦下注入键事件」+「补发」
//      各一条、且 [term:input] 的原文是完整的展开结果。只有首字符说明补漏没生效；
//      出现重复字符说明补漏与 xterm 抢着发，判据串了

// DEBUG_KEY 是开关所在的 localStorage 键。
export const TERMINAL_DEBUG_KEY = 'handoff.debug.terminal'

// terminalDebugEnabled 读开关。
//
// 返回：true 表示本次会话要打终端取证日志。
//
// 注意：每次调用都现读 localStorage 而不是模块加载时缓存一次——开关的用途
// 就是「出问题时当场打开」，缓存会逼用户刷新页面，而刷新往往就把现场弄没了。
// 隐私模式下 localStorage 可能直接抛，此时一律当作关闭（同 treePrefs 的处置）。
export function terminalDebugEnabled(): boolean {
  try {
    return localStorage.getItem(TERMINAL_DEBUG_KEY) === '1'
  } catch {
    return false
  }
}

// describeElement 把一个 DOM 元素描述成一行可读的短串，用于「焦点被谁抢走了」。
//
// 参数：el 可为 null（document.activeElement 在极少数时刻确实是 null）。
// 返回：形如 `textarea.xterm-helper-textarea` 的短串；null 返回 `(null)`。
export function describeElement(el: Element | null): string {
  if (el === null) return '(null)'
  const tag = el.tagName.toLowerCase()
  // className 在 SVG 元素上是 SVGAnimatedString 而不是 string，直接 slice 会炸
  const cls = typeof el.className === 'string' ? el.className.trim().slice(0, 60) : ''
  return cls === '' ? tag : `${tag}.${cls.split(/\s+/).join('.')}`
}

// logTermInput 记一次终端输入（③ 的主要读数）。
//
// 参数：
//   - label: 终端标识，用于在多个 tab 同时开着时分辨是哪一个
//   - data: xterm 交出来的这一批输入原文
//   - wsStatus: 发生输入时数据通道的状态。**不是 'open' 时这批输入很可能丢了**
//     —— connectPty 的 send 走 `ws?.send()`，WS 处于 CONNECTING 时会抛
//     InvalidStateError，处于重连间隙时 ws 是 null，两种情形都不会有任何提示
//
// 注意：原文经 JSON.stringify 输出，好让不可见字符（ESC、退格、回车）看得见——
// espanso 的展开动作正是「先退格删掉触发词再打替换文本」，看不见退格就判不了案。
export function logTermInput(label: string, data: string, wsStatus: string): void {
  if (!terminalDebugEnabled()) return
  if (wsStatus !== 'open') {
    console.warn('[term:input] 输入发生在数据通道未就绪时，很可能已丢失', {
      终端: label,
      状态: wsStatus,
      字符数: data.length,
      原文: JSON.stringify(data),
    })
    return
  }
  console.debug('[term:input]', { 终端: label, 字符数: data.length, 原文: JSON.stringify(data) })
}

// logTermFocus 记一次焦点变化（② 的主要读数）。
//
// 参数：
//   - label: 终端标识
//   - kind: 'focus' = xterm 拿到焦点，'blur' = 它失去焦点
//   - active: 事件发生**之后**的 document.activeElement 描述（用 describeElement 取）
//
// 注意：blur 的那一条才是关键——它说明焦点去了哪儿。如果复现失焦时压根没有
// blur 记录，那焦点根本没离开 xterm，问题在别处（见文件头）。
export function logTermFocus(label: string, kind: 'focus' | 'blur', active: string): void {
  if (!terminalDebugEnabled()) return
  console.debug(`[term:${kind}]`, { 终端: label, 当前焦点: active })
}

// logTermResize 记一次尺寸上报。
//
// 参数：label 是终端标识；cols/rows 是本次上报的尺寸；reason 说明是谁触发的
//（'attach' = 建连时重申，'observer' = 容器尺寸变化）。
//
// 为什么这条也留着：尺寸不同步曾经是「TUI 乱码、拖一下窗口就好」的根因
//（恢复已有会话的路径从不上报尺寸）。修好之后留一条读数，下次再出现同类
// 现象时能一眼确认尺寸到底发出去没有，不必再从头读一遍挂载次序。
export function logTermResize(label: string, cols: number, rows: number, reason: 'attach' | 'observer'): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:resize]', { 终端: label, cols, rows, 触发: reason })
}

// logTermFix 记一次输入补漏动作（terminalInput 的读数）。
//
// 参数：
//   - label: 终端标识
//   - kind: 本次动作。'补发' = xterm 一个字节都没发，由我们喂回去；
//     '让给 xterm' = xterm 自己处理了这个 input 事件，我们不插手（上游修好后
//     会一直是这一条，是「没有双发」的现场判据）；
//     '拦下注入键事件' = 认出了 espanso 那种「整串塞进一个键事件」的形状；
//     '⌘← 行首' / '⌘→ 行尾' / '⌘K 清屏' = 本模块转发的 mac 终端键
//   - text: 涉及的原文
//
// 注意：出现大量「补发」是正常的——WebKit 下中文标点每敲一下就是一条。
// 真正要警惕的是同一次输入既有「补发」又有「让给 xterm」，那说明判据串了，
// 用户会看到重复字符。
export function logTermFix(label: string, kind: '补发' | '让给 xterm' | '拦下注入键事件' | '⌘← 行首' | '⌘→ 行尾' | '⌘K 清屏', text: string): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:fix]', { 终端: label, 动作: kind, 原文: JSON.stringify(text) })
}

// logTermHost 记一次「拦下设备回包、不上送 PTY」。
//
// 参数：label 是终端标识；data 是被丢弃的原文。
// 成功路径也打：切 tab 重放历史时这条会成串出现，正好用来确认泄漏已经被拦住，
// 而不是「界面上看不见乱码、其实回包已经打进 shell」。
export function logTermHost(label: string, data: string): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:host]', { 终端: label, 字符数: data.length, 原文: JSON.stringify(data) })
}

// logTermWheel 记一次「把像素滚轮折成 N 格 SGR 上送」。
//
// 参数：label 是终端标识；ticks 是这一次发出的格数；data 是序列原文。
export function logTermWheel(label: string, ticks: number, data: string): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:wheel]', { 终端: label, 格数: ticks, 原文: JSON.stringify(data) })
}

// logTermWheelBypass 记一次「自定义滚轮放行、不生成报告」。
//
// 参数：label 是终端标识；reason 说明为什么放行（如 forces-selection）。
// 划词放行是成功路径，不能静默——否则「Option 拖不动字」只能猜是 handler 吃了还是 xterm 没开选区。
export function logTermWheelBypass(label: string, reason: string): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:wheel]', { 终端: label, 放行: reason })
}
