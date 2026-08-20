// agentd 的 /ws/events 客户端（浏览器侧）。
//
// 职责：
//   - 打开同源 /ws/events?task=<id>&from_seq=<cursor> 连接（经 vite 反代，
//     cookie 由浏览器在握手时自动携带）
//   - 把收到的每条 Event 交给回调；连接生命周期（打开/断开/出错/终止）都要通知
//     调用方，供界面可观察地渲染，不静默失败
//   - 指数退避自动重连：事件全部落库，凭 from_seq 游标重连即可补齐
//
// 会话失效语义（硬契约）：
//   - agentd 在会话被吊销、或订阅目标非法时以 close code **1008**（StatusPolicy
//     Violation）关闭连接。此时订阅本身已被判死，无限退避重连只会是一场静默的
//     重连风暴——本层**停止重连**并回调 onTerminal，由界面落到
//     「会话已失效，请重新打开控制台」的终止态
//   - 其他 close code（1006 网络断、1000 正常关、越限断连）都走退避重连，事件
//     全部落库，凭游标重连补拉无损
//
// cursor 归属：本层只在自己的 from_seq 参数上推进游标（收到的最后一条事件 seq），
// **不碰** ~/.handoff/cursor-* 那本 CLI 审核者的本机游标账本——浏览器是观察者，
// 与 CLI 同时盯同一任务时互不干扰。重连时用已推进的游标续拉，避免重复。
//
// 边界：
//   - 只做「收到事件就回调」的透传，不做事件归并/去重/持久化——同一连接内
//     agentd 已按 seq 保序去重，跨连接由本层的游标推进承担
//   - 不做鉴权：cookie 随同源握手自动带上，本层不碰凭据
import type { Event as HandoffEvent } from './types'

export type WsStatus = 'connecting' | 'open' | 'closed'

// WsSocketLike 是本层用到的 WebSocket 最小表面：真 WebSocket 与测试替身
// （ws.test.ts 的 FakeWebSocket）都满足。用接口而不是直接依赖 DOM 的 WebSocket，
// 是为了让连接生命周期能在 vitest 里被一个可编程替身驱动。
export interface WsSocketLike {
  url: string
  onopen: ((ev: Event) => void) | null
  onmessage: ((ev: MessageEvent) => void) | null
  onerror: ((ev: Event) => void) | null
  onclose: ((ev: CloseEvent) => void) | null
  close: () => void
}

// WsTermination 是一次不可恢复的终止：订阅被 agentd 判死（会话吊销 / 任务非法）。
export interface WsTermination {
  message: string
  closeCode: number
}

// WsOptions 参数。
//
// - taskId: 订阅的任务；fromSeq: 起始游标（不含，0=从最初事件开始）
// - onEvent: 每收到一条事件调用一次（按到达顺序）
// - onStatus: 连接状态变化回调（供界面显示连接是否健康）
// - onError: 出错/断开回调（可恢复，将自动退避重连）；message 是人类可读原因，
//   closeCode 是 WebSocket close code
// - onTerminal: 不可恢复终止回调（close code 1008）；触发后不再重连
// - create: 测试注入用；缺省时 new WebSocket(url)
export interface WsOptions {
  taskId: string
  fromSeq?: number
  onEvent: (ev: HandoffEvent) => void
  onStatus?: (status: WsStatus) => void
  onError?: (message: string, closeCode: number) => void
  onTerminal?: (termination: WsTermination) => void
  create?: (url: string) => WsSocketLike
}

