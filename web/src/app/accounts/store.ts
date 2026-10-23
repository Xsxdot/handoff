// store.ts —— 账号与额度的 Shell 级唯一数据源（B453 plan Q17）。
//
// 职责：
//   - Shell 调一次 useAccountsSource(enabled)：usePoll 每 30s 拉 GET /api/accounts，结果写进
//     模块级快照；页面隐藏停表、断线保留旧数据、401 终止，全部沿用 usePoll 的纪律
//   - 底栏、弹层、账号页、移动设置节、载体对话框用 useAccounts() 读同一份快照
//   - 立即探测 / 登记 / 删除的回执就地合入快照（不等下一轮轮询，也不另起全量 GET）
//
// 为什么是模块级单例 + 订阅而不是 Context：消费点散在 Shell 的多棵子树（底栏在壳底、
// 账号页在整页路由、移动设置节在 mobile-home 覆盖层、载体对话框在设置页），与
// useWebPrefs 同一条取舍——套 Provider 要动 Shell 的 JSX 结构，收益不抵改动面。
//
// 边界：没有数据源挂载时（单测或账本未启用）消费点读到 accounts=null，不自起轮询——
// 一份数据只有一个拉取者，多个拉取者会让同一账号在不同面上读出不同时刻的读数。
import { useCallback, useEffect, useState } from 'react'
import { getAccounts, probeAccount, type AccountView, type DiscoveryView, type LoginView } from '../../api/accounts'
import { usePoll } from '../data/usePoll'

/** 轮询间隔（plan Q17：15–60s 内由执行者定）。读数本身每 5–60 分钟才变，30s 足够跟上立即探测之外的变化。 */
export const ACCOUNTS_POLL_MS = 30_000

export interface AccountsSnapshot {
  /** null = 还没有任何一次成功读（或没有数据源）；[] = 已读到、空库。 */
  accounts: AccountView[] | null
  /** 各机「上次发现」；随 GET /api/accounts 一起回来，账号页页头读它。 */
  discovery: DiscoveryView[]
  /** 最近一次拉取失败的原因；成功后清空。断线时 accounts 保留旧值。 */
  errorText: string
  disconnected: boolean
}

let snapshot: AccountsSnapshot = { accounts: null, discovery: [], errorText: '', disconnected: false }
let refreshFn: () => void = () => {}
const subscribers = new Set<(s: AccountsSnapshot) => void>()

function publish(next: AccountsSnapshot) {
  snapshot = next
  for (const notify of subscribers) notify(next)
}

/** mergeAccount 把一条新回执合入快照：同名替换、新名追加（登记），保持原序。 */
function mergeAccount(view: AccountView) {
  const list = snapshot.accounts ?? []
  const idx = list.findIndex((a) => a.name === view.name)
  const next = idx >= 0 ? list.map((a, i) => (i === idx ? view : a)) : [...list, view]
  publish({ ...snapshot, accounts: next })
}

/**
 * useAccountsSource 是唯一拉取者，只在 Shell 挂一次。
 * 参数 enabled：账本未启用（账号域未装配，API 会 503）时传 false，不轮询。
 */
export function useAccountsSource(enabled: boolean): void {
  const poll = usePoll((signal) => getAccounts(signal), ACCOUNTS_POLL_MS, { enabled, timeoutMs: 20_000 })
  useEffect(() => {
    refreshFn = poll.refresh
  }, [poll.refresh])
  useEffect(() => {
    if (poll.data !== null) {
      console.debug('accounts.poll.success', { accounts: poll.data.accounts.length })
      publish({ accounts: poll.data.accounts, discovery: poll.data.discovery, errorText: '', disconnected: false })
    }
  }, [poll.data])
  useEffect(() => {
    if (poll.disconnected) {
      console.warn('accounts.poll.failure', { error: poll.errorText })
      publish({ ...snapshot, errorText: poll.errorText, disconnected: true })
    }
  }, [poll.disconnected, poll.errorText])
}

export interface AccountsApi extends AccountsSnapshot {
  /** 立即重拉一次全量（底栏刷新按钮之后、删除之后用）。 */
  refresh: () => void
  /** 立即探测一个账号（所在机执行），新读数就地合入并返回；失败抛 ApiError（原文透传）。 */
  probe: (name: string) => Promise<AccountView>
  /** 登记/编辑回执（PUT 返回的 AccountView）合入。 */
  apply: (view: AccountView) => void
  /** 单条登录的回执（PUT 代理）合入它所属的账号。 */
  applyLogin: (login: LoginView) => void
  /** 删除成功后把它从快照里拿掉。 */
  remove: (name: string) => void
}

/** useAccounts 读共享快照；任意多个挂载点拿到同一份。 */
export function useAccounts(): AccountsApi {
  const [state, setState] = useState<AccountsSnapshot>(snapshot)
  useEffect(() => {
    subscribers.add(setState)
    setState(snapshot) // 订阅建立前可能已有一次发布，补齐
    return () => { subscribers.delete(setState) }
  }, [])
  const probe = useCallback(async (name: string) => {
    const started = performance.now()
    console.info('accounts.probe.start', { name })
    try {
      const view = await probeAccount(name)
      console.info('accounts.probe.success', { name, state: view.state, elapsed: performance.now() - started })
      mergeAccount(view)
      return view
    } catch (cause) {
      console.error('accounts.probe.failure', { name, elapsed: performance.now() - started, cause })
      throw cause
    }
  }, [])
  const refresh = useCallback(() => refreshFn(), [])
  const apply = useCallback((view: AccountView) => mergeAccount(view), [])
  const applyLogin = useCallback((login: LoginView) => {
    const owner = (snapshot.accounts ?? []).find((a) => a.logins.some((l) => l.id === login.id))
    if (owner) mergeAccount({ ...owner, logins: owner.logins.map((l) => (l.id === login.id ? login : l)) })
  }, [])
  const remove = useCallback((name: string) => {
    publish({ ...snapshot, accounts: (snapshot.accounts ?? []).filter((a) => a.name !== name) })
  }, [])
  return { ...state, refresh, probe, apply, applyLogin, remove }
}

/** useNow 每 intervalMs 刷新一次的「现在」（倒计时文案用；与 orca 一样 30s 一跳）。 */
export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs)
    return () => window.clearInterval(timer)
  }, [intervalMs])
  return now
}

/** __setAccountsForTest 直接发布一份快照；只供组件测试，生产代码不得调用。 */
export function __setAccountsForTest(accounts: AccountView[] | null, discovery: DiscoveryView[] = []): void {
  publish({ accounts, discovery, errorText: '', disconnected: false })
}

/** __resetAccountsStoreForTest 重置模块级状态；只供测试隔离。 */
export function __resetAccountsStoreForTest(): void {
  snapshot = { accounts: null, discovery: [], errorText: '', disconnected: false }
  refreshFn = () => {}
  subscribers.clear()
}
