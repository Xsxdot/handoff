// 账本页的呈现契约：项目级请示要说得清自己是谁、且不能藏在筛选后面；
// 建卡入口传下去的项目必须来自当前视图而不是列表首张卡（B179）；
// 卡到任务深链的管线要真通（B181）。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { Task } from '../../api/types'
import { CardsPage } from './CardsPage'
import { CARD_STATUSES } from './statusVocab'

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  // CardsPage 自 B181 起挂了 useTasks()；不 mock 会在 jsdom 里发真实请求
  fetchTasks: vi.fn().mockResolvedValue([]),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
  fetchCardDetail: vi.fn(),
  fetchFlow: vi.fn(),
  fetchFlows: vi.fn().mockResolvedValue({ workflows: [], templates: [] }),
  fetchLedgerHealth: vi.fn().mockResolvedValue({ mirror: [] }),
  fetchDecisions: vi.fn().mockResolvedValue([
    { id: 2, card_id: '', body: '要不要先把 acc/ 临时分支清掉？', options: null, status: 'open', answer: '', created_by: 'cli:me@box' },
  ]),
}))

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  getQueue: vi.fn().mockResolvedValue({ queue: [] }),
}))

// 建卡对话框换成桩：这里要验的是 CardsPage 往下传了什么，不是对话框自己怎么渲染
vi.mock('./NewCardDialog', () => ({
  NewCardDialog: (props: Record<string, unknown>) => (
    <div data-testid="new-card-dialog-stub" data-project={String(props.project)} />
  ),
}))

// CardsPage 用 useNavigate，必须包在 Router 里渲染（生产态 Shell 把它挂在 <Routes> 下）
const renderPage = (entry = '/cards') =>
  render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/cards" element={<CardsPage />} />
        {/* 深链探针：只断言导航真的发生了，不复刻 TaskDeepLink 的目录解析逻辑 */}
        <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
      </Routes>
    </MemoryRouter>,
  )

describe('项目级请示横幅', () => {
  it('不开「需要你」筛选也要显示——它被算进了徽标，藏起来等于数字对不上', async () => {
    renderPage()
    expect(await screen.findByText(/要不要先把 acc\/ 临时分支清掉？/)).toBeInTheDocument()
  })

  it('要标明它不挂卡，否则贴在卡片列上方像是某张卡的', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText(/项目级请示/)).toBeInTheDocument())
    expect(screen.getByText(/不挂卡/)).toBeInTheDocument()
  })
})

describe('看板排队工具条', () => {
  it('挂载独立队列轮询并按服务端快照显示数量', async () => {
    const scheduling = await import('../../api/scheduling')
    vi.mocked(scheduling.getQueue).mockResolvedValue({
      queue: [{
        kind: 'launch_queue', id: 'q1', card: 'B1', node: '进行中', squad: 'exec',
        priority: '高', ready: false, actor: 'wake', seq: 7, position: 1,
      }],
    })
    renderPage()
    expect(await screen.findByRole('button', { name: '⧗ 排队中 1' })).toBeInTheDocument()
    expect(scheduling.getQueue).toHaveBeenCalled()
  })

  it('按 queue.poll 契约记录轮询事件和字段', async () => {
    const scheduling = await import('../../api/scheduling')
    const info = vi.spyOn(console, 'info').mockImplementation(() => undefined)
    vi.mocked(scheduling.getQueue).mockResolvedValue({
      queue: [{ kind: 'launch_queue', id: 'q1', card: 'B1', squad: 'exec', ready: true, actor: 'wake', seq: 7, position: 1 }],
    })
    try {
      renderPage()
      expect(await screen.findByRole('button', { name: '⧗ 排队中 1' })).toBeInTheDocument()
      expect(info).toHaveBeenCalledWith('queue.poll.start', { intervalMs: 5000 })
      expect(info).toHaveBeenCalledWith('queue.poll.success', { count: 1, stale: false })
      expect(info.mock.calls.some(([event]) => typeof event === 'string' && event.startsWith('cards.queue'))).toBe(false)
    } finally {
      info.mockRestore()
    }
  })
})

describe('建卡入口接线', () => {
  const cardView = {
    id: 'B187', title: '现场铁证', status: '待办', priority: '中', project: 'benchmarking',
    workflow: 'feature', parent: '', base_branch: '', attachments: [], following: '',
    blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0,
    children_total: 0, children_done: 0, conflict: false, open_tickets: 0,
  }

  it('「全部项目」下传给对话框的 project 是空串——不再拿 cards[0].project 当兜底（B187 回归网）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [cardView],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    renderPage()
    const stub = await screen.findByTestId('new-card-dialog-stub')
    expect(stub.dataset.project).toBe('')
  })

  it('从 URL 的 project 查询参数初始化项目筛选', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [cardView],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    renderPage('/cards?project=benchmarking')
    expect(await screen.findByRole('combobox', { name: '项目' })).toHaveValue('benchmarking')
  })
})

