// eventPhrase.test.ts —— 事件人话化映射：白名单内中文短语，白名单外原样透出。
import { describe, expect, it } from 'vitest'
import { eventPhrase } from './eventPhrase'

describe('eventPhrase', () => {
  it('工单类事件是 warn 并指向工单面板', () => {
    const p = eventPhrase('permission_request')
    expect(p.tone).toBe('warn')
    expect(p.text).toContain('权限工单')
    expect(p.text).toContain('工单面板')
    expect(eventPhrase('question').tone).toBe('warn')
  })
  it('生命周期事件是 info 中文短语', () => {
    expect(eventPhrase('completed')).toEqual({ text: '一轮结束，进入待审', tone: 'info' })
    expect(eventPhrase('turn_failed').text).toBe('回合失败')
    expect(eventPhrase('failed').tone).toBe('warn')
    expect(eventPhrase('stalled').text).toContain('看门狗')
  })
  it('未知类型原样透出，不吞', () => {
    expect(eventPhrase('brand_new_event')).toEqual({ text: 'brand_new_event', tone: 'info' })
  })
})
