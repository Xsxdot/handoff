// testAccounts.ts —— 账号组件测试共用的样本（只供 *.test.tsx 引用，生产代码不得 import）。
// 形状照 GET /api/accounts（B464 A2/A4）：机器/HOME/来源/指纹/代理在登录上；覆盖六种 kind、
// 五档账号 state、登录 ok/expired/gone、agy 模型组、探测失败有/无旧读数。
import type { AccountView, AccountWindowView } from '../../api/accounts'

export const H = 3600_000
const at = (ms: number) => new Date(Date.now() + ms).toISOString()
export const w = (key: string, used: number, resetIn: number, group?: string): AccountWindowView =>
  ({ key, used_percent: used, resets_at: at(resetIn), ...(group ? { group } : {}) })

/** 登录位置（B464：机器/HOME/代理在登录上）；缺省 HOME = `~/.handoff/home/<name>`。 */
interface Place { home?: string; proxy?: string; state?: string; carriers?: string[] }

function base(name: string, kind: string, machine: string, state: string, extra: Partial<AccountView>, place: Place = {}): AccountView {
  const home = place.home ?? `~/.handoff/home/${name}`
  const login = {
    id: `login-${name}`, machine, home,
    source: kind === 'agy' ? 'agy' : kind === 'claude' ? 'claude-auth' : 'auth.json', fingerprint: 'sha256:000000000000',
    state: place.state ?? 'ok', changed_at: at(-H), carriers: place.carriers ?? extra.carriers ?? [],
    relogin: {
      ...(machine === 'linux-01' ? { ssh: 'ssh root@linux-01' } : {}),
      run: `${home === '~' ? '' : `HOME=${home} `}${kind === 'apikey' ? 'opencode auth login -p deepseek' : `${kind === 'opencode-go' ? 'opencode console login' : `${kind === 'claude' ? 'claude auth login' : `${kind} login`}`}`}`,
      ...(kind === 'codex' ? { remote_run: `${home === '~' ? '' : `HOME=${home} `}codex login --device-auth` } : {}),
    },
    ...(place.proxy ? { proxy: place.proxy } : {}),
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
    base('og-main', 'opencode-go', 'linux-01', 'ok', { label: 'dev@example.com', carriers: ['mimo'], reading: reading([w('session', 34, 2 * H + 41 * 60_000), w('weekly', 62, 98 * H + 30 * 60_000), w('monthly', 22, 400 * H)], { plan_type: 'Go', fingerprint: 'sha256:3f9a1b2cc21e' }) }),
    base('og-team', 'opencode-go', 'linux-01', 'out', { carriers: ['dp'], recover_at: at(3 * H), reading: reading([w('session', 100, 3 * H), w('weekly', 71, 33 * H)]) }),
    base('og-mini', 'opencode-go', 'mac-mini', 'fail', { last_error: { category: 'proxy', message: '代理 socks5://127.0.0.1:7897 拒绝连接' }, reading: reading([w('session', 51, H), w('weekly', 63, 120 * H)]) }, { proxy: 'socks5://127.0.0.1:7897' }),
    base('codex-plus', 'codex', 'macbook-pro', 'low', { carriers: ['mbp-codex'], recover_at: at(2 * H), reading: reading([w('session', 82, 2 * H), w('weekly', 41, 77 * H)], { plan_type: 'Plus' }) }),
    base('codex-spare', 'codex', 'linux-01', 'none', { last_attempt_at: undefined }, { state: 'gone' }),
    base('grok-mbp', 'grok', 'macbook-pro', 'fail', { last_error: { category: 'credential_expired', message: 'grok 凭据已过期，等待 grok CLI 刷新（在该机执行 grok login）' } }, { state: 'expired' }),
    base('agy-main', 'agy', 'linux-01', 'ok', { reading: reading([w('session', 22, 3 * H, 'Gemini'), w('weekly', 35, 126 * H, 'Gemini'), w('session', 61, 2 * H, 'Claude/GPT'), w('weekly', 48, 126 * H, 'Claude/GPT')]) }, { home: '/root/.handoff/home/agy' }),
    claudeAccount(),
    apiKeyAccount(),
  ]
}

/**
 * ignoredAccount B467：已忽略账号样本（`ignored` 在场）。各消费点应把它从可见列表里排除，
 * 只有账号页底部「已忽略」区读它。
 */
export function ignoredAccount(): AccountView {
  const a = base('og-hidden', 'opencode-go', 'macbook-pro', 'ok', {
    label: 'hidden@example.com',
    reading: reading([w('session', 12, 3 * H), w('weekly', 24, 100 * H)]),
  })
  return { ...a, ignored: { at: at(-3 * H), by: 'linux-01' } }
}

// claude：两台 Mac 主 HOME 各一条登录（同一身份、不支持额度探测）。
function claudeAccount(): AccountView {
  const a = base('claude-main', 'claude', 'mac-mini', 'na', { label: 'xsx@example.com', carriers: ['cl'] }, { home: '~', carriers: ['cl'], state: 'expired' })
  return { ...a, logins: [a.logins[0], { ...a.logins[0], id: 'login-claude-main-mbp', machine: 'macbook-pro' }] }
}

// 第三方 key：linux-01 与 mac-mini 各一条登录（同 key、不支持额度探测）。
function apiKeyAccount(): AccountView {
  const a = base('deepseek-key', 'apikey', 'linux-01', 'na', { provider: 'deepseek', carriers: ['ds'] }, { carriers: ['ds'] })
  return { ...a, logins: [a.logins[0], { ...a.logins[0], id: 'login-deepseek-key-mini', machine: 'mac-mini', home: '~' }] }
}
