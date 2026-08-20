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

  // 承重：升级没有进度流，成功靠版本变化收尾，**失败此前没有任何出口**——
  // 真机上后端三分钟前就放弃了，界面还在转。
  //
  // 这条必须从**点击**开始：那才是真实路径，也是本地「升级中」态产生的地方。
  // 只喂服务端状态的写法测不到清态逻辑（实测：把清态改成只认成功出口，那种写法
  // 依然全绿）。
  it('点了升级后服务端报失败：清掉「升级中」并给出原文', async () => {
    const refresh = vi.fn()
    const before = machine({ name: 'mac-02', version: 'v0.2.0' })
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [before] }, disconnected: false, sessionExpired: false, errorText: '', refresh,
    })
    const { rerender } = render(<UpdatePage desktopState={null} latest={latest} />)
    await userEvent.click(screen.getByRole('button', { name: '升级到 v0.3.1' }))
    expect(screen.getByRole('button', { name: '升级中…' })).toBeDisabled()

    const failed = machine({
      name: 'mac-02', version: 'v0.2.0',
      upgrade: {
        running: false, status: 'fail', verdict: 'needs_upgrade',
        reason: '下载 checksums.txt: 尝试 3 次仍失败: i/o timeout',
      },
    })
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [failed] }, disconnected: false, sessionExpired: false, errorText: '', refresh,
    })
    rerender(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.queryByRole('button', { name: '升级中…' })).toBeNull()
    expect(screen.getByText(/i\/o timeout/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '升级到 v0.3.1' })).toBeInTheDocument()
  })

  // 刷新页面后本地态就没了，失败原文只剩服务端那一份——它必须还在。
  it('没有本地态时也从服务端读出失败原文与在跑状态', () => {
    vi.mocked(useMachines).mockReturnValue({
      data: {
        machines: [machine({
          name: 'mac-02', version: 'v0.2.0',
          upgrade: { running: false, status: 'fail', reason: 'i/o timeout', remedy: '检查该机器到 github.com 的网络' },
        })],
      },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    const { rerender } = render(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.getByText('i/o timeout')).toBeInTheDocument()
    expect(screen.getByText('检查该机器到 github.com 的网络')).toBeInTheDocument()

    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'mac-02', version: 'v0.2.0', upgrade: { running: true } })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    rerender(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.getByRole('button', { name: '升级中…' })).toBeDisabled()
  })

  // 承重：开发构建的版本戳是提交号，比不出来。此前落到「已是最新」那一支，
  // 且因为 upgradeAvailable=false，按钮根本不出现——那台机器在界面上升不了。
  it('版本比不出来时说「版本无法比较」，且仍可升级', () => {
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'linux-01', version: '7dec31185aaa' })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    render(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.getByText('版本无法比较')).toBeInTheDocument()
    expect(screen.queryByText('已是最新')).toBeNull()
    expect(screen.getByRole('button', { name: '升级到 v0.3.1' })).toBeInTheDocument()
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
