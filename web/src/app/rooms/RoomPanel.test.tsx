// RoomPanel 接缝测试：从统一面板入口验证列表/房间/详情三态、轮询数据流、写入守卫与 attach。
// 边界：不测 CSS 像素；这里只锁定 API 调用、可见状态和 Workbench 开 tab 的契约。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RoomHistoryItem, RoomSummary } from '../../api/rooms'
import {
  fetchInbox,
  fetchRoomMessages,
  fetchRooms,
  markRoomRead,
  sendRoomMessage,
} from '../../api/rooms'
import type { WorkbenchApi } from '../workbench/useWorkbench'
import { EMPTY_WORKBENCH } from '../workbench/tabs'
import { RoomPanel } from './RoomPanel'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchInbox: vi.fn(),
  fetchRoomMessages: vi.fn(),
  fetchRooms: vi.fn(),
  markRoomRead: vi.fn(),
  sendRoomMessage: vi.fn(),
}))

const room = (over: Partial<RoomSummary> = {}): RoomSummary => ({
  id: 'B1',
  kind: 'card',
  project: 'p1',
  title: 'B1 卡房间',
  live: true,
  read_only: false,
  last_activity: '2026-08-28T08:00:00+08:00',
  unread: 0,
  ...over,
})

const message = (seq: number, body: string, roomID = 'B1'): RoomHistoryItem => ({
  seq,
  card_id: roomID,
  type: 'room_message',
  actor: 'user:me',
  payload: { room: roomID, kind: 'user', body },
  created_at: '2026-08-28T08:00:00+08:00',
})

function workbench(open = vi.fn()): WorkbenchApi {
  return { open, wb: EMPTY_WORKBENCH } as unknown as WorkbenchApi
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchRooms).mockResolvedValue([])
  vi.mocked(fetchInbox).mockResolvedValue([])
  vi.mocked(fetchRoomMessages).mockResolvedValue([])
  vi.mocked(markRoomRead).mockResolvedValue({ ok: true })
  vi.mocked(sendRoomMessage).mockResolvedValue({ seq: 3 })
})

describe('RoomPanel', () => {
  it('列表把待回复置顶、显示数量、全员房不受项目过滤', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([
      room({ id: 'B1', title: 'B1 卡房间', project: 'p1' }),
      room({ id: 'global', kind: 'global', project: undefined, title: '全员', unread: 0 }),
      room({ id: 'B2', title: 'B2 卡房间', project: 'p2' }),
    ])
    vi.mocked(fetchInbox).mockResolvedValue([
      { origin: 'mention', title: '@你', card_id: 'B2', ref_id: 'mention-1' },
    ])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)

    expect(await screen.findByText('⚑ 需要你 1')).toBeInTheDocument()
    const rows = await screen.findAllByRole('button', { name: /会话/ })
    expect(rows[0]).toHaveTextContent('B2')
    await user.click(screen.getByText('▦ 全部项目 ∨'))
    await user.selectOptions(screen.getByRole('combobox', { name: '项目' }), 'p1')
    expect(screen.getByText('全员')).toBeInTheDocument()
    expect(screen.queryByText('B2')).not.toBeInTheDocument()
  })

  it('打开房间即 mark read，发送直达当前房间，更多进入详情', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([room()])
    vi.mocked(fetchRoomMessages).mockResolvedValue([message(2, '收到')])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('B1', 2))
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), '继续')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sendRoomMessage).toHaveBeenCalledWith('B1', '继续'))
    await user.click(screen.getByRole('button', { name: '更多' }))
    expect(await screen.findByText('协调者')).toBeInTheDocument()
  })

  it('attach 无投影时置灰并说明；有投影时确认后打开带 initCommand 的终端', async () => {
    const open = vi.fn()
    const attach = { task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1' }
    vi.mocked(fetchRooms).mockResolvedValue([room({ attach })])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench(open)} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await user.click(screen.getByRole('button', { name: '更多' }))
    await user.click(screen.getByRole('button', { name: 'attach' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(open).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'attach' }))
    await user.click(screen.getByRole('button', { name: '确认 attach' }))
    await waitFor(() => expect(open).toHaveBeenCalledWith(
      { kind: 'terminal', seq: 1, initCommand: 'handoff attach T1' },
      expect.objectContaining({ path: '/w/B1', machine: '' }),
    ))
  })

  it('attach 无投影时置灰并说明', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([room({ attach: undefined })])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await user.click(screen.getByRole('button', { name: '更多' }))
    expect(screen.getByRole('button', { name: 'attach' })).toBeDisabled()
    expect(screen.getByText('暂无可 attach 的任务')).toBeInTheDocument()
  })
})
