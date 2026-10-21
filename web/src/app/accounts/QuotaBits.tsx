// QuotaBits.tsx —— 账号额度的共用渲染片段（底栏、弹层、二级面板、账号页、移动设置节、载体行）。
//
// 职责：把 model.ts 的规则画成统一的小部件——kind 单色图标、细条、已用%、倒计时、状态短语、
// 窗口横排（弹层详细次行）、紧凑窗口网格（账号页卡片、移动展开）、窗口全量节（二级面板）。
// 边界：不取数、不持有状态；时间基准 now 由调用方（useNow）给，30s 一跳。
// 色彩纪律：彩色类（q-warn / q-out）只落在越线的百分比、细条与状态短语节点上（W5 测试锁）。
import type { AccountView, AccountWindowView } from '../../api/accounts'
import {
  countdown, earliestReset, pctText, recoverAt, resetAt, statusPhrase, tinyLabel, toneClass, windowLabel, winTone,
} from './model'

// 单色字形：描边/填充走 currentColor，跟随所在文字的灰阶（原型 shared/accounts.js ICONS）。
const ICON_PATHS: Record<string, React.ReactNode> = {
  'opencode-go': <><rect x="3" y="2" width="10" height="12" rx="1.2" fill="none" stroke="currentColor" strokeWidth="1.4" /><rect x="5.6" y="8.2" width="4.8" height="3.6" fill="currentColor" /></>,
  codex: <><path d="M8 1.8 13.4 4.9v6.2L8 14.2 2.6 11.1V4.9Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" /><path d="M6.4 6.3 4.9 8l1.5 1.7M9.6 6.3 11.1 8 9.6 9.7" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" /></>,
  grok: <><rect x="2.2" y="2.2" width="11.6" height="11.6" rx="2.4" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M5.2 10.8 10.8 5.2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></>,
  agy: <><path d="M2.8 13.2 8 2.8l5.2 10.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /><path d="M5.4 9.6h5.2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></>,
}

/** KindIcon kind 单色图标；未知 kind 不画。 */
export function KindIcon({ kind }: { kind: string }) {
  const path = ICON_PATHS[kind]
  return <span className="k-ic" aria-hidden="true">{path && <svg viewBox="0 0 16 16">{path}</svg>}</span>
}

export const REFRESH_ICON = <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5v3h-3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>

/** QBar 3px 细条：灰轨中性灰填充，越线才改琥珀/红。 */
export function QBar({ a, w, className = '' }: { a: AccountView; w: AccountWindowView; className?: string }) {
  const tone = toneClass(winTone(a, w))
  return <span className={`qbar ${tone} ${className}`.trim()}><i style={{ width: `${Math.min(Math.max(w.used_percent, 0), 100)}%` }} /></span>
}

/** Pct 已用百分比；越线上色。 */
export function Pct({ a, w }: { a: AccountView; w: AccountWindowView }) {
  return <span className={`q-pct ${toneClass(winTone(a, w))}`.trim()}>{pctText(w)}</span>
}

/** Reset 倒计时（tpl 里 {} 处填倒计时，如「{} 后重置」）；时刻缺席不画。 */
export function Reset({ ts, now, tpl = '{}' }: { ts: number | null; now: number; tpl?: string }) {
  if (ts === null) return null
  return <span className="q-reset">{tpl.replace('{}', countdown(ts - now))}</span>
}

/** StatusText 状态短语；正常返回 null（正常不加任何标记）。 */
export function StatusText({ a }: { a: AccountView }) {
  const p = statusPhrase(a)
  return p ? <span className={p.tone} title={a.last_error?.message}>{p.text}</span> : null
}

/** DetailNote 弹层详细首行右侧注记：正常 = 最早重置「x 后重置」；无额度 = 「无额度 · x 后恢复」；其余 = 状态短语。 */
export function DetailNote({ a, now }: { a: AccountView; now: number }) {
  if (a.state === 'out') {
    const r = recoverAt(a)
    return <><span className="q-out">无额度</span>{r !== null && <> · <Reset ts={r} now={now} tpl="{} 后恢复" /></>}</>
  }
  if (a.state !== 'ok') return <StatusText a={a} />
  return <Reset ts={earliestReset(a)} now={now} tpl="{} 后重置" />
}

/** WinsInline 详细次行：「5h ▬ 34%  周 ▬ 58%  月 ▬ 22%」；agy 按模型组一组一行；探测失败旧读数降弱色。 */
export function WinsInline({ a }: { a: AccountView }) {
  const ws = a.reading?.windows ?? []
  if (ws.length === 0) return null
  const dim = a.state === 'fail' ? ' q-cached' : ''
  const qi = (w: AccountWindowView, i: number) => (
    <span key={i} className="qi" title={windowLabel(w)}><span className="qi-l">{tinyLabel(w)}</span><QBar a={a} w={w} className="qi-b" /><Pct a={a} w={w} /></span>
  )
  const groups = [...new Set(ws.map((w) => w.group).filter((g): g is string => !!g))]
  if (groups.length) {
    return <span className={`q-inline grouped${dim}`}>{groups.map((g) => (
      <span key={g} className="qg"><span className="qg-n">{g}</span>{ws.filter((w) => w.group === g).map(qi)}</span>
    ))}</span>
  }
  return <span className={`q-inline${dim}`}>{ws.map(qi)}</span>
}

/** WinsCompact 紧凑窗口网格：窗口名 细条 已用% 倒计时（账号页卡片第三行、移动展开）。 */
export function WinsCompact({ a, now }: { a: AccountView; now: number }) {
  const ws = a.reading?.windows ?? []
  if (ws.length === 0) return <div className="q-wins-empty">{a.state === 'none' ? '尚未探测' : '额度未知'}</div>
  return (
    <div className={`q-wins${a.state === 'fail' ? ' q-cached' : ''}`}>
      {ws.map((w, i) => (
        <div key={i} className="q-win"><span className="q-wl">{windowLabel(w)}</span><QBar a={a} w={w} /><Pct a={a} w={w} /><Reset ts={resetAt(w)} now={now} /></div>
      ))}
    </div>
  )
}

/** WinsFull 二级面板：一个窗口一节（窗口名、细条、左已用右重置）。 */
export function WinsFull({ a, now }: { a: AccountView; now: number }) {
  return <>{(a.reading?.windows ?? []).map((w, i) => (
    <div key={i} className={`sp-win${a.state === 'fail' ? ' q-cached' : ''}`}>
      <div className="sp-wl">{windowLabel(w)}</div>
      <QBar a={a} w={w} />
      <div className="sp-wr"><span>已用 <Pct a={a} w={w} /></span><Reset ts={resetAt(w)} now={now} tpl="{} 后重置" /></div>
    </div>
  ))}</>
}