// wsUrl 用当前页面地址推导同源的 WS URL。
//
// 为什么从 location 推导而不是写死 localhost：dev server 与将来的部署
// host/port 都可能变，硬编码会在换端口时静默连错地址（握手上 404）。
function wsUrl(taskId: string, fromSeq: number): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}/ws/events?task=${encodeURIComponent(taskId)}&from_seq=${fromSeq}`
}

// connectEvents 打开一条事件订阅，返回可手动关闭连接的句柄。
//
// 返回：
//   - close(): 主动关闭连接并停止重连；终止后调用无害（幂等）
//
// 注意：
//   - 自动重连是无限退避（300ms 起、上限 10s、随机抖动），因为断连重连是这套
//     事件流的设计内行为（事件全部落库，重连补拉无损）；**close code 1008 除外**——
//     那是一次不可恢复的终止，本层停止重连并回调 onTerminal
//   - 游标随收到的事件推进：重连时以最后收到的 seq 续拉，不重放已见事件
export function connectEvents(options: WsOptions): { close: () => void } {
  const { taskId, onEvent } = options
  // cursor 是「已收到事件的最后 seq」：重连时作为 from_seq 续拉（不含）。
  // 从 fromSeq 起推进——收到 seq=N 后重连请求 from_seq=N，服务端只补 seq>N。
  let cursor = options.fromSeq ?? 0
  let ws: WsSocketLike | null = null
  let closedByUs = false
  let terminal = false
  let retryDelay = 300
  let retryTimer: number | undefined

  function cleanup() {
    if (ws) {
      ws.onopen = null
      ws.onmessage = null
      ws.onerror = null
      ws.onclose = null
      ws.close()
      ws = null
    }
  }

  function scheduleReconnect() {
    if (closedByUs || terminal) return
    options.onStatus?.('connecting')
    retryTimer = window.setTimeout(open, retryDelay)
    retryDelay = Math.min(retryDelay * 2 + Math.floor(Math.random() * 200), 10000)
  }

  function open() {
    if (closedByUs || terminal) return
    ws = (options.create ?? ((url: string) => new WebSocket(url)))(wsUrl(taskId, cursor))
    ws.onopen = () => {
      retryDelay = 300 // 连上后重置退避，下次断连重新从最短间隔起
      options.onStatus?.('open')
    }
    ws.onmessage = (msg) => {
      try {
        const ev = JSON.parse(String(msg.data)) as HandoffEvent
        // 收到的就是「已读水位」：同连接内 agentd 按 seq 升序保序写出，
        // 最后一条即最大 seq，重连用它续拉恰好不重不漏
        cursor = ev.seq
        onEvent(ev)
      } catch (err) {
        options.onError?.(`收到无法解析的事件帧：${err instanceof Error ? err.message : String(err)}`, 0)
      }
    }
    ws.onerror = () => {
      // 错误与关闭事件成对出现，onclose 统一收口通知，这里不重复上报
    }
    ws.onclose = (ev) => {
      if (closedByUs) return
      const reason = wsCloseReason(ev)
      options.onStatus?.('closed')
      if (ev.code === 1008) {
        // 订阅被判死（会话吊销 / 任务非法）：终止，绝不无脑重连。
        // onStatus 先报 closed 再报 terminal：界面先隐藏实时性，再落终止态
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
  }
}

// wsCloseReason 把 WS 关闭事件翻译成人类可读原因。
//
// 为什么单独归口：onclose 的 ev.code 在不同浏览器上报的既有差异，又和
// agentd 侧语义（1008=订阅非法，见 server.go 的 handleEvents 用 PolicyViolation
// 关闭的语义）叠加，集中在函数里翻译一处修改、全界面生效。
//
// 取信顺序：**服务端给的 reason 优先，code 查表只是兜底**。agentd 关连接时
// 把真实原因写在 close reason 里（如 /ws/pty 的「终端会话不存在」——agentd
// 一重启，内存里的 PTY 会话全没，重连必然撞这条），而按 code 查表只能给出
// 「订阅非法（agentd 吊销了本连接）」这种指向鉴权的通用话术。两句话把人引向
// 完全不同的排查方向，丢掉 reason 等于丢掉唯一的现场证据。
export function wsCloseReason(ev: CloseEvent): { message: string; code: number } {
  // reason 可能缺席（1006 断网、部分浏览器不回填），此处只认非空串
  if (ev.reason) return { message: ev.reason, code: ev.code }
  switch (ev.code) {
    case 1008:
      return { message: '会话已失效或订阅非法（agentd 吊销了本连接）', code: ev.code }
    case 1006:
      return { message: '连接异常断开（agentd 未运行或握手鉴权失败？）', code: ev.code }
    case 1000:
      return { message: '连接正常关闭', code: ev.code }
    default:
      return { message: `连接关闭（code ${ev.code}）`, code: ev.code }
  }
}
