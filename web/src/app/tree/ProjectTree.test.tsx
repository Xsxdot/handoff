import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ProjectNode, ProjectTreeResp, Task } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { ProjectTree } from './ProjectTree'
import { __resetTreePrefsForTest } from './useTreePrefs'

beforeEach(() => {
  localStorage.clear()
  __resetTreePrefsForTest()
})

function task(over: Partial<Task>): Task {
  return {
    id: 'i', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '', name: '',
    executor: 'opencode', model: '', work_dir: '', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '',
    done_note: '',
    machine: '', project_id: 'p1', ...over,
  }
}

// props 返回一套完整可用的 <ProjectTree> props，默认树为一个项目 handoff（p1）、
// 一台本机、主目录 /w + 工作树 /w/b2-b3，目录下挂任务 T1。over 可覆写
// 分支 / 选中目录 / 工单数 / 是否带原地任务与全部回调。
// 为什么这里只构造 props 不自己 render：调用方统一用
// `render(<ProjectTree {...props({...})} />)`，若在工厂里也 render 一次，
// 单测内会出现两棵树、getByRole/getByText 报 multiple matches。
function props(over: {
  branch?: string
  selectedKey?: string | null
  ticketCount?: number
  inPlaceTask?: boolean
  ticketsByDir?: Map<string, number>
  onSelectDir?: (b: BaseDir) => void
  onOpenTask?: (b: BaseDir | null, id: string) => void
  onOpenBoard?: () => void
  ledgerEnabled?: boolean
  onOpenTickets?: () => void
  onOpenSettings?: () => void
  onOpenFlows?: () => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
} = {}) {
  const tree: ProjectTreeResp = {
    projects: [{
      project_id: 'p1', origin_url: '', name: 'handoff',
      locations: [{
        machine: '', name: 'handoff', path: '/w', probe_error: '',
        workspaces: [
          { path: '/w', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' },
          { path: '/w/b2-b3', branch: over.branch ?? 'integration/b2-b3', head: 'def', is_main: false, managed: true, created_at: '' },
        ],
      }],
    }],
    unowned: [],
  }
  const tasks: Task[] = []
  if (over.inPlaceTask) tasks.push(task({ id: 'T0', project_id: 'p1', machine: '', work_dir: '', name: '原地任务' }))
  tasks.push(task({ id: 'T1', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '重构工单通道' }))
  const p = {
    tree, tasks,
    selectedKey: over.selectedKey ?? null,
    ticketCount: over.ticketCount ?? 0,
    ticketsByDir: over.ticketsByDir ?? new Map(),
    onSelectDir: over.onSelectDir ?? vi.fn(),
    onOpenTask: over.onOpenTask ?? vi.fn(),
    onOpenBoard: over.onOpenBoard ?? vi.fn(),
    // 这组 dock 回归测试覆盖账本已启用时的既有入口；未启用门控另由专项用例覆盖。
    ledgerEnabled: over.ledgerEnabled ?? true,
    onOpenTickets: over.onOpenTickets ?? vi.fn(),
    onOpenSettings: over.onOpenSettings ?? vi.fn(),
    onOpenFlows: over.onOpenFlows ?? vi.fn(),
    onAddProject: over.onAddProject ?? vi.fn(),
    // 「显式传 undefined」与「没传」要区分开：右键菜单测试需要 onUnregister
    // 真的是 undefined，`?? vi.fn()` 会把显式 undefined 兜底成 mock
    onUnregister: 'onUnregister' in over ? over.onUnregister : vi.fn(),
    // 与 onUnregister 同理：onEdit 也要能显式传 undefined，验证「没传就不给
    // 编辑入口」的分支
    onEdit: 'onEdit' in over ? over.onEdit : vi.fn(),
  }
  return p
}

describe('ProjectTree', () => {
  it('目录行按工单 → 任务 → 时间排序，主工作树恒第一', () => {
    const tree: ProjectTreeResp = {
      projects: [{
        project_id: 'p1', origin_url: '', name: 'handoff',
        locations: [{
          machine: '', name: 'handoff', path: '/r', probe_error: '',
          workspaces: [
            { path: '/r/main', branch: 'main', head: 'a', is_main: true, managed: false, created_at: '2020-01-01T00:00:00Z' },
            { path: '/r/quiet', branch: 'quiet', head: 'b', is_main: false, managed: true, created_at: '2026-08-17T00:00:00Z' },
            { path: '/r/busy', branch: 'busy', head: 'c', is_main: false, managed: true, created_at: '2020-01-01T00:00:00Z' },
            { path: '/r/blocked', branch: 'blocked', head: 'd', is_main: false, managed: true, created_at: '2020-01-02T00:00:00Z' },
          ],
        }],
      }],
      unowned: [],
    }
    const tasks = [
      task({ id: 'B1', project_id: 'p1', machine: '', work_dir: '/r/busy', state: 'running', name: 'busy 1' }),
      task({ id: 'B2', project_id: 'p1', machine: '', work_dir: '/r/busy', state: 'running', name: 'busy 2' }),
      task({ id: 'A1', project_id: 'p1', machine: '', work_dir: '/r/blocked', state: 'waiting_answer', name: 'blocked' }),
    ]
    render(
      <ProjectTree
        tree={tree}
        tasks={tasks}
        selectedKey={null}
        ticketCount={1}
        ticketsByDir={new Map([['/r/blocked', 1]])}
        onSelectDir={vi.fn()}
        onOpenTask={vi.fn()}
        onOpenBoard={vi.fn()}
        onOpenTickets={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getAllByTestId('workspace-row').map((row) => row.textContent?.replace(/\d+$/, ''))).toEqual([
      'main', 'blocked', 'busy', 'quiet',
    ])
  })

  it('层级是 项目 → 机器 → 目录 → 任务', () => {
    render(<ProjectTree {...props({ inPlaceTask: true })} />)
    expect(screen.getByText('handoff')).toBeInTheDocument()
    expect(screen.getByText('本机')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
    expect(screen.getByText('原地任务')).toBeInTheDocument()
  })

  it('不可达机器保持可见、标已断开、且不可展开', () => {
    const tree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'alpha',
          locations: [{
            machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: '',
            workspaces: [{ path: '/srv/a', branch: 'main', head: 'abc', is_main: true, managed: false, created_at: '' }],
          }],
        },
      ],
      unowned: [],
      machines: [{ name: 'devbox', ok: false, fetched_at: '', error: 'dial tcp 10.0.0.8:7777: connect: connection refused' }],
    }
    render(
      <ProjectTree
        tree={tree} tasks={[]} selectedKey={null} ticketCount={0} ticketsByDir={new Map()}
        onSelectDir={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
      />,
    )
    const row = screen.getByRole('button', { name: /devbox/ })
    expect(row).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText('已断开')).toBeInTheDocument()
    expect(screen.getByText(/connection refused/)).toBeInTheDocument()
    fireEvent.click(row)
    expect(screen.queryByText('main')).toBeNull()
  })

  it('探测失败的位置渲染 failed 基调的连接态圆点', () => {
    const tree: ProjectTreeResp = {
      projects: [{
        project_id: 'p1', origin_url: '', name: 'alpha',
        locations: [{ machine: 'devbox', name: 'alpha', path: '/srv/a', probe_error: 'dial tcp timeout', workspaces: [] }],
      }],
      unowned: [],
    }
    render(<ProjectTree tree={tree} tasks={[]} selectedKey={null} ticketCount={0} ticketsByDir={new Map()} onSelectDir={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()} />)
    expect(screen.getByText('已断开')).toBeInTheDocument()
    expect(document.querySelector('.bg-state-failed')).not.toBeNull()
  })

  it('probe_error 只影响该 location，不炸整棵树', () => {
    const tree: ProjectTreeResp = {
      projects: [
        {
          project_id: 'p1', origin_url: '', name: 'alpha',
          locations: [{ machine: '', name: 'alpha', path: '/a', probe_error: '目录不存在', workspaces: [] }],
        },
        {
          project_id: 'p2', origin_url: '', name: 'beta',
          locations: [{ machine: '', name: 'beta', path: '/b', probe_error: '', workspaces: [] }],
        },
      ],
      unowned: [],
    }
    render(
      <ProjectTree
        tree={tree} tasks={[]} selectedKey={null} ticketCount={0} ticketsByDir={new Map()}
        onSelectDir={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByText('目录不存在')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
  })

  it('未归属任务挂在末尾的「未归属」分组，不被吞掉', () => {
    const tree: ProjectTreeResp = { projects: [], unowned: [] }
    const tasks = [task({ id: 'u1', project_id: '', machine: '', work_dir: '/x', name: '游离任务' })]
    render(
      <ProjectTree
        tree={tree} tasks={tasks} selectedKey={null} ticketCount={0} ticketsByDir={new Map()}
        onSelectDir={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByText('未归属')).toBeInTheDocument()
    expect(screen.getByText('游离任务')).toBeInTheDocument()
  })

  it('点项目行只展开折叠，不再写筛选', () => {
    const onSelectDir = vi.fn()
    render(<ProjectTree {...props({ onSelectDir })} />)
    fireEvent.click(screen.getByText('handoff'))
    expect(onSelectDir).not.toHaveBeenCalled()
    // 折叠后其下的目录行消失
    expect(screen.queryByText('integration/b2-b3')).not.toBeInTheDocument()
  })

  it('点目录行选中它，回调带完整 BaseDir', () => {
    const onSelectDir = vi.fn()
    render(<ProjectTree {...props({ onSelectDir })} />)
    fireEvent.click(screen.getByText('integration/b2-b3'))
    expect(onSelectDir).toHaveBeenCalledWith({
      key: '/w/b2-b3',
      kind: 'workspace',
      path: '/w/b2-b3',
      label: 'integration/b2-b3',
      projectName: 'handoff',
      machine: '',
    })
  })

  it('detached 的目录用目录名兜底作为 label', () => {
    const onSelectDir = vi.fn()
    render(<ProjectTree {...props({ onSelectDir, branch: '' })} />)
    fireEvent.click(screen.getByText('b2-b3'))
    expect(onSelectDir).toHaveBeenCalledWith(expect.objectContaining({ label: 'b2-b3' }))
  })

  it('selectedKey 命中的目录行带 aria-current', () => {
    render(<ProjectTree {...props({ selectedKey: '/w/b2-b3' })} />)
    expect(screen.getByRole('button', { name: /integration\/b2-b3/ })).toHaveAttribute('aria-current', 'true')
  })

  it('点任务行同时给出它所在目录与任务 id', () => {
    const onOpenTask = vi.fn()
    render(<ProjectTree {...props({ onOpenTask })} />)
    fireEvent.click(screen.getByText('重构工单通道'))
    expect(onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/w/b2-b3' }), 'T1')
  })

  it('work_dir 为空的任务挂到主目录（原地模式）', () => {
    render(<ProjectTree {...props({ inPlaceTask: true })} />)
    // 主目录行下应出现这条任务
    expect(screen.getByText('原地任务')).toBeInTheDocument()
  })

  it('任务看板入口在底部图标区，不在顶部；也没有开发机入口', () => {
    // why：看板 / 工单 / 设置是同一类东西——都是「离开这棵树去别处看」的全局
    // 入口。看板原先单独钉在顶部，那个位置让它看起来像是树的一部分
    const onOpenBoard = vi.fn()
    const { container } = render(<ProjectTree {...props({ onOpenBoard })} />)
    const board = screen.getByRole('button', { name: /任务看板/ })
    const footer = container.querySelector('.border-t')
    expect(footer?.contains(board)).toBe(true)
    fireEvent.click(board)
    expect(onOpenBoard).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '开发机' })).not.toBeInTheDocument()
  })

  it('账本未启用时任务看板标题不带未挂账', () => {
    render(<ProjectTree {...props({ ledgerEnabled: false })} />)
    const board = screen.getByRole('button', { name: '任务看板' })
    expect(board).toHaveAttribute('title', '任务看板')
    expect(board.getAttribute('title')).not.toContain('未挂账')
  })

  it('「添加项目」在「项目 N」标题行里，不在底部入口区', () => {
    // why：它改变树本身，与底部那排「离开这棵树去别处看」的跳转入口不是一类
    // 东西；且原先钉在底部时离它作用的对象（项目列表）一屏远
    const onAddProject = vi.fn()
    const { container } = render(<ProjectTree {...props({ onAddProject })} />)
    const add = screen.getByRole('button', { name: '添加项目' })
    const header = container.querySelector('[data-testid="project-count"]')!.parentElement!
    expect(header.contains(add)).toBe(true)
    const footer = container.querySelector('.border-t')
    expect(footer?.contains(add)).toBe(false)
    fireEvent.click(add)
    expect(onAddProject).toHaveBeenCalled()
  })

  it('底部入口都在；工单数为 0 时按钮仍在但不显示角标', () => {
    render(<ProjectTree {...props({ ticketCount: 0 })} />)
    expect(screen.getByRole('button', { name: /任务看板/ })).toBeInTheDocument()
    // 任务名「重构工单通道」里含「工单」子串，正则用 ^$ 锚定到角标按钮本身
    const ticketBtn = screen.getByRole('button', { name: /^工单$/ })
    expect(ticketBtn).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '设置' })).toBeInTheDocument()
    // 角标只在 ticketCount>0 时渲染。不能靠 queryByText('0')：RowCounts 改图标形态后
    // 目录行会把值为 0 的计数渲染成可见的「0」，这里按角标自己的 token 查
    expect(ticketBtn.querySelector('.bg-state-intervention')).toBeNull()
  })

  it('工单数大于 0 时显示角标并可点开', () => {
    const onOpenTickets = vi.fn()
    render(<ProjectTree {...props({ ticketCount: 3, onOpenTickets })} />)
    expect(screen.getByText('3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^工单$/ }))
    expect(onOpenTickets).toHaveBeenCalled()
  })

  it('设置入口可点', () => {
    const onOpenSettings = vi.fn()
    render(<ProjectTree {...props({ onOpenSettings })} />)
    fireEvent.click(screen.getByRole('button', { name: '设置' }))
    expect(onOpenSettings).toHaveBeenCalled()
  })

  it('未归属任务行回调的首参是 null（没有基准目录）', () => {
    const onOpenTask = vi.fn()
    const tree: ProjectTreeResp = { projects: [], unowned: [] }
    const tasks = [task({ id: 'U1', project_id: '', machine: '', work_dir: '/x', name: '游离任务' })]
    render(<ProjectTree tree={tree} tasks={tasks} selectedKey={null} ticketCount={0} ticketsByDir={new Map()} onSelectDir={vi.fn()} onOpenTask={onOpenTask} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()} />)
    fireEvent.click(screen.getByText('游离任务'))
    expect(onOpenTask).toHaveBeenCalledWith(null, 'U1')
  })

  it('渲染搜索框与「项目 N」，N 默认是项目总数', () => {
    render(<ProjectTree {...props()} />)
    expect(screen.getByPlaceholderText('搜索项目、机器或任务')).toBeInTheDocument()
    expect(screen.getByText('项目')).toBeInTheDocument()
    expect(screen.getByTestId('project-count')).toHaveTextContent('1')
  })

  it('搜任务名：该任务可见，无关目录不可见', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: '重构工单' },
    })
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })

  it('搜项目名：N 仍是 1，整棵子树可见', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: 'handoff' },
    })
    expect(screen.getByTestId('project-count')).toHaveTextContent('1')
    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })

  it('零结果时出空态文案，N 归 0', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: 'zzzz-nothing' },
    })
    expect(screen.getByText('没有匹配的项目或任务')).toBeInTheDocument()
    expect(screen.getByTestId('project-count')).toHaveTextContent('0')
  })

  // 钉住「旁路而非清空」：搜索期间强制展开，清空后手动折叠的状态原样回来
  it('清空搜索后，此前手动折叠的节点仍是折叠的', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')

    // 先手动折叠项目 handoff
    fireEvent.click(screen.getByText('handoff'))
    expect(screen.queryByText('main')).not.toBeInTheDocument()

    // 搜索期间强制展开
    fireEvent.change(input, { target: { value: 'handoff' } })
    expect(screen.getByText('main')).toBeInTheDocument()

    // 清空后折叠态原样回来
    fireEvent.change(input, { target: { value: '' } })
    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })

  it('⌘K 聚焦搜索框', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    expect(document.activeElement).not.toBe(input)
    fireEvent.keyDown(window, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(input)
  })

  it('Ctrl+K 同样聚焦（非 mac）', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'K', ctrlKey: true })
    expect(document.activeElement).toBe(input)
  })

  it('输入框内 Esc 清空并失焦', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'handoff' } })
    expect(input.value).toBe('handoff')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(input.value).toBe('')
    expect(document.activeElement).not.toBe(input)
  })

  it('单独按 k 不聚焦（不劫持普通输入）', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'k' })
    expect(document.activeElement).not.toBe(input)
  })

  it('左栏任务行的圆点跟随任务状态', () => {
    const p = props()
    p.tasks = [
      task({ id: 'T1', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '跑测试', state: 'running' }),
      task({ id: 'T2', project_id: 'p1', machine: '', work_dir: '/w/b2-b3', name: '等你答复的活', state: 'waiting_answer' }),
    ]
    const { container } = render(<ProjectTree {...p} />)
    // 2 个 active：本机行一个连接态圆点 + 任务行 running 一个
    expect(container.querySelectorAll('.bg-state-active')).toHaveLength(2)
    expect(container.querySelectorAll('.bg-state-intervention')).toHaveLength(1)
  })

  it('工单角标用状态 token，不用裸 amber', () => {
    const { container } = render(<ProjectTree {...props({ ticketCount: 3 })} />)
    const badge = screen.getByText('3')
    expect(badge.className).toContain('bg-state-intervention')
    expect(container.innerHTML).not.toContain('bg-amber-500')
  })

  it('「项目 N」的标签与数字之间有间隔，数字更浅', () => {
    render(<ProjectTree {...props()} />)
    const count = screen.getByTestId('project-count')
    // 数字与标签必须是两个可区分的元素，且数字带独立的浅色类
    expect(count.className).toMatch(/text-muted-foreground|opacity/)
    // 间隔靠父容器的 gap 或数字自身的 margin，两者取一即可
    const parent = count.parentElement!
    expect(parent.className + count.className).toMatch(/gap-|ml-/)
  })

  it('机器行右端只剩计数，没有常驻的注销按钮压在上面', () => {
    // why：absolute right-2 的注销按钮与同一行右端的 RowCounts 抢位置。
    // 08-14 只修了垂直（定位上下文从 578px 子树收进机器行），水平仍然重叠
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    expect(container.querySelector('[aria-label="注销"]')).toBeNull()
  })

  it('右键机器行弹出菜单，含「注销」', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    const row = container.querySelector('[data-testid="machine-row"]')!
    fireEvent.contextMenu(row)
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('右键机器行弹出菜单，含「编辑」「注销」两项', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn(), onEdit: vi.fn() })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    expect(screen.getByRole('menuitem', { name: '编辑' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('点菜单「编辑」把所在的 project 交给 onEdit', () => {
    const onEdit = vi.fn()
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn(), onEdit })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    fireEvent.click(screen.getByRole('menuitem', { name: '编辑' }))
    expect(onEdit).toHaveBeenCalledTimes(1)
    const p = onEdit.mock.calls[0][0] as ProjectNode
    expect(p.project_id).toBe('p1')
    expect(p.name).toBe('handoff')
    expect(p.locations).toHaveLength(1)
  })

  it('未传 onEdit 时菜单不出现「编辑」项', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn(), onEdit: undefined })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    expect(screen.queryByRole('menuitem', { name: '编辑' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('菜单里点「注销」进既有确认弹层，文案不变', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    fireEvent.click(screen.getByRole('menuitem', { name: '注销' }))
    expect(screen.getByText(/只解除登记，不删除磁盘上的代码/)).toBeInTheDocument()
  })

  it('未传 onUnregister 与 onEdit 时右键不弹菜单——没有可做的操作', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: undefined, onEdit: undefined })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('树独立滚动，底部入口不在滚动区内', () => {
    const { container } = render(<ProjectTree {...props()} />)
    const scroller = container.querySelector('[data-testid="tree-scroll"]')!
    expect(scroller.className).toMatch(/overflow-y-auto/)
    expect(scroller.className).toMatch(/min-h-0/) // 缺这句 overflow 在 flex 子项里不生效
    // 「添加项目」必须在滚动容器之外
    const addBtn = screen.getByRole('button', { name: /添加项目/ })
    expect(scroller.contains(addBtn)).toBe(false)
  })

  it('项目图标带取色标记，同名项目刷新后同色', () => {
    const { unmount } = render(<ProjectTree {...props()} />)
    const first = document.querySelector('[data-project-color]')!.getAttribute('data-project-color')
    unmount()
    render(<ProjectTree {...props()} />)
    expect(document.querySelector('[data-project-color]')!.getAttribute('data-project-color')).toBe(first)
  })
})

