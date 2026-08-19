// MachineExecutor.test.tsx —— 开发机缺省执行者与默认模型配置块测试。

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MachineExecutor } from './MachineExecutor'
import * as client from '../../api/client'
import type { Machine } from '../../api/types'

const machine = { name: 'mac-02', reachable: true, executors: ['codex', 'opencode'], error: '' } as Machine
const resp = { default: 'opencode', model: 'm-oc', available: ['codex', 'opencode'] }

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(client, 'fetchExecutorDefault').mockResolvedValue(resp)
})

describe('MachineExecutor', () => {
  it('模型标签随缺省执行者变——让连带效应在保存前就可见', async () => {
    render(<MachineExecutor machine={machine} />)
    expect(await screen.findByLabelText('opencode 的默认模型')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '缺省执行者' }), 'codex')
    // 还没保存，但标签已经改了：用户看得见「这个模型名要套到 codex 头上了」
    expect(screen.getByLabelText('codex 的默认模型')).toBeInTheDocument()
  })

  it('下拉只列 available，不能自由输入', async () => {
    render(<MachineExecutor machine={machine} />)
    const select = await screen.findByRole('combobox', { name: '缺省执行者' })
    expect(Array.from(select.querySelectorAll('option')).map((o) => o.textContent))
      .toEqual(['codex', 'opencode'])
  })

  it('保存 payload 是整体替换的两项', async () => {
    const save = vi.spyOn(client, 'saveExecutorDefault').mockResolvedValue(
      { default: 'codex', model: 'gpt-5.6-luna', available: ['codex', 'opencode'] })
    render(<MachineExecutor machine={machine} />)
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: '缺省执行者' }), 'codex')
    const box = screen.getByLabelText('codex 的默认模型')
    await userEvent.clear(box)
    await userEvent.type(box, 'gpt-5.6-luna')
    expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(save).toHaveBeenCalledWith('mac-02', { default: 'codex', model: 'gpt-5.6-luna' })
  })

  it('后端 400 原样展示（可选名单是用户改对的线索）', async () => {
    vi.spyOn(client, 'saveExecutorDefault').mockRejectedValue(
      new client.ApiError(400, '未知 executor "opencde"（可选: codex, opencode）'))
    render(<MachineExecutor machine={machine} />)
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: '缺省执行者' }), 'codex')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/可选: codex, opencode/)).toBeInTheDocument()
  })

  it('机器断开时不发请求，展示 error 原文', () => {
    const f = vi.spyOn(client, 'fetchExecutorDefault')
    render(<MachineExecutor machine={{ ...machine, reachable: false, error: 'dial tcp: refused' } as Machine} />)
    expect(f).not.toHaveBeenCalled()
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument()
  })
})
