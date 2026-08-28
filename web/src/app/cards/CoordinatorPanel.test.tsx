// CoordinatorPanel.test.tsx —— 锁定协调者三态、服务端命令确认和交回接缝。
// 边界：每次生命周期动作都从公开按钮进入；终端真正打开由 Workbench/Shell 接缝负责。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachCoordinator, getCoordinatorStatus, launchCoordinator, releaseCoordinator } from '../../api/scheduling'
import type { CoordinatorStatus } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { TerminalTab } from '../workbench/TerminalTab'
import { useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { CoordinatorPanel } from './CoordinatorPanel'

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
vi.mock('../../api/client', () => ({
  createPtySession: (...args: unknown[]) => createPtySession(...args),
  deletePtySession: vi.fn(),
}))
vi.mock('../../api/pty', () => ({ connectPty: (...args: unknown[]) => connectPty(...args) }))

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  attachCoordinator: vi.fn(),
  getCoordinatorStatus: vi.fn(),
  launchCoordinator: vi.fn(),
  releaseCoordinator: vi.fn(),
}))

vi.mock('../data/usePoll', () => ({ usePoll: vi.fn() }))

const refresh = vi.fn()
const unbound: CoordinatorStatus = { bound: false, attach_active: false, attach: null }
const info = { machine: '', dir: '/repo/handoff', command: 'opencode --session sess-coord' }
const coordinatorBase: BaseDir = {
  key: '/repo/handoff', kind: 'workspace', path: '/repo/handoff', label: 'handoff', projectName: 'handoff', machine: '',
}

function pollState(data: CoordinatorStatus | null, over: Partial<{ disconnected: boolean; sessionExpired: boolean; errorText: string }> = {}) {
  return { data, disconnected: false, sessionExpired: false, errorText: '', refresh, ...over }
}

describe('协调者面板', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(usePoll).mockReturnValue(pollState(unbound) as never)
    vi.mocked(getCoordinatorStatus).mockResolvedValue(unbound)
    vi.mocked(launchCoordinator).mockResolvedValue({ woke: true, rebuilt: false, escalated: false, output: '协调者已拉起' })
    vi.mocked(attachCoordinator).mockResolvedValue(info)
    vi.mocked(releaseCoordinator).mockResolvedValue({ ok: true })
    createPtySession.mockResolvedValue({ id: 'pty-coordinator' })
    connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize: vi.fn() })
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  })

  it('maps unbound, bound, and attach-active status to the three prototype states', async () => {
    vi.mocked(usePoll)
      .mockReturnValueOnce(pollState(unbound) as never)
      .mockReturnValueOnce(pollState({ bound: true, attach_active: false, attach: info }) as never)
    const { rerender } = render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('未绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '▶ 拉起协调者' })).toBeVisible()
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

  it('把 AttachInfo.command 穿透到终端 initCommand，再序列化为 init_command', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: info }) as never)
    function CoordinatorTerminalBridge() {
      const workbench = useWorkbench()
      const tab = workbench.wb.groups[0].tabs[0]
      const content = tab?.content
      return <>
        <CoordinatorPanel cardId="B1" onOpenTerminal={(attached) => workbench.openTerminalWithCommand(attached.command, coordinatorBase)} />
        {workbench.base && content?.kind === 'terminal' && <TerminalTab base={workbench.base} seq={content.seq} initCommand={content.initCommand} onSession={vi.fn()} />}
      </>
    }

    render(<CoordinatorTerminalBridge />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '打开终端' }))
    await user.click(screen.getByRole('button', { name: '确认 attach' }))
    await waitFor(() => expect(createPtySession).toHaveBeenCalledWith(
      expect.objectContaining({ init_command: info.command }),
      '',
    ))
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
})
