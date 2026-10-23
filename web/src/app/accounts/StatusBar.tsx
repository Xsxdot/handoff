// StatusBar.tsx —— 控制台额度底栏与「使用量」弹层（B453 spec S9，形态基准 prototypes/b453-accounts 各页底栏）。
//
// 职责：
//   - 底栏一行弱色小字：「已用」+ 每账号一段（图标 读数；名字只在 title 悬停，B467 S8），
//     问题账号排前，最右刷新；放不下的从尾部（正常账号）收进「+N」
//   - 点底栏弹出「使用量」弹层：按类型分组、详细/精简两模式（偏好 webPrefs.usageMode，缺省详细），
//     底栏随模式变；hover（触屏/键盘为点击/聚焦）某行在右侧二级面板看全部细节与立即探测
//
// 边界：
//   - 数据只读 Shell 级共享快照（store.ts）；立即探测经 store.probe 就地合入，不整页重载
//   - 弹层底部菜单只有「管理账号…」（B467 S9），只回调 onManage，路由由 Shell 决定
//   - 紧凑档（phone/pad）不挂本组件（S10：移动端不显示底栏，额度在设置页）——由 Shell 判
//   - 底栏不写「低额度」「无额度」字样：越线只靠数值与细条的琥珀/红色区分（S9 第五轮定）
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { AccountView } from '../../api/accounts'
import { errorMessage } from '../lib/format'
import { useWebPrefs } from '../settings/useWebPrefs'
import { usageModeOf } from '../settings/webPrefs'
import {
  accountPlace, barWindows, byProblem, extraUsageText, failBrief, KIND_ORDER, kindMeta, quotaAccounts, resetAt, tightest, updatedText, type UsageMode,
} from './model'
import { DetailNote, KindIcon, Pct, QBar, REFRESH_ICON, Reset, Summary, WinsFull, WinsInline } from './QuotaBits'
import { useAccounts, useNow } from './store'
import './accounts.css'

/** BarReading 底栏一段的读数（详细：短细条 + 最短两个窗口；精简：每周或触发窗口）。 */
function BarReading({ a, mode, now }: { a: AccountView; mode: UsageMode; now: number }) {
  const ws = barWindows(a, mode)
  if (ws.length === 0) {
    // 无读数：探测失败「△ 短因」/ 未探测「··· 未探测」（弱前景）
    return a.state === 'none' ? <span className="q-mute">··· 未探测</span> : <span className="q-mute" title={a.last_error?.message}>△ {failBrief(a)}</span>
  }
  const cached = a.state === 'fail'
  const t = tightest(a)
  return (
    <>
      <span className={`sb-read${cached ? ' q-cached' : ''}`}>
        {mode === 'detail' && t && <span className="sb-mb"><QBar a={a} w={t} /></span>}
        {ws.map((w, i) => (
          <span key={i} className="sb-w">
            {i > 0 && <span className="sb-dot">·</span>}
            <Pct a={a} w={w} /> <Reset ts={resetAt(w)} now={now} />
          </span>
        ))}
      </span>
      {cached && <span className="q-mute" title={a.last_error?.message}>△</span>}
    </>
  )
}

/** SidePanel 二级面板：全部窗口、额外额度、失败原因与读数时刻、所在机器、载体、立即探测、管理账号。 */
function SidePanel({ a, now, onBack, onManage }: { a: AccountView; now: number; onBack: () => void; onManage: (name: string) => void }) {
  const { probe } = useAccounts()
  const [probing, setProbing] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { setProbing(false); setError('') }, [a.name])
  const extra = extraUsageText(a)
  const run = async () => {
    setProbing(true)
    setError('')
    try { await probe(a.name) } catch (cause) { setError(errorMessage(cause)) } finally { setProbing(false) }
  }
  return (
    <>
      <button type="button" className="sp-back" onClick={onBack}>‹ 使用量</button>
      <div className="sp-head"><span className="sp-name">{a.name}</span><span className="q-tag">{kindMeta(a.kind).name}</span>{a.reading?.plan_type && <span className="q-tag" title="套餐">{a.reading.plan_type}</span>}</div>
      <div className="sp-sub">{updatedText(a, now)}</div>
      <WinsFull a={a} now={now} />
      {extra && <div className="sp-line"><span>额外额度</span><span className="q-mono">{extra}</span></div>}
      {a.state === 'none' && <div className="sp-note">尚未探测。保存后由所在机 agentd 探测一次。</div>}
      {a.last_error && a.state !== 'none' && <div className="sp-note">△ {a.last_error.message}{a.state === 'fail' && a.reading && <><br />显示的是旧读数</>}</div>}
      <div className="sp-line"><span>所在机器</span><span>{accountPlace(a).machine}</span></div>
      <div className="sp-sec"><div className="sp-sec-t">使用此账号的载体</div><div className="q-mono">{a.carriers.length ? a.carriers.join('、') : <span className="q-mute">无</span>}</div></div>
      <button type="button" className="sp-act" disabled={probing} onClick={() => void run()}>{probing ? '探测中…' : '立即探测'}</button>
      {error && <p role="alert" className="sp-err">探测失败：{error}</p>}
      <button type="button" className="sp-act" onClick={() => onManage(a.name)}>管理账号…<span>›</span></button>
    </>
  )
}

