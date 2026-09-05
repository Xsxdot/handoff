// SchedulingPage.test.tsx —— 自动化编制公开组件的 CAS 接缝测试。
// 边界：每条断言都从页面按钮/表单进入 scheduling API；不直接测试草稿 helper。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fetchMachines } from '../../api/client'
import { detectCarrier, deleteCarrier, getCarrierRunCommand, getSquads, probeHome, putCarrier, putSquad } from '../../api/scheduling'
import { SchedulingPage } from './SchedulingPage'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchMachines: vi.fn() }
})

vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, deleteCarrier: vi.fn(), detectCarrier: vi.fn(), getCarrierRunCommand: vi.fn(), getSquads: vi.fn(), probeHome: vi.fn(), putCarrier: vi.fn(), putSquad: vi.fn() }
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
  vi.mocked(deleteCarrier).mockResolvedValue({ name: 'mbp', version: 1 })
  vi.mocked(detectCarrier).mockResolvedValue({ name: 'mbp', status: 'online', version: 1 })
  vi.mocked(getCarrierRunCommand).mockResolvedValue({ command: 'HOME=/h opencode' })
  vi.mocked(probeHome).mockResolvedValue({ kind: 'empty' })
  vi.mocked(putCarrier).mockResolvedValue({ name: 'mbp', version: 1 })
  vi.mocked(putSquad).mockResolvedValue({ name: 'exec', version: 8 })
  vi.mocked(fetchMachines).mockResolvedValue({
    machines: [
      { name: '', addr: '', reachable: true, version: 'test', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '' },
      { name: 'linux-01', addr: '100.1.1.1:7777', reachable: true, version: 'test', executors: [], default_executor: '', probe_ms: 1, active_tasks: 0, error: '' },
    ],
  })
})

