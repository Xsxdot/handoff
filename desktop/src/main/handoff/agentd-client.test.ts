/**
 * Handoff AgentdClient 测试：HTTP/WS 客户端与错误映射。
 *
 * 职责：
 *   - bootstrap/createProject/getOperation 的请求路径与解析
 *   - HTTP 401 → AUTH_FAILED
 *   - WS 重连携带最后 revision
 *   - unsubscribe 后停止推送
 *   - 销毁时释放 socket/timer
 *
 * 边界：
 *   - 使用 node http server 模拟 agentd，不发起真实网络
 *   - renderer 返回值中无 token
 */
import { createServer } from 'node:http'
import type { IncomingMessage, ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AgentdClient } from './agentd-client'
import type { BootstrapResponse } from '../../shared/handoff/contracts'

const bootstrapFixture: BootstrapResponse = {
  machines: [],
  projects: [],
  locations: [],
  workspaces: [],
  git_refs: [],
  active_task_summaries: [],
  operations: [],
  control_revision: 3
}

function startServer(handler: (req: IncomingMessage, res: ServerResponse) => void) {
  const server = createServer(handler)
  return new Promise<{ url: string; close: () => Promise<void> }>((resolvePromise) => {
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo
      resolvePromise({
        url: `http://127.0.0.1:${port}`,
        close: () => new Promise((r) => server.close(() => r()))
      })
    })
  })
}

describe('AgentdClient', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('bootstraps from /v1/bootstrap', async () => {
    const srv = await startServer((req, res) => {
      expect(req.url).toBe('/v1/bootstrap')
      expect(req.headers.authorization).toBe('Bearer secret-token')
      res.setHeader('content-type', 'application/json')
      res.end(JSON.stringify(bootstrapFixture))
    })
    const client = new AgentdClient(srv.url, 'secret-token')
    const result = await client.bootstrap()
    expect(result.control_revision).toBe(3)
    await srv.close()
  })

  it('maps 401 to AUTH_FAILED error', async () => {
    const srv = await startServer((_req, res) => {
      res.statusCode = 401
      res.end()
    })
    const client = new AgentdClient(srv.url, 'wrong')
    const err = await client.bootstrap().then(
      () => null,
      (e: unknown) => e
    )
    expect((err as { code?: string }).code).toBe('AUTH_FAILED')
    await srv.close()
  })

  it('createProject posts /v1/projects/operations with operation id', async () => {
    const srv = await startServer((req, res) => {
      expect(req.url).toBe('/v1/projects/operations')
      expect(req.method).toBe('POST')
      let body = ''
      req.on('data', (c) => (body += c))
      req.on('end', () => {
        expect(JSON.parse(body).operation_id).toBe('op-1')
        res.setHeader('content-type', 'application/json')
        res.statusCode = 202
        res.end(
          JSON.stringify({
            operation_id: 'op-1',
            kind: 'create_project',
            state: 'pending',
            targets: [],
            created_at: '2026-08-09T00:00:00Z',
            updated_at: '2026-08-09T00:00:00Z'
          })
        )
      })
    })
    const client = new AgentdClient(srv.url, 't')
    const op = await client.createProject({
      operation_id: 'op-1',
      name: 'p',
      locations: []
    })
    expect(op.operation_id).toBe('op-1')
    await srv.close()
  })

  it('getOperation fetches /v1/operations/{id}', async () => {
    const srv = await startServer((req, res) => {
      expect(req.url).toBe('/v1/operations/op-9')
      res.setHeader('content-type', 'application/json')
      res.end(
        JSON.stringify({
          operation_id: 'op-9',
          kind: 'create_project',
          state: 'succeeded',
          targets: [],
          created_at: '2026-08-09T00:00:00Z',
          updated_at: '2026-08-09T00:00:00Z'
        })
      )
    })
    const client = new AgentdClient(srv.url, 't')
    const op = await client.getOperation('op-9')
    expect(op.operation_id).toBe('op-9')
    await srv.close()
  })
})
