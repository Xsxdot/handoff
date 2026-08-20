// 账本 API 客户端：/api/cards、/api/flows、/api/decisions、/api/ledger/health。
// 与 client.ts 同一 request/postJSON 底座；类型字段名与 Go 侧 wire map 一字不差。
import { postJSON, request } from './client'

export interface CardView {
  id: string
  title: string
  status: string
  priority: string
  project: string
  workflow: string
  parent: string
  base_branch: string
  attachments: { kind: string; path: string }[]
  following: string
  blocked: boolean
  blocked_by: string[]
  merged_count: number
  needs: string
  open_decisions: number
  conflict: boolean
  open_tickets: number
}

export interface UnlinkedSummary {
  count: number
  tasks: { target: string; task_id: string; title: string; state: string }[] | null
  unknown_targets: string[] | null
}

// CardBrief 是子任务区一行的最小三元组，字段名与 Go 侧 ledger.CardBrief 一字不差。
export interface CardBrief {
  id: string
  title: string
  status: string
}

export interface CardDetail {
  card: unknown
  relations: { From: string; To: string; Type: string }[]
  events: LedgerEvent[]
  task_states: { Target: string; TaskID: string; Purpose: string; LastType: string; LastSeq: number }[]
  effective_base_branch: string
  decisions: Decision[] | null
  needs: string
  // children 是直接子卡（只一层）。可选而非必填：抽屉对每个列表都用 `?? []`
  // 防御性读取，标成必填只会逼着六处与子任务无关的测试 mock 补一个空数组。
  children?: CardBrief[] | null
}

export interface LedgerEvent {
  seq: number
  card_id: string
  type: string
  actor: string
  payload: unknown
  created_at: string
}

export interface Decision {
  id: number
  card_id: string
  body: string
  options: string[] | null
  status: string
  answer: string
  created_by?: string
}

export interface WorkflowWire {
  name: string
  version: number
  def: { states: string[]; gates?: Record<string, unknown> }
}

export interface TemplateWire {
  name: string
  version: number
  def: Record<string, unknown>
}

export interface FlowsResp {
  workflows: WorkflowWire[]
  templates: TemplateWire[]
}

export const fetchCards = (params = '') =>
  request<{ cards: CardView[]; unlinked: UnlinkedSummary }>(
    `/api/cards${params ? `?${params}` : ''}`,
  )

export const fetchCardDetail = (id: string) =>
  request<CardDetail>(`/api/cards/${encodeURIComponent(id)}`)

export const moveCard = (id: string, to: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/move`, { to })

export const noteCard = (id: string, body: string, kind = '普通') =>
  postJSON<LedgerEvent>(`/api/cards/${encodeURIComponent(id)}/note`, { body, kind })

// acceptCard 记一条「已真机验」。证据由后端强制非空（与 CLI card accept 同规则），
// 前端只是不让空提交，不是唯一的那道门。
export const acceptCard = (id: string, evidence: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/accept`, { evidence })

// runCardStep 发起一个卡环节。后端受理即 202——环节要跑几分钟到几十分钟，
// 这个 Promise resolve 只代表「收到了」，进展看卡的事件流。
export const runCardStep = (id: string, step: 'review' | 'merge') =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/step`, { step })

export const fetchFlows = () => request<FlowsResp>('/api/flows')

export const fetchDecisions = (openOnly: boolean) =>
  request<{ decisions: Decision[] }>(`/api/decisions${openOnly ? '?open=1' : ''}`).then(
    (response) => response.decisions ?? [],
  )

export const answerDecision = (id: number, answer: string) =>
  postJSON<{ ok: boolean }>(`/api/decisions/${id}/answer`, { answer })

export interface LedgerHealth {
  // enabled 账本域总开关。未启用时后端只回这一个字段（恒 200，不是 503——
  // 503 在浏览器侧与网络错无法区分，那样前端只能靠猜）。
  enabled: boolean
  mirror?: { Target: string; LastSeq: number; UpdatedAt: string }[]
}

export const fetchLedgerHealth = () =>
  request<LedgerHealth>('/api/ledger/health')
