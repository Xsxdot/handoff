import { describe, expect, it, vi, afterEach } from 'vitest'
import { ApiError, writeWorkspaceFile } from './client'

afterEach(() => vi.unstubAllGlobals())

function stubFetch(status: number, body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
}

describe('writeWorkspaceFile', () => {
  it('成功时返回新哈希与新大小', async () => {
    stubFetch(200, { sha256: 'abc123', size: 42 })
    await expect(writeWorkspaceFile('/w/b2-b3', 'go.mod', { content: 'x', base_sha256: 'old' }))
      .resolves.toEqual({ sha256: 'abc123', size: 42 })
  })

  it('409 抛的 ApiError 必须带上 body——冲突界面的两个出口都要用 current', async () => {
    stubFetch(409, {
      error: '文件已被改动',
      current: { content: '别人的内容', size: 6, sha256: 'newhash' },
    })
    const err = (await writeWorkspaceFile('/w/b2-b3', 'go.mod', {
      content: 'x',
      base_sha256: 'old',
    }).catch((e) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(409)
    expect(err.message).toBe('文件已被改动')
    expect((err.body as { current: { sha256: string } }).current.sha256).toBe('newhash')
  })

  it('400 的中文原文照旧透传', async () => {
    stubFetch(400, { error: '不允许写入 .git 目录' })
    await expect(
      writeWorkspaceFile('/w/b2-b3', '.git/config', { content: 'x', base_sha256: 'old' }),
    ).rejects.toThrow('不允许写入 .git 目录')
  })
})
