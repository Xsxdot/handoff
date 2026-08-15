import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ProjectTreeResp, Task } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { ProjectTree } from './ProjectTree'

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
  onSelectDir?: (b: BaseDir) => void
  onOpenTask?: (b: BaseDir | null, id: string) => void
  onOpenBoard?: () => void
  onOpenTickets?: () => void
  onOpenSettings?: () => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
} = {}) {
  const tree: ProjectTreeResp = {
    projects: [{
      project_id: 'p1', origin_url: '', name: 'handoff',
      locations: [{
        machine: '', name: 'handoff', path: '/w', probe_error: '',
        workspaces: [
          { path: '/w', branch: 'main', head: 'abc', is_main: true, managed: false },
          { path: '/w/b2-b3', branch: over.branch ?? 'integration/b2-b3', head: 'def', is_main: false, managed: true },
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
    onSelectDir: over.onSelectDir ?? vi.fn(),
    onOpenTask: over.onOpenTask ?? vi.fn(),
    onOpenBoard: over.onOpenBoard ?? vi.fn(),
    onOpenTickets: over.onOpenTickets ?? vi.fn(),
    onOpenSettings: over.onOpenSettings ?? vi.fn(),
    onAddProject: over.onAddProject ?? vi.fn(),
    // 「显式传 undefined」与「没传」要区分开：右键菜单测试需要 onUnregister
    // 真的是 undefined，`?? vi.fn()` 会把显式 undefined 兜底成 mock
    onUnregister: 'onUnregister' in over ? over.onUnregister : vi.fn(),
  }
  return p
}

describe('ProjectTree', () => {
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
            workspaces: [{ path: '/srv/a', branch: 'main', head: 'abc', is_main: true, managed: false }],
          }],
        },
      ],
      unowned: [],
      machines: [{ name: 'devbox', ok: false, fetched_at: '', error: 'dial tcp 10.0.0.8:7777: connect: connection refused' }],
    }
    render(
      <ProjectTree
        tree={tree} tasks={[]} selectedKey={null} ticketCount={0}
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
    render(<ProjectTree tree={tree} tasks={[]} selectedKey={null} ticketCount={0} onSelectDir={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()} />)
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
        tree={tree} tasks={[]} selectedKey={null} ticketCount={0}
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
        tree={tree} tasks={tasks} selectedKey={null} ticketCount={0}
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

  it('顶部有任务看板入口，且不再有开发机入口', () => {
    const onOpenBoard = vi.fn()
    render(<ProjectTree {...props({ onOpenBoard })} />)
    fireEvent.click(screen.getByRole('button', { name: /任务看板/ }))
    expect(onOpenBoard).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '开发机' })).not.toBeInTheDocument()
  })

  it('底部三个入口都在；工单数为 0 时按钮仍在但不显示角标', () => {
    render(<ProjectTree {...props({ ticketCount: 0 })} />)
    expect(screen.getByRole('button', { name: /添加项目/ })).toBeInTheDocument()
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
    render(<ProjectTree tree={tree} tasks={tasks} selectedKey={null} ticketCount={0} onSelectDir={vi.fn()} onOpenTask={onOpenTask} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()} />)
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

  it('菜单里点「注销」进既有确认弹层，文案不变', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    fireEvent.contextMenu(container.querySelector('[data-testid="machine-row"]')!)
    fireEvent.click(screen.getByRole('menuitem', { name: '注销' }))
    expect(screen.getByText(/只解除登记，不删除磁盘上的代码/)).toBeInTheDocument()
  })

  it('未传 onUnregister 时右键不弹菜单——没有可做的操作', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: undefined })} />)
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
