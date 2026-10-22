// model.test.ts —— 账号额度展示规则（B453 S8–S10 / plan W4、W5）的核心方法。
// 边界：只测纯函数——排序、窗口分档、底栏两模式取哪个窗口、同机判据、倒计时；
// 渲染与色彩类落点由组件测试锁。
import { describe, expect, it } from 'vitest'
import type { AccountView, AccountWindowView, LoginView } from '../../api/accounts'
import {
  barWindows, byProblem, countdown, dispatchHint, failBrief, homeText, hotWindow, KIND_ORDER, loginCommandText, loginCredText,
  loginStateHint, loginStateLabel, machinesText, quotaAccounts, recoverAt, sameMachine, statusPhrase, supportsProbe, tightest,
  toneClass, updatedText, weeklyWindow, windowLabel, winTone,
} from './model'

const NOW = Date.parse('2026-10-22T00:00:00Z')
const iso = (ms: number) => new Date(NOW + ms).toISOString()
const H = 3600_000
const win = (key: string, used: number, resetIn?: number, group?: string): AccountWindowView => ({
  key, used_percent: used, ...(resetIn === undefined ? {} : { resets_at: iso(resetIn) }), ...(group ? { group } : {}),
})
function acct(name: string, state: string, windows?: AccountWindowView[], extra: Partial<AccountView> = {}): AccountView {
  return {
    id: `codex:${name}`, name, kind: 'codex', fingerprint: 'sha256:000000000000', logins: [], interval_minutes: 15, warn_percent: 60, low_percent: 80,
    version: 1, state, stale: false, carriers: [],
    ...(windows ? { reading: { windows, probed_at: iso(-60_000) } } : {}),
    ...extra,
  }
}

