// QuotaBits.tsx —— 账号额度的共用渲染片段（底栏、弹层、二级面板、账号页、移动设置节、载体行）。
//
// 职责：把 model.ts 的规则画成统一的小部件——kind 单色图标、细条、已用%、倒计时、状态短语、
// 窗口横排（弹层详细次行）、紧凑窗口网格（账号页卡片、移动展开）、窗口全量节（二级面板）。
// 边界：不取数、不持有状态；时间基准 now 由调用方（useNow）给，30s 一跳。
// 色彩纪律：彩色类（q-warn / q-out）只落在越线的百分比、细条与状态短语节点上（W5 测试锁）。
import type { AccountView, AccountWindowView } from '../../api/accounts'
import {
  barWindows, countdown, earliestReset, pctText, recoverAt, resetAt, statusPhrase, tinyLabel, toneClass, weeklyWindow, windowLabel, winTone,
} from './model'

// 单色字形：描边/填充走 currentColor，跟随所在文字的灰阶（原型 shared/accounts.js ICONS）。
// 自绘线稿是 16×16；codex / grok 取 OpenAI、Grok 官方标志的单色矢量（@lobehub/icons-static-svg@1.95.1
// 的 openai.svg、grok.svg 路径原样内联，24×24，evenodd 填充），viewBox 见 ICON_VIEWBOX。
const ICON_PATHS: Record<string, React.ReactNode> = {
  'opencode-go': <><rect x="3" y="2" width="10" height="12" rx="1.2" fill="none" stroke="currentColor" strokeWidth="1.4" /><rect x="5.6" y="8.2" width="4.8" height="3.6" fill="currentColor" /></>,
  codex: <path d="M9.205 8.658v-2.26c0-.19.072-.333.238-.428l4.543-2.616c.619-.357 1.356-.523 2.117-.523 2.854 0 4.662 2.212 4.662 4.566 0 .167 0 .357-.024.547l-4.71-2.759a.797.797 0 00-.856 0l-5.97 3.473zm10.609 8.8V12.06c0-.333-.143-.57-.429-.737l-5.97-3.473 1.95-1.118a.433.433 0 01.476 0l4.543 2.617c1.309.76 2.189 2.378 2.189 3.948 0 1.808-1.07 3.473-2.76 4.163zM7.802 12.703l-1.95-1.142c-.167-.095-.239-.238-.239-.428V5.899c0-2.545 1.95-4.472 4.591-4.472 1 0 1.927.333 2.712.928L8.23 5.067c-.285.166-.428.404-.428.737v6.898zM12 15.128l-2.795-1.57v-3.33L12 8.658l2.795 1.57v3.33L12 15.128zm1.796 7.23c-1 0-1.927-.332-2.712-.927l4.686-2.712c.285-.166.428-.404.428-.737v-6.898l1.974 1.142c.167.095.238.238.238.428v5.233c0 2.545-1.974 4.472-4.614 4.472zm-5.637-5.303l-4.544-2.617c-1.308-.761-2.188-2.378-2.188-3.948A4.482 4.482 0 014.21 6.327v5.423c0 .333.143.571.428.738l5.947 3.449-1.95 1.118a.432.432 0 01-.476 0zm-.262 3.9c-2.688 0-4.662-2.021-4.662-4.519 0-.19.024-.38.047-.57l4.686 2.71c.286.167.571.167.856 0l5.97-3.448v2.26c0 .19-.07.333-.237.428l-4.543 2.616c-.619.357-1.356.523-2.117.523zm5.899 2.83a5.947 5.947 0 005.827-4.756C22.287 18.339 24 15.84 24 13.296c0-1.665-.713-3.282-1.998-4.448.119-.5.19-.999.19-1.498 0-3.401-2.759-5.947-5.946-5.947-.642 0-1.26.095-1.88.31A5.962 5.962 0 0010.205 0a5.947 5.947 0 00-5.827 4.757C1.713 5.447 0 7.945 0 10.49c0 1.666.713 3.283 1.998 4.448-.119.5-.19 1-.19 1.499 0 3.401 2.759 5.946 5.946 5.946.642 0 1.26-.095 1.88-.309a5.96 5.96 0 004.162 1.713z" fill="currentColor" fillRule="evenodd" />,
  grok: <path d="M9.27 15.29l7.978-5.897c.391-.29.95-.177 1.137.272.98 2.369.542 5.215-1.41 7.169-1.951 1.954-4.667 2.382-7.149 1.406l-2.711 1.257c3.889 2.661 8.611 2.003 11.562-.953 2.341-2.344 3.066-5.539 2.388-8.42l.006.007c-.983-4.232.242-5.924 2.75-9.383.06-.082.12-.164.179-.248l-3.301 3.305v-.01L9.267 15.292M7.623 16.723c-2.792-2.67-2.31-6.801.071-9.184 1.761-1.763 4.647-2.483 7.166-1.425l2.705-1.25a7.808 7.808 0 00-1.829-1A8.975 8.975 0 005.984 5.83c-2.533 2.536-3.33 6.436-1.962 9.764 1.022 2.487-.653 4.246-2.34 6.022-.599.63-1.199 1.259-1.682 1.925l7.62-6.815" fill="currentColor" fillRule="evenodd" />,
  agy: <><path d="M2.8 13.2 8 2.8l5.2 10.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /><path d="M5.4 9.6h5.2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></>,
}

// 非 16×16 的图形各自的 viewBox；缺席即 16×16 线稿。
const ICON_VIEWBOX: Record<string, string> = { codex: '0 0 24 24', grok: '0 0 24 24' }

/** KindIcon kind 单色图标；未知 kind 不画。 */
export function KindIcon({ kind }: { kind: string }) {
  const path = ICON_PATHS[kind]
  return <span className="k-ic" aria-hidden="true">{path && <svg viewBox={ICON_VIEWBOX[kind] ?? '0 0 16 16'}>{path}</svg>}</span>
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

/**
 * Summary 一句话读数（精简口径）：问题账号「状态短语 倒计时」（无额度报预计恢复、低额度报触发窗口的重置）；
 * 探测失败/未探测只有短语；正常报每周窗口——lead「已用% 倒计时」（载体行），tail「倒计时 已用%」
 * （弹层精简行，照 orca）。
 */
export function Summary({ a, now, order }: { a: AccountView; now: number; order: 'lead' | 'tail' }) {
  if (a.state === 'out') {
    const r = recoverAt(a)
    return <><StatusText a={a} />{r !== null && <> <Reset ts={r} now={now} /></>}</>
  }
  if (a.state === 'low') {
    const w = barWindows(a, 'compact')[0]
    return <><StatusText a={a} />{w && <> <Reset ts={resetAt(w)} now={now} /></>}</>
  }
  if (a.state !== 'ok') return <StatusText a={a} />
  const w = weeklyWindow(a)
  if (!w) return null
  return order === 'tail'
    ? <><Reset ts={resetAt(w)} now={now} /> <Pct a={a} w={w} /></>
    : <><Pct a={a} w={w} /> <Reset ts={resetAt(w)} now={now} /></>
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
