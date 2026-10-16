// CoordinatorPanel.test.tsx —— 锁定协调者三态、服务端命令确认和交回接缝，以及 B434 桌面端坐席入口
// （「坐下」按钮契约条 223–242、拉起器按钮契约条 203–210）。
// 边界：每次生命周期动作都从公开按钮进入；终端真正打开由 Workbench/Shell 接缝负责。
// B434：拉起材料、编码、薄壳判定与桥、剪贴板一律 vi.mock 注入 spy（契约 §4.14 断言方式）；
// 编码函数自身的行为由 claudeDeepLink.test.ts 锁，这里只锁面板怎么用它。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MockInstance } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '../../api/client'
import { attachCoordinator, getCoordinatorStatus, getDesktopLaunchMaterial, launchCoordinator, openViaDesktopLauncher, releaseCoordinator } from '../../api/scheduling'
import type { CoordinatorStatus, DesktopLaunchMaterial } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { buildClaudeNewSessionURL } from '../lib/claudeDeepLink'
import { copyToClipboard } from '../lib/clipboard'
import { isDesktopShell, requestOpenDeepLink } from '../lib/desktopShell'
import { useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { CoordinatorPanel } from './CoordinatorPanel'
import panelSource from './CoordinatorPanel.tsx?raw'
import { DESKTOP_COPY_BRIEF_BUTTON, DESKTOP_COPY_BRIEF_HINT, DESKTOP_SEAT_BUTTON, DESKTOP_TRUST_HINT } from './desktopSeatCopy'

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
const createPtySession = vi.fn()
const connectPty = vi.fn()

vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn(function () { return termInstance }) }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(function () { return { fit: vi.fn() } }) }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return { onContextLoss: vi.fn(), dispose: vi.fn() } }) }))
// why 展开 importOriginal：B434 用例要用真 ApiError 构造 409（与 request 抛出的同型）。
vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  createPtySession: (...args: unknown[]) => createPtySession(...args),
  deletePtySession: vi.fn(),
}))
vi.mock('../../api/pty', () => ({ connectPty: (...args: unknown[]) => connectPty(...args) }))

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  attachCoordinator: vi.fn(),
  getCoordinatorStatus: vi.fn(),
  getDesktopLaunchMaterial: vi.fn(),
  launchCoordinator: vi.fn(),
  openViaDesktopLauncher: vi.fn(),
  rebindCoordinatorLaunch: vi.fn(),
  releaseCoordinator: vi.fn(),
}))
vi.mock('../lib/claudeDeepLink', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/claudeDeepLink')>()),
  buildClaudeNewSessionURL: vi.fn(),
}))
vi.mock('../lib/desktopShell', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/desktopShell')>()),
  isDesktopShell: vi.fn(),
  requestOpenDeepLink: vi.fn(),
}))
vi.mock('../lib/clipboard', () => ({ copyToClipboard: vi.fn() }))

vi.mock('../data/usePoll', () => ({ usePoll: vi.fn() }))

const refresh = vi.fn()
const unbound: CoordinatorStatus = { bound: false, attach_active: false, attach: null }
const info = { machine: '', dir: '/repo/handoff', command: 'opencode --session sess-coord' }
const coordinatorBase: BaseDir = {
  key: '/repo/handoff', kind: 'workspace', path: '/repo/handoff', label: 'handoff', projectName: 'handoff', machine: '',
}
// NO_LOCAL_PROJECT 是 G1 材料端点 409 的 error 原文形状（契约 §3.5：errNoLocalProject 包装文本）。
const NO_LOCAL_PROJECT = '项目在本机没有登记位置：handoff（handoff project add 登记后可用）'
// material 是 G1 材料端点 200 体（契约 §3.2 DesktopLaunchMaterial；launcher 缺键 = 未登记拉起器）。
const material: DesktopLaunchMaterial = {
  card: 'B1',
  folder: '/repo/handoff',
  brief: '[handoff] 你是卡 B1 的协调者，坐在这场 Claude 桌面端会话里。\n\n- 卡号：B1',
}
const ENCODED = 'claude://code/new?folder=%2Frepo%2Fhandoff&q=%5Bhandoff%5D'

function pollState(data: CoordinatorStatus | null, over: Partial<{ disconnected: boolean; sessionExpired: boolean; errorText: string }> = {}) {
  return { data, disconnected: false, sessionExpired: false, errorText: '', refresh, ...over }
}

const seatButton = () => screen.getByRole('button', { name: DESKTOP_SEAT_BUTTON })

// renderReady 渲染空座面板并等材料落定：按钮由禁用（读取中）变可用才算就绪，
// 避免把读取中的禁用误当成断言结果（缺陷族·假绿）。
async function renderReady(cardId = 'B1') {
  const view = render(<CoordinatorPanel cardId={cardId} onOpenTerminal={vi.fn()} />)
  await waitFor(() => expect(seatButton()).toBeEnabled())
  return view
}

