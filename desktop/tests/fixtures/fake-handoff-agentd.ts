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
    /** 服务端实际执行的 createProject 次数（同 operation_id 幂等去重，重复请求不增）。 */
    createProject: () => number
    getOperation: () => number
  }
  /**
   * 收到的 createProject **HTTP 请求**总数（每个请求都自增，与幂等去重无关）。
   * 用途：区分「客户端发了几次请求」与「服务端执行了几次」——断言 UI 重试携带
   * 相同 operation_id、且服务端只执行一次时，两个计数分别对应这两件事。
   */
  createProjectRequests: () => number
  /** 使接下来 n 次 createProject 请求返回 500（模拟失败，供重试用例驱动）。 */
  failCreateProjectNext: (n: number) => void
  /**
   * 推送一条控制事件：入 durable buffer 并向全部在线订阅者实时发送。
   * 断线期间 push 的事件会留在 buffer，重连后按客户端 after cursor 重放——
   * 与真实 control_events 的补发语义一致（bootstrap/stream 无窗口竞态）。
   */
  push(event: ControlEventEnvelope): void
  /** 已建立的 WS 连接数（订阅就绪判定用）。 */
  wsCount: () => number
  /** 已推送的 WS 消息条数（供断言 push 确实发送）。 */
  wsMessagesSent: () => number
  /** 断开全部 WS 订阅并进入 down 状态（模拟断线；WS/HTTP 均拒绝）。 */
  disconnectAll(): void
  /** 恢复 up 状态，允许客户端重连（重连时按 after cursor 重放 buffer）。 */
  setDown(down: boolean): void
  /** 记录收到的 createProject 请求体（含被幂等去重的重复请求）。 */
  createdProjects: () => unknown[]
  close(): Promise<void>
}

/** 构造一个 fake agentd server。 */
export async function startFakeAgentd(options: FakeAgentdOptions): Promise<FakeAgentdHandle> {
  const token = options.token ?? 'test-token'
  let bootstrapCount = 0
  let createProjectCount = 0
  let createProjectRequestCount = 0
  let createProjectFailures = 0
  let getOperationCount = 0
  const createdProjects: unknown[] = []
  const createdOperationById = new Map<string, Operation>()
  const clients = new Set<WebSocket>()
  let nextRevision = options.bootstrap.control_revision
  let down = false
  let wsCount = 0
  let wsMessagesSent = 0
  // durable 事件 buffer：断线期间 push 的事件先入 buffer，重连后按 after cursor 重放。
  const eventBuffer: ControlEventEnvelope[] = []

  const server: Server = createServer((req, res) => {
    const authOk = req.headers.authorization === `Bearer ${token}`
    if (!authOk) {
      res.statusCode = 401
      res.end('{"code":"AUTH_FAILED","message":"未授权","retryable":false}')
      return
    }
    if (down) {
      res.statusCode = 503
      res.end('{"code":"LOCAL_AGENTD_UNAVAILABLE","message":"down","retryable":true}')
      return
    }
    const url = new URL(req.url ?? '/', 'http://localhost')
    switch (url.pathname) {
      case '/v1/bootstrap': {
        bootstrapCount++
        res.setHeader('content-type', 'application/json')
        res.end(JSON.stringify(options.bootstrap))
        return
      }
      case '/v1/control/events': {
        res.setHeader('content-type', 'application/json')
        res.end(JSON.stringify(eventBuffer))
        return
      }
      case '/v1/projects/operations': {
        let body = ''
        req.on('data', (c) => (body += c))
        req.on('end', () => {
          const parsed = JSON.parse(body)
          // 收到的请求总数：每个 HTTP 请求都自增，与幂等去重无关。
          createProjectRequestCount++
          createdProjects.push(parsed)
          // 模拟失败：返回 500，不落 operation（客户端重试时会用相同 operation_id
          // 再次提交；服务端对同一 id 只执行一次）。
          if (createProjectFailures > 0) {
            createProjectFailures--
            res.statusCode = 500
            res.setHeader('content-type', 'application/json')
            res.end('{"code":"LOCAL_AGENTD_UNAVAILABLE","message":"injected failure","retryable":true}')
            return
          }
          // 幂等：同 operation_id 返回已有权威 Operation，不重复执行（计数不增）。
          const existing = createdOperationById.get(parsed.operation_id)
          if (existing) {
            res.statusCode = 202
            res.setHeader('content-type', 'application/json')
            res.end(JSON.stringify(existing))
            return
          }
          createProjectCount++
          const op: Operation = {
            operation_id: parsed.operation_id,
            kind: 'create_project',
            state: 'pending',
            targets: [],
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString()
          }
          createdOperationById.set(op.operation_id, op)
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
    if (down) {
      // 断线语义：WS 升级也拒绝，客户端进入不可用并退避重连。
      socket.destroy()
      return
    }
    // 解析客户端 after cursor：重连时只重放比该 revision 新的事件。
    const after = Number(new URL(req.url ?? '/', 'http://localhost').searchParams.get('after') ?? 0)
    wss.handleUpgrade(req, socket, head, (ws) => {
      clients.add(ws)
      wsCount++
      ws.on('close', () => {
        clients.delete(ws)
      })
      ws.on('error', () => {
        clients.delete(ws)
      })
      // 连接建立后重放 durable buffer 中 after 之后的事件（无窗口竞态补发）。
      for (const ev of eventBuffer) {
        if (ev.revision > after) {
          ws.send(JSON.stringify(ev))
          wsMessagesSent++
        }
      }
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
    createProjectRequests: () => createProjectRequestCount,
    failCreateProjectNext: (n) => {
      createProjectFailures = n
    },
    wsCount: () => wsCount,
    wsMessagesSent: () => wsMessagesSent,
    push: (event) => {
      nextRevision = Math.max(nextRevision, event.revision)
      eventBuffer.push(event)
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
    setDown: (value) => {
      down = value
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
