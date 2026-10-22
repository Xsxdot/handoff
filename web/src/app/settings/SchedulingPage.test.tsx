// SchedulingPage.test.tsx —— 自动化编制公开组件的 CAS 接缝测试。
// 边界：每条断言都从页面按钮/表单进入 scheduling API；不直接测试草稿 helper。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { fetchMachines } from '../../api/client'
import { detectCarrier, deleteCarrier, getCarrierRunCommand, getSquads, putCarrier, putSquad } from '../../api/scheduling'
import { SchedulingPage } from './SchedulingPage'
import { __resetAccountsStoreForTest, __setAccountsForTest } from '../accounts/store'
import { sampleAccounts } from '../accounts/testAccounts'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchMachines: vi.fn() }
})

vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, deleteCarrier: vi.fn(), detectCarrier: vi.fn(), getCarrierRunCommand: vi.fn(), getSquads: vi.fn(), putCarrier: vi.fn(), putSquad: vi.fn() }
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [], running: [] })
  vi.mocked(deleteCarrier).mockResolvedValue({ name: 'mbp', version: 1 })
  vi.mocked(detectCarrier).mockResolvedValue({ name: 'mbp', status: 'online', version: 1 })
  vi.mocked(getCarrierRunCommand).mockResolvedValue({ command: 'HOME=/h opencode' })
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
  it('compact 载体和小队采用可展开分段列表，说明折叠且详情按行展开', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: '本机', cli: 'opencode', home_dir: '/Users/dev', model: 'sonnet', status: 'online', max_concurrency: 2, version: 3 }],
      squads: [{ name: '执行队', role: 'executor', members: [{ carrier: 'mbp', max_concurrency: 1 }], version: 4 }],
      running: [],
    })
    render(<SchedulingPage compact />)
    const help = await screen.findByText('自动化说明')
    expect(help.closest('details')).not.toHaveAttribute('open')
    expect(screen.getByRole('tablist', { name: '自动化类别' })).toBeVisible()
    expect(screen.getByRole('tab', { name: '载体' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('button', { name: '查看载体 mbp' })).toBeVisible()
    expect(screen.queryByTestId('automation-squad-row-执行队')).not.toBeInTheDocument()
    expect(screen.queryByText('sonnet')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '检测' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看载体 mbp' }))
    expect(await screen.findByText(/未选登录/)).toBeVisible()
    expect(screen.getByText('sonnet')).toBeVisible()
    await user.click(screen.getByText('操作'))
    expect(screen.getByRole('button', { name: '检测 mbp' })).toBeVisible()
    await user.click(screen.getByRole('tab', { name: '小队' }))
    expect(screen.getByRole('button', { name: '查看小队 执行队' })).toBeVisible()
    expect(screen.queryByTestId('automation-carrier-row-mbp')).not.toBeInTheDocument()
  })

  it('表单对齐原型：机器/CLI/账号登录枚举，已删 HOME 输入与凭据模式', async () => {
    const user = userEvent.setup()
    __setAccountsForTest(sampleAccounts())
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))

    expect(screen.getByText(/准入 = 小队有位.*载体有位/)).toBeVisible()
    expect(screen.getByText(/协调者优先/)).toBeVisible()
    expect(screen.getByRole('combobox', { name: '机器' })).toBeVisible()
    expect(screen.getByRole('combobox', { name: 'CLI' })).toBeVisible()
    expect(screen.getByRole('option', { name: 'agy' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '账号' })).toBeVisible()
    expect(screen.getByRole('option', { name: '无账号 · 主 HOME' })).toBeInTheDocument()
    expect(screen.queryByLabelText('凭据来源')).toBeNull()
    expect(screen.queryByLabelText('HOME 档案')).toBeNull()
  })

  it('renders carrier and squad fields and empty-state guidance', async () => {
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', status: 'online', version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [{ carrier: 'mbp' }], version: 2 }],
      running: [],
    })
    render(<SchedulingPage />)
    expect(await screen.findByText('mbp')).toBeVisible()
    expect(screen.getByText(/未选登录/)).toBeVisible()
    expect(screen.getByText('coord')).toBeVisible()
    expect(screen.getByText('拉起通道（坐下 / 叫机器人）')).toBeVisible()
  })

  it('renders missing and unknown carrier statuses as 未上线', async () => {
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [
        { name: 'missing', machine: 'local', cli: 'opencode', home_dir: '/missing', version: 1 },
        { name: 'unknown', machine: 'local', cli: 'opencode', home_dir: '/unknown', status: 'mystery' as never, version: 2 },
      ],
      squads: [],
      running: [],
    })
    render(<SchedulingPage />)
    expect(await screen.findAllByText('未上线')).toHaveLength(2)
    expect(screen.queryByText('undefined')).not.toBeInTheDocument()
  })

  it('载体对话框没有 HOME 输入；选中登录后只读显示该登录决定的 HOME/凭据/代理', async () => {
    const user = userEvent.setup()
    __setAccountsForTest(sampleAccounts())
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'exec')
    await user.selectOptions(screen.getByLabelText('机器'), '本机')
    await user.selectOptions(screen.getByLabelText('CLI'), 'grok')
    expect(screen.queryByLabelText('HOME 档案')).toBeNull()
    await user.selectOptions(screen.getByLabelText('账号'), 'login-grok-mbp')
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('~/.handoff/home/grok-mbp')
    expect(dlg.textContent).toContain('凭据 auth.json')
    expect(dlg.textContent).toContain('这条登录已过期')
  })

  it('无账号 · 主 HOME：下拉末尾固定项，保存回 no_account', async () => {
    const user = userEvent.setup()
    __setAccountsForTest(sampleAccounts())
    vi.mocked(getSquads).mockResolvedValue({
      carriers: [{ name: 'plain', machine: '本机', cli: 'grok', home_dir: '', status: 'online', version: 1, no_account: true }],
      squads: [],
      running: [],
    })
    render(<SchedulingPage />)
    const row = (await screen.findByText('plain')).closest('article')!
    expect(row.textContent).toContain('无账号 · 主 HOME')
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'freek')
    await user.selectOptions(screen.getByLabelText('机器'), '本机')
    await user.selectOptions(screen.getByLabelText('CLI'), 'grok')
    await user.selectOptions(screen.getByLabelText('账号'), 'none')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putCarrier).toHaveBeenCalledWith('freek', 0, expect.objectContaining({ machine: '本机', cli: 'grok', no_account: true }))
    expect(vi.mocked(putCarrier).mock.lastCall?.[2]).not.toHaveProperty('login')
  })

  it('按载体行编辑小队成员政策并保留模型元信息', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValueOnce({
      carriers: [
        { name: 'c1', machine: 'local', cli: 'opencode', home_dir: '/h1', model: '', status: 'online', version: 1 },
        { name: 'c2', machine: 'local', cli: 'opencode', home_dir: '/h2', model: 'flash', status: 'online', version: 1 },
      ],
      squads: [],
      running: [],
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
      carriers: [{ name: 'c1', machine: 'local', cli: 'opencode', home_dir: '/h', model: '', status: 'online', version: 1 }],
      squads: [],
      running: [],
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
    __setAccountsForTest(sampleAccounts())
    vi.mocked(getSquads).mockResolvedValueOnce({ carriers: [], squads: [], running: [] })
    render(<SchedulingPage />)
    await user.click(screen.getByRole('button', { name: '登记载体' }))
    await user.type(screen.getByLabelText('载体名'), 'mbp')
    await user.selectOptions(screen.getByLabelText('机器'), '本机')
    await user.selectOptions(screen.getByLabelText('CLI'), 'opencode')
    await user.selectOptions(screen.getByLabelText('账号'), 'login-og-mini')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putCarrier).toHaveBeenCalledWith('mbp', 0, expect.objectContaining({ machine: '本机', login: 'login-og-mini' }))
    expect(vi.mocked(putCarrier).mock.calls[0]?.[2]).not.toHaveProperty('home_dir')
    expect(vi.mocked(putCarrier).mock.calls[0]?.[2]).not.toHaveProperty('account')
    expect(detectCarrier).toHaveBeenCalledTimes(1)
    expect(vi.mocked(putCarrier).mock.calls[0]?.[2]).not.toHaveProperty('max_concurrency')

    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [{ name: 'exec', role: 'executor', members: [], version: 7 }], running: [] })
    render(<SchedulingPage />)
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(putSquad).toHaveBeenCalledWith('exec', 7, expect.objectContaining({ role: 'executor', members: [] }))
  })

  it('shows an actionable CAS conflict and retains the modal', async () => {
    const user = userEvent.setup()
    vi.mocked(getSquads).mockResolvedValue({ carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', status: 'online', version: 3, no_account: true }], squads: [], running: [] })
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
        carriers: [{ name: 'b334-probe', machine: '本机', cli: 'opencode', home_dir: '', status: 'pending', version: 1 }],
        squads: [],
        running: [],
      })
      .mockResolvedValueOnce({
        carriers: [],
        squads: [],
        running: [],
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
      carriers: [{ name: 'grok', machine: '本机', cli: 'opencode', home_dir: '', status: 'online', version: 2 }],
      squads: [{ name: 'runner', role: 'executor', members: [{ carrier: 'grok' }], version: 1 }],
      running: [],
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
      carriers: [{ name: 'mbp', machine: 'local', cli: 'opencode', home_dir: '/h', status: 'online', version: 3 }],
      squads: [{ name: 'coord', role: 'coordinator', members: [], version: 1 }],
      running: [],
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

  // B464 S6（plan W3）：载体选登录——只列所选机器上与该执行器兼容的登录（同账号同机多 HOME 用
  // 「账号 — HOME」区分，末尾固定「无账号 · 主 HOME」）；PUT 回传 login 或 no_account；
  // 已失效登录暂停派发并给复制登录命令入口，已过期只标红。
  describe('载体选登录（B464 S6）', () => {
    beforeEach(() => {
      __resetAccountsStoreForTest()
      __setAccountsForTest(sampleAccounts())
    })

    it('账号下拉只列所选机器上兼容的登录，末尾固定「无账号 · 主 HOME」', async () => {
      const user = userEvent.setup()
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '登记载体' }))
      const dlg = screen.getByRole('dialog')
      // 新建缺省第一条兼容登录（本机 opencode → login-og-mini），不是「无账号」。
      expect(within(dlg).getByLabelText('账号')).toHaveValue('login-og-mini')
      await user.selectOptions(within(dlg).getByLabelText('机器'), 'linux-01')
      const values = () => [...within(dlg).getByLabelText('账号').querySelectorAll('option')].map((o) => o.value)
      expect(values()).toEqual(['login-og-main', 'login-og-team', 'login-deepseek-key', 'none'])
      await user.selectOptions(within(dlg).getByLabelText('机器'), '本机')
      expect(values()).toEqual(['login-og-mini', 'login-deepseek-key-mini', 'none'])
      await user.selectOptions(within(dlg).getByLabelText('账号'), 'none')
      expect(within(dlg).getByText(/无账号 · 主 HOME/)).toBeVisible()
    })

    it('编辑存量未选载体：显示不可提交占位、只改模型被拦；选择后才发 login 而非 no_account（C1）', async () => {
      const user = userEvent.setup()
      vi.mocked(getSquads).mockResolvedValue({
        carriers: [{ name: 'legacy', machine: 'linux-01', cli: 'opencode', home_dir: '', status: 'online', version: 3 }],
        squads: [], running: [],
      })
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '编辑 legacy' }))
      const dlg = screen.getByRole('dialog')
      expect(within(dlg).getByLabelText('账号')).toHaveValue('')
      expect(within(dlg).getByRole('option', { name: '未选登录（请选择）' })).toBeInTheDocument()
      await user.type(within(dlg).getByLabelText('模型'), 'sonnet')
      await user.click(within(dlg).getByRole('button', { name: '保存' }))
      expect(putCarrier).not.toHaveBeenCalled()
      expect(await within(dlg).findByRole('alert')).toHaveTextContent('请先选择一条登录')
      // 选一条登录后保存：只发 login，绝不因为原来是「未选」而发 no_account。
      await user.selectOptions(within(dlg).getByLabelText('账号'), 'login-og-main')
      await user.click(within(dlg).getByRole('button', { name: '保存' }))
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).toMatchObject({ login: 'login-og-main', model: 'sonnet' })
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).not.toHaveProperty('no_account')
    })

    it('HOME 变化才触发检测：带登录载体改选「无账号 · 主 HOME」→ 检测一次（C1）', async () => {
      const user = userEvent.setup()
      vi.mocked(getSquads).mockResolvedValue({
        carriers: [{ name: 'mimo', machine: 'linux-01', cli: 'opencode', home_dir: '~/.handoff/home/og-main', status: 'online', version: 7, login: 'login-og-main', account_id: 'opencode-go:og-main', account_name: 'og-main' }],
        squads: [], running: [],
      })
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '编辑 mimo' }))
      await user.selectOptions(screen.getByLabelText('账号'), 'none')
      await user.click(screen.getByRole('button', { name: '保存' }))
      expect(detectCarrier).toHaveBeenCalledWith('mimo')
    })

    it.each(['/root/.handoff/home/og-main', '~/.handoff/home/og-main'])(
      '登录未变不触发检测：存量 home_dir=%s 与登录 HOME 写法不同也不误判（m3）',
      async (storedHome) => {
        const user = userEvent.setup()
        vi.mocked(getSquads).mockResolvedValue({
          carriers: [{ name: 'mimo', machine: 'linux-01', cli: 'opencode', home_dir: storedHome, status: 'online', version: 7, login: 'login-og-main', account_id: 'opencode-go:og-main', account_name: 'og-main' }],
          squads: [], running: [],
        })
        render(<SchedulingPage />)
        await user.click(await screen.findByRole('button', { name: '编辑 mimo' }))
        await user.click(screen.getByRole('button', { name: '保存' }))
        expect(vi.mocked(putCarrier).mock.lastCall?.[2]).toMatchObject({ login: 'login-og-main' })
        expect(detectCarrier).not.toHaveBeenCalled()
      })

    it('编辑带登录的载体：预选当前登录，保存回传 login；改选无账号则回 no_account', async () => {
      const user = userEvent.setup()
      vi.mocked(getSquads).mockResolvedValue({
        carriers: [{ name: 'mimo', machine: 'linux-01', cli: 'opencode', home_dir: '', status: 'online', version: 7, login: 'login-og-main', account_id: 'opencode-go:og-main', account_name: 'og-main' }],
        squads: [], running: [],
      })
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '编辑 mimo' }))
      const dlg = screen.getByRole('dialog')
      expect(within(dlg).getByLabelText('账号')).toHaveValue('login-og-main')
      await user.click(within(dlg).getByRole('button', { name: '保存' }))
      expect(putCarrier).toHaveBeenLastCalledWith('mimo', 7, expect.objectContaining({ login: 'login-og-main' }))
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).not.toHaveProperty('account')
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).not.toHaveProperty('home_dir')

      await user.click(await screen.findByRole('button', { name: '编辑 mimo' }))
      await user.selectOptions(within(screen.getByRole('dialog')).getByLabelText('账号'), 'none')
      await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).not.toHaveProperty('login')
      expect(vi.mocked(putCarrier).mock.lastCall?.[2]).toMatchObject({ no_account: true })
    })

    it('所选登录已失效：载体行与编辑对话框红字、给复制登录命令；没有指纹不一致红字', async () => {
      const user = userEvent.setup()
      vi.mocked(getSquads).mockResolvedValue({
        carriers: [{ name: 'dp', machine: 'linux-01', cli: 'codex', home_dir: '', status: 'online', version: 4, login: 'login-codex-spare', account_id: 'codex:codex-spare', account_name: 'codex-spare', login_state: 'gone' }],
        squads: [], running: [],
      })
      render(<SchedulingPage />)
      const row = (await screen.findByText('dp', { selector: 'strong' })).closest('article')!
      expect(within(row).getByText('已失效')).toHaveClass('q-out')
      expect(row.textContent).toContain('暂停派发')
      // dispatchHint 页面层：登录已失效 → 暂停派发并说明恢复条件（M8）
      expect(row.textContent).toContain('所用登录已失效')
      expect(row.textContent).toContain('重新登录后自动恢复')
      expect(within(row).getByRole('button', { name: '复制登录命令' })).toBeVisible()
      expect(row.textContent).not.toContain('凭据指纹与所选账号不一致')
      await user.click(within(row).getByRole('button', { name: '编辑 dp' }))
      const dlg = screen.getByRole('dialog')
      expect(dlg.textContent).toContain('这条登录已失效')
      expect(dlg.textContent).toContain('载体暂停派发')
      expect(dlg.textContent).not.toContain('凭据指纹与所选账号不一致')
    })

    it('空态：该机没有兼容登录时给「新增登录」链接并预选机器与 agent', async () => {
      const user = userEvent.setup()
      __setAccountsForTest([])
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '登记载体' }))
      const dlg = screen.getByRole('dialog')
      // 没有兼容登录才缺省「无账号 · 主 HOME」（原型 settings.html:595）。
      expect(within(dlg).getByLabelText('账号')).toHaveValue('none')
      const link = within(dlg).getByRole('link', { name: '新增登录' })
      expect(link).toHaveAttribute('href', '/accounts?login=new&agent=opencode')
    })

    it('空态链接本机别名时用 discovery.self 的机器名补 machine（M2）', async () => {
      const user = userEvent.setup()
      __setAccountsForTest([], [{ machine: 'linux-01', self: true, at: new Date().toISOString() }])
      render(<SchedulingPage />)
      await user.click(await screen.findByRole('button', { name: '登记载体' }))
      const dlg = screen.getByRole('dialog')
      const link = within(dlg).getByRole('link', { name: '新增登录' })
      expect(link).toHaveAttribute('href', '/accounts?login=new&machine=linux-01&agent=opencode')
      // 空态按 kind 补括注（原型 settings.html:603 kindsOf 拼接）。
      expect(dlg.textContent).toContain('（OpenCode Go / 第三方 API key）')
    })

    it('载体行显示所选账号的当前状态：正常报每周「已用% 倒计时」，问题报状态短语（R8 修 2）', async () => {
      vi.mocked(getSquads).mockResolvedValue({
        carriers: [
          { name: 'mimo', machine: 'linux-01', cli: 'opencode', home_dir: '', status: 'online', version: 7, login: 'login-og-main', account_id: 'opencode-go:og-main', account_name: 'og-main' },
          { name: 'gk', machine: 'macbook-pro', cli: 'grok', home_dir: '', status: 'online', version: 2, login: 'login-grok-mbp', account_id: 'grok:grok-mbp', account_name: 'grok-mbp' },
          { name: 'ag', machine: 'linux-01', cli: 'agy', home_dir: '', status: 'online', version: 3, login: 'login-agy-main', account_id: 'agy:agy-main', account_name: 'agy-main' },
          { name: 'out', machine: 'linux-01', cli: 'opencode', home_dir: '', status: 'online', version: 1, login: 'login-og-team', account_id: 'opencode-go:og-team', account_name: 'og-team' },
        ],
        squads: [], running: [],
      })
      render(<SchedulingPage />)
      const rowOf = async (name: string) => (await screen.findByText(name, { selector: 'strong' })).closest('article')!
      const mimo = await rowOf('mimo')
      expect(mimo.textContent).toMatch(/OpenCode Go.*62%\s*4d 2h/)
      expect(within(mimo).getByText('62%')).toHaveClass('q-warn')
      // 载体行账号名与行内 HOME（I6 补回）
      expect(mimo.textContent).toContain('og-main')
      expect(mimo.textContent).toContain('~/.handoff/home/og-main')
      // 无额度账号的载体行报「无额度」（I6 补回）
      expect((await rowOf('out')).textContent).toContain('无额度')
      const gk = await rowOf('gk')
      expect(gk.textContent).toContain('△ 凭据已过期')
      expect(gk.textContent).not.toContain('grok login')
      expect(gk.textContent).toContain('登录已过期')
      const ag = await rowOf('ag')
      expect(ag.textContent).toContain('48%')
      expect(ag.textContent).not.toContain('35%')
    })
  })
})
