// taskName.test.ts —— 任务显示名的纯函数表驱动测试。
import { describe, expect, it } from 'vitest'
import { taskDisplayName } from './taskName'

describe('taskDisplayName', () => {
  it('name 非空时用 name', () => {
    expect(taskDisplayName({ name: '审 B264', plan_summary: 'x' })).toBe('审 B264')
  })

  it('name 为空时回退 plan_summary', () => {
    expect(taskDisplayName({ name: '', plan_summary: '摘要' })).toBe('摘要')
  })

  it('两者都空时用「（无名称）」', () => {
    expect(taskDisplayName({ name: '', plan_summary: '' })).toBe('（无名称）')
  })
})
