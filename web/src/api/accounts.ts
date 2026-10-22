// accounts.ts —— 账号与额度 wire 的唯一 web fetch 面（B453 spec §8 F1–F10 / B464 A1–A12）。
//
// 职责：镜像 internal/proto/scheduling.go 的账号类型（AccountsResp / AccountView / LoginView /
// AccountInput / LoginProxyInput / 新增登录与立即发现），并提供读、编辑、删除、探测、代理、
// 新增登录（建 HOME / 检查）与立即发现调用。testdata/Accounts*.json + contract.test.ts 钉住线格式。
//
// 边界：
//   - 不持有业务状态、不推导状态档位：state 由服务端推导（F6），界面只读它；
//     窗口分档与排序在 app/accounts/model.ts
//   - 凭据值永不出现在这里（F10）；proxy 是服务端打码后的回显，原样回传即「保留原值」
//   - relogin 是服务端给的登录命令（ssh / run / note），前端只展示与复制，不拼接
import { postJSON, putJSON, request } from './client'

/** F7 / B464 A3：账号 kind。 */
export type AccountKind = 'opencode-go' | 'codex' | 'grok' | 'agy' | 'claude' | 'apikey'
/** F6 / B464 A3：ok 正常 / low 低额度 / out 无额度 / fail 探测失败 / none 未探测 / na 不支持额度。 */
export type AccountState = 'ok' | 'low' | 'out' | 'fail' | 'none' | 'na'
/** F8：窗口键。 */
export type WindowKey = 'session' | 'weekly' | 'monthly'
/** B464 A4：登录状态。 */
export type LoginState = 'ok' | 'expired' | 'gone'

/** F8：一个额度窗口；group 仅 agy 模型组；resets_at 可缺席。 */
export interface AccountWindowView {
  key: WindowKey | string
  group?: string
  used_percent: number
  resets_at?: string
}

/** 窗口之外的额外额度（codex credits、grok 预付/按需），各键可缺席。 */
export interface AccountExtraUsageView {
  unit?: string
  balance?: number
  used?: number
  limit?: number
  unlimited?: boolean
}

/** F8：最近一次成功读数。 */
export interface AccountReadingView {
  windows: AccountWindowView[]
  plan_type?: string
  extra_usage?: AccountExtraUsageView
  fingerprint?: string
  probed_at: string
}

/** last_error：失败（或接口明示不可用）的类别与短因。 */
export interface AccountProblemView {
  category: string
  message: string
}

/** B464 A4：重新登录命令；run 带 HOME=<登录 HOME> 前缀，ssh 仅入口机可直连的远端给。 */
export interface LoginCommand {
  ssh?: string
  run: string
  note?: string
}

/** B464 A4：账号的一条登录（机器 × HOME × 来源）。proxy 已打码；expires_at 可缺席。 */
export interface LoginView {
  id: string
  machine: string
  home: string
  source: string
  fingerprint: string
  state: LoginState | string
  changed_at: string
  expires_at?: string
  proxy?: string
  relogin?: LoginCommand
  carriers: string[]
}

/** B464 A5：一台机器的「上次发现」；at 缺席 = 从未发现。 */
export interface DiscoveryView {
  machine: string
  self: boolean
  at?: string
  error?: string
}

/** B464 A2：账号读面（账号 = 身份；machine/home_dir/entry/proxy 移到登录上）。 */
export interface AccountView {
  id: string
  name: string
  kind: AccountKind | string
  provider?: string
  label?: string
  fingerprint: string
  interval_minutes?: number
  warn_percent?: number
  low_percent?: number
  version: number
  state: AccountState | string
  stale: boolean
  reading?: AccountReadingView
  last_attempt_at?: string
  last_error?: AccountProblemView
  /** 预计离开低额度/无额度的时刻（审查第 1 轮 I2 增补）：服务端推导，越线窗口全部重置即最晚那个；不阻挡或未知时缺席。 */
  recover_at?: string
  /** B464：读数来源的可探登录 id；无正常登录时缺席。 */
  probe_login?: string
  logins: LoginView[]
  carriers: string[]
}

