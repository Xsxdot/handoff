// 协作房间域（B156.2）的 TS 契约镜像。与 internal/proto/rooms.go 逐字段
// 对应；形状由 Go 金样本（internal/proto/rooms_fixture_test.go）与本目录
// testdata/RoomsFixture.json 孪生锁定，改形状先回 contract 节点。
import type { LedgerEvent, SeatMode } from './ledger'

export const ROOM_MSG_ESCALATION = 'escalation'
export const ROOM_MSG_DEVIATION = 'deviation'
export const ROOM_MSG_CLOSING = 'closing'
export const ROOM_MSG_RELAY = 'relay'
export const ROOM_MSG_REPLY = 'reply'
export const ROOM_MSG_USER = 'user'
export const ROOM_MSG_POINTER = 'pointer'

export type RoomMsgKind =
  | 'escalation'
  | 'deviation'
  | 'closing'
  | 'relay'
  | 'reply'
  | 'user'
  | 'pointer'

// RoomMessage 是 room_message 账本事件的载荷 schema。
export interface RoomMessage {
  room: string
  kind: RoomMsgKind
  body: string
  refs?: string[]
  mentions?: string[]
  decision_id?: number
  by_system?: boolean
  reply_to?: number // 被回复消息的账本 seq；隐式寻址原作者；omitempty——缺键≠0
  // device 端戳（B358.9）：发送端登记的设备名，只用于落款显示；不参与任何判定。
  // 缺失省键——少的是落款，不是权力。
  device?: string
}

export const INBOX_ORIGIN_DECISION = 'decision'
export const INBOX_ORIGIN_TICKET = 'ticket'
export const INBOX_ORIGIN_MENTION = 'mention'

export type InboxOrigin = 'decision' | 'ticket' | 'mention'

// InboxItem 是待回复收件箱的聚合条目（三源：open 裁决 / 兜底工单 / @提及）。
export interface InboxItem {
  origin: InboxOrigin
  title: string
  card_id?: string
  ref_id: string
  payload?: unknown
}

// RoomAttach 是房间详情可执行的任务 attach 投影；target 缺席表示当前 agentd。
export interface RoomAttach {
  target?: string
  task_id: string
  work_dir: string
  command: string
}

// RoomPreview 是服务端随会话列表投影的最后一条消息摘要；列表不再逐房间读历史。
export interface RoomPreview {
  body: string
  seq: number
  created_at: string
}

// RoomSummary 是会话列表（扁平活动排序）的单行。
export interface RoomSummary {
  id: string
  kind: 'card' | 'project' | 'global'
  project?: string
  title: string
  bound_session?: string
  live: boolean
  read_only: boolean
  last_activity: string
  unread: number
  attach?: RoomAttach
  preview?: RoomPreview
}

/**
 * 消息收件地址集为空的原因（B435 ⑤ C-509；与 proto.NoRecipientReason 逐值同序，
 * B435Vocab.json 夹具对照）。读时求值（D-E）：同一条消息随席位/驻场者变化可能不再出键。
 */
export type NoRecipientReason =
  | 'structural'
  | 'not_session_room'
  | 'non_human_unaddressed'
  | 'addressed_unresolved'
  | 'root_no_seat'
  | 'needs_mention'

/** NoRecipientReason 词表（只读，顺序 = Go 常量声明序）。 */
export const NO_RECIPIENT_REASONS: readonly NoRecipientReason[] = [
  'structural',
  'not_session_room',
  'non_human_unaddressed',
  'addressed_unresolved',
  'root_no_seat',
  'needs_mention',
]

/** isNoRecipientReason 判 wire 值是否在词表内；空串与词表外值 false。 */
export function isNoRecipientReason(value: unknown): value is NoRecipientReason {
  return typeof value === 'string' && (NO_RECIPIENT_REASONS as readonly string[]).includes(value)
}

/**
 * 房间历史视图一行（GET /api/rooms/{id}/messages 的 messages[]）：账本事件平铺 +
 * 可省略的 no_recipient（有收件人、非 room_message 或服务端判定读失败时缺键，C-507）。
 */
export interface RoomHistoryItem extends LedgerEvent {
  no_recipient?: NoRecipientReason
}

// ---- B358 会话（群）域 wire 形状（§3.2 键集；与 internal/proto/sessions.go 逐字段对应，
// 孪生金样本 = testdata/RoomsFixture.json + internal/proto/sessions_fixture_test.go，改形状先回 contract）----

export const SESSION_KIND = 'session'

/** 成员种类（B435 增 resident = 房间驻场者，无 card_id；与 proto.SessionMember* 逐值同序）。 */
export type SessionMemberKind = 'human' | 'agent' | 'seat' | 'resident'

