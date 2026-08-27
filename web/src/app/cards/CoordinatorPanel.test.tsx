import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ApiError } from '../../api/client'
import {
  attachCoordinator,
  getCoordinatorStatus,
  launchCoordinator,
  releaseCoordinator,
} from '../../api/scheduling'
import type { CoordinatorStatus } from '../../api/scheduling'
import { usePoll } from '../data/usePoll'
import { CoordinatorPanel } from './CoordinatorPanel'

vi.mock('../../api/scheduling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/scheduling')>()),
  attachCoordinator: vi.fn(),
  getCoordinatorStatus: vi.fn(),
  launchCoordinator: vi.fn(),
  releaseCoordinator: vi.fn(),
}))

vi.mock('../data/usePoll', () => ({ usePoll: vi.fn() }))

const refresh = vi.fn()

function pollState(over: Partial<{
  data: CoordinatorStatus | null
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
}> = {}) {
  return {
    data: { bound: false, attach_active: false, attach: null },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh,
    ...over,
  }
}

describe('协调者面板', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(usePoll).mockReturnValue(pollState() as never)
    vi.mocked(getCoordinatorStatus).mockResolvedValue(pollState().data as CoordinatorStatus)
    vi.mocked(launchCoordinator).mockResolvedValue({ woke: true, rebuilt: false, escalated: false, output: '协调者已拉起' })
    vi.mocked(attachCoordinator).mockResolvedValue({ machine: '', dir: '/repo/handoff', command: 'opencode --session sess' })
    vi.mocked(releaseCoordinator).mockResolvedValue({ ok: true })
  })

  it('未绑定时可以拉起，并显示服务端成功回执', async () => {
    render(<CoordinatorPanel cardId="B1" />)
    fireEvent.click(screen.getByRole('button', { name: '拉起协调者' }))
    await waitFor(() => expect(launchCoordinator).toHaveBeenCalledWith('B1'))
    expect(await screen.findByText('协调者已拉起')).toBeInTheDocument()
    expect(refresh).toHaveBeenCalled()
  })

  it('拉起失败保留后端原文', async () => {
    vi.mocked(launchCoordinator).mockRejectedValueOnce(new ApiError(400, '未登记协调者小队'))
    render(<CoordinatorPanel cardId="B1" />)
    fireEvent.click(screen.getByRole('button', { name: '拉起协调者' }))
    expect(await screen.findByText('未登记协调者小队')).toBeInTheDocument()
  })

  it('已绑定未接管时 attach 使用状态目录并显示定位三元组', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({
      data: {
        bound: true,
        attach_active: false,
        attach: { machine: '', dir: '/repo/handoff', command: 'opencode --session cached' },
      },
    }) as never)
    vi.mocked(attachCoordinator).mockResolvedValue({ machine: 'box-2', dir: '/workspace/card', command: 'claude --session sess-2' })
    render(<CoordinatorPanel cardId="B1" />)
    fireEvent.click(screen.getByRole('button', { name: '打开终端' }))
    await waitFor(() => expect(attachCoordinator).toHaveBeenCalledWith('B1', '/repo/handoff'))
    expect(await screen.findByText('box-2')).toBeInTheDocument()
    expect(screen.getByText('/workspace/card')).toBeInTheDocument()
    expect(screen.getByText('claude --session sess-2')).toBeInTheDocument()
  })

  it('接管态可以交回无头协调者', async () => {
    vi.mocked(usePoll).mockReturnValue(pollState({
      data: {
        bound: true,
        attach_active: true,
        attach: { machine: '', dir: '/repo/handoff', command: 'opencode --session sess' },
      },
    }) as never)
    render(<CoordinatorPanel cardId="B1" />)
    fireEvent.click(screen.getByRole('button', { name: '交回无头' }))
    await waitFor(() => expect(releaseCoordinator).toHaveBeenCalledWith('B1'))
    expect(refresh).toHaveBeenCalled()
  })

  it('断线保留状态但禁用会改状态的按钮', () => {
    vi.mocked(usePoll).mockReturnValue(pollState({
      data: { bound: true, attach_active: false, attach: null },
      disconnected: true,
      errorText: 'coordinator offline',
    }) as never)
    render(<CoordinatorPanel cardId="B1" />)
    expect(screen.getByText('已绑定')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '打开终端' })).toBeDisabled()
    expect(screen.getByText('coordinator offline')).toBeInTheDocument()
  })

  it('会话失效与首拉失败分别显示终止态', () => {
    vi.mocked(usePoll).mockReturnValue(pollState({ data: null, sessionExpired: true }) as never)
    const expired = render(<CoordinatorPanel cardId="B1" />)
    expect(screen.getByText(/会话已失效/)).toBeInTheDocument()
    expired.unmount()

    vi.mocked(usePoll).mockReturnValue(pollState({ data: null, errorText: 'status unavailable' }) as never)
    render(<CoordinatorPanel cardId="B1" />)
    expect(screen.getByText('status unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
