// 看板列与任务状态机映射的契约测试。
//
// 这张映射表是产品硬契约（spec §4.2）：改它必须同步本测试与 columns.ts。
//   pending → 等待执行；running/waiting_answer → 进行中（waiting_answer 带
//   「等你答复」标记）；waiting_review → Review；completed/failed → 完成
//   （failed 视觉区分）。
import { describe, expect, it } from 'vitest'
import { COLUMN_LABELS, isFailed, isWaitingAnswer, stateLabel, stateToColumn } from './columns'

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
