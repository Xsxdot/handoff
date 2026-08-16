import { describe, expect, it, vi, afterEach } from 'vitest'
import { ApiError, fetchTasks, writeWorkspaceFile } from './client'

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

describe('fetchTasks', () => {
  // 这条钉的是「看板能看见远端机器上正在跑的任务」这件事本身：不带 scope=all
  // 时 agentd 只回本机任务，跨机任务（含唯一一条 running）永远进不了看板。
  it('必须带 ?scope=all —— 否则远端机器的任务在看板上完全不存在', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ machines: [], tasks: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)
    await fetchTasks()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/tasks?scope=all')
  })

  it('拆信封返回裸任务数组，远端任务带 machine 章', async () => {
    stubFetch(200, {
      machines: [{ name: '', ok: true }, { name: 'mac-02', ok: true }],
      tasks: [
        { id: 'a', state: 'completed', machine: '' },
        { id: 'b', state: 'running', machine: 'mac-02' },
      ],
    })
    const tasks = await fetchTasks()
    expect(tasks.map((t) => t.id)).toEqual(['a', 'b'])
    expect(tasks[1].machine).toBe('mac-02')
  })

  // 兜底：信封里 tasks 缺失/为 null 时给 []，否则看板会把「加载失败」和
  // 「一条任务都没有」混成同一种空白。
  it('tasks 缺失时返回空数组而不是 null', async () => {
    stubFetch(200, { machines: [] })
    await expect(fetchTasks()).resolves.toEqual([])
  })
})

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