describe('卡到任务深链的数据通路', () => {
  // 必填字段集与 CardDrawer.test.tsx 的同名夹具同一份真相（api/types.ts:15-42）
  const task = (over: Partial<Task> = {}): Task => ({
    id: 'task-wire', target: 'local', repo_path: '/repo/handoff', branch: '',
    plan_path: '', plan_summary: '', executor_session: '', state: 'running',
    created_at: '', updated_at: '', name: '', executor: '', model: '',
    work_dir: '', worktree_managed: false, base_commit: '', base_ahead: 0,
    repo_dirty_count: 0, repo_dirty_files: '', done_note: '', machine: '', project_id: '', ...over,
  })
  // 列表接口消费 CardView，抽屉详情消费 Card；同一夹具覆盖两侧已有字段，
  // 以当前 ledger/client 的真实类型作为接缝检查，而不是用类型断言绕过它。
  const wireCard = {
    id: 'B50', title: '管线卡', status: '进行中', priority: '中', project: 'handoff',
    parent: '', workflow: '', workflow_version: 1, attachments: [], acceptance_criteria: '',
    created_at: '', updated_at: '', base_branch: '', following: '', blocked: false,
    blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0,
    children_done: 0, conflict: false, open_tickets: 0,
  }

  it('抽屉里的 ↗ 经由 CardsPage 注入的回调真的走到 /tasks/:id', async () => {
    const ledger = await import('../../api/ledger')
    const client = await import('../../api/client')
    vi.mocked(client.fetchTasks).mockResolvedValue([task()])
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [wireCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: wireCard,
      relations: [],
      events: [],
      task_states: [{ Target: 'local', TaskID: 'task-wire', Purpose: 'implement', LastType: 'question', LastSeq: 3 }],
      effective_base_branch: '', decisions: [], needs: '',
    })
    renderPage()
    fireEvent.click(await screen.findByText('管线卡')) // 看板上点开抽屉
    fireEvent.click(await screen.findByRole('button', { name: '跳到 task-wire' }))
    // 整条管线：useTasks → CardDrawer.tasks → 行内 ↗ → navigate('/tasks/task-wire')
    expect(await screen.findByText('deep-link-hit')).toBeInTheDocument()
  })
})

describe('房间面板卡片深链', () => {
  it('/cards?card=B50 会直接打开对应卡片抽屉', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [{
      id: 'B50', title: '房间入口卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
      merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
      conflict: false, open_tickets: 0,
    }], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: {
        id: 'B50', title: '房间入口卡', status: '进行中', priority: '中', project: 'handoff', parent: '',
        workflow: '', workflow_version: 1, attachments: [], acceptance_criteria: '', created_at: '', updated_at: '',
      }, relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
    })
    renderPage('/cards?card=B50')
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
  })

  it('终态卡的 /cards?card= 深链自动带 all=1 并打开抽屉', async () => {
    const ledger = await import('../../api/ledger')
    const terminalCard = {
      id: 'Bdone', title: '已归档房间卡', status: '已完成', priority: '中', project: 'handoff', workflow: '',
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
      merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
      conflict: false, open_tickets: 0,
    }
    vi.mocked(ledger.fetchCards).mockImplementation((params = '') => Promise.resolve({
      cards: params === 'all=1' ? [terminalCard] : [],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    }))
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: {
        id: 'Bdone', title: '已归档房间卡', status: '已完成', priority: '中', project: 'handoff', parent: '',
        workflow: '', workflow_version: 1, attachments: [], acceptance_criteria: '', created_at: '', updated_at: '',
      }, relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
    })
    renderPage('/cards?card=Bdone')
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(vi.mocked(ledger.fetchCards)).toHaveBeenCalledWith('all=1')
  })
})

