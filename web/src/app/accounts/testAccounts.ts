// testAccounts.ts —— 账号组件测试共用的样本（只供 *.test.tsx 引用，生产代码不得 import）。
// 形状照 GET /api/accounts（F5 / B464 A2：机器/HOME/代理在登录上）；覆盖五档 state、agy 模型组、探测失败有/无旧读数。
import type { AccountView, AccountWindowView } from '../../api/accounts'

export const H = 3600_000
const at = (ms: number) => new Date(Date.now() + ms).toISOString()
export const w = (key: string, used: number, resetIn: number, group?: string): AccountWindowView =>
  ({ key, used_percent: used, resets_at: at(resetIn), ...(group ? { group } : {}) })

/** 登录位置（B464：机器/HOME/代理在登录上）；缺省 HOME = `~/.handoff/home/<name>`。 */
interface Place { home?: string; proxy?: string }

function base(name: string, kind: string, machine: string, state: string, extra: Partial<AccountView>, place: Place = {}): AccountView {
  const login = {
    id: `login-${name}`, machine, home: place.home ?? `~/.handoff/home/${name}`,
    source: kind === 'agy' ? 'agy' : 'auth.json', fingerprint: 'sha256:000000000000', state: 'ok',
    changed_at: at(-H), carriers: extra.carriers ?? [], ...(place.proxy ? { proxy: place.proxy } : {}),
  }
  return {
    id: `${kind}:${name}`, name, kind, fingerprint: 'sha256:000000000000', interval_minutes: 15, warn_percent: 60, low_percent: 80,
    version: 2, state, stale: false, carriers: [], logins: [login], probe_login: login.id,
    last_attempt_at: at(-2 * 60_000), ...extra,
  }
}
const reading = (windows: AccountWindowView[], extra: object = {}) => ({ windows, probed_at: at(-2 * 60_000), ...extra })

export function sampleAccounts(): AccountView[] {
  return [
    base('og-main', 'opencode-go', 'linux-01', 'ok', { carriers: ['mimo'], reading: reading([w('session', 34, 2 * H + 41 * 60_000), w('weekly', 62, 98 * H + 30 * 60_000), w('monthly', 22, 400 * H)], { plan_type: 'Go', fingerprint: 'sha256:3f9a1b2cc21e' }) }),
    base('og-team', 'opencode-go', 'linux-01', 'out', { carriers: ['dp'], recover_at: at(3 * H), reading: reading([w('session', 100, 3 * H), w('weekly', 71, 33 * H)]) }),
    base('og-mini', 'opencode-go', 'mac-mini', 'fail', { last_error: { category: 'proxy', message: '代理 socks5://127.0.0.1:7897 拒绝连接' }, reading: reading([w('session', 51, H), w('weekly', 63, 120 * H)]) }, { proxy: 'socks5://127.0.0.1:7897' }),
    base('codex-plus', 'codex', 'macbook-pro', 'low', { carriers: ['mbp-codex'], recover_at: at(2 * H), reading: reading([w('session', 82, 2 * H), w('weekly', 41, 77 * H)], { plan_type: 'Plus' }) }),
    base('codex-spare', 'codex', 'linux-01', 'none', { last_attempt_at: undefined }),
    base('grok-mbp', 'grok', 'macbook-pro', 'fail', { last_error: { category: 'credential_expired', message: 'grok 凭据已过期，等待 grok CLI 刷新（在该机执行 grok login）' } }),
    base('agy-main', 'agy', 'linux-01', 'ok', { reading: reading([w('session', 22, 3 * H, 'Gemini'), w('weekly', 35, 126 * H, 'Gemini'), w('session', 61, 2 * H, 'Claude/GPT'), w('weekly', 48, 126 * H, 'Claude/GPT')]) }, { home: '/root/.handoff/home/agy' }),
  ]
}
