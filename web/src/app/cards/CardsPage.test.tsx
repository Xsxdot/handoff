// 账本页的呈现契约：项目级请示要说得清自己是谁、且不能藏在筛选后面；
// 卡到任务深链的管线要真通（B181）。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { Task } from '../../api/types'
import { CardsPage } from './CardsPage'

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  // CardsPage 自 B181 起挂了 useTasks()；不 mock 会在 jsdom 里发真实请求
  fetchTasks: vi.fn().mockResolvedValue([]),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn().mockResolvedValue({ cards: [], unlinked: { count: 0, tasks: [], unknown_targets: [] } }),
  fetchCardDetail: vi.fn(),
  fetchFlows: vi.fn().mockResolvedValue({ workflows: [], templates: [] }),
  fetchLedgerHealth: vi.fn().mockResolvedValue({ mirror: [] }),
  fetchDecisions: vi.fn().mockResolvedValue([
    { id: 2, card_id: '', body: '要不要先把 acc/ 临时分支清掉？', options: null, status: 'open', answer: '', created_by: 'cli:me@box' },
  ]),
}))

// CardsPage 用 useNavigate，必须包在 Router 里渲染（生产态 Shell 把它挂在 <Routes> 下）
const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/cards']}>
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
