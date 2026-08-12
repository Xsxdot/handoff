import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CreateProjectResp, Machine } from '../../api/types'
import { AddProjectWizard } from './AddProjectWizard'
import * as register from './register'

vi.mock('./register', () => ({ registerFromForm: vi.fn(), registerAll: vi.fn() }))

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

// localOk 是本机登记成功响应的最小 mock 形态。CreateProjectResp 是完整
// ProjectLocation（origin_url/created_at 必填），mock 只给断言关心的字段，故显式收窄。
function localOk(): CreateProjectResp {
  return { project_id: 'p', name: 'handoff', path: '/Users/me/h', origin_url: 'git@x:h.git' } as CreateProjectResp
}

function cb() {
  return { onClose: vi.fn(), onDone: vi.fn() }
}

beforeEach(() => {
  vi.clearAllMocks()
})

// fillLocalPath 填本机 path——单页表单里唯一的必填项。
function fillLocalPath(value = '/Users/me/h') {
  fireEvent.change(screen.getByPlaceholderText(/本机目录路径/), { target: { value } })
}

// enableRemote 勾上「同时登记到开发机」并选中给定远程机器。
function enableRemote(machine = 'devbox') {
  fireEvent.click(screen.getByLabelText(/同时登记到开发机/))
  fireEvent.click(screen.getByRole('radio', { name: new RegExp(machine) }))
}

