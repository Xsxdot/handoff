// 账本页的呈现契约：项目级请示要说得清自己是谁、且不能藏在筛选后面；
// 建卡入口传下去的项目必须来自当前视图而不是列表首张卡（B179）；
// 卡到任务深链的管线要真通（B181）。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { Task } from '../../api/types'
import { CardsPage } from './CardsPage'
import { DEFAULT_BOARD_COLUMNS } from './columns'

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  // CardsPage 自 B181 起挂了 useTasks()；不 mock 会在 jsdom 里发真实请求
  fetchTasks: vi.fn().mockResolvedValue([]),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
  fetchCardDetail: vi.fn(),
  answerDecision: vi.fn().mockResolvedValue(undefined),
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
  afterEach(async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchDecisions).mockResolvedValue([
      { id: 2, card_id: '', body: '要不要先把 acc/ 临时分支清掉？', options: null, status: 'open', answer: '', created_by: 'cli:me@box' },
    ])
    vi.mocked(ledger.answerDecision).mockClear()
  })

  it('compact 项目级裁决选项只填多行草稿，显式答复才提交', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchDecisions).mockResolvedValue([
      { id: 91, card_id: '', body: '是否保留当前范围？', options: ['保留范围', '缩小范围'], status: 'open', answer: '', created_by: 'cli:me@box' },
    ] as never)
    render(<MemoryRouter><CardsPage compact /></MemoryRouter>)
    const textarea = await screen.findByPlaceholderText('答复这条请示…')
    expect(textarea.tagName).toBe('TEXTAREA')
    await userEvent.click(screen.getByRole('button', { name: '保留范围' }))
    expect(textarea).toHaveValue('保留范围')
    expect(vi.mocked(ledger.answerDecision)).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '答复' }))
    await waitFor(() => expect(vi.mocked(ledger.answerDecision)).toHaveBeenCalledWith(91, '保留范围'))
  })

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

