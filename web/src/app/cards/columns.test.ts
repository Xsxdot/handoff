import { describe, expect, it } from 'vitest'
import type { CardView } from '../../api/ledger'
import { boardColumnFor, boardColumns, cardsInColumn, DEFAULT_BOARD_COLUMNS, defaultBoardLayout, filterNeeds, mergeIntoDefaultColumns, mergeStateOrder, mergedLayoutFor, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
import type { BoardLayout, CardLayoutResolver } from './columns'

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

  it('只在同列存在多个节点时返回节点标签，映射变为一对一则隐藏', () => {
    const nodes = ['待审阅', '待合并', '进行中']
    const layout = defaultBoardLayout(nodes)
    expect(nodeLabelFor('待审阅', nodes, layout)).toBe('待审阅')
    expect(nodeLabelFor('进行中', nodes, layout)).toBeUndefined()

    const oneToOne = { ...layout, state_to_column: { 待审阅: '沟通中', 待合并: '审核中', 进行中: '进行中' } }
    expect(nodeLabelFor('待审阅', nodes, oneToOne)).toBeUndefined()
  })

  it('未显式映射的节点也按看板兜底列判断是否同列', () => {
    const layout = { columns: ['代办', '沟通中', '进行中', '审核中', '结束'], fallback: '进行中', state_to_column: {} }
    expect(nodeLabelFor('自定义一', ['自定义一', '自定义二'], layout)).toBe('自定义一')
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

describe('B412 合并视图按各流当前版本 board 归列', () => {
  const charterBoard: BoardLayout = {
    columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
    state_to_column: { spec: '沟通中', review: '审核中', finish: '结束', 待办: '代办', plan: '进行中', implement: '进行中' },
    fallback: '进行中',
  }
  const customBoard: BoardLayout = {
    columns: ['收集', '沟通', '实现', '验收', '完成'],
    state_to_column: { spec: '收集' }, fallback: '实现',
  }

  it('按卡的流当前版本 board 解析列，列集合恒默认五列', () => {
    const layout = mergedLayoutFor(card({ workflow: 'charter', status: 'spec' }),
      (name) => (name === 'charter' ? charterBoard : undefined))
    expect(boardColumnFor('spec', layout)).toBe('沟通中')
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('映射目标不在默认五列的状态落「进行中」（D3）', () => {
    const layout = mergedLayoutFor(card({ workflow: 'x', status: 'spec' }), () => customBoard)
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
    expect(boardColumnFor('spec', layout)).toBe('进行中')
    expect(mergeIntoDefaultColumns(customBoard).columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('流未知（flows 未加载/已删改名）退回默认映射，不抛错', () => {
    const layout = mergedLayoutFor(card({ workflow: 'gone', status: '待审阅' }), () => undefined)
    expect(boardColumnFor('待审阅', layout)).toBe('审核中')
    expect(layout.columns).toEqual(DEFAULT_BOARD_COLUMNS)
  })

  it('合并视图列集合恒默认五列，单流视图列集合用该流自身列名', () => {
    expect(mergedLayoutFor(card({ workflow: 'x', status: '待办' }), () => customBoard).columns)
      .toEqual(DEFAULT_BOARD_COLUMNS)
    expect(normalizeBoardLayout(customBoard, ['待办']).columns).toEqual(customBoard.columns)
  })

  it('同一视图内不同流的卡各按其流映射落列（多流夹具）；被并卡不成列', () => {
    const resolver: CardLayoutResolver = (item) => mergedLayoutFor(
      item, (name) => (name === 'charter' ? charterBoard : undefined))
    const cards = [
      card({ id: 'Bs', workflow: 'charter', status: 'spec' }),
      card({ id: 'Br', workflow: 'charter', status: 'review' }),
      card({ id: 'Bp', workflow: 'charter', status: 'plan' }),
      card({ id: 'Bx', workflow: 'other', status: 'spec' }),
      card({ id: 'Bf', workflow: 'charter', status: 'spec', following: 'Bs' }),
    ]
    expect(cardsInColumn(cards, '沟通中', resolver).map((item) => item.id)).toEqual(['Bs'])
    expect(cardsInColumn(cards, '审核中', resolver).map((item) => item.id)).toEqual(['Br'])
    expect(cardsInColumn(cards, '进行中', resolver).map((item) => item.id)).toEqual(['Bp', 'Bx'])
  })
})
