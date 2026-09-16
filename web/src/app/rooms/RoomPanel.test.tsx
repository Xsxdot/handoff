// RoomPanel 接缝测试：从统一面板入口验证列表/房间/详情三态、轮询数据流、写入守卫与 attach。
// 边界：不测 CSS 像素；这里只锁定 API 调用、可见状态和 Workbench 开 tab 的契约。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '../../api/client'
import { fetchCardDetail } from '../../api/ledger'
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
import { logRoom } from './roomLog'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchInbox: vi.fn(),
  fetchRoomMessages: vi.fn(),
  fetchRooms: vi.fn(),
  markRoomRead: vi.fn(),
  sendRoomMessage: vi.fn(),
}))

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchCardDetail: vi.fn(),
}))

vi.mock('./roomLog', () => ({ logRoom: vi.fn() }))

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
  // resetAllMocks 而非 clearAllMocks：clear 不清 mockResolvedValueOnce 队列，
  // 续载用例的 once 值会泄漏到下一支测试（全量跑时表现为 fetchRooms 拿到脏数据）。
  vi.resetAllMocks()
  vi.mocked(fetchRooms).mockResolvedValue({ rooms: [], has_more: false })
  vi.mocked(fetchInbox).mockResolvedValue([])
  vi.mocked(fetchRoomMessages).mockResolvedValue([])
  vi.mocked(markRoomRead).mockResolvedValue({ ok: true })
  vi.mocked(sendRoomMessage).mockResolvedValue({ seq: 3 })
  vi.mocked(fetchCardDetail).mockResolvedValue({
    card: {
      id: 'B1', title: 'B1 卡房间', status: '待审阅', priority: '高', project: 'p1', parent: '',
      workflow: 'bug', workflow_version: 1, attachments: [{ kind: 'spec', path: 'docs/spec.md' }],
      acceptance_criteria: '', created_at: '', updated_at: '',
    },
    relations: [], events: [], task_states: [], effective_base_branch: '', decisions: [], needs: '',
  })
})

