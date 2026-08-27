// A.6 轮询间隔默认值落常量：统一 RoomPanel 的列表、收件箱与历史流把
// COLLAB_POLL_MS 传给 usePoll，不散写魔数。mock usePoll 后只断言 interval 参数。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { RoomPanel } from './RoomPanel'
import type { WorkbenchApi } from '../workbench/useWorkbench'
import { EMPTY_WORKBENCH } from '../workbench/tabs'

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

  it('统一面板的列表、收件箱、历史流都以 COLLAB_POLL_MS 轮询', () => {
    render(<RoomPanel workbench={{ wb: EMPTY_WORKBENCH, open: vi.fn() } as unknown as WorkbenchApi} persistent={false} />)
    const calls = vi.mocked(usePoll).mock.calls
    expect(calls.filter(([, interval]) => interval === COLLAB_POLL_MS)).toHaveLength(3)
  })
})
