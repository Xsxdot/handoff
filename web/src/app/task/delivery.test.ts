// delivery.test.ts —— 报工 trailer 提取：尾部 JSON → 交付摘要，其余情况返回 null。
import { describe, expect, it } from 'vitest'
import { extractDelivery } from './delivery'

const TRAILER = '{"branch":"bench/b93","commit":"4e3de5e","summary":"三缺口全落地"}'

describe('extractDelivery', () => {
  it('正文 + 尾部 trailer：拆出摘要与剩余正文', () => {
    const r = extractDelivery(`B93 全部完成。全量回归 0 FAIL。\n\n${TRAILER}`)
    expect(r).not.toBeNull()
    expect(r!.delivery).toEqual({ branch: 'bench/b93', commit: '4e3de5e', summary: '三缺口全落地' })
    expect(r!.body).toBe('B93 全部完成。全量回归 0 FAIL。')
  })
  it('纯 trailer（无正文）也能提取，body 为空串', () => {
    const r = extractDelivery(TRAILER)!
    expect(r.delivery.commit).toBe('4e3de5e')
    expect(r.body).toBe('')
  })
  it('JSON 不在末尾 / 不是对象 / 无已知字段 → null（原样当正文）', () => {
    expect(extractDelivery(`${TRAILER}\n后面还有话`)).toBeNull()
    expect(extractDelivery('末尾是 [1,2,3]')).toBeNull()
    expect(extractDelivery('末尾 {"foo":"bar"}')).toBeNull()
    expect(extractDelivery('没有任何 JSON')).toBeNull()
  })
  it('非法 JSON → null，不抛异常', () => {
    expect(extractDelivery('话 {"branch": 断掉了')).toBeNull()
  })
})
