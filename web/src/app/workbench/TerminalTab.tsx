// TerminalTab —— 中央区的真终端（W4 PTY spec §6）。
//
// 职责：
//   - 挂 xterm，把一个服务端 PTY 会话的字节流画出来
//   - 没有会话时先建一个，并把 id 回报给 tab（onSession）
//   - 按键上送、尺寸上送、断线重连（重连逻辑在 api/pty.ts，这里只消费）
//   - shell 退出后在下方显示退出码，tab 留着等用户自己关
//   - 订阅被判死（close 1008，最常见的是 agentd 重启后旧会话已不存在）时，
//     除了报出服务端给的原因，还给一个「重开一个终端」的出口——没有它，这个
//     tab 就是死物，用户只能关掉重开
//
// 边界：
//   - **不删会话**。卸载只断 WS——切 tab、切基准目录、关页面都不该杀掉
//     跑了一晚上的 build（spec §6.2）。删会话是 × 按钮的事，在 Shell 里
//   - 不做重连退避、不认识 WS 帧格式：那都在 api/pty.ts
//   - 不判断这台机器支不支持 PTY：那是 Shell 的降级门（Task 14）。
//     这里只兜住「真发了请求才知道不支持」的那一路（501）
//
// 关于切 tab：曾经只渲染激活 tab，切走即卸载、切回重放环形缓冲，TUI 必坏。
// B270 起见过的终端在后台继续活：不卸载、不把画布标成看不见、切走不
// blur/resize。xterm 的 [I]/[O] 不上送 PTY（见 terminalHostResponse.ts）。
// 刷新仍会重放，回放里的设备回包同样不上送。
import { useEffect, useRef, useState } from 'react'
import { TerminalSquare } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { createPtySession, deletePtySession } from '../../api/client'
import { connectPty, type PtyHandle } from '../../api/pty'
import { describeElement, logTermFocus, logTermHost, logTermInput, logTermKeepalive, logTermResize, logTermWheel, logTermWheelBypass, terminalDebugEnabled } from './terminalDebug'
import { isTerminalHostResponse, takeLeadingFocusReport } from './terminalHostResponse'
import { altBufferWheelReports, mouseEncodingOf, pointerCell, wheelForcesSelection, wheelPixelDeltaY } from './terminalWheel'
import { installTerminalInputFix } from './terminalInput'
import { registerFileDropTarget, shellQuote } from '../lib/desktopFileDrop'
import type { BaseDir } from './useWorkbench'

export interface TerminalTabProps {
  base: BaseDir
  seq: number
  // sessionId 缺席 = 这个 tab 还没有会话，挂载时建一个。
  sessionId?: string
  // incompatible = 服务端会话仍活着但本版协议无法接入；不应发起连接或重连。
  incompatible?: boolean
  // rel 是终端要起的工作树子目录；空串/缺席 = 工作树根。
  rel?: string
  // envFile / initCommand 是启动项换算后的具体请求字段；组件不认识启动项名字。
  envFile?: string
  initCommand?: string
  // onSession 把新建会话的 id 交回上层写进 TabContent。必须回报：
  // 不回报的话切一次 tab 就会再建一个会话，用户每切一次多留一个 shell。
  onSession: (id: string) => void
  // active=false 时本实例仍挂着（切 tab keep-alive），只是看不见。
  // 不能放进建连 effect 的依赖：放进去切走就会拆掉 xterm，等于没 keep-alive。
  active?: boolean
}

// ptyBase 把一个基准目录翻译成建会话请求的两个字段。
//
// home 基准的 path 是字面量 '~'，**不是**服务端认识的路径（useWorkbench 里
// 早有这条纪律）。base_kind=home 时服务端用它自己的 $HOME，所以这里发空串，
// 免得将来有人把 '~' 当路径去 stat。
//
// rel 只在 workspace 基准确有语义：home 的 cwd 由服务端决定，不往上带。
// rel 为空/undefined 时返回的对象与历史形态**逐字节一致**（不加 rel 键），
// 建会话的既有断言与行为不得受影响。
function ptyBase(base: BaseDir, rel?: string): { base_kind: string; base_path: string; rel?: string } {
  const out: { base_kind: string; base_path: string; rel?: string } = {
    base_kind: base.kind,
    base_path: base.kind === 'home' ? '' : base.path,
  }
  if (rel && base.kind === 'workspace') out.rel = rel
  return out
}

