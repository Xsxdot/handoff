// AccountsPage.test.tsx —— 「◔ 账号」页（B453 S8）与登记/编辑/删除对话框（S1，plan W1/W3）。
// 边界：数据经共享快照注入；写操作经 putAccount / deleteAccount / probeAccount 三个 API 缝；
// 机器候选经 fetchMachines。断言用户可见的分组、卡片、对话框缺省值与请求体。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError, fetchMachines } from '../../api/client'
import { deleteAccount, probeAccount, putAccount, type AccountView } from '../../api/accounts'
import { AccountsPage } from './AccountsPage'
import { __resetAccountsStoreForTest, __setAccountsForTest } from './store'
import { sampleAccounts } from './testAccounts'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchMachines: vi.fn() }
})
vi.mock('../../api/accounts', async () => {
  const actual = await vi.importActual<typeof import('../../api/accounts')>('../../api/accounts')
  return { ...actual, putAccount: vi.fn(), deleteAccount: vi.fn(), probeAccount: vi.fn() }
})

beforeEach(() => {
  __resetAccountsStoreForTest()
  __setAccountsForTest(sampleAccounts())
  vi.mocked(fetchMachines).mockResolvedValue({ machines: [
    { name: '', addr: '', reachable: true, version: 't', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '' },
    { name: 'linux-01', addr: 'x', reachable: true, version: 't', executors: [], default_executor: '', probe_ms: 1, active_tasks: 0, error: '' },
  ] })
})
afterEach(() => vi.clearAllMocks())

const card = (name: string) => screen.getByTestId(`acard-${name}`)

