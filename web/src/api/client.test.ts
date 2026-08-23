import { describe, expect, it, vi, afterEach } from 'vitest'
import {
  addMachine,
  ApiError,
  createWorktree,
  deleteMachine,
  fetchDiscipline,
  fetchDisciplineFile,
  fetchProjectBranches,
  fetchProjects,
  fetchTasks,
  saveDisciplineFile,
  saveDisciplineMapping,
  upgradeMachine,
  writeWorkspaceFile,
} from './client'

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

function mockFetchJSON(body: unknown) {
	const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
		new Response(JSON.stringify(body), {
			status: 200,
			headers: { 'Content-Type': 'application/json' },
		}),
	))
	vi.stubGlobal('fetch', fetchMock)
	return fetchMock
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

describe('upgradeMachine', () => {
  it('按机器名编码路径，并用 force=1 请求强制升级', async () => {
    const fetchMock = mockFetchJSON({ accepted: true, verdict: 'needs_upgrade', forcible: false })
    await upgradeMachine('mac 02', true)
    expect(fetchMock).toHaveBeenCalledWith('/api/machines/mac%2002/upgrade?force=1', expect.objectContaining({ method: 'POST' }))
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

it('addMachine 以 JSON 体 POST 到 /api/machines', async () => {
  const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ machines: [] }), { status: 200 }),
  )
  await addMachine({ name: 'box', addr: '10.0.0.1:7777', token: 't', user: 'me' })
  const [path, init] = spy.mock.calls[0]
  expect(path).toBe('/api/machines')
  expect(init?.method).toBe('POST')
  expect(JSON.parse(String(init?.body))).toMatchObject({ name: 'box', addr: '10.0.0.1:7777' })
  spy.mockRestore()
})

it('deleteMachine 对机器名做 URL 编码', async () => {
  const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ machines: [] }), { status: 200 }),
  )
  await deleteMachine('my box')
  expect(spy.mock.calls[0][0]).toBe('/api/machines/my%20box')
  spy.mockRestore()
})

describe('建树接口', () => {
  it('fetchProjectBranches 按登记名寻址并带上 machine', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ branches: [], default: 'main', worktree_root: '/d/manual' }), { status: 200 }),
    )
    await fetchProjectBranches('my repo', 'mac-02')
    expect(spy.mock.calls[0][0]).toBe('/api/projects/my%20repo/branches?machine=mac-02')
    spy.mockRestore()
  })

  it('createWorktree 本机时不带 machine 参数', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ path: '/d/manual/feat-x' }), { status: 200 }),
    )
    await createWorktree('handoff', { mode: 'new_branch', branch: 'feat/x', base: 'main' })
    expect(spy.mock.calls[0][0]).toBe('/api/projects/handoff/worktrees')
    spy.mockRestore()
  })
})

describe('列项目接口', () => {
  it('fetchProjects GET /api/projects，返回位置数组', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([
        { project_id: 'a1b2c3d4e5f60718', name: 'handoff', path: '/home/dev/handoff', origin_url: '', created_at: '', status: '有效' },
        { project_id: 'p2', name: 'sq', path: '/d/sq', origin_url: '', created_at: '' },
      ]), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
    const locs = await fetchProjects()
    expect(spy.mock.calls[0][0]).toBe('/api/projects')
    expect(locs.map((loc) => loc.name)).toEqual(['handoff', 'sq'])
    spy.mockRestore()
  })
})

describe('纪律配置接口', () => {
	it('本机不带 machine 参数，远程机带', async () => {
		const fetchMock = mockFetchJSON({ dir: '/d', builtins: [], files: [], bindings: [] })
		await fetchDiscipline('')
		expect(fetchMock.mock.calls[0][0]).toBe('/api/discipline')
		await fetchDiscipline('mac-02')
		expect(fetchMock.mock.calls[1][0]).toBe('/api/discipline?machine=mac-02')
	})

	it('文件名与机器名都过 encodeURIComponent', async () => {
		const fetchMock = mockFetchJSON({ content: '', size: 0, sha256: '' })
		await fetchDisciplineFile('mac 02', 'my rules.md')
		expect(fetchMock.mock.calls[0][0]).toBe('/api/discipline/file?name=my%20rules.md&machine=mac%2002')
	})

	it('保存映射走 PUT 并带 bindings', async () => {
		const fetchMock = mockFetchJSON({ dir: '/d', builtins: [], files: [], bindings: [] })
		await saveDisciplineMapping('', [{ executor: 'codex', mode: 'off', default_tier: 'single-context' }])
		const init = fetchMock.mock.calls[0][1] as RequestInit
		expect(init.method).toBe('PUT')
		expect(JSON.parse(init.body as string)).toEqual({
			bindings: [{ executor: 'codex', mode: 'off', default_tier: 'single-context' }],
		})
	})

	it('保存文件带文件名、机器名与前置哈希', async () => {
		const fetchMock = mockFetchJSON({ sha256: 'new', size: 3 })
		await saveDisciplineFile('mac 02', 'my rules.md', { content: 'new', base_sha256: 'old' })
		expect(fetchMock.mock.calls[0][0]).toBe('/api/discipline/file?name=my%20rules.md&machine=mac%2002')
		const init = fetchMock.mock.calls[0][1] as RequestInit
		expect(init.method).toBe('PUT')
		expect(JSON.parse(init.body as string)).toEqual({ content: 'new', base_sha256: 'old' })
	})
})
