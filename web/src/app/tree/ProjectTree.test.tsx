import { createEvent, fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { PreviewSession, ProjectNode, ProjectTreeResp, Task } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { ProjectTree, type OpenItem } from './ProjectTree'
import { __resetTreePrefsForTest } from './useTreePrefs'
import { DRAG_BASE_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'

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

// openItem 造一行「已打开项」（Shell 注入的 OpenItem 投影）。
function openItem(over: Partial<OpenItem> = {}): OpenItem {
  const base: BaseDir = { key: '/w', kind: 'workspace', path: '/w', label: 'main', projectName: 'handoff', machine: '' }
  return {
    key: '/w\x1ft9', kind: 'terminal', name: 'bash · main', taskId: undefined,
    machine: '', base, group: 'g1', tabId: 't9', ...over,
  }
}

// props 返回一套完整可用的 <ProjectTree> props，默认树为一个项目 handoff（p1）、
// 一台本机、主目录 /w + 工作树 /w/b2-b3，目录下挂任务 T1。over 可覆写
// 分支 / 选中目录 / 工单数 / 已打开项与全部回调。
// 为什么这里只构造 props 不自己 render：调用方统一用
// `render(<ProjectTree {...props({...})} />)`，若在工厂里也 render 一次，
// 单测内会出现两棵树、getByRole/getByText 报 multiple matches。
function props(over: {
  branch?: string
  selectedKey?: string | null
  ticketCount?: number
  inPlaceTask?: boolean
  ticketsByDir?: Map<string, number>
  openItems?: OpenItem[]
  focusedTaskId?: string | null
  onFocusOpenItem?: (item: OpenItem) => void
  onOpenTerminalAt?: (b: BaseDir) => void
  onOpenDirectory?: (b: BaseDir) => void
  onOpenTask?: (b: BaseDir | null, id: string) => void
  onOpenBoard?: () => void
  ledgerEnabled?: boolean
  onOpenTickets?: () => void
  onOpenSettings?: () => void
  onOpenFlows?: () => void
  onOpenProjectCards?: (project: ProjectNode) => void
  onOpenProjectCodegraph?: (project: ProjectNode) => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
  previews?: PreviewSession[]
  previewOpenKeys?: ReadonlySet<string>
  previewOpeningKeys?: ReadonlySet<string>
  onOpenPreview?: (id: string, machine: string) => void
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
    openItems: over.openItems ?? [],
    focusedTaskId: over.focusedTaskId ?? null,
    onFocusOpenItem: over.onFocusOpenItem ?? vi.fn(),
    onOpenTerminalAt: over.onOpenTerminalAt ?? vi.fn(),
    onOpenDirectory: over.onOpenDirectory ?? vi.fn(),
    onOpenTask: over.onOpenTask ?? vi.fn(),
    onOpenBoard: over.onOpenBoard ?? vi.fn(),
    // 这组 dock 回归测试覆盖账本已启用时的既有入口；未启用门控另由专项用例覆盖。
    ledgerEnabled: over.ledgerEnabled ?? true,
    onOpenTickets: over.onOpenTickets ?? vi.fn(),
    onOpenSettings: over.onOpenSettings ?? vi.fn(),
    onOpenFlows: over.onOpenFlows ?? vi.fn(),
    onOpenProjectCards: over.onOpenProjectCards,
    onOpenProjectCodegraph: over.onOpenProjectCodegraph,
    onAddProject: over.onAddProject ?? vi.fn(),
    // 「显式传 undefined」与「没传」要区分开：右键菜单测试需要 onUnregister
    // 真的是 undefined，`?? vi.fn()` 会把显式 undefined 兜底成 mock
    onUnregister: 'onUnregister' in over ? over.onUnregister : vi.fn(),
    // 与 onUnregister 同理：onEdit 也要能显式传 undefined，验证「没传就不给
    // 编辑入口」的分支
    onEdit: 'onEdit' in over ? over.onEdit : vi.fn(),
    previews: over.previews ?? [],
    previewOpenKeys: over.previewOpenKeys ?? new Set<string>(),
    previewOpeningKeys: over.previewOpeningKeys ?? new Set<string>(),
    onOpenPreview: over.onOpenPreview ?? vi.fn(),
  }
  return p
}

describe('ProjectTree 层级', () => {
  it('项目行存在，折叠箭头在进行中计数之后（DOM 顺序）', () => {
    const { container } = render(<ProjectTree {...props({})} />)
    const node = container.querySelector('[data-testid="project-node-p1"]') as HTMLElement
    const head = within(node).getByRole('button', { name: /handoff/ })
    const marker = within(node).getByTestId('project-marker-p1')
    const count = within(head).getByTestId('project-running-count')
    // 默认全展开，箭头语义是「收起」
    const arrow = within(head).getByLabelText('收起')
    expect(head.contains(count)).toBe(true)
    expect(head.contains(arrow)).toBe(true)
    expect(marker).toHaveClass('absolute', '-left-[15px]', 'top-[11px]', 'size-[9px]')
    // 箭头在计数之后：计数在箭头前面
    expect(count.compareDocumentPosition(arrow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('「任务」「目录」两个小标题行存在', () => {
    render(<ProjectTree {...props({})} />)
    expect(screen.getByTestId('task-group-head')).toHaveTextContent('任务')
    expect(screen.getByTestId('dir-group-head')).toHaveTextContent('目录')
  })

  it('目录默认收起，点击机器行后展开工作树子行', () => {
    render(<ProjectTree {...props({})} />)
    expect(screen.queryByText('integration/b2-b3')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('machine-row'))
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })

  it('项目下任务组跨机器平铺，目录组内再按机器分组', () => {
    const tree: ProjectTreeResp = {
      projects: [{
        project_id: 'p1', origin_url: '', name: 'handoff',
        locations: [
          {
            machine: '', name: 'handoff', path: '/w', probe_error: '',
            workspaces: [{ path: '/w', branch: 'main', head: 'a', is_main: true, managed: false, created_at: '' }],
          },
          {
            machine: 'linux-01', name: 'handoff', path: '/srv/w', probe_error: '',
            workspaces: [{ path: '/srv/w', branch: 'main', head: 'b', is_main: true, managed: false, created_at: '' }],
          },
        ],
      }],
      unowned: [],
    }
    const tasks = [
      task({ id: 'local-task', project_id: 'p1', machine: '', work_dir: '/w', name: '本机任务' }),
      task({ id: 'remote-task', project_id: 'p1', machine: 'linux-01', work_dir: '/srv/w', name: '远端任务' }),
    ]
    render(
      <ProjectTree
        {...props({})}
        tree={tree}
        tasks={tasks}
      />,
    )
    expect(screen.getAllByTestId('task-group')).toHaveLength(1)
    expect(screen.getAllByTestId('directory-group')).toHaveLength(1)
    expect(screen.getAllByTestId('directory-machine-row')).toHaveLength(2)
    expect(screen.getByText('本机任务')).toBeInTheDocument()
    expect(screen.getByText('远端任务')).toBeInTheDocument()
  })
})

describe('ProjectTree 任务组', () => {
  it('preview 是第四种不可拖放任务行，点击只调用 onOpenPreview 并显示本机 open 投影', () => {
    const onOpenPreview = vi.fn()
    const previews: PreviewSession[] = [
      {
        id: 'local-preview', entry_url: 'http://localhost:5173', cwd: '',
        origin_url: 'https://example.test/repo', branch: 'feature/preview', created_at: '', ttl_seconds: 7200,
      },
      {
        id: 'remote-preview', entry_url: 'http://localhost:5174', cwd: '',
        origin_url: 'https://example.test/repo', created_at: '', ttl_seconds: 7200, machine: 'devbox',
      },
    ]
    const p = props({ previews, onOpenPreview, previewOpenKeys: new Set(['\x1flocal-preview']) })
    p.tree.projects[0].origin_url = 'https://example.test/repo/'
    render(<ProjectTree {...p} />)
    const local = screen.getByTestId('preview-row-local-preview')
    expect(local).toHaveTextContent('feature/preview · localhost:5173')
    expect(local).toHaveAttribute('data-open', 'true')
    expect(local).not.toHaveAttribute('data-drag-task')
    fireEvent.click(local)
    expect(onOpenPreview).toHaveBeenCalledWith('local-preview', '')
    expect(p.onOpenTask).not.toHaveBeenCalled()
    expect(screen.getByTestId('preview-row-remote-preview')).toHaveTextContent('devbox')
  })

  it('未终态任务出现在任务组；completed / failed 不在任务组、只在已结束子行', () => {
    const p = props({})
    p.tasks.push(
      task({ id: 'T-done', state: 'completed', work_dir: '/w/gone', name: '已完成任务' }),
      task({ id: 'T-fail', state: 'failed', work_dir: '/w/gone2', name: '已失败任务' }),
    )
    render(<ProjectTree {...p} />)
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.queryByText('已完成任务')).not.toBeInTheDocument()
    expect(screen.queryByText('已失败任务')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('archived-row'))
    expect(screen.getByText('已完成任务')).toBeInTheDocument()
    expect(screen.getByText('已失败任务')).toBeInTheDocument()
  })

  it('已结束行默认收起、计数正确、点击展开；搜索命中时旁路展开', () => {
    const p = props({})
    p.tasks.push(task({ id: 'T-old', state: 'completed', work_dir: '/w/gone', name: '已回收的任务' }))
    const view = render(<ProjectTree {...p} />)
    const row = screen.getByTestId('archived-row')
    expect(row).toHaveAttribute('aria-expanded', 'false')
    expect(within(row).getByText('1')).toBeInTheDocument()
    expect(screen.queryByText('已回收的任务')).not.toBeInTheDocument()
    fireEvent.click(row)
    expect(screen.getByText('已回收的任务')).toBeInTheDocument()

    // 搜索旁路：重新挂载后直接搜任务名，已结束子行自动展开
    view.unmount()
    render(<ProjectTree {...p} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), { target: { value: '已回收' } })
    expect(screen.getByText('已回收的任务')).toBeInTheDocument()
  })

  it('任务行 mousedown 不阻断原生拖放，拖拽写入任务与基准 MIME', () => {
    const onOpenTask = vi.fn()
    render(<ProjectTree {...props({ onOpenTask })} />)
    const row = screen.getByRole('button', { name: /重构工单通道/ })
    const ev = createEvent.mouseDown(row)
    fireEvent(row, ev)
    expect(ev.defaultPrevented).toBe(false)
    const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
    fireEvent.dragStart(row, { dataTransfer })
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_TASK_MIME, 'T1')
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_BASE_MIME, expect.any(String))
    expect(row).toHaveAttribute('data-drag-task', '1')
  })

  it('点任务行同时给出它所在目录与任务 id', () => {
    const onOpenTask = vi.fn()
    render(<ProjectTree {...props({ onOpenTask })} />)
    fireEvent.click(screen.getByText('重构工单通道'))
    expect(onOpenTask).toHaveBeenCalledWith(expect.objectContaining({ key: '/w/b2-b3' }), 'T1')
  })

  it('work_dir 为空的任务（原地模式）出现在任务组', () => {
    render(<ProjectTree {...props({ inPlaceTask: true })} />)
    expect(screen.getByText('原地任务')).toBeInTheDocument()
  })

  it('任务行右端是机器簇（绿点 + 机器名），左侧不额外显示状态点', () => {
    const p = props({})
    p.tasks.push(task({ id: 'T2', project_id: 'p1', machine: 'linux-01', work_dir: '/w/b2-b3', name: '等你答复的活', state: 'waiting_answer' }))
    render(<ProjectTree {...p} />)
    const runningRow = screen.getByRole('button', { name: /重构工单通道/ })
    expect(within(runningRow).getByTestId('task-machine')).toHaveTextContent('本机')
    const waitingRow = screen.getByRole('button', { name: /等你答复的活/ })
    expect(within(waitingRow).getByTestId('task-machine')).toHaveTextContent('linux-01')
    // 任务状态只影响数据语义，不在任务图标与名称之间插入额外圆点；
    // 机器簇保留自己的在线圆点。
    expect(runningRow.querySelectorAll('.bg-state-active').length).toBeGreaterThanOrEqual(1)
    expect(waitingRow.querySelectorAll('.bg-state-intervention')).toHaveLength(0)
  })
})

describe('ProjectTree 已打开项', () => {
  it('openItems 渲染在任务组最前，名字用注入的 name，点击回调带整行数据', () => {
    const onFocusOpenItem = vi.fn()
    const items = [
      openItem({ key: '/w\x1ft9', kind: 'terminal', name: 'bash · main', tabId: 't9' }),
      openItem({ key: '/w\x1ft10', kind: 'file', name: 'go.mod', tabId: 't10' }),
    ]
    render(<ProjectTree {...props({ openItems: items, onFocusOpenItem })} />)
    const names = screen.getAllByTestId('open-item-name').map((el) => el.textContent)
    expect(names).toEqual(['bash · main', 'go.mod'])
    // 已打开行在普通任务行之前
    const firstOpen = screen.getAllByTestId('open-item-row')[0]
    const taskRow = screen.getByRole('button', { name: /重构工单通道/ })
    expect(firstOpen.compareDocumentPosition(taskRow) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    fireEvent.click(screen.getByText('bash · main'))
    expect(onFocusOpenItem).toHaveBeenCalledWith(items[0])
  })

  it('tui 打开项与同 id 任务只出现一行，带 is-open 态；焦点命中行带 is-selected', () => {
    const items = [openItem({
      key: '/w/b2-b3\x1ft11', kind: 'tui', name: '重构工单通道', taskId: 'T1',
      base: { key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3', projectName: 'handoff', machine: '' },
      tabId: 't11',
    })]
    const { container } = render(<ProjectTree {...props({ openItems: items, focusedTaskId: 'T1' })} />)
    // 只有已打开行一处出现（普通任务行不再重复）
    expect(screen.getAllByText('重构工单通道')).toHaveLength(1)
    const openRow = screen.getByTestId('open-item-row')
    expect(openRow).toHaveAttribute('data-open', 'true')
    // 焦点态：focusedTaskId 命中 → 行带 aria-current（is-selected 深一档底色）
    expect(openRow).toHaveAttribute('aria-current', 'true')
    expect(openRow.className).toContain('bg-[#ededed]')
    expect(container).toBeTruthy()
  })

  it('已打开终端/文件行生产可投放到中央的 tab MIME', () => {
    const items = [
      openItem({ key: '/w\x1ft9', kind: 'terminal', name: 'bash · main', tabId: 't9', group: 'g2' }),
      openItem({ key: '/w\x1ft10', kind: 'file', name: 'README.md', tabId: 't10', group: 'g2' }),
    ]
    render(<ProjectTree {...props({ openItems: items })} />)
    const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
    const terminalRow = screen.getByText('bash · main').closest('button')!
    const fileRow = screen.getByText('README.md').closest('button')!
    expect(terminalRow).toHaveAttribute('draggable', 'true')
    expect(fileRow).toHaveAttribute('draggable', 'true')
    fireEvent.dragStart(terminalRow, { dataTransfer })
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_TAB_MIME, JSON.stringify({ groupId: 'g2', tabId: 't9' }))
    expect(dataTransfer.effectAllowed).toBe('move')
    fireEvent.dragStart(fileRow, { dataTransfer })
    expect(dataTransfer.setData).toHaveBeenLastCalledWith(DRAG_TAB_MIME, JSON.stringify({ groupId: 'g2', tabId: 't10' }))
  })

  it('已打开行使用类型图标：tui=资产图、terminal/file=线性图标', () => {
    const items = [
      openItem({ key: '/w\x1ft9', kind: 'tui', name: '审 B264', taskId: 'T1' }),
      openItem({ key: '/w\x1ft10', kind: 'terminal', name: 'bash · main', tabId: 't10' }),
      openItem({ key: '/w\x1ft11', kind: 'file', name: 'go.mod', tabId: 't11' }),
    ]
    const { container } = render(<ProjectTree {...props({ openItems: items })} />)
    expect(container.querySelector('img[src*="dispatch-task"]')).not.toBeNull()
    expect(container.querySelector('.lucide-terminal')).not.toBeNull()
    expect(container.querySelector('.lucide-file-text')).not.toBeNull()
  })
})

describe('ProjectTree 机器行与目录', () => {
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
        openItems={[]}
        focusedTaskId={null}
        onFocusOpenItem={vi.fn()}
        onOpenTerminalAt={vi.fn()}
        onOpenDirectory={vi.fn()}
        onOpenTask={vi.fn()}
        onOpenBoard={vi.fn()}
        onOpenTickets={vi.fn()}
        onOpenSettings={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByTestId('machine-row'))
    expect(screen.getAllByTestId('workspace-row').map((row) => row.textContent?.replace(/\d+$/, ''))).toEqual([
      'main', 'blocked', 'busy', 'quiet',
    ])
  })

  it('机器行悬停动作：主目录存在时两钮齐全', () => {
    render(<ProjectTree {...props({})} onWorktreeCreated={vi.fn()} />)
    expect(screen.getByRole('button', { name: '打开主目录终端' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新建工作树' })).toBeInTheDocument()
  })

  it('机器行悬停动作：主目录不存在时终端钮不渲染', () => {
    const p = props({})
    const tree: ProjectTreeResp = {
      projects: [{
        ...p.tree.projects[0],
        locations: [{ ...p.tree.projects[0].locations[0], workspaces: [] }],
      }],
      unowned: [],
    }
    render(<ProjectTree {...p} tree={tree} onWorktreeCreated={vi.fn()} />)
    expect(screen.queryByRole('button', { name: '打开主目录终端' })).toBeNull()
    expect(screen.getByRole('button', { name: '新建工作树' })).toBeInTheDocument()
  })

  it('悬停终端钮回调带主目录基准；子行悬停终端钮回调带该目录', () => {
    const onOpenTerminalAt = vi.fn()
    render(<ProjectTree {...props({ onOpenTerminalAt })} />)
    fireEvent.click(screen.getByTestId('machine-row'))
    fireEvent.click(screen.getByRole('button', { name: '打开主目录终端' }))
    expect(onOpenTerminalAt).toHaveBeenCalledWith(expect.objectContaining({ path: '/w', label: 'main' }))
    fireEvent.click(screen.getAllByRole('button', { name: '在此打开终端' }).at(-1)!)
    expect(onOpenTerminalAt).toHaveBeenLastCalledWith(expect.objectContaining({ path: '/w/b2-b3' }))
  })

  it('目录来源（工作树子行与机器行）也带拖放放行标记 data-drag-task', () => {
    // P2-1：拖目录到中央同样要关闭窗格内容层的 pointer-events（xterm 截事件），
    // 放行谓词在 WorkbenchPage 只认 data-drag-task，所以机器行/子行也必须带标记
    render(<ProjectTree {...props({})} />)
    fireEvent.click(screen.getByTestId('machine-row'))
    const workspaceRows = screen.getAllByTestId('workspace-row')
    expect(workspaceRows.length).toBeGreaterThanOrEqual(2)
    for (const row of workspaceRows) expect(row).toHaveAttribute('data-drag-task', '1')
    expect(screen.getByTestId('machine-row')).toHaveAttribute('data-drag-task', '1')
  })

  it('点子行选中目录，回调带完整 BaseDir；selectedKey 命中行带 aria-current', () => {
    const onOpenDirectory = vi.fn()
    const view = render(<ProjectTree {...props({ onOpenDirectory, selectedKey: '/w/b2-b3' })} />)
    fireEvent.click(screen.getByTestId('machine-row'))
    fireEvent.click(screen.getByText('integration/b2-b3'))
    expect(onOpenDirectory).toHaveBeenCalledWith({
      key: '/w/b2-b3', kind: 'workspace', path: '/w/b2-b3', label: 'integration/b2-b3', projectName: 'handoff', machine: '',
    })
    expect(screen.getByRole('button', { name: /integration\/b2-b3/ })).toHaveAttribute('aria-current', 'true')
    view.unmount()
  })

  it('detached 的目录用目录名兜底作为 label', () => {
    const onOpenDirectory = vi.fn()
    render(<ProjectTree {...props({ onOpenDirectory, branch: '' })} />)
    fireEvent.click(screen.getByTestId('machine-row'))
    fireEvent.click(screen.getByText('b2-b3'))
    expect(onOpenDirectory).toHaveBeenCalledWith(expect.objectContaining({ label: 'b2-b3' }))
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
        openItems={[]} focusedTaskId={null} onFocusOpenItem={vi.fn()} onOpenTerminalAt={vi.fn()}
        onOpenDirectory={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
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
    render(<ProjectTree tree={tree} tasks={[]} selectedKey={null} ticketCount={0} ticketsByDir={new Map()} openItems={[]} focusedTaskId={null} onFocusOpenItem={vi.fn()} onOpenTerminalAt={vi.fn()} onOpenDirectory={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()} />)
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
        openItems={[]} focusedTaskId={null} onFocusOpenItem={vi.fn()} onOpenTerminalAt={vi.fn()}
        onOpenDirectory={vi.fn()} onOpenTask={vi.fn()} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByText('目录不存在')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(screen.getByText('alpha')).toBeInTheDocument()
  })

  it('点项目行只展开折叠，不再写筛选', () => {
    const onOpenDirectory = vi.fn()
    render(<ProjectTree {...props({ onOpenDirectory })} />)
    fireEvent.click(screen.getByTestId('machine-row'))
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
    // 折叠项目后其下的目录行消失
    fireEvent.click(screen.getByText('handoff'))
    expect(onOpenDirectory).not.toHaveBeenCalled()
    expect(screen.queryByText('integration/b2-b3')).not.toBeInTheDocument()
    // 再点一次恢复展开；机器行的展开态在 project 收起期间原样保留
    fireEvent.click(screen.getByText('handoff'))
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
  })
})

describe('ProjectTree 未归属与搜索', () => {
  it('未归属任务挂在末尾的「未归属」分组，不被吞掉，可拖拽', () => {
    const tree: ProjectTreeResp = { projects: [], unowned: [] }
    const tasks = [task({ id: 'u1', project_id: '', machine: '', work_dir: '/x', name: '游离任务' })]
    const onOpenTask = vi.fn()
    render(
      <ProjectTree
        tree={tree} tasks={tasks} selectedKey={null} ticketCount={0} ticketsByDir={new Map()}
        openItems={[]} focusedTaskId={null} onFocusOpenItem={vi.fn()} onOpenTerminalAt={vi.fn()}
        onOpenDirectory={vi.fn()} onOpenTask={onOpenTask} onOpenBoard={vi.fn()} onOpenTickets={vi.fn()} onOpenSettings={vi.fn()}
      />,
    )
    expect(screen.getByText('未归属')).toBeInTheDocument()
    expect(screen.getByText('游离任务')).toBeInTheDocument()
    expect(screen.getByTestId('task-machine')).toHaveTextContent('本机')
    const dataTransfer = { setData: vi.fn(), effectAllowed: '' }
    fireEvent.dragStart(screen.getByText('游离任务').closest('button')!, { dataTransfer })
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_TASK_MIME, 'u1')
    expect(dataTransfer.setData).toHaveBeenCalledWith(DRAG_BASE_MIME, 'null')
    fireEvent.click(screen.getByText('游离任务'))
    expect(onOpenTask).toHaveBeenCalledWith(null, 'u1')
  })

  it('渲染搜索框与「项目 N」，N 默认是项目总数', () => {
    render(<ProjectTree {...props()} />)
    expect(screen.getByPlaceholderText('搜索项目、机器或任务')).toBeInTheDocument()
    expect(screen.getByText('项目')).toBeInTheDocument()
    expect(screen.getByTestId('project-count')).toHaveTextContent('1')
    expect(screen.getByTestId('project-overview')).toBeVisible()
    expect(screen.getByRole('button', { name: '添加项目' })).toBeVisible()
    expect(screen.getByRole('button', { name: '显示偏好' })).toBeVisible()
  })

  it('搜任务名：该任务可见，无关目录不可见', () => {
    render(<ProjectTree {...props()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), {
      target: { value: '重构工单' },
    })
    expect(screen.getByText('重构工单通道')).toBeInTheDocument()
    expect(screen.queryByText('main')).not.toBeInTheDocument()
  })

  it('搜机器名：该机器行与它的任务可见', () => {
    const p = props({})
    const tree: ProjectTreeResp = {
      projects: [{
        ...p.tree.projects[0],
        locations: [
          ...p.tree.projects[0].locations,
          {
            machine: 'linux-01', name: 'handoff', path: '/srv/w', probe_error: '',
            workspaces: [{ path: '/srv/w', branch: 'main', head: 'b', is_main: true, managed: false, created_at: '' }],
          },
        ],
      }],
      unowned: [],
    }
    const tasks = [
      ...p.tasks,
      task({ id: 'T2', project_id: 'p1', machine: 'linux-01', work_dir: '/srv/w', name: '远端的活' }),
    ]
    render(<ProjectTree {...p} tree={tree} tasks={tasks} />)
    fireEvent.change(screen.getByPlaceholderText('搜索项目、机器或任务'), { target: { value: 'linux-01' } })
    expect(screen.getByText('远端的活')).toBeInTheDocument()
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
    expect(screen.queryByTestId('machine-row')).not.toBeInTheDocument()

    // 搜索期间强制展开
    fireEvent.change(input, { target: { value: 'handoff' } })
    expect(screen.getByTestId('machine-row')).toBeInTheDocument()

    // 清空后折叠态原样回来
    fireEvent.change(input, { target: { value: '' } })
    expect(screen.queryByTestId('machine-row')).not.toBeInTheDocument()
  })

  it('⌘K 聚焦搜索框', () => {
    render(<ProjectTree {...props()} />)
    const input = screen.getByPlaceholderText('搜索项目、机器或任务')
    expect(document.activeElement).not.toBe(input)
    fireEvent.keyDown(window, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(input)
  })

  it('焦点在 xterm textarea 时 ⌘K 不抢搜索', () => {
    render(<ProjectTree {...props()} />)
    const ta = document.createElement('textarea')
    ta.className = 'xterm-helper-textarea'
    document.body.appendChild(ta)
    ta.focus()
    const search = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(ta)
    expect(document.activeElement).not.toBe(search)
    ta.remove()
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
})

describe('ProjectTree 结构细节', () => {
  it('树独立滚动，底部入口不在滚动区内', () => {
    const { container } = render(<ProjectTree {...props()} />)
    const scroller = container.querySelector('[data-testid="tree-scroll"]')!
    expect(scroller.className).toMatch(/overflow-y-auto/)
    expect(scroller.className).toMatch(/min-h-0/)
    const addBtn = screen.getByRole('button', { name: /添加项目/ })
    expect(scroller.contains(addBtn)).toBe(false)
  })

  it('行上不再渲染计数控件（计数只用于排序与折叠判据的内部使用）', () => {
    const onAddProject = vi.fn()
    const { container } = render(<ProjectTree {...props({ onAddProject })} />)
    expect(screen.queryByTitle('开发目录')).toBeNull()
    expect(container.querySelector('[data-testid="machine-row"]')?.textContent).not.toContain('开发目录')
  })

  it('项目图标统一使用原型绿色', () => {
    render(<ProjectTree {...props()} />)
    const icons = Array.from(document.querySelectorAll('[data-project-color]'))
    expect(icons.length).toBeGreaterThan(0)
    expect(new Set(icons.map((icon) => icon.getAttribute('data-project-color')))).toEqual(new Set(['green']))
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
})

describe('dock 入口', () => {
  it('任务看板入口在底部图标区；流程入口可点；设置可点', () => {
    const onOpenBoard = vi.fn()
    const onOpenFlows = vi.fn()
    const onOpenSettings = vi.fn()
    const { container } = render(<ProjectTree {...props({ onOpenBoard, onOpenFlows, onOpenSettings })} />)
    const board = screen.getByRole('button', { name: /任务看板/ })
    const footer = container.querySelector('.border-t')
    expect(footer?.contains(board)).toBe(true)
    fireEvent.click(board)
    expect(onOpenBoard).toHaveBeenCalled()
    fireEvent.click(screen.getByLabelText('流程'))
    expect(onOpenFlows).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '设置' }))
    expect(onOpenSettings).toHaveBeenCalled()
  })

  it('账本未启用时任务看板标题不带未挂账', () => {
    render(<ProjectTree {...props({ ledgerEnabled: false })} />)
    const board = screen.getByRole('button', { name: '任务看板' })
    expect(board).toHaveAttribute('title', '任务看板')
    expect(board.getAttribute('title')).not.toContain('未挂账')
  })

  it('「添加项目」在「项目 N」标题行里，不在底部入口区', () => {
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
    const ticketBtn = screen.getByRole('button', { name: /^工单$/ })
    expect(ticketBtn).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '设置' })).toBeInTheDocument()
    expect(ticketBtn.querySelector('.bg-state-intervention')).toBeNull()
  })

  it('工单数大于 0 时显示角标并可点开', () => {
    const onOpenTickets = vi.fn()
    render(<ProjectTree {...props({ ticketCount: 3, onOpenTickets })} />)
    expect(screen.getByText('3')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^工单$/ }))
    expect(onOpenTickets).toHaveBeenCalled()
  })

  it('工单角标用状态 token，不用裸 amber', () => {
    const { container } = render(<ProjectTree {...props({ ticketCount: 3 })} />)
    const badge = screen.getByText('3')
    expect(badge.className).toContain('bg-state-intervention')
    expect(container.innerHTML).not.toContain('bg-amber-500')
  })

  it('机器行右端没有常驻的注销按钮', () => {
    const { container } = render(<ProjectTree {...props({ onUnregister: vi.fn() })} />)
    expect(container.querySelector('[aria-label="注销"]')).toBeNull()
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
    const p = props({})
    const tasks = p.tasks.map((t) => ({ ...t, state: 'done' }))
    render(<ProjectTree {...p} tasks={tasks} />)
    fireEvent.click(screen.getByRole('button', { name: '显示偏好' }))
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /隐藏无活跃任务的工作树/ }))
    fireEvent.click(screen.getByTestId('machine-row'))
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
    fireEvent.click(screen.getByTestId('machine-row'))
    expect(screen.getByText('main')).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
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
