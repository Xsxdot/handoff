import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Machine } from '../../api/types'
import { fetchEnv, saveEnvMapping } from '../../api/client'
import { MachineEnv } from './MachineEnv'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchEnv: vi.fn(), saveEnvMapping: vi.fn() }
})

const machine = { name: 'mac-02', reachable: true, executors: ['codex', 'opencode'], error: '' } as Machine

const resp = {
  dir: '/home/dev/.handoff/env',
  files: [{ name: 'proxy.env', size: 64, sha256: 'aa' }],
  bindings: [
    { executor: 'codex', mode: 'off' as const },
    { executor: 'opencode', mode: 'file' as const, file: 'proxy.env' },
  ],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchEnv).mockResolvedValue(resp as never)
})

describe('MachineEnv', () => {
  it('两档下拉：只有「不注入」与文件名，没有「内置默认」', async () => {
    render(<MachineEnv machine={machine} />)
    const select = await screen.findByRole('combobox', { name: /codex 的 env 文件/ })
    const options = Array.from(select.querySelectorAll('option')).map((option) => option.textContent)
    expect(options).toEqual(['不注入', 'proxy.env'])
  })

  it('保存 payload 用两档编码，off 不带 file', async () => {
    const save = vi.mocked(saveEnvMapping).mockResolvedValue(resp as never)
    render(<MachineEnv machine={machine} />)
    const select = await screen.findByRole('combobox', { name: /codex 的 env 文件/ })
    await userEvent.selectOptions(select, 'file:proxy.env')
    expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(save).toHaveBeenCalledWith('mac-02', [
      { executor: 'codex', mode: 'file', file: 'proxy.env' },
      { executor: 'opencode', mode: 'file', file: 'proxy.env' },
    ])
  })

  it('机器断开时不发请求，展示 error 原文', () => {
    const fetch = vi.mocked(fetchEnv)
    render(<MachineEnv machine={{ ...machine, reachable: false, error: 'dial tcp: refused' } as Machine} />)
    expect(fetch).not.toHaveBeenCalled()
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument()
  })
})
