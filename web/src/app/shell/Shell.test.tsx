// Shell 的三栏外框行为测试。
//
// 数据侧全部经 vi.mock('../../api/client') 打桩，不发真实请求：任务流、项目树流、
// 文件树列举、任务详情与 diff 都按固定 fixture 返回。
//
// 断言的是「三栏之间怎么接」：选中目录 → 右栏出现 + 面包屑；点任务/文件 → 中央
// 开对应 tab；切目录 tab 组各自保持；分屏把中央切成两组；/settings 与 /machines
// 的路由行为；/tasks/:id 深链承接。
//
// 注意：本文件依赖 Task 12-15 的组件（BoardOverlay / TicketsOverlay /
// useGlobalTickets / SettingsPage），在那些任务落地前无法运行，属预期的全期红。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppRoutes } from '../../App'
import type { ProjectTreeResp, Task } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchProjectTree: vi.fn(),
    fetchWorkspaceDir: vi.fn(),
    fetchWorkspaceFile: vi.fn(),
    fetchTaskDetail: vi.fn(),
    fetchTaskDiff: vi.fn(),
    fetchPtySessions: vi.fn(),
    fetchMachines: vi.fn(),
    deletePtySession: vi.fn(),
    createPtySession: vi.fn(),
  }
})
const { fetchTasks, fetchProjectTree, fetchWorkspaceDir, fetchWorkspaceFile, fetchTaskDetail, fetchTaskDiff, fetchPtySessions, fetchMachines, deletePtySession, createPtySession } = await import('../../api/client')
// xterm 要量真实字体尺寸，jsdom 给不了。整体替身（照 TerminalTab.test.tsx）：
// 点「新终端」后 HomeDock 会挂出 TerminalTab，真实 xterm 在 jsdom 里会抛异常
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
vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn(function () { return termInstance }) }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(function () { return { fit: vi.fn() } }) }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return { onContextLoss: vi.fn(), dispose: vi.fn() } }) }))

const connectPty = vi.fn()
vi.mock('../../api/pty', () => ({ connectPty: (...a: unknown[]) => connectPty(...a) }))

// T1 挂在 /w/b2-b3 这个工作树上（project_id 'p1'、本机、running）。
const t1: Task = {
  id: 'T1',
  target: '',
  repo_path: '/w/b2-b3',
  branch: 'integration/b2-b3',
  plan_path: '',
  plan_summary: '重构工单通道',
  executor_session: '',
  state: 'running',
  created_at: '2026-08-12T10:00:00+08:00',
  updated_at: '2026-08-12T10:00:00+08:00',
  name: '重构工单通道',
  executor: 'opencode',
  model: '',
  work_dir: '/w/b2-b3',
  worktree_managed: true,
  base_commit: '',
  base_ahead: 0,
  repo_dirty_count: 0,
  repo_dirty_files: '',
  done_note: '',
  machine: '',
  project_id: 'p1',
}

// T2 与 T1 同目录，但卡在 waiting_answer：挂了一张工单，用于「跳到该任务」用例。
const t2: Task = {
  id: 'T2',
  target: '',
  repo_path: '/w/b2-b3',
  branch: 'integration/b2-b3',
  plan_path: '',
  plan_summary: '等你批',
  executor_session: '',
  state: 'waiting_answer',
  created_at: '2026-08-12T10:00:00+08:00',
  updated_at: '2026-08-12T10:00:00+08:00',
  name: '等你批',
  executor: 'opencode',
  model: '',
  work_dir: '/w/b2-b3',
  worktree_managed: true,
  base_commit: '',
  base_ahead: 0,
  repo_dirty_count: 0,
  repo_dirty_files: '',
  done_note: '',
  machine: '',
  project_id: 'p1',
}

// 树 fixture：一个项目 handoff（project_id 'p1'）、一台本机、两个目录。
// 主目录的 branch 刻意设成「主目录」——dirLabel 优先取 branch，这样测试里
// getByText('主目录') 能命中目录行。
const tree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'p1',
      origin_url: '',
      name: 'handoff',
      locations: [
        {
          machine: '',
          name: 'handoff',
          path: '/r/handoff',
          workspaces: [
            { path: '/r/handoff', branch: '主目录', head: 'abc1234', is_main: true, managed: false, created_at: '' },
            { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc1234', is_main: false, managed: true, created_at: '' },
          ],
          probe_error: '',
        },
      ],
    },
  ],
  unowned: [],
}

beforeAll(() => {
  // jsdom 没有 ResizeObserver，而 home 浮窗里的 TerminalTab 用它跟随容器尺寸
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})