/** SessionMemberKind 词表（只读，顺序 = Go 常量声明序）。 */
export const SESSION_MEMBER_KINDS: readonly SessionMemberKind[] = ['human', 'agent', 'seat', 'resident']

/** isSessionMemberKind 判 wire 值是否在成员种类词表内。 */
export function isSessionMemberKind(value: unknown): value is SessionMemberKind {
  return typeof value === 'string' && (SESSION_MEMBER_KINDS as readonly string[]).includes(value)
}
// 「看板不说谎」：与 proto.SessionMember* 状态词表逐值一致（sessions_fixture_test.go 冻结）。
export type SessionMemberStatus = 'working' | 'listening' | 'last_active' | 'empty'

// B444：驻场者驱动形态词表（proto.ResidentDriver*）。desktop 驻场者在线上是空串 → omitempty 缺键，
// 故词表只有 push；缺键即 desktop（旧行），不得把空串当词表值（缺陷族 R10）。
export const RESIDENT_DRIVER_MODES = ['push'] as const
export type ResidentDriverMode = (typeof RESIDENT_DRIVER_MODES)[number]
export function isResidentDriverMode(value: unknown): value is ResidentDriverMode {
  return typeof value === 'string' && (RESIDENT_DRIVER_MODES as readonly string[]).includes(value)
}

export interface SessionMember {
  identity: string
  kind: SessionMemberKind
  card_id?: string
  card_title?: string
  status: SessionMemberStatus
  last_active?: string
  /** B444：只驻场者行可能出键；push 驻场者四键齐出，desktop 驻场者全缺。 */
  driver_mode?: ResidentDriverMode
  carrier?: string
  carrier_machine?: string
  model?: string
}

/** 会话里的一张卡；seat 缺键 = 空座。seat_mode（B435 C-307/327）与 seat 同缺同出。 */
export interface SessionCard { card_id: string; title?: string; status?: string; seat?: string; seat_mode?: SeatMode }

export interface SessionSummary {
  id: string
  kind: string
  title: string
  owner: string
  archived: boolean
  /** B435 ⑤ C-510/511：项目房间标记（取值项目名）；非项目房间不出键。 */
  project?: string
  /** B444 S8：会话所属项目（建时写入，不随卡变）；旧会话与项目房间不出键。 */
  home_project?: string
  unread: number
  needs_human: boolean
  last_activity: string
  preview?: RoomPreview
  members?: SessionMember[]
  cards?: SessionCard[]
}

export interface Session {
  id: string; title: string; owner: string; archived: boolean
  /** B444：会话所属项目；旧形态建会话不出键。 */
  home_project?: string
  members?: string[]; cards?: string[]
  created_at: string; updated_at: string
}

/** B444：建会话时被拒的一张卡；reason 由服务端点名冲突对象（如所在会话 id）。 */
export interface SessionRejectedCard { card: string; reason: string }

/** B444：拉起驻场者的结果——成功带 identity 与载体落点，失败只带 error（会话与卡照旧保留）。 */
export interface SessionResidentOutcome { identity?: string; carrier?: string; carrier_machine?: string; error?: string }

/** B444：POST /api/sessions 响应 = Session 键集 + 可选 rejected_cards / resident（均 omitempty）。 */
export interface SessionCreateResp extends Session {
  rejected_cards?: SessionRejectedCard[]
  resident?: SessionResidentOutcome
}

/** B444：项目内建会话请求。squad 必填（Web 形态，预填唯一协调小队）；空标题/空卡不出键。 */
export interface SessionCreateInput { project: string; squad: string; cards?: string[]; title?: string }

export interface SessionNode { card_id: string; title?: string; node: string; round?: number; state: string; target?: string }

// 与 proto.SessionEvent* 词表逐值一致（B444 增 renamed）。
export const SESSION_TIMELINE_KINDS = [
  'created', 'archived', 'card_joined', 'card_left',
  'seat_bound', 'seat_rebound', 'needs_human', 'card_closed', 'renamed',
] as const
export type SessionTimelineKind = (typeof SESSION_TIMELINE_KINDS)[number]
export function isSessionTimelineKind(value: unknown): value is SessionTimelineKind {
  return typeof value === 'string' && (SESSION_TIMELINE_KINDS as readonly string[]).includes(value)
}

export interface SessionTimelineEvent {
  seq: number; kind: SessionTimelineKind
  card_id?: string; detail?: string; actor?: string
  created_at: string
}

