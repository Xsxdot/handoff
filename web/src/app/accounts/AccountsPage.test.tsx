// AccountsPage.test.tsx —— 「◔ 账号」页（B464 S1–S7 形态）与新增登录 / 改名 / 阈值 / 代理 / 删除对话框。
// 边界：数据经共享快照注入；写操作经 accounts / client 的公开 fetch 缝；断言用户可见的六组、
// 登录子表、发现入口、新增登录三步与请求体。不测颜色内部实现，只锁越线状态词与文案。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError, fetchMachines } from '../../api/client'
import {
  checkLoginHome, createLoginHome, deleteAccount, discoverAccounts, probeAccount, putAccount, putLoginProxy,
  type AccountView,
} from '../../api/accounts'
import { AccountsPage } from './AccountsPage'
import { copyToClipboard } from '../lib/clipboard'
import { __resetAccountsStoreForTest, __setAccountsForTest } from './store'
import { sampleAccounts } from './testAccounts'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchMachines: vi.fn() }
})
vi.mock('../../api/accounts', async () => {
  const actual = await vi.importActual<typeof import('../../api/accounts')>('../../api/accounts')
  return {
    ...actual,
    checkLoginHome: vi.fn(),
    createLoginHome: vi.fn(),
    deleteAccount: vi.fn(),
    discoverAccounts: vi.fn(),
    probeAccount: vi.fn(),
    putAccount: vi.fn(),
    putLoginProxy: vi.fn(),
  }
})
vi.mock('../lib/clipboard', () => ({ copyToClipboard: vi.fn(async () => true) }))

const machines = () => vi.mocked(fetchMachines).mockResolvedValue({ machines: [
  { name: '', addr: '', reachable: true, version: 't', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '' },
  { name: 'linux-01', addr: 'x', reachable: true, version: 't', executors: [], default_executor: '', probe_ms: 1, active_tasks: 0, error: '' },
  { name: 'mac-mini', addr: 'y', reachable: true, version: 't', executors: [], default_executor: '', probe_ms: 1, active_tasks: 0, error: '' },
] })

beforeEach(() => {
  __resetAccountsStoreForTest()
  __setAccountsForTest(sampleAccounts(), [
    { machine: 'linux-01', self: true, at: new Date(Date.now() - 2 * 60_000).toISOString() },
    { machine: 'mac-mini', self: false, error: '列不出 ~/.handoff/home：permission denied' },
  ])
  machines()
})
afterEach(() => { vi.clearAllMocks(); vi.useRealTimers() })

const card = (name: string) => screen.getByTestId(`acard-${name}`)
const loginRow = (id: string) => screen.getByTestId(`login-${id}`)
const dialog = () => screen.getByRole('dialog')

