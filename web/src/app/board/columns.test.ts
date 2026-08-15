// 看板列与任务状态机映射的契约测试。
//
// 这张映射表是产品硬契约（spec §4.2）：改它必须同步本测试与 columns.ts。
//   pending → 等待执行；running/waiting_answer → 进行中（waiting_answer 带
//   「等你答复」标记）；waiting_review → Review；completed/failed → 完成
//   （failed 视觉区分）。
import { describe, expect, it } from 'vitest'
import {
  COLUMN_LABELS,
  isFailed,
  isWaitingAnswer,
  needsIntervention,
  stateBadgeVariant,
  stateLabel,
  stateToColumn,
  stateTone,
} from './columns'

describe('看板列与任务状态机的映射（硬契约）', () => {
  it('pending → 等待执行列', () => {
    expect(stateToColumn('pending')).toBe('waiting')
  })

  it('running / waiting_answer → 进行中列，waiting_answer 带「等你答复」标记', () => {
    expect(stateToColumn('running')).toBe('active')
    expect(stateToColumn('waiting_answer')).toBe('active')
    expect(isWaitingAnswer('waiting_answer')).toBe(true)
    expect(isWaitingAnswer('running')).toBe(false)
    expect(stateLabel('waiting_answer')).toBe('等你答复')
  })

  it('waiting_review → Review 列', () => {
    expect(stateToColumn('waiting_review')).toBe('review')
  })

  it('completed / failed → 完成列，failed 需要视觉区分', () => {
    expect(stateToColumn('completed')).toBe('done')
    expect(stateToColumn('failed')).toBe('done')
    expect(isFailed('failed')).toBe(true)
    expect(isFailed('completed')).toBe(false)
  })

  it('列标题完整（顺序等待执行/进行中/Review/完成）', () => {
    expect(COLUMN_LABELS).toEqual({
      waiting: '等待执行',
      active: '进行中',
      review: 'Review',
      done: '完成',
    })
  })

  it('未知状态回退「进行中」，任务不消失', () => {
    expect(stateToColumn('new_unknown_state')).toBe('active')
  })
})

describe('状态的视觉基调（硬契约）', () => {
  it('六个状态各有其基调', () => {
    expect(stateTone('pending')).toBe('idle')
    expect(stateTone('running')).toBe('active')
    expect(stateTone('waiting_answer')).toBe('intervention')
    expect(stateTone('waiting_review')).toBe('intervention')
    expect(stateTone('completed')).toBe('done')
    expect(stateTone('failed')).toBe('failed')
  })

  // 刻意与 stateToColumn 的回退值不同：分列回退 active 是为了让未知状态
  // 显眼地出现在「进行中」列（看得见）；染色回退 idle 是因为把一个没见过
  // 的状态涂成绿色或琥珀色，等于替它编造语义。两者共同保证的是「不消失」。
  it('未知状态基调回退 idle，不编造语义', () => {
    expect(stateTone('new_unknown_state')).toBe('idle')
    expect(stateToColumn('new_unknown_state')).toBe('active')
  })

  // 这条同时钉住「干预态口径与 filter.ts 的 pendingOnly、counts.ts 的
  // pending 三处一致」。三处任何一处改了这个集合，这条会红。
  it('干预态只认 waiting_answer 与 waiting_review', () => {
    expect(needsIntervention('waiting_answer')).toBe(true)
    expect(needsIntervention('waiting_review')).toBe(true)
    expect(needsIntervention('pending')).toBe(false)
    expect(needsIntervention('running')).toBe(false)
    expect(needsIntervention('completed')).toBe(false)
    expect(needsIntervention('failed')).toBe(false)
  })

  it('详情页 Badge：两个干预态改用 intervention 档', () => {
    expect(stateBadgeVariant('waiting_answer')).toBe('intervention')
    expect(stateBadgeVariant('waiting_review')).toBe('intervention')
    expect(stateBadgeVariant('failed')).toBe('destructive')
    expect(stateBadgeVariant('running')).toBe('default')
    expect(stateBadgeVariant('completed')).toBe('secondary')
    expect(stateBadgeVariant('new_unknown_state')).toBe('secondary')
  })
})
