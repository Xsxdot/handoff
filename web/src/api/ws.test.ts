// /ws/events 客户端生命周期契约测试（重点钉死 1008 终止语义）。
//
// 会话失效语义（硬契约）：agentd 在会话被吊销时以 close code **1008** 关闭
// 连接——订阅本身已被判死，无限退避重连只会是一场静默的重连风暴。因此：
//   - close code 1008 → 回调 onTerminal，**不再重连**
//   - 其他 close code → 走指数退避重连（事件全部落库，凭游标续拉无损）
//
// 用 FakeWebSocket 注入（ws.ts 的 options.create），避免依赖 jsdom 没有的
// 真 WebSocket 实现；退避定时器用 vi.useFakeTimers 控制。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Event } from './types'
import { connectEvents, wsCloseReason, type WsStatus } from './ws'

// FakeWebSocket 模拟浏览器 WebSocket 的最小可编程表面。
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  url: string
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  closeCount = 0

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closeCount++
  }

  emitOpen() {
    this.onopen?.()
  }

  emitMessage(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) })
  }

  emitClose(code: number) {
    // CloseEvent 的部分字段 jsdom 没有，只传连接生命周期关心的
    this.onclose?.({ code, reason: '', wasClean: false } as CloseEvent)
  }
}

const create = (url: string) => new FakeWebSocket(url)

function event(seq: number): Event {
  return { seq, task_id: 't1', type: 'progress', payload: {}, created_at: '2026-08-11T10:30:00+08:00' }
}

beforeEach(() => {
  FakeWebSocket.instances = []
})

describe('connectEvents 连接生命周期', () => {
  it('close code 1008 → 终止态（onTerminal），不重连', () => {
    vi.useFakeTimers()
    const statuses: WsStatus[] = []
    const errors: string[] = []
    const terminals: Array<{ message: string; closeCode: number }> = []
    const conn = connectEvents({
      taskId: 't1',
      create,
      onStatus: (s) => statuses.push(s),
      onError: (m) => errors.push(m),
      onTerminal: (t) => terminals.push(t),
      onEvent: () => {},
    })
    const first = FakeWebSocket.instances[0]
    first.emitOpen()
    first.emitClose(1008)

    // 推进所有定时器：如果 1008 还走退避重连，会创建第二个 FakeWebSocket
    vi.advanceTimersByTime(120000)

    expect(terminals).toHaveLength(1)
    expect(terminals[0].closeCode).toBe(1008)
    expect(FakeWebSocket.instances).toHaveLength(1) // 没有第二次拨号
    expect(errors).toHaveLength(0) // 终止不是可恢复错误，不走 onError
    expect(statuses).toEqual(['open', 'closed']) // 先报 closed 再落终止
    conn.close()
    vi.useRealTimers()
  })

  it('其他 close code（1006）→ 走退避重连，且游标以最后收到的 seq 续拉', () => {
    vi.useFakeTimers()
    const received: Event[] = []
    const conn = connectEvents({
      taskId: 't1',
      fromSeq: 5,
      create,
      onEvent: (ev) => received.push(ev),
      onStatus: () => {},
      onError: () => {},
      onTerminal: () => {},
    })
    const first = FakeWebSocket.instances[0]
    first.emitOpen()
    first.emitMessage(event(9))
    first.emitClose(1006)

    expect(received.map((e) => e.seq)).toEqual([9])
    expect(FakeWebSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(300) // 退避首轮 300ms 后重连
    expect(FakeWebSocket.instances).toHaveLength(2)
    // 重连请求带上 last seq：from_seq=9 只补 seq>9 的事件，不重放已见
    expect(FakeWebSocket.instances[1].url).toContain('from_seq=9')
    conn.close()
    vi.useRealTimers()
  })

  it('收到事件按序回调，重连成功时 onStatus 回到 open', () => {
    vi.useFakeTimers()
    const received: Event[] = []
    const statuses: WsStatus[] = []
    const conn = connectEvents({
      taskId: 't1',
      create,
      onEvent: (ev) => received.push(ev),
      onStatus: (s) => statuses.push(s),
      onError: () => {},
      onTerminal: () => {},
    })
    const first = FakeWebSocket.instances[0]
    first.emitOpen()
    first.emitMessage(event(1))
    first.emitMessage(event(2))
    first.emitClose(1006)
    vi.advanceTimersByTime(300)
    const second = FakeWebSocket.instances[1]
    second.emitOpen()
    expect(received.map((e) => e.seq)).toEqual([1, 2])
    expect(statuses).toContain('open')
    conn.close()
    vi.useRealTimers()
  })

  it('主动 close() 停止一切（含重连定时器）', () => {
    vi.useFakeTimers()
    const conn = connectEvents({ taskId: 't1', create, onEvent: () => {}, onStatus: () => {}, onError: () => {}, onTerminal: () => {} })
    conn.close()
    vi.advanceTimersByTime(120000)
    expect(FakeWebSocket.instances).toHaveLength(1) // close 后不自动重连
    vi.useRealTimers()
  })
})

// wsCloseReason 的取信顺序：**服务端给了原因就用服务端的**。
//
// 为什么单独钉死：agentd 关 /ws/pty 时把真实原因（如「终端会话不存在」，
// agentd 重启后必然如此）写进了 close reason，而按 code 查表得到的只有
// 「订阅非法（agentd 吊销了本连接）」——两句话指向完全不同的排查方向，
// 用户按后者查会一路查到鉴权上去。丢掉 reason 就是丢掉唯一的现场。
describe('wsCloseReason', () => {
  it('服务端给了 reason 就显示原文，不再显示按 code 猜的通用文案', () => {
    const r = wsCloseReason({ code: 1008, reason: '终端会话不存在' } as CloseEvent)
    expect(r.message).toContain('终端会话不存在')
    expect(r.code).toBe(1008)
  })

  it('reason 为空时回退到按 code 的通用文案', () => {
    expect(wsCloseReason({ code: 1008, reason: '' } as CloseEvent).message).toContain('订阅非法')
    expect(wsCloseReason({ code: 1006, reason: '' } as CloseEvent).message).toContain('agentd 未运行')
  })
})
