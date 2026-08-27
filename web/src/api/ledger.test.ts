import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { attachFile, createCard, detachFile, migrateCard, patchCard, putFlow, runCardStep } from './ledger'

// 直接打桩 fetch：这一层要验的是「方法、路径、请求体」，不是渲染。
const calls: Array<{ url: string; init: RequestInit }> = []

beforeEach(() => {
  calls.length = 0
  vi.stubGlobal('fetch', vi.fn((url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return Promise.resolve(new Response(JSON.stringify({ ok: true, id: 'B99' }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

const bodyOf = (index: number) => JSON.parse(String(calls[index].init.body))

describe('账本写操作的线格式', () => {
  it('建卡 POST /api/cards，字段名与 Go 侧一字不差', async () => {
    await createCard({ title: 'T', project: 'p', workflow: 'bug', priority: '高', base_branch: 'feat/x' })
    expect(calls[0].url).toContain('/api/cards')
    expect(calls[0].init.method).toBe('POST')
    expect(bodyOf(0)).toEqual({ title: 'T', project: 'p', workflow: 'bug', priority: '高', base_branch: 'feat/x' })
  })

  it('未定性建卡可省略 workflow，迁移一次提交目标流/列/版本', async () => {
    await createCard({ title: 'T', project: 'p' })
    expect(bodyOf(0)).toEqual({ title: 'T', project: 'p' })
    await migrateCard('B1', { workflow: 'domain', status: '拆解' })
    expect(calls[1].url).toContain('/api/cards/B1/migrate')
    expect(calls[1].init.method).toBe('POST')
    expect(bodyOf(1)).toEqual({ workflow: 'domain', status: '拆解' })
  })

  it('改卡用 PATCH，且只发调用方给的字段——缺字段在后端表示「不动」', async () => {
    await patchCard('B1', { priority: '低' })
    expect(calls[0].init.method).toBe('PATCH')
    expect(bodyOf(0)).toEqual({ priority: '低' })
    expect(Object.keys(bodyOf(0))).not.toContain('title')
  })

  it('挂附件 POST、摘附件 DELETE，路径都带卡号', async () => {
    await attachFile('B1', 'plan', 'docs/p.md')
    await detachFile('B1', 'docs/p.md')
    expect(calls[0].url).toContain('/api/cards/B1/attachments')
    expect(calls[0].init.method).toBe('POST')
    expect(calls[1].init.method).toBe('DELETE')
    expect(bodyOf(1)).toEqual({ path: 'docs/p.md' })
  })

  it('发工作流新版本用 PUT /api/flows/{name}', async () => {
    await putFlow('feature', [{ name: '待办', next: '进行中' }])
    expect(calls[0].url).toContain('/api/flows/feature')
    expect(calls[0].init.method).toBe('PUT')
    expect(bodyOf(0).nodes).toHaveLength(1)
  })

  it('发工作流新版本时透传五列看板布局', async () => {
    await putFlow('feature', [{ name: '待办' }], {
      columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
      state_to_column: { 待办: '代办' }, fallback: '进行中',
    })
    expect(bodyOf(0).board).toEqual({
      columns: ['代办', '沟通中', '进行中', '审核中', '结束'],
      state_to_column: { 待办: '代办' }, fallback: '进行中',
    })
  })

  it('卡号里的特殊字符要被编码，不能直接拼进 URL', async () => {
    await patchCard('B1/../admin', { priority: '低' })
    expect(calls[0].url).not.toContain('/../')
  })

  it('节点执行接受任意节点名，不再只认 review|merge', async () => {
    await runCardStep('B1', '收尾合并')
    expect(bodyOf(0)).toEqual({ step: '收尾合并' })
  })
})
