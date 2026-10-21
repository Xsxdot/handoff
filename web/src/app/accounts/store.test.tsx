// store.test.tsx —— 账号 Shell 级唯一数据源（plan Q17）的接缝测试。
// 边界：经 getAccounts / probeAccount 两个 API 缝进入；断言多个消费点读同一份快照、
// 立即探测返回的新读数就地合入（不另起一次全量 GET）。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import { getAccounts, probeAccount, type AccountView } from '../../api/accounts'
import { __resetAccountsStoreForTest, useAccounts, useAccountsSource } from './store'

vi.mock('../../api/accounts', async () => {
  const actual = await vi.importActual<typeof import('../../api/accounts')>('../../api/accounts')
  return { ...actual, getAccounts: vi.fn(), probeAccount: vi.fn() }
})

const view = (name: string, state: string): AccountView => ({
  name, kind: 'codex', machine: 'm', home_dir: '~/h', interval_minutes: 15, warn_percent: 60, low_percent: 80,
  version: 1, state, stale: false, carriers: [],
})

function Source() { useAccountsSource(true); return null }
function Reader({ id }: { id: string }) {
  const { accounts } = useAccounts()
  return <p data-testid={id}>{accounts === null ? 'loading' : accounts.map((a) => `${a.name}:${a.state}`).join(',')}</p>
}
let probeFn: ((name: string) => Promise<AccountView>) | null = null
function Prober() { probeFn = useAccounts().probe; return null }

beforeEach(() => {
  __resetAccountsStoreForTest()
  vi.mocked(getAccounts).mockResolvedValue({ accounts: [view('a', 'none'), view('b', 'ok')] })
})
afterEach(() => { vi.clearAllMocks(); probeFn = null })

describe('账号数据源', () => {
  it('Shell 一处拉取，多个消费点读同一份快照', async () => {
    render(<><Source /><Reader id="r1" /><Reader id="r2" /></>)
    await waitFor(() => expect(screen.getByTestId('r1')).toHaveTextContent('a:none,b:ok'))
    expect(screen.getByTestId('r2')).toHaveTextContent('a:none,b:ok')
    expect(getAccounts).toHaveBeenCalledTimes(1)
  })

  it('立即探测的新读数就地合入，不另起全量拉取', async () => {
    vi.mocked(probeAccount).mockResolvedValue(view('a', 'low'))
    render(<><Source /><Reader id="r1" /><Prober /></>)
    await waitFor(() => expect(screen.getByTestId('r1')).toHaveTextContent('a:none,b:ok'))
    await act(async () => { await probeFn!('a') })
    expect(probeAccount).toHaveBeenCalledWith('a')
    expect(screen.getByTestId('r1')).toHaveTextContent('a:low,b:ok')
    expect(getAccounts).toHaveBeenCalledTimes(1)
  })

  it('没有数据源时消费点停在 loading（不自起轮询）', () => {
    render(<Reader id="r1" />)
    expect(screen.getByTestId('r1')).toHaveTextContent('loading')
    expect(getAccounts).not.toHaveBeenCalled()
  })
})
