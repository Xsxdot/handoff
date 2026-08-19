// 设置页「更新」分区的显示契约测试。
//
// 职责：钉住无薄壳时的浏览器降级，以及本机/远端执行机的只读边界。
// 边界：下载与轮询在测试中注入，不驱动真实 agentd。
import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DownloadState, LatestResp, Machine } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useDownload } from '../data/useUpdate'
import { UpdatePage } from './UpdatePage'

vi.mock('../data/useMachines', () => ({ useMachines: vi.fn() }))
vi.mock('../data/useUpdate', () => ({ useDownload: vi.fn() }))
vi.mock('../../api/client', () => ({ fetchLatest: vi.fn(), startDownload: vi.fn() }))

const latest: LatestResp = { tag: 'v0.3.1', checked_at: '2026-08-19T12:00:00Z' }
const download: DownloadState = { stage: 'idle', percent: -1, opened: false }

const machine = (patch: Partial<Machine>): Machine => ({
  name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v0.3.1',
  executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '',
  ...patch,
})

beforeEach(() => {
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

  it('远端机器行显示「可升级」但本期不渲染升级按钮', () => {
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [machine({ name: 'mac-02', addr: '10.0.0.2:7777', version: 'v0.2.0' })] },
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    render(<UpdatePage desktopState={null} latest={latest} />)
    expect(screen.getByText('可升级')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '升级到 v0.3.1' })).toBeNull()
  })
})
