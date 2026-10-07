// B427 exercises the production page/drawer at the API boundary: detail requests stay bounded.
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { ApiError } from '../../api/client'
import type { CardDetail, CardView, FlowDetail } from '../../api/ledger'
import { CardsPage } from './CardsPage'
vi.mock('../../api/client', async (original) => ({ ...(await original<typeof import('../../api/client')>()), fetchTasks: vi.fn().mockResolvedValue([]) }))
vi.mock('../../api/ledger', async (original) => ({
  ...(await original<typeof import('../../api/ledger')>()), fetchCards: vi.fn(), fetchCardDetail: vi.fn(), fetchFlow: vi.fn(),
  fetchFlows: vi.fn().mockResolvedValue({ workflows: [], templates: [] }), fetchLedgerHealth: vi.fn().mockResolvedValue({ enabled: true, mirror: [] }), fetchDecisions: vi.fn().mockResolvedValue([]),
}))
vi.mock('../../api/scheduling', async (original) => ({ ...(await original<typeof import('../../api/scheduling')>()), getQueue: vi.fn().mockResolvedValue({ queue: [] }), getCoordinatorStatus: vi.fn().mockResolvedValue({ bound: false }) }))
const ledger = await import('../../api/ledger')
const card = (over: Partial<CardView> = {}): CardView => ({
  id: 'Bactive', title: '现役工作项', status: '待审阅', priority: '中', project: 'handoff', workflow: '', parent: '', base_branch: '', attachments: [], following: '', blocked: false,
  blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0, ...over,
})
const unlinked = { count: 0, tasks: [], unknown_targets: [] }
const detail = (view: CardView): CardDetail => ({ card: { ...view, workflow_version: view.workflow_version ?? 1, acceptance_criteria: '', created_at: '', updated_at: '' }, relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '' })
const flow: FlowDetail = { name: 'feature', version: 1, states: ['待审阅', '钉版本独有'], nodes: [{ name: '待审阅' }, { name: '待合并' }] }
function MobilePage() { const navigate = useNavigate(); return <><button onClick={() => navigate(-1)}>history back</button><button onClick={() => navigate('/cards?card=Bnew')}>new deep link</button><CardsPage compact onDrawerCardChange={(id) => navigate(id ? `/cards?card=${id}` : '/cards')} /></> }
const mount = (entry = '/cards', compact = true) => render(<MemoryRouter initialEntries={[entry]}>{compact ? <MobilePage /> : <CardsPage />}</MemoryRouter>)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [card()], unlinked })
  vi.mocked(ledger.fetchCardDetail).mockImplementation(async (id) => detail(card({ id, title: id === 'Bdone' ? '终态工作项' : '现役工作项' })))
  vi.mocked(ledger.fetchFlow).mockResolvedValue(flow)
})
afterEach(() => { vi.useRealTimers() })
describe('B427 bounded detail requests', () => {
  it('mobile click/URL/close keeps the current range and issues one initial list fetch', async () => {
    vi.useFakeTimers(); mount(); fireEvent.click(await screen.findByText('现役工作项'))
    await screen.findByRole('dialog', { name: '工作项详情' })
    await waitFor(() => expect(ledger.fetchCardDetail).toHaveBeenCalledTimes(1))
    expect(ledger.fetchCards).toHaveBeenCalledTimes(1)
    expect(ledger.fetchCards).not.toHaveBeenCalledWith('all=1')
    // URL scope must stay bounded on the next production polling tick as well.
    await act(async () => { await vi.advanceTimersByTimeAsync(2500) })
    expect(ledger.fetchCards).toHaveBeenCalledTimes(2)
    expect(ledger.fetchCards).not.toHaveBeenCalledWith('all=1')
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.getByText('现役工作项')).toBeInTheDocument(); expect(ledger.fetchCards).toHaveBeenCalledTimes(2)
  })
  it('terminal deep link fetches one detail without scanning historical cards', async () => {
    mount('/cards?card=Bdone')
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    expect(await within(drawer).findByText('终态工作项')).toBeInTheDocument()
    expect(ledger.fetchCardDetail).toHaveBeenCalledTimes(1); expect(ledger.fetchCardDetail).toHaveBeenCalledWith('Bdone')
    expect(ledger.fetchCards).not.toHaveBeenCalledWith('all=1')
    fireEvent.click(within(drawer).getByRole('button', { name: '关闭' })); expect(screen.getByText('现役工作项')).toBeInTheDocument()
  })
  it('unknown deep link renders the existing detail error', async () => {
    vi.mocked(ledger.fetchCardDetail).mockRejectedValue(new ApiError(404, 'card missing')); mount('/cards?card=missing')
    expect(await screen.findByText('card missing')).toBeInTheDocument(); expect(ledger.fetchCards).not.toHaveBeenCalledWith('all=1')
  })
  it('URL history back closes a deep link without reloading or clearing desktop local selection', async () => {
    render(<MemoryRouter initialEntries={['/cards', '/cards?card=Bdone']} initialIndex={1}><MobilePage /></MemoryRouter>)
    await screen.findByText('终态工作项')
    fireEvent.click(screen.getByRole('button', { name: 'history back' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '工作项详情' })).toBeNull())
    expect(screen.getByText('现役工作项')).toBeInTheDocument()
    expect(ledger.fetchCards).toHaveBeenCalledTimes(1)
  })
  it('a delayed old detail cannot replace a newer deep link or its pinned metadata', async () => {
    let oldResult!: (value: CardDetail) => void
    vi.mocked(ledger.fetchCardDetail).mockImplementation((id) => id === 'Bdone' ? new Promise((resolve) => { oldResult = resolve }) : Promise.resolve(detail(card({ id, title: '新详情' }))))
    mount('/cards?card=Bdone')
    await waitFor(() => expect(ledger.fetchCardDetail).toHaveBeenCalledWith('Bdone'))
    fireEvent.click(screen.getByRole('button', { name: 'new deep link' }))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    await within(drawer).findByText('新详情')
    await act(async () => { oldResult(detail(card({ id: 'Bdone', title: '过期详情', workflow: 'old', workflow_version: 1 }))) })
    expect(within(drawer).getByText('新详情')).toBeInTheDocument()
    expect(screen.queryByText('过期详情')).toBeNull()
    expect(ledger.fetchFlow).not.toHaveBeenCalledWith('old', 1)
  })
  it('desktop selection stays current; explicit historical range fetches immediately', async () => {
    mount('/cards', false); fireEvent.click(await screen.findByText('现役工作项')); await screen.findByRole('dialog', { name: '工作项详情' })
    expect(ledger.fetchCards).not.toHaveBeenCalledWith('all=1'); fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    fireEvent.click(screen.getByRole('button', { name: '列表' })); fireEvent.click(screen.getByRole('checkbox'))
    await waitFor(() => expect(ledger.fetchCards).toHaveBeenCalledWith('all=1'))
  })
})
describe('B427 pinned workflow partial failure', () => {
  it('keeps successful pinned drawer actions when another version is 404 and does not refetch either on polls', async () => {
    const views = [card({ workflow: 'feature', workflow_version: 1 }), card({ id: 'Bgone', title: '缺工作流', workflow: 'removed', workflow_version: 3 })]
    vi.mocked(ledger.fetchCards).mockImplementation(async () => ({ cards: [...views], unlinked }))
    vi.mocked(ledger.fetchFlow).mockImplementation(async (name) => { if (name === 'removed') throw new ApiError(404, 'version missing'); return flow })
    vi.useFakeTimers(); mount(); await screen.findByText('现役工作项')
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) }); expect(ledger.fetchFlow).toHaveBeenCalledTimes(2)
    fireEvent.click(screen.getByText('现役工作项')); const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    fireEvent.click(await within(drawer).findByRole('button', { name: '转移状态…' }))
    expect(await within(drawer).findByRole('option', { name: '钉版本独有' })).toBeInTheDocument()
    expect(ledger.fetchFlow).toHaveBeenCalledTimes(2)
    fireEvent.click(within(drawer).getByRole('button', { name: '关闭' }))
    fireEvent.click(screen.getByText('缺工作流'))
    await screen.findByRole('dialog', { name: '工作项详情' })
    await act(async () => {})
    expect(ledger.fetchFlow).toHaveBeenCalledTimes(2)
  })
  it('network failures stay retryable and recover pinned labels on a later card poll', async () => {
    vi.mocked(ledger.fetchCards).mockImplementation(async () => ({ cards: [card({ workflow: 'feature', workflow_version: 1 })], unlinked }))
    vi.mocked(ledger.fetchFlow).mockRejectedValueOnce(new ApiError(0, 'offline')).mockResolvedValue(flow)
    vi.useFakeTimers(); mount(); await screen.findByText('现役工作项'); await waitFor(() => expect(ledger.fetchFlow).toHaveBeenCalledTimes(1))
    await act(async () => { await vi.advanceTimersByTimeAsync(2500) }); expect(ledger.fetchFlow).toHaveBeenCalledTimes(2)
    fireEvent.click(screen.getByText('现役工作项'))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    fireEvent.click(await within(drawer).findByRole('button', { name: '转移状态…' }))
    expect(await within(drawer).findByRole('option', { name: '钉版本独有' })).toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(ledger.fetchFlow).toHaveBeenCalledTimes(2)
  })
  it('401 is terminal within this lifetime and never treated as a cached missing version', async () => {
    vi.mocked(ledger.fetchCards).mockImplementation(async () => ({ cards: [card({ workflow: 'feature', workflow_version: 1 })], unlinked }))
    vi.mocked(ledger.fetchFlow).mockRejectedValue(new ApiError(401, 'expired'))
    vi.useFakeTimers(); mount(); await screen.findByText('现役工作项')
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(ledger.fetchFlow).toHaveBeenCalledTimes(1)
  })
  it('late standalone workflow results cannot overwrite the injected Shell lifetime', async () => {
    const current = card({ workflow: 'feature', workflow_version: 1 })
    vi.mocked(ledger.fetchCards).mockResolvedValue({ cards: [current], unlinked })
    vi.mocked(ledger.fetchCardDetail).mockResolvedValue(detail(current))
    let oldResult!: (value: FlowDetail) => void
    vi.mocked(ledger.fetchFlow).mockImplementationOnce(() => new Promise((done) => { oldResult = done })).mockResolvedValue({ ...flow, states: ['待审阅', '新来源目标态'] })
    const page = render(<MemoryRouter><CardsPage compact /></MemoryRouter>)
    await screen.findByText('现役工作项')
    await waitFor(() => expect(ledger.fetchFlow).toHaveBeenCalledTimes(1))
    const state = <T,>(data: T) => ({ data, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() })
    page.rerender(<MemoryRouter><CardsPage compact sharedData={{ cards: state({ cards: [current], unlinked }), decisions: state([]), tasks: state([]) }} /></MemoryRouter>)
    await waitFor(() => expect(ledger.fetchFlow).toHaveBeenCalledTimes(2))
    await act(async () => { oldResult({ ...flow, states: ['待审阅', '旧来源目标态'] }) })
    fireEvent.click(screen.getByText('现役工作项'))
    const drawer = await screen.findByRole('dialog', { name: '工作项详情' })
    fireEvent.click(await within(drawer).findByRole('button', { name: '转移状态…' }))
    expect(await within(drawer).findByRole('option', { name: '新来源目标态' })).toBeInTheDocument()
    expect(within(drawer).queryByRole('option', { name: '旧来源目标态' })).toBeNull()
  })

})

