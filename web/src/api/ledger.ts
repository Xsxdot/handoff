// 账本 API 客户端：/api/cards、/api/flows、/api/decisions、/api/ledger/health。
// 与 client.ts 同一 request/postJSON 底座；类型字段名与 Go 侧 wire map 一字不差。
import { deleteJSON, patchJSON, postJSON, putJSON, request } from './client'

export interface CardView {
  id: string
  title: string
  status: string
  priority: string
  project: string
  workflow: string
  parent: string
  base_branch: string
  // 列表投影直接给出是否出现过 dispatched，避免建树弹层为每张卡拉详情事件流。
  base_frozen?: boolean
  attachments: Attachment[]
  following: string
  blocked: boolean
  blocked_by: string[]
  merged_count: number
  needs: string
  open_decisions: number
  children_total: number
  children_done: number // 已完结（含终止），与聚合闸同一把尺
  conflict: boolean
  open_tickets: number
}

export interface Attachment {
  kind: string
  path: string
}

export interface Card {
  id: string
  title: string
  status: string
  terminate_reason?: string
  priority: string
  project: string
  parent: string
  workflow: string
  workflow_version: number
  attachments?: Attachment[]
  acceptance_criteria?: string
  base_branch?: string
  driver_session?: string
  driver_heartbeat_at?: string
  created_at: string
  updated_at: string
}

export interface Relation {
  From: string
  To: string
  Type: string
  CreatedAt: string
}

export interface TaskStateRow {
  Target: string
  TaskID: string
  Purpose: string
  LastType: string
  LastSeq: number
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
  card: Card
  relations: Relation[]
  events: LedgerEvent[]
  task_states: TaskStateRow[]
  effective_base_branch: string
  decisions: Decision[] | null
  needs: string
  // children 是直接子卡（只一层）。可选而非必填：抽屉对每个列表都用 `?? []`
  // 防御性读取，标成必填只会逼着六处与子任务无关的测试 mock 补一个空数组。
  children?: CardBrief[] | null
}

// NodeOverride 节点对模板的单字段覆盖；省略的字段 = 沿用模板。
// 字段名与 Go 侧 ledger.NodeOverride 一字不差。
export interface NodeOverride {
  executor?: string
  discipline?: string
  target?: string
  model?: string
  // purpose 按节点覆盖模板派发用途（如 implement / review）。
  purpose?: string
}

export interface Gate {
  require_attachment?: string
  require_acceptance?: boolean
  require_children_done?: boolean
}

// NodeOutput 是节点 pass 后由协调者校验并挂载的单一附件声明。
// 可选字段由 NodeDef 指针投影而来；缺失表示旧工作流没有产出声明。
export interface NodeOutput {
  kind: string
  path: string
}

// NodeDef 工作流的一个节点：看板的一列 + 卡走到这列时的执行规矩。
// 字段名与 Go 侧 ledger.NodeDef 一字不差。
export interface NodeDef {
  name: string
  template?: string
  override?: NodeOverride
  dispatch?: boolean
  verdict?: boolean
  carry_card_context?: boolean
  max_rounds?: number
  // omit_acceptance 关闭本节点的整卡验收判据注入。
  omit_acceptance?: boolean
  next?: string
  on_fail?: string
  gate?: Gate
  human_bases?: string[]
  produces?: NodeOutput
}

export interface FlowDetail {
  name: string
  version: number
  nodes: NodeDef[]
  states: string[]
}

export interface NewCardReq {
	title: string
	project: string
  workflow?: string
  priority?: string
  parent?: string
	base_branch?: string
}

// CardStepReq 是卡节点提交的 wire 请求镜像；可选覆盖项缺席表示沿用下层配置。
// PlanPath 及任何调用方本地文件字段不属于 --step 请求。
export interface CardStepReq {
  step: string
  target?: string
  executor?: string
  model?: string
  extra?: string
  actor: string
}

export interface CardCreateResp {
  id: string
}

export interface MigrateCardReq {
  workflow: string
  status: string
  version?: number
}

export interface CardWorkflowLocation {
  id: string
  workflow: string
  workflow_version: number
  status: string
}

export interface MigrateCardResp {
  ok: boolean
  id: string
  from: CardWorkflowLocation
  to: CardWorkflowLocation
  event: LedgerEvent
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
  answered_by?: string
  created_at?: string
  answered_at?: string
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

// runCardStep 发起一个卡环节。step 是**节点名**（= 看板列名），由卡钉住的
// 工作流决定，不再是写死的 review|merge。后端受理即返回——环节要跑几分钟到
// 几十分钟，这个 Promise resolve 只代表「收到了」，进展看卡的事件流。
export const runCardStep = (id: string, step: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/step`, { step })

// createCard 建卡。base_branch 只在建卡时能给，之后不可改。
export const createCard = (req: NewCardReq) =>
  postJSON<CardCreateResp>('/api/cards', req)

export const migrateCard = (id: string, req: MigrateCardReq) =>
  postJSON<MigrateCardResp>(`/api/cards/${encodeURIComponent(id)}/migrate`, req)

// CardPatch 的四个字段**全部可选，缺席即「不动该字段」**（不是置空）。
// 调用方只放要改的键，别为了「补全」而把现值原样塞回去——那会在没改动的
// 字段上也落一条事件。
export interface CardPatch {
  title?: string
  priority?: string
  acceptance_criteria?: string
  base_branch?: string
}

export const patchCard = (id: string, patch: CardPatch) =>
  patchJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}`, patch)

export const attachFile = (id: string, kind: string, path: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/attachments`, { kind, path })

export const detachFile = (id: string, path: string) =>
  deleteJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/attachments`, { path })

// clearCardNeeds 人工撤回卡上的「等人」标记。无条件清除——人对任何来源的
// 标记都有处置权；环节只能撤自己打的那条，那条逻辑在后端。
export const clearCardNeeds = (id: string) =>
  postJSON<{ ok: boolean }>(`/api/cards/${encodeURIComponent(id)}/needs/clear`, {})

export const fetchFlows = () => request<FlowsResp>('/api/flows')

export const fetchFlow = (name: string) =>
  request<FlowDetail>(`/api/flows/${encodeURIComponent(name)}`)

// putFlow 发布该工作流的**下一个版本**。工作流不可变版本化——保存不是「改」，
// 已钉在老版本上的卡完全不受影响。
export const putFlow = (name: string, nodes: NodeDef[]) =>
  putJSON<{ name: string; version: number }>(`/api/flows/${encodeURIComponent(name)}`, { nodes })

export const fetchDisciplineNames = () =>
  request<{ names: string[] }>('/api/disciplines').then((response) => response.names ?? [])

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
