// 会话列表页的数据流与交互语义断言（B156.2 C8）。
// jsdom 看不见布局（已知陷阱一）：断言只锁数据流/交互/文案，布局进真机清单。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { fetchRooms } from '../../api/rooms'
import type { RoomSummary } from '../../api/rooms'
import { RoomsListPage } from './RoomsListPage'

vi.mock('../../api/rooms', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/rooms')>()),
  fetchRooms: vi.fn(),
}))

const room = (over: Partial<RoomSummary> = {}): RoomSummary => ({
  id: 'B1', kind: 'card', title: 'B1 卡会话', live: false, read_only: false,
  last_activity: '2026-08-26T08:00:00+08:00', ...over,
})

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/rooms']}>
      <Routes>
        <Route path="/rooms" element={<RoomsListPage />} />
        <Route path="/rooms/:id" element={<p>room-hit</p>} />
      </Routes>
    </MemoryRouter>,
  )

describe('会话列表页', () => {
  it('渲染服务端给出的顺序（LastActivity 降序由服务端保证，页面不二次排序）', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([
      room({ id: 'B2', title: '较新房间' }),
      room({ id: 'B1', title: '较旧房间' }),
    ])
    renderPage()
    await screen.findByText('较新房间')
    const list = screen.getByTestId('room-list')
    expect(list.textContent!.indexOf('较新房间')).toBeLessThan(list.textContent!.indexOf('较旧房间'))
  })

  it('live/read_only 徽标', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([room({ live: true, read_only: true })])
    renderPage()
    await screen.findByText('B1 卡会话')
    expect(screen.getByText('在线')).toBeInTheDocument()
    expect(screen.getByText('只读')).toBeInTheDocument()
  })

  it('bound_session 原样展示，不做格式假设（澄清一）', async () => {
    const opaque = 'console:alice@box/with spaces::weird'
    vi.mocked(fetchRooms).mockResolvedValue([room({ bound_session: opaque })])
    renderPage()
    expect(await screen.findByText(opaque)).toBeInTheDocument()
  })

  it('项目筛选：卡房间按 project、项目群按 project:<name>，其余隐藏', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([
      room({ id: 'B1', project: 'handoff', title: 'handoff 卡' }),
      room({ id: 'B2', project: 'benchmarking', title: 'benchmarking 卡' }),
      { id: 'project:handoff', kind: 'project', title: 'handoff 项目群', live: false, read_only: false, last_activity: 'x' },
    ])
    renderPage()
    await screen.findByText('handoff 卡')
    fireEvent.change(screen.getByLabelText('项目'), { target: { value: 'handoff' } })
    expect(screen.getByText('handoff 项目群')).toBeInTheDocument()
    expect(screen.queryByText('benchmarking 卡')).not.toBeInTheDocument()
  })

  it('错误态不是空列表：数据源失败显示错误横幅，不渲染（空）', async () => {
    // 台账：mockRejectedValue 在「调用时刻」即造出已 rejected 的 promise，若此前
    // 没有任何已执行的调用把 handler 拉起来，Node 记一次 unhandled rejection、
    // vitest 据此判红（红的是错误本身，不是断言）。前置一次 mockImplementation
    // resolve 调用（D 形态）让拒绝干净穿过 usePoll 的 try/catch。
    vi.mocked(fetchRooms).mockImplementation(() => Promise.resolve([]))
    vi.mocked(fetchRooms).mockRejectedValue(new Error('账本库未配置'))
    renderPage()
    expect(await screen.findByText(/会话列表加载失败/)).toBeInTheDocument()
    expect(screen.queryByText('（空）')).not.toBeInTheDocument()
  })

  it('空列表渲染（空），不显示错误', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([])
    renderPage()
    expect(await screen.findByText('（空）')).toBeInTheDocument()
    expect(screen.queryByText(/会话列表加载失败/)).not.toBeInTheDocument()
  })

  it('点击行导航到 /rooms/:id', async () => {
    vi.mocked(fetchRooms).mockResolvedValue([room({ id: 'B1' })])
    renderPage()
    fireEvent.click(await screen.findByText('B1 卡会话'))
    expect(await screen.findByText('room-hit')).toBeInTheDocument()
  })
})