describe('B427 page expiry presentation', () => {
  it('standalone cards first-load 401 uses the existing expiry banner instead of endless loading', async () => {
    vi.mocked(ledger.fetchCards).mockRejectedValueOnce(new ApiError(401, 'expired'))
    mount()
    expect(await screen.findByText('会话已失效，请重新打开控制台')).toBeInTheDocument()
    expect(screen.queryByText('正在读取账本…')).toBeNull()
    expect(screen.queryByText('（没有匹配的工作项）')).toBeNull()
  })
  it.each(['decisions', 'tasks'] as const)('an expired injected %s stream explicitly marks retained data', async (expiredStream) => {
    const state = <T,>(data: T, expired = false) => ({ data, disconnected: false, sessionExpired: expired, errorText: '', refresh: vi.fn() })
    render(<MemoryRouter><CardsPage compact sharedData={{ cards: state({ cards: [card()], unlinked }), decisions: state([], expiredStream === 'decisions'), tasks: state([], expiredStream === 'tasks') }} /></MemoryRouter>)
    expect(await screen.findByText('现役工作项')).toBeInTheDocument()
    expect(screen.getByText('会话已失效，请重新打开控制台')).toBeInTheDocument()
    expect(screen.getByText('保留上次获取的数据，当前状态尚未确认。')).toBeInTheDocument()
    expect(ledger.fetchCards).not.toHaveBeenCalled()
  })
})
