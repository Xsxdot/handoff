// agentd 的 /ws/pty 客户端（浏览器侧）——ws.ts 的孪生。
//
// 职责：
//   - 打开同源 /ws/pty?session=&since=[&machine=]，收发 PTY 字节
//   - binary 帧 = 数据（双向），text 帧 = JSON 控制（attached / exit / error / resize）
//   - 指数退避自动重连，**按已收字节数续传**
//
// 与 ws.ts 的两点关键差异（不要照抄 ws.ts 了事）：
//   1. 游标是**字节数**不是事件 seq：PTY 输出没有天然的条目边界，服务端的
//      环形缓冲按绝对字节序号索引，客户端也就只能数字节（抄 handoff attach）
//   2. 必须设 binaryType='arraybuffer'：默认的 'blob' 会让 onmessage 拿到一个
//      需要 await 的 Blob，终端渲染顺序会因此乱掉
//
// 终止语义（两种，都不重连）：
//   - close code 1008：会话不存在 / 连接数超限 / 会话被吊销
//   - 收到 exit 控制帧：shell 自己退出了，会话已成终态
// 其余（1006 网络断、1000 正常关）一律退避重连——这正是「关掉页面走人，
// 回来接着看」能成立的地方。
//
// 边界：不认识 xterm、不解析转义序列，只搬字节。
import type { PtyControl } from './types'
import { wsCloseReason, type WsStatus, type WsTermination } from './ws'

// PtySocketLike 是本层用到的 WebSocket 最小表面（真 WebSocket 与测试替身都满足）。
export interface PtySocketLike {
  url: string
  binaryType: string
  onopen: ((ev: Event) => void) | null
  onmessage: ((ev: MessageEvent) => void) | null
  onerror: ((ev: Event) => void) | null
  onclose: ((ev: CloseEvent) => void) | null
  send: (data: string | ArrayBufferLike) => void
  close: () => void
}

export interface PtyOptions {
  sessionId: string
  machine?: string
  // since: 起始字节游标。恢复已有会话时传 0 即可——服务端会把整个环形缓冲
  // 回放给你并在 attached 帧里标 truncated。
  since?: number
  onData: (bytes: Uint8Array) => void
  // onAttached 在**每次**建连时触发（含重连）。truncated=true 表示中间丢了一段，
  // 调用方必须先清屏再灌，否则同一段输出会被重复画。backlog_bytes 缺席时表示
  // 旧服务端，调用方不能把缺席填成 0。
  onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
  // onExit：shell 已退出。exitCode 可能缺席（对端没给），此时不要显示成 0。
  onExit: (exitCode?: number) => void
  onStatus?: (status: WsStatus) => void
  onError?: (message: string, closeCode: number) => void
  onTerminal?: (termination: WsTermination) => void
  create?: (url: string) => PtySocketLike
}

export interface PtyHandle {
  close: () => void
  send: (bytes: Uint8Array) => void
  resize: (cols: number, rows: number) => void
  // debug 把一条取证记到 agentd 日志（type=debug 控制帧），不进 PTY。
  debug: (message: string) => void
}

function ptyUrl(sessionId: string, since: number, machine?: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const q = new URLSearchParams({ session: sessionId, since: String(since) })
  if (machine) q.set('machine', machine)
  return `${proto}//${window.location.host}/ws/pty?${q.toString()}`
}

