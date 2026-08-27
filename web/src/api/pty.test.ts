import { describe, expect, it, vi } from 'vitest'
import { connectPty, type PtySocketLike } from './pty'

// FakePtySocket 是可编程替身：测试手动驱动 open/message/close。
class FakePtySocket implements PtySocketLike {
  url: string
  binaryType = 'blob'
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  sent: Array<string | ArrayBufferLike> = []
  closed = false
  constructor(url: string) {
    this.url = url
  }
  send(d: string | ArrayBufferLike) {
    this.sent.push(d)
  }
  close() {
    this.closed = true
  }
  emitText(obj: unknown) {
    this.onmessage?.({ data: JSON.stringify(obj) } as MessageEvent)
  }
  emitBinary(s: string) {
    this.onmessage?.({ data: new TextEncoder().encode(s).buffer } as MessageEvent)
  }
  emitClose(code: number) {
    this.onclose?.({ code } as CloseEvent)
  }
  emitOpen() {
    this.onopen?.({} as Event)
  }
}

function harness(overrides: Partial<Parameters<typeof connectPty>[0]> = {}) {
  const sockets: FakePtySocket[] = []
  const onData = vi.fn()
  const onAttached = vi.fn()
  const onExit = vi.fn()
  const handle = connectPty({
    sessionId: 's1',
    onData,
    onAttached,
    onExit,
    create: (url) => {
      const s = new FakePtySocket(url)
      sockets.push(s)
      return s
    },
    ...overrides,
  })
  return { sockets, onData, onAttached, onExit, handle }
}

describe('connectPty', () => {
  it('binaryType 必须是 arraybuffer：默认 blob 会让 onData 拿到一个 Promise 而不是字节', () => {
    const { sockets } = harness()
    expect(sockets[0].binaryType).toBe('arraybuffer')
  })

  it('首帧 attached 转成回调；二进制帧转成字节', () => {
    const { sockets, onAttached, onData } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false })
    expect(onAttached).toHaveBeenCalledWith({ since: 0, truncated: false })
    sockets[0].emitBinary('hi')
    expect(new TextDecoder().decode(onData.mock.calls[0][0])).toBe('hi')
  })

  it('重连时按已收字节数续传，不重复请求已看过的输出', () => {
    vi.useFakeTimers()
    const { sockets } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false })
    sockets[0].emitBinary('12345') // 5 字节
    sockets[0].emitClose(1006)
    vi.advanceTimersByTime(20000)
    expect(sockets.length).toBeGreaterThan(1)
    expect(sockets[1].url).toContain('since=5')
    vi.useRealTimers()
  })

  it('收到 exit 后停止重连——会话没了，重连一百次也没用', () => {
    vi.useFakeTimers()
    const { sockets, onExit } = harness()
    sockets[0].emitText({ type: 'exit', exit_code: 7 })
    expect(onExit).toHaveBeenCalledWith(7)
    sockets[0].emitClose(1000)
    vi.advanceTimersByTime(60000)
    expect(sockets).toHaveLength(1)
    vi.useRealTimers()
  })

  it('close code 1008 是终止而不是抖动，不重连', () => {
    vi.useFakeTimers()
    const onTerminal = vi.fn()
    const { sockets } = harness({ onTerminal })
    sockets[0].emitClose(1008)
    vi.advanceTimersByTime(60000)
    expect(sockets).toHaveLength(1)
    expect(onTerminal).toHaveBeenCalled()
    vi.useRealTimers()
  })

  it('send 走二进制帧，resize 走 JSON 文本帧', () => {
    const { sockets, handle } = harness()
    handle.send(new TextEncoder().encode('ls\n'))
    handle.resize(120, 40)
    expect(sockets[0].sent[0]).toBeInstanceOf(ArrayBuffer)
    expect(JSON.parse(String(sockets[0].sent[1]))).toEqual({ type: 'resize', cols: 120, rows: 40 })
  })

  it('debug 走 JSON 文本帧，不走二进制——取证不能进 PTY', () => {
    const { sockets, handle } = harness()
    sockets[0].emitOpen()
    handle.debug('active cycle=1 mouse=vt200')
    expect(JSON.parse(String(sockets[0].sent[0]))).toEqual({
      type: 'debug', message: 'active cycle=1 mouse=vt200',
    })
  })

  it('open 之前的 debug 在 open 后补发，切 tab 取证不能丢在 CONNECTING', () => {
    const { sockets, handle } = harness()
    handle.debug('mount')
    expect(sockets[0].sent).toHaveLength(0)
    sockets[0].emitOpen()
    expect(JSON.parse(String(sockets[0].sent[0]))).toEqual({ type: 'debug', message: 'mount' })
  })

  it('machine 非空时进查询串——远程终端由本机 agentd 反代', () => {
    const { sockets } = harness({ machine: 'devbox' })
    expect(sockets[0].url).toContain('machine=devbox')
  })
})
