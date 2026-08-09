/**
 * Handoff AgentdClient：Electron Main 对本地 agentd 的 HTTP/WS 客户端。
 *
 * 职责：
 *   - bootstrap / createProject / getOperation / subscribeControl
 *   - HTTP 401 → AUTH_FAILED；响应先 Zod parse 再返回
 *   - WS 重连携带最后 revision；unsubscribe 停止推送
 *
 * 边界：
 *   - token 只在 Main 持有，不通过 preload 暴露
 *   - 不记录 token 到日志
 */
import WebSocket from 'ws'
import {
  bootstrapResponseSchema,
  createProjectRequestSchema,
  controlEventEnvelopeSchema,
  operationSchema,
  type BootstrapResponse,
  type ControlEventEnvelope,
  type CreateProjectCommand,
  type Operation
} from '../../shared/handoff/contracts'
import { HandoffLogger } from './logger'

export type LocalAgentdConnectionStatus = 'connecting' | 'connected' | 'unavailable'

export class AgentdError extends Error {
  constructor(
    readonly code: string,
    message: string
  ) {
    super(message)
    this.name = 'AgentdError'
  }
}

const AUTH_FAILED = 'AUTH_FAILED'

/**
 * AgentdClient：本地 agentd 的 HTTP/WS 客户端。
 * @param endpoint agentd 地址（如 http://127.0.0.1:7777）
 * @param token 本机 agentd 访问 token（来自 config.yaml）
 * @param logger 结构化日志入口
 */
export class AgentdClient {
  private readonly endpoint: string
  private readonly token: string
  private readonly logger: HandoffLogger

  constructor(endpoint: string, token: string, logger?: HandoffLogger) {
    this.endpoint = endpoint.replace(/\/$/, '')
    this.token = token
    this.logger = logger ?? new HandoffLogger({ dir: '/tmp', redactFields: [] })
  }

  /** bootstrap 获取控制面快照。 */
  async bootstrap(signal?: AbortSignal): Promise<BootstrapResponse> {
    const raw = await this.request('/v1/bootstrap', signal)
    return bootstrapResponseSchema.parse(raw)
  }

  /** 创建项目 Operation（202；同 ID 幂等）。 */
  async createProject(command: CreateProjectCommand): Promise<Operation> {
    const payload = createProjectRequestSchema.parse(command)
    const raw = await this.request('/v1/projects/operations', undefined, 'POST', payload)
    return operationSchema.parse(raw)
  }

  /** 查询 Operation。 */
  async getOperation(operationId: string, signal?: AbortSignal): Promise<Operation> {
    const raw = await this.request(`/v1/operations/${encodeURIComponent(operationId)}`, signal)
    return operationSchema.parse(raw)
  }

  /**
   * 订阅控制流。
   * @param after 起始 revision（bootstrap 返回的 control_revision）
   * @param onEvent 每条控制事件回调
   * @param onStatus 连接状态回调
   * @returns 取消订阅函数
   */
  subscribeControl(
    after: number,
    onEvent: (event: ControlEventEnvelope) => void,
    onStatus: (status: LocalAgentdConnectionStatus) => void
  ): () => void {
    let closed = false
    let cursor = after
    let socket: WebSocket | null = null

    const connect = (): void => {
      if (closed) {
        return
      }
      onStatus('connecting')
      const url = new URL('/v1/control/stream', this.wsBase())
      url.searchParams.set('after', String(cursor))
      const ws = new WebSocket(url.toString(), {
        headers: { authorization: `Bearer ${this.token}` }
      })
      socket = ws
      ws.on('open', () => {
        if (closed) {
          return
        }
        this.logger.info('控制流已连接', { after: cursor })
        onStatus('connected')
      })
      ws.on('message', (raw) => {
        if (closed) {
          return
        }
        try {
          const parsed = controlEventEnvelopeSchema.parse(JSON.parse(String(raw)))
          cursor = Math.max(cursor, parsed.revision)
          onEvent(parsed)
        } catch {
          this.logger.warn('控制流消息解析失败')
        }
      })
      ws.on('close', () => {
        if (closed) {
          return
        }
        onStatus('unavailable')
        // 简单退避重连（生产由更完备的 supervisor 管理；此处保证不吞断线）
        setTimeout(connect, 1000)
      })
      ws.on('error', () => {
        ws.close()
      })
    }
    connect()

    return () => {
      closed = true
      socket?.close()
    }
  }

  private async request(
    path: string,
    signal?: AbortSignal,
    method = 'GET',
    body?: unknown
  ): Promise<unknown> {
    const init: RequestInit = {
      method,
      signal,
      headers: {
        Authorization: `Bearer ${this.token}`,
        'Content-Type': 'application/json'
      }
    }
    if (method !== 'GET' && body !== undefined) {
      init.body = JSON.stringify(body)
    }
    const resp = await fetch(this.endpoint + path, init)
    if (resp.status === 401 || resp.status === 403) {
      throw new AgentdError(AUTH_FAILED, '本地 agentd 认证失败')
    }
    if (!resp.ok) {
      throw new AgentdError(String(resp.status), `agentd 请求失败: ${resp.status}`)
    }
    return resp.json()
  }

  private wsBase(): string {
    return this.endpoint.replace(/^http/, 'ws')
  }
}
