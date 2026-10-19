// ListView.test.tsx —— 锁定列表直接投影协调者席位与来源词。
// 边界：从 CardView 列表字段渲染，不调用卡详情；未知来源必须显式暴露为异常。
import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { SEAT_MODE_HINT, SEAT_MODE_UNKNOWN_LABEL, type CardView } from '../../api/ledger'
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

  // B435 Q5（C-305）：席位列附 driver_mode 原值 + 悬停说明；坐着无 mode →「模式未知」；空座无模式。
  it('席位模式：push/pull 原值与悬停说明，来源仍在；坐着无 mode 标未知；空座不出模式', () => {
    render(<ListView
      cards={[
        card({ id: 'B-push', driver_session: 'cli:opencode#s1', driver_source: 'coordinate', driver_mode: 'push' }),
        card({ id: 'B-pull', driver_session: 'cli:claude#s2', driver_source: 'bind', driver_mode: 'pull' }),
        card({ id: 'B-nomode', driver_session: 'cli:old#s3', driver_source: 'bind' }),
        card({ id: 'B-empty' }),
      ]}
      includeArchived={false}
      onIncludeArchivedChange={() => {}}
      onOpen={() => {}}
    />)
    const modeOf = (id: string) => within(screen.getByText(id).closest('tr')!).queryByTestId('list-seat-mode')
    expect(modeOf('B-push')).toHaveTextContent('push')
    expect(modeOf('B-push')).toHaveAttribute('title', SEAT_MODE_HINT.push)
    expect(modeOf('B-pull')).toHaveTextContent('pull')
    expect(modeOf('B-pull')).toHaveAttribute('title', SEAT_MODE_HINT.pull)
    expect(modeOf('B-nomode')).toHaveTextContent(SEAT_MODE_UNKNOWN_LABEL)
    expect(modeOf('B-empty')).toBeNull()
    expect(within(screen.getByText('B-push').closest('tr')!).getByText('叫机器人')).toBeInTheDocument()
    expect(within(screen.getByText('B-pull').closest('tr')!).getByText('坐下')).toBeInTheDocument()
  })
})
