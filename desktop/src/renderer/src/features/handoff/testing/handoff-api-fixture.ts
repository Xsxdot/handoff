/**
 * Handoff renderer 测试 fixture：注入 window.handoff 的实现。
 *
 * 职责：
 *   - 提供可控的 HandoffRendererAPI 内存实现（供 renderer 测试注入）
 *   - 不发起真实网络
 *
 * 边界：
 *   - 只用于 renderer 单测；E2E 走 fake-handoff-agentd（真实 wire）
 */
import { vi } from 'vitest'
import type { HandoffRendererAPI } from '../../../../../preload/handoff-api-types'
import type {
  BootstrapResponse,
  ControlEventEnvelope,
  CreateProjectCommand,
  Operation
} from '../../../../../shared/handoff/contracts'

const emptyBootstrap: BootstrapResponse = {
  machines: [],
  projects: [],
  locations: [],
  workspaces: [],
  git_refs: [],
  active_task_summaries: [],
  operations: [],
  control_revision: 0
}

/** 构造一个注入 window.handoff 的 fixture，返回可编程句柄。 */
export function installHandoffApiFixture(initial: BootstrapResponse = emptyBootstrap): {
  bootstrap: ReturnType<typeof vi.fn>
  createProject: ReturnType<typeof vi.fn>
  getOperation: ReturnType<typeof vi.fn>
  pickLocalDirectory: ReturnType<typeof vi.fn>
  subscribeControl: ReturnType<typeof vi.fn>
  emitControlEvent: (ev: ControlEventEnvelope) => void
  emitConnectionStatus: (status: 'connecting' | 'connected' | 'unavailable') => void
  windowHandoff: HandoffRendererAPI
} {
  const controlListeners = new Set<(ev: ControlEventEnvelope) => void>()
  const statusListeners = new Set<(status: 'connecting' | 'connected' | 'unavailable') => void>()

  const windowHandoff: HandoffRendererAPI = {
    bootstrap: vi.fn().mockResolvedValue(initial),
    createProject: vi.fn(async (command: CreateProjectCommand): Promise<Operation> => {
      return {
        operation_id: command.operation_id,
        kind: 'create_project',
        state: 'pending',
        targets: [],
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString()
      }
    }),
    getOperation: vi.fn().mockResolvedValue(null),
    pickLocalDirectory: vi.fn().mockResolvedValue({ canceled: true }),
    onControlEvent: (cb: (ev: ControlEventEnvelope) => void): (() => void) => {
      controlListeners.add(cb)
      return () => controlListeners.delete(cb)
    },
    onConnectionStatus: (
      cb: (status: 'connecting' | 'connected' | 'unavailable') => void
    ): (() => void) => {
      statusListeners.add(cb)
      return () => statusListeners.delete(cb)
    },
    subscribeControl: vi.fn().mockResolvedValue(undefined),
    unsubscribeControl: vi.fn().mockResolvedValue(undefined)
  }
  ;(window as unknown as { handoff?: HandoffRendererAPI }).handoff = windowHandoff

  return {
    bootstrap: windowHandoff.bootstrap as ReturnType<typeof vi.fn>,
    createProject: windowHandoff.createProject as ReturnType<typeof vi.fn>,
    getOperation: windowHandoff.getOperation as ReturnType<typeof vi.fn>,
    pickLocalDirectory: windowHandoff.pickLocalDirectory as ReturnType<typeof vi.fn>,
    subscribeControl: windowHandoff.subscribeControl as ReturnType<typeof vi.fn>,
    emitControlEvent: (ev: ControlEventEnvelope) => {
      for (const cb of controlListeners) {
        cb(ev)
      }
    },
    emitConnectionStatus: (status: 'connecting' | 'connected' | 'unavailable') => {
      for (const cb of statusListeners) {
        cb(status)
      }
    },
    windowHandoff
  }
}