describe('RoomPanel', () => {
  it('列表把待回复置顶、显示数量、全员房不受项目过滤', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [
      room({ id: 'B1', title: 'B1 卡房间', project: 'p1' }),
      room({ id: 'global', kind: 'global', project: undefined, title: '全员', unread: 0 }),
      room({ id: 'B2', title: 'B2 卡房间', project: 'p2' }),
    ], has_more: false })
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

  it('列表直接使用服务端 preview，不发逐房间 limit=1 请求', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room({ preview: { body: '服务端预览', seq: 7, created_at: '2026-08-28T08:00:00+08:00' } })], has_more: false })
    vi.mocked(fetchRoomMessages).mockResolvedValue([message(7, '不应被列表读取')])
    render(<RoomPanel workbench={workbench()} persistent={false} />)

    expect(await screen.findByText('服务端预览')).toBeInTheDocument()
    expect(fetchRoomMessages).not.toHaveBeenCalled()
  })

  it('打开房间即 mark read，发送直达当前房间，更多进入详情', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    vi.mocked(fetchRoomMessages).mockResolvedValue([message(2, '收到')])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await waitFor(() => expect(markRoomRead).toHaveBeenCalledWith('B1', 2))
    expect(fetchRoomMessages).toHaveBeenCalledTimes(1)
    expect(fetchRoomMessages).toHaveBeenCalledWith('B1', { limit: 200 })
    await user.type(screen.getByRole('textbox', { name: '发送消息' }), '继续')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sendRoomMessage).toHaveBeenCalledWith('B1', '继续'))
    await user.click(screen.getByRole('button', { name: '更多' }))
    expect(await screen.findByText('协调者')).toBeInTheDocument()
  })

  it('attach 无投影时置灰并说明；有投影时确认后打开带 initCommand 的终端', async () => {
    const open = vi.fn()
    const attach = { task_id: 'T1', work_dir: '/w/B1', command: 'handoff attach T1' }
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room({ attach })], has_more: false })
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
      { kind: 'terminal', seq: 1, spawn: true, initCommand: 'handoff attach T1' },
      expect.objectContaining({ path: '/w/B1', machine: '' }),
    ))
  })

  it('attach 无投影时置灰并说明', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room({ attach: undefined })], has_more: false })
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await user.click(screen.getByRole('button', { name: '更多' }))
    expect(screen.getByRole('button', { name: 'attach' })).toBeDisabled()
    expect(screen.getByText('暂无可 attach 的任务')).toBeInTheDocument()
  })

  it('详情显示卡片信息，点击卡片跳到 /cards 并打开抽屉', async () => {
    const openCard = vi.fn()
    const user = userEvent.setup()
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} onOpenCard={openCard} />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await user.click(screen.getByRole('button', { name: '更多' }))
    expect(await screen.findByText('卡片')).toBeInTheDocument()
    expect(screen.getByText('待审阅')).toBeInTheDocument()
    expect(screen.getByText('高')).toBeInTheDocument()
    expect(screen.getByText('spec')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '打开卡片 B1' }))
    expect(openCard).toHaveBeenCalledWith('B1')
    expect(logRoom).toHaveBeenCalledWith('debug', 'card_open_requested', { room: 'B1', view: 'detail' })
  })

  it('打开卡片先记录 room/view，再通知上层打开抽屉', async () => {
    const sequence: string[] = []
    vi.mocked(logRoom).mockImplementation((_level, event) => {
      if (event === 'card_open_requested') sequence.push('logRoom')
    })
    const openCard = vi.fn(() => { sequence.push('onOpenCard') })
    const user = userEvent.setup()
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} onOpenCard={openCard} />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))
    await user.click(screen.getByRole('button', { name: '更多' }))
    await screen.findByRole('button', { name: '打开卡片 B1' })

    await user.click(screen.getByRole('button', { name: '打开卡片 B1' }))

    expect(sequence).toEqual(['logRoom', 'onOpenCard'])
    expect(logRoom).toHaveBeenCalledWith('debug', 'card_open_requested', { room: 'B1', view: 'detail' })
  })

  it('常驻栏带 min-h-0，会话列表不能把整页撑高', async () => {
    render(<RoomPanel workbench={workbench()} persistent />)
    const panel = await screen.findByTestId('room-panel')
    expect(panel.className).toMatch(/min-h-0/)
  })

  it('常驻栏 overflow-hidden，列表溢出不泄漏成文档滚动', async () => {
    render(<RoomPanel workbench={workbench()} persistent />)
    const panel = await screen.findByTestId('room-panel')
    expect(panel.className).toMatch(/overflow-hidden/)
  })

  it('常驻面板收起后保留 FAB，并可重新打开', async () => {
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent />)
    expect(await screen.findByTestId('room-panel')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '收起房间面板' }))
    expect(screen.queryByTestId('room-panel')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '打开房间面板' }))
    expect(screen.getByTestId('room-panel')).toBeInTheDocument()
  })

  it('消息流 401 终止时显示告警并禁用发送', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    vi.mocked(fetchRoomMessages).mockImplementation((_id, opts = {}) => (
      opts.limit === 1
        ? Promise.resolve([])
        : Promise.reject(new ApiError(401, '会话失效'))
    ))
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))

    expect(await screen.findByText('消息会话已过期，请重新登录。')).toBeInTheDocument()
    expect(screen.queryByText('正在读取…')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '发送消息' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
  })

  it('会话列表 401 终止时显示告警', async () => {
    vi.mocked(fetchRooms).mockRejectedValue(new ApiError(401, '会话失效'))
    render(<RoomPanel workbench={workbench()} persistent />)

    expect(await screen.findByText('会话已过期，请重新登录。')).toBeInTheDocument()
  })

  it('空房间历史加载完成后显示空态而不是持续读取', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    vi.mocked(fetchRoomMessages).mockResolvedValue([])
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent />)
    await user.click(await screen.findByRole('button', { name: /卡房间/ }))

    expect(await screen.findByText('（还没有消息）')).toBeInTheDocument()
    expect(screen.queryByText('正在读取…')).not.toBeInTheDocument()
  })

  it('首屏以 limit=50 拉一页', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByRole('button', { name: /卡房间/ })
    expect(fetchRooms).toHaveBeenCalledWith({ limit: 50 })
  })

  it('has_more=true 滚动到底按 cursor 续载，has_more=false 终止', async () => {
    // 按入参分派而非 once 队列：全量跑时 5s 轮询可能在断言前打一发，once 队列
    // 会被首屏轮询吃掉第二页，续载路径消失（假红）。按 opts.cursor 分派与轮询节奏无关。
    vi.mocked(fetchRooms).mockImplementation((opts = {}) => Promise.resolve(
      opts.cursor
        ? { rooms: [room({ id: 'B2', title: 'B2 卡房间' })], has_more: false }
        : { rooms: [room({ id: 'B1' })], has_more: true, next_cursor: 'C1' },
    ))
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByRole('button', { name: /卡房间/ })
    const list = screen.getByTestId('room-list-scroll')
    Object.defineProperty(list, 'scrollHeight', { value: 1000, configurable: true })
    Object.defineProperty(list, 'clientHeight', { value: 0, configurable: true })
    Object.defineProperty(list, 'scrollTop', { value: 1000, configurable: true, writable: true })
    // 首屏签名 effect 与 findByRole 之间存在调度竞窗：首拉数据已渲染、续载态
    // 还没落定时单发一次 scroll 会丢事件。在 waitFor 内重发，直到续载真正发生。
    await waitFor(() => {
      fireEvent.scroll(list)
      expect(fetchRooms).toHaveBeenCalledWith({ cursor: 'C1' })
    })
    expect(await screen.findByText('B2 卡房间')).toBeInTheDocument()
    fireEvent.scroll(list)
    // has_more=false 后不得再发任何带 cursor 的续载（首屏 limit 轮询不算）。
    const cursorCalls = vi.mocked(fetchRooms).mock.calls.filter(([opts]) => opts?.cursor !== undefined)
    expect(cursorCalls).toHaveLength(1)
  })

  it('426 渲染可行动升级提示，不显示半页', async () => {
    vi.mocked(fetchRooms).mockRejectedValue(
      new ApiError(426, '客户端版本过旧：会话列表已改为分页加载，请升级 handoff 桌面端与控制台后重试。'))
    render(<RoomPanel workbench={workbench()} persistent />)
    expect(await screen.findByText(/客户端版本过旧/)).toBeInTheDocument()
  })
})