beforeEach(() => {
  vi.mocked(fetchTasks).mockResolvedValue([t1])
  vi.mocked(fetchProjectTree).mockResolvedValue(tree)
  vi.mocked(fetchWorkspaceDir).mockResolvedValue({ entries: [{ name: 'go.mod', is_dir: false, size: 5 }] })
  // 文件内容要可编辑：sha256 有值才进 textarea 分支（FileTab 的三态判据）。
  // 没 mock 的话 FileTab 会发真实请求，测试环境里直接炸
  vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'module handoff\n', size: 15, sha256: 'h1' })
  vi.mocked(fetchTaskDetail).mockResolvedValue({
    task: t1,
    pending_tickets: [],
    recent_events: [],
  })
  vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: '' })
  vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
  // 本机上报支持 PTY：能力门在既有用例里必须是「放行」，否则一堆无关用例
  // 会因为终端项被收起而失败。Machine 其余字段按 /api/machines 契约给全，
  // 否则 /settings 里的 MachineDetail 会在 machine.executors 上崩
  vi.mocked(fetchMachines).mockResolvedValue({
    machines: [
      {
        name: '',
        addr: '',
        reachable: true,
        version: '',
        executors: [],
        default_executor: '',
        probe_ms: 0,
        active_tasks: 0,
        error: '',
        pty_supported: true,
      },
    ],
  })
  vi.mocked(deletePtySession).mockResolvedValue({ ok: true })
  // 建会话成功：home 浮窗里 TerminalTab 挂载后靠它拿到 sessionId 回报给 dock
  vi.mocked(createPtySession).mockResolvedValue({
    id: 'new-1', machine: '', base_path: '~', base_kind: 'home', shell: '',
    created_at: '', cols: 100, rows: 30, attached: 0, pid: 0,
    foreground: false, bytes_out: 0,
  })
  connectPty.mockReturnValue({ close: vi.fn(), send: vi.fn(), resize: vi.fn() })
})

function renderShell(path = '/') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AppRoutes />
    </MemoryRouter>,
  )
}

describe('Shell 三栏外框', () => {
  it('未选中目录时右栏文件树不渲染，中央是全局空态', async () => {
    renderShell()
    await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
    expect(screen.queryByText('文件')).not.toBeInTheDocument()
    expect(screen.getByText(/从侧边栏选择一个目录开始/)).toBeInTheDocument()
  })

  it('选中目录后右栏出现、面包屑显示 项目 / 开发机 / 目录', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    const crumb = screen.getByLabelText('当前位置')
    expect(crumb).toHaveTextContent('handoff')
    expect(crumb).toHaveTextContent('本机')
    expect(crumb).toHaveTextContent('integration/b2-b3')
  })

  it('点左栏任务在中央开 TUI tab', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /TUI · T1/ })).toBeInTheDocument())
  })

  it('点右栏文件在中央开 file tab', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(await screen.findByText('go.mod'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument())
  })

  it('切到另一个目录再切回来，两边的 tab 组各自保持', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('tab', { name: /go.mod/ })

    fireEvent.click(screen.getByText('主目录'))
    await waitFor(() => expect(screen.queryByRole('tab', { name: /go.mod/ })).not.toBeInTheDocument())

    fireEvent.click(screen.getByText('integration/b2-b3'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument())
  })

  it('面包屑的分屏按钮把中央分成两组', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(screen.getByRole('button', { name: '分屏' }))
    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(2))
  })

  it('连点两次分屏得到三栏，按钮随即 disabled', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(screen.getByRole('button', { name: '分屏' }))
    fireEvent.click(screen.getByRole('button', { name: '分屏' }))
    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(3))
    expect(screen.getByRole('button', { name: '分屏' })).toBeDisabled()
  })

  it('⌘D 分屏，并拦掉浏览器的「加入书签」', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))

    const ev = new KeyboardEvent('keydown', { key: 'd', metaKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(2))
    expect(ev.defaultPrevented).toBe(true)
  })

  it('Ctrl+D 不分屏：终端里那是 EOF，抢走会毁掉终端', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))

    const ev = new KeyboardEvent('keydown', { key: 'd', ctrlKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByRole('tablist')).toHaveLength(1))
    expect(ev.defaultPrevented).toBe(false)
  })

  it('/settings 整页替换中央，左栏仍在', async () => {
    renderShell('/settings')
    await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
    expect(screen.getByText('handoff')).toBeInTheDocument()
  })

  it('/machines 重定向到 /settings', async () => {
    renderShell('/machines')
    await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
  })

  it('/tasks/:id 深链选中目录、开 TUI tab 并换回 /', async () => {
    renderShell('/tasks/T1')
    await waitFor(() => expect(screen.getByRole('tab', { name: /TUI · T1/ })).toBeInTheDocument())
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('integration/b2-b3')
  })

  it('顶部 tab 条已删除', async () => {
    renderShell()
    await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
  })

  it('从工单弹层点「跳到该任务」切到该任务所在目录', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([t1, t2])
    vi.mocked(fetchTaskDetail).mockImplementation(async (id: string) => {
      if (id === 'T2') {
        return {
          task: t2,
          pending_tickets: [{ id: 'K1', kind: 'question', question: '要不要' }],
          recent_events: [],
        } as never
      }
      return { task: t1, pending_tickets: [], recent_events: [] }
    })
    renderShell()
    // 不点任务行（那会直接开 TUI tab），直接从左栏底部「工单」入口打开弹层。
    // 按钮在 ProjectTree 里，而树是异步拉取的——必须等它先出来再点（其余用例同款）
    fireEvent.click(await screen.findByRole('button', { name: /^工单$/ }))
    const jump = await screen.findByRole('button', { name: '跳到该任务' })
    fireEvent.click(jump)
    await waitFor(() => expect(screen.getByLabelText('当前位置')).toHaveTextContent('integration/b2-b3'))
  })

  it('home 终端不进中央 tab 条', async () => {
    renderShell()
    // 从悬浮入口新建一个 home 终端
    fireEvent.click(await screen.findByLabelText('home 基准终端'))
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    // 浮窗出现，内容渲染在浮窗里
    expect(screen.getByTestId('home-window-title')).toBeInTheDocument()
    // 中央 tab 条上不应出现它——home 终端不挂在任何目录上
    expect(screen.queryByRole('tab', { name: /home/ })).toBeNull()
  })

  it('恢复时 home 会话进浮窗、工作树会话进中央', async () => {
    vi.mocked(fetchPtySessions).mockResolvedValue({
      sessions: [
        { id: 's-home', base_kind: 'home', base_path: '~', machine: '', shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40, attached: 0, pid: 1, bytes_out: 0, foreground: false },
        { id: 's-ws', base_kind: 'workspace', base_path: '/repo/x', machine: '', shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40, attached: 0, pid: 2, bytes_out: 0, foreground: false },
      ],
    })
    renderShell()

    // home 那条：圆钮角标出现 1
    expect(await screen.findByTestId('home-badge')).toHaveTextContent('1')
    // 且浮窗没有被自动弹出——恢复是后台动作
    expect(screen.queryByTestId('home-window-title')).toBeNull()

    // 工作树那条：不该计进 home 角标
    expect(screen.getByTestId('home-badge')).not.toHaveTextContent('2')
  })

  it('对端不支持 PTY 时不渲染圆钮——说实话而不是给个死按钮', async () => {
    vi.mocked(fetchMachines).mockResolvedValue({
      machines: [{ name: '', addr: '', reachable: true, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '', pty_supported: false }],
    })
    renderShell()
    await waitFor(() => expect(screen.queryByLabelText('home 基准终端')).toBeNull())
  })
})

