/**
 * Fake Handoff agentd：可编程的 agentd HTTP/WS wire fixture（E2E 用）。
 *
 * 职责：
 *   - 实现真实 HTTP/WS wire（/v1/bootstrap、/v1/control/stream、
 *     /v1/projects/operations、/v1/operations/{id}）
 *   - 支持脚本化 revision、gap、disconnect、operation partial
 *   - 暴露调用计数供断言
 *
 * 边界：
 *   - 不直接调用 renderer store（E2E 必须走真实 wire）
 *   - 只用于测试；不承载 agentd 真实逻辑
 */
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { WebSocketServer, type WebSocket } from 'ws'
import type {
  BootstrapResponse,
  ControlEventEnvelope,
  Operation
} from '../../src/shared/handoff/contracts'

export type FakeAgentdOptions = {
  bootstrap: BootstrapResponse
  token?: string
}

export type FakeAgentdHandle = {
  url: string
  wsUrl: string
  /** 调用计数 */
  calls: {
    bootstrap: () => number
    createProject: () => number
    getOperation: () => number
  }
  /** 推送一条控制事件到全部订阅者。 */
  push(event: ControlEventEnvelope): void
  /** 已建立的 WS 连接数（订阅就绪判定用）。 */
  wsCount: () => number
  /** 已推送的 WS 消息条数（供断言 push 确实发送）。 */
  wsMessagesSent: () => number
  /** 断开全部 WS 订阅（模拟断线）。 */
  disconnectAll(): void
  /** 记录收到的 createProject 请求体。 */
  createdProjects: () => unknown[]
  close(): Promise<void>
}

/** 构造一个 fake agentd server。 */
export async function startFakeAgentd(options: FakeAgentdOptions): Promise<FakeAgentdHandle> {
  const token = options.token ?? 'test-token'
  let bootstrapCount = 0
  let createProjectCount = 0
  let getOperationCount = 0
  const createdProjects: unknown[] = []
  const clients = new Set<WebSocket>()
  let nextRevision = options.bootstrap.control_revision
  let down = false
  let wsCount = 0
  let wsMessagesSent = 0

  const server: Server = createServer((req, res) => {
    const authOk = req.headers.authorization === `Bearer ${token}`
    if (!authOk) {
      res.statusCode = 401
      res.end('{"code":"AUTH_FAILED","message":"未授权","retryable":false}')
      return
    }
    const url = new URL(req.url ?? '/', 'http://localhost')
    switch (url.pathname) {
      case '/v1/bootstrap': {
        if (down) {
          res.statusCode = 503
          res.end('{"code":"LOCAL_AGENTD_UNAVAILABLE","message":"down","retryable":true}')
          return
        }
        bootstrapCount++
        res.setHeader('content-type', 'application/json')
        res.end(JSON.stringify(options.bootstrap))
        return
      }
      case '/v1/control/events': {
        res.setHeader('content-type', 'application/json')
        res.end('[]')
        return
      }
      case '/v1/projects/operations': {
        createProjectCount++
        let body = ''
        req.on('data', (c) => (body += c))
        req.on('end', () => {
          const parsed = JSON.parse(body)
          createdProjects.push(parsed)
          const op: Operation = {
            operation_id: parsed.operation_id,
            kind: 'create_project',
            state: 'pending',
            targets: [],
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString()
          }
          res.statusCode = 202
          res.setHeader('content-type', 'application/json')
          res.end(JSON.stringify(op))
        })
        return
      }
      default: {
        // /v1/operations/{id}
        const m = /^\/v1\/operations\/(.+)$/.exec(url.pathname)
        if (m) {
          getOperationCount++
          res.setHeader('content-type', 'application/json')
          res.end(
            JSON.stringify({
              operation_id: m[1],
              kind: 'create_project',
              state: 'succeeded',
              targets: [],
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString()
            })
          )
          return
        }
        res.statusCode = 404
        res.end('{"code":"RESOURCE_NOT_FOUND","message":"not found","retryable":false}')
      }
    }
  })

  const wss = new WebSocketServer({ noServer: true })
  server.on('upgrade', (req, socket, head) => {
    const authOk = req.headers.authorization === `Bearer ${token}`
    if (!authOk) {
      socket.destroy()
      return
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      clients.add(ws)
      wsCount++
      ws.on('close', () => {
        clients.delete(ws)
      })
      ws.on('error', () => {
        clients.delete(ws)
      })
    })
  })

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const port = (server.address() as AddressInfo).port
  const url = `http://127.0.0.1:${port}`
  const wsUrl = `ws://127.0.0.1:${port}`

  return {
    url,
    wsUrl,
    calls: {
      bootstrap: () => bootstrapCount,
      createProject: () => createProjectCount,
      getOperation: () => getOperationCount
    },
    wsCount: () => wsCount,
    wsMessagesSent: () => wsMessagesSent,
    push: (event) => {
      nextRevision = Math.max(nextRevision, event.revision)
      const payload = JSON.stringify(event)
      for (const ws of clients) {
        if (ws.readyState === ws.OPEN) {
          ws.send(payload)
          wsMessagesSent++
        }
      }
    },
    disconnectAll: () => {
      down = true
      for (const ws of clients) {
        ws.close()
      }
      clients.clear()
    },
    createdProjects: () => createdProjects,
    close: async () => {
      for (const ws of clients) {
        ws.terminate()
      }
      wss.close()
      await new Promise<void>((resolve) => server.close(() => resolve()))
    }
  }
}