describe('RoomPanel 发送后刷新（B287）', () => {
  it('refresh 不复用旧 nonce 的在飞请求：发送后收件箱立即重取，不等下一轮周期', async () => {
    // 收件箱首拉故意挂起不放行：制造「发送那一刻有一个旧 nonce 在飞请求」的现场。
    // 无修复时 refresh 会采纳这个旧请求（内容不可能含新事实），不发新调用；
    // 有修复时 nonce 变更触发第二只真实请求。
    let releaseInbox!: (value: Awaited<ReturnType<typeof fetchInbox>>) => void
    vi.mocked(fetchInbox).mockImplementation(
      () => new Promise((resolve) => { releaseInbox = resolve }),
    )
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    const user = userEvent.setup()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    await user.click(await screen.findByRole('button', { name: /会话 B1/ }))
    await user.type(await screen.findByLabelText('发送消息'), 'hi')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(vi.mocked(fetchInbox).mock.calls.length).toBeGreaterThanOrEqual(2))
    releaseInbox([])
  })
})

describe('RoomPanel 悬浮球（B287）', () => {
  it('收起球上移错开 + 球：bottom-[104px]（+球占 44–88px，净距 16px）', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    const fab = await screen.findByRole('button', { name: '打开房间面板' })
    expect(fab.className).toContain('bottom-[104px]')
    expect(fab.className).not.toContain('bottom-20')
  })
})

describe('RoomPanel 浮窗几何（B287）', () => {
  it('浮窗按 geom 摆位：拖标题栏改 left/top，拉角落改宽高，最小尺寸钳制', async () => {
    window.localStorage.setItem('handoff:room-panel-geom.v1', JSON.stringify({ x: 100, y: 80, w: 360, h: 520 }))
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [], has_more: false })
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    const panel = await screen.findByTestId('room-panel')
    expect(panel.style.left).toBe('100px')
    expect(panel.style.top).toBe('80px')
    fireEvent.pointerDown(screen.getByTestId('room-panel-title'), { clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 30 })
    fireEvent.pointerUp(document)
    expect(panel.style.left).toBe('130px')
    expect(panel.style.top).toBe('100px')
    fireEvent.pointerDown(screen.getByTestId('room-panel-corner'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: -500, clientY: -500 })
    fireEvent.pointerUp(document)
    expect(panel.style.width).toBe('320px')
    expect(panel.style.height).toBe('360px')
  })

  it('几何本机持久化：拖动后写入 localStorage，重挂载恢复摆法', async () => {
    window.localStorage.clear()
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [], has_more: false })
    const view = render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByTestId('room-panel')
    fireEvent.pointerDown(screen.getByTestId('room-panel-title'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 15, clientY: 10 })
    fireEvent.pointerUp(document)
    const stored = JSON.parse(window.localStorage.getItem('handoff:room-panel-geom.v1') ?? '{}')
    expect(stored).toMatchObject({ w: 360, h: 520 })
    view.unmount()
    render(<RoomPanel workbench={workbench()} persistent={false} />)
    const restored = await screen.findByTestId('room-panel')
    expect(restored.style.left).toBe(`${stored.x}px`)
    expect(restored.style.top).toBe(`${stored.y}px`)
  })
})

describe('RoomPanel 常驻栏独立开合（B287 返修）', () => {
  it('浮窗收起不牵连工作项页常驻栏；常驻栏自己可收起、可由悬浮球重开', async () => {
    vi.mocked(fetchRooms).mockResolvedValue({ rooms: [room()], has_more: false })
    const user = userEvent.setup()
    const view = render(<RoomPanel workbench={workbench()} persistent={false} />)
    await screen.findByTestId('room-panel')
    await user.click(screen.getByRole('button', { name: '收起房间面板' }))
    expect(screen.queryByTestId('room-panel')).toBeNull()
    // 切到工作项页（persistent=true）：常驻栏应仍开着——两种形态状态分账
    view.rerender(<RoomPanel workbench={workbench()} persistent />)
    expect(screen.getByTestId('room-panel')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '收起房间面板' }))
    expect(screen.queryByTestId('room-panel')).toBeNull()
    await user.click(screen.getByRole('button', { name: '打开房间面板' }))
    expect(screen.getByTestId('room-panel')).toBeInTheDocument()
  })
})