describe('SchedulingPage', () => {
  it('表单对齐原型：准入说明、机器/CLI/凭据枚举和主 HOME 同步提示完整', async () => {
    const user = userEvent.setup()
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))

    expect(screen.getByText(/准入 = 小队有位.*载体有位/)).toBeVisible()
    expect(screen.getByText(/协调者优先/)).toBeVisible()
    expect(screen.getByRole('combobox', { name: '机器' })).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'CLI' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'agy' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '凭据来源' })).toBeVisible()
    expect(screen.getByRole('option', { name: '主 HOME 同步' })).toBeVisible()
    expect(screen.getByText(/主 HOME 同步 = 把主环境的认证态搬进隔离 HOME/)).toBeVisible()
  })

  it('renders carrier and squad fields and empty-state guidance', async () => {
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', status: 'online', version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [{ carrier: 'mbp' }], version: 2 }],
    })
    render(<SchedulingPage />)
    expect(await screen.findByText('mbp')).toBeVisible()
    expect(screen.getByText('/h')).toBeVisible()
    expect(screen.getByText('coord')).toBeVisible()
    expect(screen.getByText('拉起通道（坐下 / 叫机器人）')).toBeVisible()
  })

  it('renders missing and unknown carrier statuses as 未上线', async () => {
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [
        { name: 'missing', machine: 'local', cli: 'opencode', home_dir: '/missing', credential: 'standalone', version: 1 },
        { name: 'unknown', machine: 'local', cli: 'opencode', home_dir: '/unknown', credential: 'standalone', status: 'mystery' as never, version: 2 },
      ],
      squads: [],
    })
    render(<SchedulingPage />)
    expect(await screen.findAllByText('未上线')).toHaveLength(2)
    expect(screen.queryByText('undefined')).not.toBeInTheDocument()
  })

  it('new carrier HOME stays empty when the name is typed', async () => {
    const user = userEvent.setup()
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    const name = screen.getByLabelText('载体名')
    const home = screen.getByLabelText('HOME 档案')
    await user.type(name, 'exec')
    expect(home).toHaveValue('')
    await user.type(home, '/custom/home')
    await user.clear(name)
    await user.type(name, 'renamed')
    expect(home).toHaveValue('/custom/home')
  })

  it('lists 主 HOME and skips probe when HOME is empty', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'plain', machine: '本机', cli: 'grok', home_dir: '', credential: 'standalone', status: 'online', version: 1 }],
      squads: [],
    })
    render(<SchedulingPage />)
    expect(await screen.findByText('主 HOME')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'mbp')
    await waitFor(() => expect(fetchMachines).toHaveBeenCalled())
    expect(probeHome).not.toHaveBeenCalled()
    expect(screen.getByRole('option', { name: 'linux-01' })).toBeInTheDocument()
  })

  it('probes the current draft through the API and renders the result', async () => {
    const user = userEvent.setup()
    vi.mocked(probeHome).mockResolvedValue({ kind: 'logged_in', detail: 'credential found' })
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('HOME 档案'), '~/.handoff/home/mbp')
    await waitFor(() => expect(probeHome).toHaveBeenCalledWith({
      cli: 'opencode', path: '~/.handoff/home/mbp', credential: 'standalone', machine: '本机',
    }))
    expect(await screen.findByText(/已发现该 CLI 凭据。 credential found/)).toBeVisible()
  })

  it('按载体行编辑小队成员政策并保留模型元信息', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValueOnce({
      carriers: [
        { name: 'c1', machine: 'local', cli: 'opencode', home_dir: '/h1', model: '', credential: 'standalone', status: 'online', version: 1 },
        { name: 'c2', machine: 'local', cli: 'opencode', home_dir: '/h2', model: 'flash', credential: 'standalone', status: 'online', version: 1 },
      ],
      squads: [],
    })
    render(<SchedulingPage />)
    await screen.findByText('c1')
    await user.click(screen.getByRole('button', { name: '建小队' }))
    expect(screen.getAllByText('CLI 默认').length).toBeGreaterThan(0)
    expect(screen.getAllByText('flash').length).toBeGreaterThan(0)

    await user.type(screen.getByLabelText('小队名'), 'exec')
    await user.click(screen.getByRole('checkbox', { name: /c1/ }))
    await user.type(screen.getByRole('textbox', { name: /c1.*政策/ }), '2')
    await user.click(screen.getByRole('checkbox', { name: /c2/ }))
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(putSquad).toHaveBeenCalledWith('exec', 0, {
      name: 'exec', role: 'executor',
      members: [{ carrier: 'c1', max_concurrency: 2 }, { carrier: 'c2' }],
    })
  })

  it.each(['0', '-1', '1.5', 'abc', '9007199254740992'])('非法成员政策 %s 在保存前阻断并显示合法示例', async (raw) => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValueOnce({
      carriers: [{ name: 'c1', machine: 'local', cli: 'opencode', home_dir: '/h', model: '', credential: 'standalone', status: 'online', version: 1 }],
      squads: [],
    })
    render(<SchedulingPage />)
    await screen.findByText('c1')
    await user.click(screen.getByRole('button', { name: '建小队' }))
    await user.type(screen.getByLabelText('小队名'), 'exec')
    await user.click(screen.getByRole('checkbox', { name: /c1/ }))
    const policy = screen.getByRole('textbox', { name: /c1.*政策/ })
    await user.type(policy, raw)
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/(正整数.*合法示例：2|安全整数范围)/)
    expect(putSquad).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('creates with expect zero and edits with the row version', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValueOnce({ carriers: [], squads: [] })
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'mbp')
    await user.selectOptions(screen.getByLabelText('机器'), '本机')
    await user.selectOptions(screen.getByLabelText('CLI'), 'opencode')
    await user.clear(screen.getByLabelText('HOME 档案'))
    await user.type(screen.getByLabelText('HOME 档案'), '/h')
    await user.selectOptions(screen.getByLabelText('凭据来源'), 'standalone')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putCarrier).toHaveBeenCalledWith('mbp', 0, expect.objectContaining({ machine: '本机', home_dir: '/h' }))
    expect(detectCarrier).toHaveBeenCalledTimes(1)
    expect(vi.mocked(putCarrier).mock.calls[0]?.[2]).not.toHaveProperty('max_concurrency')

    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 7 }] })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putSquad).toHaveBeenCalledWith('exec', 7, expect.objectContaining({ role: 'executor', members: [] }))
  })

  it('shows an actionable CAS conflict and retains the modal', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({ carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', status: 'online', version: 3 }], squads: [] })
    vi.mocked(putCarrier).mockRejectedValue(new Error('409: 版本冲突，请刷新后重试'))
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑 mbp' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/版本冲突/)).toBeVisible()
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(detectCarrier).not.toHaveBeenCalled()
  })

  it('carrier row delete button calls deleteCarrier with row version and reloads', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads)
      .mockResolvedValueOnce({
        carriers: [{ name: 'b334-probe', machine: '本机', cli: 'opencode', home_dir: '', credential: 'standalone', status: 'pending', version: 1 }],
        squads: [],
      })
      .mockResolvedValueOnce({
        carriers: [],
        squads: [],
      })
    render(<SchedulingPage />)
    expect(await screen.findByText('b334-probe')).toBeVisible()
    const deleteButton = screen.getByRole('button', { name: '删除 b334-probe' })
    await user.click(deleteButton)
    expect(deleteCarrier).toHaveBeenCalledWith('b334-probe', 1)
    await waitFor(() => expect(screen.queryByText('b334-probe')).not.toBeInTheDocument())
  })

  it('carrier deletion failure displays server error on that row', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'grok', machine: '本机', cli: 'opencode', home_dir: '', credential: 'standalone', status: 'online', version: 2 }],
      squads: [{ name: 'runner', role: 'executor', members: [{ carrier: 'grok' }], version: 1 }],
    })
    vi.mocked(deleteCarrier).mockRejectedValue(new Error('400: 载体 grok 仍在小队 runner 中，请先从小队移除'))
    render(<SchedulingPage />)
    expect(await screen.findByText('grok')).toBeVisible()
    await user.click(screen.getByRole('button', { name: '删除 grok' }))
    expect(deleteCarrier).toHaveBeenCalledWith('grok', 2)
    expect(await screen.findByText(/载体 grok 仍在小队 runner 中/)).toBeVisible()
    expect(screen.getByText('grok')).toBeVisible()
  })
})