describe('可配置看板列入口', () => {
  it('选中工作流时按其看板映射渲染五列', async () => {
    const ledger = await import('../../api/ledger')
    const baseCard = {
      id: 'B200', title: '普通卡', status: '待办', priority: '中', project: 'p', workflow: 'custom', parent: '', base_branch: '', attachments: [], following: '',
      blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0,
      children_total: 0, children_done: 0, conflict: false, open_tickets: 0,
    }
    vi.mocked(ledger.fetchFlows).mockResolvedValue({
      workflows: [{
        name: 'custom', version: 2,
        def: {
          states: ['待办'],
          board: {
            columns: ['收集', '沟通', '实现', '验收', '完成'],
            state_to_column: { 待办: '收集' }, fallback: '实现',
          },
        },
      }],
      templates: [],
    })
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [{
        ...baseCard, title: '自定义看板卡',
      }],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    renderPage()
    await screen.findByText('自定义看板卡')
    fireEvent.change(screen.getByRole('combobox', { name: '工作流' }), { target: { value: 'custom' } })
    expect(await screen.findByText('收集')).toBeInTheDocument()
    expect(screen.getByText('完成')).toBeInTheDocument()
  })
})

describe('卡片节点标签版本来源', () => {
  it('按卡片钉住的工作流版本取节点集，不借用最新版给旧卡贴标签', async () => {
    const ledger = await import('../../api/ledger')
    const oldCard = {
      id: 'B201', title: '旧版本卡', status: '待审阅', priority: '中', project: 'p', workflow: 'custom', workflow_version: 1,
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0,
      needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0,
    }
    vi.mocked(ledger.fetchFlows).mockResolvedValue({
      workflows: [{ name: 'custom', version: 2, def: { states: ['待办', '进行中', '待审阅'] } }],
      templates: [],
    })
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [oldCard],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    vi.mocked(ledger.fetchFlow).mockImplementation(async (name, version) => ({
      name,
      version: version ?? 0,
      states: version === 1 ? ['待办', '进行中', '待审阅', '待合并'] : ['待办', '进行中', '待审阅'],
      nodes: version === 1
        ? [{ name: '待办' }, { name: '进行中' }, { name: '待审阅' }, { name: '待合并' }]
        : [{ name: '待办' }, { name: '进行中' }, { name: '待审阅' }],
    }))

    renderPage()
    const card = (await screen.findByText('旧版本卡')).closest('article')
    expect(card).not.toBeNull()
    // B287 状态唯一化：右上角单枚 chip 承载节点标签（v1 节点集多对一列 → 显形），
    // 标签行不再重复——旧断言数到 2（右上角文本 + 标签行 pill）已随本卡变更。
    await waitFor(() => expect(within(card!).getAllByText('待审阅')).toHaveLength(1))
    expect(within(card!).getAllByText('待审阅')[0]!.className).toContain('bg-slate-900')
    expect(vi.mocked(ledger.fetchFlow)).toHaveBeenCalledWith('custom', 1)
  })

  it('列表缺 workflow_version 时不猜最新版，也不加载无版本节点集', async () => {
    const ledger = await import('../../api/ledger')
    const legacyCard = {
      id: 'B202', title: '缺版本卡', status: '待审阅', priority: '中', project: 'p', workflow: 'custom',
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0,
      needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0,
    }
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [legacyCard],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ...legacyCard, workflow_version: 0, acceptance_criteria: '', created_at: '', updated_at: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
    })
    vi.mocked(ledger.fetchFlow).mockClear()

    renderPage()
    fireEvent.click(await screen.findByText('缺版本卡'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(vi.mocked(ledger.fetchFlow)).not.toHaveBeenCalled()
  })
})

describe('事件流滞后灯', () => {
  it('全归档的 target 即使心跳很旧也不亮——没东西可镜像不算断链', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchLedgerHealth).mockResolvedValue({
      enabled: true,
      mirror: [{ Target: 'mac-02', LastSeq: 6594, UpdatedAt: '2020-01-01T00:00:00.000Z', Live: false }],
    })
    renderPage()
    await waitFor(() => expect(ledger.fetchLedgerHealth).toHaveBeenCalled())
    expect(screen.queryByText(/事件流滞后/)).not.toBeInTheDocument()
    expect(screen.getByTitle('镜像正常')).toBeInTheDocument()
  })

  it('仍有在飞挂账且心跳过期要点名是哪台', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchLedgerHealth).mockResolvedValue({
      enabled: true,
      mirror: [{ Target: 'linux-01', LastSeq: 1, UpdatedAt: '2020-01-01T00:00:00.000Z', Live: true }],
    })
    renderPage()
    expect(await screen.findByText('事件流滞后: linux-01')).toBeInTheDocument()
  })
})

const DESKTOP_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 handoff-desktop'
const BROWSER_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Safari/605.1.15'

function setUA(ua: string): void {
  Object.defineProperty(navigator, 'userAgent', { value: ua, configurable: true })
}

function bridge(): { webkit?: unknown } {
  return window as unknown as { webkit?: unknown }
}

