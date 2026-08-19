import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { SettingsPage } from './SettingsPage'

vi.mock('../data/useMachines', () => ({
  useMachines: () => ({
    data: {
      machines: [{
        name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v1',
        executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '',
      }],
    },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))
vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({
    data: { projects: [], machines: [], unowned: [] },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchDiscipline: vi.fn().mockResolvedValue({ dir: '/d', builtins: [], files: [], bindings: [] }),
  }
})

describe('SettingsPage', () => {
  it('四个分区都在，缺省停在开发机', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument()
    for (const label of ['开发机', '执行纪律', '常规', 'Env 文件']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
    }
    await waitFor(() => expect(screen.getAllByText('本机').length).toBeGreaterThan(0))
  })

  it('点「执行纪律」能切到该分区', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '执行纪律' }))
    expect(screen.getByRole('heading', { name: '执行纪律' })).toBeInTheDocument()
  })

  it('切到常规分区显示明确的空占位，而不是空白', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '常规' }))
    expect(screen.getByText(/本期没有可配置项/)).toBeInTheDocument()
  })

  it('切到 Env 文件分区说明本期不做', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Env 文件' }))
    expect(screen.getByText(/本期不做/)).toBeInTheDocument()
  })

  it('返回工作台调 onClose', () => {
    const onClose = vi.fn()
    render(<SettingsPage onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '返回工作台' }))
    expect(onClose).toHaveBeenCalled()
  })
})
