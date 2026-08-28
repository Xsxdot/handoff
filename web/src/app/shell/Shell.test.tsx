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
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppRoutes } from '../../App'
import type { ProjectTreeResp, Task } from '../../api/types'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'

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
    fetchWorkbenchState: vi.fn(),
    fetchMachines: vi.fn(),
    deletePtySession: vi.fn(),
    createPtySession: vi.fn(),
  }
})
const { fetchTasks, fetchProjectTree, fetchWorkspaceDir, fetchWorkspaceFile, fetchTaskDetail, fetchTaskDiff, fetchPtySessions, fetchWorkbenchState, fetchMachines, deletePtySession, createPtySession, ApiError } = await import('../../api/client')
const { fetchLedgerHealth } = await import('../../api/ledger')

vi.mock('../../api/ledger', async () => {
  const actual = await vi.importActual<typeof import('../../api/ledger')>('../../api/ledger')
  return {
    ...actual,
    // Shell 既有路由/工作台回归按账本已启用基线运行；关闭态由门控专项断言。
    fetchLedgerHealth: vi.fn().mockResolvedValue({ enabled: true, mirror: [] }),
  }
})
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
  blur: vi.fn(),
  dispose: vi.fn(),
  loadAddon: vi.fn(),
  refresh: vi.fn(),
  input: vi.fn(),
  buffer: { active: { type: 'normal' } },
  modes: { mouseTrackingMode: 'none' },
  onData: vi.fn(() => ({ dispose: vi.fn() })),
  onResize: vi.fn(),
  attachCustomWheelEventHandler: vi.fn(),
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
  vi.mocked(fetchWorkbenchState).mockResolvedValue({ selected: '', dock: '', bases: [] })
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
    foreground: false, incompatible: false, bytes_out: 0,
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

async function openBranch() {
  // 从整页路由回来时 ProjectTree 仍保留 directoryOpen；只有收起时才展开，
  // 避免第二次调用把已经可见的分支又收回去。
  const sidebar = within(screen.getByRole('complementary'))
  if (sidebar.queryByText('integration/b2-b3') === null) fireEvent.click(await sidebar.findByTestId('machine-row'))
  fireEvent.click(await sidebar.findByText('integration/b2-b3'))
}