describe('dock 入口', () => {
  // 流程页以前只能手敲 URL——dock 上没有它的按钮，而 spec §5 写的是
  // 「入口挂底部 dock」（2026-08-19 真机找不到）。
  it('有流程页入口', () => {
    const onOpenFlows = vi.fn()
    render(<ProjectTree {...props({ onOpenFlows })} />)
    fireEvent.click(screen.getByLabelText('流程'))
    expect(onOpenFlows).toHaveBeenCalled()
  })
})

describe('显示偏好', () => {
  it('取消勾选项目后它不在树上，「项目 N」旁说明已隐藏几个', () => {
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /handoff/ }))
    expect(within(screen.getByTestId('tree-scroll')).queryByText('handoff')).toBeNull()
    expect(screen.getByTestId('project-count')).toHaveTextContent('0')
    expect(screen.getByText(/已隐藏 1/)).toBeInTheDocument()
  })

  it('开「隐藏无活跃任务的工作树」后，没有活跃任务的目录收进「已隐藏」行，点开还能看到', () => {
    // 默认树里 /w 是主目录（豁免），/w/b2-b3 挂着一条 running 任务。
    // 把那条任务改成 done，它就成了空闲目录
    const p = props({})
    const tasks = p.tasks.map((t) => ({ ...t, state: 'done' }))
    render(<ProjectTree {...p} tasks={tasks} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(screen.queryByText('integration/b2-b3')).toBeNull()
    fireEvent.click(screen.getByText(/已隐藏 1 个目录/))
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })

  it('主工作树与当前选中目录不会被折叠', () => {
    const p = props({ selectedKey: '/w/b2-b3' })
    const tasks = p.tasks.map((t) => ({ ...t, state: 'done' }))
    render(<ProjectTree {...p} tasks={tasks} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(screen.getByText('main')).toBeInTheDocument()          // 主目录
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument() // 选中目录
    expect(screen.queryByText(/已隐藏/)).toBeNull()
  })

  it('搜索期间旁路隐藏偏好：藏起来的项目照样能被搜出来', () => {
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /handoff/ }))
    expect(within(screen.getByTestId('tree-scroll')).queryByText('handoff')).toBeNull()
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), { target: { value: 'handoff' } })
    expect(within(screen.getByTestId('tree-scroll')).getByText('handoff')).toBeInTheDocument()
  })

  it('项目分组下直接列出项目勾选，没有全选/全不选', () => {
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    expect(screen.getByRole('menuitemcheckbox', { name: /handoff/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: '全选' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: '全不选' })).toBeNull()
  })

  it('开「隐藏已结束分组」后，「已结束」行不再出现', () => {
    const p = props({})
    p.tasks.push(task({
      id: 'T-old', state: 'completed', work_dir: '/w/gone', name: '已回收的任务',
    }))
    render(<ProjectTree {...p} />)
    expect(screen.getByTestId('archived-row')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏已结束分组/ }))
    expect(screen.queryByTestId('archived-row')).toBeNull()
  })

  it('搜索期间旁路「隐藏已结束分组」：能搜到已回收任务', () => {
    const p = props({})
    p.tasks.push(task({
      id: 'T-old', state: 'completed', work_dir: '/w/gone', name: '已回收的任务',
    }))
    render(<ProjectTree {...p} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏已结束分组/ }))
    expect(screen.queryByTestId('archived-row')).toBeNull()
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), { target: { value: '已回收' } })
    expect(screen.getByTestId('archived-row')).toBeInTheDocument()
    expect(within(screen.getByTestId('tree-scroll')).getByText('已回收的任务')).toBeInTheDocument()
  })
})

