// StatusBar.test.tsx —— 额度底栏与「使用量」弹层（B453 S9，plan W4/W5）的组件接缝。
// 边界：数据经共享快照（store）注入、立即探测经 probeAccount 缝；断言用户可见的文字、
// 顺序与色彩类落点。溢出收纳「+N」依赖真实布局宽度，jsdom 量不出，交浏览器形态验收（R8）。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { probeAccount } from '../../api/accounts'
import { __resetWebPrefsForTest } from '../settings/useWebPrefs'
import { PREFS_KEY } from '../settings/webPrefs'
import { __resetAccountsStoreForTest, __setAccountsForTest } from './store'
import { UsageStatusBar } from './StatusBar'
import { ignoredAccount, sampleAccounts } from './testAccounts'

vi.mock('../../api/accounts', async () => {
  const actual = await vi.importActual<typeof import('../../api/accounts')>('../../api/accounts')
  return { ...actual, probeAccount: vi.fn() }
})

beforeEach(() => {
  localStorage.clear()
  __resetWebPrefsForTest()
  __resetAccountsStoreForTest()
  __setAccountsForTest(sampleAccounts())
})
afterEach(() => vi.clearAllMocks())

const segNames = () => screen.getAllByTestId(/^sb-seg-/).map((el) => el.dataset.testid!.replace('sb-seg-', ''))
const seg = (name: string) => screen.getByTestId(`sb-seg-${name}`)

