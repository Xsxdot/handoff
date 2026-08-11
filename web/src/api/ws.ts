// agentd 的 /ws/events 客户端（浏览器侧）。
//
// 职责：
//   - 打开同源 /ws/events?task=<id>&from_seq=<cursor> 连接（经 vite 反代，
//     cookie 由浏览器在握手时自动携带）
//   - 把收到的每条 Event 交给回调；连接生命周期（打开/断开/出错）都要通知
//     调用方，供界面可观察地渲染，不静默失败
//   - 简单的指数退避自动重连：事件全部落库，凭 from_seq 游标重连即可补齐
//
// 边界：
//   - 只做「收到事件就回调」的透传，不做事件归并/去重/游标持久化——那是
//     后续任务现场的责任；本层保留 from_seq 参数位置便于扩展
//   - 不做鉴权：cookie 随同源握手自动带上，本层不碰凭据
import type { Event } from './types'

export type WsStatus = 'connecting' | 'open' | 'closed'

// WsClient 参数。
//
// - taskId: 订阅的任务；fromSeq: 起始游标（不含，0=从最初事件开始）
// - onEvent: 每收到一条事件调用一次（按到达顺序）
// - onStatus: 连接状态变化回调（供界面显示连接是否健康）
// - onError: 出错/断开回调；message 是人类可读原因，closeCode 是
//   WebSocket close code（0=未给出，1008=agentd 判该订阅非法，见 server.go
//   的 handleEvents 用 PolicyViolation 关闭的语义）
export interface WsOptions {
  taskId: string
  fromSeq?: number
  onEvent: (ev: Event) => void
  onStatus?: (status: WsStatus) => void
  onError?: (message: string, closeCode: number) => void
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
//   - close(): 主动关闭连接并停止重连；onClose 里也应先调用它防重连
//
// 注意：
//   - 自动重连是无限退避（300ms 起、上限 10s、随机抖动），因为断连重连是
//     这套事件流的设计内行为（事件全部落库，重连补拉无损）；若界面想彻底
//     停掉订阅，调用返回的 close()
export function connectEvents(options: WsOptions): { close: () => void } {
  const { taskId, fromSeq = 0 } = options
  let ws: WebSocket | null = null
  let closedByUs = false
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
    if (closedByUs) return
    options.onStatus?.('connecting')
    retryTimer = window.setTimeout(open, retryDelay)
    retryDelay = Math.min(retryDelay * 2 + Math.floor(Math.random() * 200), 10000)
  }

  function open() {
    if (closedByUs) return
    ws = new WebSocket(wsUrl(taskId, fromSeq))
    ws.onopen = () => {
      retryDelay = 300 // 连上后重置退避，下次断连重新从最短间隔起
      options.onStatus?.('open')
    }
    ws.onmessage = (msg) => {
      try {
        options.onEvent(JSON.parse(String(msg.data)) as Event)
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
// agentd 侧语义（1008=订阅非法）叠加，集中在函数里翻译一处修改、全界面生效。
function wsCloseReason(ev: CloseEvent): { message: string; code: number } {
  switch (ev.code) {
    case 1008:
      return { message: '订阅被 agentd 判定非法并关闭（任务不存在或会话已被吊销）', code: ev.code }
    case 1006:
      return { message: '连接异常断开（agentd 未运行或握手鉴权失败？）', code: ev.code }
    case 1000:
      return { message: '连接正常关闭', code: ev.code }
    default:
      return { message: `连接关闭（code ${ev.code}）`, code: ev.code }
  }
}