describe('机器行新建工作树', () => {
  it('传了 onWorktreeCreated 才给 + 按钮', () => {
    const { rerender } = render(<ProjectTree {...props({})} onWorktreeCreated={vi.fn()} />)
    expect(screen.getByRole('button', { name: '新建工作树' })).toBeInTheDocument()
    rerender(<ProjectTree {...props({})} />)
    expect(screen.queryByRole('button', { name: '新建工作树' })).toBeNull()
  })

  it('机器不可达时不给这个入口', () => {
    const p = props({})
    const tree = {
      ...p.tree,
      projects: [{
        ...p.tree.projects[0],
        locations: [{ ...p.tree.projects[0].locations[0], probe_error: 'ssh 超时' }],
      }],
    }
    render(<ProjectTree {...p} tree={tree} onWorktreeCreated={vi.fn()} />)
    expect(screen.queryByRole('button', { name: '新建工作树' })).toBeNull()
  })

  it('机器不可达时右键菜单不给新建工作树，但保留编辑与注销', () => {
    const p = props({})
    const tree = {
      ...p.tree,
      projects: [{
        ...p.tree.projects[0],
        locations: [{ ...p.tree.projects[0].locations[0], probe_error: 'ssh 超时' }],
      }],
    }
    render(<ProjectTree {...p} tree={tree} onWorktreeCreated={vi.fn()} />)
    fireEvent.contextMenu(screen.getByTestId('machine-row'))
    expect(screen.queryByRole('menuitem', { name: '新建工作树' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: '编辑' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: '注销' })).toBeInTheDocument()
  })

  it('点 + 开弹层；右键菜单里也有同一个入口', () => {
    render(<ProjectTree {...props({})} onWorktreeCreated={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建工作树' }))
    expect(screen.getByRole('dialog', { name: '新建工作树' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    fireEvent.contextMenu(screen.getByTestId('machine-row'))
    expect(screen.getByText('新建工作树')).toBeInTheDocument()
  })
})
