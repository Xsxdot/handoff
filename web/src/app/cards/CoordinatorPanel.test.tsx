// CoordinatorPanel.test.tsx —— 锁定协调者三态、服务端命令确认和交回接缝。
// 边界：每次生命周期动作都从公开按钮进入；终端真正打开由 Workbench/Shell 接缝负责。
// B435（D10、契约 §7 退役）：B434 桌面端坐席三按钮与拉起器客户端已退役——空座态只剩
// 「▶ 叫机器人」；反面用例以退役文案字面量查询「不出现」（常量已随删，这是唯一合法的另写一份），
// 源码守卫（?raw）锁住面板、抽屉与调度客户端不再引用退役面。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachCoordinator, getCoordinatorStatus, launchCoordinator, releaseCoordinator } from '../../api/scheduling'
import type { CoordinatorStatus } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { CoordinatorPanel } from './CoordinatorPanel'
import panelSource from './CoordinatorPanel.tsx?raw'
import drawerSource from './CardDrawer.tsx?raw'
import schedulingSource from '../../api/scheduling.ts?raw'

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
  launchCoordinator: vi.fn(),
  rebindCoordinatorLaunch: vi.fn(),
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

  // ---- B435 退役（D10、契约 §7；S7 控制台面） ----

  it('空座态只见「▶ 叫机器人」，B434 三按钮与复制提示都不出现', async () => {
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('未绑定')).toBeVisible()
    expect(screen.getByRole('button', { name: '▶ 叫机器人' })).toBeVisible()
    expect(screen.getAllByRole('button')).toHaveLength(1)
    for (const retired of ['用 Claude Code 坐下', '复制简报', '让拉起器代开']) {
      expect(screen.queryByRole('button', { name: retired })).toBeNull()
      expect(screen.queryByText(retired)).toBeNull()
    }
    expect(screen.queryByText('到 Claude 桌面端新建会话后粘贴')).toBeNull()
    expect(screen.queryByText('如弹出信任文件夹，点信任，再按发送')).toBeNull()
  })

  it('已绑定态同样不出现退役按钮', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ bound: true, attach_active: false, attach: info }) as never)
    render(<CoordinatorPanel cardId="B1" onOpenTerminal={vi.fn()} />)
    expect(await screen.findByText('已绑定')).toBeVisible()
    for (const retired of ['用 Claude Code 坐下', '复制简报', '让拉起器代开']) {
      expect(screen.queryByRole('button', { name: retired })).toBeNull()
    }
  })

  // 退役面标记以分段拼接给出：plan 段 6 的独立判据是「web/src 里 grep 退役标记零命中」，
  // 守卫自身若写整串就会被 grep 命中——拼接让守卫与 grep 两道判据同时成立。
  const mark = (...parts: string[]) => parts.join('')

  it('调度客户端不再导出 B434 拉起器两函数', async () => {
    const scheduling = await vi.importActual<Record<string, unknown>>('../../api/scheduling')
    expect(scheduling).not.toHaveProperty(mark('getDesktop', 'LaunchMaterial'))
    expect(scheduling).not.toHaveProperty(mark('openVia', 'DesktopLauncher'))
  })

  it('源码守卫：面板、抽屉与调度客户端不引用退役面', () => {
    const retiredMarks = [
      mark('desktop', '-launch'), mark('claude://', 'code/new'), mark('desktop', 'SeatCopy'), mark('claude', 'DeepLink'),
      mark('requestOpen', 'DeepLink'), mark('getDesktop', 'LaunchMaterial'), mark('openVia', 'DesktopLauncher'),
      mark('Desktop', 'LaunchMaterial'), mark('Desktop', 'LauncherOpenResp'),
    ]
    for (const [name, source] of [['CoordinatorPanel.tsx', panelSource], ['CardDrawer.tsx', drawerSource], ['scheduling.ts', schedulingSource]] as const) {
      for (const mark of retiredMarks) {
        expect(source.includes(mark), `${name} 含 ${mark}`).toBe(false)
      }
    }
  })
})