describe('额度底栏', () => {
  it('问题账号排前，底栏不写「低额度」「无额度」字样，只有细条与数值（W4）', () => {
    render(<UsageStatusBar onManage={() => {}} />)
    expect(screen.getByText('已用')).toBeInTheDocument()
    expect(segNames()).toEqual(['og-team', 'codex-plus', 'og-mini', 'grok-mbp', 'codex-spare', 'og-main', 'agy-main'])
    const bar = screen.getByTestId('usage-statusbar')
    expect(bar.textContent).not.toMatch(/低额度|无额度/)
    // 详细：最短两个窗口的「已用% 倒计时」+ 一根短细条
    expect(seg('og-main').textContent).toMatch(/34%\s*2h4\dm.*62%\s*4d 2h/)
    expect(seg('og-main').querySelectorAll('.qbar')).toHaveLength(1)
    // 探测失败有旧读数：数值降弱色 + △；无读数：△ 短因；未探测
    expect(seg('og-mini').querySelector('.q-cached')).not.toBeNull()
    expect(seg('og-mini').textContent).toContain('△')
    // 短因按失败类别映射（R8 修 1）：底栏段不塞整句 message，整句只在 title 与二级面板
    expect(seg('grok-mbp').textContent).toContain('△ 凭据已过期')
    expect(seg('grok-mbp').textContent).not.toContain('grok login')
    expect(seg('codex-spare').textContent).toContain('··· 未探测')
  })

  it('色彩纪律：彩色类只出现在越线数字与细条上（W5）', () => {
    render(<UsageStatusBar onManage={() => {}} />)
    const colored = [...screen.getByTestId('usage-statusbar').querySelectorAll('.q-warn, .q-out')]
    expect(colored.length).toBeGreaterThan(0)
    for (const el of colored) {
      const isBar = el.classList.contains('qbar')
      expect(isBar || /^\d+%$/.test(el.textContent ?? ''), `彩色类落在了「${el.textContent}」上`).toBe(true)
    }
    expect(seg('og-team').querySelector('.q-pct.q-out')?.textContent).toBe('100%')
    expect(seg('og-main').querySelector('.q-pct.q-warn')?.textContent).toBe('62%')
  })

  it('弹层：详细两行、按类型分组；切精简后底栏随模式变且记住选择（W4）', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<UsageStatusBar onManage={() => {}} />)
    await user.click(seg('og-main'))
    const pop = screen.getByRole('dialog', { name: '使用量' })
    expect([...pop.querySelectorAll('.up-group')].map((g) => g.textContent)).toEqual(['OpenCode Go', 'Codex', 'Grok', 'Antigravity'])
    const row = within(pop).getByTestId('up-row-og-team')
    expect(row.textContent).toMatch(/无额度 · \d+h\d+m 后恢复/)
    expect(within(pop).getByTestId('up-row-agy-main').querySelectorAll('.qg')).toHaveLength(2)
    expect(within(pop).getByTestId('up-row-codex-plus').textContent).toContain('低额度 · 会话 82%')
    expect(within(pop).getByTestId('up-row-grok-mbp').textContent).toContain('△ 凭据已过期')
    expect(within(pop).getByTestId('up-row-grok-mbp').textContent).not.toContain('grok login')
    expect(within(pop).getByTestId('up-row-og-mini').textContent).toContain('△ 代理连接失败')

    await user.click(within(pop).getByRole('tab', { name: '精简' }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).usageMode).toBe('compact')
    // 精简：底栏只报每周；低额度账号改报触发窗口（会话 82%），不让每周 41% 掩盖警示
    expect(seg('og-main').textContent).toMatch(/62%\s*4d 2h/)
    expect(seg('og-main').textContent).not.toContain('34%')
    expect(seg('codex-plus').textContent).toContain('82%')
    expect(seg('codex-plus').textContent).not.toContain('41%')
    expect(within(pop).getByTestId('up-row-og-main').querySelector('.up-r2')).toBeNull()
    // 多组 kind（agy）精简取各组每周里已用最高的（Claude/GPT 周 48%），不只报 Gemini 周（R8 修 3）
    expect(seg('agy-main').textContent).toContain('48%')
    expect(seg('agy-main').textContent).not.toContain('35%')
    expect(within(pop).getByTestId('up-row-agy-main').textContent).toContain('48%')
    expect(within(pop).getByTestId('up-row-agy-main').textContent).not.toContain('35%')
    unmount()
    __resetWebPrefsForTest()
    render(<UsageStatusBar onManage={() => {}} />)
    expect(seg('og-main').textContent).not.toContain('34%')
  })

  it('二级面板：聚焦/点击行看全部窗口、载体与立即探测；探测结果就地刷新；管理账号回调', async () => {
    const user = userEvent.setup()
    const onManage = vi.fn()
    const probed = { ...sampleAccounts()[4], state: 'ok', reading: { windows: [{ key: 'weekly', used_percent: 7 }], probed_at: new Date().toISOString() } }
    vi.mocked(probeAccount).mockResolvedValue(probed)
    render(<UsageStatusBar onManage={onManage} />)
    await user.click(screen.getByText('已用'))
    const pop = screen.getByRole('dialog', { name: '使用量' })
    await user.click(within(pop).getByTestId('up-row-codex-plus'))
    const side = screen.getByTestId('usage-side')
    expect(side.textContent).toContain('codex-plus')
    expect(side.textContent).toContain('会话 5h')
    expect(side.textContent).toContain('每周')
    expect(side.textContent).toContain('mbp-codex')
    expect(side.textContent).toContain('macbook-pro')
    // 完整失败原因只在二级面板
    act(() => { within(pop).getByTestId('up-row-grok-mbp').focus() })
    expect(screen.getByTestId('usage-side').textContent).toContain('在该机执行 grok login')
    act(() => { within(pop).getByTestId('up-row-codex-spare').focus() })
    expect(screen.getByTestId('usage-side').textContent).toContain('尚未探测')
    await user.click(within(screen.getByTestId('usage-side')).getByRole('button', { name: '立即探测' }))
    expect(probeAccount).toHaveBeenCalledWith('codex-spare')
    expect(await within(pop).findByTestId('up-row-codex-spare')).toHaveTextContent('7%')
    await user.click(within(screen.getByTestId('usage-side')).getByRole('button', { name: /管理账号/ }))
    expect(onManage).toHaveBeenCalledWith('codex-spare')
  })

  it('已忽略账号不进底栏、弹层与「刷新所有账号」', async () => {
    const user = userEvent.setup()
    __setAccountsForTest([...sampleAccounts(), ignoredAccount()])
    vi.mocked(probeAccount).mockResolvedValue(ignoredAccount())
    render(<UsageStatusBar onManage={() => {}} />)
    expect(segNames()).not.toContain('og-hidden')
    await user.click(screen.getByText('已用'))
    const pop = screen.getByRole('dialog', { name: '使用量' })
    expect(within(pop).queryByTestId('up-row-og-hidden')).toBeNull()
    await user.click(within(pop).getByRole('button', { name: /所有账号/ }))
    expect(probeAccount).not.toHaveBeenCalledWith('og-hidden')
  })

  it('底栏段只有图标与读数、名字在 title；弹层菜单只剩「管理账号…」（S8/S9）', async () => {
    const user = userEvent.setup()
    const onManage = vi.fn()
    render(<UsageStatusBar onManage={onManage} />)
    const s = seg('og-main')
    expect(s.textContent).not.toContain('og-main')
    expect(s).toHaveAttribute('title', 'og-main')
    expect(s.querySelector('.sb-name')).toBeNull()
    await user.click(screen.getByText('已用'))
    const pop = screen.getByRole('dialog', { name: '使用量' })
    expect(within(pop).queryByText('使用详情')).toBeNull()
    const manage = within(pop).getByRole('button', { name: /管理账号/ })
    expect(manage).toBeVisible()
    await user.click(manage)
    expect(onManage).toHaveBeenCalled()
  })

  it('还没有快照（账本未启用/首拉未回）时不渲染', () => {
    __setAccountsForTest(null)
    render(<UsageStatusBar onManage={() => {}} />)
    expect(screen.queryByTestId('usage-statusbar')).toBeNull()
  })
})
