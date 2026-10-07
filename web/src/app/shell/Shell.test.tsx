// Shell 的三栏外框行为测试。
//
// 数据侧全部经 vi.mock('../../api/client') 打桩，不发真实请求：任务流、项目树流、
// 文件树列举、任务详情与 diff 都按固定 fixture 返回。
//
// 断言的是「三栏之间怎么接」：选中目录 → 右栏出现 + 面包屑；点任务/文件 → 中央
// 开对应 tab；切目录 tab 组各自保持；拖放把中央切列；/settings 与 /machines
// 的路由行为；/tasks/:id 深链承接。
//
// 注意：本文件依赖 Task 12-15 的组件（BoardOverlay / TicketsOverlay /
// useGlobalTickets / SettingsPage），在那些任务落地前无法运行，属预期的全期红。
import { createEvent, fireEvent, render, screen, waitFor, within, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Router, UNSAFE_createMemoryHistory, useLocation } from 'react-router-dom'
import { useLayoutEffect, useState } from 'react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppRoutes } from '../../App'
import { coordinatorBase, unlinkedTaskIdsForSummary } from './Shell'
import type { UnlinkedSummary } from '../../api/ledger'
import type { ProjectTreeResp, Task } from '../../api/types'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_SESSION_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'

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
const { fetchSessions } = await import('../../api/rooms')

vi.mock('../../api/ledger', async () => {
  const actual = await vi.importActual<typeof import('../../api/ledger')>('../../api/ledger')
  return {
    ...actual,
    // Shell 既有路由/工作台回归按账本已启用基线运行；关闭态由门控专项断言。
    fetchLedgerHealth: vi.fn().mockResolvedValue({ enabled: true, mirror: [] }),
    // B369.7：卡流/卡详情入桩（默认空集；此前为真 fetch 失败被静默吞掉的等价
    // 空态，现在可断言也不外发请求）。用例内按需覆写。
    fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
    fetchCardDetail: vi.fn().mockResolvedValue(null),
  }
})
// —— B369.7：协调者状态/attach 入桩（默认未绑定；用例⑧在用例内覆写）。
// getQueue 维持真实现（与既有基线一致），只拦协调者两枚。
vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return {
    ...actual,
    getCoordinatorStatus: vi.fn().mockResolvedValue({ bound: false, attach_active: false, attach: null }),
    attachCoordinator: vi.fn(),
  }
})
const { getCoordinatorStatus, attachCoordinator } = await import('../../api/scheduling')
// —— B156.2 C8 追加：房间面路由与 dock 入口（seam 贯穿——点击/挂载最终到达 rooms API）——
// B358.6：会话面经同一 rooms seam 进壳，fetchSessions/createSession/fetchSessionDetail 一并入桩。
vi.mock('../../api/rooms', async () => {
  const actual = await vi.importActual<typeof import('../../api/rooms')>('../../api/rooms')
  return {
    ...actual,
    fetchRooms: vi.fn().mockResolvedValue({ rooms: [], has_more: false }),
    fetchInbox: vi.fn().mockResolvedValue([]),
    fetchRoomMessages: vi.fn().mockResolvedValue([]),
    markRoomRead: vi.fn().mockResolvedValue({ ok: true }),
    sendRoomMessage: vi.fn().mockResolvedValue({ seq: 1 }),
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchSessionDetail: vi.fn().mockResolvedValue(null),
    createSession: vi.fn(),
    addSessionMember: vi.fn().mockResolvedValue({ ok: true }),
    fetchIdentity: vi.fn().mockResolvedValue({ member: 'user:sycm', device: '', configured: true }),
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
  // B300 起 TerminalTab 挂载即注册 OSC 52 handler，替身必须提供 parser 键
  parser: { registerOscHandler: vi.fn(() => ({ dispose: vi.fn() })) },
}
vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn(function () { return termInstance }) }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(function () { return { fit: vi.fn() } }) }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return { onContextLoss: vi.fn(), dispose: vi.fn() } }) }))

const connectPty = vi.fn()
vi.mock('../../api/pty', () => ({ connectPty: (...a: unknown[]) => connectPty(...a) }))

vi.mock('../data/usePreviews', async () => {
  const actual = await vi.importActual<typeof import('../data/usePreviews')>('../data/usePreviews')
  return { ...actual, usePreviews: vi.fn() }
})
const { usePreviews } = await import('../data/usePreviews')

describe('未挂账筛选可信边界', () => {
  const latest: UnlinkedSummary = {
    status: 'latest', observed_at: new Date().toISOString(), count: 1,
    tasks: [{ target: 'linux-01', task_id: 'T1', title: '任务', state: 'running' }],
    unknown_targets: [],
  }

  it('只有带时间戳的完整最新观测能生成当前筛选集合', () => {
    expect([...unlinkedTaskIdsForSummary(latest)!]).toEqual(['T1'])
    expect(unlinkedTaskIdsForSummary({ ...latest, observed_at: null })).toBeNull()
    expect(unlinkedTaskIdsForSummary({ ...latest, unknown_targets: ['linux-02'] })).toBeNull()
    const observedAt = Date.parse(latest.observed_at!)
    expect(unlinkedTaskIdsForSummary(latest, observedAt + 30_000)).not.toBeNull()
    expect(unlinkedTaskIdsForSummary(latest, observedAt + 30_001)).toBeNull()
  })

  it('partial、stale、unavailable、缺失状态和未知状态都不过滤任务', () => {
    for (const status of ['partial', 'stale', 'unavailable', undefined, 'future'] as const) {
      const summary = { ...latest, status } as unknown as UnlinkedSummary
      expect(unlinkedTaskIdsForSummary(summary), String(status)).toBeNull()
    }
  })
})

// T1 挂在 /w/b2-b3 这个工作树上（project_id 'p1'、本机、running）。
// plan_summary 与 name 不同文：taskDisplayName 口径下「名是摘要前缀」视为
// prompt 回声、让位给分支（taskName.ts），夹具不能踩中这条。
const t1: Task = {
  id: 'T1',
  target: '',
  repo_path: '/w/b2-b3',
  branch: 'integration/b2-b3',
  plan_path: '',
  plan_summary: '# 重构工单通道：把工单流挪出看板',
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
  plan_summary: '# 等你批：允许往 go.mod 写依赖吗',
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

afterEach(() => {
  vi.unstubAllGlobals()
  // 紧凑视口用例会改写 innerWidth；每个用例后复位成 jsdom 默认（桌面档），
  // 否则后面的桌面断言会被上一个用例污染（用例顺序耦合是隐性假绿）。
  Object.defineProperty(window, 'innerWidth', { value: 1024, configurable: true, writable: true })
})

beforeEach(() => {
  // 新建会话 owner 记忆是 localStorage 级状态：不清则上一个建会话用例的记忆
  // 会预填进下一个对话框，user.type 变成追加（走查 09-17 修后回退链的连带）。
  window.localStorage.clear()
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
  vi.mocked(usePreviews).mockReturnValue({
    data: { sessions: [], machines: [] }, error: '', refresh: vi.fn(), open: vi.fn().mockResolvedValue(undefined),
    isOpen: () => false, openKeys: new Set(), openingKeys: new Set(),
  })
})

// LocationProbe 把当前 URL 投到 DOM（data-ref=pathname+search），B369.7 起本文件
// 全部 URL 断言的读取口；与 AppRoutes 并排挂在同一 Router 下，不影响被测路由。
function LocationProbe() {
  const location = useLocation()
  return <span data-testid="test-location" data-ref={location.pathname + location.search} />
}

function renderShell(path = '/') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <LocationProbe />
      <AppRoutes />
    </MemoryRouter>,
  )
}

