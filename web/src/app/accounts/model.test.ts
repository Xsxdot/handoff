// model.test.ts —— 账号额度展示规则（B453 S8–S10 / plan W4、W5）的核心方法。
// 边界：只测纯函数——排序、窗口分档、底栏两模式取哪个窗口、同机判据、倒计时；
// 渲染与色彩类落点由组件测试锁。
import { describe, expect, it } from 'vitest'
import type { AccountView, AccountWindowView } from '../../api/accounts'
import {
  barWindows, byProblem, countdown, hotWindow, KIND_ORDER, sameMachine, statusPhrase, tightest,
  toneClass, windowLabel, winTone,
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
    expect(statusPhrase(acct('f', 'fail', undefined, { last_error: { category: 'proxy', message: '代理拒绝连接' } }))).toEqual({ tone: 'q-mute', text: '△ 代理拒绝连接' })
    expect(statusPhrase(acct('n', 'none'))).toEqual({ tone: 'q-mute', text: '··· 未探测' })
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