describe('AddProjectWizard', () => {
  it('单页直接提交，没有「下一步」两步向导', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    expect(screen.queryByRole('button', { name: '下一步' })).toBeNull()
    expect(screen.getByRole('button', { name: '提交' })).toBeInTheDocument()
  })

  it('有 name 输入框（可选；默认用仓库名）', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    expect(screen.getByPlaceholderText(/可选.*仓库名/)).toBeInTheDocument()
  })

  it('本机区块固定必选，没有「本机」checkbox', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    expect(screen.queryByRole('checkbox', { name: '本机' })).toBeNull()
  })

  it('本机 path 为空时提交禁用，填了才放行（gitUrl 不作要求）', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    expect(screen.getByRole('button', { name: '提交' })).toBeDisabled()
    fillLocalPath()
    expect(screen.getByRole('button', { name: '提交' })).toBeEnabled()
  })

  it('勾了远程但没选机器时提交禁用，选中后放行', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath()
    fireEvent.click(screen.getByLabelText(/同时登记到开发机/))
    expect(screen.getByRole('button', { name: '提交' })).toBeDisabled()
    fireEvent.click(screen.getByRole('radio', { name: /devbox/ }))
    expect(screen.getByRole('button', { name: '提交' })).toBeEnabled()
  })

  it('远程勾选后仍只有一处 Git URL 输入（远程复用本机 origin，不再填 URL）', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fireEvent.click(screen.getByLabelText(/同时登记到开发机/))
    expect(screen.getAllByPlaceholderText(/Git/)).toHaveLength(1)
  })

  it('远程机器单选——选了 devbox 再选 nas 会替换而不是叠加（ADR-0008：至多一台远程）', () => {
    render(<AddProjectWizard open machines={[localMachine, devbox, nas]} {...cb()} />)
    fireEvent.click(screen.getByLabelText(/同时登记到开发机/))
    fireEvent.click(screen.getByRole('radio', { name: /devbox/ }))
    fireEvent.click(screen.getByRole('radio', { name: /nas/ }))
    expect(screen.getByRole('radio', { name: /devbox/ })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: /nas/ })).toBeChecked()
  })

  it('不可达的远程机器可选，但给出「登记可能失败」的提示', () => {
    render(<AddProjectWizard open machines={[localMachine, nas]} {...cb()} />)
    fireEvent.click(screen.getByLabelText(/同时登记到开发机/))
    const opt = screen.getByRole('radio', { name: /nas/ })
    expect(opt).toBeEnabled() // 可选：要不要试是用户的决定，不替他挡
    fireEvent.click(opt)
    expect(screen.getByText(/登记可能失败/)).toBeInTheDocument()
  })

  it('提交按表单字段调用 registerFromForm（未勾远程时 remoteMachine 为 null）', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fireEvent.change(screen.getByPlaceholderText(/可选.*仓库名/), { target: { value: 'demo' } })
    fillLocalPath('/Users/me/h')
    fireEvent.change(screen.getByPlaceholderText(/Git/), { target: { value: 'git@x:h.git' } })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() =>
      expect(register.registerFromForm).toHaveBeenCalledWith({
        name: 'demo',
        localPath: '/Users/me/h',
        gitUrl: 'git@x:h.git',
        remoteMachine: null,
        remotePath: '',
      }),
    )
  })

  it('勾了远程时提交带上选中的机器与远程 path', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: true, error: '', result: localOk() },
      { machine: 'devbox', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath()
    enableRemote('devbox')
    fireEvent.change(screen.getByPlaceholderText(/留空由该机器 clone/), { target: { value: '/srv/h' } })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() =>
      expect(register.registerFromForm).toHaveBeenCalledWith({
        name: '',
        localPath: '/Users/me/h',
        gitUrl: '',
        remoteMachine: 'devbox',
        remotePath: '/srv/h',
      }),
    )
  })

  it('一成一败时逐位置显示结果，成功的保留、失败的可重试', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: true, error: '', result: localOk() },
      { machine: 'devbox', ok: false, error: 'clone 失败：Permission denied (publickey)' },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath()
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/Permission denied/)).toBeInTheDocument())
    expect(screen.getByText('本机')).toBeInTheDocument()
    expect(screen.getByText('devbox')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()
  })

  it('远程重试复用本机成功结果里的权威 origin/name，不要求重填 URL', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: true, error: '', result: localOk() },
      { machine: 'devbox', ok: false, error: 'clone 失败：Permission denied (publickey)' },
    ])
    vi.mocked(register.registerAll).mockResolvedValue([
      { machine: 'devbox', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath()
    enableRemote('devbox')
    fireEvent.change(screen.getByPlaceholderText(/留空由该机器 clone/), { target: { value: '/srv/h' } })
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/Permission denied/)).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() =>
      expect(register.registerAll).toHaveBeenCalledWith([
        { machine: 'devbox', originUrl: 'git@x:h.git', name: 'handoff', path: '/srv/h' },
      ]),
    )
  })

  it('本机失败时远程行标为未尝试；gitUrl 为空则远程重试禁用并提示', async () => {
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: false, error: '路径不存在' },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fillLocalPath('/nope')
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/未尝试/)).toBeInTheDocument())
    expect(screen.getByText(/先修好本机.*Git 地址/)).toBeInTheDocument()
    const retries = screen.getAllByRole('button', { name: '重试' })
    // 本机行可重试；远程行在本机没成功且没填 Git 地址时禁用
    expect(retries).toHaveLength(2)
    expect(retries[0]).toBeEnabled()
    expect(retries[1]).toBeDisabled()
  })

  it('本机失败但填了 gitUrl 时，远程可用表单 gitUrl + name 单独重试', async () => {
    // 本 task 里编排还只回一条本机结果，「未尝试」那行仍由组件现编（Task 5 才下沉）
    vi.mocked(register.registerFromForm).mockResolvedValue([
      { machine: '', ok: false, error: '路径不存在' },
    ])
    vi.mocked(register.registerAll).mockResolvedValue([
      { machine: 'devbox', ok: true, error: '', result: localOk() },
    ])
    render(<AddProjectWizard open machines={[localMachine, devbox]} {...cb()} />)
    fireEvent.change(screen.getByPlaceholderText(/可选.*仓库名/), { target: { value: 'demo' } })
    fillLocalPath('/nope')
    fireEvent.change(screen.getByPlaceholderText(/Git/), { target: { value: 'git@x:h.git' } })
    enableRemote('devbox')
    fireEvent.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(screen.getByText(/未尝试/)).toBeInTheDocument())
    const retries = screen.getAllByRole('button', { name: '重试' })
    fireEvent.click(retries[1])
    await waitFor(() =>
      expect(register.registerAll).toHaveBeenCalledWith([
        // 表单里填了 name 就必须带上——否则远程会按 origin 末段自己派生一个，
        // 与本机成功时用权威 name 的行为不一致
        { machine: 'devbox', originUrl: 'git@x:h.git', name: 'demo', path: '' },
      ]),
    )
  })
})
