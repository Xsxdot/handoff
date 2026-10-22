// model.ts —— 账号、登录与额度的展示规则（B464 spec S1–S7，形态基准 prototypes/b464-accounts/shared/accounts.js）。
//
// 职责：纯函数——kind 词表（六组）、问题优先排序、窗口分档与色彩类、底栏两模式取哪个窗口、
// 状态短语与失败短因、登录状态词与凭据时刻文案、载体「同机登录」判据与派发提示、倒计时与时间文案。
// 账号页、底栏、弹层、移动设置节、载体对话框共用这一份，保证同一个账号在各处读出同一句话。
//
// 边界：
//   - 不推导账号状态档位：state 是服务端结论（F6），这里只按它排序与选措辞
//   - 不推导登录状态：state 是发现/证据的结论（S3），这里只映射成词
//   - 窗口分档（预警/低额度/100%）只决定颜色与「触发窗口」，不改 state
//   - 无 DOM、无 React；时间基准 now 由调用方给（测试可注入）
//
// 色彩纪律（S8，2026-10-22 用户定）：整体灰阶；彩色类只给越线的数字/状态短语本身——
// ≥ 预警线与低额度琥珀（q-warn），无额度与登录已过期/已失效红（q-out），探测失败/未探测/
// 不支持额度探测弱前景（q-mute），正常不加类。
import type { AccountView, AccountWindowView, LoginCommand, LoginView } from '../../api/accounts'
import type { UsageMode } from '../settings/webPrefs'

export type Tone = 'ok' | 'warn' | 'low' | 'out'
/** 弹层与底栏的两种模式（S9）：详细 / 精简；偏好走 webPrefs（类型单一来源在那里）。 */
export type { UsageMode }

export interface KindMeta {
  name: string
  label: string
  /** 对应执行器：载体对话框按它过滤兼容登录，新增登录按它给命令。 */
  cli: string
  /** 是否支持额度探测（claude 与第三方 API key 为 false，底栏/弹层/移动节不要求读数）。 */
  quota: boolean
  blurb: string
}

/** 六组 kind 的固定展示顺序（S8 分组顺序 + S7 的 claude / 第三方 API key）。 */
export const KIND_ORDER = ['opencode-go', 'codex', 'grok', 'agy', 'claude', 'apikey']

export const KINDS: Record<string, KindMeta> = {
  'opencode-go': {
    name: 'OpenCode Go', label: 'opencode-go 订阅', cli: 'opencode', quota: true,
    blurb: 'opencode-go 订阅；按 opencode 账号 id + org 认身份，额度由持有正常登录的机器定时探测。',
  },
  codex: {
    name: 'Codex', label: 'codex（ChatGPT 订阅）', cli: 'codex', quota: true,
    blurb: 'ChatGPT 订阅下的 codex 登录；按邮箱认身份，按会话 5h 与每周窗口计额度。',
  },
  grok: {
    name: 'Grok', label: 'grok（SuperGrok 订阅）', cli: 'grok', quota: true,
    blurb: 'SuperGrok 订阅；token 由该机 grok CLI 自己刷新，探测只读不刷。',
  },
  agy: {
    name: 'Antigravity', label: 'Antigravity（agy）', cli: 'agy', quota: true,
    blurb: 'Antigravity CLI 订阅；按模型组（Gemini / Claude·GPT）各计 5h 与每周额度，凭据是 agy CLI 自己的登录态。',
  },
  claude: {
    name: 'Claude', label: 'Claude（Claude Code 订阅）', cli: 'claude', quota: false,
    blurb: 'Claude Code 订阅登录；按账号身份认人。不支持额度探测，引用它的载体照常派发。',
  },
  apikey: {
    name: '第三方 API key', label: '第三方 provider API key（opencode）', cli: 'opencode', quota: false,
    blurb: 'opencode auth.json 里的第三方 provider API key（deepseek、alibaba…）；按 key 短指纹认身份，不支持额度探测，引用它的载体照常派发。',
  },
}

