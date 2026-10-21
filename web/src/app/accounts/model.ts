// model.ts —— 账号额度的展示规则（B453 spec S8–S10，形态基准 prototypes/b453-accounts/shared/accounts.js）。
//
// 职责：纯函数——kind 词表、问题优先排序、窗口分档与色彩类、底栏两模式取哪个窗口、
// 状态短语、倒计时与时间文案、载体「同机账号」判据。账号页、底栏、弹层、移动设置节、
// 载体对话框共用这一份，保证同一个账号在各处读出同一句话。
//
// 边界：
//   - 不推导状态档位：state 是服务端结论（F6），这里只按它排序与选措辞
//   - 窗口分档（预警/低额度/100%）只决定颜色与「触发窗口」，不改 state
//   - 无 DOM、无 React；时间基准 now 由调用方给（测试可注入）
//
// 色彩纪律（S8，2026-10-22 用户定）：整体灰阶；彩色类只给越线的数字/状态短语本身——
// ≥ 预警线与低额度琥珀（q-warn），无额度红（q-out），探测失败/未探测弱前景（q-mute），正常不加类。
import type { AccountView, AccountWindowView } from '../../api/accounts'
import type { UsageMode } from '../settings/webPrefs'

export type Tone = 'ok' | 'warn' | 'low' | 'out'
/** 弹层与底栏的两种模式（S9）：详细 / 精简；偏好走 webPrefs（类型单一来源在那里）。 */
export type { UsageMode }

export interface KindMeta {
  name: string
  label: string
  /** 缺省凭据条目（auth.json 里的键）；agy 为空：凭据是 agy CLI 自己的登录态。 */
  entry: string
  blurb: string
}

/** 四种 kind 的固定展示顺序（S8 分组顺序）。 */
export const KIND_ORDER = ['opencode-go', 'codex', 'grok', 'agy']

export const KINDS: Record<string, KindMeta> = {
  'opencode-go': { name: 'OpenCode Go', label: 'opencode-go 订阅', entry: 'opencode-go',
    blurb: '登记在 handoff 机器上的 opencode-go 订阅；额度由所在机定时探测。' },
  codex: { name: 'Codex', label: 'codex（ChatGPT 订阅）', entry: 'tokens',
    blurb: 'ChatGPT 订阅下的 codex 登录；按会话 5h 与每周窗口计额度。' },
  grok: { name: 'Grok', label: 'grok（SuperGrok 订阅）', entry: 'https://auth.x.ai',
    blurb: 'SuperGrok 订阅；token 由该机 grok CLI 自己刷新，探测只读不刷。' },
  agy: { name: 'Antigravity', label: 'Antigravity（agy）', entry: '',
    blurb: 'Antigravity CLI 订阅；按模型组（Gemini / Claude·GPT）各计 5h 与每周额度，凭据是 agy CLI 自己的登录态。' },
}

/** kindMeta 取 kind 元信息；未知 kind（新版服务端）回退为原名，不抛。 */
export function kindMeta(kind: string): KindMeta {
  return KINDS[kind] ?? { name: kind, label: kind, entry: '', blurb: '' }
}

export const DEFAULT_INTERVAL = 15
export const DEFAULT_WARN = 60
export const DEFAULT_LOW = 80
export const INTERVALS = [5, 10, 15, 30, 60]

const STATE_RANK: Record<string, number> = { out: 0, low: 1, fail: 2, none: 3, ok: 4 }
const rank = (a: AccountView) => STATE_RANK[a.state] ?? 5

/** byProblem 问题账号排前（无额度 > 低额度 > 探测失败 > 未探测 > 正常）；同档保持原序。 */
export function byProblem(list: AccountView[]): AccountView[] {
  return list.map((a, i) => ({ a, i })).sort((x, y) => (rank(x.a) - rank(y.a)) || (x.i - y.i)).map((x) => x.a)
}

/** stateLabel 是状态词（spec 实现决定的用户可见名）。 */
export function stateLabel(state: string): string {
  return { ok: '正常', low: '低额度', out: '无额度', fail: '探测失败', none: '未探测' }[state] ?? state
}

const windows = (a: AccountView): AccountWindowView[] => a.reading?.windows ?? []

/** winTone 窗口分档：100% 无额度；≥ 低额度线 低额度；≥ 预警线 只变色；其余正常。 */
export function winTone(a: AccountView, w: AccountWindowView): Tone {
  if (w.used_percent >= 100) return 'out'
  if (w.used_percent >= a.low_percent) return 'low'
  if (w.used_percent >= a.warn_percent) return 'warn'
  return 'ok'
}