// connectPty 打开一条 PTY 数据通道，返回可写可关的句柄。
//
// 注意：close() 只断连接，**不删会话**——服务端会话继续跑。要真的杀掉它，
// 调 deletePtySession（spec §3.2、§6.2）。
export function connectPty(options: PtyOptions): PtyHandle {
  // cursor 是「已收到的字节数」：重连时作为 since 续传。
  let cursor = options.since ?? 0
  let ws: PtySocketLike | null = null
  let closedByUs = false
  let terminal = false
  let retryDelay = 300
  let retryTimer: number | undefined
  let opened = false
  const pendingDebug: string[] = []

  function cleanup() {
    if (!ws) return
    ws.onopen = null
    ws.onmessage = null
    ws.onerror = null
    ws.onclose = null
    ws.close()
    ws = null
  }

  function scheduleReconnect() {
    if (closedByUs || terminal) return
    options.onStatus?.('connecting')
    retryTimer = window.setTimeout(open, retryDelay)
    retryDelay = Math.min(retryDelay * 2 + Math.floor(Math.random() * 200), 10000)
  }

  function handleControl(raw: string) {
    let ctrl: PtyControl
    try {
      ctrl = JSON.parse(raw) as PtyControl
    } catch (err) {
      options.onError?.(`收到无法解析的控制帧：${err instanceof Error ? err.message : String(err)}`, 0)
      return
    }
    switch (ctrl.type) {
      case 'attached':
        // 服务端说它从哪个字节开始给：以**它**的口径为准推进游标。
        // 用本地的猜测会在 truncated 时把游标停在一个环里已经没有的位置。
        cursor = ctrl.since
        const info: { since: number; truncated: boolean; backlog_bytes?: number } = {
          since: ctrl.since,
          truncated: ctrl.truncated,
        }
        // 缺键表示旧服务端；不要填 0，否则调用方会误把旧录像当成没有旧录像。
        if (typeof ctrl.backlog_bytes === 'number') info.backlog_bytes = ctrl.backlog_bytes
        options.onAttached(info)
        return
      case 'exit':
        terminal = true
        options.onExit(ctrl.exit_code)
        return
      case 'error':
        options.onError?.(ctrl.message ?? '服务端报告了一个未说明的终端错误', 0)
        return
      default:
        // 不认识的控制帧一律忽略：前端比后端晚部署是常态，新增一种控制帧
        // 不该让旧前端崩掉（与 KNOWN_FRAME_TYPES 同一条纪律）
        return
    }
  }

  function open() {
    if (closedByUs || terminal) return
    opened = false
    ws = (options.create ?? ((url: string) => new WebSocket(url) as unknown as PtySocketLike))(
      ptyUrl(options.sessionId, cursor, options.machine),
    )
    // 必须在任何消息到达之前设：blob 模式下 onmessage 拿到的是需要 await 的对象
    ws.binaryType = 'arraybuffer'
    ws.onopen = () => {
      opened = true
      retryDelay = 300
      options.onStatus?.('open')
      while (pendingDebug.length > 0) {
        const message = pendingDebug.shift()
        if (message !== undefined) {
          try {
            ws?.send(JSON.stringify({ type: 'debug', message }))
          } catch {
            break
          }
        }
      }
    }
    ws.onmessage = (msg) => {
      if (typeof msg.data === 'string') {
        handleControl(msg.data)
        return
      }
      const bytes = new Uint8Array(msg.data as ArrayBuffer)
      cursor += bytes.byteLength
      options.onData(bytes)
    }
    ws.onerror = () => {
      // 与 onclose 成对出现，统一在 onclose 收口
    }
    ws.onclose = (ev) => {
      if (closedByUs) return
      options.onStatus?.('closed')
      if (terminal) {
        // 已经收到 exit：这次关闭是它的正常收尾，不是故障，也不重连
        cleanup()
        return
      }
      const reason = wsCloseReason(ev)
      if (ev.code === 1008) {
        terminal = true
        cleanup()
        options.onTerminal?.({ message: reason.message, closeCode: ev.code })
        return
      }
      options.onError?.(reason.message, ev.code)
      cleanup()
      scheduleReconnect()
    }
  }

  open()

  return {
    close() {
      closedByUs = true
      if (retryTimer !== undefined) window.clearTimeout(retryTimer)
      cleanup()
    },
    send(bytes) {
      // 切出一个独立的 ArrayBuffer：Uint8Array 可能是某个更大 buffer 的视图，
      // 直接送 .buffer 会把整块内存发出去。new Uint8Array(bytes) 是逐元素复制，
      // 恰好满足两件事：产物是独立 buffer（视图不会拖走整块底层内存），并且
      // buffer 落在本模块的 realm 里（jsdom 测试环境下直接 slice 会拿到另一个
      // realm 的 ArrayBuffer，WebSocket.send 两侧都不认它）。
      ws?.send(new Uint8Array(bytes).buffer)
    },
    resize(cols, rows) {
      ws?.send(JSON.stringify({ type: 'resize', cols, rows }))
    },
    debug(message) {
      if (!opened) {
        if (pendingDebug.length < 50) pendingDebug.push(message)
        return
      }
      try {
        ws?.send(JSON.stringify({ type: 'debug', message }))
      } catch {
        // 通道未就绪时取证丢失，不能把诊断路径变成输入故障
      }
    },
  }
}