/** kindMeta 取 kind 元信息；未知 kind（新版服务端）回退为原名，不抛。 */
export function kindMeta(kind: string): KindMeta {
  return KINDS[kind] ?? { name: kind, label: kind, cli: '', quota: false, blurb: '' }
}

/** supportsProbe kind 是否支持额度探测（claude / apikey 为 false；未知 kind 按 false）。 */
export function supportsProbe(kind: string): boolean {
  return KINDS[kind]?.quota === true
}

/** quotaAccounts 只保留支持额度探测的账号（底栏与使用量弹层只列这些）。 */
export function quotaAccounts(list: AccountView[]): AccountView[] {
  return list.filter((a) => supportsProbe(a.kind))
}

export const DEFAULT_INTERVAL = 15
export const DEFAULT_WARN = 60
export const DEFAULT_LOW = 80
export const INTERVALS = [5, 10, 15, 30, 60]

const STATE_RANK: Record<string, number> = { out: 0, low: 1, fail: 2, none: 3, ok: 4, na: 5 }
const rank = (a: AccountView) => STATE_RANK[a.state] ?? 5

/** byProblem 问题账号排前（无额度 > 低额度 > 探测失败 > 未探测 > 正常 > 不支持探测）；同档保持原序。 */
export function byProblem(list: AccountView[]): AccountView[] {
  return list.map((a, i) => ({ a, i })).sort((x, y) => (rank(x.a) - rank(y.a)) || (x.i - y.i)).map((x) => x.a)
}

/** stateLabel 是账号额度状态词（spec 实现决定的用户可见名）。 */
export function stateLabel(state: string): string {
  return { ok: '正常', low: '低额度', out: '无额度', fail: '探测失败', none: '未探测', na: '不支持额度探测' }[state] ?? state
}

const windows = (a: AccountView): AccountWindowView[] => a.reading?.windows ?? []

/** machinesOf 账号名下登录所在机器（去重、按登录顺序）；无登录为 []。 */
export function machinesOf(a: AccountView): string[] {
  return [...new Set(a.logins.map((l) => l.machine))]
}

/** machinesText 账号登录所在机器一句话；无登录回「无登录」。 */
export function machinesText(a: AccountView): string {
  return machinesOf(a).join(' · ') || '无登录'
}

/**
 * accountPlace 账号读面的「所在位置」（B464：机器/HOME/来源/代理在登录上）：取可探登录，缺席时
 * 取第一条登录；名下无登录时各字段为空串。只在需要单一位置的摘要处用（底栏/移动节/侧栏）。
 */
export function accountPlace(a: AccountView): { machine: string; home: string; source: string; proxy?: string } {
  const l = a.logins.find((x) => x.id === a.probe_login) ?? a.logins[0]
  return { machine: l?.machine ?? '', home: l?.home ?? '', source: l?.source ?? '', proxy: l?.proxy }
}

/** homeText 登录 HOME 的显示写法：`~` = 该机主 HOME。 */
export function homeText(home: string): string {
  return home === '~' ? '~（主 HOME）' : home
}

/** LOGIN_STATE 登录状态词与提示（S3）：已过期只提示不拦；已失效暂停派发。 */
export const LOGIN_STATE: Record<string, { label: string; hint: string }> = {
  ok: { label: '正常', hint: '' },
  expired: { label: '已过期', hint: 'CLI 运行时可能自行刷新；如失败请重新登录' },
  gone: { label: '已失效', hint: '上次发现后找不到凭据，重新登录后自动恢复' },
}

/** loginStateLabel 登录状态词；未知状态回原名。 */
export function loginStateLabel(state: string): string {
  return LOGIN_STATE[state]?.label ?? state
}

/** loginStateHint 登录状态的行动提示；正常/未知为空串。 */
export function loginStateHint(state: string): string {
  return LOGIN_STATE[state]?.hint ?? ''
}

