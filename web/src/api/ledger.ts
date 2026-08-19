// 账本 API 客户端：/api/cards、/api/flows、/api/decisions、/api/ledger/health。
// 与 client.ts 同一 request/postJSON 底座；类型字段名与 Go 侧 wire map 一字不差。
import { postJSON, request } from './client'

export interface CardView {
  id: string
  title: string
  status: string
  priority: string
  project: string
  parent: string
  base_branch: string
  attachments: { kind: string; path: string }[]
  following: string
  blocked: boolean
  blocked_by: string[]
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

export interface CardDetail {
  card: unknown
  relations: { From: string; To: string; Type: string }[]
  events: LedgerEvent[]
  task_states: { Target: string; TaskID: string; Purpose: string; LastType: string; LastSeq: number }[]
  effective_base_branch: string
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

export const fetchFlows = () => request<FlowsResp>('/api/flows')

export const fetchDecisions = (openOnly: boolean) =>
  request<{ decisions: Decision[] }>(`/api/decisions${openOnly ? '?open=1' : ''}`).then(
    (response) => response.decisions ?? [],
  )

export const answerDecision = (id: number, answer: string) =>
  postJSON<{ ok: boolean }>(`/api/decisions/${id}/answer`, { answer })

export const fetchLedgerHealth = () =>
  request<{ mirror: { Target: string; LastSeq: number; UpdatedAt: string }[] }>(
    '/api/ledger/health',
  )