describe('CardsPage 从浏览器打开', () => {
  afterEach(() => {
    delete bridge().webkit
    setUA(BROWSER_UA)
    window.history.replaceState({}, '', '/')
  })

  it('桌面 UA 显示按钮，点击发送当前 /cards query 且不离开页面', async () => {
    setUA(DESKTOP_UA)
    window.history.replaceState({}, '', '/cards?project=handoff')
    const postMessage = vi.fn()
    bridge().webkit = { messageHandlers: { external: { postMessage } } }

    renderPage('/cards?project=handoff')
    const button = screen.getByRole('button', { name: '从浏览器打开' })
    expect(button).toHaveClass('ml-auto')
    expect(screen.getByTitle('镜像正常')).not.toHaveClass('ml-auto')

    fireEvent.click(button)

    expect(postMessage).toHaveBeenCalledTimes(1)
    expect(postMessage).toHaveBeenCalledWith(
      `handoff:open-browser:${window.location.origin}/cards?project=handoff`,
    )
    expect(window.location.pathname + window.location.search).toBe('/cards?project=handoff')
    expect(screen.getByText('工作项')).toBeInTheDocument()
  })

  it('普通浏览器不渲染按钮且健康点仍占右侧', async () => {
    setUA(BROWSER_UA)
    renderPage('/cards?project=handoff')

    expect(screen.queryByRole('button', { name: '从浏览器打开' })).toBeNull()
    expect(screen.getByTitle('镜像正常')).toHaveClass('ml-auto')
  })
})

describe('B369.6 状态词表筛选（移动卡 tab）', () => {
  const cardView = (over: Partial<import('../../api/ledger').CardView>) => ({
    id: 'B1', title: '卡', status: '待办', priority: '中', project: 'handoff', workflow: 'feature',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  it('chip 行逐值渲染受控词表的五个状态', async () => {
    renderPage()
    for (const status of ['待办', '进行中', '待审阅', '已完成', '终止']) {
      expect(await screen.findByTestId(`card-status-${status}`)).toBeInTheDocument()
    }
  })

  it('点状态 chip 只留该状态；再点一次回全部', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [
        cardView({ id: 'B1', title: '已完成卡', status: '已完成' }),
        cardView({ id: 'B2', title: '进行中卡', status: '进行中' }),
      ],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    } as never)
    renderPage()
    fireEvent.click(await screen.findByTestId('card-status-已完成'))
    // 断言钉在标题而非卡号：队列面板等别处的 fixture 也可能带同样的 B 号，
    // 用卡号会撞到跨用例残留 mock（假红）。
    expect(await screen.findByText('已完成卡')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('进行中卡')).toBeNull())
    fireEvent.click(screen.getByTestId('card-status-已完成'))
    expect(await screen.findByText('进行中卡')).toBeInTheDocument()
  })
})

// —— B369.8 T3：compact 单列扫描面 + 三行头部 ——
// 桌面零漂移的证据 = 本文件全部既有 describe（桌面头部/看板/列表）全绿；
// 这里只锁 compact 分支自身的形状。
describe('B369.8 compact 单列', () => {
  const compactCard = (over: Partial<import('../../api/ledger').CardView> = {}) => ({
    id: 'B1', title: '单列卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  async function renderCompact(entry = '/cards') {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [compactCard()], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    render(
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact />} />
          <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByTestId('cards-single-column')
  }

  it('单列在场（CardItem 纵堆）；看板横滚容器/ListView/视图切换一律不渲染', async () => {
    await renderCompact()
    expect(screen.getByText('单列卡')).toBeInTheDocument()
    // compact 的扫描面只有单列：视图切换是无消费者的死控件，看板横滚容器不渲染
    expect(screen.queryByRole('button', { name: '看板' })).toBeNull()
    expect(screen.queryByRole('button', { name: '列表' })).toBeNull()
    expect(document.querySelector('main .overflow-x-auto')).toBeNull()
  })

  it('「⚑ 需要你」在行 1 且计数含项目级请示（默认 mock 1 条不挂卡裁决）', async () => {
    await renderCompact()
    const needsButton = await screen.findByRole('button', { name: /⚑ 需要你 1/ })
    const row1 = needsButton.closest('div')!
    // 行 1（主控）= 工作项标题 + 健康灯 + 需要你；ml-auto 推到行尾
    expect(row1.textContent).toContain('工作项')
    expect(needsButton.className).toContain('min-h-11')
    expect(needsButton.className).toContain('ml-auto')
  })

  it('行 2 状态 chips 逐值渲染 CARD_STATUSES 词表；行 3 次级四控件在场且 min-h-11', async () => {
    await renderCompact()
    for (const status of CARD_STATUSES) {
      expect(screen.getByTestId(`card-status-${status}`)).toBeInTheDocument()
    }
    const secondary = screen.getByTestId('cards-controls-secondary')
    expect(within(secondary).getByRole('combobox', { name: '项目' })).toBeInTheDocument()
    expect(within(secondary).getByRole('combobox', { name: '工作流' })).toBeInTheDocument()
    expect(within(secondary).getByPlaceholderText('搜 B 号 / 标题')).toBeInTheDocument()
    expect(within(secondary).getByRole('button', { name: '+ 新建' })).toBeInTheDocument()
    for (const control of within(secondary).getAllByRole('combobox')) {
      expect(control.className).toContain('min-h-11')
    }
  })

  it('main 根挂触点基线类（24px 次级底线）', async () => {
    await renderCompact()
    expect(document.querySelector('main')!.className).toContain('button:not(.min-h-11)')
  })
})

