import { describe, expect, it } from 'vitest'
import type { CardView } from '../../api/ledger'
import { boardColumns, cardsInColumn, filterNeeds, needsAttention } from './columns'

const card = (over: Partial<CardView>): CardView => ({
  id: 'B1', title: 't', status: '待办', priority: '中', project: 'p', workflow: 'bug', parent: '',
  base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
  needs: '', open_decisions: 0, conflict: false, open_tickets: 0, ...over,
})

describe('工作项看板契约', () => {
  it('被并卡不在看板成列（跟随只在列表/抽屉可见）', () => {
    const cards = [card({ id: 'B1' }), card({ id: 'B2', following: 'B1' })]
    expect(cardsInColumn(cards, '待办').map((item) => item.id)).toEqual(['B1'])
  })

  it('需要你 = 等人 ∪ open 裁决 ∪ conflict ∪ 未决工单', () => {
    expect(needsAttention(card({ needs: '审阅超轮' }))).toBe(true)
    expect(needsAttention(card({ open_decisions: 1 }))).toBe(true)
    expect(needsAttention(card({ conflict: true }))).toBe(true)
    expect(needsAttention(card({ open_tickets: 2 }))).toBe(true)
    expect(needsAttention(card({}))).toBe(false)
    expect(filterNeeds([card({}), card({ id: 'B2', needs: 'x' })], true)).toHaveLength(1)
  })

  it('列序 = 工作流状态序 + 终止收尾', () => {
    expect(boardColumns(['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成']))
      .toEqual(['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成', '终止'])
  })
})
