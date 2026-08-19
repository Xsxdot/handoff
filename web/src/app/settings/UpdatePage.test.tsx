// 设置页「更新」分区的显示契约测试。
//
// 职责：钉住无薄壳时的浏览器降级，以及本机无按钮、远端升级动作的边界。
// 边界：下载、机器轮询与升级请求在测试中注入，不驱动真实 agentd。
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DownloadState, LatestResp, Machine } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useDownload } from '../data/useUpdate'
import { UpdatePage } from './UpdatePage'

vi.mock('../data/useMachines', () => ({ useMachines: vi.fn() }))
vi.mock('../data/useUpdate', () => ({ useDownload: vi.fn() }))
vi.mock('../../api/client', () => ({
  fetchLatest: vi.fn(),
  startDownload: vi.fn(),
  upgradeMachine: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number
    body: unknown
    constructor(status: number, _message: string, body: unknown) {
      super('api error')
      this.status = status
      this.body = body
    }
  },
}))

import { ApiError, upgradeMachine } from '../../api/client'

const latest: LatestResp = { tag: 'v0.3.1', checked_at: '2026-08-19T12:00:00Z' }
const download: DownloadState = { stage: 'idle', percent: -1, opened: false }

const machine = (patch: Partial<Machine>): Machine => ({
  name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v0.3.1',
  executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '',
  ...patch,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(upgradeMachine).mockResolvedValue({ accepted: true, verdict: 'needs_upgrade', forcible: false })
  vi.mocked(useDownload).mockReturnValue({ data: download, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() })
  vi.mocked(useMachines).mockReturnValue({
    data: { machines: [machine({})] }, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
  })
})

describe('UpdatePage', () => {
  it('desktopState 为 null 时只渲染执行机块', () => {
    render(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.queryByRole('heading', { name: '桌面应用' })).toBeNull()
    expect(screen.queryByRole('heading', { name: '同步状态' })).toBeNull()
    expect(screen.getByRole('heading', { name: '执行机' })).toBeInTheDocument()
  })

  it('本机行显示「随桌面应用一起更新」且没有按钮', () => {
    render(<UpdatePage desktopState={{ app_version: 'v0.3.1', sync_plan: 'done', sync_busy: 0 }} latest={latest} />)
    expect(screen.getByText('随桌面应用一起更新')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /升级到/ })).toBeNull()
  })

  it('远端可升级的机器显示升级按钮并在点击后禁用', async () => {
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'mac-02', addr: '10.0.0.2:7777', version: 'v0.2.0' })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    render(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.getByText('可升级')).toBeInTheDocument()
    const button = screen.getByRole('button', { name: '升级到 v0.3.1' })
    await userEvent.click(button)
    expect(screen.getByRole('button', { name: '升级中…' })).toBeDisabled()
    expect(upgradeMachine).toHaveBeenCalledWith('mac-02', false)
  })

  it('有活跃任务时给「仍要升级」', async () => {
    vi.mocked(upgradeMachine).mockRejectedValueOnce(new ApiError(409, 'busy', {
      accepted: false, verdict: 'needs_upgrade', reason: '2 个活跃任务', forcible: true, busy: 2,
    }))
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'mac-02', version: 'v0.2.0' })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    const user = userEvent.setup()
    render(<UpdatePage desktopState={null} latest={latest} />)
    await user.click(screen.getByRole('button', { name: '升级到 v0.3.1' }))
    expect(screen.getByText('2 个活跃任务')).toBeInTheDocument()
    const force = screen.getByRole('button', { name: '仍要升级' })
    await user.click(force)
    expect(upgradeMachine).toHaveBeenLastCalledWith('mac-02', true)
    expect(screen.getByRole('button', { name: '升级中…' })).toBeDisabled()
  })

  it('非托管时只显示处置建议，不给强制入口', async () => {
    vi.mocked(upgradeMachine).mockRejectedValueOnce(new ApiError(422, 'unmanaged', {
      accepted: false, verdict: 'unmanaged', reason: 'agentd 非托管启动，重启后不会被拉起',
      remedy: '先在该机器上 handoff service install', forcible: false,
    }))
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'mac-02', version: 'v0.2.0' })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    const user = userEvent.setup()
    render(<UpdatePage desktopState={null} latest={latest} />)
    await user.click(screen.getByRole('button', { name: '升级到 v0.3.1' }))
    expect(screen.getByText('先在该机器上 handoff service install')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '仍要升级' })).toBeNull()
  })
})