/** toneClass 分档 → 文字/细条色类；正常返回空串（正常不加任何标记）。 */
export function toneClass(t: Tone): string {
  return t === 'out' ? 'q-out' : t === 'low' || t === 'warn' ? 'q-warn' : ''
}

const tiny: Record<string, string> = { session: '5h', weekly: '周', monthly: '月' }
const full: Record<string, string> = { session: '会话 5h', weekly: '每周', monthly: '每月' }

/** windowLabel 窗口全名；agy 模型组窗口带组名（「Gemini 5h」「Claude/GPT 周」）。 */
export function windowLabel(w: AccountWindowView): string {
  if (w.group) return `${w.group} ${tiny[w.key] ?? w.key}`
  return full[w.key] ?? w.key
}

/** tinyLabel 弹层详细次行用的极短窗口名（5h / 周 / 月）。 */
export function tinyLabel(w: AccountWindowView): string {
  return tiny[w.key] ?? w.key
}

/** shortLabel 状态短语里的窗口名（会话 5h → 会话）；组窗口保留全名。 */
export function shortLabel(w: AccountWindowView): string {
  return w.group ? windowLabel(w) : windowLabel(w).replace(/\s*5h$/, '')
}

/** pctText 已用百分比取整显示；读数原值不动。 */
export function pctText(w: AccountWindowView): string {
  return `${Math.round(w.used_percent)}%`
}

/** resetAt 窗口重置时刻（毫秒）；缺席或解析失败为 null。 */
export function resetAt(w: AccountWindowView): number | null {
  if (!w.resets_at) return null
  const t = Date.parse(w.resets_at)
  return Number.isNaN(t) ? null : t
}

const minReset = (ws: AccountWindowView[]): number | null => {
  const ts = ws.map(resetAt).filter((t): t is number => t !== null)
  return ts.length ? Math.min(...ts) : null
}

/** hotWindow 触发低额度/无额度的那个窗口（第一个越过低额度线的）。 */
export function hotWindow(a: AccountView): AccountWindowView | undefined {
  return windows(a).find((w) => { const t = winTone(a, w); return t === 'low' || t === 'out' })
}

/** recoverAt 与当前状态相关的恢复时刻：越线窗口里最早的重置；无越线窗口取全部窗口最早。 */
export function recoverAt(a: AccountView): number | null {
  const ws = windows(a)
  const hot = ws.filter((w) => { const t = winTone(a, w); return t === 'low' || t === 'out' })
  return minReset(hot.length ? hot : ws)
}

/** earliestReset 全部窗口里最早的重置时刻（弹层详细首行「x 后重置」）。 */
export function earliestReset(a: AccountView): number | null {
  return minReset(windows(a))
}

/** tightest 最紧窗口：已用最高，并列取先重置的（底栏详细段的短细条画它）。 */
export function tightest(a: AccountView): AccountWindowView | undefined {
  return windows(a).slice().sort((x, y) => (y.used_percent - x.used_percent) ||
    ((resetAt(x) ?? Infinity) - (resetAt(y) ?? Infinity)))[0]
}

/** weeklyWindow 每周窗口（精简口径）；没有每周窗口时退为最紧窗口。 */
export function weeklyWindow(a: AccountView): AccountWindowView | undefined {
  return windows(a).find((w) => w.key === 'weekly') ?? tightest(a)
}

const SPAN: Record<string, number> = { session: 0, weekly: 1, monthly: 2 }

/**
 * barWindows 底栏一段报哪些窗口（S9）：
 *   - 详细 = 时长最短的两个窗口（5h < 周 < 月，同档保持原序）
 *   - 精简 = 每周；但低额度/无额度若由别的窗口触发，改报触发窗口，免得警示被每周读数掩盖
 * 无读数返回 []（调用方改写「△ 短因」/「··· 未探测」）。
 */
export function barWindows(a: AccountView, mode: UsageMode): AccountWindowView[] {
  const ws = windows(a)
  if (ws.length === 0) return []
  if (mode === 'detail') {
    return ws.map((w, i) => ({ w, i })).sort((x, y) => ((SPAN[x.w.key] ?? 9) - (SPAN[y.w.key] ?? 9)) || (x.i - y.i))
      .slice(0, 2).map((x) => x.w)
  }
  const hot = (a.state === 'out' || a.state === 'low') ? hotWindow(a) : undefined
  const w = hot ?? weeklyWindow(a)
  return w ? [w] : []
}

/** failBrief 探测失败的短因（底栏/弹层只放得下短语，全文在二级面板与账号页）。 */
export function failBrief(a: AccountView): string {
  return a.last_error?.message || '探测失败'
}

/**
 * statusPhrase 状态短语（S8–S10 共用）：正常返回 null（不加标记）；
 * 无额度红、低额度琥珀（带触发窗口与已用%）、探测失败「△ 短因」、未探测「··· 未探测」弱前景。
 */
