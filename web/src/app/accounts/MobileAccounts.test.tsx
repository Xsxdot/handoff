// MobileAccounts.test.tsx —— 移动端设置「账号与额度」一节（B453 S10，plan W6）。
// 边界：数据经共享快照注入、立即探测经 probeAccount 缝；从设置中心首页（SettingsHub）真实入口进入。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { probeAccount } from '../../api/accounts'
import { SettingsHub } from '../settings/SettingsHub'
import { __resetAccountsStoreForTest, __setAccountsForTest } from './store'
import { sampleAccounts } from './testAccounts'

vi.mock('../../api/accounts', async () => {
  const actual = await vi.importActual<typeof import('../../api/accounts')>('../../api/accounts')
  return { ...actual, probeAccount: vi.fn() }
})

const hub = () => render(<SettingsHub tree={null} desktopState={null} latest={null} updateAvailable={false} sub={null} onSubChange={() => {}} />)

beforeEach(() => {
  __resetAccountsStoreForTest()
  __setAccountsForTest(sampleAccounts())
})
afterEach(() => vi.clearAllMocks())

describe('移动设置 · 账号与额度', () => {
  it('设置中心首页有「账号与额度」一节：按类型分组、组内问题账号排前、每账号两行（W6）', () => {
    hub()
    const sec = screen.getByTestId('settings-section-accounts')
    expect(sec).toHaveTextContent('账号与额度')
    const list = screen.getByTestId('mobile-accounts')
    expect([...list.querySelectorAll('.m-acct-group')].map((g) => g.textContent)).toEqual(['OpenCode Go', 'Codex', 'Grok', 'Antigravity'])
    const rows = [...list.querySelectorAll('[data-testid^="m-acct-"]')].map((r) => r.getAttribute('data-testid'))
    expect(rows.slice(0, 3)).toEqual(['m-acct-og-team', 'm-acct-og-mini', 'm-acct-og-main'])
    const main = screen.getByTestId('m-acct-og-main')
    expect(main.querySelector('.r2 .q-inline')).not.toBeNull()
    expect(main.textContent).toMatch(/后重置/)
    expect(screen.getByTestId('m-acct-agy-main').querySelectorAll('.qg')).toHaveLength(2)
    expect(screen.getByTestId('m-acct-og-team').textContent).toContain('无额度')
    expect(screen.getByText('登记、编辑和删除账号请到桌面控制台的「账号」页。')).toBeInTheDocument()
    // 只读：没有编辑/删除入口
    expect(within(list).queryByRole('button', { name: /编辑|删除/ })).toBeNull()
  })

  it('点行展开各窗口重置与失败原因，可立即探测', async () => {
    const user = userEvent.setup()
    vi.mocked(probeAccount).mockResolvedValue(sampleAccounts()[2])
    hub()
    const row = screen.getByTestId('m-acct-og-mini')
    expect(row.querySelector('.more')).toBeNull()
    await user.click(within(row).getByRole('button', { name: /og-mini/ }))
    expect(row.querySelector('.q-wins')).not.toBeNull()
    expect(row.textContent).toContain('△ 代理 socks5://127.0.0.1:7897 拒绝连接')
    await user.click(within(row).getByRole('button', { name: '立即探测' }))
    expect(probeAccount).toHaveBeenCalledWith('og-mini')
  })

  it('账本未启用（无快照）时不出这一节', () => {
    __setAccountsForTest(null)
    hub()
    expect(screen.queryByTestId('settings-section-accounts')).toBeNull()
  })
})