export type SessionRoleKind = 'coordinator' | 'resident' | 'executor'
export type SessionRoleState = 'running' | 'idle' | 'needs_you' | 'waiting' | 'exception' | 'unknown'
export type SessionRoleTone = 'neutral' | 'needs_you' | 'failed'
export type SessionRoleRecentSource = 'wake_round' | 'room_message' | 'task_mirrored' | 'dispatched' | 'needs_human' | 'needs_cleared' | 'driver_seat_bound' | 'driver_takeover' | 'status_moved'

export interface SessionRoleRecent {
  seq: number
  time: string
  summary: string
  tone: SessionRoleTone
  source: SessionRoleRecentSource
}

export interface SessionRoleTicketRef {
  card_id: string
  target: string
  task_id: string
  ticket_id: string
  task_type: 'permission_request' | 'question'
}

export interface SessionRoleStatus {
  id: string
  kind: SessionRoleKind
  identity?: string
  card_id?: string
  task_id?: string
  target?: string
  state: SessionRoleState
  since?: string
  conclusion: string
  reason?: string
  waiting_for?: string
  recent?: SessionRoleRecent[]
  more_activity?: boolean
  ticket_refs?: SessionRoleTicketRef[]
  can_request_wake?: boolean
}

export interface SessionRoleStatusProjection {
  observed_at: string
  roles: SessionRoleStatus[]
}

export interface SessionDetail { summary: SessionSummary; nodes?: SessionNode[]; timeline?: SessionTimelineEvent[]; role_status?: SessionRoleStatusProjection }

// ---- C8 接线（契约 §3.5 端点 + §3.6 收件箱；响应信封形状与 C6 handler 逐字一致）----
import { deleteJSON, postJSON, request } from './client'

// RoomsPage 是 GET /api/rooms 的分页信封（B374，契约 §3.5）：rooms 恒出、
// has_more 恒出、next_cursor 仅非空时出键。与 internal/proto/rooms.go 逐字段对应。
export interface RoomsPage {
  rooms: RoomSummary[]
  next_cursor?: string
  has_more: boolean
}

// fetchRooms 会话列表（GET /api/rooms?project=&cursor=&limit=）。
//
// 首屏必须显式传 limit（拍板 P4：不带 limit 也不带 cursor 即旧客户端，服务端 426）。
// next_cursor 原样回传取下一页；has_more=false 终止续载。
export const fetchRooms = (
  opts: { project?: string; cursor?: string; limit?: number } = {},
): Promise<RoomsPage> => {
  const q = new URLSearchParams()
  if (opts.project) q.set('project', opts.project)
  if (opts.cursor) q.set('cursor', opts.cursor)
  if (opts.limit !== undefined) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return request<RoomsPage>(`/api/rooms${qs ? `?${qs}` : ''}`)
}

// fetchRoomMessages 房间历史（GET /api/rooms/{id}/messages）。before 排他游标、
// limit<=0 由服务端取 200；返回升序 room_message 事件。
export const fetchRoomMessages = (
  id: string,
  opts: { before?: number; limit?: number; signal?: AbortSignal } = {},
): Promise<RoomHistoryItem[]> => {
  const q = new URLSearchParams()
  if (opts.before !== undefined) q.set('before', String(opts.before))
  if (opts.limit !== undefined) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return request<{ messages: RoomHistoryItem[] }>(
    `/api/rooms/${encodeURIComponent(id)}/messages${qs ? `?${qs}` : ''}`,
    { signal: opts.signal },
  ).then((response) => response.messages ?? [])
}

// sendRoomMessage 用户发言（POST /api/rooms/{id}/messages）。kind 服务端固定 user、
// actor 服务端注入；refs/mentions 为空时不出键（与 Go 侧 omitempty 一致）。
export const sendRoomMessage = (
  id: string,
  body: string,
  opts: { refs?: string[]; mentions?: string[] } = {},
): Promise<{ seq: number }> => {
  const payload: { body: string; refs?: string[]; mentions?: string[] } = { body }
  if (opts.refs !== undefined && opts.refs.length > 0) payload.refs = opts.refs
  if (opts.mentions !== undefined && opts.mentions.length > 0) payload.mentions = opts.mentions
  return postJSON<{ seq: number }>(`/api/rooms/${encodeURIComponent(id)}/messages`, payload)
}

