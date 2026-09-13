// A.6 轮询间隔默认值落常量：会话 tab 的详情与历史两流把 COLLAB_POLL_MS 传给
// usePoll，不散写魔数（B358.6：断言对象从旧房间面板三流迁 SessionTab 两流）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { SessionTab } from './SessionTab'

const pollState = { data: null, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() }

vi.mock('../data/usePoll', () => ({ usePoll: vi.fn(() => pollState) }))
vi.mock('../../api/rooms', () => ({
  fetchSessionDetail: vi.fn(),
  fetchRoomMessages: vi.fn(),
  markRoomRead: vi.fn(),
  joinSessionCard: vi.fn(),
  sendRoomMessage: vi.fn(),
}))

describe('A.6 轮询间隔常量', () => {
  beforeEach(() => vi.mocked(usePoll).mockClear())

  it('会话 tab 的详情与历史流都以 COLLAB_POLL_MS 轮询', () => {
    render(<SessionTab sessionId="session:1" title="架构物理化" />)
    const calls = vi.mocked(usePoll).mock.calls
    expect(calls.filter(([, interval]) => interval === COLLAB_POLL_MS)).toHaveLength(2)
  })
})
