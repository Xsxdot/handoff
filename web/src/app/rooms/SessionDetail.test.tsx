// SessionDetail.test.tsx —— 详情三块：成员状态四值渲染、节点、timeline（含未知 kind 兜底）。
// 夹具直接取孪生金样本（同一 JSON）——组件渲染与 Go 编码器同源。
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
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

  it('空座席位成员渲染「无人推」与空座标签', () => {
    const summaryCase = cases.find((c) => c.case === 'session-summary-archived-empty')!.summary!
    const withEmptySeat = { ...summaryCase, members: [{ identity: '', kind: 'seat' as const, card_id: 'B2', card_title: '空座卡', status: 'empty' as const }] }
    render(<SessionDetail detail={{ summary: withEmptySeat, nodes: [], timeline: [] }} />)
    expect(screen.getByText(/无人推/)).toBeInTheDocument()
    expect(screen.getByTestId('member-status-0')).toHaveTextContent('空座')
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
})