// markRoomRead 置已读游标（POST /api/rooms/{id}/read）：打开房间即已读（spec §7）。
export const markRoomRead = (id: string, uptoSeq: number): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/rooms/${encodeURIComponent(id)}/read`, { upto_seq: uptoSeq })

// fetchInbox 待回复收件箱（GET /api/inbox）：三源聚合在 gateway 编排（契约 §3.6）。
export const fetchInbox = (): Promise<InboxItem[]> =>
  request<{ items: InboxItem[] }>('/api/inbox').then((response) => response.items ?? [])

// ---- B358.6 会话六端点（B358.4 已收口的 /api/sessions 面；发言/已读/历史复用上方
// sendRoomMessage/markRoomRead/fetchRoomMessages——会话号作 {id}，签名零改动）----

// IdentityResp 是 GET /api/identity 的响应（B358.9 契约 §3.1）。三键恒出（无
// omitempty）：member 空可能是 console_user 未配置，也可能是后端太老没发键。
export interface IdentityResp {
  member: string
  device: string
  configured: boolean
}

// fetchIdentity 控制台身份读缝（GET /api/identity）：本文请求的人名+端名+是否已配名。
// 落款显示、isSelfActor 判据、缺名可行动提示的数据源。
export const fetchIdentity = (): Promise<IdentityResp> => request<IdentityResp>('/api/identity')

// fetchSessions 会话列表（GET /api/sessions）：member 维度服务端注入（解析人名），
// 前端不得自报身份——不设 member 参数（与 S4 缺陷族 5 反例镜像）。
export const fetchSessions = (signal?: AbortSignal): Promise<SessionSummary[]> =>
  request<{ sessions: SessionSummary[] }>('/api/sessions', { signal }).then((r) => r.sessions ?? [])

// fetchSessionDetail 会话详情三块（GET /api/sessions/{id}）。
export const fetchSessionDetail = (id: string, signal?: AbortSignal): Promise<SessionDetail> =>
  request<SessionDetail>(`/api/sessions/${encodeURIComponent(id)}`, { signal })

// createSession 项目内建会话（B444：POST /api/sessions {project, squad, cards?, title?}）。
// owner 仍由服务端按解析人名缺省（B358.9 契约 §3.8），前端不传 owner。服务端同步建行、
// 拉卡并拉起驻场者首轮，可能耗时较长；首轮失败仍 200，失败原因在 resident.error。
// 空标题（trim 后）与空卡列表不出键——标题留空即交服务端自动命名。
export const createSession = (input: SessionCreateInput): Promise<SessionCreateResp> => {
  const payload: SessionCreateInput = { project: input.project, squad: input.squad }
  if (input.cards !== undefined && input.cards.length > 0) payload.cards = input.cards
  const title = input.title?.trim() ?? ''
  if (title !== '') payload.title = title
  return postJSON<SessionCreateResp>('/api/sessions', payload)
}

// renameSession 改会话名（B444 S7：POST /api/sessions/{id}/rename）。expectTitle 非空即 CAS 前值
// （不符 409）；人在 Web 上改名不带 expect。身份服务端注入（群主或驻场者才可改）。
export const renameSession = (id: string, title: string, expectTitle = ''): Promise<Session> => {
  const payload: { title: string; expect_title?: string } = { title }
  if (expectTitle !== '') payload.expect_title = expectTitle
  return postJSON<Session>(`/api/sessions/${encodeURIComponent(id)}/rename`, payload)
}

// archiveSession 归档会话（POST …/archive，幂等）。控制台本期无归档入口
// （CLI `session archive` 是操作面），函数保留为六端点镜像完整性。
export const archiveSession = (id: string): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/archive`, {})

// addSessionMember 以当前身份加入会话（POST /api/sessions/{id}/members，B366：
// 控制台 403「以当前身份加入会话」一键的后端）。成员身份服务端权威：identity
// 缺省不出键（空体）——服务端以注入 actor 入列，前端不自报身份（与 fetchSessions
// 同款纪律，B358.4 门禁）；identity 参数保留为端点形状镜像，服务端仍校验统一
// 记法前缀并忽略塞值（TestSessionMemberAddEndpoint ③④ 反例）。
export const addSessionMember = (id: string, identity = ''): Promise<{ ok: boolean }> => {
  const payload: { identity?: string } = {}
  if (identity !== '') payload.identity = identity
  return postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/members`, payload)
}

// joinSessionCard 拉卡进群（POST …/cards {card}；进群 ≠ 配人）。
export const joinSessionCard = (id: string, card: string): Promise<{ ok: boolean }> =>
  postJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/cards`, { card })

// leaveSessionCard 移卡出群（DELETE …/cards/{cardID}，幂等）。控制台本期无入口，镜像保留。
export const leaveSessionCard = (id: string, card: string): Promise<{ ok: boolean }> =>
  deleteJSON<{ ok: boolean }>(`/api/sessions/${encodeURIComponent(id)}/cards/${encodeURIComponent(card)}`, {})