const canHover = () => typeof window.matchMedia === 'function' && window.matchMedia('(hover: hover)').matches

export function UsageStatusBar({ onManage }: { onManage: (name?: string) => void }) {
  const { accounts, probe } = useAccounts()
  const [prefs, setPrefs] = useWebPrefs()
  const mode = usageModeOf(prefs)
  const now = useNow()
  const [open, setOpen] = useState(false)
  const [current, setCurrent] = useState('')
  const [hiddenFrom, setHiddenFrom] = useState(-1)
  const [probingAll, setProbingAll] = useState(false)
  const barRef = useRef<HTMLElement>(null)
  const segsRef = useRef<HTMLDivElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const sideRef = useRef<HTMLDivElement>(null)

  // 底栏与使用量弹层只列支持额度探测的 kind（claude / 第三方 API key 的 na 不进）。
  const sorted = accounts ? byProblem(quotaAccounts(accounts)) : []
  const selected = sorted.find((a) => a.name === current)

  const close = useCallback(() => { setOpen(false); setCurrent('') }, [])

  // 溢出收纳：问题优先排序后从尾部（正常的）收进「+N」，收起来的永远是最不需要看的。
  // 量不到宽度（未布局）时不收，宁可全显示也不把整条读数藏掉。
  useLayoutEffect(() => {
    const el = segsRef.current
    if (!el) return
    const fit = () => {
      const avail = el.clientWidth
      if (avail <= 0) { setHiddenFrom(-1); return }
      const segs = [...el.children] as HTMLElement[]
      const gap = 12, reserve = 34
      let used = 0, cut = -1
      segs.forEach((c, i) => {
        used += c.offsetWidth + (i ? gap : 0)
        if (cut < 0 && used > avail - (i === segs.length - 1 ? 0 : reserve)) cut = i
      })
      setHiddenFrom(cut)
    }
    setHiddenFrom(-1)
    // 先全显示一帧再量：被藏的段 offsetWidth 为 0，不放出来量不准
    const raf = window.requestAnimationFrame(fit)
    window.addEventListener('resize', fit)
    return () => { window.cancelAnimationFrame(raf); window.removeEventListener('resize', fit) }
  }, [accounts, mode, now])

  // 点弹层与底栏之外 / Esc 关闭
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (popRef.current?.contains(t) || barRef.current?.contains(t)) return
      close()
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close() }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open, close])

  // 二级面板顶部对齐被选中的行，放不下时整体上移，不出视口、不压列表（窄屏改盖在列表上，CSS 处理）
  useLayoutEffect(() => {
    const side = sideRef.current, pop = popRef.current
    if (!side || !pop || !current) return
    side.style.top = ''
    side.style.bottom = ''
    if (typeof window.matchMedia === 'function' && !window.matchMedia('(min-width: 681px)').matches) return
    const row = pop.querySelector<HTMLElement>(`[data-acct="${CSS.escape(current)}"]`)
    if (!row) return
    const popTop = pop.getBoundingClientRect().top
    const floor = (barRef.current?.getBoundingClientRect().top ?? window.innerHeight) - 6
    const top = Math.max(8, Math.min(row.getBoundingClientRect().top, floor - side.offsetHeight))
    side.style.top = `${top - popTop}px`
    side.style.bottom = 'auto'
  }, [current, mode, open, accounts])

  if (accounts === null) return null

  const setMode = (next: UsageMode) => {
    console.info('accounts.usage.mode', { mode: next })
    setPrefs({ ...prefs, usageMode: next })
  }
  const probeAll = async () => {
    if (probingAll) return
    setProbingAll(true)
    console.info('accounts.probe_all.start', { accounts: sorted.length })
    const results = await Promise.allSettled(sorted.map((a) => probe(a.name)))
    const failed = results.filter((r) => r.status === 'rejected').length
    console.info('accounts.probe_all.done', { accounts: sorted.length, failed })
    setProbingAll(false)
  }
  const onBarClick = (e: React.MouseEvent) => {
    const target = e.target as HTMLElement
    if (target.closest('.sb-refresh')) return
    const seg = target.closest<HTMLElement>('.sb-seg')
    const name = seg?.dataset.acct ?? ''
    if (open && (!seg || name === current)) { close(); return }
    setOpen(true)
    setCurrent(name)
  }
  const select = (name: string) => setCurrent(name)

  const rowFor = (a: AccountView) => {
    const on = a.name === current ? ' on' : ''
    const common = {
      type: 'button' as const, 'data-acct': a.name, 'data-testid': `up-row-${a.name}`,
      onMouseEnter: () => { if (canHover()) select(a.name) },
      onFocus: () => select(a.name),
      onClick: () => select(a.name),
    }
    if (mode === 'compact') {
      return <button key={a.name} className={`up-row${on}`} {...common}>
        <span className="up-r1"><span className="up-name">{a.name}</span><span className="up-mach">{accountPlace(a).machine}</span><span className="up-val"><Summary a={a} now={now} order="tail" /></span><span className="up-chev">›</span></span>
      </button>
    }
    const hasWins = (a.reading?.windows.length ?? 0) > 0
    return <button key={a.name} className={`up-row d${on}`} {...common}>
      <span className="up-r1"><span className="up-name">{a.name}</span><span className="up-mach">{accountPlace(a).machine}</span><span className="up-note"><DetailNote a={a} now={now} /></span><span className="up-chev">›</span></span>
      {hasWins && <span className="up-r2"><WinsInline a={a} /></span>}
    </button>
  }

  const kinds = [...KIND_ORDER, ...new Set(sorted.map((a) => a.kind).filter((k) => !KIND_ORDER.includes(k)))]

  return (
    <>
      <footer ref={barRef} data-testid="usage-statusbar" className="usage-statusbar" title="使用量" onClick={onBarClick}>
        <span className="sb-lead">已用</span>
        <div ref={segsRef} className="sb-segs">
          {sorted.length === 0 && <span>还没有可探测额度的账号</span>}
          {sorted.map((a, i) => (
            <button key={a.name} type="button" className="sb-seg" title={a.name} data-acct={a.name} data-testid={`sb-seg-${a.name}`} hidden={hiddenFrom >= 0 && i >= hiddenFrom}>
              <KindIcon kind={a.kind} /><span className="sb-val"><BarReading a={a} mode={mode} now={now} /></span>
            </button>
          ))}
        </div>
        {hiddenFrom >= 0 && <span className="sb-more">+{sorted.length - hiddenFrom}</span>}
        <button type="button" className={`sb-refresh${probingAll ? ' spinning' : ''}`} aria-label="刷新所有账号" title="刷新所有账号" onClick={() => void probeAll()}>{REFRESH_ICON}</button>
      </footer>
      {open && createPortal(
        <div ref={popRef} className="usage-pop" role="dialog" aria-label="使用量">
          <div className="up-main">
            <div className="up-head"><span className="up-title">使用量</span><button type="button" className={`up-all${probingAll ? ' spinning' : ''}`} title="立即探测所有账号" onClick={() => void probeAll()}>所有账号 <span className="up-ic">{REFRESH_ICON}</span></button></div>
            <div className="up-seg" role="tablist" aria-label="显示模式">
              <button type="button" role="tab" aria-selected={mode === 'detail'} onClick={() => setMode('detail')}>详细</button>
              <button type="button" role="tab" aria-selected={mode === 'compact'} onClick={() => setMode('compact')}>精简</button>
            </div>
            <div className={`up-list ${mode}`}>
              {sorted.length === 0 && <p className="up-empty">还没有可探测额度的账号。到「账号与额度」页新增登录后，这里显示额度。</p>}
              {kinds.map((kind) => {
                const list = sorted.filter((a) => a.kind === kind)
                if (list.length === 0) return null
                return [<div key={`g-${kind}`} className="up-group"><KindIcon kind={kind} />{kindMeta(kind).name}</div>, ...list.map(rowFor)]
              })}
            </div>
            <div className="up-menu">
              <button type="button" onClick={() => { close(); onManage() }}>管理账号…<span>›</span></button>
            </div>
          </div>
          {selected && (
            <div ref={sideRef} className="up-side" data-testid="usage-side">
              <SidePanel a={selected} now={now} onBack={() => setCurrent('')} onManage={(name) => { close(); onManage(name) }} />
            </div>
          )}
        </div>,
        document.body,
      )}
    </>
  )
}
