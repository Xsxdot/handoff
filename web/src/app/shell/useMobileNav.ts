// useMobileNav —— 紧凑视口导航的统一模型（B369.7）：URL 是唯一事实源。
//
// 职责：把 Shell 原先三个互不同步的 useState（mobileTab / mobileDetail / fileDrawer）
// 收编进一个 hook——任何导航动作 = 写 URL，任何 UI 呈现 = 读 URL。刷新、前进后退、
// 深链直达都自然落在同一状态上，不再有「URL 变了没人消费」的死状态。
//
// 边界：
//   - 桌面（compact=false）直通：三个状态走 hook 内部 useState（初值同收编前），
//     所有 setter 只写 state、URL 一字不动——「桌面路由行为不回归」由既有测试
//     直接锁住（spec §2.5）。hooks 规则上无条件调用，分支在值层不在 hook 层。
//   - URL 语汇（双宿主，plan §2 岔口 1）：
//       /cards（pathname 本身即「卡 tab」的表达）：card=<id>、project=<name>、
//         from=session-<id>|card-<id>（来源，任务现场返回与 ↗ 语境消费）
//       /：tab ∈ sessions|cards|projects|settings（缺省 sessions）、detail=1（下钻态）、
//         dir=<baseKey>（目录覆盖层）、from=…（任务现场返回来源，仅 detail=1 时
//         有消费者——返回条；/cards 上的 from 由任务跳转 seam 消费）
//     派生规则：pathname==='/cards' ⇒ tab='cards'；否则读 tab 参数。任务现场终态
//     形如 /?tab=<tab>&detail=1&from=<来源>；「/ 上 tab=cards 且无 detail」是残留
//     形状，由 normalize ② replace 成 /cards，不留第二形状。
//   - push/replace 纪律（plan §3.1）：前进类（setTab/openCard/enterDetail/setDir 开）
//     push；返回/取消类（exitDetail/closeCard/setDir 关）与瞬态跳板（/tasks/:id）
//     、normalize 改写一律 replace，不留历史格。
//   - 埋点统一 console.debug('shell.mobile_nav.*', …)，替换既有
//     shell.mobile_tab.select / shell.mobile_detail.back（无测试断言旧事件名）。
//   - 不取数、不渲染：fileDrawer 的 BaseDir 反解（findBaseByKey）归 Shell——
//     它持有树流；本 hook 只给 dirKey。
import { useCallback, useMemo, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import type { MobileTab } from './MobileTabBar'

// MobileFrom 是「来源」的受控词表：会话来源（session-<id>）或卡来源（card-<id>）。
// 任务现场返回条与卡详情 ↗ 的 from 语境都只允许这两种前缀，其他值按无来源处理。
export type MobileFrom = `session-${string}` | `card-${string}`

const MOBILE_TABS: readonly string[] = ['sessions', 'cards', 'projects', 'settings']

function tabOfParams(params: URLSearchParams): MobileTab {
  const raw = params.get('tab')
  return raw !== null && MOBILE_TABS.includes(raw) ? (raw as MobileTab) : 'sessions'
}

function isMobileFrom(raw: string | null): raw is MobileFrom {
  return raw !== null && (/^session-.+/.test(raw) || /^card-.+/.test(raw))
}

export interface MobileNav {
  // 读（compact 下从 URL 派生；桌面下从内部 state 派生——同一 API 两种存储）
  tab: MobileTab
  detail: boolean
  dirKey: string | null
  from: MobileFrom | null
  // 写（compact 下写 URL；桌面下写内部 state、URL 一字不动）
  setTab(t: MobileTab): void
  // ctx 值放宽为 string|null：调用方（TaskDeepLink/openTaskTui）从 URL 参数原样
  // 透传，非法值按「未指定」处理——from 未指定时保留当前 URL 的 from（同一任务
  // 现场里经看板换任务不丢来源链），tab 未指定时用派生 tab。
  enterDetail(ctx?: { from?: string | null; tab?: string | null }): void
  exitDetail(): void
  setDir(key: string | null): void
  openCard(cardId: string, from?: MobileFrom): void
  closeCard(): void
  normalize(gate: { ledgerEnabled: boolean; ledgerLoading: boolean }): void
}

export function useMobileNav({ compact }: { compact: boolean }): MobileNav {
  const location = useLocation()
  const navigate = useNavigate()
  // 桌面直通 state：初值同收编前的三个 useState（sessions / false / null）。
  const [desktopTab, setDesktopTab] = useState<MobileTab>('sessions')
  const [desktopDetail, setDesktopDetail] = useState(false)
  const [desktopDirKey, setDesktopDirKey] = useState<string | null>(null)

  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const derivedTab: MobileTab = location.pathname === '/cards' ? 'cards' : tabOfParams(params)
  const tab = compact ? derivedTab : desktopTab
  const detail = compact ? params.get('detail') === '1' : desktopDetail
  const dirKey = compact ? params.get('dir') : desktopDirKey
  const rawFrom = params.get('from')
  const from = compact && isMobileFrom(rawFrom) ? rawFrom : null

  // here 是当前完整 URL，所有写动作先比对它再 navigate（幂等写，plan §7.2）：
  // 同址不写，避免往历史里塞无意义的同址条目。
  const here = `${location.pathname}${location.search}`

  const setTab = useCallback((t: MobileTab) => {
    if (!compact) {
      setDesktopTab(t)
      return
    }
    const to = t === 'cards' ? '/cards' : `/?tab=${t}`
    if (to === here) return
    console.debug('shell.mobile_nav.tab_select', { tab: t })
    navigate(to)
  }, [compact, here, navigate])

  const enterDetail = useCallback((ctx?: { from?: string | null; tab?: string | null }) => {
    if (!compact) {
      setDesktopDetail(true)
      return
    }
    const rawTab = ctx?.tab ?? null
    const nextTab = rawTab !== null && MOBILE_TABS.includes(rawTab) ? (rawTab as MobileTab) : tab
    // from：ctx 给了合法值就用它；未给/非法（含 null）保留当前 URL 的 from——
    // 任务现场之间换入口不该抹掉「你从哪来」。
    const ctxFrom = ctx?.from ?? null
    const src = isMobileFrom(ctxFrom) ? ctxFrom : from
    const p = new URLSearchParams()
    p.set('tab', nextTab)
    p.set('detail', '1')
    if (src) p.set('from', src)
    // dir 随行：目录覆盖层里下钻（文件/终端）后返回条要能回到目录（逐级返回）。
    const dir = params.get('dir')
    if (dir) p.set('dir', dir)
    const to = `/?${p.toString()}`
    if (to === here) return
    console.debug('shell.mobile_nav.detail_enter', { tab: nextTab, from: src ?? null })
    // /tasks/:id 等瞬态路径（非 '/' 与 '/cards'）用 replace：跳板不留历史格。
    const transient = location.pathname !== '/' && location.pathname !== '/cards'
    navigate(to, { replace: transient })
  }, [compact, tab, from, params, here, location.pathname, navigate])

  const exitDetail = useCallback(() => {
    if (!compact) {
      setDesktopDetail(false)
      return
    }
    // 清 detail+from，留 tab/dir（无 from 兜底返回）。replace：返回类不留历史格。
    const p = new URLSearchParams()
    p.set('tab', tab)
    const dir = params.get('dir')
    if (dir) p.set('dir', dir)
    const to = `/?${p.toString()}`
    if (to === here) return
    console.debug('shell.mobile_nav.detail_back', { tab })
    navigate(to, { replace: true })
  }, [compact, tab, params, here, navigate])

  const setDir = useCallback((key: string | null) => {
    if (!compact) {
      setDesktopDirKey(key)
      return
    }
    if (key === null) {
      const p = new URLSearchParams(params)
      p.delete('dir')
      const qs = p.toString()
      const to = `/${qs ? `?${qs}` : ''}`
      if (to === here) return
      console.debug('shell.mobile_nav.dir_close', {})
      navigate(to, { replace: true })
      return
    }
    const to = `/?tab=projects&dir=${encodeURIComponent(key)}`
    if (to === here) return
    console.debug('shell.mobile_nav.dir_open', { dir: key })
    navigate(to)
  }, [compact, params, here, navigate])

  const openCard = useCallback((cardId: string, src?: MobileFrom) => {
    // 桌面无此动作：卡选中归 CardsPage 内部 state，URL 改写只发生在紧凑视口。
    if (!compact) return
    const p = new URLSearchParams(params)
    // '/' 宿主的语汇不跟随：tab/detail/dir 是 '/' 专属概念，/cards 上无消费者。
    p.delete('tab')
    p.delete('detail')
    p.delete('dir')
    p.delete('card')
    p.delete('from')
    // project 过滤保留：从带 project 参数的看板点开卡，丢参数会把用户已选的
    // 项目筛选静默清掉（CardsPage 的 URL→state effect 会跟着置空）。
    p.set('card', cardId)
    if (src) p.set('from', src)
    const to = `/cards?${p.toString()}`
    if (to === here) return
    console.debug('shell.mobile_nav.card_open', { card: cardId, from: src ?? null })
    navigate(to)
  }, [compact, params, here, navigate])

  const closeCard = useCallback(() => {
    if (!compact) return
    const p = new URLSearchParams(params)
    p.delete('card')
    p.delete('from')
    const qs = p.toString()
    const to = `/cards${qs ? `?${qs}` : ''}`
    if (to === here) return
    console.debug('shell.mobile_nav.card_close', {})
    navigate(to, { replace: true })
  }, [compact, params, here, navigate])

  const normalize = useCallback((gate: { ledgerEnabled: boolean; ledgerLoading: boolean }) => {
    if (!compact) return
    const p = new URLSearchParams(location.search)
    const isCards = location.pathname === '/cards'
    // ① 账本未启用：会话/卡两个 tab 无内容面（含 /cards 直达与缺省首屏），
    //    退回项目。replace 语义防历史堆积。必须等探测结束（!ledgerLoading）：
    //    enabled 在探到之前恒 false，若不等，移动首屏会在加载期被误判。
    if (!gate.ledgerLoading && !gate.ledgerEnabled && (derivedTab === 'sessions' || derivedTab === 'cards')) {
      if (here !== '/?tab=projects') {
        console.debug('shell.mobile_nav.normalize', { rule: 'ledger_gate', from: here })
        navigate('/?tab=projects', { replace: true })
      }
      return
    }
    // ② '/' 上 tab=cards 且无 detail 是任务现场/返回后的残留形状，归一成 /cards。
    // 瞬态跳板路径（/tasks/:id 等）不动：其参数正由跳板消费者（TaskDeepLink）
    // 读取，此刻改写会跟 enterDetail 的收口竞态、把跳板 URL 撕掉。
    if (location.pathname !== '/' && !isCards) return
    if (location.pathname === '/' && p.get('tab') === 'cards' && p.get('detail') !== '1') {
      if (here !== '/cards') {
        console.debug('shell.mobile_nav.normalize', { rule: 'cards_shape', from: here })
        navigate('/cards', { replace: true })
      }
      return
    }
    // ③ 无消费者的残参清理：dir 只在项目 tab 有消费者；from 在 '/' 上只有
    //    detail=1（返回条）有消费者——/cards 上的 from 由任务跳转 seam 消费，
    //    不是残参（会话来源链靠它回会话）。
    let dirty = false
    if (p.has('dir') && p.get('tab') !== 'projects') {
      p.delete('dir')
      dirty = true
    }
    if (p.has('from') && !isCards && p.get('detail') !== '1') {
      p.delete('from')
      dirty = true
    }
    if (dirty) {
      const qs = p.toString()
      const to = `${location.pathname}${qs ? `?${qs}` : ''}`
      console.debug('shell.mobile_nav.normalize', { rule: 'residual', from: here, to })
      navigate(to, { replace: true })
    }
  }, [compact, location.pathname, location.search, derivedTab, here, navigate])

  return useMemo(() => ({
    tab, detail, dirKey, from,
    setTab, enterDetail, exitDetail, setDir, openCard, closeCard, normalize,
  }), [tab, detail, dirKey, from, setTab, enterDetail, exitDetail, setDir, openCard, closeCard, normalize])
}