describe('关闭带草稿的文件 tab 要二次确认', () => {
  // 在同一个基准目录里切走再切回是造「带 draft 的 file tab」的唯一途径：draft
  // 只活在 FileTab 内部 state，卸载时才经 onDraftChange 回写进 tab 内容。UI 层面
  // 点 × 时内容还来不及拿到草稿（× 一触发 onBeforeClose 就被拦下，FileTab 没机会
  // 卸载）。
  //
  // 为什么切 tab 而不是切目录：wb.select 会同步改 baseRef.current，切目录时 FileTab
  // 的卸载回写会把草稿写进**新目录**的 workbench，草稿就丢了。同基准内点 + 开空白
  // tab 不碰 baseRef，回写目标才是对的（回写经 setTabContent 还会把 go.mod 重新设回
  // 激活项，正好省掉「点回去」这一步）
  it('关一个有草稿的文件 tab 会先弹确认，不直接关掉', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('tab', { name: /go.mod/ })

    // 等文件读出来、textarea 可用后打一行字，把「脏」造出来
    const ta = await screen.findByRole('textbox', { name: 'go.mod' })
    fireEvent.change(ta, { target: { value: 'module handoff\nx' } })

    // 点 + 开一个空白 tab：激活它让 FileTab 卸载回写草稿，内容就此带上 draft
    fireEvent.click(screen.getByRole('button', { name: '新建标签页' }))

    // 点 tab 条上的 ×：这次 tab.content 里已有 draft，应弹确认而不是直接关
    fireEvent.click(screen.getByRole('button', { name: '关闭 go.mod' }))
    expect(screen.getByRole('heading', { name: '关闭未保存的文件' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /go.mod/ })).toBeInTheDocument()

    // 确认后真的关掉
    fireEvent.click(screen.getByRole('button', { name: '不保存，关闭' }))
    await waitFor(() => expect(screen.queryByRole('tab', { name: /go.mod/ })).not.toBeInTheDocument())
  })

  it('干净的文件 tab 直接关，不打扰', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('integration/b2-b3'))
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('tab', { name: /go.mod/ })

    // 没打过字，内容没有 draft——× 应该直接关，连弹层都不出现
    fireEvent.click(screen.getByRole('button', { name: '关闭 go.mod' }))
    await waitFor(() => expect(screen.queryByRole('tab', { name: /go.mod/ })).not.toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: '关闭未保存的文件' })).not.toBeInTheDocument()
  })
})