describe('SchedulingPage 编辑弹窗对齐原型（B287）', () => {
  it('label 语义后缀、角色中文选项、政策位与 role hint、弹窗宽度逐项对齐 settings.html', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', status: 'online', version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [], version: 1 }],
    })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    expect(screen.getByText('小队名（唯一）')).toBeVisible()
    expect(screen.getByText('角色（不混编）')).toBeVisible()
    expect(screen.getByRole('option', { name: '执行者队' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '协调者队' })).toBeInTheDocument()
    expect(screen.getByText('成员载体（按勾选顺序解析：第一个健康且有空的载体领活）')).toBeVisible()
    // 成员行带 机器 · CLI 元信息（原型 check-list 每行形态）
    expect(within(screen.getByRole('dialog')).getByText('local · opencode')).toBeVisible()
    expect(screen.getByLabelText('mbp 政策并发')).toBeVisible()
    // role hint 在弹窗内新增；页面小队区块本就有一条同文提示，故圈定弹窗内断言。
    expect(within(screen.getByRole('dialog')).getByText(/协调者队成员必须落在协调机；执行者队成员可以是任何执行机。/)).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[440px]')
    await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(await screen.findByRole('button', { name: '编辑 mbp' }))
    expect(screen.getByText('载体名（唯一，登记后不可改）')).toBeVisible()
    expect(screen.getByText('模型（留空 = CLI 默认）')).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[480px]')
  })
})
