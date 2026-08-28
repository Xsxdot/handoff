// SchedulingPage.test.tsx —— 自动化编制公开组件的 CAS 接缝测试。
// 边界：每条断言都从页面按钮/表单进入 scheduling API；不直接测试草稿 helper。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { SchedulingPage } from './SchedulingPage'

vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, getSquads: vi.fn(), putCarrier: vi.fn(), putSquad: vi.fn() }
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
  vi.mocked(putCarrier).mockResolvedValue({ name: 'mbp', version: 1 })
  vi.mocked(putSquad).mockResolvedValue({ name: 'exec', version: 8 })
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
    expect(screen.getByRole('combobox', { name: '凭据来源' })).toBeVisible()
    expect(screen.getByRole('option', { name: '主 HOME 同步' })).toBeVisible()
    expect(screen.getByText(/主 HOME 同步 = 把主环境的认证态搬进隔离 HOME/)).toBeVisible()
  })

  it('renders carrier and squad fields and empty-state guidance', async () => {
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: ['mbp'], version: 2 }],
    })
    render(<SchedulingPage />)
    expect(await screen.findByText('mbp')).toBeVisible()
    expect(screen.getByText('/h')).toBeVisible()
    expect(screen.getByText('coord')).toBeVisible()
    expect(screen.getByText('拉起通道（开卡即绑 / 一键拉起）')).toBeVisible()
  })

  it('creates with expect zero and edits with the row version', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValueOnce({ carriers: [], squads: [] })
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'mbp')
    await user.selectOptions(screen.getByLabelText('机器'), '本机')
    await user.selectOptions(screen.getByLabelText('CLI'), 'opencode')
    await user.type(screen.getByLabelText('HOME 档案'), '/h')
    await user.selectOptions(screen.getByLabelText('凭据来源'), 'standalone')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putCarrier).toHaveBeenCalledWith('mbp', 0, expect.objectContaining({ machine: '本机', home_dir: '/h' }))
    expect(vi.mocked(putCarrier).mock.calls[0]?.[2]).not.toHaveProperty('max_concurrency')

    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 7 }] })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putSquad).toHaveBeenCalledWith('exec', 7, expect.objectContaining({ role: 'executor', members: [] }))
  })

  it('shows an actionable CAS conflict and retains the modal', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({ carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }], squads: [] })
    vi.mocked(putCarrier).mockRejectedValue(new Error('409: 版本冲突，请刷新后重试'))
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑 mbp' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/版本冲突/)).toBeVisible()
    expect(screen.getByRole('dialog')).toBeVisible()
  })
})

describe('SchedulingPage 编辑弹窗对齐原型（B287）', () => {
  it('label 语义后缀、角色中文选项、政策位与 role hint、弹窗宽度逐项对齐 settings.html', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', credential: 'standalone', healthy: true, version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [], version: 1 }],
    })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    expect(screen.getByText('小队名（唯一）')).toBeVisible()
    expect(screen.getByText('角色（不混编）')).toBeVisible()
    expect(screen.getByRole('option', { name: '执行者队' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '协调者队' })).toBeInTheDocument()
    expect(screen.getByText('成员载体（按勾选顺序解析：第一个健康且有空的载体领活）')).toBeVisible()
    expect(screen.getByText('并发上限（政策位；0 / 留空 = 不限）')).toBeVisible()
    expect(screen.getByText(/协调者队成员必须落在协调机；执行者队成员可以是任何执行机。/)).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[440px]')
    await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(await screen.findByRole('button', { name: '编辑 mbp' }))
    expect(screen.getByText('载体名（唯一，登记后不可改）')).toBeVisible()
    expect(screen.getByText('模型（留空 = CLI 默认）')).toBeVisible()
    expect(screen.getByRole('dialog').querySelector('form')?.className).toContain('max-w-[480px]')
  })
})