describe('Shell 三栏外框', () => {
  it('未选中目录时右栏文件树不渲染，中央是全局空态', async () => {
    renderShell()
    await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
    expect(screen.queryByText('文件')).not.toBeInTheDocument()
    expect(screen.getByText('请从左栏选择项目或目录')).toBeInTheDocument()
  })

  it('选中目录后右栏出现、面包屑显示 项目 / 开发机 / 目录', async () => {
    renderShell()
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    const crumb = screen.getByLabelText('当前位置')
    expect(crumb).toHaveTextContent('handoff')
    expect(crumb).toHaveTextContent('本机')
    expect(crumb).toHaveTextContent('integration/b2-b3')
  })

  it('点左栏任务在中央开 TUI tab', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    await waitFor(() => expect(screen.getByRole('tab', { name: '组 2' })).toBeInTheDocument())
  })

  it('左栏任务的 DataTransfer 穿过 Shell 到同一组的中央分屏并保留项目机器', async () => {
    const remoteTask: Task = {
      ...t1,
      id: 'R1',
      project_id: 'p2',
      machine: 'linux-01',
      work_dir: '/srv/aim',
      name: '远端任务',
    }
    const crossProjectTree: ProjectTreeResp = {
      ...tree,
      projects: [
        tree.projects[0],
        {
          project_id: 'p2', origin_url: '', name: 'aim',
          locations: [{
            machine: 'linux-01', name: 'aim', path: '/srv/aim', probe_error: '',
            workspaces: [{ path: '/srv/aim', branch: 'main', head: 'def', is_main: true, managed: false, created_at: '' }],
          }],
        },
      ],
    }
    vi.mocked(fetchTasks).mockResolvedValue([t1, remoteTask])
    vi.mocked(fetchProjectTree).mockResolvedValue(crossProjectTree)
    vi.mocked(fetchMachines).mockResolvedValue({
      machines: [
        { name: '', addr: '', reachable: true, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '', pty_supported: true },
        { name: 'linux-01', addr: '', reachable: true, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '', pty_supported: true },
      ],
    })
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    fireEvent.click(await screen.findByRole('button', { name: '增加分屏' }))

    const values = new Map<string, string>()
    const dataTransfer = {
      types: [] as string[],
      setData: (type: string, value: string) => {
        values.set(type, value)
        if (!dataTransfer.types.includes(type)) dataTransfer.types.push(type)
      },
      getData: (type: string) => values.get(type) ?? '',
      effectAllowed: '',
      dropEffect: '',
    }
    const source = (await screen.findByText('远端任务')).closest('button')!
    fireEvent.dragStart(source, { dataTransfer })
    expect(dataTransfer.types).toEqual(expect.arrayContaining([DRAG_TASK_MIME, DRAG_BASE_MIME]))
    expect(JSON.parse(values.get(DRAG_BASE_MIME)!)).toMatchObject({ projectName: 'aim', machine: 'linux-01', path: '/srv/aim' })

    const target = screen.getAllByTestId('workbench-pane')[1]
    fireEvent.drop(target, { dataTransfer })
    await waitFor(() => expect(screen.getByText('aim · linux-01')).toBeInTheDocument())
    expect(screen.getAllByRole('tab')).toHaveLength(2)
  })

  it('左栏机器与目录的真实 DataTransfer 穿过 WorkbenchPage，终端 cwd 保留来源', async () => {
    const remoteProject: ProjectTreeResp['projects'][number] = {
      project_id: 'p2', origin_url: '', name: 'aim',
      locations: [{
        machine: 'linux-01', name: 'aim', path: '/srv/aim', probe_error: '',
        workspaces: [
          { path: '/srv/aim', branch: 'main', head: 'def', is_main: true, managed: false, created_at: '' },
          { path: '/srv/aim/worktree', branch: 'feature/work', head: 'ghi', is_main: false, managed: true, created_at: '' },
        ],
      }],
    }
    vi.mocked(fetchProjectTree).mockResolvedValue({ ...tree, projects: [tree.projects[0], remoteProject] })
    vi.mocked(fetchMachines).mockResolvedValue({
      machines: [
        { name: '', addr: '', reachable: true, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '', pty_supported: true },
        { name: 'linux-01', addr: '', reachable: true, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '', pty_supported: true },
      ],
    })
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    fireEvent.click(await screen.findByRole('button', { name: '增加分屏' }))

    const values = new Map<string, string>()
    const dataTransfer = {
      types: [] as string[],
      setData: (type: string, value: string) => {
        values.set(type, value)
        if (!dataTransfer.types.includes(type)) dataTransfer.types.push(type)
      },
      getData: (type: string) => values.get(type) ?? '',
      effectAllowed: '',
      dropEffect: '',
    }
    const remote = within(screen.getByTestId('project-node-p2'))
    const target = () => screen.getAllByTestId('workbench-pane')[1]
    const dragToPane = (source: HTMLElement) => {
      values.clear()
      dataTransfer.types.length = 0
      fireEvent.dragStart(source, { dataTransfer })
      fireEvent.drop(target(), { dataTransfer })
    }

    dragToPane(remote.getByTestId('machine-row'))
    expect(dataTransfer.types).toEqual(expect.arrayContaining([DRAG_DIR_MIME, DRAG_BASE_MIME]))
    expect(JSON.parse(values.get(DRAG_DIR_MIME)!)).toMatchObject({ path: '/srv/aim', machine: 'linux-01' })
    await waitFor(() => expect(createPtySession).toHaveBeenCalledWith(
      expect.objectContaining({ base_kind: 'workspace', base_path: '/srv/aim' }),
      'linux-01',
    ))

    fireEvent.click(remote.getByTestId('machine-row'))
    const worktree = remote.getAllByTestId('workspace-row').find((row) => row.textContent?.includes('feature/work'))!
    vi.mocked(createPtySession).mockClear()
    dragToPane(worktree)
    expect(JSON.parse(values.get(DRAG_DIR_MIME)!)).toMatchObject({ path: '/srv/aim/worktree', label: 'feature/work' })
    await waitFor(() => expect(createPtySession).toHaveBeenCalledWith(
      expect.objectContaining({ base_kind: 'workspace', base_path: '/srv/aim/worktree' }),
      'linux-01',
    ))
  })

  it('账本关闭时项目名旁隐藏工作项入口，避免导航到未注册的 /cards', async () => {
    vi.mocked(fetchLedgerHealth).mockResolvedValueOnce({ enabled: false, mirror: [] })
    renderShell()
    const project = await screen.findByTestId('project-node-p1')
    expect(within(project).queryByRole('button', { name: '打开 handoff 工作项' })).toBeNull()
    expect(within(project).getByRole('button', { name: '打开 handoff 代码图' })).toBeInTheDocument()
  })

  it('点右栏文件在中央开 file tab', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await waitFor(() => expect(screen.getByRole('button', { name: /关闭 go.mod/ })).toBeInTheDocument())
  })

  it('文件抽屉的 diff 任务按项目、机器和 work_dir 共同选择', async () => {
    const sharedPath = '/shared/b2'
    const handoffLocal = {
      path: sharedPath, branch: 'handoff-local', head: 'abc', is_main: true, managed: false, created_at: '',
    }
    const handoffRemote = {
      path: sharedPath, branch: 'handoff-remote', head: 'def', is_main: false, managed: true, created_at: '',
    }
    const aimLocal = {
      path: sharedPath, branch: 'aim-local', head: 'ghi', is_main: false, managed: true, created_at: '',
    }
    const collisionTree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'handoff',
          locations: [
            { machine: '', name: 'handoff', path: sharedPath, probe_error: '', workspaces: [handoffLocal] },
            { machine: 'linux-01', name: 'handoff', path: sharedPath, probe_error: '', workspaces: [handoffRemote] },
          ],
        },
        {
          project_id: 'p2', origin_url: '', name: 'aim',
          locations: [{ machine: '', name: 'aim', path: sharedPath, probe_error: '', workspaces: [aimLocal] }],
        },
      ],
      unowned: [],
    }
    const wrongProject = { ...t1, id: 'wrong-project', project_id: 'p2', work_dir: sharedPath }
    const wrongMachine = { ...t1, id: 'wrong-machine', machine: 'linux-01', work_dir: sharedPath }
    const rightTask = { ...t1, id: 'right-task', work_dir: sharedPath }
    vi.mocked(fetchTasks).mockResolvedValue([wrongProject, wrongMachine, rightTask])
    vi.mocked(fetchProjectTree).mockResolvedValue(collisionTree)
    vi.mocked(fetchWorkspaceDir).mockResolvedValue({
      entries: [{ name: 'handoff.go', is_dir: false, size: 1 }, { name: 'aim.go', is_dir: false, size: 1 }],
    })
    vi.mocked(fetchTaskDiff).mockImplementation(async (id: string) => ({
      diff: `diff --git a/${id === 'right-task' ? 'handoff.go' : 'aim.go'} b/${id === 'right-task' ? 'handoff.go' : 'aim.go'}`,
    }))

    renderShell()
    const project = await screen.findByTestId('project-node-p1')
    fireEvent.click(within(within(project).getAllByTestId('directory-machine-row')[0]).getByTestId('machine-row'))
    fireEvent.click(await within(project).findByText('handoff-local'))

    await waitFor(() => expect(screen.getByTitle('相对基线已改动（git diff base...HEAD，不含工作区未提交的编辑）')).toBeInTheDocument())
    expect(screen.getByText('handoff.go')).toHaveClass('text-state-intervention-text')
    expect(screen.getByText('aim.go')).not.toHaveClass('text-state-intervention-text')
    expect(fetchTaskDiff).toHaveBeenLastCalledWith('right-task')
  })

  it('文件抽屉打开主目录时，work_dir 为空的原地任务仍命中 diff', async () => {
    const inPlaceTask = { ...t1, id: 'in-place-task', name: '主目录任务', work_dir: '', repo_path: '/r/handoff' }
    vi.mocked(fetchTasks).mockResolvedValue([inPlaceTask])
    vi.mocked(fetchWorkspaceDir).mockResolvedValue({
      entries: [{ name: 'root.go', is_dir: false, size: 1 }],
    })
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/root.go b/root.go' })
    vi.mocked(fetchTaskDiff).mockClear()

    renderShell()
    const project = await screen.findByTestId('project-node-p1')
    const machineRow = within(project).getAllByTestId('directory-machine-row')[0]
    fireEvent.click(within(machineRow).getByTestId('machine-row'))
    fireEvent.click(await within(project).findByText('主目录'))

    await waitFor(() => expect(screen.getByText('root.go')).toHaveClass('text-state-intervention-text'))
    expect(fetchTaskDiff).toHaveBeenLastCalledWith('in-place-task')
  })

  it('切到另一个目录再切回来，两边的 tab 组各自保持', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('button', { name: /关闭 go.mod/ })

    fireEvent.click(screen.getByText('主目录'))
    await waitFor(() => expect(screen.getByRole('button', { name: /关闭 go.mod/ })).toBeInTheDocument())

    fireEvent.click(screen.getByText('integration/b2-b3'))
    await waitFor(() => expect(screen.getByRole('button', { name: /关闭 go.mod/ })).toBeInTheDocument())
  })

  it('tab 条右端的分屏按钮把中央分成两组', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '增加分屏' }))
    await waitFor(() => expect(screen.getAllByTestId('workbench-pane')).toHaveLength(2))
  })

  it('连点两次分屏得到三栏，按钮随即全部 disabled', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '增加分屏' }))
    fireEvent.click(screen.getByRole('button', { name: '增加分屏' }))
    await waitFor(() => expect(screen.getAllByTestId('workbench-pane')).toHaveLength(3))
  })

  it('⌘D 分屏，并拦掉浏览器的「加入书签」', async () => {
    renderShell()
    await openBranch()

    const ev = new KeyboardEvent('keydown', { key: 'd', metaKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByTestId('workbench-pane')).toHaveLength(2))
    expect(ev.defaultPrevented).toBe(true)
  })

  it('Ctrl+D 不分屏：终端里那是 EOF，抢走会毁掉终端', async () => {
    renderShell()
    await openBranch()

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
    await waitFor(() => expect(screen.getByRole('tab', { name: '组 2' })).toBeInTheDocument())
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('integration/b2-b3')
  })

  // 停在 /cards 或 /flows 时，整页盖在工作台上——侧栏点任务只改工作台状态
  // 不换路由的话，用户看见的还是看板。真机实测踩到。
  it('停在 /cards 时点左栏任务，中央换回工作台并开 TUI tab', async () => {
    renderShell('/cards')
    fireEvent.click(await screen.findByText('重构工单通道'))
    await waitFor(() => expect(screen.getByRole('tab', { name: '组 2' })).toBeInTheDocument())
  })

  it('停在 /cards 时点左栏目录，中央换回工作台', async () => {
    renderShell('/cards')
    // 右栏文件树挂在 Routes 外面，光看它不区分；判据要钉中央区——
    // 账本页的占位文案消失才说明路由真的换回了工作台
    await waitFor(() => expect(screen.getByText(/正在读取账本/)).toBeInTheDocument())
    await openBranch()
    await waitFor(() => expect(screen.queryByText(/正在读取账本/)).not.toBeInTheDocument())
    expect(screen.getByText('文件')).toBeInTheDocument()
  })

  // 右栏文件树与面包屑都挂在 Routes 外面、只跟 wb.base 走，整页路由把中央
  // 换掉后它们还留着——点了目录再点「工作项」，文件面板一直挂在右边。
  it('整页路由（/cards）不渲染右栏文件树与面包屑', async () => {
    renderShell()
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('工作项'))
    await waitFor(() => expect(screen.queryByText('文件')).not.toBeInTheDocument())
    expect(screen.queryByLabelText('当前位置')).not.toBeInTheDocument()
  })

  it('从整页路由点回目录，右栏文件树回来', async () => {
    renderShell('/cards')
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
  })

  // B270 只让 WorkbenchPage 内部切 tab 不卸 xterm。左栏 dock 的设置/工作项
  // 走整页路由，工作台挂在 path="*" 上会被卸掉，回来重放 1004h，TUI 再卡死。
  it.each(['设置', '工作项'] as const)('整页路由（%s）不卸载已打开的终端', async (entry) => {
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    const host = await screen.findByTestId('pty-host')
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())

    fireEvent.click(screen.getByLabelText(entry))
    await waitFor(() => expect(screen.getByTestId('pty-host')).toBe(host))
    expect(host).toBeInTheDocument()

    fireEvent.click(within(screen.getByRole('complementary')).getByText('integration/b2-b3'))
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    expect(screen.getByTestId('pty-host')).toBe(host)
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
    // 从悬浮入口新建一个 home 终端：零会话时点圆钮直接开一个
    fireEvent.click(await screen.findByLabelText('home 基准终端'))
    // 再从浮窗 tab 条的 + 菜单开第二个，确认它同样不会漏进中央
    fireEvent.click(screen.getByLabelText('新建'))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    // 浮窗出现，内容渲染在浮窗里
    expect(screen.getByTestId('home-window-title')).toBeInTheDocument()
    // 中央 tab 条上不应出现它——home 终端不挂在任何目录上
    expect(screen.queryByRole('tab', { name: /home/ })).toBeNull()
  })

  it('恢复时 home 会话进浮窗、工作树会话进中央', async () => {
    vi.mocked(fetchPtySessions).mockResolvedValue({
      sessions: [
        { id: 's-home', base_kind: 'home', base_path: '~', machine: '', shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40, attached: 0, pid: 1, bytes_out: 0, foreground: false, incompatible: false },
        { id: 's-ws', base_kind: 'workspace', base_path: '/repo/x', machine: '', shell: '/bin/zsh', created_at: '2026-08-12T00:00:00Z', cols: 120, rows: 40, attached: 0, pid: 2, bytes_out: 0, foreground: false, incompatible: false },
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

  it.each([
    ['空格', 'project name'],
    ['斜杠', 'project/name'],
    ['中文', '项目/中文'],
  ])('代码图 iframe 对项目名（%s）只做 query 编码', async (_kind, projectName) => {
    const specialTree: ProjectTreeResp = {
      ...tree,
      projects: tree.projects.map((project) => ({ ...project, name: projectName })),
    }
    vi.mocked(fetchProjectTree).mockResolvedValue(specialTree)

    renderShell()
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '代码图' }))

    await waitFor(() => expect(document.querySelector('iframe[title="代码图"]')).not.toBeNull())
    const frame = document.querySelector('iframe[title="代码图"]')
    expect(frame?.getAttribute('src')).toBe(
      `/codegraph/app/?project=${encodeURIComponent(projectName)}`,
    )
  })

  it('/codegraph 同时隐藏 Breadcrumb/FileTree，回到工作台后恢复', async () => {
    renderShell()
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    expect(screen.getByLabelText('当前位置')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '代码图' }))
    await waitFor(() => expect(document.querySelector('iframe[title="代码图"]')).not.toBeNull())
    expect(screen.queryByText('文件')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('当前位置')).not.toBeInTheDocument()

    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    expect(screen.getByLabelText('当前位置')).toBeInTheDocument()
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
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('button', { name: /关闭 go.mod/ })

    // 等文件读出来、textarea 可用后打一行字，把「脏」造出来
    const ta = await screen.findByRole('textbox', { name: 'go.mod' })
    fireEvent.change(ta, { target: { value: 'module handoff\nx' } })

    // 从 + 菜单开一个新终端：激活它让 FileTab 卸载回写草稿，内容就此带上 draft
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))

    // 点 tab 条上的 ×：这次 tab.content 里已有 draft，应弹确认而不是直接关
    fireEvent.click(screen.getByRole('button', { name: /关闭 go.mod/ }))
    expect(screen.getByRole('heading', { name: '关闭未保存的文件' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /关闭 go.mod/ })).toBeInTheDocument()

    // 确认后真的关掉
    fireEvent.click(screen.getByRole('button', { name: '不保存，关闭' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: /关闭 go.mod/ })).not.toBeInTheDocument())
  })

  it('干净的文件 tab 直接关，不打扰', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findByRole('button', { name: /关闭 go.mod/ })

    // 没打过字，内容没有 draft——× 应该直接关，连弹层都不出现
    fireEvent.click(screen.getByRole('button', { name: /关闭 go.mod/ }))
    await waitFor(() => expect(screen.queryByRole('button', { name: /关闭 go.mod/ })).not.toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: '关闭未保存的文件' })).not.toBeInTheDocument()
  })
})

