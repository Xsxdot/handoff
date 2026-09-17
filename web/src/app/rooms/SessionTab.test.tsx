// SessionTab.test.tsx —— tab 窗格宿主：⋯ 右侧抽屉（B358.8 #3，chat↔detail 双态退役）、
// 打开即已读、拉卡入口移位（footer 工具钮）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { archiveSession, fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import { fetchCards } from '../../api/ledger'
import { openSessionDetail } from './sessionDetailOpener'
import type { RoomHistoryItem, SessionDetail, SessionSummary } from '../../api/rooms'
import fixture from '../../api/testdata/RoomsFixture.json'
import { SessionTab } from './SessionTab'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchSessionDetail: vi.fn(),
  fetchRoomMessages: vi.fn(),
  markRoomRead: vi.fn(),
  joinSessionCard: vi.fn(),
  archiveSession: vi.fn(),
}))
vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCards: vi.fn(),
}))

const cases = fixture as { case: string; detail?: SessionDetail; summary?: SessionSummary }[]
// 详情载荷用 golden 的 nodes/timeline，summary 换成带成员与卡的投影（群主标识/卡列表行）。
const detailOf = () => {
  const base = cases.find((c) => c.case === 'session-detail-golden')!.detail!
  const withMembers = cases.find((c) => c.case === 'session-summary-golden')!.summary!
  return { ...base, summary: withMembers } satisfies SessionDetail
}
const event = (seq: number, body: string): RoomHistoryItem => ({
  seq, card_id: '', type: 'room_message', actor: 'cli:opencode#s1',
  payload: { room: 'session:7', kind: 'user', body }, created_at: '2026-09-12T00:00:00Z',
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchSessionDetail).mockResolvedValue(detailOf())
  vi.mocked(fetchRoomMessages).mockResolvedValue([event(7, '收到')])
  vi.mocked(markRoomRead).mockResolvedValue({ ok: true })
  vi.mocked(joinSessionCard).mockResolvedValue({ ok: true })
  vi.mocked(archiveSession).mockResolvedValue({ ok: true })
  vi.mocked(fetchCards).mockResolvedValue({
    cards: [
      { id: 'B1', title: '竖切卡', status: '进行中', priority: 'P2', project: 'handoff', workflow: 'charter', parent: '', base_branch: 'main', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0 },
      { id: 'B233.14', title: '编制入站封界', status: '进行中', priority: 'P2', project: 'handoff', workflow: 'charter', parent: '', base_branch: 'main', attachments: [], following: '', blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0, conflict: false, open_tickets: 0 },
    ],
    unlinked: { tasks: [] },
  } as never)
})

describe('SessionTab', () => {
  it('头部精简：无标题行、无拉卡钮、无 ⋯（⋯ 住窗格标题行，标题唯一来源不变）', async () => {
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByRole('textbox', { name: '发送消息' })
    expect(screen.queryByText('架构物理化')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '返回群聊' })).toBeNull()
    // 拉卡入口唯一：SessionChat footer 的工具钮（头部按钮已删）
    expect(screen.getAllByRole('button', { name: '拉卡进群' })).toHaveLength(1)
    // ⋯ 住窗格标题行（WorkbenchPage 渲染），本组件内不再渲染
    expect(screen.queryByRole('button', { name: '会话详情' })).toBeNull()
  })

  it('⋯ 开右侧抽屉：群主标识、卡列表行回调 onOpenCard，抽屉打开时会话消息仍可见', async () => {
    const onOpenCard = vi.fn()
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" onOpenCard={onOpenCard} />)
    await screen.findByRole('textbox', { name: '发送消息' })
    // ⋯ 住窗格标题行：经注册表投递开启（标题行按钮的组件半边见 WorkbenchPage.test）
    act(() => { openSessionDetail('session:7') })
    const drawer = await screen.findByTestId('session-drawer')
    expect(within(drawer).getAllByText(/user:sy|无人推/).length).toBeGreaterThan(0)
    expect(within(drawer).getByTestId('member-owner-0')).toHaveTextContent('群主')
    await user.click(within(drawer).getByTestId('session-card-row'))
    expect(onOpenCard).toHaveBeenCalledWith('B1')
    // 抽屉无遮罩、与会话框并存：消息体照常可见可读
    expect(screen.getByText('收到')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '发送消息' })).toBeInTheDocument()
  })

  it('抽屉收起：关闭钮与 Esc 双通道', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByRole('textbox', { name: '发送消息' })
    act(() => { openSessionDetail('session:7') })
    expect(await screen.findByTestId('session-drawer')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '关闭详情' }))
    await waitFor(() => expect(screen.queryByTestId('session-drawer')).toBeNull())
    act(() => { openSessionDetail('session:7') })
    expect(await screen.findByTestId('session-drawer')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('session-drawer')).toBeNull())
  })

  it('Esc 分层：@ 面板开着按 Esc 只关面板，抽屉保持（review P2）', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByRole('textbox', { name: '发送消息' })
    act(() => { openSessionDetail('session:7') })
    expect(await screen.findByTestId('session-drawer')).toBeInTheDocument()
    const input = screen.getByRole('textbox', { name: '发送消息' })
    await user.type(input, '@')
    expect(screen.getByTestId('mention-menu')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByTestId('mention-menu')).toBeNull()
    // 抽屉的 window 监听器不得同帧收到这次 Esc
    expect(screen.getByTestId('session-drawer')).toBeInTheDocument()
  })

  it('抽屉归档入口：确认后调既有幂等端点 archiveSession', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByRole('textbox', { name: '发送消息' })
    act(() => { openSessionDetail('session:7') })
    await user.click(await screen.findByTestId('session-archive'))
    await user.click(screen.getByRole('button', { name: '归档' }))
    await waitFor(() => expect(archiveSession).toHaveBeenCalledWith('session:7'))
  })

  it('打开即 markRead 到历史最大 seq（沿用 markedReads 去重守卫）', async () => {
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('session:7', 7))
    expect(markRoomRead).toHaveBeenCalledTimes(1)
  })

  it('拉卡工具钮 → 卡选择器勾选确认 → 逐张 POST …/cards（B358.8 #8）', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await user.click(await screen.findByRole('button', { name: '拉卡进群' }))
    // 选择器列出可选卡；已在会话的卡禁选
    expect(await screen.findByText('编制入站封界')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: '选择 B233.14' }))
    await user.click(screen.getByRole('button', { name: /确认拉卡（1）/ }))
    await waitFor(() => expect(joinSessionCard).toHaveBeenCalledWith('session:7', 'B233.14'))
  })

  it('已在会话内的卡在选择器中禁选（existingCardIds 来自 summary.cards）', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await user.click(await screen.findByRole('button', { name: '拉卡进群' }))
    expect(await screen.findByText('已在会话')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '选择 B1' })).toBeDisabled()
  })
})
