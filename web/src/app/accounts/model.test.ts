// model.test.ts —— 账号额度展示规则（B453 S8–S10 / plan W4、W5）的核心方法。
// 边界：只测纯函数——排序、窗口分档、底栏两模式取哪个窗口、同机判据、倒计时；
// 渲染与色彩类落点由组件测试锁。
import { describe, expect, it } from 'vitest'
import type { AccountView, AccountWindowView } from '../../api/accounts'
import {
  barWindows, byProblem, countdown, failBrief, hotWindow, KIND_ORDER, recoverAt, sameMachine, statusPhrase, tightest,
  toneClass, weeklyWindow, windowLabel, winTone,
} from './model'

const NOW = Date.parse('2026-10-22T00:00:00Z')
const iso = (ms: number) => new Date(NOW + ms).toISOString()
const H = 3600_000
const win = (key: string, used: number, resetIn?: number, group?: string): AccountWindowView => ({
  key, used_percent: used, ...(resetIn === undefined ? {} : { resets_at: iso(resetIn) }), ...(group ? { group } : {}),
})
function acct(name: string, state: string, windows?: AccountWindowView[], extra: Partial<AccountView> = {}): AccountView {
  return {
    name, kind: 'codex', machine: 'm', home_dir: '~/h', interval_minutes: 15, warn_percent: 60, low_percent: 80,
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

  it('窗口名：agy 带模型组；四种 kind 固定顺序', () => {
    expect(windowLabel(win('session', 1))).toBe('会话 5h')
    expect(windowLabel(win('weekly', 1))).toBe('每周')
    expect(windowLabel(win('monthly', 1))).toBe('每月')
    expect(windowLabel(win('session', 1, undefined, 'Gemini'))).toBe('Gemini 5h')
    expect(windowLabel(win('weekly', 1, undefined, 'Claude/GPT'))).toBe('Claude/GPT 周')
    expect(KIND_ORDER).toEqual(['opencode-go', 'codex', 'grok', 'agy'])
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
