import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Machine } from '../../api/types'
import * as client from '../../api/client'
import { MachineLaunchers } from './MachineLaunchers'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchEnv: vi.fn(),
    fetchLaunchers: vi.fn(),
    putLaunchers: vi.fn(),
  }
})

const baseMachine = {
  name: 'mac-02', addr: '', reachable: true, version: '', executors: [], default_executor: '',
  probe_ms: 0, active_tasks: 0, error: '', launchers_supported: true,
} as Machine

const launchers = {
  launchers: [{ name: '测试', env_file: 'gone.env', command: 'echo test', env_missing: true }],
}

const env = {
  dir: '/tmp/env', files: [{ name: 'proxy.env', size: 10, sha256: 'aa' }], bindings: [],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(client.fetchLaunchers).mockResolvedValue(launchers)
  vi.mocked(client.fetchEnv).mockResolvedValue(env)
})

describe('MachineLaunchers', () => {
  it('名字为空时不发请求，并展示本地校验实话', async () => {
    const put = vi.mocked(client.putLaunchers).mockResolvedValue(launchers)
    render(<MachineLaunchers machine={baseMachine} />)
    const name = await screen.findByLabelText('第 1 条启动项名称')
    await userEvent.clear(name)
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(put).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('第 1 条启动项的名字不能为空')
  })

  it('env 与命令都空时不发请求，并展示本地校验实话', async () => {
    const put = vi.mocked(client.putLaunchers).mockResolvedValue(launchers)
    render(<MachineLaunchers machine={baseMachine} />)
    await userEvent.selectOptions(await screen.findByLabelText('第 1 条启动项 env 文件'), '')
    await userEvent.clear(screen.getByLabelText('第 1 条启动项命令'))
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(put).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toHaveTextContent('Env 文件与执行命令至少填一个')
  })

  it('env_missing 的条目有可见标注', async () => {
    render(<MachineLaunchers machine={baseMachine} />)
    expect(await screen.findByText(/env 文件缺失：gone\.env/)).toBeInTheDocument()
  })

  it('保存后用服务端返回值刷新，而不是本地乐观值', async () => {
    const returned = { launchers: [{ name: '服务端规整', command: 'echo normalized', env_missing: false }] }
    const put = vi.mocked(client.putLaunchers).mockResolvedValue(returned)
    render(<MachineLaunchers machine={baseMachine} />)
    const name = await screen.findByLabelText('第 1 条启动项名称')
    fireEvent.change(name, { target: { value: 'local-draft' } })
    const saveButton = screen.getByRole('button', { name: '保存' })
    expect(saveButton).not.toBeDisabled()
    await userEvent.click(saveButton)
    expect(put).toHaveBeenCalled()
    expect(await screen.findByDisplayValue('服务端规整')).toBeInTheDocument()
    expect(screen.queryByDisplayValue('local-draft')).not.toBeInTheDocument()
  })

  it.each([
    ['true', true, true],
    ['false', false, false],
    ['undefined', undefined, false],
  ])('launchers_supported=%s 时按三态门渲染', async (_label, supported, visible) => {
    render(<MachineLaunchers machine={{ ...baseMachine, launchers_supported: supported } as Machine} />)
    if (visible) {
      expect(await screen.findByText('自定义启动项')).toBeInTheDocument()
    } else {
      expect(screen.queryByText('自定义启动项')).not.toBeInTheDocument()
      expect(client.fetchLaunchers).not.toHaveBeenCalled()
    }
  })
})