describe('账号页（六组与登录子表）', () => {
  it('按六种 kind 分组、组内问题账号排前；登录子表列出机器/HOME/来源/指纹/状态与提示（W1/W3/S3）', () => {
    render(<AccountsPage />)
    expect(screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)).toEqual([
      'OpenCode Go', 'Codex', 'Grok', 'Antigravity', 'Claude', '第三方 API key',
    ])
    const og = screen.getByTestId('kgroup-opencode-go')
    expect([...og.querySelectorAll('[data-testid^="acard-"]')].map((el) => el.getAttribute('data-testid')))
      .toEqual(['acard-og-team', 'acard-og-mini', 'acard-og-main'])
    expect(card('og-team').querySelector('.a-state')?.textContent).toBe('无额度')
    expect(card('og-mini').querySelector('.a-state')?.textContent).toBe('△ 代理连接失败')
    expect(card('og-main').textContent).toContain('dev@example.com')
    expect(card('og-main').textContent).toContain('1 条登录 · linux-01')
    // 窗口名、失败时旧读数提示与完整原因（B453 仍成立断言，I6 补回）
    expect(card('og-main').textContent).toContain('会话 5h')
    expect(card('og-mini').textContent).toContain('显示的是')
    expect(card('grok-mbp').querySelector('.a-r3')?.textContent).toContain('在该机执行 grok login')
    const main = loginRow('login-og-main')
    expect(main.textContent).toContain('linux-01')
    expect(main.textContent).toContain('~/.handoff/home/og-main')
    expect(main.textContent).toContain('auth.json')
    expect(main.textContent).toContain('sha256:000000000000')
    expect(main.textContent).toContain('正常')
    // 登录子表有「载体」列，这一行列出使用它的载体
    expect(main.closest('table')!.querySelector('thead')!.textContent).toContain('载体')
    expect(main.textContent).toContain('mimo')
    // 已过期只提示、已失效给重新登录提示；两者都在登录行里红字标出
    expect(loginRow('login-grok-mbp').textContent).toContain('已过期')
    expect(loginRow('login-grok-mbp').textContent).toContain('CLI 运行时可能自行刷新')
    expect(loginRow('login-codex-spare').textContent).toContain('已失效')
    expect(loginRow('login-codex-spare').textContent).toContain('重新登录后自动恢复')
    // claude / 第三方 key 无额度窗口、无立即探测入口，但有登录子表
    expect(card('claude-main').querySelector('.a-state')?.textContent).toBe('')
    expect(within(card('claude-main')).queryByRole('button', { name: '立即探测' })).toBeNull()
    expect(card('claude-main').textContent).toContain('不支持额度探测')
    expect(loginRow('login-claude-main').textContent).toContain('~（主 HOME）')
    expect(card('deepseek-key').textContent).toContain('deepseek')
    expect(within(card('deepseek-key')).getAllByText(/登录/).length).toBeGreaterThan(0)
  })

  it('点卡头展开详情；折叠时不显示阈值（I6 补回点击展开）', async () => {
    const user = userEvent.setup()
    render(<AccountsPage />)
    expect(card('og-main')).not.toHaveClass('open')
    await user.click(within(card('og-main')).getByText('og-main'))
    expect(card('og-main')).toHaveClass('open')
    expect(card('og-main').textContent).toContain('预警 ≥ 60%')
    expect(card('og-main').textContent).toContain('每 15 分钟')
  })

  it('顶部显示上次发现（取最新）、立即发现逐机报错；页头可开新增登录', async () => {
    const user = userEvent.setup()
    vi.mocked(discoverAccounts).mockResolvedValue({ machines: [
      { machine: 'linux-01', ok: true, at: new Date().toISOString() },
      { machine: 'mac-mini', ok: false, error: '转发到 mac-mini 失败: 连接被拒绝' },
    ] })
    render(<AccountsPage />)
    const note = screen.getByTestId('discovery-note')
    expect(note.textContent).toContain('上次发现')
    expect(note.textContent).toContain('linux-01')
    expect(note.textContent).toContain('permission denied')
    await user.click(screen.getByRole('button', { name: '立即发现' }))
    expect(discoverAccounts).toHaveBeenCalledWith()
    await waitFor(() => expect(screen.getByTestId('discovery-note').textContent).toContain('连接被拒绝'))
    expect(screen.getByRole('button', { name: '＋ 新增登录' })).toBeVisible()
  })

  it('?account= 预先展开并高亮；展开区含阈值/间隔与探测信息', () => {
    render(<AccountsPage highlight="og-main" />)
    expect(card('og-main')).toHaveClass('open', 'flash')
    expect(card('og-main').textContent).toContain('预警 ≥ 60%')
    expect(card('og-main').textContent).toContain('低额度 ≥ 80%')
    expect(card('og-main').textContent).toContain('每 15 分钟')
    expect(screen.getByLabelText('预警线')).toHaveValue(60)
  })
})