// launcherFields 把启动项参数翻译成建会话请求的两个字段。
// 不带时返回空对象，保证普通终端请求与历史形态逐字节一致；对象展开不会替
// 多余属性检查，所以这里必须显式守住这个边界。
function launcherFields(envFile?: string, initCommand?: string): { env_file?: string; init_command?: string } {
  const out: { env_file?: string; init_command?: string } = {}
  if (envFile) out.env_file = envFile
  if (initCommand) out.init_command = initCommand
  return out
}

// xtermDebugSnap 是切 tab 取证用的当场只读快照：鼠标追踪、交替屏、渲染是否暂停。
function xtermDebugSnap(term: Terminal, host: HTMLElement): Record<string, unknown> {
  const rs = (term as unknown as { _core?: { _renderService?: { _isPaused?: boolean } } })._core?._renderService
  const r = host.getBoundingClientRect()
  return {
    缓冲: term.buffer.active.type,
    鼠标: term.modes.mouseTrackingMode,
    焦点报告: term.modes.sendFocusMode,
    暂停: Boolean(rs?._isPaused),
    尺寸: `${term.cols}x${term.rows}`,
    盒子: `${Math.round(r.width)}x${Math.round(r.height)}`,
    有焦点类: Boolean(term.element?.classList.contains('focus')),
  }
}

