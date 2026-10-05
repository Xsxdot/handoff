// SessionSidebar.test.tsx —— 列表行（未读角标/需要你标签/预览）、筛选与拖拽源。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import fixture from '../../api/testdata/RoomsFixture.json'
import type { SessionMember, SessionSummary } from '../../api/rooms'
import { DRAG_SESSION_MIME } from '../workbench/paneDrop'
import { SessionSidebar } from './SessionSidebar'

const cases = fixture as { case: string; summary?: SessionSummary }[]

const defaultProps = {
  loading: false,
  errorText: '',
  needsOnly: false,
  onToggleNeeds: () => {},
  onOpen: () => {},
  onCreate: () => {},
  projectFilter: '',
  onProjectFilter: () => {},
  projectOptions: [] as string[],
  projectOfCard: () => '',
}

describe('SessionSidebar', () => {
  it('读取失败时显示错误而非“暂无会话”', () => {
    render(<SessionSidebar sessions={[]} {...defaultProps} errorText="请求超时" />)
    expect(screen.getByRole('alert')).toHaveTextContent('请求超时')
    expect(screen.queryByText('（暂无会话）')).not.toBeInTheDocument()
  })
  it('查询失败时保留上次成功缓存的会话行并显示断连提示', () => {
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    render(<SessionSidebar sessions={[golden]} {...defaultProps} errorText="连接数据库超时" />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接数据库超时')
    expect(screen.getByTestId('session-row')).toHaveTextContent(golden.title)
    expect(screen.queryByText('（暂无会话）')).not.toBeInTheDocument()
  })
  it('fixture 行渲染未读角标与需要你标签，点击行回调会话', async () => {
    const onOpen = vi.fn()
    const user = userEvent.setup()
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    render(<SessionSidebar sessions={[golden]} {...defaultProps} onOpen={onOpen} />)
    const row = screen.getByTestId('session-row')
    expect(row).toHaveTextContent('架构物理化')
    expect(screen.getByTestId('session-unread')).toHaveTextContent('2')
    expect(row).toHaveTextContent('需要你')
    await user.click(row)
    expect(onOpen).toHaveBeenCalledWith(golden)
  })

  it('needsOnly 过滤与计数；无会话空态', async () => {
    const onToggleNeeds = vi.fn()
    const user = userEvent.setup()
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    const plain = { ...golden, id: 'session:8', title: '跨机执行面', needs_human: false, unread: 0 }
    const { rerender } = render(<SessionSidebar sessions={[golden, plain]} {...defaultProps} onToggleNeeds={onToggleNeeds} />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('2 个会话')
    await user.click(screen.getByRole('button', { name: /需要你/ }))
    expect(onToggleNeeds).toHaveBeenCalled()
    rerender(<SessionSidebar sessions={[golden, plain]} {...defaultProps} needsOnly />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('1 个会话')
    expect(screen.queryByText('跨机执行面')).not.toBeInTheDocument()
    rerender(<SessionSidebar sessions={[]} {...defaultProps} />)
    expect(screen.getByText('（暂无会话）')).toBeInTheDocument()
  })

  it('行可拖：dragstart 写入会话 MIME 与 {sessionId,title} 载荷，拖源行降低透明度（B358.8 #1）', () => {
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    const view = render(<SessionSidebar sessions={[golden]} {...defaultProps} />)
    const row = screen.getByTestId('session-row')
    expect(row).toHaveAttribute('draggable', 'true')
    const dragData = { setData: vi.fn(), effectAllowed: '', dropEffect: '', types: [] as string[], getData: () => '' }
    fireEvent.dragStart(row, { dataTransfer: dragData })
    expect(dragData.setData).toHaveBeenCalledWith(DRAG_SESSION_MIME, JSON.stringify({ sessionId: golden.id, title: golden.title }))
    expect(dragData.effectAllowed).toBe('move')
    expect(row.className).toContain('opacity-50')
    fireEvent.dragEnd(row, { dataTransfer: dragData })
    expect(row.className).not.toContain('opacity-50')
    expect(view.container.querySelector('[data-drag-session]')).not.toBeNull()
  })

  it('项目筛选：选项目 → 列表过滤、计数随筛选走；切回全部恢复（B358.8 #6）', async () => {
    const user = userEvent.setup()
    const projectOfCard = (cardId: string) => ({ B1: 'handoff', B2: 'aim' })[cardId as 'B1' | 'B2'] ?? ''
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary! // cards: [B1 → handoff]
    const noCard = { ...golden, id: 'session:8', title: '跨机执行面', cards: undefined, needs_human: false, unread: 0 }
    const { rerender } = render(
      <SessionSidebar sessions={[golden, noCard]} {...defaultProps} projectOptions={['handoff', 'aim']} projectOfCard={projectOfCard} />,
    )
    expect(screen.getByTestId('session-total')).toHaveTextContent('2 个会话')
    const select = screen.getByTestId('session-project-filter') as HTMLSelectElement
    expect(select).toHaveValue('')
    await user.selectOptions(select, 'handoff')
    rerender(<SessionSidebar sessions={[golden, noCard]} {...defaultProps} projectFilter="handoff" projectOptions={['handoff', 'aim']} projectOfCard={projectOfCard} />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('1 个会话')
    expect(screen.getByText('架构物理化')).toBeInTheDocument()
    expect(screen.queryByText('跨机执行面')).not.toBeInTheDocument()
    // 切回「全部」恢复
    await user.selectOptions(screen.getByTestId('session-project-filter'), '')
    rerender(<SessionSidebar sessions={[golden, noCard]} {...defaultProps} projectOptions={['handoff', 'aim']} projectOfCard={projectOfCard} />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('2 个会话')
  })

  it('筛选单行（走查 09-17，对 board.html 原型）：项目下拉、需要你、计数同处一行纯文字项', () => {
    render(<SessionSidebar sessions={[]} {...defaultProps} projectOptions={['handoff']} />)
    const row = screen.getByTestId('session-filters')
    expect(row).toContainElement(screen.getByTestId('session-project-filter'))
    expect(row).toContainElement(screen.getByRole('button', { name: /需要你/ }))
    expect(row).toContainElement(screen.getByTestId('session-total'))
    // 纯文字项：筛选项无边框表单控件样式（原型 .im-filter 无 border）
    expect(screen.getByTestId('session-project-filter').className).not.toMatch(/border/)
  })

  // B406：expired 是 401 终止态——断线行与「暂无会话」空态都是假读数，一律让位
  // 给过期横幅；读取中也不再出现（轮询已停表，不存在"还在读"）。
  it('expired：过期横幅替代断线行与「暂无会话」空态，读取中一并抑制', () => {
    render(<SessionSidebar sessions={[]} {...defaultProps} loading errorText="无法连接 agentd（反代失败？）" expired />)
    expect(screen.getByText(/会话已失效/)).toBeInTheDocument()
    expect(screen.queryByText('（暂无会话）')).not.toBeInTheDocument()
    expect(screen.queryByText(/会话列表已断开/)).not.toBeInTheDocument()
    expect(screen.getByTestId('session-total')).not.toHaveTextContent('读取中')
  })
})

// —— B369.10 T6：compact 会话首页（chips 行 + 行内成员横排/群主行，岔口 5/8）——
describe('B369.10 compact 会话首页', () => {
  const golden = () => cases.find((c) => c.case === 'session-summary-golden')!.summary!
  const member = (identity: string): SessionMember => ({ identity, kind: 'agent', status: 'listening' })
  const withMembers = (members: SessionMember[], owner = 'user:sy'): SessionSummary => ({ ...golden(), members, owner })

  it('chips 行在上、筛选行在下：needs-count 在场，两态切换走既有 needsOnly 回调', async () => {
    const user = userEvent.setup()
    const onToggleNeeds = vi.fn()
    const { rerender } = render(<SessionSidebar sessions={[golden()]} {...defaultProps} compact onToggleNeeds={onToggleNeeds} />)
    // 次序：chips 行在项目筛选行之前（原型「筛选紧贴头部」阅读序）
    const chips = screen.getByTestId('session-filter-chips')
    expect(chips.compareDocumentPosition(screen.getByTestId('session-project-filter')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByTestId('needs-count')).toHaveTextContent('1')
    // needsOnly=false：需要你 chip 未按下，点它 → 回调翻转
    const needsChip = screen.getByRole('button', { name: /需要你/ })
    expect(needsChip).toHaveAttribute('aria-pressed', 'false')
    await user.click(needsChip)
    expect(onToggleNeeds).toHaveBeenCalledTimes(1)
    // needsOnly=true：全部 chip 未按下，点它 → 回调翻转；点已按下的需要你 chip 不再翻转
    rerender(<SessionSidebar sessions={[golden()]} {...defaultProps} compact needsOnly onToggleNeeds={onToggleNeeds} />)
    expect(screen.getByRole('button', { name: /需要你/ })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: '全部' }))
    expect(onToggleNeeds).toHaveBeenCalledTimes(2)
    await user.click(screen.getByRole('button', { name: /需要你/ }))
    expect(onToggleNeeds).toHaveBeenCalledTimes(2)
  })

  it('成员横排 ≤5 全显：逐枚身份染色块（aria-label=identity）+ 群主行', () => {
    const members = ['user:sy', 'cli:claude#1', 'cli:codex#2', 'cli:opencode#3', 'cli:grok#4'].map(member)
    render(<SessionSidebar sessions={[withMembers(members)]} {...defaultProps} compact />)
    const row = screen.getByTestId('session-members')
    const avatars = Array.from(row.querySelectorAll('span[aria-label]'))
    expect(avatars).toHaveLength(5)
    expect(avatars.map((a) => a.getAttribute('aria-label'))).toEqual(['user:sy', 'cli:claude#1', 'cli:codex#2', 'cli:opencode#3', 'cli:grok#4'])
    expect(avatars[0].textContent).toBe('us')
    expect(avatars[0].className).not.toContain('bg-amber')
    expect(screen.getByTestId('session-owner')).toHaveTextContent('群主：user:sy')
    expect(screen.queryByTestId('session-members-more')).toBeNull()
  })

  it('成员 >5 溢出「+N」：前 5 枚折后 N=总数−5', () => {
    const members = Array.from({ length: 8 }, (_, i) => member(`cli:agent-${i}`))
    render(<SessionSidebar sessions={[withMembers(members)]} {...defaultProps} compact />)
    expect(screen.getAllByText('cl').length).toBeGreaterThanOrEqual(5)
    expect(screen.getByTestId('session-members-more')).toHaveTextContent('+3')
  })

  it('owner 空串不渲染群主行；无成员不渲染横排', () => {
    render(<SessionSidebar sessions={[{ ...golden(), members: undefined, owner: '' }]} {...defaultProps} compact />)
    expect(screen.queryByTestId('session-members')).toBeNull()
    expect(screen.queryByTestId('session-owner')).toBeNull()
  })

  it('桌面反例锁：toggle 行原样、chips 行与成员横排/群主行均不渲染', () => {
    const members = Array.from({ length: 8 }, (_, i) => member(`cli:agent-${i}`))
    render(<SessionSidebar sessions={[withMembers(members)]} {...defaultProps} />)
    // toggle 行原样：aria-pressed 钮 + needs-count + session-total 读数
    expect(screen.getByRole('button', { name: /需要你/ })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('needs-count')).toBeInTheDocument()
    expect(screen.getByTestId('session-total')).toBeInTheDocument()
    expect(screen.queryByTestId('session-filter-chips')).toBeNull()
    expect(screen.queryByTestId('session-members')).toBeNull()
    expect(screen.queryByTestId('session-owner')).toBeNull()
  })
})
