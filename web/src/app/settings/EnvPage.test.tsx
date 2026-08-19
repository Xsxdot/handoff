// EnvPage.test.tsx —— Env 设置分区的变量清单、按需正文与断开降级测试。
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EnvPage } from './EnvPage'
import * as client from '../../api/client'
import { useMachines } from '../data/useMachines'

vi.mock('../data/useMachines', () => ({
  useMachines: vi.fn(),
}))

const envResp = {
  dir: '/home/dev/.handoff/env',
  files: [{ name: 'proxy.env', size: 64, sha256: 'aa' }],
  bindings: [{ executor: 'opencode', mode: 'file' as const, file: 'proxy.env' }],
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.mocked(useMachines).mockReturnValue({
    data: { machines: [{ name: '', reachable: true, executors: ['opencode'], error: '' }] },
    errorText: '', sessionExpired: false,
  } as never)
  vi.spyOn(client, 'fetchEnv').mockResolvedValue(envResp)
  vi.spyOn(client, 'fetchEnvKeys').mockResolvedValue({
    keys: [
      { key: 'HTTPS_PROXY', value_bytes: 34 },
      { key: 'GOPROXY', value_bytes: 21, duplicate: true },
    ],
  })
})

describe('EnvPage', () => {
  it('默认显示变量清单，不显示值，也不拉全文', async () => {
    const full = vi.spyOn(client, 'fetchEnvFile')
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    expect(await screen.findByText('HTTPS_PROXY')).toBeInTheDocument()
    expect(screen.getByText(/34 字节/)).toBeInTheDocument()
    expect(screen.getByText(/重复定义/)).toBeInTheDocument()
    // 承重判据：默认视图不得触碰含值的全文接口
    expect(full).not.toHaveBeenCalled()
    expect(screen.queryByRole('textbox', { name: /env 文件正文/ })).not.toBeInTheDocument()
  })

  it('点「编辑正文」才拉全文并给出编辑器', async () => {
    const full = vi.spyOn(client, 'fetchEnvFile')
      .mockResolvedValue({ content: 'HTTPS_PROXY=http://x\n', size: 21, sha256: 'bb' })
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    await userEvent.click(await screen.findByRole('button', { name: '编辑正文' }))
    expect(full).toHaveBeenCalledWith('', 'proxy.env')
    expect(await screen.findByRole('textbox', { name: /env 文件正文/ })).toHaveValue('HTTPS_PROXY=http://x\n')
  })

  it('语法错误时展示后端原文且不清空编辑内容', async () => {
    vi.spyOn(client, 'fetchEnvFile')
      .mockResolvedValue({ content: 'A=1\n', size: 4, sha256: 'bb' })
    vi.spyOn(client, 'saveEnvFile').mockRejectedValue(
      new client.ApiError(400, 'env 文件第 2 行语法错误：1BAD=x'))
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    await userEvent.click(await screen.findByRole('button', { name: '编辑正文' }))
    const box = await screen.findByRole('textbox', { name: /env 文件正文/ })
    await userEvent.type(box, '1BAD=x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/第 2 行语法错误/)).toBeInTheDocument()
    expect(box).toHaveValue('A=1\n1BAD=x') // 编辑内容不许被清掉
  })

  it('没有内置版：左栏只有一组文件，也没有「以此为模板新建」', async () => {
    render(<EnvPage />)
    await screen.findByRole('button', { name: /proxy\.env/ })
    expect(screen.queryByText(/内置/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '以此为模板新建' })).not.toBeInTheDocument()
  })

  it('机器断开时不发请求，展示 error 原文', () => {
    const fetchEnv = vi.spyOn(client, 'fetchEnv')
    vi.mocked(useMachines).mockReturnValue({
      data: { machines: [{ name: '', reachable: false, executors: ['opencode'], error: 'dial tcp: refused' }] },
      errorText: '', sessionExpired: false,
    } as never)
    render(<EnvPage />)
    expect(fetchEnv).not.toHaveBeenCalled()
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument()
  })
})
