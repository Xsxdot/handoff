// SessionDetail.test.tsx —— 详情抽屉 body：成员状态四值渲染（含群主标识）、会话卡
// 列表、节点、timeline（含未知 kind 兜底）、归档入口。
// 夹具直接取孪生金样本（同一 JSON）——组件渲染与 Go 编码器同源。
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import fixture from '../../api/testdata/RoomsFixture.json'
import type { SessionDetail as SessionDetailDTO, SessionSummary } from '../../api/rooms'
import { SessionDetail } from './SessionDetail'

const cases = fixture as { case: string; detail?: SessionDetailDTO; summary?: SessionSummary }[]

describe('SessionDetail', () => {
  it('成员块渲染四值状态中文标签，DOM 不出现在线字样（反例断言）', () => {
    const summaryCase = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    render(<SessionDetail detail={{ summary: summaryCase, nodes: [], timeline: [] }} />)
    expect(screen.getByTestId('member-status-0')).toHaveTextContent('最后活跃')
    expect(screen.getByTestId('member-status-1')).toHaveTextContent('工作中')
    expect(document.body.textContent).not.toContain('在线')
    expect(document.body.textContent).not.toContain('online')
  })

  it('群主标识：identity 等于 summary.owner 的成员行带「群主」badge（B358.8 #3）', () => {
    const summaryCase = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    render(<SessionDetail detail={{ summary: summaryCase, nodes: [], timeline: [] }} />)
    expect(screen.getByTestId('member-owner-0')).toHaveTextContent('群主')
    // 空座行不标群主
    expect(screen.queryByTestId('member-owner-1')).toBeNull()
  })

  it('空座席位成员渲染「无人推」与空座标签', () => {
    const summaryCase = cases.find((c) => c.case === 'session-summary-archived-empty')!.summary!
    const withEmptySeat = { ...summaryCase, members: [{ identity: '', kind: 'seat' as const, card_id: 'B2', card_title: '空座卡', status: 'empty' as const }] }
    render(<SessionDetail detail={{ summary: withEmptySeat, nodes: [], timeline: [] }} />)
    expect(screen.getByText(/无人推/)).toBeInTheDocument()
    expect(screen.getByTestId('member-status-0')).toHaveTextContent('空座')
  })

  it('会话卡列表行：回调 onOpenCard；空座标注「还没配人」（原 chips 数据改列表行）', async () => {
    const onOpenCard = vi.fn()
    const user = userEvent.setup()
    const summaryCase = { ...cases.find((c) => c.case === 'session-summary-golden')!.summary!, cards: [{ card_id: 'B1', title: '竖切卡', status: '进行中', seat: 'cli:opencode#s1' }, { card_id: 'B3', status: '待办' }] }
    render(<SessionDetail detail={{ summary: summaryCase, nodes: [], timeline: [] }} onOpenCard={onOpenCard} />)
    const rows = screen.getAllByTestId('session-card-row')
    expect(rows[0]).toHaveTextContent('竖切卡')
    expect(rows[1]).toHaveTextContent('空座 · 还没配人')
    await user.click(rows[1])
    expect(onOpenCard).toHaveBeenCalledWith('B3')
  })

  it('任务节点与 timeline 渲染；timeline detail 原样透传不二次解释', () => {
    const detail = cases.find((c) => c.case === 'session-detail-golden')!.detail!
    render(<SessionDetail detail={detail} />)
    expect(screen.getByTestId('session-node-0')).toHaveTextContent('implement')
    expect(screen.getByTestId('timeline-row-3')).toHaveTextContent('席位换绑')
    expect(screen.getByTestId('timeline-row-3')).toHaveTextContent('cli:grok#s2')
    expect(screen.getByTestId('timeline-row-5')).toHaveTextContent('卡收口')
  })

  it('未知 timeline kind 原样显示，不白屏', () => {
    const detail = cases.find((c) => c.case === 'session-detail-golden')!.detail!
    const rogue = { ...detail, timeline: [{ seq: 99, kind: 'weird_kind' as never, created_at: '1970-01-01T00:00:00Z' }] }
    render(<SessionDetail detail={rogue} />)
    expect(screen.getByTestId('timeline-row-0')).toHaveTextContent('weird_kind')
  })

  it('归档入口：未归档显按钮回调 onArchive；已归档只给只读说明', async () => {
    const onArchive = vi.fn()
    const user = userEvent.setup()
    const summaryCase = cases.find((c) => c.case === 'session-summary-golden')!.summary!
    const view = render(<SessionDetail detail={{ summary: summaryCase, nodes: [], timeline: [] }} onArchive={onArchive} />)
    await user.click(screen.getByTestId('session-archive'))
    expect(onArchive).toHaveBeenCalledOnce()

    const archived = cases.find((c) => c.case === 'session-summary-archived-empty')!.summary!
    view.rerender(<SessionDetail detail={{ summary: archived, nodes: [], timeline: [] }} onArchive={onArchive} />)
    expect(screen.queryByTestId('session-archive')).toBeNull()
    expect(screen.getByText('会话已归档（只读）。')).toBeInTheDocument()
  })
})