describe('Shell 到 BoardPage 的摘要真实 JSON 接缝', () => {
  it('仅新鲜完整 latest 过滤 task；其他 wire 状态都把全量 task 交给看板', async () => {
    // B369.7 起本文件在模块层把 fetchCards 入桩（默认空集，供 compact 用例）；
    // 本用例验的是「真实 JSON 接缝」，把 fetchCards 接回真实现——请求经下面
    // vi.stubGlobal('fetch', fetchMock) 的 fetchMock 走，wire JSON 逐份喂进来。
    const ledger = await import('../../api/ledger')
    const actualLedger = await vi.importActual<typeof import('../../api/ledger')>('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockImplementation(actualLedger.fetchCards)
    const now = Date.now()
    const linkedTask = { target: 'linux-01', task_id: 'T2', title: '未挂账任务', state: 'running' }
    const states = [
      { name: 'latest', expected: 1, summary: { status: 'latest', observed_at: new Date(now).toISOString(), count: 1, tasks: [linkedTask], unknown_targets: [] } },
      { name: 'partial', expected: 2, summary: { status: 'partial', observed_at: new Date(now).toISOString(), count: 1, tasks: [linkedTask], unknown_targets: ['linux-02'] } },
      { name: 'expired latest', expected: 2, summary: { status: 'latest', observed_at: new Date(now - 30_001).toISOString(), count: 1, tasks: [linkedTask], unknown_targets: [] } },
      { name: 'stale', expected: 2, summary: { status: 'stale', observed_at: '2020-01-01T00:00:00Z', count: 1, tasks: [linkedTask], unknown_targets: [] } },
      { name: 'unavailable', expected: 2, summary: { status: 'unavailable', observed_at: null, count: 0, tasks: [], unknown_targets: ['linux-02'] } },
      { name: 'missing status', expected: 2, summary: { observed_at: new Date(now).toISOString(), count: 1, tasks: [linkedTask], unknown_targets: [] } },
      { name: 'unknown status', expected: 2, summary: { status: 'future', observed_at: new Date(now).toISOString(), count: 1, tasks: [linkedTask], unknown_targets: [] } },
      { name: 'malformed count', expected: 2, summary: { status: 'latest', observed_at: new Date(now).toISOString(), count: 2, tasks: [linkedTask], unknown_targets: [] } },
    ]
    const secondTask: Task = { ...t1, id: 'T2', name: '未挂账任务', repo_path: '/w/second', work_dir: '/w/second' }
    vi.mocked(fetchTasks).mockResolvedValue([t1, secondTask])
    const json = (body: unknown) => new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
    let currentUnlinked: unknown = states[0].summary
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/cards') return json({
        cards: [{ id: 'C1', project: 'p1', needs: '等待处理', open_decisions: 0, conflict: false, open_tickets: 0 }],
        unlinked: currentUnlinked,
      })
      if (path === '/api/decisions?open=1') return json({ decisions: [] })
      throw new Error(`unexpected network request: ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    let cardsResponses = 0
    for (const state of states) {
      currentUnlinked = state.summary
      const responseNo = cardsResponses + 1
      const rendered = renderShell()
      await waitFor(() => {
        cardsResponses = fetchMock.mock.calls.filter(([input]) => String(input) === '/api/cards').length
        expect(cardsResponses).toBeGreaterThanOrEqual(responseNo)
        expect(screen.getByRole('button', { name: '工作项' })).toHaveTextContent('1')
      })
      await userEvent.click(screen.getByRole('button', { name: '任务看板' }))
      expect(await screen.findByText(`共 ${state.expected} 个任务`), state.name).toBeInTheDocument()
      rendered.unmount()
    }
  })
})

// locationRef 读探针当前记录的 URL。
function locationRef(): string | null {
  return screen.getByTestId('test-location').getAttribute('data-ref')
}

// S2（B426）：compact 项目 tab = 原型卡流 MobileProjectList（桌面 ProjectTree 的
// compact 复用退役）。下钻助手：点项目卡 → MobileProjectDetail 覆盖层。
async function openMobileProjectDetail() {
  fireEvent.click(await screen.findByTestId('mobile-project-card'))
  await screen.findByTestId('mobile-project-detail')
}

// renderShellWithHistory 与 renderShell 同构，但把 memory history 句柄交出来。
// window.history 不驱动 MemoryRouter；用例⑯用 history.go(-1)（POP 语义）验证
// 浏览器返回键一致性——与真机返回键走的是同一份 in-memory 历史栈。
function renderShellWithHistory(path = '/') {
  const history = UNSAFE_createMemoryHistory({ initialEntries: [path], v5Compat: true })
  function ShellRouter({ router }: { router: typeof history }) {
    const [state, setState] = useState({ action: router.action, location: router.location })
    useLayoutEffect(() => router.listen(setState), [router])
    return (
      <Router location={state.location} navigationType={state.action} navigator={router}>
        <LocationProbe />
        <AppRoutes />
      </Router>
    )
  }
  const rendered = render(<ShellRouter router={history} />)
  return { history, ...rendered }
}

async function openBranch() {
  // 从整页路由回来时 ProjectTree 仍保留 directoryOpen；只有收起时才展开，
  // 避免第二次调用把已经可见的分支又收回去。
  const sidebar = within(screen.getByRole('complementary', { name: '项目导航' }))
  if (sidebar.queryByText('integration/b2-b3') === null) fireEvent.click(await sidebar.findByTestId('machine-row'))
  fireEvent.click(await sidebar.findByText('integration/b2-b3'))
}

function setPaneRect(element: Element, width = 400, height = 400) {
  element.getBoundingClientRect = () => ({ left: 0, top: 0, right: width, bottom: height, width, height, x: 0, y: 0, toJSON: () => ({}) }) as DOMRect
}

function dropAt(element: Element, dataTransfer: { types: string[]; getData: (type: string) => string; setData: (type: string, value: string) => void }, clientX = 360, clientY = 200) {
  const event = createEvent.drop(element, { dataTransfer: dataTransfer as unknown as DataTransfer })
  Object.defineProperty(event, 'clientX', { value: clientX })
  Object.defineProperty(event, 'clientY', { value: clientY })
  fireEvent(element, event)
}

describe('Shell 三栏外框', () => {
  it('协调者 attach 优先复用树中基准，找不到时建立显式 synthetic workspace', () => {
    expect(coordinatorBase(tree, { machine: '', dir: '/w/b2-b3', command: 'server command' })).toMatchObject({
      key: '/w/b2-b3', path: '/w/b2-b3', projectName: 'handoff', machine: '', kind: 'workspace',
    })
    expect(coordinatorBase(tree, { machine: 'box-2', dir: '/remote/card', command: 'server command' })).toMatchObject({
      key: '/remote/card@box-2', path: '/remote/card', label: 'card', projectName: '', machine: 'box-2', kind: 'workspace',
    })
  })

  it('Shell 把 preview 接到树的第四种行，点击不打开 workbench task tab', async () => {
    const onOpen = vi.fn().mockResolvedValue(undefined)
    vi.mocked(usePreviews).mockReturnValue({
      data: {
        sessions: [{ id: 'preview-1', entry_url: 'http://localhost:5173', cwd: '', origin_url: 'https://example.test/repo', branch: 'feature/preview', created_at: '', ttl_seconds: 7200 }],
        machines: [],
      }, error: '', refresh: vi.fn(), open: onOpen, isOpen: () => false, openKeys: new Set(), openingKeys: new Set(),
    })
    vi.mocked(fetchProjectTree).mockResolvedValueOnce({
      ...tree,
      projects: [{ ...tree.projects[0], origin_url: 'https://example.test/repo/' }],
    })
    renderShell()
    const row = await screen.findByTestId('preview-row-preview-1')
    expect(row).toHaveTextContent('feature/preview · localhost:5173')
    fireEvent.click(row)
    expect(onOpen).toHaveBeenCalledWith('preview-1', '')
    expect(screen.queryByRole('tab', { name: /feature\/preview/ })).toBeNull()
  })

  it('Shell 保留 preview 汇总里的机器错误，不把失联 owner 静默掉', async () => {
    vi.mocked(usePreviews).mockReturnValue({
      data: {
        sessions: [],
        machines: [{ name: 'devbox', ok: false, error: 'dial tcp 10.0.0.8:7777: connect: connection refused', fetched_at: '2026-08-29T00:00:00Z' }],
      }, error: '', refresh: vi.fn(), open: vi.fn().mockResolvedValue(undefined), isOpen: () => false,
      openKeys: new Set(), openingKeys: new Set(),
    })
    renderShell()
    expect(await screen.findByTestId('preview-machine-error-devbox')).toHaveTextContent('connection refused')
  })

  it('未选中目录时右栏文件树不渲染，中央是全局空态', async () => {
    renderShell()
    await waitFor(() => expect(screen.getByText('handoff')).toBeInTheDocument())
    expect(screen.queryByText('文件')).not.toBeInTheDocument()
    expect(screen.getByText('请从左栏选择项目或目录')).toBeInTheDocument()
  })

  it('选中目录后右栏出现，但没有焦点 pane 时不渲染面包屑', async () => {
    renderShell()
    await openBranch()
    await waitFor(() => expect(screen.getByText('文件')).toBeInTheDocument())
    expect(screen.queryByLabelText('当前位置')).toBeNull()
  })

  it('左栏开目录终端创建新组，焦点面包屑跟当前 pane 而非 selected tree', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('重构工单通道'))
    const beforeTabs = screen.getAllByRole('tab').length
    const project = await screen.findByTestId('project-node-p1')
    fireEvent.click(within(project).getByRole('button', { name: '打开主目录终端' }))
    await waitFor(() => expect(screen.getAllByRole('tab')).toHaveLength(beforeTabs + 1))
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('主目录')
    fireEvent.click(within(project).getByText('integration/b2-b3'))
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('主目录')
    expect(screen.getByLabelText('当前位置')).not.toHaveTextContent('integration/b2-b3')
  })

  it('点左栏任务在中央开 TUI tab，顶部 tab 条显示任务原名', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument())
  })

  it('左栏开任务日志带项目、机器、路径和 taskId', async () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {})
    renderShell()

    fireEvent.click(await screen.findByText('重构工单通道'))

    await waitFor(() => expect(screen.getByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument())
    expect(debug).toHaveBeenCalledWith('shell.task.open', expect.objectContaining({
      project: 'handoff', machine: '', path: '/w/b2-b3', taskId: 'T1',
    }))
    debug.mockRestore()
  })

  it('再次点左栏已打开任务只聚焦原 tab，不新增标签', async () => {
    renderShell()
    fireEvent.click(await screen.findByText('重构工单通道'))
    const sidebar = within(screen.getByRole('complementary', { name: '项目导航' }))
    // 左栏已打开行与顶部 chrome 同名：任务原名（不再显示 TUI · T1）
    await waitFor(() => expect(sidebar.getAllByText('重构工单通道').length).toBeGreaterThanOrEqual(1))
    // 2 = 初始空组「组 1」+ 任务的组（基线语义：空组也渲染标签）
    expect(within(screen.getByRole('tablist', { name: '标签组' })).getAllByRole('tab')).toHaveLength(2)
    // 已打开行已带 aria-current（焦点态），点击后仍是聚焦且不新增
    fireEvent.click(sidebar.getAllByText('重构工单通道')[0])
    await waitFor(() => expect(screen.getByRole('tab', { name: /重构工单通道/ })).toHaveAttribute('aria-selected', 'true'))
    // B358.6：左栏新增「会话|任务」tab 行（role=tab），计数收窄到中央标签组
    expect(within(screen.getByRole('tablist', { name: '标签组' })).getAllByRole('tab')).toHaveLength(2)
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

    const target = screen.getAllByTestId('workbench-pane')[0]
    setPaneRect(target)
    dropAt(target, dataTransfer)
    await waitFor(() => expect(screen.getByText('aim · linux-01')).toBeInTheDocument())
    // B358.6：左栏新增 tab 行，计数收窄到中央标签组
    expect(within(screen.getByRole('tablist', { name: '标签组' })).getAllByRole('tab')).toHaveLength(2)
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
    const target = () => {
      const pane = screen.getAllByTestId('workbench-pane')[screen.getAllByTestId('workbench-pane').length - 1]
      setPaneRect(pane)
      return pane
    }
    const dragToPane = (source: HTMLElement) => {
      values.clear()
      dataTransfer.types.length = 0
      fireEvent.dragStart(source, { dataTransfer })
      dropAt(target(), dataTransfer)
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
    // 关闭钮在窗格头与顶部标签条各有一处（chrome 重绘后同一标题两处入口）
    await waitFor(() => expect(screen.getAllByRole('button', { name: /关闭 go.mod/ }).length).toBeGreaterThan(0))
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
    await screen.findAllByRole('button', { name: /关闭 go.mod/ })

    fireEvent.click(screen.getByText('主目录'))
    await waitFor(() => expect(screen.getAllByRole('button', { name: /关闭 go.mod/ }).length).toBeGreaterThan(0))

    fireEvent.click(within(screen.getAllByRole('complementary')[0]).getByText('integration/b2-b3'))
    await waitFor(() => expect(screen.getAllByRole('button', { name: /关闭 go.mod/ }).length).toBeGreaterThan(0))
  })

  it('中央不再渲染手动分屏按钮，布局只由拖放产生', async () => {
    renderShell()
    await openBranch()
    expect(screen.queryByRole('button', { name: '增加分屏' })).toBeNull()
    expect(screen.getAllByTestId('workbench-pane')).toHaveLength(1)
  })

  it('⌘D 不再分屏，也不拦掉浏览器的「加入书签」', async () => {
    renderShell()
    await openBranch()

    const ev = new KeyboardEvent('keydown', { key: 'd', metaKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    await waitFor(() => expect(screen.getAllByTestId('workbench-pane')).toHaveLength(1))
    expect(ev.defaultPrevented).toBe(false)
  })

  it('Ctrl+D 不分屏：终端里那是 EOF，抢走会毁掉终端', async () => {
    renderShell()
    await openBranch()

    const ev = new KeyboardEvent('keydown', { key: 'd', ctrlKey: true, bubbles: true, cancelable: true })
    window.dispatchEvent(ev)

    // B358.6：左栏新增 tab 行自带 tablist，断言收窄到 main（中央）内
    await waitFor(() => expect(within(screen.getByRole('main')).getAllByRole('tablist')).toHaveLength(1))
    expect(ev.defaultPrevented).toBe(false)
  })

  it('/settings 整页替换中央，左栏仍在', async () => {
    renderShell('/settings')
    await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
    // B413：左栏树行随轮询数据改经 startTransition 提交，晚一帧落 DOM——
    // 同步断言改 findByText 重试等待（与同文件其他树行用例同形态）
    expect(await screen.findByText('handoff')).toBeInTheDocument()
  })

  it('/machines 重定向到 /settings', async () => {
    renderShell('/machines')
    await waitFor(() => expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument())
  })

  it('/tasks/:id 深链选中目录、开 TUI tab 并换回 /', async () => {
    renderShell('/tasks/T1')
    await waitFor(() => expect(screen.getByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument())
    // 面包屑第三段跟焦点窗格内容名（任务原名），替换掉目录名
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('重构工单通道')
  })

  // 停在 /cards 或 /flows 时，整页盖在工作台上——侧栏点任务只改工作台状态
  // 不换路由的话，用户看见的还是看板。真机实测踩到。
  it('停在 /cards 时点左栏任务，中央换回工作台并开 TUI tab', async () => {
    renderShell('/cards')
    fireEvent.click(await screen.findByText('重构工单通道'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument())
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

    fireEvent.click(within(screen.getByRole('complementary', { name: '项目导航' })).getByText('integration/b2-b3'))
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
    // 第三段跟焦点窗格内容名：跳到的是 T2（等你批），不是 T1
    await waitFor(() => expect(screen.getByLabelText('当前位置')).toHaveTextContent('等你批'))
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

  it('openItems 顺序固定：按打开顺序排列，不随当前基准重排（2026-08-29）；点已打开行 focusTab 切基准并激活', async () => {
    renderShell()
    await openBranch()
    // 在 /w/b2-b3 开 file tab（第一个已打开行）
    fireEvent.click(await screen.findByText('go.mod'))
    const project = await screen.findByTestId('project-node-p1')
    // 选中 /w（主目录）并在那里开终端（第二个已打开行）
    fireEvent.click(within(project).getByText('主目录'))
    fireEvent.click(within(project).getByRole('button', { name: '打开主目录终端' }))
    // 顺序 = 打开顺序（go.mod 先开）；随后切走基准（聚焦 go.mod 行）顺序**不变**
    // ——「当前基准置顶」分区已删，左栏不再随打开动作洗牌
    await waitFor(() => expect(
      screen.getAllByTestId('open-item-name').map((el) => el.textContent),
    ).toEqual(['go.mod', 'bash · 主目录']))
    fireEvent.click(screen.getAllByTestId('open-item-row').find((row) => row.textContent?.includes('bash · 主目录'))!)
    await waitFor(() => expect(screen.getByRole('tab', { name: /bash · 主目录/ })).toHaveAttribute('aria-selected', 'true'))
    expect(
      screen.getAllByTestId('open-item-name').map((el) => el.textContent),
    ).toEqual(['go.mod', 'bash · 主目录'])
  })

  it('左栏已打开行圆点按行类着色：文件有草稿→琥珀、终端连接正常→绿（2026-08-29）', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    // 在文件里打字：草稿经 onDraftChangeLive 写回 tab 内容，左栏文件行圆点转琥珀。
    // textarea 带 aria-label=rel；搜索框也是 textbox，先按角色收窄再按名字取
    await waitFor(() => expect(screen.getAllByRole('textbox').some((el) => el.getAttribute('aria-label') === 'go.mod')).toBe(true))
    const box = screen.getAllByRole('textbox').find((el) => el.getAttribute('aria-label') === 'go.mod')!
    fireEvent.change(box, { target: { value: 'dirty' } })
    await waitFor(() => {
      const fileRow = screen.getAllByTestId('open-item-row').find((row) => row.textContent?.includes('go.mod'))!
      expect(fileRow.querySelector('.bg-state-intervention')).not.toBeNull()
    })
    // 终端（会话建立中/连接正常，无断开上报）圆点保持绿
    const project = await screen.findByTestId('project-node-p1')
    fireEvent.click(within(project).getByText('主目录'))
    fireEvent.click(within(project).getByRole('button', { name: '打开主目录终端' }))
    await waitFor(() => {
      const terminalRow = screen.getAllByTestId('open-item-row').find((row) => row.textContent?.includes('bash · 主目录'))!
      expect(terminalRow.querySelector('.bg-state-active')).not.toBeNull()
    })
  })

  it('面包屑第三段随激活 tab 变化', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('go.mod')
    // 激活切到终端 tab：第三段跟终端标题
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    await waitFor(() => expect(screen.getByLabelText('当前位置')).toHaveTextContent('bash · integration/b2-b3'))
  })

  it('/codegraph 同时隐藏 Breadcrumb/FileTree，回到工作台后恢复', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('重构工单通道'))
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
    await screen.findAllByRole('button', { name: /关闭 go.mod/ })

    // 等文件读出来、textarea 可用后打一行字，把「脏」造出来
    const ta = await screen.findByRole('textbox', { name: 'go.mod' })
    fireEvent.change(ta, { target: { value: 'module handoff\nx' } })

    // 从 + 菜单开一个新终端：激活它让 FileTab 卸载回写草稿，内容就此带上 draft
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))

    // 点窗格头上的 ×：这次 tab.content 里已有 draft，应弹确认而不是直接关
    //（组标签条的关闭钮语义是关闭整组，必须瞄准窗格头的那个）
    const draftPane = screen.getAllByTestId('workbench-pane').find((paneEl) => paneEl.textContent?.includes('go.mod'))!
    fireEvent.click(within(draftPane).getByRole('button', { name: /关闭 go.mod/ }))
    expect(screen.getByRole('heading', { name: '关闭未保存的文件' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /关闭 go.mod/ }).length).toBeGreaterThan(0)

    // 确认后真的关掉
    fireEvent.click(screen.getByRole('button', { name: '不保存，关闭' }))
    await waitFor(() => expect(screen.queryAllByRole('button', { name: /关闭 go.mod/ })).toHaveLength(0))
  })

  it('干净的文件 tab 直接关，不打扰', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findAllByRole('button', { name: /关闭 go.mod/ })

    // 没打过字，内容没有 draft——窗格头 × 应该直接关，连弹层都不出现
    const cleanPane = screen.getAllByTestId('workbench-pane').find((paneEl) => paneEl.textContent?.includes('go.mod'))!
    fireEvent.click(within(cleanPane).getByRole('button', { name: /关闭 go.mod/ }))
    await waitFor(() => expect(screen.queryAllByRole('button', { name: /关闭 go.mod/ })).toHaveLength(0))
    expect(screen.queryByRole('heading', { name: '关闭未保存的文件' })).not.toBeInTheDocument()
  })
})

// 左栏已打开行的悬停 ×（2026-08-29）：它是窗格 × 的**另一个入口**，不是另一条
// 规则——守卫（终端会话确认 / 脏草稿确认）必须同一条，断言就钉在这条等价性上。
describe('左栏已打开行的悬停 ×', () => {
  it('干净的文件 tab 点左栏 × 直接关，不打扰', async () => {
    renderShell()
    await openBranch()
    fireEvent.click(await screen.findByText('go.mod'))
    await screen.findAllByRole('button', { name: /关闭 go.mod/ })

    // 左栏 × 与窗格头 × 同名（关闭 go.mod），必须圈定在侧栏里点
    const sidebar = within(screen.getAllByRole('complementary')[0])
    fireEvent.click(sidebar.getByRole('button', { name: '关闭 go.mod' }))
    await waitFor(() => expect(screen.queryAllByRole('button', { name: /关闭 go.mod/ })).toHaveLength(0))
    expect(screen.queryByRole('heading', { name: '关闭未保存的文件' })).not.toBeInTheDocument()
  })

  it('带会话的终端点左栏 × 先弹确认，确认后删会话并关 tab', async () => {
    // 探测答「会话已不在」→ 确认按钮是「关闭」而不是「关闭并终止」
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
    renderShell()
    await openBranch()
    fireEvent.click(screen.getByRole('button', { name: '新建内容' }))
    fireEvent.click(screen.getByRole('menuitem', { name: /新终端/ }))
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())

    const sidebar = within(screen.getAllByRole('complementary')[0])
    fireEvent.click(sidebar.getByRole('button', { name: /关闭 bash/ }))
    expect(await screen.findByRole('heading', { name: '关闭终端会话' })).toBeInTheDocument()
    // 守卫拦下了：tab 还在，× 也还在
    expect(sidebar.getByTestId('open-item-row')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(deletePtySession).toHaveBeenCalledWith('new-1', undefined))
    await waitFor(() => expect(sidebar.queryByTestId('open-item-row')).toBeNull())
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
    // 瞄准窗格头的关闭钮（组标签条同名关闭钮的语义是关闭整组）
    const pane = screen.getAllByTestId('workbench-pane').find((paneEl) => paneEl.textContent?.includes('bash'))!
    return within(pane).getByRole('button', { name: /关闭 bash/ })
  }

  it('DELETE 返回 404 时照样关掉 tab——要杀的东西已经不在了', async () => {
    // agentd 重启后的现场：列表里一个会话都没有，DELETE 也只会 404
    vi.mocked(fetchPtySessions).mockResolvedValue({ sessions: [] })
    vi.mocked(deletePtySession).mockRejectedValue(new ApiError(404, '终端会话 new-1 不存在'))
    const closeBtn = await openTerminalTab()

    fireEvent.click(closeBtn)
    expect(await screen.findByRole('heading', { name: '关闭终端会话' })).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: '关闭' }))

    await waitFor(() => expect(screen.queryAllByRole('button', { name: /关闭 bash/ })).toHaveLength(0))
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
    expect(screen.getAllByRole('button', { name: /关闭 bash/ }).length).toBeGreaterThan(0)
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

    const gonePane = screen.getAllByTestId('workbench-pane').find((paneEl) => paneEl.textContent?.includes('bash'))!
    fireEvent.click(within(gonePane).getByRole('button', { name: /关闭 bash/ }))
    expect(await screen.findByText(/在服务端已经不存在了/)).toBeInTheDocument()
    expect(screen.queryByText(/会被一并结束/)).toBeNull()
    // 没有东西可终止，按钮就不该再叫「关闭并终止」
    expect(screen.getByRole('button', { name: '关闭' })).toBeInTheDocument()
  })
})

// —— B361 会话 IA（B358.6）：左栏两 tab + 会话工作台 tab + 移除反例断言 ——
const sessionSummary = (over: Record<string, unknown> = {}) => ({
  id: 'session:1', kind: 'session', title: '架构物理化', owner: 'user:sy',
  archived: false, unread: 0, needs_human: false, last_activity: '2026-09-12T00:00:00Z',
  ...over,
})

describe('B361 会话 IA', () => {
	it('会话列表首拉失败时经 Shell 显示错误，不显示真实空态', async () => {
		vi.mocked(fetchSessions).mockRejectedValueOnce(new Error('账本查询超时'))
		renderShell()
		expect(await screen.findByRole('alert')).toHaveTextContent('账本查询超时')
		expect(screen.queryByText('（暂无会话）')).toBeNull()
	})

	it('左栏两 tab 点击切换：会话 tab 显列表（默认），任务 tab 显项目树且双挂载不卸载', async () => {
    // 徽章读数需要非空会话流：本支显式给 unread=2（默认桩是空列表）
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary({ unread: 2 })] as never)
    renderShell()
    expect(await screen.findByTestId('session-list')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: /任务/ }))
    expect(await screen.findByText('handoff')).toBeInTheDocument()
    // 双挂载：切到任务 tab 后会话 pane 仍在文档里，仅以 hidden class 隐藏
    // （attribute 版 hidden 会被 role/text 查询判不可达，见台账 Task 4 实证）
    expect(screen.getByTestId('session-list').closest('.hidden')).not.toBeNull()
    fireEvent.click(screen.getByRole('tab', { name: /会话/ }))
    expect(screen.getByTestId('session-list').closest('.hidden')).toBeNull()
    expect(screen.getByTestId('sidebar-unread')).toBeInTheDocument()
  })

  it('账本关闭时不渲染会话 tab（与旧房间面同门控）', async () => {
    vi.mocked(fetchLedgerHealth).mockResolvedValueOnce({ enabled: false, mirror: [] })
    renderShell()
    await waitFor(() => expect(screen.queryByRole('tab', { name: /会话/ })).toBeNull())
  })

  it('会话行开成工作台 tab（组标签=会话 · 标题），可多开；面包屑跟会话 tab', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      sessionSummary(),
      sessionSummary({ id: 'session:2', title: '桌面端体验' }),
    ] as never)
    const user = userEvent.setup()
    renderShell()
    // 两行会话使 session-row 非唯一：findAllBy 等行渲染完成（依赖 fetchSessions
    // 落数，偶发晚于列表容器挂载——2026-09-12 偶发红根因），再按下标点行
    const rows = await screen.findAllByTestId('session-row')
    await user.click(rows[0])
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    await user.click(screen.getAllByTestId('session-row')[1])
    expect(screen.getByRole('tab', { name: /桌面端体验/ })).toBeInTheDocument()
    // 同一会话重复点击只聚焦不开新 tab（dedupKey 全局去重）
    await user.click(screen.getAllByTestId('session-row')[0])
    expect(within(screen.getByRole('tablist', { name: '标签组' })).getAllByRole('tab', { name: /架构物理化/ })).toHaveLength(1)
    // 面包屑随工作台外壳恢复：焦点是会话 tab 时面包屑行仍在。已知缺口（台账
    // Task 4）：breadcrumbSegments 对 kind==='home' 硬编码单段，会话基准
    // （plan §2.4 kind:'home'）的内容名 tail 被吞——「第三段=会话标题」需
    // Breadcrumb.tsx 一行修复（越界文件，归协调者裁决，本卡不动）。
    expect(screen.getByLabelText('当前位置')).toBeInTheDocument()
  })

  it('左栏会话行的 DataTransfer 穿过 Shell 投到工作台窗格右缘分屏（B358.8 #1）', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary()] as never)
    renderShell()
    const row = await screen.findByTestId('session-row')
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
    fireEvent.dragStart(row, { dataTransfer })
    expect(dataTransfer.types).toContain(DRAG_SESSION_MIME)
    expect(JSON.parse(values.get(DRAG_SESSION_MIME)!)).toMatchObject({ sessionId: 'session:1', title: '架构物理化' })

    const target = screen.getAllByTestId('workbench-pane')[0]
    setPaneRect(target)
    dropAt(target, dataTransfer, 360, 200)
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    expect(within(screen.getByRole('tablist', { name: '标签组' })).getAllByRole('tab', { name: /架构物理化/ })).toHaveLength(1)
  })

  it('反例断言：旧房间面板任何形态都不再出现', async () => {
    renderShell('/cards')
    await screen.findByTestId('session-list')
    expect(screen.queryByTestId('room-panel')).toBeNull()
    expect(screen.queryByRole('button', { name: '打开房间面板' })).toBeNull()
    expect(screen.queryByTestId('room-panel-corner')).toBeNull()
    renderShell('/settings')
    expect(screen.queryByRole('button', { name: '打开房间面板' })).toBeNull()
  })

  it('新建会话：对话框只有标题，createSession 只收标题并刷新列表（B358.9 反例：无群主输入）', async () => {
    const { createSession } = await import('../../api/rooms')
    vi.mocked(createSession).mockResolvedValue({ id: 'session:9', title: '新场', owner: 'user:sycm', archived: false, created_at: '', updated_at: '' })
    vi.mocked(fetchSessions).mockResolvedValue([] as never)
    const user = userEvent.setup()
    renderShell()
    await user.click(await screen.findByRole('button', { name: '新建会话' }))
    expect(screen.queryByRole('combobox', { name: '群主身份' })).toBeNull()
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '新场')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(createSession).toHaveBeenCalledWith('新场'))
    // 不再有 owner 记忆（B358.9：owner 服务端按解析人名缺省）
    expect(window.localStorage.getItem('handoff.last-session-owner')).toBeNull()
  })

  it('建会话后不调用 addSessionMember：owner 缺省即创建者入列，补员冗余（B358.9）', async () => {
    const { addSessionMember, createSession } = await import('../../api/rooms')
    vi.mocked(createSession).mockResolvedValue({ id: 'session:9', title: '新场', owner: 'user:sycm', archived: false, created_at: '', updated_at: '' })
    vi.mocked(fetchSessions).mockResolvedValue([] as never)
    const user = userEvent.setup()
    renderShell()
    await user.click(await screen.findByRole('button', { name: '新建会话' }))
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '新场')
    await user.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建会话' })).toBeNull())
    expect(addSessionMember).not.toHaveBeenCalled()
  })

  it('关闭会话 tab（组关闭）后 tabbar 不再含该会话', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary()] as never)
    const user = userEvent.setup()
    renderShell()
    await user.click(await screen.findByTestId('session-row'))
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    // 组关闭钮与窗格头关闭钮同名（「关闭 会话 · 架构物理化」）——收窄到标签组
    // tablist（读数：TabClose aria-label = '关闭 ' + 组标签，plan 预留裁量区）
    fireEvent.click(within(screen.getByRole('tablist', { name: '标签组' })).getByRole('button', { name: /关闭 会话 · 架构物理化/ }))
    await waitFor(() => expect(within(screen.getByRole('tablist', { name: '标签组' })).queryByRole('tab', { name: /架构物理化/ })).toBeNull())
  })

  // B406：sessions 流 401 是终止态——左栏要落过期横幅，不能把「读取中」挂成
  // 永久转圈、也不能让位成「暂无会话」假读数。
  it('会话流 401：左栏渲染过期横幅，不永转圈、不落「暂无会话」假读数', async () => {
    vi.mocked(fetchSessions).mockRejectedValue(
      new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie'))
    renderShell()
    const sidebar = await screen.findByTestId('session-list')
    // 401 拒绝经 usePoll 异步落地：等横幅出现再断言假读数被抑制（全量并发下同步断言会抢跑）
    await within(sidebar).findByText(/会话已失效/)
    expect(within(sidebar).queryByText('（暂无会话）')).not.toBeInTheDocument()
    expect(within(sidebar).getByTestId('session-total')).not.toHaveTextContent('读取中')
  })
})

// —— B369.6 移动断点谱系：紧凑视口底栏四 tab、桌面零漂移、下钻往返 ——
describe('B369.6 移动断点谱系', () => {
  it('紧凑视口渲染底栏四 tab、不渲染桌面左栏与右栏文件树', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell()
    await screen.findByTestId('mobile-home')
    for (const tab of ['sessions', 'cards', 'projects', 'settings']) {
      expect(screen.getByTestId(`mobile-tab-${tab}`)).toBeInTheDocument()
    }
    expect(screen.queryByRole('complementary', { name: '项目导航' })).toBeNull()
    expect(screen.queryByText('文件')).toBeNull()
  })

  it('桌面视口不渲染底栏（既有三栏行为零漂移）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 1440, configurable: true, writable: true })
    renderShell()
    await screen.findByText('handoff')
    expect(screen.queryByTestId('mobile-tabbar')).toBeNull()
    expect(screen.getByRole('complementary', { name: '项目导航' })).toBeInTheDocument()
  })

  it('切换底栏 tab 换成对应内容面，工作台仍常驻（不卸载）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell()
    await screen.findByTestId('mobile-home')
    fireEvent.click(screen.getByTestId('mobile-tab-projects'))
    expect(await screen.findByTestId('mobile-project-card')).toBeInTheDocument()
    // 工作台容器仍在 DOM（B280 keep-alive）
    expect(document.querySelector('[data-testid="workbench-group"]')).not.toBeNull()
  })

  it('项目 tab → 卡 → 详情 → 目录覆盖层；逐级返回回到底栏首页（下钻往返）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell()
    fireEvent.click(await screen.findByTestId('mobile-tab-projects'))
    // S2：点项目卡进移动详情层。
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    expect(await screen.findByTestId('mobile-project-detail')).toBeInTheDocument()
    // 与桌面 openDirectory 同源：详情「浏览文件」开 mobile-dir 覆盖层（main 层
    // 兄弟、DOM 序在后，project+dir 同持时目录层天然盖在详情层上）。
    fireEvent.click(screen.getAllByTestId('project-wt-files')[0])
    expect(await screen.findByTestId('mobile-dir')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('mobile-dir-back'))
    await waitFor(() => expect(screen.queryByTestId('mobile-dir')).toBeNull())
    // 逐级返回：目录 → 详情（project 随行）→ 底栏首页
    expect(screen.getByTestId('mobile-project-detail')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('project-detail-back'))
    await waitFor(() => expect(screen.queryByTestId('mobile-project-detail')).toBeNull())
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
  })

  it('点会话进下钻态：底栏首页让开、返回条出现；返回回到底栏首页', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary()] as never)
    renderShell()
    fireEvent.click(await screen.findByTestId('session-row'))
    expect(await screen.findByTestId('mobile-detail-bar')).toBeInTheDocument()
    // 下钻态：底栏首页整层让开，工作台仍在 DOM（keep-alive）
    expect(screen.queryByTestId('mobile-home')).toBeNull()
    expect(document.querySelector('[data-testid="workbench-group"]')).not.toBeNull()
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    expect(await screen.findByTestId('mobile-home')).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
  })

  it('移动「项目」tab：不可达位置标离线、详情位可看不可操作（不降级只读）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    // 本机位置可用，另加一台远端位置探测失败：probe_error 非空即 CONTEXT
    // 「项目位置不可用」——卡上只标离线，详情位禁用，不渲染缓存内容。
    vi.mocked(fetchProjectTree).mockResolvedValue({
      ...tree,
      machines: [],
      projects: [{
        ...tree.projects[0],
        locations: [
          ...tree.projects[0].locations,
          {
            machine: 'devbox', name: 'handoff', path: '/srv/handoff',
            probe_error: 'dial tcp 10.0.0.8:7777: connect: connection refused',
            workspaces: [{ path: '/srv/handoff/wt', branch: '离线分支', head: 'abc1234', is_main: false, managed: true, created_at: '' }],
          },
        ],
      }],
    })
    renderShell()
    fireEvent.click(await screen.findByTestId('mobile-tab-projects'))
    // S2：项目卡上的位置 chips——两枚各一，断开的那个报「离线」、状态点灰。
    const card = await screen.findByTestId('mobile-project-card')
    const locs = within(card).getAllByTestId('mobile-project-loc')
    expect(locs).toHaveLength(2)
    expect(locs[0].textContent).toContain('本机')
    expect(locs[1].textContent).toBe('devbox · 离线')
    expect(locs[1].querySelector('span[aria-hidden]')!.className).toContain('d4d4d4')
    // 进详情：离线位 pill disabled（可看不可操作）；其工作树内容一格不渲染
    //（activeLoc 停在第一个可用位，不静默展示缓存快照）。
    fireEvent.click(card)
    const detail = await screen.findByTestId('mobile-project-detail')
    const pills = within(detail).getAllByTestId('project-loc-pill')
    expect(pills[1]).toBeDisabled()
    // 文本松匹配：详情 pill 的「devbox· 离线」是既有 JSX 空白合并 + gap 视觉间距，
    // 本卡不动 MobileProjectDetail（spec：详情面 props/行为零改动）。
    expect(pills[1].textContent).toContain('devbox')
    expect(pills[1].textContent).toContain('离线')
    expect(screen.queryByText('离线分支')).toBeNull()
  })
})

// —— 原「统一房间面板挂载」节内与房间面无关的回归支（B358.6 迁移保留）——
describe('Shell 杂项回归', () => {
  it('Shell 不再挂更新提示组件', async () => {
    renderShell('/settings')
    await waitFor(() => expect(screen.queryByTestId('update-toasts')).not.toBeInTheDocument())
  })
})

// —— B369.7 紧凑导航统一：URL 单一事实源（tab↔URL 双向、账本门改写、
// 会话↔卡深链与返回语义）——
// 卡夹具：字段集对齐 api/ledger 的 CardView / CardDetail 消费面（与
// CardsPage.test.tsx 的 wireCard 同一真相），抽屉 URL effect 与详情轮询都能落数。
const b1CardView = {
  id: 'B1', title: '卡甲', status: '进行中', priority: '中', project: 'handoff',
  workflow: '', workflow_version: 1, parent: '', base_branch: '', attachments: [],
  acceptance_criteria: '', created_at: '', updated_at: '', following: '', blocked: false,
  blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0,
  children_done: 0, conflict: false, open_tickets: 0,
}
const b1CardDetail = (over: Record<string, unknown> = {}) => ({
  card: b1CardView,
  relations: [],
  events: [],
  task_states: [],
  effective_base_branch: '',
  decisions: [],
  needs: '',
  ...over,
})

async function mockCardLedger(taskStates: Record<string, unknown>[] = []) {
  const ledger = vi.mocked(await import('../../api/ledger'))
  ledger.fetchCards.mockResolvedValue({ cards: [b1CardView], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
  ledger.fetchCardDetail.mockResolvedValue(b1CardDetail({ task_states: taskStates }) as never)
}

describe('B369.7 紧凑导航统一', () => {
  it('点底栏 tab 写 URL：projects → /?tab=projects（tab→URL）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell()
    await screen.findByTestId('mobile-home')
    fireEvent.click(screen.getByTestId('mobile-tab-projects'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects'))
    expect(screen.getByTestId('mobile-tab-projects')).toHaveAttribute('aria-selected', 'true')
  })

  it('直达 /?tab=projects → projects tab 高亮且项目卡流渲染（URL→tab）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=projects')
    await waitFor(() => expect(screen.getByTestId('mobile-tab-projects')).toHaveAttribute('aria-selected', 'true'))
    expect(await screen.findByTestId('mobile-project-card')).toBeInTheDocument()
  })

  it('直达 /cards → cards tab 高亮 + CardsPage 内容面（pathname 即卡 tab 表达）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/cards')
    await waitFor(() => expect(screen.getByTestId('mobile-tab-cards')).toHaveAttribute('aria-selected', 'true'))
    expect(await screen.findByText('工作项')).toBeInTheDocument()
  })

  it('账本关闭 compact 首屏改写 /?tab=projects（normalize ①）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    vi.mocked(fetchLedgerHealth).mockResolvedValueOnce({ enabled: false, mirror: [] })
    renderShell()
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects'))
    expect(screen.getByTestId('mobile-tab-projects')).toHaveAttribute('aria-selected', 'true')
  })

  it('会话卡身份行 → 卡 tab 卡详情（带会话来源），不直接进任务现场', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary()] as never)
    rooms.fetchSessionDetail.mockResolvedValue({
      summary: { ...sessionSummary(), cards: [{ card_id: 'B1', title: '卡甲', seat: 'user:sy' }] },
      nodes: [],
      timeline: [],
    } as never)
    await mockCardLedger()
    renderShell('/')
    fireEvent.click(await screen.findByTestId('session-row'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    // 会话 tab 在场 → compact「详情」tab（B369.8 T5：两态取代「⋯」抽屉）→ 点卡身份行
    fireEvent.click(await screen.findByTestId('session-view-detail'))
    fireEvent.click(await screen.findByTestId('session-card-row'))
    // from 值里的会话 id 冒号按 URLSearchParams 规则转义；读回时自动解码
    await waitFor(() => expect(locationRef()).toBe('/cards?card=B1&from=session-session%3A1'))
    expect(screen.getByTestId('mobile-tab-cards')).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    // 卡身份入口只到卡详情，不进任务现场
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
  })

  it('直达 /cards?card=<id>&from=session-<sid> 与会话进入得到同一状态', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    await mockCardLedger()
    renderShell('/cards?card=B1&from=session-session:1')
    await waitFor(() => expect(screen.getByTestId('mobile-tab-cards')).toHaveAttribute('aria-selected', 'true'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
    expect(locationRef()).toBe('/cards?card=B1&from=session-session:1')
  })

  it('卡抽屉 ↗ → 任务现场 /?tab=cards&detail=1&from=card-<选中卡>，TUI tab 在场', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    await mockCardLedger([{ Target: 'local', TaskID: 'T1', Purpose: 'implement', LastType: 'question', LastSeq: 3 }])
    renderShell('/cards?card=B1')
    fireEvent.click(await screen.findByRole('button', { name: '跳到 T1' }))
    expect(await screen.findByTestId('mobile-detail-bar')).toBeInTheDocument()
    await waitFor(() => expect(locationRef()).toBe('/?tab=cards&detail=1&from=card-B1'))
    expect(await screen.findByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument()
  })

  it('协调者「打开终端」→ 同款任务现场断言（来源卡取自 URL card 参数）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    await mockCardLedger()
    vi.mocked(getCoordinatorStatus).mockResolvedValue({ bound: true, attach_active: false, attach: { machine: '', dir: '/r/handoff', command: 'coord command' } })
    vi.mocked(attachCoordinator).mockResolvedValue({ machine: '', dir: '/r/handoff', command: 'coord command' })
    renderShell('/cards?card=B1')
    fireEvent.click(await screen.findByRole('button', { name: '打开终端' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认 attach' }))
    expect(await screen.findByTestId('mobile-detail-bar')).toBeInTheDocument()
    await waitFor(() => expect(locationRef()).toBe('/?tab=cards&detail=1&from=card-B1'))
    // 终端 tab 命名口径：bash · <基准 label>（attach 落在主目录基准上）
    expect(await screen.findByRole('tab', { name: /bash · 主目录/ })).toBeInTheDocument()
  })

  it('from=card-<id> 点返回 → /cards?card=<id> 抽屉重开（不带 from：来源链到此为止）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    await mockCardLedger()
    renderShell('/?tab=cards&detail=1&from=card-B1')
    fireEvent.click(await screen.findByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/cards?card=B1'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
  })

  it('from=session-<id> 点返回 → 会话 workbench tab 重建且仍在下钻态（from 清除）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary()] as never)
    renderShell('/?tab=sessions&detail=1&from=session-session:1')
    // 返回分派要用会话流解析标题：等 fetchSessions 真的落数再点返回。
    // mock resolve ≠ React 已提交 sessions state（usePoll 在 then 里 setData），
    // 先 act 冲刷微任务，否则点击落进兜底标题（sessionId）且不会自愈。
    await waitFor(() => expect(rooms.fetchSessions.mock.results.some((r) => r.type === 'return')).toBe(true))
    await act(async () => {})
    fireEvent.click(await screen.findByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    expect(screen.getByTestId('mobile-detail-bar')).toBeInTheDocument()
    // from 已清除：再点返回走无 from 兜底（逐级出栈）
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions'))
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
  })

  it('无 from 点返回 → 回底栏首页（现状兜底不回归）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=sessions&detail=1')
    fireEvent.click(await screen.findByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions'))
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
  })

  it('S2 反例锁：compact 项目 tab 不再出现桌面树件（树轨/worktree 计数/⌘K/页脚/死按钮）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=projects')
    // 原型卡流在场：apphead + 项目卡
    expect(await screen.findByTestId('mobile-project-card')).toBeInTheDocument()
    expect(screen.getByTestId('mobile-add-project')).toBeInTheDocument()
    // 桌面 ProjectTree 四件套不在 compact 项目 tab（S2 承重断言）
    expect(screen.queryByTestId('project-node-p1')).toBeNull()
    expect(screen.queryByTestId('tree-scroll')).toBeNull()
    expect(screen.queryByTestId('project-count')).toBeNull()
    expect(screen.queryByTestId('mobile-nav-note')).toBeNull()
    expect(screen.queryByPlaceholderText('搜索项目、机器或任务')).toBeNull()
    // 流程/代码图死按钮随 ProjectTree 复用一并退役（原属其行簇）
    expect(screen.queryByRole('button', { name: '流程' })).toBeNull()
    expect(screen.queryByRole('button', { name: '代码图' })).toBeNull()
  })

  it('S2 形态裁定：compact 项目 tab 无「工作项」行钮（能力随树复用退役，卡页走底栏 tab）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=projects')
    await screen.findByTestId('mobile-project-card')
    expect(screen.queryByRole('button', { name: '打开 handoff 工作项' })).toBeNull()
  })

  it('桌面视口零漂移：行钮仍 hover-only，流程/代码图钮在场', async () => {
    renderShell()
    const project = await screen.findByTestId('project-node-p1')
    const button = within(project).getByRole('button', { name: '打开 handoff 工作项' })
    // 右侧簇容器是行钮的直接父 span；断言它仍是 hover-only 可见性策略
    const cluster = button.closest('span')!
    expect(cluster.className).toContain('hidden group-hover:flex')
    expect(screen.getByRole('button', { name: '流程' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '代码图' })).toBeInTheDocument()
  })

  it('北极星全链：会话→卡详情→任务现场→返回会话→返回列表（URL 轨迹+状态集）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary()] as never)
    rooms.fetchSessionDetail.mockResolvedValue({
      summary: { ...sessionSummary(), cards: [{ card_id: 'B1', title: '卡甲', seat: 'user:sy' }] },
      nodes: [],
      timeline: [],
    } as never)
    await mockCardLedger([{ Target: 'local', TaskID: 'T1', Purpose: 'implement', LastType: 'question', LastSeq: 3 }])
    renderShell('/')
    // ① 会话行 → 下钻态
    fireEvent.click(await screen.findByTestId('session-row'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    // ② 卡身份行 → 卡 tab 卡详情（不进任务现场）；
    //    详情面板入口 = compact「详情」tab（B369.8 T5：两态取代「⋯」抽屉）
    fireEvent.click(await screen.findByTestId('session-view-detail'))
    fireEvent.click(await screen.findByTestId('session-card-row'))
    await waitFor(() => expect(locationRef()).toBe('/cards?card=B1&from=session-session%3A1'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(screen.getByTestId('mobile-tab-cards')).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
    // ③ 卡抽屉 ↗ → 任务现场（会话来源随行，返回条在场，TUI tab 在场）
    fireEvent.click(await screen.findByRole('button', { name: '跳到 T1' }))
    expect(await screen.findByTestId('mobile-detail-bar')).toBeInTheDocument()
    await waitFor(() => expect(locationRef()).toBe('/?tab=cards&detail=1&from=session-session%3A1'))
    expect(await screen.findByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument()
    // ④ 返回 → 会话 tab 重建（openOrFocus 幂等），仍在下钻态
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    // ⑤ 再返回 → 会话列表（from 已清，逐级出栈兜底）
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions'))
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-detail-bar')).toBeNull()
  })

  it('浏览器返回键一致性：目录下钻后历史回退到上一导航态，无死 URL', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    const { history } = renderShellWithHistory('/')
    await screen.findByTestId('mobile-home')
    fireEvent.click(screen.getByTestId('mobile-tab-projects'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects'))
    // S2：卡 → 详情 → 浏览文件（dir 叠加在详情上，project 随行）
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    await screen.findByTestId('mobile-project-detail')
    fireEvent.click(screen.getAllByTestId('project-wt-files')[0])
    await waitFor(() => expect(screen.getByTestId('mobile-dir')).toBeInTheDocument())
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects&dir=%2Fr%2Fhandoff&project=p1'))
    // 返回键（memory history go(-1)，POP）：回退到下钻前的详情态——
    // 目录覆盖层收起、详情在场，不落在一个无人消费的死 URL 上
    act(() => { history.go(-1) })
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects&project=p1'))
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-dir')).toBeNull()
    expect(screen.getByTestId('mobile-project-detail')).toBeInTheDocument()
    expect(screen.getByTestId('mobile-tab-projects')).toHaveAttribute('aria-selected', 'true')
  })

  it('桌面免疫冒烟：from 深链不被改写、/?tab= 无副作用', async () => {
    await mockCardLedger()
    // 桌面 /cards 深链：抽屉开、URL 原样（from 不被消费也不被清除）
    const first = renderShell('/cards?card=B1&from=session-session:1')
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(locationRef()).toBe('/cards?card=B1&from=session-session:1')
    expect(screen.queryByTestId('mobile-home')).toBeNull()
    first.unmount()
    // 桌面 /?tab=projects：工作台正常渲染，URL 无副作用
    renderShell('/?tab=projects')
    expect(await screen.findByTestId('project-node-p1')).toBeInTheDocument()
    expect(screen.queryByTestId('mobile-home')).toBeNull()
    expect(locationRef()).toBe('/?tab=projects')
  })
})

// —— B369.8 设置两级：设置中心四分区 + 二级页 sub 语汇 + 偏好消费（角标门控、
// 会话打开方式 scene 档）——
const { __resetWebPrefsForTest } = await import('../settings/useWebPrefs')
const { savePrefs, DEFAULT_WEB_PREFS } = await import('../settings/webPrefs')

// webPrefs 是模块级单例：任何改写偏好/落盘的用例都必须在用例边界 reset，
// 否则状态会泄给同文件后续用例（隐性用例顺序耦合）。
describe('B369.8 设置两级（compact）', () => {

  beforeEach(() => {
    localStorage.clear()
    __resetWebPrefsForTest()
  })
  afterEach(() => {
    localStorage.clear()
    __resetWebPrefsForTest()
  })

  it('设置中心缺省三节 + 五入口行（pairing 出列）；点「执行机与配对」→ sub 落 URL + MachinesPage 在场', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=settings')
    await screen.findByTestId('pref-session-open-mode')
    expect(screen.getByTestId('pref-badges')).toBeInTheDocument()
    expect(screen.getByTestId('settings-about')).toBeInTheDocument()
    expect(screen.getByText('显示与可访问性')).toBeInTheDocument()
    // B369.10 T10：三节骨架 + 五入口行；pairing 行从 hub 出列（合一入口承接），
    // 词表项与 sub=pairing 深链在 SettingsPage.test 另锁
    for (const id of ['settings-section-work', 'settings-section-machine', 'settings-section-about']) {
      expect(screen.getByTestId(id)).toBeInTheDocument()
    }
    for (const key of ['machines', 'discipline', 'automation', 'env', 'update']) {
      expect(screen.getByTestId(`settings-sub-${key}`)).toBeInTheDocument()
    }
    expect(screen.queryByTestId('settings-sub-pairing')).toBeNull()
    fireEvent.click(screen.getByTestId('settings-sub-machines'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=settings&sub=machines'))
    expect(await screen.findByTestId('settings-sub-back')).toBeInTheDocument()
    expect((await screen.findAllByText('本机')).length).toBeGreaterThan(0)
  })

  it('深链直达 /?tab=settings&sub=machines → 同状态（URL→sub）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=settings&sub=machines')
    await waitFor(() => expect(screen.getByTestId('mobile-tab-settings')).toHaveAttribute('aria-selected', 'true'))
    expect(await screen.findByTestId('settings-sub-back')).toBeInTheDocument()
    expect((await screen.findAllByText('本机')).length).toBeGreaterThan(0)
    // 返回行 → 设置中心（URL 剥 sub）
    fireEvent.click(screen.getByTestId('settings-sub-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=settings'))
    expect(screen.getByTestId('pref-session-open-mode')).toBeInTheDocument()
  })

  it('/?tab=cards&sub=machines 被 normalize 清参（② cards 形状归一连带剥掉残参）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=cards&sub=machines')
    await waitFor(() => expect(locationRef()).toBe('/cards'))
    expect(screen.queryByTestId('settings-sub-back')).toBeNull()
  })

  it('提醒关 → 底栏角标消失（mock 未读；行内未读点不在compact会话列表断言面）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary({ unread: 2 })] as never)
    renderShell('/')
    await screen.findByTestId('mobile-home')
    const sessionsTab = screen.getByTestId('mobile-tab-sessions')
    // 会话流是异步的：徽标渲染要等 fetchSessions 落数（同步 getByText 会跑赢数据）
    expect(await within(sessionsTab).findByText('2')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('mobile-tab-settings'))
    fireEvent.click(await screen.findByLabelText(/底栏显示/))
    await waitFor(() => expect(within(sessionsTab).queryByText('2')).toBeNull())
  })

  it('scene 档：有在跑任务的会话 → 群聊先开，解析命中后跳任务现场（TUI tab 在场）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    savePrefs({ ...DEFAULT_WEB_PREFS, sessionOpenMode: 'scene' })
    __resetWebPrefsForTest()
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary({ cards: [{ card_id: 'B1', title: '卡甲' }] })] as never)
    await mockCardLedger([{ Target: 'local', TaskID: 'T1', Purpose: 'implement', LastType: 'question', LastSeq: 3 }])
    renderShell('/')
    fireEvent.click(await screen.findByTestId('session-row'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    // 解析链异步：挂卡 B1 → task_states ∩ 任务流 → T1 running → 跳任务现场
    expect(await screen.findByRole('tab', { name: /重构工单通道/ })).toBeInTheDocument()
    expect(screen.getByTestId('mobile-detail-bar')).toBeInTheDocument()
  })

  it('scene 档：无卡会话 → 留群聊态不报错不跳转（最坏情况 = 现状）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    savePrefs({ ...DEFAULT_WEB_PREFS, sessionOpenMode: 'scene' })
    __resetWebPrefsForTest()
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary()] as never)
    renderShell('/')
    fireEvent.click(await screen.findByTestId('session-row'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=sessions&detail=1'))
    await act(async () => {})   // 冲刷解析链微任务：无卡 → 静默 noop
    expect(screen.queryByRole('tab', { name: /重构工单通道/ })).toBeNull()
  })
})

// —— B369.8 T7：覆盖层 a11y 硬闸（workbench-underlay 三件套）——
describe('B369.8 覆盖层硬闸', () => {
  it('compact 首页：underlay aria-hidden+inert+pointer-events-none（后台对读屏/键盘/指针三路不可达）', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell()
    await screen.findByTestId('mobile-home')
    const underlay = screen.getByTestId('workbench-underlay')
    expect(underlay.getAttribute('aria-hidden')).toBe('true')
    expect(underlay.hasAttribute('inert')).toBe(true)
    expect(underlay.className).toContain('pointer-events-none')
  })

  it('compact 下钻：会话行进任务现场 → 三件套整体摘除（工作台是活面，反例锁）；返回 → 恢复', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary()] as never)
    renderShell('/')
    fireEvent.click(await screen.findByTestId('session-row'))
    await screen.findByTestId('mobile-detail-bar')
    const underlay = screen.getByTestId('workbench-underlay')
    expect(underlay.getAttribute('aria-hidden')).toBe('false')
    expect(underlay.hasAttribute('inert')).toBe(false)
    expect(underlay.className).not.toContain('pointer-events-none')
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-home')
    expect(underlay.getAttribute('aria-hidden')).toBe('true')
    expect(underlay.hasAttribute('inert')).toBe(true)
  })

  it('compact 目录覆盖层：mobile-dir 在场 → 同锁；关目录 → 恢复', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    renderShell('/?tab=projects')
    // S2：卡 → 详情 → 浏览文件开目录覆盖层
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    await screen.findByTestId('mobile-project-detail')
    fireEvent.click(screen.getAllByTestId('project-wt-files')[0])
    expect(await screen.findByTestId('mobile-dir')).toBeInTheDocument()
    const underlay = screen.getByTestId('workbench-underlay')
    expect(underlay.getAttribute('aria-hidden')).toBe('true')
    expect(underlay.hasAttribute('inert')).toBe(true)
    fireEvent.click(screen.getByTestId('mobile-dir-back'))
    await waitFor(() => expect(screen.queryByTestId('mobile-dir')).toBeNull())
    // 关目录回到详情层（project 随行）：覆盖层只是换了一层（详情接手），三件套仍在
    expect(screen.getByTestId('mobile-project-detail')).toBeInTheDocument()
    expect(screen.getByTestId('mobile-home')).toBeInTheDocument()
    expect(underlay.getAttribute('aria-hidden')).toBe('true')
    expect(underlay.hasAttribute('inert')).toBe(true)
  })

  it('桌面：裸工作台 aria-hidden="false" 无 inert（布局无感证据）；/cards 整页覆盖期 → true+inert（FullPageCover 桌面免疫冒烟的反面=覆盖期确实闸上）', async () => {
    await mockCardLedger()
    const first = renderShell('/')
    const underlay = await screen.findByTestId('workbench-underlay')
    expect(underlay.getAttribute('aria-hidden')).toBe('false')
    expect(underlay.hasAttribute('inert')).toBe(false)
    first.unmount()
    renderShell('/cards')
    expect(await screen.findByText('工作项')).toBeInTheDocument()
    const covered = screen.getByTestId('workbench-underlay')
    expect(covered.getAttribute('aria-hidden')).toBe('true')
    expect(covered.hasAttribute('inert')).toBe(true)
  })

  it('跨缝串烧：设置 hub→执行机→返回；卡单列→抽屉三层→关闭焦点归还；会话两态往返；项目折叠展开', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    vi.mocked(fetchSessions).mockResolvedValue([sessionSummary()] as never)
    await mockCardLedger()
    renderShell('/')
    // ① 设置 hub → 执行机 → 返回中心
    fireEvent.click(await screen.findByTestId('mobile-tab-settings'))
    fireEvent.click(await screen.findByTestId('settings-sub-machines'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=settings&sub=machines'))
    fireEvent.click(screen.getByTestId('settings-sub-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=settings'))
    expect(screen.getByTestId('pref-session-open-mode')).toBeInTheDocument()
    // ② 卡单列 → 抽屉三层 → 关闭焦点归还
    fireEvent.click(screen.getByTestId('mobile-tab-cards'))
    const trigger = await screen.findByText('卡甲')
    fireEvent.click(trigger)
    expect(await screen.findByTestId('card-tier-work')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    // ③ 会话两态往返
    fireEvent.click(screen.getByTestId('mobile-tab-sessions'))
    fireEvent.click(await screen.findByTestId('session-row'))
    await screen.findByTestId('mobile-detail-bar')
    fireEvent.click(await screen.findByTestId('session-view-detail'))
    expect(screen.getByTestId('session-view-detail')).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(screen.getByTestId('session-view-chat'))
    expect(screen.getByTestId('session-view-chat')).toHaveAttribute('aria-selected', 'true')
    // ④ 项目：卡主点击进详情（S2 后的主通道）→ 返回列表；workbench-underlay
    //    三件套随 mobile-home 覆盖层在场
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    fireEvent.click(await screen.findByTestId('mobile-tab-projects'))
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects&project=p1'))
    expect(await screen.findByTestId('mobile-project-detail')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('project-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects'))
    expect(screen.queryByTestId('mobile-project-detail')).toBeNull()
    expect(screen.getByTestId('workbench-underlay').hasAttribute('inert')).toBe(true)
  })
})

// —— B369.9 单焦点投影（plan §5 T2）：phone 下钻态单焦点窗格 + 可达单键条。
// 两把 Shell 级变异锁落在这一节：「卸载即红」（pty-host 跨切组恒等——任何
// 「非焦点=条件渲染卸载」的实现让长度掉 1 或节点换新 → 红）与「跨档同节点」
// （375→768→1024 只翻类/属性零重挂——结构分支跨档必重挂 → 红，spec §6 风险 3
// 的执法条款：落红即回 spec 翻案，不得静默带过）。
describe('B369.9 单焦点投影', () => {
  // 两个终端 = 两个真实会话：createPtySession 必须每次给不同 id——固定 id 会让
  // 第二个终端的 onSession 写回被 setTabContent 的 pty:<id> 全局去重撞掉
  //（关新 tab、激活旧 tab），那不是回归，是 mock 失真。
  let ptySeq = 0
  beforeEach(() => {
    ptySeq = 0
    vi.mocked(createPtySession).mockImplementation(async () => ({
      id: `pty-test-${++ptySeq}`, machine: '', base_path: '~', base_kind: 'home', shell: '',
      created_at: '', cols: 100, rows: 30, attached: 0, pid: 0,
      foreground: false, incompatible: false, bytes_out: 0,
    }))
  })

  // reachable（plan §3.2）：键条/窗格「可达性唯一」的可机械化语义——jsdom 里
  // keep-alive 实例恒多（这正是 keep-alive 语义），可达（不在任何 inert /
  // aria-hidden 层内）数 ≤1 才是「单焦点窗格 / 共享单键条」的真义。
  const reachable = (el: HTMLElement) =>
    el.closest('[inert]') === null && el.closest('[aria-hidden="true"]') === null

  const setViewport = (width: number) =>
    Object.defineProperty(window, 'innerWidth', { value: width, configurable: true, writable: true })

  const reachablePanes = () => screen.getAllByTestId('workbench-pane').filter(reachable)
  const reachableKeybars = () => screen.getAllByTestId('mobile-keybar').filter(reachable)
  const reachableSwitchers = () => screen.getAllByTestId('pane-switcher').filter(reachable) as HTMLSelectElement[]

  // compact 下钻三步（S2/B426）：项目卡 → 详情层 → 工作树卡「打开终端」。
  // 下钻态 mobile-home 让开；返回后详情位保留（project 参数随行），每次重进
  // 详情按 rowText 选工作树卡。
  async function openWorkspaceTerminal(rowText: string) {
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    await screen.findByTestId('mobile-project-detail')
    const card = screen.getAllByTestId('project-wt-card').find((item) => item.textContent?.includes(rowText))!
    fireEvent.click(within(card).getByTestId('project-wt-terminal'))
    await screen.findByTestId('mobile-detail-bar')
    await screen.findAllByTestId('pty-host')
  }

  const tabByLabel = (fragment: string) =>
    screen.getAllByRole('tab').find((tab) => (tab.getAttribute('aria-label') ?? '').includes(fragment))!

  it('375 下钻单焦点：可达窗格恰 1、可达键条恰 1，键栏容器在可达层内且类原样', async () => {
    setViewport(375)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    const panes = reachablePanes()
    expect(panes).toHaveLength(1)
    const keybars = reachableKeybars()
    expect(keybars).toHaveLength(1)
    // 键栏容器在可达窗格内（焦点终端自己的键条），容器类原样——投影不改键栏
    expect(panes[0].contains(keybars[0])).toBe(true)
    expect(keybars[0].className).toContain('overflow-x-auto')
    // 焦点窗格是投影焦点层：z-10 无三件套（全量属性断言在组件级 T1）
    expect(panes[0].className).toContain('z-10')
    expect(panes[0].className).not.toContain('pointer-events-none')
  })

  it('两终端两组：可达窗格/键条仍恰 1（另一终端整组在后台 inert 层），组间切换焦点换组', async () => {
    setViewport(375)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    // review 建议修：返回→再进的「返回」分支直接锁死验收原句「返回…不重连」——
    // 第一个终端的 pty-host 节点跨返回/再开/切组全程身份保留
    const host1 = screen.getByTestId('pty-host')
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-home')
    await openWorkspaceTerminal('integration/b2-b3')
    // 两终端 = 两个独立新组（openOrFocus 终端无去重键）+ 初始空组
    expect(screen.getAllByTestId('pty-host')).toHaveLength(2)
    expect(reachablePanes()).toHaveLength(1)
    expect(reachableKeybars()).toHaveLength(1)
    expect(within(reachablePanes()[0]).getAllByText(/bash · integration\/b2-b3/).length).toBeGreaterThan(0)
    // TabBar 组间切换：可达数不变，焦点换到另一条终端
    fireEvent.click(tabByLabel('主目录'))
    await waitFor(() => expect(within(reachablePanes()[0]).getAllByText(/bash · 主目录/).length).toBeGreaterThan(0))
    expect(reachablePanes()).toHaveLength(1)
    expect(reachableKeybars()).toHaveLength(1)
    expect(screen.getAllByTestId('pty-host')).toContain(host1)
  })

  it('变异锁·卸载即红：切组往返后 pty-host 恒 2 且两节点身份都保留', async () => {
    setViewport(375)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-home')
    await openWorkspaceTerminal('integration/b2-b3')
    const hosts = screen.getAllByTestId('pty-host')
    expect(hosts).toHaveLength(2)
    // DOM 序 = 组序：hosts[0]=主目录组，hosts[1]=b2-b3 组
    fireEvent.click(tabByLabel('主目录'))
    await waitFor(() => expect(within(reachablePanes()[0]).getAllByText(/bash · 主目录/).length).toBeGreaterThan(0))
    expect(screen.getAllByTestId('pty-host')).toHaveLength(2)
    expect(screen.getAllByTestId('pty-host')[0]).toBe(hosts[0])
    expect(screen.getAllByTestId('pty-host')[1]).toBe(hosts[1])
    fireEvent.click(tabByLabel('b2-b3'))
    await waitFor(() => expect(within(reachablePanes()[0]).getAllByText(/bash · integration\/b2-b3/).length).toBeGreaterThan(0))
    expect(screen.getAllByTestId('pty-host')).toHaveLength(2)
    expect(screen.getAllByTestId('pty-host')[0]).toBe(hosts[0])
    expect(screen.getAllByTestId('pty-host')[1]).toBe(hosts[1])
  })

  it('变异锁·跨档同节点：375→768→1024 样式闸翻转零重挂，pty-host 节点恒等', async () => {
    setViewport(375)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-home')
    await openWorkspaceTerminal('integration/b2-b3')
    const hosts = screen.getAllByTestId('pty-host')
    expect(hosts).toHaveLength(2)
    // 375 → 768（pad）：投影判据按视口档整体摘除（pane 类回现状），pty-host 不重挂
    setViewport(768)
    window.dispatchEvent(new Event('resize'))
    await waitFor(() => expect(screen.getAllByTestId('workbench-pane')[0].className).toContain('relative flex'))
    expect(screen.getAllByTestId('workbench-pane')[0].className).not.toContain('absolute inset-0')
    expect(screen.getAllByTestId('pty-host')[0]).toBe(hosts[0])
    expect(screen.getAllByTestId('pty-host')[1]).toBe(hosts[1])
    // 768 → 1024（desktop）：移动壳整体让位，pty-host 仍恒等
    setViewport(1024)
    window.dispatchEvent(new Event('resize'))
    await waitFor(() => expect(screen.queryByTestId('mobile-detail-bar')).toBeNull())
    expect(screen.getAllByTestId('pty-host')[0]).toBe(hosts[0])
    expect(screen.getAllByTestId('pty-host')[1]).toBe(hosts[1])
  })

  // 下钻态工作树行不在 DOM（mobile-home 让开），按 WorkbenchPage.test「从远端
  // 项目拖目录到窗格」先例直接构造行 dragstart 的同款 DRAG_DIR_MIME 载荷，投到
  // 窗格右半 → 同组第二列 terminal（投影不拆拖放 handler，岔口 4）。
  async function dropDirOntoPane() {
    const values = new Map<string, string>()
    const dataTransfer = {
      types: [DRAG_DIR_MIME, DRAG_BASE_MIME],
      setData: (type: string, value: string) => {
        values.set(type, value)
        if (!dataTransfer.types.includes(type)) dataTransfer.types.push(type)
      },
      getData: (type: string) => values.get(type) ?? '',
      effectAllowed: '',
      dropEffect: '',
    }
    const dirBase = { key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3', projectName: 'handoff', machine: '' }
    values.set(DRAG_DIR_MIME, JSON.stringify(dirBase))
    values.set(DRAG_BASE_MIME, JSON.stringify(dirBase))
    const pane = reachablePanes()[0]
    setPaneRect(pane, 800, 600)
    const event = createEvent.drop(pane, { dataTransfer: dataTransfer as unknown as DataTransfer })
    Object.defineProperty(event, 'clientX', { value: 720 })
    Object.defineProperty(event, 'clientY', { value: 300 })
    fireEvent(pane, event)
  }

  it('pad 768 反例锚：同组两列并排零投影痕迹——pane 类现状、separator 在场、逐终端键条、无切换入口', async () => {
    setViewport(768)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    const host = await screen.findByTestId('pty-host')
    await dropDirOntoPane()
    await waitFor(() => expect(screen.getAllByTestId('pty-host')).toHaveLength(2))
    expect(screen.getAllByTestId('pty-host')[0]).toBe(host)
    // pad 反例锚：两列并排，投影零痕迹
    const panes = screen.getAllByTestId('workbench-pane')
    expect(panes.filter(reachable)).toHaveLength(2)
    for (const item of panes) {
      expect(item.className).toContain('relative flex')
      expect(item.className).not.toContain('absolute inset-0')
    }
    expect(screen.getAllByRole('separator')).toHaveLength(1)
    // 逐终端键条：可达键条数 = 终端数（pad 不收窄成单键条）
    expect(reachableKeybars()).toHaveLength(2)
    // pad 不投影也就没有切换入口（T3 后补的断言）
    expect(screen.queryByTestId('pane-switcher')).toBeNull()
  })

  it('375 冒烟：同组双列焦点头 pane-switcher 两步切换；TabBar 切到单格组后不在场', async () => {
    setViewport(375)
    renderShell('/?tab=projects')
    await openWorkspaceTerminal('主目录')
    await dropDirOntoPane()
    await waitFor(() => expect(screen.getAllByTestId('pty-host')).toHaveLength(2))
    // 焦点头 switcher 可达恰 1，option = 本组非空格数
    const switcher = reachableSwitchers()[0]
    expect(switcher).toBeDefined()
    const options = within(switcher).getAllByRole('option') as HTMLOptionElement[]
    expect(options).toHaveLength(2)
    // 两步切换：change 选中另一格 → 焦点换列（可达窗格节点换成另一格的窗格）
    const zhuOption = options.find((option) => option.textContent?.includes('主目录'))!
    const paneBefore = reachablePanes()[0]
    fireEvent.change(switcher, { target: { value: zhuOption.value } })
    await waitFor(() => expect(reachablePanes()[0]).not.toBe(paneBefore))
    // 受控 select 跟随焦点：新焦点头的 switcher 选中主目录终端
    expect(reachableSwitchers()[0].value).toBe(zhuOption.value)
    // 再开一终端 = 独立新组（组内单格）→ 切过去后 switcher 不可达（不在场）
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-home')
    await openWorkspaceTerminal('integration/b2-b3')
    expect(reachableSwitchers()).toHaveLength(0)
    // TabBar 切回双列组：switcher 又可达恰 1（在焦点头）
    fireEvent.click(tabByLabel('主目录'))
    await waitFor(() => expect(reachableSwitchers()).toHaveLength(1))
    expect(reachableSwitchers()[0].value).toBe(zhuOption.value)
  })
})

// —— B369.10 T4：移动项目详情（深链入层、双动作、逐级返回、bogus 自愈）——
// 列表行主点击进详情的入口由 T3 接线（onOpenProjectDetail），本 describe 用深链
// 直达详情层，锁挂载形状与返回链；pty-host keep-alive 锁（B270/B369.9）在此不动。
describe('B369.10 项目详情', () => {
  function setCompact() {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
  }

  it('深链 /?tab=projects&project=p1 直达详情层：列表仍在下层、URL 保持', async () => {
    setCompact()
    renderShell('/?tab=projects&project=p1')
    await screen.findByTestId('mobile-project-detail')
    expect(screen.getByTestId('mobile-project-detail').textContent).toContain('handoff')
    // MobileProjectList 常驻下层（覆盖层保挂载，S2 后列表层是原型卡流）
    expect(screen.getByTestId('mobile-project-card')).toBeInTheDocument()
    expect(locationRef()).toBe('/?tab=projects&project=p1')
  })

  it('详情「浏览文件」→ project+dir 叠加，mobile-dir 盖上；dir 返回 → 详情；详情返回 → 列表', async () => {
    setCompact()
    renderShell('/?tab=projects&project=p1')
    await screen.findByTestId('mobile-project-detail')
    fireEvent.click(screen.getAllByTestId('project-wt-files')[0])
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects&dir=%2Fr%2Fhandoff&project=p1'))
    await screen.findByTestId('mobile-dir')
    // 逐级返回第一级：目录层 → 详情层
    fireEvent.click(screen.getByTestId('mobile-dir-back'))
    await screen.findByTestId('mobile-project-detail')
    expect(locationRef()).toBe('/?tab=projects&project=p1')
    // 第二级：详情层 → 列表
    fireEvent.click(screen.getByTestId('project-detail-back'))
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects'))
    expect(screen.queryByTestId('mobile-project-detail')).toBeNull()
  })

  it('详情「打开终端」→ URL 带 project+detail 下钻；返回条回列表后详情仍在场', async () => {
    setCompact()
    renderShell('/?tab=projects&project=p1')
    await screen.findByTestId('mobile-project-detail')
    fireEvent.click(screen.getAllByTestId('project-wt-terminal')[0])
    await waitFor(() => expect(locationRef()).toBe('/?tab=projects&detail=1&project=p1'))
    await screen.findByTestId('pty-host')
    // 逐级返回：任务现场 → 详情（project 随行保留）
    fireEvent.click(screen.getByTestId('mobile-detail-back'))
    await screen.findByTestId('mobile-project-detail')
    expect(locationRef()).toBe('/?tab=projects&project=p1')
  })

  it('project=<bogus> 深链自愈：详情层不渲染、列表照常', async () => {
    setCompact()
    renderShell('/?tab=projects&project=bogus')
    await screen.findByTestId('mobile-home')
    expect(screen.queryByTestId('mobile-project-detail')).toBeNull()
    expect(await screen.findByTestId('mobile-project-card')).toBeInTheDocument()
  })
})

// —— B369.10 T5：Shell 裁决横幅（组级判据、切段不丢、去查证）——
// 判据落点=焦点**组**扫描（bannerTask memo），不是焦点窗格——原型 note ①
// 「钉在顶部，切对话/终端/文件都不丢」的法定语义锁在第二个用例。横幅在 Shell
// 层 absolute 容器内，不进 WorkbenchPage 窗格树（B369.9 两把锁不需要新增）。
describe('B369.10 裁决横幅', () => {
  const setViewport = (width: number) =>
    Object.defineProperty(window, 'innerWidth', { value: width, configurable: true, writable: true })
  const reachable = (el: HTMLElement) =>
    el.closest('[inert]') === null && el.closest('[aria-hidden="true"]') === null
  const reachablePanes = () => screen.getAllByTestId('workbench-pane').filter(reachable)

  // 横幅用例的夹具基取 t2（等你批）：判据语义就是「等你」任务，TuiHeader/中央
  // tab 条渲染 task.name，断言（/等你批/）跟着夹具走；state 由参数覆写。
  function mockTaskState(state: string, pendingTickets: number = 0) {
    const task = { ...t2, state }
    vi.mocked(fetchTasks).mockResolvedValue([task])
    vi.mocked(fetchTaskDetail).mockResolvedValue({
      task,
      pending_tickets: Array.from({ length: pendingTickets }, (_, i) => ({
        id: `tk-${i}`, task_id: task.id, kind: 'question', request: {}, created_at: '',
      })),
      recent_events: [],
    })
  }

  // 下钻任务现场（S2 后）：项目卡 → 详情 → 工作树卡上的等待任务行
  //（project-wt-task → onOpenTask → detail=1）。
  async function openTaskScene() {
    fireEvent.click(await screen.findByTestId('mobile-project-card'))
    await screen.findByTestId('mobile-project-detail')
    fireEvent.click(screen.getAllByTestId('project-wt-task')[0])
    await screen.findByTestId('mobile-detail-bar')
    await screen.findByRole('tab', { name: /等你批/ })
  }

  // dropDirOntoPane：把目录拖进焦点窗格右半 → 同组新列终端窗格并夺焦（place
  // 语义，B369.9 describe 同款手法）。由此焦点窗格不再是 tui，但组内仍有 tui
  // ——组级判据的分界样本。clientX/clientY 必须显式给：jsdom 的 drop 事件不带
  // 坐标，NaN 会投影成 center 落点、把 tui 窗格整个替换掉（切段不丢语义就没了）。
  async function dropDirOntoPane() {
    const values = new Map<string, string>()
    const dataTransfer = {
      types: [DRAG_DIR_MIME, DRAG_BASE_MIME],
      setData: (type: string, value: string) => {
        values.set(type, value)
        if (!dataTransfer.types.includes(type)) dataTransfer.types.push(type)
      },
      getData: (type: string) => values.get(type) ?? '',
      effectAllowed: '',
      dropEffect: '',
    }
    const dirBase = { key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3', projectName: 'handoff', machine: '' }
    values.set(DRAG_DIR_MIME, JSON.stringify(dirBase))
    values.set(DRAG_BASE_MIME, JSON.stringify(dirBase))
    const pane = reachablePanes()[0]
    setPaneRect(pane!, 800, 600)
    const event = createEvent.drop(pane!, { dataTransfer: dataTransfer as unknown as DataTransfer })
    Object.defineProperty(event, 'clientX', { value: 720 })
    Object.defineProperty(event, 'clientY', { value: 300 })
    fireEvent(pane!, event)
    await screen.findAllByTestId('workbench-pane')
  }

  it('waiting_review 任务下钻 → 横幅在场（等你裁决文案 + 去查证钮）', async () => {
    setViewport(375)
    mockTaskState('waiting_review')
    renderShell('/?tab=projects')
    await openTaskScene()
    const banner = screen.getByTestId('task-verdict-banner')
    expect(banner).toBeInTheDocument()
    expect(banner.textContent).toContain('等你裁决 · 交付与作答在对话段')
    expect(screen.getByTestId('task-verdict-activate')).toBeInTheDocument()
    // review 建议修（M1）：新面根节点挂触点基线，去查证抬到 24×24 底线
    expect(banner.className).toContain('[&_button:not(.min-h-11)]:min-h-6')
    expect(banner.className).toContain('[&_button]:min-w-6')
    // review 建议修（M2）：横幅是提示面、不承载作答——内里只有一枚「去查证」，
    // 原型 mock 的 A/B 两个选项钮在实现里没有落点（spec §2.3 已定，记台账）
    expect(within(banner).getAllByRole('button')).toHaveLength(1)
    expect(within(banner).queryByText(/选项|方案 A|方案 B/)).toBeNull()
  })

  it('组级判据：焦点切到同组终端窗格后横幅仍在场（切段不丢）', async () => {
    setViewport(375)
    mockTaskState('waiting_review')
    renderShell('/?tab=projects')
    await openTaskScene()
    await dropDirOntoPane()
    // 拖放后焦点窗格是终端段——若判据字面取「焦点窗格是 tui」，此刻横幅已消失
    expect(screen.getByTestId('task-verdict-banner')).toBeInTheDocument()
  })

  it('去查证 → 焦点回 tui 窗格（审阅面由 TuiTab 既有 effect 自动展开）', async () => {
    setViewport(375)
    mockTaskState('waiting_review')
    renderShell('/?tab=projects')
    await openTaskScene()
    await dropDirOntoPane()
    fireEvent.click(screen.getByTestId('task-verdict-activate'))
    await waitFor(() => expect(reachablePanes()[0].textContent).toContain('等你批'))
  })

  it('waiting_answer + 挂起工单 → 工单文案横幅（N 张工单等你答复）', async () => {
    setViewport(375)
    mockTaskState('waiting_answer', 2)
    renderShell('/?tab=projects')
    await openTaskScene()
    await waitFor(() => expect(screen.getByTestId('task-verdict-banner')).toBeInTheDocument())
    expect(screen.getByTestId('task-verdict-banner').textContent).toContain('2 张工单等你答复')
  })

  it('running 任务 → 横幅不在场', async () => {
    setViewport(375)
    mockTaskState('running')
    renderShell('/?tab=projects')
    await openTaskScene()
    expect(screen.queryByTestId('task-verdict-banner')).toBeNull()
  })

  it('桌面档不渲染横幅（compact 判据）', async () => {
    setViewport(1024)
    mockTaskState('waiting_review')
    renderShell('/')
    await screen.findByTestId('task-row')
    fireEvent.click(screen.getAllByTestId('task-row')[0])
    await screen.findByRole('tab', { name: /等你批/ })
    expect(screen.queryByTestId('task-verdict-banner')).toBeNull()
  })
})

// —— B369.10 T8 seam 冒烟：卡抽屉「驾驶会话」→ Shell 会话流反查开群聊 ——
describe('B369.10 卡到会话双跳 seam', () => {
  it('compact 抽屉「驾驶会话」行 → openSession：session tab 在场 + 下钻任务层', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true, writable: true })
    const rooms = vi.mocked(await import('../../api/rooms'))
    rooms.fetchSessions.mockResolvedValue([sessionSummary({ cards: [{ card_id: 'B1' }] })] as never)
    await mockCardLedger()
    // 夹具补 driver_session（mockCardLedger 的 b1CardView 无此字段）
    const ledger = vi.mocked(await import('../../api/ledger'))
    ledger.fetchCardDetail.mockResolvedValue({
      ...b1CardDetail(), card: { ...b1CardView, driver_session: 'session:1' },
    } as never)
    renderShell('/cards?card=B1')
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    fireEvent.click(await screen.findByTestId('card-jump-session'))
    // 会话流反查命中 session:1 → 群聊 tab 开在中央区 + 下钻（返回条在场）
    expect(await screen.findByRole('tab', { name: /架构物理化/ })).toBeInTheDocument()
    expect(await screen.findByTestId('mobile-detail-bar')).toBeInTheDocument()
  })
})
