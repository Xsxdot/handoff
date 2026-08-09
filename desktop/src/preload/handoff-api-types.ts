/**
 * Handoff preload 类型：window.handoff 的窄 IPC 接口。
 *
 * 职责：
 *   - 定义 renderer 可见的 handoff API 类型（不含 endpoint/token）
 *   - 只暴露 catalog 方法、本机目录选择和订阅
 *
 * 边界：
 *   - 不接受任意 URL、远端机器或远端 path
 *   - 类型与 shared/handoff/contracts 共用 wire 类型
 */
import type {
  BootstrapResponse,
  ControlEventEnvelope,
  CreateProjectCommand,
  Operation
} from '../shared/handoff/contracts'

export type LocalAgentdConnectionStatus = 'connecting' | 'connected' | 'unavailable'

export type PickLocalDirectoryResult = {
  canceled: boolean
  path?: string
}

/** window.handoff 的窄 IPC surface。 */
export type HandoffRendererAPI = {
  /** bootstrap 获取控制面快照。 */
  bootstrap(): Promise<BootstrapResponse>
  /** 创建项目 Operation（同 ID 幂等）。 */
  createProject(command: CreateProjectCommand): Promise<Operation>
  /** 查询 Operation。 */
  getOperation(operationId: string): Promise<Operation>
  /** 本机目录选择（Finder；只返回结果，最终由 agentd InspectPath 校验）。 */
  pickLocalDirectory(): Promise<PickLocalDirectoryResult>
  /** 订阅控制流；返回取消函数。 */
  onControlEvent(callback: (event: ControlEventEnvelope) => void): () => void
  /** 订阅连接状态；返回取消函数。 */
  onConnectionStatus(callback: (status: LocalAgentdConnectionStatus) => void): () => void
  /** 发起控制流订阅（after=bootstrap 返回的 control_revision）。 */
  subscribeControl(after: number): Promise<void>
  /** 停止控制流订阅。 */
  unsubscribeControl(): Promise<void>
}
