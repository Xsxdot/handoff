import { describe, expect, it } from 'vitest'
import type { CardView } from '../../api/ledger'
import { boardColumnFor, boardColumns, cardsInColumn, defaultBoardLayout, filterNeeds, mergeStateOrder, needsAttention, normalizeBoardLayout, visibleColumns } from './columns'

const card = (over: Partial<CardView>): CardView => ({
  id: 'B1', title: 't', status: '待办', priority: '中', project: 'p', workflow: 'bug', parent: '',
  base_branch: '', attachments: [], following: '', blocked: false, blocked_by: [],
  merged_count: 0, needs: '', open_decisions: 0, children_total: 0, children_done: 0,
  conflict: false, open_tickets: 0, ...over,
})

describe('工作项看板契约', () => {
	it('默认五列、状态映射与未知状态兜底固定', () => {
		const layout = defaultBoardLayout(['待办', '终止', '自定义'])
		expect(layout.columns).toEqual(['代办', '沟通中', '进行中', '审核中', '结束'])
		expect(layout.state_to_column['待办']).toBe('代办')
		expect(layout.state_to_column['终止']).toBe('结束')
		expect(boardColumnFor('自定义', layout)).toBe('进行中')
		expect(boardColumns(['待办'], layout)).toEqual(layout.columns)
	})

	it('非法布局退回默认且未知映射使用安全兜底', () => {
		const layout = normalizeBoardLayout({ columns: ['a', 'a', 'b', 'c', 'd'], state_to_column: {}, fallback: 'z' }, ['待办'])
		expect(layout.columns).toEqual(['代办', '沟通中', '进行中', '审核中', '结束'])
	})
  it('被并卡不在看板成列（跟随只在列表/抽屉可见）', () => {
    const cards = [card({ id: 'B1' }), card({ id: 'B2', following: 'B1' })]
    expect(cardsInColumn(cards, '代办').map((item) => item.id)).toEqual(['B1'])
  })

  it('需要你 = 等人 ∪ open 裁决 ∪ conflict ∪ 未决工单', () => {
    expect(needsAttention(card({ needs: '审阅超轮' }))).toBe(true)
    expect(needsAttention(card({ open_decisions: 1 }))).toBe(true)
    expect(needsAttention(card({ conflict: true }))).toBe(true)
    expect(needsAttention(card({ open_tickets: 2 }))).toBe(true)
    expect(needsAttention(card({}))).toBe(false)
    expect(filterNeeds([card({}), card({ id: 'B2', needs: 'x' })], true)).toHaveLength(1)
  })

  it('列序固定为默认五列而非平铺工作流状态', () => {
    expect(boardColumns(['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成']))
      .toEqual(['代办', '沟通中', '进行中', '审核中', '结束'])
  })
})

describe('多工作流的列序', () => {
  const feature = ['待办', '已出spec', '进行中', '待审阅', '待合并', '已完成']
  const bug = ['待办', '进行中', '待审阅', '已完成']

  it('并集要按流程先后拓扑合并，不是按出现先后拼', () => {
    // 取并集的旧写法会得到 待办→进行中→待审阅→已完成→已出spec→待合并，
    // 已出spec 掉到最后（2026-08-19 真机看到）
    expect(mergeStateOrder([bug, feature])).toEqual(feature)
    expect(mergeStateOrder([feature, bug])).toEqual(feature)
  })

  it('互不相交的两条流按输入先后接起来', () => {
    expect(mergeStateOrder([['甲', '乙'], ['丙', '丁']])).toEqual(['甲', '乙', '丙', '丁'])
  })

  it('先后关系成环时不丢状态（环上的按首次出现兜底）', () => {
    expect(mergeStateOrder([['甲', '乙'], ['乙', '甲']]).sort()).toEqual(['乙', '甲'].sort())
  })
})

describe('需要你筛选时的空列', () => {
  const cards = [card({ id: 'B1', status: '待办', needs: '前置已终止' })]
  it('筛选开着时折叠空列，命中的卡不被空列挤出视野', () => {
    expect(visibleColumns(['代办', '进行中', '审核中'], cards, true)).toEqual(['代办'])
  })
  it('筛选关着时列全在（空列也画，看板要能看出流程形状）', () => {
    expect(visibleColumns(['代办', '进行中', '审核中'], cards, false)).toEqual(['代办', '进行中', '审核中'])
  })
})