describe('账号管理', () => {
  it('改名：卡头铅笔进入输入，Enter 提交 name 并就地更新；服务端 409 原文显示', async () => {
    const user = userEvent.setup()
    const renamed: AccountView = { ...sampleAccounts()[0], name: 'og-renamed', version: 3 }
    vi.mocked(putAccount).mockResolvedValueOnce(renamed)
    render(<AccountsPage />)
    await user.click(within(card('og-main')).getByRole('button', { name: '改名 og-main' }))
    const input = within(card('og-main')).getByLabelText('账号名')
    await user.clear(input)
    await user.type(input, 'og-renamed{Enter}')
    expect(putAccount).toHaveBeenCalledWith('opencode-go:og-main', 2, { name: 'og-renamed' })
    expect(await screen.findByTestId('acard-og-renamed')).toBeInTheDocument()

    vi.mocked(putAccount).mockRejectedValueOnce(new ApiError(409, '账号名字 "og-renamed" 已被账号 x 占用'))
    await user.click(within(card('og-renamed')).getByRole('button', { name: '改名 og-renamed' }))
    const again = within(card('og-renamed')).getByLabelText('账号名')
    await user.clear(again)
    await user.type(again, 'taken{Enter}')
    expect(await within(card('og-renamed')).findByRole('alert')).toHaveTextContent('已被账号 x 占用')
  })

  it('阈值与探测间隔：展开区保存走 CAS；被 400 原文挡住时保留输入', async () => {
    const user = userEvent.setup()
    vi.mocked(putAccount).mockResolvedValueOnce({ ...sampleAccounts()[0], warn_percent: 70, low_percent: 90, interval_minutes: 30, version: 3 })
    render(<AccountsPage highlight="og-main" />)
    const warn = screen.getByLabelText('预警线')
    await user.clear(warn)
    await user.type(warn, '70')
    const low = screen.getByLabelText('低额度线')
    await user.clear(low)
    await user.type(low, '90')
    await user.selectOptions(screen.getByLabelText('探测间隔'), '30')
    await user.click(screen.getByRole('button', { name: '保存阈值' }))
    expect(putAccount).toHaveBeenCalledWith('opencode-go:og-main', 2, { interval_minutes: 30, warn_percent: 70, low_percent: 90 })

    vi.mocked(putAccount).mockRejectedValueOnce(new ApiError(400, '预警线 95 必须小于低额度线 90'))
    await user.click(screen.getByRole('button', { name: '保存阈值' }))
    expect(await within(card('og-main')).findByRole('alert')).toHaveTextContent('预警线 95 必须小于低额度线 90')
  })

  it('登录代理：行内编辑保存走 PUT 并就地回显；打码值原样提交', async () => {
    const user = userEvent.setup()
    const updated = { ...sampleAccounts()[2].logins[0], proxy: 'socks5://u:***@127.0.0.1:7897' }
    vi.mocked(putLoginProxy).mockResolvedValue(updated)
    render(<AccountsPage />)
    const row = loginRow('login-og-mini')
    expect(row.textContent).toContain('socks5://127.0.0.1:7897')
    await user.click(within(row).getByRole('button', { name: '编辑代理 login-og-mini' }))
    const input = within(row).getByLabelText('代理 login-og-mini')
    expect(input).toHaveValue('socks5://127.0.0.1:7897')
    await user.clear(input)
    await user.type(input, 'socks5://u:***@127.0.0.1:7897')
    await user.click(within(row).getByRole('button', { name: '保存代理' }))
    expect(putLoginProxy).toHaveBeenCalledWith('login-og-mini', 'socks5://u:***@127.0.0.1:7897')
    await waitFor(() => expect(loginRow('login-og-mini').textContent).toContain('socks5://u:***@127.0.0.1:7897'))
  })

  it('复制登录命令：用服务端 relogin 文案给反馈；探测只对支持额度探测的账号出现', async () => {
    const user = userEvent.setup()
    vi.mocked(probeAccount).mockResolvedValue({ ...sampleAccounts()[4], state: 'ok', reading: { windows: [{ key: 'weekly', used_percent: 9 }], probed_at: new Date().toISOString() } })
    render(<AccountsPage />)
    await user.click(within(loginRow('login-og-main')).getByRole('button', { name: '复制登录命令' }))
    expect(card('og-main').textContent).toContain('ssh root@linux-01 后执行：HOME=~/.handoff/home/og-main opencode console login')
    expect(within(card('claude-main')).queryByRole('button', { name: '立即探测' })).toBeNull()
    await user.click(within(card('codex-spare')).getByRole('button', { name: '立即探测' }))
    expect(probeAccount).toHaveBeenCalledWith('codex-spare')
    expect(card('codex-spare').textContent).toContain('探测完成')
  })

  it('登录行：有 remote_run 时给「复制远端登录命令」，复制内容是远端变体；没有时不出该按钮', async () => {
    const user = userEvent.setup()
    render(<AccountsPage />)
    await user.click(within(loginRow('login-codex-spare')).getByRole('button', { name: '复制远端登录命令' }))
    expect(copyToClipboard).toHaveBeenLastCalledWith('ssh root@linux-01 后执行：HOME=~/.handoff/home/codex-spare codex login --device-auth')
    expect(card('codex-spare').textContent).toContain('已复制远端登录命令')
    expect(within(loginRow('login-og-main')).queryByRole('button', { name: '复制远端登录命令' })).toBeNull()
  })

  it('立即探测请求失败：显示「探测请求失败：<原因>」（I6 补回）', async () => {
    const user = userEvent.setup()
    vi.mocked(probeAccount).mockRejectedValueOnce(new ApiError(502, '上游超时'))
    render(<AccountsPage />)
    await user.click(within(card('og-main')).getByRole('button', { name: '立即探测' }))
    expect(await within(card('og-main')).findByRole('alert')).toHaveTextContent('探测请求失败：上游超时')
  })

  it('删除：被载体引用时拦住并点名；无引用时确认框写明下次发现会重新出现', async () => {
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
    expect(confirm.textContent).toContain('下次发现会重新认出来')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    expect(deleteAccount).toHaveBeenCalledWith('opencode-go:og-mini', 2)
    await waitFor(() => expect(screen.queryByTestId('acard-og-mini')).toBeNull())
  })

  it('删除时服务端 409 回引用载体（快照过旧）也列出', async () => {
    const user = userEvent.setup()
    vi.mocked(deleteAccount).mockRejectedValue(new ApiError(409, '仍被引用', { error: '仍被引用', carriers: ['mini', 'mini-2'] }))
    render(<AccountsPage />)
    await user.click(within(card('og-mini')).getByRole('button', { name: '删除' }))
    await user.click(within(dialog()).getByRole('button', { name: '删除' }))
    const blocked = await screen.findByRole('dialog', { name: '不能删除 og-mini' })
    expect(blocked.textContent).toContain('mini-2')
  })
})

