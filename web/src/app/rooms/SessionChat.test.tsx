// SessionChat.test.tsx —— 群聊面：@高亮、引用条跳转、卡 chips 空座、发送、
// 归档只读、403 可行动报错（本卡最重岔口 1 的组件半边反例断言）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '../../api/client'
import { addSessionMember, sendRoomMessage } from '../../api/rooms'
import type { RoomHistoryItem, SessionSummary } from '../../api/rooms'
import { SessionChat } from './SessionChat'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  sendRoomMessage: vi.fn(),
  addSessionMember: vi.fn(),
}))

const summary = (over: Partial<SessionSummary> = {}): SessionSummary => ({
  id: 'session:1', kind: 'session', title: '架构物理化', owner: 'user:sy',
  archived: false, unread: 0, needs_human: false, last_activity: '2026-09-12T00:00:00Z',
  cards: [{ card_id: 'B233.14', title: '编制入站封界', status: '进行中', seat: 'cli:opencode#s1' },
          { card_id: 'B233.17', title: '组装点收窄', status: '待办' }],
  ...over,
})

const event = (seq: number, body: string, over: Partial<RoomHistoryItem> = {}): RoomHistoryItem => ({
  seq, card_id: '', type: 'room_message', actor: 'cli:opencode#s1',
  payload: { room: 'session:1', kind: 'user', body }, created_at: '2026-09-12T00:00:00Z',
  ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(sendRoomMessage).mockResolvedValue({ seq: 3 })
  window.HTMLElement.prototype.scrollIntoView = vi.fn()
})

describe('SessionChat', () => {
  it('群主行与卡 chips 行不再渲染（B358.8 #3 反例：两块迁详情抽屉）', () => {
    render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={() => {}} />)
    expect(screen.queryByTestId('session-card-chip')).toBeNull()
    expect(screen.queryByText(/群主：/)).toBeNull()
    expect(document.body.textContent).not.toContain('还没配人')
  })

  it('拉卡入口在输入框左下工具钮：点击回调触发；不传 onJoinCard 不渲染', async () => {
    const onJoinCard = vi.fn()
    const user = userEvent.setup()
    const view = render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={() => {}} onJoinCard={onJoinCard} />)
    await user.click(screen.getByRole('button', { name: '拉卡进群' }))
    expect(onJoinCard).toHaveBeenCalledOnce()
    view.rerender(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={() => {}} />)
    expect(screen.queryByRole('button', { name: '拉卡进群' })).toBeNull()
  })

  it('@mention 高亮与回复引用条：点引用条滚动定位被引用消息', async () => {
    const user = userEvent.setup()
    render(
      <SessionChat
        sessionId="session:1" summary={summary()}
        events={[event(41, '商定的是走分支 B', { actor: 'user:sy' }), event(42, '@B233.16 收口归你，别撞 14 的分支', { payload: { room: 'session:1', kind: 'user', body: '@B233.16 收口归你', reply_to: 41, mentions: ['B233.16'] } })]}
        historyError="" onSent={() => {}}
      />,
    )
    const at = screen.getByTestId('mention-42-0')
    expect(at).toHaveTextContent('@B233.16')
    await user.click(screen.getByTestId('quote-42'))
    expect(window.HTMLElement.prototype.scrollIntoView).toHaveBeenCalled()
    expect(screen.getByTestId('msg-41').className).toMatch(/highlight/)
  })

  it('发送：正文里的 @token 解析进 mentions（服务端据此寻址）', async () => {
    const onSent = vi.fn()
    const user = userEvent.setup()
    render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={onSent} />)
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), '@B233.16 收口归你')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sendRoomMessage).toHaveBeenCalledWith('session:1', '@B233.16 收口归你', { mentions: ['B233.16'] }))
    expect(onSent).toHaveBeenCalled()
  })

  it('发送 403 渲染可行动原文（控制台非成员——岔口 1 的组件半边）', async () => {
    vi.mocked(sendRoomMessage).mockRejectedValue(new ApiError(403, '你不是这场会话的成员'))
    const user = userEvent.setup()
    render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={() => {}} />)
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), 'hi')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('你不是这场会话的成员')
  })

  it('B366 链：403 → 一键「以当前身份加入会话」（空体、不自报身份）→ 清横幅 → 再发送成功', async () => {
    vi.mocked(sendRoomMessage)
      .mockRejectedValueOnce(new ApiError(403, 'collab: 书写者与房间身份不符'))
      .mockResolvedValueOnce({ seq: 4 })
    vi.mocked(addSessionMember).mockResolvedValue({ ok: true })
    const onSent = vi.fn()
    const user = userEvent.setup()
    render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={onSent} />)
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), 'hi')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('书写者与房间身份不符')
    // 一键以调用者身份加入：addSessionMember 只带会话号（身份服务端权威）。
    await user.click(screen.getByRole('button', { name: '以当前身份加入会话' }))
    await waitFor(() => expect(addSessionMember).toHaveBeenCalledWith('session:1'))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    // 发言链通：草稿仍在，直接再发成功。
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sendRoomMessage).toHaveBeenCalledTimes(2))
    expect(onSent).toHaveBeenCalled()
  })

  it('一键只在 403 出现：非成员判定按状态码，其余错误（如 500）不给加入入口', async () => {
    vi.mocked(sendRoomMessage).mockRejectedValue(new ApiError(500, '账本写失败'))
    const user = userEvent.setup()
    render(<SessionChat sessionId="session:1" summary={summary()} events={[]} historyError="" onSent={() => {}} />)
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), 'hi')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('账本写失败')
    expect(screen.queryByRole('button', { name: '以当前身份加入会话' })).toBeNull()
  })

  it('归档只读：输入与发送禁用 + 只读横幅', () => {
    render(<SessionChat sessionId="session:1" summary={summary({ archived: true })} events={[]} historyError="" onSent={() => {}} />)
    expect(screen.getByRole('textbox', { name: '发送消息' })).toBeDisabled()
    expect(screen.getByText('会话已归档，只读。')).toBeInTheDocument()
  })
})
