/**
 * Handoff IPC 注册测试：window.handoff 的窄边界。
 *
 * 职责：
 *   - pickLocalDirectory 固定调用 dialog.showOpenDialog({ properties: ['openDirectory'] })
 *   - 取消返回 { canceled: true }
 *   - 成功只返回规范化本机绝对路径
 *   - 该 IPC 不接收 machine ID、remote path 或任意 dialog options
 *
 * 边界：
 *   - 使用 vi.mock 模拟 electron dialog，不弹真实 Finder
 */
import { describe, expect, it, vi } from 'vitest'

const { showOpenDialog } = vi.hoisted(() => ({ showOpenDialog: vi.fn() }))

vi.mock('electron', () => ({
  dialog: { showOpenDialog },
  ipcMain: {
    handle: vi.fn((channel: string, handler: unknown) => {
      ;(globalThis as Record<string, unknown>)[`__handler_${channel}`] = handler
    })
  },
  app: { getPath: vi.fn(() => '/tmp/fake-userdata') },
  BrowserWindow: class {}
}))

import { registerHandoffIpc } from './register-handoff-ipc'
import { HandoffLogger } from './logger'

describe('registerHandoffIpc pickLocalDirectory', () => {
  it('invokes showOpenDialog with openDirectory only', async () => {
    showOpenDialog.mockResolvedValue({ canceled: false, filePaths: ['/Users/me/repo'] })
    const logger = new HandoffLogger({ dir: '/tmp/logs', redactFields: [] })
    const client = { bootstrap: vi.fn(), createProject: vi.fn(), getOperation: vi.fn(), close: vi.fn() }
    registerHandoffIpc({ client, logger } as never)

    const handler = (globalThis as Record<string, unknown>)[
      '__handler_handoff:pickLocalDirectory'
    ] as () => Promise<unknown>
    const result = (await handler()) as { canceled: boolean; path?: string }
    expect(showOpenDialog).toHaveBeenCalledWith({ properties: ['openDirectory'] })
    expect(result.canceled).toBe(false)
    expect(result.path).toBe('/Users/me/repo')
  })

  it('returns canceled when user cancels', async () => {
    showOpenDialog.mockResolvedValue({ canceled: true, filePaths: [] })
    const logger = new HandoffLogger({ dir: '/tmp/logs', redactFields: [] })
    const client = { bootstrap: vi.fn(), createProject: vi.fn(), getOperation: vi.fn(), close: vi.fn() }
    registerHandoffIpc({ client, logger } as never)

    const handler = (globalThis as Record<string, unknown>)[
      '__handler_handoff:pickLocalDirectory'
    ] as () => Promise<unknown>
    const result = (await handler()) as { canceled: boolean; path?: string }
    expect(result.canceled).toBe(true)
    expect(result.path).toBeUndefined()
  })
})
