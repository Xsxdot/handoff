// 更新提示框的显示契约测试。
//
// 职责：钉住薄壳边界、三种提示条件与 sessionStorage 关闭语义。
// 边界：数据轮询本身由 useUpdate/usePoll 测试负责，这里只注入已解析的数据。
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { startDownload } from '../../api/client'
import type { DesktopState, DownloadState, LatestResp } from '../../api/types'
import { isDesktopShell } from '../lib/desktopShell'
import { useDesktopState, useDownload, useLatest } from '../data/useUpdate'
import { UpdateToasts } from './UpdateToasts'

function renderToasts(homeOpen = false) {
  return render(<MemoryRouter><UpdateToasts homeOpen={homeOpen} /></MemoryRouter>)
}

vi.mock('../../api/client', () => ({ startDownload: vi.fn() }))
vi.mock('../lib/desktopShell', () => ({ isDesktopShell: vi.fn() }))
vi.mock('../data/useUpdate', () => ({
  useDesktopState: vi.fn(),
  useDownload: vi.fn(),
  useLatest: vi.fn(),
}))

const desktopState = (patch: Partial<DesktopState> = {}): DesktopState => ({
  app_version: 'v0.3.0',
  sync_plan: 'done',
  sync_busy: 0,
  ...patch,
})

const latest = (patch: Partial<LatestResp> = {}): LatestResp => ({ tag: 'v0.3.1', ...patch })

const download = (patch: Partial<DownloadState> = {}): DownloadState => ({
  stage: 'idle',
  percent: -1,
  opened: false,
  ...patch,
})

beforeEach(() => {
  sessionStorage.clear()
  vi.mocked(isDesktopShell).mockReturnValue(true)
  vi.mocked(useDesktopState).mockReturnValue({ data: desktopState(), disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() })
  vi.mocked(useLatest).mockReturnValue({ data: latest(), disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() })
  vi.mocked(useDownload).mockReturnValue({ data: download(), disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn() })
  vi.mocked(startDownload).mockResolvedValue(undefined)
})

describe('UpdateToasts', () => {
  it('非桌面壳时不渲染任何提示', () => {
    vi.mocked(isDesktopShell).mockReturnValue(false)
    renderToasts()
    expect(screen.queryByRole('region', { name: '更新提示' })).toBeNull()
  })

  it('app_version 落后于 latest 时弹「有新版」', () => {
    renderToasts()
    expect(screen.getByText('有新版 v0.3.1 可下载')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '下载' })).toBeInTheDocument()
  })

  it('sync_plan=blocked 时弹「有更新待应用」，且主按钮是「知道了」', () => {
    vi.mocked(useDesktopState).mockReturnValue({
      data: desktopState({ app_version: 'v0.3.1', sync_plan: 'blocked', sync_busy: 2 }),
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    renderToasts()
    expect(screen.getByText('有更新待应用')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '知道了' })).toBeInTheDocument()
  })

  it('sync_plan=failed 时弹「上次同步失败」', () => {
    vi.mocked(useDesktopState).mockReturnValue({
      data: desktopState({ app_version: 'v0.3.1', sync_plan: 'failed', sync_error: '签名不匹配' }),
      disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
    })
    renderToasts()
    expect(screen.getByText('上次同步失败')).toBeInTheDocument()
    expect(screen.getByText('签名不匹配')).toBeInTheDocument()
  })

  it('关闭后同一 (kind, tag) 不再出现', () => {
    const { rerender } = renderToasts()
    fireEvent.click(screen.getByRole('button', { name: '关闭有新版' }))
    expect(screen.queryByText('有新版 v0.3.1 可下载')).toBeNull()
    rerender(<MemoryRouter><UpdateToasts homeOpen={false} /></MemoryRouter>)
    expect(screen.queryByText('有新版 v0.3.1 可下载')).toBeNull()
  })
})
