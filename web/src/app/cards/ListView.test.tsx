// ListView.test.tsx —— 锁定列表直接投影协调者席位与来源词。
// 边界：从 CardView 列表字段渲染，不调用卡详情；未知来源必须显式暴露为异常。
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { CardView } from '../../api/ledger'
import { ListView } from './ListView'

function card(over: Partial<CardView> = {}): CardView {
  return {
    id: 'B1', title: '卡', status: '进行中', priority: '中', project: 'handoff',
    workflow: 'bug', parent: '', base_branch: '', attachments: [], following: '',
    blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0,
    children_total: 0, children_done: 0, conflict: false, open_tickets: 0, ...over,
  }
}

describe('列表协调者席位', () => {
  it('空座和未知来源不会伪装成叫机器人', () => {
    render(<ListView
      cards={[
        card({ id: 'B-empty' }),
        card({ id: 'B-invalid', driver_session: 'cli:old#session', driver_source: 'manual' as never }),
      ]}
      includeArchived={false}
      onIncludeArchivedChange={() => {}}
      onOpen={() => {}}
    />)
    expect(screen.getByText('空座')).toBeInTheDocument()
    expect(screen.getByText('席位异常')).toBeInTheDocument()
  })
})
