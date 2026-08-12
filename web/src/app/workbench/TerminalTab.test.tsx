import { render, screen, waitFor } from '@testing-library/react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { TerminalTab } from './TerminalTab'
import type { BaseDir } from './useWorkbench'

// xterm 要量真实字体尺寸，jsdom 给不了。整体替身：本测试关心的是
// 「什么时候建会话、拿什么参数连、收到帧之后往终端写什么」，
// 不是 xterm 自己的渲染——那是上游的测试职责。
const termInstance = {
  cols: 100,
  rows: 30,
  open: vi.fn(),
  write: vi.fn(),
  writeln: vi.fn(),
  clear: vi.fn(),
  focus: vi.fn(),
  dispose: vi.fn(),
  loadAddon: vi.fn(),
  onData: vi.fn(),
  onResize: vi.fn(),
}
// Terminal 用常规 function 而不是箭头函数：组件以 `new Terminal(...)` 实例化它，
// 箭头函数不是构造函数，`new` 会直接抛 TypeError。function 是构造的，且 `new`
// 时返回对象即 `new` 的结果（标准 JS 语义），于是实例落到 termInstance 上。
vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn(function () { return termInstance }) }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(function () { return { fit: vi.fn() } }) }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return {} }) }))

const createPtySession = vi.fn()
const connectPty = vi.fn()
vi.mock('../../api/client', () => ({ createPtySession: (...a: unknown[]) => createPtySession(...a) }))
vi.mock('../../api/pty', () => ({ connectPty: (...a: unknown[]) => connectPty(...a) }))

const WS: BaseDir = {
  key: '/home/dev/handoff', kind: 'workspace', path: '/home/dev/handoff',
  label: 'main', projectName: 'handoff', machine: '',
}
const HOME: BaseDir = {
  key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '',
}

beforeAll(() => {
  // jsdom 没有 ResizeObserver，而组件用它跟随容器尺寸
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  createPtySession.mockResolvedValue({ id: 'new-1', base_path: WS.path })
  connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize: vi.fn() })
})

describe('TerminalTab', () => {
  it('没有会话 id 时先建会话，参数取自基准目录与当前尺寸', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalledTimes(1))
    expect(createPtySession).toHaveBeenCalledWith(
      { base_kind: 'workspace', base_path: '/home/dev/handoff', cols: 100, rows: 30 },
      '',
    )
  })

  it('home 基准不把 "~" 发给后端——那不是一个服务端认识的路径', async () => {
    render(<TerminalTab base={HOME} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())
    expect(createPtySession.mock.calls[0][0]).toMatchObject({ base_kind: 'home', base_path: '' })
  })

  it('建成后把会话 id 回报给上层，供 tab 记住', async () => {
    const onSession = vi.fn()
    render(<TerminalTab base={WS} seq={1} onSession={onSession} />)
    await waitFor(() => expect(onSession).toHaveBeenCalledWith('new-1'))
  })

  it('已有会话 id 时直接接流，不再建第二个会话', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="old-9" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    expect(createPtySession).not.toHaveBeenCalled()
    expect(connectPty.mock.calls[0][0]).toMatchObject({ sessionId: 'old-9', machine: '' })
  })

  it('收到字节写进终端', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onData(new TextEncoder().encode('hello'))
    expect(termInstance.write).toHaveBeenCalled()
  })

  it('attached 带 truncated 时先清屏——不清就会把同一段输出画两遍', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 4096, truncated: true })
    expect(termInstance.clear).toHaveBeenCalled()
  })

  it('attached 不带 truncated 时不清屏', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false })
    expect(termInstance.clear).not.toHaveBeenCalled()
  })

  it('退出后在终端下方显示退出码，tab 不自己消失', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onExit(7)
    expect(await screen.findByText(/退出码 7/)).toBeInTheDocument()
  })

  it('建会话失败时说实话，不是白屏', async () => {
    createPtySession.mockRejectedValue(new Error('该 agentd 所在平台不支持 PTY 终端'))
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    expect(await screen.findByText(/不支持 PTY 终端/)).toBeInTheDocument()
  })

  it('卸载时只断连接，不删会话', async () => {
    const handle = { close: vi.fn(), send: vi.fn(), resize: vi.fn() }
    connectPty.mockReturnValue(handle)
    const { unmount } = render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    unmount()
    expect(handle.close).toHaveBeenCalled()
    expect(termInstance.dispose).toHaveBeenCalled()
  })
})