/**
 * loginCredText 登录的凭据时刻一句话：apikey 没有到期 →「key 不过期」；有 expires_at →
 * 未到期「到期 MM-DD HH:mm」/ 已到期「已于 MM-DD HH:mm 到期」；否则「变更 x 前」。
 */
export function loginCredText(l: LoginView, kind: string, now: number): string {
  if (l.expires_at) {
    const ts = Date.parse(l.expires_at)
    if (!Number.isNaN(ts)) return ts > now ? `到期 ${clock(ts)}` : `已于 ${clock(ts)} 到期`
  }
  if (kind === 'apikey') return 'key 不过期'
  return `变更 ${ago(l.changed_at, now)}`
}

/** loginCommandText 重新登录命令一句话：远端先 ssh 再执行；note 放括号里。 */
export function loginCommandText(cmd: LoginCommand | undefined): string {
  if (!cmd) return ''
  const prefix = cmd.ssh ? `${cmd.ssh} 后执行：` : ''
  return `${prefix}${cmd.run}${cmd.note ? `（${cmd.note}）` : ''}`
}

/** winTone 窗口分档：100% 无额度；≥ 低额度线 低额度；≥ 预警线 只变色；其余正常。 */
export function winTone(a: AccountView, w: AccountWindowView): Tone {
  if (w.used_percent >= 100) return 'out'
  if (w.used_percent >= (a.low_percent ?? DEFAULT_LOW)) return 'low'
  if (w.used_percent >= (a.warn_percent ?? DEFAULT_WARN)) return 'warn'
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

/**
 * recoverAt 预计离开低额度/无额度的时刻（毫秒）：只读服务端推导的 recover_at（accounts.RecoverAt，
 * 越线窗口全部重置即最晚那个）。缺席 = 不阻挡或未知（撞额度标记、接口明示不可用、越线窗口缺重置
 * 时刻），返回 null——界面不拿窗口重置时刻自算顶替，免得与准入规则两份漂移（审查第 1 轮 I2）。
 */
export function recoverAt(a: AccountView): number | null {
  if (!a.recover_at) return null
  const t = Date.parse(a.recover_at)
  return Number.isNaN(t) ? null : t
}

/** earliestReset 全部窗口里最早的重置时刻（弹层详细首行「x 后重置」）。 */
export function earliestReset(a: AccountView): number | null {
  return minReset(windows(a))
}

const byTightness = (ws: AccountWindowView[]): AccountWindowView[] => ws.slice().sort((x, y) =>
  (y.used_percent - x.used_percent) || ((resetAt(x) ?? Infinity) - (resetAt(y) ?? Infinity)))

/** tightest 最紧窗口：已用最高，并列取先重置的（底栏详细段的短细条画它）。 */
export function tightest(a: AccountView): AccountWindowView | undefined {
  return byTightness(windows(a))[0]
}

/**
 * weeklyWindow 每周窗口（精简口径）：多组 kind（agy 按模型组各有一个每周）取已用最高的那个，
 * 并列取先重置的——精简只报一个数，报最紧的组才不掩盖警示；没有每周窗口时退为最紧窗口。
 */
export function weeklyWindow(a: AccountView): AccountWindowView | undefined {
  const weekly = windows(a).filter((w) => w.key === 'weekly')
  return byTightness(weekly)[0] ?? tightest(a)
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

// 失败类别 → 短因（类别是 quota 包的 Category；R8 修 1，2026-10-22 协调者定词）。
// message 是带处置建议的整句，放不进底栏段/弹层行；未列的类别（含新版服务端新增的）回「探测失败」。
const FAIL_BRIEF: Record<string, string> = {
  credential_expired: '凭据已过期',
  credential_missing: '凭据缺失',
  credential_invalid: '凭据无效',
  unauthorized: '凭据无效',
  proxy: '代理连接失败',
  network: '网络不通',
  timeout: '探测超时',
  version_too_old: '版本过旧，已跳过',
  cli_missing: '未安装 CLI',
  rate_limited: '被限速',
  upstream: '上游错误',
  no_subscription: '无订阅',
  no_login: '没有可用登录',
  parse: '读数无法解析',
  unsupported_platform: '本平台不支持',
}

/**
 * failBrief 探测失败的短因：按 last_error.category 映射成短语（底栏段、弹层行、账号卡片状态位、
 * 载体行）；完整 message 只在二级面板、账号卡片正文/详情与移动展开里出现。
 */
export function failBrief(a: AccountView): string {
  return FAIL_BRIEF[a.last_error?.category ?? ''] ?? '探测失败'
}

/**
 * statusPhrase 状态短语（S8–S10 共用）：正常返回 null（不加标记）；
 * 无额度红、低额度琥珀（带触发窗口与已用%）、探测失败「△ 短因」、未探测「··· 未探测」弱前景、
 * 不支持额度探测「不支持额度探测」弱前景。
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
    case 'na': return { tone: 'q-mute', text: '不支持额度探测' }
    default: return null
  }
}

export interface CarrierHint {
  tone: string
  text: string
  paused: boolean
  /** 是否给「复制登录命令」入口（登录已过期/已失效）。 */
  relogin?: boolean
}

/**
 * dispatchHint 载体行的派发提示（S6）：先看所用登录——已失效暂停派发（重新登录后恢复），
 * 已过期只标红提示、照常派发；再看账号额度——低额度/无额度暂停并报预计恢复，探测失败/未探测
 * 额度未知照常派发；不支持额度探测的 kind 只说明派发不看额度。无账号与未选登录由调用方处理。
 */
export function dispatchHint(a: AccountView | undefined, login: LoginView | undefined, now: number): CarrierHint | null {
  if (!a || !login) return null
  if (login.state === 'gone') {
    return { tone: 'q-out', paused: true, relogin: true, text: `暂停派发：所用登录已失效（${login.machine} ${homeText(login.home)}），重新登录后自动恢复` }
  }
  if (login.state === 'expired') {
    return { tone: 'q-out', paused: false, relogin: true, text: '登录已过期，CLI 运行时可能自行刷新；如失败请重新登录' }
  }
  if (!supportsProbe(a.kind) || a.state === 'na') return { tone: 'q-mute', paused: false, text: '不支持额度探测，照常派发' }
  if (a.state === 'out') {
    const r = recoverAt(a)
    return { tone: 'q-out', paused: true, text: r !== null ? `暂停派发，预计 ${countdown(r - now)} 后恢复` : '暂停派发，恢复时刻未知' }
  }
  if (a.state === 'low') {
    const w = hotWindow(a)
    const r = recoverAt(a)
    const trigger = w ? `${shortLabel(w)} 已用 ${pctText(w)}` : '低额度'
    return { tone: 'q-warn', paused: true, text: r !== null ? `暂停派发（${trigger}），预计 ${countdown(r - now)} 后恢复` : `暂停派发（${trigger}）` }
  }
  if (a.state === 'fail' || a.state === 'none') return { tone: 'q-mute', paused: false, text: '额度未知，照常派发' }
  return null
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

/** updatedText 读数新旧一句话：不支持探测 / 未探测 / 探测失败（是否有旧读数）/ x 前更新（过期另注）。 */
export function updatedText(a: AccountView, now: number): string {
  if (a.state === 'na') return '不支持额度探测'
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
  const next = t + (a.interval_minutes ?? DEFAULT_INTERVAL) * M
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

const LOCAL_ALIASES = new Set(['', '本机', 'local'])

/**
 * sameMachine 载体对话框「只列同机登录」判据（S6）：名字相等即同机；载体写的是本机别名
 * （空/本机/local）时，控制台不知道本机的全队名，取「登录所在机不是任何已登记远端机」。
 * 服务端仍是权威校验，这里只决定下拉里列谁。
 */
export function sameMachine(loginMachine: string, carrierMachine: string, remoteNames: string[]): boolean {
  if (loginMachine === carrierMachine) return true
  if (LOCAL_ALIASES.has(carrierMachine.trim())) return !remoteNames.includes(loginMachine)
  return false
}
