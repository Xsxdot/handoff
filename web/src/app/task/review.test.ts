// 审批台应答编码与工单解析的契约测试。
//
// answer 编码是硬契约（manager.go 的 gateDecision：trim 后严格等于 "allow" 才
// 回 "once"）：批准恒为 "allow"、拒绝带理由 "deny: <理由>"、提问原样透传。
// 拒绝无理由时 UI 不允许提交（validateReply 拦路）——不填理由模型会原地重试
// 同样的操作、白烧一轮。
import { describe, expect, it } from 'vitest'
import type { Ticket } from '../../api/types'
import { buildTicketAnswer, parseTicketRequest, validateReply } from './review'

describe('应答编码（硬契约）', () => {
  it('gate 批准 → 恒为 "allow"', () => {
    expect(buildTicketAnswer('gate', 'approve', '', '')).toBe('allow')
  })

  it('gate 拒绝 → "deny: <理由>"，理由 trim', () => {
    expect(buildTicketAnswer('gate', 'deny', '  太危险  ', '')).toBe('deny: 太危险')
  })

  it('ask → 自由文本原样透传', () => {
    expect(buildTicketAnswer('ask', 'answer', '', '用 pgx 不用 gorm')).toBe('用 pgx 不用 gorm')
  })
})

describe('拒绝必须带理由才能提交（硬契约）', () => {
  it('gate 拒绝且理由为空 → 校验失败（阻止提交）', () => {
    expect(validateReply('gate', 'deny', '')).not.toBeNull()
    expect(validateReply('gate', 'deny', '   ')).not.toBeNull()
  })

  it('gate 拒绝且理由非空 → 校验通过', () => {
    expect(validateReply('gate', 'deny', '太危险')).toBeNull()
  })

  it('gate 批准无需理由', () => {
    expect(validateReply('gate', 'approve', '')).toBeNull()
  })

  it('ask 回答无需理由', () => {
    expect(validateReply('ask', 'answer', '')).toBeNull()
  })
})

describe('工单 request 解析（展示读工单、不读事件）', () => {
  const ticket = (request: unknown): Ticket => ({
    id: 'tk-1',
    task_id: 't1',
    kind: 'gate',
    request,
    created_at: '2026-08-11T10:30:00+08:00',
  })

  it('gate：取 permission 全文', () => {
    const r = parseTicketRequest(ticket({ kind: 'gate', permission: 'Bash: rm -rf /' }))
    expect(r.kind).toBe('gate')
    expect(r.text).toBe('Bash: rm -rf /')
  })

  it('ask：取 question 全文', () => {
    const r = parseTicketRequest(ticket({ kind: 'ask', question: '表结构用单数还是复数?' }))
    expect(r.kind).toBe('ask')
    expect(r.text).toBe('表结构用单数还是复数?')
  })

  it('形状不符（历史样本 {"cmd":…}）回退 JSON 原文，不吞数据', () => {
    const r = parseTicketRequest(ticket({ cmd: 'go build ./...' }))
    expect(r.text).toContain('go build ./...')
  })

  it('request 是 JSON 字符串时先 parse 再解析', () => {
    const r = parseTicketRequest(ticket('{"kind":"ask","question":"继续吗"}'))
    expect(r.kind).toBe('ask')
    expect(r.text).toBe('继续吗')
  })
})