describe('账号展示规则', () => {
  it('问题账号排前：无额度 > 低额度 > 探测失败 > 未探测 > 正常，同档保持原序（W4）', () => {
    const list = [acct('ok1', 'ok'), acct('none', 'none'), acct('low', 'low'), acct('ok2', 'ok'), acct('fail', 'fail'), acct('out', 'out')]
    expect(byProblem(list).map((a) => a.name)).toEqual(['out', 'low', 'fail', 'none', 'ok1', 'ok2'])
  })

  it('窗口分档：< 预警 不上色；≥ 预警与 ≥ 低额度琥珀；100% 红（W5）', () => {
    const a = acct('a', 'ok')
    expect(winTone(a, win('weekly', 59.6))).toBe('ok')
    expect(winTone(a, win('weekly', 60))).toBe('warn')
    expect(winTone(a, win('weekly', 80))).toBe('low')
    expect(winTone(a, win('weekly', 100))).toBe('out')
    expect(toneClass('ok')).toBe('')
    expect(toneClass('warn')).toBe('q-warn')
    expect(toneClass('low')).toBe('q-warn')
    expect(toneClass('out')).toBe('q-out')
  })

  it('底栏详细取最短两个窗口；精简取每周，低额度/无额度改取触发窗口（W4）', () => {
    const ok = acct('ok', 'ok', [win('monthly', 22, 400 * H), win('weekly', 58, 98 * H), win('session', 34, 2 * H)])
    expect(barWindows(ok, 'detail').map((w) => w.key)).toEqual(['session', 'weekly'])
    expect(barWindows(ok, 'compact').map((w) => w.key)).toEqual(['weekly'])
    const low = acct('low', 'low', [win('session', 82, 2 * H), win('weekly', 41, 50 * H)])
    expect(barWindows(low, 'compact').map((w) => w.key)).toEqual(['session'])
    const grok = acct('g', 'ok', [win('weekly', 27, 80 * H), win('monthly', 46, 300 * H)])
    expect(barWindows(grok, 'detail').map((w) => w.key)).toEqual(['weekly', 'monthly'])
    const noWeekly = acct('n', 'ok', [win('session', 10, H), win('monthly', 70, 300 * H)])
    expect(barWindows(noWeekly, 'compact').map((w) => w.key)).toEqual(['monthly'])
    expect(barWindows(acct('x', 'none'), 'detail')).toEqual([])
  })

  it('触发窗口与最紧窗口', () => {
    const low = acct('low', 'low', [win('weekly', 41, 50 * H), win('session', 82, 2 * H)])
    expect(hotWindow(low)?.key).toBe('session')
    expect(tightest(low)?.key).toBe('session')
    expect(hotWindow(acct('ok', 'ok', [win('weekly', 10, H)]))).toBeUndefined()
  })

  it('状态短语：正常无标记；低额度带触发窗口；探测失败取短因；未探测', () => {
    expect(statusPhrase(acct('ok', 'ok', [win('weekly', 10, H)]))).toBeNull()
    expect(statusPhrase(acct('low', 'low', [win('session', 82.4, H)]))).toEqual({ tone: 'q-warn', text: '低额度 · 会话 82%' })
    expect(statusPhrase(acct('out', 'out', [win('session', 100, H)]))).toEqual({ tone: 'q-out', text: '无额度' })
    expect(statusPhrase(acct('f', 'fail', undefined, { last_error: { category: 'proxy', message: '代理 socks5://127.0.0.1:7897 拒绝连接' } }))).toEqual({ tone: 'q-mute', text: '△ 代理连接失败' })
    expect(statusPhrase(acct('n', 'none'))).toEqual({ tone: 'q-mute', text: '··· 未探测' })
  })

  it('探测失败短因按类别映射，不拼整句 message；缺类别/未知类别回「探测失败」（R8 修 1）', () => {
    const f = (category: string, message = '一整句很长的原因（含处置建议）') => failBrief(acct('f', 'fail', undefined, { last_error: { category, message } }))
    expect(f('credential_expired', 'opencode-go API key 已过期（在 OpenCode Console 新建 API key，写入该机 auth.json 的 opencode-go 条目）')).toBe('凭据已过期')
    expect(f('credential_missing')).toBe('凭据缺失')
    expect(f('credential_invalid')).toBe('凭据无效')
    expect(f('unauthorized')).toBe('凭据无效')
    expect(f('proxy')).toBe('代理连接失败')
    expect(f('network')).toBe('网络不通')
    expect(f('version_too_old')).toBe('版本过旧，已跳过')
    expect(f('cli_missing')).toBe('未安装 CLI')
    expect(f('rate_limited')).toBe('被限速')
    expect(f('upstream')).toBe('上游错误')
    expect(f('timeout')).toBe('探测超时')
    expect(f('no_subscription')).toBe('无订阅')
    expect(f('parse')).toBe('读数无法解析')
    expect(f('unsupported_platform')).toBe('本平台不支持')
    expect(f('unsupported_kind')).toBe('探测失败')
    expect(f('something_new')).toBe('探测失败')
    expect(failBrief(acct('f', 'fail'))).toBe('探测失败')
  })

  it('精简口径的每周窗口：多组 kind 取各组每周里已用最高的，并列取先重置的（R8 修 3）', () => {
    const agy = acct('agy', 'ok', [win('session', 22, 3 * H, 'Gemini'), win('weekly', 35, 126 * H, 'Gemini'),
      win('session', 61, 2 * H, 'Claude/GPT'), win('weekly', 48, 126 * H, 'Claude/GPT')])
    expect(windowLabel(weeklyWindow(agy)!)).toBe('Claude/GPT 周')
    expect(barWindows(agy, 'compact').map(windowLabel)).toEqual(['Claude/GPT 周'])
    const tie = acct('t', 'ok', [win('weekly', 40, 100 * H, 'A'), win('weekly', 40, 50 * H, 'B')])
    expect(weeklyWindow(tie)?.group).toBe('B')
    // 详细底栏仍取最短两个窗口（agy 即两个 5h），不受影响
    expect(barWindows(agy, 'detail').map(windowLabel)).toEqual(['Gemini 5h', 'Claude/GPT 5h'])
  })

  it('预计恢复读服务端 recover_at（越线窗口最晚重置），界面不另算；缺席即未知（审查 I2）', () => {
    // 5h 85% 2h 后重置 + 周 90% 5d 后重置：两个都越线，服务端给 5d
    const low = acct('low', 'low', [win('session', 85, 2 * H), win('weekly', 90, 120 * H)], { recover_at: iso(120 * H) })
    expect(recoverAt(low)).toBe(NOW + 120 * H)
    // 服务端没给（恢复未知）就是未知：不拿越线窗口的重置时刻顶替
    expect(recoverAt(acct('low2', 'low', [win('session', 85, 2 * H), win('weekly', 90)]))).toBeNull()
    // 标记无额度而窗口未越线 → 未知
    expect(recoverAt(acct('mark', 'out', [win('weekly', 10, 120 * H)]))).toBeNull()
    expect(recoverAt(acct('bad', 'out', undefined, { recover_at: 'not-a-time' }))).toBeNull()
  })

  it('登录状态词与提示：正常无提示；已过期只提示；已失效给恢复说明（S3）', () => {
    expect(loginStateLabel('ok')).toBe('正常')
    expect(loginStateHint('ok')).toBe('')
    expect(loginStateLabel('expired')).toBe('已过期')
    expect(loginStateHint('expired')).toContain('自行刷新')
    expect(loginStateLabel('gone')).toBe('已失效')
    expect(loginStateHint('gone')).toContain('重新登录后自动恢复')
  })

  it('凭据时刻：有到期读到期/已到期；apikey 无到期读「key 不过期」；其余读变更时刻（S3）', () => {
    const base: LoginView = { id: 'l', machine: 'm', home: '~', source: 's', fingerprint: 'sha256:0', state: 'ok', changed_at: iso(-H), carriers: [] }
    expect(loginCredText({ ...base, expires_at: iso(2 * H) }, 'grok', NOW)).toMatch(/^到期 /)
    expect(loginCredText({ ...base, expires_at: iso(-2 * H) }, 'grok', NOW)).toMatch(/^已于 .* 到期$/)
    expect(loginCredText(base, 'apikey', NOW)).toBe('key 不过期')
    expect(loginCredText(base, 'codex', NOW)).toBe('变更 1 小时前')
    expect(homeText('~')).toBe('~（主 HOME）')
    expect(homeText('~/.handoff/home/x')).toBe('~/.handoff/home/x')
  })

  it('重新登录命令：远端先 ssh 再执行，note 放括号；缺席为空串', () => {
    expect(loginCommandText({ ssh: 'ssh root@linux-01', run: 'HOME=/h codex login' })).toBe('ssh root@linux-01 后执行：HOME=/h codex login')
    expect(loginCommandText({ run: 'codex login', note: '无浏览器用 --device-auth' })).toBe('codex login（无浏览器用 --device-auth）')
    expect(loginCommandText(undefined)).toBe('')
  })

  it('六组 kind：只有支持探测的进底栏；claude/apikey 是 na 且状态短语弱前景（S7）', () => {
    expect(KIND_ORDER).toEqual(['opencode-go', 'codex', 'grok', 'agy', 'claude', 'apikey'])
    expect(supportsProbe('opencode-go')).toBe(true)
    expect(supportsProbe('claude')).toBe(false)
    expect(supportsProbe('apikey')).toBe(false)
    const accounts = [acct('a', 'ok'), acct('cl', 'na', undefined, { kind: 'claude' }), acct('k', 'na', undefined, { kind: 'apikey' })]
    expect(quotaAccounts(accounts).map((x) => x.name)).toEqual(['a'])
    expect(statusPhrase(accounts[1])).toEqual({ tone: 'q-mute', text: '不支持额度探测' })
    expect(updatedText(accounts[1], NOW)).toBe('不支持额度探测')
    expect(failBrief(acct('f', 'fail', undefined, { last_error: { category: 'no_login', message: '没有可用登录' } }))).toBe('没有可用登录')
  })

  it('账号机器一句话：登录去重、按登录顺序；无登录回「无登录」', () => {
    const login = (id: string, machine: string): LoginView => ({ id, machine, home: '~', source: 's', fingerprint: 'sha256:0', state: 'ok', changed_at: iso(0), carriers: [] })
    const a = acct('a', 'ok', undefined, { logins: [login('l1', 'linux-01'), login('l2', 'mac-mini'), login('l3', 'linux-01')] })
    expect(machinesText(a)).toBe('linux-01 · mac-mini')
    expect(machinesText(acct('b', 'ok'))).toBe('无登录')
  })

  it('派发提示：已失效暂停、已过期只提示、无额度/低额度暂停、na 照常、未探测额度未知', () => {
    const login = (state: string): LoginView => ({ id: 'l', machine: 'linux-01', home: '~/.handoff/home/x', source: 's', fingerprint: 'sha256:0', state, changed_at: iso(0), carriers: [] })
    const call = (a: AccountView, state = 'ok') => dispatchHint(a, login(state), NOW)!
    expect(call(acct('a', 'ok'), 'gone')).toMatchObject({ paused: true, relogin: true })
    expect(call(acct('a', 'ok'), 'gone').text).toContain('暂停派发：所用登录已失效')
    expect(call(acct('a', 'ok'), 'expired')).toMatchObject({ paused: false, relogin: true })
    expect(call(acct('a', 'out', [win('session', 100, 3 * H)], { recover_at: iso(3 * H) }))).toMatchObject({ paused: true })
    expect(call(acct('a', 'low', [win('session', 82, 2 * H)], { recover_at: iso(2 * H) })).text).toContain('已用 82%')
    expect(call(acct('a', 'na', undefined, { kind: 'claude' }))).toMatchObject({ paused: false })
    expect(call(acct('a', 'fail', undefined, { last_error: { category: 'proxy', message: 'x' } })).paused).toBe(false)
    expect(call(acct('a', 'none')).paused).toBe(false)
  })

  it('窗口名：agy 带模型组；六种 kind 固定顺序', () => {
    expect(windowLabel(win('session', 1))).toBe('会话 5h')
    expect(windowLabel(win('weekly', 1))).toBe('每周')
    expect(windowLabel(win('monthly', 1))).toBe('每月')
    expect(windowLabel(win('session', 1, undefined, 'Gemini'))).toBe('Gemini 5h')
    expect(windowLabel(win('weekly', 1, undefined, 'Claude/GPT'))).toBe('Claude/GPT 周')
    expect(KIND_ORDER).toHaveLength(6)
  })

  it('倒计时', () => {
    expect(countdown(-1)).toBe('即将')
    expect(countdown(30_000)).toBe('1m')
    expect(countdown(2 * H + 41 * 60_000)).toBe('2h41m')
    expect(countdown(98 * H)).toBe('4d 2h')
  })

  it('同机判据：名字相等；载体在本机别名时账号不在任何远端机上即算本机', () => {
    const remotes = ['linux-01']
    expect(sameMachine('linux-01', 'linux-01', remotes)).toBe(true)
    expect(sameMachine('mac-mini', 'linux-01', remotes)).toBe(false)
    expect(sameMachine('mac-mini', '本机', remotes)).toBe(true)
    expect(sameMachine('mac-mini', '', remotes)).toBe(true)
    expect(sameMachine('linux-01', 'local', remotes)).toBe(false)
  })
})