describe('未挂账观测状态', () => {
  it('未拿到观测时明确显示未知，不伪装成当前零值', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [],
      unlinked: { status: 'unavailable', observed_at: null, count: 0, tasks: [], unknown_targets: ['linux-01'] },
    })
    renderPage()
    const row = await screen.findByTestId('unlinked-summary-row')
    expect(row).toHaveAttribute('data-status', 'unavailable')
    expect(row).toHaveTextContent('摘要暂无可用观测')
    expect(row).toHaveTextContent('不能判断当前是否为零')
    expect(row).toHaveTextContent('linux-01')
  })

  it('部分结果标明子集和未知目标', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [],
      unlinked: {
        status: 'partial', observed_at: new Date().toISOString(), count: 1,
        tasks: [{ target: 'mac-02', task_id: 'T1', title: '待挂账任务', state: 'running' }],
        unknown_targets: ['linux-01'],
      },
    })
    renderPage()
    const row = await screen.findByTestId('unlinked-summary-row')
    expect(row).toHaveAttribute('data-status', 'partial')
    expect(row).toHaveTextContent('部分观测')
    expect(row).toHaveTextContent('未知目标: linux-01')
    expect(row).toHaveTextContent('待挂账任务')
  })

  it('超过五分钟的历史结果仍显示时间并注明不是当前数量', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [],
      unlinked: {
        status: 'stale', observed_at: '2020-01-01T00:00:00Z', count: 1,
        tasks: [{ target: 'mac-02', task_id: 'T1', title: '旧观测任务', state: 'running' }],
        unknown_targets: [],
      },
    })
    renderPage()
    const row = await screen.findByTestId('unlinked-summary-row')
    expect(row).toHaveAttribute('data-status', 'stale')
    expect(row).toHaveTextContent('上次观测：')
    expect(row).toHaveTextContent('当前数量尚未确认')
    expect(row).toHaveTextContent('2020-01-01 00:00:00 UTC')
    expect(row).toHaveTextContent('已过')
    expect(row).toHaveTextContent('旧观测任务')
  })

  it('缺失或无效的观测时间不会把 latest 当成当前数值', async () => {
    const ledger = await import('../../api/ledger')
    for (const observed_at of [undefined, 'not-a-time']) {
      vi.mocked(ledger.fetchCards).mockResolvedValue({
        cards: [],
        unlinked: {
          status: 'latest', observed_at, count: 1,
          tasks: [{ target: 'mac-02', task_id: 'T1', title: '无法确认任务', state: 'running' }],
          unknown_targets: [],
        },
      })
      const { unmount } = renderPage()
      const row = await screen.findByTestId('unlinked-summary-row')
      expect(row).toHaveAttribute('data-status', 'unavailable')
      expect(row).toHaveTextContent('不能判断当前是否为零')
      expect(row).not.toHaveTextContent('无法确认任务')
      unmount()
    }
  })

  it('最新合法空结果不占用横幅', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [],
      unlinked: { status: 'latest', observed_at: new Date().toISOString(), count: 0, tasks: [], unknown_targets: [] },
    })
    renderPage()
    await waitFor(() => expect(ledger.fetchCards).toHaveBeenCalled())
    expect(screen.queryByTestId('unlinked-summary-row')).not.toBeInTheDocument()
  })

  it('stale 零值也不刷主面（过 TTL 的空观测别冒「未挂账 task 0」）', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [],
      unlinked: {
        status: 'stale', observed_at: '2020-01-01T00:00:00Z', count: 0, tasks: [], unknown_targets: [],
      },
    })
    renderPage()
    await waitFor(() => expect(ledger.fetchCards).toHaveBeenCalled())
    expect(screen.queryByTestId('unlinked-summary-row')).not.toBeInTheDocument()
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
    // B413：轮询数据改经 startTransition 提交后晚一帧落 DOM——值断言改重试等待
    // （React 会在 options 后到的提交里重放 select value，协调者裁决 2026-09-28）
    const combobox = await screen.findByRole('combobox', { name: '项目' })
    await waitFor(() => expect(combobox).toHaveValue('benchmarking'))
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

  it('终态卡的 /cards?card= 深链按ID打开抽屉，不扩大历史范围', async () => {
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
    expect(vi.mocked(ledger.fetchCards)).not.toHaveBeenCalledWith('all=1')
    expect(vi.mocked(ledger.fetchCardDetail)).toHaveBeenCalledWith('Bdone')
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
    // S3（B426）后单流 chips 也渲染该流列名，裸 getByText 会撞 chip——列头断言
    // 收紧到 section header span（语义更新，断言意图不变：看板按该流映射渲染列）。
    expect(await screen.findByText('收集', { selector: 'section header span' })).toBeInTheDocument()
    expect(screen.getByText('完成', { selector: 'section header span' })).toBeInTheDocument()
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

describe('B369.6 状态 chip（S3 后：看板五列语义，B426）', () => {
  const cardView = (over: Partial<import('../../api/ledger').CardView>) => ({
    id: 'B1', title: '卡', status: '待办', priority: '中', project: 'handoff', workflow: 'feature',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  it('chip 行逐值渲染看板五列（displayedColumns，与桌面看板同源）', async () => {
    renderPage()
    for (const column of DEFAULT_BOARD_COLUMNS) {
      expect(await screen.findByTestId(`card-status-${column}`)).toBeInTheDocument()
    }
  })

  it('点列 chip 只留该列（未知流卡按默认映射归桶）；再点一次回全部', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [
        cardView({ id: 'B1', title: '已完成卡', status: '已完成' }),
        cardView({ id: 'B2', title: '进行中卡', status: '进行中' }),
      ],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    } as never)
    renderPage()
    // flows 未含 'feature' 流 → 未知流兜底：已完成 → 结束列、进行中 → 进行中列
    fireEvent.click(await screen.findByTestId('card-status-结束'))
    // 断言钉在标题而非卡号：队列面板等别处的 fixture 也可能带同样的 B 号，
    // 用卡号会撞到跨用例残留 mock（假红）。
    expect(await screen.findByText('已完成卡')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('进行中卡')).toBeNull())
    fireEvent.click(screen.getByTestId('card-status-结束'))
    expect(await screen.findByText('进行中卡')).toBeInTheDocument()
  })
})

