// accounts.ts —— 账号与额度 wire 的唯一 web fetch 面（B453 spec §8 F1–F10）。
//
// 职责：镜像 internal/proto/scheduling.go 的账号类型（AccountsResp / AccountView /
// AccountInput / AccountDeleteRefusal），并提供 GET/PUT/DELETE/立即探测四个调用。
// testdata/Accounts*.json + contract.test.ts 钉住线格式。
//
// 边界：
//   - 不持有业务状态、不推导状态档位：state 由服务端推导（F6），界面只读它；
//     窗口分档与排序在 app/accounts/model.ts
//   - 凭据值永不出现在这里（F10）；proxy 是服务端打码后的回显，原样回传即「保留原值」
import { putJSON, request } from './client'

/** F7：账号 kind。 */
export type AccountKind = 'opencode-go' | 'codex' | 'grok' | 'agy'
/** F6：ok 正常 / low 低额度 / out 无额度 / fail 探测失败 / none 未探测。 */
export type AccountState = 'ok' | 'low' | 'out' | 'fail' | 'none'
/** F8：窗口键。 */
export type WindowKey = 'session' | 'weekly' | 'monthly'

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

/** F5：账号读面。可缺席键：entry（agy）、proxy、reading、last_attempt_at、last_error。 */
export interface AccountView {
  name: string
  kind: AccountKind | string
  machine: string
  home_dir: string
  entry?: string
  proxy?: string
  interval_minutes: number
  warn_percent: number
  low_percent: number
  version: number
  state: AccountState | string
  stale: boolean
  reading?: AccountReadingView
  last_attempt_at?: string
  last_error?: AccountProblemView
  carriers: string[]
}

/** F1：GET /api/accounts 响应。 */
export interface AccountsResp {
  accounts: AccountView[]
}

/** F9：PUT 请求体；名称取路径；数值键缺席 = 服务端取缺省。 */
export interface AccountInput {
  kind: string
  machine: string
  home_dir: string
  entry?: string
  proxy?: string
  interval_minutes?: number
  warn_percent?: number
  low_percent?: number
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

/** F2：按名称与 CAS 版本登记/编辑；expect=0 新建。返回含新版本与读数的 AccountView。 */
export const putAccount = (name: string, expect: number, input: AccountInput) =>
  putJSON<AccountView>(`${path(name)}?expect=${expect}`, input)

/** F3：按 CAS 版本删除；被引用时抛 ApiError(409)，body 为 AccountDeleteRefusal。 */
export const deleteAccount = (name: string, expect: number) =>
  request<{ name: string; version: number }>(`${path(name)}?expect=${expect}`, { method: 'DELETE' })

/** F4：立即探测（入口机转发到所在机），返回本次探测后的 AccountView（失败写在 last_error）。 */
export const probeAccount = (name: string) =>
  request<AccountView>(`${path(name)}/probe`, { method: 'POST' })
