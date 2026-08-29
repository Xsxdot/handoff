// scheduling.fetch.test.ts —— 调度 wire 导出面的 HTTP 序列化接缝。
// 职责：从公开 fetch 函数验证 URL、方法、请求体和可选字段边界；不测试
// JSON.stringify 内部实现，不模拟组件状态。
// 边界：响应体由 client 解析，本文件只验证 scheduling.ts 沿既有 client 面发出的请求。
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  attachCoordinator,
  getCoordinatorStatus,
  getQueue,
  getSquads,
  launchCoordinator,
  putCarrier,
  putSquad,
  releaseCoordinator,
} from './scheduling'

afterEach(() => vi.unstubAllGlobals())

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

describe('scheduling fetch seam', () => {
  it('reads squads and queue without changing empty arrays', async () => {
    const squadsFetchMock = mockFetchJSON({ carriers: [], squads: [] })
    await expect(getSquads()).resolves.toEqual({ carriers: [], squads: [] })
    expect(squadsFetchMock).toHaveBeenLastCalledWith('/api/squads', { credentials: 'same-origin' })
    expect((squadsFetchMock.mock.calls[0][1] as RequestInit).method ?? 'GET').toBe('GET')

    const queueFetchMock = mockFetchJSON({ queue: [] })
    await expect(getQueue()).resolves.toEqual({ queue: [] })
    expect(queueFetchMock).toHaveBeenLastCalledWith('/api/queue', { credentials: 'same-origin' })
    expect((queueFetchMock.mock.calls[0][1] as RequestInit).method ?? 'GET').toBe('GET')
  })

  it('serializes CAS writes and preserves zero/omitted optional fields', async () => {
    const fetchMock = mockFetchJSON({ name: 'carrier-a', version: 4 })
    await putCarrier('carrier a/特殊', 3, {
      name: 'carrier a', machine: 'local', cli: 'opencode', home_dir: '/h',
      credential: 'standalone', max_concurrency: 0,
    })
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/squads/carriers/carrier%20a%2F%E7%89%B9%E6%AE%8A?expect=3')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(String(init.body))).toEqual({
      name: 'carrier a', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone',
    })

    const squadFetchMock = mockFetchJSON({ name: 'squad-a', version: 1 })
    await putSquad('squad a/特殊', 0, {
      role: 'executor',
      members: [{ carrier: 'c1', max_concurrency: 2 }, { carrier: 'c2', max_concurrency: 0 }],
    })
    const [squadURL, squadInit] = squadFetchMock.mock.calls[0] as [string, RequestInit]
    expect(squadURL).toBe('/api/squads/squads/squad%20a%2F%E7%89%B9%E6%AE%8A?expect=0')
    expect(squadInit.method).toBe('PUT')
    expect(JSON.parse(String(squadInit.body))).toEqual({
      role: 'executor', members: [{ carrier: 'c1', max_concurrency: 2 }, { carrier: 'c2' }],
    })
  })

  it('keeps manual launch body empty but serializes card-create source', async () => {
    const fetchMock = mockFetchJSON({ woke: true, rebuilt: false, escalated: false })
    await launchCoordinator('B/1 特殊')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/cards/B%2F1%20%E7%89%B9%E6%AE%8A/coordinator/launch')
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe('POST')
    expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBe('{}')

    const cardCreateFetchMock = mockFetchJSON({ woke: true, rebuilt: false, escalated: false })
    await launchCoordinator('B/1 特殊', 'card_create')
    expect(cardCreateFetchMock.mock.calls[0][0]).toBe('/api/cards/B%2F1%20%E7%89%B9%E6%AE%8A/coordinator/launch')
    expect((cardCreateFetchMock.mock.calls[0][1] as RequestInit).method).toBe('POST')
    expect(JSON.parse(String((cardCreateFetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({ source: 'card_create' })
  })

  it('preserves attach null, false, zero-like and service-generated command', async () => {
    const status = {
      bound: true,
      attach_active: false,
      attach: { machine: '', dir: '/repo/handoff', command: 'opencode --session sess-coord' },
    }
    const statusFetchMock = mockFetchJSON(status)
    await expect(getCoordinatorStatus('B/1 特殊')).resolves.toEqual(status)
    expect(statusFetchMock).toHaveBeenLastCalledWith('/api/cards/B%2F1%20%E7%89%B9%E6%AE%8A/coordinator', { credentials: 'same-origin' })
    expect((statusFetchMock.mock.calls[0][1] as RequestInit).method ?? 'GET').toBe('GET')
    expect(JSON.parse(JSON.stringify({ bound: false, attach_active: false, attach: null }))).toHaveProperty('attach', null)

    const attachFetchMock = mockFetchJSON(status.attach)
    await expect(attachCoordinator('B/1 特殊', '/repo/handoff')).resolves.toEqual(status.attach)
    expect(attachFetchMock.mock.calls[0][0]).toBe('/api/cards/B%2F1%20%E7%89%B9%E6%AE%8A/attach')
    expect((attachFetchMock.mock.calls[0][1] as RequestInit).method).toBe('POST')
    expect(JSON.parse(String((attachFetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({ active: true, workdir: '/repo/handoff' })

    const releaseFetchMock = mockFetchJSON({ ok: true })
    await releaseCoordinator('B/1 特殊')
    expect(releaseFetchMock.mock.calls[0][0]).toBe('/api/cards/B%2F1%20%E7%89%B9%E6%AE%8A/attach')
    expect((releaseFetchMock.mock.calls[0][1] as RequestInit).method).toBe('POST')
    expect(JSON.parse(String((releaseFetchMock.mock.calls[0][1] as RequestInit).body))).toEqual({ active: false })
  })
})