// —— S3（B426）：状态 chip 以看板五列为准，过滤走真实归桶解析链（mergedLayoutFor /
// layoutForWorkflow），不用 mock 布局顶替；必含「工作流状态卡归入语义桶」与
// 「未知流兜底进行中」两条，双端同断言（唯一动桌面的例外，spec §5）。 ——
describe('S3 状态 chip 归看板五列（B426，双端）', () => {
  const charterCard = (over: Record<string, unknown> = {}) => ({
    id: 'B1', title: '卡', status: '待办', priority: '中', project: 'p', workflow: 'charter',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  const charterFlows = {
    workflows: [{
      name: 'charter', version: 12,
      def: {
        states: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish'],
        board: {
          columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
          state_to_column: {
            spec: '沟通中', review: '审核中', acceptance: '审核中', integrate: '审核中',
            图对账: '审核中', finish: '结束', 待办: '代办', plan: '进行中', implement: '进行中',
          },
          fallback: '进行中',
        },
      },
    }],
    templates: [],
  }

  const mockCharter = async (cards: Record<string, unknown>[]) => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchFlows).mockResolvedValue(charterFlows)
    vi.mocked(ledger.fetchFlow).mockImplementation(async (name, version) => ({
      name, version: version ?? 0,
      states: name === 'charter' ? charterFlows.workflows[0].def.states : [],
      nodes: name === 'charter' ? charterFlows.workflows[0].def.states.map((node) => ({ name: node })) : [],
    }))
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards, unlinked: { count: 0, tasks: [], unknown_targets: [] },
    } as never)
  }

  it('必含①：工作流状态卡归入语义桶——charter 流 spec 卡点「沟通中」chip 后可见（桌面 header chips）', async () => {
    await mockCharter([
      charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec', workflow_version: 12 }),
      charterCard({ id: 'Br', title: 'review 卡', status: 'review', workflow_version: 12 }),
    ])
    renderPage()
    fireEvent.click(await screen.findByTestId('card-status-沟通中'))
    expect(await screen.findByText('spec 卡')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('review 卡')).toBeNull())
  })

  it('必含①（compact 同断言）：compact 行 2 chips 同样把 spec 卡归「沟通中」', async () => {
    await mockCharter([
      charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec', workflow_version: 12 }),
      charterCard({ id: 'Br', title: 'review 卡', status: 'review', workflow_version: 12 }),
    ])
    render(
      <MemoryRouter initialEntries={['/cards']}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact />} />
          <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByTestId('card-status-沟通中'))
    expect(await screen.findByText('spec 卡')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('review 卡')).toBeNull())
  })

  it('必含②：未知流兜底——flows 未含该卡 workflow 且状态非映射串 → 归「进行中」chip', async () => {
    await mockCharter([
      // workflow 'ghost-flow' 不在 flows；status 'mystery-state' 不在任何映射表
      charterCard({ id: 'Bg', title: '幽灵卡', status: 'mystery-state', workflow: 'ghost-flow', workflow_version: 1 }),
      charterCard({ id: 'Bt', title: '待办卡', status: '待办', workflow_version: 12 }),
    ])
    renderPage()
    fireEvent.click(await screen.findByTestId('card-status-进行中'))
    // 幽灵卡走 mergedLayoutFor 默认兜底落「进行中」；真待办卡不在此列
    expect(await screen.findByText('幽灵卡')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('待办卡')).toBeNull())
    // 点「代办」chip → 幽灵卡不可见（证明兜底确落在进行中，而非无过滤）。
    // 注意列名是现行词表「代办」（错字在账本工作流定义内，本卡不改）。
    fireEvent.click(screen.getByTestId('card-status-代办'))
    await waitFor(() => expect(screen.queryByText('幽灵卡')).toBeNull())
    expect(screen.getByText('待办卡')).toBeInTheDocument()
  })

  it('点已选 chip 取消回全部（toggle 原样保留）', async () => {
    await mockCharter([
      charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec', workflow_version: 12 }),
      charterCard({ id: 'Bd', title: '待办卡', status: '待办', workflow_version: 12 }),
    ])
    renderPage()
    fireEvent.click(await screen.findByTestId('card-status-沟通中'))
    await waitFor(() => expect(screen.queryByText('待办卡')).toBeNull())
    fireEvent.click(screen.getByTestId('card-status-沟通中'))
    expect(await screen.findByText('待办卡')).toBeInTheDocument()
  })
})

