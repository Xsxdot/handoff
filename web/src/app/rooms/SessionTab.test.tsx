// SessionTab.test.tsx —— tab 窗格宿主：⋯ 右侧抽屉（B358.8 #3，chat↔detail 双态退役）、
// 打开即已读、拉卡入口移位（footer 工具钮）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { archiveSession, fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
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
})

describe('SessionTab', () => {
  it('头部精简：无标题行、无拉卡钮，只剩 ⋯（标题唯一来源=窗格标题行）', async () => {
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByRole('textbox', { name: '发送消息' })
    expect(screen.queryByText('架构物理化')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '返回群聊' })).toBeNull()
    // 拉卡入口唯一：SessionChat footer 的工具钮（头部按钮已删）
    expect(screen.getAllByRole('button', { name: '拉卡进群' })).toHaveLength(1)
    expect(screen.getByRole('button', { name: '会话详情' })).toHaveTextContent('⋯')
  })

  it('⋯ 开右侧抽屉：群主标识、卡列表行回调 onOpenCard，抽屉打开时会话消息仍可见', async () => {
    const onOpenCard = vi.fn()
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" onOpenCard={onOpenCard} />)
    await screen.findByRole('textbox', { name: '发送消息' })
    await user.click(screen.getByRole('button', { name: '会话详情' }))
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
    await user.click(await screen.findByRole('button', { name: '会话详情' }))
    expect(await screen.findByTestId('session-drawer')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '关闭详情' }))
    await waitFor(() => expect(screen.queryByTestId('session-drawer')).toBeNull())
    await user.click(screen.getByRole('button', { name: '会话详情' }))
    expect(await screen.findByTestId('session-drawer')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('session-drawer')).toBeNull())
  })

  it('抽屉归档入口：确认后调既有幂等端点 archiveSession', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await user.click(await screen.findByRole('button', { name: '会话详情' }))
    await user.click(await screen.findByTestId('session-archive'))
    await user.click(screen.getByRole('button', { name: '归档' }))
    await waitFor(() => expect(archiveSession).toHaveBeenCalledWith('session:7'))
  })

  it('打开即 markRead 到历史最大 seq（沿用 markedReads 去重守卫）', async () => {
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('session:7', 7))
    expect(markRoomRead).toHaveBeenCalledTimes(1)
  })

  it('拉卡入口在输入框左下工具钮：卡号确认后 POST …/cards', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await user.click(await screen.findByRole('button', { name: '拉卡进群' }))
    await user.type(screen.getByRole('textbox', { name: '卡号' }), 'B233.14')
    await user.click(screen.getByRole('button', { name: '确认拉卡' }))
    await waitFor(() => expect(joinSessionCard).toHaveBeenCalledWith('session:7', 'B233.14'))
  })
})
