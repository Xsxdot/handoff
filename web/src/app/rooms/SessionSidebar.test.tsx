// SessionSidebar.test.tsx —— 列表行（未读角标/需要你标签/预览）、筛选与拖拽源。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import fixture from '../../api/testdata/RoomsFixture.json'
import type { SessionSummary } from '../../api/rooms'
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
})
