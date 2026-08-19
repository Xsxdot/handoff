import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Machine, MachinesResp, ProjectTreeResp } from '../../api/types'
import { addMachine, deleteMachine, ApiError } from '../../api/client'
import { useMachines } from '../data/useMachines'
import { MachinesPage } from './MachinesPage'

vi.mock('../data/useMachines', () => ({ useMachines: vi.fn() }))

// 新增/删除走 agentd 写接口，单独 mock 掉；其余导出（含 ApiError）保留真实实现。
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    addMachine: vi.fn(),
    deleteMachine: vi.fn(),
    fetchDiscipline: vi.fn().mockResolvedValue({ dir: '', builtins: [], files: [], bindings: [] }),
  }
})

const localMachine: Machine = {
  name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v0.1.0',
  executors: ['opencode', 'claude'], default_executor: 'opencode', probe_ms: 0, active_tasks: 2, error: '',
}
const devbox: Machine = {
  name: 'devbox', addr: '10.0.0.8:7777', reachable: true, version: 'v0.2.0',
  executors: ['opencode'], default_executor: 'opencode', probe_ms: 42, active_tasks: 1, error: '',
}
const nas: Machine = {
  name: 'nas', addr: '10.0.1.5:7777', reachable: false, version: '',
  executors: [], default_executor: '', probe_ms: 3000, active_tasks: 0,
  error: 'dial tcp 10.0.1.5:7777: connect: connection refused',
}

function mockStream(machines: Machine[]) {
  vi.mocked(useMachines).mockReturnValue({
    data: { machines } as MachinesResp,
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  })
}

function renderMachines(machines: Machine[]) {
  mockStream(machines)
  return render(<MachinesPage tree={tree} />)
}

// 最小合法树：MachinesPage 现在由 SettingsPage 传入 tree，这里用一个空树即可。
const tree: ProjectTreeResp = { projects: [], unowned: [] }

describe('MachinesPage', () => {
  beforeEach(() => {
    // 新增/删除用例会查 addMachine 的调用序列，逐用例清掉上次的调用记录
    vi.clearAllMocks()
  })

  it('顶部三个统计：台数 / 在线数 / 运行任务数', () => {
    renderMachines([localMachine, devbox, nas])
    const stat = (label: string) => screen.getByText(label).parentElement!.querySelector('dd')!.textContent
    expect(stat('开发机')).toBe('3') // 台数含不可达的那台——少一台就是静默丢机器（spec §8）
    expect(stat('在线')).toBe('2')
    expect(stat('运行任务')).toBe('3') // 2 + 1 + 0
  })

  it('不可达机器仍然渲染，标已断开并显示 error 原文', () => {
    renderMachines([nas])
    expect(screen.getAllByText('nas').length).toBeGreaterThan(0)
    expect(screen.getAllByText('已断开').length).toBeGreaterThan(0)
    expect(screen.getAllByText(/connection refused/).length).toBeGreaterThan(0)
  })

  it('本机（name:""）显示「本机」且不显示延迟格', () => {
    renderMachines([localMachine])
    expect(screen.getAllByText('本机').length).toBeGreaterThan(0)
    expect(screen.queryByText(/延迟/)).toBeNull()
  })

  it('可用执行者渲染为只读列表，默认执行者有标记，且没有任何开关', () => {
    renderMachines([localMachine])
    expect(screen.getByText('opencode')).toBeInTheDocument()
    expect(screen.getByText('claude')).toBeInTheDocument()
    expect(screen.getByText(/默认/)).toBeInTheDocument()
    expect(screen.queryByRole('switch')).toBeNull()
    expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('不渲染未实现功能：配对开发机 / Env 文件（形态未定；重启/终端是 NOT_WIRED 按钮）', () => {
    renderMachines([localMachine, devbox])
    for (const name of [/配对/, /Env/]) {
      expect(screen.queryByRole('button', { name })).toBeNull()
    }
  })

  it('不渲染「操作系统」格（后端没有这个数据）', () => {
    renderMachines([devbox])
    expect(screen.queryByText(/操作系统/)).toBeNull()
  })

  it('三个未接线的操作可点，点了明说尚未实现（不置灰）', () => {
    mockStream([localMachine])
    render(<MachinesPage tree={tree} />)
    // 卡片按钮与详情标题都含「本机」文案，点卡片按钮本身来选中本机。
    fireEvent.click(screen.getByRole('button', { name: /本机/ }))
    for (const label of ['可用执行者', '重启 agent', '打开终端']) {
      const btn = screen.getByRole('button', { name: new RegExp(label) })
      expect(btn).not.toBeDisabled()
      fireEvent.click(btn)
    }
    expect(screen.getAllByText(/尚未实现/).length).toBeGreaterThan(0)
  })

  it('离开 /machines 后停止探活', () => {
    renderMachines([localMachine])
    // 页面恒以 enabled=true 挂载机器流；路由切走时组件卸载，usePoll 的 effect
    // 清理 interval（该清理已被 Task 2 的 usePoll 测试覆盖），探活即停。
    expect(useMachines).toHaveBeenCalledWith(true)
  })

  it('提交新增表单后调用 addMachine 并刷新列表', async () => {
    const spy = vi.mocked(addMachine).mockResolvedValue({ machines: [] })
    renderMachines([localMachine])
    await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
    await userEvent.type(screen.getByLabelText('名字'), 'box')
    await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
    await userEvent.type(screen.getByLabelText('令牌'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: '添加' }))
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'box', addr: '10.0.0.1:7777', token: 'secret' }),
    )
  })

  it('探测失败时展示后端原文并提供「仍然保存」', async () => {
    vi.mocked(addMachine).mockRejectedValueOnce(new ApiError(400, '探测 10.0.0.1:7777 失败：连接被拒绝'))
    renderMachines([localMachine])
    await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
    await userEvent.type(screen.getByLabelText('名字'), 'box')
    await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
    await userEvent.type(screen.getByLabelText('令牌'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: '添加' }))
    expect(await screen.findByText(/连接被拒绝/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '仍然保存' })).toBeInTheDocument()
  })

  it('「仍然保存」以 force 重发', async () => {
    vi.mocked(addMachine)
      .mockRejectedValueOnce(new ApiError(400, '探测失败：连接被拒绝'))
      .mockResolvedValueOnce({ machines: [] })
    renderMachines([localMachine])
    await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
    await userEvent.type(screen.getByLabelText('名字'), 'box')
    await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
    await userEvent.type(screen.getByLabelText('令牌'), 'secret')
    await userEvent.click(screen.getByRole('button', { name: '添加' }))
    await userEvent.click(await screen.findByRole('button', { name: '仍然保存' }))
    expect(vi.mocked(addMachine).mock.calls[1][0]).toMatchObject({ force: true })
  })

  it('令牌输入框是密码型', async () => {
    renderMachines([localMachine])
    await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
    expect(screen.getByLabelText('令牌')).toHaveAttribute('type', 'password')
  })

  it('删除入口只给远程机器，确认后调用 deleteMachine', async () => {
    const spy = vi.mocked(deleteMachine).mockResolvedValue({ machines: [] })
    renderMachines([localMachine, devbox])
    // 本机（name:""）没有删除入口，远程机器有一处
    expect(screen.getAllByRole('button', { name: '删除' })).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    // ConfirmDialog 二次确认（弹层的确认按钮同名「删除」）
    expect(screen.getByText(/将删除「devbox」/)).toBeInTheDocument()
    await userEvent.click(screen.getAllByRole('button', { name: '删除' })[1])
    expect(spy).toHaveBeenCalledWith('devbox')
  })
})
