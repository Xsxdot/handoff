// SchedulingPage.test.tsx —— 自动化编制配置页的 CAS 与降级接缝测试。
//
// 边界：只验证页面到 scheduling API 的请求形状和用户可见错误；usePoll 的轮询
// 节奏由它自己的测试负责，本文件用受控快照隔离页面行为。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '../../api/client'
import { getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { SchedulingPage } from './SchedulingPage'

vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, getSquads: vi.fn(), putCarrier: vi.fn(), putSquad: vi.fn() }
})
vi.mock('../data/usePoll', () => ({ usePoll: vi.fn() }))

const response = {
  carriers: [{
    name: 'c1', machine: 'm1', cli: 'opencode', home_dir: '/h',
    credential: 'standalone', healthy: true, version: 3,
  }],
  squads: [{ name: 'coord', role: 'coordinator', members: [], version: 2 }],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(usePoll).mockReturnValue({
    data: response, disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
  })
  vi.mocked(getSquads).mockResolvedValue(response)
  vi.mocked(putCarrier).mockResolvedValue({ name: 'c1', version: 4 })
  vi.mocked(putSquad).mockResolvedValue({ name: 'coord', version: 3 })
})

describe('SchedulingPage', () => {
  it('显示载体/小队行版本与空 optional 字段', () => {
    render(<SchedulingPage />)
    expect(screen.getByRole('heading', { name: '自动化编制' })).toBeInTheDocument()
    expect(screen.getByText('c1')).toBeInTheDocument()
    expect(screen.getByText('v3')).toBeInTheDocument()
    expect(screen.getByText('coord')).toBeInTheDocument()
    expect(screen.getByText('v2')).toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: 'c1 max_concurrency' })).toHaveValue(null)
    expect(screen.getByRole('spinbutton', { name: 'coord max_concurrency' })).toHaveValue(null)
  })

  it('编辑载体和小队时按行携带服务端 version 保存', async () => {
    const user = userEvent.setup()
    render(<SchedulingPage />)
    const machine = screen.getByRole('textbox', { name: 'c1 machine' })
    await user.clear(machine)
    await user.type(machine, 'm2')
    await user.click(screen.getByRole('button', { name: '保存载体' }))
    expect(putCarrier).toHaveBeenCalledWith('c1', 3, {
      name: 'c1', machine: 'm2', cli: 'opencode', home_dir: '/h',
      credential: 'standalone', model: undefined, max_concurrency: undefined,
    })

    const role = screen.getByRole('textbox', { name: 'coord role' })
    await user.clear(role)
    await user.type(role, 'coordinator')
    await user.click(screen.getByRole('button', { name: '保存小队' }))
    expect(putSquad).toHaveBeenCalledWith('coord', 2, {
      name: 'coord', role: 'coordinator', members: [], max_concurrency: undefined,
    })
  })

  it('400/409 显示 API 原文', async () => {
    const user = userEvent.setup()
    vi.mocked(putCarrier).mockRejectedValueOnce(new ApiError(400, '载体不可达'))
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '保存载体' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('载体不可达')

    vi.mocked(putSquad).mockRejectedValueOnce(new ApiError(409, '版本冲突：当前是 4'))
    await user.click(screen.getByRole('button', { name: '保存小队' }))
    expect(await screen.findByText('版本冲突：当前是 4')).toBeInTheDocument()
  })

  it('首拉失败显示重试态，断线保留数据并禁用保存', () => {
    vi.mocked(usePoll).mockReturnValue({
      data: null, disconnected: false, sessionExpired: false,
      errorText: '读取配置失败', refresh: vi.fn(),
    })
    const { rerender } = render(<SchedulingPage />)
    expect(screen.getByRole('alert')).toHaveTextContent('读取配置失败')

    vi.mocked(usePoll).mockReturnValue({
      data: response, disconnected: true, sessionExpired: false,
      errorText: '连接已断开', refresh: vi.fn(),
    })
    rerender(<SchedulingPage />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接已断开')
    expect(screen.getByRole('button', { name: '保存载体' })).toBeDisabled()
  })
})