// renderUnavailable 渲染材料 409 的空座面板，并以 title 出现为落定信号（读取中同样禁用，不能只等禁用）。
async function renderUnavailable(reason = NO_LOCAL_PROJECT) {
  vi.mocked(getDesktopLaunchMaterial).mockRejectedValue(new ApiError(409, reason, { error: reason }))
  const view = render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
  await waitFor(() => expect(seatButton()).toHaveAttribute('title', reason))
  return view
}

describe('协调者面板', () => {
  let anchorClick: MockInstance<() => void>

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(usePoll).mockReturnValue(pollState(unbound) as never)
    vi.mocked(getCoordinatorStatus).mockResolvedValue(unbound)
    vi.mocked(launchCoordinator).mockResolvedValue({ woke: true, rebuilt: false, escalated: false, output: '协调者已拉起' })
    vi.mocked(attachCoordinator).mockResolvedValue(info)
    vi.mocked(releaseCoordinator).mockResolvedValue({ ok: true })
    vi.mocked(getDesktopLaunchMaterial).mockResolvedValue(material)
    vi.mocked(openViaDesktopLauncher).mockResolvedValue({ launcher: 'local_launcher-1', machine: '', handled_by: 'mac-1' })
    vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: true, url: ENCODED })
    vi.mocked(isDesktopShell).mockReturnValue(false)
    vi.mocked(requestOpenDeepLink).mockReturnValue(true)
    vi.mocked(copyToClipboard).mockResolvedValue(true)
    // why：jsdom 不实现 claude:// 导航（打印 Not implemented）；拦下 click 既消噪，又能断言「原生导航已触发」
    anchorClick = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    createPtySession.mockResolvedValue({ id: 'pty-coordinator' })
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize: vi.fn() })
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  })

  afterEach(() => {
    anchorClick.mockRestore()
  })

  it('maps unbound, bound, and attach-active status to the three prototype states', async () => {
    // why 挂起材料：本用例按调用次序给 usePoll 喂两态，材料落定引起的额外渲染会吃掉第二个 Once 值
    vi.mocked(getDesktopLaunchMaterial).mockReturnValue(new Promise(() => {}))
    vi.mocked(usePoll)
      .mockReturnValueOnce(pollState(unbound) as never)
      .mockReturnValueOnce(pollState({ bound: true, attach_active: false, attach: info }) as never)
    const { rerender } = render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('未绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '▶ 叫机器人' })).toBeVisible()
    rerender(<CoordinatorPanel cardId="B2" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('已绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '打开终端' })).toBeVisible()
  })

  it('confirms service command and workdir, then attaches without rewriting command', async () => {
    const onOpenTerminal = vi.fn()
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: info }) as never)
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={onOpenTerminal} />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '打开终端' }))
    expect(screen.getByText(/attach 与自动唤醒互斥/)).toBeVisible()
    expect(screen.getByText('/repo/handoff')).toBeVisible()
    expect(screen.getByText('opencode --session sess-coord')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '确认 attach' }))
    expect(attachCoordinator).toHaveBeenCalledWith('B1', '/repo/handoff')
    expect(onOpenTerminal).toHaveBeenCalledWith(info)
  })

  it('bind 席位没有机器人 attach 时仍显示已绑定和换绑，不显示打开终端', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: null }) as never)
    render(<CoordinatorPanel cardId="B-bind" onOpenTerminal={vi.fn()} />)

    expect(await screen.findByText('已绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '换绑：叫机器人' })).toBeVisible()
    expect(screen.queryByRole('button', { name: '打开终端' })).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('把 AttachInfo.command 穿透到终端 initCommand', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: info }) as never)
    function CoordinatorTerminalBridge() {
      const workbench = useWorkbench()
      const tab = workbench.wb.groups[0].columns[0]?.panes[0]
      const command = tab?.content.kind === 'terminal' ? tab.content.initCommand ?? '' : ''
      return <>
        <CoordinatorPanel cardId="B1" onOpenTerminal={(attached) => workbench.openTerminalWithCommand(attached.command, coordinatorBase)} />
        <div data-testid="init-command">{command}</div>
      </>
    }

    render(<CoordinatorTerminalBridge />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '打开终端' }))
    await user.click(screen.getByRole('button', { name: '确认 attach' }))
    await waitFor(() => expect(screen.getByTestId('init-command')).toHaveTextContent(info.command))
  })

  it('releases attach and exposes errors instead of pretending success', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: true, attach: info }) as never)
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    const user = userEvent.setup()
    expect(await screen.findByText('人工接管中')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '交回无头' }))
    expect(releaseCoordinator).toHaveBeenCalledWith('B1')
    await waitFor(() => expect(refresh).toHaveBeenCalled())
  })

  it('release 返回 ok=false 时保留人工接管态并显示错误', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: true, attach: info }) as never)
    vi.mocked(releaseCoordinator).mockResolvedValue({ ok: false })
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '交回无头' }))
    expect(await screen.findByText('交回无头失败：服务端未确认释放')).toBeVisible()
    expect(screen.getByText('人工接管中')).toBeVisible()
    expect(refresh).not.toHaveBeenCalled()
  })

  // ---- B434 「用 Claude Code 坐下」（契约 §4.14 条 223–242） ----

  it('空座渲染用 Claude Code 坐下', async () => {
    await renderReady()
    expect(seatButton()).toBeVisible()
    expect(getDesktopLaunchMaterial).toHaveBeenCalledTimes(1)
    expect(getDesktopLaunchMaterial).toHaveBeenCalledWith('B1')
  })

  it('已绑定不渲染坐下按钮', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: null }) as never)
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('已绑定')).toBeVisible()
    expect(screen.queryByRole('button', { name: DESKTOP_SEAT_BUTTON })).not.toBeInTheDocument()
    expect(getDesktopLaunchMaterial).not.toHaveBeenCalled()
  })

  it('材料 409 坐下按钮禁用', async () => {
    await renderUnavailable()
    expect(seatButton()).toBeDisabled()
  })

  it('材料 409 按钮 title 逐字取服务端 error', async () => {
    await renderUnavailable()
    expect(seatButton().getAttribute('title')).toBe(NO_LOCAL_PROJECT)
  })

  it('材料 409 说明文字逐字取服务端 error', async () => {
    await renderUnavailable()
    expect(screen.getByText(NO_LOCAL_PROJECT)).toBeVisible()
  })

  it('材料 409 点击不编码', async () => {
    await renderUnavailable()
    await userEvent.setup().click(seatButton())
    expect(buildClaudeNewSessionURL).not.toHaveBeenCalled()
    expect(requestOpenDeepLink).not.toHaveBeenCalled()
  })

  it('点击以材料 folder 与 brief 编码', async () => {
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(buildClaudeNewSessionURL).toHaveBeenCalledTimes(1)
    expect(buildClaudeNewSessionURL).toHaveBeenCalledWith(material.folder, material.brief)
  })

  it('薄壳内经 requestOpenDeepLink 打开', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(requestOpenDeepLink).toHaveBeenCalledTimes(1)
    expect(requestOpenDeepLink).toHaveBeenCalledWith(ENCODED)
    expect(anchorClick).not.toHaveBeenCalled()
  })

  it('薄壳桥可用不展开复制简报', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(screen.queryByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).not.toBeInTheDocument()
    expect(screen.queryByText(DESKTOP_COPY_BRIEF_HINT)).not.toBeInTheDocument()
  })

  it('浏览器内以 a href 打开', async () => {
    const { container } = await renderReady()
    await userEvent.setup().click(seatButton())
    const anchors = container.querySelectorAll('a[href^="claude:"]')
    expect(anchors).toHaveLength(1)
    expect(anchors[0].getAttribute('href')).toBe(ENCODED)
    expect(anchorClick).toHaveBeenCalledTimes(1)
  })

  it('浏览器内不调桥', async () => {
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(requestOpenDeepLink).not.toHaveBeenCalled()
  })

  // 编码拒绝三支在薄壳形态下跑：浏览器形态复制兜底常驻（见下方「浏览器内材料就绪即常驻复制简报」），
  // 在那里断言「展开」会先绿后绿，证明不了是拒绝触发的展开（缺陷族·假绿）。
  it('编码拒绝展开复制简报', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    for (const reason of ['folder_not_absolute', 'brief_empty', 'brief_too_long', 'not_implemented'] as const) {
      vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: false, reason })
      const view = await renderReady()
      expect(screen.queryByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON }), reason).not.toBeInTheDocument()
      await userEvent.setup().click(seatButton())
      expect(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON }), reason).toBeVisible()
      view.unmount()
    }
  })

  it('编码拒绝显示粘贴提示', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: false, reason: 'brief_too_long' })
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(screen.getByText(DESKTOP_COPY_BRIEF_HINT)).toBeVisible()
  })

  it('编码拒绝不尝试打开', async () => {
    vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: false, reason: 'brief_too_long' })
    vi.mocked(isDesktopShell).mockReturnValue(true)
    const shell = await renderReady()
    await userEvent.setup().click(seatButton())
    expect(requestOpenDeepLink).not.toHaveBeenCalled()
    expect(shell.container.querySelector('a[href^="claude:"]')).toBeNull()
    shell.unmount()

    vi.mocked(isDesktopShell).mockReturnValue(false)
    const browser = await renderReady()
    await userEvent.setup().click(seatButton())
    expect(browser.container.querySelector('a[href^="claude:"]')).toBeNull()
    expect(anchorClick).not.toHaveBeenCalled()
  })

  it('无桥展开复制简报', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    vi.mocked(requestOpenDeepLink).mockReturnValue(false)
    await renderReady()
    expect(screen.queryByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).not.toBeInTheDocument()
    await userEvent.setup().click(seatButton())
    expect(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).toBeVisible()
    expect(screen.getByText(DESKTOP_COPY_BRIEF_HINT)).toBeVisible()
  })

  it('复制简报逐字复制材料简报', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    vi.mocked(requestOpenDeepLink).mockReturnValue(false)
    await renderReady()
    const user = userEvent.setup()
    await user.click(seatButton())
    await user.click(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON }))
    expect(copyToClipboard).toHaveBeenCalledTimes(1)
    expect(copyToClipboard).toHaveBeenCalledWith(material.brief)
  })

  it('打开后提示信任文件夹', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    await renderReady()
    expect(screen.queryByText(DESKTOP_TRUST_HINT)).not.toBeInTheDocument()
    await userEvent.setup().click(seatButton())
    expect(screen.getByText(DESKTOP_TRUST_HINT)).toBeVisible()
  })

  it('浏览器形态打开后同样提示信任文件夹', async () => {
    await renderReady()
    expect(screen.queryByText(DESKTOP_TRUST_HINT)).not.toBeInTheDocument()
    await userEvent.setup().click(seatButton())
    expect(screen.getByText(DESKTOP_TRUST_HINT)).toBeVisible()
  })

  it('信任提示不叠加不持久化', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    try {
      vi.mocked(isDesktopShell).mockReturnValue(true)
      await renderReady()
      const user = userEvent.setup()
      await user.click(seatButton())
      await user.click(seatButton())
      await user.click(seatButton())
      expect(requestOpenDeepLink).toHaveBeenCalledTimes(3)
      expect(screen.getAllByText(DESKTOP_TRUST_HINT)).toHaveLength(1)
      expect(setItem).not.toHaveBeenCalled()
    } finally {
      setItem.mockRestore()
    }
  })

  it('未打开不提示信任', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: false, reason: 'brief_too_long' })
    const rejected = await renderReady()
    await userEvent.setup().click(seatButton())
    expect(screen.queryByText(DESKTOP_TRUST_HINT)).not.toBeInTheDocument()
    rejected.unmount()

    vi.mocked(buildClaudeNewSessionURL).mockReturnValue({ ok: true, url: ENCODED })
    vi.mocked(requestOpenDeepLink).mockReturnValue(false)
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(screen.queryByText(DESKTOP_TRUST_HINT)).not.toBeInTheDocument()
  })

  it('坐下文案只取常量', () => {
    for (const copy of [DESKTOP_SEAT_BUTTON, DESKTOP_COPY_BRIEF_BUTTON, DESKTOP_COPY_BRIEF_HINT, DESKTOP_TRUST_HINT]) {
      expect(panelSource, copy).not.toContain(copy)
    }
    expect(panelSource).toContain("from './desktopSeatCopy'")
  })

  // ---- B434 plan 定（breakdown §5.6 静默失败/跨平台）：不在契约条目内，附加断言 ----

  it('浏览器内材料就绪即常驻复制简报', async () => {
    await renderReady()
    expect(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).toBeVisible()
    expect(screen.getByText(DESKTOP_COPY_BRIEF_HINT)).toBeVisible()
    await userEvent.setup().click(seatButton())
    expect(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).toBeVisible()
  })

  it('编码抛错时退到复制简报而不是静默无反应', async () => {
    vi.mocked(isDesktopShell).mockReturnValue(true)
    vi.mocked(buildClaudeNewSessionURL).mockImplementation(() => { throw new URIError('URI malformed') })
    await renderReady()
    await userEvent.setup().click(seatButton())
    expect(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON })).toBeVisible()
    expect(requestOpenDeepLink).not.toHaveBeenCalled()
    expect(screen.queryByText(DESKTOP_TRUST_HINT)).not.toBeInTheDocument()
  })

  it('复制失败时在 alert 段说明', async () => {
    vi.mocked(copyToClipboard).mockResolvedValue(false)
    await renderReady()
    await userEvent.setup().click(screen.getByRole('button', { name: DESKTOP_COPY_BRIEF_BUTTON }))
    expect(await screen.findByRole('alert')).toHaveTextContent('剪贴板')
  })
})
