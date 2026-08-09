/**
 * Handoff IPC 注册：把 AgentdClient 能力以窄 IPC 暴露给 renderer。
 *
 * 职责：
 *   - window.handoff 只暴露 catalog 方法、本机目录选择和订阅
 *   - 按 sender scope 管理订阅；窗口 destroyed 时清理
 *   - 不返回 endpoint/token，不接受任意 URL、远端机器或远端 path
 *
 * 边界：
 *   - renderer 不接触 token/endpoint（由 Main 持有）
 *   - 日志记录 request/operation/revision/status，不记录完整 Git URL 或 path
 */
import { app, dialog, ipcMain, BrowserWindow } from 'electron'
import { AgentdClient } from './agentd-client'
import { resolveAgentdConfigPath, readAgentdConfig } from './agentd-config'
import { defaultRedactFields, HandoffLogger, newSessionToken } from './logger'
import type { HandoffLogger as HandoffLoggerType } from './logger'

export type RegisterHandoffIpcOptions = {
  client: AgentdClient
  logger: HandoffLoggerType
}

/**
 * 从本机配置构造并注册 handoff IPC。
 *
 * 为什么单独封装：register-core-handlers 只负责接线，不关心 agentd 配置读取
 * 与 client 构造细节；本函数把「读 config → 建 client → 注册 IPC」收敛一处。
 * @param configPath 配置文件路径（默认 ~/.handoff/config.yaml）
 * @returns 清理函数
 */
export function registerHandoffIpcFromConfig(configPath?: string): () => void {
  const path = configPath ?? resolveAgentdConfigPath()
  const cfg = readAgentdConfig(path)
  const logger = new HandoffLogger({
    dir: `${app.getPath('userData')}/logs`,
    redactFields: defaultRedactFields
  })
  const sessionId = newSessionToken()
  logger.info('handoff IPC 初始化', { config_path: path, unavailable: cfg.unavailable, session_id: sessionId })
  const client = new AgentdClient(
    cfg.unavailable ? 'http://127.0.0.1:7777' : `http://${cfg.listen}`,
    cfg.token,
    logger
  )
  return registerHandoffIpc({ client, logger })
}

/**
 * 注册 handoff IPC 通道。
 * @param options 客户端与日志
 * @returns 清理函数（应用退出时调用）
 */
export function registerHandoffIpc(options: RegisterHandoffIpcOptions): () => void {
  const { client, logger } = options

  const ipcHandlers: [string, (...args: unknown[]) => unknown][] = [
    [
      'handoff:bootstrap',
      async () => {
        logger.info('handoff IPC bootstrap 请求')
        return client.bootstrap()
      }
    ],
    [
      'handoff:createProject',
      async (command: unknown) => {
        logger.info('handoff IPC createProject 请求', {
          operation_id: (command as { operation_id?: string })?.operation_id
        })
        return client.createProject(command as never)
      }
    ],
    [
      'handoff:getOperation',
      async (operationId: unknown) => {
        logger.info('handoff IPC getOperation 请求', { operation_id: String(operationId) })
        return client.getOperation(String(operationId))
      }
    ],
    [
      'handoff:pickLocalDirectory',
      async () => {
        logger.info('handoff IPC 本机目录选择请求')
        // 固定调用 showOpenDialog({ properties: ['openDirectory'] })：
        // 该 IPC 不接受 machine ID、remote path 或任意 dialog options。
        const result = await dialog.showOpenDialog({ properties: ['openDirectory'] })
        if (result.canceled || result.filePaths.length === 0) {
          return { canceled: true }
        }
        // 只返回规范化本机绝对路径（最终仍由 agentd InspectPath 校验）。
        return { canceled: false, path: result.filePaths[0] }
      }
    ]
  ]

  for (const [channel, handler] of ipcHandlers) {
    ipcMain.handle(channel, (_event, ...args) => handler(...args))
  }

  // 控制流事件与连接状态广播：订阅按窗口管理，destroyed 时清理。
  const unsubscribers = new Map<number, () => void>()

  const onControlEvent = (event: unknown): void => {
    for (const [winId] of unsubscribers) {
      const win = BrowserWindow.fromId(winId)
      if (win && !win.isDestroyed()) {
        win.webContents.send('handoff:controlEvent', event)
      }
    }
  }
  const onConnectionStatus = (status: unknown): void => {
    for (const [winId] of unsubscribers) {
      const win = BrowserWindow.fromId(winId)
      if (win && !win.isDestroyed()) {
        win.webContents.send('handoff:connectionStatus', status)
      }
    }
  }

  const subscribeHandler = (_event: Electron.IpcMainInvokeEvent, after: unknown): void => {
    const win = BrowserWindow.fromWebContents(_event.sender)
    if (!win) {
      return
    }
    const existing = unsubscribers.get(win.id)
    if (existing) {
      existing()
    }
    const unsubscribe = client.subscribeControl(
      Number(after),
      (ev) => onControlEvent(ev),
      (st) => onConnectionStatus(st)
    )
    unsubscribers.set(win.id, unsubscribe)
    const onDestroyed = (): void => {
      const unsub = unsubscribers.get(win.id)
      if (unsub) {
        unsub()
        unsubscribers.delete(win.id)
      }
    }
    win.webContents.once('destroyed', onDestroyed)
    logger.info('handoff IPC 控制流订阅', { window_id: win.id, after: Number(after) })
  }

  ipcMain.handle('handoff:subscribeControl', subscribeHandler)
  ipcMain.handle('handoff:unsubscribeControl', (_event) => {
    const win = BrowserWindow.fromWebContents(_event.sender)
    if (win) {
      const unsub = unsubscribers.get(win.id)
      if (unsub) {
        unsub()
      }
      unsubscribers.delete(win.id)
    }
  })

  return () => {
    for (const [, unsub] of unsubscribers) {
      unsub()
    }
    unsubscribers.clear()
  }
}