describe('账号页', () => {
  it('按类型四组、组内问题账号排前；卡片：状态短语、凭据位置与指纹、窗口横排（W3）', () => {
    render(<AccountsPage />)
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual(['OpenCode Go', 'Codex', 'Grok', 'Antigravity'])
    const og = screen.getByTestId('kgroup-opencode-go')
    expect([...og.querySelectorAll('[data-testid^="acard-"]')].map((el) => el.getAttribute('data-testid'))).toEqual(['acard-og-team', 'acard-og-mini', 'acard-og-main'])
    expect(card('og-team').querySelector('.a-state')?.textContent).toBe('无额度')
    expect(card('og-mini').querySelector('.a-state')?.textContent).toBe('△ 代理连接失败')
    expect(card('grok-mbp').querySelector('.a-state')?.textContent).toBe('△ 凭据已过期')
    // 完整原因只在卡片正文（无读数时第三行）与展开详情里
    expect(card('grok-mbp').querySelector('.a-r3')?.textContent).toContain('在该机执行 grok login')
    expect(card('codex-spare').querySelector('.a-state')?.textContent).toBe('··· 未探测')
    expect(card('og-main').querySelector('.a-state')?.textContent).toBe('')
    expect(card('og-main').textContent).toContain('linux-01 · ~/.handoff/home/og-main › auth.json › opencode-go · sha256:3f9a1b2cc21e')
    expect(card('agy-main').textContent).toContain('agy CLI 自己的登录态')
    expect(card('og-main').textContent).toContain('会话 5h')
    expect(card('og-mini').textContent).toContain('显示的是')
  })

  it('点开卡片展开代理、阈值、探测间隔与引用载体；?account= 预先展开并高亮', async () => {
    const user = userEvent.setup()
    render(<AccountsPage highlight="og-team" />)
    expect(card('og-team')).toHaveClass('open', 'flash')
    expect(within(card('og-team')).getByText('dp')).toBeInTheDocument()
    expect(within(card('og-mini')).queryByText('代理')).toBeNull()
    await user.click(card('og-mini').querySelector('.a-name')!)
    expect(within(card('og-mini')).getByText('socks5://127.0.0.1:7897')).toBeInTheDocument()
    expect(card('og-mini').textContent).toContain('预警 ≥ 60%')
    expect(card('og-mini').textContent).toContain('低额度 ≥ 80%')
    expect(card('og-mini').textContent).toContain('每 15 分钟')
  })

  it('登记：缺省值随 kind 预填；agy 条目置灰「由 agy CLI 管理」；保存发 expect=0 并合入列表（W1）', async () => {
    const user = userEvent.setup()
    const saved: AccountView = { ...sampleAccounts()[4], name: 'cx-new', version: 1 }
    vi.mocked(putAccount).mockResolvedValue(saved)
    render(<AccountsPage />)
    await user.click(within(screen.getByTestId('kgroup-codex')).getByRole('button', { name: '＋ 登记账号' }))
    const dlg = screen.getByRole('dialog', { name: '登记账号 · Codex' })
    expect(within(dlg).getByLabelText('类型')).toHaveValue('codex')
    expect(within(dlg).getByLabelText('凭据条目')).toHaveValue('tokens')
    expect(within(dlg).getByLabelText('预警线')).toHaveValue(60)
    expect(within(dlg).getByLabelText('低额度线')).toHaveValue(80)
    expect(within(dlg).getByLabelText('探测间隔')).toHaveValue('15')
    await user.selectOptions(within(dlg).getByLabelText('类型'), 'agy')
    expect(within(dlg).getByLabelText('凭据条目')).toBeDisabled()
    expect(within(dlg).getByLabelText('凭据条目')).toHaveAttribute('placeholder', '由 agy CLI 管理')
    expect(within(dlg).getByLabelText('凭据条目')).toHaveValue('')
    await user.selectOptions(within(dlg).getByLabelText('类型'), 'grok')
    expect(within(dlg).getByLabelText('凭据条目')).toHaveValue('https://auth.x.ai')
    await user.selectOptions(within(dlg).getByLabelText('类型'), 'codex')
    await user.type(within(dlg).getByLabelText('账号名'), 'cx-new')
    expect(within(dlg).getByLabelText('凭据 HOME')).toHaveValue('~/.handoff/home/cx-new')
    await user.clear(within(dlg).getByLabelText('所在机器'))
    await user.type(within(dlg).getByLabelText('所在机器'), 'linux-01')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    expect(putAccount).toHaveBeenCalledWith('cx-new', 0, {
      kind: 'codex', machine: 'linux-01', home_dir: '~/.handoff/home/cx-new', entry: 'tokens',
      interval_minutes: 15, warn_percent: 60, low_percent: 80,
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(card('cx-new')).toBeInTheDocument()
  })

  it('登记 opencode-go：条目缺省为自动（留空、占位说明两处来源），不填则请求体无 entry；可填 opencode.db 强制登录账号（轮 5）', async () => {
    const user = userEvent.setup()
    vi.mocked(putAccount).mockResolvedValue({ ...sampleAccounts()[0], name: 'og-new', version: 1 })
    vi.mocked(probeAccount).mockResolvedValue({ ...sampleAccounts()[0], name: 'og-new', version: 1 })
    render(<AccountsPage />)
    await user.click(within(screen.getByTestId('kgroup-opencode-go')).getByRole('button', { name: '＋ 登记账号' }))
    const dlg = screen.getByRole('dialog', { name: '登记账号 · OpenCode Go' })
    const entry = within(dlg).getByLabelText('凭据条目')
    expect(entry).toHaveValue('')
    expect(entry).toBeEnabled()
    expect(entry).toHaveAttribute('placeholder', '自动：auth.json › opencode-go，否则 opencode.db 登录账号')
    await user.type(within(dlg).getByLabelText('账号名'), 'og-new')
    await user.type(within(dlg).getByLabelText('所在机器'), 'linux-01')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    expect(vi.mocked(putAccount).mock.lastCall?.[2]).not.toHaveProperty('entry')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(within(screen.getByTestId('kgroup-opencode-go')).getByRole('button', { name: '＋ 登记账号' }))
    const dlg2 = screen.getByRole('dialog', { name: '登记账号 · OpenCode Go' })
    await user.type(within(dlg2).getByLabelText('账号名'), 'og-db')
    await user.type(within(dlg2).getByLabelText('所在机器'), 'linux-01')
    await user.type(within(dlg2).getByLabelText('凭据条目'), 'opencode.db')
    await user.click(within(dlg2).getByRole('button', { name: '保存' }))
    expect(vi.mocked(putAccount).mock.lastCall?.[2]).toEqual(expect.objectContaining({ kind: 'opencode-go', entry: 'opencode.db' }))
  })

  it('编辑：名称只读，带当前版本；代理回显打码，原样提交即保留；服务端 400 原文显示', async () => {
    const user = userEvent.setup()
    const masked: AccountView = { ...sampleAccounts()[3], logins: [{ ...sampleAccounts()[3].logins[0], proxy: 'socks5://u:***@127.0.0.1:7897' }] }
    __setAccountsForTest([masked])
    vi.mocked(putAccount).mockRejectedValueOnce(new ApiError(400, '账号登记校验失败：预警线 70 必须小于低额度线 60'))
    render(<AccountsPage />)
    await user.click(within(card('codex-plus')).getByRole('button', { name: '编辑' }))
    const dlg = screen.getByRole('dialog', { name: '编辑账号 · codex-plus' })
    expect(within(dlg).getByLabelText('账号名')).toHaveAttribute('readonly')
    expect(within(dlg).getByLabelText('代理')).toHaveValue('socks5://u:***@127.0.0.1:7897')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    expect(putAccount).toHaveBeenCalledWith('codex-plus', 2, expect.objectContaining({ proxy: 'socks5://u:***@127.0.0.1:7897', kind: 'codex', machine: 'macbook-pro' }))
    expect(await within(dlg).findByRole('alert')).toHaveTextContent('预警线 70 必须小于低额度线 60')
  })

  it('保存后自动探测一次：新建触发、只改间隔不触发、改 HOME 触发；探测失败只显示在卡片上（审查 m2；触发项全表见 model.test 的 credentialMoved）', async () => {
    const user = userEvent.setup()
    const saved: AccountView = { ...sampleAccounts()[4], name: 'cx-new', version: 1 }
    vi.mocked(putAccount).mockResolvedValue(saved)
    vi.mocked(probeAccount).mockRejectedValueOnce(new ApiError(502, '转发到 linux-01 失败: 连接被拒绝'))
    render(<AccountsPage />)
    await user.click(within(screen.getByTestId('kgroup-codex')).getByRole('button', { name: '＋ 登记账号' }))
    const dlg = screen.getByRole('dialog', { name: '登记账号 · Codex' })
    await user.type(within(dlg).getByLabelText('账号名'), 'cx-new')
    await user.type(within(dlg).getByLabelText('所在机器'), 'linux-01')
    await user.click(within(dlg).getByRole('button', { name: '保存' }))
    // 保存不被探测拦住：对话框照关，探测失败写在卡片上
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(probeAccount).toHaveBeenCalledWith('cx-new'))
    expect(await within(card('cx-new')).findByText(/探测请求失败：.*连接被拒绝/)).toBeInTheDocument()

    // 编辑只改探测间隔：不触发探测
    vi.mocked(probeAccount).mockClear()
    vi.mocked(putAccount).mockResolvedValue({ ...sampleAccounts()[3], interval_minutes: 30, version: 3 })
    await user.click(within(card('codex-plus')).getByRole('button', { name: '编辑' }))
    await user.selectOptions(within(screen.getByRole('dialog')).getByLabelText('探测间隔'), '30')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(probeAccount).not.toHaveBeenCalled()

    // 编辑改了 HOME：触发探测，成功就地刷新
    vi.mocked(putAccount).mockResolvedValue({ ...sampleAccounts()[3], logins: [{ ...sampleAccounts()[3].logins[0], home: '~/.handoff/home/cx2' }], version: 4 })
    vi.mocked(probeAccount).mockResolvedValue({ ...sampleAccounts()[3], logins: [{ ...sampleAccounts()[3].logins[0], home: '~/.handoff/home/cx2' }], version: 4 })
    await user.click(within(card('codex-plus')).getByRole('button', { name: '编辑' }))
    const home = within(screen.getByRole('dialog')).getByLabelText('凭据 HOME')
    await user.clear(home)
    await user.type(home, '~/.handoff/home/cx2')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(probeAccount).toHaveBeenCalledWith('codex-plus'))
    expect(await within(card('codex-plus')).findByText('探测完成，已写回账本。')).toBeInTheDocument()
  })

  it('删除：被载体引用时拦住并列出载体，不发请求；无引用确认后删除', async () => {
    const user = userEvent.setup()
    vi.mocked(deleteAccount).mockResolvedValue({ name: 'og-mini', version: 2 })
    render(<AccountsPage />)
    await user.click(within(card('og-team')).getByRole('button', { name: '删除' }))
    const blocked = screen.getByRole('dialog', { name: '不能删除 og-team' })
    expect(blocked.textContent).toContain('dp')
    await user.click(within(blocked).getByRole('button', { name: '关闭' }))
    expect(deleteAccount).not.toHaveBeenCalled()
    await user.click(within(card('og-mini')).getByRole('button', { name: '删除' }))
    const confirm = screen.getByRole('dialog', { name: '删除账号 og-mini？' })
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    expect(deleteAccount).toHaveBeenCalledWith('og-mini', 2)
    await waitFor(() => expect(screen.queryByTestId('acard-og-mini')).toBeNull())
  })

  it('删除时服务端 409 回引用载体（快照过旧）也列出', async () => {
    const user = userEvent.setup()
    vi.mocked(deleteAccount).mockRejectedValue(new ApiError(409, '仍被引用', { error: '仍被引用', carriers: ['mini', 'mini-2'] }))
    render(<AccountsPage />)
    await user.click(within(card('og-mini')).getByRole('button', { name: '删除' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除' }))
    const blocked = await screen.findByRole('dialog', { name: '不能删除 og-mini' })
    expect(blocked.textContent).toContain('mini-2')
  })

  it('立即探测：所在机执行，结果就地刷新卡片', async () => {
    const user = userEvent.setup()
    vi.mocked(probeAccount).mockResolvedValue({ ...sampleAccounts()[4], state: 'ok', reading: { windows: [{ key: 'weekly', used_percent: 9 }], probed_at: new Date().toISOString() } })
    render(<AccountsPage />)
    await user.click(within(card('codex-spare')).getByRole('button', { name: '立即探测' }))
    expect(probeAccount).toHaveBeenCalledWith('codex-spare')
    expect(await within(card('codex-spare')).findByText('探测完成，已写回账本。')).toBeInTheDocument()
    expect(card('codex-spare').textContent).toContain('9%')
  })
})