describe('新增登录（三步）', () => {
  it('三步：选机器与 agent → 建 HOME 给命令 → 检查识别并挂到已有账号', async () => {
    const user = userEvent.setup()
    vi.mocked(createLoginHome).mockResolvedValue({
      home: '~/.handoff/home/cx-abcd',
      command: { ssh: 'ssh root@linux-01', run: 'HOME=/root/.handoff/home/cx-abcd codex login', note: '远端或无浏览器机器用 codex login --device-auth' },
    })
    vi.mocked(checkLoginHome).mockResolvedValue({ logins: [
      { login: 'login-new', account: 'codex:0b1c2d3e4f50', account_name: 'ops@example.com', new_account: false, account_logins: 2 },
    ] })
    render(<AccountsPage />)
    await user.click(screen.getByRole('button', { name: '＋ 新增登录' }))
    expect(dialog()).toHaveAccessibleName('新增登录')
    await user.selectOptions(within(dialog()).getByLabelText('机器'), 'linux-01')
    await user.selectOptions(within(dialog()).getByLabelText('Agent'), 'codex')
    const dir = within(dialog()).getByLabelText('HOME 目录名') as HTMLInputElement
    expect(dir.value).toMatch(/^cx-[a-z0-9]{4}$/)
    await user.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    expect(createLoginHome).toHaveBeenCalledWith('linux-01', 'codex', dir.value)
    expect(await within(dialog()).findByText(/等待登录… 每 5 秒检查一次/)).toBeInTheDocument()
    expect(dialog().textContent).toContain('ssh root@linux-01 后执行：HOME=/root/.handoff/home/cx-abcd codex login')
    expect(dialog().textContent).toContain('关掉也没关系，定时发现会自动挂上')
    await user.click(within(dialog()).getByRole('button', { name: '我已登录，立即检查' }))
    expect(checkLoginHome).toHaveBeenCalledWith('linux-01', '~/.handoff/home/cx-abcd')
    expect(await within(dialog()).findByText(/已挂到账号/)).toBeInTheDocument()
    expect(dialog().textContent).toContain('ops@example.com')
    expect(dialog().textContent).toContain('已有 2 条登录')
    await user.click(within(dialog()).getByRole('button', { name: '完成' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('远端登录勾选框：非本机默认勾选并复制带 ssh 行的 remote_run；本机默认不勾，勾上后展示与复制切换', async () => {
    const user = userEvent.setup()
    const command = (machine: string) => ({
      ssh: `ssh root@${machine}`,
      run: `HOME=/root/.handoff/home/cx-abcd codex login`,
      remote_run: 'HOME=/root/.handoff/home/cx-abcd codex login --device-auth',
    })
    vi.mocked(createLoginHome).mockImplementation(async (machine: string) => ({ home: '~/.handoff/home/cx-abcd', command: command(machine) }))
    render(<AccountsPage />)

    // 本机（discovery self=true 的 linux-01）默认不勾，展示/复制 run。
    await user.click(screen.getByRole('button', { name: '＋ 新增登录' }))
    await user.selectOptions(within(dialog()).getByLabelText('机器'), 'linux-01')
    await user.selectOptions(within(dialog()).getByLabelText('Agent'), 'codex')
    await user.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    const localBox = await within(dialog()).findByLabelText('远端登录（该机没有浏览器）')
    expect(localBox).not.toBeChecked()
    expect(dialog().querySelector('pre')?.textContent).not.toContain('--device-auth')
    await user.click(within(dialog()).getByRole('button', { name: '复制' }))
    expect(copyToClipboard).toHaveBeenLastCalledWith('ssh root@linux-01 后执行：HOME=/root/.handoff/home/cx-abcd codex login')
    await user.click(localBox)
    expect(dialog().querySelector('pre')?.textContent).toContain('--device-auth')
    await user.click(within(dialog()).getByRole('button', { name: '复制' }))
    expect(copyToClipboard).toHaveBeenLastCalledWith('ssh root@linux-01 后执行：HOME=/root/.handoff/home/cx-abcd codex login --device-auth')
    await user.click(within(dialog()).getByRole('button', { name: '关闭' }))

    // 非本机（mac-mini）默认勾选，展示/复制 remote_run。
    await user.click(screen.getByRole('button', { name: '＋ 新增登录' }))
    await user.selectOptions(within(dialog()).getByLabelText('机器'), 'mac-mini')
    await user.selectOptions(within(dialog()).getByLabelText('Agent'), 'codex')
    await user.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    const remoteBox = await within(dialog()).findByLabelText('远端登录（该机没有浏览器）')
    expect(remoteBox).toBeChecked()
    expect(dialog().querySelector('pre')?.textContent).toContain('--device-auth')
    await user.click(within(dialog()).getByRole('button', { name: '复制' }))
    expect(copyToClipboard).toHaveBeenLastCalledWith('ssh root@mac-mini 后执行：HOME=/root/.handoff/home/cx-abcd codex login --device-auth')
  })

  it('新身份识别结果文案是「新账号」；darwin claude 的 400 原文照显', async () => {
    const user = userEvent.setup()
    vi.mocked(createLoginHome).mockRejectedValueOnce(new ApiError(400, 'darwin 上 claude 登录只认主 HOME：请在主 HOME 登录（钥匙串同机共用）'))
    vi.mocked(createLoginHome).mockResolvedValueOnce({ home: '~/.handoff/home/oc-abcd', command: { run: 'HOME=/root/.handoff/home/oc-abcd opencode console login' } })
    vi.mocked(checkLoginHome).mockResolvedValue({ logins: [
      { login: 'login-new', account: 'opencode-go:aaaa', account_name: 'opencode-go-abcd', new_account: true, account_logins: 1 },
    ] })
    render(<AccountsPage />)
    await user.click(screen.getByRole('button', { name: '＋ 新增登录' }))
    await user.selectOptions(within(dialog()).getByLabelText('机器'), 'mac-mini')
    await user.selectOptions(within(dialog()).getByLabelText('Agent'), 'claude')
    await user.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    expect(await within(dialog()).findByRole('alert')).toHaveTextContent('claude 登录只认主 HOME')
    await user.selectOptions(within(dialog()).getByLabelText('Agent'), 'opencode')
    await user.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    await user.click(within(dialog()).getByRole('button', { name: '我已登录，立即检查' }))
    expect(await within(dialog()).findByText(/新账号/)).toBeInTheDocument()
    expect(dialog().textContent).toContain('opencode-go-abcd')
  })

  it('第二步每 5 秒自动检查一次', async () => {
    vi.useFakeTimers()
    vi.mocked(createLoginHome).mockResolvedValue({ home: '~/.handoff/home/cx-abcd', command: { run: 'HOME=/root/.handoff/home/cx-abcd codex login' } })
    vi.mocked(checkLoginHome).mockResolvedValue({ logins: [] })
    render(<AccountsPage />)
    fireEvent.click(screen.getByRole('button', { name: '＋ 新增登录' }))
    fireEvent.change(within(dialog()).getByLabelText('机器'), { target: { value: 'linux-01' } })
    fireEvent.change(within(dialog()).getByLabelText('Agent'), { target: { value: 'codex' } })
    fireEvent.click(within(dialog()).getByRole('button', { name: '建目录并给出登录命令' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(checkLoginHome).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(checkLoginHome).toHaveBeenCalledWith('linux-01', '~/.handoff/home/cx-abcd')
  })

  it('账号卡「在其他机器登录」预选 agent；?login=new 预选机器与 agent', async () => {
    const user = userEvent.setup()
    render(<AccountsPage />)
    await user.click(within(card('codex-plus')).getByRole('button', { name: '在其他机器登录' }))
    expect(dialog()).toHaveAccessibleName('在其他机器登录 · codex-plus')
    expect(within(dialog()).getByLabelText('Agent')).toHaveValue('codex')
    await user.click(within(dialog()).getByRole('button', { name: '取消' }))

    render(<AccountsPage newLogin={{ machine: 'linux-01', agent: 'codex' }} />)
    const dlg = screen.getAllByRole('dialog').at(-1)!
    expect(within(dlg).getByLabelText('机器')).toHaveValue('linux-01')
    expect(within(dlg).getByLabelText('Agent')).toHaveValue('codex')
  })
})