describe('B412 合并视图按各流当前版本看板配置归列', () => {
  const charterCard = (over: Record<string, unknown> = {}) => ({
    id: 'B1', title: '卡', status: '待办', priority: '中', project: 'p', workflow: 'charter',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  const charterFlows = {
    workflows: [{
      name: 'charter', version: 12,
      def: {
        states: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish'],
        board: {
          columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
          state_to_column: {
            spec: '沟通中', review: '审核中', acceptance: '审核中', integrate: '审核中',
            图对账: '审核中', finish: '结束', 待办: '代办', plan: '进行中', implement: '进行中',
          },
          fallback: '进行中',
        },
      },
    }],
    templates: [],
  }
  const columnSection = (name: string): HTMLElement => {
    // 只认列头里的列名：状态词表 chip 行也渲染「进行中」等词，裸 getByText 会撞多元素。
    const headerSpan = screen.getByText(name, { selector: 'section header span' })
    const section = headerSpan.closest('section')
    if (!section) throw new Error(`找不到看板列 ${name}`)
    return section
  }

  it('全部工作流视图按各流当前版本 board 归列、列恒五列，抽屉列胶囊与看板一致', async () => {
    const ledger = await import('../../api/ledger')
    // 卡钉 v9 → 会拉 v9 的节点集（动作面）；board 归属仍看当前 v12。这里给一份可解析的 v9 详情，
    // 避免 fetchFlow 解析到 undefined 触发未处理拒绝。
    vi.mocked(ledger.fetchFlow).mockResolvedValue({
      name: 'charter', version: 9,
      states: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish'],
      nodes: ['待办', 'spec', 'plan', 'implement', 'review', 'acceptance', 'integrate', '图对账', 'finish']
        .map((name) => ({ name })),
    })
    vi.mocked(ledger.fetchFlows).mockResolvedValue(charterFlows)
    // spec 卡钉 v9（v9 无 board）——D1：列归属看当前 v12，不看钉版本。
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: [
        charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec', workflow_version: 9 }),
        charterCard({ id: 'Br', title: 'review 卡', status: 'review', workflow_version: 12 }),
        charterCard({ id: 'Bf', title: 'finish 卡', status: 'finish', workflow_version: 12 }),
        charterCard({ id: 'Bp', title: 'plan 卡', status: 'plan', workflow_version: 12 }),
      ],
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: { ...charterCard({ id: 'Bs', title: 'spec 卡', status: 'spec' }), workflow_version: 9, acceptance_criteria: '', created_at: '', updated_at: '' },
      relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
    })

    renderPage()
    expect(await screen.findByText('spec 卡')).toBeInTheDocument()
    expect(within(columnSection('沟通中')).getByText('spec 卡')).toBeInTheDocument()
    expect(within(columnSection('审核中')).getByText('review 卡')).toBeInTheDocument()
    expect(within(columnSection('结束')).getByText('finish 卡')).toBeInTheDocument()
    expect(within(columnSection('进行中')).getByText('plan 卡')).toBeInTheDocument()

    // 抽屉列胶囊与看板一致：点开 spec 卡 → 「沟通中」高亮。
    fireEvent.click(screen.getByText('spec 卡'))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    expect(within(drawer).getByText('沟通中').className).toContain('bg-primary')
  })
})