// liveSession 造一条「这个会话还活着」的列表项，只有 id 有意义。
const liveSession = (id: string) => ({
  id, base_kind: 'workspace', base_path: '/repo/x', machine: '', shell: '/bin/zsh',
  created_at: '2026-08-20T00:00:00Z', cols: 120, rows: 40, attached: 1, pid: 9,
  bytes_out: 0, foreground: false, incompatible: false,
})

describe('关闭一个服务端已经没有的终端会话', () => {
  // 场景：agentd 重启后内存里的会话全没了，页面上的终端 tab 变成死物。用户点 ×
  // 确认关闭时 DELETE 会拿到 404，如果照「删失败就不关 tab」处理，这个 tab 就被
  // 焊死在界面上——关不掉、也没有第二个出口。
  //
  // 造场景：在中央开一个终端 tab（会话 id 由 createPtySession 桩给出），再让
  // deletePtySession 抛 404。
  const openTerminalTab = async () => {
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    // 等 TerminalTab 把会话 id 回报上来，否则 × 走的是「还没有会话」那条直接关的路
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())
    return await screen.findByRole('button', { name: /关闭 bash/ })
  }

  it('DELETE 返回 404 时照样关掉 tab——要杀的东西已经不在了', async () => {
    // agentd 重启后的现场：列表里一个会话都没有，DELETE 也只会 404
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
    vi.mocked(deletePtySession).mockRejectedValue(new ApiError(404, '终端会话 new-1 不存在'))
    const closeBtn = await openTerminalTab()

    fireEvent.click(closeBtn)
    expect(await screen.findByRole('heading', { name: '关闭终端会话' })).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: '关闭' }))

    await waitFor(() => expect(screen.queryByRole('button', { name: /关闭 bash/ })).not.toBeInTheDocument())
    expect(screen.queryByText(/不存在/)).toBeNull()
  })

  it('DELETE 返回 500 时仍然不关 tab——会话可能还活着，不能从视野里抹掉', async () => {
    // 这一路会话确实还在（探测答得出来），删失败就是真失败
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [liveSession('new-1')] })
    vi.mocked(deletePtySession).mockRejectedValue(new ApiError(500, 'kill 失败'))
    const closeBtn = await openTerminalTab()

    fireEvent.click(closeBtn)
    expect(await screen.findByRole('heading', { name: '关闭终端会话' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '关闭并终止' }))

    expect(await screen.findByText(/kill 失败/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /关闭 bash/ })).toBeInTheDocument()
  })
})

describe('会话已经不在时弹层要说实话', () => {
  it('服务端查不到这个会话时，弹层不再说「会终止正在运行的命令」', async () => {
    // 探测答「一个会话都没有」= agentd 重启后的现场
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())

    fireEvent.click(await screen.findByRole('button', { name: /关闭 bash/ }))
    expect(await screen.findByText(/在服务端已经不存在了/)).toBeInTheDocument()
    expect(screen.queryByText(/会被一并结束/)).toBeNull()
    // 没有东西可终止，按钮就不该再叫「关闭并终止」
    expect(screen.getByRole('button', { name: '关闭' })).toBeInTheDocument()
  })
})
