// MobileProjectDetail 的组件契约测试（B369.10 T4）：
// locbar 位置平级/离线灰显、工作树卡双动作与任务行、建工作树弹层开合、
// pty 会话区按机器过滤与「回到这个终端」回调。不发真实请求（NewWorktreeDialog
// 的取数经 vi.mock('../../api/client') 打桩）。
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MobileProjectDetail } from './MobileProjectDetail'
import type { ProjectNode, PtySession, Task } from '../../api/types'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchProjectBranches: vi.fn().mockResolvedValue({ branches: [{ name: 'main', taken_by: null }], default_branch: 'main' }),
    fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
  }
})

// project：本机 + linux-01 两处可用位，mac-02 探测失败（离线位）。
const project: ProjectNode = {
  project_id: 'p1',
  origin_url: '',
  name: 'handoff',
  locations: [
    {
      machine: '',
      name: 'handoff',
      path: '/r/handoff',
      workspaces: [
        { path: '/r/handoff', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
        { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'abc', is_main: false, managed: true, created_at: '' },
      ],
      probe_error: '',
    },
    {
      machine: 'linux-01',
      name: 'handoff',
      path: '/srv/handoff',
      workspaces: [
        { path: '/srv/handoff', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
      ],
      probe_error: '',
    },
    {
      machine: 'mac-02',
      name: 'handoff',
      path: '/Users/x/handoff',
      workspaces: [],
      probe_error: 'CONTEXT.md「项目位置不可用」',
    },
  ],
}

function mkTask(id: string, machine: string, workDir: string, state: string): Task {
  return {
    id, target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state, created_at: '', updated_at: '', name: `任务${id}`,
    executor: '', model: '', work_dir: workDir, worktree_managed: true,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '', machine, project_id: 'p1',
  }
}

const tasks = [
  mkTask('t1', '', '/r/handoff', 'running'),
  mkTask('t2', '', '/w/b2-b3', 'waiting_review'),
  mkTask('t3', 'linux-01', '/srv/handoff', 'done'),
]

const pty: PtySession[] = [
  { id: 's1', machine: '', base_path: '/r/handoff', base_kind: 'workspace', shell: 'zsh', created_at: '', cols: 80, rows: 24, attached: 0, pid: 1, incompatible: false, foreground: false, bytes_out: 0 },
  { id: 's2', machine: '', base_path: '', base_kind: 'home', shell: 'bash', created_at: '', cols: 80, rows: 24, attached: 0, pid: 2, exit_code: 0, incompatible: false, foreground: false, bytes_out: 0 },
  { id: 's3', machine: 'linux-01', base_path: '/srv/handoff', base_kind: 'workspace', shell: 'zsh', created_at: '', cols: 80, rows: 24, attached: 0, pid: 3, incompatible: false, foreground: false, bytes_out: 0 },
]

function renderDetail(overrides: Partial<Parameters<typeof MobileProjectDetail>[0]> = {}) {
  const props = {
    project,
    machines: [],
    tasks,
    ptySessions: pty,
    onBack: vi.fn(),
    onOpenTerminalAt: vi.fn(),
    onOpenDirectory: vi.fn(),
    onOpenTask: vi.fn(),
    onWorktreeCreated: vi.fn(),
    onReopenPtySession: vi.fn(),
    ...overrides,
  }
  render(<MobileProjectDetail {...props} />)
  return props
}

describe('MobileProjectDetail locbar 位置切换条', () => {
  it('位置平级逐枚渲染，缺省选中第一个可用位（aria-pressed）', () => {
    renderDetail()
    const pills = screen.getAllByTestId('project-loc-pill')
    expect(pills).toHaveLength(3)
    expect(pills[0]).toHaveAttribute('aria-pressed', 'true')
    expect(pills[1]).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByText('本机')).toBeInTheDocument()
    expect(screen.getByText('linux-01')).toBeInTheDocument()
  })

  it('离线位 disabled 灰显（可看不可操作），标「· 离线」', () => {
    renderDetail()
    const pills = screen.getAllByTestId('project-loc-pill')
    expect(pills[2]).toBeDisabled()
    expect(pills[2].textContent).toContain('· 离线')
  })

  it('点击 pill 翻转选中位：切到 linux-01 后工作树卡跟随换位置', () => {
    renderDetail()
    fireEvent.click(screen.getAllByTestId('project-loc-pill')[1])
    const cards = screen.getAllByTestId('project-wt-card')
    expect(cards).toHaveLength(1)
    expect(cards[0].textContent).toContain('/srv/handoff')
    expect(screen.queryByTestId('project-wt-task')).toBeNull()
  })
})

describe('MobileProjectDetail 工作树卡', () => {
  it('主目录徽标、全路径（mono）与在跑/等待任务行在场', () => {
    renderDetail()
    const cards = screen.getAllByTestId('project-wt-card')
    expect(cards).toHaveLength(2)
    expect(within(cards[0]).getByText('主目录')).toBeInTheDocument()
    expect(within(cards[0]).getByText('/r/handoff')).toBeInTheDocument()
    expect(within(cards[1]).getByText('/w/b2-b3')).toBeInTheDocument()
    expect(within(cards[0]).getByText('● 在跑 · 任务t1')).toBeInTheDocument()
    expect(within(cards[1]).getByText('● 等你 · 任务t2')).toBeInTheDocument()
    // 非等待态的任务不渲染成行（t3 是 done）
    expect(screen.queryByText('任务t3')).toBeNull()
  })

  it('双动作回调带 BaseDir（key 含机器维度）', () => {
    const props = renderDetail()
    fireEvent.click(screen.getAllByTestId('project-wt-terminal')[0])
    expect(props.onOpenTerminalAt).toHaveBeenCalledWith(expect.objectContaining({ key: '/r/handoff', kind: 'workspace' }))
    fireEvent.click(screen.getAllByTestId('project-wt-files')[0])
    expect(props.onOpenDirectory).toHaveBeenCalledWith(expect.objectContaining({ key: '/r/handoff' }))
  })

  it('在跑任务行点击 onOpenTask(base, taskId) 进任务现场', () => {
    const props = renderDetail()
    fireEvent.click(screen.getByText('● 在跑 · 任务t1'))
    expect(props.onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/r/handoff' }), 't1')
  })

  it('「＋ 从分支建工作树」开弹层；关闭后不在场', async () => {
    renderDetail()
    fireEvent.click(screen.getByTestId('project-new-worktree'))
    await screen.findByRole('dialog', { name: '新建工作树' })
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建工作树' })).toBeNull())
  })
})

describe('MobileProjectDetail 终端会话区', () => {
  it('按选中位置那台机器过滤，只列活着（exit_code 缺席）的会话', () => {
    renderDetail()
    const rows = screen.getAllByTestId('project-pty-row')
    // 本机：s1 活着，s2 已退出——只剩一行
    expect(rows).toHaveLength(1)
    expect(rows[0].textContent).toContain('活着')
    expect(rows[0].textContent).toContain('zsh')
  })

  it('「回到这个终端」回调带 (sessionId, 解析出的 BaseDir)', () => {
    const props = renderDetail()
    fireEvent.click(screen.getByTestId('project-pty-resume'))
    expect(props.onReopenPtySession).toHaveBeenCalledWith('s1', expect.objectContaining({ key: '/r/handoff', kind: 'workspace' }))
  })

  it('会话列表仍在读取（null）时显示独立加载状态', () => {
    renderDetail({ ptySessions: null })
    expect(screen.getByText('正在读取终端…')).toBeInTheDocument()
  })

  it('home 会话没有项目工作目录身份，不冒充项目终端或提供项目恢复动作', () => {
    const homeSession: PtySession = { ...pty[0]!, id: 's9', base_path: '', base_kind: 'home' }
    const props = renderDetail({ ptySessions: [homeSession] })
    expect(screen.queryByTestId('project-pty-row')).toBeNull()
    expect(props.onReopenPtySession).not.toHaveBeenCalled()
  })
})