export function statusPhrase(a: AccountView): { tone: string; text: string } | null {
  switch (a.state) {
    case 'out': return { tone: 'q-out', text: '无额度' }
    case 'low': {
      const w = hotWindow(a)
      return { tone: 'q-warn', text: w ? `低额度 · ${shortLabel(w)} ${pctText(w)}` : '低额度' }
    }
    case 'fail': return { tone: 'q-mute', text: `△ ${failBrief(a)}` }
    case 'none': return { tone: 'q-mute', text: '··· 未探测' }
    default: return null
  }
}

const M = 60_000
const Hh = 60 * M
const D = 24 * Hh

/** countdown 倒计时文案（「4d 2h」「2h41m」「5m」；已到点「即将」）。 */
export function countdown(ms: number): string {
  if (ms <= 0) return '即将'
  const d = Math.floor(ms / D), h = Math.floor((ms % D) / Hh), m = Math.floor((ms % Hh) / M)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h${String(m).padStart(2, '0')}m`
  return `${Math.max(m, 1)}m`
}

/** ago 相对过去时（「刚刚」「12 分钟前」「3 小时前」「2 天前」）；无时刻回「从未」。 */
export function ago(iso: string | undefined, now: number): string {
  if (!iso) return '从未'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '从未'
  const ms = now - t
  if (ms < M) return '刚刚'
  if (ms < Hh) return `${Math.floor(ms / M)} 分钟前`
  if (ms < D) return `${Math.floor(ms / Hh)} 小时前`
  return `${Math.floor(ms / D)} 天前`
}

/** clock「MM-DD HH:mm」本地时刻。 */
export function clock(ts: number): string {
  const t = new Date(ts)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(t.getMonth() + 1)}-${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}`
}

/** updatedText 读数新旧一句话：未探测 / 探测失败（是否有旧读数）/ x 前更新（过期另注）。 */
export function updatedText(a: AccountView, now: number): string {
  if (a.state === 'none') return '尚未探测'
  const probed = a.reading?.probed_at
  if (a.state === 'fail') {
    return probed ? `${ago(a.last_attempt_at, now)}探测失败 · 读数来自 ${ago(probed, now)}` : `${ago(a.last_attempt_at, now)}探测失败 · 还没有成功读数`
  }
  return `${ago(a.last_attempt_at ?? probed, now)}更新${a.stale ? ' · 读数已过期' : ''}`
}

/**
 * nextProbeText 下次探测：上次尝试 + 间隔。读面不带退避状态，探测失败时只能给上界
 * （失败退避 30s×2ⁿ 封顶不超过间隔），如实写「不晚于」。
 */
export function nextProbeText(a: AccountView, now: number): string {
  if (!a.last_attempt_at) return '所在机随即探测'
  const t = Date.parse(a.last_attempt_at)
  if (Number.isNaN(t)) return '所在机随即探测'
  const next = t + a.interval_minutes * M
  return a.state === 'fail' ? `失败后退避重试，不晚于 ${countdown(next - now)} 后` : `下次 ${countdown(next - now)} 后`
}

/** extraUsageText 额外额度一句话；无可报数值返回空串。 */
export function extraUsageText(a: AccountView): string {
  const x = a.reading?.extra_usage
  if (!x) return ''
  const unit = x.unit ? ` ${x.unit}` : ''
  if (x.unlimited) return '不限量'
  if (x.balance !== undefined) return `余额 ${x.balance}${unit}`
  if (x.used !== undefined && x.limit !== undefined) return `已用 ${x.used} / ${x.limit}${unit}`
  if (x.used !== undefined) return `已用 ${x.used}${unit}`
  return ''
}

/** entryText 凭据位置（HOME › 条目；agy 为 CLI 自己的登录态）。 */
export function entryText(a: AccountView): string {
  return a.entry ? `${a.home_dir} › auth.json › ${a.entry}` : `${a.home_dir}（agy CLI 自己的登录态）`
}

const LOCAL_ALIASES = new Set(['', '本机', 'local'])

/**
 * sameMachine 载体对话框「只列同机账号」判据（S4）：名字相等即同机；载体写的是本机别名
 * （空/本机/local）时，控制台不知道本机的全队名，取「账号所在机不是任何已登记远端机」。
 * 服务端 checkCarrierAccount 仍是权威校验，这里只决定下拉里列谁。
 */
export function sameMachine(accountMachine: string, carrierMachine: string, remoteNames: string[]): boolean {
  if (accountMachine === carrierMachine) return true
  if (LOCAL_ALIASES.has(carrierMachine.trim())) return !remoteNames.includes(accountMachine)
  return false
}
