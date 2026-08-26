// A.6 轮询间隔默认值落常量：三个房间面页面把 COLLAB_POLL_MS 传给 usePoll，
// 不散写魔数。mock usePoll 后只断言 interval 参数 == 常量。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { InboxPage } from './InboxPage'
import { RoomDetailPage } from './RoomDetailPage'
import { RoomsListPage } from './RoomsListPage'

const pollState = { data: null, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() }

vi.mock('../data/usePoll', () => ({ usePoll: vi.fn(() => pollState) }))
vi.mock('../../api/rooms', () => ({
  fetchRooms: vi.fn(),
  fetchRoomMessages: vi.fn(),
  sendRoomMessage: vi.fn(),
  markRoomRead: vi.fn(),
  fetchInbox: vi.fn(),
}))
vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  answerDecision: vi.fn(),
}))

describe('A.6 轮询间隔常量', () => {
  beforeEach(() => vi.mocked(usePoll).mockClear())

  it('会话列表页以 COLLAB_POLL_MS 轮询', () => {
    render(<MemoryRouter><RoomsListPage /></MemoryRouter>)
    expect(vi.mocked(usePoll)).toHaveBeenCalledWith(expect.any(Function), COLLAB_POLL_MS)
  })

  it('房间页以 COLLAB_POLL_MS 轮询（摘要与历史两条）', () => {
    render(
      <MemoryRouter initialEntries={['/rooms/B1']}>
        <Routes><Route path="/rooms/:id" element={<RoomDetailPage />} /></Routes>
      </MemoryRouter>,
    )
    const calls = vi.mocked(usePoll).mock.calls
    expect(calls.filter(([, interval]) => interval === COLLAB_POLL_MS)).toHaveLength(2)
  })

  it('收件箱页以 COLLAB_POLL_MS 轮询', () => {
    render(<MemoryRouter><InboxPage /></MemoryRouter>)
    expect(vi.mocked(usePoll)).toHaveBeenCalledWith(expect.any(Function), COLLAB_POLL_MS)
  })
})