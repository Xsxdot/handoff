import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CreateProjectResp, Machine } from '../../api/types'
import { AddProjectWizard } from './AddProjectWizard'
import * as register from './register'

vi.mock('./register', () => ({ registerAll: vi.fn() }))

const localMachine: Machine = {
  name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v0.1.0',
  executors: ['opencode'], default_executor: 'opencode', probe_ms: 0, active_tasks: 0, error: '',
}
const devbox: Machine = {
  name: 'devbox', addr: '10.0.0.8:7777', reachable: true, version: 'v0.2.0',
  executors: ['opencode'], default_executor: 'opencode', probe_ms: 42, active_tasks: 0, error: '',
}
const nas: Machine = {
  name: 'nas', addr: '10.0.1.5:7777', reachable: false, version: '',
  executors: [], default_executor: '', probe_ms: 3000, active_tasks: 0,
  error: 'dial tcp 10.0.1.5:7777: connect: connection refused',
}

function cb() {
  return { onClose: vi.fn(), onDone: vi.fn() }
}

// renderAtStepTwo 选中指定位置（''=本机，其余=远程机器名）并进入第二步。
function renderAtStepTwo(selected: string[], machines: Machine[] = [localMachine, devbox]) {
  const callbacks = cb()
  render(<AddProjectWizard open machines={machines} {...callbacks} />)
  if (selected.includes('')) fireEvent.click(screen.getByRole('checkbox', { name: '本机' }))
  for (const name of selected) {
    if (name !== '') fireEvent.click(screen.getByRole('radio', { name: new RegExp(name) }))
  }
  fireEvent.click(screen.getByRole('button', { name: '下一步' }))
  return callbacks
}

describe('AddProjectWizard', () => {
  it('第一步至少选一个位置，未选时下一步禁用', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
    fireEvent.click(screen.getByRole('checkbox', { name: '本机' }))
    expect(screen.getByRole('button', { name: '下一步' })).toBeEnabled()
  })

  it('远程位置单选——选了 devbox 再选 nas 会替换而不是叠加（ADR-0008：至多一台远程）', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox, nas]} {...cb()} />)
    fireEvent.click(screen.getByRole('radio', { name: 'devbox' }))
    fireEvent.click(screen.getByRole('radio', { name: /nas/ }))
    expect(screen.getByRole('radio', { name: 'devbox' })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: /nas/ })).toBeChecked()
  })

  it('不可达的机器可选，但给出「登记可能失败」的提示', () => {
    render(<AddProjectWizard open machines={[localMachine, nas]} {...cb()} />)
    const opt = screen.getByRole('radio', { name: /nas/ })
    expect(opt).toBeEnabled() // 可选：要不要试是用户的决定，不替他挡
    fireEvent.click(opt)
    expect(screen.getByText(/登记可能失败/)).toBeInTheDocument()
  })

  it('本机位置用粘贴路径输入框，没有 Finder 选择器', () => {
    renderAtStepTwo(['', 'devbox'])
    // 浏览器里没有 Finder；File System Access API 故意不返回真实路径（spec §9）
    expect(screen.queryByRole('button', { name: /选择.*文件夹|浏览/ })).toBeNull()
    expect(screen.getAllByPlaceholderText(/粘贴.*路径/)).toHaveLength(2)
  })

  it('clone 路径留空时提示由该机器 clone 到它自己的 repo_root，不硬编码 ~/.handoff/<name>', () => {
    renderAtStepTwo(['devbox'])
    expect(screen.getByText(/clone 到它自己的 repo_root/)).toBeInTheDocument()
    // 原型标的 ~/.handoff/<project-name> 与 B62 实际的 repo_root/<name> 不一致，
    // 显示一个可能是错的默认路径比不显示更糟（spec §9）
    expect(screen.queryByText(/~\/\.handoff\//)).toBeNull()
  })

  it('一成一败时逐位置显示结果，成功的保留、失败的可重试', async () => {
    vi.mocked(register.registerAll).mockResolvedValue([
      // CreateProjectResp 是完整 ProjectLocation（origin_url/created_at 必填），
      // 这里只给断言关心的最小形态
      { machine: '', ok: true, error: '', result: { project_id: 'p', name: 'a', path: '/a' } as CreateProjectResp },
      { machine: 'devbox', ok: false, error: 'clone 失败：Permission denied (publickey)', result: undefined },
    ])
    renderAtStepTwo(['', 'devbox'])
    const gitInputs = screen.getAllByPlaceholderText(/Git 仓库地址/)
    fireEvent.change(gitInputs[0], { target: { value: 'git@x:/a.git' } })
    fireEvent.change(gitInputs[1], { target: { value: 'git@x:/a.git' } })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/Permission denied/)).toBeInTheDocument())
    expect(screen.getByText(/本机/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
  })
})
