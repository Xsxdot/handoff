// SessionTab.test.tsx —— tab 窗格宿主：⋯ 右侧抽屉（B358.8 #3，chat↔detail 双态退役）、
// 打开即已读、拉卡入口移位（footer 工具钮）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { archiveSession, fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import { ApiError } from '../../api/client'
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
  it('历史首拉未返回时显示读取中，不谎报会话没有消息', () => {
    vi.mocked(fetchRoomMessages).mockReturnValue(new Promise(() => {}))
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    expect(screen.getByText('正在读取消息…')).toBeInTheDocument()
    expect(screen.queryByText('（还没有消息）')).toBeNull()
  })

  it('历史鉴权失败时显示失效原因，不谎报会话没有消息', async () => {
    vi.mocked(fetchRoomMessages).mockRejectedValue(new ApiError(401, '会话失效'))
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('会话已失效')
    expect(screen.queryByText('（还没有消息）')).toBeNull()
  })

  it('首个历史请求挂住后继续轮询，恢复时显示真实消息', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(fetchRoomMessages)
        .mockReturnValueOnce(new Promise(() => {}))
        .mockResolvedValue([event(7, '恢复后的消息')])
      render(<SessionTab sessionId="session:7" title="架构物理化" />)
      expect(screen.getByText('正在读取消息…')).toBeInTheDocument()
      await act(async () => { await vi.advanceTimersByTimeAsync(20_000) })
      expect(vi.mocked(fetchRoomMessages).mock.calls.length).toBeGreaterThanOrEqual(2)
      expect(screen.getByText('恢复后的消息')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

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

  // B406：401 是 usePoll 的终止态（停表、data 恒 null）——消费端必须把它渲染成
  // 过期横幅，而不是把「还没有消息」空态挂成永久假读数。
  it('历史流 401：消息区渲染过期横幅替代「还没有消息」假读数，断线文案不混入', async () => {
    vi.mocked(fetchRoomMessages).mockRejectedValue(
      new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie'))
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    expect(await screen.findByText(/会话已失效/)).toBeInTheDocument()
    expect(screen.queryByText('（还没有消息）')).not.toBeInTheDocument()
    // 过期与断线是两种终止语义：footer 的「消息流已断开」不得在 401 场景出现
    expect(screen.queryByText(/消息流已断开/)).not.toBeInTheDocument()
  })

  it('详情流 401：抽屉渲染过期横幅而非「正在读取…」', async () => {
    vi.mocked(fetchSessionDetail).mockRejectedValue(
      new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie'))
    vi.mocked(fetchRoomMessages).mockRejectedValue(
      new ApiError(401, '未授权：浏览器会话已失效，请重新执行 handoff console 兑换 cookie'))
    render(<SessionTab sessionId="session:7" title="架构物理化" />)
    await screen.findByText(/会话已失效/)
    act(() => { openSessionDetail('session:7') })
    const drawer = await screen.findByTestId('session-drawer')
    expect(within(drawer).getByText(/会话已失效/)).toBeInTheDocument()
    expect(within(drawer).queryByText('正在读取…')).not.toBeInTheDocument()
  })
})

// —— S5（B426）：compact 房间两态改受控——「群聊 | 详情」tablist 删除，paneDetail
// 上提 Shell（房间头部裁决需要知道详情态），SessionTab 受控渲染 + 详情态自渲染
// 头部（左上返回回群聊、不渲染 ⋯）。 ——
// panelByLabel：hidden 面板不进可达性树（这正是闸的证据），name 过滤会撞
// 「隐藏元素可名计算为空」的库行为，故 role 全量（hidden:true）后按 aria-label 挑。
const panelByLabel = (label: string) =>
  screen.getAllByRole('tabpanel', { hidden: true }).find((p) => p.getAttribute('aria-label') === label)!

describe('S5 compact 房间两态（B426，受控 paneDetail）', () => {
  it('无「群聊|详情」tablist（S5 删除）；受控 false=群聊态：详情面板 hidden、无详情头部、无 ⋯、无 aside', async () => {
    const onPaneDetailChange = vi.fn()
    render(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail={false} onPaneDetailChange={onPaneDetailChange} />)
    await screen.findByRole('textbox', { name: '发送消息' })
    expect(screen.queryByTestId('session-view-chat')).toBeNull()
    expect(screen.queryByTestId('session-view-detail')).toBeNull()
    expect(screen.queryByTestId('session-drawer')).toBeNull()
    // ⋯ 不在本组件（房间头部归 Shell、经注册表投递回来）
    expect(screen.queryByRole('button', { name: '会话详情' })).toBeNull()
    // 详情头部仅详情态可达（面板 hidden 时头部随面板不进可达性树；DOM 内保挂载
    // 与两态面板同款手法）
    expect(screen.queryByRole('button', { name: '返回群聊' })).toBeNull()
    expect(panelByLabel('群聊').hasAttribute('hidden')).toBe(false)
    expect(screen.queryByRole('tabpanel', { name: '会话详情' })).toBeNull()
    expect(panelByLabel('会话详情').hasAttribute('hidden')).toBe(true)
  })

  it('受控 true=详情态：详情头部（返回钮、无 ⋯）在场、五块全宽、群聊 hidden；点返回回调 false（仅回群聊不出房间）', async () => {
    const onPaneDetailChange = vi.fn()
    render(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail onPaneDetailChange={onPaneDetailChange} />)
    await within(panelByLabel('会话详情')).findByRole('region', { name: '成员' })
    for (const name of ['会话卡', '任务节点', '会话 timeline', '会话管理']) {
      expect(within(panelByLabel('会话详情')).getByRole('region', { name })).toBeInTheDocument()
    }
    // 详情态头部：左上返回、不渲染 ⋯（房间任一时刻只渲染一条 header 的另一半）
    expect(screen.getByTestId('session-detail-back')).toBeInTheDocument()
    expect(within(panelByLabel('会话详情')).queryByRole('button', { name: '会话详情' })).toBeNull()
    expect(panelByLabel('群聊').hasAttribute('hidden')).toBe(true)
    expect(panelByLabel('会话详情').hasAttribute('hidden')).toBe(false)
    expect(screen.queryByTestId('session-drawer')).toBeNull()
    fireEvent.click(screen.getByTestId('session-detail-back'))
    expect(onPaneDetailChange).toHaveBeenCalledWith(false)
  })

  it('注册表投递（⋯）：compact 下回调 onPaneDetailChange(true)（桌面开抽屉路径由桌面用例锁）', async () => {
    const onPaneDetailChange = vi.fn()
    render(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail={false} onPaneDetailChange={onPaneDetailChange} />)
    await screen.findByRole('textbox', { name: '发送消息' })
    act(() => { openSessionDetail('session:7') })
    expect(onPaneDetailChange).toHaveBeenCalledWith(true)
  })

  it('Esc 关详情态：回调 false（compact 第二收起通道保持）', async () => {
    const onPaneDetailChange = vi.fn()
    render(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail onPaneDetailChange={onPaneDetailChange} />)
    await within(panelByLabel('会话详情')).findByRole('region', { name: '成员' })
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onPaneDetailChange).toHaveBeenCalledWith(false)
  })

  it('切回群聊：草稿跨切换存活（hidden 保挂载收益不变；受控翻转经 rerender）', async () => {
    const onPaneDetailChange = vi.fn()
    const view = render(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail={false} onPaneDetailChange={onPaneDetailChange} />)
    await screen.findByRole('textbox', { name: '发送消息' })
    fireEvent.change(screen.getByRole('textbox', { name: '发送消息' }), { target: { value: '草稿甲' } })
    view.rerender(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail onPaneDetailChange={onPaneDetailChange} />)
    view.rerender(<SessionTab sessionId="session:7" title="架构物理化" compact paneDetail={false} onPaneDetailChange={onPaneDetailChange} />)
    expect(screen.getByRole('textbox', { name: '发送消息' })).toHaveValue('草稿甲')
  })
})
