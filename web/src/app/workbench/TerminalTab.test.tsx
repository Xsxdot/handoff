import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
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
// webgl addon 的替身要能捕获 onContextLoss 回调并记录 dispose：
// 「上下文丢了之后有没有回退」正是本组件的职责，必须能测
const webglAddon = { onContextLoss: vi.fn(), dispose: vi.fn() }
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return webglAddon }) }))

const createPtySession = vi.fn()
const deletePtySession = vi.fn()
const connectPty = vi.fn()
vi.mock('../../api/client', () => ({
  createPtySession: (...a: unknown[]) => createPtySession(...a),
  deletePtySession: (...a: unknown[]) => deletePtySession(...a),
}))
vi.mock('../../api/pty', () => ({ connectPty: (...a: unknown[]) => connectPty(...a) }))

const WS: BaseDir = {
  key: '/home/dev/handoff', kind: 'workspace', path: '/home/dev/handoff',
  label: 'main', projectName: 'handoff', machine: '',
}
const HOME: BaseDir = {
  key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '',
}

// roCallbacks 收着组件注册的 ResizeObserver 回调，测试用它模拟「容器尺寸变了」。
// 不这么做就没法测「容器成型后补上那次尺寸重申」——那条路径只由观察者驱动。
const roCallbacks: Array<() => void> = []

beforeAll(() => {
  // jsdom 没有 ResizeObserver，而组件用它跟随容器尺寸
  globalThis.ResizeObserver = class {
    constructor(cb: () => void) {
      roCallbacks.push(cb)
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.clearAllMocks()
  roCallbacks.length = 0
  createPtySession.mockResolvedValue({ id: 'new-1', base_path: WS.path })
  deletePtySession.mockResolvedValue({ ok: true })
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

  it('WebGL 上下文丢失时 dispose 掉渲染器，交回 DOM 渲染——不能白屏', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(webglAddon.onContextLoss).toHaveBeenCalled())
    webglAddon.onContextLoss.mock.calls[0][0]({})
    expect(webglAddon.dispose).toHaveBeenCalledTimes(1)
  })

  it('构造期就不可用时照样活着——不抛出去，终端仍然能连', async () => {
    const { WebglAddon } = await import('@xterm/addon-webgl')
    vi.mocked(WebglAddon).mockImplementationOnce(() => { throw new Error('no webgl') })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
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

  it('建会话的过程中被卸载：把这个没人知道的会话删掉，不留孤儿 shell', async () => {
    let resolveCreate!: (v: unknown) => void
    createPtySession.mockReturnValue(new Promise((r) => { resolveCreate = r }))
    const { unmount } = render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())
    unmount()
    resolveCreate({ id: 'orphan-1', base_path: WS.path })
    await waitFor(() => expect(deletePtySession).toHaveBeenCalledWith('orphan-1', ''))
  })

  // 1008 = 服务端判死这条订阅，前端不重连（api/pty.ts 的终止语义）。最常见的
  // 一种是 agentd 重启过：PTY 会话只活在它的进程内存里，重启即全没，而页面里
  // 这个 tab 还记着旧 id。此时只给一行红字等于把 tab 变成死物，用户唯一的出路
  // 是关掉重开——那正是这个按钮要替他做的事。
  it('会话被判死（1008）时给出重开入口：点一下就在同一基准目录建新会话', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="dead-1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onTerminal({ message: '终端会话不存在', closeCode: 1008 })
    expect(await screen.findByText(/终端会话不存在/)).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('button', { name: /重开/ }))
    await waitFor(() => expect(createPtySession).toHaveBeenCalledTimes(1))
    expect(createPtySession.mock.calls[0][0]).toMatchObject({
      base_kind: 'workspace', base_path: '/home/dev/handoff',
    })
  })

  it('重开后把新会话 id 回报给上层——不回报的话切一次 tab 又回到那个死 id', async () => {
    const onSession = vi.fn()
    render(<TerminalTab base={WS} seq={1} sessionId="dead-1" onSession={onSession} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onTerminal({ message: '终端会话不存在', closeCode: 1008 })
    fireEvent.click(await screen.findByRole('button', { name: /重开/ }))
    await waitFor(() => expect(onSession).toHaveBeenCalledWith('new-1'))
  })

  it('重开后那行红字消失——留着它会让人以为新会话也是坏的', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="dead-1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onTerminal({ message: '终端会话不存在', closeCode: 1008 })
    fireEvent.click(await screen.findByRole('button', { name: /重开/ }))
    await waitFor(() => expect(screen.queryByText(/终端会话不存在/)).not.toBeInTheDocument())
  })

  // 只有终止态（1008）才给按钮：其余关闭码 api/pty.ts 还在退避重连，此时给一个
  // 「重开」等于鼓励用户在重连成功前再造一个会话，白留一个 shell。
  it('普通断线（还在重连）不给重开按钮', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onError('连接异常断开（agentd 未运行或握手鉴权失败？）', 1006)
    expect(await screen.findByText(/连接异常断开/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /重开/ })).not.toBeInTheDocument()
  })

  it('已回报过的会话在卸载时不删——切 tab 不能杀掉跑了一晚上的 build', async () => {
    const { unmount } = render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    unmount()
    expect(deletePtySession).not.toHaveBeenCalled()
  })
})

