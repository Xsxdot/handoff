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
// useGlobalTickets / SettingsPage / FloatingNewPane），在那些任务落地前无法运行，
// 属预期的全期红。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppRoutes } from '../../App'
import type { ProjectTreeResp, Task } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchProjectTree: vi.fn(),
    fetchWorkspaceDir: vi.fn(),
    fetchTaskDetail: vi.fn(),
    fetchTaskDiff: vi.fn(),
    fetchPtySessions: vi.fn(),
    fetchMachines: vi.fn(),
    deletePtySession: vi.fn(),
  }
})
const { fetchTasks, fetchProjectTree, fetchWorkspaceDir, fetchTaskDetail, fetchTaskDiff, fetchPtySessions, fetchMachines, deletePtySession } = await import('../../api/client')

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
            { path: '/r/handoff', branch: '主目录', head: 'abc1234', is_main: true, managed: false },
            { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc1234', is_main: false, managed: true },
          ],
          probe_error: '',
        },
      ],
    },
  ],
  unowned: [],
}

beforeEach(() => {
  vi.mocked(fetchTasks).mockResolvedValue([t1])
  vi.mocked(fetchProjectTree).mockResolvedValue(tree)
  vi.mocked(fetchWorkspaceDir).mockResolvedValue({ entries: [{ name: 'go.mod', is_dir: false, size: 5 }] })
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
})