/** F1 / B464 A1：GET /api/accounts 响应。 */
export interface AccountsResp {
  accounts: AccountView[]
  discovery: DiscoveryView[]
}

/** B464 A6：PUT 编辑体（账号只由发现建，expect=0 → 400）；键缺席 = 保留现值。 */
export interface AccountInput {
  name?: string
  interval_minutes?: number
  warn_percent?: number
  low_percent?: number
}

/** B464 A9：登录代理请求体；proxy 必给，空串 = 清除。 */
export interface LoginProxyInput {
  proxy: string
}

/** B464 A11：建 HOME 回执；home 是机器无关写法，command 是该机执行命令。 */
export interface LoginHomeResp {
  home: string
  command: LoginCommand
}

/** B464 A12：检查 HOME 的一条识别结果。 */
export interface LoginCheckItem {
  login: string
  account: string
  account_name: string
  new_account: boolean
  account_logins: number
}

/** B464 A12：检查 HOME 回执；尚未识别为 []。 */
export interface LoginCheckResp {
  logins: LoginCheckItem[]
}

/** B464 A10：一台机器的立即发现结果。 */
export interface MachineDiscoverResult {
  machine: string
  ok: boolean
  error?: string
  at?: string
}

/** B464 A10：立即发现回执。 */
export interface AccountDiscoverResp {
  machines: MachineDiscoverResult[]
}

/** F3：被载体引用拒删时的 409 体。 */
export interface AccountDeleteRefusal {
  error: string
  carriers: string[]
}

const path = (name: string) => `/api/accounts/${encodeURIComponent(name)}`

/** F1：读全部账号；空库为 []。 */
export const getAccounts = (signal?: AbortSignal) =>
  request<AccountsResp>('/api/accounts', signal ? { signal } : undefined)

/** F2：按名称与 CAS 版本登记/编辑；expect=0 新建（服务端 400）。返回含新版本与读数的 AccountView。 */
export const putAccount = (name: string, expect: number, input: AccountInput) =>
  putJSON<AccountView>(`${path(name)}?expect=${expect}`, input)

/** F3：按 CAS 版本删除；被引用时抛 ApiError(409)，body 为 AccountDeleteRefusal。 */
export const deleteAccount = (name: string, expect: number) =>
  request<{ name: string; version: number }>(`${path(name)}?expect=${expect}`, { method: 'DELETE' })

/** F4：立即探测（入口机转发到可探登录所在机），返回本次探测后的 AccountView（失败写在 last_error）。 */
export const probeAccount = (name: string) =>
  request<AccountView>(`${path(name)}/probe`, { method: 'POST' })

/** B464 A9：设置/清除一条登录的代理；服务端读-合并-CAS，打码回填保留原值。 */
export const putLoginProxy = (id: string, proxy: string) =>
  putJSON<LoginView>(`/api/logins/${encodeURIComponent(id)}/proxy`, { proxy } satisfies LoginProxyInput)

/** B464 A11：在目标机建一个新 HOME（`~/.handoff/home/<dir>`）并返回登录命令。 */
export const createLoginHome = (machine: string, agent: string, dir: string) =>
  postJSON<LoginHomeResp>(`/api/logins/home?machine=${encodeURIComponent(machine)}`, { agent, dir })

/** B464 A12：让目标机对这个 HOME 跑一次单 HOME 发现并返回识别结果。 */
export const checkLoginHome = (machine: string, home: string) =>
  postJSON<LoginCheckResp>(`/api/logins/check?machine=${encodeURIComponent(machine)}`, { home })

/** B464 A10：触发立即发现；machine 缺席 = 入口机向全部已登记机器扇出。 */
export const discoverAccounts = (machine?: string) =>
  postJSON<AccountDiscoverResp>(
    `/api/accounts/discover${machine ? `?machine=${encodeURIComponent(machine)}` : ''}`,
    {},
  )