export function TerminalTab({
  base, seq, sessionId, rel, envFile, initCommand, incompatible = false, onSession, active = true,
}: TerminalTabProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const handleRef = useRef<PtyHandle | null>(null)
  const nudgeMouseRef = useRef<() => void>(() => {})
  const revealRef = useRef<() => void>(() => {})
  const activeRef = useRef(active)
  activeRef.current = active
  const cycleRef = useRef(0)
  const prevActiveRef = useRef(active)
  const lastWheelMissAt = useRef(0)
  const [error, setError] = useState<string | null>(null)
  // exit 为 undefined 表示还活着；已退出时它是退出码（对端没给退出码时是 null）
  const [exit, setExit] = useState<number | null | undefined>(undefined)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')
  // dead：这条订阅被服务端判死（close 1008），api/pty.ts 不会再重连。
  // 与 error 分开存：普通断线也会写 error，但那时还在退避重连，不该给重开入口。
  const [dead, setDead] = useState(false)
  // discarded 是用户已经放弃的那个会话 id。不清空上层的 sessionId（那是 tab 的
  // 状态，本组件不持有），而是在本地把它「划掉」：liveId 因此回到 undefined，
  // 挂载路径原样走一遍建会话 + onSession 回报，不必给上层加新的写入口。
  const [discarded, setDiscarded] = useState<string | undefined>(undefined)
  const liveId = sessionId !== undefined && sessionId !== discarded ? sessionId : undefined
  const incompatibleLive = incompatible && liveId !== undefined

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let disposed = false
    let handle: PtyHandle | null = null
    // wsStatus 是数据通道的当前状态，供输入取证判断「这批输入发出去了没有」。
    // 用闭包变量而不是 React state：effect 里读 state 拿到的是挂载那一刻的旧值。
    let wsStatus: 'connecting' | 'open' | 'closed' = 'connecting'
    // label 只用于取证日志，让多个终端 tab 同时开着时分得清是哪一个
    const label = seq > 1 ? `${base.label} (${seq})` : base.label

    // 终端底色固定为深色：xterm 的 WebGL 渲染器不支持透明背景，跟着页面主题
    // 走会在浅色主题下拿到一块透不过去的白底。终端惯例本就是深色，不折腾。
    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12,
      cursorBlink: true,
      scrollback: 5000,
      theme: { background: '#0b0b0c' },
      macOptionIsMeta: true,
      macOptionClickForcesSelection: true,
    })
    termRef.current = term
    // restoreMouseIfNeeded：TUI 在 [O] 后常关掉 1000h。keep-alive 吞掉 [O]
    // 能避免再关；已经关上的（本页重放末尾是 1000l，或上一轮已发出去）
    // 用 ±1 行逼一次 SIGWINCH，TUI 会重开追踪。zsh 在主屏，不会走进来。
    const restoreMouseIfNeeded = () => {
      if (!activeRef.current) return
      if (term.buffer.active.type !== 'alternate') return
      if (term.modes.mouseTrackingMode !== 'none') return
      const cols = term.cols
      const rows = term.rows
      if (cols < 1 || rows < 2) return
      logTermResize(label, cols, rows, 'nudge')
      handleRef.current?.resize(cols, rows - 1)
      handleRef.current?.resize(cols, rows)
    }
    nudgeMouseRef.current = restoreMouseIfNeeded
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    // WebGL 渲染器有两条失效路径，两条都不能白屏（spec §6.3）：
    //   1. 构造期不可用（远程桌面、禁用硬件加速、老显卡）——构造直接抛，
    //      catch 住即可，xterm 用内建 DOM 渲染器继续。
    //   2. 运行期上下文丢失（GPU 复位、驱动重启、浏览器驱逐 WebGL 上下文——
    //      终端 tab 开多了就够得着）。这条 try/catch **管不着**：addon 已经
    //      挂上去了，不 dispose 它就留下一个死渲染器，画面永久停住，控制台
    //      刷 `dimensions` 的 TypeError。所以必须注册 onContextLoss 主动摘除。
    try {
      const webgl = new WebglAddon()
      webgl.onContextLoss(() => {
        console.warn('WebGL 上下文丢失，已摘除 WebGL 渲染器回退到 DOM 渲染（会慢一些，但画面继续）')
        webgl.dispose()
      })
      term.loadAddon(webgl)
      // WebGL 接管后单元格度量可能变了，下一帧再量一次，避免第一屏花掉。
      requestAnimationFrame(() => {
        if (!disposed) fitIfMeasured()
      })
    } catch (err) {
      console.warn('WebGL 渲染器不可用，已回退到 DOM 渲染', err)
    }
    // measured 表示「容器已经有布局盒子，此刻量出来的尺寸作数」。
    //
    // 为什么必须挡这一下：恢复布局时 TerminalTab 会在容器还没拿到布局盒子的
    // 那一帧挂载，此时 FitAddon 量出的是 **2×1**（xterm 的下限）。把它当真实
    // 尺寸报给服务端，PTY 就真被调成 2 列 1 行——shell 按 2 列重排，正在回放的
    // 历史当场被绞碎，正在跑的 TUI 同样遭殃；等 ResizeObserver 补一次真实尺寸
    // 时已经救不回来了。用户看到的现象是「刷新之后终端里什么都没了」。
    //
    // 判据取容器而不是取 term.cols：2×1 也可能是一个真的很窄的分栏，
    // 「有没有布局盒子」才是「这次测量算不算数」的正解。
    const measured = (): boolean => {
      const r = host.getBoundingClientRect()
      return r.width >= 1 && r.height >= 1
    }
    // reassertPending = 「这条连接的尺寸还欠服务端一次」。onAttached 撞上
    // 未成型的容器时置位，由容器成型后的第一次量兑现。
    let reassertPending = false
    // fitIfMeasured 只在容器成型后量。没量成时什么都不做——ResizeObserver
    // 会在容器拿到盒子的那一刻再来一次。
    const fitIfMeasured = (): boolean => {
      if (!measured()) return false
      fit.fit()
      // WebGL 渲染器在 fit 之后偶发不重绘（画布尺寸已经对、纹理还是旧的），
      // 看起来就是「TUI 花了，拖一下窗口才好」。强制刷新这一屏。
      term.refresh(0, Math.max(0, term.rows - 1))
      return true
    }
    // 切回时必须再量一次：visibility:hidden 第二次常把 WebGL 画布弄死，
    // 尺寸没变 ResizeObserver 不响，画面就停住。opacity:0 叠着藏可免，
    // 这里再 fit+refresh 兜住。
    revealRef.current = () => { fitIfMeasured() }

    // onResize 必须在 fit.fit() **之前**注册。
    //
    // 挂载时 term.open 给的是默认 80×24，紧接着的 fit.fit() 才算出真实尺寸——
    // 那一次 fit 会触发 onResize，注册晚了就没人接得住。它今天不会真的发出去
    //（此刻 handle 还是 null），但注册次序错了这件事本身是隐患：将来只要有人
    // 在 start() 之前建连，这一次尺寸就又悄悄丢了。真正保证尺寸对齐的是下面
    // onAttached 里的那次重申。
    term.onResize(({ cols, rows }) => {
      logTermResize(label, cols, rows, 'observer')
      handle?.resize(cols, rows)
    })
    // replayingBacklog：下一次（也是 attached 之后的第一帧）是环形缓冲回放。
    // 这段期间 xterm 解析到历史里的 1004h 会合成 [I]/[O]，那不是用户动作。
    let replayingBacklog = false
    const finishReplay = () => {
      if (!replayingBacklog) return
      replayingBacklog = false
      restoreMouseIfNeeded()
    }
    const b270 = (event: string, extra: Record<string, unknown> = {}) => {
      logTermKeepalive(label, event, extra)
      if (!terminalDebugEnabled()) return
      try {
        handleRef.current?.debug?.(`${event} ${JSON.stringify(extra)}`)
      } catch {
        // 取证失败不能变成输入故障
      }
    }
    const snap = () => xtermDebugSnap(term, host)

    term.onData((d) => {
      let rest = d
      // 1004 的 [I]/[O] 一律不上送。切 tab 时 blur 发生在 React 把 active
      // 改成 false 之前，按「仅隐藏时丢 [O]」会漏出去；漏出去再补 [I]，
      // 第一次能恢复、第二次把鼠标追踪关死。切 tab 不要让 TUI 参与 1004。
      for (let head = takeLeadingFocusReport(rest); head; head = takeLeadingFocusReport(rest)) {
        logTermHost(label, head.report)
        b270('drop-focus', { 报告: head.report === '\x1b[O' ? '[O]' : '[I]', ...snap() })
        rest = head.rest
      }
      if (rest === '') return
      // 设备回包（DA / OSC 颜色 / CPR）是 xterm 解析输出时自动生成的，
      // 不是用户按键。经 WebSocket 绕一圈再写进 PTY 经常迟到，切 tab 重放
      // 历史时更会把一串回包打进当前前台进程——zsh 就显示成乱码。
      if (isTerminalHostResponse(rest)) {
        logTermHost(label, rest)
        return
      }
      logTermInput(label, rest, wsStatus)
      handle?.send(new TextEncoder().encode(rest))
    })

    // 鼠标追踪开启时，xterm 每个 DOM wheel 只发一格报告，触控板一甩的像素
    // 全丢了。这里按滑过的行数在**指针所在格子**连发，编码跟 xterm 当前
    // 模式走（禁止写死 1006）。没开追踪则放给 xterm。Option（Mac）/ Shift
    // （其它）划词必须放行，否则自定义 handler 会把选区吃掉。
    //
    // 必须同时挂在 host 的捕获阶段：xterm 只在 1000h 时给 .xterm 绑 wheel，
    // 切 tab / 失焦后这条 listener 经常不在，自定义 handler 根本进不去。
    const wheelRemainder = { x: 0, y: 0 }
    const wheelOnce = new WeakSet<WheelEvent>()
    const isMac = /Mac|iPhone|iPod|iPad/.test(navigator.platform || navigator.userAgent)
    const handleAltWheel = (ev: WheelEvent): boolean => {
      if (!activeRef.current) return true
      if (term.buffer.active.type !== 'alternate') return true
      if (term.modes.mouseTrackingMode === 'none') {
        const now = Date.now()
        if (now - lastWheelMissAt.current > 500) {
          lastWheelMissAt.current = now
          b270('wheel-miss', { 原因: 'mouse-none', ...snap() })
        }
        return true
      }
      if (wheelForcesSelection(ev, isMac)) {
        logTermWheelBypass(label, 'forces-selection')
        return true
      }
      const screenEl = (host.querySelector('.xterm-screen') as HTMLElement | null) ?? host
      const rect = screenEl.getBoundingClientRect()
      const cellH = term.rows > 0 ? rect.height / term.rows : 16
      const cellW = term.cols > 0 ? rect.width / term.cols : 8
      const deltaY = wheelPixelDeltaY(ev, cellH, term.rows)
      const deltaX = ev.deltaX ?? 0
      if (deltaX === 0 && deltaY === 0) return true
      const { col, row } = pointerCell(ev.clientX, ev.clientY, rect, term.cols, term.rows)
      if (col < 1 || row < 1) {
        logTermKeepalive(label, 'wheel-miss', { 原因: 'no-cell', 宽: rect.width, 高: rect.height })
        return true
      }
      const pixelX = Math.max(0, Math.floor(ev.clientX - rect.left))
      const pixelY = Math.max(0, Math.floor(ev.clientY - rect.top))
      const seq = altBufferWheelReports({
        deltaX, deltaY, cellWidth: cellW, cellHeight: cellH,
        remainder: wheelRemainder, col, row, pixelX, pixelY,
        shift: ev.shiftKey, alt: ev.altKey, ctrl: ev.ctrlKey,
        encoding: mouseEncodingOf(term as Parameters<typeof mouseEncodingOf>[0]),
      })
      if (seq !== '') {
        if (!wheelOnce.has(ev)) {
          wheelOnce.add(ev)
          logTermWheel(label, seq.split('\x1b').length - 1, seq)
          term.input(seq)
        }
      }
      return false
    }
    term.attachCustomWheelEventHandler(handleAltWheel)
    const onHostWheel = (ev: WheelEvent) => {
      if (handleAltWheel(ev) === false) {
        ev.preventDefault()
        ev.stopPropagation()
      }
    }
    host.addEventListener('wheel', onHostWheel, { capture: true, passive: false })

    // 焦点取证：只记不改。relatedTarget 是「焦点去了哪儿 / 从哪儿来」的标准来源，
    // 比在 blur 里读 document.activeElement 准——blur 触发时新的焦点元素还没落定。
    // 输入补漏必须装在 term.open() 之后：它要拿 term.textarea，也要抢在 xterm
    // 的 textarea 监听之前读一次「发了没有」的计数（细节见 terminalInput.ts 文件头）。
    const inputFix = installTerminalInputFix(term, host, label)

    // 从访达拖进来的文件：把路径当成用户敲进去的字符送给 shell，跟在真终端里
    // 拖文件的观感一致（补一个尾随空格，好接着敲下一个参数）。
    //
    // 走 term.input() 而不是直接 handle.send()：路径因此与手敲的输入合流到同一条
    // onData，取证日志与 WS 未就绪的告警都照常适用，不必在这里重复一遍那些判断。
    const unregisterDrop = registerFileDropTarget({
      host,
      accept: (paths) => {
        term.focus()
        term.input(`${paths.map(shellQuote).join(' ')} `)
      },
    })

    const ta = term.textarea
    const onFocusEvt = () => logTermFocus(label, 'focus', describeElement(document.activeElement))
    const onBlurEvt = (ev: FocusEvent) => logTermFocus(label, 'blur', describeElement(ev.relatedTarget as Element | null))
    ta?.addEventListener('focus', onFocusEvt)
    ta?.addEventListener('blur', onBlurEvt)

    fitIfMeasured()

    const start = async () => {
      let id = liveId
      if (incompatibleLive) {
        setError('会话由不兼容的版本托管')
        setDead(true)
        setStatus('closed')
        return
      }
      if (!id) {
        const created = await createPtySession(
          { ...ptyBase(base, rel), ...launcherFields(envFile, initCommand), cols: term.cols, rows: term.rows },
          base.machine,
        )
        if (disposed) {
          // 会话已在服务端建成（shell 已 fork），但 id 从没回报给上层——
          // 界面上没有任何入口能连上它或杀掉它，而 ptyhost 只在 shell 退出时
          // 回收、没有空闲清扫，不删就是一个永远挂着的孤儿。
          //
          // 这跟「卸载不删会话」的纪律不冲突：那条护的是**已回报**的会话
          // （tab 里记着 id，切回来还能接上）。这里的会话没人知道它存在。
          void deletePtySession(created.id, base.machine).catch((err: unknown) => {
            console.warn('回收孤儿终端会话失败，服务端可能残留一个 shell', created.id, err)
          })
          return
        }
        id = created.id
        onSession(id)
      }
      if (disposed) return
      handle = connectPty({
        sessionId: id,
        machine: base.machine,
        onAttached: ({ truncated }) => {
          // 下一帧二进制就是 backlog（hostproc 先 attached 再整段回放）。
          replayingBacklog = true
          // 服务端说中间丢了一段：屏幕上现有的内容与即将到来的回放接不上，
          // 不清就会把同一段输出画两遍
          if (truncated) term.clear()
          // **每次建连都重申本订阅者的尺寸**，这是「TUI 乱码、拖一下窗口就好」的修复点。
          //
          // 恢复一个已存在的会话时不走 createPtySession，于是整条挂载路径从头到尾
          // 没有向服务端报过一次尺寸：服务端的 PTY 还是它被创建时的 cols/rows，
          // 里面的 TUI 按那个宽度画，而 xterm 现在是另一个宽度——就是乱码。
          // 用户拖一下窗口，ResizeObserver 触发 fit，尺寸这才发出去，于是「好了」。
          // 切 tab 走的也是这条路（WorkbenchPage 只渲染激活 tab，切回来是恢复不是新建）。
          //
          // 挂在 onAttached 而不是 connectPty 之后同步发：connectPty 内部同步 open()，
          // 此刻 WS 还是 CONNECTING，`ws.send()` 会抛 InvalidStateError。onAttached
          // 由服务端的 attached 帧驱动，触发时 WS 必然已 open。
          //
          // 它同时覆盖重连：断线期间别的订阅者可能把会话尺寸协商成了别的值，
          // 重连后重申一次才是对的。
          //
          // 但**容器还没成型时一个字节都不能报**：那一刻 fit 量出的是 2×1，
          // 报上去等于把用户的 PTY 绞成 2 列（见上面 measured 的说明）。
          // 此时把这次重申挂起，交给容器成型后的第一次量补上——**不是**指望
          // onResize 自己会发：fit 算出的尺寸与 xterm 当前值相同时它根本不触发，
          // 而「相同」恰恰是恢复布局时的常态。
          if (!fitIfMeasured()) {
            reassertPending = true
            return
          }
          reassertPending = false
          logTermResize(label, term.cols, term.rows, 'attach')
          handle?.resize(term.cols, term.rows)
        },
        onData: (bytes) => {
          if (bytes.byteLength === 0) {
            finishReplay()
            return
          }
          const before = `${term.buffer.active.type}/${term.modes.mouseTrackingMode}/${term.modes.sendFocusMode}`
          term.write(bytes, () => {
            const after = `${term.buffer.active.type}/${term.modes.mouseTrackingMode}/${term.modes.sendFocusMode}`
            if (after !== before) {
              b270('tui-mode', { 从: before, 到: after, ...snap() })
            }
            finishReplay()
          })
        },
        onExit: (code) => {
          setExit(code ?? null)
          setStatus('closed')
        },
        onStatus: (s) => {
          wsStatus = s
          setStatus(s)
        },
        onError: (message) => setError(message),
        onTerminal: ({ message }) => {
          // 判死 = 没有重连可等了，界面必须给出下一步动作，而不是只留一行红字
          setError(message)
          setDead(true)
        },
      })
      handleRef.current = handle
      b270('mount', { 会话: liveId ?? '(new)', ...snap() })
      // onData / onResize 已在 effect 开头注册（见那里的次序说明），这里不再重复挂
      // 焦点由下面的 active effect 管：切走 keep-alive 时不能在这里抢焦点。
    }

    start().catch((err: unknown) => {
      if (disposed) return
      setError(err instanceof Error ? err.message : String(err))
    })

    const ro = new ResizeObserver(() => {
      if (!fitIfMeasured()) return
      // 兑现挂起的重申。必须显式发一次而不是等 onResize：尺寸没变时 onResize
      // 不触发，而恢复布局时「量出来正好等于 xterm 当前值」是常态。
      if (reassertPending) {
        reassertPending = false
        logTermResize(label, term.cols, term.rows, 'attach')
        handle?.resize(term.cols, term.rows)
      }
    })
    ro.observe(host)

    return () => {
      disposed = true
      b270('unmount', { 会话: liveId ?? '(new)', ...snap() })
      ro.disconnect()
      host.removeEventListener('wheel', onHostWheel, { capture: true })
      ta?.removeEventListener('focus', onFocusEvt)
      ta?.removeEventListener('blur', onBlurEvt)
      inputFix.dispose()
      unregisterDrop()
      // 只断连接，不发 DELETE：服务端会话继续跑
      handle?.close()
      handleRef.current = null
      termRef.current = null
      revealRef.current = () => {}
      nudgeMouseRef.current = () => {}
      term.dispose()
    }
    // 依赖故意只有会话身份与基准：base.label 之类的展示字段变化不该重建终端。
    // rel 参与身份：改 rel 就该在新的子目录里重建会话。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [liveId, incompatibleLive, base.key, base.machine, rel])

  useEffect(() => {
    // 切 tab 只抢键盘焦点、把渲染器从 IntersectionObserver 暂停里拉回来。
    // 不 blur、不 fit、不 nudge：那些会让 xterm 暂停渲染或给 TUI 发
    // SIGWINCH，第一次切回还能用、第二次就死。后台实例保持原样跑，PTY 不断。
    const term = termRef.current
    const host = hostRef.current
    const extra = term && host ? xtermDebugSnap(term, host) : { xterm: 'none' }
    if (!active) {
      if (prevActiveRef.current) {
        if (terminalDebugEnabled()) {
          handleRef.current?.debug?.(`inactive ${JSON.stringify({ cycle: cycleRef.current, ...extra })}`)
        }
        logTermKeepalive(String(seq), 'inactive', { cycle: cycleRef.current, ...extra })
      }
      prevActiveRef.current = false
      return
    }
    if (!prevActiveRef.current) cycleRef.current += 1
    prevActiveRef.current = true
    if (terminalDebugEnabled()) {
      handleRef.current?.debug?.(`active ${JSON.stringify({ cycle: cycleRef.current, ...extra })}`)
    }
    logTermKeepalive(String(seq), 'active', { cycle: cycleRef.current, ...extra })
    let cancelled = false
    let timeout = 0
    const kick = () => {
      if (cancelled) return
      const t = termRef.current
      if (!t) return
      // xterm 被盖住时 IntersectionObserver 会把渲染器暂停；WKWebView 第二次
      // 露出来经常不回调。私有 _isPaused 是唯一能强制恢复 refresh 的开关。
      const rs = (t as unknown as { _core?: { _renderService?: { _isPaused?: boolean } } })._core?._renderService
      if (rs?._isPaused) {
        rs._isPaused = false
        if (terminalDebugEnabled()) {
          handleRef.current?.debug?.(`unpause ${JSON.stringify({ cycle: cycleRef.current })}`)
        }
        logTermKeepalive(String(seq), 'unpause', { cycle: cycleRef.current })
      }
      t.refresh(0, Math.max(0, t.rows - 1))
      t.focus()
    }
    const raf = requestAnimationFrame(() => {
      kick()
      timeout = window.setTimeout(kick, 0)
    })
    return () => {
      cancelled = true
      cancelAnimationFrame(raf)
      window.clearTimeout(timeout)
    }
  }, [active, seq])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalSquare className="size-3.5" />
        <span className="font-mono">
          {base.label}
          {seq > 1 && ` (${seq})`}
        </span>
        {status === 'connecting' && exit === undefined && <span>连接中…</span>}
        <span className="ml-auto font-mono">{base.path}</span>
      </div>
      <div ref={hostRef} data-testid="pty-host" className="min-h-0 flex-1 overscroll-none bg-[#0b0b0c]" />
      {error !== null && (
        <div className="flex items-center gap-3 border-t px-3 py-1.5 text-xs text-destructive">
          <span>{error}</span>
          {dead && (
            <button
              type="button"
              className="rounded border px-2 py-0.5 text-muted-foreground hover:text-foreground"
              onClick={() => {
                // 划掉旧 id → liveId 变 undefined → effect 重跑，在同一基准目录建新会话。
                // 老会话不发 DELETE：它在服务端要么已经不存在（agentd 重启），要么是被
                // 判死的另一条订阅，替用户去删一个可能还活着的 shell 不是这个按钮的职责。
                setDiscarded(sessionId)
                setError(null)
                setDead(false)
              }}
            >
              重开一个终端
            </button>
          )}
        </div>
      )}
      {exit !== undefined && (
        <div className="border-t px-3 py-1.5 text-xs text-muted-foreground">
          {exit === null ? 'shell 已退出（对端未给出退出码）' : `shell 已退出，退出码 ${exit}`}
          ．关闭这个 tab 即可清理
        </div>
      )}
    </div>
  )
}