// 尺寸重申：这是「TUI 乱码、拖一下窗口就好」的回归网。
//
// 恢复已有会话时不走 createPtySession，历史上整条挂载路径从头到尾没向服务端
// 报过一次尺寸——服务端 PTY 停在创建时的 cols/rows，xterm 是另一个宽度，TUI 就花了。
// stubLayout 给 jsdom 里的元素一个布局盒子。
//
// jsdom 的 getBoundingClientRect 恒返回全 0，而组件把「没有布局盒子」当成
// 「此刻量出来的尺寸不作数」——不桩掉它，所有依赖尺寸上报的用例都会走进跳过分支。
function stubLayout(width: number, height: number) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    width, height, x: 0, y: 0, top: 0, left: 0, right: width, bottom: height,
    toJSON: () => ({}),
  } as DOMRect)
}

describe('TerminalTab 建连时重申尺寸', () => {
  beforeEach(() => stubLayout(800, 400))
  afterEach(() => vi.restoreAllMocks())

  // attachOf 取出本次 connectPty 收到的 onAttached 回调。
  function attachOf(): (info: { since: number; truncated: boolean }) => void {
    const opts = connectPty.mock.calls[0][0] as {
      onAttached: (info: { since: number; truncated: boolean }) => void
    }
    return opts.onAttached
  }

  it('恢复已有会话时，attached 帧到达后立刻上报当前尺寸', async () => {
    const resize = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize })

    render(<TerminalTab base={WS} seq={1} sessionId="S1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalledTimes(1))
    // 不走建会话那条路，所以尺寸只可能由这次重申发出去
    expect(createPtySession).not.toHaveBeenCalled()
    expect(resize).not.toHaveBeenCalled()

    attachOf()({ since: 0, truncated: false })
    expect(resize).toHaveBeenCalledWith(termInstance.cols, termInstance.rows)
  })

  it('每次重连都重申一次——断线期间别的订阅者可能把尺寸协商成了别的值', async () => {
    const resize = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize })

    render(<TerminalTab base={WS} seq={1} sessionId="S1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalledTimes(1))

    const onAttached = attachOf()
    onAttached({ since: 0, truncated: false })
    onAttached({ since: 4096, truncated: true })
    expect(resize).toHaveBeenCalledTimes(2)
  })

  // 这条是承重的：2026-08-20 真机走查里，刷新页面后终端里的历史整屏消失，
  // 取证日志显示 attach 那一刻上报的是 `cols: 2, rows: 1` —— 容器还没拿到布局
  // 盒子，FitAddon 量出了 xterm 的下限，而这个尺寸被当真报给了服务端，
  // PTY 真被调成 2 列 1 行，shell 按 2 列重排把回放的历史绞碎了。
  it('容器还没有布局盒子时一个字节都不上报——那一刻量出的 2×1 会把 PTY 绞成 2 列', async () => {
    stubLayout(0, 0)
    const resize = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize })

    render(<TerminalTab base={WS} seq={1} sessionId="S1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalledTimes(1))

    attachOf()({ since: 0, truncated: false })
    expect(resize).not.toHaveBeenCalled()
  })

  // 跳过那一次不能就这么算了：恢复布局时 fit 量出的尺寸常常正好等于 xterm
  // 当前值，onResize 因此根本不触发——只靠它兜底就又回到「整条挂载路径一次
  // 尺寸都没报过」，也就是 TUI 乱码那个老毛病。
  it('容器成型后补上那次欠下的重申——尺寸没变时 onResize 不会自己发', async () => {
    stubLayout(0, 0)
    const resize = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize })

    render(<TerminalTab base={WS} seq={1} sessionId="S1" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalledTimes(1))
    attachOf()({ since: 0, truncated: false })
    expect(resize).not.toHaveBeenCalled()

    // 容器拿到布局盒子，观察者被唤醒
    stubLayout(800, 400)
    for (const cb of roCallbacks) cb()
    expect(resize).toHaveBeenCalledWith(termInstance.cols, termInstance.rows)

    // 只补一次，别每次容器尺寸变动都重发
    for (const cb of roCallbacks) cb()
    expect(resize).toHaveBeenCalledTimes(1)
  })

  it('新建会话那条路也重申一次（建会话与建连之间容器可能已经变了）', async () => {
    const resize = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize })

    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalledTimes(1))
    attachOf()({ since: 0, truncated: false })
    expect(resize).toHaveBeenCalledWith(termInstance.cols, termInstance.rows)
  })
})
