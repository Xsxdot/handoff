/**
 * Handoff preload：把窄 handoff IPC 桥接到 window.handoff。
 *
 * 职责：
 *   - 实现 HandoffRendererAPI（handoff-api-types）
 *   - 事件订阅用 ipcRenderer.on 转发，返回移除函数
 *
 * 边界：
 *   - 不暴露 endpoint/token
 *   - 只暴露白名单方法（bootstrap/createProject/getOperation/pickLocalDirectory/订阅）
 */
import { ipcRenderer, contextBridge } from 'electron'
import type {
  HandoffRendererAPI,
  LocalAgentdConnectionStatus
} from './handoff-api-types'
import type {
  BootstrapResponse,
  ControlEventEnvelope,
  CreateProjectCommand,
  Operation
} from '../shared/handoff/contracts'

const handoffApi: HandoffRendererAPI = {
  bootstrap: (): Promise<BootstrapResponse> => ipcRenderer.invoke('handoff:bootstrap'),
  createProject: (command: CreateProjectCommand): Promise<Operation> =>
    ipcRenderer.invoke('handoff:createProject', command),
  getOperation: (operationId: string): Promise<Operation> =>
    ipcRenderer.invoke('handoff:getOperation', operationId),
  pickLocalDirectory: (): Promise<{ canceled: boolean; path?: string }> =>
    ipcRenderer.invoke('handoff:pickLocalDirectory'),
  onControlEvent: (callback: (event: ControlEventEnvelope) => void): (() => void) => {
    const listener = (_event: Electron.IpcRendererEvent, data: ControlEventEnvelope): void =>
      callback(data)
    ipcRenderer.on('handoff:controlEvent', listener)
    return () => ipcRenderer.removeListener('handoff:controlEvent', listener)
  },
  onConnectionStatus: (callback: (status: LocalAgentdConnectionStatus) => void): (() => void) => {
    const listener = (_event: Electron.IpcRendererEvent, data: LocalAgentdConnectionStatus): void =>
      callback(data)
    ipcRenderer.on('handoff:connectionStatus', listener)
    return () => ipcRenderer.removeListener('handoff:connectionStatus', listener)
  },
  subscribeControl: (after: number): Promise<void> =>
    ipcRenderer.invoke('handoff:subscribeControl', after),
  unsubscribeControl: (): Promise<void> => ipcRenderer.invoke('handoff:unsubscribeControl')
}

/** 暴露 window.handoff（context-isolated 时经 contextBridge）。 */
export function exposeHandoffApi(): void {
  if (process.contextIsolated) {
    try {
      contextBridge.exposeInMainWorld('handoff', handoffApi)
    } catch {
      // 已存在或 bridge 不可用时忽略（不阻塞应用）
    }
  } else {
    // @ts-ignore define in env.d.ts
    window.handoff = handoffApi
  }
}