describe('B369.8 compact 抽屉 a11y（cards-surface 与焦点归还）', () => {
  const surfaceCard = {
    id: 'B1', title: '面卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0,
  }

  async function renderCompactSurface(entry = '/cards') {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [surfaceCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: surfaceCard, relations: [], events: [], task_states: [],
      effective_base_branch: '', decisions: [], needs: '',
    } as never)
    render(
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact />} />
          <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByTestId('cards-single-column')
  }

  it('抽屉开 → surface aria-hidden="true"+inert；关 → 撤覆盖属性', async () => {
    await renderCompactSurface()
    const surface = screen.getByTestId('cards-surface')
    // 覆盖前：aria-hidden="false"（与 WorkbenchPage 先例同款 boolean 写法）、无 inert
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
    fireEvent.click(screen.getByText('面卡'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(surface.getAttribute('aria-hidden')).toBe('true')
    expect(surface.hasAttribute('inert')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
  })

  it('关闭归还焦点到打开抽屉的触发钮（isConnected 守卫路径）', async () => {
    await renderCompactSurface()
    // 触发钮先取（抽屉开后卡标题在抽屉里重复出现，getByText 会撞多元素）
    const trigger = screen.getByText('面卡').closest('article')!
    trigger.focus()
    fireEvent.click(trigger)
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    // 开抽屉焦点移入面板（compact aria-modal 档）
    expect(document.activeElement).toBe(drawer)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(document.activeElement).toBe(trigger)
  })

  it('桌面反例锁：抽屉开时 cards-surface 不存在（双栏可达性显式反例）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [surfaceCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: surfaceCard, relations: [], events: [], task_states: [],
      effective_base_branch: '', decisions: [], needs: '',
    } as never)
    render(
      <MemoryRouter initialEntries={['/cards']}>
        <Routes>
          <Route path="/cards" element={<CardsPage />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('面卡'))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(screen.queryByTestId('cards-surface')).toBeNull()
    expect(document.querySelector('main')!.className).not.toContain('button:not(.min-h-11)')
  })
})

// —— B369.8 review 必修：头部三行并入 cards-surface 硬闸 ——
describe('B369.8 compact 头部硬闸（review round 2）', () => {
  it('抽屉覆盖期「+ 新建」「⚑ 需要你」落于 aria-hidden="true"+inert 的 cards-surface 祖先内；关时闸未挂', async () => {
    const ledger = await import('../../api/ledger')
    const surfaceCard = {
      id: 'B1', title: '闸卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
      merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
      conflict: false, open_tickets: 0,
    }
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [surfaceCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: surfaceCard, relations: [], events: [], task_states: [],
      effective_base_branch: '', decisions: [], needs: '',
    } as never)
    render(
      <MemoryRouter initialEntries={['/cards']}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact />} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByTestId('cards-single-column')
    const surface = screen.getByTestId('cards-surface')
    // 头部控件在包装内且此时闸未挂（可查 → 未覆盖）
    const newBtn = within(surface).getByRole('button', { name: '+ 新建' })
    const needsBtn = within(surface).getByRole('button', { name: /⚑ 需要你/ })
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
    fireEvent.click(screen.getByText('闸卡'))
    await screen.findByRole('dialog', { name: '工作项详情' })
    // 覆盖期：同一包装挂上三件套，头部控件就在其内——读屏/键盘/指针三路同断
    expect(surface.getAttribute('aria-hidden')).toBe('true')
    expect(surface.hasAttribute('inert')).toBe(true)
    expect(surface.contains(newBtn)).toBe(true)
    expect(surface.contains(needsBtn)).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
  })
})
