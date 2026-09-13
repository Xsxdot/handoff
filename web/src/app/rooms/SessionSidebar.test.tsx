// SessionSidebar.test.tsx —— 列表行（未读角标/需要你标签/预览）与筛选。
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import fixture from '../../api/testdata/RoomsFixture.json'
import type { SessionSummary } from '../../api/rooms'
import { SessionSidebar } from './SessionSidebar'

const cases = fixture as { case: string; summary?: SessionSummary }[]

describe('SessionSidebar', () => {
  it('fixture 行渲染未读角标与需要你标签，点击行回调会话', async () => {
    const onOpen = vi.fn()
    const user = userEvent.setup()
    const golden = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    render(<SessionSidebar sessions={[golden]} loading={false} errorText="" needsOnly={false} onToggleNeeds={() => {}} onOpen={onOpen} onCreate={() => {}} />)
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
    const { rerender } = render(<SessionSidebar sessions={[golden, plain]} loading={false} errorText="" needsOnly={false} onToggleNeeds={onToggleNeeds} onOpen={() => {}} onCreate={() => {}} />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('2 个会话')
    await user.click(screen.getByRole('button', { name: /需要你/ }))
    expect(onToggleNeeds).toHaveBeenCalled()
    rerender(<SessionSidebar sessions={[golden, plain]} loading={false} errorText="" needsOnly onToggleNeeds={() => {}} onOpen={() => {}} onCreate={() => {}} />)
    expect(screen.getByTestId('session-total')).toHaveTextContent('1 个会话')
    expect(screen.queryByText('跨机执行面')).not.toBeInTheDocument()
    rerender(<SessionSidebar sessions={[]} loading={false} errorText="" needsOnly={false} onToggleNeeds={() => {}} onOpen={() => {}} onCreate={() => {}} />)
    expect(screen.getByText('（暂无会话）')).toBeInTheDocument()
  })
})