// —— S4（B426）+ B429：次级筛选收进抽屉；项目筛回主面状态栏下；+ 新建升行 1、
// 「从浏览器打开」保留（落点=行 1 尾，执行者裁量记台账）、QueuePanel 细横条。 ——
describe('S4 卡页头部收敛（B426）', () => {
  const compactCard = (over: Partial<import('../../api/ledger').CardView> = {}) => ({
    id: 'B1', title: '卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  async function renderCompactWithCards(cards: Partial<import('../../api/ledger').CardView>[]) {
    const ledger = await import('../../api/ledger')
    const views = cards.map((over) => ({ ...compactCard(), ...over }) as import('../../api/ledger').CardView)
    vi.mocked(ledger.fetchCards).mockResolvedValue({
      cards: views,
      unlinked: { count: 0, tasks: [], unknown_targets: [] },
    })
    vi.mocked(ledger.fetchCardDetail).mockImplementation(async (id) => {
      const selected = views.find((view) => view.id === id)
      if (!selected) throw new Error(`fixture card missing: ${id}`)
      return {
        card: { ...selected, workflow_version: selected.workflow_version ?? 1, acceptance_criteria: '', created_at: '', updated_at: '' },
        relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
      }
    })
    render(
      <MemoryRouter initialEntries={['/cards']}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact />} />
          <Route path="/tasks/:id" element={<p>deep-link-hit</p>} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByTestId('cards-single-column')
  }

  it('compact queue count remains unknown until CardsPage receives its first snapshot; confirmed empty hides strip', async () => {
    const scheduling = await import('../../api/scheduling')
    let resolveQueue!: (value: { queue: [] }) => void
    vi.mocked(scheduling.getQueue).mockReturnValue(new Promise((resolve) => { resolveQueue = resolve }))
    await renderCompactWithCards([{}])
    expect(screen.getByRole('button', { name: '⧗ 排队中 …' })).toBeInTheDocument()
    resolveQueue({ queue: [] })
    await waitFor(() => expect(screen.queryByRole('button', { name: /⧗ 排队中/ })).toBeNull())
    vi.mocked(scheduling.getQueue).mockResolvedValue({ queue: [] })
  })

  it('compact 主面有弱项目下拉；无工作流/搜索/「筛选与执行工具」；+ 新建升行 1；「从浏览器打开」保留', async () => {
    setUA(DESKTOP_UA)
    await renderCompactWithCards([{}])
    // B429：项目筛在顶栏（与需要你/＋一排）；工作流/搜索仍不在主面；无筛选与执行工具入口
    const row1 = screen.getByTestId('cards-primary-actions')
    expect(within(row1).getByTestId('cards-project-filter')).toBeInTheDocument()
    expect(within(row1).getByRole('combobox', { name: '项目' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '工作流' })).toBeNull()
    expect(screen.queryByPlaceholderText('搜 B 号 / 标题')).toBeNull()
    expect(screen.queryByRole('button', { name: '筛选与执行工具' })).toBeNull()
    // + 新建升行 1
    const newButton = screen.getByRole('button', { name: '新建工作项' })
    expect(newButton.closest('div')!.textContent).toContain('工作项')
    expect(screen.getByTestId('cards-primary-actions').closest('main')).toHaveClass('mobile-cards-page')
    expect(screen.getByRole('button', { name: /需要你处理|正在读取需要你/ })).toHaveClass('mobile-needs-action')
    // 「从浏览器打开」落行 1（桌面壳 UA）
    expect(screen.getByRole('button', { name: '从浏览器打开' })).toBeInTheDocument()
  })

  it('主面弱项目下拉未开卡可筛；次级（工作流/搜索）只在抽屉；关闭后筛选 state 保留', async () => {
    const user = userEvent.setup()
    await renderCompactWithCards([{ id:'B1',title:'甲项目卡',project:'alpha' },{ id:'B2',title:'乙项目卡',project:'beta' }])
    expect(screen.queryByRole('button', { name: '筛选与执行工具' })).toBeNull()
    // B429：项目在顶栏，未开卡即可筛
    const row1 = screen.getByTestId('cards-primary-actions')
    const projectSelect = within(row1).getByRole('combobox', { name: '项目' })
    expect(projectSelect.closest('[data-testid="cards-project-filter"]')).toBeTruthy()
    await user.selectOptions(projectSelect, 'beta')
    expect(screen.queryByText('甲项目卡')).toBeNull()
    expect(screen.getByText('乙项目卡')).toBeInTheDocument()
    // 次级仍在抽屉；项目不在抽屉顶
    fireEvent.click(screen.getByText('乙项目卡'))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    expect(within(drawer).queryByRole('combobox', { name: '项目' })).toBeNull()
    expect(within(drawer).getByRole('combobox', { name: '工作流' })).toBeInTheDocument()
    expect(within(drawer).getByPlaceholderText('搜 B 号 / 标题')).toBeInTheDocument()
    fireEvent.click(within(drawer).getByRole('button', { name: '返回工作项' }))
    // 关闭后顶栏项目筛仍保留
    expect(within(screen.getByTestId('cards-primary-actions')).getByRole('combobox', { name: '项目' })).toHaveValue('beta')
    expect(screen.queryByText('甲项目卡')).toBeNull()
    expect(screen.getByText('乙项目卡')).toBeInTheDocument()
  })

  it('QueuePanel compact 细横条：收缩态无大边框盒；展开仍列完整队列', async () => {
    const scheduling = await import('../../api/scheduling')
    vi.mocked(scheduling.getQueue).mockResolvedValue({
      queue: [{
        kind: 'launch_queue', id: 'q1', card: 'B1', node: '', squad: 'exec',
        priority: '', ready: true, actor: 'wake', seq: 1, position: 1,
      }],
    })
    await renderCompactWithCards([{}])
    const toggle = screen.getByRole('button', { name: '⧗ 排队中 1' })
    const strip = toggle.closest('section')!
    // 细横条：无大边框圆角盒（原型 mobile-cards 无此盒）；与头部行同为 border-b 条
    expect(strip.className).toContain('border-b')
    expect(strip.className).not.toContain('rounded-lg')
    // 展开仍列队列
    await userEvent.setup().click(toggle)
    expect(await screen.findByRole('button', { name: '打开 B1' })).toBeInTheDocument()
  })
})

// —— B369.8 T3：compact 单列扫描面 + 头部行（S4 后行 3 撤出）——
// 桌面零漂移的证据 = 本文件全部既有 describe（桌面头部/看板/列表）全绿；
// 这里只锁 compact 分支自身的形状。
describe('B369.8 compact 单列', () => {
  const compactCard = (over: Partial<import('../../api/ledger').CardView> = {}) => ({
    id: 'B1', title: '单列卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
    parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
    merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
    conflict: false, open_tickets: 0, ...over,
  })

  async function renderCompact(entry = '/cards', cardOverrides: Partial<import('../../api/ledger').CardView> = {}) {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [compactCard(cardOverrides)], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
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

  it('「需要你」在行 1 且计数含项目级请示；健康态不占扫描面', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.fetchLedgerHealth).mockResolvedValue({ enabled: true, mirror: [] })
    await renderCompact()
    const needsButton = await screen.findByRole('button', { name: /需要你处理 1/ })
    const row1 = screen.getByTestId('cards-primary-actions')
    expect(row1.textContent).toContain('工作项')
    expect(row1.textContent).not.toContain('事件流滞后')
    await waitFor(() => expect(screen.queryByTestId('cards-ledger-health')).toBeNull())
    expect(needsButton.className).toContain('mobile-needs-action')
    expect(needsButton.className).toContain('whitespace-nowrap')
    expect(screen.getByRole('button', { name: '新建工作项' })).toBeVisible()
  })

  it('行 2 状态 chips 逐值渲染看板五列；+ 新建升行 1；顶栏弱项目下拉，工作流/搜索仍不在主面', async () => {
    await renderCompact()
    for (const column of DEFAULT_BOARD_COLUMNS) {
      expect(screen.getByTestId(`card-status-${column}`)).toBeInTheDocument()
    }
    // S4：+ 新建升行 1（与「工作项」标题同一行容器）
    const newButton = screen.getByRole('button', { name: '新建工作项' })
    expect(newButton.closest('div')!.textContent).toContain('工作项')
    // B429：顶栏有弱项目下拉；五字独占下行；工作流/搜索仍不在主面
    expect(screen.queryByTestId('cards-controls-secondary')).toBeNull()
    const row1 = screen.getByTestId('cards-primary-actions')
    expect(within(row1).getByTestId('cards-project-filter')).toBeInTheDocument()
    expect(within(row1).getByRole('combobox', { name: '项目' })).toBeInTheDocument()
    expect(within(row1).queryByTestId('card-status-filter')).toBeNull()
    expect(screen.queryByRole('combobox', { name: '工作流' })).toBeNull()
    expect(screen.queryByPlaceholderText('搜 B 号 / 标题')).toBeNull()
  })

  it('main 根挂触点基线类（24px 次级底线）', async () => {
    await renderCompact()
    expect(document.querySelector('main')!.className).toContain('touch-baseline')
  })

  it('列表把较长的等人原文保留为两行预览；卡行无⋯，迁移进详情更多操作', async () => {
    const longNeeds = '需要先阅读完整验收描述并确认服务事件流保持一致；这一段原文要完整保留供详情查看。'.repeat(3)
    const ledger = await import('../../api/ledger')
    const card = { id: 'B1', title: '单列卡', status: '进行中', priority: '中', project: 'handoff', workflow: '', parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0, needs: longNeeds, open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0 }
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [card], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card, relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: longNeeds,
    } as never)
    render(<MemoryRouter initialEntries={['/cards']}><Routes><Route path="/cards" element={<CardsPage compact />} /></Routes></MemoryRouter>)
    await screen.findByTestId('cards-single-column')
    const preview = screen.getByText(longNeeds)
    expect(preview.className).toContain('mobile-card-needs-preview')
    expect(preview.closest('article')!.querySelector('details.mobile-card-actions')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: `B1 ${card.title}` }))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    fireEvent.click(within(drawer).getByText('更多工作项操作'))
    expect(within(drawer).getByRole('button', { name: '迁移工作项' })).toBeInTheDocument()
  })

  it('列表把卡号/状态放在标题上方，标题可读两行且主按钮不再带冗余箭头', async () => {
    const longTitle = '移动工作项标题在窄屏上完整阅读并保留关键上下文'
    await renderCompact('/cards', { title: longTitle })
    const main = screen.getByRole('button', { name: `B1 ${longTitle}` })
    const article = main.closest('article')!
    const title = article.querySelector('.mobile-card-title')!
    expect(title.textContent).toBe(longTitle)
    expect(title.className).toContain('mobile-card-title')
    expect(article.querySelector('.mobile-card-identity')?.textContent).toContain('B1')
    expect(article.querySelector('.mobile-card-identity')?.textContent).toContain('进行中')
    expect(main.querySelector('svg')).toBeNull()
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
    fireEvent.click(screen.getByRole('button', { name: 'B1 面卡' }))
    expect(await screen.findByRole('dialog', { name: '工作项详情' })).toBeInTheDocument()
    expect(surface.getAttribute('aria-hidden')).toBe('true')
    expect(surface.hasAttribute('inert')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '返回工作项' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
  })

  it('关闭归还焦点到打开抽屉的触发钮（isConnected 守卫路径）', async () => {
    await renderCompactSurface()
    // 触发钮先取（抽屉开后卡标题在抽屉里重复出现，getByText 会撞多元素）
    const trigger = screen.getByRole('button', { name: 'B1 面卡' })
    trigger.focus()
    fireEvent.click(trigger)
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    // 开抽屉焦点移入面板（compact aria-modal 档）
    expect(document.activeElement).toBe(drawer)
    fireEvent.click(screen.getByRole('button', { name: '返回工作项' }))
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
    expect(document.querySelector('main')!.className).not.toContain('touch-baseline')
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
    const newBtn = within(surface).getByRole('button', { name: '新建工作项' })
    const needsBtn = within(surface).getByRole('button', { name: /需要你处理/ })
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'B1 闸卡' }))
    await screen.findByRole('dialog', { name: '工作项详情' })
    // 覆盖期：同一包装挂上三件套，头部控件就在其内——读屏/键盘/指针三路同断
    expect(surface.getAttribute('aria-hidden')).toBe('true')
    expect(surface.hasAttribute('inert')).toBe(true)
    expect(surface.contains(newBtn)).toBe(true)
    expect(surface.contains(needsBtn)).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '返回工作项' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(surface.getAttribute('aria-hidden')).toBe('false')
    expect(surface.hasAttribute('inert')).toBe(false)
  })
})

