// SessionTab.test.tsx —— tab 窗格宿主：群聊↔详情切换、打开即已读、拉卡进群。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import type { RoomHistoryItem, SessionDetail } from '../../api/rooms'
import fixture from '../../api/testdata/RoomsFixture.json'
import { SessionTab } from './SessionTab'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchSessionDetail: vi.fn(),
  fetchRoomMessages: vi.fn(),
  markRoomRead: vi.fn(),
  joinSessionCard: vi.fn(),
}))

const cases = fixture as { case: string; detail?: SessionDetail }[]
const detailOf = () => cases.find((c) => c.case === 'session-detail-golden')!.detail!
const event = (seq: number, body: string): RoomHistoryItem => ({
  seq, card_id: '', type: 'room_message', actor: 'cli:opencode#s1',
  payload: { room: 'session:1', kind: 'user', body }, created_at: '2026-09-12T00:00:00Z',
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchSessionDetail).mockResolvedValue(detailOf())
  vi.mocked(fetchRoomMessages).mockResolvedValue([event(7, '收到')])
  vi.mocked(markRoomRead).mockResolvedValue({ ok: true })
  vi.mocked(joinSessionCard).mockResolvedValue({ ok: true })
})

describe('SessionTab', () => {
  it('默认群聊态渲染详情头；⋯ 进详情、← 返回', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:1" title="架构物理化" />)
    expect(await screen.findByRole('textbox', { name: '发送消息' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '会话详情' }))
    expect(await screen.findByLabelText('会话详情')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '返回群聊' }))
    expect(screen.getByRole('textbox', { name: '发送消息' })).toBeInTheDocument()
  })

  it('打开即 markRead 到历史最大 seq（沿用 markedReads 去重守卫）', async () => {
    render(<SessionTab sessionId="session:1" title="架构物理化" />)
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('session:1', 7))
    expect(markRoomRead).toHaveBeenCalledTimes(1)
  })

  it('＋拉卡进群：输入卡号确认后 POST …/cards', async () => {
    const user = userEvent.setup()
    render(<SessionTab sessionId="session:1" title="架构物理化" />)
    await user.click(await screen.findByRole('button', { name: '拉卡进群' }))
    await user.type(screen.getByRole('textbox', { name: '卡号' }), 'B233.14')
    await user.click(screen.getByRole('button', { name: '确认拉卡' }))
    await waitFor(() => expect(joinSessionCard).toHaveBeenCalledWith('session:1', 'B233.14'))
  })
})