// —— B369.10 T8 seam 冒烟：卡 → 驾驶会话双跳（CardsPage 注入 → 抽屉行）——
describe('B369.10 卡到会话双跳 seam', () => {
  it('compact 抽屉「驾驶会话」行点击 → onOpenSessionForCard(cardId)', async () => {
    const ledger = await import('../../api/ledger')
    const jumpCard = {
      id: 'B1', title: '跳卡', status: '进行中', priority: '中', project: 'handoff', workflow: '',
      parent: '', base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
      merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
      conflict: false, open_tickets: 0, driver_session: 'session:7',
    }
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [jumpCard], unlinked: { count: 0, tasks: [], unknown_targets: [] } })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue({
      card: jumpCard, relations: [], events: [], task_states: [],
      effective_base_branch: '', decisions: [], needs: '',
    } as never)
    const onOpenSessionForCard = vi.fn()
    render(
      <MemoryRouter initialEntries={['/cards']}>
        <Routes>
          <Route path="/cards" element={<CardsPage compact onOpenSessionForCard={onOpenSessionForCard} />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('跳卡'))
    fireEvent.click(await screen.findByTestId('card-jump-session'))
    expect(onOpenSessionForCard).toHaveBeenCalledWith('B1')
  })
})

describe('mobile data availability guard', () => {
  it('disables new work items before first load and during disconnected or expired state', () => {
    const refresh = vi.fn()
    const poll = { data:null, disconnected:false, sessionExpired:false, errorText:'', refresh }
    render(<MemoryRouter><CardsPage compact sharedData={{cards:poll,decisions:poll,tasks:poll}}/></MemoryRouter>)
    expect(screen.getByRole('button',{name:'新建工作项'})).toBeDisabled()
    expect(screen.getByRole('button',{name:'正在读取需要你处理状态'})).toBeDisabled()
    expect(screen.getByTestId('cards-primary-actions').textContent).toContain('…')
    expect(screen.queryByTestId('cards-ledger-health')).toBeNull()
  })
})
